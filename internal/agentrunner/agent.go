package agentrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/JairJosafath/c4factory/internal/model"
)

// Agent does the work in a checked-out repository and answers in text.
type Agent interface {
	Run(ctx context.Context, dir, prompt string) (string, error)
}

// ClaudeAgent runs Claude Code headless. The sandbox is the fence, so it runs
// without permission prompts.
type ClaudeAgent struct {
	// Command is the CLI and its flags; the prompt goes to its standard input.
	Command []string
	// Env is added to the agent's environment.
	Env []string
	// Model is passed with --model; empty leaves the CLI's default.
	Model string
}

// DefaultClaudeCommand runs Claude Code once, headless, with JSON output.
var DefaultClaudeCommand = []string{
	"claude", "-p", "--dangerously-skip-permissions", "--output-format", "json",
}

// Run runs the agent in dir and returns its final answer.
func (a ClaudeAgent) Run(ctx context.Context, dir, prompt string) (string, error) {
	command := a.Command
	if len(command) == 0 {
		command = DefaultClaudeCommand
	}
	if a.Model != "" {
		command = append(slices.Clone(command), "--model", a.Model)
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = append(os.Environ(), a.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	var out struct {
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
		Subtype string `json:"subtype"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		if runErr != nil {
			return "", fmt.Errorf("%s failed: %v: %s", command[0], runErr, tail(stderr.String()+stdout.String(), 1500))
		}
		// Plain text output, from a custom command.
		return strings.TrimSpace(stdout.String()), nil
	}
	if out.IsError || runErr != nil {
		return out.Result, fmt.Errorf("%s ended with %s: %s", command[0], orDefault(out.Subtype, "an error"),
			tail(out.Result+" "+stderr.String(), 1500))
	}
	return out.Result, nil
}

// FakeAgent stands in for a real agent in tests and demos. Implementing, it adds
// a line to STAND-IN.md; reviewing, it approves, or first asks for changes, and
// may file a follow-up; planning, it plans from the catalog alone
// (model.StandInPlan).
type FakeAgent struct {
	Role  string
	Round int
	// ChangesFirst makes the review ask for changes in round 1.
	ChangesFirst bool
	// NoChange makes implementing change nothing.
	NoChange bool
	// FollowUp makes the approval file one follow-up with acceptance criteria.
	FollowUp bool
}

// Run does the scripted work.
func (a FakeAgent) Run(_ context.Context, dir, _ string) (string, error) {
	if a.Role == RolePlan {
		return a.plan(dir)
	}
	if a.Role == RoleReview {
		if a.ChangesFirst && a.Round == 1 {
			return `{"verdict": "changes", "summary": "Stand-in review: add a second line."}`, nil
		}
		if a.FollowUp {
			return `Looked at it.` + "\n" + `{"verdict": "approve", "summary": "Stand-in review: fine.", ` +
				`"followUps": [{"title": "Stand-in follow-up: add a third line", ` +
				`"body": "The stand-in reviewer files this to show a follow-up.", ` +
				`"criteria": ["STAND-IN.md has a third line", "make check passes"]}]}`, nil
		}
		return `Looked at it.` + "\n" + `{"verdict": "approve", "summary": "Stand-in review: fine."}`, nil
	}
	if a.NoChange {
		return "Nothing to do.", nil
	}
	f, err := os.OpenFile(filepath.Join(dir, "STAND-IN.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintf(f, "Round %d of the stand-in agent.\n", a.Round); err != nil {
		return "", err
	}
	return fmt.Sprintf("Added round %d to STAND-IN.md.", a.Round), nil
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// unavailableSigns are what Claude Code and the API say when the service, not the
// agent, can't go on: usage and rate limits, overload, and bad credentials.
var unavailableSigns = regexp.MustCompile(`(?i)usage limit|limit reached|hit your limit|rate_limit_error|` +
	`api error: 429|api error: 529|overloaded_error|authentication_error|invalid api key|/login|oauth token has expired`)

// resetEpoch is the reset time Claude Code puts after a usage limit: "...|1759417200".
var resetEpoch = regexp.MustCompile(`limit reached\|(\d{10})`)

// unavailable reports whether output says the agent's service is unavailable,
// and when it is back, if it said; a reset in the past means now.
func unavailable(output string, now time.Time) (bool, time.Time) {
	if !unavailableSigns.MatchString(output) {
		return false, time.Time{}
	}
	if m := resetEpoch.FindStringSubmatch(output); m != nil {
		if epoch, err := strconv.ParseInt(m[1], 10, 64); err == nil && time.Unix(epoch, 0).After(now) {
			return true, time.Unix(epoch, 0).UTC()
		}
	}
	return true, time.Time{}
}

// plan writes the stand-in's plan for the workspace the operator wrote on main.
func (a FakeAgent) plan(dir string) (string, error) {
	workspace := map[string]string{}
	for _, name := range model.WorkspaceFiles {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return "", err
		}
		workspace[name] = string(data)
	}
	plan, err := model.StandInPlan(workspace)
	if err != nil {
		return "", err
	}
	for name, data := range plan {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("Round %d: the stand-in planner planned from the catalog alone.", a.Round), nil
}
