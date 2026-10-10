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

// TaskKind is what a Task does: implement a goal in a Container's repository,
// or plan a round of a Brief.
// +kubebuilder:validation:Enum=Implement;Plan
type TaskKind string

// Task kinds.
const (
	TaskImplement TaskKind = "Implement"
	TaskPlan      TaskKind = "Plan"
)

// TaskSpec is a goal for one Container, done by an Agent: written here, or in an
// issue of the Container's repository. A Plan Task is a planning round of a
// Brief instead: it names the Brief, and the Brief's repository holds the goal.
// +kubebuilder:validation:XValidation:rule="has(self.kind) && self.kind == 'Plan' ? !has(self.issue) : has(self.goal) != has(self.issue)",message="set exactly one of goal and issue; a Plan Task has no issue"
// +kubebuilder:validation:XValidation:rule="has(self.containerRef) != has(self.briefRef)",message="set exactly one of containerRef and briefRef"
// +kubebuilder:validation:XValidation:rule="!has(self.briefRef) || (has(self.kind) && self.kind == 'Plan')",message="briefRef needs kind Plan"
// +kubebuilder:validation:XValidation:rule="!has(self.kind) || self.kind != 'Plan' || has(self.briefRef)",message="a Plan Task needs a briefRef"
// +kubebuilder:validation:XValidation:rule="!has(self.kind) || self.kind != 'Plan' || !has(self.reviewerRef)",message="a Plan Task has no reviewerRef"
// +kubebuilder:validation:XValidation:rule="has(self.containerRef) == has(oldSelf.containerRef) && has(self.briefRef) == has(oldSelf.briefRef)",message="containerRef and briefRef are immutable; create a new Task"
// +kubebuilder:validation:XValidation:rule="(has(self.kind) ? self.kind : 'Implement') == (has(oldSelf.kind) ? oldSelf.kind : 'Implement')",message="kind is immutable; create a new Task"
// +kubebuilder:validation:XValidation:rule="has(self.goal) == has(oldSelf.goal) && has(self.issue) == has(oldSelf.issue)",message="goal and issue are immutable; create a new Task"
// +kubebuilder:validation:XValidation:rule="has(self.reviewerRef) == has(oldSelf.reviewerRef)",message="reviewerRef is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.allowedProtectedPaths) || has(self.issue)",message="allowedProtectedPaths needs an issue"
// +kubebuilder:validation:XValidation:rule="has(self.allowedProtectedPaths) == has(oldSelf.allowedProtectedPaths)",message="allowedProtectedPaths is immutable"
type TaskSpec struct {
	// kind is Implement (the default): implement the goal in the Container's
	// repository; or Plan: plan a round of the Brief named by briefRef. It can't
	// change.
	// +kubebuilder:default=Implement
	// +optional
	Kind TaskKind `json:"kind,omitempty"`

	// containerRef names the Container to work on; every Task but a Plan Task
	// has one. It can't change.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="containerRef is immutable"
	// +optional
	ContainerRef LocalRef `json:"containerRef,omitzero"`

	// briefRef names the Brief a Plan Task plans a round of, in place of
	// containerRef. It can't change.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="briefRef is immutable"
	// +optional
	BriefRef *LocalRef `json:"briefRef,omitempty"`

	// agentRef names the Agent that implements the goal, and reviews it when no
	// reviewerRef is set. It can't change.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="agentRef is immutable"
	// +required
	AgentRef LocalRef `json:"agentRef"`

	// reviewerRef names the Agent that reviews each round, so the review can use
	// another model than the work. It can't change.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="reviewerRef is immutable"
	// +optional
	ReviewerRef *LocalRef `json:"reviewerRef,omitempty"`

	// goal says what the change must achieve, in plain words. It can't change.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=8000
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="goal is immutable; create a new Task"
	// +optional
	Goal string `json:"goal,omitempty"`

	// issue is the number of an issue in the Container's repository whose title
	// and body are the goal. The operator reads it before each run, labels it with
	// the Task's progress, and closes it when the Task merges. It can't change.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="issue is immutable; create a new Task"
	// +optional
	Issue int32 `json:"issue,omitempty"`

	// allowedProtectedPaths are Standard-protected files, other than AGENTS.md,
	// docs/agents/ and .c4factory.yaml, that this Task may change. The agent
	// still proposes them, and the change goes through its own review. It needs
	// an issue, and it can't change.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=20
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:XValidation:rule="self.all(p, p != 'AGENTS.md' && p != '.c4factory.yaml' && p != 'docs/agents' && !p.startsWith('docs/agents/'))",message="allowedProtectedPaths may not include AGENTS.md, docs/agents/ or .c4factory.yaml"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="allowedProtectedPaths is immutable"
	// +optional
	AllowedProtectedPaths []string `json:"allowedProtectedPaths,omitempty"`
}

// TaskPhase is where a Task is in its rounds.
// +kubebuilder:validation:Enum=Pending;Implementing;Checking;Reviewing;Blocked;Merged;Failed
type TaskPhase string

// Task phases.
const (
	TaskPending      TaskPhase = "Pending"
	TaskImplementing TaskPhase = "Implementing"
	TaskChecking     TaskPhase = "Checking"
	TaskReviewing    TaskPhase = "Reviewing"
	TaskBlocked      TaskPhase = "Blocked"
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

// TaskTransition is one step in a Task's timeline: from at on, the Task was in
// phase and round, for reason.
type TaskTransition struct {
	// at is when the step began.
	// +required
	At metav1.Time `json:"at"`

	// phase is the Task's phase from then on.
	// +required
	Phase TaskPhase `json:"phase"`

	// round is the implement round.
	// +optional
	Round int32 `json:"round,omitempty"`

	// reason is the Progressing condition's reason, such as Running,
	// WaitingForSlot or Checking.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	Reason string `json:"reason,omitempty"`

	// message says more, in up to 200 characters.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Message string `json:"message,omitempty"`
}

// TaskAgentRun is what a Task keeps of one agent run once it ended.
type TaskAgentRun struct {
	// round is the implement round the run belonged to.
	// +required
	Round int32 `json:"round"`

	// role is implement, review or plan; empty when neither c4agent nor the
	// sandbox's name said.
	// +kubebuilder:validation:MaxLength=32
	// +optional
	Role string `json:"role,omitempty"`

	// sandbox names the Sandbox the run ran in.
	// +kubebuilder:validation:MaxLength=63
	// +optional
	Sandbox string `json:"sandbox,omitempty"`

	// endedAt is when the Task saw the run end.
	// +required
	EndedAt metav1.Time `json:"endedAt"`

	// costMilliUSD is what the run cost, in thousandths of a US dollar, as its
	// agent reported it; unset when the agent didn't say.
	// +optional
	CostMilliUSD *int64 `json:"costMilliUSD,omitempty"`

	// tokens is what the run's agent sessions used; runsWithout is 1 when the
	// agent didn't report them.
	// +optional
	Tokens *TokenUsage `json:"tokens,omitempty"`

	// sessionHandoffs are the sizes, in tokens, at which the run's agent
	// sessions handed their work to a fresh session, the first 8.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=8
	// +optional
	SessionHandoffs []int64 `json:"sessionHandoffs,omitempty"`
}

// TaskStandardChange is the Standard change a Task proposed: the protected files
// it wants changed, and where that change is being reviewed.
type TaskStandardChange struct {
	// issue is the number of the issue filed for the change.
	// +required
	Issue int32 `json:"issue"`

	// issueURL is where to find the issue.
	// +optional
	IssueURL string `json:"issueURL,omitempty"`

	// paths are the protected files the proposal changes.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=20
	// +required
	Paths []string `json:"paths"`

	// branch is where the change is made.
	// +optional
	Branch string `json:"branch,omitempty"`

	// count is how many Standard changes this Task has proposed.
	// +required
	Count int32 `json:"count"`

	// resumedRound is the round the Task resumed in after the change merged.
	// +optional
	ResumedRound int32 `json:"resumedRound,omitempty"`
}

// TaskStatus defines the observed state of Task.
type TaskStatus struct {
	// phase is Pending, then Implementing, Checking and Reviewing each round, then
	// Merged or Failed. Blocked means the Task waits for a Standard change.
	// +optional
	Phase TaskPhase `json:"phase,omitempty"`

	// round is the implement round, from 1 up to the Agent's maxRounds.
	// +optional
	Round int32 `json:"round,omitempty"`

	// reviewAttempts counts reviewer runs in this round that gave no verdict.
	// +optional
	ReviewAttempts int32 `json:"reviewAttempts,omitempty"`

	// reviewAgains counts the review runs in this round that started again
	// because the review run that approved a Standard change ended while CI ran
	// again. They spend no review attempt.
	// +optional
	ReviewAgains int32 `json:"reviewAgains,omitempty"`

	// reruns counts the runs in this round that the infrastructure interrupted
	// (a node shutdown, an eviction, a preemption) and that ran again without
	// spending the round.
	// +optional
	Reruns int32 `json:"reruns,omitempty"`

	// restarts counts how often this round started over in place: sent back to
	// resolve conflicts with main, or resumed after its Standard change merged.
	// It gives each run of the round a sandbox name of its own.
	// +optional
	Restarts int32 `json:"restarts,omitempty"`

	// issueTitle is the title of spec.issue when the Task last read it, and
	// issueURL where to find the issue.
	// +optional
	IssueTitle string `json:"issueTitle,omitempty"`

	// +optional
	IssueURL string `json:"issueURL,omitempty"`

	// followUps are the issues filed from the reviewer's suggestions on merge.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=3
	// +optional
	FollowUps []string `json:"followUps,omitempty"`

	// byHand is the address of the kind/by-hand issue filed on merge for the
	// criteria the approved review left open, which need a person. A rerun of
	// the merge step that finds it set files none again.
	// +optional
	ByHand string `json:"byHand,omitempty"`

	// branch is where the agent works.
	// +optional
	Branch string `json:"branch,omitempty"`

	// commit is the head of the branch the current round produced.
	// +optional
	Commit string `json:"commit,omitempty"`

	// mergeCommit is the commit the pull request's squash merge made on the
	// default branch, recorded once the Task is Merged. A Crew tags releases at it.
	// +optional
	MergeCommit string `json:"mergeCommit,omitempty"`

	// pullRequest is the number of the pull request, and pullRequestURL its address.
	// +optional
	PullRequest int32 `json:"pullRequest,omitempty"`

	// +optional
	PullRequestURL string `json:"pullRequestURL,omitempty"`

	// pullRequestEnded says how a Failed Task's pull request ended once the
	// operator saw it: closed by the operator and why, closed or merged on the
	// Git host, or continued by a newer try at the issue. Empty while the
	// pull request stays open for a later try.
	// +optional
	PullRequestEnded string `json:"pullRequestEnded,omitempty"`

	// continues names the earlier try, a Failed Task on the same issue, whose
	// open pull request this Task took over: its branch, pull request and
	// feedback became this Task's when its first run started.
	// +optional
	Continues string `json:"continues,omitempty"`

	// checks is the CI result on commit.
	// +optional
	Checks *CheckStatus `json:"checks,omitempty"`

	// review is the reviewer's latest verdict.
	// +optional
	Review *TaskReview `json:"review,omitempty"`

	// mergeWhenChecksPass means the reviewer approved the change and the
	// operator merged main into its branch, since main had moved since the
	// change's checks ran: commit merges once its checks pass, with no new review.
	// +optional
	MergeWhenChecksPass bool `json:"mergeWhenChecksPass,omitempty"`

	// summary is the implementing agent's account of its latest round.
	// +optional
	Summary string `json:"summary,omitempty"`

	// feedback is what the next round must address: review findings, failed
	// checks or the reason the last round failed.
	// +optional
	Feedback string `json:"feedback,omitempty"`

	// standardChange is the Standard change the Task filed last: the one it
	// waits for while Blocked. It stays after the Task resumes: it counts the
	// changes filed, a later filing doesn't reuse its issue, and a resumed round
	// that leaves nothing to do ends the Task as DoneByStandardChange. A Crew
	// grants its paths to the Task that runs the change.
	// +optional
	StandardChange *TaskStandardChange `json:"standardChange,omitempty"`

	// costMilliUSD is what the Task's agent runs cost so far, over all rounds,
	// in thousandths of a US dollar, as the agents reported it.
	// +optional
	CostMilliUSD int64 `json:"costMilliUSD,omitempty"`

	// runsWithoutCost counts the Task's agent runs whose agent didn't report
	// what they cost; costMilliUSD leaves them out.
	// +optional
	RunsWithoutCost int32 `json:"runsWithoutCost,omitempty"`

	// tokens is what the Task's agent runs used so far, over all rounds, and
	// how large the largest session got.
	// +optional
	Tokens *TokenUsage `json:"tokens,omitempty"`

	// sandbox names the Sandbox running now, if any.
	// +optional
	Sandbox string `json:"sandbox,omitempty"`

	// sandboxPod is the UID of the pod the running sandbox's run started on.
	// Agent Sandbox replaces a pod that went away without c4agent ending it (a
	// forced delete, a lost node); another pod, or none, under the same sandbox
	// means the run was interrupted.
	// +optional
	SandboxPod string `json:"sandboxPod,omitempty"`

	// agentBackAt is when to try an unavailable agent again: its service hit a
	// usage limit, was overloaded or refused the credentials. Waiting costs no round.
	// +optional
	AgentBackAt *metav1.Time `json:"agentBackAt,omitempty"`

	// slotWaitSince is when the Task started waiting for a free agent slot; the
	// longest waiter gets the next one.
	// +optional
	SlotWaitSince *metav1.Time `json:"slotWaitSince,omitempty"`

	// phaseChangedAt is when the Task entered its current phase.
	// +optional
	PhaseChangedAt *metav1.Time `json:"phaseChangedAt,omitempty"`

	// completedAt is when the Task was Merged or Failed.
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`

	// runs records the Task's agent runs as each ends, oldest first: its round
	// and role, what it cost and used, and the sizes at which its sessions
	// handed off. The newest 64 are kept.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=64
	// +optional
	Runs []TaskAgentRun `json:"runs,omitempty"`

	// history is the Task's timeline, oldest first: an entry each time its phase,
	// round or Progressing reason changed. The newest 64 are kept.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=64
	// +optional
	History []TaskTransition `json:"history,omitempty"`

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
// +kubebuilder:printcolumn:name="Issue",type=integer,JSONPath=".spec.issue"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Round",type=integer,JSONPath=".status.round"
// +kubebuilder:printcolumn:name="Checks",type=string,JSONPath=".status.checks.state"
// +kubebuilder:printcolumn:name="Review",type=string,JSONPath=".status.review.verdict"
// +kubebuilder:printcolumn:name="PR",type=string,JSONPath=".status.pullRequestURL",priority=1
// +kubebuilder:printcolumn:name="Kind",type=string,JSONPath=".spec.kind",priority=1
// +kubebuilder:printcolumn:name="Brief",type=string,JSONPath=".spec.briefRef.name",priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Task is a goal for a Container. The operator runs an Agent in a sandbox to
// implement it on a branch, opens a pull request, waits for CI, runs the Agent
// again as reviewer, and merges when both pass. It is owned by its Container. A
// Plan Task is a planning round of a Brief, owned by the Brief.
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
