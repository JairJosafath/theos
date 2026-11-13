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

package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// The channels the adapter's tests make.
const (
	aliasSpecs  = "theos-shop-specs"
	aliasAlerts = "theos-shop-alerts"
)

// rooms is a workspace with the channels specs and alerts, and ada in both.
type rooms struct {
	workspace
	specs, alerts, ada string
}

func newRooms(t *testing.T) rooms {
	t.Helper()
	x := rooms{workspace: newWorkspace(t)}
	x.ada = x.ws.NewUser("ada")
	for _, r := range []struct {
		alias string
		id    *string
	}{{aliasSpecs, &x.specs}, {aliasAlerts, &x.alerts}} {
		room, err := x.c.EnsureRoom(t.Context(), ports.ChatRoomSpec{Alias: r.alias})
		if err != nil {
			t.Fatal(err)
		}
		if err := x.c.Invite(t.Context(), room.ID, x.ada); err != nil {
			t.Fatal(err)
		}
		*r.id = room.ID
	}
	return x
}

// post posts text in room as ada.
func (x rooms) post(t *testing.T, room, text, thread string) string {
	t.Helper()
	ts, err := x.ws.Post(room, x.ada, text, thread)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// sync syncs c from since without waiting.
func sync0(t *testing.T, c *Client, since string) ports.ChatSync {
	t.Helper()
	got, err := c.Sync(t.Context(), since, 0)
	if err != nil {
		t.Fatalf("Sync(%q) = %v", since, err)
	}
	return got
}

// ids are the events' IDs, with an edit's marked.
func ids(events []ports.ChatEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		id := e.Room + "/" + e.ID
		if e.Replaces != "" {
			id += " (edit)"
		}
		out = append(out, id)
	}
	return out
}

// wantIDs fails t unless events are the messages want, in that order.
func wantIDs(t *testing.T, what string, events []ports.ChatEvent, want ...string) {
	t.Helper()
	if got := ids(events); !slices.Equal(got, want) {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

// TestDecode covers #155: what Slack sends is read as its writer typed it
// (decision 6): the entities last, so one typed in the text makes no link.
func TestDecode(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"plain", "status", "status"},
		{"the three entities", "changes: a &lt; b &amp;&amp; c &gt; d", "changes: a < b && c > d"},
		{"an entity typed, as written", "&amp;lt;b&amp;gt; and &amp;amp;", "&lt;b&gt; and &amp;"},
		{"a labelled link", "brief a shop like <https://plants.example/a?b=1&amp;c=2|the plant shop>",
			"brief a shop like the plant shop https://plants.example/a?b=1&c=2"},
		{"a bare link, its label the address", "see <https://plants.example/|https://plants.example/>",
			"see https://plants.example/"},
		{"a link without a label", "<https://plants.example/>", "https://plants.example/"},
		{"a mailto link", "<mailto:ada@example.org|ada@example.org>", "ada@example.org"},
		{"a mention", "<@U012AB3CD> approve", "@U012AB3CD approve"},
		{"a mention with a name", "<@W012AB3CD|ada> hi", "@W012AB3CD hi"},
		{"a channel", "see <#C012AB3CD|general> and <#C0456>", "see #general and #C0456"},
		{"a special mention", "<!here> and <!channel> and <!subteam^S012|@devs>", "@here and @channel and @devs"},
		{"an angle left open", "a <b", "a <b"},
		{"an entity in a label", "<https://a.example/|x &lt; y>", "x < y https://a.example/"},
	}
	for _, tt := range tests {
		if got := Decode(tt.in); got != tt.want {
			t.Errorf("%s: Decode(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
}

// TestSyncDecodesText covers #155: a person's message reaches the port as
// they typed it, the entities, a labelled link and a mention decoded.
func TestSyncDecodesText(t *testing.T) {
	x := newRooms(t)
	since := sync0(t, x.c, "").Next
	x.post(t, x.specs, "changes: a &lt; b, see <https://docs.example/a?x=1&amp;y=2|the doc>, ask <@"+x.ada+">", "")
	got := sync0(t, x.c, since).Events
	want := "changes: a < b, see the doc https://docs.example/a?x=1&y=2, ask @" + x.ada
	if len(got) != 1 || got[0].Text != want {
		t.Errorf("Sync() = %+v, want one message reading %q", got, want)
	}
}

// TestSocketOpenedBySyncAlone covers #155: the operator, which calls no
// Sync, never opens a socket (decision 7); the first Sync opens one, and
// the next uses it.
func TestSocketOpenedBySyncAlone(t *testing.T) {
	x := newRooms(t)
	ctx := t.Context()
	id, err := x.c.Send(ctx, x.specs, ports.ChatMessage{Text: "Spec: dark mode"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.c.Reply(ctx, x.specs, id, ports.ChatMessage{Text: "approved"}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.c.Edit(ctx, x.specs, id, ports.ChatMessage{Text: "Spec: dark mode (approved)"}); err != nil {
		t.Fatal(err)
	}
	if _, err := x.c.React(ctx, x.specs, id, model.ChatDoneReaction); err != nil {
		t.Fatal(err)
	}
	if n := x.ws.Opens(); n != 0 {
		t.Fatalf("apps.connections.open was called %d times before any Sync, want none", n)
	}
	since := sync0(t, x.c, "").Next
	sync0(t, x.c, since)
	if n, open := x.ws.Opens(), x.ws.Open(); n != 1 || open != 1 {
		t.Errorf("after two Syncs, apps.connections.open was called %d times and %d sockets are open, want 1 and 1",
			n, open)
	}
}

// TestSyncDropsDeliverySentTwice covers #155: an envelope Slack sends again
// with an event_id already seen is acknowledged and dropped (decision 8).
func TestSyncDropsDeliverySentTwice(t *testing.T) {
	x := newRooms(t)
	since := sync0(t, x.c, "").Next
	said := x.post(t, x.specs, "approve", "")
	if err := x.ws.Redeliver(); err != nil {
		t.Fatal(err)
	}
	got := sync0(t, x.c, since)
	wantIDs(t, "Sync() after a delivery sent twice", got.Events, x.specs+"/"+said)
	wantIDs(t, "the next Sync()", sync0(t, x.c, got.Next).Events)
}

// TestSyncReadsALateMessage covers #155: a message from another channel
// that arrives after a newer one is still read once, since a token is a
// position in the buffer and not a time (decision 8).
func TestSyncReadsALateMessage(t *testing.T) {
	x := newRooms(t)
	since := sync0(t, x.c, "").Next
	older, deliver, err := x.ws.PostHeld(x.alerts, x.ada, "retry", "")
	if err != nil {
		t.Fatal(err)
	}
	newer := x.post(t, x.specs, "approve", "")
	first := sync0(t, x.c, since)
	wantIDs(t, "Sync() before the older message arrived", first.Events, x.specs+"/"+newer)
	if err := deliver(); err != nil {
		t.Fatal(err)
	}
	second := sync0(t, x.c, first.Next)
	wantIDs(t, "Sync() after it arrived", second.Events, x.alerts+"/"+older)
	if len(second.Events) == 1 && !second.Events[0].Time.Before(first.Events[0].Time) {
		t.Errorf("the late message's time %s is not before the newer one's %s", second.Events[0].Time,
			first.Events[0].Time)
	}
	wantIDs(t, "the next Sync()", sync0(t, x.c, second.Next).Events)
}

// TestSyncReadersWithTheirOwnSince covers #155: Chats with the same tokens
// share one Client, each syncing from its own since (decision 8). Four
// readers sync, waiting, while two people post in two channels at once;
// each reader gets every message once, each Sync a channel's oldest first.
// Slack promises no order of delivery, so two Syncs may not be in order.
func TestSyncReadersWithTheirOwnSince(t *testing.T) {
	x := newRooms(t)
	bob := x.ws.NewUser("bob")
	for _, room := range []string{x.specs, x.alerts} {
		if err := x.c.Invite(t.Context(), room, bob); err != nil {
			t.Fatal(err)
		}
	}
	const readers, each = 4, 15
	starts := make([]string, readers)
	for i := range starts {
		starts[i] = sync0(t, x.c, "").Next
	}
	var posted sync.Map // channel/ts → true
	var posters sync.WaitGroup
	for _, p := range []struct{ user, room string }{{x.ada, x.specs}, {bob, x.alerts}, {x.ada, x.alerts},
		{bob, x.specs}} {
		posters.Go(func() {
			for i := range each {
				ts, err := x.ws.Post(p.room, p.user, fmt.Sprintf("status %d", i), "")
				if err != nil {
					t.Error(err)
					return
				}
				posted.Store(p.room+"/"+ts, true)
			}
		})
	}
	const total = 4 * each
	got := make([][]ports.ChatEvent, readers)
	unordered := make([]int, readers)
	var syncs sync.WaitGroup
	for r := range readers {
		syncs.Go(func() {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			since := starts[r]
			for len(got[r]) < total && ctx.Err() == nil {
				s, err := x.c.Sync(ctx, since, 200*time.Millisecond)
				if err != nil {
					if ctx.Err() == nil {
						t.Errorf("reader %d: Sync() = %v", r, err)
					}
					return
				}
				got[r] = append(got[r], s.Events...)
				since = s.Next
				if !slices.IsSortedFunc(s.Events, func(a, b ports.ChatEvent) int { return a.Time.Compare(b.Time) }) {
					unordered[r]++
				}
			}
		})
	}
	posters.Wait()
	syncs.Wait()
	for r, events := range got {
		if unordered[r] > 0 {
			t.Errorf("reader %d got %d Syncs whose messages were not oldest first", r, unordered[r])
		}
		seen := map[string]int{}
		for _, e := range events {
			seen[e.Room+"/"+e.ID]++
		}
		n := 0
		posted.Range(func(k, _ any) bool {
			n++
			if seen[k.(string)] != 1 {
				t.Errorf("reader %d got %s %d times, want once", r, k, seen[k.(string)])
			}
			return true
		})
		if len(events) != n {
			t.Errorf("reader %d got %d messages, want the %d posted", r, len(events), n)
		}
	}
}

// waitFor polls cond until it holds, failing t after a while.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !cond(); {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// syncUntil syncs from since, waiting, until n messages came, and returns
// them and the token after them.
func syncUntil(t *testing.T, c *Client, since string, n int) ([]ports.ChatEvent, string) {
	t.Helper()
	var events []ports.ChatEvent
	for deadline := time.Now().Add(10 * time.Second); len(events) < n; {
		if time.Now().After(deadline) {
			t.Fatalf("Sync() gave %q, want %d messages", ids(events), n)
		}
		got, err := c.Sync(t.Context(), since, 500*time.Millisecond)
		if err != nil {
			t.Fatalf("Sync() = %v", err)
		}
		events, since = append(events, got.Events...), got.Next
	}
	return events, since
}

// TestSyncDialsAgainAfterDisconnect covers #155: Slack's disconnect is
// answered by a new address from apps.connections.open and a new dial
// (decision 7), and a message said while no socket was open is read once.
func TestSyncDialsAgainAfterDisconnect(t *testing.T) {
	x := newRooms(t)
	since := sync0(t, x.c, "").Next
	before := x.post(t, x.specs, "status", "")
	x.ws.Disconnect("refresh_requested")
	// Said while the adapter dials again: it reaches the new socket or the
	// catch-up, and comes once either way.
	gap := x.post(t, x.alerts, "retry", "")
	events, next := syncUntil(t, x.c, since, 2)
	wantIDs(t, "Sync() across the disconnect", events, x.specs+"/"+before, x.alerts+"/"+gap)
	waitFor(t, "a second socket is open", func() bool { return x.ws.Opens() == 2 && x.ws.Open() == 1 })
	after := x.post(t, x.specs, "help", "")
	got := sync0(t, x.c, next)
	wantIDs(t, "Sync() on the new socket", got.Events, x.specs+"/"+after)
	wantIDs(t, "the next Sync()", sync0(t, x.c, got.Next).Events)
}

// TestSyncOpensAgainAfterTheSocketDrops covers #155: a socket that closes
// without a word is opened again by the next Sync, which reads what was
// said while it was closed.
func TestSyncOpensAgainAfterTheSocketDrops(t *testing.T) {
	x := newRooms(t)
	since := sync0(t, x.c, "").Next
	x.ws.Disconnect("")
	waitFor(t, "the socket is closed", func() bool { return x.ws.Open() == 0 })
	said := x.post(t, x.specs, "status", "")
	events, _ := syncUntil(t, x.c, since, 1)
	wantIDs(t, "Sync() after the socket dropped", events, x.specs+"/"+said)
	if n := x.ws.Opens(); n != 2 {
		t.Errorf("apps.connections.open was called %d times, want 2", n)
	}
}

// TestSyncCatchesUpAfterARestart covers #155: what was said while no
// socket was open is read through the Web API when one opens (decision 8):
// each channel's history and the replies of its threads after the token's
// time, and nothing the token had read, nor the bot's own.
func TestSyncCatchesUpAfterARestart(t *testing.T) {
	x := newRooms(t)
	ctx := t.Context()
	since := sync0(t, x.c, "").Next
	root := x.post(t, x.specs, "status", "")
	got := sync0(t, x.c, since)
	wantIDs(t, "Sync() before the restart", got.Events, x.specs+"/"+root)
	token := got.Next
	x.c.Close()

	// Said while theos-chat restarts: no socket is open.
	waitFor(t, "the socket is closed", func() bool { return x.ws.Open() == 0 })
	reply := x.post(t, x.specs, "approve", root)
	alert := x.post(t, x.alerts, "retry", "")
	// A thread begun in the outage: its root is in the history and in the
	// thread, and is read once.
	alertReply := x.post(t, x.alerts, "retry again", alert)
	if _, err := x.c.Send(ctx, x.alerts, ports.ChatMessage{Text: "Retried"}); err != nil {
		t.Fatal(err)
	}
	missed := []string{x.specs + "/" + reply, x.alerts + "/" + alert, x.alerts + "/" + alertReply}

	restarted := client(t, x.ws, x.botToken, x.appToken)
	got = sync0(t, restarted, token)
	wantIDs(t, "Sync() after the restart", got.Events, missed...)
	if len(got.Events) == 2 && got.Events[0].ThreadRoot != root {
		t.Errorf("the reply read in the catch-up = %+v, want it in the thread of %s", got.Events[0], root)
	}
	later := x.post(t, x.specs, "help", "")
	wantIDs(t, "the next Sync(), on the socket", sync0(t, restarted, got.Next).Events, x.specs+"/"+later)

	// Another Chat's token of before the restart, read once the socket is
	// open for a Chat that synced first: it gets all it missed too.
	other := client(t, x.ws, x.botToken, x.appToken)
	sync0(t, other, "")
	wantIDs(t, "Sync() of an older token on an open socket", sync0(t, other, token).Events,
		slices.Concat(missed, []string{x.specs + "/" + later})...)
}

// TestEnsureRoomNameTaken covers #155: a name Slack says is taken, by a
// channel the bot is not in, is a ChatAliasTakenError naming it
// (decision 4).
func TestEnsureRoomNameTaken(t *testing.T) {
	w := newWorkspace(t)
	w.ws.Squat(aliasSpecs)
	_, err := w.c.EnsureRoom(t.Context(), ports.ChatRoomSpec{Alias: aliasSpecs, Name: "specs"})
	taken, ok := errors.AsType[*ports.ChatAliasTakenError](err)
	if !errors.Is(err, ports.ErrChatAliasTaken) || !ok || taken.Alias != "#"+aliasSpecs {
		t.Errorf("EnsureRoom() of a taken name = %v, want a ChatAliasTakenError naming #%s", err, aliasSpecs)
	}
	if _, err := w.c.EnsureRoom(t.Context(), ports.ChatRoomSpec{Alias: "shop", Space: true}); !errors.Is(err,
		ErrNoSpaces) {
		t.Errorf("EnsureRoom() of a Space = %v, want ErrNoSpaces", err)
	}
}

// TestScopeMissing covers #155: a scope Slack says is missing is
// ports.ErrChatScopeMissing, naming the scope (decision 9), for the bot
// token and for the app-level token.
func TestScopeMissing(t *testing.T) {
	for _, tt := range []struct {
		scope string
		call  func(ctx context.Context, x rooms) error
	}{
		{"groups:write", func(ctx context.Context, x rooms) error {
			_, err := x.c.EnsureRoom(ctx, ports.ChatRoomSpec{Alias: "theos-shop-general"})
			return err
		}},
		{"chat:write", func(ctx context.Context, x rooms) error {
			_, err := x.c.Send(ctx, x.specs, ports.ChatMessage{Text: "hi"})
			return err
		}},
		{"reactions:write", func(ctx context.Context, x rooms) error {
			_, err := x.c.React(ctx, x.specs, "1.000001", model.ChatDoneReaction)
			return err
		}},
		{"groups:read", func(ctx context.Context, x rooms) error {
			_, err := x.c.EnsureRoom(ctx, ports.ChatRoomSpec{Alias: aliasSpecs})
			return err
		}},
		{"groups:history", func(ctx context.Context, x rooms) error {
			// A token of before a restart: the catch-up reads the history.
			before := newWorkspaceClient(x)
			token, err := before.Sync(ctx, "", 0)
			before.Close()
			if err != nil {
				return err
			}
			_, err = x.c.Sync(ctx, token.Next, 0)
			return err
		}},
		{"connections:write", func(ctx context.Context, x rooms) error {
			_, err := x.c.Sync(ctx, "", 0)
			return err
		}},
	} {
		t.Run(tt.scope, func(t *testing.T) {
			x := newRooms(t)
			x.ws.Revoke(x.botToken, tt.scope)
			x.ws.Revoke(x.appToken, tt.scope)
			err := tt.call(t.Context(), x)
			missing, ok := errors.AsType[*ports.ChatScopeMissingError](err)
			if !errors.Is(err, ports.ErrChatScopeMissing) || !ok || missing.Scope != tt.scope {
				t.Errorf("a call without %s = %v, want a ChatScopeMissingError naming it", tt.scope, err)
			}
			if errors.Is(err, ports.ErrChatCredentialRefused) {
				t.Errorf("a call without %s = %v, which is no refused token", tt.scope, err)
			}
		})
	}
}

// newWorkspaceClient is another Client of x's workspace and bot.
func newWorkspaceClient(x rooms) *Client {
	c, _ := New(ports.ChatConfig{Address: x.ws.URL(), Token: x.botToken, AppToken: x.appToken})
	return c
}

// TestCredentialRefused covers #155: a token Slack doesn't know is
// ports.ErrChatCredentialRefused, for Sync too.
func TestCredentialRefused(t *testing.T) {
	w := newWorkspace(t)
	refused := client(t, w.ws, "x"+w.botToken, w.appToken)
	if _, err := refused.Sync(t.Context(), "", 0); !errors.Is(err, ports.ErrChatCredentialRefused) {
		t.Errorf("Sync() with a bot token Slack doesn't know = %v, want ErrChatCredentialRefused", err)
	}
	badApp := client(t, w.ws, w.botToken, "x"+w.appToken)
	if _, err := badApp.Sync(t.Context(), "", 0); !errors.Is(err, ports.ErrChatCredentialRefused) {
		t.Errorf("Sync() with an app-level token Slack doesn't know = %v, want ErrChatCredentialRefused", err)
	}
	if _, err := New(ports.ChatConfig{Address: w.ws.URL()}); !errors.Is(err, ports.ErrChatCredentialRefused) {
		t.Errorf("New() without a bot token = %v, want ErrChatCredentialRefused", err)
	}
}

// TestReactNeedsAName covers #155: a key the adapter has no emoji name for
// is an error, never a guess, and reaches no Slack call.
func TestReactNeedsAName(t *testing.T) {
	x := newRooms(t)
	id, err := x.c.Send(t.Context(), x.specs, ports.ChatMessage{Text: "Spec"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.c.React(t.Context(), x.specs, id, "🦄"); err == nil {
		t.Error("React() with a key without a name = nil error")
	}
	if n := x.ws.Calls("reactions.add"); n != 0 {
		t.Errorf("reactions.add was called %d times, want none", n)
	}
	for range 2 {
		if _, err := x.c.React(t.Context(), x.specs, id, model.ChatDoneReaction); err != nil {
			t.Errorf("React(✅) = %v; a reaction already there is no error", err)
		}
	}
	ch, _ := x.ws.Channel(x.specs)
	if got := ch.Messages[0].Reactions; !slices.Equal(got, []string{"white_check_mark"}) {
		t.Errorf("the reactions = %q, want white_check_mark once", got)
	}
}

// TestPostIsMrkdwn covers #155: a post's plain text goes out escaped as
// the message's text, and its mrkdwn as section blocks, cut at line
// breaks where it is longer than a block takes; an edit without mrkdwn
// leaves no blocks.
func TestPostIsMrkdwn(t *testing.T) {
	x := newRooms(t)
	post := model.ChatPost{Title: "Spec: a < b", Body: []string{strings.Repeat("word ", 500),
		strings.Repeat("more ", 500)}, Links: []model.ChatLink{{Text: "UI", URL: "https://ui.example/?a=1&b=2"}}}
	id, err := x.c.Send(t.Context(), x.specs, ports.ChatMessage{Text: post.Text(), Formatted: post.Mrkdwn()})
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := x.ws.Channel(x.specs)
	m := ch.Messages[0]
	if Decode(m.Text) != post.Text() || !strings.Contains(m.Text, "a &lt; b") {
		t.Errorf("the message's text = %q, want the plain text escaped", m.Text)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text struct {
			Type, Text string
		} `json:"text"`
	}
	if err := json.Unmarshal([]byte(m.Blocks), &blocks); err != nil {
		t.Fatalf("the blocks %q: %v", m.Blocks, err)
	}
	joined := make([]string, 0, len(blocks))
	for _, b := range blocks {
		joined = append(joined, b.Text.Text)
	}
	if len(blocks) < 2 || strings.Join(joined, "\n") != post.Mrkdwn() {
		t.Errorf("the blocks = %d, holding %q; want the mrkdwn cut in sections", len(blocks), joined)
	}
	if _, err := x.c.Edit(t.Context(), x.specs, id, ports.ChatMessage{Text: "Spec: merged"}); err != nil {
		t.Fatal(err)
	}
	ch, _ = x.ws.Channel(x.specs)
	if m := ch.Messages[0]; m.Blocks != "" || m.Text != "Spec: merged" || m.Edited == "" {
		t.Errorf("the message after an edit without mrkdwn = %+v, want its text alone", m)
	}
}

// TestRateLimited covers #155: a 429 is made again after the wait Slack
// asks for.
func TestRateLimited(t *testing.T) {
	w := newWorkspace(t, WithRetries(3, time.Second))
	w.ws.RateLimit(2)
	if _, err := w.c.WhoAmI(t.Context()); err != nil {
		t.Fatalf("WhoAmI() after two 429s = %v", err)
	}
	if n := w.ws.Calls("auth.test"); n != 3 {
		t.Errorf("auth.test was called %d times, want 3", n)
	}
}
