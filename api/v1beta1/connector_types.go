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
// +kubebuilder:resource:scope=Namespaced,categories=theos;c4catalog
// +kubebuilder:storageversion
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
