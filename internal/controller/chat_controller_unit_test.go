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
	"crypto/rand"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/adapters/matrix"
	"github.com/JairJosafath/theos/internal/adapters/matrix/matrixtest"
	"github.com/JairJosafath/theos/internal/adapters/slack"
	"github.com/JairJosafath/theos/internal/adapters/slack/slacktest"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// secretReads is a client.Reader that counts the Secrets read through it.
type secretReads struct {
	client.Reader
	secrets int
}

func (r *secretReads) Get(ctx context.Context, k client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*corev1.Secret); ok {
		r.secrets++
	}
	return r.Reader.Get(ctx, k, obj, opts...)
}

// TestChatOnAHostWithoutAnAdapter covers #158, kept for #159: the suite's
// operator runs Slack now, so an operator built without Slack's adapter is
// this reconciler, its map without slack. A Slack Chat with a whole Secret
// and an address is HostNotSupported: no Secret is read, no host built, and
// it is not polled, since spec.host never changes.
func TestChatOnAHostWithoutAnAdapter(t *testing.T) {
	scheme := briefScheme(t)
	chat := &c4v1beta1.Chat{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "chat", Generation: 1},
		Spec: c4v1beta1.ChatSpec{Host: c4v1beta1.ChatHostSlack, CredentialsSecretRef: c4v1beta1.LocalRef{Name: "slack-bot"}}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "slack-bot"},
		Data: map[string][]byte{model.ChatSlackBotTokenKey: []byte(rand.Text()),
			model.ChatSlackAppTokenKey: []byte(rand.Text())}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(chat, secret).
		WithStatusSubresource(&c4v1beta1.Chat{}).Build()
	reads := &secretReads{Reader: cl}
	built := 0
	r := &ChatReconciler{Client: cl, Scheme: scheme, APIReader: reads,
		Addresses: map[string]string{model.ChatMatrix: "https://matrix.test", model.ChatSlack: "https://slack.test/api"},
		ChatHosts: map[string]ports.ChatHosts{model.ChatMatrix: func(ports.ChatConfig) (ports.ChatHost, error) {
			built++
			return nil, errors.New("no Matrix host in this test")
		}}}

	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "shop",
		Name: "chat"}})
	if err != nil {
		t.Fatal(err)
	}
	if res != (ctrl.Result{}) {
		t.Errorf("Reconcile() = %+v, want no requeue: spec.host never changes", res)
	}
	if reads.secrets != 0 || built != 0 {
		t.Errorf("read %d Secrets and built %d hosts, want none", reads.secrets, built)
	}
	var got c4v1beta1.Chat
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(chat), &got); err != nil {
		t.Fatal(err)
	}
	cond := apimeta.FindStatusCondition(got.Status.Conditions, c4v1beta1.ConditionChatReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != model.ChatHostNotSupported ||
		!strings.HasPrefix(cond.Message, "Chat chat is on slack, a chat host this operator runs no adapter for") {
		t.Errorf("Ready = %+v, want False, HostNotSupported, naming slack", cond)
	}
	if got.Status.Homeserver != "" || got.Status.Bot != "" || got.Status.Space != nil || len(got.Status.Rooms) != 0 {
		t.Errorf("status = %+v, want nothing made or reached", got.Status)
	}
}

// slackWorkspaceChat is a Slack Chat with ada its first person, and its
// Secret, on a fake workspace, reconciled by a ChatReconciler on a fake
// client with the real Slack adapter, whose calls go through calls.
type slackWorkspaceChat struct {
	chatReconcile
	ws     *slacktest.Server
	ada    string
	others []string // the people after ada, as newSlackWorkspaceChat names them
}

// chatReconcile is a Chat on a fake client and the ChatReconciler that
// reconciles it, whose host's calls go through calls.
type chatReconcile struct {
	cl    client.Client
	r     *ChatReconciler
	req   ctrl.Request
	calls *failCalls
}

// failCalls is a transport that fails one call whose path ends in a
// suffix armed with fail, as a connection that drops, and passes every
// other call on.
type failCalls struct {
	mu    sync.Mutex
	armed map[string]int // path suffix → calls to pass before the one that fails
}

// fail fails the call whose path ends in suffix after letting pass more
// such calls through, once.
func (f *failCalls) fail(suffix string, pass int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.armed == nil {
		f.armed = map[string]int{}
	}
	f.armed[suffix] = pass
}

func (f *failCalls) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	for suffix, pass := range f.armed {
		if !strings.HasSuffix(r.URL.Path, suffix) {
			continue
		}
		if pass == 0 {
			delete(f.armed, suffix)
			f.mu.Unlock()
			return nil, errors.New("connection reset")
		}
		f.armed[suffix] = pass - 1
	}
	f.mu.Unlock()
	return http.DefaultTransport.RoundTrip(r)
}

// newSlackWorkspaceChat is a Slack Chat of ada and a person of each of
// others' names, after her.
func newSlackWorkspaceChat(t *testing.T, others ...string) *slackWorkspaceChat {
	t.Helper()
	ws := slacktest.New(t)
	_, botToken, appToken := ws.NewApp("theos")
	x := &slackWorkspaceChat{ws: ws, ada: ws.NewUser("ada")}
	people := make([]c4v1beta1.ChatSlackPerson, 0, 1+len(others))
	people = append(people, c4v1beta1.ChatSlackPerson{SlackID: x.ada, User: "ada"})
	for _, name := range others {
		id := ws.NewUser(name)
		x.others = append(x.others, id)
		people = append(people, c4v1beta1.ChatSlackPerson{SlackID: id, User: name})
	}
	chat := &c4v1beta1.Chat{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "chat", Generation: 1},
		Spec: c4v1beta1.ChatSpec{Host: c4v1beta1.ChatHostSlack, CredentialsSecretRef: c4v1beta1.LocalRef{Name: "slack-bot"},
			Slack: &c4v1beta1.ChatSlack{People: people}}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "slack-bot"},
		Data: map[string][]byte{model.ChatSlackBotTokenKey: []byte(botToken),
			model.ChatSlackAppTokenKey: []byte(appToken)}}
	x.chatReconcile = newChatReconcile(t, chat, secret, model.ChatSlack, ws.URL(), slack.Hosts)
	return x
}

// newChatReconcile is chat and secret on a fake client, reconciled with
// host's adapter, built by hosts, at address.
func newChatReconcile(t *testing.T, chat *c4v1beta1.Chat, secret *corev1.Secret, host, address string,
	hosts func(*http.Client) ports.ChatHosts) chatReconcile {
	t.Helper()
	scheme := briefScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(chat, secret).
		WithStatusSubresource(&c4v1beta1.Chat{}).Build()
	calls := &failCalls{}
	r := &ChatReconciler{Client: cl, Scheme: scheme, APIReader: cl, Addresses: map[string]string{host: address},
		ChatHosts: map[string]ports.ChatHosts{host: hosts(&http.Client{Transport: calls})}}
	return chatReconcile{cl: cl, r: r, calls: calls,
		req: ctrl.Request{NamespacedName: types.NamespacedName{Namespace: chat.Namespace, Name: chat.Name}}}
}

// reconcile reconciles the Chat once and returns it, Ready or not.
func (x *chatReconcile) reconcile(t *testing.T) *c4v1beta1.Chat {
	t.Helper()
	if _, err := x.r.Reconcile(t.Context(), x.req); err != nil {
		t.Fatal(err)
	}
	var got c4v1beta1.Chat
	if err := x.cl.Get(t.Context(), x.req.NamespacedName, &got); err != nil {
		t.Fatal(err)
	}
	return &got
}

// ready reconciles the Chat once and fails t unless it is Ready.
func (x *chatReconcile) ready(t *testing.T) *c4v1beta1.Chat {
	t.Helper()
	chat := x.reconcile(t)
	if c := apimeta.FindStatusCondition(chat.Status.Conditions, c4v1beta1.ConditionChatReady); c == nil ||
		c.Status != metav1.ConditionTrue {
		t.Fatalf("Ready = %+v, want True", c)
	}
	return chat
}

// notReady reconciles the Chat once and fails t if it is Ready.
func (x *chatReconcile) notReady(t *testing.T, why string) *c4v1beta1.Chat {
	t.Helper()
	chat := x.reconcile(t)
	if c := apimeta.FindStatusCondition(chat.Status.Conditions, c4v1beta1.ConditionChatReady); c == nil ||
		c.Status == metav1.ConditionTrue {
		t.Fatalf("Ready = %+v after %s, want not True", c, why)
	}
	return chat
}

// roomID is the ID status holds for purpose's room, or "".
func roomID(chat *c4v1beta1.Chat, purpose model.ChatPurpose) string {
	for _, room := range chat.Status.Rooms {
		if room.Purpose == c4v1beta1.ChatPurpose(purpose) {
			return room.ID
		}
	}
	return ""
}

// channel is the Chat's channel of purpose, as Slack shows it.
func (x *slackWorkspaceChat) channel(t *testing.T, chat *c4v1beta1.Chat, purpose model.ChatPurpose) slacktest.ChannelState {
	t.Helper()
	for _, room := range chat.Status.Rooms {
		if room.Purpose == c4v1beta1.ChatPurpose(purpose) {
			ch, ok := x.ws.Channel(room.ID)
			if !ok {
				t.Fatalf("no channel %s for %s", room.ID, purpose)
			}
			return ch
		}
	}
	t.Fatalf("status names no %s channel", purpose)
	return slacktest.ChannelState{}
}

// TestSlackChatPullsNobodyBack covers #159: a person who left a channel
// stays out of it. ada leaves general, a member empties triage's topic by
// hand, and the next reconcile puts the topic back and invites nobody.
// Before, the channel found without its topic was Created, the controller
// forgot status.invited, and six invites put ada back into general.
func TestSlackChatPullsNobodyBack(t *testing.T) {
	x := newSlackWorkspaceChat(t)
	chat := x.ready(t)
	general := x.channel(t, chat, model.ChatGeneral)
	if !slices.Contains(general.Members, x.ada) {
		t.Fatalf("ada is not in general after the first reconcile: %v", general.Members)
	}
	if err := x.ws.Remove(general.ID, x.ada); err != nil {
		t.Fatal(err)
	}
	triage := x.channel(t, chat, model.ChatTriage)
	if err := x.ws.SetTopic(triage.ID, ""); err != nil {
		t.Fatal(err)
	}
	invites := x.ws.Calls("conversations.invite")

	chat = x.ready(t)
	if got := x.channel(t, chat, model.ChatTriage).Topic; got != model.ChatTopic(model.ChatTriage) {
		t.Errorf("triage's topic = %q, want %q back", got, model.ChatTopic(model.ChatTriage))
	}
	if got := x.ws.Calls("conversations.invite") - invites; got != 0 {
		t.Errorf("conversations.invite called %d times, want none: everyone was invited once", got)
	}
	if slices.Contains(x.channel(t, chat, model.ChatGeneral).Members, x.ada) {
		t.Error("ada, who left general, is back in it")
	}
}

// TestSlackChatInvitesToAChannelLeftHalfMade covers #234 (for #159): an
// admin deletes alerts, and conversations.setTopic fails right after the
// next reconcile makes it again. The reconcile after finds it without its
// topic and the bot its one member, puts the topic back and invites ada to
// alerts, and to alerts alone: she left general, and stays out of it.
func TestSlackChatInvitesToAChannelLeftHalfMade(t *testing.T) {
	x := newSlackWorkspaceChat(t)
	chat := x.ready(t)
	general, alerts := x.channel(t, chat, model.ChatGeneral), x.channel(t, chat, model.ChatAlerts)
	if err := x.ws.Remove(general.ID, x.ada); err != nil {
		t.Fatal(err)
	}
	if err := x.ws.Delete(alerts.ID); err != nil {
		t.Fatal(err)
	}

	x.calls.fail("/conversations.setTopic", 0)
	x.notReady(t, "setTopic failed")
	invites := x.ws.Calls("conversations.invite")

	chat = x.ready(t)
	made := x.channel(t, chat, model.ChatAlerts)
	if made.ID == alerts.ID || made.Topic != model.ChatTopic(model.ChatAlerts) {
		t.Errorf("alerts = %s with topic %q, want a new channel with %q", made.ID, made.Topic,
			model.ChatTopic(model.ChatAlerts))
	}
	if !slices.Contains(made.Members, x.ada) {
		t.Errorf("ada is not in the alerts channel made again: %v", made.Members)
	}
	if got := x.ws.Calls("conversations.invite") - invites; got != 1 {
		t.Errorf("conversations.invite called %d times, want once: ada, to alerts alone", got)
	}
	if slices.Contains(x.channel(t, chat, model.ChatGeneral).Members, x.ada) {
		t.Error("ada, who left general, is back in it")
	}
}

// TestSlackChatInvitesToAChannelRemadeAfterAFailedInvite covers #159: an
// admin deletes alerts, and the reconcile that makes it again invites ada
// to it but fails to invite bob (a dropped connection; a rate limit is
// ordinary use). The next reconcile invites bob to alerts and pulls nobody
// back: ada, who left general, stays out of it. Before, status held the new
// channel's ID after the failed pass, the next reconcile found the channel
// by it, not Created, and bob was never invited to it.
func TestSlackChatInvitesToAChannelRemadeAfterAFailedInvite(t *testing.T) {
	x := newSlackWorkspaceChat(t, "bob")
	bob := x.others[0]
	chat := x.ready(t)
	general, alerts := x.channel(t, chat, model.ChatGeneral), x.channel(t, chat, model.ChatAlerts)
	if err := x.ws.Remove(general.ID, x.ada); err != nil {
		t.Fatal(err)
	}
	if err := x.ws.Delete(alerts.ID); err != nil {
		t.Fatal(err)
	}

	x.calls.fail("/conversations.invite", 1)
	chat = x.notReady(t, "bob's invite failed")
	if got := roomID(chat, model.ChatAlerts); got != alerts.ID {
		t.Errorf("status names alerts %s after its invites failed, want %s until everyone is invited", got, alerts.ID)
	}
	invites := x.ws.Calls("conversations.invite")

	chat = x.ready(t)
	made := x.channel(t, chat, model.ChatAlerts)
	if made.ID == alerts.ID {
		t.Fatalf("alerts = %s, want the channel made again", made.ID)
	}
	if !slices.Contains(made.Members, bob) || !slices.Contains(made.Members, x.ada) {
		t.Errorf("alerts' members = %v, want ada and bob", made.Members)
	}
	if got := x.ws.Calls("conversations.invite") - invites; got != 2 {
		t.Errorf("conversations.invite called %d times, want twice: ada (already in) and bob, to alerts alone", got)
	}
	if slices.Contains(x.channel(t, chat, model.ChatGeneral).Members, x.ada) {
		t.Error("ada, who left general, is back in it")
	}
}

// matrixHomeserverChat is a Matrix Chat of ada and bob on a fake
// homeserver, reconciled with the real Matrix adapter.
type matrixHomeserverChat struct {
	chatReconcile
	hs       *matrixtest.Server
	ada, bob string
}

func newMatrixHomeserverChat(t *testing.T) *matrixHomeserverChat {
	t.Helper()
	hs := matrixtest.New(t)
	_, token := hs.NewUser("theos")
	x := &matrixHomeserverChat{hs: hs}
	x.ada, _ = hs.NewUser("ada")
	x.bob, _ = hs.NewUser("bob")
	people := []c4v1beta1.ChatPerson{{MatrixID: x.ada, User: "ada"}, {MatrixID: x.bob, User: "bob"}}
	chat := &c4v1beta1.Chat{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "chat", Generation: 1},
		Spec: c4v1beta1.ChatSpec{Host: c4v1beta1.ChatHostMatrix, Homeserver: hs.URL(), People: people,
			CredentialsSecretRef: c4v1beta1.LocalRef{Name: "matrix-bot"}}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "matrix-bot"},
		Data: map[string][]byte{model.ChatTokenKey: []byte(token)}}
	x.chatReconcile = newChatReconcile(t, chat, secret, model.ChatMatrix, hs.URL(), matrix.Hosts)
	return x
}

// members are the people invited to or in room.
func (x *matrixHomeserverChat) members(t *testing.T, room string) map[string]string {
	t.Helper()
	r, ok := x.hs.Room(room)
	if !ok {
		t.Fatalf("no room %s", room)
	}
	return r.Members
}

// TestMatrixChatInvitesToARoomMadeAgain covers #159 on Matrix: an admin
// deletes general, and the next reconcile makes it again and invites ada
// and bob to it, and nobody back into a room they left: ada left triage.
// When bob's invite fails on the pass that makes it, the next pass invites
// him. Before, status held the new room's ID after the failed pass, the
// room found by it was not Created, and bob was never invited to it. The
// first case passed before too; it guards the room made in one pass.
func TestMatrixChatInvitesToARoomMadeAgain(t *testing.T) {
	for _, tt := range []struct {
		name       string
		failInvite bool
	}{
		{"made again", false},
		{"made again, an invite failing partway", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			x := newMatrixHomeserverChat(t)
			chat := x.ready(t)
			general, triage := roomID(chat, model.ChatGeneral), roomID(chat, model.ChatTriage)
			x.hs.Join(triage, x.ada)
			x.hs.Leave(triage, x.ada)
			x.hs.Delete(general)

			if tt.failInvite {
				x.calls.fail("/invite", 1)
				chat = x.notReady(t, "bob's invite failed")
				if got := roomID(chat, model.ChatGeneral); got != general {
					t.Errorf("status names general %s after its invites failed, want %s until everyone is invited",
						got, general)
				}
			}
			chat = x.ready(t)
			made := roomID(chat, model.ChatGeneral)
			if made == general {
				t.Fatalf("general = %s, want the room made again", made)
			}
			if m := x.members(t, made); m[x.ada] != "invite" || m[x.bob] != "invite" {
				t.Errorf("general's members = %v, want ada and bob invited", m)
			}
			if m := x.members(t, triage); m[x.ada] != "" {
				t.Errorf("ada, who left triage, is %s in it again", m[x.ada])
			}
		})
	}
}

// TestSlackChatRemadeChannelWithARefusedPerson covers #251 with #159's
// rule: the people are ada, a member ID the workspace has no member for
// (Slack answers user_not_found) and bob. The first pass invites ada and
// bob to every channel, stays Ready and names the refusal on Invited. Then
// ada leaves general and an admin deletes alerts: the next pass makes
// alerts again, invites ada and bob to it and records its new ID though
// the stranger is refused again, asks the stranger to each channel they
// were refused and to the new one, and pulls nobody back: ada stays out of
// general.
func TestSlackChatRemadeChannelWithARefusedPerson(t *testing.T) {
	x := newSlackWorkspaceChat(t, "bob")
	bob, ghost := x.others[0], "U0GHOST00"
	var chat c4v1beta1.Chat
	if err := x.cl.Get(t.Context(), x.req.NamespacedName, &chat); err != nil {
		t.Fatal(err)
	}
	chat.Spec.Slack.People = slices.Insert(chat.Spec.Slack.People, 1,
		c4v1beta1.ChatSlackPerson{SlackID: ghost, User: "ghost"})
	if err := x.cl.Update(t.Context(), &chat); err != nil {
		t.Fatal(err)
	}
	wantRefused := func(chat *c4v1beta1.Chat) {
		t.Helper()
		c := apimeta.FindStatusCondition(chat.Status.Conditions, c4v1beta1.ConditionChatInvited)
		if c == nil || c.Status != metav1.ConditionFalse || c.Reason != model.ChatInviteRefused ||
			!strings.Contains(c.Message, ghost+" (user_not_found)") {
			t.Errorf("Invited = %+v, want False, InviteRefused, naming %s and user_not_found", c, ghost)
		}
		want := []string{x.ada, bob}
		slices.Sort(want)
		if !slices.Equal(chat.Status.Invited, want) {
			t.Errorf("status.invited = %v, want ada and bob", chat.Status.Invited)
		}
	}

	first := x.ready(t)
	wantRefused(first)
	for _, purpose := range model.ChatPurposes {
		if m := x.channel(t, first, purpose).Members; !slices.Contains(m, x.ada) || !slices.Contains(m, bob) {
			t.Errorf("%s's members = %v, want ada and bob", purpose, m)
		}
	}
	general, alerts := x.channel(t, first, model.ChatGeneral), x.channel(t, first, model.ChatAlerts)
	if err := x.ws.Remove(general.ID, x.ada); err != nil {
		t.Fatal(err)
	}
	if err := x.ws.Delete(alerts.ID); err != nil {
		t.Fatal(err)
	}
	invites := x.ws.Calls("conversations.invite")

	again := x.ready(t)
	wantRefused(again)
	made := x.channel(t, again, model.ChatAlerts)
	if made.ID == alerts.ID {
		t.Fatalf("alerts = %s, want the channel made again, its new ID recorded", made.ID)
	}
	if !slices.Contains(made.Members, x.ada) || !slices.Contains(made.Members, bob) {
		t.Errorf("alerts' members = %v, want ada and bob", made.Members)
	}
	if got := x.ws.Calls("conversations.invite") - invites; got != 8 {
		t.Errorf("conversations.invite called %d times, want 8: the stranger to the 6 channels, ada and bob to "+
			"alerts", got)
	}
	if slices.Contains(x.channel(t, again, model.ChatGeneral).Members, x.ada) {
		t.Error("ada, who left general, is back in it")
	}
}

// refusals are the invites a refusingHost refuses: the host's code by room
// ID, "*" for every room, and then person.
type refusals struct {
	mu   sync.Mutex
	code map[string]map[string]string
}

// set refuses person's invite to room with code; "" lifts it.
func (r *refusals) set(room, person, code string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code == nil {
		r.code = map[string]map[string]string{}
	}
	if r.code[room] == nil {
		r.code[room] = map[string]string{}
	}
	r.code[room][person] = code
}

func (r *refusals) of(room, person string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if code := r.code[room][person]; code != "" {
		return code
	}
	return r.code["*"][person]
}

// refusingHost is a ChatHost that refuses the invites its refusals name,
// as Slack refuses a deactivated member everywhere or a single-channel
// guest in all but one channel (#251), and passes every other call on.
type refusingHost struct {
	ports.ChatHost
	refuse *refusals
}

func (h refusingHost) Invite(ctx context.Context, room, user string) error {
	if code := h.refuse.of(room, user); code != "" {
		return &ports.ChatInviteRefusedError{User: user, Code: code}
	}
	return h.ChatHost.Invite(ctx, room, user)
}

// refusing makes the Chat's Slack host refuse the invites the refusals it
// returns name.
func (x *slackWorkspaceChat) refusing() *refusals {
	rf := &refusals{}
	hosts := x.r.ChatHosts[model.ChatSlack]
	x.r.ChatHosts[model.ChatSlack] = func(cfg ports.ChatConfig) (ports.ChatHost, error) {
		h, err := hosts(cfg)
		if err != nil {
			return nil, err
		}
		return refusingHost{ChatHost: h, refuse: rf}, nil
	}
	return rf
}

// addPerson adds the person id to the Chat's spec.slack.people.
func (x *slackWorkspaceChat) addPerson(t *testing.T, id, user string) {
	t.Helper()
	var chat c4v1beta1.Chat
	if err := x.cl.Get(t.Context(), x.req.NamespacedName, &chat); err != nil {
		t.Fatal(err)
	}
	chat.Spec.Slack.People = append(chat.Spec.Slack.People, c4v1beta1.ChatSlackPerson{SlackID: id, User: user})
	if err := x.cl.Update(t.Context(), &chat); err != nil {
		t.Fatal(err)
	}
}

// TestSlackChatRefusedInOneChannel covers #251 with #159's rule: bob, a
// single-channel guest, is refused in alerts alone. He is invited to the
// other five channels and kept in status.refused with alerts; he leaves
// general, and each later pass asks him to alerts alone, so general never
// pulls him back. A pass with a refusal standing requeues after the check
// interval (10 minutes), not the minute's poll. Once Slack takes him in
// alerts he joins status.invited, still out of general. Before, he was
// asked to every channel each minute, and general pulled him back.
func TestSlackChatRefusedInOneChannel(t *testing.T) {
	x := newSlackWorkspaceChat(t)
	rf := x.refusing()
	first := x.ready(t)
	alerts, general := x.channel(t, first, model.ChatAlerts).ID, x.channel(t, first, model.ChatGeneral).ID
	bob := x.ws.NewUser("bob")
	rf.set(alerts, bob, "ura_max_channels")
	x.addPerson(t, bob, "bob")

	res, err := x.r.Reconcile(t.Context(), x.req)
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != 10*time.Minute {
		t.Errorf("RequeueAfter = %s with a refusal standing, want the check interval, 10m", res.RequeueAfter)
	}
	chat := x.ready(t)
	want := []c4v1beta1.ChatRefusedPerson{{ID: bob, Code: "ura_max_channels", Rooms: []string{alerts}}}
	if !reflect.DeepEqual(chat.Status.Refused, want) || !slices.Equal(chat.Status.Invited, []string{x.ada}) {
		t.Errorf("status.refused = %+v, invited = %v; want %+v and ada alone", chat.Status.Refused,
			chat.Status.Invited, want)
	}
	if c := apimeta.FindStatusCondition(chat.Status.Conditions, c4v1beta1.ConditionChatInvited); c == nil ||
		c.Status != metav1.ConditionFalse || !strings.Contains(c.Message, bob+" (ura_max_channels)") {
		t.Errorf("Invited = %+v, want False naming %s and ura_max_channels", c, bob)
	}
	for _, purpose := range model.ChatPurposes {
		if in := slices.Contains(x.channel(t, chat, purpose).Members, bob); in != (purpose != model.ChatAlerts) {
			t.Errorf("bob in %s = %v, want %v", purpose, in, purpose != model.ChatAlerts)
		}
	}

	if err := x.ws.Remove(general, bob); err != nil {
		t.Fatal(err)
	}
	for pass := range 3 {
		invites := x.ws.Calls("conversations.invite")
		chat = x.ready(t)
		if slices.Contains(x.channel(t, chat, model.ChatGeneral).Members, bob) {
			t.Fatalf("pass %d: bob, who left general, is back in it", pass)
		}
		// The refusingHost answers bob's invite to alerts without a call.
		if got := x.ws.Calls("conversations.invite") - invites; got != 0 {
			t.Errorf("pass %d: conversations.invite called %d times, want none: bob is asked to alerts alone",
				pass, got)
		}
	}

	rf.set(alerts, bob, "")
	chat = x.ready(t)
	if len(chat.Status.Refused) != 0 || !slices.Contains(chat.Status.Invited, bob) {
		t.Errorf("status.refused = %+v, invited = %v; want none refused and bob invited", chat.Status.Refused,
			chat.Status.Invited)
	}
	if !slices.Contains(x.channel(t, chat, model.ChatAlerts).Members, bob) {
		t.Error("bob is not in alerts once Slack takes him")
	}
	if slices.Contains(x.channel(t, chat, model.ChatGeneral).Members, bob) {
		t.Error("bob, who left general, is back in it")
	}
}

// TestSlackChatRemadeChannelRefusingAnInvitedPerson covers #251's remade
// channel: ada and bob are invited everywhere; bob leaves general and is
// deactivated, and an admin deletes alerts. The next pass makes alerts
// again, invites ada and records bob as refused in the new alerts alone.
// Once bob is active again he is asked to alerts alone, and general never
// pulls him back. Before, the refusal dropped bob from status.invited, and
// the pass after his account came back invited him to every channel.
func TestSlackChatRemadeChannelRefusingAnInvitedPerson(t *testing.T) {
	x := newSlackWorkspaceChat(t, "bob")
	bob := x.others[0]
	rf := x.refusing()
	first := x.ready(t)
	general, alerts := x.channel(t, first, model.ChatGeneral).ID, x.channel(t, first, model.ChatAlerts).ID
	if err := x.ws.Remove(general, bob); err != nil {
		t.Fatal(err)
	}
	if err := x.ws.Delete(alerts); err != nil {
		t.Fatal(err)
	}
	rf.set("*", bob, "user_not_found")

	chat := x.ready(t)
	made := x.channel(t, chat, model.ChatAlerts)
	want := []c4v1beta1.ChatRefusedPerson{{ID: bob, Code: "user_not_found", Rooms: []string{made.ID}}}
	if made.ID == alerts || !slices.Contains(made.Members, x.ada) {
		t.Errorf("alerts = %s with %v, want a new channel with ada", made.ID, made.Members)
	}
	if !reflect.DeepEqual(chat.Status.Refused, want) || !slices.Equal(chat.Status.Invited, []string{x.ada}) {
		t.Errorf("status.refused = %+v, invited = %v; want %+v and ada alone", chat.Status.Refused,
			chat.Status.Invited, want)
	}

	rf.set("*", bob, "")
	for pass := range 2 {
		chat = x.ready(t)
		if !slices.Contains(x.channel(t, chat, model.ChatAlerts).Members, bob) {
			t.Errorf("pass %d: bob is not in the alerts made again", pass)
		}
		if slices.Contains(x.channel(t, chat, model.ChatGeneral).Members, bob) {
			t.Fatalf("pass %d: bob, who left general, is back in it", pass)
		}
		if len(chat.Status.Refused) != 0 || !slices.Contains(chat.Status.Invited, bob) {
			t.Errorf("pass %d: status.refused = %+v, invited = %v; want none refused and bob invited", pass,
				chat.Status.Refused, chat.Status.Invited)
		}
	}
}

// TestChatInvitedNotCheckedAfterAFailedPass covers #251: a pass that ends
// Unreachable before the invites turns Invited to Unknown, NotChecked, at
// the Chat's generation, so the last pass's True is not read as current;
// the next pass that reaches the invites sets it True again.
func TestChatInvitedNotCheckedAfterAFailedPass(t *testing.T) {
	x := newSlackWorkspaceChat(t)
	chat := x.ready(t)
	if c := apimeta.FindStatusCondition(chat.Status.Conditions, c4v1beta1.ConditionChatInvited); c == nil ||
		c.Status != metav1.ConditionTrue {
		t.Fatalf("Invited = %+v, want True", c)
	}
	x.calls.fail("/auth.test", 0)
	chat = x.notReady(t, "auth.test failed")
	c := apimeta.FindStatusCondition(chat.Status.Conditions, c4v1beta1.ConditionChatInvited)
	if c == nil || c.Status != metav1.ConditionUnknown || c.Reason != model.ChatInviteNotChecked ||
		c.Message != model.ChatInviteNotCheckedMessage(model.ChatUnreachable) || c.ObservedGeneration != chat.Generation {
		t.Errorf("Invited = %+v, want Unknown, NotChecked, naming Unreachable, at generation %d", c, chat.Generation)
	}
	chat = x.ready(t)
	if c := apimeta.FindStatusCondition(chat.Status.Conditions, c4v1beta1.ConditionChatInvited); c == nil ||
		c.Status != metav1.ConditionTrue {
		t.Errorf("Invited = %+v after a pass that reached the invites, want True", c)
	}
}
