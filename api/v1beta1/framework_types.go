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

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=theos;c4catalog
// +kubebuilder:storageversion
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
