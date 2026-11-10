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
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/model"
)

// Step 12 of docs/plans/self-hosting.md: running Tasks pause at a tool
// boundary.

const pauseReasonText = `Crew "photo" paused by jair`

// annotationsFile writes the downward API file of a pod's annotations, as
// the kubelet does: key="value" lines, sorted, the value Go-quoted.
func annotationsFile(t *testing.T, path string, annotations map[string]string) {
	t.Helper()
	var b strings.Builder
	keys := make([]string, 0, len(annotations))
	for k := range annotations {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, strconv.Quote(annotations[k]))
	}
	if err := os.WriteFile(path+".new", []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".new", path); err != nil {
		t.Fatal(err)
	}
}

func TestPauseGuard(t *testing.T) {
	dir := t.TempDir()
	unpaused := filepath.Join(dir, "unpaused")
	annotationsFile(t, unpaused, map[string]string{"theos.factory.io/task": "add-crop"})
	paused := filepath.Join(dir, "paused")
	annotationsFile(t, paused, map[string]string{"theos.factory.io/task": "add-crop", PauseAnnotation: pauseReasonText})
	input := `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`

	tests := []struct {
		name, annotations, stdin string
		want                     string
	}{
		{name: "no annotations file is no pause", annotations: filepath.Join(dir, "missing"), stdin: input},
		{name: "annotations without the pause", annotations: unpaused, stdin: input},
		{name: "the pause", annotations: paused, stdin: input,
			want: `{"continue":false,"stopReason":"Paused by the factory: ` + `Crew \"photo\" paused by jair"}` + "\n"},
		{name: "the pause, whatever the input", annotations: paused, stdin: "not json",
			want: `{"continue":false,"stopReason":"Paused by the factory: ` + `Crew \"photo\" paused by jair"}` + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stopped := filepath.Join(t.TempDir(), pausedFile)
			var stdout, stderr bytes.Buffer
			code := PauseGuardMain([]string{"--annotations", tt.annotations, "--stopped", stopped},
				strings.NewReader(tt.stdin), &stdout, &stderr)
			if code != 0 || stdout.String() != tt.want {
				t.Errorf("pause-guard = %d, %q (stderr %q); want 0, %q", code, stdout.String(), stderr.String(), tt.want)
			}
			reason, err := os.ReadFile(stopped)
			if tt.want == "" && err == nil {
				t.Errorf("pause-guard marked the session paused (%q), want it left running", reason)
			}
			if tt.want != "" && string(reason) != pauseReasonText {
				t.Errorf("the session's pause mark = %q, %v; want the reason", reason, err)
			}
		})
	}
}

func TestParseAnnotations(t *testing.T) {
	got := parseAnnotations([]byte("a=\"1\"\n" + PauseAnnotation + "=\"line one\\nline \\\"two\\\"\"\nbroken\nb=unquoted\n"))
	if got["a"] != "1" || got[PauseAnnotation] != "line one\nline \"two\"" || len(got) != 2 {
		t.Errorf("parseAnnotations() = %q, want a and the Go-quoted pause", got)
	}
}

// pausingClaude is the stand-in Claude Code in the pause modes. In "pause"
// and "pause-busy", session n keeps its --session-id, leaves crop.go in the
// working tree and takes a turn every 20ms, each a tool call after which it
// runs its PostToolUse hook; it writes how many turns it took to turns-n.
// Told to stop, it writes the reason to stop-n.txt and ends with a result
// line, as Claude Code does. In "pause-busy" its third tool never ends. In
// "record" it writes its arguments to args-n.txt, and whether the session it
// resumes has its transcript where Claude Code reads it to transcript-n.txt,
// and answers. In "limit" it ends at its second turn with Claude Code's
// usage limit error.
func pausingClaude(mode string, n int, postHook string) int {
	dir := os.Getenv(standInClaudeDir)
	_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("args-%d.txt", n)), []byte(strings.Join(os.Args[1:], "\n")), 0o600)
	id := ""
	for _, flag := range []string{"--session-id", "--resume"} {
		if i := slices.Index(os.Args, flag); i > 0 && i+1 < len(os.Args) {
			id = os.Args[i+1]
		}
	}
	cwd, _ := os.Getwd()
	transcript := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", claudeProject(cwd), id+".jsonl")
	if mode == "record" {
		data, err := os.ReadFile(transcript) //nolint:gosec // the test's own
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("transcript-%d.txt", n)),
			fmt.Appendf(nil, "%t %d", err == nil, len(data)), 0o600)
		_ = os.WriteFile("STAND-IN.md", []byte("resumed\n"), 0o600)
		fmt.Printf(`{"type":"result","subtype":"success","is_error":false,"result":"Cropped.","total_cost_usd":0.01}` + "\n")
		return 0
	}
	_ = os.MkdirAll(filepath.Dir(transcript), 0o750)
	_ = os.WriteFile("crop.go", []byte("package crop\n\nfunc Crop() {}\n"), 0o600)
	for turn := 1; turn <= 1000; turn++ {
		fmt.Printf(`{"type":"assistant","message":{"id":"msg_%d_%d","usage":{"input_tokens":1,`+
			`"cache_read_input_tokens":999,"output_tokens":10},"content":[{"type":"text",`+
			`"text":"Cropping, turn %d."}]},"parent_tool_use_id":null}`+"\n", n, turn, turn)
		f, _ := os.OpenFile(transcript, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // the test's own
		_, _ = fmt.Fprintln(f, transcriptLine(1000, false))
		_ = f.Close()
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("turns-%d", n)), []byte(strconv.Itoa(turn)), 0o600)
		if mode == "limit" && turn == 2 {
			fmt.Printf(`{"type":"result","subtype":"success","is_error":true,`+
				`"result":"Claude AI usage limit reached|%d","total_cost_usd":0.03}`+"\n", standInLimitReset)
			return 1
		}
		if mode == "pause-busy" && turn == 3 {
			// A tool that doesn't end: the hook never runs.
			time.Sleep(time.Minute)
		}
		cmd := exec.Command("sh", "-c", postHook)
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"PostToolUse","tool_name":"Bash"}`)
		out, _ := cmd.Output()
		var hook struct {
			Continue   *bool  `json:"continue"`
			StopReason string `json:"stopReason"`
		}
		if json.Unmarshal(out, &hook) == nil && hook.Continue != nil && !*hook.Continue {
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("stop-%d.txt", n)), []byte(hook.StopReason), 0o600)
			fmt.Printf(`{"type":"result","subtype":"success","is_error":false,"result":"",`+
				`"total_cost_usd":0.02,"usage":{"input_tokens":%d,"output_tokens":%d}}`+"\n", turn, 10*turn)
			return 0
		}
		time.Sleep(20 * time.Millisecond)
	}
	return 1
}

// shortPause makes the pause's poll and grace fit a test.
func shortPause(t *testing.T, grace time.Duration) {
	t.Helper()
	oldPoll, oldGrace := pausePoll, pauseGrace
	pausePoll, pauseGrace = 20*time.Millisecond, grace
	t.Cleanup(func() { pausePoll, pauseGrace = oldPoll, oldGrace })
}

// waitFor waits for path to exist.
func waitFor(t *testing.T, path string) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("%s never appeared", path)
}

// pausedRun runs an implement job on a stand-in Claude Code in mode, and
// asks for the pause once its session has taken three turns: the pod's
// annotations name other things until then. It returns the repository, the
// checkpoint directory, the stand-in's log directory, the turns the session
// took before the pause was asked, and the result.
func pausedRun(t *testing.T, mode string, grace time.Duration) (url, checkpoints, log string, asked int, result Result) {
	t.Helper()
	shortPause(t, grace)
	url = bareRepo(t, map[string]string{"README.md": "hi\n"})
	checkpoints = t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	agent, log := standInAgent(t, mode)
	agent.SessionIDs = true
	job := testJob(t, url, RoleImplement, 1)
	job.CheckpointDir, job.Model, job.PodInfoDir = checkpoints, "claude-opus-5-5", t.TempDir()
	annotations := filepath.Join(job.PodInfoDir, podAnnotationsFile)
	others := map[string]string{"theos.factory.io/task": "add-crop"}
	annotationsFile(t, annotations, others)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
			data, _ := os.ReadFile(filepath.Join(log, "turns-1"))
			if n, _ := strconv.Atoi(string(data)); n >= 3 {
				asked = n
				others[PauseAnnotation] = pauseReasonText
				annotationsFile(t, annotations, others)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	result = Run(context.Background(), job, agent, t.TempDir())
	<-done
	return url, checkpoints, log, asked, result
}

func TestPauseEndsTheSessionAtAToolBoundaryWithACleanCheckpoint(t *testing.T) {
	url, checkpoints, log, asked, result := pausedRun(t, "pause", time.Minute)
	if !result.Paused || result.PauseReason != pauseReasonText || result.Error != "" || result.Interrupted ||
		result.Commit != "" {
		t.Fatalf("Run() = %+v, want a run paused for %q that pushed nothing", result, pauseReasonText)
	}
	// It stops only once the file says paused: the hook let the turns before run.
	turns, _ := os.ReadFile(filepath.Join(log, "turns-1"))
	if n, _ := strconv.Atoi(string(turns)); asked < 3 || n < asked {
		t.Errorf("the session took %s turns, the pause was asked after %d; want it to run until asked", turns, asked)
	}
	if stop, _ := os.ReadFile(filepath.Join(log, "stop-1.txt")); string(stop) != "Paused by the factory: "+pauseReasonText {
		t.Errorf("the hook's stopReason = %q, want it to name the reason", stop)
	}
	// What the paused session used counts.
	if result.CostMilliUSD == nil || *result.CostMilliUSD != 20 {
		t.Errorf("cost = %v, want the paused session's 20", result.CostMilliUSD)
	}
	if head := branchHead(t, url, "task/add-crop"); head != "" {
		t.Errorf("the branch is at %s, want it not pushed", head)
	}
	cp := readCheckpoint(t, checkpoints)
	args, _ := os.ReadFile(filepath.Join(log, "args-1.txt"))
	if cp.Kind != CheckpointClean || cp.Step != StepAgent || cp.PauseReason != pauseReasonText ||
		cp.SessionID == "" || !strings.Contains(string(args), "--session-id\n"+cp.SessionID) || cp.Work == "" {
		t.Errorf("state.json = %+v, want a clean checkpoint at the agent's session, with its ID and the reason", cp)
	}
	if !strings.Contains(cp.Note, "The factory paused the previous session ("+pauseReasonText+") after a tool call.") {
		t.Errorf("the note = %q, want it to say the factory paused the session", cp.Note)
	}
	if info, err := os.Stat(filepath.Join(checkpoints, checkpointTranscript)); err != nil || info.Size() == 0 {
		t.Errorf("the transcript: %v, want it saved", err)
	}
}

func TestPauseGraceEndsABusySessionWithAHardCheckpoint(t *testing.T) {
	_, checkpoints, _, _, result := pausedRun(t, "pause-busy", 300*time.Millisecond)
	if !result.Paused || result.PauseReason != pauseReasonText || result.Error != "" || result.Interrupted {
		t.Fatalf("Run() = %+v, want a run paused for %q", result, pauseReasonText)
	}
	cp := readCheckpoint(t, checkpoints)
	if cp.Kind != CheckpointHard || cp.Step != StepAgent || cp.PauseReason != pauseReasonText {
		t.Errorf("state.json = %+v, want a hard checkpoint with the reason: the grace ran out", cp)
	}
	if !strings.Contains(cp.Note, "and ended it when it was still busy") {
		t.Errorf("the note = %q, want it to say the session was ended past the grace", cp.Note)
	}
}

func TestPausedRunResumes(t *testing.T) {
	tests := []struct {
		name string
		// mode and grace make the pause clean or hard.
		mode  string
		grace time.Duration
		// model is the Agent's model when the run resumes.
		model string
		// resumed means the paused session goes on with --resume; else a
		// fresh session gets the note, which says want.
		resumed bool
		want    string
	}{
		{name: "clean, on the same model: --resume", mode: "pause", grace: time.Minute, model: "claude-opus-5-5",
			resumed: true},
		{name: "clean, on another model: the note", mode: "pause", grace: time.Minute, model: "claude-sonnet-5",
			want: "The factory paused the previous session (" + pauseReasonText + ") after a tool call."},
		{name: "hard: the note", mode: "pause-busy", grace: 300 * time.Millisecond, model: "claude-opus-5-5",
			want: "and ended it when it was still busy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, checkpoints, _, _, paused := pausedRun(t, tt.mode, tt.grace)
			if !paused.Paused {
				t.Fatalf("Run() = %+v, want it paused", paused)
			}
			cp := readCheckpoint(t, checkpoints)
			// The resumed pod is fresh: Claude Code's config directory is empty.
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			agent, log := standInAgent(t, "record")
			agent.SessionIDs = true
			job := testJob(t, url, RoleImplement, 1)
			job.CheckpointDir, job.Model, job.PodInfoDir = checkpoints, tt.model, t.TempDir()
			result := Run(context.Background(), job, agent, t.TempDir())
			if result.Error != "" || result.Commit == "" || result.Paused || result.ResumedFrom != cp.Kind {
				t.Fatalf("Run() = %+v, want a pushed commit, resumed from %s", result, cp.Kind)
			}
			args, _ := os.ReadFile(filepath.Join(log, "args-1.txt"))
			prompt := prompts(t, log)[0]
			if tt.resumed {
				exact := "The factory paused you (" + pauseReasonText + ") and resumed you. " +
					"The working tree is as you left it. Carry on."
				if !strings.Contains(string(args), "--resume\n"+cp.SessionID) ||
					strings.Contains(string(args), "--session-id") || prompt != exact {
					t.Errorf("the session ran with %q and prompt %q; want --resume %s and %q",
						args, prompt, cp.SessionID, exact)
				}
				if found, _ := os.ReadFile(filepath.Join(log, "transcript-1.txt")); !strings.HasPrefix(string(found), "true ") ||
					string(found) == "true 0" {
					t.Errorf("the resumed session's transcript where Claude Code reads it: %s, want it restored", found)
				}
				return
			}
			if strings.Contains(string(args), "--resume") || !strings.Contains(string(args), "--session-id") {
				t.Errorf("the session ran with %q, want a fresh session", args)
			}
			for _, want := range []string{"Add crop", "## A previous session handed off", tt.want, "?? crop.go"} {
				if !strings.Contains(prompt, want) {
					t.Errorf("the fresh session's prompt = %q, want it to say %q", prompt, want)
				}
			}
		})
	}
}

// standInLimitReset is when the stand-in's usage limit resets: 2100-01-01.
const standInLimitReset = 4102444800

// #1996: a run whose agent's service is unavailable saves a clean
// checkpoint before it reports Unavailable: the agent's own process ended,
// so its transcript is whole. The resumed run goes on in the same session
// on the same model, and in a fresh one told why on another.
func TestUnavailableAgentSavesACheckpointBeforeItReports(t *testing.T) {
	url := bareRepo(t, map[string]string{"README.md": "hi\n"})
	checkpoints := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	agent, log := standInAgent(t, "limit")
	agent.SessionIDs = true
	job := testJob(t, url, RoleImplement, 1)
	job.CheckpointDir, job.Model = checkpoints, "claude-opus-5-5"
	result := Run(context.Background(), job, agent, t.TempDir())
	if !result.Unavailable || result.RetryAt == nil || result.RetryAt.Unix() != standInLimitReset ||
		result.Checkpoint != CheckpointClean || result.Interrupted || result.Commit != "" {
		t.Fatalf("Run() = %+v, want an unavailable agent back at its reset, with a clean checkpoint saved", result)
	}
	if result.CostMilliUSD == nil || *result.CostMilliUSD != 30 {
		t.Errorf("cost = %v, want the session's 30 so far", result.CostMilliUSD)
	}
	if head := branchHead(t, url, "task/add-crop"); head != "" {
		t.Errorf("the branch is at %s, want it not pushed", head)
	}
	cp := readCheckpoint(t, checkpoints)
	args, _ := os.ReadFile(filepath.Join(log, "args-1.txt"))
	if cp.Kind != CheckpointClean || cp.Step != StepAgent || cp.SessionID == "" || cp.Work == "" ||
		!strings.Contains(string(args), "--session-id\n"+cp.SessionID) ||
		!strings.Contains(cp.Unavailable, "usage limit reached") || cp.PauseReason != "" {
		t.Errorf("state.json = %+v, want a clean checkpoint at the agent's session, with its ID and the error", cp)
	}
	if !strings.Contains(cp.Note, "The agent's service was unavailable (") {
		t.Errorf("the note = %q, want it to say the service was unavailable", cp.Note)
	}
	if info, err := os.Stat(filepath.Join(checkpoints, checkpointTranscript)); err != nil || info.Size() == 0 {
		t.Errorf("the transcript: %v, want it saved", err)
	}

	t.Run("the same model: --resume", func(t *testing.T) {
		args, prompt := resumeUnavailable(t, url, checkpoints, "claude-opus-5-5")
		if !strings.Contains(args, "--resume\n"+cp.SessionID) ||
			!strings.HasPrefix(prompt, "Your service was unavailable (") ||
			!strings.Contains(prompt, "the factory resumed you once it was back") {
			t.Errorf("the session ran with %q and prompt %q; want --resume %s, told why", args, prompt, cp.SessionID)
		}
	})
	t.Run("another model: the note", func(t *testing.T) {
		args, prompt := resumeUnavailable(t, url, checkpoints, "claude-sonnet-5")
		if strings.Contains(args, "--resume") || !strings.Contains(prompt, "The agent's service was unavailable (") {
			t.Errorf("the session ran with %q and prompt %q, want a fresh session told why", args, prompt)
		}
	})
}

// scriptedAgent answers each call with the next of its steps, and records
// the prompts.
type scriptedAgent struct {
	steps   []func(dir string) (string, error)
	prompts []string
}

func (a *scriptedAgent) Run(_ context.Context, dir, prompt string) (string, error) {
	a.prompts = append(a.prompts, prompt)
	step := a.steps[0]
	a.steps = a.steps[1:]
	return step(dir)
}

// #1996: an agent that is unavailable while it fixes the check leaves a
// checkpoint at the check; the resumed run's first fix session, a fresh
// one, is told why the earlier one stopped.
func TestUnavailableDuringTheCheckFixTellsTheResumedFix(t *testing.T) {
	url := bareRepo(t, map[string]string{"README.md": "hi\n"})
	checkpoints := t.TempDir()
	job := testJob(t, url, RoleImplement, 1)
	job.CheckpointDir, job.CheckCommand = checkpoints, []string{"sh", "-c", "test -f fixed"}
	write := func(name, reply string) func(string) (string, error) {
		return func(dir string) (string, error) {
			return reply, os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o600)
		}
	}
	limited := &scriptedAgent{steps: []func(string) (string, error){write("crop.go", "Added crop."),
		func(string) (string, error) {
			return "", errors.New("claude ended with an error: Claude AI usage limit reached|4102444800")
		}}}
	stopped := Run(context.Background(), job, limited, t.TempDir())
	if !stopped.Unavailable || stopped.Checkpoint != CheckpointClean {
		t.Fatalf("Run() = %+v, want an unavailable agent with a clean checkpoint", stopped)
	}
	if cp := readCheckpoint(t, checkpoints); cp.Step != StepCheck || cp.Summary != "Added crop." {
		t.Fatalf("state.json = %+v, want a checkpoint at the check", cp)
	}

	fixer := &scriptedAgent{steps: []func(string) (string, error){write("fixed", "Fixed.")}}
	resumed := Run(context.Background(), job, fixer, t.TempDir())
	if resumed.Error != "" || resumed.Commit == "" || resumed.ResumedFrom != CheckpointClean {
		t.Fatalf("Run() = %+v, want a pushed commit, resumed from the clean checkpoint", resumed)
	}
	// The fix is a fresh session: the round's prompt comes first (#25 theos).
	_, fix, _ := strings.Cut(strings.Join(fixer.prompts, ""), "\n\n---\n\n")
	if len(fixer.prompts) != 1 || !strings.HasPrefix(fixer.prompts[0], "You are the implementer") ||
		!strings.Contains(fix, "The agent's service was unavailable (") ||
		!strings.Contains(fix, `"sh -c test -f fixed" fails.`) {
		t.Errorf("the resumed run's prompts = %q, want one fix prompt told the service was unavailable", fixer.prompts)
	}
}

// resumeUnavailable resumes, on onModel, a copy of the checkpoint an
// unavailable agent's run left, and returns the resumed session's arguments
// and prompt.
func resumeUnavailable(t *testing.T, url, checkpoints, onModel string) (args, prompt string) {
	t.Helper()
	saved := t.TempDir()
	if err := os.CopyFS(saved, os.DirFS(checkpoints)); err != nil {
		t.Fatal(err)
	}
	// Each resume pushes to its own copy of the origin: two resumes of one
	// checkpoint a second apart make different commits on the same branch,
	// and the second push would not fast-forward (#1996).
	origin := filepath.Join(t.TempDir(), "origin.git")
	if err := os.CopyFS(origin, os.DirFS(strings.TrimPrefix(url, "file://"))); err != nil {
		t.Fatal(err)
	}
	url = "file://" + origin
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	agent, log := standInAgent(t, "record")
	agent.SessionIDs = true
	job := testJob(t, url, RoleImplement, 1)
	job.CheckpointDir, job.Model = saved, onModel
	resumed := Run(context.Background(), job, agent, t.TempDir())
	if resumed.Error != "" || resumed.Commit == "" || resumed.ResumedFrom != CheckpointClean {
		t.Fatalf("Run() = %+v, want a pushed commit, resumed from the clean checkpoint", resumed)
	}
	data, _ := os.ReadFile(filepath.Join(log, "args-1.txt"))
	return string(data), prompts(t, log)[0]
}

// A pause asked while the check runs kills the check; the run stops with a
// clean checkpoint at its check, which runs again on resume.
func TestPauseKillsARunningCheckThatRunsAgainOnResume(t *testing.T) {
	shortPause(t, time.Minute)
	url := bareRepo(t, map[string]string{"README.md": "hi\n"})
	checkpoints, marker := t.TempDir(), filepath.Join(t.TempDir(), "check-started")
	// The check's sleep is its child: killing the shell alone would leave it
	// holding the output open.
	job := testJob(t, url, RoleImplement, 1, "sh", "-c", "touch "+marker+"; sleep 30; true")
	job.CheckpointDir, job.PodInfoDir = checkpoints, t.TempDir()
	go func() {
		waitFor(t, marker)
		annotationsFile(t, filepath.Join(job.PodInfoDir, podAnnotationsFile), map[string]string{PauseAnnotation: "drain"})
	}()
	start := time.Now()
	result := Run(context.Background(), job, FakeAgent{Role: RoleImplement, Round: 1}, t.TempDir())
	if !result.Paused || result.PauseReason != "drain" || result.Commit != "" || time.Since(start) > 20*time.Second {
		t.Fatalf("Run() = %+v after %s, want it paused at once, nothing pushed", result, time.Since(start))
	}
	cp := readCheckpoint(t, checkpoints)
	if cp.Kind != CheckpointClean || cp.Step != StepCheck || cp.Summary != "Added round 1 to STAND-IN.md." {
		t.Errorf("state.json = %+v, want a clean checkpoint at the check, with the session's answer", cp)
	}

	var prompts []string
	var sawCrop bool
	job.CheckCommand, job.PodInfoDir = []string{"sh", "-c", "touch " + marker + "-again"}, t.TempDir()
	result = Run(context.Background(), job, stoppedWorkAgent{prompts: &prompts, sawCrop: &sawCrop}, t.TempDir())
	if result.Error != "" || result.Commit == "" || result.ResumedFrom != CheckpointClean || len(prompts) != 0 {
		t.Errorf("Run() = %+v with sessions %q, want the check run again and the round pushed, no session", result, prompts)
	}
	if _, err := os.Stat(marker + "-again"); err != nil {
		t.Errorf("the check didn't run again on resume: %v", err)
	}
}

// Between agent calls theos-agent stops at its next one: a pause asked before
// the run's session starts it not.
func TestPauseBeforeTheSessionStartsNone(t *testing.T) {
	url := bareRepo(t, map[string]string{"README.md": "hi\n"})
	job := testJob(t, url, RoleImplement, 1)
	job.CheckpointDir, job.PodInfoDir = t.TempDir(), t.TempDir()
	annotationsFile(t, filepath.Join(job.PodInfoDir, podAnnotationsFile), map[string]string{PauseAnnotation: "drain"})
	var prompts []string
	var sawCrop bool
	result := Run(context.Background(), job, stoppedWorkAgent{prompts: &prompts, sawCrop: &sawCrop}, t.TempDir())
	if !result.Paused || result.PauseReason != "drain" || len(prompts) != 0 {
		t.Errorf("Run() = %+v with sessions %q, want it paused before any session", result, prompts)
	}
	if cp := readCheckpoint(t, job.CheckpointDir); cp.Kind != CheckpointClean || cp.Note != "" || cp.SessionID != "" {
		t.Errorf("state.json = %+v, want a clean checkpoint with no session to tell of", cp)
	}
}

// A checkpoint's session ID names the transcript file Claude Code resumes:
// one that isn't a plain file name restores nothing.
func TestRestoreTranscriptTakesAFileName(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	checkpoints := t.TempDir()
	if err := os.WriteFile(filepath.Join(checkpoints, checkpointTranscript), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "../escape", "a/b", ".hidden"} {
		if err := restoreTranscript(checkpoints, id, t.TempDir()); err == nil {
			t.Errorf("restoreTranscript(%q) = nil, want an error", id)
		}
	}
	if err := restoreTranscript(checkpoints, "0b6f0f9e-5c1a-4d4e-9c2b-1f2e3d4c5b6a", t.TempDir()); err != nil {
		t.Errorf("restoreTranscript() of a session ID = %v, want it restored", err)
	}
}

// A run that went on from a checkpoint and is paused again before its first
// agent call keeps the stopped session's kind and model (#1995): a hard
// session, or one on another model, is not resumed with --resume later.
func TestPauseBeforeTheResumedSessionKeepsItsKindAndModel(t *testing.T) {
	tests := []struct {
		name  string
		mode  string
		grace time.Duration
		// model is the Agent's model on both resumes.
		model   string
		resumed bool
	}{
		{name: "hard stays hard", mode: "pause-busy", grace: 300 * time.Millisecond, model: "claude-opus-5-5"},
		{name: "clean on another model stays on the old one", mode: "pause", grace: time.Minute,
			model: "claude-sonnet-5"},
		{name: "clean on the same model resumes", mode: "pause", grace: time.Minute, model: "claude-opus-5-5",
			resumed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, checkpoints, _, _, paused := pausedRun(t, tt.mode, tt.grace)
			if !paused.Paused {
				t.Fatalf("Run() = %+v, want it paused", paused)
			}
			first := readCheckpoint(t, checkpoints)

			// Resumed, and paused again before its session starts. The
			// first run's short grace would end the restore itself on a
			// loaded machine (Gitea run 20): no session runs here, so the
			// grace only has to outlast the restore.
			shortPause(t, time.Minute)
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			agent, log := standInAgent(t, "record")
			agent.SessionIDs = true
			job := testJob(t, url, RoleImplement, 1)
			job.CheckpointDir, job.Model, job.PodInfoDir = checkpoints, tt.model, t.TempDir()
			annotationsFile(t, filepath.Join(job.PodInfoDir, podAnnotationsFile), map[string]string{PauseAnnotation: "drain"})
			result := Run(context.Background(), job, agent, t.TempDir())
			if !result.Paused || len(prompts(t, log)) != 0 {
				t.Fatalf("Run() = %+v, want it paused before any session", result)
			}
			second := readCheckpoint(t, checkpoints)
			if second.Kind != first.Kind || second.Model != first.Model || second.SessionID != first.SessionID ||
				second.Note != first.Note {
				t.Errorf("state.json = %+v, want the first checkpoint's session, kind %s and model %s",
					second, first.Kind, first.Model)
			}

			// Resumed again: --resume only for a clean session on its model.
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			agent, log = standInAgent(t, "record")
			agent.SessionIDs = true
			job.PodInfoDir = t.TempDir()
			result = Run(context.Background(), job, agent, t.TempDir())
			if result.Error != "" || result.Commit == "" || result.Paused {
				t.Fatalf("Run() = %+v, want a pushed commit", result)
			}
			args, _ := os.ReadFile(filepath.Join(log, "args-1.txt"))
			if got := strings.Contains(string(args), "--resume\n"+first.SessionID); got != tt.resumed {
				t.Errorf("the session ran with %q; --resume = %t, want %t", args, got, tt.resumed)
			}
		})
	}
}

// A pause asked while the base's check runs for a blocked session kills that
// check too (#1995): the run stops with a clean checkpoint, not blocked, and
// the check runs again on resume.
func TestPauseKillsTheBaseCheckOfABlockedSession(t *testing.T) {
	shortPause(t, time.Minute)
	url := bareRepo(t, map[string]string{"README.md": "hi\n"})
	checkpoints, marker := t.TempDir(), filepath.Join(t.TempDir(), "base-check-started")
	job := testJob(t, url, RoleImplement, 1, "sh", "-c", "touch "+marker+"; sleep 15; true")
	job.CheckpointDir, job.PodInfoDir = checkpoints, t.TempDir()
	go func() {
		waitFor(t, marker)
		annotationsFile(t, filepath.Join(job.PodInfoDir, podAnnotationsFile), map[string]string{PauseAnnotation: "drain"})
	}()
	blocked := "Nothing changed.\n" + model.BlockedPrefix + " the engine's release must be published first."
	start := time.Now()
	result := Run(context.Background(), job, &scriptAgent{replies: []string{blocked}}, t.TempDir())
	if !result.Paused || result.Blocked != "" || result.Commit != "" || time.Since(start) > 10*time.Second {
		t.Fatalf("Run() = %+v after %s, want it paused at once, not blocked", result, time.Since(start))
	}
	if cp := readCheckpoint(t, checkpoints); cp.Kind != CheckpointClean || cp.Step != StepAgent {
		t.Errorf("state.json = %+v, want a clean checkpoint at the agent's session", cp)
	}

	job.CheckCommand, job.PodInfoDir = []string{"sh", "-c", "touch " + marker + "-again"}, t.TempDir()
	result = Run(context.Background(), job, &scriptAgent{replies: []string{blocked}}, t.TempDir())
	if result.Paused || result.ResumedFrom != CheckpointClean {
		t.Errorf("Run() = %+v, want it resumed from the clean checkpoint", result)
	}
	if _, err := os.Stat(marker + "-again"); err != nil {
		t.Errorf("the base's check didn't run again on resume: %v", err)
	}
}
