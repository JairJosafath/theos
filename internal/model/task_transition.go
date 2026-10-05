package model

import (
	"fmt"
	"strings"
	"time"
)

// TaskProgress is the part of a Task's status that its transitions change.
// The operator copies it in from status, applies a transition and copies it back.
type TaskProgress struct {
	Phase          string
	Round          int
	ReviewAttempts int
	Reruns         int
	Feedback       string
	Commit         string
	// HasChecks means the round's check results are kept; a transition that
	// clears it drops them.
	HasChecks bool
	// ResumedRound is the round a Blocked Task resumed in; 0 when it didn't.
	ResumedRound int
}

// TaskEnd is how a Task ends: its final phase, and the reason and message of
// its Succeeded condition.
type TaskEnd struct {
	Phase   string
	Reason  string
	Message string
}

// StartRun is the progress of a Task starting a sandbox for role: the first
// run starts round 1.
func StartRun(p TaskProgress, role string) TaskProgress {
	if p.Round == 0 {
		p.Round = 1
	}
	p.Phase = TaskImplementing
	if role == RoleReviewer {
		p.Phase = TaskReviewing
	}
	return p
}

// FirstRun says whether a run is the Task's first, before StartRun numbers
// its round: only the first one announces the Task on its issue.
func FirstRun(p TaskProgress) bool { return p.Round == 0 }

// IssueClosed ends a Task whose issue was closed before it finished.
func IssueClosed(issue int) TaskEnd {
	return TaskEnd{Phase: TaskFailed, Reason: "IssueClosed",
		Message: fmt.Sprintf("Issue #%d was closed before the Task finished", issue)}
}

// OpenedPR is the progress of a Task whose round's pull request is open at
// commit: it waits for that commit's checks.
func OpenedPR(p TaskProgress, commit string) TaskProgress {
	p.Commit = commit
	p.HasChecks = false
	p.Phase = TaskChecking
	return p
}

// NextRound is the progress of a Task whose round was not accepted: the next
// round implements again with the feedback.
func NextRound(p TaskProgress, feedback string) TaskProgress {
	p.Round++
	p.Feedback = feedback
	p.ReviewAttempts = 0
	p.Reruns = 0
	p.HasChecks = false
	p.Phase = TaskImplementing
	return p
}

// ResolveConflicts is the progress of an approved Task sent back, in the same
// round, to resolve conflicts with main. Only the first run after a resume
// skips the empty-branch guard, so the resume is forgotten.
func ResolveConflicts(p TaskProgress, feedback string) TaskProgress {
	p.Feedback = feedback
	p.ReviewAttempts = 0
	p.HasChecks = false
	p.ResumedRound = 0
	p.Phase = TaskImplementing
	return p
}

// RetryReview is the progress of a Task whose reviewer gave no verdict: the
// review runs again, and counts the attempt.
func RetryReview(p TaskProgress) TaskProgress {
	p.ReviewAttempts++
	return p
}

// Rerun is the progress of a Task whose run the infrastructure interrupted:
// the same step runs again, and counts the rerun.
func Rerun(p TaskProgress) TaskProgress {
	p.Reruns++
	return p
}

// AgentBackAt is when to try an unavailable agent again: at retryAt, or after
// AgentRetryDelay when retryAt is not after now.
func AgentBackAt(retryAt, now time.Time) time.Time {
	if !retryAt.After(now) {
		return now.Add(AgentRetryDelay)
	}
	return retryAt
}

// PullFacts is what the Git host says of a Task's pull request.
type PullFacts struct {
	Merged bool
	Closed bool
	// HeadSHA is the branch's head commit; empty when the host didn't say.
	HeadSHA string
}

// MergeAction is what the operator does with an approved pull request.
type MergeAction string

// Merge actions.
const (
	// MergeFailClosed fails the Task: its pull request was closed unmerged.
	MergeFailClosed MergeAction = "FailClosed"
	// MergeRecheck checks and reviews the branch's new head: someone pushed
	// after the review.
	MergeRecheck MergeAction = "Recheck"
	// MergeSquash merges the reviewed commit.
	MergeSquash MergeAction = "Squash"
	// MergeDone records a pull request that is merged already.
	MergeDone MergeAction = "Done"
)

// DecideMerge chooses what to do with the approved pull request of the
// reviewed commit.
func DecideMerge(pr PullFacts, reviewed string) MergeAction {
	switch {
	case pr.Merged:
		return MergeDone
	case pr.Closed:
		return MergeFailClosed
	case pr.HeadSHA != "" && pr.HeadSHA != reviewed:
		return MergeRecheck
	}
	return MergeSquash
}

// PullRequestClosed ends a Task whose pull request was closed without merging.
func PullRequestClosed(url string) TaskEnd {
	return TaskEnd{Phase: TaskFailed, Reason: "PullRequestClosed",
		Message: "Pull request " + url + " was closed without merging"}
}

// Recheck is the progress of a Task whose branch moved to head after the
// review: it checks and reviews head instead.
func Recheck(p TaskProgress, head string) TaskProgress {
	p.Commit = head
	p.HasChecks = false
	p.Phase = TaskChecking
	return p
}

// SquashOutcome is how a squash of the reviewed commit went.
type SquashOutcome string

// Squash outcomes.
const (
	// SquashMerged means the pull request merged.
	SquashMerged SquashOutcome = "Merged"
	// SquashWaitForChecks waits while CI runs again on the reviewed commit; if
	// the review run ends first, the gated merge refuses it.
	SquashWaitForChecks SquashOutcome = "WaitForChecks"
	// SquashRefused fails the Task: the gated merge refused it.
	SquashRefused SquashOutcome = "Refused"
)

// AfterSquash reads a squash attempt: whether a gated merge waits for checks,
// and why it refused, if it did.
func AfterSquash(checksPending bool, refusal string) SquashOutcome {
	switch {
	case checksPending:
		return SquashWaitForChecks
	case refusal != "":
		return SquashRefused
	}
	return SquashMerged
}

// GatedMergeRefused is the failure of a Standard change whose gated merge was refused.
func GatedMergeRefused(refusal string) TaskEnd {
	return TaskEnd{Phase: TaskFailed, Reason: "GatedMergeRefused", Message: "The gated merge was refused: " + refusal}
}

// ProposeRefusal says why a Task can't propose a Standard change to paths,
// or "" when it can: only a Crew's Task for an issue can.
func ProposeRefusal(issue int, crew bool, paths []string) string {
	switch {
	case issue == 0:
		// The runner proposes for an issue only: a goal has no issue to block.
		return fmt.Sprintf("The change needs a Standard change to %s, but only an issue's Task can propose one",
			QuotePaths(paths))
	case !crew:
		// Only a Crew runs the change and grants its allowance: without one, the
		// Task would wait on it forever.
		return fmt.Sprintf("The change needs a Standard change to %s, but only a Crew's Task can propose one, "+
			"since only a Crew runs it; make the change by hand or leave the issue to a Crew", QuotePaths(paths))
	}
	return ""
}

// Proposed is the progress of a Task whose proposal is filed: it is Blocked
// on the Standard change, and its commit is the proposal's.
func Proposed(p TaskProgress, commit string) TaskProgress {
	p.Commit = commit
	p.Phase = TaskBlocked
	return p
}

// ProposalCount numbers a Task's proposals: earlier is the count of the one
// before, 0 when there was none.
func ProposalCount(earlier int) int { return earlier + 1 }

// QuotePaths lists paths for a message, each in backquotes.
func QuotePaths(paths []string) string {
	return "`" + strings.Join(paths, "`, `") + "`"
}
