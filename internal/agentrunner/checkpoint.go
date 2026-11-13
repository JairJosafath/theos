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
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/JairJosafath/theos/internal/model"
)

// A run continues from a checkpoint (docs/design.md, "A run continues from a
// checkpoint"). The sandbox has a volume of its own that outlives its pod,
// mounted at Job.CheckpointDir. Told to stop (SIGTERM: a node shutdown, an
// eviction), theos-agent ends the agent's session as it always did, then saves
// the run there: the working tree as a git bundle, its uncommitted changes as
// a commit inside it that is never pushed, the session's Claude Code
// transcript, and state.json. The sandbox's next pod clones as usual, restores
// the bundle and goes on at the recorded step, in a fresh session told what
// the stopped one left. A checkpoint it can't read starts the run over.

// Checkpoint kinds: a hard one is saved when theos-agent is told to stop, and
// its session could not end on its own, and between tool calls while the
// session goes on (periodic.go); a clean one, saved when the pause
// ended the session after a tool or stopped the run between its steps, can go
// on in the same session (pause.go).
const (
	CheckpointHard  = "hard"
	CheckpointClean = "clean"
)

// PodInfoDir is where the Sandbox pod mounts its own annotations through the
// downward API (a file named annotations, one key="value" per line). The
// pause-guard hook reads it to see theos.factory.io/pause annotation.
const PodInfoDir = "/etc/theos-agent/podinfo"

// PauseGrace is how long a tool may still run after the pause annotation
// appears: past it, theos-agent ends the run as on SIGTERM (a hard checkpoint).
const PauseGrace = 5 * time.Minute

// checkpointVersion is the format of state.json this theos-agent writes and
// reads; one of another version starts the run over.
const checkpointVersion = 1

// Files in the checkpoint directory. The bundle's name is in state.json, so
// writing state.json switches to a new checkpoint at once.
const (
	checkpointStateFile  = "state.json"
	checkpointTranscript = "transcript.jsonl"
)

// Refs the bundle carries: the working tree's commit, and the commit a merge
// in progress merges.
const (
	checkpointRef      = "refs/c4/checkpoint"
	checkpointMergeRef = "refs/c4/merge-head"
)

// Steps of a run a checkpoint goes on at: the role's agent session, or, for
// an implement run, the check and its fixes after it.
const (
	StepAgent = "agent"
	StepCheck = "check"
)

// maxUnavailableNote bounds the service's error a checkpoint keeps, for the
// resumed session.
const maxUnavailableNote = 300

// checkpointTimeout bounds saving a checkpoint: the pod's grace period is a
// minute, and the session ends first.
const checkpointTimeout = 40 * time.Second

// checkpoint is state.json.
type checkpoint struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
	Role    string `json:"role"`
	Task    string `json:"task"`
	Round   int    `json:"round"`
	// Step is where the run goes on: StepAgent or StepCheck.
	Step string `json:"step"`
	// SessionID is the stopped session's, and Model its model.
	SessionID string `json:"sessionID,omitempty"`
	Model     string `json:"model,omitempty"`
	// Handoffs, CostMilliUSD and Tokens are the run's so far, over its pods.
	Handoffs     []int64           `json:"handoffs,omitempty"`
	CostMilliUSD *int64            `json:"costMilliUSD,omitempty"`
	Tokens       *model.TokenUsage `json:"tokens,omitempty"`
	// Bundle is the bundle's file name in the checkpoint directory.
	Bundle string `json:"bundle"`
	// Head is the work branch's head; Work, when set, the bundle's commit on
	// it that holds the uncommitted changes. Start is the branch's head
	// before the run, MergeHead a merge's other parent while it is in
	// progress, and Conflicts the files its merge of the base left.
	Head      string   `json:"head"`
	Work      string   `json:"work,omitempty"`
	Start     string   `json:"start"`
	MergeHead string   `json:"mergeHead,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
	// Summary is the agent's answer at StepCheck.
	Summary string `json:"summary,omitempty"`
	// Note is what the fresh session is told of the stopped one.
	Note string `json:"note,omitempty"`
	// PauseReason is the pause annotation's value when the factory paused
	// the run: the resumed session is told it.
	PauseReason string `json:"pauseReason,omitempty"`
	// Unavailable is the error of the agent's service that stopped the run,
	// when it was unavailable: the resumed session is told it.
	Unavailable string `json:"unavailable,omitempty"`
}

// runProgress is where a run is, for its checkpoint.
type runProgress struct {
	repo    *repo
	step    string
	summary string
	// from is the checkpoint the run went on from; nil when it started afresh.
	from *checkpoint
	// resumable means the run's agent can resume a session (pause.go), and
	// resume is the session it resumes at StepAgent: from's, when from is
	// clean, on the job's model and its transcript restored.
	resumable bool
	resume    string
	// checks is the context of the run's checks: it ends at the pause.
	checks context.Context
}

// resumesAt reports whether the run went on from a checkpoint at step.
func (p *runProgress) resumesAt(step string) bool {
	return p.from != nil && p.from.Step == step
}

// session runs the run's agent session with the job's prompt. When the run
// went on from a checkpoint at that session, it resumes the stopped session,
// told it was paused, or else starts a fresh one with the stopped one's note.
func (p *runProgress) session(ctx context.Context, agent Agent, dir, prompt string) (string, error) {
	if !p.resumesAt(StepAgent) {
		return agent.Run(ctx, dir, prompt)
	}
	if p.resume != "" {
		answer, err := agent.Run(withResume(ctx, p.resume, prompt), dir, resumePrompt(p.from))
		if err == nil || !noConversation.MatchString(err.Error()) {
			return answer, err
		}
		slog.Warn("Starting a fresh session: Claude Code can't resume the paused one", "session", p.resume,
			"error", err.Error())
	}
	if p.from.Note == "" {
		return agent.Run(ctx, dir, prompt)
	}
	return agent.Run(ctx, dir, handoffPrompt(prompt, p.from.Note))
}

// checkNote is what the first fix session of a run that went on at its
// check from an unavailable agent's checkpoint is told (#1996): a fresh
// session, it would not know the earlier one stopped. "" for any other run.
func (p *runProgress) checkNote() string {
	if !p.resumesAt(StepCheck) || p.from.Unavailable == "" {
		return ""
	}
	return fmt.Sprintf("The agent's service was unavailable (%s) while the check was being fixed, and the "+
		"factory resumed the run once it was back. The working tree is as the stopped session left it.",
		p.from.Unavailable)
}

// checksContext is the context the run's checks run in: ctx, ended at the
// pause.
func (p *runProgress) checksContext(ctx context.Context) context.Context {
	if p.checks == nil {
		return ctx
	}
	return p.checks
}

// resumeSession picks the session a run that went on from cp resumes: cp's,
// when it is clean, of the job's model, its agent can resume one, and the
// session's transcript is back where Claude Code reads it. Else "": a fresh
// session with the note.
func resumeSession(job Job, cp *checkpoint, agentResumes bool, dir string) string {
	if cp == nil || cp.Kind != CheckpointClean || cp.Step != StepAgent || cp.SessionID == "" || !agentResumes {
		return ""
	}
	if cp.Model != job.Model {
		slog.Info("Starting a fresh session: the Agent's model changed since the pause", "was", cp.Model,
			"now", job.Model)
		return ""
	}
	if err := restoreTranscript(job.CheckpointDir, cp.SessionID, dir); err != nil {
		slog.Warn("Starting a fresh session: the paused session's transcript can't be restored", "error", err.Error())
		return ""
	}
	return cp.SessionID
}

// sessionLog records the agent's newest session: its ID and last words.
type sessionLog struct {
	mu       sync.Mutex
	id       string
	lastText string
}

func (l *sessionLog) started(id string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.id, l.lastText = id, ""
}

// said records the session's newest words while it runs, for a checkpoint
// saved between its tool calls.
func (l *sessionLog) said(text string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lastText = text
}

func (l *sessionLog) ended(lastText string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lastText = lastText
}

func (l *sessionLog) newest() (id, lastText string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.id, l.lastText
}

// withSessionLog has a Claude Code agent record its sessions in log, guard
// them with pause, and save periodic checkpoints between their tool calls.
func withSessionLog(agent Agent, log *sessionLog, pause *pauseWatch, periodic *periodicCheckpoints) Agent {
	if claude, ok := agent.(ClaudeAgent); ok {
		claude.sessions, claude.pause, claude.checkpoints = log, pause, periodic
		return claude
	}
	return agent
}

// newSessionID is a random UUID, as Claude Code's --session-id takes it.
func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// restoreCheckpoint goes on from the checkpoint in the job's directory, if
// there is one this run can use: it puts the bundle's working tree and
// branch state into the fresh clone r. It returns nil when the run starts
// over: no checkpoint, an unknown version, another run's, or one that can't
// be restored. An error means a restore that failed half way could not put
// the clone back as it was: the run can't start over in it.
func restoreCheckpoint(ctx context.Context, job Job, r *repo) (*checkpoint, error) {
	if job.CheckpointDir == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(job.CheckpointDir, checkpointStateFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var cp checkpoint
	switch {
	case err != nil:
	case json.Unmarshal(data, &cp) != nil:
		err = errors.New("state.json is not JSON")
	case cp.Version != checkpointVersion:
		err = fmt.Errorf("version %d, want %d", cp.Version, checkpointVersion)
	case cp.Role != job.Role || cp.Task != job.Task || cp.Round != job.Round:
		err = fmt.Errorf("it is for the %s run of Task %s, round %d", cp.Role, cp.Task, cp.Round)
	case cp.Step != StepAgent && cp.Step != StepCheck || cp.Head == "" || cp.Bundle == "" ||
		filepath.Base(cp.Bundle) != cp.Bundle:
		err = errors.New("state.json misses what the run goes on from")
	default:
		if err = cp.restore(ctx, job, r); errors.Is(err, errCloneChanged) {
			return nil, err
		}
	}
	if err != nil {
		slog.Warn("Starting the run over: its checkpoint can't be used", "error", err.Error())
		return nil, nil
	}
	slog.Info("Going on from a checkpoint", "kind", cp.Kind, "step", cp.Step, "session", cp.SessionID)
	return &cp, nil
}

// restore fetches the bundle into r, then makes r's work branch, index and
// working tree what they were. The fetch touches no file, so a bundle that
// can't be read leaves the clone as it was; a step after it that fails puts
// the clone back where it was, so a run that starts over never builds on, or
// pushes, the checkpoint's commit of the working tree. One it can't put back
// fails with errCloneChanged.
func (cp *checkpoint) restore(ctx context.Context, job Job, r *repo) error {
	orig, err := r.head(ctx)
	if err != nil {
		return err
	}
	bundle := filepath.Join(job.CheckpointDir, cp.Bundle)
	refs := []string{checkpointRef + ":" + checkpointRef}
	if cp.MergeHead != "" {
		refs = append(refs, checkpointMergeRef+":"+checkpointMergeRef)
	}
	if _, err := r.git(ctx, r.dir, append([]string{"fetch", "--quiet", "--force", bundle}, refs...)...); err != nil {
		return err
	}
	if err := cp.apply(ctx, r); err != nil {
		// A hard reset also drops a MERGE_HEAD apply wrote.
		if _, undo := r.git(ctx, r.dir, "reset", "--quiet", "--hard", orig); undo != nil {
			return fmt.Errorf("%w: restoring the checkpoint failed (%w), and putting it back at %s too: %w",
				errCloneChanged, err, orig, undo)
		}
		return err
	}
	r.start, r.conflicts = cp.Start, cp.Conflicts
	return nil
}

// errCloneChanged is a restore that failed half way and could not put the
// clone back as it was.
var errCloneChanged = errors.New("the clone is not as it was cloned")

// apply makes the clone's work branch, index and working tree the
// checkpoint's, from its fetched commits.
func (cp *checkpoint) apply(ctx context.Context, r *repo) error {
	tree := cp.Head
	if cp.Work != "" {
		tree = cp.Work
	}
	if _, err := r.git(ctx, r.dir, "reset", "--quiet", "--hard", tree); err != nil {
		return err
	}
	if cp.Work != "" {
		// The changes go back to the working tree, uncommitted as they were.
		if _, err := r.git(ctx, r.dir, "reset", "--quiet", cp.Head); err != nil {
			return err
		}
	}
	if cp.MergeHead != "" {
		if _, err := r.git(ctx, r.dir, "update-ref", "MERGE_HEAD", cp.MergeHead); err != nil {
			return err
		}
	}
	return nil
}

// saveCheckpoint saves the run, stopped at prog's step or between two of its
// tool calls, to the job's checkpoint directory as a checkpoint of kind, and
// reports whether it did: a run with no directory, or stopped before its
// clone, saves none.
// pauseReason is the pause's, when the factory paused the run. result is
// what the stopped pod reports; the checkpoint adds it to what the run's
// earlier pods used, and says when the agent's service was unavailable.
func saveCheckpoint(ctx context.Context, job Job, prog *runProgress, sessions *sessionLog, result Result,
	kind string, pauseReason *string) (bool, error) {
	if job.CheckpointDir == "" || prog.repo == nil || prog.step == "" {
		return false, nil
	}
	return true, saveRun(ctx, job, prog, sessions, result, kind, pauseReason)
}

// saveRun saves the run within checkpointTimeout, or until ctx ends: a save
// between tool calls ends with the run's context (periodic.go), the saves
// of a stopped run get a context of their own.
func saveRun(ctx context.Context, job Job, prog *runProgress, sessions *sessionLog, result Result, kind string,
	pauseReason *string) error {
	ctx, cancel := context.WithTimeout(ctx, checkpointTimeout)
	defer cancel()
	r := prog.repo
	head, err := r.head(ctx)
	if err != nil {
		return err
	}
	cp := checkpoint{Version: checkpointVersion, Kind: kind, Role: job.Role, Task: job.Task,
		Round: job.Round, Step: prog.step, Model: job.Model, Head: head, Start: r.start, Conflicts: r.conflicts,
		Summary: prog.summary, Bundle: fmt.Sprintf("work-%d.bundle", time.Now().UnixNano())}
	if cp.Work, err = r.snapshot(ctx); err != nil {
		return err
	}
	if out, err := r.git(ctx, r.dir, "rev-parse", "--quiet", "--verify", "MERGE_HEAD"); err == nil {
		cp.MergeHead = strings.TrimSpace(out)
	}
	cp.addUsage(prog.from, result)
	id, lastText := sessions.newest()
	cp.SessionID = id
	headline := "The previous session was stopped with its sandbox, before it could write a note."
	switch {
	case pauseReason == nil && result.Unavailable:
		cp.Unavailable = shorten(strings.Join(strings.Fields(result.Error), " "), maxUnavailableNote)
		headline = fmt.Sprintf("The agent's service was unavailable (%s), which ended the previous session; "+
			"the factory waited for it to be back.", cp.Unavailable)
	case pauseReason != nil:
		cp.PauseReason = *pauseReason
		headline = fmt.Sprintf("The factory paused the previous session (%s) after a tool call.",
			orDefault(*pauseReason, "no reason given"))
		if kind == CheckpointHard {
			headline = fmt.Sprintf("The factory paused the previous session (%s), and ended it when it was "+
				"still busy %s later.", orDefault(*pauseReason, "no reason given"), pauseGrace)
		}
	}
	cp.Note = endedNote(ctx, r.dir, headline, lastText)
	if id == "" {
		// No session ran in this pod: the stopped one is still the earlier
		// pod's, or none ran yet and there is nothing to tell. The earlier
		// pod's session keeps its kind and model too (#1995): a hard one, or
		// one on another model, is never resumed with --resume.
		cp.SessionID, cp.Note = "", ""
		if prog.from != nil {
			cp.SessionID, cp.Note = prog.from.SessionID, prog.from.Note
			cp.Kind, cp.Model = prog.from.Kind, prog.from.Model
		}
	}

	tip := cp.Head
	if cp.Work != "" {
		tip = cp.Work
	}
	refs := []string{checkpointRef}
	if _, err := r.git(ctx, r.dir, "update-ref", checkpointRef, tip); err != nil {
		return err
	}
	if cp.MergeHead != "" {
		if _, err := r.git(ctx, r.dir, "update-ref", checkpointMergeRef, cp.MergeHead); err != nil {
			return err
		}
		refs = append(refs, checkpointMergeRef)
	}
	bundle := filepath.Join(job.CheckpointDir, cp.Bundle)
	if _, err := r.git(ctx, r.dir, append([]string{"bundle", "create", "--quiet", bundle},
		append(refs, bundleBase(ctx, r, refs, tip)...)...)...); err != nil {
		return err
	}
	if id != "" {
		if err := copyTranscript(id, filepath.Join(job.CheckpointDir, checkpointTranscript)); err != nil {
			slog.Warn("Saving the checkpoint without the session's transcript", "session", id, "error", err.Error())
		}
	}
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	state := filepath.Join(job.CheckpointDir, checkpointStateFile)
	if err := os.WriteFile(state+".new", data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(state+".new", state); err != nil {
		return err
	}
	// The earlier checkpoints' bundles are no longer named.
	old, _ := filepath.Glob(filepath.Join(job.CheckpointDir, "work-*.bundle"))
	for _, f := range old {
		if filepath.Base(f) != cp.Bundle {
			_ = os.Remove(f)
		}
	}
	return nil
}

// bundleBase is what a checkpoint's bundle of refs leaves out: what the
// remote has, which the next pod's fresh clone has too, so the bundle holds
// only the run's own commits. When the remote has them all, git refuses an
// empty bundle: it then holds tip's commit alone, on its parents.
func bundleBase(ctx context.Context, r *repo, refs []string, tip string) []string {
	base := []string{"--not", "--remotes"}
	out, err := r.git(ctx, r.dir, append(append([]string{"rev-list", "--count"}, refs...), base...)...)
	if err == nil && strings.TrimSpace(out) == "0" {
		return []string{"--not", tip + "^@"}
	}
	return base
}

// addUsage adds the stopped pod's usage to the run's earlier pods'. A cost
// or tokens one pod didn't report leaves the run's unreported.
func (cp *checkpoint) addUsage(from *checkpoint, result Result) {
	cp.Handoffs = result.SessionHandoffs
	cp.CostMilliUSD, cp.Tokens = result.CostMilliUSD, result.Tokens
	if from == nil {
		return
	}
	cp.Handoffs = append(slices.Clone(from.Handoffs), result.SessionHandoffs...)
	if cp.CostMilliUSD != nil && from.CostMilliUSD != nil {
		cost := *from.CostMilliUSD + *cp.CostMilliUSD
		cp.CostMilliUSD = &cost
	} else {
		cp.CostMilliUSD = nil
	}
	if cp.Tokens != nil && from.Tokens != nil {
		tokens := *from.Tokens
		tokens.Add(*cp.Tokens)
		cp.Tokens = &tokens
	} else {
		cp.Tokens = nil
	}
}

// snapshot commits the working tree, untracked files too, on top of HEAD
// through an index of its own, and returns the commit; "" when the tree is
// clean. The branch, the index and the files stay as they are.
func (r *repo) snapshot(ctx context.Context) (string, error) {
	// No optional locks: a periodic save runs this beside the session, whose
	// own git commands mustn't find the index locked.
	status, err := r.gitEnv(ctx, []string{"GIT_OPTIONAL_LOCKS=0"}, "status", "--porcelain")
	if err != nil || strings.TrimSpace(status) == "" {
		return "", err
	}
	index, err := os.CreateTemp("", "theos-agent-checkpoint-index-")
	if err != nil {
		return "", err
	}
	_ = index.Close()
	defer func() { _ = os.Remove(index.Name()) }()
	env := []string{"GIT_INDEX_FILE=" + index.Name()}
	if _, err := r.gitEnv(ctx, env, "read-tree", "HEAD"); err != nil {
		return "", err
	}
	if _, err := r.gitEnv(ctx, env, "add", "--all"); err != nil {
		return "", err
	}
	tree, err := r.gitEnv(ctx, env, "write-tree")
	if err != nil {
		return "", err
	}
	commit, err := r.git(ctx, r.dir, "commit-tree", strings.TrimSpace(tree), "-p", "HEAD",
		"-m", "theos-agent checkpoint: the working tree when the sandbox stopped")
	return strings.TrimSpace(commit), err
}

// gitEnv runs git in the clone with env added.
func (r *repo) gitEnv(ctx context.Context, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // git, with arguments the runner builds
	cmd.Dir = r.dir
	cmd.Env = append(cmd.Environ(), append(env, "GIT_TERMINAL_PROMPT=0")...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// copyTranscript copies the Claude Code transcript of session id to dst.
// Claude Code keeps it as projects/<project>/<id>.jsonl in its config
// directory: CLAUDE_CONFIG_DIR, else ~/.claude.
func copyTranscript(id, dst string) error {
	config, err := claudeConfigDir()
	if err != nil {
		return err
	}
	found, _ := filepath.Glob(filepath.Join(config, "projects", "*", id+".jsonl"))
	if len(found) == 0 {
		return fmt.Errorf("no transcript of session %s in %s", id, config)
	}
	src, err := os.Open(found[0]) //nolint:gosec // Claude Code's transcript of the run's own session
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // the checkpoint volume
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
