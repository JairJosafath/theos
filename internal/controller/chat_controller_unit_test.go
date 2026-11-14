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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
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
