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
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ChatCommandKind is what a person asks of the factory in a Chat's room
// (docs/plans/done/chat-matrix.md, "The rooms").
type ChatCommandKind string

// The commands, by the room that takes them.
const (
	// Every room.
	ChatCommandHelp ChatCommandKind = "help"
	// general.
	ChatCommandStatus ChatCommandKind = "status"
	ChatCommandBrief  ChatCommandKind = "brief"
	ChatCommandPrompt ChatCommandKind = "prompt"
	ChatCommandPause  ChatCommandKind = "pause"
	ChatCommandResume ChatCommandKind = "resume"
	// A triage thread: a person's answer to a parked issue's question.
	ChatCommandAnswer ChatCommandKind = "answer"
	// A specs thread.
	ChatCommandApproveSpec ChatCommandKind = "approve-spec"
	ChatCommandChanges     ChatCommandKind = "changes"
	// A reviews thread on a deploy waiting for a person.
	ChatCommandApproveDeploy ChatCommandKind = "approve-deploy"
	// An alerts thread on a failed Container.
	ChatCommandRetry ChatCommandKind = "retry"
)

// chatWordApprove starts an approval of a spec or of a deploy.
const chatWordApprove = "approve"

// ChatCommand is a parsed command.
type ChatCommand struct {
	Kind ChatCommandKind
	// Brief names the Brief of prompt, pause and resume.
	Brief string
	// Text is a brief's idea, a prompt, an answer or the changes asked for.
	Text string
	// Version is the release approve-deploy lets in.
	Version string
}

// ErrChatUnknownCommand is a message that starts with no word the room
// takes; theos-chat decides whether it was meant for the bot at all.
var ErrChatUnknownCommand = errors.New("unknown command")

// ErrChatInvalidCommand is a command the room takes, said wrongly or in the
// wrong place.
var ErrChatInvalidCommand = errors.New("invalid command")

// The CRDs' bounds a command's words are held to before they are sent, so
// the person hears it in the room's words rather than the API server's.
const (
	chatMaxIdea    = 4000 // Brief.spec.idea
	chatMaxPrompt  = 2000 // each of Brief.spec.prompts
	chatMaxAnswer  = 4000 // an answer or the changes asked for
	chatMaxVersion = 128  // Container.spec.deploy[].approved
)

// chatName is a Kubernetes object name (a DNS-1123 subdomain).
var chatName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

// chatVersion is a release tag as Container.spec.deploy[].approved takes it.
var chatVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

// chatValidName reports whether name is an object name.
func chatValidName(name string) bool { return len(name) <= 253 && chatName.MatchString(name) }

// ParseChatCommand reads a person's message in room, in a thread or not.
// A reply's quoted fallback is left out; the first word, any case, with or
// without a colon after it, picks the command. An error wraps
// ErrChatUnknownCommand or ErrChatInvalidCommand and says, in words to post
// back, what the room takes.
func ParseChatCommand(room ChatPurpose, inThread bool, body string) (ChatCommand, error) {
	text := strings.TrimSpace(stripReplyFallback(body))
	word, rest := chatKeyword(text)
	if word == string(ChatCommandHelp) && rest == "" {
		return ChatCommand{Kind: ChatCommandHelp}, nil
	}
	switch room {
	case ChatGeneral:
		return parseGeneral(word, rest)
	case ChatTriage:
		if !inThread {
			return ChatCommand{}, fmt.Errorf("%w: answer a question in its thread; say help for what triage takes",
				ErrChatInvalidCommand)
		}
		if text == "" || len(text) > chatMaxAnswer {
			return ChatCommand{}, fmt.Errorf("%w: an answer has 1 to %d characters", ErrChatInvalidCommand, chatMaxAnswer)
		}
		return ChatCommand{Kind: ChatCommandAnswer, Text: text}, nil
	case ChatSpecs:
		return parseSpecs(inThread, word, rest)
	case ChatReviews:
		return parseReviews(inThread, word, rest)
	case ChatAlerts:
		return parseAlerts(inThread, word, rest)
	case ChatUpdates:
		return ChatCommand{}, fmt.Errorf("%w: updates is read only; ask in general", ErrChatInvalidCommand)
	}
	return ChatCommand{}, fmt.Errorf("%w: no room %q", ErrChatUnknownCommand, room)
}

func parseGeneral(word, rest string) (ChatCommand, error) {
	switch ChatCommandKind(word) {
	case ChatCommandStatus:
		if rest != "" {
			return ChatCommand{}, fmt.Errorf("%w: status takes nothing after it", ErrChatInvalidCommand)
		}
		return ChatCommand{Kind: ChatCommandStatus}, nil
	case ChatCommandBrief:
		if rest == "" || len(rest) > chatMaxIdea {
			return ChatCommand{}, fmt.Errorf("%w: brief <idea>, the idea in 1 to %d characters", ErrChatInvalidCommand,
				chatMaxIdea)
		}
		return ChatCommand{Kind: ChatCommandBrief, Text: rest}, nil
	case ChatCommandPrompt:
		name, prompt, ok := strings.Cut(rest, ":")
		name, prompt = strings.TrimSpace(name), strings.TrimSpace(prompt)
		if !ok || !chatValidName(name) || prompt == "" || len(prompt) > chatMaxPrompt {
			return ChatCommand{}, fmt.Errorf("%w: prompt <brief>: <text>, the Brief's name and the prompt in 1 to %d "+
				"characters", ErrChatInvalidCommand, chatMaxPrompt)
		}
		return ChatCommand{Kind: ChatCommandPrompt, Brief: name, Text: prompt}, nil
	case ChatCommandPause, ChatCommandResume:
		if !chatValidName(rest) {
			return ChatCommand{}, fmt.Errorf("%w: %s <brief>, the Brief's name", ErrChatInvalidCommand, word)
		}
		return ChatCommand{Kind: ChatCommandKind(word), Brief: rest}, nil
	}
	return ChatCommand{}, chatUnknown(word, ChatGeneral)
}

func parseSpecs(inThread bool, word, rest string) (ChatCommand, error) {
	switch {
	case word != chatWordApprove && word != string(ChatCommandChanges):
		return ChatCommand{}, chatUnknown(word, ChatSpecs)
	case !inThread:
		return ChatCommand{}, fmt.Errorf("%w: %s in the spec's thread", ErrChatInvalidCommand, word)
	case word == chatWordApprove && rest == "":
		return ChatCommand{Kind: ChatCommandApproveSpec}, nil
	case word == chatWordApprove:
		return ChatCommand{}, fmt.Errorf("%w: approve takes nothing after it; changes: <text> asks for changes",
			ErrChatInvalidCommand)
	case rest == "" || len(rest) > chatMaxAnswer:
		return ChatCommand{}, fmt.Errorf("%w: changes: <text>, what to change in 1 to %d characters",
			ErrChatInvalidCommand, chatMaxAnswer)
	}
	return ChatCommand{Kind: ChatCommandChanges, Text: rest}, nil
}

func parseReviews(inThread bool, word, rest string) (ChatCommand, error) {
	switch {
	case word != chatWordApprove && word != string(ChatCommandChanges):
		return ChatCommand{}, chatUnknown(word, ChatReviews)
	case !inThread:
		return ChatCommand{}, fmt.Errorf("%w: approve <version> in the deploy's thread; approve or changes: <text> "+
			"in a waiting change's", ErrChatInvalidCommand)
	case word == string(ChatCommandChanges) && (rest == "" || len(rest) > chatMaxAnswer):
		return ChatCommand{}, fmt.Errorf("%w: changes: <text>, what to change in 1 to %d characters",
			ErrChatInvalidCommand, chatMaxAnswer)
	case word == string(ChatCommandChanges):
		// Asked of a waiting change, as of a spec (#92).
		return ChatCommand{Kind: ChatCommandChanges, Text: rest}, nil
	case rest == "":
		// A waiting change's approval names nothing; a deploy's names its
		// version, and the thread's item says which this is (ChatWrite).
		return ChatCommand{Kind: ChatCommandApproveSpec}, nil
	case len(rest) > chatMaxVersion || !chatVersion.MatchString(rest):
		return ChatCommand{}, fmt.Errorf("%w: approve <version>, the release the thread names, such as v1.2.0",
			ErrChatInvalidCommand)
	}
	return ChatCommand{Kind: ChatCommandApproveDeploy, Version: rest}, nil
}

func parseAlerts(inThread bool, word, rest string) (ChatCommand, error) {
	switch {
	case word != string(ChatCommandRetry):
		return ChatCommand{}, chatUnknown(word, ChatAlerts)
	case !inThread:
		return ChatCommand{}, fmt.Errorf("%w: retry in the failed Container's thread", ErrChatInvalidCommand)
	case rest != "":
		return ChatCommand{}, fmt.Errorf("%w: retry takes nothing after it", ErrChatInvalidCommand)
	}
	return ChatCommand{Kind: ChatCommandRetry}, nil
}

func chatUnknown(word string, room ChatPurpose) error {
	if word == "" {
		return fmt.Errorf("%w: say help for what %s takes", ErrChatUnknownCommand, room)
	}
	return fmt.Errorf("%w %q: say help for what %s takes", ErrChatUnknownCommand, word, room)
}

// chatKeyword splits text into its first word, in lower case, and the rest,
// trimmed; a colon right after the word goes with it.
func chatKeyword(text string) (string, string) {
	end := strings.IndexFunc(text, func(r rune) bool { return !unicode.IsLetter(r) })
	if end < 0 {
		return strings.ToLower(text), ""
	}
	word, rest := strings.ToLower(text[:end]), text[end:]
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
	if word != "" && strings.HasPrefix(rest, ":") {
		rest = rest[1:]
	} else if r, _ := utf8.DecodeRuneInString(text[end:]); !unicode.IsSpace(r) {
		// A word glued to anything but a colon or a space is no keyword.
		return "", text
	}
	return word, strings.TrimSpace(rest)
}

// stripReplyFallback drops the quote a Matrix client puts before a reply's
// own words: leading lines starting with ">" and the blank line after them.
func stripReplyFallback(body string) string {
	lines := strings.Split(body, "\n")
	i := 0
	for i < len(lines) && strings.HasPrefix(lines[i], ">") {
		i++
	}
	if i == 0 {
		return body
	}
	if i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	return strings.Join(lines[i:], "\n")
}

// chatHelp are the lines help answers with, by room.
var chatHelp = map[ChatPurpose][]string{
	ChatUpdates: {"This room is read only: merges, releases, deploys and Brief rounds. Ask in general."},
	ChatGeneral: {
		"status: what the factory is doing",
		"brief <idea>: start a Brief from an idea",
		"prompt <brief>: <text>: add a follow-up to a Brief",
		"pause <brief>, resume <brief>: stop or restart a Brief's work",
	},
	ChatTriage: {"In a question's thread, your words are your answer: they go to the issue."},
	ChatSpecs:  {"In a spec's thread: approve, or changes: <text>."},
	ChatReviews: {"In a deploy's thread: approve <version>.",
		"In the thread of a change that waits for you: approve, or changes: <text>."},
	ChatAlerts: {"In a failed Container's thread: retry."},
}

// ChatHelp is what help answers in room.
func ChatHelp(room ChatPurpose) ChatPost {
	return ChatPost{Title: "What " + string(room) + " takes", Body: []string{strings.Join(chatHelp[room], "\n")}}
}
