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

package model

import (
	"fmt"
	"maps"
	"slices"
)

// ChatSystem is what a chat host means to a Chat: whether it has Spaces,
// which keys of the Chat's Secret hold the bot's tokens and which markup a
// post is written in. Each host is one row in chatSystems, as each CI
// system is one row in ciSystems (docs/plans/requests-and-chat.md,
// decision 5); the Chat controller and theos-chat ask the row and assume
// nothing of the host.
type ChatSystem struct {
	// Name names the host, as cmd/main.go and cmd/theos-chat key its adapter.
	Name string
	// Spaces says the host groups rooms in a Space: the controller makes the
	// Chat's Space first and each room in it. On a host without, the rooms
	// stand alone and status.space stays unset.
	Spaces bool
	// TokenKey is the key of the Chat's Secret that holds the bot's token,
	// which the operator and theos-chat read.
	TokenKey string
	// AppTokenKey is the key of a second token, which theos-chat alone reads
	// to open its own connection to the host; "" for a host that needs none.
	AppTokenKey string
	// Markup is what the host reads a formatted post in, such as
	// ChatMarkupHTML; "" for plain text alone.
	Markup string
}

// ChatMarkupHTML is a post as HTML (ChatPost.HTML).
const ChatMarkupHTML = "html"

// ChatMatrix names Matrix, the chat host of the chart's homeserver.
const ChatMatrix = "matrix"

// ChatSystemDefault is the host of every Chat today. A Chat names no host
// until spec.host (step 7 of docs/plans/requests-and-chat.md), and one
// written before it means this one; step 8 makes the Chat controller and
// theos-chat read the field where they now take this name.
const ChatSystemDefault = ChatMatrix

// chatSystems are the chat hosts the factory knows, by name.
var chatSystems = map[string]ChatSystem{
	// Matrix groups rooms in Spaces, takes one access token and reads the
	// HTML subset of a message's formatted_body.
	ChatMatrix: {Name: ChatMatrix, Spaces: true, TokenKey: ChatTokenKey, Markup: ChatMarkupHTML},
}

// ChatSystemNames names the chat hosts the factory knows, sorted.
func ChatSystemNames() []string {
	return slices.Sorted(maps.Keys(chatSystems))
}

// ChatSystemByName returns the chat host name names.
func ChatSystemByName(name string) (ChatSystem, error) {
	s, ok := chatSystems[name]
	if !ok {
		return ChatSystem{}, fmt.Errorf("unknown chat host %q", name)
	}
	return s, nil
}

// Format is p in the host's markup, "" for a host that reads plain text
// alone.
func (s ChatSystem) Format(p ChatPost) string {
	if s.Markup == ChatMarkupHTML {
		return p.HTML()
	}
	return ""
}
