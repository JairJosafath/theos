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

// ContainerSpec defines the desired state of Container.
type ContainerSpec struct {
	// softwareSystemRef names the SoftwareSystem this Container belongs to. It can't change.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="softwareSystemRef is immutable"
	// +required
	SoftwareSystemRef LocalRef `json:"softwareSystemRef"`

	// displayName is a human-friendly name, such as "Orders API".
	// +kubebuilder:validation:MaxLength=100
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// description says what the application does.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Description string `json:"description,omitempty"`

	// type is the shape of the application. Every catalog entry used must support it.
	// +required
	Type ContainerType `json:"type"`

	// gitProviderRef names the GitProvider that hosts the repository.
	// +required
	GitProviderRef LocalRef `json:"gitProviderRef"`

	// frameworkRef names the Framework catalog entry: language, framework and tooling.
	// +required
	FrameworkRef LocalRef `json:"frameworkRef"`

	// standardRef names the Standard catalog entry: coding rules, checks and CI. It
	// must list the Framework.
	// +optional
	StandardRef *LocalRef `json:"standardRef,omitempty"`

	// computeRef names the Compute catalog entry: resource sizing.
	// +optional
	ComputeRef *LocalRef `json:"computeRef,omitempty"`

	// deploymentRef names the Deployment catalog entry: how the application ships.
	// +optional
	DeploymentRef *LocalRef `json:"deploymentRef,omitempty"`
}

// ContainerPhase is a one-word summary of where the Container is in scaffolding.
// +kubebuilder:validation:Enum=Pending;Scaffolding;Ready;Failed
type ContainerPhase string

// Container phases.
const (
	ContainerPending     ContainerPhase = "Pending"
	ContainerScaffolding ContainerPhase = "Scaffolding"
	ContainerReady       ContainerPhase = "Ready"
	ContainerFailed      ContainerPhase = "Failed"
)

// AppliedConnector is what one Relationship added to a scaffold.
type AppliedConnector struct {
	// relationship is the Relationship's name.
	// +required
	Relationship string `json:"relationship"`

	// dataStore is the DataStore the Relationship pointed at.
	// +required
	DataStore string `json:"dataStore"`

	// connector is the DataStore's Connector that was applied.
	// +required
	Connector string `json:"connector"`
}

// ContainerStatus defines the observed state of Container.
type ContainerStatus struct {
	// phase is Pending, Scaffolding, then Ready or Failed.
	// +optional
	Phase ContainerPhase `json:"phase,omitempty"`

	// repositoryURL is the web address of the scaffolded repository.
	// +optional
	RepositoryURL string `json:"repositoryURL,omitempty"`

	// scaffoldedFrom lists the templates used, as name@ref, in layer order.
	// +listType=atomic
	// +optional
	ScaffoldedFrom []string `json:"scaffoldedFrom,omitempty"`

	// appliedRelationships lists the Relationships whose Connectors were part of the scaffold.
	// +listType=set
	// +optional
	AppliedRelationships []string `json:"appliedRelationships,omitempty"`

	// appliedConnectors records, for each applied Relationship, the DataStore it
	// pointed at and the Connector that went into the scaffold. A Container
	// scaffolded before this was kept has only appliedRelationships.
	// +listType=map
	// +listMapKey=relationship
	// +optional
	AppliedConnectors []AppliedConnector `json:"appliedConnectors,omitempty"`

	// checks is the latest CI result on the default branch. Only Containers with a
	// Standard have checks.
	// +optional
	Checks *CheckStatus `json:"checks,omitempty"`

	// activeSessions is the number of Active Sessions on this Container.
	// +optional
	ActiveSessions int32 `json:"activeSessions"`

	// observedGeneration is the spec generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: ReferencesResolved, ChoicesValid, RepositoryCreated and Scaffolded.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=c4
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Checks",type=string,JSONPath=".status.checks.state"
// +kubebuilder:printcolumn:name="Sessions",type=integer,JSONPath=".status.activeSessions"
// +kubebuilder:printcolumn:name="Repository",type=string,JSONPath=".status.repositoryURL",priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Container is an application with its own repository. It is owned by its
// SoftwareSystem and scaffolded exactly once.
type Container struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Container
	// +required
	Spec ContainerSpec `json:"spec"`

	// status defines the observed state of Container
	// +optional
	Status ContainerStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ContainerList contains a list of Container
type ContainerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Container `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Container{}, &ContainerList{})
		return nil
	})
}
