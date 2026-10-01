package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Task phases, mirroring the API.
const (
	TaskPending      = "Pending"
	TaskImplementing = "Implementing"
	TaskChecking     = "Checking"
	TaskReviewing    = "Reviewing"
	TaskMerged       = "Merged"
	TaskFailed       = "Failed"
)

// Roles an agent plays in a Task round, as the runner names them.
const (
	RoleImplementer = "implement"
	RoleReviewer    = "review"
)

// Review verdicts.
const (
	VerdictApprove = "approve"
	VerdictChanges = "changes"
)

// maxReviewAttempts is how often a reviewer may fail to give a verdict in one
// round before the Task fails: a broken reviewer is not the implementer's fault,
// so it doesn't cost a round.
const maxReviewAttempts = 3

// RunResult is what one sandbox run reported.
type RunResult struct {
	Commit  string
	Summary string
	Verdict string
	Error   string
}

// RunState is the sandbox for the Task's current phase and round.
type RunState struct {
	// Started means the sandbox exists.
	Started bool
	// Done means its pod has finished.
	Done bool
	// Result is the reported outcome; nil when the pod ended without one.
	Result *RunResult
	// Failure says why a pod ended without a result, such as OOMKilled.
	Failure string
}

// TaskFacts is everything the next step of a Task depends on.
type TaskFacts struct {
	Phase     string
	Container string
	Round     int
	MaxRounds int
	// ContainerReady means the repository is scaffolded.
	ContainerReady bool
	// BlockedBy names an earlier Task on the same Container that is still active.
	BlockedBy string
	Run       RunState
	// Checks is the CI state on the round's commit, and ChecksURL where to see it.
	Checks    string
	ChecksURL string
	// ChecksLog is the failed CI job's log, when it could be read.
	ChecksLog string
	// ReviewAttempts counts reviewer runs without a verdict in this round.
	ReviewAttempts int
	// InPhase is how long the Task has been in its current phase.
	InPhase time.Duration
	// NoFreeSlot means the operator already runs as many agent sandboxes as it may.
	NoFreeSlot bool
}

// ChecksTimeout is how long a Task waits for CI to report before it fails. CI that
// never reports is a broken runner, not the agent's fault, so it costs no round.
const ChecksTimeout = 30 * time.Minute

// TaskAction is what the operator does next.
type TaskAction string

// Task actions.
const (
	ActionNone           TaskAction = "None"
	ActionWait           TaskAction = "Wait"
	ActionStartImplement TaskAction = "StartImplement"
	ActionOpenPR         TaskAction = "OpenPR"
	ActionStartReview    TaskAction = "StartReview"
	ActionRetryReview    TaskAction = "RetryReview"
	ActionMerge          TaskAction = "Merge"
	ActionRetry          TaskAction = "Retry"
	ActionFail           TaskAction = "Fail"
)

// TaskStep is the next action, with the feedback for the next round when the
// action is Retry, and a message for status.
type TaskStep struct {
	Action   TaskAction
	Feedback string
	Message  string
}

// NextTaskStep decides what happens next to a Task. Starting a sandbox waits
// for a free agent slot.
func NextTaskStep(f TaskFacts) TaskStep {
	step := nextTaskStep(f)
	if f.NoFreeSlot && (step.Action == ActionStartImplement || step.Action == ActionStartReview) {
		return TaskStep{Action: ActionWait, Message: "Waiting for a free agent slot"}
	}
	return step
}

func nextTaskStep(f TaskFacts) TaskStep {
	switch f.Phase {
	case "", TaskPending:
		switch {
		case !f.ContainerReady:
			return TaskStep{Action: ActionWait, Message: fmt.Sprintf("Waiting for Container %s to be Ready", f.Container)}
		case f.BlockedBy != "":
			return TaskStep{Action: ActionWait,
				Message: fmt.Sprintf("Waiting for Task %s: one Task works on a Container at a time", f.BlockedBy)}
		}
		return TaskStep{Action: ActionStartImplement, Message: "Starting round 1"}

	case TaskImplementing:
		switch {
		case !f.Run.Started:
			return TaskStep{Action: ActionStartImplement, Message: fmt.Sprintf("Starting round %d", f.Round)}
		case !f.Run.Done:
			return TaskStep{Action: ActionWait, Message: fmt.Sprintf("The agent is working on round %d", f.Round)}
		case f.Run.Result == nil:
			return roundFailed(f, "The agent's sandbox ended without a result: "+orUnknown(f.Run.Failure))
		case f.Run.Result.Error != "":
			return roundFailed(f, f.Run.Result.Error)
		}
		return TaskStep{Action: ActionOpenPR, Message: "The agent pushed " + short(f.Run.Result.Commit)}

	case TaskChecking:
		switch f.Checks {
		case "success":
			return TaskStep{Action: ActionStartReview, Message: "Checks passed; starting the review"}
		case "failure", "error":
			feedback := fmt.Sprintf("The CI checks failed (%s). Run the check command, find the failure and fix it.",
				orUnknown(f.ChecksURL))
			if excerpt := CheckFailures(f.ChecksLog); excerpt != "" {
				feedback += " A test that passes for you but fails in CI is flaky: make it deterministic." +
					"\nWhat failed in CI:\n" + excerpt
			}
			return roundFailed(f, feedback)
		}
		if f.InPhase > ChecksTimeout {
			return TaskStep{Action: ActionFail, Message: fmt.Sprintf(
				"No CI result after %s; check that a runner for the repository is online", ChecksTimeout)}
		}
		return TaskStep{Action: ActionWait, Message: "Waiting for checks"}

	case TaskReviewing:
		switch {
		case !f.Run.Started:
			return TaskStep{Action: ActionStartReview, Message: "Starting the review"}
		case !f.Run.Done:
			return TaskStep{Action: ActionWait, Message: "The reviewer is reviewing"}
		}
		if f.Run.Result == nil || f.Run.Result.Error != "" || f.Run.Result.Verdict == "" {
			reason := f.Run.Failure
			if f.Run.Result != nil {
				reason = f.Run.Result.Error
			}
			if f.ReviewAttempts+1 < maxReviewAttempts {
				return TaskStep{Action: ActionRetryReview, Message: "The reviewer gave no verdict: " + orUnknown(reason)}
			}
			return TaskStep{Action: ActionFail,
				Message: fmt.Sprintf("The reviewer failed %d times: %s", maxReviewAttempts, orUnknown(reason))}
		}
		if f.Run.Result.Verdict == VerdictApprove {
			return TaskStep{Action: ActionMerge, Message: "Approved; merging"}
		}
		return roundFailed(f, "The reviewer asked for changes:\n"+f.Run.Result.Summary)
	}
	return TaskStep{Action: ActionNone}
}

// roundFailed sends the Task into its next round with feedback, or fails it after
// the last one.
func roundFailed(f TaskFacts, feedback string) TaskStep {
	if f.Round < f.MaxRounds {
		return TaskStep{Action: ActionRetry, Feedback: feedback,
			Message: fmt.Sprintf("Round %d was not accepted; starting round %d", f.Round, f.Round+1)}
	}
	return TaskStep{Action: ActionFail, Feedback: feedback,
		Message: fmt.Sprintf("Failed after %d rounds: %s", f.Round, feedback)}
}

// TaskRef is a Task as the queue on its Container sees it.
type TaskRef struct {
	Name string
	// Created orders Tasks; ties go by name.
	Created int64
	// Active means neither Merged nor Failed.
	Active bool
}

// BlockingTask returns the earliest active Task on the Container, when that isn't
// task itself: one Task works on a Container at a time, in creation order.
func BlockingTask(task string, tasks []TaskRef) string {
	var first *TaskRef
	for i := range tasks {
		t := &tasks[i]
		if !t.Active {
			continue
		}
		if first == nil || t.Created < first.Created || (t.Created == first.Created && t.Name < first.Name) {
			first = t
		}
	}
	if first == nil || first.Name == task {
		return ""
	}
	for _, t := range tasks {
		if t.Name == task && !t.Active {
			return ""
		}
	}
	return first.Name
}

// maxUsername is Gitea's limit on user names.
const maxUsername = 40

// AgentUsername is the Git host user an agent works as on one Container.
func AgentUsername(project, container string) string {
	name := project + "-" + container + "-agent"
	if len(name) <= maxUsername {
		return name
	}
	sum := sha256.Sum256([]byte(project + "/" + container))
	suffix := "-" + hex.EncodeToString(sum[:])[:8] + "-agent"
	return strings.TrimRight(name[:maxUsername-len(suffix)], "-") + suffix
}

// SandboxName names the sandbox of one run: task, round, role and, for reviews,
// the attempt. It fits a DNS label.
func SandboxName(task string, round int, role string, attempt int) string {
	suffix := "-r" + strconv.Itoa(round) + "-" + role
	if role == RoleReviewer {
		suffix += "-a" + strconv.Itoa(attempt)
	}
	if len(task)+len(suffix) > 63 {
		sum := sha256.Sum256([]byte(task))
		task = strings.TrimRight(task[:63-len(suffix)-9], "-") + "-" + hex.EncodeToString(sum[:])[:8]
	}
	return task + suffix
}

// TaskBranch is the branch a Task's agent works on.
func TaskBranch(task string) string { return "task/" + task }

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown reason"
	}
	return s
}

// SlotWaiter is a Task waiting for an agent slot, and since when.
type SlotWaiter struct {
	Name  string // namespace/name
	Since time.Time
}

// HasSlotTurn reports whether task may take one of the free agent slots: the
// longest-waiting Tasks go first, ties by name, so a busy Task can't starve the
// others. A Task not waiting yet joins the queue at now.
func HasSlotTurn(task string, now time.Time, waiting []SlotWaiter, free int) bool {
	if free <= 0 {
		return false
	}
	queue := slices.Clone(waiting)
	if !slices.ContainsFunc(queue, func(w SlotWaiter) bool { return w.Name == task }) {
		queue = append(queue, SlotWaiter{Name: task, Since: now})
	}
	slices.SortFunc(queue, func(a, b SlotWaiter) int {
		if c := a.Since.Compare(b.Since); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	for i, w := range queue {
		if w.Name == task {
			return i < free
		}
	}
	return false
}

// maxCheckExcerpt bounds the CI log excerpt sent to the agent.
const maxCheckExcerpt = 4000

// CheckFailures picks the lines of a CI log that say what failed: failed tests,
// analyzer findings, exceptions and make errors, with a little context after a
// failed test. The agent can't open the CI log from its sandbox, so this is what
// it gets instead.
func CheckFailures(log string) string {
	lines := strings.Split(log, "\n")
	var out []string
	size := 0
	add := func(line string) bool {
		if size+len(line)+1 > maxCheckExcerpt {
			out = append(out, "…")
			return false
		}
		out = append(out, line)
		size += len(line) + 1
		return true
	}
	for i := 0; i < len(lines); i++ {
		line := stripLogPrefix(lines[i])
		if !isFailureLine(line) {
			continue
		}
		if !add(line) {
			break
		}
		// A failed test is followed by its exception and expectation.
		if strings.Contains(line, "(failed)") || strings.Contains(line, "EXCEPTION CAUGHT") {
			for j := i + 1; j < len(lines) && j <= i+12; j++ {
				next := stripLogPrefix(lines[j])
				if strings.HasPrefix(next, "✅") || strings.HasPrefix(next, "::endgroup::") {
					break
				}
				if strings.TrimSpace(next) != "" && !add(next) {
					return strings.Join(out, "\n")
				}
				i = j
			}
		}
	}
	return strings.Join(out, "\n")
}

func isFailureLine(line string) bool {
	for _, marker := range []string{"❌", "(failed)", "error •", "warning •", "info •", "make: ***",
		"EXCEPTION CAUGHT", "Expected:", "Actual:", "Some tests failed", "Error:"} {
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}

// stripLogPrefix removes the timestamp a CI runner puts before each line.
func stripLogPrefix(line string) string {
	if t, rest, ok := strings.Cut(line, " "); ok && len(t) > 20 && t[4] == '-' && strings.Contains(t, "T") {
		return rest
	}
	return line
}
