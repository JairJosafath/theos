/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/adapters/matrix"
	"github.com/JairJosafath/theos/internal/adapters/matrix/matrixtest"
	"github.com/JairJosafath/theos/internal/chatbot"
	"github.com/JairJosafath/theos/internal/model"
)

// newTheosChat is theos-chat as cmd/theos-chat runs it, on chatFake, as its
// own ServiceAccount bound to config/chat/rbac.yaml's role; each call is a
// fresh process, as after a restart: all it knows is in its ConfigMap.
func newTheosChat() *chatbot.Bot {
	GinkgoHelper()
	c := theosChatConfig()
	cl, err := client.New(c, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	return &chatbot.Bot{Client: cl, APIReader: cl, Config: c, ChatHosts: matrix.Hosts(nil)}
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
})
