package model

import (
	"strings"
	"testing"
	"time"
)

func TestNextTaskStep(t *testing.T) {
	done := func(r RunResult) RunState { return RunState{Started: true, Done: true, Result: &r} }
	running := RunState{Started: true}
	prop := &Proposal{Branch: "standard/t", Paths: []string{"Makefile"}}

	tests := []struct {
		name  string
		facts TaskFacts
		want  TaskAction
		// feedback and message must contain these.
		feedback, message string
	}{
		{name: "waits for the Container", facts: TaskFacts{Phase: TaskPending, Protected: true, Round: 1, MaxRounds: 3},
			want: ActionWait, message: "Waiting for Container photo-editor to be Ready"},
		{name: "waits behind an earlier Task", facts: TaskFacts{Phase: TaskPending, Protected: true, ContainerReady: true,
			BlockedBy: "add-crop", Round: 1, MaxRounds: 3}, want: ActionWait, message: "Waiting for Task add-crop"},
		{name: "starts when the Container is Ready", facts: TaskFacts{Phase: TaskPending, Protected: true, ContainerReady: true,
			Round: 1, MaxRounds: 3}, want: ActionStartImplement},
		{name: "a new Task starts pending", facts: TaskFacts{Protected: true, ContainerReady: true, MaxRounds: 3},
			want: ActionStartImplement},
		{name: "fails on a Container without protection", facts: TaskFacts{Phase: TaskPending, ContainerReady: true,
			Container: "photo-editor", MaxRounds: 3}, want: ActionFail, message: "photo-editor has no Standard"},

		{name: "restarts a lost implement sandbox", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3},
			want: ActionStartImplement},
		{name: "waits for the agent", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3, Run: running},
			want: ActionWait},
		{name: "opens the pull request", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: done(RunResult{Commit: "abc"})}, want: ActionOpenPR},
		{name: "no change is a failed round", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: done(RunResult{Error: "the agent made no changes"})},
			want: ActionRetry, feedback: "the agent made no changes",
			message: "Round 1 was not accepted (the agent made no changes); starting round 2"},
		{name: "no change and no note says so", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: done(RunResult{Error: NoChangeNoReason})}, want: ActionRetry, feedback: "gave no reason",
			message: "Round 1 was not accepted (the agent made no changes and gave no reason"},
		{name: "a blocked agent fails the Task at once", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: done(RunResult{Blocked: "cel-lens#10 must be released first"})},
			want: ActionFail, message: "The agent can't start the issue: cel-lens#10 must be released first"},
		{name: "a blocked note outweighs an error", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: done(RunResult{Blocked: "waits on the engine", Error: "boom"})},
			want: ActionFail, message: "waits on the engine"},
		{name: "the next round says why, in short", facts: TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3,
			Run: done(RunResult{Verdict: "changes", Summary: "\nTest an empty image.\nAnd more."})},
			want: ActionRetry, message: "(The reviewer asked for changes: Test an empty image.)"},
		{name: "a sandbox without result is a failed round", facts: TaskFacts{Phase: TaskImplementing, Round: 2,
			MaxRounds: 3, Run: RunState{Started: true, Done: true, Failure: "OOMKilled"}},
			want: ActionRetry, feedback: "ended without a result: OOMKilled"},
		{name: "the last round fails the Task", facts: TaskFacts{Phase: TaskImplementing, Round: 3, MaxRounds: 3,
			Run: done(RunResult{Error: "make check still fails"})},
			want: ActionFail, message: "Failed after 3 rounds: make check still fails"},

		{name: "waits for checks", facts: TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3, Checks: "pending"},
			want: ActionWait, message: "Waiting for checks"},
		{name: "waits for checks to start", facts: TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3},
			want: ActionWait},
		{name: "green checks start the review", facts: TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3,
			Checks: "success"}, want: ActionStartReview},
		{name: "red checks send it back", facts: TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3,
			Checks: "failure", ChecksURL: "http://gitea/runs/4"}, want: ActionRetry, feedback: "http://gitea/runs/4"},
		{name: "red checks pass on what failed", facts: TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3,
			Checks: "failure", ChecksLog: "x\n--- FAIL: TestA (0.00s)\n    a_test.go:9: got 2, want 1\n"}, want: ActionRetry,
			feedback: "What failed in CI:\n--- FAIL: TestA (0.00s)\n    a_test.go:9: got 2, want 1"},
		{name: "red checks from a runner out of room fail the Task, costing no round", facts: TaskFacts{
			Phase: TaskChecking, Round: 1, MaxRounds: 3, Checks: "failure", ChecksURL: "http://gitea/runs/5",
			ChecksLog: "--- FAIL: TestBinary\n  compile: writing output: write $WORK/b001/_pkg_.a: no space left on device\n"},
			want: ActionFail, message: "http://gitea/runs/5 failed because the runner ran out of room"},

		{name: "waits for the reviewer", facts: TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3, Run: running},
			want: ActionWait},
		{name: "restarts a lost review sandbox", facts: TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3},
			want: ActionStartReview},
		{name: "approval merges", facts: TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3,
			Run: done(RunResult{Verdict: VerdictApprove, Summary: "Clean."})}, want: ActionMerge},
		{name: "requested changes send it back", facts: TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3,
			Run: done(RunResult{Verdict: VerdictChanges, Summary: "Test empty input."})},
			want: ActionRetry, feedback: "Test empty input."},
		{name: "a reviewer without verdict runs again", facts: TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3,
			Run: done(RunResult{Error: "the reviewer gave no JSON verdict"})}, want: ActionRetryReview},
		{name: "a reviewer failing twice fails the Task", facts: TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3,
			ReviewAttempts: 2, Run: done(RunResult{Error: "the reviewer gave no JSON verdict"})},
			want: ActionFail, message: "The reviewer failed 3 times"},
		{name: "an approval waiting for CI still merges", facts: TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3,
			MergeWaitsForChecks: true, Run: done(RunResult{Verdict: VerdictApprove})}, want: ActionMerge},
		{name: "a review that ends while its merge waits for CI is checked and reviewed again, not failed",
			facts: TaskFacts{Phase: TaskReviewing, Round: 2, MaxRounds: 3, MergeWaitsForChecks: true,
				Run: RunState{Started: true, Done: true, Failure: "the sandbox reached its shutdown time"}},
			want: ActionReviewAgain, message: "ended while CI ran again on the reviewed commit; checking and " +
				"reviewing it again in round 2"},
		{name: "even after earlier reviewer failures", facts: TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3,
			MergeWaitsForChecks: true, ReviewAttempts: 2, Run: RunState{Started: true, Done: true}},
			want: ActionReviewAgain},
		{name: "a reviewer after two ran-out approvals still gets its no-verdict attempts", facts: TaskFacts{
			Phase: TaskReviewing, Round: 1, MaxRounds: 3, ReviewAgains: 2, ReviewAttempts: 1,
			Run: done(RunResult{Error: "the reviewer gave no JSON verdict"})}, want: ActionRetryReview},
		{name: "an approval that keeps running out fails the Task at the bound", facts: TaskFacts{
			Phase: TaskReviewing, Round: 2, MaxRounds: 3, MergeWaitsForChecks: true, ReviewAgains: 3,
			Run: RunState{Started: true, Done: true}}, want: ActionFail, message: "4 times in round 2"},
		{name: "the bound holds with main merged in too", facts: TaskFacts{Phase: TaskChecking, Round: 1,
			MaxRounds: 3, Checks: "success", MergeWhenChecksPass: true, ApprovalEnded: true, ReviewAgains: 3},
			want: ActionFail, message: "with main merged in, 4 times"},
		{name: "below the bound it is reviewed again", facts: TaskFacts{Phase: TaskReviewing, Round: 1,
			MaxRounds: 3, MergeWaitsForChecks: true, ReviewAgains: 2, Run: RunState{Started: true, Done: true}},
			want: ActionReviewAgain},
		{name: "green checks with main merged in merge", facts: TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3,
			Checks: "success", MergeWhenChecksPass: true}, want: ActionMerge, message: "with main merged in; merging"},
		{name: "green checks with main merged in review again once the approval ended", facts: TaskFacts{
			Phase: TaskChecking, Round: 1, MaxRounds: 3, Checks: "success", MergeWhenChecksPass: true,
			ApprovalEnded: true, Behind: true}, want: ActionReviewAgain, message: "ended while CI ran again with " +
			"main merged in; checking and reviewing it again in round 1"},
		{name: "red checks with main merged in still send it back once the approval ended", facts: TaskFacts{
			Phase: TaskChecking, Round: 1, MaxRounds: 3, Checks: "failure", MergeWhenChecksPass: true,
			ApprovalEnded: true}, want: ActionRetry},

		{name: "waits for a free agent slot to start", facts: TaskFacts{Phase: TaskPending, Protected: true, ContainerReady: true,
			Round: 1, MaxRounds: 3, NoFreeSlot: true}, want: ActionWait, message: "Waiting for a free agent slot"},
		{name: "waits for a free slot to review", facts: TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3,
			Checks: "success", NoFreeSlot: true}, want: ActionWait, message: "free agent slot"},
		{name: "a full house does not stop a running round", facts: TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, NoFreeSlot: true, Run: done(RunResult{Commit: "abc"})}, want: ActionOpenPR},

		{name: "a proposal files a Standard change", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: done(RunResult{Commit: "c", Proposal: prop})}, want: ActionPropose, message: "Standard change to Makefile"},
		{name: "a second proposal is still filed", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			StandardChanges: 1, Run: done(RunResult{Commit: "c", Proposal: prop})}, want: ActionPropose},
		{name: "a third proposal fails the Task", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			StandardChanges: 2, Run: done(RunResult{Commit: "c", Proposal: prop})},
			want: ActionFail, message: "more than two Standard changes"},
		{name: "an error beats a proposal", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: done(RunResult{Error: "boom", Proposal: prop})}, want: ActionRetry},
		{name: "an empty resume is done by the Standard change", facts: TaskFacts{Phase: TaskImplementing, Resumed: true,
			Change: ChangeFacts{Issue: 31}, Run: done(RunResult{Empty: true})},
			want: ActionDoneByStandardChange, message: "#31"},
		{name: "empty counts only on a resume", facts: TaskFacts{Phase: TaskImplementing,
			Run: done(RunResult{Commit: "c", Empty: true})}, want: ActionOpenPR},
		{name: "blocked waits for the Standard change", facts: TaskFacts{Phase: TaskBlocked,
			Change: ChangeFacts{Issue: 31, Paths: []string{"Makefile"}}},
			want: ActionWait, message: "Waiting for Standard change #31 (Makefile)"},
		{name: "a merged Standard change resumes the Task", facts: TaskFacts{Phase: TaskBlocked, Round: 3, MaxRounds: 3,
			Feedback: "- fix x", Change: ChangeFacts{Issue: 31, Paths: []string{"Makefile"}, Merged: true}},
			want: ActionResume, feedback: "- fix x", message: "Standard change #31 merged; round 3 goes on"},
		{name: "a failed Standard change fails the Task", facts: TaskFacts{Phase: TaskBlocked,
			Change: ChangeFacts{Issue: 31, Failed: "Task x-31 failed: Failed after 3 rounds"}},
			want: ActionFail, message: "Standard change #31 will not merge: Task x-31 failed"},
		{name: "a full house does not stop a resume", facts: TaskFacts{Phase: TaskBlocked, NoFreeSlot: true,
			AgentBackIn: time.Hour, Change: ChangeFacts{Issue: 31, Merged: true}}, want: ActionResume},
		{name: "a full house does not make blocked a slot wait", facts: TaskFacts{Phase: TaskBlocked, NoFreeSlot: true,
			Change: ChangeFacts{Issue: 31}}, want: ActionWait, message: "Waiting for Standard change #31"},
		{name: "merged is final", facts: TaskFacts{Phase: TaskMerged}, want: ActionNone},
		{name: "failed is final", facts: TaskFacts{Phase: TaskFailed}, want: ActionNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.facts.Container == "" {
				tt.facts.Container = "photo-editor"
			}
			got := NextTaskStep(tt.facts)
			if got.Action != tt.want {
				t.Fatalf("NextTaskStep() = %+v, want action %s", got, tt.want)
			}
			if !strings.Contains(got.Feedback, tt.feedback) {
				t.Errorf("Feedback = %q, want it to contain %q", got.Feedback, tt.feedback)
			}
			if !strings.Contains(got.Message, tt.message) {
				t.Errorf("Message = %q, want it to contain %q", got.Message, tt.message)
			}
			if blocked := tt.facts.Run.Result != nil && tt.facts.Run.Result.Blocked != ""; blocked != (got.Reason == ReasonBlocked) {
				t.Errorf("Reason = %q for a run blocked %v", got.Reason, blocked)
			}
		})
	}
}

// A Task in Checking waits while its run is queued on an online runner or
// running, and fails, reason NoCIResult, only when CI won't report (#758).
func TestNextTaskStepChecksWait(t *testing.T) {
	since := time.Date(2026, 10, 9, 1, 13, 0, 0, time.UTC)
	started := time.Date(2026, 10, 9, 1, 40, 0, 0, time.UTC)
	queued := CIRun{Commit: "abcdef123", State: CIRunQueued, URL: "http://gitea/runs/7"}
	noRunner := queued
	noRunner.NoRunnerOnline = true
	running := func(d time.Duration) CIRun {
		return CIRun{Commit: "abcdef123", State: CIRunRunning, URL: "http://gitea/runs/7", Started: started, Running: d}
	}
	tests := []struct {
		name    string
		ci      CIRun
		in      time.Duration
		want    TaskAction
		message string
	}{
		{"queued 45 minutes with a runner online waits", queued, 45 * time.Minute, ActionWait,
			"Waiting for checks: queued behind other jobs since 01:13Z"},
		{"queued with a runner unknown waits", CIRun{State: CIRunQueued}, 45 * time.Minute, ActionWait,
			"queued behind other jobs"},
		{"queued past 6 hours fails", queued, 6*time.Hour + time.Minute, ActionFail,
			"CI run http://gitea/runs/7 has been queued for 6h0m0s"},
		{"queued with no runner online waits 30 minutes", noRunner, 20 * time.Minute, ActionWait,
			"Waiting for checks: queued with no runner online since 01:13Z"},
		{"queued with no runner online after 30 minutes fails", noRunner, 31 * time.Minute, ActionFail,
			"CI run http://gitea/runs/7 is queued and no runner is online; start a runner for the repository"},
		{"no run waits 30 minutes", CIRun{Commit: "abcdef123"}, 20 * time.Minute, ActionWait,
			"no CI run has started yet"},
		{"no run after 30 minutes fails", CIRun{Commit: "abcdef123"}, 31 * time.Minute, ActionFail,
			"No CI run started for abcdef1 after 30m0s; check that the repository's workflows run on pull requests"},
		{"running 2 hours waits", running(2 * time.Hour), 3 * time.Hour, ActionWait,
			"Waiting for checks: running since 01:40Z"},
		{"running past 3h15m fails", running(3*time.Hour + 16*time.Minute), 4 * time.Hour, ActionFail,
			"CI run http://gitea/runs/7 has run for 3h16m0s, past 3h15m0s"},
		{"running with no start counts from Checking", CIRun{State: CIRunRunning}, 3*time.Hour + 16*time.Minute,
			ActionFail, "past 3h15m0s"},
		{"ended runs with no result fail after 30 minutes", CIRun{Commit: "abcdef123", State: CIRunCompleted},
			31 * time.Minute, ActionFail, "No CI result for abcdef1 after 30m0s, though its CI runs ended"},
		{"unknown runs wait", CIRun{State: CIRunUnknown}, 2 * time.Hour, ActionWait,
			"doesn't say whether a CI run is queued"},
		{"unknown runs fail past 6 hours", CIRun{Commit: "abcdef123", State: CIRunUnknown},
			6*time.Hour + time.Minute, ActionFail, "No CI result for abcdef1 after 6h0m0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextTaskStep(TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3, Checks: "pending",
				CIRun: tt.ci, InPhase: tt.in, CheckingSince: since})
			if got.Action != tt.want || !strings.Contains(got.Message, tt.message) {
				t.Fatalf("NextTaskStep() = %+v, want %s with %q", got, tt.want, tt.message)
			}
			if failed := got.Action == ActionFail; failed != (got.Reason == ReasonNoCIResult) {
				t.Errorf("Reason = %q, want %s on each failure only", got.Reason, ReasonNoCIResult)
			}
		})
	}
}

func TestCommitRunState(t *testing.T) {
	early, late := time.Unix(1000, 0), time.Unix(2000, 0)
	tests := []struct {
		name  string
		runs  []WorkflowRun
		state string
		url   string
	}{
		{"none", nil, CIRunNone, ""},
		{"all ended", []WorkflowRun{{Status: "completed", URL: "a"}}, CIRunCompleted, "a"},
		{"one waits", []WorkflowRun{{Status: "completed", URL: "a"}, {Status: "waiting", URL: "b"},
			{Status: "queued", URL: "c"}}, CIRunQueued, "b"},
		{"running outweighs queued, the longest first", []WorkflowRun{{Status: "queued", URL: "a"},
			{Status: "in_progress", Started: late, URL: "b"}, {Status: "in_progress", Started: early, URL: "c"}},
			CIRunRunning, "c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, run := CommitRunState(tt.runs)
			if state != tt.state || run.URL != tt.url {
				t.Errorf("CommitRunState() = %q, %q; want %q, %q", state, run.URL, tt.state, tt.url)
			}
		})
	}
}

func TestChecksBound(t *testing.T) {
	for message, want := range map[string]string{
		"Waiting for checks: no CI run has started yet":                 "if no CI run starts within 30 minutes",
		"Waiting for checks: queued behind other jobs since 01:13Z":     "still queued 6 hours into its checks",
		"Waiting for checks: queued with no runner online since 01:13Z": "30 minutes into its checks unless a runner comes online",
		"Waiting for checks: running since 01:40Z":                      "still running 3 hours 15 minutes after it started",
	} {
		if got := ChecksBound(message); !strings.Contains(got, want) {
			t.Errorf("ChecksBound(%q) = %q, want it to contain %q", message, got, want)
		}
	}
}

// A round turned back for scope twice in a row stops the Task blocked, and a
// Task whose Standard change stopped blocked stops blocked too (#578).
func TestNextTaskStepScope(t *testing.T) {
	refusal := ScopeRefusal([]string{"go.mod"}, []string{"Makefile"})
	done := func(r RunResult) RunState { return RunState{Started: true, Done: true, Result: &r} }
	tests := []struct {
		name   string
		facts  TaskFacts
		want   TaskAction
		reason string
		// message must contain this.
		message string
	}{
		{name: "the first refusal costs a round", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: done(RunResult{Error: refusal})}, want: ActionRetry, message: "Round 1 was not accepted (Out of scope"},
		{name: "a refusal after other feedback costs a round", facts: TaskFacts{Phase: TaskImplementing, Round: 2,
			MaxRounds: 3, Feedback: "make check still fails", Run: done(RunResult{Error: refusal})}, want: ActionRetry},
		{name: "the second refusal in a row stops the Task blocked", facts: TaskFacts{Phase: TaskImplementing,
			Round: 2, MaxRounds: 3, Feedback: refusal, Run: done(RunResult{Error: refusal})},
			want: ActionFail, reason: ReasonBlocked,
			message: "Two rounds in a row changed files this Task may not change, so it stops instead of spending " +
				"more rounds: the change touches go.mod; this Task may change only Makefile"},
		{name: "a Standard change that stopped blocked holds the Task", facts: TaskFacts{Phase: TaskBlocked,
			Change: ChangeFacts{Issue: 53, Failed: "Task x-53 failed: m", Blocked: true}},
			want: ActionFail, reason: ReasonBlocked, message: "Standard change #53 will not merge: Task x-53 failed: m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextTaskStep(tt.facts)
			if got.Action != tt.want || got.Reason != tt.reason || !strings.Contains(got.Message, tt.message) {
				t.Errorf("NextTaskStep() = %+v; want %s, reason %q, a message with %q", got, tt.want, tt.reason, tt.message)
			}
		})
	}
}

func TestImplementedStepNamesExtras(t *testing.T) {
	got := NextTaskStep(TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3, Run: RunState{Started: true,
		Done: true, Result: &RunResult{Commit: "abcdef1234", Extra: []string{"e2e_test.go", "release_test.go"}}}})
	want := "besides its Standard change's files it changes e2e_test.go and release_test.go, which no Standard protects"
	if got.Action != ActionOpenPR || !strings.Contains(got.Message, want) {
		t.Errorf("NextTaskStep() = %+v, want a message with %q", got, want)
	}
}

// A Plan Task's steps (docs/plans/done/brief.md, step 5): no Container to wait
// for, and the Brief's verdict in place of CI and a review.
func TestNextTaskStepPlan(t *testing.T) {
	done := func(r RunResult) RunState { return RunState{Started: true, Done: true, Result: &r} }
	const sha = "0123456789abcdef"
	tests := []struct {
		name              string
		facts             TaskFacts
		want              TaskAction
		feedback, message string
	}{
		{name: "starts without a Container", facts: TaskFacts{}, want: ActionStartImplement, message: "Starting round 1"},
		{name: "waits for a slot", facts: TaskFacts{Phase: TaskPending, NoFreeSlot: true},
			want: ActionWait, message: "Waiting for a free agent slot"},
		{name: "waits for its agent", facts: TaskFacts{Phase: TaskPending, AgentBackIn: time.Minute},
			want: ActionWait, message: "The agent is unavailable"},
		{name: "waits for the planner", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: RunState{Started: true}}, want: ActionWait},
		{name: "a pushed plan waits for the Brief", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: done(RunResult{Commit: sha, Summary: "Planned it."})},
			want: ActionAwaitPlanCheck, message: "The planner pushed 0123456; waiting for the Brief to check the plan"},
		{name: "a proposal means nothing to a plan", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: done(RunResult{Commit: sha, Proposal: &Proposal{Paths: []string{"Makefile"}}, Empty: true})},
			want: ActionAwaitPlanCheck},
		{name: "a run without a plan is a failed round", facts: TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, Run: done(RunResult{Error: "the planner wrote no plan/"})},
			want: ActionRetry, feedback: "the planner wrote no plan/"},
		{name: "a run that pushed nothing is a failed round", facts: TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, Run: done(RunResult{})}, want: ActionRetry, feedback: "pushed no plan"},
		{name: "an interrupted run runs again", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: RunState{Started: true, Done: true, Interrupted: true, Failure: "evicted"}}, want: ActionRerun},
		{name: "an unavailable planner waits", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: done(RunResult{Unavailable: true, Error: "usage limit"})}, want: ActionAgentUnavailable},
		{name: "waits for the verdict", facts: TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3,
			InPhase: 2 * QueuedChecksLimit}, want: ActionWait, message: "Waiting for the Brief to check the plan"},
		{name: "an accepted plan merges", facts: TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3,
			PlanChecked: true}, want: ActionPlanAccepted, message: "The Brief accepted the plan"},
		{name: "a refused plan is a failed round", facts: TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3,
			PlanChecked: true, PlanRefusals: "Container/web: type is missing"},
			want: ActionRetry, feedback: "The Brief refused the plan:\nContainer/web: type is missing",
			message: "starting round 2"},
		{name: "a refusal in the last round fails", facts: TaskFacts{Phase: TaskChecking, Round: 3, MaxRounds: 3,
			PlanChecked: true, PlanRefusals: "Container/web: type is missing"},
			want: ActionFail, message: "Failed after 3 rounds"},
		{name: "merged is final", facts: TaskFacts{Phase: TaskMerged}, want: ActionNone},
		{name: "failed is final", facts: TaskFacts{Phase: TaskFailed}, want: ActionNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.facts.Plan = true
			got := NextTaskStep(tt.facts)
			if got.Action != tt.want {
				t.Fatalf("NextTaskStep() = %+v, want action %s", got, tt.want)
			}
			if !strings.Contains(got.Feedback, tt.feedback) {
				t.Errorf("Feedback = %q, want it to contain %q", got.Feedback, tt.feedback)
			}
			if !strings.Contains(got.Message, tt.message) {
				t.Errorf("Message = %q, want it to contain %q", got.Message, tt.message)
			}
		})
	}
}

func TestNextTaskStepQueuesOnlyForSlots(t *testing.T) {
	tests := []struct {
		name  string
		facts TaskFacts
		want  bool
	}{
		{"would start", TaskFacts{Phase: TaskPending, Protected: true, ContainerReady: true, NoFreeSlot: true}, true},
		{"would review", TaskFacts{Phase: TaskChecking, Round: 1, Checks: "success", NoFreeSlot: true}, true},
		{"blocked by another Task", TaskFacts{Phase: TaskPending, Protected: true, ContainerReady: true,
			BlockedBy: "add-crop", NoFreeSlot: true}, false},
		{"waiting for checks", TaskFacts{Phase: TaskChecking, Round: 1, NoFreeSlot: true}, false},
		{"free slot", TaskFacts{Phase: TaskPending, Protected: true, ContainerReady: true}, false},
	}
	for _, tt := range tests {
		if got := NextTaskStep(tt.facts).SlotWait; got != tt.want {
			t.Errorf("%s: SlotWait = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestBlockingTask(t *testing.T) {
	tasks := []TaskRef{
		{Name: "add-share", Created: 3, Active: true},
		{Name: "add-crop", Created: 1, Active: false},
		{Name: "add-rotate", Created: 2, Active: true},
		{Name: "add-filters", Created: 2, Active: true},
	}
	tests := []struct{ task, want string }{
		{"add-crop", ""},              // finished tasks block nothing and aren't blocked
		{"add-filters", ""},           // earliest active, by creation then name
		{"add-rotate", "add-filters"}, // same second, later name
		{"add-share", "add-filters"},
	}
	for _, tt := range tests {
		if got := BlockingTask(tt.task, tasks); got != tt.want {
			t.Errorf("BlockingTask(%s) = %q, want %q", tt.task, got, tt.want)
		}
	}
}

func TestAgentUsername(t *testing.T) {
	if got := AgentUsername("apps", "photo-editor"); got != "apps-photo-editor-agent" {
		t.Errorf("AgentUsername() = %q", got)
	}
	long := AgentUsername("a-very-long-project-name", "an-even-longer-container-name")
	if len(long) > 40 || !strings.HasSuffix(long, "-agent") {
		t.Errorf("AgentUsername() = %q (%d chars), want at most 40 ending in -agent", long, len(long))
	}
	if long == AgentUsername("a-very-long-project-name", "an-even-longer-container-other") {
		t.Error("two long names map to the same user")
	}
}

func TestSandboxName(t *testing.T) {
	if got := SandboxName("add-crop", 2, RoleReviewer, 1, 0); got != "add-crop-r2-review-a1" {
		t.Errorf("SandboxName() = %q", got)
	}
	if got := SandboxName("add-crop", 1, RoleImplementer, 1, 2); got != "add-crop-r1-implement-x2" {
		t.Errorf("SandboxName() of a rerun = %q", got)
	}
	long := SandboxName(strings.Repeat("x", 80), 10, RoleImplementer, 0, 2)
	if len(long) > 63 {
		t.Errorf("SandboxName() = %q is longer than 63", long)
	}
}

func TestHasSlotTurn(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)
	waiting := []SlotWaiter{
		{Name: "apps/bg-eraser", Since: t0},
		{Name: "apps/doc-scanner", Since: t0.Add(time.Minute)},
	}
	tests := []struct {
		name string
		task string
		free int
		want bool
	}{
		{"the longest waiter goes first", "apps/bg-eraser", 1, true},
		{"later waiters wait", "apps/doc-scanner", 1, false},
		{"a newcomer joins at the back", "apps/audio-cutter", 1, false},
		{"two slots take two waiters", "apps/doc-scanner", 2, true},
		{"no free slot, no turn", "apps/bg-eraser", 0, false},
		{"an empty queue lets a newcomer start", "apps/audio-cutter", 3, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasSlotTurn(tt.task, t0.Add(time.Hour), waiting, tt.free); got != tt.want {
				t.Errorf("HasSlotTurn(%s, free %d) = %v, want %v", tt.task, tt.free, got, tt.want)
			}
		})
	}
}

func TestNoFreeSlot(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)
	waiting := []SlotWaiter{{Name: "apps/bg-eraser", Since: t0}}
	tests := []struct {
		name                    string
		task                    string
		maxSandboxes, sandboxes int
		want                    bool
	}{
		{"a free slot and no queue", "apps/bg-eraser", 2, 1, false},
		{"every slot taken", "apps/bg-eraser", 2, 2, true},
		{"more sandboxes than slots", "apps/bg-eraser", 2, 3, true},
		{"the one free slot goes to the longer waiter", "apps/doc-scanner", 2, 1, true},
		{"two free slots serve the newcomer too", "apps/doc-scanner", 3, 1, false},
		{"no slots configured", "apps/bg-eraser", 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NoFreeSlot(tt.task, t0.Add(time.Hour), waiting, tt.maxSandboxes, tt.sandboxes)
			if got != tt.want {
				t.Errorf("NoFreeSlot(%s, max %d, running %d) = %v, want %v",
					tt.task, tt.maxSandboxes, tt.sandboxes, got, tt.want)
			}
		})
	}
}

func TestCheckFailures(t *testing.T) {
	stamp := func(lines ...string) string {
		for i, l := range lines {
			lines[i] = "2026-10-01T13:40:01.1234567Z " + l
		}
		return strings.Join(lines, "\n")
	}
	tests := []struct {
		name           string
		log            string
		want, unwanted []string
	}{
		{
			name: "go test",
			log: stamp(
				"go test -race -count=1 ./...",
				"ok  \texample.local/app/internal/check\t1.240s",
				"=== RUN   TestLargeFileLatency",
				"--- FAIL: TestLargeFileLatency (1.17s)",
				"    server_test.go:385: diagnostics arrived 520.021451ms after the change, want within 500ms",
				"=== RUN   TestTable",
				"    --- FAIL: TestTable/empty (0.00s)",
				"        table_test.go:40: got 1, want 0",
				"FAIL",
				"FAIL\texample.local/app/internal/server\t2.702s",
				"panic: runtime error: index out of range [3] with length 3",
				"",
				"goroutine 7 [running]:",
				"example.local/app/internal/x.f(...)",
				"WARNING: DATA RACE",
				"Write at 0x00c000126000 by goroutine 8:",
				"make: *** [Makefile:41: test] Error 1",
			),
			want: []string{"--- FAIL: TestLargeFileLatency (1.17s)", "server_test.go:385: diagnostics arrived 520",
				"--- FAIL: TestTable/empty", "table_test.go:40: got 1, want 0", "FAIL\texample.local/app/internal/server",
				"panic: runtime error: index out of range", "goroutine 7 [running]:", "WARNING: DATA RACE",
				"Write at 0x00c000126000 by goroutine 8:", "make: *** [Makefile:41: test]"},
			unwanted: []string{"internal/check", "=== RUN", "go test -race"},
		},
		{
			name: "go build, vet and lint",
			log: stamp(
				"go vet ./...",
				"# example.local/app/internal/server",
				"internal/server/server.go:12:3: undefined: foo",
				"golangci-lint run",
				"internal/check/check.go:30:9: Error return value of `f.Close` is not checked (errcheck)",
				"\tf.Close()",
				"1 issues:",
			),
			want: []string{"internal/server/server.go:12:3: undefined: foo",
				"internal/check/check.go:30:9: Error return value"},
			unwanted: []string{"go vet ./...", "golangci-lint run"},
		},
		{
			name: "vitest, tsc and eslint",
			log: stamp(
				" ✓ src/ok.test.ts (3 tests) 4ms",
				" × src/server.test.ts > findServer > prefers the setting 3ms",
				" FAIL  src/server.test.ts > findServer > prefers the setting",
				"AssertionError: expected '/usr/bin/app' to be '/opt/app' // Object.is equality",
				" ❯ src/server.test.ts:42:17",
				"⎯⎯⎯⎯⎯⎯⎯[1/1]⎯",
				" Test Files  1 failed | 1 passed (2)",
				"src/extension.ts(10,7): error TS2322: Type 'string' is not assignable to type 'number'.",
				"/workspace/src/extension.ts",
				"  3:7  error  'x' is assigned a value but never used  @typescript-eslint/no-unused-vars",
				"✖ 1 problem (1 error, 0 warnings)",
			),
			want: []string{"× src/server.test.ts > findServer > prefers the setting",
				"FAIL  src/server.test.ts > findServer > prefers the setting", "AssertionError: expected '/usr/bin/app'",
				"❯ src/server.test.ts:42:17", "Test Files  1 failed", "error TS2322", "3:7  error  'x' is assigned",
				"✖ 1 problem"},
			unwanted: []string{"src/ok.test.ts"},
		},
		{
			name: "pytest",
			log: stamp(
				"tests/test_ok.py::test_ok PASSED",
				"E       assert 1 == 2",
				"FAILED tests/test_api.py::test_create - assert 1 == 2",
			),
			want:     []string{"E       assert 1 == 2", "FAILED tests/test_api.py::test_create"},
			unwanted: []string{"test_ok"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckFailures(tt.log)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("CheckFailures() lacks %q:\n%s", want, got)
				}
			}
			for _, unwanted := range tt.unwanted {
				if strings.Contains(got, unwanted) {
					t.Errorf("CheckFailures() has %q:\n%s", unwanted, got)
				}
			}
		})
	}
	if long := CheckFailures(strings.Repeat("Error: x\n", 2000)); len(long) > maxCheckExcerpt+10 {
		t.Errorf("CheckFailures() of a long log = %d bytes", len(long))
	}
}

func TestNextTaskStepInterrupted(t *testing.T) {
	killed := RunState{Started: true, Done: true, Interrupted: true,
		Result: &RunResult{Error: "claude failed: signal: killed"}}
	evicted := RunState{Started: true, Done: true, Interrupted: true, Failure: "Evicted"}
	oom := RunState{Started: true, Done: true, Failure: "OOMKilled (exit code 137)"}
	tests := []struct {
		name    string
		facts   TaskFacts
		want    TaskAction
		message string
	}{
		{"a killed implementer reruns its round", TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: killed}, ActionRerun,
			"The run was interrupted (claude failed: signal: killed); round 1 runs again (rerun 1 of 2)"},
		{"even in the last round", TaskFacts{Phase: TaskImplementing, Round: 3, MaxRounds: 3, Reruns: 1,
			Run: evicted}, ActionRerun, "(Evicted); round 3 runs again (rerun 2 of 2)"},
		{"an interrupted reviewer reruns too", TaskFacts{Phase: TaskReviewing, Round: 2, MaxRounds: 3,
			Run: evicted}, ActionRerun, "round 2 runs again"},
		{"a run whose Git host stayed unreachable reruns its round", TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, Run: RunState{Started: true, Done: true, Interrupted: true, Result: &RunResult{
				Error: "the Git host stayed unreachable: git push: unable to access 'http://gitea/x/'"}}},
			ActionRerun, "(the Git host stayed unreachable"},
		{"past the cap the round is spent", TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3, Reruns: 2,
			Run: killed}, ActionRetry, "claude failed: signal: killed"},
		{"past the cap the reviewer runs again", TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3, Reruns: 2,
			Run: evicted}, ActionRetryReview, "Evicted"},
		{"an out-of-memory kill is the agent's", TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: oom}, ActionRetry, "OOMKilled"},
		{"a pushed commit stands though the node then shut down", TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, Run: RunState{Started: true, Done: true, Interrupted: true,
				Result: &RunResult{Commit: "abc1234"}}}, ActionOpenPR, ""},
		{"so does a verdict", TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3, Run: RunState{Started: true,
			Done: true, Interrupted: true, Result: &RunResult{Verdict: VerdictApprove}}}, ActionMerge, ""},
		{"an unavailable agent waits", TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: RunState{Started: true, Done: true, Interrupted: true,
				Result: &RunResult{Error: "usage limit reached", Unavailable: true}}}, ActionAgentUnavailable, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextTaskStep(tt.facts)
			if got.Action != tt.want || !strings.Contains(got.Message, tt.message) {
				t.Errorf("NextTaskStep() = %+v, want %s with %q", got, tt.want, tt.message)
			}
		})
	}
}

func TestMissedShutdown(t *testing.T) {
	shutdown := time.Date(2026, 10, 3, 9, 58, 49, 0, time.UTC)
	tests := []struct {
		name    string
		noticed time.Time
		want    bool
	}{
		{"noticed at the shutdown time", shutdown, false},
		{"noticed a little late", shutdown.Add(ShutdownSlack), false},
		{"noticed after the cluster came back", shutdown.Add(7 * time.Hour), true},
		{"noticed just past the slack", shutdown.Add(ShutdownSlack + time.Second), true},
		{"the condition changed before the shutdown time", shutdown.Add(-10 * time.Minute), false},
		{"no time noticed", time.Time{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MissedShutdown(shutdown, tt.noticed); got != tt.want {
				t.Errorf("MissedShutdown(%v, %v) = %v, want %v", shutdown, tt.noticed, got, tt.want)
			}
		})
	}
	if MissedShutdown(time.Time{}, shutdown) {
		t.Error("MissedShutdown() without a shutdown time = true, want false")
	}
}

func TestNextTaskStepAgentUnavailable(t *testing.T) {
	back := time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC)
	unavailable := RunState{Started: true, Done: true, Result: &RunResult{Unavailable: true, RetryAt: back,
		Error: "usage limit reached"}}
	tests := []struct {
		name    string
		facts   TaskFacts
		want    TaskAction
		message string
	}{
		{"an implementer without its model keeps the round", TaskFacts{Phase: TaskImplementing, Round: 3, MaxRounds: 3,
			Run: unavailable}, ActionAgentUnavailable, "The agent is unavailable (usage limit reached); round 3 waits for it"},
		{"so does a reviewer", TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3, Run: unavailable},
			ActionAgentUnavailable, "round 1 waits"},
		{"a server error from the model keeps the round", TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: RunState{Started: true, Done: true, Result: &RunResult{Unavailable: true,
				Error: "claude ended with success: API Error: 500 Internal server error"}}},
			ActionAgentUnavailable, "The agent is unavailable (claude ended with success: API Error: 500 Internal " +
				"server error); round 1 waits for it"},
		{"waits before starting again", TaskFacts{Phase: TaskImplementing, Round: 2, MaxRounds: 3,
			AgentBackIn: 10 * time.Minute}, ActionWait, "The agent is unavailable; trying again in 10m0s"},
		{"starts again when the time has come", TaskFacts{Phase: TaskReviewing, Round: 2, MaxRounds: 3},
			ActionStartReview, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextTaskStep(tt.facts)
			if got.Action != tt.want || !strings.Contains(got.Message, tt.message) {
				t.Errorf("NextTaskStep() = %+v, want %s with %q", got, tt.want, tt.message)
			}
			if got.Action == ActionAgentUnavailable && !got.RetryAt.Equal(tt.facts.Run.Result.RetryAt) {
				t.Errorf("RetryAt = %v, want %v", got.RetryAt, tt.facts.Run.Result.RetryAt)
			}
		})
	}
	// An unavailable agent doesn't queue for a slot it can't use.
	if got := NextTaskStep(TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3, AgentBackIn: time.Minute,
		NoFreeSlot: true}); got.SlotWait {
		t.Errorf("NextTaskStep() = %+v, want no slot wait", got)
	}
}

func TestNextTaskStepConflicts(t *testing.T) {
	got := ConflictStep(3)
	if got.Action != ActionResolveConflicts || !strings.Contains(got.Feedback, "Merge main into your branch") ||
		!strings.Contains(got.Message, "round 3 resolves them") {
		t.Errorf("ConflictStep() = %+v, want the implementer to resolve the conflicts in the same round", got)
	}
	approved := RunState{Started: true, Done: true, Result: &RunResult{Verdict: VerdictApprove, Summary: "Good."}}
	if got := NextTaskStep(TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3, Run: approved}); got.Action != ActionMerge {
		t.Errorf("NextTaskStep() = %+v, want a merge without conflicts", got)
	}
	// The Git host sends no event once it found the pull request merges.
	if got := NextTaskStep(TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3, Run: approved,
		Unmergeable: true}); !got.Recheck {
		t.Errorf("NextTaskStep() = %+v, want a wait that looks again", got)
	}
}

// TestNextTaskStepBehindMain: a change merges only on checks that ran against
// the main it merges into. One approved while main moved takes in main and is
// checked again, in the same round and with no new review.
func TestNextTaskStepBehindMain(t *testing.T) {
	approved := RunState{Started: true, Done: true, Result: &RunResult{Verdict: VerdictApprove, Summary: "Good."}}
	tests := []struct {
		name    string
		facts   TaskFacts
		want    TaskAction
		message string
	}{
		{name: "an approved change whose checks ran on an older main takes in main",
			facts: TaskFacts{Phase: TaskReviewing, Round: 3, MaxRounds: 3, Run: approved, Behind: true},
			want:  ActionUpdateBranch, message: "checking it again in round 3"},
		{name: "an unmergeable change behind main takes in main: the update tells a conflict",
			facts: TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3, Run: approved, Behind: true, Unmergeable: true},
			want:  ActionUpdateBranch, message: "checking it again in round 1"},
		{name: "a change that has main's head waits while the Git host tests whether it merges",
			facts: TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3, Run: approved, Unmergeable: true},
			want:  ActionWait, message: "waiting for the Git host"},
		{name: "the updated branch waits while the Git host tests whether it merges, then merges",
			facts: TaskFacts{Phase: TaskChecking, Round: 3, MaxRounds: 3, MergeWhenChecksPass: true, Checks: "success",
				Unmergeable: true},
			want: ActionWait, message: "waiting for the Git host"},
		{name: "an approved change up to date with main merges",
			facts: TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3, Run: approved},
			want:  ActionMerge, message: "Approved; merging"},
		{name: "the updated branch waits for its checks",
			facts: TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3, MergeWhenChecksPass: true},
			want:  ActionWait, message: "Waiting for checks"},
		{name: "the updated branch merges once its checks pass, with no new review",
			facts: TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3, MergeWhenChecksPass: true, Checks: "success"},
			want:  ActionMerge, message: "with main merged in; merging"},
		{name: "main moved again while the checks ran: take it in again",
			facts: TaskFacts{Phase: TaskChecking, Round: 2, MaxRounds: 3, MergeWhenChecksPass: true, Checks: "success",
				Behind: true},
			want: ActionUpdateBranch, message: "in round 2"},
		{name: "main moved again, maybe with a conflict: the update tells",
			facts: TaskFacts{Phase: TaskChecking, Round: 2, MaxRounds: 3, MergeWhenChecksPass: true, Checks: "success",
				Behind: true, Unmergeable: true},
			want: ActionUpdateBranch, message: "in round 2"},
		{name: "failing checks with main merged in start the next round",
			facts: TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3, MergeWhenChecksPass: true, Checks: "failure",
				ChecksURL: "http://ci/1"},
			want: ActionRetry, message: "starting round 2"},
		{name: "a change that isn't approved yet is reviewed once its checks pass",
			facts: TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3, Checks: "success", Behind: true},
			want:  ActionStartReview, message: "starting the review"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextTaskStep(tt.facts)
			if got.Action != tt.want || !strings.Contains(got.Message, tt.message) {
				t.Errorf("NextTaskStep() = %+v, want %s with %q", got, tt.want, tt.message)
			}
		})
	}
	got := NextTaskStep(TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3, MergeWhenChecksPass: true,
		Checks: "failure", ChecksURL: "http://ci/1"})
	if !strings.Contains(got.Feedback, "main moved since its checks ran") || !strings.Contains(got.Feedback, "http://ci/1") {
		t.Errorf("Feedback = %q, want it to say main was merged in, with the checks' link", got.Feedback)
	}
}

func TestRunnerOutOfSpaceLog(t *testing.T) {
	for _, tc := range []struct {
		name string
		log  string
		want bool
	}{
		{name: "the Go compiler", want: true,
			log: "compile: writing output: write $WORK/b001/_pkg_.a: no space left on device\n"},
		{name: "the go command, behind the runner's timestamps", want: true,
			log: "2026-10-10T08:00:00.0000000Z go: creating work dir: mkdir /tmp/go-build1: no space left on device\n"},
		{name: "npm", want: true, log: "npm error code ENOSPC\nENOSPC: No space left on device, write\n"},
		{name: "the shell", want: true, log: "cp: error writing 'dist/app': No space left on device\n"},
		{name: "the toolchain after a Go test's message", want: true, log: "--- FAIL: TestDisk (0.00s)\n" +
			"    disk_test.go:9: got nil\nFAIL\nlink: write /tmp/go-link/a.out: no space left on device\n"},
		{name: "the toolchain after pytest's reports", want: true, log: "___ test_disk ___\n" +
			"E   assert 'no space left on device' == ''\n=== 1 failed in 0.1s ===\n" +
			"OSError: [Errno 28] No space left on device: '/root/.cache/pip'\n"},
		{name: "a passing log", log: "ok  \texample.com/app\t0.01s\n"},
		{name: "a Go test's message",
			log: "--- FAIL: TestDiskFull (0.00s)\n    disk_test.go:12: Save() = write /tmp/x: no space left on device, want ErrFull\nFAIL\n"},
		{name: "a Go subtest's message over lines", log: "--- FAIL: TestSave (0.00s)\n    --- FAIL: TestSave/full (0.00s)\n" +
			"        save_test.go:30: \n            \tError Trace:\tsave_test.go:30\n" +
			"            \tError:      \tNot equal:\n            \t            \texpected: \"no space left on device\"\n" +
			"\nFAIL\texample.com/app\t0.01s\n"},
		{name: "a linter's finding", log: "store/disk.go:14:9: string `no space left on device` has 3 occurrences (goconst)\n"},
		{name: "a pytest report and summary", log: "_________________ test_disk_full _________________\n" +
			"    def test_disk_full():\n>       assert save() == 'no space left on device'\n" +
			"E       AssertionError: assert 'ok' == 'no space left on device'\n" +
			"------ Captured stdout call ------\nno space left on device\n" +
			"=========== short test summary info ===========\n" +
			"FAILED tests/test_disk.py::test_disk_full - AssertionError: assert 'ok' == 'no space left on device'\n"},
		{name: "a Vitest report", log: "⎯⎯⎯⎯⎯⎯⎯ Failed Tests 1 ⎯⎯⎯⎯⎯⎯⎯\n\n FAIL  src/disk.test.ts > save > reports a full disk\n" +
			"AssertionError: expected 'ok' to be 'ENOSPC: no space left on device'\n" +
			"  5|   expect(save()).toBe('ENOSPC: no space left on device')\n\n Test Files  1 failed (1)\n"},
		{name: "a Vitest test's output", log: "stdout | src/disk.test.ts > save > reports a full disk\n" +
			"ENOSPC: no space left on device, write 'out.txt'\n\n ❯ src/disk.test.ts (1 test | 1 failed)\n"},
	} {
		if got := RunnerOutOfSpace(tc.log); got != tc.want {
			t.Errorf("%s: RunnerOutOfSpace() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRunnerOutOfSpace(t *testing.T) {
	got := NextTaskStep(TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3, Checks: "failure",
		ChecksLog: "--- FAIL: TestDiskFull (0.00s)\n    disk_test.go:12: Save() = no space left on device, want ErrFull\nFAIL\n"})
	if got.Action != ActionRetry || got.Reason == ReasonRunnerOutOfSpace ||
		!strings.Contains(got.Feedback, "disk_test.go:12: Save() = no space left on device") {
		t.Errorf("a test's own output: NextTaskStep() = %+v, want ActionRetry with the failure as feedback", got)
	}
	for _, merge := range []bool{false, true} {
		got := NextTaskStep(TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3, MergeWhenChecksPass: merge,
			Checks: "error", ChecksLog: "npm error code ENOSPC\nnpm error syscall write\nENOSPC: No space left on device\n"})
		if got.Action != ActionFail || got.Reason != ReasonRunnerOutOfSpace {
			t.Errorf("merge %v: NextTaskStep() = %+v, want ActionFail, reason %s", merge, got, ReasonRunnerOutOfSpace)
		}
	}
	for reason, want := range map[string]bool{ReasonNoCIResult: true, ReasonRunnerOutOfSpace: true,
		ReasonApprovalRanOut: true, "RoundsExhausted": false, "": false} {
		if got := RunnerFault(reason); got != want {
			t.Errorf("RunnerFault(%q) = %v, want %v", reason, got, want)
		}
	}
}

// TestRanOutApprovalsSpendNoReviewAttempt walks a round whose approval runs
// out twice: its reviewer still gets maxReviewAttempts runs without a verdict,
// each in a sandbox of its own, and review-again has a bound of its own.
func TestRanOutApprovalsSpendNoReviewAttempt(t *testing.T) {
	p := TaskProgress{Phase: TaskReviewing, Round: 1, Commit: "abc", HasChecks: true}
	facts := func(run RunState) TaskFacts {
		return TaskFacts{Phase: p.Phase, Round: p.Round, MaxRounds: 3, ReviewAttempts: p.ReviewAttempts,
			ReviewAgains: p.ReviewAgains, MergeWaitsForChecks: true, Run: run}
	}
	sandbox := func() string { return SandboxName("t", p.Round, RoleReviewer, p.ReviewAttempts+p.ReviewAgains+1, 0) }
	seen := map[string]bool{sandbox(): true}
	ended := RunState{Started: true, Done: true, Failure: "the sandbox reached its shutdown time"}
	for i := range 2 {
		if got := NextTaskStep(facts(ended)); got.Action != ActionReviewAgain {
			t.Fatalf("approval %d ran out: NextTaskStep() = %+v, want %s", i+1, got, ActionReviewAgain)
		}
		p = ReviewAgain(p)
		p.Phase, p.HasChecks = TaskReviewing, true
		if seen[sandbox()] {
			t.Fatalf("review-again %d reuses sandbox %s", i+1, sandbox())
		}
		seen[sandbox()] = true
	}
	noVerdict := RunState{Started: true, Done: true, Result: &RunResult{Error: "the reviewer gave no JSON verdict"}}
	for i := range maxReviewAttempts - 1 {
		f := facts(noVerdict)
		f.MergeWaitsForChecks = false
		if got := NextTaskStep(f); got.Action != ActionRetryReview {
			t.Fatalf("no-verdict run %d: NextTaskStep() = %+v, want %s", i+1, got, ActionRetryReview)
		}
		p = RetryReview(p)
		if seen[sandbox()] {
			t.Fatalf("retry %d reuses sandbox %s", i+1, sandbox())
		}
		seen[sandbox()] = true
	}
	f := facts(noVerdict)
	f.MergeWaitsForChecks = false
	if got := NextTaskStep(f); got.Action != ActionFail || !strings.Contains(got.Message, "failed 3 times") {
		t.Errorf("third no-verdict run: NextTaskStep() = %+v, want the reviewer failed 3 times", got)
	}

	p.ReviewAgains = maxReviewAgains
	got := NextTaskStep(facts(ended))
	if got.Action != ActionFail || got.Reason != ReasonApprovalRanOut {
		t.Errorf("past the bound: NextTaskStep() = %+v, want ActionFail, reason %s", got, ReasonApprovalRanOut)
	}
}

func TestNextTaskStepBlockedSlotWait(t *testing.T) {
	got := NextTaskStep(TaskFacts{Phase: TaskBlocked, NoFreeSlot: true, Change: ChangeFacts{Issue: 31}})
	if got.SlotWait {
		t.Errorf("a blocked Task must not queue for a slot: %+v", got)
	}
	got = NextTaskStep(TaskFacts{Phase: TaskBlocked, NoFreeSlot: true, AgentBackIn: time.Hour,
		Change: ChangeFacts{Issue: 31, Merged: true}})
	if got.SlotWait || got.Action != ActionResume {
		t.Errorf("a resume is not a start: %+v", got)
	}
}

func TestResumeFeedbackInStep(t *testing.T) {
	got := NextTaskStep(TaskFacts{Phase: TaskBlocked, Round: 3, MaxRounds: 3, Feedback: "- fix x",
		Change: ChangeFacts{Issue: 31, Paths: []string{"Makefile"}, Merged: true}})
	for _, want := range []string{"#31", "Makefile", "- fix x"} {
		if !strings.Contains(got.Feedback, want) {
			t.Errorf("Feedback = %q, want it to contain %q", got.Feedback, want)
		}
	}
}

func TestStandardChangeOutcome(t *testing.T) {
	tests := []struct {
		name       string
		tasks      []ChangeTask
		closed     bool
		wantMerged bool
		wantFailed string
		wantBlock  bool
	}{
		{name: "no tasks, open"},
		// The gated merge closes the issue in the reconcile that sets Merged, so the
		// issue can read closed before any Task says Merged: wait, don't fail.
		{name: "closed while the Task is still active", tasks: []ChangeTask{{Name: "x-31"}}, closed: true},
		{name: "merged", tasks: []ChangeTask{{Name: "x-31", Final: true}}, closed: true, wantMerged: true},
		{name: "failed", tasks: []ChangeTask{{Name: "x-31", Final: true, Failed: true, Message: "Failed after 3 rounds"}},
			wantFailed: "Task x-31 failed: Failed after 3 rounds"},
		{name: "failed blocked", tasks: []ChangeTask{{Name: "x-31", Final: true, Failed: true, Blocked: true, Message: "m"}},
			wantFailed: "Task x-31 failed: m", wantBlock: true},
		{name: "failed, then retried", tasks: []ChangeTask{{Name: "x-31", Final: true, Failed: true, Message: "m"},
			{Name: "x-31-2"}}},
		{name: "failed, then merged", tasks: []ChangeTask{{Name: "x-31", Final: true, Failed: true, Message: "m"},
			{Name: "x-31-2", Final: true}}, wantMerged: true},
		{name: "closed without any Task", closed: true, wantFailed: "issue #31 was closed without a merge"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged, failed, blocked := StandardChangeOutcome(31, tt.tasks, tt.closed)
			if merged != tt.wantMerged || failed != tt.wantFailed || blocked != tt.wantBlock {
				t.Errorf("StandardChangeOutcome() = %v, %q, %v; want %v, %q, %v", merged, failed, blocked,
					tt.wantMerged, tt.wantFailed, tt.wantBlock)
			}
		})
	}
}

func TestResumeFeedback(t *testing.T) {
	got := ResumeFeedback(31, []string{"Makefile"}, "")
	for _, want := range []string{"#31", "Makefile", "merged into main", "takes another Standard change"} {
		if !strings.Contains(got, want) {
			t.Errorf("ResumeFeedback() = %q, want it to contain %q", got, want)
		}
	}
	// A second Standard change is allowed: the feedback must not forbid one.
	if strings.Contains(got, "do not edit") {
		t.Errorf("ResumeFeedback() = %q, forbids editing the files again", got)
	}
	got = ResumeFeedback(31, []string{"Makefile"}, "The reviewer asked for changes:\n- fix x")
	if !strings.Contains(got, "- fix x") {
		t.Errorf("ResumeFeedback() = %q, want the earlier feedback", got)
	}
}

func TestGatedMergeRefusal(t *testing.T) {
	mk := []string{"Makefile"}
	ci := map[string]string{"check / check (pull_request)": "success"}
	tests := []struct {
		name             string
		checks, verdict  string
		contexts         map[string]string
		changed, allowed []string
		want             string // substring; "" means merge
	}{
		{name: "merges", checks: "success", contexts: ci, verdict: VerdictApprove, changed: mk, allowed: mk},
		{name: "a directory allowance", checks: "success", contexts: ci, verdict: VerdictApprove,
			changed: []string{".gitea/workflows/release.yaml"}, allowed: []string{".gitea/"}},
		{name: "a skipped push check", checks: "success", verdict: VerdictApprove, changed: mk, allowed: mk,
			contexts: map[string]string{"check / check (pull_request)": "success", "check / check (push)": "skipped"}},
		{name: "red CI", checks: "failure", contexts: ci, verdict: VerdictApprove, changed: mk, allowed: mk, want: "CI"},
		{name: "check context missing", checks: "success", verdict: VerdictApprove, changed: mk, allowed: mk,
			contexts: map[string]string{"CI / check (pull_request)": "success"}, want: `"check*"`},
		{name: "no check context at all", checks: "success", verdict: VerdictApprove, changed: mk, allowed: mk,
			want: `"check*"`},
		{name: "a required check not passed", checks: "success", verdict: VerdictApprove, changed: mk, allowed: mk,
			contexts: map[string]string{"check / check (pull_request)": "success", "check / lint (pull_request)": "pending"},
			want:     `"check*"`},
		{name: "no verdict", checks: "success", contexts: ci, verdict: VerdictChanges, changed: mk, allowed: mk,
			want: "approve"},
		{name: "a protected file outside the allowance", checks: "success", contexts: ci, verdict: VerdictApprove,
			changed: []string{"Makefile", "README.md"}, allowed: mk, want: "README.md"},
		{name: "a protected path in another case", checks: "success", contexts: ci, verdict: VerdictApprove,
			changed: []string{"Makefile", "makefile"}, allowed: mk, want: "makefile"},
		{name: "only unprotected files", checks: "success", contexts: ci, verdict: VerdictApprove,
			changed: []string{"release_test.go"}, allowed: mk, want: "touches none of Makefile"},
		{name: "an unprotected file beside the allowance", checks: "success", contexts: ci, verdict: VerdictApprove,
			changed: []string{"Makefile", "release_test.go"}, allowed: mk},
		{name: "empty change", checks: "success", contexts: ci, verdict: VerdictApprove, allowed: mk,
			want: "changes nothing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GatedMergeRefusal(tt.checks, tt.contexts, tt.verdict, tt.changed, tt.allowed,
				AgentProtectedPaths([]string{"Makefile", "README.md"}))
			if tt.want == "" && got != "" || !strings.Contains(got, tt.want) {
				t.Errorf("GatedMergeRefusal() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFollowUpIssue(t *testing.T) {
	tests := []struct {
		name      string
		followUp  FollowUp
		want      []string
		readyAsIs bool
	}{
		{name: "criteria become the checklist", followUp: FollowUp{Title: "Cache schemas", Body: "Parse once.",
			Criteria: []string{"Each schema is parsed once", "A test\ncounts  parses"}},
			want: []string{"Parse once.\n\n## Acceptance criteria\n\n- [ ] Each schema is parsed once\n- [ ] A test counts parses\n\n---\n",
				"Suggested by Agent `reviewer` while reviewing http://pr/5 for #3 (Task `api-3`).",
				"check the criteria, move it into a release and label it status/ready"},
			readyAsIs: true},
		{name: "without criteria a person adds them", followUp: FollowUp{Title: "Cache schemas", Body: "Parse once."},
			want: []string{"Parse once.\n\n---\n", `add an "Acceptance criteria" checklist and label it status/ready`}},
		{name: "a code block cut short is closed", followUp: FollowUp{Body: "Like this:\n```go\nfunc f() {…",
			Criteria: []string{"Done"}}, want: []string{"func f() {…\n```\n\n## Acceptance criteria"}, readyAsIs: true},
		{name: "without a body", followUp: FollowUp{Title: "Cache schemas", Criteria: []string{"Parsed once"}},
			want: []string{"## Acceptance criteria\n\n- [ ] Parsed once\n"}, readyAsIs: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FollowUpIssue(tt.followUp, "Agent `reviewer`", "http://pr/5", 3, "api-3")
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("FollowUpIssue() = %q, want it to contain %q", got, w)
				}
			}
			if ready := MissingAcceptanceCriteria(got) == ""; ready != tt.readyAsIs {
				t.Errorf("FollowUpIssue() = %q: ready as it stands = %v, want %v", got, ready, tt.readyAsIs)
			}
		})
	}
}

func TestEndedAtDeadline(t *testing.T) {
	deadline := time.Date(2026, 10, 3, 9, 48, 49, 0, time.UTC)
	tests := []struct {
		name     string
		finished time.Time
		want     bool
	}{
		{"ended at the deadline", deadline, true},
		{"ended after its grace period", deadline.Add(32 * time.Second), true},
		{"ended at the slack", deadline.Add(DeadlineSlack), true},
		{"ended just past the slack", deadline.Add(DeadlineSlack + time.Second), false},
		{"ended when the node came back", deadline.Add(5 * time.Minute), false},
		{"died with its node before the deadline", deadline.Add(-20 * time.Minute), false},
		{"no finish time", time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EndedAtDeadline(deadline, tt.finished); got != tt.want {
				t.Errorf("EndedAtDeadline(%v, %v) = %v, want %v", deadline, tt.finished, got, tt.want)
			}
		})
	}
	if !EndedAtDeadline(time.Time{}, deadline) {
		t.Error("EndedAtDeadline() without a deadline = false, want true")
	}
}

func TestRunStateCost(t *testing.T) {
	tests := []struct {
		name        string
		run         RunState
		milliUSD    int64
		withoutCost int32
	}{
		{"no sandbox", RunState{}, 0, 0},
		{"a reported cost", RunState{Started: true, Done: true,
			Result: &RunResult{CostMilliUSD: 312, CostReported: true}}, 312, 0},
		{"a reported cost of nothing", RunState{Started: true, Done: true,
			Result: &RunResult{CostReported: true}}, 0, 0},
		{"a result without a cost", RunState{Started: true, Done: true, Result: &RunResult{}}, 0, 1},
		{"no result", RunState{Started: true, Done: true, Failure: "OOMKilled"}, 0, 1},
		{"ended while running", RunState{Started: true}, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			milliUSD, withoutCost := tt.run.Cost()
			if milliUSD != tt.milliUSD || withoutCost != tt.withoutCost {
				t.Errorf("Cost() = %d, %d; want %d, %d", milliUSD, withoutCost, tt.milliUSD, tt.withoutCost)
			}
		})
	}
}

func TestEarlierTry(t *testing.T) {
	try1 := Try{Name: "core-139-1", Issue: 139, Created: 10, Failed: true, PullRequest: 150}
	try2 := Try{Name: "core-139-2", Issue: 139, Created: 20, Failed: true, PullRequest: 155}
	tests := []struct {
		name  string
		issue int
		tries []Try
		want  string
	}{
		{"none before", 139, nil, ""},
		{"the newest failed try with a pull request", 139, []Try{try1, try2}, "core-139-2"},
		{"a try without a pull request is passed over", 139,
			[]Try{try1, {Name: "core-139-2", Issue: 139, Created: 20, Failed: true}}, "core-139-1"},
		{"an active try is not continued", 139,
			[]Try{{Name: "core-139-2", Issue: 139, Created: 20, PullRequest: 155}}, ""},
		{"another issue's try is not continued", 139,
			[]Try{{Name: "core-140-1", Issue: 140, Created: 20, Failed: true, PullRequest: 156}}, ""},
		{"a later try is not continued", 139,
			[]Try{{Name: "core-139-4", Issue: 139, Created: 40, Failed: true, PullRequest: 160}}, ""},
		{"the Task itself is not its earlier try", 139,
			[]Try{{Name: "core-139-3", Issue: 139, Created: 30, Failed: true, PullRequest: 157}}, ""},
		{"a written goal continues nothing", 0,
			[]Try{{Name: "core-x", Created: 20, Failed: true, PullRequest: 157}}, ""},
		{"ties go by name", 139, []Try{{Name: "core-139-2b", Issue: 139, Created: 30, Failed: true, PullRequest: 158},
			{Name: "core-139-2a", Issue: 139, Created: 30, Failed: true, PullRequest: 159}}, "core-139-2b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := EarlierTry("core-139-3", 30, tt.issue, tt.tries)
			if ok != (tt.want != "") || got.Name != tt.want {
				t.Errorf("EarlierTry = %q, %v; want %q", got.Name, ok, tt.want)
			}
		})
	}
}

func TestEndedUnreviewed(t *testing.T) {
	step := func(phase string, round int) TryStep { return TryStep{Phase: phase, Round: round} }
	pending, failed := step(TaskPending, 0), step(TaskFailed, 0)
	tests := []struct {
		name    string
		history []TryStep
		want    bool
	}{
		{"implemented, then failed checking with NoCIResult", []TryStep{pending,
			step(TaskImplementing, 1), step(TaskChecking, 1), step(TaskFailed, 1)}, true},
		{"red checks on the last round, which may later re-run green", []TryStep{pending,
			step(TaskImplementing, 1), step(TaskChecking, 1), step(TaskImplementing, 2), step(TaskChecking, 2),
			step(TaskFailed, 2)}, true},
		{"reviewed, then changes requested, then failed checking", []TryStep{pending,
			step(TaskImplementing, 1), step(TaskChecking, 1), step(TaskReviewing, 1),
			step(TaskImplementing, 2), step(TaskChecking, 2), step(TaskFailed, 2)}, false},
		{"approved, then failed checking main merged in", []TryStep{pending,
			step(TaskImplementing, 1), step(TaskChecking, 1), step(TaskReviewing, 1), step(TaskChecking, 1),
			step(TaskFailed, 1)}, false},
		{"changes requested on the last round", []TryStep{pending,
			step(TaskImplementing, 1), step(TaskChecking, 1), step(TaskReviewing, 1), step(TaskFailed, 1)}, false},
		{"failed implementing", []TryStep{pending, step(TaskImplementing, 1), failed}, false},
		{"failed waiting for a Standard change", []TryStep{pending,
			step(TaskImplementing, 1), step(TaskBlocked, 1), failed}, false},
		{"started at review and failed checking", []TryStep{step(TaskChecking, 1), failed}, true},
		{"its start dropped from the timeline", []TryStep{step(TaskImplementing, 2), step(TaskChecking, 2),
			failed}, false},
		{"no timeline", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EndedUnreviewed(tt.history); got != tt.want {
				t.Errorf("EndedUnreviewed(%v) = %v, want %v", tt.history, got, tt.want)
			}
		})
	}
}

func TestStartsAtReview(t *testing.T) {
	green := InheritedWork{Pushed: "abc", Head: "abc", Unreviewed: true, Checks: "success"}
	tests := []struct {
		name string
		edit func(*InheritedWork)
		want bool
	}{
		{"its checks went green", func(*InheritedWork) {}, true},
		{"its checks still pending", func(w *InheritedWork) { w.Checks = "pending" }, false},
		{"its checks failed", func(w *InheritedWork) { w.Checks = "failure" }, false},
		{"someone moved the head", func(w *InheritedWork) { w.Head = "def" }, false},
		{"a reviewer judged it", func(w *InheritedWork) { w.Unreviewed = false }, false},
		{"the earlier try pushed nothing", func(w *InheritedWork) { w.Pushed, w.Head = "", "" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := green
			tt.edit(&w)
			if got := StartsAtReview(w); got != tt.want {
				t.Errorf("StartsAtReview(%+v) = %v, want %v", w, got, tt.want)
			}
		})
	}
}

func TestInheritedChecks(t *testing.T) {
	got := InheritedChecks(TaskProgress{Phase: TaskPending, Feedback: "say how to try it"}, "abc")
	want := TaskProgress{Phase: TaskChecking, Round: 1, Commit: "abc", Feedback: "say how to try it"}
	if got != want {
		t.Errorf("InheritedChecks = %+v, want %+v", got, want)
	}
}
