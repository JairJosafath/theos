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
	"crypto/rand"
	"maps"
	"slices"
	"testing"

	"github.com/JairJosafath/theos/internal/adapters/matrix/matrixtest"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
)

// TestChatHostContract covers #61: the Matrix adapter passes the ChatHost
// contract against the fake homeserver, each subtest on a homeserver of its
// own. People post, edit, join and hold aliases on the homeserver itself,
// not through the adapter.
func TestChatHostContract(t *testing.T) {
	contract.ChatHost(t, contract.ChatHostEnv{
		New: func(t *testing.T) contract.ChatServer {
			t.Helper()
			c, hs, me := bot(t)
			refused, err := New(ports.ChatConfig{Address: hs.URL(), Token: rand.Text()})
			if err != nil {
				t.Fatal(err)
			}
			return contract.ChatServer{
				Host:    c,
				Refused: refused,
				Person: func(_ *testing.T, name string) string {
					user, _ := hs.NewUser(name)
					return user
				},
				Join: func(_ *testing.T, room, user string) { hs.Join(room, user) },
				Post: func(_ *testing.T, room, user, text, threadRoot string) string {
					return hs.Post(room, user, text, threadRoot)
				},
				Amend: func(_ *testing.T, room, user, message, text string) string {
					return hs.Edit(room, user, message, text)
				},
				Squat: func(_ *testing.T, alias string) {
					stranger, _ := hs.NewUser("mallory")
					hs.CreateRoomAs(stranger, alias)
				},
				Remove: func(_ *testing.T, room string) { hs.Leave(room, me) },
				Room:   func(t *testing.T, room string) contract.ChatRoomView { return shown(t, hs, room) },
			}
		},
		// Matrix can do all of it: nothing is skipped.
		Cannot: map[string]string{},
	})
}

// shown is room as a Matrix client shows it: its messages, each with the
// text of its sender's last edit, its thread and its reactions.
func shown(t *testing.T, hs *matrixtest.Server, room string) contract.ChatRoomView {
	t.Helper()
	r, ok := hs.Room(room)
	if !ok {
		t.Fatalf("the homeserver has no room %s", room)
	}
	view := contract.ChatRoomView{Name: r.Name, Topic: r.Topic, Private: r.JoinRule == "invite",
		Members: slices.Sorted(maps.Keys(r.Members))}
	at := map[string]int{}
	for _, e := range r.Events {
		rel, _ := e.Content["m.relates_to"].(map[string]any)
		relType, _ := rel[keyRelType].(string)
		target, _ := rel[keyEventID].(string)
		i, known := at[target]
		switch {
		case e.Type == typeReaction && relType == relAnnotation && known:
			key, _ := rel["key"].(string)
			view.Posts[i].Reactions = append(view.Posts[i].Reactions, key)
		case e.Type != typeMessage:
		case relType == relReplace:
			if edited, _ := e.Content["m.new_content"].(map[string]any); known && view.Posts[i].Sender == e.Sender {
				view.Posts[i].Text, _ = edited["body"].(string)
			}
		default:
			p := contract.ChatPosted{ID: e.ID, Sender: e.Sender}
			p.Text, _ = e.Content["body"].(string)
			if relType == relThread {
				p.ThreadRoot = target
			}
			at[e.ID] = len(view.Posts)
			view.Posts = append(view.Posts, p)
		}
	}
	return view
}
