/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
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

// +kubebuilder:deprecatedversion:warning="c4.factory.io/v1alpha1 is deprecated since v0.30.0; use c4.factory.io/v1beta1. A release 90 or more days after v0.30.0 may drop it (#1963)."
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
