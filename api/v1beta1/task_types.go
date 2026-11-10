/*
Copyright 2026 The theos Authors.

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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// TaskKind is what a Task does: implement a goal in a Container's repository,
// plan a round of a Brief, triage an issue, write an issue's spec, or judge
// another Task's finished change for a Scorer.
// +kubebuilder:validation:Enum=Implement;Plan;Triage;Spec;Score
type TaskKind string

// Task kinds.
const (
	TaskImplement TaskKind = "Implement"
	TaskPlan      TaskKind = "Plan"
	// TaskTriage decides what a proposed issue needs next; the operator then
	// labels, comments on or closes the issue (docs/design.md, "Triage").
	TaskTriage TaskKind = "Triage"
	// TaskKindSpec writes an issue's spec, docs/specs/<issue>.md, in a pull
	// request that waits for its review point before it merges; the operator
	// then copies its acceptance criteria into the issue (docs/design.md,
	// "Spec").
	TaskKindSpec TaskKind = "Spec"
	// TaskScore judges another Task's finished change against a Scorer's
	// criteria: one read-only run of the Scorer's judge, whose verdicts are
	// its status.score (docs/plans/done/measure-and-improve.md). Its Scorer makes
	// it and owns it.
	TaskScore TaskKind = "Score"
)

// TaskSpec is a goal for one Container, done by an Agent: written here, or in an
// issue of the Container's repository. A Plan Task is a planning round of a
// Brief instead: it names the Brief, and the Brief's repository holds the goal.
// +kubebuilder:validation:XValidation:rule="has(self.kind) && self.kind == 'Plan' ? !has(self.issue) : has(self.goal) != has(self.issue)",message="set exactly one of goal and issue; a Plan Task has no issue"
// +kubebuilder:validation:XValidation:rule="has(self.containerRef) != has(self.briefRef)",message="set exactly one of containerRef and briefRef"
// +kubebuilder:validation:XValidation:rule="!has(self.briefRef) || (has(self.kind) && self.kind == 'Plan')",message="briefRef needs kind Plan"
// +kubebuilder:validation:XValidation:rule="!has(self.kind) || self.kind != 'Plan' || has(self.briefRef)",message="a Plan Task needs a briefRef"
// +kubebuilder:validation:XValidation:rule="!has(self.kind) || self.kind != 'Plan' || !has(self.reviewerRef)",message="a Plan Task has no reviewerRef"
// +kubebuilder:validation:XValidation:rule="!has(self.kind) || self.kind != 'Triage' || has(self.issue)",message="a Triage Task needs an issue"
// +kubebuilder:validation:XValidation:rule="!has(self.kind) || self.kind != 'Triage' || (!has(self.reviewerRef) && !has(self.allowedProtectedPaths))",message="a Triage Task has no reviewerRef and no allowedProtectedPaths"
// +kubebuilder:validation:XValidation:rule="!has(self.kind) || self.kind != 'Spec' || (has(self.issue) && !has(self.allowedProtectedPaths))",message="a Spec Task needs an issue and has no allowedProtectedPaths"
// +kubebuilder:validation:XValidation:rule="has(self.containerRef) == has(oldSelf.containerRef) && has(self.briefRef) == has(oldSelf.briefRef)",message="containerRef and briefRef are immutable; create a new Task"
// +kubebuilder:validation:XValidation:rule="(has(self.kind) ? self.kind : 'Implement') == (has(oldSelf.kind) ? oldSelf.kind : 'Implement')",message="kind is immutable; create a new Task"
// +kubebuilder:validation:XValidation:rule="has(self.goal) == has(oldSelf.goal) && has(self.issue) == has(oldSelf.issue)",message="goal and issue are immutable; create a new Task"
// +kubebuilder:validation:XValidation:rule="has(self.reviewerRef) == has(oldSelf.reviewerRef)",message="reviewerRef is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.verifierRef) == has(oldSelf.verifierRef)",message="verifierRef is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.verifierRef) || ((!has(self.kind) || self.kind == 'Implement') && !has(self.allowedProtectedPaths))",message="only an Implement Task without allowedProtectedPaths has a verifierRef: a spec, a plan, a triage and a Standard change are not verified"
// +kubebuilder:validation:XValidation:rule="!has(self.allowedProtectedPaths) || has(self.issue)",message="allowedProtectedPaths needs an issue"
// +kubebuilder:validation:XValidation:rule="has(self.allowedProtectedPaths) == has(oldSelf.allowedProtectedPaths)",message="allowedProtectedPaths is immutable"
// +kubebuilder:validation:XValidation:rule="(has(self.kind) && self.kind == 'Score') == has(self.score)",message="a Score Task needs score, and only a Score Task has one"
// +kubebuilder:validation:XValidation:rule="!has(self.kind) || self.kind != 'Score' || (has(self.goal) && has(self.containerRef) && !has(self.reviewerRef) && !has(self.verifierRef) && !has(self.allowedProtectedPaths))",message="a Score Task has a goal and a containerRef, and no reviewerRef, verifierRef or allowedProtectedPaths"
// +kubebuilder:validation:XValidation:rule="has(self.score) == has(oldSelf.score)",message="score is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.benchmark) || ((!has(self.kind) || self.kind == 'Implement') && has(self.goal) && !has(self.allowedProtectedPaths))",message="a Benchmark's Task is an Implement Task with a goal and no allowedProtectedPaths"
// +kubebuilder:validation:XValidation:rule="has(self.benchmark) == has(oldSelf.benchmark)",message="benchmark is immutable"
type TaskSpec struct {
	// kind is Implement (the default): implement the goal in the Container's
	// repository; Plan: plan a round of the Brief named by briefRef; Triage:
	// decide what the issue needs next, which the operator then applies to the
	// issue; Spec: write the issue's spec, docs/specs/<issue>.md, which
	// merges after its review point and gives the issue its acceptance
	// criteria; or Score: judge another Task's finished change for the
	// Scorer named in score. It can't change.
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

	// verifierRef names the Agent that verifies the change once the reviewer
	// approved it, before it merges: it builds and starts the change and
	// checks each acceptance criterion against what runs, in the phase
	// Verifying (docs/design.md, "Verify"). Only an Implement Task without
	// allowedProtectedPaths has one. Unset, the reviewer's approval merges. It
	// can't change.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="verifierRef is immutable"
	// +optional
	VerifierRef *LocalRef `json:"verifierRef,omitempty"`

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
	// docs/agents/ and .theos.yaml, that this Task may change. The agent
	// still proposes them, and the change goes through its own review. It needs
	// an issue, and it can't change.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=20
	// +kubebuilder:validation:items:MaxLength=256
	// +kubebuilder:validation:XValidation:rule="self.all(p, p != 'AGENTS.md' && p != '.theos.yaml' && p != 'docs/agents' && !p.startsWith('docs/agents/'))",message="allowedProtectedPaths may not include AGENTS.md, docs/agents/ or .theos.yaml"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="allowedProtectedPaths is immutable"
	// +optional
	AllowedProtectedPaths []string `json:"allowedProtectedPaths,omitempty"`

	// score is what a Score Task judges: the Scorer that made it, the Task it
	// judges and the criteria, copied from the Scorer when it was made. It
	// can't change.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="score is immutable"
	// +optional
	Score *TaskScoreSpec `json:"score,omitempty"`

	// benchmark makes the Task one of a Benchmark's runs: it branches from
	// the Benchmark's base commit, never takes in main, and ends Approved
	// instead of merging. It can't change.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="benchmark is immutable"
	// +optional
	Benchmark *TaskBenchmarkSpec `json:"benchmark,omitempty"`
}

// TaskBenchmarkSpec is which Benchmark run a Task is.
type TaskBenchmarkSpec struct {
	// benchmarkRef names the Benchmark.
	// +required
	BenchmarkRef LocalRef `json:"benchmarkRef"`
	// variant, goal and repetition say which run: the variant's name, the
	// goal's place (from 0) and the repetition (from 1).
	// +kubebuilder:validation:MaxLength=16
	// +required
	Variant string `json:"variant"`
	// +kubebuilder:validation:Minimum=0
	// +optional
	Goal int32 `json:"goal,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +required
	Repetition int32 `json:"repetition"`
	// base is the commit the Task branches from.
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{40}$`
	// +required
	Base string `json:"base"`
}

// TaskScoreSpec is what a Score Task judges.
type TaskScoreSpec struct {
	// scorerRef names the Scorer that made the Task.
	// +required
	ScorerRef LocalRef `json:"scorerRef"`

	// scorerGeneration is the Scorer's generation the criteria are of.
	// +kubebuilder:validation:Minimum=1
	// +required
	ScorerGeneration int64 `json:"scorerGeneration"`

	// taskRef names the finished Task it judges.
	// +required
	TaskRef LocalRef `json:"taskRef"`

	// criteria are the Scorer's criteria, as they were.
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=20
	// +kubebuilder:validation:items:MaxLength=500
	// +required
	Criteria []string `json:"criteria"`

	// config is the Agent configuration the judged Task's implementer ran on,
	// copied from its runs, so Scores compare configurations.
	// +optional
	Config TaskRunConfig `json:"config,omitzero"`
}

// TaskRunConfig is the Agent configuration an agent run ran on.
type TaskRunConfig struct {
	// +kubebuilder:validation:MaxLength=253
	// +optional
	Agent string `json:"agent,omitempty"`
	// +kubebuilder:validation:MaxLength=32
	// +optional
	Harness string `json:"harness,omitempty"`
	// +kubebuilder:validation:MaxLength=100
	// +optional
	Model string `json:"model,omitempty"`
}

// ScoreVerdict is a judge's verdict on a criterion.
// +kubebuilder:validation:Enum=Pass;Fail;Unclear
type ScoreVerdict string

// Score verdicts.
const (
	ScorePass    ScoreVerdict = "Pass"
	ScoreFail    ScoreVerdict = "Fail"
	ScoreUnclear ScoreVerdict = "Unclear"
)

// TaskScoreCriterion is the judge's verdict on one criterion, and why.
type TaskScoreCriterion struct {
	// criterion is the criterion's text.
	// +kubebuilder:validation:MaxLength=500
	// +required
	Criterion string `json:"criterion"`

	// verdict is Pass, Fail, or Unclear when the judge couldn't tell or said
	// nothing of it.
	// +required
	Verdict ScoreVerdict `json:"verdict"`

	// reason is the judge's why, in a sentence or two.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Reason string `json:"reason,omitempty"`
}

// TaskScoreStatus is what a Score Task's judge found.
type TaskScoreStatus struct {
	// criteria are the verdicts, one per criterion of spec.score, in its order.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=20
	// +optional
	Criteria []TaskScoreCriterion `json:"criteria,omitempty"`
}

// TaskPhase is where a Task is in its rounds.
// +kubebuilder:validation:Enum=Pending;Implementing;Checking;Reviewing;Verifying;AwaitingApproval;Blocked;Merged;Failed;Approved
type TaskPhase string

// Task phases.
const (
	TaskPending      TaskPhase = "Pending"
	TaskImplementing TaskPhase = "Implementing"
	TaskChecking     TaskPhase = "Checking"
	TaskReviewing    TaskPhase = "Reviewing"
	// TaskVerifying is a change the reviewer approved, which the Task's
	// verifier builds, starts and checks against its acceptance criteria
	// before it merges.
	TaskVerifying TaskPhase = "Verifying"
	// TaskAwaitingApproval is a spec, or a change its Crew's approval rules
	// hold for a person, that passed CI and the factory's review: it waits
	// for a person to approve its pull request.
	TaskAwaitingApproval TaskPhase = "AwaitingApproval"
	TaskBlocked          TaskPhase = "Blocked"
	TaskMerged           TaskPhase = "Merged"
	TaskFailed           TaskPhase = "Failed"
	// TaskApproved ends a Benchmark's Task whose change passed its checks and
	// its review: it would have merged, and never does
	// (docs/plans/done/measure-and-improve.md, step 6).
	TaskApproved TaskPhase = "Approved"
)

// TaskReview is the reviewer's verdict on a round.
type TaskReview struct {
	// verdict is approve or changes.
	// +optional
	Verdict string `json:"verdict,omitempty"`

	// summary is what the reviewer found.
	// +optional
	Summary string `json:"summary,omitempty"`

	// followUps are the work an approving reviewer suggested for later, kept
	// here while its change is verified, and filed as issues when it merges.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=3
	// +optional
	FollowUps []TaskFollowUp `json:"followUps,omitempty"`

	// open are the acceptance criteria an approving reviewer accepted as left
	// open for a person, kept here while its change is verified.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=3
	// +optional
	Open []TaskOpenCriterion `json:"open,omitempty"`
}

// TaskFollowUp is work a reviewer suggested for later: the title, body and
// acceptance criteria of the issue it becomes.
type TaskFollowUp struct {
	// title is the issue's title.
	// +kubebuilder:validation:MaxLength=256
	// +required
	Title string `json:"title"`

	// body says what and why.
	// +kubebuilder:validation:MaxLength=2000
	// +optional
	Body string `json:"body,omitempty"`

	// criteria are the issue's acceptance criteria.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=3
	// +kubebuilder:validation:items:MaxLength=512
	// +optional
	Criteria []string `json:"criteria,omitempty"`
}

// TaskOpenCriterion is an acceptance criterion an approval left open, as it
// needs what no agent's sandbox has, and why.
type TaskOpenCriterion struct {
	// criterion is the acceptance criterion.
	// +kubebuilder:validation:MaxLength=512
	// +required
	Criterion string `json:"criterion"`

	// why says what it needs that no sandbox has.
	// +kubebuilder:validation:MaxLength=512
	// +optional
	Why string `json:"why,omitempty"`
}

// TaskVerification is the verifier's verdict on a round's change, which it
// built, started and checked against the acceptance criteria.
type TaskVerification struct {
	// verdict is approve or changes.
	// +optional
	Verdict string `json:"verdict,omitempty"`

	// summary is what the verifier ran and found: each acceptance criterion
	// met, not met or open, with the command it ran and what that printed.
	// +optional
	Summary string `json:"summary,omitempty"`

	// commit is the head the verifier ran.
	// +kubebuilder:validation:MaxLength=64
	// +optional
	Commit string `json:"commit,omitempty"`

	// open are the acceptance criteria an approving verifier left open, as no
	// sandbox can meet them, each with why; they go to a person with the
	// review's when the change merges.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=3
	// +optional
	Open []TaskOpenCriterion `json:"open,omitempty"`
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

// TaskPullRequestSize is the size of a pull request's change.
type TaskPullRequestSize struct {
	// additions are the lines it adds.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Additions int32 `json:"additions,omitempty"`

	// deletions are the lines it removes.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Deletions int32 `json:"deletions,omitempty"`

	// files are the files it changes.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Files int32 `json:"files,omitempty"`
}

// TaskAgentRun is what a Task keeps of one agent run once it ended.
type TaskAgentRun struct {
	// round is the implement round the run belonged to.
	// +required
	Round int32 `json:"round"`

	// role is implement, review, verify, plan or triage; empty when neither
	// theos-agent nor the sandbox's name said.
	// +kubebuilder:validation:MaxLength=32
	// +optional
	Role string `json:"role,omitempty"`

	// sandbox names the Sandbox the run ran in.
	// +kubebuilder:validation:MaxLength=63
	// +optional
	Sandbox string `json:"sandbox,omitempty"`

	// agent names the Agent the run ran as, copied when its sandbox was made
	// or last resumed, so a later change to the Agent doesn't rewrite it
	// (docs/plans/done/measure-and-improve.md, decision 2); empty for runs from
	// before it was recorded.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	Agent string `json:"agent,omitempty"`

	// harness is the Agent's harness the run ran on, copied as agent is.
	// +kubebuilder:validation:MaxLength=32
	// +optional
	Harness string `json:"harness,omitempty"`

	// model is the Agent's model the run ran on, copied as agent is; empty
	// when the Agent named none (the harness's default) or it wasn't
	// recorded.
	// +kubebuilder:validation:MaxLength=100
	// +optional
	Model string `json:"model,omitempty"`

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

// Triage decisions a triager reports.
const (
	TriageReady     = "ready"
	TriageSpec      = "spec"
	TriagePark      = "park"
	TriageDuplicate = "duplicate"
)

// TriageDecision is what a Triage Task's triager decided about its issue, and
// what the operator did with it.
type TriageDecision struct {
	// decision is ready (clear, with acceptance criteria), spec (needs a spec
	// first), park (needs a person's answer) or duplicate (of another issue).
	// +kubebuilder:validation:Enum=ready;spec;park;duplicate
	// +required
	Decision string `json:"decision"`

	// reason is why, in the triager's words.
	// +kubebuilder:validation:MaxLength=2000
	// +optional
	Reason string `json:"reason,omitempty"`

	// criteria are the acceptance criteria a ready decision gave, which the
	// operator writes into an issue body that has none.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:items:MaxLength=512
	// +optional
	Criteria []string `json:"criteria,omitempty"`

	// question is what a park decision asks a person.
	// +kubebuilder:validation:MaxLength=2000
	// +optional
	Question string `json:"question,omitempty"`

	// duplicate is the issue a duplicate decision names.
	// +optional
	Duplicate int32 `json:"duplicate,omitempty"`

	// environments are the test environments a ready decision named, which
	// the operator labelled the issue env/<name> for when the project has
	// them (docs/plans/done/test-environments.md, T13).
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:items:MaxLength=32
	// +optional
	Environments []string `json:"environments,omitempty"`

	// risk is "high" when the triager called the issue's change costly or
	// hard to undo if done wrong. The operator labelled the issue risk/high
	// for it, which the Crew's approval rules can name.
	// +kubebuilder:validation:Enum=high
	// +optional
	Risk string `json:"risk,omitempty"`

	// applied says what the operator did to the issue, or why it did nothing:
	// a person closed or triaged the issue meanwhile.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Applied string `json:"applied,omitempty"`

	// answeredBy is the Kubernetes user whose answer to a park decision's
	// question, from chat (the theos.factory.io/answer annotation), the
	// operator took to the issue; the issue then goes back to
	// status/proposed and is triaged again.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	AnsweredBy string `json:"answeredBy,omitempty"`

	// answered says what the operator did with that answer.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Answered string `json:"answered,omitempty"`
}

// Who approves a spec's pull request before it merges.
const (
	// SpecApprovalPerson waits for a person, someone other than the factory's
	// own accounts, to approve the pull request on the Git host.
	SpecApprovalPerson SpecApproval = "Person"
	// SpecApprovalReviewer takes the reviewer Agent's approval as enough, for
	// a factory no person watches.
	SpecApprovalReviewer SpecApproval = "Reviewer"
)

// SpecApproval is who approves a spec's pull request before it merges.
// +kubebuilder:validation:Enum=Person;Reviewer
type SpecApproval string

// TaskSpecResult is what a Spec Task's spec came to: its file, who approved
// it, and the acceptance criteria copied into its issue once it merged.
type TaskSpecResult struct {
	// path is the spec's file, docs/specs/<issue>.md.
	// +kubebuilder:validation:MaxLength=256
	// +required
	Path string `json:"path"`

	// approval is who the spec waits for: Person or Reviewer.
	// +optional
	Approval SpecApproval `json:"approval,omitempty"`

	// approvedBy is who approved the spec, once a person did: the Git host
	// user who approved the pull request, or the Kubernetes user who approved
	// it from chat (the theos.factory.io/approve-spec annotation; then
	// approvedInChat); empty when the reviewer's approval was enough.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	ApprovedBy string `json:"approvedBy,omitempty"`

	// approvedInChat says approvedBy approved the spec from chat, not on the
	// Git host.
	// +optional
	ApprovedInChat bool `json:"approvedInChat,omitempty"`

	// criteria are the spec's acceptance criteria, copied into the issue's
	// description once the spec merged.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=20
	// +kubebuilder:validation:items:MaxLength=512
	// +optional
	Criteria []string `json:"criteria,omitempty"`

	// applied says what the operator did to the issue once the spec merged.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Applied string `json:"applied,omitempty"`
}

// TaskApproval is what a Crew's approval rules (its spec.approval) ask of a
// Task's change, and who met it.
type TaskApproval struct {
	// require is what the change needs before it merges: Person, a person's
	// approval of its pull request after the agents'; Reviewer, the agents'
	// alone; or None, CI alone.
	// +required
	Require ApprovalRequirement `json:"require"`

	// rule is the number, from 1, of the rule in the Crew's approval that
	// decided; unset when a file no rule matches did.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Rule int32 `json:"rule,omitempty"`

	// reason says which rule asks it, and for which file.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Reason string `json:"reason,omitempty"`

	// commit is the head the answer is of; a later head is asked again once
	// its checks pass.
	// +kubebuilder:validation:MaxLength=64
	// +optional
	Commit string `json:"commit,omitempty"`

	// rules names the Crew's rules the answer came from, by what they say:
	// when the Crew's rules change, the answer is worked out again.
	// +kubebuilder:validation:MaxLength=64
	// +optional
	Rules string `json:"rules,omitempty"`

	// approvedBy is who approved the change, once a person did: the Git host
	// user who approved the pull request, or the Kubernetes user who approved
	// it from chat (the theos.factory.io/approve-spec annotation; then
	// approvedInChat).
	// +kubebuilder:validation:MaxLength=256
	// +optional
	ApprovedBy string `json:"approvedBy,omitempty"`

	// approvedInChat says approvedBy approved the change from chat, not on
	// the Git host.
	// +optional
	ApprovedInChat bool `json:"approvedInChat,omitempty"`
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
	// phase is Pending, then Implementing, Checking and Reviewing each round,
	// and Verifying when the Task has a verifier, then Merged or Failed.
	// Blocked means the Task waits for a Standard change, and AwaitingApproval
	// a Spec Task's spec for a person's approval.
	// +optional
	Phase TaskPhase `json:"phase,omitempty"`

	// round is the implement round, from 1 up to the Agent's maxRounds.
	// +optional
	Round int32 `json:"round,omitempty"`

	// reviewAttempts counts reviewer runs in this round that gave no verdict,
	// and, once the Task is Verifying, the verifier's.
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

	// resumes counts the runs in this round that the infrastructure
	// interrupted and that resumed in place, from the checkpoint their
	// sandbox kept, without a rerun or spending the round. Past five, an
	// interrupted run is rerun.
	// +optional
	Resumes int32 `json:"resumes,omitempty"`

	// checksReruns counts the CI runs at this round's commit whose failed jobs
	// ran again because the machine killed them for lack of memory, without
	// spending the round.
	// +optional
	ChecksReruns int32 `json:"checksReruns,omitempty"`

	// checksRerunAt is when the failed jobs last ran again; a failed status is
	// read as their result only once every run at the commit ended, one started
	// since.
	// +optional
	ChecksRerunAt *metav1.Time `json:"checksRerunAt,omitempty"`

	// checksRerunDue means this round pushed again the commit whose checks
	// failed: their failed jobs run again before the checks are read.
	// +optional
	ChecksRerunDue bool `json:"checksRerunDue,omitempty"`

	// restarts counts how often this round started over in place: sent back to
	// resolve conflicts with main, or resumed after its Standard change merged,
	// and how often a run of it that paused could not resume and ran again.
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

	// pullRequestSize is the size of the merged pull request's change, read
	// once when it merged with personCommits (docs/plans/done/measure-and-improve.md,
	// decision 4); unset before then, or when the Git host couldn't be read.
	// A value, not a pointer: a version that lacks the field carries it in an
	// annotation, which keeps no empty object.
	// +optional
	PullRequestSize TaskPullRequestSize `json:"pullRequestSize,omitzero"`

	// personCommits counts the merged pull request's commits a person wrote:
	// any not by an agent and not a merge (decision 3), read with
	// pullRequestSize. Zero means the factory alone wrote the change; unset,
	// that it wasn't read.
	// +kubebuilder:validation:Minimum=0
	// +optional
	PersonCommits *int32 `json:"personCommits,omitempty"`

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

	// verification is the verifier's latest verdict, once it gave one: what it
	// ran and found, and the criteria it left open.
	// +optional
	Verification *TaskVerification `json:"verification,omitempty"`

	// mergeWhenChecksPass means the reviewer (and the verifier, when the Task
	// has one) approved the change and the operator merged main into its
	// branch, since main had moved since the change's checks ran: commit
	// merges once its checks pass, with no new review or verification.
	// +optional
	MergeWhenChecksPass bool `json:"mergeWhenChecksPass,omitempty"`

	// summary is the implementing agent's account of its latest round.
	// +optional
	Summary string `json:"summary,omitempty"`

	// feedback is what the next round must address: review findings, failed
	// checks or the reason the last round failed.
	// +optional
	Feedback string `json:"feedback,omitempty"`

	// triage is a Triage Task's decision, once its triager reported one, and
	// what the operator did with it.
	// +optional
	Triage *TriageDecision `json:"triage,omitempty"`

	// score is a Score Task's verdicts, once its judge ran; a run that failed
	// leaves each criterion Unclear with why. A value, as pullRequestSize is.
	// +optional
	Score TaskScoreStatus `json:"score,omitzero"`

	// spec is a Spec Task's spec: its file, who approved it, and what the
	// operator did to the issue once it merged.
	// +optional
	Spec *TaskSpecResult `json:"spec,omitempty"`

	// approval is what the Crew's approval rules ask of the Task's change,
	// once its checks passed, and who approved it when they ask for a
	// person. Unset for a Task whose Crew has no rules.
	// +optional
	Approval *TaskApproval `json:"approval,omitempty"`

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
	// Agent Sandbox replaces a pod that went away without theos-agent ending it (a
	// forced delete, a lost node); another pod, or none, under the same sandbox
	// means the run was interrupted.
	// +optional
	SandboxPod string `json:"sandboxPod,omitempty"`

	// countedPod is the UID of the pod, under the running sandbox, whose
	// interrupted run's cost and resume are counted already: a suspend that
	// fails after the count is tried again without counting them twice.
	// +optional
	CountedPod string `json:"countedPod,omitempty"`

	// agentBackAt is when to try an unavailable agent again: its service hit a
	// usage limit, was overloaded or refused the credentials. Waiting costs no round.
	// +optional
	AgentBackAt *metav1.Time `json:"agentBackAt,omitempty"`

	// slotWaitSince is when the Task started waiting for a free agent slot; the
	// longest waiter gets the next one.
	// +optional
	SlotWaitSince *metav1.Time `json:"slotWaitSince,omitempty"`

	// environments are the test environments the current run holds, one
	// lease each, taken before its sandbox and returned when it ends or is
	// suspended (docs/plans/done/test-environments.md, T3).
	// +kubebuilder:validation:MaxItems=16
	// +optional
	Environments []TaskEnvironmentLease `json:"environments,omitempty"`

	// leasedEnvironments are the test environments any run of the Task
	// leased, by name: a by-hand issue its merge files names them.
	// +listType=set
	// +kubebuilder:validation:MaxItems=16
	// +optional
	LeasedEnvironments []string `json:"leasedEnvironments,omitempty"`

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

	// conditions: Progressing and Succeeded, and NeedsYou while a Spec Task's
	// spec waits for a person's approval.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=theos
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Container",type=string,JSONPath=".spec.containerRef.name"
// +kubebuilder:printcolumn:name="Issue",type=integer,JSONPath=".spec.issue"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Round",type=integer,JSONPath=".status.round"
// +kubebuilder:printcolumn:name="Checks",type=string,JSONPath=".status.checks.state"
// +kubebuilder:printcolumn:name="Review",type=string,JSONPath=".status.review.verdict"
// +kubebuilder:printcolumn:name="Verified",type=string,JSONPath=".status.verification.verdict",priority=1
// +kubebuilder:printcolumn:name="PR",type=string,JSONPath=".status.pullRequestURL",priority=1
// +kubebuilder:printcolumn:name="Kind",type=string,JSONPath=".spec.kind",priority=1
// +kubebuilder:printcolumn:name="Brief",type=string,JSONPath=".spec.briefRef.name",priority=1
// +kubebuilder:printcolumn:name="Decision",type=string,JSONPath=".status.triage.decision",priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Task is a goal for a Container. The operator runs an Agent in a sandbox to
// implement it on a branch, opens a pull request, waits for CI, runs the Agent
// again as reviewer, and merges when both pass; with a verifier, that Agent
// first builds, starts and checks the approved change. It is owned by its
// Container. A Plan Task is a planning round of a Brief, owned by the Brief. A Triage Task
// runs a triager on one proposed issue and applies what it decided. A Spec
// Task writes an issue's spec in a pull request that a person approves.
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

// TaskEnvironmentLease is a lease of a test environment a Task's run holds.
type TaskEnvironmentLease struct {
	// name is the TestEnvironment's.
	// +required
	Name string `json:"name"`
	// lease is the lease's ID, which the environment's provider names it by.
	// +required
	Lease string `json:"lease"`
	// sandbox is the run that holds it.
	// +required
	Sandbox string `json:"sandbox"`
	// since is when it was leased.
	// +optional
	Since *metav1.Time `json:"since,omitempty"`
}
