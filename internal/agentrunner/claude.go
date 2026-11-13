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
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/JairJosafath/theos/internal/model"
)

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
	// theos-agent itself.
	Guard string
	// SessionIDs starts each session with a --session-id of its own, which
	// theos-agent records for a checkpoint; Claude Code's own command takes it.
	SessionIDs bool
	// sessions, when set, records each session's ID and last words.
	sessions *sessionLog
	// pause, when set, guards each session with Guard's pause-guard, and
	// learns of the sessions it ended.
	pause *pauseWatch
	// checkpoints, when set, saves the run between the sessions' tool calls
	// when Guard's pause-guard asks (periodic.go).
	checkpoints *periodicCheckpoints
}

// keepingSessions has the agent record its sessions in log, guard them with
// pause, and save periodic checkpoints between their tool calls.
func (a ClaudeAgent) keepingSessions(log *sessionLog, pause *pauseWatch, periodic *periodicCheckpoints) Agent {
	a.sessions, a.pause, a.checkpoints = log, pause, periodic
	return a
}

// resumesSessions reports whether a checkpoint can name a session to go on
// with: Claude Code's own command, whose sessions have IDs.
func (a ClaudeAgent) resumesSessions() bool { return a.SessionIDs }

// DefaultClaudeCommand runs Claude Code once, headless, streaming every turn
// as JSON lines, so each turn's tokens can be read.
var DefaultClaudeCommand = []string{
	"claude", "-p", "--dangerously-skip-permissions", "--output-format", "stream-json", "--verbose",
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
	if a.Guard != "" && (a.SessionTokenLimit > 0 || a.pause.annotations() != "" || a.checkpoints != nil) {
		return a.runSessions(ctx, dir, prompt)
	}
	resume, _ := resumeFrom(ctx)
	s := a.runSession(ctx, dir, prompt, nil, resume.id)
	return s.answer, s.usage, s.err
}

// stopWait is how long a session theos-agent ended may hold its output open,
// through a process it started.
const stopWait = 10 * time.Second

// runSession runs one Claude Code session, or with resume, goes on with that
// one; with a guard, it is guarded and ended when it passes the guard's stopAt.
func (a ClaudeAgent) runSession(ctx context.Context, dir, prompt string, guard *sessionGuard, resume string) session {
	command := a.Command
	if len(command) == 0 {
		command = DefaultClaudeCommand
	}
	if a.Model != "" {
		command = append(slices.Clone(command), "--model", a.Model)
	}
	switch {
	case resume != "":
		// A resumed session keeps its ID.
		command = append(slices.Clone(command), "--resume", resume)
		a.sessions.started(resume)
	case a.SessionIDs:
		id := newSessionID()
		command = append(slices.Clone(command), "--session-id", id)
		a.sessions.started(id)
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var s session
	stdout := &streamWatch{}
	if guard != nil && guard.checkpoints {
		// A checkpoint saved between tool calls tells of the session's
		// newest words.
		stdout.said = a.sessions.said
	}
	if guard != nil {
		command = append(slices.Clone(command), "--settings", guard.settings())
	}
	if guard != nil && guard.limit > 0 {
		stdout.stopAt = guard.stopAt()
		stdout.stop = func() { s.stopped = true; cancel() }
	}
	cmd := exec.CommandContext(sessionCtx, command[0], command[1:]...) //nolint:gosec // the Agent's command; running it is the runner's job
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
	a.sessions.ended(s.lastText)
	if reason, paused := guard.paused(); paused {
		// pause-guard ended it after a tool: what it used counts, its answer doesn't.
		a.pause.request(reason)
		s.usage.Tokens, s.err = &out.turns, errPaused
		if ok {
			s.usage.Tokens = out.tokens()
			if out.CostUSD != nil && *out.CostUSD >= 0 {
				s.usage.MilliUSD, s.usage.Reported = int64(math.Round(*out.CostUSD*1000)), true
			}
		}
		return s
	}
	if !ok {
		switch {
		case s.stopped:
			// No result line: its turns say what it used; what it cost is unknown.
			s.usage.Tokens = &out.turns
			s.err = fmt.Errorf("theos-agent ended the session at %d tokens", s.size)
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

// unavailableSigns are what Claude Code and the API say when the service, not the
// agent, can't go on: usage and rate limits, overload, a temporary server error
// (any 5xx, or an api_error), and bad credentials.
var unavailableSigns = regexp.MustCompile(`(?i)usage limit|limit reached|hit your limit|rate_limit_error|` +
	`api error: 429|api error: 5\d\d\b|overloaded_error|\bapi_error\b|` +
	`authentication_error|invalid api key|/login|oauth token has expired`)

// resetEpoch is the reset time Claude Code puts after a usage limit: "...|1759417200".
var resetEpoch = regexp.MustCompile(`limit reached\|(\d{10})`)

// claudeUnavailable reports whether output says Claude's service is
// unavailable, and when it is back, if it said; a reset in the past means now.
func claudeUnavailable(output string, now time.Time) (bool, time.Time) {
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
