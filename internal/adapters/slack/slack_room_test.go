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
