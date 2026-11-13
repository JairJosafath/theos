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

package ports

import (
	"context"
	"errors"
	"time"
)

// ChatConfig is how to reach one chat homeserver as the factory's bot.
type ChatConfig struct {
	// Homeserver is the homeserver's address, http(s)://host[:port].
	Homeserver string
	// Token is the bot's access token, from the Chat's Secret.
	Token string
}

// ChatRoomSpec is a room EnsureRoom finds or makes.
type ChatRoomSpec struct {
	// ID is the room's ID as last recorded, "" for none: the room is kept
	// while the bot is in it.
	ID string
	// Alias is the localpart of the room's alias: the room is found by it
	// when ID finds none, and made with it.
	Alias string
	// Name and Topic are the room's when it is made; an existing room's name
	// follows Name.
	Name  string
	Topic string
	// Space makes the room a Space.
	Space bool
	// Parent is the room ID of the Space the room belongs to, "" for none:
	// the Space lists it as a child and the room names the Space its parent.
	Parent string
}

// ChatRoom is a room EnsureRoom found or made.
type ChatRoom struct {
	// ID is the room's ID; Alias its full alias, #localpart:server.
	ID    string
	Alias string
	// Created says EnsureRoom made it: nobody is invited to it yet.
	Created bool
}

// ChatMessage is a message in plain text and in the host's HTML.
type ChatMessage struct {
	Text string
	HTML string
}

// ChatEvent is a message a person posted in a room the bot is in.
type ChatEvent struct {
	// Room and ID say where and which event.
	Room string
	ID   string
	// Sender is the poster's user ID.
	Sender string
	// Text is the message's plain text.
	Text string
	// ThreadRoot is the event the message answers in a thread, "" outside one.
	ThreadRoot string
	// Replaces is the event an edit replaces, "" for a new message: an edit
	// runs no command again.
	Replaces string
	// Time is when the homeserver received it, never the zero time.
	Time time.Time
}

// ChatSync is what happened since a sync's token.
type ChatSync struct {
	// Next is the token the next sync starts from, never "", and not the
	// token this sync started from when Events holds a message.
	Next string
	// Events are the new messages, a room's oldest first; the messages of
	// two rooms come in no promised order.
	Events []ChatEvent
}

// ChatHost is the chat port (docs/plans/done/chat-matrix.md, "Matrix through a
// port"): the operator makes a Chat's Space and rooms with it, and
// theos-chat posts, threads, edits, reacts and reads the rooms. One value
// speaks as one bot on one homeserver. What the two rely on of every
// adapter is the suite contract.ChatHost (internal/ports/contract/chathost.go),
// which each adapter's tests run.
type ChatHost interface {
	// WhoAmI is the bot's user ID; ErrChatCredentialRefused when the
	// homeserver doesn't know the token.
	WhoAmI(ctx context.Context) (string, error)
	// EnsureRoom finds the room spec describes, by its ID and then its alias,
	// among the rooms the bot is in, or makes it: private, invite only and
	// unencrypted. It makes an existing room's name and Space link match the
	// spec. An alias held by a room the bot is not in is ErrChatAliasTaken.
	// A token the homeserver doesn't know is ErrChatCredentialRefused, as
	// WhoAmI's.
	EnsureRoom(ctx context.Context, spec ChatRoomSpec) (ChatRoom, error)
	// Invite invites user to room; a user already invited or in the room is
	// no error.
	Invite(ctx context.Context, room, user string) error
	// Send posts msg in room and returns its event ID.
	Send(ctx context.Context, room string, msg ChatMessage) (string, error)
	// Reply posts msg in the thread whose root is threadRoot.
	Reply(ctx context.Context, room, threadRoot string, msg ChatMessage) (string, error)
	// Edit replaces the text of the bot's event with msg.
	Edit(ctx context.Context, room, event string, msg ChatMessage) (string, error)
	// React puts the reaction key, such as ✅, on event.
	React(ctx context.Context, room, event, key string) (string, error)
	// Sync returns the messages people posted in the bot's rooms since the
	// token since ("" for the first sync), each once and none of the bot's
	// own, waiting up to wait for one: with a wait of 0 it returns at once,
	// with or without messages.
	Sync(ctx context.Context, since string, wait time.Duration) (ChatSync, error)
}

// ChatHosts builds the ChatHost of one homeserver and bot, as cmd/main.go
// picks the adapter.
type ChatHosts func(cfg ChatConfig) (ChatHost, error)

// ErrChatCredentialRefused is a token the homeserver doesn't take.
var ErrChatCredentialRefused = errors.New("the homeserver refused the access token")

// ErrChatAliasTaken is an alias held by a room the bot is not in.
var ErrChatAliasTaken = errors.New("the alias belongs to a room the bot is not in")

// ChatAliasTakenError is ErrChatAliasTaken naming the alias.
type ChatAliasTakenError struct {
	// Alias is the full alias, #localpart:server.
	Alias string
}

func (e *ChatAliasTakenError) Error() string { return ErrChatAliasTaken.Error() + ": " + e.Alias }

// Is makes the error ErrChatAliasTaken.
func (e *ChatAliasTakenError) Is(target error) bool { return target == ErrChatAliasTaken }
