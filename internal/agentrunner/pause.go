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
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Running Tasks pause at a tool boundary (docs/plans/self-hosting.md, step
// 12). To pause a run, the operator sets the PauseAnnotation on its Sandbox's
// pod, which sees its own annotations at PodInfoDir through the downward API.
// Every Claude Code session runs theos-agent pause-guard as its PostToolUse hook:
// once the annotation is there, it ends the session after the tool that just
// ran, and theos-agent saves a clean checkpoint and reports Paused. Between agent
// calls theos-agent stops at its next call, and a check that runs is killed: it
// runs again on resume. A session still running PauseGrace after the request
// (a tool that doesn't end) is ended as on SIGTERM: a hard checkpoint, still
// Paused. A clean checkpoint on the same model resumes the same session with
// claude -p --resume; a hard one, or another model, starts a fresh session
// with the stopped one's note.

// PauseAnnotation is v1beta1.AnnotationPause: the runner has no cluster
// access and doesn't import the API, so cmd/theos-agent's tests pin that the two
// match.
const PauseAnnotation = "theos.factory.io/pause"

// PauseGuardCommand is the theos-agent subcommand every session's PostToolUse
// hook runs.
const PauseGuardCommand = "pause-guard"

// podAnnotationsFile is the downward API file of the pod's annotations, in
// PodInfoDir.
const podAnnotationsFile = "annotations"

// pausedFile is what pause-guard writes in a session's guard directory when
// it ends the session: so theos-agent tells a paused session from one that ended.
const pausedFile = "PAUSED"

// pauseGrace and pausePoll are PauseGrace and how often theos-agent reads the
// annotations; tests shorten them.
var (
	pauseGrace = PauseGrace
	pausePoll  = 5 * time.Second
)

// errPaused is an agent call the pause ended, or one theos-agent didn't start
// because of it.
var errPaused = errors.New("paused by the factory")

// pauseStopReason is what the hook tells Claude Code, and so the session's
// user, when it ends the session.
func pauseStopReason(reason string) string {
	if reason == "" {
		return "Paused by the factory"
	}
	return "Paused by the factory: " + reason
}

// resumePrompt is the prompt that resumes a session that ended cleanly at
// cp: the pause ended it, or its agent's service being unavailable did.
func resumePrompt(cp *checkpoint) string {
	if cp.Unavailable != "" {
		return fmt.Sprintf("Your service was unavailable (%s), and the factory resumed you once it was back. "+
			"The working tree is as you left it. Carry on.", cp.Unavailable)
	}
	reason := cp.PauseReason
	if reason == "" {
		return "The factory paused you and resumed you. The working tree is as you left it. Carry on."
	}
	return fmt.Sprintf("The factory paused you (%s) and resumed you. The working tree is as you left it. Carry on.",
		reason)
}

// pauseRequest reads the pause annotation from the downward API file of the
// pod's annotations: its value, and whether it is set. A file that is missing
// or can't be read is no pause.
func pauseRequest(file string) (string, bool) {
	data, err := os.ReadFile(file) //nolint:gosec // the pod's own downward API file
	if err != nil {
		return "", false
	}
	reason, ok := parseAnnotations(data)[PauseAnnotation]
	return reason, ok
}

// parseAnnotations reads the downward API's format: one key="value" per
// line, the value quoted as Go quotes it. A line it can't read is skipped.
func parseAnnotations(data []byte) map[string]string {
	annotations := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		if unquoted, err := strconv.Unquote(value); err == nil {
			annotations[key] = unquoted
		}
	}
	return annotations
}

// PauseGuardMain is theos-agent pause-guard: every session's PostToolUse hook.
// It reads the hook's input on stdin and, while the pod's annotations ask for
// a pause, writes the reason to --stopped and tells Claude Code on stdout to
// end the session. Else, with --checkpoint, it asks theos-agent on that socket to
// save the run when its last checkpoint is CheckpointEvery old, and waits
// for it (periodic.go). It allows what it can't judge: a broken guard never
// stops an agent.
func PauseGuardMain(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet(PauseGuardCommand, flag.ContinueOnError)
	flags.SetOutput(stderr)
	annotations := flags.String("annotations", "",
		"the downward API file of the pod's annotations; none: no pause")
	stopped := flags.String("stopped", "", "where to write the reason when the session is ended")
	checkpoint := flags.String("checkpoint", "", "the socket theos-agent saves a checkpoint on when asked")
	err := flags.Parse(args)
	if err == nil {
		err = guardPause(stdin, stdout, *annotations, *stopped, *checkpoint)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "theos-agent %s: %v\n", PauseGuardCommand, err)
	}
	return 0
}

func guardPause(stdin io.Reader, stdout io.Writer, annotations, stopped, checkpoint string) error {
	// Claude Code writes the hook's input whether it is read or not.
	_, _ = io.Copy(io.Discard, stdin)
	var reason string
	var paused bool
	if annotations != "" {
		reason, paused = pauseRequest(annotations)
	}
	if !paused {
		if checkpoint == "" {
			return nil
		}
		// A session the pause ends gets its clean checkpoint instead.
		return askCheckpoint(checkpoint)
	}
	if stopped != "" {
		if err := os.WriteFile(stopped, []byte(reason), 0o600); err != nil {
			return err
		}
	}
	return json.NewEncoder(stdout).Encode(map[string]any{"continue": false, "stopReason": pauseStopReason(reason)})
}

// pauseWatch is a run's view of the pause: it reads the annotations every
// pausePoll and when an agent call or check asks. A request stays for the
// rest of the run. With it, the checks' context ends at once, and the run's
// context pauseGrace later. A nil pauseWatch never pauses.
type pauseWatch struct {
	file string

	mu        sync.Mutex
	requested bool
	reason    string
	// hard means the grace ran out and ended the run's context.
	hard bool
	done bool
	// pauseChecks and endRun cancel the checks' context and the run's.
	pauseChecks, endRun context.CancelFunc
	grace               *time.Timer
}

// newPauseWatch watches the job's pod annotations; nil without PodInfoDir.
func newPauseWatch(job Job) *pauseWatch {
	if job.PodInfoDir == "" {
		return nil
	}
	return &pauseWatch{file: filepath.Join(job.PodInfoDir, podAnnotationsFile)}
}

// start watches until stop. The run's context ends with ctx or when the
// pause's grace runs out; the checks' context ends with it or at the pause.
func (w *pauseWatch) start(ctx context.Context) (run, checks context.Context, stop func()) {
	if w == nil {
		return ctx, ctx, func() {}
	}
	run, w.endRun = context.WithCancel(ctx)
	checks, w.pauseChecks = context.WithCancel(run)
	ticker := time.NewTicker(pausePoll)
	quit := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				w.poll()
			case <-quit:
				return
			}
		}
	}()
	var once sync.Once
	return run, checks, func() {
		once.Do(func() {
			ticker.Stop()
			close(quit)
			w.mu.Lock()
			defer w.mu.Unlock()
			w.done = true
			if w.grace != nil {
				w.grace.Stop()
			}
		})
	}
}

// poll reads the annotations, and reports whether the pause was requested.
func (w *pauseWatch) poll() bool {
	if w == nil {
		return false
	}
	if reason, paused := pauseRequest(w.file); paused {
		w.request(reason)
	}
	return w.isRequested()
}

// request records the pause, ends the checks and starts the grace.
func (w *pauseWatch) request(reason string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.requested || w.done {
		return
	}
	w.requested, w.reason = true, reason
	slog.Info("Pausing at the next tool boundary", "reason", reason, "grace", pauseGrace.String())
	if w.pauseChecks != nil {
		w.pauseChecks()
	}
	w.grace = time.AfterFunc(pauseGrace, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.done {
			return
		}
		w.hard = true
		slog.Warn("Ending the run: it is still busy past the pause's grace", "grace", pauseGrace.String())
		if w.endRun != nil {
			w.endRun()
		}
	})
}

// annotations is the file the sessions' pause-guard reads; "" without a watch.
func (w *pauseWatch) annotations() string {
	if w == nil {
		return ""
	}
	return w.file
}

func (w *pauseWatch) isRequested() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.requested
}

// state is whether the pause was requested, its reason, and whether its
// grace ran out.
func (w *pauseWatch) state() (requested bool, reason string, hard bool) {
	if w == nil {
		return false, "", false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.requested, w.reason, w.hard
}

// pausingAgent starts no agent call once the pause was requested: the run
// stops at its next step.
type pausingAgent struct {
	agent Agent
	pause *pauseWatch
}

func (a pausingAgent) Run(ctx context.Context, dir, prompt string) (string, error) {
	if a.pause.poll() {
		return "", errPaused
	}
	return a.agent.Run(ctx, dir, prompt)
}

// resumeKey carries, in an agent call's context, the session it resumes.
type resumeKey struct{}

// resumedSession is the session an agent call resumes, and the prompt a
// fresh session after a handoff from it gets.
type resumedSession struct {
	id     string
	prompt string
}

func withResume(ctx context.Context, id, prompt string) context.Context {
	return context.WithValue(ctx, resumeKey{}, resumedSession{id: id, prompt: prompt})
}

func resumeFrom(ctx context.Context) (resumedSession, bool) {
	r, ok := ctx.Value(resumeKey{}).(resumedSession)
	return r, ok
}

// resumable reports whether agent's sessions can be resumed: Claude Code's
// own command, whose sessions have IDs a checkpoint records.
func resumable(agent Agent) bool {
	claude, ok := agent.(ClaudeAgent)
	return ok && claude.SessionIDs
}

// noConversation is Claude Code's answer to a --resume of a session it has
// no transcript of.
var noConversation = regexp.MustCompile(`(?i)no conversation found`)

// claudeProject is the directory of Claude Code's config directory that keeps
// the transcripts of sessions run in dir: its absolute path with every
// character but letters and digits made a dash.
func claudeProject(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(dir, "-")
}

// claudeConfigDir is Claude Code's config directory: CLAUDE_CONFIG_DIR, else
// ~/.claude.
func claudeConfigDir() (string, error) {
	if config := os.Getenv("CLAUDE_CONFIG_DIR"); config != "" {
		return config, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// restoreTranscript puts the checkpoint's transcript of session id where
// Claude Code looks for it when a session in dir is resumed.
func restoreTranscript(checkpointDir, id, dir string) error {
	if id == "" || filepath.Base(id) != id || strings.HasPrefix(id, ".") {
		return fmt.Errorf("the checkpoint's session ID %q is not a file name", id)
	}
	data, err := os.ReadFile(filepath.Join(checkpointDir, checkpointTranscript)) //nolint:gosec // the checkpoint volume
	if err != nil {
		return err
	}
	config, err := claudeConfigDir()
	if err != nil {
		return err
	}
	project := filepath.Join(config, "projects", claudeProject(dir))
	if err := os.MkdirAll(project, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(project, id+".jsonl"), data, 0o600) //nolint:gosec // id is a file name, checked above
}
