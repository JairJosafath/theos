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

package fake_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

// What the fake chat host does its own way, beside the suite (#153): a
// message's ID is its room's alone, a Sync with a wait wakes for a message,
// and a host without Spaces is never made to hold one.
func TestChatHostOwnWays(t *testing.T) {
	ctx := context.Background()
	h := fake.NewChatHost()
	_, token := h.NewUser("bot")
	bot := h.For(ports.ChatConfig{Address: h.Address(), Token: token})
	ada, _ := h.NewUser("ada")
	aliases := []string{"shop-specs", "shop-general"}
	rooms := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		room := must(bot.EnsureRoom(ctx, ports.ChatRoomSpec{Alias: alias, Name: alias}))(t)
		noErr(t, bot.Invite(ctx, room.ID, ada))
		noErr(t, h.Join(room.ID, ada))
		rooms = append(rooms, room.ID)
	}

	sent := must(bot.Send(ctx, rooms[0], ports.ChatMessage{Text: "Spec of issue #1"}))(t)
	said := must(h.Post(rooms[1], ada, "hello", ""))(t)
	if sent != said {
		t.Errorf("the first messages of two rooms are %s and %s, want one ID, as a Slack ts is its channel's", sent,
			said)
	}

	since := must(bot.Sync(ctx, "", 0))(t).Next
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = h.Post(rooms[1], ada, "status", "")
	}()
	got := must(bot.Sync(ctx, since, time.Minute))(t)
	if len(got.Events) != 1 || got.Events[0].Text != "status" {
		t.Errorf("Sync() with a wait = %+v, want the message said while it waited", got)
	}
	start := time.Now()
	if idle := must(bot.Sync(ctx, got.Next, 50*time.Millisecond))(t); len(idle.Events) != 0 ||
		time.Since(start) < 50*time.Millisecond {
		t.Errorf("Sync() with a wait and nothing said = %+v after %s, want nothing, after the wait", idle,
			time.Since(start))
	}

	slack := fake.NewChatHost(fake.WithoutSpaces())
	_, token = slack.NewUser("bot")
	_, err := slack.For(ports.ChatConfig{Address: slack.Address(), Token: token}).EnsureRoom(ctx,
		ports.ChatRoomSpec{Alias: "shop", Space: true})
	if !errors.Is(err, fake.ErrNoSpaces) || len(slack.Rooms()) != 0 {
		t.Errorf("EnsureRoom() of a Space on a host without = %v, with rooms %v; want ErrNoSpaces and none", err,
			slack.Rooms())
	}
	if _, err := slack.Hosts()(ports.ChatConfig{Address: "https://elsewhere.test", Token: token}); err == nil {
		t.Error("Hosts() built a ChatHost of another address")
	}
}
