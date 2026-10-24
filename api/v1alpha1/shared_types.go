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

// License is the SPDX id of a license the operator holds the text of, the
// set a Project's or a Container's spec.license may name. The scaffold writes
// that text as the repository's LICENSE (docs/design.md, "Write LICENSE");
// the schema refuses any other id at admission. Adding one is its text under
// internal/scaffold/licenses and an entry here.
// +kubebuilder:validation:Enum=MIT;Apache-2.0;BSD-2-Clause;BSD-3-Clause;ISC;0BSD;MPL-2.0;GPL-2.0-only;GPL-3.0-only;LGPL-3.0-only;AGPL-3.0-only;Unlicense
type License string

// ContainerType is the fixed list of application shapes a Container can take.
// +kubebuilder:validation:Enum=static-site;service;job;worker;mobile-app;cli;extension;operator;function
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
	ContainerTypeOperator   ContainerType = "operator"
	ContainerTypeFunction   ContainerType = "function"
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
	// its Standard's ref; False with reason FilesBehind while some an update
	// left out wait on a person.
	ConditionStandardUpdated = "StandardUpdated"
	// ConditionBundleCurrent is True when a Ready Container's Bundle still
	// names the entries it scaffolded from; False with reason BundleChanged
	// listing the sides that differ, or saying the Bundle is gone. A changed
	// Bundle is reported, never applied. Absent on a Container naming its sides.
	ConditionBundleCurrent = "BundleCurrent"
	// ConditionChecksApplied is True when a Container's Standard runs its CI's
	// full check when the Container's spec.checks asks, and its repository's CI
	// already does; False with the update in flight or its stop while not.
	ConditionChecksApplied = "ChecksApplied"
	// ConditionDomainsApplied is True when a Ready Container's system names the
	// Domains the Container was scaffolded with; False with reason
	// AddedAfterScaffold listing the names added and removed since (a Domain is
	// written once, at the scaffold). Absent when the system names none and
	// none was scaffolded.
	ConditionDomainsApplied = "DomainsApplied"
	// ConditionOperationsApplied is True when a scaffolded Container's
	// spec.operations is the block its repository's infra/operations.ts was
	// last rendered with; False with reason Updating while the pull request
	// that renders a changed block waits for CI, or with why it stopped.
	ConditionOperationsApplied = "OperationsApplied"
	// ConditionDeployed is True when each entry of a Ready Container's
	// spec.deploy runs its wanted version; False with the first reason that
	// holds, in the order of internal/model's DeployedCondition.
	ConditionDeployed = "Deployed"
	// ConditionImagePublished is True on a Container whose Deployment builds an
	// image when its newest release's image is pushed to the owner's
	// registry; False with status.image's state as the reason.
	ConditionImagePublished = "ImagePublished"
)

// Condition reasons.
const (
	ReasonResolved             = "Resolved"
	ReasonNotFound             = "NotFound"
	ReasonValid                = "Valid"
	ReasonUnsupportedType      = "UnsupportedType"
	ReasonUnsupportedFramework = "UnsupportedFramework"
	ReasonUnsupportedCI        = "UnsupportedCI"
	ReasonDeploysItself        = "DeploysItself"
	ReasonCreated              = "Created"
	ReasonScaffolding          = "Scaffolding"
	ReasonScaffolded           = "Scaffolded"
	ReasonFailed               = "Failed"
	ReasonGitError             = "GitError"
	ReasonForeignCommits       = "ForeignCommits"
	// ReasonNothingToAdopt: the Container has spec.adopt set but its
	// repository has no commits; there is nothing to take over.
	ReasonNothingToAdopt = "NothingToAdopt"
	// ReasonNoDefaultBranch: an adopted repository has commits but no main
	// branch to read the identity and the Standard's files from; rename its
	// default branch to main before adopting it.
	ReasonNoDefaultBranch = "NoDefaultBranch"
	// ReasonIdentityMismatch: an adopted repository's main has no
	// .c4factory.yaml, or one naming another project or Container.
	ReasonIdentityMismatch = "IdentityMismatch"
	// ReasonStandardFilesDiffer: an adopted repository's main is missing a
	// file of the Container's Standard, or holds one with other content than
	// the Standard renders for this Container at its ref.
	ReasonStandardFilesDiffer = "StandardFilesDiffer"
	ReasonLayerConflict       = "LayerConflict"
	// ReasonDomainNotMarkdown: a Domain's template renders a file that is not
	// Markdown; a Domain holds only *.md files. The message names the file.
	ReasonDomainNotMarkdown = "DomainNotMarkdown"
	// ReasonDomainNoReadme: a Domain's template renders no README.md, the
	// index the Standards' guides point at.
	ReasonDomainNoReadme     = "DomainNoReadme"
	ReasonUnpinnedRef        = "UnpinnedRef"
	ReasonWaiting            = "WaitingForScaffold"
	ReasonAppliedAtScaffold  = "AppliedAtScaffold"
	ReasonAddedAfterScaffold = "AddedAfterScaffold"
	ReasonUpToDate           = "UpToDate"
	ReasonUpdating           = "Updating"
	ReasonChecksFailed       = "ChecksFailed"
	ReasonUpdateClosed       = "UpdateClosed"
	ReasonUpdateRefused      = "UpdateRefused"
	ReasonFilesBehind        = "FilesBehind"
	// ReasonBundleChanged: the Bundle a Ready Container scaffolded from now
	// names other entries, or is gone; the Container keeps what it has.
	ReasonBundleChanged = "BundleChanged"
	// ReasonExternalSystem: the Container or DataStore names an External
	// SoftwareSystem, which the factory never builds.
	ReasonExternalSystem = "ExternalSystem"
	// ReasonChecksLeftOut: an update that changes the Container's checks would
	// leave out a file the change of checks writes, changed in the repository
	// since, so it isn't opened and the checks stay as they are.
	ReasonChecksLeftOut = "ChecksLeftOut"
	// ReasonNoLightCheck: an update that changes the Container's checks to
	// BeforeRelease waits, unopened, because the repository's Makefile has no
	// check-light target for each change's light check to run; it is planned
	// again every pass, so adding the target lets it through.
	ReasonNoLightCheck = "NoLightCheck"
	// ReasonStandardRunsFullChecks: the Container asks for checks BeforeRelease
	// and its Standard can't, so CI runs the full check on every change.
	ReasonStandardRunsFullChecks = "StandardRunsFullChecks"
	// ReasonEveryChange and ReasonBeforeRelease: ChecksApplied is True, and CI
	// runs the full check on every change, or only before a release.
	ReasonEveryChange   = "EveryChange"
	ReasonBeforeRelease = "BeforeRelease"
	// ReasonPending: ChecksApplied is False while the Standard update that brings
	// the Container's checks to its repository hasn't merged yet.
	ReasonPending = "Pending"
	// ReasonChangedAfterScaffold: OperationsApplied is False because the
	// Container's spec.operations changed after its scaffold and the operator
	// can't carry the change to its repository: its Framework says it reads
	// the block but renders no infra/operations.ts. A Task changes the file.
	ReasonChangedAfterScaffold = "ChangedAfterScaffold"
	// ReasonAdoptedRepository: OperationsApplied is False because the
	// Container's spec.operations changed after it adopted its repository
	// (spec.adopt). The factory never scaffolded that repository, so it can't
	// know whether its infra/operations.ts is one it can render again; a Task
	// changes the file (#1909).
	ReasonAdoptedRepository = "AdoptedRepository"
	// ReasonNotReadByFramework: OperationsApplied is False because
	// spec.operations is set on a Container whose Framework doesn't read the
	// block (readsOperations is not set), so its repository has none of it;
	// a Task changes the repository. A Container scaffolded before the
	// factory kept the block counts as not read. Without a block, such a
	// Container has no OperationsApplied condition.
	ReasonNotReadByFramework = "NotReadByFramework"
	// ReasonUpdateMerged: OperationsApplied is True because the operations
	// update that renders spec.operations into infra/operations.ts merged; the
	// message names the commit.
	ReasonUpdateMerged = "UpdateMerged"
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
	LabelSharedSecrets = "c4.factory.io/shared-secrets" //nolint:gosec // a label's name, not a credential
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

// AnnotationPausedBy on a Crew says who paused it: PausedByBrief when its
// Brief did, which then unpauses it on resume. A Crew without it was paused
// by hand and stays paused.
const AnnotationPausedBy = "c4.factory.io/paused-by"

// PausedByBrief is AnnotationPausedBy's value on a Crew its Brief paused.
const PausedByBrief = "brief"

// AnnotationBriefPublishReleases on a Crew a Brief made holds the JSON of the
// spec.publishReleases its Brief last wrote from a spec.releases entry
// (docs/plans/done/brief-release-target.md). A Crew whose spec.publishReleases
// differs from it was changed under the Brief and publishes nothing until the
// two agree. A Crew without it publishes where its own spec says.
const AnnotationBriefPublishReleases = "c4.factory.io/brief-publish-releases"

// ConditionReleaseTarget on a Crew with AnnotationBriefPublishReleases says
// that its Brief sets where it publishes: True, reason
// ReasonReleaseTargetSetByBrief, while the two agree; False, reason
// ReasonReleaseTargetDiffers, while the Crew was changed under the Brief.
const ConditionReleaseTarget = "ReleaseTarget"

// Reasons of ConditionReleaseTarget, and of the Published condition of a
// held Crew.
const (
	ReasonReleaseTargetSetByBrief = "SetByBrief"
	ReasonReleaseTargetDiffers    = "DiffersFromBrief"
	ReasonTargetDiffersFromBrief  = "TargetDiffersFromBrief"
)

// AnnotationRetry, set or changed on a Failed Container, makes the operator try
// to scaffold it again: after emptying its repository, for example.
const AnnotationRetry = "c4.factory.io/retry"

// AnnotationStandardTake and AnnotationStandardKeep, set on a Container to the
// Standard ref its files are at, settle the files its Standard updates left
// out (status.standard.behind): take the Standard's version, or keep the
// repository's. An answer for another ref is ignored.
const (
	AnnotationStandardTake = "c4.factory.io/standard-take"
	AnnotationStandardKeep = "c4.factory.io/standard-keep"
)

// AnnotationRerunRelease, set or changed on a Crew whose last release's run
// Failed, makes the operator re-run that release's failed workflow runs, once
// per value.
const AnnotationRerunRelease = "c4.factory.io/rerun-release"

// AnnotationAcceptRepository, set on a Crew to the GitHub id of its publish
// target, accepts that repository in place of the one the Crew published to
// before (status.publishTarget), which was deleted or renamed and a new one
// took its name. The operator records the id and removes the annotation.
const AnnotationAcceptRepository = "c4.factory.io/accept-repository"

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

// TokenUsage is what agent sessions used, in tokens, as the agents reported it.
type TokenUsage struct {
	// input are the prompt tokens that were neither read from nor written to
	// the cache.
	// +optional
	Input int64 `json:"input,omitempty"`

	// output are the tokens the agents wrote.
	// +optional
	Output int64 `json:"output,omitempty"`

	// cacheRead are the prompt tokens read from the cache.
	// +optional
	CacheRead int64 `json:"cacheRead,omitempty"`

	// cacheWrite are the prompt tokens written to the cache.
	// +optional
	CacheWrite int64 `json:"cacheWrite,omitempty"`

	// largestSession is how large the largest agent session got: the prompt
	// of its biggest turn, in tokens.
	// +optional
	LargestSession int64 `json:"largestSession,omitempty"`

	// runsWithout counts the agent runs whose agent didn't report their
	// tokens; the other fields leave them out.
	// +optional
	RunsWithout int32 `json:"runsWithout,omitempty"`

	// sessionHandoffs counts the agent sessions that handed their work to a
	// fresh session.
	// +optional
	SessionHandoffs int32 `json:"sessionHandoffs,omitempty"`
}
