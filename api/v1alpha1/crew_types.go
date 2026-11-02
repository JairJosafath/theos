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

// CrewSpec is who works on a Container's issues: the Agent that implements, the
// Agent that reviews, the Agents labelled issues go to instead, and whether they
// pick up ready issues.
// +kubebuilder:validation:XValidation:rule="!has(self.publishReleases) || (has(self.tagReleases) && self.tagReleases)",message="publishReleases needs tagReleases: only a release the Crew tagged is published"
// +kubebuilder:validation:XValidation:rule="!has(self.pauseRuns) || !self.pauseRuns || (has(self.paused) && self.paused)",message="pauseRuns needs paused: a Crew pauses its running Tasks only while it picks up no new issue"
type CrewSpec struct {
	// containerRef names the Container whose repository's issues the Crew works
	// on. It can't change.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="containerRef is immutable"
	// +required
	ContainerRef LocalRef `json:"containerRef"`

	// displayName is a human-friendly name.
	// +kubebuilder:validation:MaxLength=100
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// description says what the Crew is for.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Description string `json:"description,omitempty"`

	// implementer names the Agent that implements each issue.
	// +required
	Implementer LocalRef `json:"implementer"`

	// reviewer names the Agent that reviews each round; usually a stronger model
	// than the implementer's.
	// +required
	Reviewer LocalRef `json:"reviewer"`

	// paused stops the Crew from picking up new issues. A Task already started
	// runs to its end.
	// +optional
	Paused bool `json:"paused,omitempty"`

	// pauseRuns, with paused, also pauses the Crew's running Tasks at their
	// next tool boundary (docs/plans/self-hosting.md, step 12): each run saves
	// a clean checkpoint and suspends, with no round or rerun spent and its
	// cost so far counted. Unset, each paused run resumes where it stopped.
	// +optional
	PauseRuns bool `json:"pauseRuns,omitempty"`

	// parallel is how many issues the Crew works on at once, each in its own Task
	// and branch. A change that conflicts with one merged before it goes back to
	// its implementer to merge main, without costing a round.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=5
	// +kubebuilder:default=1
	// +optional
	Parallel int32 `json:"parallel,omitempty"`

	// taskBudgetUSD is the most a Task's agent runs may cost, in US dollars,
	// summed over its rounds from what the agents report (status.costMilliUSD).
	// At the cap the Task starts no further run, reason OverBudget, until the
	// cap is raised. Unset means no cap.
	// +kubebuilder:validation:Minimum=1
	// +optional
	TaskBudgetUSD int32 `json:"taskBudgetUSD,omitempty"`

	// tagReleases lets the Crew tag a release once it is done: a milestone with a
	// due date and a vMAJOR.MINOR.PATCH title, with no open issue and no Task at
	// work on one of its issues. The tag goes at the merge commit of the release's
	// most recently merged issue, and the milestone is then closed. Releases go
	// out in order, and an existing tag is never moved.
	// +optional
	TagReleases bool `json:"tagReleases,omitempty"`

	// prepareReleases lets the Crew file each release's prepare issue: once the
	// first open release is done with merged work and has no issue labelled
	// kind/release, the Crew files "Prepare vX.Y.Z" in its milestone, labelled
	// kind/release and status/ready, to set the version and write the changelog.
	// Its merge is then the release's newest, where tagReleases puts the tag; work
	// merged after it holds the tag for a person.
	// +optional
	PrepareReleases bool `json:"prepareReleases,omitempty"`

	// publishReleases copies each release the Crew tags to another repository
	// once its release runs succeed: the same tag, notes and files. It only adds
	// releases there, never a commit. It needs tagReleases. Removing it stops
	// publishing; what is published stays.
	// +optional
	PublishReleases *CrewPublishReleases `json:"publishReleases,omitempty"`

	// routes send the issues with a label to other Agents than implementer and
	// reviewer, such as kind/docs to a cheaper model or a security label to a
	// stronger reviewer. For each role, the first route in the list whose label
	// the issue has and that names an Agent for the role decides; with none,
	// the Crew's own Agent takes it. The labels are read as the Task starts,
	// and its agentRef and reviewerRef name the Agents it got.
	// +listType=map
	// +listMapKey=label
	// +kubebuilder:validation:MaxItems=20
	// +optional
	Routes []CrewRoute `json:"routes,omitempty"`
}

// CrewRoute sends a Crew's issues with a label to other Agents.
// +kubebuilder:validation:XValidation:rule="has(self.implementer) || has(self.reviewer)",message="a route names an implementer, a reviewer or both"
type CrewRoute struct {
	// label is an issue label, such as kind/docs, matched exactly.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +required
	Label string `json:"label"`

	// implementer names the Agent that implements an issue with the label.
	// +optional
	Implementer *LocalRef `json:"implementer,omitempty"`

	// reviewer names the Agent that reviews an issue with the label.
	// +optional
	Reviewer *LocalRef `json:"reviewer,omitempty"`
}

// CrewPublishReleases is where a Crew publishes its releases.
type CrewPublishReleases struct {
	// github is the GitHub repository the releases go to.
	// +required
	GitHub CrewPublishGitHub `json:"github"`

	// tagPrefix goes before the Crew's tag on the target, such as "cli-" when
	// two Containers publish to one repository: cli-v0.2.0.
	// +kubebuilder:validation:MaxLength=40
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9._/-]*$`
	// +optional
	TagPrefix string `json:"tagPrefix,omitempty"`

	// latest says whether each release published becomes the repository's
	// Latest release on GitHub. It defaults to true; set it to false for every
	// app but the one people install, when several publish to one repository,
	// so a library's release doesn't take Latest from it. A release published
	// before it changed keeps what it got.
	// +optional
	Latest *bool `json:"latest,omitempty"`

	// source puts the app's source, as of each release's tag, into a folder
	// of the repository, with a tag of its own, so go install from GitHub
	// finds it. The factory's own files are left out. Removing it stops
	// publishing the source; what is on GitHub stays.
	// +optional
	Source *CrewPublishSource `json:"source,omitempty"`
}

// CrewPublishSource is the folder of the GitHub repository a Crew publishes
// its releases' source to, and the tag the source goes out under.
type CrewPublishSource struct {
	// path is the folder, such as engine; "." is the whole repository. The
	// folder is the app's: each release replaces what it holds. Two Crews
	// publishing to one repository need folders apart, neither inside the
	// other.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:Pattern=`^([.]|[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*)$`
	// +kubebuilder:validation:XValidation:rule="self == '.' || !self.matches('(^|/)([.][.]?|[.][gG][iI][tT])(/|$)')",message="path must have no '.', '..' or '.git' folder in it"
	// +required
	Path string `json:"path"`

	// tagPrefix goes before the Crew's tag in the source's tag. Unset, it is
	// the path and "/" (engine/v0.6.1, the tag go install looks for), and
	// nothing for ".".
	// +kubebuilder:validation:MaxLength=101
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9._/-]*$`
	// +kubebuilder:validation:XValidation:rule="!self.matches('^[./-]|//|/[.]|[.][.]|[.]lock(/|$)')",message="tagPrefix must make a git tag name: no leading '.', '/' or '-', no '..', '//' or '/.', and no folder ending in .lock"
	// +optional
	TagPrefix *string `json:"tagPrefix,omitempty"`
}

// CrewPublishGitHub is a repository on github.com and the token that publishes
// to it.
type CrewPublishGitHub struct {
	// repository is owner/name on github.com.
	// +kubebuilder:validation:MaxLength=140
	// +kubebuilder:validation:XValidation:rule="self.matches('^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$')",message="repository must be owner/name"
	// +required
	Repository string `json:"repository"`

	// tokenSecretRef names a Secret in the Crew's namespace whose key token is
	// a fine-grained token for that repository alone, with Contents read and
	// write, or whose keys appId, installationId and privateKey (its PEM) are
	// a GitHub App's installed on that repository with the same permission;
	// token wins when both are set. The user creates it; the credential never
	// leaves the operator.
	// +required
	TokenSecretRef LocalRef `json:"tokenSecretRef"`
}

// CrewFullCheck is a release's full check, run on the branch
// release-check/<tag> at the commit the tag will name before the Crew tags it.
type CrewFullCheck struct {
	// tag is the release's version, the milestone's title.
	// +required
	Tag string `json:"tag"`

	// commit is the commit the tag will name, where the branch points.
	// +required
	Commit string `json:"commit"`

	// time is when the Crew pushed the branch, or found it at the commit; the
	// hour a run has to start counts from it.
	// +required
	Time metav1.Time `json:"time"`

	// run is how the runs the branch's push started went, as for a release.
	// +kubebuilder:validation:Enum=Waiting;Running;Succeeded;Failed
	// +optional
	Run string `json:"run,omitempty"`

	// runURL links to the failed run, else to the newest one.
	// +optional
	RunURL string `json:"runURL,omitempty"`

	// issue is the issue the Crew filed when the check failed at commit.
	// +optional
	Issue int32 `json:"issue,omitempty"`
}

// CrewRelease is a release a Crew tagged.
type CrewRelease struct {
	// tag is the release's version, the milestone's title.
	// +required
	Tag string `json:"tag"`

	// commit is the commit the tag names.
	// +required
	Commit string `json:"commit"`

	// time is when the Crew tagged the release and closed its milestone.
	// +required
	Time metav1.Time `json:"time"`

	// run is how the CI workflow runs the tag started went: Waiting until one
	// starts, Running while one hasn't ended, Failed as soon as one ended other
	// than success or skipped, Succeeded once all ended well.
	// +kubebuilder:validation:Enum=Waiting;Running;Succeeded;Failed
	// +optional
	Run string `json:"run,omitempty"`

	// runURL links to the failed run, else to the newest one.
	// +optional
	RunURL string `json:"runURL,omitempty"`

	// rerunTime is when the operator last re-ran the release's failed runs, on
	// a c4.factory.io/rerun-release request; the hour the runs have to end
	// counts from it instead of from time.
	// +optional
	RerunTime *metav1.Time `json:"rerunTime,omitempty"`

	// bumps are the bump issues filed for this release, one per Container that
	// bundles this one.
	// +listType=map
	// +listMapKey=container
	// +optional
	Bumps []CrewReleaseBump `json:"bumps,omitempty"`

	// publish is how publishing the release went, when spec.publishReleases is
	// set.
	// +optional
	Publish *CrewReleasePublish `json:"publish,omitempty"`

	// goModule is the Go module the release published to the Git host's
	// package registry, under the module path its go.mod declares and with the
	// tag as its version.
	// +optional
	GoModule *CrewReleaseGoModule `json:"goModule,omitempty"`

	// packages is how publishing the release's npm and PyPI packages, the
	// tarballs, wheels and sdists its release run attached, to the registries
	// the GitProvider's spec.packages names went.
	// +optional
	Packages *CrewReleasePackages `json:"packages,omitempty"`
}

// CrewReleasePackages is how publishing a release's npm and PyPI packages went.
type CrewReleasePackages struct {
	// state is Waiting until the release runs succeed, Published once every
	// package file of the release is in its registry, NoPackage when the
	// release attached no package file, Invalid when a file can't be published
	// at the tag's version, and Failed while the Git host can't be reached,
	// tried again each poll.
	// +kubebuilder:validation:Enum=Waiting;Published;NoPackage;Invalid;Failed
	// +required
	State string `json:"state"`

	// message says what happened last: what was published, why a file wasn't,
	// and an npm tarball under another scope, or none, left alone, with the
	// scope it lacks.
	// +optional
	Message string `json:"message,omitempty"`

	// published lists the packages the release has in its registries, one per
	// package file published, now or before.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=50
	// +optional
	Published []CrewReleasePackage `json:"published,omitempty"`
}

// CrewReleasePackage is one package a release published.
type CrewReleasePackage struct {
	// ecosystem is the package's registry kind, npm or PyPI.
	// +kubebuilder:validation:Enum=npm;PyPI
	// +required
	Ecosystem string `json:"ecosystem"`

	// name is the package's name as its metadata declares it.
	// +required
	Name string `json:"name"`

	// version is the package's version as its metadata declares it, the
	// release's tag without its v.
	// +required
	Version string `json:"version"`
}

// CrewReleaseGoModule is how publishing a release's Go module went.
type CrewReleaseGoModule struct {
	// state is Published once go get finds the version where its path is
	// read: the registry for a c4factory.local/ path, the Go proxy for any
	// other. NotGo when the repository has no go.mod at its root at the tag,
	// Invalid when Go can't take the module at that version, such as a v2.0.0
	// whose module path lacks /v2, Failed while the Git host can't be reached
	// (tried again each poll), Waiting while the Crew's GitHub source tag isn't
	// out or the Go proxy can't be reached, and NotFetchable when the Go proxy
	// can't fetch the version or the factory can't tell.
	// +kubebuilder:validation:Enum=Published;NotGo;Invalid;Failed;Waiting;NotFetchable
	// +required
	State string `json:"state"`

	// path is the module path go.mod declares.
	// +optional
	Path string `json:"path,omitempty"`

	// version is the module's version, the release's tag.
	// +optional
	Version string `json:"version,omitempty"`

	// message says what happened last, and why the module isn't published.
	// +optional
	Message string `json:"message,omitempty"`

	// checkedTime is when the Go proxy was last asked about the version; it is
	// asked at most every 5 minutes until it finds it.
	// +optional
	CheckedTime *metav1.Time `json:"checkedTime,omitempty"`
}

// CrewReleasePublish is how publishing a release to its target went.
type CrewReleasePublish struct {
	// state is Pending until the release runs succeed, Publishing while the
	// release is copied, Published once it is out and Failed while it can't
	// be; a failure is tried again each poll.
	// +kubebuilder:validation:Enum=Pending;Publishing;Published;Failed
	// +required
	State string `json:"state"`

	// url is the published release.
	// +optional
	URL string `json:"url,omitempty"`

	// message says what happened last, and why it failed.
	// +optional
	Message string `json:"message,omitempty"`

	// source is the release's source on GitHub, once it is out, when the Crew
	// publishes it.
	// +optional
	Source *CrewReleaseSource `json:"source,omitempty"`
}

// CrewReleaseSource is a release's source published to GitHub.
type CrewReleaseSource struct {
	// commit is the commit the source is in.
	// +required
	Commit string `json:"commit"`

	// tag is the source's tag.
	// +required
	Tag string `json:"tag"`

	// url is the source folder at the tag.
	// +optional
	URL string `json:"url,omitempty"`

	// private is whether GitHub reports the repository private, read as the
	// tag went out, or after, for a release older than the field: the public
	// Go proxy can't read a private one.
	// +optional
	Private *bool `json:"private,omitempty"`
}

// CrewReleaseBump is a bump issue filed in a Container that bundles the released
// one.
type CrewReleaseBump struct {
	// container is the Container that bundles the release.
	// +required
	Container string `json:"container"`

	// issueURL is the bump issue's address.
	// +optional
	IssueURL string `json:"issueURL,omitempty"`
}

// CrewPhase says what a Crew is doing.
// +kubebuilder:validation:Enum=Waiting;Idle;Working;Paused
type CrewPhase string

// Crew phases.
const (
	// CrewWaiting means the Crew can't pick up issues yet, such as while its
	// Container isn't Ready; the Ready condition says why.
	CrewWaiting CrewPhase = "Waiting"
	// CrewIdle means no issue is ready to pick up.
	CrewIdle CrewPhase = "Idle"
	// CrewWorking means a Task works on one of the Container's issues.
	CrewWorking CrewPhase = "Working"
	// CrewPaused means spec.paused is set.
	CrewPaused CrewPhase = "Paused"
)

// CrewPublishTarget is a GitHub repository a Crew published to.
type CrewPublishTarget struct {
	// repository is owner/name, as spec.publishReleases named it.
	// +kubebuilder:validation:MaxLength=140
	// +required
	Repository string `json:"repository"`

	// id is GitHub's id of the repository, which a rename keeps and a new
	// repository under the same name doesn't.
	// +required
	ID int64 `json:"id"`
}

// CrewStatus is what the Crew saw on its last look at the issues.
type CrewStatus struct {
	// phase is Waiting, Idle, Working or Paused.
	// +optional
	Phase CrewPhase `json:"phase,omitempty"`

	// activeTasks names the Tasks working on the Container now.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=10
	// +optional
	ActiveTasks []string `json:"activeTasks,omitempty"`

	// ready lists the ready issues waiting for a free place, in pick-up order:
	// the release due first first, and issues outside a release last.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=100
	// +optional
	Ready []int32 `json:"ready,omitempty"`

	// waiting lists the ready issues that wait on open dependencies.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=100
	// +optional
	Waiting []int32 `json:"waiting,omitempty"`

	// lastRelease is the release the Crew tagged last, when spec.tagReleases is set.
	// +optional
	LastRelease *CrewRelease `json:"lastRelease,omitempty"`

	// fullCheck is the full check of the next release, while it holds the tag:
	// set when the Container's checks are BeforeRelease and the release is done,
	// until the release is tagged.
	// +optional
	FullCheck *CrewFullCheck `json:"fullCheck,omitempty"`

	// publishTarget is the GitHub repository the Crew published its releases
	// to, by GitHub's id: set at the first publish that succeeds, or when
	// c4.factory.io/accept-repository accepts another, and reset when
	// spec.publishReleases names another repository. A publish to a repository
	// renamed or replaced since fails before anything is written to it.
	// +optional
	PublishTarget *CrewPublishTarget `json:"publishTarget,omitempty"`

	// rerunRelease is the c4.factory.io/rerun-release value the operator last
	// answered, so each value re-runs the failed release runs once.
	// +optional
	RerunRelease string `json:"rerunRelease,omitempty"`

	// lastPolled is when the Crew last read the issues.
	// +optional
	LastPolled *metav1.Time `json:"lastPolled,omitempty"`

	// observedGeneration is the spec generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Ready says whether the Crew can pick up issues, and why not.
	// Released, when spec.tagReleases is set, says whether the Crew's next
	// release went out, and why not. Published, when spec.publishReleases is
	// set, says how publishing the last release went, and why it failed.
	// ModulePublished, when spec.tagReleases is set and a release was tagged,
	// says whether the last release's Go module is in the registry, and why not.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:deprecatedversion:warning="c4.factory.io/v1alpha1 is deprecated since v0.30.0; use c4.factory.io/v1beta1. A release 90 or more days after v0.30.0 may drop it (#1963)."
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=c4
// +kubebuilder:printcolumn:name="Container",type=string,JSONPath=".spec.containerRef.name"
// +kubebuilder:printcolumn:name="Implementer",type=string,JSONPath=".spec.implementer.name"
// +kubebuilder:printcolumn:name="Reviewer",type=string,JSONPath=".spec.reviewer.name"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Parallel",type=integer,JSONPath=".spec.parallel"
// +kubebuilder:printcolumn:name="Tasks",type=string,JSONPath=".status.activeTasks"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Crew is the team of Agents that works on a Container's issues: it picks up
// issues labelled ready, in order and once their dependencies are closed, and
// starts a Task for each, up to spec.parallel at once. It is owned by its
// Container.
type Crew struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the Container and the Agents for each role
	// +required
	Spec CrewSpec `json:"spec"`

	// status defines the observed state of Crew
	// +optional
	Status CrewStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// CrewList contains a list of Crew
type CrewList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Crew `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Crew{}, &CrewList{})
		return nil
	})
}
