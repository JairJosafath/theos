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
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
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

// slackWorkspaceChat is a Slack Chat with ada its one person, and its Secret, on a
// fake workspace, reconciled by a ChatReconciler on a fake client with the
// real Slack adapter, whose calls go through transport.
type slackWorkspaceChat struct {
	ws        *slacktest.Server
	ada       string
	cl        client.Client
	r         *ChatReconciler
	req       ctrl.Request
	setTopics *failSetTopic
}

// failSetTopic is a transport that fails the next conversations.setTopic
// once armed, as a connection that drops right after
// conversations.create does, and passes every other call on.
type failSetTopic struct{ armed atomic.Bool }

func (f *failSetTopic) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/conversations.setTopic") && f.armed.CompareAndSwap(true, false) {
		return nil, errors.New("connection reset")
	}
	return http.DefaultTransport.RoundTrip(r)
}

func newSlackWorkspaceChat(t *testing.T) *slackWorkspaceChat {
	t.Helper()
	ws := slacktest.New(t)
	_, botToken, appToken := ws.NewApp("theos")
	ada := ws.NewUser("ada")
	scheme := briefScheme(t)
	chat := &c4v1beta1.Chat{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "chat", Generation: 1},
		Spec: c4v1beta1.ChatSpec{Host: c4v1beta1.ChatHostSlack, CredentialsSecretRef: c4v1beta1.LocalRef{Name: "slack-bot"},
			Slack: &c4v1beta1.ChatSlack{People: []c4v1beta1.ChatSlackPerson{{SlackID: ada, User: "ada"}}}}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "slack-bot"},
		Data: map[string][]byte{model.ChatSlackBotTokenKey: []byte(botToken),
			model.ChatSlackAppTokenKey: []byte(appToken)}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(chat, secret).
		WithStatusSubresource(&c4v1beta1.Chat{}).Build()
	setTopics := &failSetTopic{}
	r := &ChatReconciler{Client: cl, Scheme: scheme, APIReader: cl,
		Addresses: map[string]string{model.ChatSlack: ws.URL()},
		ChatHosts: map[string]ports.ChatHosts{model.ChatSlack: slack.Hosts(&http.Client{Transport: setTopics})}}
	return &slackWorkspaceChat{ws: ws, ada: ada, cl: cl, r: r, setTopics: setTopics,
		req: ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "shop", Name: "chat"}}}
}

// reconcile reconciles the Chat once and returns it, Ready or not.
func (x *slackWorkspaceChat) reconcile(t *testing.T) *c4v1beta1.Chat {
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
func (x *slackWorkspaceChat) ready(t *testing.T) *c4v1beta1.Chat {
	t.Helper()
	chat := x.reconcile(t)
	if c := apimeta.FindStatusCondition(chat.Status.Conditions, c4v1beta1.ConditionChatReady); c == nil ||
		c.Status != metav1.ConditionTrue {
		t.Fatalf("Ready = %+v, want True", c)
	}
	return chat
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

	x.setTopics.armed.Store(true)
	chat = x.reconcile(t)
	if c := apimeta.FindStatusCondition(chat.Status.Conditions, c4v1beta1.ConditionChatReady); c == nil ||
		c.Status == metav1.ConditionTrue {
		t.Fatalf("Ready = %+v after setTopic failed, want not True", c)
	}
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
