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

// RelationshipSpec defines the desired state of Relationship.
// +kubebuilder:validation:XValidation:rule="!(self.source.kind == self.target.kind && self.source.name == self.target.name)",message="a Relationship cannot point to itself"
// +kubebuilder:validation:XValidation:rule="!has(self.bundles) || !self.bundles || (self.source.kind == 'Container' && self.target.kind == 'Container')",message="only a Container to Container link can bundle"
type RelationshipSpec struct {
	// source is the element the link starts from, and its owner. It can't change.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="source is immutable"
	// +required
	Source ElementRef `json:"source"`

	// target is the element the link points to. It can't change: whether a link
	// was applied is recorded by name, so point elsewhere with a new Relationship.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="target is immutable"
	// +required
	Target ElementRef `json:"target"`

	// description says what the link is, such as "Reads and writes orders".
	// +kubebuilder:validation:MaxLength=200
	// +optional
	Description string `json:"description,omitempty"`

	// bundles says the source Container ships a pinned release of the target
	// Container, as an extension ships its server. Each release the target's Crew
	// tags files a bump issue in the source's repository. Only a Container to
	// Container link can bundle; it can change, as it touches no scaffold.
	// +optional
	Bundles bool `json:"bundles,omitempty"`
}

// RelationshipStatus defines the observed state of Relationship.
type RelationshipStatus struct {
	// conditions: ReferencesResolved, and Applied for a Container to DataStore link.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=c4,shortName=rel
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=".spec.source.name"
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=".spec.target.name"
// +kubebuilder:printcolumn:name="Description",type=string,JSONPath=".spec.description"
// +kubebuilder:printcolumn:name="Applied",type=string,JSONPath=".status.conditions[?(@.type=='Applied')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Relationship is a directed link between two elements, owned by its source.
// Only a Container to DataStore link changes a scaffold; the rest tell the story.
type Relationship struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Relationship
	// +required
	Spec RelationshipSpec `json:"spec"`

	// status defines the observed state of Relationship
	// +optional
	Status RelationshipStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// RelationshipList contains a list of Relationship
type RelationshipList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Relationship `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Relationship{}, &RelationshipList{})
		return nil
	})
}
