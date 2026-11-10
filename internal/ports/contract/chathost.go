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

package contract

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// ChatHostEnv builds the ChatHost under test.
type ChatHostEnv struct {
	// New returns a fresh chat host (a homeserver, a workspace) that holds
	// no room, with the ChatHost that speaks as its bot.
	New func(t *testing.T) ChatServer
	// Cannot names the subtests the host can't run, with why; they are
	// skipped.
	Cannot map[string]string
}

// ChatServer is a chat host as the suite reaches it: through the port as
// its bot, and beside the port as its people and as the host itself.
type ChatServer struct {
	// Host speaks as the bot.
	Host ports.ChatHost
	// Refused is a ChatHost of the same host with a credential the host
	// doesn't know.
	Refused ports.ChatHost
	// Person makes a person's account and returns their user ID, as Invite
	// takes it and a ChatEvent names its sender.
	Person func(t *testing.T, name string) string
	// Join makes user a member of room, as accepting an invite does.
	Join func(t *testing.T, room, user string)
	// Post posts text in room as user, in the thread whose root is
	// threadRoot when it isn't "", and returns the message's ID. It returns
	// only once the host would deliver the message to a Sync: the suite
	// reads it with the next one, without waiting.
	Post func(t *testing.T, room, user, text, threadRoot string) string
	// Amend replaces the text of user's message with text, as a person's
	// edit does, and returns the edit's ID as a Sync names it: the edited
	// message's own on a host whose edits have no ID of their own. Like
	// Post, it returns only once the host would deliver the edit to a Sync.
	Amend func(t *testing.T, room, user, message, text string) string
	// Squat makes a room the bot is not in that holds alias, as someone
	// else on the host would.
	Squat func(t *testing.T, alias string)
	// Remove takes the bot out of room, as a person who removes it does.
	Remove func(t *testing.T, room string)
	// Room is room as the host shows it to the people in it.
	Room func(t *testing.T, room string) ChatRoomView
}

// ChatRoomView is a room as its host shows it.
type ChatRoomView struct {
	// Name is the room's name, and Topic its topic.
	Name  string
	Topic string
	// Private says only the people invited can enter the room and read it.
	Private bool
	// Members are the users in the room and those invited to it.
	Members []string
	// Posts are the room's messages, oldest first; an edit is no post of
	// its own.
	Posts []ChatPosted
}

// ChatPosted is a message as its host shows it.
type ChatPosted struct {
	// ID and Sender say which message and whose.
	ID     string
	Sender string
	// Text is its plain text as shown now: its last edit's.
	Text string
	// ThreadRoot is the message it answers in a thread, "" outside one.
	ThreadRoot string
	// Reactions are the reaction keys on it, as React was given them.
	Reactions []string
}

// The names the ChatHost suite uses more than once.
const (
	chatSpace    = "contract-shop"
	chatGeneral  = "contract-shop-general"
	chatAlerts   = "contract-shop-alerts"
	nameGeneral  = "general"
	nameAlerts   = "alerts"
	topicGeneral = "Anything"
	personAda    = "ada"
	reactedDone  = "✅"
	// The bot's two messages.
	textDarkMode = "Spec: dark mode"
	textExport   = "Spec: export"
)

// ChatHost states what the Chat controller and theos-chat rely on of every
// ChatHost: WhoAmI is the bot's user ID, and a credential the host doesn't
// know is ErrChatCredentialRefused; EnsureRoom makes a private room once
// and finds it again by its ID, then by its alias; a room has the name
// asked for, when it is made and when it is found; a room made has its
// topic; a Space and the rooms in it are each a room of their own; an alias
// held by a room the bot is not in, one it was taken out of included, is a
// ChatAliasTakenError that names it; Invite is no error however often it is
// asked; Send, Reply, Edit and React post as the bot, in the thread, in
// place and on the message named; and Sync returns what people said since
// its token, each message once, a room's oldest first, with its room,
// sender, thread and the message an edit replaces, and nothing the bot
// said, and with a wait of 0 it returns at once when nothing is new.
//
// A room's name, its topic and a Space are asserted only in the subtests
// named for them, and no other subtest makes a Space or gives a topic: a
// host without one of the three names that subtest in Cannot and runs the
// rest.
//
// What a host does its own way stays in its adapter's tests: how a Space
// and its rooms are linked, the HTML and the events written, rate limits,
// and how long a Sync with a wait above 0 waits.
func ChatHost(t *testing.T, env ChatHostEnv) {
	t.Helper()
	ctx, cancel := suiteContext()
	defer cancel()
	runSubtests(t, chatSuite{ctx: ctx, env: env}, env.Cannot, []subtest[chatSuite]{
		{"says who the bot is, and that a credential is refused", chatSuite.whoAmI},
		{"makes a private room once, and finds it by its ID, then its alias", chatSuite.ensuresRoom},
		{"brings a room's name to the one asked for", chatSuite.names},
		{"gives a room it makes its topic", chatSuite.topics},
		{"makes a Space and the rooms in it, each a room of its own", chatSuite.spaces},
		{"refuses an alias held by a room the bot is not in", chatSuite.aliasTaken},
		{"does not go back into a room the bot was taken out of", chatSuite.removed},
		{"invites a person, however often it is asked", chatSuite.invites},
		{"posts as the bot", chatSuite.sends},
		{"replies in the thread of the bot's message and of a person's", chatSuite.replies},
		{"edits its message in place", chatSuite.edits},
		{"puts a reaction on a message", chatSuite.reacts},
		{"reads what people said since a token, each message once and none of the bot's", chatSuite.syncs},
		{"reads a person's edit as the message it replaces", chatSuite.syncsEdits},
		{"reads nothing, at once, when nothing is new", chatSuite.syncsIdle},
	})
}

// chatSuite holds what every subtest of ChatHost shares.
type chatSuite struct {
	ctx context.Context
	env ChatHostEnv
}

// chatRooms is a fresh host with the rooms general and alerts, and ada in
// both.
type chatRooms struct {
	ChatServer
	bot, ada        string
	general, alerts string
}

// room makes the room spec describes, which EnsureRoom must report as made.
func (s chatSuite) room(t *testing.T, srv ChatServer, spec ports.ChatRoomSpec) ports.ChatRoom {
	t.Helper()
	room := must(srv.Host.EnsureRoom(s.ctx, spec))(t)
	if room.ID == "" || !room.Created || !strings.Contains(room.Alias, spec.Alias) {
		t.Fatalf("EnsureRoom(%s) on a host without it = %+v, want a room made, with its ID and alias", spec.Alias,
			room)
	}
	return room
}

// rooms makes a fresh host's two rooms and puts ada in them. The rooms are
// in no Space and have no topic: a host without either still runs every
// subtest that starts here.
func (s chatSuite) rooms(t *testing.T) chatRooms {
	t.Helper()
	x := chatRooms{ChatServer: s.env.New(t)}
	x.bot = must(x.Host.WhoAmI(s.ctx))(t)
	x.general = s.room(t, x.ChatServer, ports.ChatRoomSpec{Alias: chatGeneral, Name: nameGeneral}).ID
	x.alerts = s.room(t, x.ChatServer, ports.ChatRoomSpec{Alias: chatAlerts, Name: nameAlerts}).ID
	x.ada = x.Person(t, personAda)
	for _, room := range []string{x.general, x.alerts} {
		if err := x.Host.Invite(s.ctx, room, x.ada); err != nil {
			t.Fatalf("Invite() = %v", err)
		}
		x.Join(t, room, x.ada)
	}
	return x
}

// send posts text in room as the bot.
func (s chatSuite) send(t *testing.T, x chatRooms, room, text string) string {
	t.Helper()
	id := must(x.Host.Send(s.ctx, room, ports.ChatMessage{Text: text, HTML: "<p>" + text + "</p>"}))(t)
	if id == "" {
		t.Fatalf("Send(%q) returned no ID", text)
	}
	return id
}

// posted is the message id as room shows it; it fails t when the room
// shows none, or more than one.
func posted(t *testing.T, x chatRooms, room, id string) ChatPosted {
	t.Helper()
	var found []ChatPosted
	for _, p := range x.Room(t, room).Posts {
		if p.ID == id {
			found = append(found, p)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the room shows message %s %d times, want once", id, len(found))
	}
	return found[0]
}

func (s chatSuite) whoAmI(t *testing.T) {
	srv := s.env.New(t)
	bot := must(srv.Host.WhoAmI(s.ctx))(t)
	if again := must(srv.Host.WhoAmI(s.ctx))(t); bot == "" || again != bot {
		t.Errorf("WhoAmI() = %q, then %q; want the bot's one user ID", bot, again)
	}
	if _, err := srv.Refused.WhoAmI(s.ctx); !errors.Is(err, ports.ErrChatCredentialRefused) {
		t.Errorf("WhoAmI() with a credential the host doesn't know = %v, want ErrChatCredentialRefused", err)
	}
	if _, err := srv.Refused.EnsureRoom(s.ctx, ports.ChatRoomSpec{Alias: chatGeneral,
		Name: nameGeneral}); !errors.Is(err, ports.ErrChatCredentialRefused) {
		t.Errorf("EnsureRoom() with a credential the host doesn't know = %v, want ErrChatCredentialRefused", err)
	}
}

func (s chatSuite) ensuresRoom(t *testing.T) {
	srv := s.env.New(t)
	bot := must(srv.Host.WhoAmI(s.ctx))(t)
	spec := ports.ChatRoomSpec{Alias: chatGeneral, Name: nameGeneral}
	made := s.room(t, srv, spec)
	if got := srv.Room(t, made.ID); !got.Private || !slices.Equal(got.Members, []string{bot}) {
		t.Errorf("the room made = %+v, want it private, with the bot %s alone", got, bot)
	}

	withID := spec
	withID.ID = made.ID
	s.found(t, srv, "its ID and alias", withID, made)
	s.found(t, srv, "its ID alone", ports.ChatRoomSpec{ID: made.ID, Name: nameGeneral}, made)
	s.found(t, srv, "its alias alone", spec, made)
	// An ID the host has no room for, as a recorded one that was lost.
	lost := spec
	lost.ID = made.ID + "0"
	s.found(t, srv, "its alias, with an ID the host doesn't have", lost, made)
}

// found fails t unless EnsureRoom finds the room made for spec, and makes
// none; how says what of spec it is found by.
func (s chatSuite) found(t *testing.T, srv ChatServer, how string, spec ports.ChatRoomSpec, made ports.ChatRoom) {
	t.Helper()
	got, err := srv.Host.EnsureRoom(s.ctx, spec)
	if err != nil || got.ID != made.ID || got.Created {
		t.Errorf("EnsureRoom() by %s = %+v, %v; want room %s found, not made", how, got, err, made.ID)
	}
	if spec.Alias != "" && got.Alias != made.Alias {
		t.Errorf("EnsureRoom() by %s gave alias %q, want %q", how, got.Alias, made.Alias)
	}
}

func (s chatSuite) names(t *testing.T) {
	srv := s.env.New(t)
	spec := ports.ChatRoomSpec{Alias: chatGeneral, Name: nameGeneral}
	made := s.room(t, srv, spec)
	if got := srv.Room(t, made.ID).Name; got != nameGeneral {
		t.Errorf("the name of the room made = %q, want %q", got, nameGeneral)
	}
	for _, renamed := range []ports.ChatRoomSpec{{ID: made.ID, Alias: chatGeneral, Name: "lobby"},
		{Alias: chatGeneral, Name: "foyer"}} {
		how := "its alias"
		if renamed.ID != "" {
			how = "its ID"
		}
		s.found(t, srv, how+", with a new name", renamed, made)
		if got := srv.Room(t, made.ID).Name; got != renamed.Name {
			t.Errorf("the room's name after EnsureRoom() by %s with a new one = %q, want %q", how, got,
				renamed.Name)
		}
	}
}

func (s chatSuite) topics(t *testing.T) {
	srv := s.env.New(t)
	made := s.room(t, srv, ports.ChatRoomSpec{Alias: chatGeneral, Name: nameGeneral, Topic: topicGeneral})
	if got := srv.Room(t, made.ID).Topic; got != topicGeneral {
		t.Errorf("the topic of the room made = %q, want %q", got, topicGeneral)
	}
}

// spaces makes a Space and its rooms as the Chat controller does: the Space
// first, then each room with the Space's ID as its Parent.
func (s chatSuite) spaces(t *testing.T) {
	srv := s.env.New(t)
	bot := must(srv.Host.WhoAmI(s.ctx))(t)
	specs := []ports.ChatRoomSpec{{Alias: chatSpace, Name: "Shop", Space: true},
		{Alias: chatGeneral, Name: nameGeneral}, {Alias: chatAlerts, Name: nameAlerts}}
	made := make([]ports.ChatRoom, len(specs))
	for i := range specs {
		if !specs[i].Space {
			specs[i].Parent = made[0].ID
		}
		made[i] = s.room(t, srv, specs[i])
		if got := srv.Room(t, made[i].ID); !got.Private || !slices.Equal(got.Members, []string{bot}) {
			t.Errorf("%s as made = %+v, want it private, with the bot %s alone", specs[i].Alias, got, bot)
		}
	}
	if made[0].ID == made[1].ID || made[0].ID == made[2].ID || made[1].ID == made[2].ID {
		t.Fatalf("EnsureRoom() made the Space %s and its rooms %s and %s, want three rooms", made[0].ID,
			made[1].ID, made[2].ID)
	}
	for i, spec := range specs {
		s.found(t, srv, "the alias of "+spec.Alias, spec, made[i])
		spec.ID = made[i].ID
		s.found(t, srv, "the ID of "+spec.Alias, spec, made[i])
	}
}

// wantAliasTaken fails t unless err is a ChatAliasTakenError naming alias.
func wantAliasTaken(t *testing.T, err error, alias string) {
	t.Helper()
	taken, ok := errors.AsType[*ports.ChatAliasTakenError](err)
	if !errors.Is(err, ports.ErrChatAliasTaken) || !ok || !strings.Contains(taken.Alias, alias) {
		t.Errorf("EnsureRoom() = %v, want a ChatAliasTakenError naming %s", err, alias)
	}
}

func (s chatSuite) aliasTaken(t *testing.T) {
	srv := s.env.New(t)
	srv.Squat(t, chatGeneral)
	_, err := srv.Host.EnsureRoom(s.ctx, ports.ChatRoomSpec{Alias: chatGeneral, Name: nameGeneral})
	wantAliasTaken(t, err, chatGeneral)
}

func (s chatSuite) removed(t *testing.T) {
	srv := s.env.New(t)
	spec := ports.ChatRoomSpec{Alias: chatGeneral, Name: nameGeneral}
	spec.ID = s.room(t, srv, spec).ID
	srv.Remove(t, spec.ID)
	_, err := srv.Host.EnsureRoom(s.ctx, spec)
	wantAliasTaken(t, err, chatGeneral)
}

func (s chatSuite) invites(t *testing.T) {
	srv := s.env.New(t)
	bot := must(srv.Host.WhoAmI(s.ctx))(t)
	room := s.room(t, srv, ports.ChatRoomSpec{Alias: chatGeneral, Name: nameGeneral}).ID
	ada := srv.Person(t, personAda)
	wantMembers := func(when string) {
		t.Helper()
		if got := srv.Room(t, room).Members; !slices.Equal(sorted(got), sorted([]string{bot, ada})) {
			t.Errorf("the room's members %s = %q, want the bot and %s, once each", when, got, ada)
		}
	}
	for range 2 {
		if err := srv.Host.Invite(s.ctx, room, ada); err != nil {
			t.Fatalf("Invite() = %v", err)
		}
	}
	wantMembers("after two invites")
	srv.Join(t, room, ada)
	if err := srv.Host.Invite(s.ctx, room, ada); err != nil {
		t.Errorf("Invite() of someone in the room = %v", err)
	}
	wantMembers("after an invite of someone in it")
}

func (s chatSuite) sends(t *testing.T) {
	x := s.rooms(t)
	first := s.send(t, x, x.general, textDarkMode)
	second := s.send(t, x, x.general, textExport)
	if first == second {
		t.Fatalf("Send() gave two messages the ID %s", first)
	}
	want := []ChatPosted{{ID: first, Sender: x.bot, Text: textDarkMode},
		{ID: second, Sender: x.bot, Text: textExport}}
	if got := x.Room(t, x.general).Posts; !slices.EqualFunc(got, want, samePosted) {
		t.Errorf("the room shows %+v, want %+v", got, want)
	}
	if got := x.Room(t, x.alerts).Posts; len(got) != 0 {
		t.Errorf("another room shows %+v, want nothing", got)
	}
}

// samePosted is whether a and b are one message shown the same way.
func samePosted(a, b ChatPosted) bool {
	return a.ID == b.ID && a.Sender == b.Sender && a.Text == b.Text && a.ThreadRoot == b.ThreadRoot &&
		slices.Equal(a.Reactions, b.Reactions)
}

func (s chatSuite) replies(t *testing.T) {
	x := s.rooms(t)
	root := s.send(t, x, x.general, textDarkMode)
	said := x.Post(t, x.general, x.ada, "status", "")
	for _, to := range []string{root, said} {
		id := must(x.Host.Reply(s.ctx, x.general, to, ports.ChatMessage{Text: "Nothing done"}))(t)
		if got := posted(t, x, x.general, id); got.Sender != x.bot || got.Text != "Nothing done" ||
			got.ThreadRoot != to {
			t.Errorf("Reply() to %s posted %+v, want the bot's answer in that thread", to, got)
		}
	}
	if got := posted(t, x, x.general, root); got.ThreadRoot != "" || got.Text != textDarkMode {
		t.Errorf("the thread's root after Reply() = %+v, want it as sent", got)
	}
}

func (s chatSuite) edits(t *testing.T) {
	x := s.rooms(t)
	root := s.send(t, x, x.general, textDarkMode)
	other := s.send(t, x, x.general, textExport)
	for _, text := range []string{"Spec: dark mode (approved)", "Spec: dark mode (merged)"} {
		if _, err := x.Host.Edit(s.ctx, x.general, root, ports.ChatMessage{Text: text,
			HTML: "<p>" + text + "</p>"}); err != nil {
			t.Fatalf("Edit() = %v", err)
		}
	}
	want := []ChatPosted{{ID: root, Sender: x.bot, Text: "Spec: dark mode (merged)"},
		{ID: other, Sender: x.bot, Text: textExport}}
	if got := x.Room(t, x.general).Posts; !slices.EqualFunc(got, want, samePosted) {
		t.Errorf("the room after two edits shows %+v, want %+v", got, want)
	}
}

func (s chatSuite) reacts(t *testing.T) {
	x := s.rooms(t)
	root := s.send(t, x, x.general, textDarkMode)
	other := s.send(t, x, x.general, textExport)
	if _, err := x.Host.React(s.ctx, x.general, root, reactedDone); err != nil {
		t.Fatalf("React() = %v", err)
	}
	want := []ChatPosted{{ID: root, Sender: x.bot, Text: textDarkMode, Reactions: []string{reactedDone}},
		{ID: other, Sender: x.bot, Text: textExport}}
	if got := x.Room(t, x.general).Posts; !slices.EqualFunc(got, want, samePosted) {
		t.Errorf("the room after React() shows %+v, want %+v", got, want)
	}
}

// read syncs from since, without waiting, and fails t unless it returns
// want: every message once, each room's in the order given, with a time.
// Messages of two rooms may come in either order, since a host tells their
// order by its clock.
func (s chatSuite) read(t *testing.T, x chatRooms, since string, want ...ports.ChatEvent) string {
	t.Helper()
	got := must(x.Host.Sync(s.ctx, since, 0))(t)
	if got.Next == "" {
		t.Fatalf("Sync(%q) returned no token to go on from", since)
	}
	same := len(got.Events) == len(want)
	for i := range got.Events {
		if got.Events[i].Time.IsZero() {
			t.Errorf("Sync(%q) returned %s without its time", since, got.Events[i].ID)
		}
		got.Events[i].Time = time.Time{}
	}
	for _, room := range []string{x.general, x.alerts} {
		elsewhere := func(e ports.ChatEvent) bool { return e.Room != room }
		same = same && slices.Equal(slices.DeleteFunc(slices.Clone(got.Events), elsewhere),
			slices.DeleteFunc(slices.Clone(want), elsewhere))
	}
	if !same {
		t.Errorf("Sync(%q) returned%s\nwant%s", since, chatLines(got.Events), chatLines(want))
	}
	return got.Next
}

// chatLines is events as a failure shows them, a line each.
func chatLines(events []ports.ChatEvent) string {
	var b strings.Builder
	for _, e := range events {
		fmt.Fprintf(&b, "\n  %s in %s by %s: %q", e.ID, e.Room, e.Sender, e.Text)
		if e.ThreadRoot != "" {
			b.WriteString(", in the thread of " + e.ThreadRoot)
		}
		if e.Replaces != "" {
			b.WriteString(", replacing " + e.Replaces)
		}
	}
	if len(events) == 0 {
		b.WriteString("\n  nothing")
	}
	return b.String()
}

func (s chatSuite) syncs(t *testing.T) {
	x := s.rooms(t)
	// theos-chat's first Sync only marks where the rooms stand.
	x.Post(t, x.general, x.ada, "said before the bot read the rooms", "")
	since := must(x.Host.Sync(s.ctx, "", 0))(t).Next
	if since == "" {
		t.Fatal("the first Sync() returned no token to go on from")
	}

	root := s.send(t, x, x.alerts, "api failed")
	status := x.Post(t, x.general, x.ada, "status", "")
	retry := x.Post(t, x.alerts, x.ada, "retry", root)
	help := x.Post(t, x.general, x.ada, "help", "")
	if _, err := x.Host.Reply(s.ctx, x.alerts, root, ports.ChatMessage{Text: "Retried by ada"}); err != nil {
		t.Fatalf("Reply() = %v", err)
	}
	next := s.read(t, x, since,
		ports.ChatEvent{Room: x.general, ID: status, Sender: x.ada, Text: "status"},
		ports.ChatEvent{Room: x.alerts, ID: retry, Sender: x.ada, Text: "retry", ThreadRoot: root},
		ports.ChatEvent{Room: x.general, ID: help, Sender: x.ada, Text: "help"})
	if next == since {
		t.Errorf("Sync() after new messages returned the token %q it started from", since)
	}

	// From the new token: only what came after, so no command runs twice.
	pause := x.Post(t, x.general, x.ada, "pause shop", "")
	next = s.read(t, x, next, ports.ChatEvent{Room: x.general, ID: pause, Sender: x.ada, Text: "pause shop"})
	// Nothing new, and the token of that still reads what comes next.
	next = s.read(t, x, next)
	resume := x.Post(t, x.general, x.ada, "resume shop", "")
	s.read(t, x, next, ports.ChatEvent{Room: x.general, ID: resume, Sender: x.ada, Text: "resume shop"})
}

func (s chatSuite) syncsEdits(t *testing.T) {
	x := s.rooms(t)
	since := must(x.Host.Sync(s.ctx, "", 0))(t).Next
	said := x.Post(t, x.general, x.ada, "pause shp", "")
	edit := x.Amend(t, x.general, x.ada, said, "pause shop")
	s.read(t, x, since,
		ports.ChatEvent{Room: x.general, ID: said, Sender: x.ada, Text: "pause shp"},
		ports.ChatEvent{Room: x.general, ID: edit, Sender: x.ada, Text: "pause shop", Replaces: said})
}

// chatAtOnce is far longer than a Sync with a wait of 0 takes, and far
// shorter than the suite's own bound: a host that waits for a message
// anyway fails here, not after ten minutes.
const chatAtOnce = 30 * time.Second

// syncsIdle is theos-chat's pass over rooms where nobody said anything: it
// syncs with a wait of 0 and goes on with the token that returns.
func (s chatSuite) syncsIdle(t *testing.T) {
	x := s.rooms(t)
	idle := func(when, since string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(s.ctx, chatAtOnce)
		defer cancel()
		got, err := x.Host.Sync(ctx, since, 0)
		if err != nil || len(got.Events) != 0 || got.Next == "" {
			t.Fatalf("Sync(%q, 0) %s = %+v, %v; want no message and a token to go on from, at once", since,
				when, got, err)
		}
		return got.Next
	}
	next := idle("the first time, in rooms where nobody spoke", "")
	next = idle("with nothing new", next)
	next = idle("with nothing new, again", next)
	s.send(t, x, x.general, textDarkMode)
	next = idle("after only the bot spoke", next)
	said := x.Post(t, x.general, x.ada, "status", "")
	s.read(t, x, next, ports.ChatEvent{Room: x.general, ID: said, Sender: x.ada, Text: "status"})
}
