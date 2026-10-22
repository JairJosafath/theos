/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/
package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=c4;c4catalog
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=".spec.displayName"
// +kubebuilder:printcolumn:name="Types",type=string,JSONPath=".spec.supportedTypes"
// +kubebuilder:printcolumn:name="Ref",type=string,JSONPath=".spec.template.ref"
// +kubebuilder:printcolumn:name="Deploys Itself",type=boolean,JSONPath=".spec.deploysItself",priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Framework is a catalog entry: the language, framework and tooling of a Container.
// It is owned by the Project.
type Framework struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the template and the Container types it supports
	// +required
	Spec FrameworkSpec `json:"spec"`
}

// FrameworkSpec is a catalog entry that can also be its Container's deployment.
type FrameworkSpec struct {
	CatalogEntrySpec `json:",inline"`

	// deploysItself says the template carries its own deployment, such as a
	// CDK app that builds and runs the Container on AWS. A Container with this
	// Framework takes no Compute and no Deployment but gitea-release, which
	// ships its cloud assembly; the operator fails one that names another.
	// +optional
	DeploysItself bool `json:"deploysItself,omitempty"`

	// readsOperations says the template at spec.template.ref renders
	// infra/operations.ts from a Container's spec.operations. A Container with
	// a Framework that doesn't gets no OperationsApplied condition, and with a
	// block set reports it False, NotReadByFramework; a Brief's planner
	// refuses operations on such a Container.
	// +optional
	ReadsOperations bool `json:"readsOperations,omitempty"`
}

// +kubebuilder:object:root=true

// FrameworkList contains a list of Framework
type FrameworkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Framework `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Framework{}, &FrameworkList{})
		return nil
	})
}
