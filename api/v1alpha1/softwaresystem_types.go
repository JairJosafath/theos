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

// SoftwareSystemSpec defines the desired state of SoftwareSystem.
type SoftwareSystemSpec struct {
	// displayName is a human-friendly name, such as "Webshop Core".
	// +kubebuilder:validation:MaxLength=100
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// description says what the system does.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Description string `json:"description,omitempty"`

	// needs are the outcomes the system exists for, in the words of its users.
	// +kubebuilder:validation:MaxItems=20
	// +listType=atomic
	// +optional
	Needs []string `json:"needs,omitempty"`

	// kpis are how you know the system delivers.
	// +kubebuilder:validation:MaxItems=20
	// +listType=atomic
	// +optional
	KPIs []string `json:"kpis,omitempty"`
}

// SoftwareSystemStatus rolls up the state of the system's Containers.
type SoftwareSystemStatus struct {
	// containers is the number of Containers in the system.
	// +optional
	Containers int32 `json:"containers"`

	// readyContainers is the number of Containers in phase Ready.
	// +optional
	ReadyContainers int32 `json:"readyContainers"`

	// dataStores is the number of DataStores in the system.
	// +optional
	DataStores int32 `json:"dataStores"`

	// activeSessions is the number of Active Sessions across the system's Containers.
	// +optional
	ActiveSessions int32 `json:"activeSessions"`

	// summary is a one-line roll-up, such as "3 of 4 containers Ready, 2 active sessions".
	// +optional
	Summary string `json:"summary,omitempty"`

	// conditions represent the current state of the SoftwareSystem.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=c4,shortName=sys
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=".spec.displayName"
// +kubebuilder:printcolumn:name="Containers",type=integer,JSONPath=".status.containers"
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=".status.readyContainers"
// +kubebuilder:printcolumn:name="Sessions",type=integer,JSONPath=".status.activeSessions"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// SoftwareSystem is a system that delivers value, with its needs and KPIs.
// It is owned by the Project and owns its Containers and DataStores.
type SoftwareSystem struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of SoftwareSystem
	// +optional
	Spec SoftwareSystemSpec `json:"spec"`

	// status defines the observed state of SoftwareSystem
	// +optional
	Status SoftwareSystemStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// SoftwareSystemList contains a list of SoftwareSystem
type SoftwareSystemList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []SoftwareSystem `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &SoftwareSystem{}, &SoftwareSystemList{})
		return nil
	})
}
