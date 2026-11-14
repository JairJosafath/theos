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

// TestChatHostDeactivate covers #251: an invite of a member Deactivate
// closed is refused, theirs alone, with CodeUserNotFound, and taken again
// after Reactivate.
func TestChatHostDeactivate(t *testing.T) {
	ctx := context.Background()
	h := fake.NewChatHost(fake.MemberIDs())
	_, token := h.NewUser("bot")
	bot := h.For(ports.ChatConfig{Address: h.Address(), Token: token})
	ada, _ := h.NewUser("ada")
	room := must(bot.EnsureRoom(ctx, ports.ChatRoomSpec{Alias: "shop-general"}))(t)
	h.Deactivate(ada)
	err := bot.Invite(ctx, room.ID, ada)
	if refused, ok := errors.AsType[*ports.ChatInviteRefusedError](err); !ok || refused.User != ada ||
		refused.Code != fake.CodeUserNotFound {
		t.Fatalf("Invite of a deactivated member = %v, want a ChatInviteRefusedError naming %s and %s", err, ada,
			fake.CodeUserNotFound)
	}
	h.Reactivate(ada)
	noErr(t, bot.Invite(ctx, room.ID, ada))
}

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

// #159: the fake shaped as Slack for the Chat controller's and theos-chat's
// specs. Its user IDs are member IDs, a scope Revoke takes from a token
// refuses the methods named until Grant gives it back, a token made again
// speaks as the same bot, and a ChatHost For built counts as open until
// its Close, after which it takes no call (#234).
func TestChatHostShapedAsSlack(t *testing.T) {
	ctx := context.Background()
	h := fake.NewChatHost(fake.WithoutSpaces(), fake.OneNamePerRoom(), fake.MemberIDs())
	botID, token := h.NewUser("bot")
	ada, _ := h.NewUser("ada")
	if again, _ := h.NewUser("ada"); again != ada || botID != "U00000001" || ada != "U00000002" {
		t.Errorf("user IDs = %s, %s and %s again, want U00000001, U00000002 and the same", botID, ada, again)
	}

	bot := h.For(ports.ChatConfig{Address: h.Address(), Token: token})
	h.Revoke(token, "groups:write", "Invite")
	room := must(bot.EnsureRoom(ctx, ports.ChatRoomSpec{Alias: "shop-specs"}))(t)
	err := bot.Invite(ctx, room.ID, ada)
	if missing, ok := errors.AsType[*ports.ChatScopeMissingError](err); !ok || missing.Scope != "groups:write" ||
		!errors.Is(err, ports.ErrChatScopeMissing) {
		t.Errorf("Invite() without its scope = %v, want a ChatScopeMissingError naming groups:write", err)
	}
	h.Grant(token, "groups:write")
	noErr(t, bot.Invite(ctx, room.ID, ada))

	second, err := h.NewToken(botID)
	noErr(t, err)
	other := h.For(ports.ChatConfig{Address: h.Address(), Token: second})
	if me := must(other.WhoAmI(ctx))(t); me != botID {
		t.Errorf("WhoAmI() with a token made again = %s, want %s", me, botID)
	}
	if _, err := h.NewToken("U99999999"); err == nil {
		t.Error("NewToken() of no user = nil error")
	}
	if got := h.Open(); got != 2 {
		t.Errorf("Open() = %d with two ChatHosts built, want 2", got)
	}
	bot.Close()
	bot.Close()
	if got := h.Open(); got != 1 {
		t.Errorf("Open() = %d after one of two closed, twice, want 1", got)
	}
	if _, err := bot.Sync(ctx, "", 0); !errors.Is(err, fake.ErrChatClosed) {
		t.Errorf("Sync() after Close = %v, want ErrChatClosed", err)
	}
	must(other.Sync(ctx, "", 0))(t)
}
