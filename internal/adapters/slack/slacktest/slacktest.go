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

// Package slacktest is a fake Slack workspace with one app: the Web API
// calls the slack adapter makes, over httptest, and Socket Mode on the same
// server, as Slack's reference describes them (docs/plans/requests-and-chat.md,
// "Step 5 as built"). Each method asks for the scope Slack's reference names
// (Scopes), so a token without it gets missing_scope with the scope needed.
// A message event goes to the newest open socket and waits for its
// acknowledgement, so a person's message is buffered when Post returns; a
// message said while no socket is open is only in the history, as on Slack.
// A section block's text must say verbatim, so that no `@channel` or
// `#name` a person typed becomes a mention.
package slacktest

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// Scopes are the scopes a token needs for each Web API method the fake
// serves, as Slack's reference names them: the bot token's, and
// connections:write, the app-level token's.
var Scopes = map[string]string{
	"auth.test":              "",
	"users.conversations":    "groups:read",
	"conversations.create":   scopeGroupsWrite,
	"conversations.setTopic": scopeGroupsWrite,
	"conversations.invite":   scopeGroupsWrite,
	methodHistory:            "groups:history",
	"conversations.replies":  "groups:history",
	"chat.postMessage":       "chat:write",
	"chat.update":            "chat:write",
	"reactions.add":          "reactions:write",
	"apps.connections.open":  "connections:write",
}

// scopeGroupsWrite is the scope of making, inviting to and setting the
// topic of a private channel.
const scopeGroupsWrite = "groups:write"

// methodHistory is the method that lists a channel's messages.
const methodHistory = "conversations.history"

// The keys of the events and answers the fake writes more than once.
const (
	keyType    = "type"
	keyUser    = "user"
	keyChannel = "channel"
	keyText    = "text"
	keyName    = "name"
	// typeMessage is a message event's type, and the key of the message in
	// an edit's event and in an answer.
	typeMessage = "message"
)

// appMethod is the one method the app-level token calls, and the only one
// it may.
const appMethod = "apps.connections.open"

// ackWait bounds the wait for an envelope's acknowledgement.
const ackWait = 10 * time.Second

// The error codes the fake answers more than once.
const (
	errChannelNotFound = "channel_not_found"
	errMessageNotFound = "message_not_found"
	errNotInChannel    = "not_in_channel"
)

// channelName is a name Slack takes for a channel.
var channelName = regexp.MustCompile(`^[a-z0-9_-]{1,80}$`)

// emojiName is a name reactions.add takes.
var emojiName = regexp.MustCompile(`^[a-z0-9_+-]+$`)

// Server is a fake Slack workspace.
type Server struct {
	srv *httptest.Server

	mu       sync.Mutex
	users    map[string]string // user ID → name
	tokens   map[string]*credential
	channels map[string]*channel
	order    []string // channel IDs, oldest first
	bot      string   // the app's bot user
	botID    string
	next     int
	last     time.Time // the newest ts given
	socks    []*sock   // open sockets, oldest first
	tickets  map[string]bool
	opens    int
	events   int
	sent     *delivery // the last event delivered, for Redeliver
	limited  int
	calls    map[string]int
	refuse   int                      // dials to refuse, at the handshake
	maxPage  int                      // the most items a page holds, 0 for the limit asked
	holds    map[string]chan struct{} // by method, closed when it may answer
}

type credential struct {
	user   string
	app    bool
	scopes map[string]bool
}

type channel struct {
	id, name, creator, topic string
	members                  map[string]bool
	messages                 []*message
}

type message struct {
	ts, user, botID, text, threadTS, edited string
	blocks                                  string
	reactions                               []reaction
}

type reaction struct{ name, user string }

// sock is an open Socket Mode connection.
type sock struct {
	ws   *websocket.Conn
	wmu  sync.Mutex
	done chan struct{}
	mu   sync.Mutex
	acks map[string]chan struct{}
}

// delivery is an event as it went out, to send again.
type delivery struct {
	eventID string
	event   map[string]any
}

// New starts a fake workspace, closed when t ends.
func New(t testing.TB) *Server {
	s := Start()
	t.Cleanup(s.Close)
	return s
}

// Start starts a fake workspace the caller closes with Close.
func Start() *Server {
	s := &Server{users: map[string]string{}, tokens: map[string]*credential{}, channels: map[string]*channel{},
		tickets: map[string]bool{}, calls: map[string]int{}, holds: map[string]chan struct{}{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", s.serve)
	mux.Handle("/link", websocket.Handler(s.socket))
	s.srv = httptest.NewServer(mux)
	return s
}

// Close closes every socket and stops the server.
func (s *Server) Close() {
	s.Disconnect("")
	s.srv.Close()
}

// URL is the Web API's address, as the adapter's ChatConfig takes it.
func (s *Server) URL() string { return s.srv.URL + "/api" }

// id is a fresh ID with prefix; s.mu is held.
func (s *Server) id(prefix string) string {
	s.next++
	return fmt.Sprintf("%s%07X", prefix, s.next)
}

// tick is a fresh ts, after every one given and never before now; s.mu is
// held.
func (s *Server) tick() string {
	t := time.Now()
	if !t.After(s.last) {
		t = s.last.Add(time.Microsecond)
	}
	s.last = t.Truncate(time.Microsecond)
	return fmt.Sprintf("%d.%06d", s.last.Unix(), s.last.Nanosecond()/1000)
}

// NewApp makes the workspace's one app: its bot user, a bot token with
// every scope of Scopes and an app-level token with connections:write.
func (s *Server) NewApp(name string) (bot, botToken, appToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bot, s.botID = s.id("U"), s.id("B")
	s.users[s.bot] = name
	scopes := map[string]bool{}
	for method, scope := range Scopes {
		if method != appMethod && scope != "" {
			scopes[scope] = true
		}
	}
	botToken, appToken = rand.Text(), rand.Text()
	s.tokens[botToken] = &credential{user: s.bot, scopes: scopes}
	s.tokens[appToken] = &credential{app: true, scopes: map[string]bool{Scopes[appMethod]: true}}
	return s.bot, botToken, appToken
}

// NewUser makes a person and returns their member ID.
func (s *Server) NewUser(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.id("U")
	s.users[id] = name
	return id
}

// Revoke takes scope from token.
func (s *Server) Revoke(token, scope string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.tokens[token]; ok {
		delete(c.scopes, scope)
	}
}

// RateLimit makes the next n calls answer 429, with Retry-After: 0.
func (s *Server) RateLimit(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limited = n
}

// Calls is how many times method was called.
func (s *Server) Calls(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[method]
}

// Opens is how many sockets apps.connections.open gave an address for.
func (s *Server) Opens() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opens
}

// Open is how many sockets are open now.
func (s *Server) Open() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.socks)
}

// Join puts user in channel, as an invite does.
func (s *Server) Join(channelID, user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channels[channelID]
	if !ok {
		return errors.New(errChannelNotFound)
	}
	ch.members[user] = true
	return nil
}

// Remove takes user out of channel, as a person who removes them does.
func (s *Server) Remove(channelID, user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channels[channelID]
	if !ok {
		return errors.New(errChannelNotFound)
	}
	delete(ch.members, user)
	return nil
}

// Squat makes a private channel named name that the bot is not in, as
// someone else in the workspace would, and returns its ID.
func (s *Server) Squat(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	stranger := s.id("U")
	s.users[stranger] = "mallory"
	return s.create(stranger, name).id
}

// create makes a private channel; s.mu is held.
func (s *Server) create(creator, name string) *channel {
	ch := &channel{id: s.id("C"), name: name, creator: creator, members: map[string]bool{creator: true}}
	s.channels[ch.id] = ch
	s.order = append(s.order, ch.id)
	return ch
}

// Post posts text in channel as user, in the thread of threadTS when it
// isn't "", and returns its ts once an open socket acknowledged the event.
// text is as Slack sends it: the writer's &, < and > as entities, a link
// as <address|words> and a mention as <@U…>.
func (s *Server) Post(channelID, user, text, threadTS string) (string, error) {
	ts, deliver, err := s.PostHeld(channelID, user, text, threadTS)
	if err != nil {
		return "", err
	}
	return ts, deliver()
}

// PostHeld posts as Post does, but sends the event only when deliver is
// called: a message that arrives late, after newer ones.
func (s *Server) PostHeld(channelID, user, text, threadTS string) (string, func() error, error) {
	s.mu.Lock()
	ch, ok := s.channels[channelID]
	if !ok || !ch.members[user] {
		s.mu.Unlock()
		return "", nil, fmt.Errorf("%s is not in %s", user, channelID)
	}
	m, err := s.say(ch, user, "", text, "", threadTS)
	s.mu.Unlock()
	if err != nil {
		return "", nil, err
	}
	return m.ts, func() error { return s.deliver(messageEvent(ch.id, m)) }, nil
}

// Edit replaces the text of user's message ts with text, as a person's
// edit does, and returns the message's ts, which the edit's event carries,
// once an open socket acknowledged it.
func (s *Server) Edit(channelID, user, ts, text string) (string, error) {
	s.mu.Lock()
	ch, ok := s.channels[channelID]
	if !ok {
		s.mu.Unlock()
		return "", errors.New(errChannelNotFound)
	}
	m := find(ch, ts)
	if m == nil || m.user != user {
		s.mu.Unlock()
		return "", fmt.Errorf("%s has no message %s", user, ts)
	}
	ev := s.change(ch, m, text, "")
	s.mu.Unlock()
	return ts, s.deliver(ev)
}

// Redeliver sends the last event delivered again, with its event_id, in a
// new envelope, as Slack does when it doubts an acknowledgement came.
func (s *Server) Redeliver() error {
	s.mu.Lock()
	d := s.sent
	s.mu.Unlock()
	if d == nil {
		return errors.New("no event was delivered")
	}
	return s.send(d.eventID, d.event, 1)
}

// Disconnect sends Slack's disconnect with reason on every open socket, as
// Slack does every few hours, and leaves each open, as Slack does for about
// ten seconds after its warning: it gets events until the app closes it or
// Disconnect("") does. Disconnect("") closes every socket without a word.
func (s *Server) Disconnect(reason string) {
	s.mu.Lock()
	socks := slices.Clone(s.socks)
	if reason == "" {
		s.socks = nil
	}
	s.mu.Unlock()
	for _, k := range socks {
		if reason != "" {
			_ = k.write(map[string]any{keyType: "disconnect", "reason": reason,
				"debug_info": map[string]string{"host": "slacktest"}})
			continue
		}
		_ = k.ws.Close()
	}
}

// HoldOpens makes apps.connections.open wait, until release is called, to
// answer: a socket being opened again. Calls counts a call that waits.
// Release is safe to call twice.
func (s *Server) HoldOpens() (release func()) { return s.Hold(appMethod) }

// Hold makes every call of method wait, until release is called, to answer:
// a catch-up under way, when method is one it reads. Calls counts a call
// that waits. Release is safe to call twice.
func (s *Server) Hold(method string) (release func()) {
	held := make(chan struct{})
	s.mu.Lock()
	s.holds[method] = held
	s.mu.Unlock()
	return sync.OnceFunc(func() { close(held) })
}

// RefuseDials makes the next n dials to an address apps.connections.open
// gave fail at the handshake, as a socket server that is down does.
func (s *Server) RefuseDials(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refuse = n
}

// MaxPage caps every listing's page at n items, whatever limit the caller
// asks for, as Slack may return fewer than the limit.
func (s *Server) MaxPage(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxPage = n
}

// ChannelState is a channel as the people in it see it.
type ChannelState struct {
	ID, Name, Topic string
	Private         bool
	// Members are the member IDs in it, sorted.
	Members []string
	// Messages are its messages, threads included, oldest first.
	Messages []Message
}

// Message is a message as the people see it.
type Message struct {
	TS, User, ThreadTS string
	// Text is as Slack sends it (entities, <…>), Blocks the blocks' JSON,
	// "" for none.
	Text, Blocks string
	// Edited is the ts of its last edit, "" for none.
	Edited string
	// Reactions are the reactions' emoji names, in the order put.
	Reactions []string
}

// Channel is channel as its members see it.
func (s *Server) Channel(id string) (ChannelState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.channels[id]
	if !ok {
		return ChannelState{}, false
	}
	st := ChannelState{ID: ch.id, Name: ch.name, Topic: ch.topic, Private: true,
		Members: slices.Sorted(maps.Keys(ch.members))}
	for _, m := range ch.messages {
		msg := Message{TS: m.ts, User: m.user, ThreadTS: m.threadTS, Text: m.text, Blocks: m.blocks,
			Edited: m.edited}
		for _, r := range m.reactions {
			msg.Reactions = append(msg.Reactions, r.name)
		}
		st.Messages = append(st.Messages, msg)
	}
	return st, true
}

// find is ch's message ts, nil for none.
func find(ch *channel, ts string) *message {
	for _, m := range ch.messages {
		if m.ts == ts {
			return m
		}
	}
	return nil
}

// say posts a message in ch; s.mu is held.
func (s *Server) say(ch *channel, user, botID, text, blocks, threadTS string) (*message, error) {
	if threadTS != "" {
		root := find(ch, threadTS)
		if root == nil {
			return nil, errors.New(errMessageNotFound)
		}
		if root.threadTS != "" {
			threadTS = root.threadTS
		}
		root.threadTS = threadTS
	}
	m := &message{ts: s.tick(), user: user, botID: botID, text: text, blocks: blocks, threadTS: threadTS}
	ch.messages = append(ch.messages, m)
	return m, nil
}

// change edits m; s.mu is held. It returns the message_changed event.
func (s *Server) change(ch *channel, m *message, text, blocks string) map[string]any {
	prev := messageJSON(ch, m)
	m.text, m.blocks, m.edited = text, blocks, s.tick()
	now := messageJSON(ch, m)
	now["edited"] = map[string]string{keyUser: m.user, "ts": m.edited}
	return map[string]any{keyType: typeMessage, "subtype": "message_changed", "hidden": true, keyChannel: ch.id,
		"channel_type": "group", "ts": m.edited, "event_ts": m.edited, typeMessage: now, "previous_message": prev}
}

// messageJSON is m as the Web API and events show it.
func messageJSON(ch *channel, m *message) map[string]any {
	out := map[string]any{keyType: typeMessage, keyUser: m.user, keyText: m.text, "ts": m.ts}
	if m.botID != "" {
		out["bot_id"] = m.botID
	}
	if m.threadTS != "" {
		out["thread_ts"] = m.threadTS
	}
	if m.blocks != "" {
		out["blocks"] = json.RawMessage(m.blocks)
	}
	if m.edited != "" {
		out["edited"] = map[string]string{keyUser: m.user, "ts": m.edited}
	}
	if m.threadTS == m.ts {
		var n int
		latest := ""
		for _, r := range ch.messages {
			if r.threadTS == m.ts && r.ts != m.ts {
				n++
				latest = r.ts
			}
		}
		out["reply_count"], out["latest_reply"] = n, latest
	}
	return out
}

// messageEvent is m's message event.
func messageEvent(channelID string, m *message) map[string]any {
	ev := map[string]any{keyType: typeMessage, keyChannel: channelID, "channel_type": "group", keyUser: m.user,
		keyText: m.text, "ts": m.ts, "event_ts": m.ts}
	if m.botID != "" {
		ev["bot_id"] = m.botID
	}
	if m.threadTS != "" && m.threadTS != m.ts {
		ev["thread_ts"] = m.threadTS
	}
	return ev
}

// deliver sends ev to the app with a fresh event_id, when the bot is in its
// channel.
func (s *Server) deliver(ev map[string]any) error {
	s.mu.Lock()
	ch := s.channels[ev[keyChannel].(string)]
	if ch == nil || !ch.members[s.bot] {
		s.mu.Unlock()
		return nil
	}
	s.events++
	d := &delivery{eventID: fmt.Sprintf("Ev%08d", s.events), event: ev}
	s.sent = d
	s.mu.Unlock()
	return s.send(d.eventID, d.event, 0)
}

// send puts the event in an envelope on the newest open socket and waits
// for its acknowledgement. With no socket open it sends nothing: the
// message is in the history, where a catch-up reads it.
func (s *Server) send(eventID string, ev map[string]any, attempt int) error {
	s.mu.Lock()
	var k *sock
	if n := len(s.socks); n > 0 {
		k = s.socks[n-1]
	}
	s.mu.Unlock()
	if k == nil {
		return nil
	}
	envelope := rand.Text()
	ack := make(chan struct{})
	k.mu.Lock()
	k.acks[envelope] = ack
	k.mu.Unlock()
	reason := ""
	if attempt > 0 {
		reason = "timeout"
	}
	err := k.write(map[string]any{keyType: "events_api", "envelope_id": envelope, "accepts_response_payload": false,
		"retry_attempt": attempt, "retry_reason": reason,
		"payload": map[string]any{keyType: "event_callback", "team_id": "T0SLACKTEST", "api_app_id": "A0SLACKTEST",
			"event_id": eventID, "event_time": time.Now().Unix(), "event": ev}})
	if err != nil {
		return nil // the socket closed: the message is in the history
	}
	timer := time.NewTimer(ackWait)
	defer timer.Stop()
	select {
	case <-ack:
		return nil
	case <-k.done:
		return nil
	case <-timer.C:
		return fmt.Errorf("the app acknowledged no envelope of event %s in %s", eventID, ackWait)
	}
}

// write sends v on the socket.
func (k *sock) write(v any) error {
	k.wmu.Lock()
	defer k.wmu.Unlock()
	return websocket.JSON.Send(k.ws, v)
}

// socket serves one Socket Mode connection: hello, then acknowledgements
// until it closes.
func (s *Server) socket(ws *websocket.Conn) {
	ticket := ws.Request().URL.Query().Get("ticket")
	s.mu.Lock()
	ok := s.tickets[ticket]
	delete(s.tickets, ticket)
	s.mu.Unlock()
	if !ok {
		_ = ws.Close()
		return
	}
	k := &sock{ws: ws, done: make(chan struct{}), acks: map[string]chan struct{}{}}
	defer close(k.done)
	// The socket is open, and gets events, before the app reads hello; the
	// hello goes first, since every write waits for it.
	k.wmu.Lock()
	s.mu.Lock()
	s.socks = append(s.socks, k)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.socks = slices.DeleteFunc(s.socks, func(o *sock) bool { return o == k })
		s.mu.Unlock()
	}()
	err := websocket.JSON.Send(ws, map[string]any{keyType: "hello", "num_connections": 1,
		"connection_info": map[string]string{"app_id": "A0SLACKTEST"},
		"debug_info":      map[string]any{"host": "slacktest", "approximate_connection_time": 18060}})
	k.wmu.Unlock()
	if err != nil {
		return
	}
	for {
		var ack struct {
			EnvelopeID string `json:"envelope_id"`
		}
		if err := websocket.JSON.Receive(ws, &ack); err != nil {
			return
		}
		k.mu.Lock()
		if c, ok := k.acks[ack.EnvelopeID]; ok {
			close(c)
			delete(k.acks, ack.EnvelopeID)
		}
		k.mu.Unlock()
	}
}

// answer writes a Web API answer.
func answer(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// fail is Slack's refusal with code.
func fail(code string) map[string]any { return map[string]any{"ok": false, "error": code} }

// ok is a success with fields.
func ok(fields map[string]any) map[string]any {
	out := map[string]any{"ok": true}
	maps.Copy(out, fields)
	return out
}

// serve serves one Web API call.
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	method := strings.TrimPrefix(r.URL.Path, "/api/")
	if err := r.ParseForm(); err != nil {
		answer(w, http.StatusOK, fail("invalid_form_data"))
		return
	}
	s.mu.Lock()
	s.calls[method]++
	held, hold := s.holds[method]
	s.mu.Unlock()
	if hold {
		select {
		case <-held:
		case <-r.Context().Done():
			return
		}
	}
	s.mu.Lock()
	if s.limited > 0 {
		s.limited--
		s.mu.Unlock()
		w.Header().Set("Retry-After", "0")
		answer(w, http.StatusTooManyRequests, fail("ratelimited"))
		return
	}
	scope, known := Scopes[method]
	if !known {
		s.mu.Unlock()
		answer(w, http.StatusNotFound, fail("unknown_method"))
		return
	}
	if r.Form.Get("token") != "" {
		// Slack takes a token in the form, but the adapter sends it only in
		// the header, and apps.connections.open refuses it there.
		s.mu.Unlock()
		answer(w, http.StatusOK, fail("invalid_arguments"))
		return
	}
	bearer, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	cred := s.tokens[bearer]
	switch {
	case !found || bearer == "":
		s.mu.Unlock()
		answer(w, http.StatusOK, fail("not_authed"))
		return
	case cred == nil:
		s.mu.Unlock()
		answer(w, http.StatusOK, fail("invalid_auth"))
		return
	case cred.app != (method == appMethod):
		s.mu.Unlock()
		answer(w, http.StatusOK, fail("not_allowed_token_type"))
		return
	case scope != "" && !cred.scopes[scope]:
		provided := strings.Join(slices.Sorted(maps.Keys(cred.scopes)), ",")
		s.mu.Unlock()
		answer(w, http.StatusOK, map[string]any{"ok": false, "error": "missing_scope", "needed": scope,
			"provided": provided})
		return
	}
	body, event := s.handle(method, cred.user, r.Form)
	s.mu.Unlock()
	if event != nil {
		if err := s.deliver(event); err != nil {
			answer(w, http.StatusInternalServerError, fail(err.Error()))
			return
		}
	}
	answer(w, http.StatusOK, body)
}

// handle answers method as user, and gives the event it makes; s.mu is
// held.
func (s *Server) handle(method, user string, form url.Values) (map[string]any, map[string]any) {
	ch := s.channels[form.Get(keyChannel)]
	member := ch != nil && ch.members[user]
	switch method {
	case "auth.test":
		return ok(map[string]any{"user_id": user, "bot_id": s.botID, "team_id": "T0SLACKTEST",
			"url": "https://slacktest.example/"}), nil
	case appMethod:
		ticket := rand.Text()
		s.tickets[ticket] = true
		s.opens++
		path := "/link"
		if s.refuse > 0 {
			// No socket is served there: the handshake gets a 404.
			s.refuse--
			path = "/refused"
		}
		return ok(map[string]any{"url": "ws" + strings.TrimPrefix(s.srv.URL, "http") + path + "?ticket=" + ticket}),
			nil
	case "users.conversations":
		return s.listChannels(user, form), nil
	case "conversations.create":
		return s.createChannel(user, form), nil
	case "conversations.setTopic":
		if !member {
			return fail(errNotInChannel), nil
		}
		ch.topic = form.Get("topic")
		return ok(nil), nil
	case "conversations.invite":
		return s.invite(ch, member, user, form.Get("users")), nil
	case "chat.postMessage":
		if !member {
			return fail(errChannelNotFound), nil
		}
		if body, ok := blocksAllowed(form.Get("blocks")); !ok {
			return body, nil
		}
		m, err := s.say(ch, user, s.botID, form.Get("text"), form.Get("blocks"), form.Get("thread_ts"))
		if err != nil {
			return fail(err.Error()), nil
		}
		return ok(map[string]any{keyChannel: ch.id, "ts": m.ts, typeMessage: messageJSON(ch, m)}), messageEvent(ch.id, m)
	case "chat.update":
		return s.update(ch, member, user, form)
	case "reactions.add":
		return s.react(ch, member, user, form), nil
	case "conversations.history", "conversations.replies":
		if !member {
			return fail(errChannelNotFound), nil
		}
		return s.history(ch, method, form), nil
	}
	return fail("unknown_method"), nil
}

// blocksAllowed checks blocks as Slack does: a JSON array of at most 50
// section blocks whose text is at most 3000 characters. It also refuses a
// section whose mrkdwn is not verbatim, which Slack takes but which turns
// `@here`, `@channel`, `@everyone` and `#name` in the text into mentions
// and links: the adapter must never send one.
func blocksAllowed(raw string) (map[string]any, bool) {
	if raw == "" {
		return nil, true
	}
	var blocks []struct {
		Type string `json:"type"`
		Text struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Verbatim bool   `json:"verbatim"`
		} `json:"text"`
	}
	if err := json.Unmarshal([]byte(raw), &blocks); err != nil || len(blocks) > 50 {
		return fail("invalid_blocks"), false
	}
	for _, b := range blocks {
		if b.Type != "section" || b.Text.Type != "mrkdwn" || !b.Text.Verbatim || b.Text.Text == "" ||
			len([]rune(b.Text.Text)) > 3000 {
			return fail("invalid_blocks"), false
		}
	}
	return nil, true
}

// page is the slice of n items a listing's limit and cursor ask for, at
// most maxPage when it is set, and the next cursor.
func page(n int, form url.Values, maxPage int) (from, to int, next string) {
	limit, err := strconv.Atoi(form.Get("limit"))
	if err != nil || limit <= 0 {
		limit = 100
	}
	if maxPage > 0 {
		limit = min(limit, maxPage)
	}
	from, _ = strconv.Atoi(form.Get("cursor"))
	from = min(max(from, 0), n)
	to = min(from+limit, n)
	if to < n {
		next = strconv.Itoa(to)
	}
	return from, to, next
}

func (s *Server) listChannels(user string, form url.Values) map[string]any {
	if !strings.Contains(form.Get("types"), "private_channel") {
		return ok(map[string]any{"channels": []any{}})
	}
	var mine []map[string]any
	for _, id := range s.order {
		ch := s.channels[id]
		if ch.members[user] {
			mine = append(mine, map[string]any{"id": ch.id, keyName: ch.name, "is_private": true,
				"topic": map[string]string{"value": ch.topic}})
		}
	}
	from, to, next := page(len(mine), form, s.maxPage)
	return ok(map[string]any{"channels": mine[from:to], "response_metadata": map[string]string{"next_cursor": next}})
}

func (s *Server) createChannel(user string, form url.Values) map[string]any {
	name := form.Get("name")
	if !channelName.MatchString(name) {
		return fail("invalid_name_specials")
	}
	if form.Get("is_private") != "true" {
		// The fake holds private channels alone, as the adapter makes.
		return fail("invalid_arguments")
	}
	for _, ch := range s.channels {
		if ch.name == name {
			return fail("name_taken")
		}
	}
	ch := s.create(user, name)
	return ok(map[string]any{keyChannel: map[string]any{"id": ch.id, keyName: ch.name, "is_private": true,
		"topic": map[string]string{"value": ""}}})
}

func (s *Server) invite(ch *channel, member bool, user, users string) map[string]any {
	switch {
	case ch == nil:
		return fail(errChannelNotFound)
	case !member:
		return fail(errNotInChannel)
	case users == user:
		return fail("cant_invite_self")
	case ch.members[users]:
		return fail("already_in_channel")
	}
	if _, ok := s.users[users]; !ok {
		return fail("user_not_found")
	}
	ch.members[users] = true
	return ok(map[string]any{keyChannel: map[string]any{"id": ch.id, keyName: ch.name}})
}

func (s *Server) update(ch *channel, member bool, user string, form url.Values) (map[string]any, map[string]any) {
	if !member {
		return fail(errChannelNotFound), nil
	}
	m := find(ch, form.Get("ts"))
	switch {
	case m == nil:
		return fail(errMessageNotFound), nil
	case m.user != user:
		return fail("cant_update_message"), nil
	}
	blocks := form.Get("blocks")
	if blocks == "[]" {
		blocks = ""
	}
	if body, ok := blocksAllowed(blocks); !ok {
		return body, nil
	}
	ev := s.change(ch, m, form.Get("text"), blocks)
	return ok(map[string]any{keyChannel: ch.id, "ts": m.ts, keyText: m.text}), ev
}

func (s *Server) react(ch *channel, member bool, user string, form url.Values) map[string]any {
	if !member {
		return fail(errChannelNotFound)
	}
	m := find(ch, form.Get("timestamp"))
	name := form.Get("name")
	switch {
	case m == nil:
		return fail(errMessageNotFound)
	case !emojiName.MatchString(name):
		return fail("invalid_name")
	}
	for _, r := range m.reactions {
		if r.name == name && r.user == user {
			return fail("already_reacted")
		}
	}
	m.reactions = append(m.reactions, reaction{name: name, user: user})
	return ok(nil)
}

// history answers conversations.history (the channel's messages outside
// threads and the threads' roots, newest first) and conversations.replies
// (a thread, its root first), each after oldest when it is given.
func (s *Server) history(ch *channel, method string, form url.Values) map[string]any {
	oldest := form.Get("oldest")
	after := func(ts string) bool { return oldest == "" || tsLess(oldest, ts) }
	var msgs []map[string]any
	if method == methodHistory {
		for _, m := range slices.Backward(ch.messages) {
			if (m.threadTS == "" || m.threadTS == m.ts) && after(m.ts) {
				msgs = append(msgs, messageJSON(ch, m))
			}
		}
	} else {
		root := form.Get("ts")
		if find(ch, root) == nil {
			return fail("thread_not_found")
		}
		for _, m := range ch.messages {
			if (m.ts == root || m.threadTS == root) && after(m.ts) {
				msgs = append(msgs, messageJSON(ch, m))
			}
		}
	}
	from, to, next := page(len(msgs), form, s.maxPage)
	return ok(map[string]any{"messages": msgs[from:to], "has_more": next != "",
		"response_metadata": map[string]string{"next_cursor": next}})
}

// tsLess is whether ts a is before ts b.
func tsLess(a, b string) bool {
	as, af, _ := strings.Cut(a, ".")
	bs, bf, _ := strings.Cut(b, ".")
	if len(as) != len(bs) {
		return len(as) < len(bs)
	}
	if as != bs {
		return as < bs
	}
	return (af + "000000")[:6] < (bf + "000000")[:6]
}
