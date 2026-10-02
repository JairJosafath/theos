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
	// FollowUps are the reviewer's suggestions for later work.
	FollowUps []model.FollowUp `json:"followUps,omitempty"`
	// Unavailable means the agent's service couldn't work (a limit, an overload,
	// bad credentials); RetryAt is when it said it is back.
	Unavailable bool       `json:"unavailable,omitempty"`
	RetryAt     *time.Time `json:"retryAt,omitempty"`
}

// MaxResultBytes fits a Kubernetes termination message (4096 bytes).
const MaxResultBytes = 4000

// Follow-ups share the termination message with the verdict, so they stay short.
const (
	maxFollowUpTitle = 100
	maxFollowUpBody  = 400
)

// Encode returns the Result as JSON of at most MaxResultBytes, shortening the
// follow-ups, summary and error when needed.
func (r Result) Encode() []byte {
	r.FollowUps = slices.Clone(r.FollowUps)
	for i := range r.FollowUps {
		r.FollowUps[i].Title = shorten(r.FollowUps[i].Title, maxFollowUpTitle)
		r.FollowUps[i].Body = shorten(r.FollowUps[i].Body, maxFollowUpBody)
	}
	for {
		data, _ := json.Marshal(r)
		if len(data) <= MaxResultBytes {
			return data
		}
		switch {
		case len(r.Summary) > 200:
			r.Summary = shorten(r.Summary, len(r.Summary)-(len(data)-MaxResultBytes)-16)
		case len(r.Error) > 200:
			r.Error = shorten(r.Error, len(r.Error)-(len(data)-MaxResultBytes)-16)
		case len(r.FollowUps) > 0:
			// Escaping can make even short follow-ups too long; the verdict matters more.
			r.FollowUps = r.FollowUps[:len(r.FollowUps)-1]
		default:
			r.Summary, r.Error = shorten(r.Summary, 100), shorten(r.Error, 100)
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

// checkFixAttempts is how often the agent is sent back when the check fails.
const checkFixAttempts = 2

// Run does one job in dir and reports the outcome. Failures of the work itself
// are in Result.Error; the run as such does not fail.
func Run(ctx context.Context, job Job, agent Agent, dir string) Result {
	result := Result{Role: job.Role}
	r, err := clone(ctx, job, dir)
	if err != nil {
		result.Error = err.Error()
		return result
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
	if head == start {
		result.Error = "the agent made no changes"
		return result
	}
	changed, err := r.changedPaths(ctx, job.Base)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if touched := model.TouchedPaths(changed, model.AgentProtectedPaths(job.ProtectedPaths)); len(touched) > 0 {
		result.Error = fmt.Sprintf("the change touches %s, which the platform and the Standard protect; leave them as they are",
			strings.Join(touched, ", "))
		return result
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
	}
	return result
}

func review(ctx context.Context, job Job, agent Agent, r *repo, ws Workspace, result Result) Result {
	head, err := r.head(ctx)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Commit = head
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
