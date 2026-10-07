package agentrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JairJosafath/c4factory/internal/model"
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
	// Proposal is the Standard change the agent asks for: its branch, paths and
	// reason. The patch itself stays in Git.
	Proposal *model.Proposal `json:"proposal,omitempty"`
	// Empty is set when the round leaves nothing for the Task branch.
	Empty bool `json:"empty,omitempty"`
	// FollowUps are the reviewer's suggestions for later work.
	FollowUps []model.FollowUp `json:"followUps,omitempty"`
	// Unavailable means the agent's service couldn't work (a limit, an overload,
	// bad credentials); RetryAt is when it said it is back.
	Unavailable bool       `json:"unavailable,omitempty"`
	RetryAt     *time.Time `json:"retryAt,omitempty"`
	// Interrupted means c4agent was told to stop (SIGTERM: a node shutdown, an
	// eviction, a preemption) before the run finished: the infrastructure ended
	// it, not the agent.
	Interrupted bool `json:"interrupted,omitempty"`
	// CostMilliUSD is what the run's agent calls cost, in thousandths of a US
	// dollar; nil when the agent didn't report it.
	CostMilliUSD *int64 `json:"costMilliUSD,omitempty"`
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
	if r.Proposal != nil {
		p := *r.Proposal
		r.Proposal = &p
	}
	for stage := 0; ; {
		data, _ := json.Marshal(r)
		if len(data) <= MaxResultBytes {
			return data
		}
		switch {
		case len(r.FollowUps) > 0 && r.Verdict != VerdictApprove:
			// Follow-ups are filed only on merge; a summary asking for changes matters more.
			r.FollowUps = nil
		case len(r.Summary) > 200:
			r.Summary = shorten(r.Summary, len(r.Summary)-(len(data)-MaxResultBytes)-16)
		case len(r.Error) > 200:
			r.Error = shorten(r.Error, len(r.Error)-(len(data)-MaxResultBytes)-16)
		case r.Proposal != nil && len(r.Proposal.Reason) > 200:
			r.Proposal.Reason = shorten(r.Proposal.Reason, len(r.Proposal.Reason)-(len(data)-MaxResultBytes)-16)
		case len(r.FollowUps) > 0:
			// Escaping can make even short follow-ups too long; the verdict matters more.
			r.FollowUps = r.FollowUps[:len(r.FollowUps)-1]
		case stage == 0:
			stage++
			r.Summary, r.Error = shorten(r.Summary, 100), shorten(r.Error, 100)
			if r.Proposal != nil {
				r.Proposal.Reason = shorten(r.Proposal.Reason, 100)
			}
		case stage == 1:
			stage++
			r.Summary, r.Error = "", ""
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

// stopGrace is how long a failed run waits for c4agent to be told to stop. A
// node restart signals every process of the pod at once, so the agent may die
// before c4agent's own SIGTERM cancels ctx.
var stopGrace = 2 * time.Second

// Run does one job in dir and reports the outcome. Failures of the work itself
// are in Result.Error; the run as such does not fail. A run that fails when ctx
// is cancelled, or within stopGrace after, is Interrupted: c4agent was told to
// stop, and the kill of the agent that comes with it is not the agent's failure.
func Run(ctx context.Context, job Job, agent Agent, dir string) Result {
	meter := &costMeter{agent: agent}
	result := run(ctx, job, meter, dir)
	result.CostMilliUSD = meter.total()
	if result.Error != "" && !result.Unavailable && stoppedWithin(ctx, stopGrace) {
		result.Interrupted = true
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

func run(ctx context.Context, job Job, agent Agent, dir string) Result {
	result := Result{Role: job.Role}
	r, err := clone(ctx, job, dir)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if job.Role == RolePlan {
		return plan(ctx, job, agent, r, result)
	}
	ws := Workspace{Guide: r.showBase(ctx, job.Base, roleGuides[job.Role]), Conflicts: r.conflicts}
	ws.Related = cloneRelated(ctx, job, filepath.Join(filepath.Dir(dir), "related"))
	if job.Role == RoleReview {
		return review(ctx, job, agent, r, ws, result)
	}
	return implement(ctx, job, agent, r, ws, result)
}

func implement(ctx context.Context, job Job, agent Agent, r *repo, ws Workspace, result Result) Result {
	start := r.start
	summary, err := agent.Run(ctx, r.dir, ImplementPrompt(job, ws))
	result.Summary = summary
	if err != nil {
		return agentFailed(result, err)
	}

	checkOutput, checkErr := runCheck(ctx, job, r.dir)
	for attempt := 1; checkErr != nil && attempt <= checkFixAttempts && ctx.Err() == nil; attempt++ {
		prompt := fmt.Sprintf("%q fails. Fix every failure, then run it again until it passes. Output:\n%s",
			strings.Join(job.CheckCommand, " "), tail(checkOutput, 6000))
		if _, err := agent.Run(ctx, r.dir, prompt); err != nil {
			return agentFailed(result, err)
		}
		checkOutput, checkErr = runCheck(ctx, job, r.dir)
	}

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
	if head == start && !job.Resumed {
		result.Error = "the agent made no changes"
		return result
	}
	if job.Resumed {
		// The Standard change may have done the whole issue: nothing is left
		// beyond main, which a merge commit can hide from the head comparison.
		same, err := r.sameTreeAsBase(ctx, job.Base)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		if same {
			result.Empty = true
			return result
		}
	}
	changed, err := r.changedPaths(ctx, job.Base)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	split := model.SplitChange(changed, model.AgentProtectedPaths(job.ProtectedPaths), job.AllowedPaths, job.Issue > 0)
	if err := checkSplit(job, split); err != nil {
		result.Error = err.Error()
		return result
	}
	if len(split.Proposal) > 0 {
		reason := shorten(model.ProposalReason(summary), maxProposalReason)
		proposal := model.Proposal{Branch: model.ProposalBranch(job.Task), Paths: split.Proposal}
		if err := proposalFits(result, head, proposal); err != nil {
			result.Error = err.Error()
			return result
		}
		proposal.Reason = reason
		if checkErr == nil {
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
	if checkErr != nil {
		result.Error = fmt.Sprintf("%q still fails after %d attempts to fix it:\n%s",
			strings.Join(job.CheckCommand, " "), checkFixAttempts, tail(checkOutput, 2500))
		if len(split.Proposal) > 0 {
			result.Error += fmt.Sprintf("\nYour change to %s was not kept: a Standard change is filed only when the check passes with it. Make it again.",
				strings.Join(split.Proposal, ", "))
		}
	}
	return result
}

// maxProposalReason keeps a proposal's reason short enough for the result.
const maxProposalReason = 1000

// maxProposalPathBytes is the longest path a proposal may carry.
const maxProposalPathBytes = 256

// checkSplit fails a round whose change has paths nobody may change, or a
// proposal too large to travel in the result.
func checkSplit(job Job, split model.ChangeSplit) error {
	if len(split.Refused) > 0 {
		if len(job.AllowedPaths) > 0 {
			return fmt.Errorf("the change touches %s; this Task may change only %s",
				strings.Join(split.Refused, ", "), strings.Join(job.AllowedPaths, ", "))
		}
		return fmt.Errorf("the change touches %s, which the platform and the Standard protect; leave them as they are",
			strings.Join(split.Refused, ", "))
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

func review(ctx context.Context, job Job, agent Agent, r *repo, ws Workspace, result Result) Result {
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
	output, err := agent.Run(ctx, r.dir, ReviewPrompt(job, ws))
	if err != nil {
		return agentFailed(result, err)
	}
	verdict, err := ParseVerdict(output)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Verdict, result.Summary, result.FollowUps = verdict.Verdict, verdict.Summary, verdict.FollowUps
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
	cmd := exec.CommandContext(ctx, job.CheckCommand[0], job.CheckCommand[1:]...)
	cmd.Dir = dir
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
