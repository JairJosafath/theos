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
	"crypto/rand"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/adapters/matrix"
	"github.com/JairJosafath/theos/internal/adapters/matrix/matrixtest"
	"github.com/JairJosafath/theos/internal/chatbot"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

// newTheosChat is theos-chat as cmd/theos-chat runs it, on chatFake (a Chat at
// chatPortFake's address reaches chatPortFake), as its own ServiceAccount
// bound to config/chat/rbac.yaml's role; each call is a fresh process, as
// after a restart: all it knows is in its ConfigMap.
func newTheosChat() *chatbot.Bot {
	GinkgoHelper()
	c := theosChatConfig()
	cl, err := client.New(c, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	return &chatbot.Bot{Client: cl, APIReader: cl, Config: c, ChatHosts: chatHosts()}
}

// chatPortFake is the ports' fake chat host (#153), at an address of its
// own: a Chat that names it is on the fake, every other on chatFake. Its
// message IDs are unique in a room alone, as Slack's are.
var chatPortFake = fake.NewChatHost()

// chatSlackFake is the ports' fake chat host shaped as Slack (#159): no
// Space, a channel's one name, member IDs. It is the suite's Slack: the
// operator's address for a Slack Chat is its own.
var chatSlackFake = fake.NewChatHost(fake.WithoutSpaces(), fake.OneNamePerRoom(), fake.MemberIDs(),
	fake.ChatAddress("https://slack.test/api"))

// chatHosts are the chat adapters of the suite's operator and theos-chat, as
// cmd/main.go gives them: Matrix's, which reaches chatFake, and for a Chat at
// chatPortFake's address, the fake's; and Slack's, chatSlackFake.
func chatHosts() map[string]ports.ChatHosts {
	matrixHosts, onFake := matrix.Hosts(nil), chatPortFake.Hosts()
	return map[string]ports.ChatHosts{model.ChatMatrix: func(cfg ports.ChatConfig) (ports.ChatHost, error) {
		if cfg.Address == chatPortFake.Address() {
			return onFake(cfg)
		}
		return matrixHosts(cfg)
	}, model.ChatSlack: chatSlackFake.Hosts()}
}

// slackFakeChat is a Slack Chat on chatSlackFake whose app's bot and two
// tokens are in Secret slack-bot in ns, with people; it returns the Chat,
// the bot's member ID and its bot token.
func slackFakeChat(ns string, people ...c4v1beta1.ChatSlackPerson) (*c4v1beta1.Chat, string, string) {
	GinkgoHelper()
	bot, token := chatSlackFake.NewUser("bot-" + ns)
	mustCreate(&corev1.Secret{ObjectMeta: objectMeta(ns, "slack-bot"), Data: map[string][]byte{
		model.ChatSlackBotTokenKey: []byte(token), model.ChatSlackAppTokenKey: []byte(rand.Text())}})
	chat := newSlackChat(ns, "chat")
	chat.Spec.Slack = &c4v1beta1.ChatSlack{People: people}
	return chat, bot, token
}

// slackFakePosts are channel's messages on chatSlackFake.
func slackFakePosts(channel string) []fake.ChatPost {
	GinkgoHelper()
	r, ok := chatSlackFake.Room(channel)
	Expect(ok).To(BeTrue(), "channel %s", channel)
	return r.Posts
}

// portFakeChat is a Chat on chatPortFake with its bot, whose token is in
// Secret chat-bot in ns, and the bot's user ID.
func portFakeChat(ns string, people ...c4v1beta1.ChatPerson) (*c4v1beta1.Chat, string) {
	GinkgoHelper()
	bot, token := chatPortFake.NewUser("bot-" + ns)
	mustCreate(&corev1.Secret{ObjectMeta: objectMeta(ns, "chat-bot"),
		Data: map[string][]byte{model.ChatTokenKey: []byte(token)}})
	return &c4v1beta1.Chat{ObjectMeta: objectMeta(ns, "chat"), Spec: c4v1beta1.ChatSpec{
		Homeserver: chatPortFake.Address(), CredentialsSecretRef: c4v1beta1.LocalRef{Name: "chat-bot"},
		People: people}}, bot
}

// portFakePosts are room's messages on chatPortFake.
func portFakePosts(room string) []fake.ChatPost {
	GinkgoHelper()
	r, ok := chatPortFake.Room(room)
	Expect(ok).To(BeTrue(), "room %s", room)
	return r.Posts
}

// chatPass runs one of bot's passes over chat.
func chatPass(bot *chatbot.Bot, chat *c4v1beta1.Chat) {
	GinkgoHelper()
	_, err := bot.Reconcile(ctx, ctrl.Request{NamespacedName: key(chat.Namespace, chat.Name)})
	Expect(err).NotTo(HaveOccurred())
}

// chatRoom is the ID of chat's room for purpose.
func chatRoom(chat *c4v1beta1.Chat, purpose model.ChatPurpose) string {
	for _, r := range chat.Status.Rooms {
		if model.ChatPurpose(r.Purpose) == purpose {
			return r.ID
		}
	}
	return ""
}

// relation is an event's relation: its kind and the event it relates to.
func relation(e matrixtest.Event) (string, string) {
	rel, _ := e.Content["m.relates_to"].(map[string]any)
	typ, _ := rel["rel_type"].(string)
	id, _ := rel["event_id"].(string)
	return typ, id
}

// roomEvents are room's timeline events.
func roomEvents(room string) []matrixtest.Event {
	GinkgoHelper()
	r, ok := chatFake.Room(room)
	Expect(ok).To(BeTrue(), "room %s", room)
	return r.Events
}

// botPosts are the bodies of the messages sender posted in room outside any
// thread, edit or reply, with their event IDs.
func botPosts(room, sender string) map[string]string {
	out := map[string]string{}
	for _, e := range roomEvents(room) {
		if typ, _ := relation(e); e.Type == "m.room.message" && e.Sender == sender && typ == "" {
			out[e.ID], _ = e.Content["body"].(string)
		}
	}
	return out
}

// related are the bodies of sender's events in room of relation typ to
// event: a thread's replies (m.thread), a root's edits (m.replace, their new
// text) or its reactions (m.annotation, their keys).
func related(room, sender, typ, event string) []string {
	var out []string
	for _, e := range roomEvents(room) {
		t, id := relation(e)
		if t != typ || id != event || e.Sender != sender {
			continue
		}
		var body string
		switch typ {
		case "m.replace":
			nc, _ := e.Content["m.new_content"].(map[string]any)
			body, _ = nc["body"].(string)
		case "m.annotation":
			rel, _ := e.Content["m.relates_to"].(map[string]any)
			body, _ = rel["key"].(string)
		default:
			body, _ = e.Content["body"].(string)
		}
		out = append(out, body)
	}
	return out
}

// theos-chat's ConfigMap and the rooms it reads are its own: a Chat that
// isn't Ready is left alone.
var _ = Describe("theos-chat", func() {
	It("leaves a Chat whose rooms aren't made alone", func() {
		ns := newProjectNamespace()
		chat := newChat(ns, "chat", "matrix-bot")
		mustCreate(chat)
		chatPass(newTheosChat(), chat)
		err := k8sClient.Get(ctx, key(ns, chatbot.StateConfigMap), &corev1.ConfigMap{})
		Expect(client.IgnoreNotFound(err)).To(Succeed())
		Expect(err).To(HaveOccurred(), "no state for a Chat without rooms")
	})

	It("leaves a Chat on a host it runs no adapter for alone, even one that reads Ready (#158)", func() {
		ns := newProjectNamespace()
		chat := newSlackChat(ns, "chat")
		mustCreate(chat)
		chatReady(chat, metav1.ConditionFalse, model.ChatSecretMissing)

		// As the operator, which runs Slack, would write it: a theos-chat
		// built without Slack's adapter (#159) has none for the host, so it
		// reads no Secret (there is none) and posts nothing.
		bot := newTheosChat()
		delete(bot.ChatHosts, model.ChatSlack)
		orig := chat.DeepCopy()
		chat.Status.Homeserver = "https://slack.example/api"
		for _, p := range model.ChatPurposes {
			chat.Status.Rooms = append(chat.Status.Rooms, c4v1beta1.ChatRoomStatus{
				Purpose: c4v1beta1.ChatPurpose(p), ID: "C" + strings.ToUpper(string(p))})
		}
		apimeta.SetStatusCondition(&chat.Status.Conditions, metav1.Condition{Type: c4v1beta1.ConditionChatReady,
			Status: metav1.ConditionTrue, Reason: model.ChatReady, Message: "ready", ObservedGeneration: chat.Generation})
		Expect(k8sClient.Status().Patch(ctx, chat, client.MergeFrom(orig))).To(Succeed())

		res, err := bot.Reconcile(ctx, ctrl.Request{NamespacedName: key(ns, "chat")})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))
		err = k8sClient.Get(ctx, key(ns, chatbot.StateConfigMap), &corev1.ConfigMap{})
		Expect(client.IgnoreNotFound(err)).To(Succeed())
		Expect(err).To(HaveOccurred(), "no state for a Chat on a host theos-chat doesn't run")
	})
})
