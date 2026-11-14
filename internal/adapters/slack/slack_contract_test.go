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
	"testing"

	"github.com/JairJosafath/theos/internal/adapters/slack/slacktest"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
)

// workspace is a fresh fake workspace with the app's bot, and the Client
// that speaks as it, closed when t ends.
type workspace struct {
	ws                 *slacktest.Server
	c                  *Client
	bot                string
	botToken, appToken string
}

func newWorkspace(t *testing.T, opts ...Option) workspace {
	t.Helper()
	ws := slacktest.New(t)
	bot, botToken, appToken := ws.NewApp("theos")
	return workspace{ws: ws, c: client(t, ws, botToken, appToken, opts...), bot: bot, botToken: botToken,
		appToken: appToken}
}

// client is a Client of ws with the two tokens, closed when t ends.
func client(t *testing.T, ws *slacktest.Server, botToken, appToken string, opts ...Option) *Client {
	t.Helper()
	c, err := New(ports.ChatConfig{Address: ws.URL(), Token: botToken, AppToken: appToken}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

// TestChatHostContract covers #155: the Slack adapter passes the ChatHost
// contract on the fake workspace, each subtest on a workspace of its own,
// with the two skips of decision 22. People post, edit and squat on a name
// in the workspace itself, not through the adapter.
func TestChatHostContract(t *testing.T) {
	contract.ChatHost(t, contract.ChatHostEnv{
		New: func(t *testing.T) contract.ChatServer {
			t.Helper()
			w := newWorkspace(t)
			refused := client(t, w.ws, rand.Text(), rand.Text())
			fail := func(t *testing.T, err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			return contract.ChatServer{
				Host:    w.c,
				Refused: refused,
				Person:  func(_ *testing.T, name string) string { return w.ws.NewUser(name) },
				// A member ID the workspace has no member for.
				Stranger: "U0GHOST00",
				Join:     func(t *testing.T, room, user string) { fail(t, w.ws.Join(room, user)) },
				Post: func(t *testing.T, room, user, text, threadRoot string) string {
					ts, err := w.ws.Post(room, user, text, threadRoot)
					fail(t, err)
					return ts
				},
				Amend: func(t *testing.T, room, user, message, text string) string {
					ts, err := w.ws.Edit(room, user, message, text)
					fail(t, err)
					return ts
				},
				Squat:  func(_ *testing.T, alias string) { w.ws.Squat(alias) },
				Remove: func(t *testing.T, room string) { fail(t, w.ws.Remove(room, w.bot)) },
				Room:   func(t *testing.T, room string) contract.ChatRoomView { return shown(t, w.ws, room) },
			}
		},
		Cannot: map[string]string{
			"makes a Space and the rooms in it, each a room of its own": "a workspace has no Spaces: the " +
				"workspace is the Space (decision 5)",
			"brings a room's name to the one asked for": "a channel's one name is its alias (decision 4)",
		},
	})
}

// shown is room as Slack shows it to its members: each message with its
// text decoded (the bot's is its text, which carries the plain message),
// its thread and its reactions as the keys React was given.
func shown(t *testing.T, ws *slacktest.Server, room string) contract.ChatRoomView {
	t.Helper()
	ch, ok := ws.Channel(room)
	if !ok {
		t.Fatalf("the workspace has no channel %s", room)
	}
	keys := map[string]string{}
	for key, name := range emojiNames {
		keys[name] = key
	}
	view := contract.ChatRoomView{Name: ch.Name, Topic: ch.Topic, Private: ch.Private, Members: ch.Members}
	for _, m := range ch.Messages {
		p := contract.ChatPosted{ID: m.TS, Sender: m.User, Text: Decode(m.Text)}
		if m.ThreadTS != m.TS {
			p.ThreadRoot = m.ThreadTS
		}
		for _, name := range m.Reactions {
			p.Reactions = append(p.Reactions, keys[name])
		}
		view.Posts = append(view.Posts, p)
	}
	return view
}
