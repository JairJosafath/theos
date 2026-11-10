/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package agentrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/JairJosafath/theos/internal/model"
)

// Result is what one sandbox run reports. It travels in the pod's termination
// message, so Encode keeps it under MaxResultBytes.
type Result struct {
	Role string `json:"role"`
	// Commit is the head of the branch after an implement run.
	Commit string `json:"commit,omitempty"`
	// Summary is the agent's own account, or the reviewer's findings.
	Summary string `json:"summary,omitempty"`
	// Verdict is approve or changes, for a review.
	Verdict string `json:"verdict,omitempty"`
	// Error says why the run failed. Feedback for the next round when set.
	Error string `json:"error,omitempty"`
	// CheckFailsOnBase is the end of the check's output on the base commit,
	// set when the check fails on the branch and on the base too: the
	// workspace can't judge the change, so CI decides it.
	CheckFailsOnBase string `json:"checkFailsOnBase,omitempty"`
	// Proposal is the Standard change the agent asks for: its branch, paths and
	// reason. The patch itself stays in Git.
	Proposal *model.Proposal `json:"proposal,omitempty"`
	// Extra are the unprotected paths a Standard change changes besides its
	// allowed ones, so its checks pass: the history and the pull request name them.
	Extra []string `json:"extra,omitempty"`
	// Empty is set when the round leaves nothing for the Task branch, or, in a
	// Standard change, leaves every path it is for as on the base.
	Empty bool `json:"empty,omitempty"`
	// Blocked is the implementer's blocked note (model.BlockedNote): it can't
	// start the issue, and says why.
	Blocked string `json:"blocked,omitempty"`
	// FollowUps are the reviewer's suggestions for later work.
	FollowUps []model.FollowUp `json:"followUps,omitempty"`
	// Open are the acceptance criteria an approving reviewer accepted as left
	// open, for a person.
	Open []model.OpenCriterion `json:"open,omitempty"`
	// Risk is why the reviewer calls the change it approves high-risk; empty
	// when it doesn't.
	Risk string `json:"risk,omitempty"`
	// Triage is a triager's decision about its issue.
	Triage *model.Triage `json:"triage,omitempty"`
	// Score is a judge run's verdicts, one per criterion.
	Score []model.ScoreCriterion `json:"score,omitempty"`
	// Unavailable means the agent's service couldn't work (a limit, an overload,
	// bad credentials); RetryAt is when it said it is back.
	Unavailable bool       `json:"unavailable,omitempty"`
	RetryAt     *time.Time `json:"retryAt,omitempty"`
	// Interrupted means theos-agent was told to stop (SIGTERM: a node shutdown, an
	// eviction, a preemption) before the run finished, or its Git host stayed
	// unreachable: the infrastructure ended it, not the agent.
	Interrupted bool `json:"interrupted,omitempty"`
	// CostMilliUSD is what the run's agent calls cost, in thousandths of a US
	// dollar; nil when the agent didn't report it.
	CostMilliUSD *int64 `json:"costMilliUSD,omitempty"`
	// Tokens is what the run's agent sessions used; nil when the agent didn't
	// report it.
	Tokens *model.TokenUsage `json:"tokens,omitempty"`
	// SessionHandoffs are the sizes, in tokens, at which the run's agent
	// sessions handed off to fresh ones.
	SessionHandoffs []int64 `json:"sessionHandoffs,omitempty"`
	// ResumedFrom is the kind of checkpoint the run went on from, hard or
	// clean; empty when it started afresh. The cost, tokens and handoffs are
	// this pod's: the operator counted the earlier pods' when they ended.
	ResumedFrom string `json:"resumedFrom,omitempty"`
	// Paused means the run stopped because the factory asked it to (the pod's
	// theos.factory.io/pause annotation) and saved a clean checkpoint: the
	// operator suspends the Sandbox with no round or rerun spent. PauseReason
	// is the annotation's value.
	Paused      bool   `json:"paused,omitempty"`
	PauseReason string `json:"pauseReason,omitempty"`
	// Checkpoint is the kind of checkpoint the run saved before it reported,
	// hard or clean; empty when it saved none. An unavailable agent's run
	// that saved one is suspended until the agent is back, and resumes.
	Checkpoint string `json:"checkpoint,omitempty"`
}

// MaxResultBytes fits a Kubernetes termination message (4096 bytes).
const MaxResultBytes = 4000

// Follow-ups share the termination message with the verdict, so they stay short.
// A criterion is a checklist item the work is judged by, so it gets room for a
// whole sentence: the reviewer is asked for at most maxCriterionChars, and the
// rest is margin.
const (
	maxFollowUpTitle     = 100
	maxFollowUpBody      = 400
	maxFollowUpCriterion = 240
	maxCriterionChars    = 200
	// maxTriageText bounds a triager's reason and question; its criteria are
	// bounded as a follow-up's.
	maxTriageText = 600
)

// Encode returns the Result as JSON of at most MaxResultBytes, shortening the
// follow-ups, the proposal's reason, the summary and the error when needed.
func (r Result) Encode() []byte {
	r.FollowUps = slices.Clone(r.FollowUps)
	for i := range r.FollowUps {
		r.FollowUps[i].Title = shortenWords(r.FollowUps[i].Title, maxFollowUpTitle)
		r.FollowUps[i].Body = shortenWords(r.FollowUps[i].Body, maxFollowUpBody)
		r.FollowUps[i].Criteria = slices.Clone(r.FollowUps[i].Criteria)
		for j, c := range r.FollowUps[i].Criteria {
			r.FollowUps[i].Criteria[j] = shortenWords(c, maxFollowUpCriterion)
		}
	}
	r.Open = slices.Clone(r.Open)
	for i := range r.Open {
		r.Open[i].Criterion = shortenWords(r.Open[i].Criterion, maxFollowUpCriterion)
		r.Open[i].Why = shortenWords(r.Open[i].Why, maxFollowUpCriterion)
	}
	r.Risk = shortenWords(r.Risk, maxFollowUpCriterion)
	if r.Proposal != nil {
		p := *r.Proposal
		r.Proposal = &p
	}
	if r.Triage != nil {
		t := *r.Triage
		t.Reason, t.Question = shortenWords(t.Reason, maxTriageText), shortenWords(t.Question, maxTriageText)
		t.Criteria = slices.Clone(t.Criteria)
		for i, c := range t.Criteria {
			t.Criteria[i] = shortenWords(c, maxFollowUpCriterion)
		}
		r.Triage = &t
	}
	for stage := 0; ; {
		data, _ := json.Marshal(r)
		if len(data) <= MaxResultBytes {
			return data
		}
		switch {
		case (len(r.FollowUps) > 0 || len(r.Open) > 0) && r.Verdict != VerdictApprove:
			// Follow-ups and open criteria are filed only on merge; a summary asking
			// for changes matters more.
			r.FollowUps, r.Open = nil, nil
		case len(r.Summary) > 200:
			r.Summary = shorten(r.Summary, len(r.Summary)-(len(data)-MaxResultBytes)-16)
		case len(r.Error) > 200:
			r.Error = shorten(r.Error, len(r.Error)-(len(data)-MaxResultBytes)-16)
		case len(r.CheckFailsOnBase) > 200:
			r.CheckFailsOnBase = shortenHead(r.CheckFailsOnBase, len(r.CheckFailsOnBase)-(len(data)-MaxResultBytes)-16)
		case len(r.Blocked) > 200:
			r.Blocked = shortenWords(r.Blocked, len(r.Blocked)-(len(data)-MaxResultBytes)-16)
		case r.Proposal != nil && len(r.Proposal.Reason) > 200:
			r.Proposal.Reason = shorten(r.Proposal.Reason, len(r.Proposal.Reason)-(len(data)-MaxResultBytes)-16)
		case len(r.FollowUps) > 0:
			// Escaping can make even short follow-ups too long; the verdict matters more.
			r.FollowUps = r.FollowUps[:len(r.FollowUps)-1]
		case len(r.Open) > 0:
			// Open criteria are work the issue asked for, so they go after the follow-ups.
			r.Open = r.Open[:len(r.Open)-1]
		case len(r.Extra) > 0:
			// The reviewer sees every path in the diff; the list only points at them.
			r.Extra = r.Extra[:len(r.Extra)-1]
		case r.Triage != nil && len(r.Triage.Criteria) > 1:
			// A ready decision keeps its first criteria; a person adds the rest.
			r.Triage.Criteria = r.Triage.Criteria[:len(r.Triage.Criteria)-1]
		case r.Triage != nil && len(r.Triage.Reason)+len(r.Triage.Question) > 400:
			r.Triage.Reason, r.Triage.Question = shortenWords(r.Triage.Reason, 200), shortenWords(r.Triage.Question, 200)
		case stage == 0:
			stage++
			r.Summary, r.Error = shorten(r.Summary, 100), shorten(r.Error, 100)
			r.CheckFailsOnBase = shortenHead(r.CheckFailsOnBase, 100)
			if r.Proposal != nil {
				r.Proposal.Reason = shorten(r.Proposal.Reason, 100)
			}
		case stage == 1:
			stage++
			r.Summary, r.Error, r.CheckFailsOnBase = "", "", ""
			if r.Proposal != nil {
				r.Proposal.Reason = ""
			}
		default:
			// Only the branch and paths are left; the caller refuses a proposal
			// whose paths don't fit, so this cannot happen from implement.
			return data
		}
	}
}

func shorten(s string, n int) string {
	if n < 0 {
		n = 0
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// shortenHead is the end of s in at most n bytes, after "…" when cut: the last
// lines of an output are the ones that say why it failed.
func shortenHead(s string, n int) string {
	if n < 0 {
		n = 0
	}
	if len(s) <= n {
		return s
	}
	cut := len(s) - n
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	return "…" + s[cut:]
}

// shortenWords is s in at most n bytes: whole, or cut after its last whole word
// that fits and ended with "…", so text a person reads is never cut mid-word. A
// single word longer than n is cut at a rune.
func shortenWords(s string, n int) string {
	const ellipsis = "…"
	if len(s) <= n {
		return s
	}
	cut := max(n-len(ellipsis), 0)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	if i := strings.LastIndexAny(s[:cut+1], " \t\n"); i > 0 {
		if word := strings.TrimRight(s[:i], " \t\n,.;:"); word != "" {
			return word + ellipsis
		}
	}
	return s[:cut] + ellipsis
}

// checkFixAttempts is how often the agent is sent back when the check fails.
const checkFixAttempts = 2

// stopGrace is how long a failed run waits for theos-agent to be told to stop. A
// node restart signals every process of the pod at once, so the agent may die
// before theos-agent's own SIGTERM cancels ctx.
var stopGrace = 2 * time.Second

// Run does one job in dir and reports the outcome. Failures of the work itself
// are in Result.Error; the run as such does not fail. A run that fails when ctx
// is cancelled, or within stopGrace after, is Interrupted: theos-agent was told to
// stop, and the kill of the agent that comes with it is not the agent's failure.
// So is a run whose Git host stayed unreachable through every retry: a Gitea
// restart, not the agent, failed it.
//
// With a checkpoint directory, a run told to stop saves a hard checkpoint
// there before it reports, and a run that finds one goes on from it
// (checkpoint.go). Between tool calls, a run saves a hard checkpoint every
// CheckpointEvery and goes on, for a pod lost with no warning (periodic.go). A run the factory pauses stops at its next tool boundary
// or step, saves a clean checkpoint (a hard one when it is still busy
// pauseGrace later), and reports Paused (pause.go). A run whose agent's
// service was unavailable saves a clean checkpoint before it reports
// Unavailable, to go on from when the agent is back; one theos-agent was told to
// stop is interrupted instead, whatever the agent printed.
func Run(ctx context.Context, job Job, agent Agent, dir string) Result {
	sessions := &sessionLog{}
	pause := newPauseWatch(job)
	runCtx, checks, stopWatch := pause.start(ctx)
	defer stopWatch()
	prog := &runProgress{resumable: resumable(agent), checks: checks}
	meter := &usageMeter{}
	meter.agent = withSessionLog(agent, sessions, pause, newPeriodicCheckpoints(job, prog, sessions, meter))
	result := run(runCtx, job, pausingAgent{agent: meter, pause: pause}, dir, prog)
	stopWatch()
	result.CostMilliUSD, result.Tokens = meter.total(), meter.totalTokens()
	result.SessionHandoffs = meter.handoffs
	if prog.from != nil {
		result.ResumedFrom = prog.from.Kind
	}
	// A run the pause stopped didn't finish: its error is the pause's, or the
	// grace or a stop ended it after the pause was asked.
	if paused, reason, hard := pause.state(); paused &&
		(result.Error == errPaused.Error() || hard || ctx.Err() != nil && result.Error != "") {
		kind := CheckpointClean
		if hard || ctx.Err() != nil {
			kind = CheckpointHard
		}
		slog.Info("Paused by the factory", "reason", reason, "checkpoint", kind, "step", prog.step)
		if saved, err := saveCheckpoint(context.Background(), job, prog, sessions, result, kind, &reason); err != nil {
			slog.Error("Failed to save the checkpoint; a resume starts from an earlier one, else over", "error", err.Error())
		} else if saved {
			result.Checkpoint = kind
		}
		result.Paused, result.PauseReason = true, reason
		result.Error, result.Interrupted, result.Unavailable, result.RetryAt = "", false, false, nil
		result.Commit, result.Blocked = "", ""
		return result
	}
	// A run theos-agent was told to stop (a SIGTERM, or the run's deadline) is
	// interrupted, whatever the agent printed before it was killed: an
	// earlier "overloaded" in its output doesn't make the stop a wait for the
	// service, which would resume it with no round spent (#1996).
	if result.Unavailable && ctx.Err() != nil {
		result.Unavailable, result.RetryAt = false, nil
	}
	if result.Error != "" && !result.Unavailable &&
		(unreachableHost(result.Error) || stoppedWithin(ctx, stopGrace)) {
		result.Interrupted = true
	}
	if result.Interrupted && ctx.Err() != nil {
		if saved, err := saveCheckpoint(context.Background(), job, prog, sessions, result, CheckpointHard, nil); err != nil {
			slog.Error("Failed to save the checkpoint; a resume starts from an earlier one, else over", "error", err.Error())
		} else if saved {
			result.Checkpoint = CheckpointHard
		}
	}
	if result.Unavailable {
		// The agent's own process ended with the service's error, and
		// nobody stopped theos-agent: its transcript is whole, and the same
		// session can go on.
		kind := CheckpointClean
		if saved, err := saveCheckpoint(context.Background(), job, prog, sessions, result, kind, nil); err != nil {
			slog.Error("Failed to save the checkpoint; the step runs again when the agent is back",
				"error", err.Error())
		} else if saved {
			slog.Info("The agent is unavailable; saved the run to go on when it is back", "checkpoint", kind,
				"step", prog.step)
			result.Checkpoint = kind
		}
	}
	return result
}

func stoppedWithin(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-timer.C:
		return false
	}
}

func run(ctx context.Context, job Job, agent Agent, dir string, prog *runProgress) Result {
	result := Result{Role: job.Role}
	r, err := clone(ctx, job, dir)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	prog.repo, prog.step = r, StepAgent
	if prog.from, err = restoreCheckpoint(ctx, job, r); err != nil {
		result.Error = err.Error()
		return result
	} else if prog.from != nil {
		prog.step, prog.summary = prog.from.Step, prog.from.Summary
		prog.resume = resumeSession(job, prog.from, prog.resumable, r.dir)
	}
	if job.Role == RolePlan {
		return plan(ctx, job, agent, r, prog, result)
	}
	ws := Workspace{Guide: r.showBase(ctx, job.Base, roleGuides[guideFor(job)]), Conflicts: r.conflicts}
	ws.Related = cloneRelated(ctx, job, filepath.Join(filepath.Dir(dir), "related"))
	if job.Role == RoleReview {
		return review(ctx, job, agent, r, ws, prog, result)
	}
	if job.Role == RoleVerify {
		return verify(ctx, job, agent, r, ws, prog, result)
	}
	if job.Role == RoleTriage {
		return triage(ctx, job, agent, r, ws, prog, result)
	}
	if job.Role == RoleScore {
		return score(ctx, job, agent, r, ws, prog, result)
	}
	return implement(ctx, job, agent, r, ws, prog, result)
}

func implement(ctx context.Context, job Job, agent Agent, r *repo, ws Workspace, prog *runProgress,
	result Result) Result {
	start := r.start
	check := strings.Join(job.CheckCommand, " ")
	base := baseCheck{job: job, r: r}
	var summary string
	// Every session of the round starts fresh, so each carries the round's
	// prompt, the issue included (#2037, #25).
	prompt := implementPrompt(job, ws)
	if prog.resumesAt(StepCheck) {
		// The agent's session had ended: the run goes on at its check.
		summary = prog.from.Summary
	} else {
		var err error
		if summary, err = prog.session(ctx, agent, r.dir, prompt); err != nil {
			result.Summary = summary
			return agentFailed(result, err)
		}
		if model.BlockedNote(summary) != "" {
			if summary, err = sendBackBlocked(ctx, prog.checksContext(ctx), job, agent, r.dir, &base, prompt,
				summary); err != nil {
				result.Summary = summary
				return agentFailed(result, err)
			}
			if note := model.BlockedNote(summary); note != "" {
				// The agent can't start the issue and says why: nothing it left is kept.
				result.Summary, result.Blocked = summary, note
				return result
			}
		}
	}
	result.Summary = summary
	prog.step, prog.summary = StepCheck, summary

	checkOutput, baseOutput, checkErr, err := checkAndFix(ctx, prog.checksContext(ctx), job, agent, r.dir, &base,
		prompt, prog.checkNote())
	if err != nil {
		return agentFailed(result, err)
	}
	// With the check failing on the base too, the workspace can't judge the
	// change: CI does.
	failsOnBase := checkErr != nil && baseOutput != ""

	message := fmt.Sprintf("%s\n\nTask %s, round %d.", firstLine(job.Goal), job.Task, job.Round)
	if err := r.commitAll(ctx, message); err != nil {
		result.Error = err.Error()
		return result
	}
	head, err := r.head(ctx)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if head == start && !job.Resumed && job.PullRequest == 0 {
		// With a pull request, the round may have changed only it: the
		// reviewer reviews the branch as it is.
		result.Error = model.NoChangeError(summary)
		return result
	}
	changed, empty, err := roundChange(ctx, job, r)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if empty {
		result.Empty = true
		return result
	}
	if refusal := specScope(job, changed); refusal != "" {
		// A spec changes its spec file alone: nothing else is pushed.
		result.Error = refusal
		return result
	}
	split := model.SplitChange(changed, protectedList(job), job.AllowedPaths, job.Issue > 0)
	if err := checkSplit(job, split); err != nil {
		result.Error = err.Error()
		return result
	}
	if len(job.AllowedPaths) > 0 {
		result.Extra = model.StandardChangeExtras(split.Task, job.AllowedPaths)
	}
	if len(split.Proposal) > 0 {
		reason := shorten(model.ProposalReason(summary), maxProposalReason)
		proposal := model.Proposal{Branch: model.ProposalBranch(job.Task), Paths: split.Proposal}
		if err := proposalFits(result, head, proposal); err != nil {
			result.Error = err.Error()
			return result
		}
		proposal.Reason = reason
		// A proposal's own gated Task runs CI before it merges.
		if checkErr == nil || failsOnBase {
			if err := r.proposeBranch(ctx, job.Base, proposal.Branch, proposal.Paths, message); err != nil {
				result.Error = err.Error()
				return result
			}
			result.Proposal = &proposal
		}
		if err := r.restorePaths(ctx, job.Base, proposal.Paths, "Leave "+strings.Join(proposal.Paths, ", ")+" to the Standard change\n\nTask "+job.Task+", round "+strconv.Itoa(job.Round)+"."); err != nil {
			result.Proposal, result.Error = nil, err.Error()
			return result
		}
		if head, err = r.head(ctx); err != nil {
			result.Proposal, result.Error = nil, err.Error()
			return result
		}
	}
	// Push even when the check fails, so the next round continues from here.
	if err := r.push(ctx, job.Branch); err != nil {
		result.Error = err.Error()
		return result
	}
	result.Commit = head
	switch {
	case failsOnBase:
		result.CheckFailsOnBase = tail(baseOutput, maxCheckFailsOnBase)
	case checkErr != nil:
		result.Error = fmt.Sprintf("%q still fails after %d attempts to fix it:\n%s",
			check, checkFixAttempts, tail(checkOutput, 2500))
		if len(split.Proposal) > 0 {
			result.Error += fmt.Sprintf("\nYour change to %s was not kept: a Standard change is filed only when the check passes with it. Make it again.",
				strings.Join(split.Proposal, ", "))
		}
	}
	return result
}

// roundChange returns the paths the round's branch changes beside the base,
// and whether it leaves nothing to merge.
func roundChange(ctx context.Context, job Job, r *repo) (changed []string, empty bool, err error) {
	if job.Resumed {
		// The Standard change may have done the whole issue: nothing is left
		// beyond main, which a merge commit can hide from the head comparison.
		if same, err := r.sameTreeAsBase(ctx, job.Base); err != nil || same {
			return nil, same, err
		}
	}
	if changed, err = r.changedPaths(ctx, job.Base); err != nil {
		return nil, false, err
	}
	// A Standard change that leaves its paths as on the base, often because
	// the round undid it on review, has nothing to merge: no review or round
	// can change that.
	return changed, len(job.AllowedPaths) > 0 && model.LeavesAllowedAsOnBase(changed, job.AllowedPaths), nil
}

// sendBackBlocked sends an agent that wrote a blocked note back once when the
// check fails on the base: that is the workspace's, not a reason to block. It
// returns the agent's newest summary.
//
// The send-back is a fresh session, so its prompt is the round's own prompt,
// the issue included, then what the agent answered the first time and why it
// is sent back: a session told only that it stopped never saw the issue
// (#2037).
func sendBackBlocked(ctx, checks context.Context, job Job, agent Agent, dir string, base *baseCheck,
	prompt, summary string) (string, error) {
	baseOutput := base.fails(checks)
	if checks.Err() != nil && ctx.Err() == nil {
		// The pause killed the base's check (#1995): it runs again on resume.
		return summary, errPaused
	}
	if baseOutput == "" {
		return summary, nil
	}
	return agent.Run(ctx, dir, sendBackPrompt(job, prompt, summary, baseOutput))
}

// maxSendBackAnswer is how much of the agent's first answer a send-back
// prompt quotes.
const maxSendBackAnswer = 4000

// sendBackPrompt is the prompt of an agent sent back after a blocked note:
// the round's prompt, the agent's first answer, and the base's failure.
func sendBackPrompt(job Job, prompt, summary, baseOutput string) string {
	return laterSessionPrompt(prompt, fmt.Sprintf("An earlier session in this workspace worked on the task above and "+
		"stopped with a %q line. Its answer:\n%s\n\n%q fails on %s in this workspace too, before any change:\n%s\n"+
		"It was green in CI when this Task started, so that is not a reason to block the issue: CI runs the "+
		"check and decides it. Do the issue above now, going on from what the working tree holds, "+
		"and end your summary with a %q line again only if something else blocks it.",
		model.BlockedPrefix, tail(summary, maxSendBackAnswer),
		strings.Join(job.CheckCommand, " "), job.Base, tail(baseOutput, 3000), model.BlockedPrefix))
}

// laterSessionPrompt is the prompt of a fresh session that goes on from an
// earlier one in the round: the round's prompt, the issue and its rules
// included, then what this session is for. A session told only that a check
// fails never saw the issue (#2037, #25).
func laterSessionPrompt(prompt, body string) string {
	return strings.TrimRight(prompt, "\n") + "\n\n---\n\n" + body
}

// fixPrompt is the prompt of an agent sent back because the check fails on
// its branch: the round's prompt, then the failure.
func fixPrompt(prompt, failure string) string {
	return laterSessionPrompt(prompt, "An earlier session in this workspace worked on the task above. "+failure)
}

// checkAndFix runs the check, and while it fails sends the agent back up to
// checkFixAttempts times, telling it when the check fails on the base too. It
// returns the check's last output, the base's output when the base fails too,
// the check's error, and the agent's. The checks run in checks, which the
// pause ends: a check it killed is errPaused, and runs again on resume.
//
// Each fix prompt is a fresh session's, so it starts with roundPrompt, the
// round's own prompt (#25). note, when set, starts the first fix prompt's
// failure: it tells a run that went on from a checkpoint at its check why the
// earlier one stopped.
func checkAndFix(ctx, checks context.Context, job Job, agent Agent, dir string, base *baseCheck,
	roundPrompt, note string) (string, string, error, error) {
	check := strings.Join(job.CheckCommand, " ")
	paused := func() bool { return checks.Err() != nil && ctx.Err() == nil }
	checkOutput, checkErr := runCheck(checks, job, dir)
	if paused() {
		return checkOutput, "", checkErr, errPaused
	}
	var baseOutput string
	if checkErr != nil {
		baseOutput = base.fails(checks)
	}
	if paused() {
		return checkOutput, "", checkErr, errPaused
	}
	for attempt := 1; checkErr != nil && attempt <= checkFixAttempts && ctx.Err() == nil; attempt++ {
		prompt := fmt.Sprintf("%q fails. Fix every failure, then run it again until it passes. Output:\n%s",
			check, tail(checkOutput, 6000))
		if baseOutput != "" {
			prompt = fmt.Sprintf("%q fails. It fails on %s in this workspace too, before your change:\n%s\n"+
				"It was green in CI when this Task started, so that failure is the workspace's. Fix the failures your change causes, and leave the rest to CI, which runs the check and decides it. "+
				"Then run it again. Output on your branch:\n%s",
				check, job.Base, tail(baseOutput, 2000), tail(checkOutput, 6000))
		}
		if note != "" {
			prompt, note = note+"\n\n"+prompt, ""
		}
		if _, err := agent.Run(ctx, dir, fixPrompt(roundPrompt, prompt)); err != nil {
			return checkOutput, baseOutput, checkErr, err
		}
		checkOutput, checkErr = runCheck(checks, job, dir)
		if paused() {
			return checkOutput, baseOutput, checkErr, errPaused
		}
	}
	return checkOutput, baseOutput, checkErr, nil
}

// maxCheckFailsOnBase is how much of the base's check output the result carries.
const maxCheckFailsOnBase = 1000

// baseCheck runs the check on the base commit at most once per run, in a
// worktree of the clone beside it, so the agent's work is left alone.
type baseCheck struct {
	job    Job
	r      *repo
	ran    bool
	output string
}

// fails returns the end of the check's output on the base when it fails
// there, and "" when it passes. A base that can't be checked out counts as
// passing: the round is then judged as it was before the base check.
func (b *baseCheck) fails(ctx context.Context) string {
	if b.ran {
		return b.output
	}
	b.ran = true
	dir, err := os.MkdirTemp(filepath.Dir(b.r.dir), "base-check-")
	if err != nil {
		return ""
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if _, err := b.r.git(ctx, b.r.dir, "worktree", "add", "--quiet", "--detach", dir, "origin/"+b.job.Base); err != nil {
		return ""
	}
	defer func() { _, _ = b.r.git(ctx, b.r.dir, "worktree", "remove", "--force", dir) }()
	output, err := runCheck(ctx, b.job, dir)
	var exitErr *exec.ExitError
	// A check stopped with the run proves nothing about the base.
	if errors.As(err, &exitErr) && ctx.Err() == nil {
		b.output = strings.TrimSpace(output)
		if b.output == "" {
			b.output = err.Error()
		}
	}
	return b.output
}

// maxProposalReason keeps a proposal's reason short enough for the result.
const maxProposalReason = 1000

// maxProposalPathBytes is the longest path a proposal may carry.
const maxProposalPathBytes = 256

// checkSplit fails a round whose change has paths nobody may change, or a
// proposal too large to travel in the result.
func checkSplit(job Job, split model.ChangeSplit) error {
	if len(split.Refused) > 0 {
		return errors.New(model.ScopeRefusal(split.Refused, job.AllowedPaths))
	}
	if n := len(split.Proposal); n > model.MaxProposalPaths {
		return fmt.Errorf("the Standard change touches %d protected files; at most %d go in one", n, model.MaxProposalPaths)
	}
	return nil
}

// proposalFits refuses a proposal whose branch and paths alone would not fit the
// result with room left for the summary and the error.
func proposalFits(result Result, head string, p model.Proposal) error {
	for _, path := range p.Paths {
		if len(path) > maxProposalPathBytes {
			return fmt.Errorf("the Standard change names a path of %d bytes; at most %d fit", len(path), maxProposalPathBytes)
		}
	}
	result.Commit, result.Proposal, result.Summary, result.Error = head, &p, "", ""
	if data, _ := json.Marshal(result); len(data) > MaxResultBytes-1000 {
		return errors.New("the Standard change names too many or too long paths to report; split it into smaller changes")
	}
	return nil
}

func review(ctx context.Context, job Job, agent Agent, r *repo, ws Workspace, prog *runProgress,
	result Result) Result {
	head, err := r.head(ctx)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Commit = head
	if ws.Images, err = r.changedImages(ctx, job.Base); err != nil {
		result.Error = err.Error()
		return result
	}
	output, err := prog.session(ctx, agent, r.dir, reviewPrompt(job, ws))
	if err != nil {
		return agentFailed(result, err)
	}
	verdict, err := ParseVerdict(output)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Verdict, result.Summary, result.FollowUps = verdict.Verdict, verdict.Summary, verdict.FollowUps
	result.Open, result.Risk = verdict.Open, verdict.Risk
	return result
}

// agentFailed reports an agent run that failed, telling an unavailable service
// (which costs no round) from the agent's own failure.
func agentFailed(result Result, err error) Result {
	result.Error = err.Error()
	if down, back := unavailable(result.Error, time.Now()); down {
		result.Unavailable = true
		if !back.IsZero() {
			result.RetryAt = &back
		}
	}
	return result
}

func runCheck(ctx context.Context, job Job, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, job.CheckCommand[0], job.CheckCommand[1:]...) //nolint:gosec // the Standard's check; running it is the runner's job
	cmd.Dir = dir
	// A check the pause or a stop ends ends what it started too, such as the
	// tests make runs, which would hold its output open.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = stopWait
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return err.Error(), err
	}
	return out.String(), err
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return shorten(line, 72)
}
