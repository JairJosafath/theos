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

package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// ChatReconciler makes each Chat's six rooms on its chat host, and their
// Space on a host that has Spaces (model.ChatSystem), invites the Chat's
// people, and reports the rooms' IDs and Ready
// (docs/plans/done/chat-matrix.md). It finds what it made before by the
// IDs in status, then by alias, so a restart makes nothing twice. It never
// posts or reads the rooms; theos-chat does.
type ChatReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader reads the Chat's Secret live: the operator caches no Secrets.
	APIReader client.Reader
	// ChatHosts builds the ChatHost of a host and bot, by the name of the
	// host's model.ChatSystem, as cmd/main.go gives the adapters.
	ChatHosts map[string]ports.ChatHosts
	// Addresses are each host's address for a Chat that names none, by the
	// name of the host's model.ChatSystem: Matrix's is the operator's
	// --chat-homeserver, "" when the chart runs none, and Slack's its
	// --chat-slack-api-url (model.ChatSystem.AddressFlag).
	Addresses map[string]string
	// PollInterval is how often a Chat that isn't Ready is tried again; 0
	// means a minute.
	PollInterval time.Duration
	// CheckInterval is how often a Ready Chat's rooms are checked; 0 means
	// 10 minutes.
	CheckInterval time.Duration
}

// +kubebuilder:rbac:groups=theos.factory.io,resources=chats,verbs=get;list;watch
// +kubebuilder:rbac:groups=theos.factory.io,resources=chats/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=theos.factory.io,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

// Reconcile reads the Chat's Secret, makes or finds its Space and rooms,
// invites its people and writes what it found into status. A Chat on a
// host the operator runs no adapter for is HostNotSupported, and nothing
// more.
func (r *ChatReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var chat c4v1beta1.Chat
	if err := r.Get(ctx, req.NamespacedName, &chat); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	status := chat.Status.DeepCopy()
	check := model.ChatCheck{Chat: chat.Name, Secret: chat.Spec.CredentialsSecretRef.Name}
	poll := ctrl.Result{RequeueAfter: r.pollInterval()}
	notReady := func(reason string) (ctrl.Result, error) {
		r.setReady(status, chat.Generation, reason, check)
		return poll, r.writeStatus(ctx, &chat, status)
	}

	// A namespace has one Chat: the oldest serves it.
	if other, err := r.olderChat(ctx, &chat); err != nil {
		return ctrl.Result{}, err
	} else if other != "" {
		check.Other = other
		return notReady(model.ChatDuplicate)
	}

	// A Chat on a host the operator runs no adapter for (a host newer than
	// the operator, or one it was built without) is left alone before its
	// Secret is read or a host called. spec.host is immutable, so it is not
	// polled: only a new spec, or an operator restarted with that host's
	// adapter, tries it again.
	check.Host = model.ChatHostName(string(chat.Spec.Host))
	system, err := model.ChatSystemByName(check.Host)
	hosts, ok := r.ChatHosts[system.Name]
	if err != nil || !ok {
		if cond := apimeta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionChatReady); cond == nil ||
			cond.Reason != model.ChatHostNotSupported {
			logf.FromContext(ctx).Info("Left the Chat alone: the operator runs no adapter for its chat host",
				"host", check.Host)
		}
		r.setReady(status, chat.Generation, model.ChatHostNotSupported, check)
		return ctrl.Result{}, r.writeStatus(ctx, &chat, status)
	}

	// A Slack Chat names no address (CEL): the operator's flag gives it.
	check.Homeserver = chat.Spec.Homeserver
	if check.Homeserver == "" {
		check.Homeserver = r.Addresses[system.Name]
	}
	if check.Homeserver == "" {
		return notReady(model.ChatHomeserverUnset)
	}
	var secret corev1.Secret
	err = r.APIReader.Get(ctx, client.ObjectKey{Namespace: chat.Namespace, Name: check.Secret}, &secret)
	switch {
	case apierrors.IsNotFound(err):
		return notReady(model.ChatSecretMissing)
	case err != nil:
		return ctrl.Result{}, err
	}
	// Every key the host's bot takes is there, so Ready names a missing one
	// before theos-chat, which reads them all, fails on it.
	for _, k := range system.Keys() {
		if len(secret.Data[k]) == 0 {
			check.Key = k
			return notReady(model.ChatSecretIncomplete)
		}
	}

	// The operator reads no rooms and opens no connection to the host, so
	// it takes the bot's token alone (docs/plans/requests-and-chat.md,
	// decision 9).
	host, err := hosts(ports.ChatConfig{Address: check.Homeserver, Token: string(secret.Data[system.TokenKey])})
	if err == nil {
		defer host.Close()
		err = r.ensureRooms(ctx, &chat, host, system, status, &check)
	}
	if err != nil {
		check.Detail = err.Error()
		reason := model.ChatUnreachable
		if taken, ok := errors.AsType[*ports.ChatAliasTakenError](err); ok {
			reason, check.Alias = model.ChatAliasTaken, taken.Alias
		} else if missing, ok := errors.AsType[*ports.ChatScopeMissingError](err); ok {
			reason, check.Scope = model.ChatScopeMissing, missing.Scope
		} else if errors.Is(err, ports.ErrChatCredentialRefused) {
			reason = model.ChatCredentialRefused
		}
		if cond := apimeta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionChatReady); cond == nil ||
			cond.Reason != reason {
			logf.FromContext(ctx).Info("Could not make the chat rooms", "reason", reason, "error", err.Error())
		}
		return notReady(reason)
	}
	if cond := apimeta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionChatReady); cond == nil ||
		cond.Status != metav1.ConditionTrue {
		logf.FromContext(ctx).Info("Made the chat rooms", "space", check.Space, "people", check.People)
	}
	r.setReady(status, chat.Generation, model.ChatReady, check)
	if err := r.writeStatus(ctx, &chat, status); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: r.checkInterval()}, nil
}

// olderChat is the name of another Chat in chat's namespace made before it
// (by name when made in the same second), or "".
func (r *ChatReconciler) olderChat(ctx context.Context, chat *c4v1beta1.Chat) (string, error) {
	var chats c4v1beta1.ChatList
	if err := r.List(ctx, &chats, client.InNamespace(chat.Namespace)); err != nil {
		return "", err
	}
	for _, other := range chats.Items {
		if other.Name == chat.Name || other.DeletionTimestamp != nil {
			continue
		}
		if t := other.CreationTimestamp.Compare(chat.CreationTimestamp.Time); t < 0 || (t == 0 && other.Name < chat.Name) {
			return other.Name, nil
		}
	}
	return "", nil
}

// chatRoomMade is the Space or a room ensureRooms made or found: its ID,
// whether it is new, and how it is recorded in status.
type chatRoomMade struct {
	id     string
	new    bool
	record func()
}

// ensureRooms makes or finds the Space, on a host that has Spaces, and the
// six rooms, invites the people, and records each step in status as it
// goes, so a failure halfway keeps what was made.
//
// status.invited are the people invited to every room status lists. A
// person not in it is invited to every room; the rest only to a room that
// is new, never again to one they were invited to: on Slack an invite is a
// join, so it would pull back into a room whoever chose to leave it
// (#159). A room is new when EnsureRoom made it (Created), or when its ID
// is not the one status holds for it: it was made again, on this pass or
// on one whose invites failed partway. So status takes a new room's ID
// only once everyone is invited to it, and the people an earlier pass did
// not reach are invited on the next. While status.invited is empty, as
// before the first pass that invites everyone, every person is invited to
// every room anyway, so a new room's ID is recorded at once.
func (r *ChatReconciler) ensureRooms(ctx context.Context, chat *c4v1beta1.Chat, host ports.ChatHost,
	system model.ChatSystem, status *c4v1beta1.ChatStatus, check *model.ChatCheck) error {
	bot, err := host.WhoAmI(ctx)
	if err != nil {
		return err
	}
	status.Homeserver, status.Bot = check.Homeserver, bot
	prefix := model.ChatAliasPrefix(chat.Namespace, chat.Spec.AliasPrefix)
	var made []chatRoomMade
	// keep records room in status now when it is not new or nobody is in
	// status.invited, else once its people are invited.
	keep := func(room ports.ChatRoom, recorded string, record func()) {
		m := chatRoomMade{id: room.ID, new: room.Created || room.ID != recorded, record: record}
		if !m.new || len(status.Invited) == 0 {
			record()
		}
		made = append(made, m)
	}
	parent := ""
	if system.Spaces {
		recorded := ""
		if status.Space != nil {
			recorded = status.Space.ID
		}
		space, err := r.ensureSpace(ctx, chat, host, prefix, recorded, check)
		if err != nil {
			return err
		}
		keep(space, recorded, func() { status.Space = &c4v1beta1.ChatRoomRef{ID: space.ID, Alias: space.Alias} })
		parent = space.ID
	}
	for _, purpose := range model.ChatPurposes {
		spec := ports.ChatRoomSpec{Alias: model.ChatRoomAlias(prefix, purpose), Name: chatRoomName(chat.Spec.Rooms, purpose),
			Topic: model.ChatTopic(purpose), Parent: parent}
		if i := chatRoomIndex(status.Rooms, purpose); i >= 0 {
			spec.ID = status.Rooms[i].ID
		}
		room, err := host.EnsureRoom(ctx, spec)
		if err != nil {
			return err
		}
		entry := c4v1beta1.ChatRoomStatus{Purpose: c4v1beta1.ChatPurpose(purpose), ID: room.ID, Alias: room.Alias}
		keep(room, spec.ID, func() {
			if i := chatRoomIndex(status.Rooms, purpose); i >= 0 {
				status.Rooms[i] = entry
			} else {
				status.Rooms = append(status.Rooms, entry)
			}
		})
	}
	check.Rooms = len(model.ChatPurposes)
	people := chatPeopleIDs(chat, system)
	var newcomers []string
	for _, id := range people {
		if !slices.Contains(status.Invited, id) {
			newcomers = append(newcomers, id)
		}
	}
	for _, room := range made {
		to := newcomers
		if room.new {
			to = people
		}
		for _, id := range to {
			if err := host.Invite(ctx, room.id, id); err != nil {
				return fmt.Errorf("inviting %s: %w", id, err)
			}
		}
		if room.new {
			room.record()
		}
	}
	slices.Sort(people)
	status.Invited = people
	check.People = len(people)
	return nil
}

// chatRoomIndex is the index of purpose's room in rooms, or -1.
func chatRoomIndex(rooms []c4v1beta1.ChatRoomStatus, purpose model.ChatPurpose) int {
	return slices.IndexFunc(rooms, func(s c4v1beta1.ChatRoomStatus) bool {
		return s.Purpose == c4v1beta1.ChatPurpose(purpose)
	})
}

// ensureSpace makes or finds the Chat's Space, named for the Chat's space,
// else its Project, by recorded, the ID status holds for it.
func (r *ChatReconciler) ensureSpace(ctx context.Context, chat *c4v1beta1.Chat, host ports.ChatHost, prefix string,
	recorded string, check *model.ChatCheck) (ports.ChatRoom, error) {
	var project c4v1beta1.Project
	if err := r.Get(ctx, client.ObjectKey{Namespace: chat.Namespace, Name: chat.Namespace}, &project); err != nil &&
		!apierrors.IsNotFound(err) {
		return ports.ChatRoom{}, err
	}
	spec := ports.ChatRoomSpec{Alias: prefix, Topic: model.ChatSpaceTopic, Space: true,
		Name: model.ChatSpaceName(chat.Spec.Space, project.Spec.DisplayName, chat.Namespace)}
	check.Space = spec.Name
	spec.ID = recorded
	space, err := host.EnsureRoom(ctx, spec)
	if err != nil {
		return ports.ChatRoom{}, err
	}
	if space.Alias != "" {
		check.Space = space.Alias
	}
	return space, nil
}

// chatPeopleIDs are the IDs on the host of the people chat lists in the
// field its host's row names: spec.slack.people's member IDs on Slack,
// spec.people's Matrix IDs on Matrix.
func chatPeopleIDs(chat *c4v1beta1.Chat, system model.ChatSystem) []string {
	var ids []string
	if system.Name == model.ChatSlack {
		if chat.Spec.Slack != nil {
			for _, p := range chat.Spec.Slack.People {
				ids = append(ids, p.SlackID)
			}
		}
		return ids
	}
	for _, p := range chat.Spec.People {
		ids = append(ids, p.MatrixID)
	}
	return ids
}

// chatRoomName is the room's display name: the Chat's, else its purpose.
func chatRoomName(rooms c4v1beta1.ChatRooms, purpose model.ChatPurpose) string {
	name := map[model.ChatPurpose]string{model.ChatUpdates: rooms.Updates, model.ChatGeneral: rooms.General,
		model.ChatTriage: rooms.Triage, model.ChatSpecs: rooms.Specs, model.ChatReviews: rooms.Reviews,
		model.ChatAlerts: rooms.Alerts}[purpose]
	if name == "" {
		return string(purpose)
	}
	return name
}

// setReady sets the Ready condition to reason.
func (r *ChatReconciler) setReady(status *c4v1beta1.ChatStatus, generation int64, reason string, check model.ChatCheck) {
	value := metav1.ConditionFalse
	if reason == model.ChatReady {
		value = metav1.ConditionTrue
	}
	setCondition(&status.Conditions, generation, c4v1beta1.ConditionChatReady, value, reason,
		model.ChatReadyMessage(reason, check))
}

// writeStatus patches chat's status to status when it changed.
func (r *ChatReconciler) writeStatus(ctx context.Context, chat *c4v1beta1.Chat, status *c4v1beta1.ChatStatus) error {
	if equality.Semantic.DeepEqual(&chat.Status, status) {
		return nil
	}
	orig := chat.DeepCopy()
	chat.Status = *status
	if err := r.Status().Patch(ctx, chat, client.MergeFrom(orig)); err != nil {
		return fmt.Errorf("writing the status of Chat %s: %w", chat.Name, err)
	}
	return nil
}

func (r *ChatReconciler) pollInterval() time.Duration {
	if r.PollInterval > 0 {
		return r.PollInterval
	}
	return time.Minute
}

func (r *ChatReconciler) checkInterval() time.Duration {
	if r.CheckInterval > 0 {
		return r.CheckInterval
	}
	return 10 * time.Minute
}

// SetupWithManager sets up the controller with the Manager. A status write
// starts no reconcile; a spec change does, and so does each poll.
func (r *ChatReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1beta1.Chat{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("chat").
		Complete(r)
}
