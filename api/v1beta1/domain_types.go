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

// DomainSpec is a catalog entry for what a system is about, such as a webshop:
// its practices, common mistakes and controversies, as Markdown an agent reads.
// It has no supportedTypes: every Container of a system that names it gets it.
type DomainSpec struct {
	// displayName is a human-friendly name, such as "Webshop".
	// +kubebuilder:validation:MaxLength=100
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// description says what the domain covers.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Description string `json:"description,omitempty"`

	// template is the repository and pinned ref to scaffold from.
	// +required
	Template TemplateSource `json:"template"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=theos;c4catalog
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=".spec.displayName"
// +kubebuilder:printcolumn:name="Ref",type=string,JSONPath=".spec.template.ref"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Domain is a catalog entry: what a SoftwareSystem is about, such as a webshop.
// A system names its Domains in spec.domainRefs, and each of its Containers is
// scaffolded with them under docs/agents/domain/<name>/. It is owned by the Project.
type Domain struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the template
	// +required
	Spec DomainSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// DomainList contains a list of Domain
type DomainList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Domain `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Domain{}, &DomainList{})
		return nil
	})
}
