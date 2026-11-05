/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package chatbot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// StateConfigMap is the ConfigMap in a Chat's namespace where theos-chat
// keeps what it posted and where it read the rooms up to. Its RBAC names it
// (config/chat/rbac.yaml), so theos-chat writes no other ConfigMap.
const StateConfigMap = "theos-chat"

// The keys of the state ConfigMap: the sync token, when the last digest
// went out, whether the Chat had its first pass, and one key per news item
// posted, itemPrefix and the news's key.
const (
	keySince    = "since"
	keyDigestAt = "digestAt"
	keySeeded   = "seeded"
	itemPrefix  = "item."
)

// state is a Chat's state as read from its ConfigMap and changed by a pass.
type state struct {
	cm       *corev1.ConfigMap
	since    string
	digestAt time.Time
	seeded   bool
	posted   map[string]*model.ChatPosted
	dirty    bool
}

func (s *state) set(key string, p *model.ChatPosted) {
	s.posted[key], s.dirty = p, true
}

func (s *state) remove(key string) {
	delete(s.posted, key)
	s.dirty = true
}

// thread is the item of the thread rooted at event in room purpose, false
// when theos-chat started no such thread.
func (s *state) thread(purpose model.ChatPurpose, event string) (model.ChatItem, bool) {
	if event == "" {
		return model.ChatItem{}, false
	}
	for _, p := range s.posted {
		if p.Event == event && p.Room == purpose && p.Item.Event != "" {
			return p.Item, true
		}
	}
	return model.ChatItem{}, false
}

// ownedBy reports whether cm belongs to chat: a ConfigMap of a Chat deleted
// and made again under the same name starts over.
func ownedBy(cm *corev1.ConfigMap, chat *c4v1beta1.Chat) bool {
	for _, ref := range cm.OwnerReferences {
		if ref.UID == chat.UID {
			return true
		}
	}
	return false
}

// loadState reads chat's state from its ConfigMap, live; none, or one of an
// earlier Chat, is a fresh start.
func (b *Bot) loadState(ctx context.Context, chat *c4v1beta1.Chat) (*state, error) {
	st := &state{posted: map[string]*model.ChatPosted{}}
	var cm corev1.ConfigMap
	err := b.APIReader.Get(ctx, client.ObjectKey{Namespace: chat.Namespace, Name: StateConfigMap}, &cm)
	switch {
	case apierrors.IsNotFound(err):
		return st, nil
	case err != nil:
		return nil, fmt.Errorf("reading ConfigMap %s: %w", StateConfigMap, err)
	}
	st.cm = &cm
	if !ownedBy(&cm, chat) {
		st.dirty = true
		return st, nil
	}
	st.since, st.seeded = cm.Data[keySince], cm.Data[keySeeded] == "true"
	if at := cm.Data[keyDigestAt]; at != "" {
		if t, err := time.Parse(time.RFC3339, at); err == nil {
			st.digestAt = t
		}
	}
	for k, v := range cm.Data {
		key, ok := strings.CutPrefix(k, itemPrefix)
		if !ok {
			continue
		}
		var p model.ChatPosted
		if err := json.Unmarshal([]byte(v), &p); err == nil {
			st.posted[key] = &p
		}
	}
	return st, nil
}

// saveState writes st to chat's ConfigMap when it changed, making it, owned
// by the Chat, the first time.
func (b *Bot) saveState(ctx context.Context, chat *c4v1beta1.Chat, st *state) error {
	if !st.dirty {
		return nil
	}
	data := map[string]string{keySince: st.since}
	if st.seeded {
		data[keySeeded] = "true"
	}
	if !st.digestAt.IsZero() {
		data[keyDigestAt] = st.digestAt.UTC().Format(time.RFC3339)
	}
	for key, p := range st.posted {
		v, err := json.Marshal(p)
		if err != nil {
			return err
		}
		data[itemPrefix+key] = string(v)
	}
	owner := metav1.OwnerReference{APIVersion: c4v1beta1.GroupVersion.String(), Kind: "Chat", Name: chat.Name,
		UID: chat.UID}
	if st.cm == nil {
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: chat.Namespace, Name: StateConfigMap,
			Labels:          map[string]string{"app.kubernetes.io/name": "theos-chat"},
			OwnerReferences: []metav1.OwnerReference{owner}}, Data: data}
		if err := b.Client.Create(ctx, cm); err != nil {
			return fmt.Errorf("making ConfigMap %s: %w", StateConfigMap, err)
		}
		st.cm, st.dirty = cm, false
		return nil
	}
	st.cm.Data, st.cm.OwnerReferences = data, []metav1.OwnerReference{owner}
	if err := b.Client.Update(ctx, st.cm); err != nil {
		return fmt.Errorf("writing ConfigMap %s: %w", StateConfigMap, err)
	}
	st.dirty = false
	return nil
}
