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

package chatbot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// commands reads what was said in chat's rooms since the last pass and runs
// each command. The first pass only marks where the rooms stand: what was
// said before theos-chat read them is not run. Each message is read once:
// the sync token moves on even when an answer couldn't be posted, so a
// command never runs twice.
func (b *Bot) commands(ctx context.Context, chat *c4v1beta1.Chat, host chatHost, st *state) error {
	if st.since == "" {
		sync, err := host.Sync(ctx, "", 0)
		if err != nil {
			return fmt.Errorf("reading the rooms: %w", err)
		}
		st.since, st.dirty = sync.Next, true
		return nil
	}
	sync, err := host.Sync(ctx, st.since, 0)
	if err != nil {
		return fmt.Errorf("reading the rooms: %w", err)
	}
	purposes := map[string]model.ChatPurpose{}
	for purpose, id := range rooms(chat) {
		purposes[id] = purpose
	}
	var errs []error
	for _, ev := range sync.Events {
		purpose, ours := purposes[ev.Room]
		if !ours || ev.Replaces != "" {
			continue
		}
		if err := b.command(ctx, chat, host, st, purpose, ev); err != nil {
			errs = append(errs, err)
		}
	}
	if sync.Next != "" && sync.Next != st.since {
		st.since, st.dirty = sync.Next, true
	}
	return errors.Join(errs...)
}

// people are chat's people on its host as model.ChatPersonFor reads them:
// spec.slack.people by member ID on Slack, spec.people by Matrix ID on
// Matrix (docs/plans/requests-and-chat.md, decision 10).
func people(chat *c4v1beta1.Chat, system model.ChatSystem) []model.ChatPerson {
	var out []model.ChatPerson
	if system.Name == model.ChatSlack {
		if chat.Spec.Slack != nil {
			for _, p := range chat.Spec.Slack.People {
				out = append(out, model.ChatPerson{ID: p.SlackID, User: p.User, Groups: p.Groups})
			}
		}
		return out
	}
	for _, p := range chat.Spec.People {
		out = append(out, model.ChatPerson{ID: p.MatrixID, User: p.User, Groups: p.Groups})
	}
	return out
}

// command runs one message said in room purpose: words outside a thread
// theos-chat started that aren't a command are people talking, and left
// alone; a sender who isn't one of the Chat's people gets a one-line
// refusal; a person's command becomes its one request (model.ChatCommandRequest),
// sent as them, and the API server's answer goes back in the thread.
func (b *Bot) command(ctx context.Context, chat *c4v1beta1.Chat, host chatHost, st *state,
	purpose model.ChatPurpose, ev ports.ChatEvent) error {
	item, ours := st.thread(purpose, ev.ThreadRoot)
	cmd, perr := model.ParseChatCommand(purpose, ev.ThreadRoot != "", ev.Text)
	if errors.Is(perr, model.ErrChatUnknownCommand) && !ours {
		return nil
	}
	root := ev.ThreadRoot
	if root == "" {
		root = ev.ID
	}
	reply := func(p model.ChatPost) error {
		if _, err := host.Reply(ctx, ev.Room, root, host.message(p)); err != nil {
			return fmt.Errorf("answering %s in %s: %w", ev.ID, purpose, err)
		}
		return nil
	}
	say := func(line string) error { return reply(model.ChatPost{Status: line}) }
	log := logf.FromContext(ctx).WithValues("room", purpose, "sender", ev.Sender)
	person, ok := model.ChatPersonFor(people(chat, host.system), ev.Sender)
	if !ok {
		log.Info("Refused a command from someone not among the Chat's people")
		return say(model.ChatStrangerRefusal(ev.Sender, chat.Name, host.system.People))
	}
	if perr != nil {
		return say("Nothing done: " + perr.Error())
	}
	switch cmd.Kind {
	case model.ChatCommandHelp:
		return reply(model.ChatHelp(purpose))
	case model.ChatCommandStatus:
		counts, err := b.status(ctx, chat.Namespace)
		if err != nil {
			return errors.Join(err, say("Could not read the factory's status; try again"))
		}
		return reply(model.ChatStatus(counts))
	}
	c, err := b.context(ctx, chat.Namespace, cmd, item, person)
	if err != nil {
		return say("Nothing done: " + err.Error())
	}
	req, writes, err := model.ChatCommandRequest(cmd, c)
	if err != nil {
		return say("Nothing done: " + err.Error())
	}
	if !writes {
		return nil
	}
	code, made, refusal, err := b.send(ctx, chat.Namespace, person, req)
	if err != nil {
		log.Info("Could not send a command", "error", err.Error())
		return errors.Join(err, say("Nothing done: the cluster could not be reached; say it again"))
	}
	if code >= http.StatusMultipleChoices {
		log.Info("The cluster refused a command", "user", person.User, "code", code)
		return say(model.ChatRefused(person.User, code, refusal))
	}
	log.Info("Ran a command", "command", cmd.Kind, "user", person.User)
	return say(model.ChatDone(cmd, item, person.User, made))
}

// context is what cmd's write is made from: the thread's item, the person,
// and the Brief or Container it changes as read now.
func (b *Bot) context(ctx context.Context, namespace string, cmd model.ChatCommand, item model.ChatItem,
	person model.ChatPerson) (model.ChatContext, error) {
	c := model.ChatContext{Item: item, By: person.User, Now: b.now()}
	switch cmd.Kind {
	case model.ChatCommandPrompt, model.ChatCommandResume:
		var brief c4v1beta1.Brief
		if err := b.APIReader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: cmd.Brief}, &brief); err != nil {
			if apierrors.IsNotFound(err) {
				return c, fmt.Errorf("there is no Brief %s", cmd.Brief)
			}
			return c, err
		}
		c.Brief = model.ChatBriefView{ResourceVersion: brief.ResourceVersion, Prompts: brief.Spec.Prompts,
			PromptsRetired: int(brief.Spec.PromptsRetired), PromptsPlanned: int(brief.Status.PromptsPlanned),
			PauseRuns: brief.Spec.PauseRuns}
	case model.ChatCommandApproveDeploy:
		var container c4v1beta1.Container
		if err := b.APIReader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: item.Name}, &container); err != nil {
			if apierrors.IsNotFound(err) {
				return c, fmt.Errorf("there is no Container %s", item.Name)
			}
			return c, err
		}
		for _, d := range container.Spec.Deploy {
			c.Deploy = append(c.Deploy, model.ChatDeployView{Target: d.Target, After: d.After, Version: d.Version,
				Approved: d.Approved})
		}
	}
	return c, nil
}

// send makes req in namespace as person: theos-chat's own credentials,
// impersonating the person's Kubernetes user and groups, so the cluster's
// RBAC and admission decide. It returns the HTTP status, the name of an
// object made, and the API server's refusal.
func (b *Bot) send(ctx context.Context, namespace string, person model.ChatPerson, req model.ChatRequest) (
	code int, made, refusal string, err error) {
	if err := model.CheckChatRequest(req); err != nil {
		return 0, "", "", err
	}
	cfg := rest.CopyConfig(b.Config)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: person.User, Groups: person.Groups}
	hc, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return 0, "", "", err
	}
	url := strings.TrimSuffix(cfg.Host, "/") + req.Path(namespace)
	hreq, err := http.NewRequestWithContext(ctx, req.Method, url, bytes.NewReader(req.Body))
	if err != nil {
		return 0, "", "", err
	}
	hreq.Header.Set("Content-Type", req.ContentType)
	hreq.Header.Set("Accept", "application/json")
	resp, err := hc.Do(hreq)
	if err != nil {
		return 0, "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, "", "", err
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		var status metav1.Status
		if json.Unmarshal(body, &status) == nil && status.Message != "" {
			return resp.StatusCode, "", status.Message, nil
		}
		return resp.StatusCode, "", strings.TrimSpace(string(body)), nil
	}
	var obj metav1.PartialObjectMetadata
	_ = json.Unmarshal(body, &obj)
	return resp.StatusCode, obj.Name, "", nil
}

// status counts namespace's work for the status command.
func (b *Bot) status(ctx context.Context, namespace string) (model.ChatStatusCounts, error) {
	var (
		c          model.ChatStatusCounts
		tasks      c4v1beta1.TaskList
		containers c4v1beta1.ContainerList
		briefs     c4v1beta1.BriefList
	)
	for _, list := range []client.ObjectList{&tasks, &containers, &briefs} {
		if err := b.Client.List(ctx, list, client.InNamespace(namespace)); err != nil {
			return c, err
		}
	}
	for _, t := range tasks.Items {
		switch t.Status.Phase {
		case c4v1beta1.TaskMerged:
		case c4v1beta1.TaskFailed:
			c.TasksFailed++
		case c4v1beta1.TaskAwaitingApproval:
			c.Tasks++
			c.TasksWaiting++
		default:
			c.Tasks++
		}
	}
	c.Containers = len(containers.Items)
	for _, ct := range containers.Items {
		if ct.Status.Phase == c4v1beta1.ContainerReady {
			c.ContainersReady++
		}
	}
	c.Briefs = len(briefs.Items)
	return c, nil
}
