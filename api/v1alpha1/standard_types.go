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

// StandardSpec is a catalog entry for how code must be written and checked: lint
// and format configuration, the agent guide, style digests, the check contract
// and CI. It fits only the Frameworks it lists.
type StandardSpec struct {
	CatalogEntrySpec `json:",inline"`

	// supportedFrameworks lists the Frameworks, by name, whose code this Standard can check.
	// +kubebuilder:validation:MinItems=1
	// +listType=set
	// +required
	SupportedFrameworks []string `json:"supportedFrameworks"`

	// protectedPaths are the Standard's files that agents must not change, such as
	// the check contract and lint configuration: a file path, or a directory ending
	// in "/". The Git host refuses to merge pull requests that change them, next to
	// the paths the platform always protects (.gitea/, .devcontainer/, .c4factory.yaml).
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:Pattern=`^[^/*?\[\]][^*?\[\]]*$`
	// +listType=set
	// +optional
	ProtectedPaths []string `json:"protectedPaths,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=c4;c4catalog
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=".spec.displayName"
// +kubebuilder:printcolumn:name="Frameworks",type=string,JSONPath=".spec.supportedFrameworks"
// +kubebuilder:printcolumn:name="Ref",type=string,JSONPath=".spec.template.ref"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Standard is a catalog entry: the coding rules and checks of a Container. It is
// rendered as a layer right after the Framework. It is owned by the Project.
type Standard struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the template, and the Container types and Frameworks it supports
	// +required
	Spec StandardSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// StandardList contains a list of Standard
type StandardList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Standard `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Standard{}, &StandardList{})
		return nil
	})
}
