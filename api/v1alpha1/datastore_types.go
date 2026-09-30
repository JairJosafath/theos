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

// DataStoreSpec defines the desired state of DataStore.
type DataStoreSpec struct {
	// softwareSystemRef names the SoftwareSystem this DataStore belongs to. It can't change.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="softwareSystemRef is immutable"
	// +required
	SoftwareSystemRef LocalRef `json:"softwareSystemRef"`

	// displayName is a human-friendly name, such as "Orders DB".
	// +kubebuilder:validation:MaxLength=100
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// description says what is stored.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Description string `json:"description,omitempty"`

	// connectorRef names the Connector catalog entry. A Relationship from a
	// Container to this DataStore adds that Connector to the Container's scaffold.
	// It can't change, since a scaffold that applied it already has it.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="connectorRef is immutable"
	// +required
	ConnectorRef LocalRef `json:"connectorRef"`
}

// DataStoreStatus defines the observed state of DataStore.
type DataStoreStatus struct {
	// conditions: ReferencesResolved.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=c4
// +kubebuilder:printcolumn:name="Connector",type=string,JSONPath=".spec.connectorRef.name"
// +kubebuilder:printcolumn:name="Resolved",type=string,JSONPath=".status.conditions[?(@.type=='ReferencesResolved')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// DataStore is a database or storage, backed by a Connector. It is owned by its
// SoftwareSystem. The factory only scaffolds for it; provisioning is left to other tools.
type DataStore struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of DataStore
	// +required
	Spec DataStoreSpec `json:"spec"`

	// status defines the observed state of DataStore
	// +optional
	Status DataStoreStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DataStoreList contains a list of DataStore
type DataStoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DataStore `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &DataStore{}, &DataStoreList{})
		return nil
	})
}
