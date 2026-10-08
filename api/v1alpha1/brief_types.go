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

// BriefSpec is an app idea in plain words, the follow-up prompts that steer
// it, and the Agents that plan and build it.
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.prompts) || size(oldSelf.prompts) == 0 || has(self.prompts)",message="prompts are append-only: an earlier prompt can't change or go"
type BriefSpec struct {
	// idea says in plain words what the app is for and who uses it. It can't
	// change; steer with prompts.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=4000
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="idea is immutable; add a prompt"
	// +required
	Idea string `json:"idea"`

	// prompts are follow-ups in plain words, oldest first. A prompt is only
	// ever appended: an earlier one can't change or go.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=2000
	// +kubebuilder:validation:XValidation:rule="size(self) >= size(oldSelf) && oldSelf.all(i, p, self[i] == p)",message="prompts are append-only: an earlier prompt can't change or go"
	// +optional
	Prompts []string `json:"prompts,omitempty"`

	// agents names the Agents the Brief works with.
	// +kubebuilder:default={}
	// +optional
	Agents BriefAgents `json:"agents,omitzero"`

	// gitProviderRef names the GitProvider that hosts the Brief's repository
	// and the repositories of the Containers it plans.
	// +kubebuilder:default={name: gitea}
	// +optional
	GitProviderRef LocalRef `json:"gitProviderRef,omitzero"`

	// releases name the Containers whose releases the person asks for, and the
	// GitHub repository each one's releases are published to. An entry turns
	// releases on for its Container and sets the publishReleases of the Crew
	// the Brief made for it; removing it leaves that Crew as it is.
	// +listType=map
	// +listMapKey=container
	// +kubebuilder:validation:MaxItems=10
	// +optional
	Releases []BriefRelease `json:"releases,omitempty"`

	// autopilot says whether the Brief tends its failed issues by itself, in
	// tending rounds with no new prompt (docs/plans/done/brief-autopilot.md):
	// Off tends nothing; Ask decides them and lists each answer under
	// status.needsYou for a prompt to confirm; On applies them. Unset is the
	// operator's default, Ask.
	// +kubebuilder:validation:Enum=Off;Ask;On
	// +optional
	Autopilot string `json:"autopilot,omitempty"`

	// parallel is how many issues each Crew the Brief made works on at once,
	// set on those Crews every reconcile, earlier rounds' included
	// (docs/plans/done/brief-parallel.md). Unset is the operator's default, 3.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=5
	// +optional
	Parallel int32 `json:"parallel,omitempty"`
}

// BriefRelease is where a Container's releases are published: the fields of a
// Crew's publishReleases, for the Crew the Brief made for the Container.
type BriefRelease struct {
	// container names the Container, a DNS label.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z0-9]([-a-z0-9]*[a-z0-9])?$')",message="container must be a DNS label: lowercase letters, digits and '-'"
	// +required
	Container string `json:"container"`

	// The GitHub repository, its token Secret in the Brief's namespace, and
	// the tag prefix, as in a Crew's publishReleases.
	CrewPublishReleases `json:",inline"`
}

// BriefAgents names the Agent for each role: the planner turns the Brief into
// the model, and each new Container's Crew gets the other three.
type BriefAgents struct {
	// planner names the Agent that plans each round.
	// +kubebuilder:default={name: planner}
	// +optional
	Planner LocalRef `json:"planner,omitzero"`

	// implementer names the Agent that implements the Crews' issues.
	// +kubebuilder:default={name: implementer}
	// +optional
	Implementer LocalRef `json:"implementer,omitzero"`

	// reviewer names the Agent that reviews the Crews' work.
	// +kubebuilder:default={name: reviewer}
	// +optional
	Reviewer LocalRef `json:"reviewer,omitzero"`

	// designer names the Agent that designs for the Crews.
	// +kubebuilder:default={name: designer}
	// +optional
	Designer LocalRef `json:"designer,omitzero"`
}

// BriefPhase says what a Brief is doing.
// +kubebuilder:validation:Enum=Pending;Planning;Applying;Planned;NeedsInput;Failed
type BriefPhase string

// Brief phases.
const (
	// BriefPending means no round has started yet.
	BriefPending BriefPhase = "Pending"
	// BriefPlanning means the planner works on a round.
	BriefPlanning BriefPhase = "Planning"
	// BriefApplying means the operator applies a round's plan.
	BriefApplying BriefPhase = "Applying"
	// BriefPlanned means the last round is applied and no prompt waits.
	BriefPlanned BriefPhase = "Planned"
	// BriefNeedsInput means the Brief waits for the person; the NeedsInput
	// condition says what and why.
	BriefNeedsInput BriefPhase = "NeedsInput"
	// BriefFailed means a round failed; the Planned condition says why.
	BriefFailed BriefPhase = "Failed"
)

// Brief condition types.
const (
	// ConditionPlanned is True when the last round's plan is applied.
	ConditionPlanned = "Planned"
	// ConditionNeedsInput is True while the Brief waits for the person.
	ConditionNeedsInput = "NeedsInput"
	// ConditionIssuesFailed is True while issues of the Brief's Containers are
	// handed back to the person, waiting for a prompt to say whether to try
	// them again.
	ConditionIssuesFailed = "IssuesFailed"
	// ConditionNeedsYou is True while status.needsYou lists something only
	// the person can decide; it says the first and how many more.
	ConditionNeedsYou = "NeedsYou"
)

// BriefObject names an object a Brief created.
type BriefObject struct {
	// kind of the object, such as Container or Crew.
	// +kubebuilder:validation:MaxLength=63
	// +required
	Kind string `json:"kind"`

	// name of the object.
	// +kubebuilder:validation:MaxLength=253
	// +required
	Name string `json:"name"`
}

// BriefIssue is an issue a Brief filed.
type BriefIssue struct {
	// repository is the Container's repository, owner/name.
	// +kubebuilder:validation:MaxLength=140
	// +required
	Repository string `json:"repository"`

	// number of the issue in its repository.
	// +kubebuilder:validation:Minimum=1
	// +required
	Number int32 `json:"number"`

	// url is the issue's address.
	// +optional
	URL string `json:"url,omitempty"`

	// title of the issue.
	// +kubebuilder:validation:MaxLength=255
	// +optional
	Title string `json:"title,omitempty"`

	// round is the Brief round that filed the issue.
	// +optional
	Round int32 `json:"round,omitempty"`

	// n is the issue's number in its round's plan, as in its marker.
	// +optional
	N int32 `json:"n,omitempty"`
}

// BriefSuggestion is a reviewer's suggestion waiting in the repository of a
// Container the Brief made, for the person to answer in a prompt.
type BriefSuggestion struct {
	// container is the Container whose repository has the suggestion.
	// +kubebuilder:validation:MaxLength=253
	// +required
	Container string `json:"container"`

	// number of the suggestion's issue in its repository.
	// +kubebuilder:validation:Minimum=1
	// +required
	Number int32 `json:"number"`

	// title of the suggestion.
	// +kubebuilder:validation:MaxLength=255
	// +optional
	Title string `json:"title,omitempty"`

	// url is the suggestion's address.
	// +optional
	URL string `json:"url,omitempty"`

	// criteria says the suggestion has acceptance criteria, so a prompt can
	// take it as it stands.
	// +optional
	Criteria bool `json:"criteria,omitempty"`
}

// BriefFailedIssue is an issue handed back to a person (status/needs-human) in
// the repository of a Container the Brief made, and why.
type BriefFailedIssue struct {
	// container is the Container whose repository has the issue.
	// +kubebuilder:validation:MaxLength=253
	// +required
	Container string `json:"container"`

	// number of the issue in its repository.
	// +kubebuilder:validation:Minimum=1
	// +required
	Number int32 `json:"number"`

	// title of the issue.
	// +kubebuilder:validation:MaxLength=255
	// +optional
	Title string `json:"title,omitempty"`

	// url is the issue's address.
	// +optional
	URL string `json:"url,omitempty"`

	// kind says why it was handed back: unclear (no acceptance criteria),
	// merged (a Task merged it already), exhausted (agents tried it as often
	// as a Crew does), failed (its last Task failed), blocked (its last
	// Task's agent said it can't start the issue yet, and why), person (a
	// person handed it back) or byHand (a kind/by-hand issue: criteria only a
	// person can do, which a plan may close but never retry).
	// +kubebuilder:validation:Enum=unclear;merged;exhausted;failed;blocked;person;byHand
	// +required
	Kind string `json:"kind"`

	// reason says why in a sentence, such as the failed Task's message.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Reason string `json:"reason,omitempty"`

	// tries is how many Tasks the issue has had.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Tries int32 `json:"tries,omitempty"`
}

// BriefHealing is an item the Brief watches and leaves alone for now, as it
// may heal by itself, and when the Brief acts if it hasn't.
type BriefHealing struct {
	// kind of the item: Issue, Task, Crew or Container.
	// +kubebuilder:validation:Enum=Issue;Task;Crew;Container
	// +required
	Kind string `json:"kind"`

	// name of the item; container#number for an issue.
	// +kubebuilder:validation:MaxLength=320
	// +required
	Name string `json:"name"`

	// reason says why it is left alone, in a sentence.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Reason string `json:"reason,omitempty"`

	// actsAt is when the Brief acts if it hasn't healed; unset when it acts
	// only once the item changes.
	// +optional
	ActsAt *metav1.Time `json:"actsAt,omitempty"`
}

// BriefNeedsYou is an item only the person can decide: the question, what the
// Brief would do and why. A prompt answers it.
type BriefNeedsYou struct {
	// kind of the item: Issue, Task, Crew or Container.
	// +kubebuilder:validation:Enum=Issue;Task;Crew;Container
	// +required
	Kind string `json:"kind"`

	// name of the item; container#number for an issue.
	// +kubebuilder:validation:MaxLength=320
	// +required
	Name string `json:"name"`

	// question says in one sentence what needs the person.
	// +kubebuilder:validation:MaxLength=1000
	// +required
	Question string `json:"question"`

	// recommend is what the Brief would do.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Recommend string `json:"recommend,omitempty"`

	// why the Brief would do it.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Why string `json:"why,omitempty"`

	// since when the item needs the person.
	// +optional
	Since *metav1.Time `json:"since,omitempty"`
}

// BriefRecommendation is what a round would do with a failed issue it asks
// the person about.
type BriefRecommendation struct {
	// answer is retry, close or wait.
	// +kubebuilder:validation:Enum=retry;close;wait
	// +required
	Answer string `json:"answer"`

	// why the round would do it.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Why string `json:"why,omitempty"`

	// clarify is the clarification a retry would append to the issue's body.
	// +kubebuilder:validation:MaxLength=4000
	// +optional
	Clarify string `json:"clarify,omitempty"`
}

// BriefTendedIssue is a failed issue a round answered, as the Brief remembers
// it: it is not raised again until its tries grow or a wait lapses.
type BriefTendedIssue struct {
	// container is the Container whose repository has the issue.
	// +kubebuilder:validation:MaxLength=253
	// +required
	Container string `json:"container"`

	// number of the issue in its repository.
	// +kubebuilder:validation:Minimum=1
	// +required
	Number int32 `json:"number"`

	// answer is retry, close, wait or ask.
	// +kubebuilder:validation:Enum=retry;close;wait;ask
	// +required
	Answer string `json:"answer"`

	// why the round answered so.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Why string `json:"why,omitempty"`

	// recommend is what an ask recommends.
	// +optional
	Recommend *BriefRecommendation `json:"recommend,omitempty"`

	// tries is how many Tasks the issue had when it was answered.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Tries int32 `json:"tries,omitempty"`

	// at is when it was answered.
	// +required
	At metav1.Time `json:"at"`

	// round is the round that answered it.
	// +kubebuilder:validation:Minimum=1
	// +required
	Round int32 `json:"round"`
}

// BriefApp is a Container the Brief made, or one spec.releases names, and how
// its releases go: copied from the Crew, which holds every fact.
type BriefApp struct {
	// container names the Container.
	// +kubebuilder:validation:MaxLength=253
	// +required
	Container string `json:"container"`

	// crew names the Container's Crew; empty while it has none.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	Crew string `json:"crew,omitempty"`

	// releases is On when the Crew tags releases, else Off.
	// +kubebuilder:validation:Enum=On;Off
	// +required
	Releases string `json:"releases"`

	// parallel is how many issues the Crew works on at once; 0 while it has
	// no Crew.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=5
	// +optional
	Parallel int32 `json:"parallel,omitempty"`

	// publishTo is the GitHub repository, owner/name, the Crew publishes its
	// releases to; empty when it publishes none.
	// +kubebuilder:validation:MaxLength=140
	// +optional
	PublishTo string `json:"publishTo,omitempty"`

	// lastRelease is the Crew's latest release.
	// +optional
	LastRelease *BriefAppRelease `json:"lastRelease,omitempty"`

	// message says in one plain sentence how the app's releases go.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Message string `json:"message,omitempty"`
}

// BriefAppRelease is an app's latest release, from its Crew's status.lastRelease.
type BriefAppRelease struct {
	// tag is the release's version.
	// +required
	Tag string `json:"tag"`

	// url is the release's page on the Git host.
	// +optional
	URL string `json:"url,omitempty"`

	// time is when the Crew tagged the release.
	// +optional
	Time *metav1.Time `json:"time,omitempty"`

	// run is how the tag's CI runs went: Waiting, Running, Succeeded or Failed.
	// +optional
	Run string `json:"run,omitempty"`

	// publishState is how publishing the release went: Pending, Publishing,
	// Published or Failed; empty when the Crew publishes none.
	// +optional
	PublishState string `json:"publishState,omitempty"`

	// publishedURL is the published release.
	// +optional
	PublishedURL string `json:"publishedURL,omitempty"`
}

// BriefRound is one round of planning: the idea, or a batch of new prompts.
type BriefRound struct {
	// round is the round's number, from 1.
	// +kubebuilder:validation:Minimum=1
	// +required
	Round int32 `json:"round"`

	// summary says in a few words what the round did.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Summary string `json:"summary,omitempty"`

	// choices are what the planner chose, in plain words.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MaxLength=500
	// +optional
	Choices []string `json:"choices,omitempty"`

	// costMilliUSD is what the round's planning cost, in thousandths of a
	// US dollar.
	// +optional
	CostMilliUSD int64 `json:"costMilliUSD,omitempty"`

	// runsWithoutCost counts the round's planner runs whose agent didn't report
	// what they cost; costMilliUSD leaves them out.
	// +optional
	RunsWithoutCost int32 `json:"runsWithoutCost,omitempty"`

	// tokens is what the round's planner runs used, and how large the largest
	// session got.
	// +optional
	Tokens *TokenUsage `json:"tokens,omitempty"`

	// outcome is how the round ended: Applied, PlannerNeedsInput or
	// RoundFailed; Tended for a tending round that applied.
	// +kubebuilder:validation:Enum=Applied;PlannerNeedsInput;RoundFailed;Tended
	// +optional
	Outcome string `json:"outcome,omitempty"`

	// needsInput is the planner's question, when the round asked for input.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	NeedsInput string `json:"needsInput,omitempty"`
}

// BriefStatus is what the Brief planned and made so far.
type BriefStatus struct {
	// phase is Pending, Planning, Applying, Planned, NeedsInput or Failed.
	// +optional
	Phase BriefPhase `json:"phase,omitempty"`

	// round is the number of the round running now or last run; 0 before the
	// first.
	// +optional
	Round int32 `json:"round,omitempty"`

	// roundPrompts is how many of spec.prompts the running or last round took.
	// +optional
	RoundPrompts int32 `json:"roundPrompts,omitempty"`

	// promptsPlanned is how many of spec.prompts the last applied round took;
	// the prompts after them wait for the next round.
	// +optional
	PromptsPlanned int32 `json:"promptsPlanned,omitempty"`

	// parallel is how many issues each Crew the Brief made works on at once,
	// as a prompt asked: the planner relays it in plan/parallel.yaml and the
	// round that applies it records it here. spec.parallel wins over it
	// (docs/plans/done/brief-parallel.md).
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=5
	// +optional
	Parallel int32 `json:"parallel,omitempty"`

	// parallelRound is the round whose prompt gave parallel.
	// +optional
	ParallelRound int32 `json:"parallelRound,omitempty"`

	// choices are what the planner chose in the last round, in plain words.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MaxLength=500
	// +optional
	Choices []string `json:"choices,omitempty"`

	// created lists the objects the Brief made, over all rounds.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=500
	// +optional
	Created []BriefObject `json:"created,omitempty"`

	// issues lists the issues the Brief filed, over all rounds.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=500
	// +optional
	Issues []BriefIssue `json:"issues,omitempty"`

	// suggestions are the reviewers' suggestions waiting in the repositories
	// of the Containers the Brief made, oldest first; at most 50. A prompt
	// answers them: the operator takes the ones it says to take, ready for
	// the Crew, and closes the ones it says to skip.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=50
	// +optional
	Suggestions []BriefSuggestion `json:"suggestions,omitempty"`

	// suggestionsWaiting is how many reviewers' suggestions wait for an
	// answer, those past the 50 listed too.
	// +optional
	SuggestionsWaiting int32 `json:"suggestionsWaiting,omitempty"`

	// failed are the issues handed back to a person in the repositories of
	// the Containers the Brief made, by Container and number; at most 50. A
	// prompt says whether to try them again.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=50
	// +optional
	Failed []BriefFailedIssue `json:"failed,omitempty"`

	// failedWaiting is how many failed issues wait for an answer, those past
	// the 50 listed too.
	// +optional
	FailedWaiting int32 `json:"failedWaiting,omitempty"`

	// attention lists what the Brief made that it leaves alone for now, as
	// it may heal by itself, and when it acts if it hasn't; at most 100.
	// Empty while autopilot is Off.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=100
	// +optional
	Attention []BriefHealing `json:"attention,omitempty"`

	// needsYou lists what only the person can decide, oldest first, each
	// with what the Brief would do; at most 50. A prompt answers it. Empty
	// while autopilot is Off.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=50
	// +optional
	NeedsYou []BriefNeedsYou `json:"needsYou,omitempty"`

	// needsYouWaiting is how many items need the person, those past the 50
	// listed too.
	// +optional
	NeedsYouWaiting int32 `json:"needsYouWaiting,omitempty"`

	// tended are the failed issues rounds answered and the Brief remembers,
	// so it raises one again only when its tries grow or a wait lapses; at
	// most 50.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=50
	// +optional
	Tended []BriefTendedIssue `json:"tended,omitempty"`

	// tendRound is the last tending round: a round with no new prompt that
	// decides the failed issues.
	// +optional
	TendRound int32 `json:"tendRound,omitempty"`

	// lastTendTime is when the last tending round started.
	// +optional
	LastTendTime *metav1.Time `json:"lastTendTime,omitempty"`

	// apps has a line per Container the Brief made, then one per spec.releases
	// entry naming no Container it made: whether its releases are on, where
	// they are published, the latest one, and a sentence on how they go.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=100
	// +optional
	Apps []BriefApp `json:"apps,omitempty"`

	// history has an entry per round, oldest first; the newest 20 are kept.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=20
	// +optional
	History []BriefRound `json:"history,omitempty"`

	// repositoryURL is the Brief's own repository, where every round's plan
	// is kept.
	// +optional
	RepositoryURL string `json:"repositoryURL,omitempty"`

	// costMilliUSD is what the Brief's planning cost over all rounds, in
	// thousandths of a US dollar.
	// +optional
	CostMilliUSD int64 `json:"costMilliUSD,omitempty"`

	// runsWithoutCost counts the planner runs, over all rounds, whose agent
	// didn't report what they cost; costMilliUSD leaves them out.
	// +optional
	RunsWithoutCost int32 `json:"runsWithoutCost,omitempty"`

	// tokens is what the planner runs used over all rounds, and how large the
	// largest session got.
	// +optional
	Tokens *TokenUsage `json:"tokens,omitempty"`

	// observedGeneration is the spec generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Planned says whether the last round's plan is applied, and
	// why not. NeedsInput, while True, says in one sentence what the Brief
	// waits for from the person. NeedsYou, while True, says the first of
	// needsYou and how many more.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=c4
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Round",type=integer,JSONPath=".status.round"
// +kubebuilder:printcolumn:name="Suggestions",type=integer,JSONPath=".status.suggestionsWaiting"
// +kubebuilder:printcolumn:name="Failed",type=integer,JSONPath=".status.failedWaiting"
// +kubebuilder:printcolumn:name="Needs you",type=integer,JSONPath=".status.needsYouWaiting"
// +kubebuilder:printcolumn:name="Releases",type=string,JSONPath=".status.apps[*].lastRelease.tag"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Brief is an app idea in plain words and its follow-up prompts. A planner
// Agent turns each round (the idea, then each batch of new prompts) into the
// model, a Crew per new Container and the first issues, and says in plain
// words what it chose. Deleting a Brief keeps what it made.
type Brief struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec holds the idea, the prompts and the Agents
	// +required
	Spec BriefSpec `json:"spec"`

	// status defines the observed state of Brief
	// +optional
	Status BriefStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// BriefList contains a list of Brief
type BriefList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Brief `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Brief{}, &BriefList{})
		return nil
	})
}
