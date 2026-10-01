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

// TaskSpec is a goal for one Container, done by an Agent.
type TaskSpec struct {
	// containerRef names the Container to work on. It can't change.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="containerRef is immutable"
	// +required
	ContainerRef LocalRef `json:"containerRef"`

	// agentRef names the Agent that does the work and the review. It can't change.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="agentRef is immutable"
	// +required
	AgentRef LocalRef `json:"agentRef"`

	// goal says what the change must achieve, in plain words. It can't change.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=8000
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="goal is immutable; create a new Task"
	// +required
	Goal string `json:"goal"`
}

// TaskPhase is where a Task is in its rounds.
// +kubebuilder:validation:Enum=Pending;Implementing;Checking;Reviewing;Merged;Failed
type TaskPhase string

// Task phases.
const (
	TaskPending      TaskPhase = "Pending"
	TaskImplementing TaskPhase = "Implementing"
	TaskChecking     TaskPhase = "Checking"
	TaskReviewing    TaskPhase = "Reviewing"
	TaskMerged       TaskPhase = "Merged"
	TaskFailed       TaskPhase = "Failed"
)

// TaskReview is the reviewer's verdict on a round.
type TaskReview struct {
	// verdict is approve or changes.
	// +optional
	Verdict string `json:"verdict,omitempty"`

	// summary is what the reviewer found.
	// +optional
	Summary string `json:"summary,omitempty"`
}

// TaskStatus defines the observed state of Task.
type TaskStatus struct {
	// phase is Pending, then Implementing, Checking and Reviewing each round, then
	// Merged or Failed.
	// +optional
	Phase TaskPhase `json:"phase,omitempty"`

	// round is the implement round, from 1 up to the Agent's maxRounds.
	// +optional
	Round int32 `json:"round,omitempty"`

	// reviewAttempts counts reviewer runs in this round that gave no verdict.
	// +optional
	ReviewAttempts int32 `json:"reviewAttempts,omitempty"`

	// branch is where the agent works.
	// +optional
	Branch string `json:"branch,omitempty"`

	// commit is the head of the branch the current round produced.
	// +optional
	Commit string `json:"commit,omitempty"`

	// pullRequest is the number of the pull request, and pullRequestURL its address.
	// +optional
	PullRequest int32 `json:"pullRequest,omitempty"`

	// +optional
	PullRequestURL string `json:"pullRequestURL,omitempty"`

	// checks is the CI result on commit.
	// +optional
	Checks *CheckStatus `json:"checks,omitempty"`

	// review is the reviewer's latest verdict.
	// +optional
	Review *TaskReview `json:"review,omitempty"`

	// summary is the implementing agent's account of its latest round.
	// +optional
	Summary string `json:"summary,omitempty"`

	// feedback is what the next round must address: review findings, failed
	// checks or the reason the last round failed.
	// +optional
	Feedback string `json:"feedback,omitempty"`

	// sandbox names the Sandbox running now, if any.
	// +optional
	Sandbox string `json:"sandbox,omitempty"`

	// phaseChangedAt is when the Task entered its current phase.
	// +optional
	PhaseChangedAt *metav1.Time `json:"phaseChangedAt,omitempty"`

	// completedAt is when the Task was Merged or Failed.
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`

	// observedGeneration is the spec generation the status reflects.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Progressing and Succeeded.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=c4
// +kubebuilder:printcolumn:name="Container",type=string,JSONPath=".spec.containerRef.name"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Round",type=integer,JSONPath=".status.round"
// +kubebuilder:printcolumn:name="Checks",type=string,JSONPath=".status.checks.state"
// +kubebuilder:printcolumn:name="Review",type=string,JSONPath=".status.review.verdict"
// +kubebuilder:printcolumn:name="PR",type=string,JSONPath=".status.pullRequestURL",priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Task is a goal for a Container. The operator runs an Agent in a sandbox to
// implement it on a branch, opens a pull request, waits for CI, runs the Agent
// again as reviewer, and merges when both pass. It is owned by its Container.
type Task struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the goal, the Container and the Agent
	// +required
	Spec TaskSpec `json:"spec"`

	// status defines the observed state of Task
	// +optional
	Status TaskStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TaskList contains a list of Task
type TaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Task `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Task{}, &TaskList{})
		return nil
	})
}
