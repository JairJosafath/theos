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
	// RolePlanner is a Plan Task's run: the planner writes a Brief round's plan.
	RolePlanner = "plan"
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
	// Extra are the unprotected paths a Standard change changes besides its
	// allowed ones (StandardChangeExtras).
	Extra []string
	// Empty means a resumed branch equals the base: the Standard change did it all.
	Empty bool
	// Blocked is the agent's blocked note (BlockedNote): it can't start the
	// issue, and says why. The Task fails at once.
	Blocked string
	// FollowUps are the reviewer's suggestions for later work.
	FollowUps []FollowUp
	// Open are the criteria an approved review accepted as left open: they need
	// what no agent's sandbox has, so a person does them.
	Open []OpenCriterion
	// Unavailable means the agent's service couldn't work: a usage limit, an
	// overload, a temporary server error or bad credentials. It is not the agent's fault and costs no round.
	Unavailable bool
	// RetryAt is when the service said it is back; zero when it didn't say.
	RetryAt time.Time
	// CostMilliUSD is what the run cost, in thousandths of a US dollar, when
	// CostReported; the agent didn't say otherwise.
	CostMilliUSD int64
	CostReported bool
	// Tokens is what the run's agent sessions used; nil when the agent didn't
	// say.
	Tokens *TokenUsage
}

// FollowUp is work a reviewer suggests for later, outside the change it reviewed:
// an issue for a person to triage. Its Criteria become the issue's "Acceptance
// criteria" checklist, so a person who moves it into a release can ready it as it
// stands.
type FollowUp struct {
	Title    string   `json:"title"`
	Body     string   `json:"body,omitempty"`
	Criteria []string `json:"criteria,omitempty"`
}

// OpenCriterion is an acceptance criterion the implementer left open, and the
// reviewer accepted as such, because it needs what no agent's sandbox has (a
// person's desktop or device, an account, a purchase). Why says what is missing.
// An approval carries at most MaxFollowUpCriteria of them.
type OpenCriterion struct {
	Criterion string `json:"criterion"`
	Why       string `json:"why,omitempty"`
}

// MaxFollowUps is how many follow-ups one review may file, and
// MaxFollowUpCriteria how many acceptance criteria each may carry.
const (
	MaxFollowUps        = 3
	MaxFollowUpCriteria = 3
)

// FollowUpIssue renders the body of the issue filed for a reviewer's follow-up:
// its text, its criteria as an "Acceptance criteria" checklist, and where it came
// from. by names the reviewer, review the pull request it reviewed for issue in
// Task task.
func FollowUpIssue(f FollowUp, by, review string, issue int, task string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(f.Body))
	if strings.Count(f.Body, "```")%2 == 1 {
		// A body cut short inside a code block would swallow the checklist.
		b.WriteString("\n```")
	}
	if len(f.Criteria) > 0 {
		b.WriteString("\n\n## Acceptance criteria\n")
		for _, c := range f.Criteria {
			fmt.Fprintf(&b, "\n- [ ] %s", strings.Join(strings.Fields(c), " "))
		}
	}
	fmt.Fprintf(&b, "\n\n---\nSuggested by %s while reviewing %s for #%d (Task `%s`). ", by, review, issue, task)
	if len(f.Criteria) > 0 {
		fmt.Fprintf(&b, "To have agents do it, check the criteria, move it into a release and label it %s.", LabelReady)
	} else {
		fmt.Fprintf(&b, "To have agents do it, add an \"Acceptance criteria\" checklist and label it %s.", LabelReady)
	}
	return strings.TrimLeft(b.String(), "\n")
}

// RunState is the sandbox for the Task's current phase and round.
type RunState struct {
	// Started means the sandbox exists.
	Started bool
	// Pod is the UID of the sandbox's pod the run is on now, if any.
	Pod string
	// Done means its pod has finished.
	Done bool
	// Result is the reported outcome; nil when the pod ended without one.
	Result *RunResult
	// Failure says why a pod ended without a result, such as OOMKilled.
	Failure string
	// Interrupted means the infrastructure ended the run (a node shutdown or
	// restart, an eviction, a preemption, a cluster outage), not the agent: an
	// out-of-memory kill is the agent's.
	Interrupted bool
}

// Cost is what a run adds to its Task's cost: what its agent reported, or one
// run without a cost when the agent ran and didn't say. A sandbox that is gone
// adds nothing; whoever ended it counted it.
func (s RunState) Cost() (milliUSD int64, withoutCost int32) {
	switch {
	case !s.Started:
		return 0, 0
	case s.Result != nil && s.Result.CostReported:
		return s.Result.CostMilliUSD, 0
	default:
		return 0, 1
	}
}

// ShutdownSlack is how late Agent Sandbox may notice a sandbox's shutdown time
// while the cluster runs; it notices within seconds.
const ShutdownSlack = 2 * time.Minute

// MissedShutdown reports whether Agent Sandbox noticed a sandbox's shutdown time
// so late that the cluster was down at it. Its pod's deadline comes earlier and
// ends a run that takes too long while the operator watches, so a pod lost in
// that outage didn't run out its time: the outage ended it, not the agent. One
// that did, its pod ended at the deadline while only the control plane was down
// and deleted before the operator saw it, gets a free rerun too; MaxReruns caps
// that.
func MissedShutdown(shutdown, noticed time.Time) bool {
	return !shutdown.IsZero() && !noticed.IsZero() && noticed.Sub(shutdown) > ShutdownSlack
}

// DeadlineSlack is how long past a pod's deadline the kubelet may end it while
// its node runs: it notices within seconds and gives the container its grace
// period, 30 seconds, to stop.
const DeadlineSlack = 2 * time.Minute

// EndedAtDeadline reports whether a pod Kubernetes marked DeadlineExceeded was
// ended at its deadline, so the run took too long: its container finished
// between the deadline and DeadlineSlack later. One that finished earlier died
// with its node, and was marked once the node came back past the deadline; one
// that finished well after it was ended when the node came back from an outage
// across the deadline. Either way the outage ended the run, not the agent.
// Without both times it can't tell, and the run took too long.
func EndedAtDeadline(deadline, finished time.Time) bool {
	if deadline.IsZero() || finished.IsZero() {
		return true
	}
	return !finished.Before(deadline) && finished.Sub(deadline) <= DeadlineSlack
}

// MaxReruns is how often a round's runs are rerun for free after the
// infrastructure interrupted them. The cap keeps a run that is interrupted every
// time, which may be the run's own doing, from looping forever.
const MaxReruns = 2

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
	// Reruns counts the free reruns of interrupted runs in this round.
	Reruns int
	// InPhase is how long the Task has been in its current phase.
	InPhase time.Duration
	// NoFreeSlot means the operator already runs as many agent sandboxes as it may.
	NoFreeSlot bool
	// AgentBackIn is how long until an unavailable agent is tried again; zero or
	// less means now.
	AgentBackIn time.Duration
	// Unmergeable means the Git host doesn't say the pull request merges. It is
	// no conflict by itself: the host says so too while it tests the pull
	// request against a main that just moved. Only merging main into the
	// branch tells a conflict.
	Unmergeable bool
	// Behind means main moved since the pull request's branch last took it in:
	// its checks ran without what merged since.
	Behind bool
	// MergeWhenChecksPass means the approved change's branch took in main, and
	// its new head merges once its checks pass, with no new review.
	MergeWhenChecksPass bool
	// MergeWaitsForChecks means the reviewer approved a Standard change whose
	// gated merge waits for CI, which runs again on the reviewed commit.
	MergeWaitsForChecks bool
	// StandardChanges counts the Standard changes filed so far for the Task.
	StandardChanges int
	// Resumed means this implement run is in the round the Task resumed in after
	// its Standard change merged.
	Resumed bool
	// Change is the Standard change the Task filed last. Its issue and paths come
	// from status in every phase: the resumed run's DoneByStandardChange names it.
	// Merged and Failed are read only while the Task is Blocked on it.
	Change ChangeFacts
	// Feedback is the round's feedback, status.feedback.
	Feedback string
	// Plan means a Plan Task: the planner writes a Brief round's plan, and the
	// Brief checks it. It has no Container, protection, queue, CI or review.
	Plan bool
	// PlanChecked means the Brief gave its verdict on the round's commit, and
	// PlanRefusals are its reasons when it refused the plan.
	PlanChecked  bool
	PlanRefusals string
}

// ChangeFacts is a Standard change a Task filed.
type ChangeFacts struct {
	Issue  int
	Paths  []string
	Merged bool
	// Failed says why the change will not merge; empty while it still may.
	// Blocked means its Task failed blocked, as nothing more rounds can fix.
	Failed  string
	Blocked bool
}

// ChangeTask is a Task that works on a Standard change's issue.
type ChangeTask struct {
	Name string
	// Final means the Task has ended, Failed that it ended in failure, and
	// Blocked that it failed reason ReasonBlocked.
	Final, Failed, Blocked bool
	Message                string
}

// StandardChangeOutcome reads the Tasks on a Standard change's issue. Any merged
// Task means the change merged; any active one means it is pending, even when the
// issue is closed, since the gated merge closes the issue in the same reconcile
// that sets Merged. Otherwise the last failed Task fails it, and a closed issue
// with no Task at all was closed without a merge. blocked tells that the last
// failed Task failed blocked.
func StandardChangeOutcome(issue int, tasks []ChangeTask, issueClosed bool) (merged bool, failed string, blocked bool) {
	var last *ChangeTask
	active := false
	for i, t := range tasks {
		switch {
		case t.Final && !t.Failed:
			return true, "", false
		case !t.Final:
			active = true
		default:
			last = &tasks[i]
		}
	}
	switch {
	case active:
		return false, "", false
	case last != nil:
		return false, fmt.Sprintf("Task %s failed: %s", last.Name, last.Message), last.Blocked
	case issueClosed:
		return false, fmt.Sprintf("issue #%d was closed without a merge", issue), false
	}
	return false, "", false
}

// ResumeFeedback is what the implementer is told when its Standard change merged:
// carry on with the change, now that the protected files are in main. The
// earlier feedback of the round follows.
func ResumeFeedback(issue int, paths []string, earlier string) string {
	out := fmt.Sprintf("Standard change #%d (%s) has merged into main. Merge main into your branch and "+
		"finish the change. Those files are in main now; changing them again takes another Standard change.",
		issue, joinAnd(paths))
	if earlier != "" {
		out += "\n\n" + earlier
	}
	return out
}

// GatedMergeRefusal says why a Standard change's pull request may not merge, or ""
// when it may: CI is green, every check main's protection requires passed (the
// merge overrides that protection, so it must not be laxer), the reviewer
// approved, and the change changes an allowed path and no protected path
// outside the allowed ones, case ignored. contexts is the state of each check on the commit by its context.
func GatedMergeRefusal(checks string, contexts map[string]string, verdict string, changed, allowed,
	protected []string) string {
	if refusal := checksRefusal(checks, contexts); refusal != "" {
		return refusal
	}
	if verdict != VerdictApprove {
		return fmt.Sprintf("the reviewer's verdict is %q, not %q", verdict, VerdictApprove)
	}
	if len(changed) == 0 {
		return "the change changes nothing"
	}
	var outside []string
	inside := false
	for _, p := range changed {
		switch {
		case coveredByAny(allowed, p):
			inside = true
		case coveredFold(protected, p):
			outside = append(outside, p)
		}
	}
	if !inside {
		return fmt.Sprintf("the change touches none of %s, the paths it is for", joinAnd(allowed))
	}
	if len(outside) > 0 {
		return fmt.Sprintf("the change touches %s, protected and outside the allowed paths", joinAnd(outside))
	}
	return ""
}

// checksRefusal says why a commit's checks don't let a merge that overrides
// main's protection through, or "" when CI is green and every check main
// requires passed.
func checksRefusal(checks string, contexts map[string]string) string {
	if checks != checkSuccess {
		return fmt.Sprintf("CI is %s, not success", orUnknown(checks))
	}
	if missing := UnpassedChecks(contexts); len(missing) > 0 {
		for i, m := range missing {
			missing[i] = strconv.Quote(m)
		}
		return fmt.Sprintf("no passing check matches %s, which main requires", joinAnd(missing))
	}
	return ""
}

// ConflictFeedback is what the implementer is told when its approved change no
// longer merges.
const ConflictFeedback = "Your change was approved, but another change merged into main first and now " +
	"conflicts with yours. Merge main into your branch, resolve every conflict so both changes keep " +
	"working, and make sure the check command passes. Change nothing else."

// updatedChecksFeedback is what the implementer is told when its approved
// change's checks fail once main is merged into its branch.
const updatedChecksFeedback = "Your change was approved, but main moved since its checks ran, and with main " +
	"merged into your branch the CI checks fail (%s). Pull your branch, run the check command, and fix the " +
	"failure so both your change and what merged into main keep working."

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
	// ActionUpdateBranch merges main into an approved change's branch, as main
	// moved since its checks ran, and waits for the checks again: no agent runs,
	// and it costs no round.
	ActionUpdateBranch TaskAction = "UpdateBranch"
	// ActionAgentUnavailable ends the run and tries the same step again later.
	ActionAgentUnavailable TaskAction = "AgentUnavailable"
	// ActionRerun ends a run the infrastructure interrupted and runs the same
	// step again, in the same round.
	ActionRerun TaskAction = "Rerun"
	// ActionRefuseMerge fails a Standard change whose gated merge is refused, and
	// hands its issue back.
	ActionRefuseMerge TaskAction = "RefuseMerge"
	// ActionAwaitPlanCheck records the plan a planner pushed and waits for the
	// Brief's verdict on it.
	ActionAwaitPlanCheck TaskAction = "AwaitPlanCheck"
	// ActionPlanAccepted ends a Plan Task whose plan the Brief accepted.
	ActionPlanAccepted TaskAction = "PlanAccepted"
	ActionFail         TaskAction = "Fail"
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
	// Recheck means the wait ends on the Git host, which sends no event: the
	// Task looks again after a while.
	Recheck bool
	// RetryAt is when to try an unavailable agent again; zero means the default delay.
	RetryAt time.Time
	// Reason is a failed Task's reason, when not ReasonFailed.
	Reason string
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
	if f.Plan {
		return planStep(f)
	}
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
		return implementingStep(f)

	case TaskBlocked:
		return blockedStep(f)

	case TaskChecking:
		switch f.Checks {
		case checkSuccess:
			if f.MergeWhenChecksPass {
				return approvedStep(f, "Checks passed on the branch with main merged in; merging")
			}
			return TaskStep{Action: ActionStartReview, Message: "Checks passed; starting the review"}
		case "failure", "error":
			feedback := fmt.Sprintf("The CI checks failed (%s). Run the check command, find the failure and fix it.",
				orUnknown(f.ChecksURL))
			if f.MergeWhenChecksPass {
				feedback = fmt.Sprintf(updatedChecksFeedback, orUnknown(f.ChecksURL))
			}
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
		return reviewingStep(f)
	}
	return TaskStep{Action: ActionNone}
}

// planStep decides what happens next to a Plan Task: the planner pushes a
// plan, and the Brief checks it. A refused plan costs a round, as a refused
// change does.
func planStep(f TaskFacts) TaskStep {
	switch f.Phase {
	case "", TaskPending:
		return TaskStep{Action: ActionStartImplement, Message: "Starting round 1"}
	case TaskImplementing:
		return implementingStep(f)
	case TaskChecking:
		switch {
		case !f.PlanChecked:
			return TaskStep{Action: ActionWait, Message: "Waiting for the Brief to check the plan"}
		case f.PlanRefusals != "":
			return roundFailed(f, "The Brief refused the plan:\n"+f.PlanRefusals)
		}
		return TaskStep{Action: ActionPlanAccepted, Message: "The Brief accepted the plan"}
	}
	return TaskStep{Action: ActionNone}
}

// planPushedStep decides what follows a finished planner run that reported
// neither an error nor an outage.
func planPushedStep(f TaskFacts) TaskStep {
	if f.Run.Result.Commit == "" {
		return roundFailed(f, "The planner's run pushed no plan")
	}
	return TaskStep{Action: ActionAwaitPlanCheck, Message: "The planner pushed " + short(f.Run.Result.Commit) +
		"; waiting for the Brief to check the plan"}
}

// implementingStep decides what follows in the Implementing phase.
func implementingStep(f TaskFacts) TaskStep {
	switch {
	case !f.Run.Started:
		return TaskStep{Action: ActionStartImplement, Message: fmt.Sprintf("Starting round %d", f.Round)}
	case !f.Run.Done:
		return TaskStep{Action: ActionWait, Message: fmt.Sprintf("The agent is working on round %d", f.Round)}
	case interrupted(f.Run) && f.Reruns < MaxReruns:
		return rerun(f)
	case f.Run.Result == nil:
		return roundFailed(f, "The agent's sandbox ended without a result: "+orUnknown(f.Run.Failure))
	case f.Run.Result.Unavailable:
		return agentUnavailable(f)
	case f.Run.Result.Blocked != "" && !f.Plan:
		// The agent says the issue can't start yet: more rounds won't change that.
		return TaskStep{Action: ActionFail, Reason: ReasonBlocked, Message: BlockedMessage(f.Run.Result.Blocked)}
	case IsScopeRefusal(f.Run.Result.Error) && IsScopeRefusal(f.Feedback):
		// Turned back for scope twice in a row: the change can't be made here.
		return TaskStep{Action: ActionFail, Reason: ReasonBlocked, Feedback: f.Run.Result.Error,
			Message: ScopeBlockedMessage(f.Run.Result.Error)}
	case f.Run.Result.Error != "":
		return roundFailed(f, f.Run.Result.Error)
	case f.Plan:
		return planPushedStep(f)
	}
	return implementedStep(f)
}

// reviewingStep decides what follows in the Reviewing phase.
func reviewingStep(f TaskFacts) TaskStep {
	switch {
	case !f.Run.Started:
		return TaskStep{Action: ActionStartReview, Message: "Starting the review"}
	case !f.Run.Done:
		return TaskStep{Action: ActionWait, Message: "The reviewer is reviewing"}
	case interrupted(f.Run) && f.Reruns < MaxReruns:
		return rerun(f)
	case f.Run.Result != nil && f.Run.Result.Unavailable:
		return agentUnavailable(f)
	}
	if f.Run.Result == nil || f.Run.Result.Error != "" || f.Run.Result.Verdict == "" {
		return noVerdictStep(f)
	}
	if f.Run.Result.Verdict == VerdictApprove {
		return approvedStep(f, "Approved; merging")
	}
	return roundFailed(f, "The reviewer asked for changes:\n"+f.Run.Result.Summary)
}

// ConflictStep sends an approved change that conflicts with main back to its
// implementer, in the same round.
func ConflictStep(round int) TaskStep {
	return TaskStep{Action: ActionResolveConflicts, Feedback: ConflictFeedback, Message: fmt.Sprintf(
		"Approved, but main moved and the pull request conflicts with it; round %d resolves them", round)}
}

// approvedStep decides what follows an approval: a merge, unless main moved
// since the change's checks ran. Then the change takes in main and is checked
// again first, or, when main conflicts with it, goes back to its implementer
// (ActionUpdateBranch tells which). Neither is the agent's fault: another change
// merged first. Neither costs a round. A branch that has main's head can't
// conflict with it: while the Git host doesn't yet say it merges, it waits.
func approvedStep(f TaskFacts, merging string) TaskStep {
	switch {
	case f.Behind:
		return TaskStep{Action: ActionUpdateBranch, Message: fmt.Sprintf(
			"Approved, but main moved since the checks ran; merging main into the branch and checking it again "+
				"in round %d", f.Round)}
	case f.Unmergeable:
		return TaskStep{Action: ActionWait, Recheck: true,
			Message: "Approved; waiting for the Git host to find that the pull request merges"}
	}
	return TaskStep{Action: ActionMerge, Message: merging}
}

// noVerdictStep decides what follows a review run that ended without a verdict:
// another run, up to maxReviewAttempts. A run that ends while the approved
// change's merge waits for CI, its sandbox expired, can no longer vouch for the
// change: the merge is refused, and no reviewer run is spent on a CI wait.
func noVerdictStep(f TaskFacts) TaskStep {
	reason := f.Run.Failure
	if f.Run.Result != nil {
		reason = f.Run.Result.Error
	}
	if f.MergeWaitsForChecks {
		return TaskStep{Action: ActionRefuseMerge, Message: fmt.Sprintf(
			"CI is still pending on the reviewed commit, and the review run that approved it ended (%s)",
			orUnknown(reason))}
	}
	if f.ReviewAttempts+1 < maxReviewAttempts {
		return TaskStep{Action: ActionRetryReview, Message: "The reviewer gave no verdict: " + orUnknown(reason)}
	}
	return TaskStep{Action: ActionFail,
		Message: fmt.Sprintf("The reviewer failed %d times: %s", maxReviewAttempts, orUnknown(reason))}
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
	msg := "The agent pushed " + short(res.Commit)
	if len(res.Extra) > 0 {
		msg += fmt.Sprintf("; besides its Standard change's files it changes %s, which no Standard protects",
			joinAnd(res.Extra))
	}
	return TaskStep{Action: ActionOpenPR, Message: msg}
}

// blockedStep waits for the Standard change a Task needs, then resumes or fails it.
func blockedStep(f TaskFacts) TaskStep {
	c := f.Change
	switch {
	case c.Merged:
		return TaskStep{Action: ActionResume, Feedback: ResumeFeedback(c.Issue, c.Paths, f.Feedback),
			Message: fmt.Sprintf("Standard change #%d merged; round %d goes on", c.Issue, f.Round)}
	case c.Failed != "":
		// A change that failed blocked holds this Task too: say so, not that it failed.
		step := TaskStep{Action: ActionFail,
			Message: fmt.Sprintf("Standard change #%d will not merge: %s", c.Issue, c.Failed)}
		if c.Blocked {
			step.Reason = ReasonBlocked
		}
		return step
	}
	return TaskStep{Action: ActionWait,
		Message: fmt.Sprintf("Waiting for Standard change #%d (%s)", c.Issue, joinAnd(c.Paths))}
}

// agentUnavailable keeps the round and waits for the agent's service to be back.
func agentUnavailable(f TaskFacts) TaskStep {
	return TaskStep{Action: ActionAgentUnavailable, RetryAt: f.Run.Result.RetryAt, Message: fmt.Sprintf(
		"The agent is unavailable (%s); round %d waits for it", gist(f.Run.Result.Error), f.Round)}
}

// interrupted reports whether the infrastructure ended a run before it finished:
// a run that reported its outcome, or an unavailable agent, is not rerun however
// its pod ended.
func interrupted(run RunState) bool {
	return run.Interrupted && (run.Result == nil || run.Result.Error != "" && !run.Result.Unavailable)
}

// rerun runs the step of an interrupted run again, without spending the round.
func rerun(f TaskFacts) TaskStep {
	reason := f.Run.Failure
	if f.Run.Result != nil {
		reason = f.Run.Result.Error
	}
	return TaskStep{Action: ActionRerun, Message: fmt.Sprintf(
		"The run was interrupted (%s); round %d runs again (rerun %d of %d)",
		gist(orUnknown(reason)), f.Round, f.Reruns+1, MaxReruns)}
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

// SandboxName names the sandbox of one run: task, round, role, for reviews the
// attempt, and the rerun after an interruption, if any. A rerun gets a name of
// its own, so the sandbox it replaces, still being deleted, is never read as its
// own. It fits a DNS label.
func SandboxName(task string, round int, role string, attempt, rerun int) string {
	suffix := "-r" + strconv.Itoa(round) + "-" + role
	if role == RoleReviewer {
		suffix += "-a" + strconv.Itoa(attempt)
	}
	if rerun > 0 {
		suffix += "-x" + strconv.Itoa(rerun)
	}
	if len(task)+len(suffix) > 63 {
		sum := sha256.Sum256([]byte(task))
		task = strings.TrimRight(task[:63-len(suffix)-9], "-") + "-" + hex.EncodeToString(sum[:])[:8]
	}
	return task + suffix
}

// TaskBranch is the branch a Task's agent works on, unless it continues an
// earlier try's pull request (EarlierTry).
func TaskBranch(task string) string { return "task/" + task }

// Try is a Task on an issue, as a later try on the same issue sees it.
type Try struct {
	Name  string
	Issue int
	// Created orders tries; ties go by name.
	Created int64
	// Failed means the Task ended Failed.
	Failed bool
	// PullRequest is the pull request the Task opened; 0 when none.
	PullRequest int
}

// EarlierTry returns the try whose pull request a new Task on issue continues:
// the newest other Failed Task on the issue, created before it, that opened a
// pull request. Whether that pull request is still open is for the Git host to
// say. ok is false when there is none.
func EarlierTry(task string, created int64, issue int, tries []Try) (Try, bool) {
	var best Try
	found := false
	for _, t := range tries {
		before := t.Created < created || t.Created == created && t.Name < task
		if t.Name == task || t.Issue != issue || issue == 0 || !t.Failed || t.PullRequest == 0 || !before {
			continue
		}
		if !found || t.Created > best.Created || t.Created == best.Created && t.Name > best.Name {
			best, found = t, true
		}
	}
	return best, found
}

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

// NoFreeSlot reports whether task must wait for an agent slot: the running
// sandboxes take all maxSandboxes, or Tasks that have waited longer take the
// ones left (see HasSlotTurn).
func NoFreeSlot(task string, now time.Time, waiting []SlotWaiter, maxSandboxes, sandboxes int) bool {
	return !HasSlotTurn(task, now, waiting, maxSandboxes-sandboxes)
}

// maxCheckExcerpt bounds the CI log excerpt sent to the agent.
const maxCheckExcerpt = 4000

// CheckFailures picks the lines of a CI log that say what failed: failed tests
// (Go, Vitest, pytest), compiler, vet and linter findings, panics, data
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
	sourceFinding = regexp.MustCompile(`^\S+\.(go|ts|tsx|js|mjs|py|rs):\d+(:\d+)?: `)
	// eslintFinding is a finding under ESLint's file heading: line:column error message.
	eslintFinding = regexp.MustCompile(`^\s+\d+:\d+\s+(error|warning)\s`)
)

func isFailureLine(line string) bool {
	for _, marker := range []string{"make: ***", "Expected:", "Actual:", "Some tests failed", "Error:",
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

// startsBlock reports whether the lines after a failure line explain it: a Go
// test's log or a panic's stack, a Vitest assertion.
func startsBlock(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "--- FAIL:") || strings.HasPrefix(trimmed, "panic:") ||
		strings.HasPrefix(trimmed, "WARNING: DATA RACE") || strings.HasPrefix(trimmed, "FAIL  ")
}

// endsBlock reports whether a line closes the explanation of a failure: the next
// test, a passing package or the runner closing a group.
func endsBlock(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "FAIL" || trimmed == "PASS" {
		return true
	}
	for _, prefix := range []string{"::endgroup::", "=== ", "--- PASS", "--- SKIP", "ok  ", "FAIL\t", "⎯"} {
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
