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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/JairJosafath/theos/internal/model"
)

// A Claude Code session whose context passes its Agent's sessionTokenLimit
// hands its work to a fresh session (docs/plans/done/session-tokens.md, decisions
// 6-9). Its PreToolUse hook, theos-agent session-guard, denies every tool but
// writing the handoff note once the session's newest turn is past the limit;
// a session past it by half again is ended by theos-agent, which writes the note
// itself. The fresh session gets the same prompt and the note.

// SessionGuardCommand is the theos-agent subcommand the session's hook runs.
const SessionGuardCommand = "session-guard"

// maxSessionHandoffs is how often one agent call hands off; the session after
// the last runs unguarded to its end, so a run never fails for length alone.
const maxSessionHandoffs = 3

// handoffFile is the note a session past its limit writes, outside the
// repository so it never lands in a commit.
const handoffFile = "HANDOFF.md"

// maxHandoffNote bounds the last words theos-agent carries into the fresh prompt
// from a session it ended.
const maxHandoffNote = 4000

// session is what one Claude Code session did.
type session struct {
	answer string
	usage  Usage
	err    error
	// size is the main session's biggest turn's prompt; zero without a stream.
	size int64
	// lastText is the main session's last words, for a note theos-agent writes.
	lastText string
	// stopped means theos-agent ended the session for passing its limit by half again.
	stopped bool
}

// sessionGuard is one guarded session's settings: a directory outside the
// repository with the Claude Code settings file and, once written, the note,
// the pause's mark and theos-agent's checkpoint socket.
type sessionGuard struct {
	dir string
	// limit is the session's token limit; zero: none.
	limit int64
	// checkpoints means pause-guard asks theos-agent for a checkpoint on the
	// socket after each tool (periodic.go).
	checkpoints bool
}

// newSessionGuard writes the settings that make program's session-guard the
// session's PreToolUse hook, with a limit, and its pause-guard its
// PostToolUse hook, with the downward API file of the pod's annotations, or
// with checkpoints, the socket it asks theos-agent for one on, or both. It is nil
// with none of the three.
func newSessionGuard(program string, limit int64, annotations string, checkpoints bool) (*sessionGuard, error) {
	if limit <= 0 && annotations == "" && !checkpoints {
		return nil, nil
	}
	dir, err := os.MkdirTemp("", "theos-agent-session-")
	if err != nil {
		return nil, err
	}
	g := &sessionGuard{dir: dir, limit: max(limit, 0), checkpoints: checkpoints}
	hooks := map[string]any{}
	matchAll := func(command string) []any {
		return []any{map[string]any{"matcher": "*", "hooks": []any{map[string]any{"type": "command", "command": command}}}}
	}
	if g.limit > 0 {
		hooks["PreToolUse"] = matchAll(fmt.Sprintf("%s %s --limit %d --handoff %s", shellQuote(program),
			SessionGuardCommand, limit, shellQuote(g.handoff())))
	}
	if annotations != "" || checkpoints {
		command := shellQuote(program) + " " + PauseGuardCommand
		if annotations != "" {
			command += fmt.Sprintf(" --annotations %s --stopped %s", shellQuote(annotations),
				shellQuote(g.pausedMark()))
		}
		if checkpoints {
			command += " --checkpoint " + shellQuote(g.checkpointSocket())
		}
		hooks["PostToolUse"] = matchAll(command)
	}
	data, err := json.Marshal(map[string]any{"hooks": hooks})
	if err == nil {
		err = os.WriteFile(g.settings(), data, 0o600)
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return g, nil
}

func (g *sessionGuard) settings() string { return filepath.Join(g.dir, "settings.json") }
func (g *sessionGuard) handoff() string  { return filepath.Join(g.dir, handoffFile) }
func (g *sessionGuard) remove()          { _ = os.RemoveAll(g.dir) }

func (g *sessionGuard) pausedMark() string { return filepath.Join(g.dir, pausedFile) }

// checkpointSocket is where theos-agent answers pause-guard's asks for a
// checkpoint; "" without them.
func (g *sessionGuard) checkpointSocket() string {
	if g == nil || !g.checkpoints {
		return ""
	}
	return filepath.Join(g.dir, checkpointSocket)
}

// paused reports whether pause-guard ended the session, and the reason it
// gave.
func (g *sessionGuard) paused() (string, bool) {
	if g == nil {
		return "", false
	}
	data, err := os.ReadFile(g.pausedMark())
	return string(data), err == nil
}

// stopAt is the size past which theos-agent ends a session that didn't stop.
func (g *sessionGuard) stopAt() int64 { return g.limit + g.limit/2 }

// note is what the session hands the next one, or "" when it didn't hand
// off: the note it wrote or, when theos-agent ended it or it ended past its limit
// without one, its last words and the working tree's state. A session that
// failed otherwise doesn't hand off: its error, such as a usage limit, says more.
func (g *sessionGuard) note(ctx context.Context, dir string, s session) string {
	if ctx.Err() != nil || g.limit == 0 {
		return ""
	}
	ended := "ended"
	if !s.stopped {
		if s.err != nil {
			return ""
		}
		if data, err := os.ReadFile(g.handoff()); err == nil && strings.TrimSpace(string(data)) != "" {
			return strings.TrimSpace(string(data))
		}
		if s.size <= g.limit {
			return ""
		}
		ended = "stopped"
	}
	return endedNote(ctx, dir, fmt.Sprintf("The previous session %s at %d tokens, past its limit of %d, "+
		"without writing a note.", ended, s.size, g.limit), s.lastText)
}

// endedNote is the note for a session that ended without writing one: why
// it ended, its last words and the working tree's state. theos-agent writes it
// for a session it ended past its limit, and for one its sandbox's stop
// ended (checkpoint.go).
func endedNote(ctx context.Context, dir, headline, lastText string) string {
	// No optional locks: a periodic save runs this beside the session, whose
	// own git commands mustn't find the index locked.
	cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "status", "--short")
	cmd.Dir = dir
	status, _ := cmd.CombinedOutput()
	return fmt.Sprintf("%s Its last words:\n\n%s\n\n`git status --short`:\n\n%s", headline,
		tail(orDefault(lastText, "(none)"), maxHandoffNote), orDefault(strings.TrimSpace(string(status)), "(clean)"))
}

// handoffPrompt is the job's prompt for the session after a handoff.
func handoffPrompt(prompt, note string) string {
	return prompt + "\n\n## A previous session handed off\n\n" +
		"A previous session worked on this and handed off. Its changes are in the working tree " +
		"(git status, git diff). Its note:\n\n" + note + "\n"
}

// runSessions runs the agent in sessions of at most SessionTokenLimit tokens:
// a session that hands off is followed by a fresh one with the note. The last
// session's answer is the call's; every session's cost and tokens add up.
// Each session is guarded by the pause too. A call that resumes a session
// resumes it in the first; a fresh one after it gets the job's prompt.
func (a ClaudeAgent) runSessions(ctx context.Context, dir, prompt string) (string, Usage, error) {
	total := Usage{Reported: true}
	var tokens model.TokenUsage
	tokensReported := true
	next := prompt
	// The resumed session is the first; a fresh one after it gets the job's prompt.
	resume, resuming := resumeFrom(ctx)
	if resuming {
		prompt = resume.prompt
	}
	for {
		limit := a.SessionTokenLimit
		if len(total.Handoffs) >= maxSessionHandoffs {
			limit = 0
		}
		guard, err := newSessionGuard(a.Guard, limit, a.pause.annotations(), a.checkpoints != nil)
		if err != nil {
			// What the sessions so far used stays counted.
			total.Reported, total.Tokens = false, nil
			if tokensReported {
				total.Tokens = &tokens
			}
			return "", total, fmt.Errorf("session guard: %w", err)
		}
		sofar := Usage{MilliUSD: total.MilliUSD, Reported: total.Reported, Handoffs: slices.Clone(total.Handoffs)}
		if tokensReported {
			sofar.Tokens = new(tokens)
		}
		stopCheckpoints := a.checkpoints.serve(ctx, guard.checkpointSocket(), sofar)
		s := a.runSession(ctx, dir, next, guard, resume.id)
		stopCheckpoints()
		resume.id = ""
		total.MilliUSD += s.usage.MilliUSD
		total.Reported = total.Reported && s.usage.Reported
		if s.usage.Tokens != nil {
			tokens.Add(*s.usage.Tokens)
		} else {
			tokensReported = false
		}
		note := ""
		if guard != nil {
			note = guard.note(ctx, dir, s)
			guard.remove()
		}
		if note == "" {
			if tokensReported {
				total.Tokens = &tokens
			}
			return s.answer, total, s.err
		}
		total.Handoffs = append(total.Handoffs, s.size)
		next = handoffPrompt(prompt, note)
	}
}

// streamWatch keeps a session's output and ends the session, with stop, when
// a turn of its main session passes stopAt. said, when set, hears the main
// session's newest words as they come.
type streamWatch struct {
	buf     bytes.Buffer
	scanned int
	stopAt  int64
	stop    func()
	said    func(string)
}

func (w *streamWatch) Write(p []byte) (int, error) {
	n, _ := w.buf.Write(p)
	for w.stop != nil || w.said != nil {
		rest := w.buf.Bytes()[w.scanned:]
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			break
		}
		w.scanned += i + 1
		size, text := mainTurn(rest[:i])
		if w.stop != nil && size > w.stopAt {
			w.stop()
			w.stop = nil
		}
		if w.said != nil && text != "" {
			w.said(text)
		}
	}
	return n, nil
}

// lineAssistant is the type of a turn's line, in a stream and a transcript.
const lineAssistant = "assistant"

// assistantMark skips lines that can't be a turn without decoding them: tool
// results are big.
var assistantMark = []byte(`"` + lineAssistant + `"`)

// mainTurn is the prompt of a main-session turn on a stream-json line, and
// its last words; zero and "" for any other line.
func mainTurn(raw []byte) (size int64, text string) {
	if !bytes.Contains(raw, assistantMark) {
		return 0, ""
	}
	var line claudeLine
	if json.Unmarshal(raw, &line) != nil || line.Type != lineAssistant || line.ParentToolUseID != "" ||
		line.Message == nil {
		return 0, ""
	}
	for _, c := range line.Message.Content {
		if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
			text = strings.TrimSpace(c.Text)
		}
	}
	if line.Message.Usage != nil {
		size = line.Message.Usage.prompt()
	}
	return size, text
}

// SessionGuardMain is theos-agent session-guard: Claude Code's PreToolUse hook
// for a session with a token limit. It reads the hook's input on stdin and
// denies the tool on stdout when the session is past the limit. It allows
// what it can't judge: a broken guard never blocks an agent.
func SessionGuardMain(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(SessionGuardCommand, flag.ContinueOnError)
	flags.SetOutput(stderr)
	limit := flags.Int64("limit", 0, "the session's token limit")
	handoff := flags.String("handoff", "", "the note the session may still write")
	err := flags.Parse(args)
	if err == nil {
		err = guardSession(stdin, stdout, *limit, *handoff)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "theos-agent %s: %v\n", SessionGuardCommand, err)
	}
	return 0
}

// hookInput is the part of a PreToolUse hook's input the guard reads.
type hookInput struct {
	TranscriptPath string `json:"transcript_path"`
	ToolName       string `json:"tool_name"`
	ToolInput      struct {
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
}

// fileTools are the tools that may still write the handoff note.
var fileTools = []string{"Write", "Edit", "MultiEdit"}

func guardSession(stdin io.Reader, stdout io.Writer, limit int64, handoff string) error {
	if limit <= 0 || handoff == "" {
		return errors.New("needs --limit and --handoff")
	}
	var in hookInput
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return fmt.Errorf("reading the hook's input: %w", err)
	}
	size, err := newestTurn(in.TranscriptPath)
	if err != nil {
		return err
	}
	if size <= limit || slices.Contains(fileTools, in.ToolName) &&
		filepath.Clean(in.ToolInput.FilePath) == filepath.Clean(handoff) {
		return nil
	}
	reason := fmt.Sprintf("This session is past %d tokens. Stop here: write to %s what is done, what is left "+
		"and what you learned that the code doesn't show, then end your turn.", limit, handoff)
	return json.NewEncoder(stdout).Encode(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": reason,
	}})
}

// newestTurn is the prompt of the main session's newest turn in a Claude Code
// transcript; a subagent's turns are sidechains.
func newestTurn(transcript string) (int64, error) {
	f, err := os.Open(transcript) //nolint:gosec // the transcript Claude Code names in its hook input
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	var size int64
	r := bufio.NewReader(f)
	for {
		raw, err := r.ReadBytes('\n')
		if bytes.Contains(raw, assistantMark) {
			var line struct {
				Type        string `json:"type"`
				IsSidechain bool   `json:"isSidechain"`
				Message     *struct {
					Usage *claudeUsage `json:"usage"`
				} `json:"message"`
			}
			// A turn Claude Code makes up, after an API error, reports no tokens.
			if json.Unmarshal(raw, &line) == nil && line.Type == lineAssistant && !line.IsSidechain &&
				line.Message != nil && line.Message.Usage != nil && line.Message.Usage.prompt() > 0 {
				size = line.Message.Usage.prompt()
			}
		}
		if errors.Is(err, io.EOF) {
			return size, nil
		}
		if err != nil {
			return 0, err
		}
	}
}

// shellQuote quotes s for sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
