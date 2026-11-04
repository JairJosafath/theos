/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/
package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Actor says who is at work in a Session.
// +kubebuilder:validation:Enum=agent;human
type Actor string

// Actors.
const (
	ActorAgent Actor = "agent"
	ActorHuman Actor = "human"
)

// SessionSpec defines the desired state of Session.
type SessionSpec struct {
	// containerRef names the Container being worked on. It can't change.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="containerRef is immutable"
	// +required
	ContainerRef LocalRef `json:"containerRef"`

	// actor is agent or human. Both are equal in the model.
	// +required
	Actor Actor `json:"actor"`

	// summary is a one-line description of the work, such as "Adding order validation".
	// +kubebuilder:validation:MaxLength=200
	// +optional
	Summary string `json:"summary,omitempty"`
}

// SessionPhase is Active while heartbeats arrive and Stale when they stop.
// +kubebuilder:validation:Enum=Active;Stale
type SessionPhase string

// Session phases.
const (
	SessionActive SessionPhase = "Active"
	SessionStale  SessionPhase = "Stale"
)

// SessionStatus is written by the client. The operator only marks it Stale.
type SessionStatus struct {
	// phase is Active or Stale.
	// +optional
	Phase SessionPhase `json:"phase,omitempty"`

	// lastHeartbeat is when the client last reported in.
	// +optional
	LastHeartbeat *metav1.Time `json:"lastHeartbeat,omitempty"`

	// openInEditor is a link to the devcontainer, set only when it is reachable.
	// +optional
	OpenInEditor string `json:"openInEditor,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=theos
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Container",type=string,JSONPath=".spec.containerRef.name"
// +kubebuilder:printcolumn:name="Actor",type=string,JSONPath=".spec.actor"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Summary",type=string,JSONPath=".spec.summary"
// +kubebuilder:printcolumn:name="Heartbeat",type=date,JSONPath=".status.lastHeartbeat"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Session is an agent or human at work in a devcontainer, owned by its Container.
type Session struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Session
	// +required
	Spec SessionSpec `json:"spec"`

	// status defines the observed state of Session
	// +optional
	Status SessionStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// SessionList contains a list of Session
type SessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Session `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Session{}, &SessionList{})
		return nil
	})
}
