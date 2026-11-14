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

package matrix

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/adapters/matrix/matrixtest"
	"github.com/JairJosafath/theos/internal/ports"
)

// bot is a Client of a fresh fake homeserver as its bot, @theos.
func bot(t *testing.T, opts ...Option) (*Client, *matrixtest.Server, string) {
	t.Helper()
	hs := matrixtest.New(t)
	me, token := hs.NewUser("theos")
	c, err := New(ports.ChatConfig{Address: hs.URL(), Token: token}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c, hs, me
}

// space makes the Space theos-shop and its rooms general and alerts.
func space(t *testing.T, c *Client) (ports.ChatRoom, []ports.ChatRoom) {
	t.Helper()
	ctx := context.Background()
	sp, err := c.EnsureRoom(ctx, ports.ChatRoomSpec{Alias: "theos-shop", Name: "Shop", Topic: "The shop", Space: true})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"general", "alerts"}
	rooms := make([]ports.ChatRoom, 0, len(names))
	for _, name := range names {
		r, err := c.EnsureRoom(ctx, ports.ChatRoomSpec{Alias: "theos-shop-" + name, Name: name, Topic: name + "!",
			Parent: sp.ID})
		if err != nil {
			t.Fatal(err)
		}
		rooms = append(rooms, r)
	}
	return sp, rooms
}

// A Space and its rooms are made private, invite only and unencrypted,
// linked both ways, with their aliases; a second pass finds them by ID or
// by alias and makes nothing.
func TestEnsureRoomMakesSpaceAndRoomsOnce(t *testing.T) {
	c, hs, me := bot(t)
	ctx := context.Background()
	sp, rooms := space(t, c)
	if !sp.Created || sp.Alias != "#theos-shop:"+matrixtest.ServerName {
		t.Errorf("Space = %+v", sp)
	}
	got, _ := hs.Room(sp.ID)
	if !got.Space || got.Name != "Shop" || got.Topic != "The shop" || got.JoinRule != "invite" || got.Encrypted ||
		got.Creator != me {
		t.Errorf("Space on the homeserver = %+v", got)
	}
	if want := []string{rooms[0].ID, rooms[1].ID}; !reflect.DeepEqual(got.Children, want) {
		t.Errorf("Space children = %v, want %v", got.Children, want)
	}
	for i, name := range []string{"general", "alerts"} {
		r, _ := hs.Room(rooms[i].ID)
		if r.Space || r.Name != name || r.Topic != name+"!" || r.JoinRule != "invite" || r.Encrypted ||
			!reflect.DeepEqual(r.Parents, []string{sp.ID}) || r.Alias != "#theos-shop-"+name+":"+matrixtest.ServerName {
			t.Errorf("room %s = %+v", name, r)
		}
		if !rooms[i].Created || rooms[i].Alias != r.Alias {
			t.Errorf("EnsureRoom(%s) = %+v", name, rooms[i])
		}
	}
	made, puts := hs.Calls("createRoom"), hs.Calls("state.put")
	if made != 3 {
		t.Errorf("made %d rooms, want 3", made)
	}

	// Again, by ID and by alias alone: nothing is made or written.
	again, err := c.EnsureRoom(ctx, ports.ChatRoomSpec{ID: rooms[0].ID, Alias: "theos-shop-general", Name: "general",
		Parent: sp.ID})
	if err != nil || again != (ports.ChatRoom{ID: rooms[0].ID, Alias: rooms[0].Alias}) {
		t.Errorf("EnsureRoom by ID = %+v, %v", again, err)
	}
	byAlias, err := c.EnsureRoom(ctx, ports.ChatRoomSpec{Alias: "theos-shop-alerts", Name: "alerts", Parent: sp.ID})
	if err != nil || byAlias != (ports.ChatRoom{ID: rooms[1].ID, Alias: rooms[1].Alias}) {
		t.Errorf("EnsureRoom by alias = %+v, %v", byAlias, err)
	}
	if hs.Calls("createRoom") != made || hs.Calls("state.put") != puts {
		t.Errorf("a second pass made %d rooms and wrote %d state events", hs.Calls("createRoom")-made,
			hs.Calls("state.put")-puts)
	}

	// A new name is set; a missing link is made again.
	if _, err := c.EnsureRoom(ctx, ports.ChatRoomSpec{ID: rooms[0].ID, Name: "lobby", Parent: sp.ID}); err != nil {
		t.Fatal(err)
	}
	if r, _ := hs.Room(rooms[0].ID); r.Name != "lobby" {
		t.Errorf("renamed room = %q", r.Name)
	}
	if hs.Calls("createRoom") != made {
		t.Error("a rename made a room")
	}
}

// An alias held by a room the bot is not in is never taken over.
func TestEnsureRoomAliasTaken(t *testing.T) {
	c, hs, _ := bot(t)
	stranger, _ := hs.NewUser("mallory")
	hs.CreateRoomAs(stranger, "theos-shop-general")
	_, err := c.EnsureRoom(context.Background(), ports.ChatRoomSpec{Alias: "theos-shop-general", Name: "general"})
	taken, ok := errors.AsType[*ports.ChatAliasTakenError](err)
	if !errors.Is(err, ports.ErrChatAliasTaken) || !ok ||
		taken.Alias != "#theos-shop-general:"+matrixtest.ServerName {
		t.Errorf("EnsureRoom on a taken alias = %v, want ErrChatAliasTaken naming it", err)
	}

	// A room the bot was made to leave is not joined again by its ID.
	c, hs, _ = bot(t)
	sp, _ := space(t, c)
	me, _ := c.WhoAmI(context.Background())
	hs.Leave(sp.ID, me)
	if _, err := c.EnsureRoom(context.Background(), ports.ChatRoomSpec{ID: sp.ID, Alias: "theos-shop", Space: true}); !errors.Is(err, ports.ErrChatAliasTaken) {
		t.Errorf("EnsureRoom of a room the bot left = %v, want ErrChatAliasTaken", err)
	}
}

func TestInvite(t *testing.T) {
	c, hs, _ := bot(t)
	ctx := context.Background()
	sp, _ := space(t, c)
	ada, _ := hs.NewUser("ada")
	for range 2 {
		if err := c.Invite(ctx, sp.ID, ada); err != nil {
			t.Fatalf("Invite = %v", err)
		}
	}
	hs.Join(sp.ID, ada)
	if err := c.Invite(ctx, sp.ID, ada); err != nil {
		t.Errorf("Invite of a member = %v", err)
	}
	if r, _ := hs.Room(sp.ID); r.Members[ada] != "join" {
		t.Errorf("members = %v", r.Members)
	}
	if hs.Calls("invite") != 1 {
		t.Errorf("invited %d times, want 1", hs.Calls("invite"))
	}
	other := hs.CreateRoomAs(ada, "")
	if err := c.Invite(ctx, other, ada); err == nil || errors.Is(err, ports.ErrChatInviteRefused) {
		t.Errorf("Invite into a room the bot is not in = %v, want an error that is not the person's", err)
	}
	// #251: a user of the server it has no account for is refused, theirs
	// alone, with the homeserver's code.
	ghost := "@ghost:" + matrixtest.ServerName
	err := c.Invite(ctx, sp.ID, ghost)
	if refused, ok := errors.AsType[*ports.ChatInviteRefusedError](err); !ok || refused.User != ghost ||
		refused.Code != "M_NOT_FOUND" {
		t.Errorf("Invite of %s = %v, want a ChatInviteRefusedError naming them and M_NOT_FOUND", ghost, err)
	}
}

// TestInviteRefusedOrUnreachable covers #251: which homeserver answers to
// an invite refuse the person alone (a ChatInviteRefusedError), and which
// say the host could not be reached or took no token.
func TestInviteRefusedOrUnreachable(t *testing.T) {
	for _, tt := range []struct {
		status       int
		code         string
		refused      bool
		tokenRefused bool
	}{
		{status: http.StatusNotFound, code: "M_NOT_FOUND", refused: true},
		{status: http.StatusBadRequest, code: "M_INVALID_PARAM", refused: true},
		{status: http.StatusNotFound, code: "M_UNRECOGNIZED"},
		{status: http.StatusForbidden, code: "M_FORBIDDEN"},
		{status: http.StatusTooManyRequests, code: "M_LIMIT_EXCEEDED"},
		{status: http.StatusInternalServerError, code: "M_UNKNOWN"},
		{status: http.StatusBadGateway},
		{status: http.StatusUnauthorized, code: "M_UNKNOWN_TOKEN", tokenRefused: true},
	} {
		t.Run(fmt.Sprintf("%d %s", tt.status, tt.code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_ = json.NewEncoder(w).Encode(map[string]string{"errcode": tt.code, "error": "no"})
			}))
			defer srv.Close()
			c, err := New(ports.ChatConfig{Address: srv.URL, Token: rand.Text()}, WithRetries(0, 0))
			if err != nil {
				t.Fatal(err)
			}
			err = c.Invite(t.Context(), "!room:"+matrixtest.ServerName, "@ghost:"+matrixtest.ServerName)
			refused, ok := errors.AsType[*ports.ChatInviteRefusedError](err)
			switch {
			case err == nil:
				t.Fatal("Invite = nil, want an error")
			case ok != tt.refused:
				t.Errorf("Invite = %v, refused the person %v, want %v", err, ok, tt.refused)
			case ok && (refused.Code != tt.code || refused.User != "@ghost:"+matrixtest.ServerName):
				t.Errorf("Invite = %+v, want the person and %s", refused, tt.code)
			case errors.Is(err, ports.ErrChatCredentialRefused) != tt.tokenRefused:
				t.Errorf("Invite = %v, token refused %v, want %v", err, !tt.tokenRefused, tt.tokenRefused)
			}
		})
	}
}

// Send, Reply, Edit and React write the events Matrix clients read: a
// notice with its HTML, a thread reply, a replacement and an annotation.
func TestSendThreadEditReact(t *testing.T) {
	c, hs, me := bot(t)
	ctx := context.Background()
	_, rooms := space(t, c)
	room := rooms[0].ID
	msg := ports.ChatMessage{Text: "Spec: dark mode", Formatted: "<p><b>Spec: dark mode</b></p>"}
	root, err := c.Send(ctx, room, msg)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := c.Reply(ctx, room, root, ports.ChatMessage{Text: "Approved by ada"})
	if err != nil {
		t.Fatal(err)
	}
	edit, err := c.Edit(ctx, room, root, ports.ChatMessage{Text: "Spec: dark mode (merged)", Formatted: "<p>merged</p>"})
	if err != nil {
		t.Fatal(err)
	}
	react, err := c.React(ctx, room, root, "✅")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := hs.Room(room)
	byID := map[string]matrixtest.Event{}
	for _, e := range r.Events {
		byID[e.ID] = e
	}
	want := map[string]matrixtest.Event{
		root: {ID: root, Type: "m.room.message", Sender: me, Content: map[string]any{"msgtype": "m.notice",
			"body": "Spec: dark mode", "format": "org.matrix.custom.html", "formatted_body": "<p><b>Spec: dark mode</b></p>"}},
		reply: {ID: reply, Type: "m.room.message", Sender: me, Content: map[string]any{"msgtype": "m.notice",
			"body": "Approved by ada", "m.relates_to": map[string]any{"rel_type": "m.thread", "event_id": root,
				"is_falling_back": true, "m.in_reply_to": map[string]any{"event_id": root}}}},
		edit: {ID: edit, Type: "m.room.message", Sender: me, Content: map[string]any{"msgtype": "m.notice",
			"body": "* Spec: dark mode (merged)", "format": "org.matrix.custom.html", "formatted_body": "* <p>merged</p>",
			"m.new_content": map[string]any{"msgtype": "m.notice", "body": "Spec: dark mode (merged)",
				"format": "org.matrix.custom.html", "formatted_body": "<p>merged</p>"},
			"m.relates_to": map[string]any{"rel_type": "m.replace", "event_id": root}}},
		react: {ID: react, Type: "m.reaction", Sender: me, Content: map[string]any{
			"m.relates_to": map[string]any{"rel_type": "m.annotation", "event_id": root, "key": "✅"}}},
	}
	for id, w := range want {
		if !reflect.DeepEqual(byID[id], w) {
			t.Errorf("event %s =\n%+v\nwant\n%+v", id, byID[id], w)
		}
	}
	if hs.Calls("send.m.room.message") != 3 || hs.Calls("send.m.reaction") != 1 {
		t.Errorf("sent %d messages and %d reactions", hs.Calls("send.m.room.message"), hs.Calls("send.m.reaction"))
	}
}

// A sync returns the people's messages since its token, oldest first, with
// thread roots and edits, and none of the bot's own; the next sync starts
// where it left off.
func TestSyncSince(t *testing.T) {
	c, hs, _ := bot(t)
	ctx := context.Background()
	_, rooms := space(t, c)
	general, alerts := rooms[0].ID, rooms[1].ID
	ada, _ := hs.NewUser("ada")
	hs.Join(general, ada)
	hs.Join(alerts, ada)
	root, _ := c.Send(ctx, alerts, ports.ChatMessage{Text: "api failed"})
	first := hs.Post(general, ada, "status", "")
	retry := hs.Post(alerts, ada, "retry", root)

	s1, err := c.Sync(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.ChatEvent{{Room: general, ID: first, Sender: ada, Text: "status"},
		{Room: alerts, ID: retry, Sender: ada, Text: "retry", ThreadRoot: root}}
	if got := withoutTime(s1.Events); !reflect.DeepEqual(got, want) || s1.Next == "" {
		t.Errorf("first sync = %+v, next %q; want %+v", got, s1.Next, want)
	}

	// Since the first: only what came after, an edit marked as one.
	_, _ = c.Send(ctx, general, ports.ChatMessage{Text: "the bot's own"})
	later := hs.Post(general, ada, "pause shop", "")
	s2, err := c.Sync(ctx, s1.Next, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := withoutTime(s2.Events); !reflect.DeepEqual(got, []ports.ChatEvent{{Room: general, ID: later, Sender: ada,
		Text: "pause shop"}}) || s2.Next == s1.Next {
		t.Errorf("second sync = %+v, next %q", got, s2.Next)
	}

	// Nothing new: a sync waits, then returns nothing and the same token.
	start := time.Now()
	s3, err := c.Sync(ctx, s2.Next, 50*time.Millisecond)
	if err != nil || len(s3.Events) != 0 || s3.Next != s2.Next || time.Since(start) < 50*time.Millisecond {
		t.Errorf("idle sync = %+v, %v after %s", s3, err, time.Since(start))
	}
}

// An edit is read as its new text and the event it replaces.
func TestSyncReadsEdits(t *testing.T) {
	c, hs, _ := bot(t)
	ctx := context.Background()
	_, rooms := space(t, c)
	ada, token := hs.NewUser("ada")
	hs.Join(rooms[0].ID, ada)
	person, err := New(ports.ChatConfig{Address: hs.URL(), Token: token})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := person.Send(ctx, rooms[0].ID, ports.ChatMessage{Text: "pause shp"})
	edit, _ := person.Edit(ctx, rooms[0].ID, id, ports.ChatMessage{Text: "pause shop"})
	s, err := c.Sync(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.ChatEvent{{Room: rooms[0].ID, ID: id, Sender: ada, Text: "pause shp"},
		{Room: rooms[0].ID, ID: edit, Sender: ada, Text: "pause shop", Replaces: id}}
	if got := withoutTime(s.Events); !reflect.DeepEqual(got, want) {
		t.Errorf("sync = %+v, want %+v", got, want)
	}
}

func withoutTime(events []ports.ChatEvent) []ports.ChatEvent {
	out := make([]ports.ChatEvent, 0, len(events))
	for _, e := range events {
		if e.Time.IsZero() {
			return nil
		}
		e.Time = time.Time{}
		out = append(out, e)
	}
	return out
}

// A 429 is waited out for as long as the homeserver asks, and the call made
// again with the same transaction ID, so a message is posted once; past the
// retries it is an error.
func TestRateLimitRetried(t *testing.T) {
	c, hs, _ := bot(t)
	ctx := context.Background()
	_, rooms := space(t, c)
	hs.RateLimit(2, 30)
	start := time.Now()
	id, err := c.Send(ctx, rooms[0].ID, ports.ChatMessage{Text: "hello"})
	if err != nil || id == "" {
		t.Fatalf("Send after two 429s = %q, %v", id, err)
	}
	if waited := time.Since(start); waited < 60*time.Millisecond {
		t.Errorf("waited %s for two 429s of 30ms", waited)
	}
	if hs.Calls("send.m.room.message") != 1 {
		t.Errorf("posted %d messages, want 1", hs.Calls("send.m.room.message"))
	}

	c2, hs2, _ := bot(t, WithRetries(1, time.Second))
	hs2.RateLimit(5, 1)
	_, err = c2.WhoAmI(ctx)
	if e, ok := errors.AsType[*Error](err); !ok || e.Status != 429 || e.Code != "M_LIMIT_EXCEEDED" {
		t.Errorf("WhoAmI past the retries = %v", err)
	}

	// A wait longer than the context's is cut short.
	hs2.RateLimit(1, 60_000)
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := c2.WhoAmI(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("WhoAmI with a short context = %v", err)
	}
}

func TestCredentialRefused(t *testing.T) {
	hs := matrixtest.New(t)
	unknown := rand.Text()
	c, err := New(ports.ChatConfig{Address: hs.URL(), Token: unknown})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WhoAmI(context.Background()); !errors.Is(err, ports.ErrChatCredentialRefused) {
		t.Errorf("WhoAmI with an unknown token = %v", err)
	}
	for _, cfg := range []ports.ChatConfig{{Address: "ftp://matrix.example", Token: unknown},
		{Address: "https://", Token: unknown}, {Address: "https://matrix.example?x=1", Token: unknown},
		{Address: "https://matrix.example"}} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) took it", cfg)
		}
	}
	if _, err := Hosts(nil)(ports.ChatConfig{Address: hs.URL(), Token: unknown}); err != nil {
		t.Errorf("Hosts(nil) = %v", err)
	}
}
