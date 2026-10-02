/*
Copyright 2026.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// CrewSpec is who works on a Container's issues: the Agent that implements, the
// Agent that reviews, and whether they pick up ready issues.
type CrewSpec struct {
	// containerRef names the Container whose repository's issues the Crew works
	// on. It can't change.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="containerRef is immutable"
	// +required
	ContainerRef LocalRef `json:"containerRef"`

	// displayName is a human-friendly name.
	// +kubebuilder:validation:MaxLength=100
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// description says what the Crew is for.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Description string `json:"description,omitempty"`

	// implementer names the Agent that implements each issue.
	// +required
	Implementer LocalRef `json:"implementer"`

	// reviewer names the Agent that reviews each round; usually a stronger model
	// than the implementer's.
	// +required
	Reviewer LocalRef `json:"reviewer"`

	// paused stops the Crew from picking up new issues. A Task already started
	// runs to its end.
	// +optional
	Paused bool `json:"paused,omitempty"`
}

// CrewPhase says what a Crew is doing.
// +kubebuilder:validation:Enum=Waiting;Idle;Working;Paused
type CrewPhase string

// Crew phases.
const (
	// CrewWaiting means the Crew can't pick up issues yet, such as while its
	// Container isn't Ready; the Ready condition says why.
	CrewWaiting CrewPhase = "Waiting"
	// CrewIdle means no issue is ready to pick up.
	CrewIdle CrewPhase = "Idle"
	// CrewWorking means a Task works on one of the Container's issues.
	CrewWorking CrewPhase = "Working"
	// CrewPaused means spec.paused is set.
	CrewPaused CrewPhase = "Paused"
)

// CrewStatus is what the Crew saw on its last look at the issues.
type CrewStatus struct {
	// phase is Waiting, Idle, Working or Paused.
	// +optional
	Phase CrewPhase `json:"phase,omitempty"`

	// activeTask names the Task working on the Container now, if any.
	// +optional
	ActiveTask string `json:"activeTask,omitempty"`

	// ready lists the ready issues the Crew can start, in pick-up order.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=100
	// +optional
	Ready []int32 `json:"ready,omitempty"`

	// waiting lists the ready issues blocked by open dependencies.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=100
	// +optional
	Waiting []int32 `json:"waiting,omitempty"`

	// lastPolled is when the Crew last read the issues.
	// +optional
	LastPolled *metav1.Time `json:"lastPolled,omitempty"`

	// observedGeneration is the spec generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Ready says whether the Crew can pick up issues, and why not.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=c4
// +kubebuilder:printcolumn:name="Container",type=string,JSONPath=".spec.containerRef.name"
// +kubebuilder:printcolumn:name="Implementer",type=string,JSONPath=".spec.implementer.name"
// +kubebuilder:printcolumn:name="Reviewer",type=string,JSONPath=".spec.reviewer.name"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Task",type=string,JSONPath=".status.activeTask"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Crew is the team of Agents that works on a Container's issues: it picks up
// issues labelled ready, in order and once their dependencies are closed, and
// starts a Task for each, one at a time. It is owned by its Container.
type Crew struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the Container and the Agents for each role
	// +required
	Spec CrewSpec `json:"spec"`

	// status defines the observed state of Crew
	// +optional
	Status CrewStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// CrewList contains a list of Crew
type CrewList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Crew `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Crew{}, &CrewList{})
		return nil
	})
}
