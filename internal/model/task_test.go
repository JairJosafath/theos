package model

import (
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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
		{name: "waits while main is red (#1892)", facts: TaskFacts{Phase: TaskPending, Protected: true,
			ContainerReady: true, Base: "failure", BaseURL: "https://ci/1", Round: 1, MaxRounds: 3},
			want: ActionWait, message: "Main's checks failed (https://ci/1): round 1 starts only on a green base"},
		{name: "waits while main's checks run (#1892)", facts: TaskFacts{Phase: TaskPending, Protected: true,
			ContainerReady: true, Base: "pending", Round: 1, MaxRounds: 3},
			want: ActionWait, message: "Waiting for main's checks to end before round 1"},
		{name: "starts on a green main (#1892)", facts: TaskFacts{Phase: TaskPending, Protected: true,
			ContainerReady: true, Base: "success", Round: 1, MaxRounds: 3}, want: ActionStartImplement},
		{name: "waits behind an earlier Task before reading main", facts: TaskFacts{Phase: TaskPending, Protected: true,
			ContainerReady: true, BlockedBy: "add-crop", Base: "failure", Round: 1, MaxRounds: 3},
			want: ActionWait, message: "Waiting for Task add-crop"},
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

// A Crew's cost cap (#1893): at the summed cost of its runs reaching the cap
// the Task starts no further run, under it or with no cap it goes on, and a
// raised cap resumes it. The cap is read before the slot and agent gates, so a
// Task at it holds no place in the slot queue.
func TestNextTaskStepOverBudget(t *testing.T) {
	pending := TaskFacts{Phase: TaskPending, Protected: true, ContainerReady: true, Round: 1, MaxRounds: 3}
	reviewing := TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3, Checks: "success"}
	with := func(f TaskFacts, cost, budget int64) TaskFacts {
		f.CostMilliUSD, f.BudgetMilliUSD = cost, budget
		return f
	}
	tests := []struct {
		name    string
		facts   TaskFacts
		want    TaskAction
		reason  string
		message string
	}{
		{name: "stops at the cap before round 1", facts: with(pending, 1000, 1000), want: ActionWait,
			reason: ReasonOverBudget, message: "The Task's runs cost $1.00, at its Crew's cap of $1; raise the Crew's taskBudgetUSD"},
		{name: "stops over the cap", facts: with(pending, 1250, 1000), want: ActionWait, reason: ReasonOverBudget,
			message: "cost $1.25, at its Crew's cap of $1"},
		{name: "starts no review at the cap", facts: with(reviewing, 2000, 2000), want: ActionWait, reason: ReasonOverBudget},
		{name: "goes on under the cap", facts: with(pending, 999, 1000), want: ActionStartImplement},
		{name: "reviews under the cap", facts: with(reviewing, 999, 1000), want: ActionStartReview},
		{name: "goes on with no cap", facts: with(pending, 5000, 0), want: ActionStartImplement},
		{name: "a raised cap resumes it", facts: with(pending, 1000, 2000), want: ActionStartImplement},
		{name: "a raised cap resumes the review", facts: with(reviewing, 2000, 3000), want: ActionStartReview},
		{name: "the cap beats a full house", facts: with(TaskFacts{Phase: TaskPending, Protected: true,
			ContainerReady: true, Round: 1, MaxRounds: 3, NoFreeSlot: true}, 1000, 1000), want: ActionWait,
			reason: ReasonOverBudget},
		{name: "the cap beats an unavailable agent", facts: with(TaskFacts{Phase: TaskPending, Protected: true,
			ContainerReady: true, Round: 1, MaxRounds: 3, AgentBackIn: time.Minute}, 1000, 1000), want: ActionWait,
			reason: ReasonOverBudget},
		{name: "a running round is not stopped", facts: with(TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: RunState{Started: true}}, 1000, 1000), want: ActionWait, reason: ""},
		{name: "a pushed round still opens its pull request", facts: with(TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, Run: RunState{Started: true, Done: true, Result: &RunResult{Commit: "abc"}}}, 1000, 1000),
			want: ActionOpenPR},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.facts.Container = "photo-editor"
			got := NextTaskStep(tt.facts)
			if got.Action != tt.want || got.Reason != tt.reason || !strings.Contains(got.Message, tt.message) {
				t.Fatalf("NextTaskStep() = %+v, want %s reason %q with %q", got, tt.want, tt.reason, tt.message)
			}
			if got.SlotWait {
				t.Errorf("SlotWait = true; a Task at its cap holds no slot place")
			}
		})
	}
}

// A Task on a red main waits under its own reason and looks at main again,
// which sends no event when it turns green (#1892).
func TestNextTaskStepBaseRed(t *testing.T) {
	step := NextTaskStep(TaskFacts{Phase: TaskPending, Protected: true, ContainerReady: true, Base: "error",
		Round: 1, MaxRounds: 3})
	if step.Action != ActionWait || step.Reason != ReasonBaseRed || !step.Recheck {
		t.Errorf("NextTaskStep on a red main = %+v, want a rechecked wait with reason %s", step, ReasonBaseRed)
	}
	if pending := NextTaskStep(TaskFacts{Phase: TaskPending, Protected: true, ContainerReady: true, Base: "pending",
		Round: 1, MaxRounds: 3}); pending.Reason != "" || !pending.Recheck {
		t.Errorf("NextTaskStep on a pending main = %+v, want a rechecked plain wait", pending)
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

// A round whose check fails on main in the workspace too goes on to the pull
// request and CI, saying so (docs/plans/done/workspace-check-differs.md).
func TestImplementedStepCheckFailsOnBase(t *testing.T) {
	got := NextTaskStep(TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3, Run: RunState{Started: true,
		Done: true, Result: &RunResult{Commit: "abcdef1234", CheckFailsOnBase: "curl: (22) 404 Not Found"}}})
	want := "The agent pushed abcdef1; `make check` fails on main in the agent's workspace too, so CI decides"
	if got.Action != ActionOpenPR || got.Message != want {
		t.Errorf("NextTaskStep() = %+v, want %s with %q", got, ActionOpenPR, want)
	}
}

func TestCheckFailsOnBaseNotes(t *testing.T) {
	output := "make: entering\n" + strings.Repeat("é", 300) + "\ncurl: (22) 404 Not Found\n\n"
	event := CheckFailsOnBaseEvent(2, output)
	if want := "Round 2: `make check` fails on main in the agent's workspace too, so CI decides " +
		"(curl: (22) 404 Not Found)"; event != want {
		t.Errorf("CheckFailsOnBaseEvent() = %q, want %q", event, want)
	}
	long := CheckFailsOnBaseEvent(1, strings.Repeat("é", 300))
	if !utf8.ValidString(long) || !strings.HasSuffix(long, "…)") {
		t.Errorf("CheckFailsOnBaseEvent() = %q, want a long last line clipped at a rune", long)
	}
	for _, c := range []struct{ checks, ci string }{
		{"", "CI runs the same check on this head and decides"},
		{ChecksEveryChange, "CI runs the same check on this head and decides"},
		{ChecksBeforeRelease, "CI runs only the light check, `make check-light`, on this head and decides"},
	} {
		comment := CheckFailsOnBaseComment(2, output, c.checks)
		for _, want := range []string{"Round 2: `make check` fails in the agent's workspace on main too",
			c.ci, "````\nmake: entering\n", "404 Not Found\n````\n"} {
			if !strings.Contains(comment, want) {
				t.Errorf("CheckFailsOnBaseComment(checks %q) = %q, want it to contain %q", c.checks, comment, want)
			}
		}
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
	if got := SandboxName("add-crop", 2, 0, RoleReviewer, 1, 0); got != "add-crop-r2-review-a1" {
		t.Errorf("SandboxName() = %q", got)
	}
	if got := SandboxName("add-crop", 1, 0, RoleImplementer, 1, 2); got != "add-crop-r1-implement-x2" {
		t.Errorf("SandboxName() of a rerun = %q", got)
	}
	if got := SandboxName("add-crop", 1, 2, RoleReviewer, 1, 1); got != "add-crop-r1-review-s2-a1-x1" {
		t.Errorf("SandboxName() of a restarted round = %q", got)
	}
	long := SandboxName(strings.Repeat("x", 80), 10, 3, RoleReviewer, 3, 2)
	if len(long) > 63 {
		t.Errorf("SandboxName() = %q is longer than 63", long)
	}
}

// TestRestartedRoundsNameRunsAfresh walks a round that is sent back to resolve
// conflicts and later resumed after a Standard change: every implement and
// review run in it gets a sandbox name no earlier run of the round had, so an
// earlier sandbox still being deleted is never read as the new run's.
func TestRestartedRoundsNameRunsAfresh(t *testing.T) {
	p := StartRun(TaskProgress{}, RoleImplementer)
	seen := map[string]string{}
	run := func(step string) {
		t.Helper()
		role := RoleImplementer
		if p.Phase == TaskReviewing {
			role = RoleReviewer
		}
		name := SandboxName("t", p.Round, p.Restarts, role, p.ReviewAttempts+p.ReviewAgains+1, p.Reruns)
		if earlier, ok := seen[name]; ok {
			t.Fatalf("%s reuses sandbox %s of %s", step, name, earlier)
		}
		seen[name] = step
	}
	review := func() { p = StartRun(OpenedPR(p, "abc"), RoleReviewer) }

	run("the first implement run")
	review()
	run("the first review run")
	p = RetryReview(p)
	run("the review retry")
	p = ResolveConflicts(p, ConflictFeedback)
	run("the conflict's implement run")
	review()
	run("the review after the conflict")
	p = RetryReview(p)
	run("the review retry after the conflict")
	p = Rerun(p)
	run("the rerun after the conflict")
	p = ResumeRound(Proposed(p, "std"), "the change is in main")
	run("the resumed implement run")
	review()
	run("the review after the resume")
	p = ResolveConflicts(p, ConflictFeedback)
	run("the second conflict's implement run")
	review()
	run("the review after the second conflict")
	p = NextRound(p, "again")
	run("round 2's implement run")
	review()
	run("round 2's review run")
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

// Step 11 of docs/plans/self-hosting.md: an interrupted run whose sandbox has
// the checkpoint volume resumes in place, up to MaxResumes times in a round,
// and is rerun past that, as is one whose sandbox was made before checkpoints.
func TestNextTaskStepResumesFromACheckpoint(t *testing.T) {
	killed := RunState{Started: true, Done: true, Interrupted: true, Checkpoint: true,
		Result: &RunResult{Error: "claude failed: signal: killed"}}
	lost := RunState{Started: true, Done: true, Interrupted: true, Checkpoint: true,
		Failure: "the sandbox's pod was deleted before it finished"}
	old := RunState{Started: true, Done: true, Interrupted: true, Failure: "Evicted"}
	tests := []struct {
		name    string
		facts   TaskFacts
		want    TaskAction
		message string
	}{
		{"an interrupted run with the volume resumes without a rerun", TaskFacts{Phase: TaskImplementing,
			Round: 1, MaxRounds: 3, Run: killed}, ActionSuspendRun,
			"The run was interrupted (claude failed: signal: killed); round 1 resumes in place from its checkpoint (resume 1 of 5)"},
		{"so does one whose pod went without a result", TaskFacts{Phase: TaskImplementing, Round: 2,
			MaxRounds: 3, Run: lost}, ActionSuspendRun, "round 2 resumes in place"},
		{"so does a reviewer", TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3, Resumes: 2, Run: lost},
			ActionSuspendRun, "(resume 3 of 5)"},
		{"the fifth resume in a round still resumes", TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Resumes: 4, Run: killed}, ActionSuspendRun, "(resume 5 of 5)"},
		{"the sixth resume in a round falls back to a rerun", TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, Resumes: MaxResumes, Run: killed}, ActionRerun, "round 1 runs again (rerun 1 of 2)"},
		{"past both caps the round is spent", TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Resumes: MaxResumes, Reruns: MaxReruns, Run: killed}, ActionRetry, "claude failed: signal: killed"},
		{"a sandbox made before checkpoints is rerun as before", TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, Run: old}, ActionRerun, "(Evicted); round 1 runs again (rerun 1 of 2)"},
		{"a run that reported its outcome is not resumed", TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, Run: RunState{Started: true, Done: true, Interrupted: true, Checkpoint: true,
				Result: &RunResult{Commit: "abc1234"}}}, ActionOpenPR, ""},
		{"a suspended sandbox waits for its pod to go", TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Resumes: 1, Run: RunState{Started: true, Suspended: true, Checkpoint: true, Pod: "uid-1"}},
			ActionWait, "waits for its pod to go"},
		{"then resumes", TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3, Resumes: 1,
			Run: RunState{Started: true, Suspended: true, Checkpoint: true}}, ActionResumeRun,
			"Resuming the interrupted run of round 1 in place (resume 1 of 5)"},
		{"a suspended review resumes too", TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3, Resumes: 1,
			Run: RunState{Started: true, Suspended: true, Checkpoint: true}}, ActionResumeRun, ""},
		{"a planner resumes too", TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3, Plan: true,
			Run: killed}, ActionSuspendRun, "resumes in place"},
		{"a resume waits at the Crew's cost cap", TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Resumes: 1, BudgetMilliUSD: 5000, CostMilliUSD: 5000,
			Run: RunState{Started: true, Suspended: true, Checkpoint: true}},
			ActionWait, "at its Crew's cap of $5"},
		{"a suspended sandbox whose pod is still there within the bound waits", TaskFacts{Phase: TaskImplementing,
			Round: 1, MaxRounds: 3, Resumes: 1, Run: RunState{Started: true, Suspended: true, Checkpoint: true,
				Pod: "uid-1", SuspendedFor: MaxSuspendWait}}, ActionWait, "waits for its pod to go"},
		// The review of step 11: a suspended sandbox's pod that never goes, or a
		// sandbox that expires while suspended, held the Task forever.
		{"a suspended sandbox whose pod outlasts the bound is rerun", TaskFacts{Phase: TaskImplementing,
			Round: 1, MaxRounds: 3, Resumes: 1, Run: RunState{Started: true, Suspended: true, Checkpoint: true,
				Pod: "uid-1", SuspendedFor: MaxSuspendWait + time.Second}}, ActionRerun,
			"(the suspended run could not resume: its pod did not go within 10m0s of the suspend); round 1 runs again"},
		{"a suspended sandbox that expired is rerun", TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3,
			Resumes: 1, Run: RunState{Started: true, Suspended: true}}, ActionRerun,
			"its sandbox reached its shutdown time); round 1 runs again (rerun 1 of 2)"},
		{"a planner's suspended sandbox that expired is rerun", TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, Plan: true, Resumes: 1, Run: RunState{Started: true, Suspended: true, Pod: "uid-1"}},
			ActionRerun, "runs again"},
		{"past the reruns an expired suspended run spends the round", TaskFacts{Phase: TaskImplementing,
			Round: 1, MaxRounds: 3, Resumes: 1, Reruns: MaxReruns, Run: RunState{Started: true, Suspended: true}},
			ActionRetry, "could not resume"},
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

// Step 12 of docs/plans/self-hosting.md: a pause of the runs (a Crew's or
// Brief's spec.pauseRuns) asks a working agent to pause, suspends the run that
// paused with no round, rerun or resume spent, holds every start and resume,
// and resumes the paused run in place once it ends.
func TestNextTaskStepPausesRuns(t *testing.T) {
	const pause = "Crew linter paused its runs"
	working := RunState{Started: true, Checkpoint: true, Pod: "uid-1"}
	paused := RunState{Started: true, Done: true, Checkpoint: true, Pod: "uid-1",
		Result: &RunResult{Paused: true, PauseReason: pause, CostMilliUSD: 400, CostReported: true}}
	suspended := RunState{Started: true, Suspended: true, Paused: true, Checkpoint: true}
	tests := []struct {
		name    string
		facts   TaskFacts
		want    TaskAction
		state   PauseState
		message string
	}{
		{"a working agent is asked to pause", TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Pause: pause, Run: working}, ActionWait, PausePausing,
			"The agent is working on round 1; it pauses at its next tool boundary (" + pause + ")"},
		{"so is a reviewer", TaskFacts{Phase: TaskReviewing, Round: 2, MaxRounds: 3, Pause: pause, Run: working},
			ActionWait, PausePausing, "The reviewer is reviewing; it pauses at its next tool boundary"},
		{"one without the checkpoint volume runs to its end", TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, Pause: pause, Run: RunState{Started: true, Pod: "uid-1"}}, ActionWait, PausePausing,
			"it runs to its end, as its sandbox has no checkpoint to pause to"},
		{"a paused run suspends with no round, rerun or resume", TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, Pause: pause, Run: paused}, ActionPauseRun, PausePaused,
			"The run of round 1 paused (" + pause + ") and saved its checkpoint"},
		{"even past the resume and rerun caps", TaskFacts{Phase: TaskReviewing, Round: 3, MaxRounds: 3,
			Resumes: MaxResumes, Reruns: MaxReruns, Pause: pause, Run: paused}, ActionPauseRun, PausePaused, ""},
		{"a run that paused as the pause ended suspends too", TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, Run: paused}, ActionPauseRun, PauseNone, "resumes in place when the pause ends"},
		{"a paused planner suspends", TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3, Plan: true,
			Pause: pause, Run: paused}, ActionPauseRun, PausePaused, ""},
		{"a paused run whose sandbox expired runs again", TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, Pause: pause, Run: RunState{Started: true, Done: true,
				Result: &RunResult{Paused: true, PauseReason: pause}}}, ActionRerunPaused, PausePaused,
			"its sandbox has no checkpoint any more; round 1 runs again, with no rerun spent"},
		// #1995: a pause costs no rerun, even when it costs the checkpoint.
		{"with no rerun spent, past the rerun cap too", TaskFacts{Phase: TaskReviewing, Round: 2, MaxRounds: 3,
			Reruns: MaxReruns, Run: RunState{Started: true, Done: true,
				Result: &RunResult{Paused: true, PauseReason: pause}}}, ActionRerunPaused, PauseNone,
			"round 2 runs again, with no rerun spent"},
		{"as does one whose sandbox expired while suspended", TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, Reruns: MaxReruns, Run: RunState{Started: true, Suspended: true, Paused: true}},
			ActionRerunPaused, PauseNone,
			"The paused run can't resume (the suspended run could not resume: its sandbox reached its shutdown " +
				"time); round 1 runs again, with no rerun spent"},
		{"a paused run waits for its pod to go", TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Pause: pause, Run: RunState{Started: true, Suspended: true, Paused: true, Checkpoint: true, Pod: "uid-1"}},
			ActionWait, PausePaused, "The paused run of round 1 waits for its pod to go"},
		{"then stays paused", TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3, Pause: pause,
			Run: suspended}, ActionWait, PausePaused,
			"Paused by the factory (" + pause + "); no agent runs until the pause ends"},
		{"however long the pause lasts", TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3, Pause: pause,
			Run: RunState{Started: true, Suspended: true, Paused: true, Checkpoint: true,
				SuspendedFor: 3 * 24 * time.Hour}}, ActionWait, PausePaused, "Paused by the factory"},
		{"and resumes in place, counting no resume, once it ends", TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, Resumes: MaxResumes, Run: RunState{Started: true, Suspended: true, Paused: true,
				Checkpoint: true, SuspendedFor: 3 * 24 * time.Hour}}, ActionResumeRun, PauseNone,
			"Resuming the paused run of round 1 in place"},
		{"an interrupted run's resume waits for the pause too", TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, Resumes: 1, Pause: pause, Run: RunState{Started: true, Suspended: true, Checkpoint: true,
				SuspendedFor: time.Hour}}, ActionWait, PausePaused, "Paused by the factory"},
		{"a paused resume still waits at the cost cap", TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, BudgetMilliUSD: 1000, CostMilliUSD: 1000, Run: suspended}, ActionWait, PauseNone,
			"at its Crew's cap of $1"},
		{"no round starts while paused", TaskFacts{Phase: TaskPending, Protected: true, ContainerReady: true,
			Base: checkSuccess, Pause: pause}, ActionWait, PausePaused, "Paused by the factory"},
		{"nor a review", TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3, Checks: checkSuccess,
			Pause: pause}, ActionWait, PausePaused, "Paused by the factory"},
		{"nor the next round", TaskFacts{Phase: TaskImplementing, Round: 2, MaxRounds: 3, Pause: pause},
			ActionWait, PausePaused, "Paused by the factory"},
		{"a Task waiting on CI is paused, and its checks go on", TaskFacts{Phase: TaskChecking, Round: 1,
			MaxRounds: 3, Checks: checkPending, CIRun: CIRun{State: CIRunRunning}, Pause: pause}, ActionWait,
			PausePaused, "Waiting for checks"},
		{"a Task that merges while paused merges", TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3,
			Pause: pause, Run: RunState{Started: true, Done: true, Result: &RunResult{Verdict: VerdictApprove}}},
			ActionMerge, PausePaused, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextTaskStep(tt.facts)
			if got.Action != tt.want || got.Pause != tt.state || !strings.Contains(got.Message, tt.message) {
				t.Errorf("NextTaskStep() = %+v, want %s, pause %q, with %q", got, tt.want, tt.state, tt.message)
			}
		})
	}
}

// The pause annotation goes only on the sandbox of a run that can save a
// checkpoint, while the factory pauses the runs.
func TestPauseRequest(t *testing.T) {
	const pause = "Brief cel-lens paused its runs"
	for _, tt := range []struct {
		name  string
		facts TaskFacts
		want  string
	}{
		{"a pause with the volume", TaskFacts{Pause: pause, Run: RunState{Started: true, Checkpoint: true}}, pause},
		{"no pause", TaskFacts{Run: RunState{Started: true, Checkpoint: true}}, ""},
		{"a sandbox without the volume", TaskFacts{Pause: pause, Run: RunState{Started: true}}, ""},
	} {
		if got := PauseRequest(tt.facts); got != tt.want {
			t.Errorf("%s: PauseRequest() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestAfterInterruption(t *testing.T) {
	interrupted := RunState{Started: true, Done: true, Interrupted: true, Failure: "Evicted"}
	withVolume := interrupted
	withVolume.Checkpoint = true
	tests := []struct {
		name            string
		run             RunState
		resumes, reruns int
		want            Interruption
	}{
		{"with the volume: resume", withVolume, 0, 0, InterruptionResume},
		{"a resume costs no rerun", withVolume, 4, 0, InterruptionResume},
		{"the sixth resume reruns", withVolume, 5, 0, InterruptionRerun},
		{"the sixth resume past the reruns: neither", withVolume, 5, 2, InterruptionNone},
		{"without the volume: rerun", interrupted, 0, 0, InterruptionRerun},
		{"without the volume past the reruns: neither", interrupted, 0, 2, InterruptionNone},
		{"not interrupted: neither", RunState{Started: true, Done: true, Checkpoint: true,
			Failure: "OOMKilled"}, 0, 0, InterruptionNone},
		{"a result stands: neither", RunState{Started: true, Done: true, Interrupted: true, Checkpoint: true,
			Result: &RunResult{Verdict: VerdictApprove}}, 0, 0, InterruptionNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AfterInterruption(tt.run, tt.resumes, tt.reruns); got != tt.want {
				t.Errorf("AfterInterruption() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResumeDeadline(t *testing.T) {
	tests := []struct {
		name         string
		timeout, ran time.Duration
		want         time.Duration
	}{
		{"what the timeout leaves", 45 * time.Minute, 9 * time.Minute, 36 * time.Minute},
		{"a run that ran nothing gets it all", 45 * time.Minute, 0, 45 * time.Minute},
		{"at least the floor", 45 * time.Minute, 40 * time.Minute, MinResumeDeadline},
		{"even past the timeout", 45 * time.Minute, 3 * time.Hour, MinResumeDeadline},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResumeDeadline(tt.timeout, tt.ran); got != tt.want {
				t.Errorf("ResumeDeadline(%s, %s) = %s, want %s", tt.timeout, tt.ran, got, tt.want)
			}
		})
	}
	start := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	if got := PodRunTime(start, start.Add(9*time.Minute)); got != 9*time.Minute {
		t.Errorf("PodRunTime() = %s, want 9m", got)
	}
	for _, ended := range []time.Time{{}, start.Add(-time.Minute)} {
		if got := PodRunTime(start, ended); got != 0 {
			t.Errorf("PodRunTime(start, %s) = %s, want 0", ended, got)
		}
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

// Step 13 of docs/plans/self-hosting.md (#1996): an unavailable agent's run
// that saved its checkpoint, in a sandbox that has the volume, waits
// suspended and resumes in place when the agent is back, with no round,
// rerun or resume spent; any other is handled as before.
func TestNextTaskStepSuspendsAnUnavailableRun(t *testing.T) {
	back := time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)
	saved := func(retryAt time.Time) RunState {
		return RunState{Started: true, Done: true, Checkpoint: true, Pod: "uid-1", Result: &RunResult{
			Unavailable: true, RetryAt: retryAt, Error: "usage limit reached", Checkpoint: true,
			CostMilliUSD: 250, CostReported: true}}
	}
	waiting := RunState{Started: true, Suspended: true, AgentWait: true, Checkpoint: true}
	tests := []struct {
		name    string
		facts   TaskFacts
		want    TaskAction
		retryAt time.Time
		message string
	}{
		{"#1996: with the volume and its checkpoint saved, it suspends until RetryAt", TaskFacts{
			Phase: TaskImplementing, Round: 2, MaxRounds: 3, Run: saved(back)}, ActionSuspendForAgent, back,
			"The agent is unavailable (usage limit reached); the run of round 2 saved its checkpoint and " +
				"resumes in place when the agent is back"},
		{"#1996: or the default delay when the service gave no time", TaskFacts{Phase: TaskImplementing,
			Round: 1, MaxRounds: 3, Run: saved(time.Time{})}, ActionSuspendForAgent, time.Time{}, ""},
		{"#1996: past the resume and rerun caps and in the last round too", TaskFacts{Phase: TaskReviewing,
			Round: 3, MaxRounds: 3, Resumes: MaxResumes, Reruns: MaxReruns, Run: saved(back)},
			ActionSuspendForAgent, back, "the run of round 3 saved its checkpoint"},
		{"#1996: a planner's run too", TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3, Plan: true,
			Run: saved(back)}, ActionSuspendForAgent, back, ""},
		{"#1996: without the volume, as before", TaskFacts{Phase: TaskImplementing, Round: 2, MaxRounds: 3,
			Run: func() RunState { r := saved(back); r.Checkpoint = false; return r }()},
			ActionAgentUnavailable, back, "The agent is unavailable (usage limit reached); round 2 waits for it"},
		{"#1996: with no checkpoint saved, as before", TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3,
			Run: func() RunState { r := saved(back); r.Result.Checkpoint = false; return r }()},
			ActionAgentUnavailable, back, "round 1 waits for it"},
		{"#1996: it waits for its pod to go", TaskFacts{Phase: TaskImplementing, Round: 2, MaxRounds: 3,
			AgentBackIn: time.Hour, Run: RunState{Started: true, Suspended: true, AgentWait: true,
				Checkpoint: true, Pod: "uid-1"}}, ActionWait, time.Time{},
			"The waiting run of round 2 waits for its pod to go"},
		{"#1996: then until the agent is back, however long", TaskFacts{Phase: TaskImplementing, Round: 2,
			MaxRounds: 3, AgentBackIn: 5 * time.Hour, Run: RunState{Started: true, Suspended: true,
				AgentWait: true, Checkpoint: true, SuspendedFor: 3 * time.Hour}}, ActionWait, time.Time{},
			"The agent is unavailable; the run of round 2 resumes in place in 5h0m0s"},
		{"#1996: and resumes in place at RetryAt, counting no resume", TaskFacts{Phase: TaskImplementing,
			Round: 2, MaxRounds: 3, Resumes: MaxResumes, Run: waiting}, ActionResumeRun, time.Time{},
			"Resuming the run of round 2 in place: its agent is due back"},
		{"#1996: a reviewer's too", TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3,
			AgentBackIn: -time.Minute, Run: waiting}, ActionResumeRun, time.Time{}, ""},
		{"#1996: a pause holds the resume", TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Pause: "Crew linter paused its runs", Run: waiting}, ActionWait, time.Time{}, "Paused by the factory"},
		{"#1996: so does the cost cap, the cost so far counted", TaskFacts{Phase: TaskImplementing, Round: 1,
			MaxRounds: 3, BudgetMilliUSD: 1000, CostMilliUSD: 1000, Run: waiting}, ActionWait, time.Time{},
			"at its Crew's cap of $1"},
		{"#1996: checkpoint gone on resume: its step runs again, with no rerun spent", TaskFacts{
			Phase: TaskImplementing, Round: 2, MaxRounds: 3, Reruns: MaxReruns, Run: RunState{Started: true,
				Suspended: true, AgentWait: true}}, ActionRerunPaused, time.Time{},
			"The run that waited for its agent can't resume (the suspended run could not resume: its sandbox " +
				"reached its shutdown time); round 2 runs again, with no rerun spent"},
		{"#1996: as does one whose pod did not go", TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3,
			Run: RunState{Started: true, Suspended: true, AgentWait: true, Checkpoint: true, Pod: "uid-1",
				SuspendedFor: MaxSuspendWait + time.Minute}}, ActionRerunPaused, time.Time{},
			"its pod did not go within 10m0s of the suspend"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextTaskStep(tt.facts)
			if got.Action != tt.want || !strings.Contains(got.Message, tt.message) || !got.RetryAt.Equal(tt.retryAt) {
				t.Errorf("NextTaskStep() = %+v, want %s, RetryAt %v, with %q", got, tt.want, tt.retryAt, tt.message)
			}
		})
	}
	// #1996: a run that waits for its agent, or paused, gives up its agent
	// slot, so its resume waits for a free one, as a start does; an
	// interrupted run keeps its own and resumes at once.
	for _, tt := range []struct {
		name string
		run  RunState
		slot bool
	}{
		{"#1996: a run back from its agent wait", waiting, true},
		{"#1996: a run back from a pause", RunState{Started: true, Suspended: true, Paused: true, Checkpoint: true}, true},
		{"#1996: an interrupted run", RunState{Started: true, Suspended: true, Checkpoint: true}, false},
	} {
		got := NextTaskStep(TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3, Run: tt.run, NoFreeSlot: true})
		if tt.slot && (got.Action != ActionWait || !got.SlotWait || got.Message != "Waiting for a free agent slot") {
			t.Errorf("%s: NextTaskStep() = %+v, want a wait for a free slot", tt.name, got)
		}
		if !tt.slot && got.Action != ActionResumeRun {
			t.Errorf("%s: NextTaskStep() = %+v, want it resumed in its own slot", tt.name, got)
		}
	}
	// Its wait for the pod to go looks again; its wait for the agent is the
	// operator's to time.
	if got := NextTaskStep(TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3, AgentBackIn: time.Hour,
		Run: waiting}); got.Recheck || got.SlotWait {
		t.Errorf("NextTaskStep() = %+v, want a plain wait", got)
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
		ReasonRunnerOutOfMemory: true, ReasonRunnerLost: true, ReasonApprovalRanOut: true, "RoundsExhausted": false,
		"": false} {
		if got := RunnerFault(reason); got != want {
			t.Errorf("RunnerFault(%q) = %v, want %v", reason, got, want)
		}
	}
}

func TestRunnerKilledLog(t *testing.T) {
	for _, tc := range []struct {
		name string
		log  string
		want bool
	}{
		{name: "go build", want: true, log: "go build example.com/app: /usr/local/go/pkg/tool/compile: signal: killed\n"},
		{name: "the shell's job report, behind the runner's timestamps", want: true,
			log: "2026-10-10T14:39:00.0000000Z /bin/sh: line 1:  1234 Killed                  go test ./...\n"},
		{name: "a bare Killed", want: true, log: "Killed\n"},
		{name: "make", want: true, log: "make: *** [Makefile:12: test] Error 137\n"},
		{name: "an exit code", want: true, log: "Process completed with exit code 137.\n"},
		{name: "Node", want: true,
			log: "FATAL ERROR: Reached heap limit Allocation failed - JavaScript heap out of memory\n"},
		{name: "the toolchain after a Go test's message", want: true, log: "--- FAIL: TestA (0.00s)\n" +
			"    a_test.go:9: got nil\nFAIL\ngo test: signal: killed\n"},
		{name: "a passing log", log: "ok  \texample.com/app\t0.01s\n"},
		{name: "a failed test", log: "--- FAIL: TestA (0.00s)\n    a_test.go:9: got 1, want 2\nFAIL\nmake: *** [test] Error 1\n"},
		{name: "a Go test's message", log: "--- FAIL: TestRun (0.00s)\n" +
			"    run_test.go:12: Run() = signal: killed, want exit status 0\nFAIL\n"},
		{name: "a pytest report", log: "_________________ test_oom _________________\n" +
			"E       AssertionError: assert 'ok' == 'Killed'\n" +
			"=========== short test summary info ===========\n" +
			"FAILED tests/test_oom.py::test_oom - AssertionError: exit code 137\n"},
		{name: "a word in a sentence", log: "the worker was not Killed by the scheduler\n"},
	} {
		if got := RunnerKilled(tc.log); got != tc.want {
			t.Errorf("%s: RunnerKilled() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestKilledChecksRunAgain walks a round whose checks the machine kills for
// lack of memory: twice its failed jobs run again in the same round, with no
// feedback for the agent, and the third kill fails the Task at no try.
func TestKilledChecksRunAgain(t *testing.T) {
	killed := "go build example.com/app: signal: killed\nFAIL\texample.com/app\t613.2s\n"
	f := TaskFacts{Phase: TaskChecking, Round: 2, MaxRounds: 3, Checks: "failure", ChecksURL: "http://ci/962",
		ChecksLog: killed}
	p := TaskProgress{Phase: TaskChecking, Round: 2, Commit: "296c7ab", HasChecks: true}
	at := time.Date(2026, 10, 10, 14, 40, 0, 0, time.UTC)
	for i := range MaxChecksReruns {
		got := NextTaskStep(f)
		if got.Action != ActionRerunChecks || got.Feedback != "" || !strings.Contains(got.Message, "http://ci/962") {
			t.Fatalf("kill %d: NextTaskStep() = %+v, want ActionRerunChecks with no feedback", i+1, got)
		}
		p = RerunChecks(p, at)
		if p.Phase != TaskChecking || p.Round != 2 || p.ChecksReruns != i+1 || !p.ChecksRerunAt.Equal(at) {
			t.Fatalf("kill %d: RerunChecks() = %+v, want Checking in round 2, %d reruns at %s", i+1, p, i+1, at)
		}
		f.ChecksReruns, f.ChecksRerunAt, f.ChecksRerunEnded = p.ChecksReruns, p.ChecksRerunAt, true
	}
	got := NextTaskStep(f)
	if got.Action != ActionFail || got.Reason != ReasonRunnerOutOfMemory || !RunnerFault(got.Reason) ||
		!strings.Contains(got.Message, "free memory on the runner") {
		t.Fatalf("third kill: NextTaskStep() = %+v, want ActionFail, reason %s", got, ReasonRunnerOutOfMemory)
	}

	next := NextRound(p, "fix it")
	if next.ChecksReruns != 0 || !next.ChecksRerunAt.IsZero() {
		t.Errorf("NextRound() = %+v, want the checks' reruns reset", next)
	}
	if opened := OpenedPR(p, "abc1234"); opened.ChecksReruns != p.ChecksReruns || !opened.ChecksRerunAt.IsZero() {
		t.Errorf("OpenedPR() = %+v, want the reruns kept and the rerun time dropped with the checks", opened)
	}

	running := f
	running.ChecksReruns, running.CIRun = 0, CIRun{Commit: "296c7ab1", State: CIRunRunning,
		Started: time.Date(2026, 10, 10, 14, 30, 0, 0, time.UTC), Running: time.Minute}
	if got := NextTaskStep(running); got.Action != ActionWait {
		t.Errorf("a kill while another run still runs: NextTaskStep() = %+v, want ActionWait", got)
	}
	running.CIRun.Running = RunningChecksLimit + time.Minute
	if got := NextTaskStep(running); got.Action != ActionFail || got.Reason != ReasonNoCIResult {
		t.Errorf("a kill while another run runs past its bound: NextTaskStep() = %+v, want %s", got, ReasonNoCIResult)
	}

	own := f
	own.ChecksReruns, own.ChecksRerunAt = 0, time.Time{}
	own.ChecksLog = "--- FAIL: TestRun (0.00s)\n    run_test.go:12: Run() = signal: killed, want nil\nFAIL\n"
	if got := NextTaskStep(own); got.Action != ActionRetry || !strings.Contains(got.Feedback, "flaky") {
		t.Errorf("a test's own signal: killed: NextTaskStep() = %+v, want ActionRetry with the failure as feedback", got)
	}
}

func TestRunnerAbandoned(t *testing.T) {
	const at = "2026-10-15T12:00:00.1234567Z "
	failed := at + "--- FAIL: TestA (0.00s)\n" + at + "    a_test.go:9: got 1, want 2\n" + at + "FAIL\n" +
		at + "make: *** [Makefile:12: test] Error 1\n" + at + "##[error]Process completed with exit code 2.\n"
	for _, tc := range []struct {
		name      string
		cancelled bool
		noLog     bool
		log       string
		want      bool
	}{
		{name: "a run cancelled while no one asked: its runner stopped", cancelled: true,
			log: at + "go test ./...\n" + at + "Job cancelled\n" + at + "context canceled\n", want: true},
		{name: "a job that started and has no log", noLog: true, want: true},
		{name: "a log cut off mid-test, failed by Gitea once the runner stopped reporting",
			log: at + "go test ./...\n" + at + "=== RUN   TestA\n", want: true},
		{name: "the runner's last words when it ended with no result",
			log: at + "go test ./...\n" + at + "Early termination\n", want: true},
		{name: "a failed test", log: failed + at + "Job failed\n"},
		{name: "a failed test, the runner's error after its result", log: failed + at + "Job failed\n" +
			at + "exit status 2\n"},
		{name: "an older runner's flag", log: failed + at + "🏁  Job failed\n"},
		{name: "a log without Gitea's times", log: "--- FAIL: TestA (0.00s)\nFAIL\n"},
		{name: "a log that couldn't be read"},
		{name: "a test that prints the runner's words", log: at + "    a_test.go:9: Early termination\n" +
			at + "Job failed\n"},
	} {
		if got := RunnerAbandoned(tc.cancelled, tc.noLog, tc.log); got != tc.want {
			t.Errorf("%s: RunnerAbandoned() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCancelledRuns(t *testing.T) {
	cancelled := WorkflowRun{Workflow: "check.yaml", Event: "pull_request", Status: CIRunCompleted,
		Conclusion: "cancelled"}
	failed, passed := cancelled, cancelled
	failed.Conclusion, passed.Conclusion = "failure", "success"
	for _, tc := range []struct {
		name string
		runs []WorkflowRun
		want bool
	}{
		{name: "a cancelled run", runs: []WorkflowRun{cancelled}, want: true},
		{name: "a failed run", runs: []WorkflowRun{failed}},
		{name: "a cancelled run whose rerun failed", runs: []WorkflowRun{failed, cancelled}},
		{name: "a cancelled run whose rerun passed", runs: []WorkflowRun{passed, cancelled}},
		{name: "no runs"},
	} {
		if got := len(CancelledRuns(tc.runs)) > 0; got != tc.want {
			t.Errorf("%s: CancelledRuns() = %v, want some: %v", tc.name, CancelledRuns(tc.runs), tc.want)
		}
	}
}

// TestAbandonedChecksRunAgain walks a round whose checks their runner
// abandons: a cancelled run, a job with no log, a log cut off. Each time its
// failed jobs run again in the same round, with no feedback for the agent,
// sharing MaxChecksReruns with the jobs killed for lack of memory, and the
// next abandoned run fails the Task at no try.
func TestAbandonedChecksRunAgain(t *testing.T) {
	const at = "2026-10-15T12:00:00.1234567Z "
	for _, tc := range []struct {
		name string
		f    TaskFacts
	}{
		{name: "a run its runner cancelled", f: TaskFacts{CIRun: CIRun{State: CIRunCompleted,
			CancelledBy: CancelledByRunner}, ChecksLog: at + "Job cancelled\n"}},
		{name: "no log", f: TaskFacts{ChecksNoLog: true}},
		{name: "a lost runner's log", f: TaskFacts{ChecksLog: at + "go test ./...\n" + at + "=== RUN   TestA\n"}},
	} {
		f := tc.f
		f.Phase, f.Round, f.MaxRounds, f.Checks, f.ChecksURL = TaskChecking, 2, 3, "failure", "http://ci/962"
		p := TaskProgress{Phase: TaskChecking, Round: 2, Commit: "296c7ab", HasChecks: true}
		at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
		for i := range MaxChecksReruns {
			got := NextTaskStep(f)
			if got.Action != ActionRerunChecks || got.Feedback != "" ||
				!strings.Contains(got.Message, "http://ci/962 was abandoned by its runner") {
				t.Fatalf("%s, %d: NextTaskStep() = %+v, want ActionRerunChecks with no feedback", tc.name, i+1, got)
			}
			p = RerunChecks(p, at)
			if p.Round != 2 || p.ChecksReruns != i+1 {
				t.Fatalf("%s, %d: RerunChecks() = %+v, want round 2, %d reruns", tc.name, i+1, p, i+1)
			}
			f.ChecksReruns, f.ChecksRerunAt, f.ChecksRerunEnded = p.ChecksReruns, p.ChecksRerunAt, true
		}
		got := NextTaskStep(f)
		if got.Action != ActionFail || got.Reason != ReasonRunnerLost || !RunnerFault(got.Reason) ||
			!strings.Contains(got.Message, "3 times in round 2") {
			t.Errorf("%s: the cap: NextTaskStep() = %+v, want ActionFail, reason %s", tc.name, got, ReasonRunnerLost)
		}
	}

	running := TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3, Checks: "failure", ChecksNoLog: true,
		CIRun: CIRun{State: CIRunRunning, Started: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC),
			Running: time.Minute}}
	if got := NextTaskStep(running); got.Action != ActionWait {
		t.Errorf("an abandoned job while another run still runs: NextTaskStep() = %+v, want ActionWait", got)
	}

	failed := TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3, Checks: "failure",
		CIRun:     CIRun{State: CIRunCompleted},
		ChecksLog: at + "--- FAIL: TestA (0.00s)\n" + at + "FAIL\n" + at + "Job failed\n"}
	if got := NextTaskStep(failed); got.Action != ActionRetry {
		t.Errorf("a run the change failed: NextTaskStep() = %+v, want ActionRetry, a fix round", got)
	}
}

func TestCanceller(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	long, just := now.Add(-RunnerGoneAfter-time.Second), now.Add(-30*time.Second)
	stayed := CIJob{Conclusion: "cancelled", Runner: 4, RunnerOnline: true, Ended: long}
	gone := CIJob{Conclusion: "cancelled", Runner: 5, Ended: long}
	passed := CIJob{Conclusion: "success", Runner: 6, Ended: just}
	for _, tc := range []struct {
		name string
		jobs []CIJob
		read bool
		want string
	}{
		// #1143: a person's cancel in Gitea leaves its runner online, taking the next job.
		{name: "a runner that stayed online", jobs: []CIJob{stayed}, read: true, want: CancelledByPerson},
		{name: "a job no runner started", jobs: []CIJob{{Conclusion: "cancelled", Ended: just}}, read: true,
			want: CancelledByPerson},
		{name: "a job that passed, its runner gone since", jobs: []CIJob{stayed, passed}, read: true,
			want: CancelledByPerson},
		{name: "no cancelled job", read: true, want: CancelledByPerson},
		// A runner told to stop cancels its job and goes away; its pod's
		// successor registers as a new runner.
		{name: "a runner gone offline", jobs: []CIJob{stayed, gone}, read: true, want: CancelledByRunner},
		{name: "a runner gone offline just now", jobs: []CIJob{{Conclusion: "cancelled", Runner: 5, Ended: just}},
			read: true, want: CancelledByRunner},
		{name: "a runner the host doesn't say of", jobs: []CIJob{{Conclusion: "cancelled", Runner: 4,
			RunnerUnknown: true, Ended: long}}, read: true, want: CancelledByRunner},
		{name: "jobs that couldn't be read", read: false, want: CancelledByRunner},
		{name: "a runner still online just after", jobs: []CIJob{stayed, {Conclusion: "cancelled", Runner: 6,
			RunnerOnline: true, Ended: just}}, read: true, want: CancelledByUnsure},
		// #1140: Gitea deletes a pool pod's ephemeral runner once its job ends,
		// so the pod's fate tells, not whether the runner stayed online.
		{name: "a pool pod that ended on its own", jobs: []CIJob{{Conclusion: "cancelled", Runner: 7,
			RunnerName: "ci-gitea-go-abc12", PodFate: PodFateEnded, Ended: just}}, read: true, want: CancelledByPerson},
		{name: "a pool pod lost before its job ended", jobs: []CIJob{stayed, {Conclusion: "cancelled", Runner: 7,
			RunnerName: "ci-gitea-go-abc12", PodFate: PodFateLost, RunnerOnline: true, Ended: long}}, read: true,
			want: CancelledByRunner},
		{name: "a pool pod the pool no longer records", jobs: []CIJob{{Conclusion: "cancelled", Runner: 7,
			RunnerName: "ci-gitea-go-abc12", Ended: long}}, read: true, want: CancelledByRunner},
	} {
		if got := Canceller(tc.jobs, tc.read, now); got != tc.want {
			t.Errorf("%s: Canceller() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestPersonCancelledChecks walks a round whose CI run a person cancelled in
// Gitea (#1143): it costs the round, with feedback saying so, as any failed
// check does, while the same run cancelled by its runner runs again at no
// round, and one whose canceller can't be told yet waits.
func TestPersonCancelledChecks(t *testing.T) {
	const at = "2026-10-16T12:00:00.1234567Z "
	f := TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3, Checks: "failure", ChecksURL: "http://ci/1143",
		CIRun:     CIRun{State: CIRunCompleted, CancelledBy: CancelledByPerson},
		ChecksLog: at + "go test ./...\n" + at + "Job cancelled\n"}
	got := NextTaskStep(f)
	if got.Action != ActionRetry || !strings.Contains(got.Feedback, "The CI checks failed (http://ci/1143)") ||
		!strings.Contains(got.Feedback, "Someone cancelled the checks before they ended") {
		t.Errorf("a person's cancel: NextTaskStep() = %+v, want ActionRetry, a fix round saying so", got)
	}
	merged := f
	merged.MergeWhenChecksPass = true
	if got := NextTaskStep(merged); got.Action != ActionRetry || !strings.Contains(got.Feedback, "main moved") ||
		!strings.Contains(got.Feedback, "Someone cancelled the checks") {
		t.Errorf("a person's cancel with main merged in: NextTaskStep() = %+v, want both said", got)
	}
	// Another run's runner died at the same commit: its cut-off log still says so.
	lost := f
	lost.ChecksLog = at + "go test ./...\n" + at + "=== RUN   TestA\n"
	if got := NextTaskStep(lost); got.Action != ActionRerunChecks {
		t.Errorf("a person's cancel next to a lost runner's job: NextTaskStep() = %+v, want ActionRerunChecks", got)
	}
	last := f
	last.Round = 3
	if got := NextTaskStep(last); got.Action != ActionFail || got.Reason != "" || RunnerFault(got.Reason) {
		t.Errorf("a person's cancel in the last round: NextTaskStep() = %+v, want ActionFail costing a try", got)
	}

	runner := f
	runner.CIRun.CancelledBy = CancelledByRunner
	if got := NextTaskStep(runner); got.Action != ActionRerunChecks || got.Feedback != "" {
		t.Errorf("a runner's cancel: NextTaskStep() = %+v, want ActionRerunChecks at no round", got)
	}

	unsure := f
	unsure.CIRun.CancelledBy = CancelledByUnsure
	if got := NextTaskStep(unsure); got.Action != ActionWait || !strings.Contains(got.Message, "2 minutes") {
		t.Errorf("a cancel not yet told: NextTaskStep() = %+v, want ActionWait", got)
	}
}

// TestPushedAgainRerunsChecks walks a round that pushes again the commit whose
// checks failed in the round before: its failed jobs run again before the
// checks are read, without counting towards MaxChecksReruns, while a new
// commit's checks are read as they come.
func TestPushedAgainRerunsChecks(t *testing.T) {
	failed := TaskProgress{Phase: TaskChecking, Round: 1, Commit: "296c7ab", HasChecks: true, ChecksFailed: true}
	next := NextRound(failed, "The CI checks failed")
	if !next.HasChecks || next.Round != 2 {
		t.Fatalf("NextRound() = %+v, want the failed checks kept into round 2", next)
	}
	if passed := NextRound(TaskProgress{Round: 1, Commit: "296c7ab", HasChecks: true}, "changes"); passed.HasChecks {
		t.Errorf("NextRound() = %+v, want checks that didn't fail dropped", passed)
	}

	implemented := TaskFacts{Phase: TaskImplementing, Round: 2, MaxRounds: 3, FailedCommit: "296c7ab"}
	for _, tt := range []struct {
		name, commit string
		want         TaskAction
	}{
		{"the failed commit again", "296c7ab", ActionRerunChecks},
		{"a new commit", "abc1234", ActionOpenPR},
	} {
		f := implemented
		f.Run = RunState{Started: true, Done: true, Result: &RunResult{Commit: tt.commit}}
		if got := NextTaskStep(f); got.Action != tt.want {
			t.Errorf("%s: NextTaskStep() = %+v, want %s", tt.name, got, tt.want)
		}
	}

	p := PushedAgain(next, "296c7ab")
	if p.Phase != TaskChecking || !p.ChecksRerunDue || p.HasChecks {
		t.Fatalf("PushedAgain() = %+v, want Checking with a rerun due", p)
	}
	f := TaskFacts{Phase: TaskChecking, Round: 2, MaxRounds: 3, Checks: "failure", ChecksURL: "http://ci/962",
		ChecksLog: "--- FAIL: TestRun (0.00s)\nFAIL\n", ChecksRerunDue: true, ChecksReruns: MaxChecksReruns}
	got := NextTaskStep(f)
	if got.Action != ActionRerunChecks || got.Feedback != "" || !strings.Contains(got.Message, "http://ci/962") {
		t.Fatalf("NextTaskStep() = %+v, want ActionRerunChecks though the kills' reruns are spent", got)
	}
	running := f
	running.CIRun = CIRun{Commit: "296c7ab", State: CIRunRunning, Started: time.Now(), Running: time.Minute}
	if got := NextTaskStep(running); got.Action != ActionWait {
		t.Errorf("another run still runs: NextTaskStep() = %+v, want ActionWait", got)
	}

	at := time.Date(2026, 10, 10, 14, 40, 0, 0, time.UTC)
	p = RerunPushedChecks(p, at)
	if p.ChecksRerunDue || !p.ChecksRerunAt.Equal(at) || p.ChecksReruns != 0 || p.Round != 2 {
		t.Fatalf("RerunPushedChecks() = %+v, want the rerun at %s, not counted, in round 2", p, at)
	}
	f.ChecksRerunDue, f.ChecksRerunAt, f.SinceChecksRerun = false, at, time.Minute
	f.CIRun = CIRun{Commit: "296c7ab", State: CIRunCompleted}
	if got := NextTaskStep(f); got.Action != ActionWait {
		t.Errorf("before the rerun ended: NextTaskStep() = %+v, want ActionWait, not the old failure", got)
	}
	f.ChecksRerunEnded = true
	if got := NextTaskStep(f); got.Action != ActionRetry {
		t.Errorf("the rerun failed: NextTaskStep() = %+v, want ActionRetry: the round is spent", got)
	}

	if opened := OpenedPR(p, "abc1234"); opened.ChecksRerunDue || opened.HasChecks {
		t.Errorf("OpenedPR() = %+v, want no rerun due for a new commit", opened)
	}
}

// TestKilledRunsFailureIsNotRead checks that a failed status read before a
// run at the commit started since the rerun waits as pending checks do.
func TestKilledRunsFailureIsNotRead(t *testing.T) {
	at := time.Date(2026, 10, 10, 14, 40, 0, 0, time.UTC)
	f := TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3, Checks: "failure", ChecksURL: "http://ci/962",
		ChecksReruns: 1, ChecksRerunAt: at, InPhase: 2 * time.Hour}
	tests := []struct {
		name    string
		ci      CIRun
		since   time.Duration
		want    TaskAction
		message string
	}{
		{"queued since the rerun waits", CIRun{Commit: "296c7ab1", State: CIRunQueued}, 5 * time.Minute,
			ActionWait, "queued behind other jobs since 14:40Z"},
		{"not yet listed again waits", CIRun{Commit: "296c7ab1", State: CIRunCompleted}, 5 * time.Minute,
			ActionWait, "the failed jobs run again since 14:40Z"},
		{"not started 30 minutes after the rerun fails", CIRun{Commit: "296c7ab1", State: CIRunCompleted},
			31 * time.Minute, ActionFail, "The failed jobs at 296c7ab, run again at 14:40Z, didn't start within 30m0s"},
		{"unknown runs wait", CIRun{State: CIRunUnknown}, 5 * time.Minute, ActionWait,
			"doesn't say whether a CI run is queued"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := f
			f.CIRun, f.SinceChecksRerun = tt.ci, tt.since
			got := NextTaskStep(f)
			if got.Action != tt.want || !strings.Contains(got.Message, tt.message) {
				t.Fatalf("NextTaskStep() = %+v, want %s with %q", got, tt.want, tt.message)
			}
			if failed := got.Action == ActionFail; failed != (got.Reason == ReasonNoCIResult) {
				t.Errorf("Reason = %q, want %s on each failure only", got.Reason, ReasonNoCIResult)
			}
		})
	}

	f.ChecksRerunEnded = true
	if got := NextTaskStep(f); got.Action != ActionRetry {
		t.Errorf("the rerun's own failure: NextTaskStep() = %+v, want ActionRetry", got)
	}
}

func TestCommitRunsAfterAKill(t *testing.T) {
	at := time.Date(2026, 10, 10, 14, 40, 0, 0, time.UTC)
	runs := []WorkflowRun{ // newest first
		{ID: 9, Workflow: "ci.yaml", Event: "pull_request", Status: "completed", Conclusion: "failure",
			Started: at.Add(-20 * time.Minute)},
		{ID: 8, Workflow: "ci.yaml", Event: "push", Status: "completed", Conclusion: "success"},
		{ID: 7, Workflow: "lint.yaml", Event: "pull_request", Status: "completed", Conclusion: "skipped"},
		{ID: 6, Workflow: "ci.yaml", Event: "pull_request", Status: "completed", Conclusion: "failure"},
		{ID: 5, Workflow: "e2e.yaml", Event: "pull_request", Status: "completed", Conclusion: "cancelled"},
		{ID: 4, Workflow: "docs.yaml", Event: "pull_request", Status: "in_progress"},
	}
	ids := make([]int64, 0, len(runs))
	for _, run := range FailedCommitRuns(runs) {
		ids = append(ids, run.ID)
	}
	if !slices.Equal(ids, []int64{9, 5}) {
		t.Errorf("FailedCommitRuns() = %v, want the newest failed run of each workflow and event, [9 5]", ids)
	}
	if RunsEnded(runs) || RerunEnded(runs, at) {
		t.Error("RunsEnded() or RerunEnded() with a run in progress = true, want false")
	}
	runs[5].Status = "completed"
	if !RunsEnded(runs) || RerunEnded(runs, at) {
		t.Error("with every run started before the rerun: want RunsEnded() and not RerunEnded()")
	}
	runs[0].Started = at.Add(time.Minute)
	if !RerunEnded(runs, at) {
		t.Error("RerunEnded() with every run ended, one started after the rerun = false, want true")
	}
	runs[1].Status = "queued"
	if RerunEnded(runs, at) {
		t.Error("RerunEnded() with a run started after the rerun but another one queued = true, want false")
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
	sandbox := func() string { return SandboxName("t", p.Round, 0, RoleReviewer, p.ReviewAttempts+p.ReviewAgains+1, 0) }
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
			got := GatedMergeRefusal(giteaActions, tt.checks, tt.contexts, tt.verdict, tt.changed, tt.allowed,
				AgentProtectedPaths(giteaActions, []string{"Makefile", "README.md"}))
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

// A Standard change whose round left its paths as on main stops at once,
// reason Blocked, with what the round before was sent back with: nothing is
// left to review or merge (#929).
func TestNextTaskStepEmptiedStandardChange(t *testing.T) {
	done := func(r RunResult) RunState { return RunState{Started: true, Done: true, Result: &r} }
	facts := TaskFacts{Phase: TaskImplementing, Container: "engine", Round: 2, MaxRounds: 3,
		AllowedPaths: []string{".gitea/workflows/check.yaml"},
		Feedback:     "The reviewer asked for changes:\nA Standard change may not skip a CI job.",
		Run:          done(RunResult{Empty: true})}
	got := NextTaskStep(facts)
	if got.Action != ActionFail || got.Reason != ReasonBlocked {
		t.Fatalf("NextTaskStep() = %+v, want a Blocked failure", got)
	}
	for _, want := range []string{"leaves .gitea/workflows/check.yaml as on main", "nothing to merge",
		"sent back with: The reviewer asked for changes: A Standard change may not skip a CI job."} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("Message = %q, want it to contain %q", got.Message, want)
		}
	}

	facts.Feedback = ""
	if got := NextTaskStep(facts); strings.Contains(got.Message, "sent back") {
		t.Errorf("Message = %q names feedback there was none of", got.Message)
	}
	facts.AllowedPaths = nil
	facts.Run = done(RunResult{Commit: "c", Empty: true})
	if got := NextTaskStep(facts); got.Action != ActionOpenPR {
		t.Errorf("a Task that is no Standard change: NextTaskStep() = %+v, want %s", got, ActionOpenPR)
	}
}

func TestLeavesAllowedAsOnBase(t *testing.T) {
	allowed := []string{"Makefile", ".gitea/"}
	tests := []struct {
		changed []string
		want    bool
	}{
		{nil, true},
		{[]string{"README.md"}, true},
		{[]string{"makefile"}, true},
		{[]string{"Makefile"}, false},
		{[]string{"README.md", ".gitea/workflows/check.yaml"}, false},
	}
	for _, tt := range tests {
		if got := LeavesAllowedAsOnBase(tt.changed, allowed); got != tt.want {
			t.Errorf("LeavesAllowedAsOnBase(%v) = %v, want %v", tt.changed, got, tt.want)
		}
	}
}
