package agentrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
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
	// SessionTokenLimit, with Guard, hands a session whose context passes it
	// to a fresh one (session.go); zero runs one session to its end.
	SessionTokenLimit int64
	// Guard is the program whose session-guard subcommand guards a session:
	// c4agent itself.
	Guard string
}

// DefaultClaudeCommand runs Claude Code once, headless, streaming every turn
// as JSON lines, so each turn's tokens can be read.
var DefaultClaudeCommand = []string{
	"claude", "-p", "--dangerously-skip-permissions", "--output-format", "stream-json", "--verbose",
}

// Usage is what one agent call cost and the tokens it used, as the agent
// reported them.
type Usage struct {
	MilliUSD int64
	// Reported is false when the agent's output didn't say what the call cost.
	Reported bool
	// Tokens is nil when the agent's output didn't say what tokens the call used.
	Tokens *model.TokenUsage
	// Handoffs are the sizes at which the call's sessions handed off to fresh ones.
	Handoffs []int64
}

// UsageReporter is an Agent that says what each call cost and used. The calls
// of an Agent that isn't one leave the run's cost and tokens unreported.
type UsageReporter interface {
	RunReportingUsage(ctx context.Context, dir, prompt string) (string, Usage, error)
}

// Run runs the agent in dir and returns its final answer.
func (a ClaudeAgent) Run(ctx context.Context, dir, prompt string) (string, error) {
	answer, _, err := a.RunReportingUsage(ctx, dir, prompt)
	return answer, err
}

// RunReportingUsage runs the agent in dir and returns its final answer and
// what the call cost and used, from the CLI's stream-json output, or the one
// JSON object a custom command prints.
func (a ClaudeAgent) RunReportingUsage(ctx context.Context, dir, prompt string) (string, Usage, error) {
	if a.SessionTokenLimit > 0 && a.Guard != "" {
		return a.runSessions(ctx, dir, prompt)
	}
	s := a.runSession(ctx, dir, prompt, nil)
	return s.answer, s.usage, s.err
}

// stopWait is how long a session c4agent ended may hold its output open,
// through a process it started.
const stopWait = 10 * time.Second

// runSession runs one Claude Code session; with a guard, it is guarded and
// ended when it passes the guard's stopAt.
func (a ClaudeAgent) runSession(ctx context.Context, dir, prompt string, guard *sessionGuard) session {
	command := a.Command
	if len(command) == 0 {
		command = DefaultClaudeCommand
	}
	if a.Model != "" {
		command = append(slices.Clone(command), "--model", a.Model)
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var s session
	stdout := &streamWatch{}
	if guard != nil {
		command = append(slices.Clone(command), "--settings", guard.settings())
		stdout.stopAt = guard.stopAt()
		stdout.stop = func() { s.stopped = true; cancel() }
	}
	cmd := exec.CommandContext(sessionCtx, command[0], command[1:]...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = append(os.Environ(), a.Env...)
	cmd.WaitDelay = stopWait
	// Ending the session ends what its tools started, such as a check that
	// would go on changing the working tree under the next session.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = stdout, &stderr
	runErr := cmd.Run()

	output := stdout.buf.String()
	out, ok, streamed := parseClaudeOutput(stdout.buf.Bytes())
	s.size, s.lastText = out.mainSession, out.lastText
	if !ok {
		switch {
		case s.stopped:
			// No result line: its turns say what it used; what it cost is unknown.
			s.usage.Tokens = &out.turns
			s.err = fmt.Errorf("c4agent ended the session at %d tokens", s.size)
		case runErr != nil:
			// stderr goes last: a stream's lines come before it and the tail
			// keeps what says why, such as a limit or an overload.
			s.err = fmt.Errorf("%s failed: %v: %s", command[0], runErr, tail(output+"\n"+stderr.String(), 1500))
		case streamed:
			s.err = fmt.Errorf("%s ended without a result: %s", command[0], tail(output+"\n"+stderr.String(), 1500))
		default:
			// Plain text output, from a custom command.
			s.answer = strings.TrimSpace(output)
		}
		return s
	}
	s.answer, s.usage.Tokens = out.Result, out.tokens()
	if out.CostUSD != nil && *out.CostUSD >= 0 {
		s.usage.MilliUSD, s.usage.Reported = int64(math.Round(*out.CostUSD*1000)), true
	}
	if out.IsError || runErr != nil {
		s.err = fmt.Errorf("%s ended with %s: %s", command[0], orDefault(out.Subtype, "an error"),
			tail(out.Result+" "+stderr.String(), 1500))
	}
	return s
}

// claudeResult is Claude Code's answer: the one JSON object of
// --output-format json, or the last line of stream-json.
type claudeResult struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Subtype string `json:"subtype"`
	// CostUSD is missing when the CLI doesn't report it.
	CostUSD *float64 `json:"total_cost_usd"`
	// Usage is the main session's tokens; ModelUsage covers its subagents
	// too, per model.
	Usage      *claudeUsage                `json:"usage"`
	ModelUsage map[string]claudeModelUsage `json:"modelUsage"`
	// largestSession is the biggest turn's prompt of any one session, and
	// mainSession the main session's, from the stream's assistant lines; zero
	// without a stream.
	largestSession, mainSession int64
	// lastText is the main session's last words on the stream.
	lastText string
	// turns adds up the stream's turns: what a session that ended without a
	// result line used.
	turns model.TokenUsage
}

type claudeUsage struct {
	Input      int64 `json:"input_tokens"`
	Output     int64 `json:"output_tokens"`
	CacheRead  int64 `json:"cache_read_input_tokens"`
	CacheWrite int64 `json:"cache_creation_input_tokens"`
}

// prompt is a turn's prompt: everything its session held so far.
func (u claudeUsage) prompt() int64 { return u.Input + u.CacheRead + u.CacheWrite }

type claudeModelUsage struct {
	Input      int64 `json:"inputTokens"`
	Output     int64 `json:"outputTokens"`
	CacheRead  int64 `json:"cacheReadInputTokens"`
	CacheWrite int64 `json:"cacheCreationInputTokens"`
}

// claudeLine is one line of stream-json.
type claudeLine struct {
	claudeResult
	Type string `json:"type"`
	// ParentToolUseID is set on a subagent's lines: the tool call that started it.
	ParentToolUseID string `json:"parent_tool_use_id"`
	Message         *struct {
		ID      string       `json:"id"`
		Usage   *claudeUsage `json:"usage"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

// parseClaudeOutput reads the CLI's output: one JSON object, or a stream of
// JSON lines ending in a result line. It is false for anything else, and
// streamed when the output was a stream that ended without a result.
func parseClaudeOutput(stdout []byte) (out claudeResult, ok, streamed bool) {
	var whole claudeLine
	if json.Unmarshal(stdout, &whole) == nil && (whole.Type == "" || whole.Type == "result") {
		return whole.claudeResult, true, false
	}
	found := false
	sessions := map[string]int64{}
	// A message's content blocks come on lines of their own, each with the
	// message's usage.
	turns := map[string]claudeUsage{}
	lastText := ""
	n := 0
	for raw := range bytes.SplitSeq(stdout, []byte("\n")) {
		n++
		var line claudeLine
		if json.Unmarshal(raw, &line) != nil {
			continue
		}
		streamed = streamed || line.Type != ""
		switch line.Type {
		case lineAssistant:
			if line.Message == nil {
				continue
			}
			if u := line.Message.Usage; u != nil {
				sessions[line.ParentToolUseID] = max(sessions[line.ParentToolUseID], u.prompt())
				turns[orDefault(line.Message.ID, strconv.Itoa(n))] = *u
			}
			for _, c := range line.Message.Content {
				if line.ParentToolUseID == "" && c.Type == "text" && strings.TrimSpace(c.Text) != "" {
					lastText = c.Text
				}
			}
		case "result":
			out, found = line.claudeResult, true
		}
	}
	for _, size := range sessions {
		out.largestSession = max(out.largestSession, size)
	}
	for _, u := range turns {
		out.turns.Add(model.TokenUsage{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite})
	}
	out.turns.LargestSession = out.largestSession
	out.mainSession, out.lastText = sessions[""], strings.TrimSpace(lastText)
	return out, found, streamed && !found
}

// tokens is what the call used, summed over its models, else the main
// session's; nil when the CLI didn't say.
func (r claudeResult) tokens() *model.TokenUsage {
	var t model.TokenUsage
	switch {
	case len(r.ModelUsage) > 0:
		for _, u := range r.ModelUsage {
			t.Add(model.TokenUsage{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite})
		}
	case r.Usage != nil:
		t = model.TokenUsage{Input: r.Usage.Input, Output: r.Usage.Output,
			CacheRead: r.Usage.CacheRead, CacheWrite: r.Usage.CacheWrite}
	default:
		return nil
	}
	t.LargestSession = r.largestSession
	return &t
}

// usageMeter adds up what an Agent's calls in one run cost and used. One call
// that doesn't report its cost leaves the whole run's cost unreported, and
// one that doesn't report its tokens the run's tokens.
type usageMeter struct {
	agent            Agent
	milliUSD         int64
	unreported       bool
	tokens           model.TokenUsage
	tokensUnreported bool
	// handoffs are the sizes at which the calls' sessions handed off.
	handoffs []int64
}

func (m *usageMeter) Run(ctx context.Context, dir, prompt string) (string, error) {
	reporter, ok := m.agent.(UsageReporter)
	if !ok {
		m.unreported, m.tokensUnreported = true, true
		return m.agent.Run(ctx, dir, prompt)
	}
	answer, usage, err := reporter.RunReportingUsage(ctx, dir, prompt)
	m.milliUSD += usage.MilliUSD
	m.unreported = m.unreported || !usage.Reported
	m.handoffs = append(m.handoffs, usage.Handoffs...)
	if usage.Tokens != nil {
		m.tokens.Add(*usage.Tokens)
	} else {
		m.tokensUnreported = true
	}
	return answer, err
}

// total is what the run cost in thousandths of a US dollar, or nil when a call
// didn't report its cost. A run that called no agent cost nothing.
func (m *usageMeter) total() *int64 {
	if m.unreported {
		return nil
	}
	return &m.milliUSD
}

// totalTokens is what the run's calls used, or nil when a call didn't report
// its tokens. A run that called no agent used none.
func (m *usageMeter) totalTokens() *model.TokenUsage {
	if m.tokensUnreported {
		return nil
	}
	return &m.tokens
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
	// Open makes the approval accept one criterion as left open, for a person.
	Open bool
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
		if a.Open {
			return `Looked at it.` + "\n" + `{"verdict": "approve", "summary": "Stand-in review: fine, one criterion open.", ` +
				`"open": [{"criterion": "A screenshot of the editor", "why": "no desktop in the sandbox"}]}`, nil
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
// agent, can't go on: usage and rate limits, overload, a temporary server error
// (any 5xx, or an api_error), and bad credentials.
var unavailableSigns = regexp.MustCompile(`(?i)usage limit|limit reached|hit your limit|rate_limit_error|` +
	`api error: 429|api error: 5\d\d\b|overloaded_error|\bapi_error\b|` +
	`authentication_error|invalid api key|/login|oauth token has expired`)

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
