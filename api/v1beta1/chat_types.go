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

// ChatSpec is a project's conversation with the factory on a Matrix
// homeserver: one Space, named after the Project, with a room per kind of
// work, and the people who may speak to it (docs/plans/done/chat-matrix.md).
// +kubebuilder:validation:XValidation:rule="has(self.aliasPrefix) == has(oldSelf.aliasPrefix)",message="aliasPrefix can't be set or unset once the Chat exists: the rooms carry its aliases"
type ChatSpec struct {
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
	// and a prefix of the user's own avoids it on a shared homeserver.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9._=-]*$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="aliasPrefix is immutable: the rooms carry its aliases"
	// +optional
	AliasPrefix string `json:"aliasPrefix,omitempty"`

	// rooms names each room; each defaults to its purpose.
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
	// in a room gets a one-line refusal.
	// +listType=map
	// +listMapKey=matrixID
	// +kubebuilder:validation:MaxItems=100
	// +optional
	People []ChatPerson `json:"people,omitempty"`
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

	// id is the room's ID, !opaque:server.
	// +required
	ID string `json:"id"`

	// alias is the room's alias, #localpart:server.
	// +optional
	Alias string `json:"alias,omitempty"`
}

// ConditionChatReady on a Chat says whether the Space and its six rooms
// are there with the people invited. Its reasons are Ready, SecretMissing,
// SecretIncomplete, HomeserverUnset, CredentialRefused, Unreachable,
// AliasTaken and Duplicate; its message carries the last error.
const ConditionChatReady = "Ready"

// ChatStatus is what the operator made and found on the homeserver.
type ChatStatus struct {
	// homeserver is the address the operator last reached the rooms at.
	// +optional
	Homeserver string `json:"homeserver,omitempty"`

	// bot is the Matrix user the access token belongs to.
	// +optional
	Bot string `json:"bot,omitempty"`

	// space is the Space the rooms belong to.
	// +optional
	Space *ChatRoomRef `json:"space,omitempty"`

	// rooms are the Space's rooms, one per purpose.
	// +listType=map
	// +listMapKey=purpose
	// +optional
	Rooms []ChatRoomStatus `json:"rooms,omitempty"`

	// invited are the people invited to the Space and every room.
	// +listType=set
	// +optional
	Invited []string `json:"invited,omitempty"`

	// conditions: Ready.
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

// Chat is a project's Space on a Matrix homeserver, where people follow and
// steer the factory: one per project namespace.
type Chat struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec is the homeserver, the bot's Secret, the rooms and the people.
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
