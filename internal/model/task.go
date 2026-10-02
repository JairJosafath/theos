package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
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
	// TaskBlocked waits for a Standard change that the Task's change needs.
	TaskBlocked = "Blocked"
	TaskMerged  = "Merged"
	TaskFailed  = "Failed"
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
	// Proposal is a Standard change the agent asks for, besides its own change.
	Proposal *Proposal
	// Empty means a resumed branch equals the base: the Standard change did it all.
	Empty bool
	// FollowUps are the reviewer's suggestions for later work.
	FollowUps []FollowUp
	// Unavailable means the agent's service couldn't work: a usage limit, an
	// overload or bad credentials. It is not the agent's fault and costs no round.
	Unavailable bool
	// RetryAt is when the service said it is back; zero when it didn't say.
	RetryAt time.Time
}

// FollowUp is work a reviewer suggests for later, outside the change it reviewed:
// an issue for a person to triage.
type FollowUp struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

// MaxFollowUps is how many follow-ups one review may file.
const MaxFollowUps = 3

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
	// Protected means the Container's default branch is protected (see Protected):
	// agents work only where the Git host enforces checks and review.
	Protected bool
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
	// AgentBackIn is how long until an unavailable agent is tried again; zero or
	// less means now.
	AgentBackIn time.Duration
	// Conflicted means the pull request can no longer merge: main moved since
	// the branch was made, with changes that conflict.
	Conflicted bool
	// StandardChanges counts the Standard changes filed so far for the Task.
	StandardChanges int
	// Resumed means this implement run is the first after a Standard change merged.
	Resumed bool
	// Change is the Standard change a Blocked Task waits for.
	Change ChangeFacts
	// Feedback is the round's feedback, status.feedback.
	Feedback string
}

// ChangeFacts is the Standard change a Task waits for.
type ChangeFacts struct {
	Issue  int
	Paths  []string
	Merged bool
	// Failed says why the change will not merge; empty while it still may.
	Failed string
}

// ChangeTask is a Task that works on a Standard change's issue.
type ChangeTask struct {
	Name string
	// Final means the Task has ended, Failed that it ended in failure.
	Final, Failed bool
	Message       string
}

// StandardChangeOutcome reads the Tasks on a Standard change's issue. Any merged
// Task means the change merged; any active one means it is pending, even when the
// issue is closed, since the gated merge closes the issue in the same reconcile
// that sets Merged. Otherwise the last failed Task fails it, and a closed issue
// with no Task at all was closed without a merge.
func StandardChangeOutcome(issue int, tasks []ChangeTask, issueClosed bool) (merged bool, failed string) {
	var last *ChangeTask
	active := false
	for i, t := range tasks {
		switch {
		case t.Final && !t.Failed:
			return true, ""
		case !t.Final:
			active = true
		default:
			last = &tasks[i]
		}
	}
	switch {
	case active:
		return false, ""
	case last != nil:
		return false, fmt.Sprintf("Task %s failed: %s", last.Name, last.Message)
	case issueClosed:
		return false, fmt.Sprintf("issue #%d was closed without a merge", issue)
	}
	return false, ""
}

// ResumeFeedback is what the implementer is told when its Standard change merged:
// carry on with the change, now that the protected files are in main.
func ResumeFeedback(issue int, paths []string, earlier string) string {
	out := fmt.Sprintf("Standard change #%d (%s) has merged into main. Merge main into your branch and "+
		"finish the change; do not edit those files again.", issue, joinAnd(paths))
	if earlier != "" {
		out += "\n\n" + earlier
	}
	return out
}

// GatedMergeRefusal says why a Standard change's pull request may not merge, or ""
// when it may: CI is green, the reviewer approved, and the change is not empty and
// stays inside the allowed paths.
func GatedMergeRefusal(checks, verdict string, changed, allowed []string) string {
	if checks != "success" {
		return fmt.Sprintf("CI is %s, not success", orUnknown(checks))
	}
	if verdict != VerdictApprove {
		return fmt.Sprintf("the reviewer's verdict is %q, not %q", verdict, VerdictApprove)
	}
	if len(changed) == 0 {
		return "the change changes nothing"
	}
	var outside []string
	for _, p := range changed {
		if !coveredByAny(allowed, p) {
			outside = append(outside, p)
		}
	}
	if len(outside) > 0 {
		return fmt.Sprintf("the change touches %s, outside the allowed paths", joinAnd(outside))
	}
	return ""
}

// ConflictFeedback is what the implementer is told when its approved change no
// longer merges.
const ConflictFeedback = "Your change was approved, but another change merged into main first and now " +
	"conflicts with yours. Merge main into your branch, resolve every conflict so both changes keep " +
	"working, and make sure the check command passes. Change nothing else."

// AgentRetryDelay is how long a Task waits for an unavailable agent that didn't
// say when it is back.
const AgentRetryDelay = 15 * time.Minute

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
	// ActionPropose files the agent's proposal as a Standard change and blocks the Task.
	ActionPropose TaskAction = "Propose"
	// ActionResume puts a Blocked Task back to work once its Standard change merged.
	ActionResume TaskAction = "Resume"
	// ActionDoneByStandardChange ends a resumed Task whose branch has nothing
	// beyond main: the Standard change did the whole issue.
	ActionDoneByStandardChange TaskAction = "DoneByStandardChange"
	// ActionResolveConflicts sends an approved change back to the implementer, in
	// the same round, to merge main and resolve the conflicts.
	ActionResolveConflicts TaskAction = "ResolveConflicts"
	// ActionAgentUnavailable ends the run and tries the same step again later.
	ActionAgentUnavailable TaskAction = "AgentUnavailable"
	ActionFail             TaskAction = "Fail"
)

// TaskStep is the next action, with the feedback for the next round when the
// action is Retry, and a message for status.
type TaskStep struct {
	Action   TaskAction
	Feedback string
	Message  string
	// SlotWait means the Task would start a sandbox but waits for a free slot;
	// only such Tasks queue for one (see HasSlotTurn).
	SlotWait bool
	// RetryAt is when to try an unavailable agent again; zero means the default delay.
	RetryAt time.Time
}

// NextTaskStep decides what happens next to a Task. Starting a sandbox waits
// for a free agent slot.
func NextTaskStep(f TaskFacts) TaskStep {
	step := nextTaskStep(f)
	starts := step.Action == ActionStartImplement || step.Action == ActionStartReview
	if starts && f.AgentBackIn > 0 {
		return TaskStep{Action: ActionWait, Message: fmt.Sprintf("The agent is unavailable; trying again in %s",
			f.AgentBackIn.Round(time.Second))}
	}
	if f.NoFreeSlot && (step.Action == ActionStartImplement || step.Action == ActionStartReview) {
		return TaskStep{Action: ActionWait, Message: "Waiting for a free agent slot", SlotWait: true}
	}
	return step
}

func nextTaskStep(f TaskFacts) TaskStep {
	switch f.Phase {
	case "", TaskPending:
		switch {
		case !f.Protected:
			return TaskStep{Action: ActionFail, Message: fmt.Sprintf("Container %s has no Standard, so its default "+
				"branch has no checks to protect it; agents work only on protected repositories", f.Container)}
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
		case f.Run.Result.Unavailable:
			return agentUnavailable(f)
		case f.Run.Result.Error != "":
			return roundFailed(f, f.Run.Result.Error)
		}
		return implementedStep(f)

	case TaskBlocked:
		return blockedStep(f)

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
		case f.Run.Result != nil && f.Run.Result.Unavailable:
			return agentUnavailable(f)
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
		if f.Run.Result.Verdict == VerdictApprove && f.Conflicted {
			// Not the agent's fault: another change merged first. It costs no round.
			return TaskStep{Action: ActionResolveConflicts, Feedback: ConflictFeedback, Message: fmt.Sprintf(
				"Approved, but main moved and the pull request conflicts with it; round %d resolves them", f.Round)}
		}
		if f.Run.Result.Verdict == VerdictApprove {
			return TaskStep{Action: ActionMerge, Message: "Approved; merging"}
		}
		return roundFailed(f, "The reviewer asked for changes:\n"+f.Run.Result.Summary)
	}
	return TaskStep{Action: ActionNone}
}

// implementedStep decides what follows a finished implement run that reported
// neither an error nor an outage.
func implementedStep(f TaskFacts) TaskStep {
	res := f.Run.Result
	if p := res.Proposal; p != nil {
		if f.StandardChanges >= MaxStandardChanges {
			return TaskStep{Action: ActionFail, Message: fmt.Sprintf("The change needs a third Standard change (%s); "+
				"more than two Standard changes need a person", joinAnd(p.Paths))}
		}
		return TaskStep{Action: ActionPropose,
			Message: fmt.Sprintf("The change needs a Standard change to %s; filing it", joinAnd(p.Paths))}
	}
	if f.Resumed && res.Empty {
		return TaskStep{Action: ActionDoneByStandardChange, Message: fmt.Sprintf(
			"Standard change #%d did the whole issue: the branch has nothing beyond main", f.Change.Issue)}
	}
	return TaskStep{Action: ActionOpenPR, Message: "The agent pushed " + short(res.Commit)}
}

// blockedStep waits for the Standard change a Task needs, then resumes or fails it.
func blockedStep(f TaskFacts) TaskStep {
	c := f.Change
	switch {
	case c.Merged:
		return TaskStep{Action: ActionResume, Feedback: ResumeFeedback(c.Issue, c.Paths, f.Feedback),
			Message: fmt.Sprintf("Standard change #%d merged; round %d goes on", c.Issue, f.Round)}
	case c.Failed != "":
		return TaskStep{Action: ActionFail,
			Message: fmt.Sprintf("Standard change #%d will not merge: %s", c.Issue, c.Failed)}
	}
	return TaskStep{Action: ActionWait,
		Message: fmt.Sprintf("Waiting for Standard change #%d (%s)", c.Issue, joinAnd(c.Paths))}
}

// agentUnavailable keeps the round and waits for the agent's service to be back.
func agentUnavailable(f TaskFacts) TaskStep {
	return TaskStep{Action: ActionAgentUnavailable, RetryAt: f.Run.Result.RetryAt, Message: fmt.Sprintf(
		"The agent is unavailable (%s); round %d waits for it", gist(f.Run.Result.Error), f.Round)}
}

// roundFailed sends the Task into its next round with feedback, or fails it after
// the last one.
func roundFailed(f TaskFacts, feedback string) TaskStep {
	if f.Round < f.MaxRounds {
		return TaskStep{Action: ActionRetry, Feedback: feedback,
			Message: fmt.Sprintf("Round %d was not accepted (%s); starting round %d", f.Round, gist(feedback), f.Round+1)}
	}
	return TaskStep{Action: ActionFail, Feedback: feedback,
		Message: fmt.Sprintf("Failed after %d rounds: %s", f.Round, feedback)}
}

// gist is the start of some feedback, for a one-line message: its first line,
// joined with the next when it only introduces it ("The reviewer asked for
// changes:"), at most 120 characters.
func gist(feedback string) string {
	var lines []string
	for line := range strings.SplitSeq(feedback, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return "no reason given"
	}
	out := lines[0]
	if strings.HasSuffix(out, ":") && len(lines) > 1 {
		out += " " + lines[1]
	}
	if runes := []rune(out); len(runes) > 120 {
		out = string(runes[:119]) + "…"
	}
	return out
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

// CheckFailures picks the lines of a CI log that say what failed: failed tests
// (Flutter, Go, Vitest, pytest), compiler, vet and linter findings, panics, data
// races and make errors, with the lines after a failed test or a panic that
// explain it. The agent can't open the CI log from its sandbox, so this is what
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
		if !startsBlock(line) {
			continue
		}
		for j := i + 1; j < len(lines) && j <= i+12; j++ {
			next := stripLogPrefix(lines[j])
			if endsBlock(next) || startsBlock(next) {
				break
			}
			if strings.TrimSpace(next) != "" && !add(next) {
				return strings.Join(out, "\n")
			}
			i = j
		}
	}
	return strings.Join(out, "\n")
}

var (
	// sourceFinding is a compiler, vet or linter finding: path:line[:column]: message.
	sourceFinding = regexp.MustCompile(`^\S+\.(go|dart|ts|tsx|js|mjs|py|rs):\d+(:\d+)?: `)
	// eslintFinding is a finding under ESLint's file heading: line:column error message.
	eslintFinding = regexp.MustCompile(`^\s+\d+:\d+\s+(error|warning)\s`)
)

func isFailureLine(line string) bool {
	for _, marker := range []string{"❌", "(failed)", "error •", "warning •", "info •", "make: ***",
		"EXCEPTION CAUGHT", "Expected:", "Actual:", "Some tests failed", "Error:",
		"--- FAIL:", "WARNING: DATA RACE", "error TS"} {
		if strings.Contains(line, marker) {
			return true
		}
	}
	trimmed := strings.TrimSpace(line)
	for _, prefix := range []string{"FAIL\t", "panic:", "FAIL ", "× ", "FAILED ", "ERROR ", "E   "} {
		if strings.HasPrefix(trimmed, prefix) || strings.HasPrefix(line, prefix) {
			return true
		}
	}
	if strings.HasPrefix(trimmed, "✖ ") && strings.Contains(trimmed, "problem") {
		return true
	}
	if (strings.HasPrefix(trimmed, "Test Files ") || strings.HasPrefix(trimmed, "Tests ")) &&
		strings.Contains(trimmed, "failed") {
		return true
	}
	return sourceFinding.MatchString(line) || eslintFinding.MatchString(line)
}

// startsBlock reports whether the lines after a failure line explain it: a
// Flutter test's exception, a Go test's log or a panic's stack, a Vitest assertion.
func startsBlock(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.Contains(line, "(failed)") || strings.Contains(line, "EXCEPTION CAUGHT") ||
		strings.HasPrefix(trimmed, "--- FAIL:") || strings.HasPrefix(trimmed, "panic:") ||
		strings.HasPrefix(trimmed, "WARNING: DATA RACE") || strings.HasPrefix(trimmed, "FAIL  ")
}

// endsBlock reports whether a line closes the explanation of a failure: the next
// test, a passing package or the runner closing a group.
func endsBlock(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "FAIL" || trimmed == "PASS" {
		return true
	}
	for _, prefix := range []string{"✅", "::endgroup::", "=== ", "--- PASS", "--- SKIP", "ok  ", "FAIL\t", "⎯"} {
		if strings.HasPrefix(trimmed, prefix) {
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
