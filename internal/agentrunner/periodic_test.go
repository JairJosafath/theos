package agentrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/model"
)

// Step 14 of docs/plans/self-hosting.md (#1997): between tool calls,
// pause-guard has theos-agent save a hard checkpoint when the last is
// CheckpointEvery old, and the session goes on.

// periodicTurns is how many tool calls the stand-in in mode "periodic"
// makes.
const periodicTurns = 4

// periodicClaude is the stand-in Claude Code in mode "periodic": session n
// keeps its --session-id, leaves crop.go in the working tree and makes
// periodicTurns tool calls, each followed by its PostToolUse hook. Before
// hook k it writes ready-k and waits for go-k, so the test sets theos-agent's
// clock for that hook; after the last it writes ready-(periodicTurns+1)
// and waits for its go, then ends with a result line.
func periodicClaude(n int, postHook string) int {
	dir := os.Getenv(standInClaudeDir)
	_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("args-%d.txt", n)), []byte(strings.Join(os.Args[1:], "\n")), 0o600)
	id := ""
	if i := slices.Index(os.Args, "--session-id"); i > 0 && i+1 < len(os.Args) {
		id = os.Args[i+1]
	}
	cwd, _ := os.Getwd()
	transcript := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", claudeProject(cwd), id+".jsonl")
	_ = os.MkdirAll(filepath.Dir(transcript), 0o750)
	_ = os.WriteFile("crop.go", []byte("package crop\n\nfunc Crop() {}\n"), 0o600)
	wait := func(k int) bool {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("ready-%d", k)), nil, 0o600)
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
			if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("go-%d", k))); err == nil {
				return true
			}
			time.Sleep(5 * time.Millisecond)
		}
		return false
	}
	for turn := 1; turn <= periodicTurns; turn++ {
		fmt.Printf(`{"type":"assistant","message":{"id":"msg_%d_%d","usage":{"input_tokens":1,`+
			`"cache_read_input_tokens":999,"output_tokens":10},"content":[{"type":"text",`+
			`"text":"Cropping, turn %d."}]},"parent_tool_use_id":null}`+"\n", n, turn, turn)
		f, _ := os.OpenFile(transcript, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // the test's own
		_, _ = fmt.Fprintln(f, transcriptLine(1000, false))
		_ = f.Close()
		if !wait(turn) {
			return 1
		}
		cmd := exec.Command("sh", "-c", postHook)
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"PostToolUse","tool_name":"Bash"}`)
		out, _ := cmd.Output()
		if len(bytes.TrimSpace(out)) > 0 {
			// The hook only allows here: anything else ends the session.
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("hook-%d.txt", turn)), out, 0o600)
			return 1
		}
	}
	if !wait(periodicTurns + 1) {
		return 1
	}
	fmt.Printf(`{"type":"result","subtype":"success","is_error":false,"result":"Cropped.",` +
		`"total_cost_usd":0.04,"usage":{"input_tokens":4,"output_tokens":40}}` + "\n")
	return 0
}

// checkpointClock makes theos-agent's clock one the test sets: base, which the
// run starts at, plus what set says.
func checkpointClock(t *testing.T) (set func(time.Duration)) {
	t.Helper()
	base := time.Now()
	var offset atomic.Int64
	old := checkpointNow
	checkpointNow = func() time.Time { return base.Add(time.Duration(offset.Load())) }
	t.Cleanup(func() { checkpointNow = old })
	return func(d time.Duration) { offset.Store(int64(d)) }
}

// periodicRun runs an implement job on the stand-in in mode "periodic",
// with theos-agent's clock at ats[k-1] for hook k. It returns the state.json
// each hook left ("" for none), the result, the stand-in's log, and copies
// of the checkpoint directory and the origin as they were after the last
// hook: what a pod killed then, with no report, leaves.
func periodicRun(t *testing.T, ats []time.Duration) (states []string, result Result, log, checkpoints,
	origin string) {
	t.Helper()
	set := checkpointClock(t)
	url := bareRepo(t, map[string]string{"README.md": "hi\n"})
	live := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	agent, log := standInAgent(t, "periodic")
	agent.SessionIDs = true
	job := testJob(t, url, RoleImplement, 1)
	job.CheckpointDir, job.Model = live, "claude-opus-5-5"
	checkpoints, origin = filepath.Join(t.TempDir(), "checkpoint"), filepath.Join(t.TempDir(), "origin.git")

	states = make([]string, len(ats))
	var wg sync.WaitGroup
	wg.Go(func() {
		for k := 1; k <= len(ats)+1; k++ {
			waitFor(t, filepath.Join(log, fmt.Sprintf("ready-%d", k)))
			if k > 1 {
				data, _ := os.ReadFile(filepath.Join(live, checkpointStateFile))
				states[k-2] = string(data)
			}
			if k <= len(ats) {
				set(ats[k-1])
			} else {
				// The pod dies here, before the session ends: its volume and
				// the origin stay as they are now.
				if err := os.CopyFS(checkpoints, os.DirFS(live)); err != nil {
					t.Error(err)
				}
				if err := os.CopyFS(origin, os.DirFS(strings.TrimPrefix(url, "file://"))); err != nil {
					t.Error(err)
				}
			}
			if err := os.WriteFile(filepath.Join(log, fmt.Sprintf("go-%d", k)), nil, 0o600); err != nil {
				t.Error(err)
			}
		}
	})
	result = Run(context.Background(), job, agent, t.TempDir())
	wg.Wait()
	return states, result, log, checkpoints, "file://" + origin
}

// periodicAts are theos-agent's clock at each hook: twice before
// CheckpointEvery, then at it, then again at once.
var periodicAts = []time.Duration{CheckpointEvery / 2, CheckpointEvery - time.Second, CheckpointEvery,
	CheckpointEvery}

func TestPauseGuardSavesAHardCheckpointEveryTenMinutes(t *testing.T) {
	states, result, log, checkpoints, origin := periodicRun(t, periodicAts)
	// The session went on to its end: the run is not paused, and pushed.
	if result.Error != "" || result.Commit == "" || result.Paused || result.Summary != "Cropped." ||
		result.Checkpoint != "" {
		t.Fatalf("Run() = %+v, want the session's answer and a pushed commit, nothing paused", result)
	}
	if hooks, _ := filepath.Glob(filepath.Join(log, "hook-*.txt")); len(hooks) != 0 {
		t.Errorf("the hook answered %v, want it to allow every tool", hooks)
	}
	if states[0] != "" || states[1] != "" {
		t.Errorf("state.json after the hooks before %s: %q, %q; want none", CheckpointEvery, states[0], states[1])
	}
	if states[2] == "" {
		t.Fatalf("no state.json after the hook at %s, want a checkpoint", CheckpointEvery)
	}
	// The ask right after it finds a fresh checkpoint: the bundle's name
	// would change with a new one.
	if states[3] != states[2] {
		t.Errorf("state.json after the next ask = %s\nwant it as it was: %s", states[3], states[2])
	}
	var cp checkpoint
	if err := json.Unmarshal([]byte(states[2]), &cp); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(filepath.Join(log, "args-1.txt"))
	if cp.Kind != CheckpointHard || cp.Step != StepAgent || cp.SessionID == "" || cp.PauseReason != "" ||
		!strings.Contains(string(args), "--session-id\n"+cp.SessionID) || cp.Work == "" || cp.Head == "" {
		t.Errorf("state.json = %+v, want a hard checkpoint at the agent's session, with its ID and the working tree",
			cp)
	}
	// The session in progress has no cost yet; no call before it finished.
	if cp.CostMilliUSD == nil || *cp.CostMilliUSD != 0 {
		t.Errorf("state.json's cost = %v, want the finished calls' 0", cp.CostMilliUSD)
	}
	for _, want := range []string{"The previous session was stopped with its sandbox, before it could write a note.",
		"Its last words:\n\nCropping, turn ", "?? crop.go"} {
		if !strings.Contains(cp.Note, want) {
			t.Errorf("the note = %q, want it to say %q", cp.Note, want)
		}
	}
	// The bundle holds the uncommitted crop.go, in its commit of the
	// working tree.
	bundle := filepath.Join(checkpoints, cp.Bundle)
	clone := t.TempDir()
	for _, args := range [][]string{{"clone", "--quiet", origin, clone},
		{"-C", clone, "fetch", "--quiet", bundle, checkpointRef + ":" + checkpointRef},
		{"-C", clone, "cat-file", "-e", cp.Work + ":crop.go"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Errorf("git %v: %v: %s; want the bundle to hold crop.go at %s", args, err, out, cp.Work)
		}
	}
}

// #1997: a pod killed with no report, after a checkpoint between tool
// calls, leaves the next pod that checkpoint: the working tree comes back,
// and a fresh session gets the job's prompt and the stopped one's note.
func TestRunGoesOnFromACheckpointSavedBetweenToolCalls(t *testing.T) {
	states, _, _, checkpoints, origin := periodicRun(t, periodicAts)
	var cp checkpoint
	if err := json.Unmarshal([]byte(states[3]), &cp); err != nil {
		t.Fatalf("state.json after the last hook = %q: %v", states[3], err)
	}

	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	agent, log := standInAgent(t, "record")
	agent.SessionIDs = true
	job := testJob(t, origin, RoleImplement, 1)
	job.CheckpointDir, job.Model = checkpoints, "claude-opus-5-5"
	result := Run(context.Background(), job, agent, t.TempDir())
	if result.Error != "" || result.Commit == "" || result.ResumedFrom != CheckpointHard {
		t.Fatalf("Run() = %+v, want a pushed commit, resumed from the hard checkpoint", result)
	}
	args, _ := os.ReadFile(filepath.Join(log, "args-1.txt"))
	if strings.Contains(string(args), "--resume") || !strings.Contains(string(args), "--session-id") {
		t.Errorf("the session ran with %q, want a fresh session", args)
	}
	prompt := prompts(t, log)[0]
	for _, want := range []string{"Add crop", "## A previous session handed off",
		"The previous session was stopped with its sandbox, before it could write a note.", "?? crop.go"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the fresh session's prompt = %q, want it to say %q", prompt, want)
		}
	}
	// The stopped session's crop.go was back in the working tree: the round
	// pushed it.
	dir := t.TempDir()
	if out, err := exec.Command("git", "clone", "--quiet", "--branch", "task/add-crop", origin, dir).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "crop.go")); err != nil {
		t.Errorf("crop.go is not on the pushed branch: %v; want the checkpoint's working tree restored", err)
	}
}

// #1997: the save itself. Before CheckpointEvery it saves nothing; at it, of
// two asks at once (parallel tool calls) one saves and the other finds the
// fresh checkpoint. A run with no clone yet saves nothing, and one with no
// checkpoint directory has no saver.
func TestPeriodicCheckpointsSaveOnceDue(t *testing.T) {
	if p := newPeriodicCheckpoints(Job{}, &runProgress{}, &sessionLog{}, &usageMeter{}); p != nil {
		t.Errorf("newPeriodicCheckpoints() without a checkpoint directory = %v, want nil", p)
	}
	set := checkpointClock(t)
	ctx := context.Background()
	url := bareRepo(t, map[string]string{"README.md": "hi\n"})
	job := testJob(t, url, RoleImplement, 1)
	job.CheckpointDir = t.TempDir()

	early := newPeriodicCheckpoints(job, &runProgress{}, &sessionLog{}, &usageMeter{})
	set(CheckpointEvery)
	if got := early.save(ctx); got != answerNothing {
		t.Errorf("save() before the clone = %q, want %q", got, answerNothing)
	}

	set(0)
	r, err := clone(ctx, job, filepath.Join(t.TempDir(), "repo"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, "crop.go"), []byte("package crop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions := &sessionLog{}
	sessions.started("0b6f0f9e-5c1a-4d4e-9c2b-1f2e3d4c5b6a")
	sessions.said("Cropping is half done.")
	meter := &usageMeter{milliUSD: 120}
	p := newPeriodicCheckpoints(job, &runProgress{repo: r, step: StepAgent}, sessions, meter)

	set(CheckpointEvery - time.Nanosecond)
	if got := p.save(ctx); got != answerNotDue {
		t.Errorf("save() just before %s = %q, want %q", CheckpointEvery, got, answerNotDue)
	}
	if _, err := os.Stat(filepath.Join(job.CheckpointDir, checkpointStateFile)); err == nil {
		t.Fatalf("state.json saved before %s", CheckpointEvery)
	}

	set(CheckpointEvery)
	answers := make([]string, 2)
	var wg sync.WaitGroup
	for i := range answers {
		wg.Go(func() { answers[i] = p.save(ctx) })
	}
	wg.Wait()
	slices.Sort(answers)
	if !slices.Equal(answers, []string{answerNotDue, answerSaved}) {
		t.Errorf("two asks at once = %q, want one saved, the other not due", answers)
	}
	cp := readCheckpoint(t, job.CheckpointDir)
	if cp.Kind != CheckpointHard || cp.SessionID != "0b6f0f9e-5c1a-4d4e-9c2b-1f2e3d4c5b6a" || cp.Work == "" ||
		cp.CostMilliUSD == nil || *cp.CostMilliUSD != 120 || !strings.Contains(cp.Note, "Cropping is half done.") {
		t.Errorf("state.json = %+v, want a hard checkpoint of the session, its finished calls' cost and its words",
			cp)
	}
}

// #1997: pause-guard allows a tool when it can't ask theos-agent for a
// checkpoint, or gets no answer within its bound, and doesn't ask at all
// when the pause ends the session.
func TestPauseGuardAllowsWhenTheCheckpointAskFails(t *testing.T) {
	oldWait := askWait
	askWait = 300 * time.Millisecond
	t.Cleanup(func() { askWait = oldWait })
	dir := t.TempDir()
	// A socket that never answers: the kernel takes the connection, nobody
	// accepts it.
	silent := filepath.Join(dir, "silent.sock")
	ln, err := net.Listen("unix", silent)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	paused := filepath.Join(dir, "paused")
	annotationsFile(t, paused, map[string]string{PauseAnnotation: pauseReasonText})

	tests := []struct {
		name string
		args []string
		// asked means the guard waited for an answer.
		asked bool
		want  string
	}{
		{name: "no socket", args: []string{"--checkpoint", filepath.Join(dir, "missing.sock")}},
		{name: "a socket that doesn't answer", args: []string{"--checkpoint", silent}, asked: true},
		{name: "the pause doesn't ask", args: []string{"--annotations", paused, "--stopped",
			filepath.Join(dir, pausedFile), "--checkpoint", silent},
			want: `{"continue":false,"stopReason":"Paused by the factory: Crew \"photo\" paused by jair"}` + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			start := time.Now()
			code := PauseGuardMain(tt.args, strings.NewReader(`{"hook_event_name":"PostToolUse"}`), &stdout, &stderr)
			took := time.Since(start)
			if code != 0 || stdout.String() != tt.want {
				t.Errorf("pause-guard = %d, %q (stderr %q); want 0, %q", code, stdout.String(), stderr.String(), tt.want)
			}
			if took > askWait+2*time.Second || !tt.asked && took >= askWait {
				t.Errorf("pause-guard took %s, want it within its bound of %s (waited: %t)", took, askWait, tt.asked)
			}
		})
	}
}

// #1997: a session with the checkpoint socket alone still gets the
// pause-guard as its PostToolUse hook, which asks on the socket.
func TestSessionGuardWithTheCheckpointSocketAlone(t *testing.T) {
	g, err := newSessionGuard("theos-agent", 0, "", true)
	if err != nil || g == nil {
		t.Fatalf("newSessionGuard() with the socket alone = %v, %v; want a guard", g, err)
	}
	defer g.remove()
	data, err := os.ReadFile(g.settings())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"hooks":{"PostToolUse":[{"hooks":[{"command":"'theos-agent' pause-guard --checkpoint '` +
		filepath.Join(g.dir, checkpointSocket) + `'","type":"command"}],"matcher":"*"}]}}`
	if string(data) != want {
		t.Errorf("settings = %s\nwant %s", data, want)
	}

	all, err := newSessionGuard("theos-agent", 130000, "/etc/theos-agent/podinfo/annotations", true)
	if err != nil {
		t.Fatal(err)
	}
	defer all.remove()
	data, _ = os.ReadFile(all.settings())
	if !strings.Contains(string(data), "PreToolUse") || !strings.Contains(string(data),
		`--stopped '`+all.pausedMark()+`' --checkpoint '`+all.checkpointSocket()+`'`) {
		t.Errorf("settings with all three = %s, want the session-guard and one pause-guard that pauses and asks", data)
	}
}

// #1997, review of step 14: a save between tool calls counts the sessions of
// the agent call in progress that already handed off, not only the calls
// that returned, so a lost pod's next one doesn't under-count the run's cost.
func TestPeriodicCheckpointCountsTheCallsHandedOffSessions(t *testing.T) {
	set := checkpointClock(t)
	ctx := context.Background()
	url := bareRepo(t, map[string]string{"README.md": "hi\n"})
	job := testJob(t, url, RoleImplement, 1)
	job.CheckpointDir = t.TempDir()
	r, err := clone(ctx, job, filepath.Join(t.TempDir(), "repo"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, "crop.go"), []byte("package crop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions := &sessionLog{}
	sessions.started("0b6f0f9e-5c1a-4d4e-9c2b-1f2e3d4c5b6a")
	meter := &usageMeter{milliUSD: 120, handoffs: []int64{90_000}, tokens: model.TokenUsage{Input: 10}}
	p := newPeriodicCheckpoints(job, &runProgress{repo: r, step: StepAgent}, sessions, meter)

	sockDir, err := os.MkdirTemp("", "c4ck-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	socket := filepath.Join(sockDir, checkpointSocket)
	stop := p.serve(ctx, socket, Usage{MilliUSD: 3000, Reported: true, Handoffs: []int64{130_500},
		Tokens: &model.TokenUsage{Input: 5}})
	set(CheckpointEvery)
	if err := askCheckpoint(socket); err != nil {
		t.Fatal(err)
	}
	stop()
	cp := readCheckpoint(t, job.CheckpointDir)
	if cp.CostMilliUSD == nil || *cp.CostMilliUSD != 3120 || !slices.Equal(cp.Handoffs, []int64{90_000, 130_500}) ||
		cp.Tokens == nil || cp.Tokens.Input != 15 {
		t.Errorf("state.json cost %v, handoffs %v, tokens %+v; want 3120, [90000 130500] and 15 input",
			cp.CostMilliUSD, cp.Handoffs, cp.Tokens)
	}
}

// #1997, review of step 14: a save between tool calls runs git beside the
// session, so it takes no optional lock: git status doesn't refresh the
// clone's index, which the agent's own git add or commit would find locked.
func TestPeriodicCheckpointLeavesTheIndexAlone(t *testing.T) {
	ctx := context.Background()
	url := bareRepo(t, map[string]string{"README.md": "hi\n"})
	job := testJob(t, url, RoleImplement, 1)
	r, err := clone(ctx, job, filepath.Join(t.TempDir(), "repo"))
	if err != nil {
		t.Fatal(err)
	}
	// Same content, new stat data: a plain git status would rewrite the index.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(r.dir, "README.md"), later, later); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, "crop.go"), []byte("package crop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(r.dir, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if work, err := r.snapshot(ctx); err != nil || work == "" {
		t.Fatalf("snapshot() = %q, %v; want a commit", work, err)
	}
	if note := endedNote(ctx, r.dir, "Stopped.", "half"); !strings.Contains(note, "crop.go") {
		t.Errorf("endedNote() = %q, want the tree's state", note)
	}
	after, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the save rewrote the clone's index; want it untouched")
	}
}
