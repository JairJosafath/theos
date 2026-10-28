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

// PersonSpec defines the desired state of Person.
type PersonSpec struct {
	// displayName is a human-friendly name, such as "Customer".
	// +kubebuilder:validation:MaxLength=100
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// description says who this is and what they want.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Description string `json:"description,omitempty"`
}

// +kubebuilder:deprecatedversion:warning="c4.factory.io/v1alpha1 is deprecated since v0.30.0; use c4.factory.io/v1beta1. A release 90 or more days after v0.30.0 may drop it (#1963)."
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=c4
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=".spec.displayName"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Person is a user, role or stakeholder in the story. It is owned by the Project.
type Person struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Person
	// +optional
	Spec PersonSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// PersonList contains a list of Person
type PersonList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Person `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Person{}, &PersonList{})
		return nil
	})
}
