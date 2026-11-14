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

package fake

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// ChatHost is a chat host in memory, for the controllers' tests and the
// ChatHost suite: its users, rooms and messages, and the log a Sync reads.
// For gives the ports.ChatHost one bot speaks through; the other methods
// are the host's people and the host itself, as a test drives them.
//
// Its shape is given as options (docs/plans/requests-and-chat.md, step 3):
// WithoutSpaces and OneNamePerRoom make it a host shaped like Slack, with
// no Space and a channel's one name. In every shape it gives no more than
// the port promises, so a caller that relies on more fails on it: a
// message's ID is unique in its room only, as a Slack message's ts is in
// its channel, and a person's edit carries the ID of the message it
// replaces.
type ChatHost struct {
	s *chatState
}

// ChatOption shapes a ChatHost.
type ChatOption func(*chatState)

// WithoutSpaces makes a host without Spaces: EnsureRoom is refused a Space
// and a Parent, as the port says such a host is never asked for either.
func WithoutSpaces() ChatOption {
	return func(s *chatState) { s.noSpaces = true }
}

// OneNamePerRoom makes a host whose room has one name, its alias, as a
// Slack channel has: the room shows its alias as its name, and the Name
// EnsureRoom is given never changes it.
func OneNamePerRoom() ChatOption {
	return func(s *chatState) { s.oneName = true }
}

// MemberIDs makes a host whose user IDs name no server, as a Slack
// workspace's member IDs: U and then digits, one per account, in the order
// the accounts are made.
func MemberIDs() ChatOption {
	return func(s *chatState) { s.names = map[string]string{} }
}

// ChatAddress is the address the host answers at; the default is
// DefaultChatAddress.
func ChatAddress(address string) ChatOption {
	return func(s *chatState) { s.address = address }
}

// DefaultChatAddress is a ChatHost's address unless ChatAddress gives one.
const DefaultChatAddress = "https://chat.test"

// ErrNoSpaces is a Space asked of a host without Spaces.
var ErrNoSpaces = errors.New("the chat host has no Spaces")

// chatState is what every view of one ChatHost shares.
type chatState struct {
	mu       sync.Mutex
	address  string
	server   string // the address's host, as a user ID ends
	noSpaces bool
	oneName  bool
	// names are the member IDs by account name, nil on a host whose IDs
	// name its server (MemberIDs).
	names map[string]string
	// users are the user IDs by their token, and tokens the tokens by user.
	users  map[string]string
	tokens map[string]string
	// inactive are the accounts Deactivate closed, by user ID.
	inactive map[string]bool
	// refuse are the codes RefuseIn refuses a user's invite with, by room
	// and then user.
	refuse map[string]map[string]string
	// lacks are the scopes a token lacks, by the token and then the port
	// method that needs the scope (Revoke).
	lacks map[string]map[string]string
	// open counts the bots For built that are not closed.
	open  int
	rooms map[string]*chatRoom
	order []string // room IDs, as made
	// log is what people said, in the order the host received it; a sync
	// token is a position in it.
	log []ports.ChatEvent
	// said is closed and replaced whenever log grows, to wake a Sync that
	// waits.
	said chan struct{}
}

type chatRoom struct {
	id, alias, name, topic string
	space                  bool
	parent                 string
	members, invited       map[string]bool
	posts                  []*ChatPost
	// messages counts the room's messages, which number its message IDs.
	messages int
}

// ChatPost is a message as the host shows it.
type ChatPost struct {
	// ID and Sender say which message and whose.
	ID     string
	Sender string
	// Text is its plain text, as its last edit left it, and Formatted the
	// markup it was sent with, "" for none.
	Text      string
	Formatted string
	// ThreadRoot is the message it answers in a thread, "" outside one.
	ThreadRoot string
	// Reactions are the reaction keys on it, each once.
	Reactions []string
}

// ChatRoomState is a room as the host shows it.
type ChatRoomState struct {
	ID, Alias, Name, Topic string
	// Space says the room is a Space, and Parent names the Space it is in.
	Space  bool
	Parent string
	// Private is true of every room: only the people invited may enter it.
	Private bool
	// Members are the users in the room and those invited to it, sorted.
	Members []string
	// Posts are its messages, oldest first.
	Posts []ChatPost
}

var _ ports.ChatHost = (*chatBot)(nil)

// NewChatHost returns an empty chat host, shaped by opts.
func NewChatHost(opts ...ChatOption) *ChatHost {
	s := &chatState{address: DefaultChatAddress, users: map[string]string{}, tokens: map[string]string{},
		rooms: map[string]*chatRoom{}, said: make(chan struct{})}
	for _, opt := range opts {
		opt(s)
	}
	s.server = s.address
	if u, err := url.Parse(s.address); err == nil && u.Hostname() != "" {
		s.server = u.Hostname()
	}
	return &ChatHost{s: s}
}

// Address is the address the host answers at, as ChatConfig.Address names
// it.
func (h *ChatHost) Address() string { return h.s.address }

// For returns the ChatHost of the bot whose token cfg holds; a token the
// host doesn't know is refused on every call, ErrChatCredentialRefused. It
// counts as open (Open) until it is closed.
func (h *ChatHost) For(cfg ports.ChatConfig) ports.ChatHost {
	h.s.mu.Lock()
	defer h.s.mu.Unlock()
	h.s.open++
	return &chatBot{s: h.s, token: cfg.Token}
}

// Open counts the ChatHosts For built that are not closed: on a host that
// sends each event to one open connection, each one a reader holds and
// doesn't read swallows events.
func (h *ChatHost) Open() int {
	h.s.mu.Lock()
	defer h.s.mu.Unlock()
	return h.s.open
}

// ErrChatClosed is a call of a ChatHost after its Close.
var ErrChatClosed = errors.New("the chat host's connection is closed")

// NewToken gives user a second token, as a token made again does; the
// first goes on working until the host forgets it.
func (h *ChatHost) NewToken(user string) (string, error) {
	s := h.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tokens[user]; !ok {
		return "", fmt.Errorf("no user %s", user)
	}
	token := rand.Text()
	s.users[token] = user
	return token, nil
}

// Revoke takes scope from token for the port's methods named, such as
// "EnsureRoom" or "Invite": each such call is refused with a
// ports.ChatScopeMissingError naming scope, as a host whose tokens carry
// scopes refuses one without.
func (h *ChatHost) Revoke(token, scope string, methods ...string) {
	s := h.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lacks == nil {
		s.lacks = map[string]map[string]string{}
	}
	if s.lacks[token] == nil {
		s.lacks[token] = map[string]string{}
	}
	for _, m := range methods {
		s.lacks[token][m] = scope
	}
}

// Grant gives scope back to token, as installing the bot again with it does.
func (h *ChatHost) Grant(token, scope string) {
	s := h.s
	s.mu.Lock()
	defer s.mu.Unlock()
	maps.DeleteFunc(s.lacks[token], func(_, lacked string) bool { return lacked == scope })
}

// CodeUserNotFound is the code an invite of a person the host has no
// account for, or whose account Deactivate closed, is refused with: a
// ports.ChatInviteRefusedError, as Slack answers user_not_found for both
// (#251).
const CodeUserNotFound = "user_not_found"

// Deactivate closes user's account, as an admin who deactivates a member
// does: an invite of them is refused (CodeUserNotFound) until Reactivate.
func (h *ChatHost) Deactivate(user string) {
	s := h.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inactive == nil {
		s.inactive = map[string]bool{}
	}
	s.inactive[user] = true
}

// Reactivate opens the account Deactivate closed.
func (h *ChatHost) Reactivate(user string) {
	s := h.s
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inactive, user)
}

// RefuseIn makes an invite of user to room refused with code, a
// ports.ChatInviteRefusedError, as Slack refuses a single-channel guest in
// every channel but theirs (ura_max_channels); a code of "" lifts it
// (#251). It refuses user in that room alone.
func (h *ChatHost) RefuseIn(room, user, code string) {
	s := h.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if code == "" {
		delete(s.refuse[room], user)
		return
	}
	if s.refuse == nil {
		s.refuse = map[string]map[string]string{}
	}
	if s.refuse[room] == nil {
		s.refuse[room] = map[string]string{}
	}
	s.refuse[room][user] = code
}

// Hosts builds the ChatHost of a bot on this host, as cmd/main.go's
// adapters are given: a config with another address is refused.
func (h *ChatHost) Hosts() ports.ChatHosts {
	return func(cfg ports.ChatConfig) (ports.ChatHost, error) {
		if cfg.Address != h.s.address {
			return nil, fmt.Errorf("the fake chat host answers at %s, not %s", h.s.address, cfg.Address)
		}
		return h.For(cfg), nil
	}
}

// NewUser makes an account, the bot's or a person's, and returns its user
// ID and token; asked again for name, it returns the same.
func (h *ChatHost) NewUser(name string) (id, token string) {
	s := h.s
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.user(name)
}

func (s *chatState) user(name string) (id, token string) {
	id = "@" + name + ":" + s.server
	if s.names != nil {
		if id = s.names[name]; id == "" {
			id = fmt.Sprintf("U%08d", len(s.names)+1)
			s.names[name] = id
		}
	}
	if token, ok := s.tokens[id]; ok {
		return id, token
	}
	token = rand.Text()
	s.users[token], s.tokens[id] = id, token
	return id, token
}

// Join makes user a member of room, as accepting its invite does; a user
// not invited can't enter.
func (h *ChatHost) Join(room, user string) error {
	s := h.s
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.room(room)
	if err != nil {
		return err
	}
	if !r.members[user] && !r.invited[user] {
		return fmt.Errorf("%s is not invited to room %s", user, room)
	}
	delete(r.invited, user)
	r.members[user] = true
	return nil
}

// Remove takes user out of room, as a person who removes the bot does.
func (h *ChatHost) Remove(room, user string) error {
	s := h.s
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.room(room)
	if err != nil {
		return err
	}
	delete(r.members, user)
	delete(r.invited, user)
	return nil
}

// Squat makes a room that holds alias, made by an account of its own, as
// someone else on the host would, and returns its ID.
func (h *ChatHost) Squat(alias string) (string, error) {
	s := h.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.byAlias(alias); r != nil {
		return "", fmt.Errorf("alias %s is held by room %s", alias, r.id)
	}
	stranger, _ := s.user("squatter")
	return s.make(stranger, ports.ChatRoomSpec{Alias: alias, Name: alias}).id, nil
}

// Post posts text in room as user, in the thread of threadRoot when it
// isn't "", and returns the message's ID; a Sync reads it at once.
func (h *ChatHost) Post(room, user, text, threadRoot string) (string, error) {
	s := h.s
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.member(room, user)
	if err != nil {
		return "", err
	}
	p, err := r.post(user, ports.ChatMessage{Text: text}, threadRoot)
	if err != nil {
		return "", err
	}
	s.heard(ports.ChatEvent{Room: room, ID: p.ID, Sender: user, Text: text, ThreadRoot: threadRoot})
	return p.ID, nil
}

// Amend replaces the text of user's message in room with text, as a
// person's edit does, and returns the ID a Sync names the edit by: the
// message's own.
func (h *ChatHost) Amend(room, user, message, text string) (string, error) {
	s := h.s
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.member(room, user)
	if err != nil {
		return "", err
	}
	p, err := r.own(user, message)
	if err != nil {
		return "", err
	}
	p.Text = text
	s.heard(ports.ChatEvent{Room: room, ID: message, Sender: user, Text: text, Replaces: message})
	return message, nil
}

// Room is room as the host shows it to the people in it.
func (h *ChatHost) Room(room string) (ChatRoomState, bool) {
	s := h.s
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[room]
	if !ok {
		return ChatRoomState{}, false
	}
	return r.state(s.oneName), true
}

// Rooms are the IDs of the host's rooms, Spaces included, as they were
// made.
func (h *ChatHost) Rooms() []string {
	s := h.s
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.order)
}

func (r *chatRoom) state(oneName bool) ChatRoomState {
	out := ChatRoomState{ID: r.id, Alias: r.alias, Name: r.name, Topic: r.topic, Space: r.space, Parent: r.parent,
		Private: true}
	if oneName {
		out.Name = r.alias
	}
	for user := range r.members {
		out.Members = append(out.Members, user)
	}
	for user := range r.invited {
		out.Members = append(out.Members, user)
	}
	slices.Sort(out.Members)
	for _, p := range r.posts {
		c := *p
		c.Reactions = slices.Clone(p.Reactions)
		out.Posts = append(out.Posts, c)
	}
	return out
}

// room is the room id, or an error.
func (s *chatState) room(id string) (*chatRoom, error) {
	r, ok := s.rooms[id]
	if !ok {
		return nil, fmt.Errorf("no room %s", id)
	}
	return r, nil
}

// member is room id when user is in it, or an error.
func (s *chatState) member(id, user string) (*chatRoom, error) {
	r, err := s.room(id)
	if err != nil {
		return nil, err
	}
	if !r.members[user] {
		return nil, fmt.Errorf("%s is not in room %s", user, id)
	}
	return r, nil
}

// byAlias is the room that holds alias, nil for none.
func (s *chatState) byAlias(alias string) *chatRoom {
	for _, id := range s.order {
		if r := s.rooms[id]; r.alias == alias {
			return r
		}
	}
	return nil
}

// make makes the room spec describes, with creator alone in it.
func (s *chatState) make(creator string, spec ports.ChatRoomSpec) *chatRoom {
	// A fixed width, so no room's ID is another's with a digit added.
	r := &chatRoom{id: fmt.Sprintf("C%06d", len(s.order)+1), alias: spec.Alias, name: spec.Name, topic: spec.Topic,
		space: spec.Space, parent: spec.Parent, members: map[string]bool{creator: true}, invited: map[string]bool{}}
	s.rooms[r.id] = r
	s.order = append(s.order, r.id)
	return r
}

// heard adds e to the log, and wakes the Syncs that wait.
func (s *chatState) heard(e ports.ChatEvent) {
	e.Time = time.Now()
	s.log = append(s.log, e)
	close(s.said)
	s.said = make(chan struct{})
}

// post adds msg to r as sender, in the thread of threadRoot when it isn't
// "": a thread's root is a message of the room outside any thread.
func (r *chatRoom) post(sender string, msg ports.ChatMessage, threadRoot string) (*ChatPost, error) {
	if threadRoot != "" {
		root, err := r.message(threadRoot)
		if err != nil {
			return nil, err
		}
		if root.ThreadRoot != "" {
			return nil, fmt.Errorf("message %s is in a thread and roots none", threadRoot)
		}
	}
	r.messages++
	// Numbered in its room alone: two rooms each have a message 000001.
	p := &ChatPost{ID: fmt.Sprintf("%06d", r.messages), Sender: sender, Text: msg.Text, Formatted: msg.Formatted,
		ThreadRoot: threadRoot}
	r.posts = append(r.posts, p)
	return p, nil
}

// message is r's message id, or an error.
func (r *chatRoom) message(id string) (*ChatPost, error) {
	for _, p := range r.posts {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no message %s in room %s", id, r.id)
}

// own is r's message id when sender sent it, or an error.
func (r *chatRoom) own(sender, id string) (*ChatPost, error) {
	p, err := r.message(id)
	if err != nil {
		return nil, err
	}
	if p.Sender != sender {
		return nil, fmt.Errorf("message %s in room %s is %s's, not %s's", id, r.id, p.Sender, sender)
	}
	return p, nil
}

// chatBot is a ChatHost's port as the bot whose token it holds.
type chatBot struct {
	s     *chatState
	token string
	// closed is set by Close, under s.mu.
	closed bool
}

// bot locks the host for a call of the port's method and returns the bot's
// user ID; a closed bot is ErrChatClosed, a token the host doesn't know
// ErrChatCredentialRefused and one Revoke took the method's scope from a
// ChatScopeMissingError, and the host is unlocked. The caller unlocks it
// otherwise.
func (b *chatBot) bot(method string) (string, error) {
	b.s.mu.Lock()
	id, ok := b.s.users[b.token]
	scope, lacks := b.s.lacks[b.token][method]
	switch {
	case b.closed:
		b.s.mu.Unlock()
		return "", ErrChatClosed
	case !ok || b.token == "":
		b.s.mu.Unlock()
		return "", ports.ErrChatCredentialRefused
	case lacks:
		b.s.mu.Unlock()
		return "", &ports.ChatScopeMissingError{Scope: scope}
	}
	return id, nil
}

// Close closes the bot: it takes no call after, and the host counts it open
// no more (Open).
func (b *chatBot) Close() {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	if !b.closed {
		b.closed = true
		b.s.open--
	}
}

func (b *chatBot) WhoAmI(context.Context) (string, error) {
	id, err := b.bot("WhoAmI")
	if err != nil {
		return "", err
	}
	b.s.mu.Unlock()
	return id, nil
}

func (b *chatBot) EnsureRoom(_ context.Context, spec ports.ChatRoomSpec) (ports.ChatRoom, error) {
	bot, err := b.bot("EnsureRoom")
	if err != nil {
		return ports.ChatRoom{}, err
	}
	s := b.s
	defer s.mu.Unlock()
	if s.noSpaces && (spec.Space || spec.Parent != "") {
		return ports.ChatRoom{}, ErrNoSpaces
	}
	if spec.Parent != "" {
		if p, ok := s.rooms[spec.Parent]; !ok || !p.space || !p.members[bot] {
			return ports.ChatRoom{}, fmt.Errorf("no Space %s the bot is in", spec.Parent)
		}
	}
	r, found := s.rooms[spec.ID]
	if !found || !r.members[bot] {
		r = s.byAlias(spec.Alias)
		if r != nil && !r.members[bot] {
			return ports.ChatRoom{}, &ports.ChatAliasTakenError{Alias: spec.Alias}
		}
	}
	if r == nil {
		if spec.Alias == "" {
			return ports.ChatRoom{}, errors.New("a room is made with an alias")
		}
		r = s.make(bot, spec)
		return ports.ChatRoom{ID: r.id, Alias: r.alias, Created: true}, nil
	}
	if r.space != spec.Space {
		return ports.ChatRoom{}, fmt.Errorf("room %s is a Space %v, asked for a Space %v", r.id, r.space, spec.Space)
	}
	if !s.oneName {
		r.name = spec.Name
	}
	r.parent = spec.Parent
	return ports.ChatRoom{ID: r.id, Alias: r.alias}, nil
}

func (b *chatBot) Invite(_ context.Context, room, user string) error {
	bot, err := b.bot("Invite")
	if err != nil {
		return err
	}
	s := b.s
	defer s.mu.Unlock()
	r, err := s.member(room, bot)
	if err != nil {
		return err
	}
	if _, ok := s.tokens[user]; !ok || s.inactive[user] {
		return &ports.ChatInviteRefusedError{User: user, Code: CodeUserNotFound}
	}
	if code := s.refuse[room][user]; code != "" {
		return &ports.ChatInviteRefusedError{User: user, Code: code}
	}
	if !r.members[user] {
		r.invited[user] = true
	}
	return nil
}

// say posts msg in room as the bot, in the thread of threadRoot when it
// isn't "". The bot's messages are not in the log: no Sync returns them.
// method is the port method that says it.
func (b *chatBot) say(method, room, threadRoot string, msg ports.ChatMessage) (string, error) {
	bot, err := b.bot(method)
	if err != nil {
		return "", err
	}
	s := b.s
	defer s.mu.Unlock()
	r, err := s.member(room, bot)
	if err != nil {
		return "", err
	}
	p, err := r.post(bot, msg, threadRoot)
	if err != nil {
		return "", err
	}
	return p.ID, nil
}

func (b *chatBot) Send(_ context.Context, room string, msg ports.ChatMessage) (string, error) {
	return b.say("Send", room, "", msg)
}

func (b *chatBot) Reply(_ context.Context, room, threadRoot string, msg ports.ChatMessage) (string, error) {
	if threadRoot == "" {
		return "", errors.New("a reply names its thread's root")
	}
	return b.say("Reply", room, threadRoot, msg)
}

func (b *chatBot) Edit(_ context.Context, room, event string, msg ports.ChatMessage) (string, error) {
	bot, err := b.bot("Edit")
	if err != nil {
		return "", err
	}
	s := b.s
	defer s.mu.Unlock()
	r, err := s.member(room, bot)
	if err != nil {
		return "", err
	}
	p, err := r.own(bot, event)
	if err != nil {
		return "", err
	}
	p.Text, p.Formatted = msg.Text, msg.Formatted
	return event, nil
}

func (b *chatBot) React(_ context.Context, room, event, key string) (string, error) {
	bot, err := b.bot("React")
	if err != nil {
		return "", err
	}
	s := b.s
	defer s.mu.Unlock()
	r, err := s.member(room, bot)
	if err != nil {
		return "", err
	}
	p, err := r.message(event)
	if err != nil {
		return "", err
	}
	if !slices.Contains(p.Reactions, key) {
		p.Reactions = append(p.Reactions, key)
	}
	return event, nil
}

// syncToken is a position in the log, as a Sync returns it.
func syncToken(n int) string { return "s" + strconv.Itoa(n) }

func (b *chatBot) Sync(ctx context.Context, since string, wait time.Duration) (ports.ChatSync, error) {
	deadline := time.Now().Add(wait)
	for {
		bot, err := b.bot("Sync")
		if err != nil {
			return ports.ChatSync{}, err
		}
		s := b.s
		if since == "" {
			// The first sync marks where the rooms stand.
			next := syncToken(len(s.log))
			s.mu.Unlock()
			return ports.ChatSync{Next: next}, nil
		}
		from, err := strconv.Atoi(strings.TrimPrefix(since, "s"))
		if err != nil || !strings.HasPrefix(since, "s") || from < 0 || from > len(s.log) {
			s.mu.Unlock()
			return ports.ChatSync{}, fmt.Errorf("no sync token %q", since)
		}
		out := ports.ChatSync{Next: syncToken(len(s.log))}
		for _, e := range s.log[from:] {
			if r := s.rooms[e.Room]; r != nil && r.members[bot] && e.Sender != bot {
				out.Events = append(out.Events, e)
			}
		}
		said := s.said
		s.mu.Unlock()
		left := time.Until(deadline)
		if len(out.Events) > 0 || left <= 0 {
			return out, nil
		}
		// Nothing for the bot yet: wait for the log to grow, and read on
		// from the same token.
		timer := time.NewTimer(left)
		select {
		case <-said:
			timer.Stop()
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ports.ChatSync{}, ctx.Err()
		}
	}
}
