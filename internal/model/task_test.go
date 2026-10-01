package model

import (
	"strings"
	"testing"
	"time"
)

func TestNextTaskStep(t *testing.T) {
	done := func(r RunResult) RunState { return RunState{Started: true, Done: true, Result: &r} }
	running := RunState{Started: true}

	tests := []struct {
		name  string
		facts TaskFacts
		want  TaskAction
		// feedback and message must contain these.
		feedback, message string
	}{
		{name: "waits for the Container", facts: TaskFacts{Phase: TaskPending, Round: 1, MaxRounds: 3},
			want: ActionWait, message: "Waiting for Container photo-editor to be Ready"},
		{name: "waits behind an earlier Task", facts: TaskFacts{Phase: TaskPending, ContainerReady: true,
			BlockedBy: "add-crop", Round: 1, MaxRounds: 3}, want: ActionWait, message: "Waiting for Task add-crop"},
		{name: "starts when the Container is Ready", facts: TaskFacts{Phase: TaskPending, ContainerReady: true,
			Round: 1, MaxRounds: 3}, want: ActionStartImplement},
		{name: "a new Task starts pending", facts: TaskFacts{ContainerReady: true, MaxRounds: 3}, want: ActionStartImplement},

		{name: "restarts a lost implement sandbox", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3},
			want: ActionStartImplement},
		{name: "waits for the agent", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3, Run: running},
			want: ActionWait},
		{name: "opens the pull request", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: done(RunResult{Commit: "abc"})}, want: ActionOpenPR},
		{name: "no change is a failed round", facts: TaskFacts{Phase: TaskImplementing, Round: 1, MaxRounds: 3,
			Run: done(RunResult{Error: "the agent made no changes"})},
			want: ActionRetry, feedback: "the agent made no changes"},
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
