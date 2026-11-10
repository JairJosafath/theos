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

// BundleSpec names the catalog entries a team picked together. It names
// entries only: never a Connector (those come through Relationships), a
// DeployTarget or a credential.
type BundleSpec struct {
	// displayName is a human-friendly name.
	// +kubebuilder:validation:MaxLength=100
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// description says what the bundle is for.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Description string `json:"description,omitempty"`

	// frameworkRef names the Framework catalog entry.
	// +required
	FrameworkRef LocalRef `json:"frameworkRef"`

	// standardRef names the Standard catalog entry. It must list the Framework.
	// +optional
	StandardRef *LocalRef `json:"standardRef,omitempty"`

	// computeRef names the Compute catalog entry.
	// +optional
	ComputeRef *LocalRef `json:"computeRef,omitempty"`

	// deploymentRef names the Deployment catalog entry.
	// +optional
	DeploymentRef *LocalRef `json:"deploymentRef,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=theos;c4catalog
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Framework",type=string,JSONPath=".spec.frameworkRef.name"
// +kubebuilder:printcolumn:name="Standard",type=string,JSONPath=".spec.standardRef.name"
// +kubebuilder:printcolumn:name="Compute",type=string,JSONPath=".spec.computeRef.name"
// +kubebuilder:printcolumn:name="Deployment",type=string,JSONPath=".spec.deploymentRef.name"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Bundle is a catalog entry with no template of its own: a Framework, Standard,
// Compute and Deployment picked together, which a Container names instead of
// the four. It has no reconciler and no status; what it supports is the
// intersection of its entries' supportedTypes, computed where it is read.
// It is owned by the Project.
type Bundle struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec names the bundle's catalog entries
	// +required
	Spec BundleSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// BundleList contains a list of Bundle
type BundleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Bundle `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Bundle{}, &BundleList{})
		return nil
	})
}
