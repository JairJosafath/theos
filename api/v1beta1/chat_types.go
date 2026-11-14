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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ChatPurpose names one of a Chat's rooms by the work it holds
// (docs/plans/done/chat-matrix.md, "The rooms").
// +kubebuilder:validation:Enum=updates;general;triage;specs;reviews;alerts
type ChatPurpose string

// The rooms of a Chat.
const (
	ChatPurposeUpdates ChatPurpose = "updates"
	ChatPurposeGeneral ChatPurpose = "general"
	ChatPurposeTriage  ChatPurpose = "triage"
	ChatPurposeSpecs   ChatPurpose = "specs"
	ChatPurposeReviews ChatPurpose = "reviews"
	ChatPurposeAlerts  ChatPurpose = "alerts"
)

// ChatHost names the chat app a Chat is on (docs/plans/requests-and-chat.md,
// decision 1).
// +kubebuilder:validation:Enum=matrix;slack
type ChatHost string

// The chat hosts a Chat can name.
const (
	ChatHostMatrix ChatHost = "matrix"
	ChatHostSlack  ChatHost = "slack"
)

// ChatSpec is a project's conversation with the factory on a chat host: on
// Matrix one Space, named after the Project, with a room per kind of work,
// and the people who may speak to it (docs/plans/done/chat-matrix.md); on
// Slack the same rooms as channels, and the people of spec.slack
// (docs/plans/requests-and-chat.md, decisions 1 to 3). Each rule below that
// names host passes every Chat written before the field.
// +kubebuilder:validation:XValidation:rule="has(self.aliasPrefix) == has(oldSelf.aliasPrefix)",message="aliasPrefix can't be set or unset once the Chat exists: the rooms carry its aliases"
// +kubebuilder:validation:XValidation:rule="!has(self.slack) || (has(self.host) && self.host == 'slack')",message="slack is only for a Chat with host: slack"
// +kubebuilder:validation:XValidation:rule="!has(self.host) || self.host != 'slack' || !has(self.homeserver)",message="a Chat with host: slack names no homeserver: that is Matrix's"
// +kubebuilder:validation:XValidation:rule="!has(self.host) || self.host != 'slack' || !has(self.space)",message="a Chat with host: slack names no space: the Slack workspace is the Space"
// +kubebuilder:validation:XValidation:rule="!has(self.host) || self.host != 'slack' || !has(self.people) || size(self.people) == 0",message="a Chat with host: slack lists its people under slack.people, by slackID, not under people"
// +kubebuilder:validation:XValidation:rule="!has(self.host) || self.host != 'slack' || !has(self.rooms) || ((!has(self.rooms.updates) || self.rooms.updates == 'updates') && (!has(self.rooms.general) || self.rooms.general == 'general') && (!has(self.rooms.triage) || self.rooms.triage == 'triage') && (!has(self.rooms.specs) || self.rooms.specs == 'specs') && (!has(self.rooms.reviews) || self.rooms.reviews == 'reviews') && (!has(self.rooms.alerts) || self.rooms.alerts == 'alerts'))",message="a Chat with host: slack keeps each room's name its purpose: a channel is named <aliasPrefix>-<purpose>"
// +kubebuilder:validation:XValidation:rule="!has(self.host) || self.host != 'slack' || !has(self.aliasPrefix) || (size(self.aliasPrefix) <= 70 && self.aliasPrefix.matches('^[a-z0-9_-]+$'))",message="a Chat with host: slack needs an aliasPrefix Slack takes in a channel name: no . or =, at most 70 characters"
type ChatSpec struct {
	// host is the chat app the Chat is on: matrix, a Matrix homeserver, or
	// slack, a Slack workspace. A Chat written before the field is on
	// matrix. It can't change: the rooms are on the host they were made on.
	// +kubebuilder:default=matrix
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="host is immutable: the rooms are on the host they were made on"
	// +optional
	Host ChatHost `json:"host,omitempty"`

	// homeserver is the Matrix homeserver's address, http(s)://host[:port],
	// whose client-server API the operator and theos-chat call. Unset, it is
	// the operator's --chat-homeserver, the chart's in-cluster homeserver
	// when chat.homeserver.enabled; a Chat on another homeserver, matrix.org
	// included, names it here.
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="self.matches('^https?://[^/?#[:space:]]+(/[^?#[:space:]]*)?$')",message="homeserver must be an http or https address, with no query or fragment"
	// +optional
	Homeserver string `json:"homeserver,omitempty"`

	// credentialsSecretRef names a Secret in the Chat's namespace that the
	// user creates, holding the bot's access token under accessToken. The
	// bot is an ordinary Matrix account on the homeserver; it makes the Space
	// and the rooms and posts in them. Only the operator and theos-chat read
	// it.
	// +required
	CredentialsSecretRef LocalRef `json:"credentialsSecretRef"`

	// space is the Space's display name. Unset, it is the Project's
	// displayName, else the namespace.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +optional
	Space string `json:"space,omitempty"`

	// aliasPrefix names the aliases the operator gives the Space,
	// #<aliasPrefix>:<server>, and each room, #<aliasPrefix>-<purpose>:<server>,
	// by which it finds them again. Unset, it is theos-<namespace>. An alias
	// another account holds is not taken over: the Chat reports AliasTaken,
	// and a prefix of the user's own avoids it on a shared homeserver. On
	// Slack it names the channels, <aliasPrefix>-<purpose>, so it takes no
	// . or = and at most 70 characters.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9._=-]*$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="aliasPrefix is immutable: the rooms carry its aliases"
	// +optional
	AliasPrefix string `json:"aliasPrefix,omitempty"`

	// rooms names each room; each defaults to its purpose. On Slack each
	// keeps its default: a channel has one name, its alias.
	// +kubebuilder:default={updates: updates, general: general, triage: triage, specs: specs, reviews: reviews, alerts: alerts}
	// +optional
	Rooms ChatRooms `json:"rooms,omitzero"`

	// digestMinutes is how long the updates room's news waits to be posted
	// as one message; 0 posts each as it comes.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1440
	// +kubebuilder:default=10
	// +optional
	DigestMinutes int32 `json:"digestMinutes,omitempty"`

	// people may speak to the factory: each is invited to the Space and its
	// rooms, and their commands are made as their Kubernetes user and
	// groups, by impersonation, so the cluster's RBAC decides. Anyone else
	// in a room gets a one-line refusal. A Chat with host: slack lists its
	// people under slack.people instead.
	// +listType=map
	// +listMapKey=matrixID
	// +kubebuilder:validation:MaxItems=100
	// +optional
	People []ChatPerson `json:"people,omitempty"`

	// slack is what a Chat with host: slack holds of its own: its people,
	// by Slack member ID.
	// +optional
	Slack *ChatSlack `json:"slack,omitempty"`
}

// ChatSlack is a Slack Chat's own settings (docs/plans/requests-and-chat.md,
// decision 2).
type ChatSlack struct {
	// people may speak to the factory, each by their Slack member ID: each
	// is invited to the rooms, and their commands are made as their
	// Kubernetes user and groups, by impersonation, so the cluster's RBAC
	// decides. Anyone else in a room gets a one-line refusal.
	// +listType=map
	// +listMapKey=slackID
	// +kubebuilder:validation:MaxItems=100
	// +optional
	People []ChatSlackPerson `json:"people,omitempty"`
}

// ChatSlackPerson is a Slack member and the Kubernetes identity their
// commands are made as, held to the same rules as a ChatPerson.
type ChatSlackPerson struct {
	// slackID is the person's Slack member ID, U or W and then capital
	// letters and digits, which a message's sender carries; not the e-mail
	// address, which changes.
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:XValidation:rule="self.matches('^[UW][A-Z0-9]+$')",message="slackID must be a Slack member ID: U or W, then capital letters and digits"
	// +required
	SlackID string `json:"slackID"`

	// user is the Kubernetes user name the person's commands are made as.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('system:')",message="user must be a person's name, not a system: identity"
	// +required
	User string `json:"user"`

	// groups are the Kubernetes groups the person's commands are made with.
	// +listType=set
	// +kubebuilder:validation:MaxItems=20
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self.all(g, !g.startsWith('system:'))",message="groups must be people's groups, not system: groups"
	// +optional
	Groups []string `json:"groups,omitempty"`
}

// ChatRooms are the display names of a Chat's rooms, one per purpose.
// +kubebuilder:validation:XValidation:rule="[self.updates, self.general, self.triage, self.specs, self.reviews, self.alerts].all(n, [self.updates, self.general, self.triage, self.specs, self.reviews, self.alerts].filter(m, m == n).size() == 1)",message="each room needs a name of its own"
type ChatRooms struct {
	// updates holds merges, releases, deploys and Brief rounds, batched by
	// digestMinutes; nobody gives commands there.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:default=updates
	// +optional
	Updates string `json:"updates,omitempty"`

	// general answers questions and takes status, brief, prompt, pause,
	// resume and help.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:default=general
	// +optional
	General string `json:"general,omitempty"`

	// triage holds each Triage Task's decision and a parked issue's question
	// as a thread; an answer there goes to the issue.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:default=triage
	// +optional
	Triage string `json:"triage,omitempty"`

	// specs holds a thread per spec waiting for approval: approve, or
	// changes: <text>.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:default=specs
	// +optional
	Specs string `json:"specs,omitempty"`

	// reviews holds a thread per deploy waiting for a person (approve
	// <version>) and each review's verdict.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:default=reviews
	// +optional
	Reviews string `json:"reviews,omitempty"`

	// alerts holds failed deploys and Tasks, a red base and an unhealthy
	// factory part; retry on a failed Container.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:default=alerts
	// +optional
	Alerts string `json:"alerts,omitempty"`
}

// ChatPerson is a Matrix account and the Kubernetes identity its commands
// are made as.
type ChatPerson struct {
	// matrixID is the person's Matrix user ID, @localpart:server.
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:XValidation:rule="self.matches('^@[a-z0-9._=/+-]+:[A-Za-z0-9.-]+(:[0-9]{1,5})?$')",message="matrixID must be a Matrix user ID, @localpart:server"
	// +required
	MatrixID string `json:"matrixID"`

	// user is the Kubernetes user name the person's commands are made as.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('system:')",message="user must be a person's name, not a system: identity"
	// +required
	User string `json:"user"`

	// groups are the Kubernetes groups the person's commands are made with.
	// +listType=set
	// +kubebuilder:validation:MaxItems=20
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self.all(g, !g.startsWith('system:'))",message="groups must be people's groups, not system: groups"
	// +optional
	Groups []string `json:"groups,omitempty"`
}

// ChatRoomRef is a Matrix room the operator made or found.
type ChatRoomRef struct {
	// id is the room's ID, !opaque:server.
	// +required
	ID string `json:"id"`

	// alias is the room's alias, #localpart:server.
	// +optional
	Alias string `json:"alias,omitempty"`
}

// ChatRoomStatus is one of a Chat's rooms.
type ChatRoomStatus struct {
	// purpose is what the room holds.
	// +required
	Purpose ChatPurpose `json:"purpose"`

	// id is the room's ID: !opaque:server on Matrix, the channel's ID on
	// Slack.
	// +required
	ID string `json:"id"`

	// alias is the room's alias: #localpart:server on Matrix, #name on
	// Slack.
	// +optional
	Alias string `json:"alias,omitempty"`
}

// ConditionChatReady on a Chat says whether its six rooms, and their Space
// on Matrix, are there with the people invited. Its reasons are Ready,
// SecretMissing, SecretIncomplete, HomeserverUnset, CredentialRefused,
// ScopeMissing, Unreachable, AliasTaken, Duplicate and HostNotSupported;
// its message carries the last error. A person the host refuses to invite
// leaves it True and shows on ConditionChatInvited.
const ConditionChatReady = "Ready"

// ConditionChatInvited on a Chat says whether every person it lists is
// invited to every room (#251). Its reasons are Invited and InviteRefused,
// whose message names each person the host refused to invite with the
// host's error code. Each pass that reaches the invites sets it.
const ConditionChatInvited = "Invited"

// ChatStatus is what the operator made and found on the chat host.
type ChatStatus struct {
	// homeserver is the chat host's address the operator last reached the
	// rooms at: the Matrix homeserver, or Slack's Web API. theos-chat
	// reaches the host there.
	// +optional
	Homeserver string `json:"homeserver,omitempty"`

	// bot is the user ID on the chat host the bot's token belongs to.
	// +optional
	Bot string `json:"bot,omitempty"`

	// space is the Space the rooms belong to; unset on Slack, which has
	// none.
	// +optional
	Space *ChatRoomRef `json:"space,omitempty"`

	// rooms are the Chat's rooms, one per purpose: on Slack, private
	// channels.
	// +listType=map
	// +listMapKey=purpose
	// +optional
	Rooms []ChatRoomStatus `json:"rooms,omitempty"`

	// invited are the people invited to the Space and every room.
	// +listType=set
	// +optional
	Invited []string `json:"invited,omitempty"`

	// conditions: Ready and Invited.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=theos
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Homeserver",type=string,JSONPath=".status.homeserver"
// +kubebuilder:printcolumn:name="Space",type=string,JSONPath=".status.space.alias"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type==\"Ready\")].status"
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=".status.conditions[?(@.type==\"Ready\")].reason"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Chat is a project's rooms on a chat host, a Space on a Matrix homeserver
// or channels in a Slack workspace, where people follow and steer the
// factory: one per project namespace.
type Chat struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec is the host, the bot's Secret, the rooms and the people.
	// +required
	Spec ChatSpec `json:"spec"`

	// status is the Space and rooms the operator made, and Ready.
	// +optional
	Status ChatStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ChatList contains a list of Chat
type ChatList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Chat `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Chat{}, &ChatList{})
		return nil
	})
}
