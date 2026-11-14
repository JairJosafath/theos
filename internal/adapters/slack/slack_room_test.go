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
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/JairJosafath/theos/internal/ports"
)

// failOnce is a transport that fails the first call of one Web API method,
// as a connection that drops does, and passes every other call on.
type failOnce struct {
	method string
	failed atomic.Bool
}

func (f *failOnce) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/"+f.method) && f.failed.CompareAndSwap(false, true) {
		return nil, errors.New("connection reset")
	}
	return http.DefaultTransport.RoundTrip(r)
}

// TestEnsureRoomFinishesAChannelLeftWithoutItsTopic covers #234 (for #159):
// when conversations.setTopic fails after conversations.create, the next
// EnsureRoom finds the channel with no topic. It sets the topic and says
// the channel Created, so the Chat controller invites its people, where it
// returned the channel as found before and nobody was invited. A channel
// with its topic is found as it is.
func TestEnsureRoomFinishesAChannelLeftWithoutItsTopic(t *testing.T) {
	w := newWorkspace(t, WithHTTPClient(&http.Client{Transport: &failOnce{method: "conversations.setTopic"}}))
	spec := ports.ChatRoomSpec{Alias: aliasSpecs, Topic: "A thread per spec waiting for approval"}
	if _, err := w.c.EnsureRoom(t.Context(), spec); err == nil {
		t.Fatal("EnsureRoom() with setTopic failing = nil error")
	}
	if got := w.ws.Calls("conversations.create"); got != 1 {
		t.Fatalf("conversations.create called %d times, want 1", got)
	}

	room, err := w.c.EnsureRoom(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !room.Created {
		t.Error("EnsureRoom() of the channel left without its topic: Created false, want true so its people are invited")
	}
	ch, ok := w.ws.Channel(room.ID)
	if !ok || ch.Topic != spec.Topic {
		t.Errorf("the channel's topic = %q, want %q", ch.Topic, spec.Topic)
	}
	if got := w.ws.Calls("conversations.create"); got != 1 {
		t.Errorf("conversations.create called %d times, want 1: the channel is found, not made again", got)
	}

	spec.ID = room.ID
	again, err := w.c.EnsureRoom(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if again.Created || again.ID != room.ID || again.Alias != "#"+aliasSpecs {
		t.Errorf("EnsureRoom() of the finished channel = %+v, want %s found, not Created", again, room.ID)
	}
	if got := w.ws.Calls("conversations.setTopic"); got != 1 {
		t.Errorf("conversations.setTopic reached Slack %d times, want once: the call that finished the channel", got)
	}
}

// TestEnsureRoomPutsBackATopicEmptiedByHand covers #159: a channel whose
// topic a member emptied by hand gets its topic back and is not Created,
// so the caller invites nobody, and nobody who left a channel is pulled
// back into it. Before, it was Created, and the Chat controller invited
// every person to all six channels, ada again into a channel she left.
func TestEnsureRoomPutsBackATopicEmptiedByHand(t *testing.T) {
	x := newRooms(t)
	bob := x.ws.NewUser("bob")
	if err := x.c.Invite(t.Context(), x.specs, bob); err != nil {
		t.Fatal(err)
	}
	if err := x.ws.Remove(x.specs, bob); err != nil {
		t.Fatal(err)
	}
	if err := x.ws.SetTopic(x.specs, ""); err != nil {
		t.Fatal(err)
	}
	spec := ports.ChatRoomSpec{ID: x.specs, Alias: aliasSpecs, Topic: "A thread per spec waiting for approval"}
	room, err := x.c.EnsureRoom(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if room.Created || room.ID != x.specs {
		t.Errorf("EnsureRoom() of a channel whose topic was emptied by hand = %+v, want %s found, not Created",
			room, x.specs)
	}
	ch, _ := x.ws.Channel(x.specs)
	if ch.Topic != spec.Topic {
		t.Errorf("the channel's topic = %q, want %q back", ch.Topic, spec.Topic)
	}
	if got := x.ws.Calls("conversations.members"); got != 1 {
		t.Errorf("conversations.members called %d times, want once, for the channel without its topic", got)
	}
}

// answerMembers is a transport that answers every conversations.members
// itself, with status and body, and passes every other call on.
type answerMembers struct {
	status int
	body   string
}

func (a *answerMembers) RoundTrip(r *http.Request) (*http.Response, error) {
	if !strings.HasSuffix(r.URL.Path, "/conversations.members") {
		return http.DefaultTransport.RoundTrip(r)
	}
	return &http.Response{StatusCode: a.status, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(a.body)), Request: r}, nil
}

// TestEnsureRoomDoubtsTheMembersCall covers #159: conversations.members,
// which says whether a channel found without its topic was left half made,
// never makes a healthy channel Created when it fails or answers without
// the bot, and never fails EnsureRoom: the topic is put back, and the
// channel is found as it is, so nobody is invited into it again. Before,
// an error failed EnsureRoom, which left the Chat not Ready over a channel
// that was fine, and a listing without the bot read as the bot alone.
func TestEnsureRoomDoubtsTheMembersCall(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
	}{
		{"an error", http.StatusOK, `{"ok":false,"error":"internal_error"}`},
		{"a missing scope", http.StatusOK, `{"ok":false,"error":"missing_scope","needed":"groups:read"}`},
		{"a server error", http.StatusInternalServerError, `{"ok":false}`},
		{"a listing without the bot", http.StatusOK, `{"ok":true,"members":[]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			x := newRooms(t)
			c := client(t, x.ws, x.botToken, x.appToken,
				WithHTTPClient(&http.Client{Transport: &answerMembers{status: tt.status, body: tt.body}}))
			if err := x.ws.SetTopic(x.specs, ""); err != nil {
				t.Fatal(err)
			}
			spec := ports.ChatRoomSpec{ID: x.specs, Alias: aliasSpecs, Topic: "A thread per spec waiting for approval"}
			room, err := c.EnsureRoom(t.Context(), spec)
			if err != nil {
				t.Fatalf("EnsureRoom() with conversations.members answering %s = %v, want the channel found", tt.body, err)
			}
			if room.Created || room.ID != x.specs {
				t.Errorf("EnsureRoom() = %+v, want %s found, not Created", room, x.specs)
			}
			if ch, _ := x.ws.Channel(x.specs); ch.Topic != spec.Topic {
				t.Errorf("the channel's topic = %q, want %q back", ch.Topic, spec.Topic)
			}
		})
	}
}
