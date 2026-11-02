package agentrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/c4factory/internal/model"
)

// transcriptLine is a Claude Code transcript's line for a turn of size tokens.
func transcriptLine(size int64, sidechain bool) string {
	return fmt.Sprintf(`{"type":"assistant","isSidechain":%t,"message":{"usage":{"input_tokens":1,`+
		`"cache_read_input_tokens":%d,"output_tokens":10}}}`, sidechain, size-1)
}

func TestSessionGuard(t *testing.T) {
	dir := t.TempDir()
	handoff := filepath.Join(dir, handoffFile)
	transcript := func(lines ...string) string {
		path := filepath.Join(t.TempDir(), "transcript.jsonl")
		// A user line with a big tool result, like a real transcript's.
		content := `{"type":"user","message":{"content":"` + strings.Repeat("x", 100000) + `"}}` + "\n" +
			strings.Join(lines, "\n") + "\n"
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	below := transcript(transcriptLine(150000, false), transcriptLine(90000, false))
	past := transcript(transcriptLine(90000, false), transcriptLine(100001, false))
	pastInASubagent := transcript(transcriptLine(90000, false), transcriptLine(150000, true))

	tests := []struct {
		name       string
		transcript string
		tool       string
		file       string
		deny       bool
	}{
		{"below the limit, newest turn first", below, "Bash", "", false},
		{"past the limit", past, "Bash", "", true},
		{"past the limit, editing the code", past, "Edit", filepath.Join(dir, "main.go"), true},
		{"past the limit, writing the handoff", past, "Write", handoff, false},
		{"past the limit, editing the handoff", past, "Edit", handoff, false},
		{"only a subagent past the limit", pastInASubagent, "Bash", "", false},
		{"no transcript: allowed", filepath.Join(dir, "missing.jsonl"), "Bash", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, _ := json.Marshal(map[string]any{"transcript_path": tt.transcript, "tool_name": tt.tool,
				"tool_input": map[string]string{"file_path": tt.file}})
			var stdout, stderr bytes.Buffer
			code := SessionGuardMain([]string{"--limit", "100000", "--handoff", handoff}, bytes.NewReader(input),
				&stdout, &stderr)
			var out struct {
				HookSpecificOutput struct {
					HookEventName, PermissionDecision, PermissionDecisionReason string
				}
			}
			if code != 0 {
				t.Errorf("exit code = %d, want 0", code)
			}
			if !tt.deny {
				if stdout.Len() != 0 {
					t.Errorf("stdout = %q, want the tool allowed", stdout.String())
				}
				return
			}
			if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
				t.Fatalf("stdout = %q: %v", stdout.String(), err)
			}
			want := "This session is past 100000 tokens. Stop here: write to " + handoff + " what is done"
			if o := out.HookSpecificOutput; o.HookEventName != "PreToolUse" || o.PermissionDecision != "deny" ||
				!strings.HasPrefix(o.PermissionDecisionReason, want) {
				t.Errorf("hook output = %+v, want a denial saying %q", o, want)
			}
		})
	}
}

func TestSessionGuardSettings(t *testing.T) {
	g, err := newSessionGuard("/usr/local/bin/c4 agent", 130000, "/etc/c4agent/podinfo/annotations")
	if err != nil {
		t.Fatal(err)
	}
	defer g.remove()
	data, err := os.ReadFile(g.settings())
	if err != nil {
		t.Fatal(err)
	}
	// Step 12 of docs/plans/self-hosting.md: every session's settings have
	// the pause-guard as their PostToolUse hook.
	want := `{"hooks":{"PostToolUse":[{"hooks":[{"command":"'/usr/local/bin/c4 agent' pause-guard ` +
		`--annotations '/etc/c4agent/podinfo/annotations' --stopped '` + g.pausedMark() + `'","type":"command"}],` +
		`"matcher":"*"}],"PreToolUse":[{"hooks":[{"command":"'/usr/local/bin/c4 agent' session-guard --limit 130000 ` +
		`--handoff '` + g.handoff() + `'","type":"command"}],"matcher":"*"}]}}`
	if string(data) != want {
		t.Errorf("settings = %s\nwant %s", data, want)
	}
	if g.stopAt() != 195000 {
		t.Errorf("stopAt() = %d, want 195000", g.stopAt())
	}

	// The session after the last handoff has no limit, and still the pause-guard.
	unlimited, err := newSessionGuard("c4agent", 0, "/etc/c4agent/podinfo/annotations")
	if err != nil {
		t.Fatal(err)
	}
	defer unlimited.remove()
	data, _ = os.ReadFile(unlimited.settings())
	if strings.Contains(string(data), "PreToolUse") || !strings.Contains(string(data), "PostToolUse") {
		t.Errorf("settings without a limit = %s, want the pause-guard alone", data)
	}
	if none, err := newSessionGuard("c4agent", 0, ""); none != nil || err != nil {
		t.Errorf("newSessionGuard() without a limit or a pause = %v, %v; want no guard", none, err)
	}
}

// standInClaudeMode, when set, makes the test binary a stand-in Claude Code
// session that streams turns growing by standInTurn tokens and runs the
// PreToolUse hook of its --settings after each. Told to stop, it writes its
// handoff note and ends ("honour"), ends without one ("quiet"), or goes on
// ("ignore"); "short" ends after
// two turns, and so does any session after three unguarded turns. It keeps
// each prompt in standInClaudeDir.
const (
	standInClaudeMode = "C4_TEST_CLAUDE"
	standInClaudeDir  = "C4_TEST_CLAUDE_DIR"
	standInTurn       = 30000
)

var handoffPath = regexp.MustCompile(`write to (\S+) what is done`)

func standInClaude(mode string) int {
	dir := os.Getenv(standInClaudeDir)
	prompts, _ := filepath.Glob(filepath.Join(dir, "prompt-*.txt"))
	n := len(prompts) + 1
	prompt, _ := readAll(os.Stdin)
	_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("prompt-%d.txt", n)), prompt, 0o600)

	hook, postHook := settingsHooks()
	switch mode {
	case "hang":
		return hangingClaude(n)
	case "pause", "pause-busy", "record":
		return pausingClaude(mode, n, postHook)
	}
	transcript := filepath.Join(dir, fmt.Sprintf("transcript-%d.jsonl", n))
	runHook := func(tool, file string) string {
		input, _ := json.Marshal(map[string]any{"transcript_path": transcript, "tool_name": tool,
			"tool_input": map[string]string{"file_path": file}})
		cmd := exec.Command("sh", "-c", hook)
		cmd.Stdin = bytes.NewReader(input)
		out, _ := cmd.Output()
		return string(out)
	}
	result := func(turns int) int {
		fmt.Printf(`{"type":"result","subtype":"success","is_error":false,"result":"done in session %d",`+
			`"total_cost_usd":0.01,"usage":{"input_tokens":%d,"output_tokens":%d}}`+"\n", n, turns, 10*turns)
		return 0
	}
	for turn := 1; turn <= 50; turn++ {
		size := int64(turn * standInTurn)
		fmt.Printf(`{"type":"assistant","message":{"id":"msg_%d_%d","usage":{"input_tokens":1,`+
			`"cache_read_input_tokens":%d,"output_tokens":10},"content":[{"type":"text","text":"turn %d"}]},`+
			`"parent_tool_use_id":null}`+"\n", n, turn, size-1, turn)
		f, _ := os.OpenFile(transcript, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		_, _ = fmt.Fprintln(f, transcriptLine(size, false))
		_ = f.Close()
		if hook == "" || mode == "short" {
			if turn == 3 || mode == "short" && turn == 2 {
				return result(turn)
			}
			continue
		}
		denial := runHook("Bash", "")
		if !strings.Contains(denial, `"deny"`) {
			continue
		}
		if mode == "ignore" {
			if size > 150000 {
				// Past the limit by half again: c4agent ends it here.
				time.Sleep(time.Minute)
			}
			continue
		}
		if mode == "quiet" {
			return result(turn)
		}
		m := handoffPath.FindStringSubmatch(denial)
		if m == nil || strings.Contains(runHook("Write", m[1]), `"deny"`) {
			return 1
		}
		_ = os.WriteFile(m[1], fmt.Appendf(nil, "note of session %d\n", n), 0o600)
		return result(turn)
	}
	return 1
}

// settingsHooks are the commands of the PreToolUse and PostToolUse hooks in
// the session's --settings; "" for one it doesn't have.
func settingsHooks() (pre, post string) {
	i := slices.Index(os.Args, "--settings")
	if i < 0 || i+1 >= len(os.Args) {
		return "", ""
	}
	type hooks []struct {
		Hooks []struct{ Command string }
	}
	var settings struct {
		Hooks struct {
			PreToolUse  hooks
			PostToolUse hooks
		}
	}
	data, _ := os.ReadFile(os.Args[i+1])
	_ = json.Unmarshal(data, &settings)
	command := func(h hooks) string {
		if len(h) == 0 || len(h[0].Hooks) == 0 {
			return ""
		}
		return h[0].Hooks[0].Command
	}
	return command(settings.Hooks.PreToolUse), command(settings.Hooks.PostToolUse)
}

func readAll(f *os.File) ([]byte, error) {
	var b bytes.Buffer
	_, err := b.ReadFrom(f)
	return b.Bytes(), err
}

// standInAgent is Claude Code run by the stand-in in mode, guarded at 100000
// tokens.
func standInAgent(t *testing.T, mode string) (ClaudeAgent, string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	return ClaudeAgent{Command: []string{self, "-p"}, Env: []string{standInClaudeMode + "=" + mode,
		standInClaudeDir + "=" + dir}, SessionTokenLimit: 100000, Guard: self}, dir
}

func prompts(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for n := 1; ; n++ {
		data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("prompt-%d.txt", n)))
		if err != nil {
			return out
		}
		out = append(out, string(data))
	}
}

func TestAgentSessionHandsOff(t *testing.T) {
	agent, log := standInAgent(t, "honour")
	work := t.TempDir()
	answer, usage, err := agent.RunReportingUsage(context.Background(), work, "Add crop")
	if err != nil {
		t.Fatal(err)
	}
	// Each guarded session is told to stop at its 120k turn; the fourth runs
	// unguarded to its end.
	if answer != "done in session 4" || !reflect.DeepEqual(usage.Handoffs, []int64{120000, 120000, 120000}) {
		t.Errorf("RunReportingUsage() = %q, handoffs %v; want session 4's answer after 3 handoffs at 120000",
			answer, usage.Handoffs)
	}
	want := &model.TokenUsage{Input: 15, Output: 150, LargestSession: 120000}
	if !usage.Reported || usage.MilliUSD != 40 || !reflect.DeepEqual(usage.Tokens, want) {
		t.Errorf("usage = %d (reported %t), %+v; want 40 and %+v: every session added up",
			usage.MilliUSD, usage.Reported, usage.Tokens, want)
	}
	got := prompts(t, log)
	if len(got) != 4 || got[0] != "Add crop" {
		t.Fatalf("prompts = %q, want 4 sessions, the first with the job's prompt", got)
	}
	for i, p := range got[1:] {
		note := fmt.Sprintf("Its note:\n\nnote of session %d\n", i+1)
		if !strings.HasPrefix(p, "Add crop\n\n## A previous session handed off") || !strings.HasSuffix(p, note) {
			t.Errorf("session %d's prompt = %q, want the job's prompt and the note %q", i+2, p, note)
		}
	}
	if leftover, _ := filepath.Glob(filepath.Join(work, "*")); len(leftover) != 0 {
		t.Errorf("the working tree has %v, want no settings or note in it", leftover)
	}
}

func TestAgentSessionUnderTheLimitRunsOnce(t *testing.T) {
	agent, log := standInAgent(t, "short")
	answer, usage, err := agent.RunReportingUsage(context.Background(), t.TempDir(), "Add crop")
	if err != nil || answer != "done in session 1" || usage.Handoffs != nil || len(prompts(t, log)) != 1 {
		t.Errorf("RunReportingUsage() = %q, %+v, %v; want one session, no handoff", answer, usage, err)
	}
}

func TestAgentSessionThatWontStopIsEnded(t *testing.T) {
	agent, log := standInAgent(t, "ignore")
	work := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", work).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(work, "crop.go"), []byte("package crop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	answer, usage, err := agent.RunReportingUsage(context.Background(), work, "Add crop")
	if err != nil {
		t.Fatal(err)
	}
	// Past 150k by half again: c4agent ends each guarded session at its 180k turn.
	if answer != "done in session 4" || !reflect.DeepEqual(usage.Handoffs, []int64{180000, 180000, 180000}) {
		t.Errorf("RunReportingUsage() = %q, handoffs %v; want session 4's answer after 3 handoffs at 180000",
			answer, usage.Handoffs)
	}
	// An ended session has no result line: its turns say what it used, and
	// nothing says what it cost.
	if usage.Reported || usage.Tokens == nil || usage.Tokens.LargestSession != 180000 || usage.Tokens.Output != 3*60+30 {
		t.Errorf("usage = %d (reported %t), %+v; want the cost unreported and the turns' tokens",
			usage.MilliUSD, usage.Reported, usage.Tokens)
	}
	got := prompts(t, log)
	if len(got) != 4 {
		t.Fatalf("prompts = %d, want 4 sessions", len(got))
	}
	for _, want := range []string{"The previous session ended at 180000 tokens, past its limit of 100000",
		"Its last words:\n\nturn 6", "?? crop.go"} {
		if !strings.Contains(got[1], want) {
			t.Errorf("session 2's prompt = %q, want it to say %q", got[1], want)
		}
	}
}

func TestAgentSessionStoppingWithoutANoteHandsOff(t *testing.T) {
	agent, log := standInAgent(t, "quiet")
	answer, usage, err := agent.RunReportingUsage(context.Background(), t.TempDir(), "Add crop")
	if err != nil || answer != "done in session 4" || len(usage.Handoffs) != 3 {
		t.Fatalf("RunReportingUsage() = %q, %v, %v; want session 4's answer after 3 handoffs", answer, usage.Handoffs, err)
	}
	want := "The previous session stopped at 120000 tokens, past its limit of 100000, without writing a note. " +
		"Its last words:\n\nturn 4"
	if got := prompts(t, log); !strings.Contains(got[1], want) {
		t.Errorf("session 2's prompt = %q, want it to say %q", got[1], want)
	}
}

func TestAgentSessionFailingDoesNotHandOff(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	agent := ClaudeAgent{Command: []string{"sh", "-c", `cat >/dev/null; echo '{"type":"result","is_error":true,` +
		`"subtype":"error_during_execution","result":"API Error: 529 overloaded_error"}'`},
		SessionTokenLimit: 100000, Guard: self}
	_, usage, err := agent.RunReportingUsage(context.Background(), t.TempDir(), "Add crop")
	if err == nil || !strings.Contains(err.Error(), "overloaded_error") || usage.Handoffs != nil {
		t.Errorf("RunReportingUsage() = %+v, %v; want the error, no handoff", usage, err)
	}
}

func TestRunReportsTheSessionHandoffs(t *testing.T) {
	url := bareRepo(t, map[string]string{"README.md": "hi\n"})
	ctx := context.Background()
	Run(ctx, testJob(t, url, RoleImplement, 1), FakeAgent{Role: RoleImplement, Round: 1}, t.TempDir())

	agent, _ := standInAgent(t, "honour")
	result := Run(ctx, testJob(t, url, RoleReview, 1), agent, t.TempDir())
	var decoded Result
	if err := json.Unmarshal(result.Encode(), &decoded); err != nil ||
		!reflect.DeepEqual(decoded.SessionHandoffs, []int64{120000, 120000, 120000}) {
		t.Errorf("decoded = %+v, %v; want the 3 handoffs kept", decoded, err)
	}
}
