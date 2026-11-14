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
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"

	"github.com/JairJosafath/theos/internal/ports"
)

// logCapture is a logger whose lines the test reads.
type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func (l *logCapture) logger() logr.Logger {
	return funcr.New(func(prefix, args string) {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.lines = append(l.lines, prefix+" "+args)
	}, funcr.Options{})
}

func (l *logCapture) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// TestFileShareIsAMessage covers #234: a message with a file attached
// (subtype file_share) is a person's message, so a command typed with a
// file is read; the other subtypes stay dropped.
func TestFileShareIsAMessage(t *testing.T) {
	event := func(subtype string) event {
		return event{Type: "message", Subtype: subtype, Channel: "C0000001", User: "U0000001", Text: "approve",
			TS: "1700000000.000100"}
	}
	for _, tt := range []struct {
		subtype string
		want    bool
	}{{"", true}, {"thread_broadcast", true}, {"file_share", true}, {"bot_message", false},
		{"channel_join", false}, {"message_deleted", false}} {
		_, ev, ok := chatEvent(event(tt.subtype), "C0000001", "U0000099", "")
		if ok != tt.want || (ok && ev.Text != "approve") {
			t.Errorf("chatEvent of subtype %q = %+v, %v; want read %v", tt.subtype, ev, ok, tt.want)
		}
	}

	// And over the socket, from the fake workspace.
	x := newRooms(t)
	since := sync0(t, x.c, "").Next
	if err := x.ws.SendEvent(map[string]any{"type": "message", "subtype": "file_share", "channel": x.specs,
		"user": x.ada, "text": "approve", "ts": "1700000000.000200", "files": []map[string]string{{"id": "F0000001"}}}); err != nil {
		t.Fatal(err)
	}
	got := sync0(t, x.c, since)
	wantIDs(t, "Sync after a message with a file", got.Events, x.specs+"/1700000000.000200")
}

// TestUnreadableFrameIsLoggedAndCounted covers #234: a frame that does not
// parse is dropped with a log line and a count, and the line holds no part
// of the frame; the socket goes on.
func TestUnreadableFrameIsLoggedAndCounted(t *testing.T) {
	x := newRooms(t)
	logs := &logCapture{}
	ctx := logr.NewContext(t.Context(), logs.logger())
	since, err := x.c.Sync(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	marker := rand.Text()
	// Not JSON at all, then JSON of the wrong shape, then an envelope whose
	// payload is not an event callback.
	for _, raw := range []string{`{"type": "events_api", "payload": ` + marker, `[` + strconv.Quote(marker) + `]`,
		`{"type": "events_api", "payload": {"event_id": 7, "note": "` + marker + `"}}`} {
		if err := x.ws.SendRaw(raw); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "three unreadable frames to be counted", func() bool {
		x.c.feed.mu.Lock()
		defer x.c.feed.mu.Unlock()
		return x.c.feed.unreadable >= 3
	})
	if out := logs.text(); !strings.Contains(out, "could not be read") || strings.Contains(out, marker) {
		t.Errorf("the log = %q, want a line that a frame could not be read, without the frame", out)
	}
	// The socket stays open and still delivers.
	x.post(t, x.specs, "approve", "")
	got, err := x.c.Sync(ctx, since.Next, 0)
	if err != nil || len(got.Events) != 1 {
		t.Errorf("Sync after unreadable frames = %+v, %v; want the one message", got, err)
	}

	// A payload that is JSON but not an event_callback's shape.
	c, err := New(ports.ChatConfig{Address: "http://slack.invalid/api", Token: rand.Text()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	c.feed.useLog(ctx)
	c.receive(json.RawMessage(`{"event_id": 7}`))
	c.feed.mu.Lock()
	n := c.feed.unreadable
	c.feed.mu.Unlock()
	if n != 1 {
		t.Errorf("a payload of the wrong shape counted %d unreadable frames, want 1", n)
	}
}

// TestSocketOriginHasNoPath covers #234: the Origin of the socket's
// handshake is the scheme and host of the API address, not the address.
func TestSocketOriginHasNoPath(t *testing.T) {
	x := newWorkspace(t)
	if _, err := x.c.Sync(t.Context(), "", 0); err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSuffix(x.ws.URL(), "/api")
	if got := x.ws.Origins(); len(got) != 1 || got[0] != want {
		t.Errorf("the Origin of the socket = %q, want [%q]", got, want)
	}
	for base, want := range map[string]string{
		"https://slack.com/api":         "https://slack.com",
		"https://gov.slack.test:8443/a": "https://gov.slack.test:8443",
		"http://127.0.0.1:1/api/v2":     "http://127.0.0.1:1",
	} {
		c := &Client{base: base}
		if got := c.origin(); got != want {
			t.Errorf("origin of %q = %q, want %q", base, got, want)
		}
	}
}

// formsOf serves chat.postMessage and chat.update, and records each form.
func formsOf(t *testing.T) (*Client, func() url.Values) {
	t.Helper()
	var mu sync.Mutex
	var last url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		last = r.PostForm
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ts": "1700000000.000100"})
	}))
	t.Cleanup(srv.Close)
	c, err := New(ports.ChatConfig{Address: srv.URL, Token: rand.Text()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c, func() url.Values {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

// TestPostWithoutMrkdwnSendsNoBlocks covers #234: a post with no mrkdwn
// leaves the blocks field out, instead of sending an empty array; a post
// of one too long for blocks does the same. An edit has to clear what the
// message held, so it sends the empty array.
func TestPostWithoutMrkdwnSendsNoBlocks(t *testing.T) {
	c, last := formsOf(t)
	ctx := t.Context()
	tooLong := strings.Repeat("a", maxSectionText*(maxBlocks+1))
	for _, tt := range []struct {
		name   string
		do     func() error
		blocks string // "" for none
		text   string
	}{
		{"send, no mrkdwn", func() error { _, err := c.Send(ctx, "C1", ports.ChatMessage{Text: "hi"}); return err }, "", "hi"},
		{"reply, no mrkdwn", func() error { _, err := c.Reply(ctx, "C1", "1.1", ports.ChatMessage{Text: "hi"}); return err }, "", "hi"},
		{"send, too long for blocks", func() error {
			_, err := c.Send(ctx, "C1", ports.ChatMessage{Text: "hi", Formatted: tooLong})
			return err
		}, "", tooLong},
		{"edit, no mrkdwn", func() error { _, err := c.Edit(ctx, "C1", "1.1", ports.ChatMessage{Text: "hi"}); return err }, "[]", "hi"},
	} {
		if err := tt.do(); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		form := last()
		got, has := form["blocks"]
		switch {
		case tt.blocks == "" && has:
			t.Errorf("%s sent blocks %q, want the field left out", tt.name, got)
		case tt.blocks != "" && (!has || got[0] != tt.blocks):
			t.Errorf("%s sent blocks %q, want %q", tt.name, got, tt.blocks)
		}
		if form.Get("text") != tt.text {
			t.Errorf("%s sent text %q, want %q", tt.name, form.Get("text"), tt.text)
		}
	}
	if _, err := c.Send(ctx, "C1", ports.ChatMessage{Text: "hi", Formatted: "*hi*"}); err != nil || last().Get("blocks") == "" {
		t.Errorf("a post with mrkdwn sent blocks %q (%v), want its sections", last().Get("blocks"), err)
	}
}

// TestMissingArgsIsNoRefusedToken covers #234: missing_args is a malformed
// request, not a token Slack refuses; the other codes of a refused token
// stay refused, which #251's per-invitee classification relies on.
func TestMissingArgsIsNoRefusedToken(t *testing.T) {
	for code, refused := range map[string]bool{"missing_args": false, "invalid_auth": true, "not_authed": true,
		"account_inactive": true, "token_revoked": true, "token_expired": true, "not_allowed_token_type": true,
		"channel_not_found": false} {
		err := &Error{Method: "conversations.invite", Status: http.StatusOK, Code: code}
		if got := errors.Is(err, ports.ErrChatCredentialRefused); got != refused {
			t.Errorf("%s is a refused token = %v, want %v", code, got, refused)
		}
	}
	// Invite: the code stays an error on the call, naming no person.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "missing_args"})
	}))
	defer srv.Close()
	c, err := New(ports.ChatConfig{Address: srv.URL, Token: rand.Text()})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Invite(t.Context(), "C1", "U0000001")
	if _, ok := errors.AsType[*ports.ChatInviteRefusedError](err); ok || err == nil ||
		errors.Is(err, ports.ErrChatCredentialRefused) {
		t.Errorf("Invite answered missing_args = %v, want a plain error", err)
	}
}

// TestRetryAfterIsClamped covers #234: a Retry-After of more seconds than a
// time.Duration holds is clamped, never overflowed into a negative wait.
func TestRetryAfterIsClamped(t *testing.T) {
	for _, tt := range []struct {
		header string
		want   time.Duration
	}{
		{"", time.Second},
		{"abc", time.Second},
		{"-5", time.Second},
		{"0", 0},
		{"7", 7 * time.Second},
		{"9223372036", 24 * time.Hour},
		{"9223372037", 24 * time.Hour},
		{"9223372036854775807", 24 * time.Hour},
		{"99999999999999999999999999", 24 * time.Hour},
		{"-99999999999999999999999999", time.Second},
	} {
		h := http.Header{}
		if tt.header != "" {
			h.Set("Retry-After", tt.header)
		}
		if got := retryAfter(h); got != tt.want {
			t.Errorf("retryAfter(%q) = %v, want %v", tt.header, got, tt.want)
		}
	}
}

// fill buffers n messages in f, each with its own key, from ts 1700000000.
func fill(f *feed, from, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := from; i < from+n; i++ {
		ts := strconv.Itoa(1700000000+i) + ".000000"
		f.add("C1/"+ts, ports.ChatEvent{Room: "C1", ID: ts, Sender: "U1", Text: "x", Time: time.Unix(int64(1700000000+i), 0)})
	}
}

// TestFullBufferDoesNotCloneOnEveryAdd covers #234: once the buffer holds
// maxBuffered messages, an add drops the oldest without copying the
// buffer, which was an allocation of the whole buffer on every add.
func TestFullBufferDoesNotCloneOnEveryAdd(t *testing.T) {
	f := newFeed()
	defer f.close()
	fill(f, 0, maxBuffered)
	// Bytes, not allocations: each add allocates a little for its own wake
	// channel, but a clone of the buffer is megabytes.
	const adds = 100
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	fill(f, maxBuffered, adds)
	runtime.ReadMemStats(&after)
	if perAdd := (after.TotalAlloc - before.TotalAlloc) / adds; perAdd > 100<<10 {
		t.Errorf("an add to a full buffer allocated %d bytes on average, want a few hundred (no clone of the buffer)", perAdd)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.buf) != maxBuffered || len(f.keys) != maxBuffered {
		t.Fatalf("the buffer holds %d messages and %d keys, want %d of each", len(f.buf), len(f.keys), maxBuffered)
	}
	if first := f.buf[0].seq; first != f.seq-maxBuffered+1 {
		t.Errorf("the oldest message kept has seq %d, want %d: the oldest go first", first, f.seq-maxBuffered+1)
	}
	if _, ok := f.keys["C1/1700000000.000000"]; ok {
		t.Error("the oldest message's key is still held after it was dropped")
	}
}

// TestReaderToldWhenMessagesWereLost covers #234: a reader whose position
// the buffer dropped reads from the oldest message kept, and Sync logs how
// many messages it missed.
func TestReaderToldWhenMessagesWereLost(t *testing.T) {
	x := newWorkspace(t)
	logs := &logCapture{}
	ctx := logr.NewContext(t.Context(), logs.logger())
	since, err := x.c.Sync(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	fill(x.c.feed, 0, maxBuffered+5)
	got, err := x.c.Sync(ctx, since.Next, 0)
	if err != nil || len(got.Events) != maxBuffered {
		t.Fatalf("Sync from a dropped position = %d events, %v; want the %d kept", len(got.Events), err, maxBuffered)
	}
	if out := logs.text(); !strings.Contains(out, "dropped from the Slack buffer") || !strings.Contains(out, `"lost"=5`) {
		t.Errorf("the log = %q, want a line that 5 messages were lost", out)
	}
	// A reader that is up to date is told of nothing.
	before := logs.text()
	if _, err := x.c.Sync(ctx, got.Next, 0); err != nil || logs.text() != before {
		t.Errorf("Sync from the newest position logged %q (%v), want nothing new", strings.TrimPrefix(logs.text(), before), err)
	}
}
