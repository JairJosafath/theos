/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

// Command c4agent runs inside an agent's sandbox. It reads its job from the
// environment, does it with the agent, and writes one JSON result to the
// termination message file, where the operator reads it. It exits 0 when it
// could report; the result says whether the work succeeded.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/JairJosafath/c4factory/internal/agentrunner"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == agentrunner.SessionGuardCommand {
		os.Exit(agentrunner.SessionGuardMain(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	resultPath := flag.String("result", "/dev/termination-log", "where to write the JSON result")
	workdir := flag.String("workdir", "/workspace/repo", "where to clone the repository")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	result := run(ctx, *workdir)
	slog.Info("Finished job", "role", result.Role, "commit", result.Commit, "verdict", result.Verdict,
		"error", result.Error)
	fmt.Println(result.Summary)
	if err := os.WriteFile(*resultPath, result.Encode(), 0o600); err != nil {
		slog.Error("Failed to write the result", "path", *resultPath, "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, workdir string) agentrunner.Result {
	job, err := agentrunner.JobFromEnv(os.Getenv)
	if err != nil {
		return agentrunner.Result{Role: os.Getenv(agentrunner.EnvRole), Error: "c4agent: " + err.Error()}
	}
	slog.Info("Starting job", "role", job.Role, "container", job.Container, "brief", job.Brief, "branch", job.Branch,
		"round", job.Round)
	if err := os.MkdirAll(workdir, 0o750); err != nil {
		return agentrunner.Result{Role: job.Role, Error: err.Error()}
	}
	// For an Agent whose own GOPROXY names the Git host's registry, and for
	// uv's installs from the owner's index.
	home, err := os.UserHomeDir()
	if err == nil {
		err = agentrunner.WriteNetrc(home, job)
	}
	if err != nil {
		return agentrunner.Result{Role: job.Role, Error: "c4agent: .netrc: " + err.Error()}
	}
	// For npm's installs of the owner's scope.
	if err := agentrunner.WriteNpmrc(home, job); err != nil {
		return agentrunner.Result{Role: job.Role, Error: "c4agent: .npmrc: " + err.Error()}
	}
	stopProxy, err := serveGoProxy(job)
	if err != nil {
		return agentrunner.Result{Role: job.Role, Error: "c4agent: Go module proxy: " + err.Error()}
	}
	defer stopProxy()
	return agentrunner.Run(ctx, job, agentFor(job), workdir)
}

// serveGoProxy serves the job's Go module proxy on loopback, where GOPROXY
// points, while the agent works; the returned func stops it.
func serveGoProxy(job agentrunner.Job) (func(), error) {
	proxy, err := agentrunner.NewGoProxy(job)
	if err != nil || proxy == nil {
		return func() {}, err
	}
	ln, err := net.Listen("tcp", agentrunner.GoProxyAddr)
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: proxy, ReadHeaderTimeout: 30 * time.Second}
	go func() { _ = server.Serve(ln) }()
	return func() { _ = server.Close() }, nil
}

// agentFor picks the agent: the stand-in for the stand-in harness (or when
// C4AGENT_FAKE is set), else Claude Code with the job's model, session
// limit and session IDs, or the command in C4AGENT_COMMAND, whose one session
// runs to its end.
func agentFor(job agentrunner.Job) agentrunner.Agent {
	if job.Harness == agentrunner.HarnessStandIn || os.Getenv("C4AGENT_FAKE") != "" {
		changesFirst, _ := strconv.ParseBool(os.Getenv("C4AGENT_FAKE_CHANGES_FIRST"))
		followUp, _ := strconv.ParseBool(os.Getenv("C4AGENT_FAKE_FOLLOW_UP"))
		return agentrunner.FakeAgent{Role: job.Role, Round: job.Round, ChangesFirst: changesFirst, FollowUp: followUp}
	}
	agent := agentrunner.ClaudeAgent{Model: job.Model}
	if command := strings.Fields(os.Getenv("C4AGENT_COMMAND")); len(command) > 0 {
		agent.Command = command
		return agent
	}
	// Claude Code's own command takes a --session-id, which a checkpoint records.
	agent.SessionIDs = true
	if self, err := os.Executable(); err == nil {
		agent.SessionTokenLimit, agent.Guard = job.SessionTokenLimit, self
	} else {
		slog.Error("Running sessions unguarded: c4agent can't find itself", "error", err)
	}
	return agent
}
