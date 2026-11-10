/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package chatbot is theos-chat's logic (docs/plans/done/chat-matrix.md): for each
// Ready Chat it posts what the factory does in the Chat's rooms, as
// internal/model routes it, and runs the commands its people say there, each
// one request sent as the person, by impersonation, so the cluster's RBAC
// decides. It keeps what it posted, and where it read the rooms up to, in a
// ConfigMap in the Chat's namespace, so a restart says nothing twice.
//
// Its RBAC is config/chat/rbac.yaml, written by hand: it has no
// +kubebuilder:rbac markers, which would land in the operator's role.
package chatbot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// Bot is theos-chat: a reconciler of Chats that posts and reads their rooms.
type Bot struct {
	// Client reads theos's objects, from the manager's cache in cmd/theos-chat,
	// and writes theos-chat's ConfigMaps.
	Client client.Client
	// APIReader reads live: a Chat's Secret, theos-chat's ConfigMap, and the
	// objects a command's write is made from.
	APIReader client.Reader
	// Config is theos-chat's own; a command's write is sent with a copy that
	// impersonates the person.
	Config *rest.Config
	// ChatHosts builds the ChatHost of a homeserver and bot, as
	// cmd/theos-chat picks the adapter.
	ChatHosts ports.ChatHosts
	// Interval is how often a Chat's rooms are read and its news posted; 0
	// means five seconds.
	Interval time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// UIURL is the UI's address (model.ChatUIURL), so a post links to its
	// object's page; "" links to the Git host alone.
	UIURL string

	mu    sync.Mutex
	hosts map[ports.ChatConfig]ports.ChatHost
}

// The conditions of Tasks and Containers theos-chat reads, as the operator
// names them.
const (
	conditionProgressing = "Progressing"
	conditionSucceeded   = "Succeeded"
	conditionReady       = "Ready"
)

// Reconcile posts the news of a Ready Chat's namespace and runs the commands
// said in its rooms since the last pass. An error, the homeserver's or a
// room's, is retried with the queue's backoff; what was done before it is
// kept.
func (b *Bot) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var chat c4v1beta1.Chat
	if err := b.Client.Get(ctx, req.NamespacedName, &chat); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !chatReady(&chat) {
		return ctrl.Result{RequeueAfter: b.interval()}, nil
	}
	host, err := b.host(ctx, &chat)
	if err != nil {
		return ctrl.Result{}, err
	}
	st, err := b.loadState(ctx, &chat)
	if err != nil {
		return ctrl.Result{}, err
	}
	err = b.post(ctx, &chat, host, st)
	// Commands are read even when a post failed: a missing room keeps the
	// others going.
	err = errors.Join(err, b.commands(ctx, &chat, host, st))
	if serr := b.saveState(ctx, &chat, st); serr != nil {
		err = errors.Join(err, serr)
	}
	if err != nil {
		logf.FromContext(ctx).Info("Could not do all of a chat pass; trying again", "error", err.Error())
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: b.interval()}, nil
}

// chatReady reports whether the operator made chat's rooms: Ready, with the
// homeserver and each room's ID in status.
func chatReady(chat *c4v1beta1.Chat) bool {
	return apimeta.IsStatusConditionTrue(chat.Status.Conditions, c4v1beta1.ConditionChatReady) &&
		chat.Status.Homeserver != "" && len(chat.Status.Rooms) == len(model.ChatPurposes)
}

// rooms maps each of chat's purposes to its room's ID.
func rooms(chat *c4v1beta1.Chat) map[model.ChatPurpose]string {
	out := map[model.ChatPurpose]string{}
	for _, r := range chat.Status.Rooms {
		out[model.ChatPurpose(r.Purpose)] = r.ID
	}
	return out
}

// host is the ChatHost of chat's homeserver as its bot, built once per
// homeserver and token.
func (b *Bot) host(ctx context.Context, chat *c4v1beta1.Chat) (ports.ChatHost, error) {
	var secret corev1.Secret
	name := chat.Spec.CredentialsSecretRef.Name
	if err := b.APIReader.Get(ctx, client.ObjectKey{Namespace: chat.Namespace, Name: name}, &secret); err != nil {
		return nil, fmt.Errorf("reading Secret %s: %w", name, err)
	}
	token := string(secret.Data[model.ChatTokenKey])
	if token == "" {
		return nil, fmt.Errorf("secret %s has no %s", name, model.ChatTokenKey)
	}
	cfg := ports.ChatConfig{Homeserver: chat.Status.Homeserver, Token: token}
	b.mu.Lock()
	defer b.mu.Unlock()
	if h, ok := b.hosts[cfg]; ok {
		return h, nil
	}
	h, err := b.ChatHosts(cfg)
	if err != nil {
		return nil, err
	}
	if b.hosts == nil {
		b.hosts = map[ports.ChatConfig]ports.ChatHost{}
	}
	b.hosts[cfg] = h
	return h, nil
}

func (b *Bot) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Bot) interval() time.Duration {
	if b.Interval > 0 {
		return b.Interval
	}
	return 5 * time.Second
}

// message is p as the host's message.
func message(p model.ChatPost) ports.ChatMessage {
	return ports.ChatMessage{Text: p.Text(), HTML: p.HTML()}
}

// post posts the namespace's news (model.ChatNextPost): threads started,
// edited and marked done, plain posts, and the updates digest when it is
// due. A failed post is skipped for this pass and the rest go on.
func (b *Bot) post(ctx context.Context, chat *c4v1beta1.Chat, host ports.ChatHost, st *state) error {
	news, err := b.news(ctx, chat.Namespace)
	if err != nil {
		return err
	}
	ids := rooms(chat)
	baseline := !st.seeded
	var (
		pending []model.ChatNews
		errs    []error
	)
	keep := map[string]bool{}
	for _, n := range news {
		keep[n.Key] = true
		route, err := model.ChatRouteFor(n.Event)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		prev := st.posted[n.Key]
		step, _ := model.ChatNextPost(prev, n, baseline)
		switch {
		case step.Seen:
			st.set(n.Key, &model.ChatPosted{Room: route.Room})
		case step.Digest:
			pending = append(pending, n)
		case step.New:
			id, err := host.Send(ctx, ids[route.Room], message(n.Post))
			if err != nil {
				errs = append(errs, fmt.Errorf("posting %s in %s: %w", n.Key, route.Room, err))
				continue
			}
			st.set(n.Key, &model.ChatPosted{Room: route.Room, Event: id, Hash: model.ChatHash(n.Post), Item: n.Item,
				Done: n.Done})
		case prev != nil && route.Thread && !prev.Done && prev.Event != "":
			changed, err := b.follow(ctx, host, ids[prev.Room], prev, n, step)
			if err != nil {
				errs = append(errs, fmt.Errorf("following %s in %s: %w", n.Key, prev.Room, err))
			}
			st.dirty = st.dirty || changed
		}
	}
	for key := range st.posted {
		if !keep[key] {
			st.remove(key)
		}
	}
	if model.ChatDigestDue(len(pending), st.digestAt, b.now(), time.Duration(chat.Spec.DigestMinutes)*time.Minute) {
		id, err := host.Send(ctx, ids[model.ChatUpdates], message(model.ChatDigest(pending)))
		if err != nil {
			errs = append(errs, fmt.Errorf("posting the updates digest: %w", err))
		} else {
			for _, n := range pending {
				st.set(n.Key, &model.ChatPosted{Room: model.ChatUpdates, Event: id})
			}
			st.digestAt, st.dirty = b.now(), true
		}
	}
	if !st.seeded {
		st.seeded, st.dirty = true, true
	}
	return errors.Join(errs...)
}

// follow edits an open thread's root to the news's text and marks it done
// with ✅, recording each step in prev as it is done, and says whether it
// changed prev; the thread's item follows the news's, so a command answers
// what the root shows.
func (b *Bot) follow(ctx context.Context, host ports.ChatHost, room string, prev *model.ChatPosted, n model.ChatNews,
	step model.ChatPostStep) (bool, error) {
	changed := false
	if step.Edit {
		if _, err := host.Edit(ctx, room, prev.Event, message(n.Post)); err != nil {
			return changed, err
		}
		prev.Hash, changed = model.ChatHash(n.Post), true
	}
	if prev.Item != n.Item {
		prev.Item, changed = n.Item, true
	}
	if step.React {
		if _, err := host.React(ctx, room, prev.Event, model.ChatDoneReaction); err != nil {
			return changed, err
		}
		prev.Done, changed = true, true
	}
	return changed, nil
}

// news is everything theos-chat says of namespace's Tasks, Containers and
// Briefs, in a stable order.
func (b *Bot) news(ctx context.Context, namespace string) ([]model.ChatNews, error) {
	var (
		tasks      c4v1beta1.TaskList
		containers c4v1beta1.ContainerList
		briefs     c4v1beta1.BriefList
	)
	for _, list := range []client.ObjectList{&tasks, &containers, &briefs} {
		if err := b.Client.List(ctx, list, client.InNamespace(namespace)); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(tasks.Items, func(a, b c4v1beta1.Task) int {
		if c := a.CreationTimestamp.Compare(b.CreationTimestamp.Time); c != 0 {
			return c
		}
		return compareStrings(a.Name, b.Name)
	})
	var news []model.ChatNews
	for i := range tasks.Items {
		v := taskView(&tasks.Items[i])
		v.PageURL = model.ChatPageURL(b.UIURL, namespace, "Task", v.Name)
		news = append(news, model.ChatTaskNews(v)...)
	}
	for i := range containers.Items {
		v := containerView(&containers.Items[i])
		v.PageURL = model.ChatPageURL(b.UIURL, namespace, "Container", v.Name)
		news = append(news, model.ChatContainerNews(v)...)
	}
	for _, br := range briefs.Items {
		news = append(news, model.ChatBriefNews(model.ChatBriefRound{Name: br.Name, Round: int(br.Status.Round),
			Phase: string(br.Status.Phase)})...)
	}
	return news, nil
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// conditionMessage is the message of conds' condition typ, "" for none.
func conditionMessage(conds []metav1.Condition, typ string) string {
	if c := apimeta.FindStatusCondition(conds, typ); c != nil {
		return c.Message
	}
	return ""
}

// taskView is t as model.ChatTaskNews reads it.
func taskView(t *c4v1beta1.Task) model.ChatTaskView {
	s := t.Status
	v := model.ChatTaskView{Name: t.Name, Kind: string(t.Spec.Kind), Phase: string(s.Phase), Round: int(s.Round),
		Issue: int(t.Spec.Issue), IssueTitle: s.IssueTitle, IssueURL: s.IssueURL, PullRequest: int(s.PullRequest),
		PullURL: s.PullRequestURL, Commit: s.Commit, Message: conditionMessage(s.Conditions, conditionProgressing),
		Benchmark: t.Spec.Benchmark != nil}
	if s.Phase == c4v1beta1.TaskFailed {
		v.Message = conditionMessage(s.Conditions, conditionSucceeded)
	}
	if s.Spec != nil {
		v.SpecApproval, v.ApprovedBy, v.ApprovedInChat = string(s.Spec.Approval), s.Spec.ApprovedBy, s.Spec.ApprovedInChat
	}
	if d := s.Triage; d != nil {
		v.Triage = &model.ChatTriageView{Decision: d.Decision, Reason: d.Reason, Question: d.Question,
			Duplicate: int(d.Duplicate), Applied: d.Applied, AnsweredBy: d.AnsweredBy}
	}
	if s.Review != nil {
		v.ReviewVerdict, v.ReviewSummary = s.Review.Verdict, s.Review.Summary
	}
	return v
}

// containerView is c as model.ChatContainerNews reads it.
func containerView(c *c4v1beta1.Container) model.ChatContainerView {
	v := model.ChatContainerView{Name: c.Name, Phase: string(c.Status.Phase), RepositoryURL: c.Status.RepositoryURL,
		NewestRelease: c.Status.NewestRelease, Message: conditionMessage(c.Status.Conditions, conditionReady)}
	for _, d := range c.Status.Deploys {
		v.Deploys = append(v.Deploys, model.ChatDeployState{Target: d.Target, Version: d.Version,
			WantedVersion: d.WantedVersion, Phase: d.Phase, Reason: d.Reason, Message: d.Message, URL: d.URL})
	}
	return v
}

// SetupWithManager runs the Bot on each Chat, again whenever a Task,
// Container or Brief in its namespace changes, and every Interval.
func (b *Bot) SetupWithManager(mgr ctrl.Manager) error {
	toChats := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var chats c4v1beta1.ChatList
		if err := mgr.GetClient().List(ctx, &chats, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(chats.Items))
		for _, c := range chats.Items {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&c)})
		}
		return reqs
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1beta1.Chat{}).
		Watches(&c4v1beta1.Task{}, toChats).
		Watches(&c4v1beta1.Container{}, toChats).
		Watches(&c4v1beta1.Brief{}, toChats).
		Named("theos-chat").
		Complete(b)
}
