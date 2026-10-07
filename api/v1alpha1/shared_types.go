/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
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
// +kubebuilder:validation:Enum=static-site;service;job;worker;mobile-app;cli;extension
type ContainerType string

// Container types.
const (
	ContainerTypeStaticSite ContainerType = "static-site"
	ContainerTypeService    ContainerType = "service"
	ContainerTypeJob        ContainerType = "job"
	ContainerTypeWorker     ContainerType = "worker"
	ContainerTypeMobileApp  ContainerType = "mobile-app"
	ContainerTypeCLI        ContainerType = "cli"
	ContainerTypeExtension  ContainerType = "extension"
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
	// ConditionStandardUpdated is True when a Container's Standard files are at
	// its Standard's ref.
	ConditionStandardUpdated = "StandardUpdated"
)

// Condition reasons.
const (
	ReasonResolved             = "Resolved"
	ReasonNotFound             = "NotFound"
	ReasonValid                = "Valid"
	ReasonUnsupportedType      = "UnsupportedType"
	ReasonUnsupportedFramework = "UnsupportedFramework"
	ReasonCreated              = "Created"
	ReasonScaffolding          = "Scaffolding"
	ReasonScaffolded           = "Scaffolded"
	ReasonFailed               = "Failed"
	ReasonGitError             = "GitError"
	ReasonForeignCommits       = "ForeignCommits"
	ReasonLayerConflict        = "LayerConflict"
	ReasonUnpinnedRef          = "UnpinnedRef"
	ReasonWaiting              = "WaitingForScaffold"
	ReasonAppliedAtScaffold    = "AppliedAtScaffold"
	ReasonAddedAfterScaffold   = "AddedAfterScaffold"
	ReasonUpToDate             = "UpToDate"
	ReasonUpdating             = "Updating"
	ReasonChecksFailed         = "ChecksFailed"
	ReasonUpdateClosed         = "UpdateClosed"
	ReasonUpdateRefused        = "UpdateRefused"
)

// Labels the operator sets so a resource can be found by its ancestors.
const (
	LabelProject        = "c4.factory.io/project"
	LabelSoftwareSystem = "c4.factory.io/software-system"
	LabelContainer      = "c4.factory.io/container"
	// LabelBrief names the Brief an object came from.
	LabelBrief = "c4.factory.io/brief"
	// LabelBriefRound is the Brief round an object came from, or a Plan Task plans.
	LabelBriefRound = "c4.factory.io/brief-round"
	// LabelBriefRoundKind on a Plan Task is "tend" when its round is a tending
	// round: no new prompt, it decides the Brief's failed issues.
	LabelBriefRoundKind = "c4.factory.io/brief-round-kind"
)

// BriefRoundKindTend is LabelBriefRoundKind's value on a tending round's Plan Task.
const BriefRoundKindTend = "tend"

// Shared credentials: the operator copies Secrets from its own namespace into
// the project namespaces that opt in.
const (
	// LabelSharedSecrets on a Namespace, "true", opts it in. Only an admin or
	// the chart can label a Namespace, so a Project alone receives nothing.
	LabelSharedSecrets = "c4.factory.io/shared-secrets"
	// LabelSharedFrom marks a Secret the operator copied, naming its source.
	// A Secret without it is the project's own, which the operator never touches.
	LabelSharedFrom = "c4.factory.io/shared-from"
)

// The Brief's verdict on a Plan Task's plan, written by the Brief on the Task.
const (
	// AnnotationPlanChecked is the commit of the plan the Brief checked.
	AnnotationPlanChecked = "c4.factory.io/plan-checked"
	// AnnotationPlanRefusals are the Brief's reasons for refusing the checked
	// plan; absent when it accepted it.
	AnnotationPlanRefusals = "c4.factory.io/plan-refusals"
)

// AnnotationRetry, set or changed on a Failed Container, makes the operator try
// to scaffold it again: after emptying its repository, for example.
const AnnotationRetry = "c4.factory.io/retry"

// CheckStatus is the combined state of the CI checks on one commit.
type CheckStatus struct {
	// commit is the SHA the checks ran on.
	// +optional
	Commit string `json:"commit,omitempty"`

	// state is pending, success, failure or error; empty until a check reports.
	// +optional
	State string `json:"state,omitempty"`

	// url links to the check run.
	// +optional
	URL string `json:"url,omitempty"`
}
