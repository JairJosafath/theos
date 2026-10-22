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
// +kubebuilder:resource:scope=Namespaced,categories=c4;c4catalog
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
