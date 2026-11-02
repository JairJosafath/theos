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

// maxReviewAgains is how often an approved Standard change may be checked and
// reviewed again in one round because its approval ran out while CI ran
// again. A runner that keeps CI waiting longer than a review run lasts would
// otherwise loop through review runs without end.
const maxReviewAgains = 3

// RunResult is what one sandbox run reported.
type RunResult struct {
	Commit  string
	Summary string
	Verdict string
	Error   string
	// CheckFailsOnBase is the end of CheckCommand's output on main, set when
	// the check fails in the agent's workspace on its branch and on main too:
	// the workspace can't judge the change, so CI decides it.
	CheckFailsOnBase string
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
	// SessionHandoffs are the sizes, in tokens, at which the run's agent
	// sessions handed off to fresh ones.
	SessionHandoffs []int64
	// Role is the sandbox's role as c4agent reported it: implement, review or plan.
	Role string
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
	// Checkpoint means the sandbox has the checkpoint volume and has not
	// expired: an interrupted run can resume in place from what c4agent saved
	// there. A sandbox made before checkpoints has none, and is rerun.
	Checkpoint bool
	// Suspended means the operator suspended the sandbox to resume its run:
	// its pod goes, its volume stays. Pod is the old pod until it is gone.
	// SuspendedAt is when, if the operator knows, and SuspendedFor how long
	// ago.
	Suspended    bool
	SuspendedAt  time.Time
	SuspendedFor time.Duration
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

// MaxResumes is how often a round's interrupted runs resume in place, from
// their sandbox's checkpoint, before an interruption is rerun instead
// (docs/design.md, "A run continues from a checkpoint"). The cap keeps a run
// whose own doing ends its pod each time from resuming forever.
const MaxResumes = 5

// Interruption is what an interrupted run gets.
type Interruption string

// What an interrupted run gets: it resumes in place, it runs again in a new
// sandbox, or neither, and the round is judged on what it left.
const (
	InterruptionResume Interruption = "Resume"
	InterruptionRerun  Interruption = "Rerun"
	InterruptionNone   Interruption = ""
)

// AfterInterruption decides what a run gets, given the resumes and reruns its
// round had so far: a run the infrastructure interrupted resumes in place
// when its sandbox has the checkpoint volume, up to MaxResumes times in the
// round; past that, or without the volume, it runs again, up to MaxReruns
// times. A run that wasn't interrupted, or one past both caps, gets neither.
func AfterInterruption(run RunState, resumes, reruns int) Interruption {
	switch {
	case !interrupted(run):
		return InterruptionNone
	case run.Checkpoint && resumes < MaxResumes:
		return InterruptionResume
	case reruns < MaxReruns:
		return InterruptionRerun
	}
	return InterruptionNone
}

// MaxSuspendWait bounds how long a suspended sandbox's run waits for its old
// pod to go before it resumes. Agent Sandbox deletes the pod at once, and
// c4agent has its grace period to save the run; a pod still there long after
// (kept, or stuck terminating on a lost node) would hold the Task forever, as
// the operator deletes no pods.
const MaxSuspendWait = 10 * time.Minute

// settleSuspended is a suspended run that can no longer resume, because its
// sandbox expired (it has no Checkpoint any more) or its old pod did not go
// within MaxSuspendWait, as an interrupted run without a checkpoint: it is
// rerun, as a run in a sandbox without the volume is. Any other run is as it
// is.
func settleSuspended(run RunState) RunState {
	if !run.Suspended || run.Checkpoint && run.SuspendedFor <= MaxSuspendWait {
		return run
	}
	reason := fmt.Sprintf("its pod did not go within %s of the suspend", MaxSuspendWait)
	if !run.Checkpoint {
		reason = "its sandbox reached its shutdown time"
	}
	run.Suspended, run.Checkpoint, run.Done, run.Interrupted = false, false, true, true
	run.Failure = "the suspended run could not resume: " + reason
	return run
}

// MinResumeDeadline is the least time a resumed run gets, however long its
// pods ran before: a run that ran out its own time was not interrupted, so
// what it lacks is the time an outage took.
const MinResumeDeadline = 10 * time.Minute

// ResumeDeadline is the time a resumed run gets: what its timeout leaves
// after the time its earlier pods ran, and at least MinResumeDeadline.
func ResumeDeadline(timeout, ran time.Duration) time.Duration {
	return max(timeout-ran, MinResumeDeadline)
}

// PodRunTime is how long a pod's run took, from its start to its end; zero
// when either is unknown or the end is not after the start.
func PodRunTime(started, ended time.Time) time.Duration {
	if started.IsZero() || ended.IsZero() || !ended.After(started) {
		return 0
	}
	return ended.Sub(started)
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
	// Base is the CI state of main's head for the Container's check contexts
	// (see BaseState), read before round 1, and BaseURL where to see it.
	Base    string
	BaseURL string
	Run     RunState
	// Checks is the CI state on the round's commit, and ChecksURL where to see it.
	Checks    string
	ChecksURL string
	// ChecksLog is the failed CI job's log, when it could be read, and
	// ChecksNoLog means the job started and the Git host has no log of it.
	ChecksLog   string
	ChecksNoLog bool
	// CIRun is the state of the runs at the round's commit while its checks
	// are pending, and CheckingSince when the Task entered Checking.
	CIRun         CIRun
	CheckingSince time.Time
	// ReviewAttempts counts reviewer runs without a verdict in this round.
	ReviewAttempts int
	// ReviewAgains counts the review runs in this round started again because
	// an approval ran out (ReviewAgain).
	ReviewAgains int
	// Reruns counts the free reruns of interrupted runs in this round.
	Reruns int
	// Resumes counts the interrupted runs in this round that resumed in place.
	Resumes int
	// ChecksReruns counts the reruns of CI jobs the runner failed in this
	// round, killed for lack of memory or abandoned, and
	// ChecksRerunAt is when the last one was asked for, SinceChecksRerun how long
	// ago. ChecksRerunEnded means the rerun gave its result (RerunEnded), so a
	// failed status is the rerun's, not the killed run's.
	ChecksReruns     int
	ChecksRerunAt    time.Time
	SinceChecksRerun time.Duration
	ChecksRerunEnded bool
	// ChecksRerunDue means the round pushed again the commit whose checks
	// failed (PushedAgain): their failed jobs run again before they are read.
	ChecksRerunDue bool
	// FailedCommit is the commit whose checks failed in an earlier round, kept
	// by NextRound; empty when none did.
	FailedCommit string
	// InPhase is how long the Task has been in its current phase.
	InPhase time.Duration
	// NoFreeSlot means the operator already runs as many agent sandboxes as it may.
	NoFreeSlot bool
	// AgentBackIn is how long until an unavailable agent is tried again; zero or
	// less means now.
	AgentBackIn time.Duration
	// CostMilliUSD is what the Task's runs cost so far, in thousandths of a US
	// dollar, and BudgetMilliUSD its Crew's cap on it; zero means no cap.
	CostMilliUSD   int64
	BudgetMilliUSD int64
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
	// ApprovalEnded means the review run that approved a Standard change ended
	// while its branch, with main merged in, was checked again: its gated merge
	// has no approval to merge with.
	ApprovalEnded bool
	// StandardChanges counts the Standard changes filed so far for the Task.
	StandardChanges int
	// Resumed means this implement run is in the round the Task resumed in after
	// its Standard change merged.
	Resumed bool
	// AllowedPaths are the protected paths a Standard change's Task is for;
	// empty for any other Task.
	AllowedPaths []string
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
// outside the allowed ones, case ignored. contexts is the state of each check on the commit by its
// context, and ci the Container's CI system, whose check contexts main requires.
func GatedMergeRefusal(ci CI, checks string, contexts map[string]string, verdict string, changed, allowed,
	protected []string) string {
	if refusal := checksRefusal(ci.CheckContexts, checks, contexts); refusal != "" {
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
// requires (required, the CI system's check contexts) passed.
func checksRefusal(required []string, checks string, contexts map[string]string) string {
	if len(required) == 0 {
		// A CI system always names its contexts; none means it wasn't given.
		return "no CI system names the checks main requires"
	}
	if checks != checkSuccess {
		return fmt.Sprintf("CI is %s, not success", orUnknown(checks))
	}
	if missing := UnpassedChecks(required, contexts); len(missing) > 0 {
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

// cancelledChecksFeedback ends the feedback of a round whose CI run a person
// cancelled in the Git host (CancelledByPerson).
const cancelledChecksFeedback = "Someone cancelled the checks before they ended: make sure the check command " +
	"passes and ends in good time, and fix what fails or hangs."

// AgentRetryDelay is how long a Task waits for an unavailable agent that didn't
// say when it is back.
const AgentRetryDelay = 15 * time.Minute

// A Task in Checking waits for checks a runner will run, and fails, reason
// ReasonNoCIResult, when CI won't report: CI that never reports is the
// infrastructure's fault, not the agent's, so it costs no round.
const (
	// ChecksStartLimit is how long a Task waits for a CI run to start at its
	// commit, and for a runner to come online while its run is queued.
	ChecksStartLimit = 30 * time.Minute
	// QueuedChecksLimit is how long a Task waits while its run is queued with
	// a runner online, behind other jobs.
	QueuedChecksLimit = 6 * time.Hour
	// RunningChecksLimit is how long a run may run: act_runner's default job
	// timeout, plus 15 minutes for it to report.
	RunningChecksLimit = 3*time.Hour + 15*time.Minute
)

// ReasonBaseRed is the reason of a Task that waits to start round 1 because
// main's checks failed.
const ReasonBaseRed = "BaseRed"

// ReasonNoCIResult is the reason of a Task that failed because CI won't report.
const ReasonNoCIResult = "NoCIResult"

// ReasonRunnerOutOfSpace is the reason of a Task whose checks failed because
// the CI runner ran out of room ("no space left on device"): the runner's
// fault, not the change's, so it costs no round and its issue no try.
const ReasonRunnerOutOfSpace = "RunnerOutOfSpace"

// ReasonRunnerOutOfMemory is the reason of a Task whose checks the machine
// killed for lack of memory more than MaxChecksReruns times in a round: the
// runner's fault, not the change's, so it costs its issue no try.
const ReasonRunnerOutOfMemory = "RunnerOutOfMemory"

// ReasonRunnerLost is the reason of a Task whose CI jobs their runner
// abandoned (RunnerAbandoned) more than MaxChecksReruns times in a round: the
// runner's fault, not the change's, so it costs its issue no try.
const ReasonRunnerLost = "RunnerLost"

// MaxChecksReruns is how often a round's failed CI jobs run again after the
// machine killed them for lack of memory or their runner abandoned them. The
// cap keeps a runner that is always short of memory, or keeps dying, from
// looping forever.
const MaxChecksReruns = 2

// ReasonOverBudget is the reason of a Task waiting at its Crew's cost cap:
// its runs cost at least taskBudgetUSD, so it starts no further run until
// the cap is raised.
const ReasonOverBudget = "OverBudget"

// ReasonApprovalRanOut is the reason of a Task whose approved Standard change
// was reviewed again maxReviewAgains times in a round and whose approval ran
// out once more while CI ran again: the runner's slowness, not the change's
// fault, so it costs no round and its issue no try.
const ReasonApprovalRanOut = "ApprovalRanOut"

// RunnerFault reports whether a Task that failed for reason failed through
// its CI runner's fault, so the failure costs its issue no try.
func RunnerFault(reason string) bool {
	switch reason {
	case ReasonNoCIResult, ReasonRunnerOutOfSpace, ReasonRunnerOutOfMemory, ReasonRunnerLost, ReasonApprovalRanOut:
		return true
	}
	return false
}

// RunnerOutOfSpace reports whether a failed CI log shows the runner out of
// room: Go, Node and the shell all say "no space left on device" (ENOSPC).
// Only what the runner and the toolchain print counts, not what the change's
// own tests and code print, since a test of disk-full handling says the phrase
// too: a Go test's messages, compiler and linter findings, the failure reports
// and summary of pytest and Vitest, and the output Vitest captures from a test
// are passed over. What a Go test prints to stdout can't be told from the
// toolchain's and still counts.
func RunnerOutOfSpace(log string) bool {
	return runnerSays(log, func(line string) bool {
		return strings.Contains(strings.ToLower(line), "no space left on device")
	})
}

// RunnerKilled reports whether a failed CI log shows a job killed for lack of
// memory: Go's "signal: killed", the shell's "Killed" report of a job, make's
// "Error 137", exit code 137 and Node's "JavaScript heap out of memory". It
// reads the log as RunnerOutOfSpace does, so a test that prints the phrase in
// its own message doesn't count.
func RunnerKilled(log string) bool {
	return runnerSays(log, func(line string) bool { return killedLine.MatchString(strings.TrimSpace(line)) })
}

// RunnerAbandoned reports whether a failed CI job ended because its runner
// went away, not because the change failed it. Gitea 1.27 says so in no field,
// so it reads what the runner left:
//   - cancelled: the run ended cancelled by its runner (CancelledByRunner). A
//     runner told to stop (its pod replaced, say) cancels its job.
//   - noLog: the job started, but Gitea has no log of it: its runner died
//     before it sent a line.
//   - a log as Gitea serves it, a timestamp on every line, that stops before
//     the runner said how the job ended ("Job failed"), or whose last line is
//     the runner's "Early termination": the runner died mid-job, and Gitea
//     failed the job once it stopped reporting (its zombie task timeout).
//
// A log that can't be read is none of these: the failure is read as the change's.
func RunnerAbandoned(cancelled, noLog bool, log string) bool {
	return cancelled || noLog || cutOffLog(log)
}

// cutOffLog reports whether a CI log as Gitea serves it stops before its
// runner said how the job ended. The runner sends that line at its log level
// info, which hack/runner/config.yaml keeps.
func cutOffLog(log string) bool {
	lines := strings.Split(strings.TrimRight(log, "\n"), "\n")
	last := lines[len(lines)-1]
	if !giteaLogLine.MatchString(last) {
		return false
	}
	if strings.TrimSpace(stripLogPrefix(last)) == "Early termination" {
		return true
	}
	for _, line := range lines {
		if jobEndedLine.MatchString(strings.TrimSpace(stripLogPrefix(line))) {
			return false
		}
	}
	return true
}

var (
	// giteaLogLine is a line of a CI log as Gitea serves it: its time, then
	// what the runner sent.
	giteaLogLine = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?Z `)
	// jobEndedLine is the runner saying how a job ended, as it does last.
	jobEndedLine = regexp.MustCompile(`^(🏁\s+)?Job (succeeded|failed|cancelled)$`)
)

// killedLine is a line of the runner or toolchain saying a job was killed for
// lack of memory.
var killedLine = regexp.MustCompile(`signal: killed|^Killed(\s|$)|\d Killed(\s|$)|Error 137\b|` +
	`exit (code|status) 137\b|JavaScript heap out of memory`)

// runnerSays reports whether a line of a CI log that the runner or the
// toolchain printed, not the change's own tests and code, says what says
// reports.
func runnerSays(log string, says func(line string) bool) bool {
	section := noSection
	goMessage := -1 // the indent of the Go test message whose lines follow, or -1
	for raw := range strings.SplitSeq(log, "\n") {
		line := stripLogPrefix(raw)
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if goMessage >= 0 && (trimmed == "" || indent > goMessage) {
			continue
		}
		goMessage = -1
		if section != noSection {
			if section.ends(trimmed) {
				section = noSection
			}
			continue
		}
		switch {
		case goTestMessage.MatchString(line):
			goMessage = indent
		case sourceFinding.MatchString(trimmed), pytestSummary.MatchString(trimmed):
		case pytestReport.MatchString(trimmed):
			section = pytestSection
		case strings.HasPrefix(trimmed, "FAIL  "), strings.HasPrefix(trimmed, "⎯") && strings.Contains(trimmed, "Failed"):
			section = vitestSection
		case strings.HasPrefix(trimmed, "stdout | "), strings.HasPrefix(trimmed, "stderr | "):
			section = capturedSection
		case says(line):
			return true
		}
	}
	return false
}

// testSection is a part of a CI log where a test framework reports a test's
// own output.
type testSection int

const (
	noSection testSection = iota
	// pytestSection is the failed tests' reports, from the first's
	// "___ name ___" heading to the summary.
	pytestSection
	// vitestSection is the failed tests' reports, up to the summary.
	vitestSection
	// capturedSection is what Vitest captured from a test's stdout or stderr.
	capturedSection
)

// ends reports whether a line, trimmed, closes the section.
func (s testSection) ends(trimmed string) bool {
	if strings.HasPrefix(trimmed, "::endgroup::") {
		return true
	}
	switch s {
	case pytestSection:
		return strings.HasPrefix(trimmed, "===")
	case vitestSection:
		return strings.HasPrefix(trimmed, "Test Files ")
	case capturedSection:
		return trimmed == ""
	}
	return true
}

var (
	// goTestMessage is a Go test's message (t.Log, t.Error): file_test.go:line: message.
	goTestMessage = regexp.MustCompile(`^\s+\S+_test\.go:\d+: `)
	// pytestReport heads a failed pytest test's report: ___ test_name ___.
	pytestReport = regexp.MustCompile(`^_{3,} .+ _{3,}$`)
	// pytestSummary is a line of pytest's short summary: FAILED path.py::test - message.
	pytestSummary = regexp.MustCompile(`^(FAILED|ERROR) \S+\.py`)
)

// The states of the CI runs at a Task's commit (CIRun.State).
const (
	// CIRunNone means no run at the commit.
	CIRunNone = ""
	// CIRunQueued means a run waits for a runner.
	CIRunQueued = "queued"
	// CIRunRunning means a runner runs one.
	CIRunRunning = "running"
	// CIRunCompleted means every run at the commit has ended, as a run that
	// ended has that status.
	CIRunCompleted = "completed"
	// CIRunUnknown means the Git host didn't say.
	CIRunUnknown = "unknown"
)

// CIRun is what the Git host's Actions say of the runs at a Task's commit,
// read while its checks are pending: whether one is queued or running, which,
// and whether any runner is online.
type CIRun struct {
	// Commit is the round's commit.
	Commit string
	State  string
	// URL is the run's page, and Started when a runner started it.
	URL     string
	Started time.Time
	// Running is how long it has run.
	Running time.Duration
	// NoRunnerOnline means the Git host says none of its runners is online. A
	// host that doesn't say counts as having one: a wrong "offline" fails good
	// work, a wrong "online" only waits longer.
	NoRunnerOnline bool
	// CancelledBy says who cancelled a failed run at the commit, the newest of
	// its workflow and event (see CancelledRuns and Canceller); empty when none
	// ended cancelled.
	CancelledBy string
}

// Who cancelled a failed CI run at a Task's commit (CIRun.CancelledBy).
const (
	// CancelledByRunner means a runner told to stop cancelled its job and went
	// away, or that the Git host doesn't say.
	CancelledByRunner = "runner"
	// CancelledByPerson means the runner of every cancelled job stayed online:
	// a person cancelled the run in the Git host.
	CancelledByPerson = "person"
	// CancelledByUnsure means a cancelled job's runner is still online, but the
	// job ended too lately for a runner that went away to show offline yet.
	CancelledByUnsure = "unsure"
)

// RunnerGoneAfter is how long after a cancelled CI job ended its runner, had
// it gone away, shows offline: Gitea shows a runner offline a minute after it
// last wrote down hearing from it, which it writes at most 30 seconds apart,
// and a runner's pod has 30 seconds to stop.
const RunnerGoneAfter = 2 * time.Minute

const (
	// PodFateEnded is the fate of a pool runner pod (#1140) whose runner ran
	// its one job and exited on its own: nobody told it to stop, so a cancel
	// of its job was a person's. The controller writes it on the pod.
	PodFateEnded = "ended"
	// PodFateLost is the fate of a pool runner pod deleted, evicted or killed
	// before its job ended: its runner cancelled the job.
	PodFateLost = "lost"
)

// CIJob is a job of a CI run, as the Git host lists it.
type CIJob struct {
	// Conclusion is how it ended: success, failure, cancelled or skipped.
	Conclusion string
	// Runner is the ID of the runner that ran it; zero when none started it.
	Runner int64
	// RunnerOnline means the Git host says that runner is online now, and
	// RunnerUnknown that it doesn't say.
	RunnerOnline  bool
	RunnerUnknown bool
	// RunnerName is the runner's name, and PodFate, when that names a pod of
	// the factory's pool that the operator still keeps, how the pod ended:
	// PodFateEnded or PodFateLost; empty for a runner outside the pool, or a
	// pod the pool no longer records.
	RunnerName string
	PodFate    string
	// Ended is when it ended.
	Ended time.Time
}

// Cancelled reports whether the job ended cancelled.
func (j CIJob) Cancelled() bool { return j.Conclusion == checkCancelled }

// Canceller tells who cancelled the cancelled jobs of the failed runs at a
// Task's commit. Gitea keeps no record of who cancelled a run, and a runner
// ends a job's log the same way whoever cancelled it ("Job cancelled"), so it
// reads the jobs' runners instead: a runner told to stop (its pod replaced,
// say) cancels its job and goes away, and the factory's runner registers as a
// new one each time its pod starts (hack/runner/register.sh), while a person's
// cancel leaves the runner online, taking the next job. A job no runner started was cancelled by a person (or by Gitea,
// past any of the Task's bounds). read false means the jobs couldn't be read:
// the cancel counts as the runner's, MaxChecksReruns bounding what a person's
// costs.
//
// A job that ran on a pod of the factory's pool (#1140) is told by the pod
// instead: Gitea deletes an ephemeral runner once its job ends, so its runner
// is gone after a person's cancel too. A pod that ended on its own
// (PodFateEnded) was not told to stop, so a person cancelled; one lost before
// its job ended (PodFateLost) was.
func Canceller(jobs []CIJob, read bool, now time.Time) string {
	if !read {
		return CancelledByRunner
	}
	who := CancelledByPerson
	for _, job := range jobs {
		switch {
		case !job.Cancelled(), job.Runner == 0:
		case job.PodFate == PodFateLost:
			return CancelledByRunner
		case job.PodFate == PodFateEnded:
		case job.RunnerUnknown || !job.RunnerOnline:
			return CancelledByRunner
		case now.Sub(job.Ended) < RunnerGoneAfter:
			who = CancelledByUnsure
		}
	}
	return who
}

// WorkflowRun is one CI run at a commit, as the Git host lists it.
type WorkflowRun struct {
	ID int64
	// Workflow is the workflow file it runs, and Event what started it.
	Workflow string
	Event    string
	// Status is queued, waiting, in_progress or completed, and Conclusion how a
	// completed run ended.
	Status     string
	Conclusion string
	Started    time.Time
	URL        string
}

// FailedCommitRuns are the runs at a commit, listed newest first, whose
// failed jobs run again: the newest run of each workflow and event, when it
// ended neither passing nor skipped.
func FailedCommitRuns(runs []WorkflowRun) []WorkflowRun {
	var failed []WorkflowRun
	seen := map[string]bool{}
	for _, run := range runs {
		key := run.Workflow + "\x00" + run.Event
		if !seen[key] && run.Status == CIRunCompleted && run.Conclusion != checkSuccess &&
			run.Conclusion != checkSkipped {
			failed = append(failed, run)
		}
		seen[key] = true
	}
	return failed
}

// CancelledRuns are the failed runs at a commit (FailedCommitRuns) that ended
// cancelled.
func CancelledRuns(runs []WorkflowRun) []WorkflowRun {
	var cancelled []WorkflowRun
	for _, run := range FailedCommitRuns(runs) {
		if run.Conclusion == checkCancelled {
			cancelled = append(cancelled, run)
		}
	}
	return cancelled
}

// RunsEnded reports whether every run at a commit has ended: only then are
// its failed jobs run again, as the Git host runs a run's jobs again only
// once it ended.
func RunsEnded(runs []WorkflowRun) bool {
	for _, run := range runs {
		if run.Status != CIRunCompleted {
			return false
		}
	}
	return len(runs) > 0
}

// RerunEnded reports whether the runs at a commit whose failed jobs ran again
// at at have given their result: every run has ended, and one started at or
// after at. Only then is a failed status the rerun's result rather than the
// killed run's; a run of another workflow that started since doesn't say the
// rerun did.
func RerunEnded(runs []WorkflowRun, at time.Time) bool {
	if !RunsEnded(runs) {
		return false
	}
	for _, run := range runs {
		if !run.Started.IsZero() && !run.Started.Before(at) {
			return true
		}
	}
	return false
}

// CommitRunState sums up the runs at a commit: running when any runs (the
// longest running one), else queued when any waits (the first one listed),
// else completed when there are runs, else none.
func CommitRunState(runs []WorkflowRun) (string, WorkflowRun) {
	state, pick := CIRunNone, WorkflowRun{}
	for _, run := range runs {
		switch {
		case run.Status == "in_progress":
			if state != CIRunRunning || !run.Started.IsZero() && (pick.Started.IsZero() || run.Started.Before(pick.Started)) {
				state, pick = CIRunRunning, run
			}
		case run.Status == CIRunCompleted:
			if state == CIRunNone {
				state, pick = CIRunCompleted, run
			}
		case state != CIRunRunning && state != CIRunQueued:
			state, pick = CIRunQueued, run
		}
	}
	return state, pick
}

// The messages of a Task that waits for checks, by the state of its run.
const (
	waitingQueued    = "Waiting for checks: queued behind other jobs since "
	waitingNoRunner  = "Waiting for checks: queued with no runner online since "
	waitingRunning   = "Waiting for checks: running since "
	waitingNoRun     = "Waiting for checks: no CI run has started yet"
	waitingUnknownCI = "Waiting for checks; the Git host doesn't say whether a CI run is queued"
)

// ChecksBound says when the operator gives up on a Task that waits for checks
// with message, its Progressing condition's.
func ChecksBound(message string) string {
	switch {
	case strings.HasPrefix(message, waitingRunning):
		return fmt.Sprintf("The operator fails it if its CI run is still running %s after it started.",
			spellDuration(RunningChecksLimit))
	case strings.HasPrefix(message, waitingNoRunner):
		return fmt.Sprintf("The operator fails it %s into its checks unless a runner comes online.",
			spellDuration(ChecksStartLimit))
	case strings.HasPrefix(message, waitingQueued), strings.HasPrefix(message, waitingUnknownCI):
		return fmt.Sprintf("The operator fails it if its CI run is still queued %s into its checks.",
			spellDuration(QueuedChecksLimit))
	}
	return fmt.Sprintf("The operator fails it if no CI run starts within %s.", spellDuration(ChecksStartLimit))
}

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
	// ActionSuspendRun counts what an interrupted run cost and suspends its
	// sandbox, which keeps its checkpoint volume: the run resumes in place.
	ActionSuspendRun TaskAction = "SuspendRun"
	// ActionResumeRun resumes a suspended sandbox, its pod gone, with the
	// current Agent and the run's remaining time: c4agent goes on from the
	// checkpoint.
	ActionResumeRun TaskAction = "ResumeRun"
	// ActionReviewAgain checks and reviews an approved Standard change again, in
	// the same round, as the review run whose approval its gated merge needs
	// ended while CI ran again: no try and no round is spent.
	ActionReviewAgain TaskAction = "ReviewAgain"
	// ActionRerunChecks runs again the failed jobs of the CI runs at the round's
	// commit, which the machine killed for lack of memory or the round pushed
	// again, and waits for their result in the same round: the agent is told
	// nothing. Ending an implement run, it opens the pull request first.
	ActionRerunChecks TaskAction = "RerunChecks"
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
	// Recheck means the wait ends on the Git host, or at a time, which send
	// no event: the Task looks again after a while.
	Recheck bool
	// RetryAt is when to try an unavailable agent again; zero means the default delay.
	RetryAt time.Time
	// Reason is a failed Task's reason, when not ReasonFailed, or a waiting
	// one's when it is not a plain wait (ReasonOverBudget, ReasonBaseRed).
	Reason string
}

// NextTaskStep decides what happens next to a Task. Starting a sandbox waits
// for a free agent slot.
func NextTaskStep(f TaskFacts) TaskStep {
	f.Run = settleSuspended(f.Run)
	step := nextTaskStep(f)
	starts := step.Action == ActionStartImplement || step.Action == ActionStartReview
	// A resumed run spends like a new one: what the run cost so far counts.
	if (starts || step.Action == ActionResumeRun) && f.BudgetMilliUSD > 0 && f.CostMilliUSD >= f.BudgetMilliUSD {
		return TaskStep{Action: ActionWait, Reason: ReasonOverBudget, Message: fmt.Sprintf(
			"The Task's runs cost $%.2f, at its Crew's cap of $%d; raise the Crew's taskBudgetUSD to go on",
			float64(f.CostMilliUSD)/1000, f.BudgetMilliUSD/1000)}
	}
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
		case checksFailed(f.Base):
			return TaskStep{Action: ActionWait, Reason: ReasonBaseRed, Recheck: true,
				Message: fmt.Sprintf("Main's checks failed (%s): round 1 starts only on a green base; "+
					"fix main's checks", orUnknown(f.BaseURL))}
		case f.Base == checkPending:
			return TaskStep{Action: ActionWait, Recheck: true, Message: "Waiting for main's checks to end before round 1"}
		}
		return TaskStep{Action: ActionStartImplement, Message: "Starting round 1"}

	case TaskImplementing:
		return implementingStep(f)

	case TaskBlocked:
		return blockedStep(f)

	case TaskChecking:
		if f.ChecksRerunDue && checksFailed(f.Checks) {
			return pushedAgainStep(f)
		}
		if !f.ChecksRerunAt.IsZero() {
			// The checks started again at the rerun: their wait counts from then.
			f.CheckingSince, f.InPhase = f.ChecksRerunAt, min(f.InPhase, f.SinceChecksRerun)
			if checksFailed(f.Checks) && !f.ChecksRerunEnded {
				return rerunPendingStep(f)
			}
		}
		switch f.Checks {
		case checkSuccess:
			switch {
			case f.MergeWhenChecksPass && f.ApprovalEnded:
				return reviewAgainStep(f, "with main merged in")
			case f.MergeWhenChecksPass:
				return approvedStep(f, "Checks passed on the branch with main merged in; merging")
			}
			return TaskStep{Action: ActionStartReview, Message: "Checks passed; starting the review"}
		case checkFailure, checkError:
			if RunnerOutOfSpace(f.ChecksLog) {
				return TaskStep{Action: ActionFail, Reason: ReasonRunnerOutOfSpace, Message: fmt.Sprintf(
					"CI run %s failed because the runner ran out of room (no space left on device); "+
						"free the runner's space or restart it", orUnknown(f.ChecksURL))}
			}
			if RunnerKilled(f.ChecksLog) {
				return rerunStep(f, "was killed for lack of memory", ReasonRunnerOutOfMemory,
					"free memory on the runner or run fewer jobs at once")
			}
			if f.CIRun.CancelledBy == CancelledByUnsure {
				return TaskStep{Action: ActionWait, Message: fmt.Sprintf("CI run %s was cancelled; waiting to tell "+
					"whether its runner went away, which shows within %s", orUnknown(f.ChecksURL),
					spellDuration(RunnerGoneAfter))}
			}
			if RunnerAbandoned(f.CIRun.CancelledBy == CancelledByRunner, f.ChecksNoLog, f.ChecksLog) {
				return rerunStep(f, "was abandoned by its runner", ReasonRunnerLost,
					"check why the runners stop mid-job (a runner pod that restarts ends its job)")
			}
			feedback := fmt.Sprintf("The CI checks failed (%s). Run the check command, find the failure and fix it.",
				orUnknown(f.ChecksURL))
			if f.MergeWhenChecksPass {
				feedback = fmt.Sprintf(updatedChecksFeedback, orUnknown(f.ChecksURL))
			}
			if f.CIRun.CancelledBy == CancelledByPerson {
				feedback += " " + cancelledChecksFeedback
			}
			if excerpt := CheckFailures(f.ChecksLog); excerpt != "" {
				feedback += " A test that passes for you but fails in CI is flaky: make it deterministic." +
					"\nWhat failed in CI:\n" + excerpt
			}
			return roundFailed(f, feedback)
		}
		return checksPendingStep(f)

	case TaskReviewing:
		return reviewingStep(f)
	}
	return TaskStep{Action: ActionNone}
}

// rerunStep runs again the checks that failed through the runner's fault, as
// why says, up to MaxChecksReruns times in a round, and then fails the Task
// for reason at no try, saying what to do.
func rerunStep(f TaskFacts, why, reason, todo string) TaskStep {
	switch {
	case f.CIRun.State == CIRunRunning || f.CIRun.State == CIRunQueued:
		// The Git host runs a run's jobs again only once it ended.
		return checksPendingStep(f)
	case f.ChecksReruns < MaxChecksReruns:
		return TaskStep{Action: ActionRerunChecks, Message: fmt.Sprintf("CI run %s %s; running its failed jobs "+
			"again (rerun %d of %d in round %d)", orUnknown(f.ChecksURL), why, f.ChecksReruns+1, MaxChecksReruns,
			f.Round)}
	}
	return TaskStep{Action: ActionFail, Reason: reason, Message: fmt.Sprintf("CI run %s %s, %d times in round %d; %s",
		orUnknown(f.ChecksURL), why, MaxChecksReruns+1, f.Round, todo)}
}

// pushedAgainStep runs again the failed jobs at a commit the round pushed
// again: the failure read is the earlier round's. It waits first while a run
// at the commit still runs, as killedStep does.
func pushedAgainStep(f TaskFacts) TaskStep {
	if f.CIRun.State == CIRunRunning || f.CIRun.State == CIRunQueued {
		return checksPendingStep(f)
	}
	return TaskStep{Action: ActionRerunChecks, Message: fmt.Sprintf("The agent pushed the commit whose CI run %s "+
		"failed again; running its failed jobs again in round %d", orUnknown(f.ChecksURL), f.Round)}
}

// rerunPendingStep waits for the failed jobs run again after a kill: the
// failed status is still the killed run's until the rerun ends.
func rerunPendingStep(f TaskFacts) TaskStep {
	switch f.CIRun.State {
	case CIRunNone, CIRunCompleted:
		if f.InPhase > ChecksStartLimit {
			return TaskStep{Action: ActionFail, Reason: ReasonNoCIResult, Message: fmt.Sprintf(
				"The failed jobs at %s, run again at %s, didn't start within %s; check the repository's runners",
				short(f.CIRun.Commit), clock(f.ChecksRerunAt), ChecksStartLimit)}
		}
		return TaskStep{Action: ActionWait, Message: "Waiting for checks: the failed jobs run again since " +
			clock(f.ChecksRerunAt)}
	}
	return checksPendingStep(f)
}

// checksPendingStep decides what follows while the checks have no result: wait
// while the run is queued on an online runner or running, and fail when CI
// won't report.
func checksPendingStep(f TaskFacts) TaskStep {
	noCI := func(format string, args ...any) TaskStep {
		return TaskStep{Action: ActionFail, Reason: ReasonNoCIResult, Message: fmt.Sprintf(format, args...)}
	}
	ci := f.CIRun
	switch ci.State {
	case CIRunRunning:
		running := ci.Running
		if ci.Started.IsZero() {
			running = f.InPhase // the host didn't say when it started
		}
		if running > RunningChecksLimit {
			return noCI("CI run %s has run for %s, past %s; check its runner", orUnknown(ci.URL),
				running.Round(time.Minute), RunningChecksLimit)
		}
		return TaskStep{Action: ActionWait, Message: waitingRunning + clock(ci.Started)}
	case CIRunQueued:
		switch {
		case ci.NoRunnerOnline && f.InPhase > ChecksStartLimit:
			return noCI("CI run %s is queued and no runner is online; start a runner for the repository",
				orUnknown(ci.URL))
		case f.InPhase > QueuedChecksLimit:
			return noCI("CI run %s has been queued for %s; check the repository's runners", orUnknown(ci.URL),
				QueuedChecksLimit)
		case ci.NoRunnerOnline:
			return TaskStep{Action: ActionWait, Message: waitingNoRunner + clock(f.CheckingSince)}
		}
		return TaskStep{Action: ActionWait, Message: waitingQueued + clock(f.CheckingSince)}
	case CIRunUnknown:
		if f.InPhase > QueuedChecksLimit {
			return noCI("No CI result for %s after %s; check that a runner for the repository is online",
				short(ci.Commit), QueuedChecksLimit)
		}
		return TaskStep{Action: ActionWait, Message: waitingUnknownCI}
	case CIRunCompleted:
		if f.InPhase > ChecksStartLimit {
			return noCI("No CI result for %s after %s, though its CI runs ended; check that the workflows "+
				"report the checks main requires", short(ci.Commit), ChecksStartLimit)
		}
		return TaskStep{Action: ActionWait, Message: "Waiting for checks"}
	}
	if f.InPhase > ChecksStartLimit {
		return noCI("No CI run started for %s after %s; check that the repository's workflows run on pull requests",
			short(ci.Commit), ChecksStartLimit)
	}
	return TaskStep{Action: ActionWait, Message: waitingNoRun}
}

// spellDuration writes d in hours and minutes, such as 3 hours 15 minutes.
func spellDuration(d time.Duration) string {
	unit := func(n int, name string) string {
		if n == 1 {
			return "1 " + name
		}
		return fmt.Sprintf("%d %ss", n, name)
	}
	hours, minutes := int(d.Hours()), int(d.Minutes())%60
	switch {
	case hours == 0:
		return unit(minutes, "minute")
	case minutes == 0:
		return unit(hours, "hour")
	}
	return unit(hours, "hour") + " " + unit(minutes, "minute")
}

// clock is t as a time of day in UTC, such as 01:13Z; unknown when zero.
func clock(t time.Time) string {
	if t.IsZero() {
		return "an unknown time"
	}
	return t.UTC().Format("15:04Z")
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
	case f.Run.Suspended:
		return suspendedStep(f)
	case !f.Run.Done:
		return TaskStep{Action: ActionWait, Message: fmt.Sprintf("The agent is working on round %d", f.Round)}
	case AfterInterruption(f.Run, f.Resumes, f.Reruns) != InterruptionNone:
		return interruptedStep(f)
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
	case f.Run.Suspended:
		return suspendedStep(f)
	case !f.Run.Done:
		return TaskStep{Action: ActionWait, Message: "The reviewer is reviewing"}
	case AfterInterruption(f.Run, f.Resumes, f.Reruns) != InterruptionNone:
		return interruptedStep(f)
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

// reviewAgainStep sends an approved Standard change, whose approving review run
// ended while CI ran again on its branch (where), back to be checked and
// reviewed again. The approval ran out only because CI waited, often behind
// other jobs while main moved: neither the agent's fault nor the change's, so
// it costs no round and no try. The new review starts only once the checks
// pass, so no reviewer run is spent on a CI wait. After maxReviewAgains of
// them in a round, the Task fails: CI keeps taking longer than a review run
// lasts, which another review won't change.
func reviewAgainStep(f TaskFacts, where string) TaskStep {
	if f.ReviewAgains >= maxReviewAgains {
		return TaskStep{Action: ActionFail, Reason: ReasonApprovalRanOut, Message: fmt.Sprintf(
			"The review run that approved it ended while CI ran again %s, %d times in round %d; "+
				"CI takes longer than a review run lasts, so free or speed up the runner",
			where, maxReviewAgains+1, f.Round)}
	}
	return TaskStep{Action: ActionReviewAgain, Message: fmt.Sprintf(
		"The review run that approved it ended while CI ran again %s; checking and reviewing it again in round %d",
		where, f.Round)}
}

// noVerdictStep decides what follows a review run that ended without a verdict:
// another run, up to maxReviewAttempts. A run that ends while the approved
// change's merge waits for CI, its sandbox expired, can no longer vouch for the
// change: it is checked and reviewed again.
func noVerdictStep(f TaskFacts) TaskStep {
	reason := f.Run.Failure
	if f.Run.Result != nil {
		reason = f.Run.Result.Error
	}
	if f.MergeWaitsForChecks {
		return reviewAgainStep(f, "on the reviewed commit")
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
	if len(f.AllowedPaths) > 0 && res.Empty {
		// Nothing is left to review or merge: more rounds won't change that.
		return TaskStep{Action: ActionFail, Reason: ReasonBlocked, Message: EmptiedMessage(f.AllowedPaths, f.Feedback)}
	}
	msg := "The agent pushed " + short(res.Commit)
	action := ActionOpenPR
	if res.Commit != "" && res.Commit == f.FailedCommit {
		// The agent found nothing to change: the old failure is not read again.
		msg += " again, whose checks failed; its failed jobs run again"
		action = ActionRerunChecks
	}
	if len(res.Extra) > 0 {
		msg += fmt.Sprintf("; besides its Standard change's files it changes %s, which no Standard protects",
			joinAnd(res.Extra))
	}
	if res.CheckFailsOnBase != "" {
		msg += "; " + checkFailsOnBase
	}
	return TaskStep{Action: action, Message: msg}
}

// CheckCommand is the check every Standard's repository has: the agent's
// workspace runs it after the agent, and CI on the pushed head.
const CheckCommand = "make check"

// CheckLightCommand is the quick part of CheckCommand, without the tests, that
// CI runs on each change of a repository whose checks are BeforeRelease.
const CheckLightCommand = "make check-light"

// checkFailsOnBase says that the workspace's check fails on main too. It costs
// no round and no try: CI decides.
const checkFailsOnBase = "`" + CheckCommand + "` fails on main in the agent's workspace too, so CI decides"

// CheckFailsOnBaseEvent is the Task Event's note of a round whose check fails
// on main in the agent's workspace too, with the output's last line, clipped.
func CheckFailsOnBaseEvent(round int, output string) string {
	return fmt.Sprintf("Round %d: %s (%s)", round, checkFailsOnBase, clipTitle(lastLine(output)))
}

// CheckFailsOnBaseComment is the pull request's comment for a round whose
// check fails on main in the agent's workspace too: CI decides the change,
// and a person can give the workspace what CI has. checks is what the
// repository's CI runs (status.standard.checks): with BeforeRelease, only
// CheckLightCommand on each change, and the full check before a release.
func CheckFailsOnBaseComment(round int, output, checks string) string {
	ci := "CI runs the same check on this head and decides; this costs no round. " +
		"If CI passes, the workspace lacks something CI has, such as a download or a service: give it that, " +
		"so the workspace judges changes again."
	if checks == ChecksBeforeRelease {
		ci = "CI runs only the light check, `" + CheckLightCommand + "`, on this head and decides; this costs no " +
			"round. `" + CheckCommand + "` runs in full before the release is tagged, and a failure there is " +
			"filed then. Give the workspace what it lacks, such as a download or a service, so it judges changes again."
	}
	return fmt.Sprintf("Round %d: `%s` fails in the agent's workspace on main too, before this change, so the "+
		"workspace can't judge it. %s Its output on main ends:\n\n````\n%s\n````\n",
		round, CheckCommand, ci, strings.TrimRight(output, "\n"))
}

// lastLine is s's last line that isn't blank.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
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

// interruptedStep resumes an interrupted run in place or runs its step
// again, as AfterInterruption says, without spending the round.
func interruptedStep(f TaskFacts) TaskStep {
	reason := f.Run.Failure
	if f.Run.Result != nil {
		reason = f.Run.Result.Error
	}
	if AfterInterruption(f.Run, f.Resumes, f.Reruns) == InterruptionResume {
		return TaskStep{Action: ActionSuspendRun, Message: fmt.Sprintf(
			"The run was interrupted (%s); round %d resumes in place from its checkpoint (resume %d of %d)",
			gist(orUnknown(reason)), f.Round, f.Resumes+1, MaxResumes)}
	}
	return TaskStep{Action: ActionRerun, Message: fmt.Sprintf(
		"The run was interrupted (%s); round %d runs again (rerun %d of %d)",
		gist(orUnknown(reason)), f.Round, f.Reruns+1, MaxReruns)}
}

// suspendedStep waits for a suspended sandbox's pod to go, then resumes it.
func suspendedStep(f TaskFacts) TaskStep {
	if f.Run.Pod != "" {
		// Nothing tells the Task when MaxSuspendWait is up: it looks again.
		return TaskStep{Action: ActionWait, Recheck: true, Message: fmt.Sprintf(
			"The interrupted run of round %d waits for its pod to go before it resumes", f.Round)}
	}
	return TaskStep{Action: ActionResumeRun, Message: fmt.Sprintf(
		"Resuming the interrupted run of round %d in place (resume %d of %d)", f.Round, f.Resumes, MaxResumes)}
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

// SandboxName names the sandbox of one run: task, round, role, the round's
// restart (conflicts, a resume), if any, for reviews the attempt, and the rerun
// after an interruption, if any. A rerun or a restarted round's run gets a name
// of its own, so the sandbox it replaces, still being deleted, is never read as
// its own. It fits a DNS label.
func SandboxName(task string, round, restart int, role string, attempt, rerun int) string {
	suffix := "-r" + strconv.Itoa(round) + "-" + role
	if restart > 0 {
		suffix += "-s" + strconv.Itoa(restart)
	}
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

// InheritedWork is what a new try knows of the earlier try's pull request it
// continues.
type InheritedWork struct {
	// Pushed is the commit the earlier try pushed last; Head the pull
	// request's head now.
	Pushed, Head string
	// Unreviewed means the earlier try ended waiting for the checks of Pushed,
	// so no reviewer has judged it.
	Unreviewed bool
	// Checks is the combined state of the checks on Head.
	Checks string
}

// TryStep is one entry of a try's timeline: the phase it entered, in round.
type TryStep struct {
	Phase string
	Round int
}

// EndedUnreviewed reports whether a try whose timeline is history ended
// waiting for the checks of a commit no reviewer judged: the step before it
// finished was Checking, and no reviewer ran in any of its rounds. The
// timeline records no commits, and a round after a reviewer's changes may push
// nothing, so a Checking that follows any review may be of the judged commit:
// such a try is not unreviewed. Red checks that end the try count as ending
// in Checking: when they later pass, its commit goes to the reviewer. A
// timeline whose start was dropped (its first step is past round 1) may have
// lost a review, so it is not unreviewed either.
func EndedUnreviewed(history []TryStep) bool {
	if len(history) == 0 || history[0].Round > 1 {
		return false
	}
	last := ""
	for _, h := range history {
		switch h.Phase {
		case TaskReviewing:
			return false
		case TaskFailed, TaskMerged:
		default:
			last = h.Phase
		}
	}
	return last == TaskChecking
}

// StartsAtReview reports whether a try that continues an earlier try's pull
// request skips the implementer and enters Checking at w.Head, whose passing
// checks start the review: the earlier try pushed it and ended before anyone
// reviewed it, no one moved the head since, and its checks now pass. Anything
// else, a reviewer's changes to make included, starts round 1.
func StartsAtReview(w InheritedWork) bool {
	return w.Unreviewed && w.Pushed != "" && w.Head == w.Pushed && w.Checks == checkSuccess
}

// StartAtReviewMessage says why a try that continues earlier's pull request
// starts at commit's checks (StartsAtReview).
func StartAtReviewMessage(commit, earlier string) string {
	return fmt.Sprintf("Continuing commit %s, which Task %s pushed and no one reviewed; its checks passed, "+
		"so the review starts with no implement run", short(commit), earlier)
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
