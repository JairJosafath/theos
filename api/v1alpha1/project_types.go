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

// ProjectSpec defines the desired state of Project.
type ProjectSpec struct {
	// displayName is a human-friendly name for the story.
	// +kubebuilder:validation:MaxLength=100
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// description tells what the project is about.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Description string `json:"description,omitempty"`
}

// ProjectStatus rolls up the state of everything in the project.
type ProjectStatus struct {
	// softwareSystems is the number of SoftwareSystems in the project.
	// +optional
	SoftwareSystems int32 `json:"softwareSystems"`

	// containers is the number of Containers in the project.
	// +optional
	Containers int32 `json:"containers"`

	// readyContainers is the number of Containers in phase Ready.
	// +optional
	ReadyContainers int32 `json:"readyContainers"`

	// activeSessions is the number of Active Sessions across all Containers.
	// +optional
	ActiveSessions int32 `json:"activeSessions"`

	// summary is a one-line roll-up, such as "3 of 4 containers Ready, 2 active sessions".
	// +optional
	Summary string `json:"summary,omitempty"`

	// conditions represent the current state of the Project.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=c4
// +kubebuilder:printcolumn:name="Systems",type=integer,JSONPath=".status.softwareSystems"
// +kubebuilder:printcolumn:name="Containers",type=integer,JSONPath=".status.containers"
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=".status.readyContainers"
// +kubebuilder:printcolumn:name="Sessions",type=integer,JSONPath=".status.activeSessions"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Project is the whole story, one per namespace. Its name equals the namespace.
type Project struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Project
	// +optional
	Spec ProjectSpec `json:"spec"`

	// status defines the observed state of Project
	// +optional
	Status ProjectStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ProjectList contains a list of Project
type ProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Project `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Project{}, &ProjectList{})
		return nil
	})
}
