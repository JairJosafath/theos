package model

import (
	"strings"
	"testing"
	"time"
)

func TestTaskProgressTransitions(t *testing.T) {
	// mid is a Task in the middle of round 2, with everything set.
	mid := TaskProgress{Phase: TaskReviewing, Round: 2, ReviewAttempts: 1, Reruns: 1, Feedback: "earlier",
		Commit: "abc", HasChecks: true, ResumedRound: 2}

	tests := []struct {
		name string
		got  TaskProgress
		want TaskProgress
	}{
		{name: "the first run starts round 1", got: StartRun(TaskProgress{Phase: TaskPending}, RoleImplementer),
			want: TaskProgress{Phase: TaskImplementing, Round: 1}},
		{name: "a review run keeps the round", got: StartRun(mid, RoleReviewer), want: mid},
		{name: "an implement run in a later round keeps it",
			got:  StartRun(TaskProgress{Phase: TaskImplementing, Round: 3, Feedback: "fix it"}, RoleImplementer),
			want: TaskProgress{Phase: TaskImplementing, Round: 3, Feedback: "fix it"}},
		{name: "an opened pull request waits for the commit's checks", got: OpenedPR(mid, "def"),
			want: TaskProgress{Phase: TaskChecking, Round: 2, ReviewAttempts: 1, Reruns: 1, Feedback: "earlier",
				Commit: "def", ResumedRound: 2}},
		{name: "the next round starts afresh with the feedback", got: NextRound(mid, "test the empty image"),
			want: TaskProgress{Phase: TaskImplementing, Round: 3, Feedback: "test the empty image", Commit: "abc",
				ResumedRound: 2}},
		{name: "resolving conflicts stays in the round and forgets the resume",
			got: ResolveConflicts(mid, ConflictFeedback),
			want: TaskProgress{Phase: TaskImplementing, Round: 2, Reruns: 1, Feedback: ConflictFeedback,
				Commit: "abc"}},
		{name: "a review without verdict counts an attempt", got: RetryReview(mid),
			want: TaskProgress{Phase: TaskReviewing, Round: 2, ReviewAttempts: 2, Reruns: 1, Feedback: "earlier",
				Commit: "abc", HasChecks: true, ResumedRound: 2}},
		{name: "an interrupted run counts a rerun", got: Rerun(mid),
			want: TaskProgress{Phase: TaskReviewing, Round: 2, ReviewAttempts: 1, Reruns: 2, Feedback: "earlier",
				Commit: "abc", HasChecks: true, ResumedRound: 2}},
		{name: "a moved branch checks its new head", got: Recheck(mid, "fed"),
			want: TaskProgress{Phase: TaskChecking, Round: 2, ReviewAttempts: 1, Reruns: 1, Feedback: "earlier",
				Commit: "fed", ResumedRound: 2}},
		{name: "a proposal blocks the Task on its commit", got: Proposed(mid, "std"),
			want: TaskProgress{Phase: TaskBlocked, Round: 2, ReviewAttempts: 1, Reruns: 1, Feedback: "earlier",
				Commit: "std", HasChecks: true, ResumedRound: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %+v, want %+v", tt.got, tt.want)
			}
		})
	}
}

func TestFirstRun(t *testing.T) {
	if !FirstRun(TaskProgress{}) {
		t.Error("a Task without a round is on its first run")
	}
	if FirstRun(StartRun(TaskProgress{}, RoleImplementer)) {
		t.Error("a Task in round 1 has run before")
	}
}

func TestAgentBackAt(t *testing.T) {
	now := time.Date(2026, 10, 6, 1, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		retryAt time.Time
		want    time.Time
	}{
		{name: "the agent's own time", retryAt: now.Add(time.Hour), want: now.Add(time.Hour)},
		{name: "no time is the default delay", want: now.Add(AgentRetryDelay)},
		{name: "a time past is the default delay", retryAt: now.Add(-time.Minute), want: now.Add(AgentRetryDelay)},
		{name: "now is the default delay", retryAt: now, want: now.Add(AgentRetryDelay)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AgentBackAt(tt.retryAt, now); !got.Equal(tt.want) {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestDecideMerge(t *testing.T) {
	tests := []struct {
		name string
		pr   PullFacts
		want MergeAction
	}{
		{name: "an open pull request at the reviewed commit merges", pr: PullFacts{HeadSHA: "abc"}, want: MergeSquash},
		{name: "a host that doesn't name the head merges", pr: PullFacts{}, want: MergeSquash},
		{name: "a push after the review is checked again", pr: PullFacts{HeadSHA: "def"}, want: MergeRecheck},
		{name: "a closed pull request fails the Task", pr: PullFacts{Closed: true, HeadSHA: "abc"},
			want: MergeFailClosed},
		{name: "closed beats a moved head", pr: PullFacts{Closed: true, HeadSHA: "def"}, want: MergeFailClosed},
		{name: "a merged pull request is done", pr: PullFacts{Merged: true, Closed: true, HeadSHA: "def"},
			want: MergeDone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DecideMerge(tt.pr, "abc"); got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestAfterSquash(t *testing.T) {
	tests := []struct {
		name    string
		pending bool
		refusal string
		want    SquashOutcome
	}{
		{name: "merged", want: SquashMerged},
		{name: "a gated merge waits for CI", pending: true, want: SquashWaitForChecks},
		{name: "a refused gated merge fails", refusal: "checks failed", want: SquashRefused},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AfterSquash(tt.pending, tt.refusal); got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestTaskEnds(t *testing.T) {
	tests := []struct {
		name string
		got  TaskEnd
		want TaskEnd
	}{
		{name: "a closed issue", got: IssueClosed(7), want: TaskEnd{Phase: TaskFailed, Reason: "IssueClosed",
			Message: "Issue #7 was closed before the Task finished"}},
		{name: "a closed pull request", got: PullRequestClosed("http://git/pulls/3"), want: TaskEnd{Phase: TaskFailed,
			Reason: "PullRequestClosed", Message: "Pull request http://git/pulls/3 was closed without merging"}},
		{name: "a refused gated merge", got: GatedMergeRefused("checks failed"), want: TaskEnd{Phase: TaskFailed,
			Reason: "GatedMergeRefused", Message: "The gated merge was refused: checks failed"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %+v, want %+v", tt.got, tt.want)
			}
		})
	}
}

func TestProposeRefusal(t *testing.T) {
	paths := []string{"Makefile", ".github/workflows/ci.yaml"}
	tests := []struct {
		name  string
		issue int
		crew  bool
		// want is in the refusal; empty means none.
		want string
	}{
		{name: "a Crew's issue Task proposes", issue: 4, crew: true},
		{name: "a goal Task can't", crew: true, want: "only an issue's Task can propose one"},
		{name: "a goal outside a Crew is refused as a goal", want: "only an issue's Task can propose one"},
		{name: "a Task outside a Crew can't", issue: 4, want: "only a Crew's Task can propose one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProposeRefusal(tt.issue, tt.crew, paths)
			if tt.want == "" {
				if got != "" {
					t.Errorf("refused: %q", got)
				}
				return
			}
			if !strings.Contains(got, tt.want) || !strings.Contains(got, "`Makefile`, `.github/workflows/ci.yaml`") {
				t.Errorf("got %q, want it to contain %q and the paths", got, tt.want)
			}
		})
	}
}

func TestProposalCount(t *testing.T) {
	if got := ProposalCount(0); got != 1 {
		t.Errorf("the first proposal is %d, want 1", got)
	}
	if got := ProposalCount(2); got != 3 {
		t.Errorf("after 2 proposals got %d, want 3", got)
	}
}
