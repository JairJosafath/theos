/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/
package v1beta1

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
	// the paths the platform always protects (.gitea/, .devcontainer/, .theos.yaml).
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:Pattern=`^[^/*?\[\]][^*?\[\]]*$`
	// +listType=set
	// +optional
	ProtectedPaths []string `json:"protectedPaths,omitempty"`

	// checksBeforeRelease says the Standard's templates can run the full check
	// only before a release: a light check on each change when a Container asks
	// for checks BeforeRelease, and the full one on a release-check/** branch.
	// Without it, its Containers run the full check on every change.
	// +optional
	ChecksBeforeRelease bool `json:"checksBeforeRelease,omitempty"`

	// testEnvironments names the TestEnvironments, such as browser, that
	// the runs of its Containers' Tasks get when the project has them
	// (docs/plans/done/test-environments.md, T2). One the project lacks gives the
	// Container TestEnvironments=False, NotInProject, and the runs go on
	// without it; an issue labelled env/<name> asks for one it can't do
	// without.
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:items:Pattern=`^[a-z]([-a-z0-9]{0,30}[a-z0-9])?$`
	// +listType=set
	// +optional
	TestEnvironments []string `json:"testEnvironments,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=theos;c4catalog
// +kubebuilder:storageversion
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
