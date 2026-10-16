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

	// checks is when CI runs the application's full check: EveryChange runs it on
	// every pull request and push to main; BeforeRelease runs a light check on each
	// change and the full one on the commit a release will be tagged at. It takes
	// effect only when the Standard says it can (checksBeforeRelease); the
	// condition ChecksApplied says when it doesn't.
	// +kubebuilder:default=EveryChange
	// +optional
	Checks ChecksMode `json:"checks,omitempty"`

	// operations is what the application promises whatever tier runs it: how
	// long its backups are kept and where they are copied, its recovery
	// objectives and who its alarms tell. The tier itself is the deploy
	// target's, never the Container's. A field left out takes the factory's
	// default; a change after the scaffold shows on the condition
	// OperationsApplied.
	// +optional
	Operations *OperationsSpec `json:"operations,omitempty"`
}

// OperationsSpec is what a Container promises in any tier. Every field is
// optional: the factory fills in the defaults when it renders the block, and
// never writes them back here.
type OperationsSpec struct {
	// backups says how long backups are kept and where they are copied.
	// +optional
	Backups *OperationsBackups `json:"backups,omitempty"`

	// recovery states the recovery objectives the runbook, the alarms and the
	// checks are written to.
	// +optional
	Recovery *OperationsRecovery `json:"recovery,omitempty"`

	// alerts says who the alarms tell by default.
	// +optional
	Alerts *OperationsAlerts `json:"alerts,omitempty"`
}

// OperationsBackups is a Container's backup choices.
type OperationsBackups struct {
	// retentionDays is how many days daily backups are kept in production; 0
	// means point-in-time recovery only. Left out, 35.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=3650
	// +optional
	RetentionDays *int32 `json:"retentionDays,omitempty"`

	// copyToRegion is a second AWS region the backups are copied to, such as
	// eu-west-1; empty means none.
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:Pattern=`^([a-z]{2}(-[a-z]+)+-[0-9]+)?$`
	// +optional
	CopyToRegion string `json:"copyToRegion,omitempty"`
}

// OperationsRecovery is a Container's recovery objectives, each a number of
// minutes (m), hours (h) or days (d).
type OperationsRecovery struct {
	// rpo is the recovery point objective: how much data a recovery may lose.
	// Left out, 1h.
	// +kubebuilder:validation:MaxLength=16
	// +kubebuilder:validation:Pattern=`^[0-9]+(m|h|d)$`
	// +optional
	RPO string `json:"rpo,omitempty"`

	// rto is the recovery time objective: how long a recovery may take. Left
	// out, 4h.
	// +kubebuilder:validation:MaxLength=16
	// +kubebuilder:validation:Pattern=`^[0-9]+(m|h|d)$`
	// +optional
	RTO string `json:"rto,omitempty"`
}

// OperationsAlerts is who a Container's alarms tell.
type OperationsAlerts struct {
	// email is the address the alarms tell by default; a deploy target
	// overrides it per environment. Empty means nobody is told.
	// +kubebuilder:validation:MaxLength=254
	// +kubebuilder:validation:Pattern=`^([^@\s]+@[^@\s]+)?$`
	// +optional
	Email string `json:"email,omitempty"`
}

// ChecksMode is when a Container's CI runs its full check.
// +kubebuilder:validation:Enum=EveryChange;BeforeRelease
type ChecksMode string

// Checks modes.
const (
	ChecksEveryChange   ChecksMode = "EveryChange"
	ChecksBeforeRelease ChecksMode = "BeforeRelease"
)

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

// ContainerStandardStatus says which ref of its Standard the Container's
// Standard files are at, and the update in flight.
type ContainerStandardStatus struct {
	// name is the Standard's name.
	// +required
	Name string `json:"name"`

	// ref is the Standard's template ref the repository's Standard files are at:
	// the scaffold's, then each merged update's.
	// +required
	Ref string `json:"ref"`

	// checks is when the repository's CI runs the full check, as its Standard
	// files were last rendered: the scaffold's, then each merged update's. Empty
	// reads as EveryChange.
	// +optional
	Checks ChecksMode `json:"checks,omitempty"`

	// leftOut are the paths of behind, kept for what reads them.
	// +listType=atomic
	// +optional
	LeftOut []string `json:"leftOut,omitempty"`

	// behind are the Standard's files an update left out, because the
	// repository had changed them in lines the Standard changed too, each with
	// the ref its repository copy was last brought to. Each later update merges
	// them against that ref; they stay until it brings them up to date or a
	// person settles them.
	// +listType=map
	// +listMapKey=path
	// +optional
	Behind []StandardFile `json:"behind,omitempty"`

	// kept are the Standard's files a person chose to keep as the repository
	// has them, each with the ref it was kept at.
	// +listType=map
	// +listMapKey=path
	// +optional
	Kept []StandardFile `json:"kept,omitempty"`

	// update is the Standard update in flight, or the last one, stopped.
	// +optional
	Update *StandardUpdateStatus `json:"update,omitempty"`
}

// StandardFile is one of a Standard's files in the repository, at a ref of the
// Standard.
type StandardFile struct {
	// path is the file's path in the repository.
	// +required
	Path string `json:"path"`

	// ref is the Standard's template ref the file was last brought to, or
	// kept at.
	// +required
	Ref string `json:"ref"`

	// pullRequestURL is the address of the update's pull request that last
	// left the file out, when one did.
	// +optional
	PullRequestURL string `json:"pullRequestURL,omitempty"`

	// change is, for a file behind, what the Standard changed in it from ref
	// to the Standard's ref the files are at, as a unified diff, and the
	// repository's lines taking the Standard's version would drop, as they
	// were when the update that left it out was planned. It is cut at 4 KiB,
	// and only the first 16 files behind carry it; the rest say so.
	// +kubebuilder:validation:MaxLength=4096
	// +optional
	Change string `json:"change,omitempty"`
}

// StandardUpdateStatus is one Standard update: a pull request the operator
// opened to bring the Standard's files to another ref, or, when take is set, to
// take the Standard's version at its ref of the files behind.
type StandardUpdateStatus struct {
	// ref is the Standard's template ref the update brings the files to.
	// +required
	Ref string `json:"ref"`

	// take says the pull request takes the Standard's version at ref of the
	// files behind, as a person answered with the standard-take annotation.
	// +optional
	Take bool `json:"take,omitempty"`

	// checks is when CI runs the full check once the update merges. Empty reads
	// as EveryChange.
	// +optional
	Checks ChecksMode `json:"checks,omitempty"`

	// branch is the update's branch, and commit the one commit on it.
	// +optional
	Branch string `json:"branch,omitempty"`

	// +optional
	Commit string `json:"commit,omitempty"`

	// pullRequest is the number of the update's pull request, and pullRequestURL
	// its address.
	// +optional
	PullRequest int32 `json:"pullRequest,omitempty"`

	// +optional
	PullRequestURL string `json:"pullRequestURL,omitempty"`

	// paths are the files the update writes or removes.
	// +listType=atomic
	// +optional
	Paths []string `json:"paths,omitempty"`

	// merged are the paths the update writes that the repository changed too
	// since the Standard's files were last rendered, in other lines: the update
	// writes the two changes merged.
	// +listType=atomic
	// +optional
	Merged []string `json:"merged,omitempty"`

	// leftOut are the files the update leaves out, because the repository
	// changed them since the Standard's files were last rendered in ways that
	// don't merge with the Standard's changes.
	// +listType=atomic
	// +optional
	LeftOut []string `json:"leftOut,omitempty"`

	// behind are the files behind once the update merges, each with the ref
	// its copy is at and the Standard's change to it; status.standard.behind
	// takes them when it merges.
	// +listType=map
	// +listMapKey=path
	// +optional
	Behind []StandardFile `json:"behind,omitempty"`

	// stopped is why the update stopped without merging, such as UpdateClosed;
	// a stopped update waits for the Standard's ref or the Container's effective
	// checks to change, except NoLightCheck, which is planned again every pass.
	// +optional
	Stopped string `json:"stopped,omitempty"`
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

	// standard says which ref of its Standard the repository's Standard files are
	// at, and the update in flight. Only a Ready Container with a Standard that
	// was part of its scaffold has it.
	// +optional
	Standard *ContainerStandardStatus `json:"standard,omitempty"`

	// operations is the operations block the scaffold rendered with, its
	// defaults filled in. OperationsApplied compares spec.operations to it. A
	// Container scaffolded before this was kept has none.
	// +optional
	Operations *OperationsSpec `json:"operations,omitempty"`

	// activeSessions is the number of Active Sessions on this Container.
	// +optional
	ActiveSessions int32 `json:"activeSessions"`

	// observedGeneration is the spec generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: ReferencesResolved, ChoicesValid, RepositoryCreated, Scaffolded
	// and, for a Container with a Standard, StandardUpdated and ChecksApplied;
	// once scaffolded, OperationsApplied.
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
