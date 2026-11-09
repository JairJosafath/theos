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

// ChatReconciler makes each Chat's Space and its six rooms on the Matrix
// homeserver, invites the Chat's people, and reports the rooms' IDs and
// Ready (docs/plans/done/chat-matrix.md). It finds what it made before by the
// IDs in status, then by alias, so a restart makes nothing twice. It never
// posts or reads the rooms; theos-chat does.
type ChatReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader reads the Chat's Secret live: the operator caches no Secrets.
	APIReader client.Reader
	// ChatHosts builds the ChatHost of a homeserver and bot, as cmd/main.go
	// picks the adapter.
	ChatHosts ports.ChatHosts
	// Homeserver is the homeserver of a Chat that names none, the operator's
	// --chat-homeserver; "" when the chart runs none.
	Homeserver string
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
// invites its people and writes what it found into status.
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

	check.Homeserver = chat.Spec.Homeserver
	if check.Homeserver == "" {
		check.Homeserver = r.Homeserver
	}
	if check.Homeserver == "" {
		return notReady(model.ChatHomeserverUnset)
	}
	var secret corev1.Secret
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: chat.Namespace, Name: check.Secret}, &secret)
	switch {
	case apierrors.IsNotFound(err):
		return notReady(model.ChatSecretMissing)
	case err != nil:
		return ctrl.Result{}, err
	case len(secret.Data[model.ChatTokenKey]) == 0:
		return notReady(model.ChatSecretIncomplete)
	}

	host, err := r.ChatHosts(ports.ChatConfig{Homeserver: check.Homeserver, Token: string(secret.Data[model.ChatTokenKey])})
	if err == nil {
		err = r.ensureRooms(ctx, &chat, host, status, &check)
	}
	if err != nil {
		check.Detail = err.Error()
		reason := model.ChatUnreachable
		if taken, ok := errors.AsType[*ports.ChatAliasTakenError](err); ok {
			reason, check.Alias = model.ChatAliasTaken, taken.Alias
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

// ensureRooms makes or finds the Space and the six rooms, invites the
// people, and records each step in status as it goes, so a failure halfway
// keeps what was made.
func (r *ChatReconciler) ensureRooms(ctx context.Context, chat *c4v1beta1.Chat, host ports.ChatHost,
	status *c4v1beta1.ChatStatus, check *model.ChatCheck) error {
	bot, err := host.WhoAmI(ctx)
	if err != nil {
		return err
	}
	status.Homeserver, status.Bot = check.Homeserver, bot
	var project c4v1beta1.Project
	if err := r.Get(ctx, client.ObjectKey{Namespace: chat.Namespace, Name: chat.Namespace}, &project); err != nil &&
		!apierrors.IsNotFound(err) {
		return err
	}
	prefix := model.ChatAliasPrefix(chat.Namespace, chat.Spec.AliasPrefix)
	spaceSpec := ports.ChatRoomSpec{Alias: prefix, Topic: model.ChatSpaceTopic, Space: true,
		Name: model.ChatSpaceName(chat.Spec.Space, project.Spec.DisplayName, chat.Namespace)}
	check.Space = spaceSpec.Name
	if status.Space != nil {
		spaceSpec.ID = status.Space.ID
	}
	space, err := host.EnsureRoom(ctx, spaceSpec)
	if err != nil {
		return err
	}
	status.Space = &c4v1beta1.ChatRoomRef{ID: space.ID, Alias: space.Alias}
	if space.Alias != "" {
		check.Space = space.Alias
	}
	// status.invited are the people invited to every room it lists: a room
	// made anew empties it, so everyone is invited again (an invite of a
	// member is no error), even when a later step fails.
	invited := map[string]bool{}
	for _, id := range status.Invited {
		invited[id] = true
	}
	forget := func() { status.Invited, invited = nil, map[string]bool{} }
	if space.Created {
		forget()
	}
	rooms := []string{space.ID}
	for _, purpose := range model.ChatPurposes {
		spec := ports.ChatRoomSpec{Alias: model.ChatRoomAlias(prefix, purpose), Name: chatRoomName(chat.Spec.Rooms, purpose),
			Topic: model.ChatTopic(purpose), Parent: space.ID}
		i := slices.IndexFunc(status.Rooms, func(s c4v1beta1.ChatRoomStatus) bool {
			return s.Purpose == c4v1beta1.ChatPurpose(purpose)
		})
		if i >= 0 {
			spec.ID = status.Rooms[i].ID
		}
		room, err := host.EnsureRoom(ctx, spec)
		if err != nil {
			return err
		}
		entry := c4v1beta1.ChatRoomStatus{Purpose: c4v1beta1.ChatPurpose(purpose), ID: room.ID, Alias: room.Alias}
		if i >= 0 {
			status.Rooms[i] = entry
		} else {
			status.Rooms = append(status.Rooms, entry)
		}
		if room.Created {
			forget()
		}
		rooms = append(rooms, room.ID)
	}
	check.Rooms = len(model.ChatPurposes)
	var people []string
	for _, p := range chat.Spec.People {
		people = append(people, p.MatrixID)
		if invited[p.MatrixID] {
			continue
		}
		for _, room := range rooms {
			if err := host.Invite(ctx, room, p.MatrixID); err != nil {
				return fmt.Errorf("inviting %s: %w", p.MatrixID, err)
			}
		}
	}
	slices.Sort(people)
	status.Invited = people
	check.People = len(people)
	return nil
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
