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
	"crypto/rand"
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
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	portsfake "github.com/JairJosafath/theos/internal/ports/fake"
)

// slackChat is a Ready Slack Chat in namespace on h, its channels made by
// the bot whose token is in its Secret slack-bot with appToken,
// as the operator leaves it.
func slackChat(t *testing.T, h *portsfake.ChatHost, namespace, token, appToken string) (*c4v1beta1.Chat, *corev1.Secret) {
	t.Helper()
	chat := &c4v1beta1.Chat{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "chat"},
		Spec: c4v1beta1.ChatSpec{Host: c4v1beta1.ChatHostSlack, CredentialsSecretRef: c4v1beta1.LocalRef{
			Name: "slack-bot"}, DigestMinutes: 10},
		Status: c4v1beta1.ChatStatus{Homeserver: h.Address(), Conditions: []metav1.Condition{{
			Type: c4v1beta1.ConditionChatReady, Status: metav1.ConditionTrue, Reason: model.ChatReady,
			LastTransitionTime: metav1.Now()}}}}
	maker := h.For(ports.ChatConfig{Address: h.Address(), Token: token})
	defer maker.Close()
	for _, p := range model.ChatPurposes {
		room, err := maker.EnsureRoom(context.Background(), ports.ChatRoomSpec{
			Alias: model.ChatRoomAlias("theos-"+namespace, p)})
		if err != nil {
			t.Fatal(err)
		}
		chat.Status.Rooms = append(chat.Status.Rooms, c4v1beta1.ChatRoomStatus{Purpose: c4v1beta1.ChatPurpose(p),
			ID: room.ID})
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "slack-bot"},
		Data: map[string][]byte{model.ChatSlackBotTokenKey: []byte(token),
			model.ChatSlackAppTokenKey: []byte(appToken)}}
	return chat, secret
}

// TestReplacedHostIsClosed covers #234 (for #159): a ChatHost theos-chat
// built for a token is closed once no Chat uses it, after a token change or
// the Chat's deletion, so its connection swallows no event; one another
// Chat still uses stays open.
func TestReplacedHostIsClosed(t *testing.T) {
	ctx := context.Background()
	h := portsfake.NewChatHost(portsfake.WithoutSpaces(), portsfake.OneNamePerRoom(), portsfake.MemberIDs())
	botID, token := h.NewUser("bot")
	appToken := rand.Text()
	shop, shopSecret := slackChat(t, h, "shop", token, appToken)
	cart, cartSecret := slackChat(t, h, "cart", token, appToken)
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, c4v1beta1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(shop, shopSecret, cart, cartSecret).Build()
	bot := &Bot{Client: kube, APIReader: kube, ChatHosts: map[string]ports.ChatHosts{model.ChatSlack: h.Hosts()},
		Now: func() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) }}
	pass := func(chat *c4v1beta1.Chat) {
		t.Helper()
		if _, err := bot.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(chat)}); err != nil {
			t.Fatal(err)
		}
	}
	rotate := func(secret *corev1.Secret) {
		t.Helper()
		token, err := h.NewToken(botID)
		if err != nil {
			t.Fatal(err)
		}
		secret.Data[model.ChatSlackBotTokenKey] = []byte(token)
		if err := kube.Update(ctx, secret); err != nil {
			t.Fatal(err)
		}
	}
	wantOpen := func(want int, when string) {
		t.Helper()
		if got := h.Open(); got != want {
			t.Errorf("%s: %d hosts open, want %d", when, got, want)
		}
	}

	pass(shop)
	pass(cart)
	wantOpen(1, "two Chats on one bot and token")

	rotate(shopSecret)
	pass(shop)
	wantOpen(2, "shop's token changed while cart still uses the old one")

	rotate(cartSecret)
	pass(cart)
	wantOpen(2, "cart's token changed too: the old host is closed, each Chat's new one open")

	if err := kube.Delete(ctx, cart); err != nil {
		t.Fatal(err)
	}
	pass(cart)
	wantOpen(1, "cart deleted")
	pass(shop)
	wantOpen(1, "shop's pass again, on the host it has")
	if _, err := bot.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "gone",
		Name: "chat"}}); err != nil {
		t.Fatal(err)
	}
	wantOpen(1, "a Chat theos-chat never served gone")
}
