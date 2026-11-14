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
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/net/websocket"

	"github.com/JairJosafath/theos/internal/ports"
)

// Sync on a socket (docs/plans/requests-and-chat.md, decisions 7 and 8).
//
// Slack pushes each event once to one of the app's open sockets and wants
// it acknowledged; it may send one again. The adapter buffers people's
// messages in the order they arrive, each once by its channel and ts, and
// drops an envelope whose event_id it has seen. Socket Mode promises no
// order across channels, so a token is a position in the buffer, with the
// newest message time returned for the catch-up read alone. A read empties
// nothing: Chats with the same tokens share one Client, each with its own
// since.
//
// What was said while no socket was open is read through the Web API when
// one opens: on the first open after a restart, from the token's time; after
// a redial, from a minute before the newest time heard.

// Bounds of the buffer.
const (
	// maxBuffered is how many messages the buffer holds; the oldest go
	// first.
	maxBuffered = 10000
	// maxSeenEvents is how many event IDs are remembered to drop a delivery
	// sent again.
	maxSeenEvents = 10000
)

// Times of the socket.
const (
	// helloWait bounds the wait for Slack's hello on a new socket.
	helloWait = 30 * time.Second
	// redialWait bounds a redial after Slack's disconnect, catch-up included.
	redialWait = 2 * time.Minute
	// overlap is how far before the newest time heard a catch-up after a
	// redial reads: the clocks of Slack and of the cluster are not one.
	overlap = time.Minute
)

// syncPrefix starts every sync token the adapter gives.
const syncPrefix = "slack1"

// buffered is a message in the buffer, at seq.
type buffered struct {
	seq uint64
	key string
	ev  ports.ChatEvent
}

// feed is what a Client holds of the socket and the buffer.
type feed struct {
	// epoch names this buffer in every token: a token of another (a Client
	// before a restart) holds a position that means nothing here.
	epoch string
	// ctx ends when the Client is closed; redials run under it.
	ctx    context.Context
	cancel context.CancelFunc

	// dial holds a token while a socket is opened, so one opens at a time;
	// a channel, so that a Sync waiting for it heeds its ctx.
	dial chan struct{}

	mu      sync.Mutex
	conn    *websocket.Conn
	caught  bool      // the open socket's first catch-up is read into the buffer
	gen     uint64    // counts the sockets opened
	opened  bool      // a socket was ever open
	floor   time.Time // the buffer holds every message after it
	heard   time.Time // when the last frame came, on the cluster's clock
	pending time.Time // a catch-up that failed, to read again on the next open
	closed  bool

	// log is the logger of the latest Sync's context: the socket's reader
	// has no context of its own. It is never given a frame's content, which
	// may hold what people wrote.
	log        logr.Logger
	unreadable int // frames dropped for not parsing, since the Client was made

	buf    []buffered
	seq    uint64            // the seq of the newest message buffered
	keys   map[string]uint64 // channel/ts → seq, of the messages buffered
	newest time.Time         // the newest message time buffered
	seen   map[string]bool   // event IDs delivered
	ring   []string          // seen, oldest first
	wake   chan struct{}     // closed when a message is buffered or the socket goes
}

func newFeed() *feed {
	ctx, cancel := context.WithCancel(context.Background())
	return &feed{epoch: strings.ToLower(rand.Text()[:10]), ctx: ctx, cancel: cancel,
		keys: map[string]uint64{}, seen: map[string]bool{}, wake: make(chan struct{}), dial: make(chan struct{}, 1)}
}

// lockDial takes f.dial, or gives ctx's error when ctx ends first.
func (f *feed) lockDial(ctx context.Context) error {
	select {
	case f.dial <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// unlockDial gives f.dial back.
func (f *feed) unlockDial() { <-f.dial }

// close closes the socket and keeps it closed.
func (f *feed) close() {
	f.cancel()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	if f.conn != nil {
		_ = f.conn.Close()
		f.conn, f.caught = nil, false
	}
	f.notify()
}

// notify wakes every Sync that waits; f.mu is held.
func (f *feed) notify() {
	close(f.wake)
	f.wake = make(chan struct{})
}

// token is a position and a time as a Sync token.
type token struct {
	epoch string
	pos   uint64
	time  time.Time
}

func (t token) String() string {
	return fmt.Sprintf("%s.%s.%d.%d", syncPrefix, t.epoch, t.pos, t.time.UnixMicro())
}

// parseToken reads a token the adapter gave.
func parseToken(s string) (token, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 4 || parts[0] != syncPrefix {
		return token{}, fmt.Errorf("slack: %q is no sync token of this adapter", s)
	}
	pos, err1 := strconv.ParseUint(parts[2], 10, 64)
	micros, err2 := strconv.ParseInt(parts[3], 10, 64)
	if err1 != nil || err2 != nil || parts[1] == "" {
		return token{}, fmt.Errorf("slack: %q is no sync token of this adapter", s)
	}
	return token{epoch: parts[1], pos: pos, time: time.UnixMicro(micros).UTC()}, nil
}

// tsTime is a message's ts as a time: seconds and microseconds.
func tsTime(ts string) (time.Time, bool) {
	sec, frac, _ := strings.Cut(ts, ".")
	s, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	var us int64
	if frac != "" {
		frac = (frac + "000000")[:6]
		if us, err = strconv.ParseInt(frac, 10, 64); err != nil {
			return time.Time{}, false
		}
	}
	return time.Unix(s, us*1000).UTC(), true
}

// tsOf is t as a ts, for oldest.
func tsOf(t time.Time) string {
	return fmt.Sprintf("%d.%06d", t.Unix(), t.Nanosecond()/1000)
}

// Sync returns what people said since the token since, each message once
// and none of the bot's own, waiting up to wait for one. The first Sync
// opens the socket; one that finds it closed opens it again.
func (c *Client) Sync(ctx context.Context, since string, wait time.Duration) (ports.ChatSync, error) {
	if _, err := c.WhoAmI(ctx); err != nil {
		return ports.ChatSync{}, err
	}
	var tok token
	if since != "" {
		var err error
		if tok, err = parseToken(since); err != nil {
			return ports.ChatSync{}, err
		}
	}
	f := c.feed
	f.useLog(ctx)
	if err := c.open(ctx, since, tok); err != nil {
		return ports.ChatSync{}, err
	}
	switch {
	case since == "":
		// The first Sync marks where the rooms stand.
		f.mu.Lock()
		next := token{epoch: f.epoch, pos: f.seq, time: later(f.newest, time.Now())}
		f.mu.Unlock()
		return ports.ChatSync{Next: next.String()}, nil
	case tok.epoch != f.epoch:
		return c.syncForeign(ctx, tok)
	}
	deadline := time.Now().Add(wait)
	for {
		f.mu.Lock()
		events, end, lost := f.after(tok.pos)
		wake, closed, log := f.wake, f.closed, f.log
		f.mu.Unlock()
		if lost > 0 {
			// The reader was away while more came in than the buffer
			// holds: what it missed is not read again (#234).
			log.Info("Messages were dropped from the Slack buffer before they were read",
				"lost", lost, "kept", maxBuffered)
		}
		left := time.Until(deadline)
		if len(events) > 0 || wait <= 0 || left <= 0 || closed {
			return ports.ChatSync{Next: next(f.epoch, end, tok.time, events).String(), Events: events}, nil
		}
		timer := time.NewTimer(left)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ports.ChatSync{}, ctx.Err()
		case <-wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// later is the later of a and b.
func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// next is the token after events, read up to end from a token of time t.
func next(epoch string, end uint64, t time.Time, events []ports.ChatEvent) token {
	for _, e := range events {
		t = later(t, e.Time)
	}
	return token{epoch: epoch, pos: end, time: t}
}

// useLog keeps the logger of ctx, if it has one, for the socket's reader and
// for what Sync finds lost.
func (f *feed) useLog(ctx context.Context) {
	if l := logr.FromContextOrDiscard(ctx); l.GetSink() != nil {
		f.mu.Lock()
		f.log = l
		f.mu.Unlock()
	}
}

// after is the buffer's messages after pos, a room's oldest first, the
// position of the last, and how many messages after pos the buffer dropped
// before they were read; f.mu is held. A position the buffer dropped reads
// from its oldest message.
func (f *feed) after(pos uint64) ([]ports.ChatEvent, uint64, int) {
	i, _ := slices.BinarySearchFunc(f.buf, pos+1, func(b buffered, seq uint64) int { return cmp.Compare(b.seq, seq) })
	events := make([]ports.ChatEvent, 0, len(f.buf)-i)
	for _, b := range f.buf[i:] {
		events = append(events, b.ev)
	}
	sortByTime(events)
	lost := 0
	if len(f.buf) > 0 && f.buf[0].seq > pos+1 {
		lost = int(f.buf[0].seq - pos - 1)
	}
	return events, max(pos, f.seq), lost
}

// sortByTime orders events by time, which keeps each room's oldest first.
func sortByTime(events []ports.ChatEvent) {
	slices.SortStableFunc(events, func(a, b ports.ChatEvent) int { return a.Time.Compare(b.Time) })
}

// syncForeign answers a token of another buffer, one given before a
// restart: what is buffered after its time, and what Slack holds after it
// that the buffer has not, when the buffer starts later. What the read
// finds after the buffer's floor goes into the buffer, which is meant to
// hold all of it, so that its event, still on its way, is dropped as a
// repeat; only what is at or before the floor is this reader's alone.
func (c *Client) syncForeign(ctx context.Context, tok token) (ports.ChatSync, error) {
	f := c.feed
	f.mu.Lock()
	floor := f.floor
	f.mu.Unlock()
	var read []buffered
	if tok.time.Before(floor) {
		var err error
		if read, err = c.catchUp(ctx, tok.time); err != nil {
			return ports.ChatSync{}, err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var events []ports.ChatEvent
	for _, b := range read {
		switch _, ok := f.keys[b.key]; {
		case b.ev.Time.After(f.floor):
			f.add(b.key, b.ev)
		case !ok:
			events = append(events, b.ev)
		}
	}
	for _, b := range f.buf {
		if b.ev.Time.After(tok.time) {
			events = append(events, b.ev)
		}
	}
	sortByTime(events)
	return ports.ChatSync{Next: next(f.epoch, f.seq, tok.time, events).String(), Events: events}, nil
}

// open opens the socket unless one is open. The first open after a restart
// reads what was said after since's time; an open after the socket went
// down reads from a minute before the newest time heard.
func (c *Client) open(ctx context.Context, since string, tok token) error {
	f := c.feed
	// An open socket whose catch-up is read serves, even while a redial
	// after Slack's disconnect holds f.dial. One whose catch-up is still
	// being read does not: until it is, the buffer is not whole, and a
	// position in it would skip what the catch-up adds (#155).
	f.mu.Lock()
	closed, open := f.closed, f.conn != nil && f.caught
	f.mu.Unlock()
	if closed {
		return errors.New("slack: the client is closed")
	}
	if open {
		return nil
	}
	if err := f.lockDial(ctx); err != nil {
		return err
	}
	defer f.unlockDial()
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return errors.New("slack: the client is closed")
	}
	if f.conn != nil {
		f.mu.Unlock()
		return nil
	}
	first := !f.opened
	from := f.catchUpFrom()
	f.mu.Unlock()
	if first {
		from = time.Time{}
		if since != "" && tok.epoch != f.epoch {
			from = tok.time
		}
	}
	return c.connect(ctx, nil, first, from)
}

// catchUpFrom is where a catch-up after a redial reads from; f.mu is held.
// It is never before the floor: the buffer holds every message after it,
// and one before it may have been given already, by a token of before a
// restart or as the mark of a first Sync.
func (f *feed) catchUpFrom() time.Time {
	from := later(f.newest, f.heard).Add(-overlap)
	if !f.pending.IsZero() && f.pending.Before(from) {
		from = f.pending
	}
	return later(from, f.floor)
}

// connect asks apps.connections.open for an address, dials it, waits for
// Slack's hello and reads what was said from from, unless from is zero.
// The old socket, when there is one, is closed once the new one is open.
// f.dial is held.
func (c *Client) connect(ctx context.Context, old *websocket.Conn, first bool, from time.Time) error {
	f := c.feed
	ws, err := c.dialSocket(ctx)
	if err != nil {
		return err
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		_ = ws.Close()
		return errors.New("slack: the client is closed")
	}
	f.gen++
	gen := f.gen
	f.conn, f.opened, f.heard = ws, true, time.Now()
	// caught speaks of an open socket: set it with conn when no catch-up is
	// needed, and after a catch-up only if this socket is still the open one,
	// so that one which dropped meanwhile stays not caught (#155).
	if from.IsZero() {
		f.caught = true
	}
	if first {
		f.floor = from
		if from.IsZero() {
			f.floor = time.Now()
		}
	}
	f.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	go c.read(ws, gen)
	if from.IsZero() {
		return nil
	}
	read, err := c.catchUp(ctx, from)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil {
		// Read it again on the next open: close this socket, so that the
		// next Sync opens one.
		f.pending = from
		if f.conn == ws {
			f.conn, f.caught = nil, false
		}
		_ = ws.Close()
		f.notify()
		return fmt.Errorf("slack: reading what was said while no socket was open: %w", err)
	}
	f.pending = time.Time{}
	for _, b := range read {
		f.add(b.key, b.ev)
	}
	f.caught = f.conn == ws
	return nil
}

// dialSocket opens a socket at the address apps.connections.open gives for
// the app-level token, and reads Slack's hello.
func (c *Client) dialSocket(ctx context.Context) (*websocket.Conn, error) {
	if c.appToken == "" {
		return nil, errors.New("slack: no app-level token to open a socket with")
	}
	var answer struct {
		URL string `json:"url"`
	}
	if err := c.call(ctx, c.appToken, "apps.connections.open", nil, &answer); err != nil {
		return nil, err
	}
	// The address holds the socket's ticket, a credential: no error names
	// more of it than its host, as theos-chat logs what Sync returns.
	cfg, err := websocket.NewConfig(answer.URL, c.origin())
	if err != nil {
		return nil, errors.New("slack: apps.connections.open gave no socket address the adapter can dial")
	}
	ws, err := cfg.DialContext(ctx)
	if err != nil {
		if dialErr, ok := errors.AsType[*websocket.DialError](err); ok {
			err = dialErr.Err
		}
		return nil, fmt.Errorf("slack: dialling the socket at %s: %w", cfg.Location.Host, err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(helloWait))
	var hello frame
	if err := websocket.JSON.Receive(ws, &hello); err != nil || hello.Type != "hello" {
		_ = ws.Close()
		return nil, fmt.Errorf("slack: the socket said no hello (%q): %v", hello.Type, err)
	}
	_ = ws.SetReadDeadline(time.Time{})
	return ws, nil
}

// origin is the Origin the socket's handshake sends: the scheme and host of
// the API address, with no path (#234).
func (c *Client) origin() string {
	u, err := url.Parse(c.base)
	if err != nil {
		return c.base
	}
	return u.Scheme + "://" + u.Host
}

// frame is a message on the socket.
type frame struct {
	Type       string          `json:"type"`
	EnvelopeID string          `json:"envelope_id"`
	Reason     string          `json:"reason"`
	Payload    json.RawMessage `json:"payload"`
}

// read reads socket gen until it closes: it buffers each event and then
// acknowledges its envelope, and answers Slack's disconnect with a new
// socket.
func (c *Client) read(ws *websocket.Conn, gen uint64) {
	f := c.feed
	for {
		var fr frame
		if err := websocket.JSON.Receive(ws, &fr); err != nil {
			if unparsed(err) {
				f.dropUnreadable()
				continue
			}
			f.down(gen)
			return
		}
		f.mu.Lock()
		f.heard = time.Now()
		f.mu.Unlock()
		switch fr.Type {
		case "disconnect":
			go c.redial(ws, gen)
			continue
		case "events_api":
			c.receive(fr.Payload)
		}
		if fr.EnvelopeID != "" {
			_ = websocket.JSON.Send(ws, map[string]string{"envelope_id": fr.EnvelopeID})
		}
	}
}

// unparsed says err is a frame that arrived whole and is not JSON of the
// shape of a frame, not a socket that failed.
func unparsed(err error) bool {
	_, syntax := errors.AsType[*json.SyntaxError](err)
	_, typed := errors.AsType[*json.UnmarshalTypeError](err)
	return syntax || typed
}

// dropUnreadable counts and logs a frame that could not be read, which is
// dropped. The line names no part of it and quotes no decoder error: a frame
// holds what people wrote, and the decoder's errors quote it (#234).
func (f *feed) dropUnreadable() {
	f.mu.Lock()
	f.unreadable++
	n, log := f.unreadable, f.log
	f.mu.Unlock()
	log.Info("A frame on the Slack socket could not be read and was dropped", "unreadable", n)
}

// down marks socket gen closed, unless another took its place.
func (f *feed) down(gen uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gen == gen && f.conn != nil {
		_ = f.conn.Close()
		f.conn, f.caught = nil, false
		f.notify()
	}
}

// redial answers Slack's disconnect on socket gen: a new address, a new
// dial and a catch-up, then the old socket closed. When it fails, the
// socket is down and the next Sync opens one.
func (c *Client) redial(old *websocket.Conn, gen uint64) {
	f := c.feed
	ctx, cancel := context.WithTimeout(f.ctx, redialWait)
	defer cancel()
	if err := f.lockDial(ctx); err != nil {
		f.down(gen)
		return
	}
	defer f.unlockDial()
	f.mu.Lock()
	// The old socket may have closed already, as Slack closes it after a
	// disconnect; a socket another call opened since takes its place.
	current := f.gen == gen && (f.conn == old || f.conn == nil) && !f.closed
	from := f.catchUpFrom()
	f.mu.Unlock()
	if !current {
		return
	}
	if err := c.connect(ctx, old, false, from); err != nil {
		f.down(gen)
	}
}

// event is the part of a message event, or of a message from
// conversations.history, the adapter reads.
type event struct {
	Type     string `json:"type"`
	Subtype  string `json:"subtype"`
	Channel  string `json:"channel"`
	User     string `json:"user"`
	BotID    string `json:"bot_id"`
	Text     string `json:"text"`
	TS       string `json:"ts"`
	ThreadTS string `json:"thread_ts"`
	Edited   *struct {
		TS string `json:"ts"`
	} `json:"edited"`
	Message *event `json:"message"`
	// ReplyCount and LatestReply are a thread root's, in history.
	ReplyCount  int    `json:"reply_count"`
	LatestReply string `json:"latest_reply"`
}

// receive buffers the message event of an events_api payload, once by its
// event_id.
func (c *Client) receive(payload json.RawMessage) {
	var p struct {
		EventID string `json:"event_id"`
		Event   event  `json:"event"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		c.feed.dropUnreadable()
		return
	}
	me, botID := c.self()
	f := c.feed
	f.mu.Lock()
	defer f.mu.Unlock()
	if p.EventID != "" {
		if f.seen[p.EventID] {
			return
		}
		f.seen[p.EventID] = true
		f.ring = append(f.ring, p.EventID)
		if len(f.ring) > maxSeenEvents {
			delete(f.seen, f.ring[0])
			f.ring = f.ring[1:]
		}
	}
	if key, ev, ok := chatEvent(p.Event, p.Event.Channel, me, botID); ok {
		f.add(key, ev)
	}
}

// self is the bot's user ID and bot ID, as Sync asked them.
func (c *Client) self() (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.me, c.botID
}

// chatEvent is a person's message, or their edit of one, as the port names
// it, with its key in the buffer: false for the bot's own, a change that is
// no edit (a link unfurled) and every other subtype. A message with a file
// attached (file_share) is a person's message, its text what they typed
// with the file (#234).
func chatEvent(e event, channel, me, botID string) (string, ports.ChatEvent, bool) {
	own := func(m event) bool { return m.User == "" || m.User == me || (botID != "" && m.BotID == botID) }
	thread := func(m event) string {
		if m.ThreadTS != m.TS {
			return m.ThreadTS
		}
		return ""
	}
	switch e.Subtype {
	case "", "thread_broadcast", "file_share":
		t, ok := tsTime(e.TS)
		if !ok || own(e) || channel == "" {
			return "", ports.ChatEvent{}, false
		}
		return channel + "/" + e.TS, ports.ChatEvent{Room: channel, ID: e.TS, Sender: e.User, Text: Decode(e.Text),
			ThreadRoot: thread(e), Time: t}, true
	case "message_changed":
		m := e.Message
		if m == nil || m.Edited == nil || own(*m) || channel == "" {
			return "", ports.ChatEvent{}, false
		}
		t, ok := tsTime(m.Edited.TS)
		if !ok {
			return "", ports.ChatEvent{}, false
		}
		return channel + "/" + m.TS + "/" + m.Edited.TS, ports.ChatEvent{Room: channel, ID: m.TS, Sender: m.User,
			Text: Decode(m.Text), ThreadRoot: thread(*m), Replaces: m.TS, Time: t}, true
	}
	return "", ports.ChatEvent{}, false
}

// add buffers ev under key unless the key is there; f.mu is held.
func (f *feed) add(key string, ev ports.ChatEvent) {
	if _, ok := f.keys[key]; ok {
		return
	}
	f.seq++
	f.buf = append(f.buf, buffered{seq: f.seq, key: key, ev: ev})
	f.keys[key] = f.seq
	f.newest = later(f.newest, ev.Time)
	if len(f.buf) > maxBuffered {
		// Shift the slice: append makes room again once its end is reached,
		// copying what is kept once in many adds, not on every one (#234).
		drop := len(f.buf) - maxBuffered
		for _, b := range f.buf[:drop] {
			delete(f.keys, b.key)
		}
		clear(f.buf[:drop])
		f.buf = f.buf[drop:]
	}
	f.notify()
}

// catchUp reads through the Web API what people said after from in the
// bot's channels: each channel's history, and the replies of each thread
// among its last 200 messages, oldest first.
func (c *Client) catchUp(ctx context.Context, from time.Time) ([]buffered, error) {
	me, botID := c.self()
	chans, err := c.channels(ctx)
	if err != nil {
		return nil, err
	}
	var out []buffered
	// A thread's root after from is in the history and in its thread: it
	// is kept once.
	kept := map[string]bool{}
	keep := func(channel string, msgs []event) {
		for _, m := range msgs {
			key, ev, ok := chatEvent(m, channel, me, botID)
			if ok && ev.Time.After(from) && !kept[key] {
				kept[key] = true
				out = append(out, buffered{key: key, ev: ev})
			}
		}
	}
	for _, ch := range chans {
		msgs, err := c.history(ctx, ch.ID, from, true)
		if err != nil {
			return nil, err
		}
		keep(ch.ID, msgs)
		recent, err := c.history(ctx, ch.ID, time.Time{}, false)
		if err != nil {
			return nil, err
		}
		for _, root := range recent {
			if root.ReplyCount == 0 {
				continue
			}
			if latest, ok := tsTime(root.LatestReply); ok && !latest.After(from) {
				continue
			}
			replies, err := c.replies(ctx, ch.ID, root.TS, from)
			if err != nil {
				return nil, err
			}
			keep(ch.ID, replies)
		}
	}
	slices.SortStableFunc(out, func(a, b buffered) int { return a.ev.Time.Compare(b.ev.Time) })
	return out, nil
}

// history is channel's messages after from, every page when all is true,
// else its last page of 200.
func (c *Client) history(ctx context.Context, channel string, from time.Time, all bool) ([]event, error) {
	args := url.Values{argChannel: {channel}, argLimit: {strconv.Itoa(pageSize)}}
	if !from.IsZero() {
		args.Set("oldest", tsOf(from))
	}
	return c.messages(ctx, "conversations.history", args, all)
}

// replies is the thread of root in channel after from, every page.
func (c *Client) replies(ctx context.Context, channel, root string, from time.Time) ([]event, error) {
	return c.messages(ctx, "conversations.replies", url.Values{argChannel: {channel}, "ts": {root},
		"oldest": {tsOf(from)}, argLimit: {strconv.Itoa(pageSize)}}, true)
}

// messages reads a listing of messages, every page when all is true.
func (c *Client) messages(ctx context.Context, method string, args url.Values, all bool) ([]event, error) {
	var out []event
	for {
		var answer struct {
			page
			Messages []event `json:"messages"`
		}
		if err := c.bot(ctx, method, args, &answer); err != nil {
			return nil, err
		}
		out = append(out, answer.Messages...)
		if !all || answer.Meta.NextCursor == "" {
			return out, nil
		}
		args.Set("cursor", answer.Meta.NextCursor)
	}
}
