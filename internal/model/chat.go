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
	"strings"
)

// ChatPurpose names one of a Chat's rooms by the work it holds, as
// Chat.status.rooms[].purpose does (docs/plans/done/chat-matrix.md, "The rooms").
type ChatPurpose string

// The rooms of a Chat.
const (
	ChatUpdates ChatPurpose = "updates"
	ChatGeneral ChatPurpose = "general"
	ChatTriage  ChatPurpose = "triage"
	ChatSpecs   ChatPurpose = "specs"
	ChatReviews ChatPurpose = "reviews"
	ChatAlerts  ChatPurpose = "alerts"
)

// ChatPurposes are a Chat's rooms in the order the Space lists them.
var ChatPurposes = []ChatPurpose{ChatUpdates, ChatGeneral, ChatTriage, ChatSpecs, ChatReviews, ChatAlerts}

// chatTopics are each room's topic: what it holds and what a person says
// there.
var chatTopics = map[ChatPurpose]string{
	ChatUpdates: "Merges, releases, deploys and Brief rounds, batched. Read only.",
	ChatGeneral: "Ask the factory: status, brief <idea>, prompt <brief>: <text>, pause <brief>, resume <brief>, help.",
	ChatTriage:  "Each triage decision; a parked issue's question as a thread, where your answer goes to the issue.",
	ChatSpecs:   "A thread per spec waiting for approval: approve, or changes: <text>.",
	ChatReviews: "A thread per deploy waiting for a person (approve <version>), and each review's verdict.",
	ChatAlerts:  "Failed deploys and Tasks, a red base, an unhealthy factory part; retry on a failed Container.",
}

// ChatTopic is the topic of the room for purpose p.
func ChatTopic(p ChatPurpose) string { return chatTopics[p] }

// ChatSpaceTopic is the Space's topic.
const ChatSpaceTopic = "The factory's rooms for this project. Say help in any room for what it takes."

// ChatAliasPrefix is the prefix of a Chat's aliases: prefix when the Chat
// names one, else theos-<namespace>.
func ChatAliasPrefix(namespace, prefix string) string {
	if prefix != "" {
		return prefix
	}
	return "theos-" + namespace
}

// ChatRoomAlias is the alias localpart of the room for purpose p under
// prefix; the Space's is prefix itself.
func ChatRoomAlias(prefix string, p ChatPurpose) string { return prefix + "-" + string(p) }

// ChatSpaceName is the Space's display name: the Chat's space when set, else
// the Project's displayName, else the namespace.
func ChatSpaceName(space, projectDisplayName, namespace string) string {
	for _, name := range []string{space, projectDisplayName} {
		if name = strings.TrimSpace(name); name != "" {
			return name
		}
	}
	return namespace
}

// The reasons of a Chat's Ready condition.
const (
	ChatReady             = "Ready"
	ChatSecretMissing     = "SecretMissing"
	ChatSecretIncomplete  = "SecretIncomplete"
	ChatHomeserverUnset   = "HomeserverUnset"
	ChatCredentialRefused = "CredentialRefused" //nolint:gosec // a reason, not a credential
	ChatUnreachable       = "Unreachable"
	ChatAliasTaken        = "AliasTaken"
	ChatDuplicate         = "Duplicate"
	// ChatHostNotSupported is a Chat on a host the operator runs no adapter
	// for: it is left alone before its Secret is read or a host called.
	ChatHostNotSupported = "HostNotSupported"
	// ChatScopeMissing is a bot token the host takes but that lacks a scope
	// a call needs (docs/plans/requests-and-chat.md, decision 9).
	ChatScopeMissing = "ScopeMissing"
)

// The reasons of a Chat's Invited condition (#251): every person listed
// is invited to every room, the host refused to invite some of them, or
// the last pass ended before the invites (NotChecked, Unknown).
const (
	ChatInvited          = "Invited"
	ChatInviteRefused    = "InviteRefused"
	ChatInviteNotChecked = "NotChecked"
)

// ChatInviteNotCheckedMessage is the message of a Chat's Invited condition
// when the last pass ended not Ready, with readyReason, before it reached
// the invites (#251): what the condition said before is no longer known.
func ChatInviteNotCheckedMessage(readyReason string) string {
	return "The invites were not checked: the last pass ended before them, the Chat not Ready (" + readyReason + ")"
}

// ChatRefusal is a person the chat host refused to invite, and the host's
// error code.
type ChatRefusal struct {
	Person, Code string
}

// ChatInvitedReason is the reason of a Chat's Invited condition when the
// host refused refused.
func ChatInvitedReason(refused []ChatRefusal) string {
	if len(refused) > 0 {
		return ChatInviteRefused
	}
	return ChatInvited
}

// ChatInvitedMessage is the message of a Chat's Invited condition: how many
// people are invited, and each person the host refused with its code.
func ChatInvitedMessage(c ChatCheck) string {
	field := "spec.people"
	if system, ok := chatSystems[ChatHostName(c.Host)]; ok && system.People != "" {
		field = system.People
	}
	if len(c.Refused) == 0 {
		return fmt.Sprintf("The %d %s in %s %s invited to every room", c.People, plural(c.People, "person", "people"),
			field, plural(c.People, "is", "are"))
	}
	named := make([]string, 0, len(c.Refused))
	for _, r := range c.Refused {
		code := r.Code
		if code == "" {
			code = "no code"
		}
		named = append(named, fmt.Sprintf("%s (%s)", r.Person, code))
	}
	return fmt.Sprintf("%s refused to invite %s; the other %d %s in %s %s invited. Fix the account or the ID in %s; "+
		"the operator tries again", c.Homeserver, strings.Join(named, ", "), c.People,
		plural(c.People, "person", "people"), field, plural(c.People, "is", "are"), field)
}

// ChatTokenKey is the key of a Chat's Secret that holds the bot's access
// token.
const ChatTokenKey = "accessToken" //nolint:gosec // a key name, not a credential

// ChatCheck is what a Chat's Ready message names.
type ChatCheck struct {
	// Chat is the Chat's name; Secret its Secret's.
	Chat, Secret string
	// Homeserver is the homeserver's address.
	Homeserver string
	// Space is the Space's alias, or its name before it has one.
	Space string
	// Alias is the alias another account holds, for AliasTaken.
	Alias string
	// Rooms and People count the rooms ready and the people invited.
	Rooms, People int
	// Refused are the people the host refused to invite, for the Invited
	// condition (#251).
	Refused []ChatRefusal
	// Other is the Chat that already serves the namespace, for Duplicate.
	Other string
	// Host is the Chat's chat host, the name of its ChatSystem; "" is the
	// default's.
	Host string
	// Key is the key of the Secret that is missing, for SecretIncomplete.
	Key string
	// Scope is the scope the bot's token lacks, for ScopeMissing; "" when
	// the host named none.
	Scope string
	// Detail is the homeserver's error.
	Detail string
}

// ChatReadyMessage is the message of a Chat's Ready condition with reason.
// It speaks of the Space and the bot's tokens as the Chat's host has them
// (ChatSystem).
func ChatReadyMessage(reason string, c ChatCheck) string {
	detail := ""
	if c.Detail != "" {
		detail = ": " + c.Detail
	}
	system, known := chatSystems[ChatHostName(c.Host)]
	switch reason {
	case ChatReady:
		people := fmt.Sprintf("%d %s invited", c.People, plural(c.People, "person", "people"))
		if known && !system.Spaces {
			return fmt.Sprintf("The %d rooms are ready on %s, with no Space; %s", c.Rooms, c.Homeserver, people)
		}
		return fmt.Sprintf("Space %s and its %d rooms are ready on %s; %s", c.Space, c.Rooms, c.Homeserver, people)
	case ChatSecretMissing:
		if known && system.AppTokenKey != "" {
			return fmt.Sprintf("Secret %s is missing; whoever owns Chat %s creates it with the bot token under %s "+
				"and the app-level token under %s", c.Secret, c.Chat, system.TokenKey, system.AppTokenKey)
		}
		return fmt.Sprintf("Secret %s is missing; whoever owns Chat %s creates it with the bot's access token under %s",
			c.Secret, c.Chat, ChatTokenKey)
	case ChatSecretIncomplete:
		key := c.Key
		if key == "" {
			key = ChatTokenKey
		}
		if known && system.AppTokenKey != "" {
			return fmt.Sprintf("Secret %s has no %s; it holds the bot token under %s and the app-level token "+
				"under %s", c.Secret, key, system.TokenKey, system.AppTokenKey)
		}
		return fmt.Sprintf("Secret %s has no %s; it holds the bot's access token there", c.Secret, key)
	case ChatHomeserverUnset:
		if known && !system.Spaces {
			return fmt.Sprintf("The operator has no address for %s (%s), so Chat %s reaches no host; run the "+
				"operator with the flag set", system.Name, system.AddressFlag, c.Chat)
		}
		return fmt.Sprintf("Chat %s names no homeserver and the operator has none (--chat-homeserver); "+
			"set spec.homeserver, or install the chart with chat.homeserver.enabled", c.Chat)
	case ChatCredentialRefused:
		return fmt.Sprintf("%s refused the bot's token in Secret %s%s", c.Homeserver, c.Secret, detail)
	case ChatScopeMissing:
		scope := "a scope"
		if c.Scope != "" {
			scope = "the scope " + c.Scope
		}
		return fmt.Sprintf("The bot's token in Secret %s lacks %s, which %s says a call needs%s; give the bot "+
			"the scope, and the operator tries again", c.Secret, scope, c.Homeserver, detail)
	case ChatUnreachable:
		return fmt.Sprintf("Could not reach the rooms on %s%s; the operator tries again", c.Homeserver, detail)
	case ChatAliasTaken:
		return fmt.Sprintf("Alias %s on %s belongs to a room the bot is not in; set spec.aliasPrefix to a "+
			"prefix of your own, or free the alias", c.Alias, c.Homeserver)
	case ChatDuplicate:
		return fmt.Sprintf("Chat %s already serves this namespace; a namespace has one Chat, so this one does nothing",
			c.Other)
	case ChatHostNotSupported:
		return fmt.Sprintf("Chat %s is on %s, a chat host this operator runs no adapter for; it reads no Secret and "+
			"makes no rooms until an operator that runs %s is installed", c.Chat, c.Host, c.Host)
	}
	return reason + detail
}

// ChatEventKind is something the factory tells a Chat's people about.
type ChatEventKind string

// The events theos-chat posts, by the room the plan's table puts them in.
const (
	// updates
	ChatEventMerged     ChatEventKind = "Merged"
	ChatEventReleased   ChatEventKind = "Released"
	ChatEventDeployed   ChatEventKind = "Deployed"
	ChatEventBriefRound ChatEventKind = "BriefRound"
	// general
	ChatEventAnswer ChatEventKind = "Answer"
	// triage
	ChatEventTriaged        ChatEventKind = "Triaged"
	ChatEventTriageQuestion ChatEventKind = "TriageQuestion"
	// specs
	ChatEventSpecWaiting ChatEventKind = "SpecWaiting"
	// reviews
	ChatEventDeployWaiting ChatEventKind = "DeployWaiting"
	ChatEventReviewVerdict ChatEventKind = "ReviewVerdict"
	// ChatEventChangeWaiting is a change its Crew's approval rules hold for
	// a person (#92): a thread that takes approve and changes, as a spec's.
	ChatEventChangeWaiting ChatEventKind = "ChangeWaiting"
	// alerts
	ChatEventDeployFailed    ChatEventKind = "DeployFailed"
	ChatEventTaskFailed      ChatEventKind = "TaskFailed"
	ChatEventContainerFailed ChatEventKind = "ContainerFailed"
	ChatEventBaseRed         ChatEventKind = "BaseRed"
	ChatEventUnhealthy       ChatEventKind = "Unhealthy"
)

// ChatRoute is where an event goes and how it is posted.
type ChatRoute struct {
	// Room is the room it is posted in.
	Room ChatPurpose
	// Thread makes it a live item: a thread root the bot edits as the item
	// moves and marks done, whose thread takes the room's commands.
	Thread bool
	// Digest batches it with the room's other news into one message per
	// digest window.
	Digest bool
}

// chatRoutes is the plan's table, event by event.
var chatRoutes = map[ChatEventKind]ChatRoute{
	ChatEventMerged:          {Room: ChatUpdates, Digest: true},
	ChatEventReleased:        {Room: ChatUpdates, Digest: true},
	ChatEventDeployed:        {Room: ChatUpdates, Digest: true},
	ChatEventBriefRound:      {Room: ChatUpdates, Digest: true},
	ChatEventAnswer:          {Room: ChatGeneral},
	ChatEventTriaged:         {Room: ChatTriage},
	ChatEventTriageQuestion:  {Room: ChatTriage, Thread: true},
	ChatEventSpecWaiting:     {Room: ChatSpecs, Thread: true},
	ChatEventDeployWaiting:   {Room: ChatReviews, Thread: true},
	ChatEventReviewVerdict:   {Room: ChatReviews},
	ChatEventChangeWaiting:   {Room: ChatReviews, Thread: true},
	ChatEventDeployFailed:    {Room: ChatAlerts},
	ChatEventTaskFailed:      {Room: ChatAlerts},
	ChatEventContainerFailed: {Room: ChatAlerts, Thread: true},
	ChatEventBaseRed:         {Room: ChatAlerts},
	ChatEventUnhealthy:       {Room: ChatAlerts},
}

// ChatRouteFor is where an event of kind goes.
func ChatRouteFor(kind ChatEventKind) (ChatRoute, error) {
	route, ok := chatRoutes[kind]
	if !ok {
		return ChatRoute{}, fmt.Errorf("no room takes events of kind %q", kind)
	}
	return route, nil
}
