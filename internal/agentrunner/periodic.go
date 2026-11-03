package agentrunner

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

// A run killed with no warning (a power loss, wsl --shutdown) has no SIGTERM
// to save it on (docs/plans/self-hosting.md, step 14, #1997). So between tool
// calls, pause-guard also has c4agent save a hard checkpoint when the last
// one is CheckpointEvery old, and the session goes on. The next pod resumes
// from it as from any hard checkpoint: a fresh session with the note.
//
// The hook asks and c4agent saves, rather than the hook saving itself: the
// run's state (its step, its session, what its finished agent calls used)
// lives in the c4agent process, and pause-guard is a process of its own that
// Claude Code starts. So c4agent listens on a Unix socket in the session
// guard's directory, and pause-guard connects and waits for the answer.
// Claude Code waits for its hook, so the save happens between tool calls,
// while no tool writes. A guard that can't connect, or gets no answer in
// checkpointWait, allows: a broken guard never stops an agent.

// CheckpointEvery is how old the last checkpoint gets before c4agent saves
// another between tool calls.
const CheckpointEvery = 10 * time.Minute

// checkpointSocket is the socket's name in the session guard's directory.
const checkpointSocket = "checkpoint.sock"

// checkpointWait bounds how long pause-guard waits for c4agent's answer: the
// save's own bound and a margin, so the hook doesn't give up on a save that
// is still writing. It stays under Claude Code's 60 seconds for a hook.
const checkpointWait = checkpointTimeout + 10*time.Second

// Answers c4agent gives pause-guard; it only logs them.
const (
	answerSaved   = "saved"
	answerNotDue  = "not due"
	answerNothing = "nothing to save"
)

// checkpointEvery, checkpointNow and askWait are CheckpointEvery, the
// clock and checkpointWait; tests change them.
var (
	checkpointEvery = CheckpointEvery
	checkpointNow   = time.Now
	askWait         = checkpointWait
)

// periodicCheckpoints saves a run's hard checkpoint between tool calls. nil
// saves none: the run has no checkpoint directory.
type periodicCheckpoints struct {
	job      Job
	prog     *runProgress
	sessions *sessionLog
	meter    *usageMeter

	// mu serializes the asks: parallel tool calls ask at once, and the
	// second finds a fresh checkpoint.
	mu sync.Mutex
	// last is when this pod saved its newest checkpoint, else when its run
	// started: a checkpoint it restored doesn't count, as the pod's own work
	// starts the clock.
	last time.Time
}

func newPeriodicCheckpoints(job Job, prog *runProgress, sessions *sessionLog,
	meter *usageMeter) *periodicCheckpoints {
	if job.CheckpointDir == "" {
		return nil
	}
	return &periodicCheckpoints{job: job, prog: prog, sessions: sessions, meter: meter, last: checkpointNow()}
}

// save saves a hard checkpoint of the run, in its session, when the last is
// checkpointEvery old, and returns what it did. A save that fails is logged,
// and tried again checkpointEvery later: a volume that is full shouldn't
// slow every tool call.
func (p *periodicCheckpoints) save(ctx context.Context) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if checkpointNow().Sub(p.last) < checkpointEvery {
		return answerNotDue
	}
	// What this pod's finished agent calls used. The session in progress
	// reports its cost only when it ends, so it adds none yet: the run's
	// final result, or a later checkpoint, counts it.
	result := Result{Role: p.job.Role, SessionHandoffs: p.meter.handoffs}
	if cost := p.meter.total(); cost != nil {
		result.CostMilliUSD = new(*cost)
	}
	if tokens := p.meter.totalTokens(); tokens != nil {
		result.Tokens = new(*tokens)
	}
	saved, err := saveCheckpoint(ctx, p.job, p.prog, p.sessions, result, CheckpointHard, nil)
	if err != nil || saved {
		p.last = checkpointNow()
	}
	if err != nil {
		slog.Warn("Failed to save a checkpoint between tool calls; the session goes on", "error", err.Error())
		return "failed: " + err.Error()
	}
	if !saved {
		return answerNothing
	}
	id, _ := p.sessions.newest()
	slog.Info("Saved a checkpoint between tool calls", "checkpoint", CheckpointHard, "step", p.prog.step,
		"session", id)
	return answerSaved
}

// serve answers pause-guard's asks on socket while one session runs. The
// returned func stops it and waits for a save in progress: the next session,
// or a later save of the run, must not run beside it. ctx ends a save in
// progress, so a stop doesn't wait for it before its own save.
func (p *periodicCheckpoints) serve(ctx context.Context, socket string) (stop func()) {
	if p == nil || socket == "" {
		return func() {}
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "unix", socket)
	if err != nil {
		slog.Warn("Saving no checkpoints between this session's tool calls", "error", err.Error())
		return func() {}
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				defer func() { _ = conn.Close() }()
				_ = conn.SetWriteDeadline(time.Now().Add(checkpointWait))
				_, _ = fmt.Fprintln(conn, p.save(ctx))
			})
		}
	})
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = ln.Close()
			wg.Wait()
		})
	}
}

// askCheckpoint is pause-guard's ask: it connects to c4agent's socket and
// waits, at most askWait, for c4agent to save the run when it is due.
func askCheckpoint(socket string) error {
	deadline := time.Now().Add(askWait)
	conn, err := net.DialTimeout("unix", socket, askWait)
	if err != nil {
		return fmt.Errorf("asking for a checkpoint: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	answer, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return fmt.Errorf("asking for a checkpoint: %w", err)
	}
	if answer = strings.TrimSpace(answer); strings.HasPrefix(answer, "failed") {
		return fmt.Errorf("checkpoint %s", answer)
	}
	return nil
}
