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
		{name: "gives up on checks that never report", facts: TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3,
			Checks: "pending", InPhase: 31 * time.Minute}, want: ActionFail, message: "No CI result after 30m0s"},
		{name: "green checks start the review", facts: TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3,
			Checks: "success"}, want: ActionStartReview},
		{name: "red checks send it back", facts: TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3,
			Checks: "failure", ChecksURL: "http://gitea/runs/4"}, want: ActionRetry, feedback: "http://gitea/runs/4"},
		{name: "red checks pass on what failed", facts: TaskFacts{Phase: TaskChecking, Round: 1, MaxRounds: 3,
			Checks: "failure", ChecksLog: "x\n❌ test/a_test.dart: A (failed)\nExpected: 1\n"}, want: ActionRetry,
			feedback: "What failed in CI:\n❌ test/a_test.dart: A (failed)\nExpected: 1"},

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
		{name: "a review that ends while its merge waits for CI refuses the merge, not reviews again",
			facts: TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3, MergeWaitsForChecks: true,
				Run: RunState{Started: true, Done: true, Failure: "the sandbox reached its shutdown time"}},
			want: ActionRefuseMerge, message: "CI is still pending on the reviewed commit"},
		{name: "even after earlier reviewer failures", facts: TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3,
			MergeWaitsForChecks: true, ReviewAttempts: 2, Run: RunState{Started: true, Done: true}},
			want: ActionRefuseMerge, message: "CI is still pending"},

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
	if got := AgentUsername("toolbox", "photo-editor"); got != "toolbox-photo-editor-agent" {
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
	if got := SandboxName("add-crop", 2, RoleReviewer, 1); got != "add-crop-r2-review-a1" {
		t.Errorf("SandboxName() = %q", got)
	}
	long := SandboxName(strings.Repeat("x", 80), 10, RoleImplementer, 0)
	if len(long) > 63 {
		t.Errorf("SandboxName() = %q is longer than 63", long)
	}
}

func TestHasSlotTurn(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)
	waiting := []SlotWaiter{
		{Name: "toolbox/bg-eraser", Since: t0},
		{Name: "toolbox/doc-scanner", Since: t0.Add(time.Minute)},
	}
	tests := []struct {
		name string
		task string
		free int
		want bool
	}{
		{"the longest waiter goes first", "toolbox/bg-eraser", 1, true},
		{"later waiters wait", "toolbox/doc-scanner", 1, false},
		{"a newcomer joins at the back", "toolbox/audio-cutter", 1, false},
		{"two slots take two waiters", "toolbox/doc-scanner", 2, true},
		{"no free slot, no turn", "toolbox/bg-eraser", 0, false},
		{"an empty queue lets a newcomer start", "toolbox/audio-cutter", 3, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasSlotTurn(tt.task, t0.Add(time.Hour), waiting, tt.free); got != tt.want {
				t.Errorf("HasSlotTurn(%s, free %d) = %v, want %v", tt.task, tt.free, got, tt.want)
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
			name: "flutter",
			log: stamp(
				"+ very_good_analysis 11.0.0",
				"✅ test/a_test.dart: A works",
				"::group::❌ test/recorder_page_test.dart: RecorderPage shows the stop button (failed)",
				"══╡ EXCEPTION CAUGHT BY FLUTTER TEST FRAMEWORK ╞══",
				"Expected: exactly one matching candidate",
				"  Actual: _TextWidgetFinder:<Found 0 widgets>",
				"::endgroup::",
				"✅ test/b_test.dart: B works",
				"   info • Unnecessary cast • lib/x.dart:3:5 • unnecessary_cast",
				"make: *** [Makefile:21: test] Error 1",
			),
			want: []string{"RecorderPage shows the stop button (failed)", "Expected: exactly one",
				"Actual: _TextWidgetFinder", "info • Unnecessary cast", "make: *** [Makefile:21: test]"},
			unwanted: []string{"very_good_analysis", "A works", "B works", "2026-10-01T"},
		},
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
				"AssertionError: expected '/usr/bin/cel' to be '/opt/cel' // Object.is equality",
				" ❯ src/server.test.ts:42:17",
				"⎯⎯⎯⎯⎯⎯⎯[1/1]⎯",
				" Test Files  1 failed | 1 passed (2)",
				"src/extension.ts(10,7): error TS2322: Type 'string' is not assignable to type 'number'.",
				"/workspace/src/extension.ts",
				"  3:7  error  'x' is assigned a value but never used  @typescript-eslint/no-unused-vars",
				"✖ 1 problem (1 error, 0 warnings)",
			),
			want: []string{"× src/server.test.ts > findServer > prefers the setting",
				"FAIL  src/server.test.ts > findServer > prefers the setting", "AssertionError: expected '/usr/bin/cel'",
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
	if long := CheckFailures(strings.Repeat("error • x\n", 2000)); len(long) > maxCheckExcerpt+10 {
		t.Errorf("CheckFailures() of a long log = %d bytes", len(long))
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
			if got.Action == ActionAgentUnavailable && !got.RetryAt.Equal(back) {
				t.Errorf("RetryAt = %v, want %v", got.RetryAt, back)
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
	approved := RunState{Started: true, Done: true, Result: &RunResult{Verdict: VerdictApprove, Summary: "Good."}}
	got := NextTaskStep(TaskFacts{Phase: TaskReviewing, Round: 3, MaxRounds: 3, Run: approved, Conflicted: true})
	if got.Action != ActionResolveConflicts || !strings.Contains(got.Feedback, "Merge main into your branch") ||
		!strings.Contains(got.Message, "round 3 resolves them") {
		t.Errorf("NextTaskStep() = %+v, want the implementer to resolve the conflicts in the same round", got)
	}
	if got := NextTaskStep(TaskFacts{Phase: TaskReviewing, Round: 1, MaxRounds: 3, Run: approved}); got.Action != ActionMerge {
		t.Errorf("NextTaskStep() = %+v, want a merge without conflicts", got)
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
	}{
		{name: "no tasks, open"},
		// The gated merge closes the issue in the reconcile that sets Merged, so the
		// issue can read closed before any Task says Merged: wait, don't fail.
		{name: "closed while the Task is still active", tasks: []ChangeTask{{Name: "x-31"}}, closed: true},
		{name: "merged", tasks: []ChangeTask{{Name: "x-31", Final: true}}, closed: true, wantMerged: true},
		{name: "failed", tasks: []ChangeTask{{Name: "x-31", Final: true, Failed: true, Message: "Failed after 3 rounds"}},
			wantFailed: "Task x-31 failed: Failed after 3 rounds"},
		{name: "failed, then retried", tasks: []ChangeTask{{Name: "x-31", Final: true, Failed: true, Message: "m"},
			{Name: "x-31-2"}}},
		{name: "failed, then merged", tasks: []ChangeTask{{Name: "x-31", Final: true, Failed: true, Message: "m"},
			{Name: "x-31-2", Final: true}}, wantMerged: true},
		{name: "closed without any Task", closed: true, wantFailed: "issue #31 was closed without a merge"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged, failed := StandardChangeOutcome(31, tt.tasks, tt.closed)
			if merged != tt.wantMerged || failed != tt.wantFailed {
				t.Errorf("StandardChangeOutcome() = %v, %q; want %v, %q", merged, failed, tt.wantMerged, tt.wantFailed)
			}
		})
	}
}

func TestResumeFeedback(t *testing.T) {
	got := ResumeFeedback(31, []string{"Makefile"}, "")
	for _, want := range []string{"#31", "Makefile", "merged into main"} {
		if !strings.Contains(got, want) {
			t.Errorf("ResumeFeedback() = %q, want it to contain %q", got, want)
		}
	}
	got = ResumeFeedback(31, []string{"Makefile"}, "The reviewer asked for changes:\n- fix x")
	if !strings.Contains(got, "- fix x") {
		t.Errorf("ResumeFeedback() = %q, want the earlier feedback", got)
	}
}

func TestGatedMergeRefusal(t *testing.T) {
	mk := []string{"Makefile"}
	tests := []struct {
		name             string
		checks, verdict  string
		changed, allowed []string
		want             string // substring; "" means merge
	}{
		{name: "merges", checks: "success", verdict: VerdictApprove, changed: mk, allowed: mk},
		{name: "a directory allowance", checks: "success", verdict: VerdictApprove,
			changed: []string{".gitea/workflows/release.yaml"}, allowed: []string{".gitea/"}},
		{name: "red CI", checks: "failure", verdict: VerdictApprove, changed: mk, allowed: mk, want: "CI"},
		{name: "no verdict", checks: "success", verdict: VerdictChanges, changed: mk, allowed: mk, want: "approve"},
		{name: "outside the allowance", checks: "success", verdict: VerdictApprove,
			changed: []string{"Makefile", "README.md"}, allowed: mk, want: "README.md"},
		{name: "empty change", checks: "success", verdict: VerdictApprove, allowed: mk, want: "changes nothing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GatedMergeRefusal(tt.checks, tt.verdict, tt.changed, tt.allowed)
			if tt.want == "" && got != "" || !strings.Contains(got, tt.want) {
				t.Errorf("GatedMergeRefusal() = %q, want %q", got, tt.want)
			}
		})
	}
}
