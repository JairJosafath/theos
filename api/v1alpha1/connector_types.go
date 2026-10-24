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

// ConnectorSpec is a catalog entry for what a Container needs to talk to a
// DataStore: a pinned template, the Container types it supports and, when its
// files only work with code a Framework renders, the Frameworks that wire it.
type ConnectorSpec struct {
	CatalogEntrySpec `json:",inline"`

	// wiredBy lists the Frameworks, by name, whose templates call this
	// Connector's files. When set, a DataStore on this Connector is refused
	// for a Container whose Framework is not listed (ChoicesValid=False,
	// reason NotWired). Unset means no restriction: a Connector whose files
	// stand alone, such as the cluster's postgres, needs no wiring.
	// +kubebuilder:validation:MaxItems=64
	// +listType=set
	// +optional
	WiredBy []string `json:"wiredBy,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=c4;c4catalog
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=".spec.displayName"
// +kubebuilder:printcolumn:name="Types",type=string,JSONPath=".spec.supportedTypes"
// +kubebuilder:printcolumn:name="Ref",type=string,JSONPath=".spec.template.ref"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Connector is a catalog entry: what a Container needs to talk to a DataStore, such as Postgres.
// It is owned by the Project.
type Connector struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the template, the Container types it supports and the Frameworks that wire it
	// +required
	Spec ConnectorSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// ConnectorList contains a list of Connector
type ConnectorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Connector `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Connector{}, &ConnectorList{})
		return nil
	})
}
