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
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/adapters/matrix"
	"github.com/JairJosafath/theos/internal/adapters/matrix/matrixtest"
	"github.com/JairJosafath/theos/internal/model"
)

// The posting tests run theos-chat on the fake homeserver and a fake API
// server: the commands, impersonation and the operator are the controller
// suite's (internal/controller/chatbot_test.go, task_spec_test.go).

const ns = "shop"

// world is a Ready Chat on a fake homeserver, its rooms made as the
// operator would, and a fake API server.
type world struct {
	t     *testing.T
	hs    *matrixtest.Server
	bot   string
	chat  *c4v1beta1.Chat
	kube  client.Client
	now   time.Time
	rooms map[model.ChatPurpose]string
}

func newWorld(t *testing.T, objs ...client.Object) *world {
	t.Helper()
	hs := matrixtest.New(t)
	bot, token := hs.NewUser("bot")
	w := &world{t: t, hs: hs, bot: bot, now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC),
		rooms: map[model.ChatPurpose]string{}}
	w.chat = &c4v1beta1.Chat{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "chat", UID: "chat-uid"},
		Spec: c4v1beta1.ChatSpec{CredentialsSecretRef: c4v1beta1.LocalRef{Name: "matrix-bot"}, DigestMinutes: 10},
		Status: c4v1beta1.ChatStatus{Homeserver: hs.URL(), Bot: bot, Conditions: []metav1.Condition{{
			Type: c4v1beta1.ConditionChatReady, Status: metav1.ConditionTrue, Reason: model.ChatReady,
			LastTransitionTime: metav1.Now()}}}}
	for _, p := range model.ChatPurposes {
		id := hs.CreateRoomAs(bot, "theos-shop-"+string(p))
		w.rooms[p] = id
		w.chat.Status.Rooms = append(w.chat.Status.Rooms, c4v1beta1.ChatRoomStatus{
			Purpose: c4v1beta1.ChatPurpose(p), ID: id})
	}
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, c4v1beta1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "matrix-bot"},
		Data: map[string][]byte{model.ChatTokenKey: []byte(token)}}
	w.kube = fake.NewClientBuilder().WithScheme(scheme).WithObjects(append(objs, w.chat, secret)...).Build()
	return w
}

// theosChat is a fresh theos-chat on w, as after a restart.
func (w *world) theosChat() *Bot {
	return &Bot{Client: w.kube, APIReader: w.kube, ChatHosts: matrix.Hosts(nil), Now: func() time.Time { return w.now }}
}

// pass runs one pass of bot; it fails the test on an error unless failing.
func (w *world) pass(bot *Bot) error {
	w.t.Helper()
	_, err := bot.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns,
		Name: "chat"}})
	return err
}

// posts are the bodies of the bot's messages in room purpose: posts, thread
// replies and edits alike.
func (w *world) posts(purpose model.ChatPurpose) []string {
	w.t.Helper()
	room, _ := w.hs.Room(w.rooms[purpose])
	var out []string
	for _, e := range room.Events {
		if e.Type == "m.room.message" && e.Sender == w.bot {
			body, _ := e.Content["body"].(string)
			out = append(out, body)
		}
	}
	return out
}

func (w *world) create(obj client.Object) {
	w.t.Helper()
	if err := w.kube.Create(context.Background(), obj); err != nil {
		w.t.Fatal(err)
	}
}

// merged is an Implement Task that merged pull request pr for issue.
func merged(name string, pr int32) *c4v1beta1.Task {
	return &c4v1beta1.Task{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:   c4v1beta1.TaskSpec{Kind: c4v1beta1.TaskImplement, Issue: pr + 10},
		Status: c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskMerged, PullRequest: pr, IssueTitle: "Change " + name}}
}

// The updates room batches its news into one message per digestMinutes,
// and a restart posts none of it again.
func TestDigestBatchesUpdates(t *testing.T) {
	w := newWorld(t, merged("old", 1))
	if err := w.pass(w.theosChat()); err != nil {
		t.Fatal(err)
	}
	if got := w.posts(model.ChatUpdates); len(got) != 0 {
		t.Fatalf("the first pass told the history: %q", got)
	}

	w.create(merged("a", 2))
	if err := w.pass(w.theosChat()); err != nil {
		t.Fatal(err)
	}
	if got := w.posts(model.ChatUpdates); len(got) != 1 || !strings.Contains(got[0], "Merged pull request #2") {
		t.Fatalf("the first news goes out at once: %q", got)
	}

	w.create(merged("b", 3))
	w.create(merged("c", 4))
	w.now = w.now.Add(9 * time.Minute)
	if err := w.pass(w.theosChat()); err != nil {
		t.Fatal(err)
	}
	if got := w.posts(model.ChatUpdates); len(got) != 1 {
		t.Fatalf("news within the window was posted: %q", got)
	}
	w.now = w.now.Add(time.Minute)
	if err := w.pass(w.theosChat()); err != nil {
		t.Fatal(err)
	}
	got := w.posts(model.ChatUpdates)
	if len(got) != 2 || !strings.HasPrefix(got[1], "Updates (2)") || !strings.Contains(got[1], "Merged pull request #3") ||
		!strings.Contains(got[1], "Merged pull request #4") {
		t.Fatalf("the window's news in one message: %q", got)
	}
	w.now = w.now.Add(time.Hour)
	for range 2 {
		if err := w.pass(w.theosChat()); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.posts(model.ChatUpdates); len(got) != 2 {
		t.Fatalf("a restart posted again: %q", got)
	}
}

// A room the bot can't post in fails that post, and the pass, for the queue
// to try again with backoff; the other rooms' news goes out, and the failed
// post goes out once the room is back.
func TestMissingRoomIsRetried(t *testing.T) {
	failed := &c4v1beta1.Task{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "api-6"},
		Spec:   c4v1beta1.TaskSpec{Kind: c4v1beta1.TaskImplement, Issue: 6},
		Status: c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskFailed}}
	w := newWorld(t)
	bot := w.theosChat()
	if err := w.pass(bot); err != nil {
		t.Fatal(err)
	}
	alerts := w.rooms[model.ChatAlerts]
	w.hs.Leave(alerts, w.bot)
	w.create(failed)
	w.create(merged("a", 2))
	if err := w.pass(bot); err == nil || !strings.Contains(err.Error(), "posting TaskFailed.api-6 in alerts") {
		t.Fatalf("pass = %v, want the alert's post failed", err)
	}
	if got := w.posts(model.ChatUpdates); len(got) != 1 {
		t.Fatalf("the other rooms' news waited: %q", got)
	}
	w.hs.Join(alerts, w.bot)
	if err := w.pass(w.theosChat()); err != nil {
		t.Fatal(err)
	}
	if got := w.posts(model.ChatAlerts); len(got) != 1 || !strings.Contains(got[0], "Task api-6 on issue #6 failed") {
		t.Fatalf("the alert once the room is back: %q", got)
	}
}

// A Chat whose rooms the operator hasn't made is left for later.
func TestChatNotReadyWaits(t *testing.T) {
	w := newWorld(t)
	w.chat.Status.Conditions[0].Status = metav1.ConditionFalse
	if err := w.kube.Update(context.Background(), w.chat); err != nil {
		t.Fatal(err)
	}
	res, err := w.theosChat().Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{
		Namespace: ns, Name: "chat"}})
	if err != nil || res.RequeueAfter != 5*time.Second {
		t.Fatalf("Reconcile = %+v, %v", res, err)
	}
	var cm corev1.ConfigMap
	if err := w.kube.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: StateConfigMap}, &cm); err == nil {
		t.Fatal("state kept for a Chat without rooms")
	}
}

// With the UI's address, a post links to its object's page in the UI, in the
// Chat's namespace; the formatted body carries it as a link.
func TestPostsLinkToTheUI(t *testing.T) {
	failed := &c4v1beta1.Task{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "api-6"},
		Spec:   c4v1beta1.TaskSpec{Kind: c4v1beta1.TaskImplement, Issue: 6},
		Status: c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskFailed}}
	w := newWorld(t)
	bot := w.theosChat()
	bot.UIURL = "https://theos.example.com"
	if err := w.pass(bot); err != nil {
		t.Fatal(err)
	}
	w.create(failed)
	if err := w.pass(bot); err != nil {
		t.Fatal(err)
	}
	page := "https://theos.example.com/?kind=Task&name=api-6&ns=" + ns
	got := w.posts(model.ChatAlerts)
	if len(got) != 1 || !strings.HasSuffix(got[0], "Task in theos: "+page) {
		t.Fatalf("the alert links to the Task's page: %q", got)
	}
	room, _ := w.hs.Room(w.rooms[model.ChatAlerts])
	for _, e := range room.Events {
		if e.Type != "m.room.message" || e.Sender != w.bot {
			continue
		}
		if html, _ := e.Content["formatted_body"].(string); !strings.Contains(html,
			`<a href="https://theos.example.com/?kind=Task&amp;name=api-6&amp;ns=shop">Task in theos</a>`) {
			t.Errorf("the alert's formatted body has no link to the page: %s", html)
		}
	}
}
