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

	// outcome is how the round ended: Applied, PlannerNeedsInput or RoundFailed.
	// +kubebuilder:validation:Enum=Applied;PlannerNeedsInput;RoundFailed
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

	// observedGeneration is the spec generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Planned says whether the last round's plan is applied, and
	// why not. NeedsInput, while True, says in one sentence what the Brief
	// waits for from the person.
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
