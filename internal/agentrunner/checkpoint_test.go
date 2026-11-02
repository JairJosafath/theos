package agentrunner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// hangingClaude is the stand-in Claude Code in mode "hang": session n keeps
// its --session-id, writes its transcript where Claude Code would, streams one
// turn, leaves crop.go uncommitted in the working tree, says it started, and
// works until it is killed.
func hangingClaude(n int) int {
	dir := os.Getenv(standInClaudeDir)
	id := ""
	if i := slices.Index(os.Args, "--session-id"); i > 0 && i+1 < len(os.Args) {
		id = os.Args[i+1]
	}
	_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("session-%d.txt", n)), []byte(id), 0o600)
	project := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", "-workspace-repo")
	_ = os.MkdirAll(project, 0o750)
	_ = os.WriteFile(filepath.Join(project, id+".jsonl"), []byte(transcriptLine(30000, false)+"\n"), 0o600)
	fmt.Printf(`{"type":"assistant","message":{"id":"msg_%d_1","usage":{"input_tokens":1,`+
		`"cache_read_input_tokens":29999,"output_tokens":10},"content":[{"type":"text",`+
		`"text":"Cropping is half done: the handler is in, its test is next."}]},"parent_tool_use_id":null}`+"\n", n)
	_ = os.WriteFile("crop.go", []byte("package crop\n\nfunc Crop() {}\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("started-%d", n)), nil, 0o600)
	time.Sleep(time.Minute)
	return 0
}

// interruptedRun runs an implement job on a stand-in Claude Code that hangs
// mid-session, and sends c4agent's process SIGTERM, as the kubelet does when
// it stops the sandbox's pod. It returns the repository, the checkpoint
// directory, the stand-in's log directory and the result.
func interruptedRun(t *testing.T) (url, checkpoints, log string, result Result) {
	t.Helper()
	url = bareRepo(t, map[string]string{"README.md": "hi\n"})
	checkpoints = t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	agent, log := standInAgent(t, "hang")
	agent.SessionIDs = true
	job := testJob(t, url, RoleImplement, 1)
	job.CheckpointDir, job.Model = checkpoints, "claude-opus-5-5"

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	go func() {
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
			if _, err := os.Stat(filepath.Join(log, "started-1")); err == nil {
				_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	result = Run(ctx, job, agent, t.TempDir())
	return url, checkpoints, log, result
}

func readCheckpoint(t *testing.T, dir string) checkpoint {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, checkpointStateFile))
	if err != nil {
		t.Fatal(err)
	}
	var cp checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		t.Fatal(err)
	}
	return cp
}

func writeCheckpoint(t *testing.T, dir string, cp checkpoint) {
	t.Helper()
	data, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, checkpointStateFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Step 11 of docs/plans/self-hosting.md: SIGTERM mid-session ends the session
// as before, then leaves a hard checkpoint of the run, and pushes nothing.
func TestSIGTERMMidSessionLeavesAHardCheckpoint(t *testing.T) {
	url, checkpoints, log, result := interruptedRun(t)
	if !result.Interrupted || result.Commit != "" || result.ResumedFrom != "" {
		t.Errorf("Run() = %+v, want an interrupted run that pushed nothing", result)
	}
	if head := branchHead(t, url, "task/add-crop"); head != "" {
		t.Errorf("the branch is at %s, want it not pushed", head)
	}
	cp := readCheckpoint(t, checkpoints)
	id, _ := os.ReadFile(filepath.Join(log, "session-1.txt"))
	if cp.Version != checkpointVersion || cp.Kind != CheckpointHard || cp.Step != StepAgent ||
		cp.Role != RoleImplement || cp.Task != "add-crop" || cp.Round != 1 || cp.Model != "claude-opus-5-5" {
		t.Errorf("state.json = %+v, want a hard checkpoint of round 1's implement run at its agent session", cp)
	}
	if len(id) == 0 || cp.SessionID != string(id) {
		t.Errorf("state.json's session is %q, want the one the session was started with, %q", cp.SessionID, id)
	}
	if cp.Head == "" || cp.Work == "" || cp.Start != cp.Head {
		t.Errorf("state.json = %+v, want the branch's head and a commit of the working tree on it", cp)
	}
	for _, want := range []string{"The previous session was stopped with its sandbox",
		"Its last words:\n\nCropping is half done", "?? crop.go"} {
		if !strings.Contains(cp.Note, want) {
			t.Errorf("the note = %q, want it to say %q", cp.Note, want)
		}
	}
	for _, f := range []string{cp.Bundle, checkpointTranscript} {
		if info, err := os.Stat(filepath.Join(checkpoints, f)); err != nil || info.Size() == 0 {
			t.Errorf("checkpoint file %s: %v, want it saved", f, err)
		}
	}
	out, err := exec.Command("git", "bundle", "list-heads", filepath.Join(checkpoints, cp.Bundle)).CombinedOutput()
	if err != nil || !strings.Contains(string(out), cp.Work+" "+checkpointRef) {
		t.Errorf("the bundle's heads = %s, %v; want %s at the working tree's commit %s", out, err, checkpointRef, cp.Work)
	}
	// The review of step 11: the bundle carried the whole history. It holds
	// only what the remote lacks, on the remote's main as its prerequisite.
	if prereqs := bundlePrerequisites(t, filepath.Join(checkpoints, cp.Bundle)); !slices.Contains(prereqs,
		branchHead(t, url, "main")) {
		t.Errorf("the bundle's prerequisites = %v, want the remote's main, %s, left out", prereqs, branchHead(t, url, "main"))
	}
}

// bundlePrerequisites lists the commits a git bundle needs its repository to
// have: the header's lines that start with a dash.
func bundlePrerequisites(t *testing.T, bundle string) []string {
	t.Helper()
	f, err := os.Open(bundle)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var prereqs []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() && scanner.Text() != "" {
		if line, ok := strings.CutPrefix(scanner.Text(), "-"); ok {
			prereqs = append(prereqs, strings.Fields(line)[0])
		}
	}
	return prereqs
}

// stoppedWorkAgent records the prompts it gets and whether the stopped
// session's crop.go was in the working tree, uncommitted, when it started,
// then adds a line to STAND-IN.md.
type stoppedWorkAgent struct {
	prompts *[]string
	sawCrop *bool
}

func (a stoppedWorkAgent) Run(ctx context.Context, dir, prompt string) (string, error) {
	*a.prompts = append(*a.prompts, prompt)
	status, _ := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain").Output()
	*a.sawCrop = strings.Contains(string(status), "?? crop.go")
	if err := os.WriteFile(filepath.Join(dir, "STAND-IN.md"), []byte("resumed\n"), 0o600); err != nil {
		return "", err
	}
	return "Cropped.", nil
}

func TestRunGoesOnFromAHardCheckpoint(t *testing.T) {
	tests := []struct {
		name string
		// edit changes the saved checkpoint before the next pod starts.
		edit func(*checkpoint)
		// resumed means the run goes on from the checkpoint.
		resumed bool
		// prompted means the agent gets a session; noted, the stopped one's note.
		prompted, noted bool
	}{
		{name: "at its agent session, in a fresh session with the note", resumed: true, prompted: true, noted: true},
		{name: "at its check, with the session's answer", edit: func(cp *checkpoint) {
			cp.Step, cp.Summary = StepCheck, "Cropped before the stop."
		}, resumed: true},
		{name: "an unknown version starts over", edit: func(cp *checkpoint) { cp.Version = 99 }, prompted: true},
		{name: "another run's starts over", edit: func(cp *checkpoint) { cp.Round = 2 }, prompted: true},
		// The review of step 11: a restore that failed after it reset the clone
		// to the working tree's commit started over on it, and pushed it.
		{name: "a restore that fails half way starts over from the clone", edit: func(cp *checkpoint) {
			cp.Head = strings.Repeat("0", 40)
		}, prompted: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, checkpoints, _, _ := interruptedRun(t)
			if tt.edit != nil {
				cp := readCheckpoint(t, checkpoints)
				tt.edit(&cp)
				writeCheckpoint(t, checkpoints, cp)
			}
			var prompts []string
			var sawCrop bool
			job := testJob(t, url, RoleImplement, 1)
			job.CheckpointDir = checkpoints
			result := Run(context.Background(), job, stoppedWorkAgent{prompts: &prompts, sawCrop: &sawCrop}, t.TempDir())
			if result.Error != "" || result.Commit == "" {
				t.Fatalf("Run() = %+v, want a pushed commit", result)
			}
			wantFrom := ""
			if tt.resumed {
				wantFrom = CheckpointHard
			}
			if result.ResumedFrom != wantFrom {
				t.Errorf("resumedFrom = %q, want %q", result.ResumedFrom, wantFrom)
			}
			if got := len(prompts) > 0; got != tt.prompted {
				t.Fatalf("prompts = %q, want a session: %t", prompts, tt.prompted)
			}
			if tt.prompted {
				noted := strings.Contains(prompts[0], "## A previous session handed off") &&
					strings.Contains(prompts[0], "Cropping is half done") && strings.Contains(prompts[0], "?? crop.go")
				if noted != tt.noted || sawCrop != tt.resumed {
					t.Errorf("the session's prompt has the note: %t, saw crop.go: %t; want %t and %t\n%s",
						noted, sawCrop, tt.noted, tt.resumed, prompts[0])
				}
			}
			if tt.edit != nil && tt.resumed && result.Summary != "Cropped before the stop." {
				t.Errorf("summary = %q, want the stopped session's answer", result.Summary)
			}
			dir := t.TempDir()
			if out, err := exec.Command("git", "clone", "--quiet", "--branch", "task/add-crop", url, dir).CombinedOutput(); err != nil {
				t.Fatalf("clone: %v: %s", err, out)
			}
			_, err := os.Stat(filepath.Join(dir, "crop.go"))
			if (err == nil) != tt.resumed {
				t.Errorf("crop.go pushed: %t, want %t: the stopped session's work goes on only from the checkpoint",
					err == nil, tt.resumed)
			}
			log, _ := exec.Command("git", "-C", dir, "log", "--format=%s").Output()
			if strings.Contains(string(log), "c4agent checkpoint") {
				t.Errorf("the branch's history has the checkpoint's commit:\n%s", log)
			}
		})
	}
}

func TestCheckpointAddsThePodsUsageToTheRuns(t *testing.T) {
	cost := func(n int64) *int64 { return &n }
	from := &checkpoint{Handoffs: []int64{120000}, CostMilliUSD: cost(300)}
	var cp checkpoint
	cp.addUsage(from, Result{CostMilliUSD: cost(200), SessionHandoffs: []int64{130000}})
	if cp.CostMilliUSD == nil || *cp.CostMilliUSD != 500 || !slices.Equal(cp.Handoffs, []int64{120000, 130000}) {
		t.Errorf("addUsage() = %v, %v; want 500 and both pods' handoffs", cp.CostMilliUSD, cp.Handoffs)
	}
	cp.addUsage(from, Result{})
	if cp.CostMilliUSD != nil {
		t.Errorf("addUsage() of a pod without a cost = %d, want the run's cost unreported", *cp.CostMilliUSD)
	}
}

// The review of step 11: a bundle holds only what the remote lacks. When the
// remote has the checkpoint's commit already (the agent committed nothing new
// and left the tree clean), git refuses an empty bundle; it then holds that
// commit alone, and a fresh clone still restores from it.
func TestCheckpointBundleOfACommitTheRemoteHas(t *testing.T) {
	url := bareRepo(t, map[string]string{"README.md": "hi\n"})
	dir := filepath.Join(t.TempDir(), "repo")
	if out, err := exec.Command("git", "clone", "--quiet", url, dir).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	ctx := context.Background()
	r := &repo{dir: dir}
	head, err := r.head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.git(ctx, dir, "update-ref", checkpointRef, head); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "work.bundle")
	refs := []string{checkpointRef}
	if _, err := r.git(ctx, dir, append(append([]string{"bundle", "create", "--quiet", bundle}, refs...),
		bundleBase(ctx, r, refs, head)...)...); err != nil {
		t.Fatalf("bundle create: %v", err)
	}
	fresh := filepath.Join(t.TempDir(), "fresh")
	if out, err := exec.Command("git", "clone", "--quiet", url, fresh).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", fresh, "fetch", "--quiet", bundle,
		checkpointRef+":"+checkpointRef).CombinedOutput(); err != nil {
		t.Errorf("a fresh clone can't fetch the bundle: %v: %s", err, out)
	}
}
