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

// LocalRef names an object of a known kind in the same namespace.
// There are no references across namespaces: reuse is by copying.
type LocalRef struct {
	// name of the referenced object.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +required
	Name string `json:"name"`
}

// ElementKind is a kind of story element that a Relationship can connect.
// +kubebuilder:validation:Enum=Person;SoftwareSystem;Container;DataStore
type ElementKind string

// Story element kinds.
const (
	ElementPerson         ElementKind = "Person"
	ElementSoftwareSystem ElementKind = "SoftwareSystem"
	ElementContainer      ElementKind = "Container"
	ElementDataStore      ElementKind = "DataStore"
)

// ElementRef names a story element in the same namespace.
type ElementRef struct {
	// kind of the element.
	// +required
	Kind ElementKind `json:"kind"`

	// name of the element.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +required
	Name string `json:"name"`
}

// ContainerType is the fixed list of application shapes a Container can take.
// +kubebuilder:validation:Enum=static-site;service;job;worker
type ContainerType string

// Container types.
const (
	ContainerTypeStaticSite ContainerType = "static-site"
	ContainerTypeService    ContainerType = "service"
	ContainerTypeJob        ContainerType = "job"
	ContainerTypeWorker     ContainerType = "worker"
)

// TemplateSource points to a platform-managed template repository at a pinned ref.
type TemplateSource struct {
	// url of the template's Git repository.
	// +kubebuilder:validation:Pattern=`^https?://`
	// +required
	URL string `json:"url"`

	// ref is a tag or a full commit SHA, never a branch.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +required
	Ref string `json:"ref"`
}

// CatalogEntrySpec is shared by the four catalog kinds: Framework, Compute,
// Deployment and Connector. Each is one side of a Container.
type CatalogEntrySpec struct {
	// displayName is a human-friendly name.
	// +kubebuilder:validation:MaxLength=100
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// description says what this entry adds to a Container.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Description string `json:"description,omitempty"`

	// supportedTypes lists the Container types this entry can scaffold.
	// +kubebuilder:validation:MinItems=1
	// +listType=set
	// +required
	SupportedTypes []ContainerType `json:"supportedTypes"`

	// template is the repository and pinned ref to scaffold from.
	// +required
	Template TemplateSource `json:"template"`
}

// Condition types used across the story kinds.
const (
	// ConditionReferencesResolved is True when every object named in spec exists.
	ConditionReferencesResolved = "ReferencesResolved"
	// ConditionChoicesValid is True when every catalog entry supports the Container type.
	ConditionChoicesValid = "ChoicesValid"
	// ConditionRepositoryCreated is True when the Container's repository exists.
	ConditionRepositoryCreated = "RepositoryCreated"
	// ConditionScaffolded is True when the initial commit is pushed.
	ConditionScaffolded = "Scaffolded"
	// ConditionApplied is True when a Relationship's Connector was part of the scaffold.
	ConditionApplied = "Applied"
)

// Condition reasons.
const (
	ReasonResolved           = "Resolved"
	ReasonNotFound           = "NotFound"
	ReasonValid              = "Valid"
	ReasonUnsupportedType    = "UnsupportedType"
	ReasonCreated            = "Created"
	ReasonScaffolding        = "Scaffolding"
	ReasonScaffolded         = "Scaffolded"
	ReasonFailed             = "Failed"
	ReasonGitError           = "GitError"
	ReasonForeignCommits     = "ForeignCommits"
	ReasonLayerConflict      = "LayerConflict"
	ReasonUnpinnedRef        = "UnpinnedRef"
	ReasonWaiting            = "WaitingForScaffold"
	ReasonAppliedAtScaffold  = "AppliedAtScaffold"
	ReasonAddedAfterScaffold = "AddedAfterScaffold"
)

// Labels the operator sets so a resource can be found by its ancestors.
const (
	LabelProject        = "c4.factory.io/project"
	LabelSoftwareSystem = "c4.factory.io/software-system"
	LabelContainer      = "c4.factory.io/container"
)

// AnnotationRetry, set or changed on a Failed Container, makes the operator try
// to scaffold it again: after emptying its repository, for example.
const AnnotationRetry = "c4.factory.io/retry"
