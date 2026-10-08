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

	"github.com/JairJosafath/c4factory/internal/model"
)

// A Claude Code session whose context passes its Agent's sessionTokenLimit
// hands its work to a fresh session (docs/plans/done/session-tokens.md, decisions
// 6-9). Its PreToolUse hook, c4agent session-guard, denies every tool but
// writing the handoff note once the session's newest turn is past the limit;
// a session past it by half again is ended by c4agent, which writes the note
// itself. The fresh session gets the same prompt and the note.

// SessionGuardCommand is the c4agent subcommand the session's hook runs.
const SessionGuardCommand = "session-guard"

// maxSessionHandoffs is how often one agent call hands off; the session after
// the last runs unguarded to its end, so a run never fails for length alone.
const maxSessionHandoffs = 3

// handoffFile is the note a session past its limit writes, outside the
// repository so it never lands in a commit.
const handoffFile = "HANDOFF.md"

// maxHandoffNote bounds the last words c4agent carries into the fresh prompt
// from a session it ended.
const maxHandoffNote = 4000

// session is what one Claude Code session did.
type session struct {
	answer string
	usage  Usage
	err    error
	// size is the main session's biggest turn's prompt; zero without a stream.
	size int64
	// lastText is the main session's last words, for a note c4agent writes.
	lastText string
	// stopped means c4agent ended the session for passing its limit by half again.
	stopped bool
}

// sessionGuard is one guarded session's settings: a directory outside the
// repository with the Claude Code settings file and, once written, the note.
type sessionGuard struct {
	dir   string
	limit int64
}

// newSessionGuard writes the settings that make program's session-guard the
// session's PreToolUse hook.
func newSessionGuard(program string, limit int64) (*sessionGuard, error) {
	dir, err := os.MkdirTemp("", "c4agent-session-")
	if err != nil {
		return nil, err
	}
	g := &sessionGuard{dir: dir, limit: limit}
	hook := fmt.Sprintf("%s %s --limit %d --handoff %s", shellQuote(program), SessionGuardCommand, limit,
		shellQuote(g.handoff()))
	settings := map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{
		"matcher": "*", "hooks": []any{map[string]any{"type": "command", "command": hook}},
	}}}}
	data, err := json.Marshal(settings)
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

// stopAt is the size past which c4agent ends a session that didn't stop.
func (g *sessionGuard) stopAt() int64 { return g.limit + g.limit/2 }

// note is what the session hands the next one, or "" when it didn't hand
// off: the note it wrote or, when c4agent ended it or it ended past its limit
// without one, its last words and the working tree's state. A session that
// failed otherwise doesn't hand off: its error, such as a usage limit, says more.
func (g *sessionGuard) note(ctx context.Context, dir string, s session) string {
	if ctx.Err() != nil {
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
	cmd := exec.CommandContext(ctx, "git", "status", "--short")
	cmd.Dir = dir
	status, _ := cmd.CombinedOutput()
	return fmt.Sprintf("The previous session %s at %d tokens, past its limit of %d, without writing a note. "+
		"Its last words:\n\n%s\n\n`git status --short`:\n\n%s", ended, s.size, g.limit,
		tail(orDefault(s.lastText, "(none)"), maxHandoffNote), orDefault(strings.TrimSpace(string(status)), "(clean)"))
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
func (a ClaudeAgent) runSessions(ctx context.Context, dir, prompt string) (string, Usage, error) {
	total := Usage{Reported: true}
	var tokens model.TokenUsage
	tokensReported := true
	next := prompt
	for {
		var guard *sessionGuard
		if len(total.Handoffs) < maxSessionHandoffs {
			var err error
			if guard, err = newSessionGuard(a.Guard, a.SessionTokenLimit); err != nil {
				// What the sessions so far used stays counted.
				total.Reported, total.Tokens = false, nil
				if tokensReported {
					total.Tokens = &tokens
				}
				return "", total, fmt.Errorf("session guard: %w", err)
			}
		}
		s := a.runSession(ctx, dir, next, guard)
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
// a turn of its main session passes stopAt.
type streamWatch struct {
	buf     bytes.Buffer
	scanned int
	stopAt  int64
	stop    func()
}

func (w *streamWatch) Write(p []byte) (int, error) {
	n, _ := w.buf.Write(p)
	for w.stop != nil {
		rest := w.buf.Bytes()[w.scanned:]
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			break
		}
		w.scanned += i + 1
		if mainTurnSize(rest[:i]) > w.stopAt {
			w.stop()
			w.stop = nil
		}
	}
	return n, nil
}

// lineAssistant is the type of a turn's line, in a stream and a transcript.
const lineAssistant = "assistant"

// assistantMark skips lines that can't be a turn without decoding them: tool
// results are big.
var assistantMark = []byte(`"` + lineAssistant + `"`)

// mainTurnSize is the prompt of a main-session turn on a stream-json line,
// else zero.
func mainTurnSize(raw []byte) int64 {
	if !bytes.Contains(raw, assistantMark) {
		return 0
	}
	var line claudeLine
	if json.Unmarshal(raw, &line) != nil || line.Type != lineAssistant || line.ParentToolUseID != "" ||
		line.Message == nil || line.Message.Usage == nil {
		return 0
	}
	return line.Message.Usage.prompt()
}

// SessionGuardMain is c4agent session-guard: Claude Code's PreToolUse hook
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
		_, _ = fmt.Fprintf(stderr, "c4agent %s: %v\n", SessionGuardCommand, err)
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
	f, err := os.Open(transcript)
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
