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
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/JairJosafath/c4factory/internal/agentrunner"
)

func main() {
	resultPath := flag.String("result", "/dev/termination-log", "where to write the JSON result")
	workdir := flag.String("workdir", "/workspace/repo", "where to clone the repository")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	result := run(ctx, *workdir)
	slog.Info("Finished job", "role", result.Role, "commit", result.Commit, "verdict", result.Verdict,
		"error", result.Error)
	fmt.Println(result.Summary)
	if err := os.WriteFile(*resultPath, result.Encode(), 0o644); err != nil {
		slog.Error("Failed to write the result", "path", *resultPath, "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, workdir string) agentrunner.Result {
	job, err := agentrunner.JobFromEnv(os.Getenv)
	if err != nil {
		return agentrunner.Result{Role: os.Getenv(agentrunner.EnvRole), Error: "c4agent: " + err.Error()}
	}
	slog.Info("Starting job", "role", job.Role, "container", job.Container, "branch", job.Branch, "round", job.Round)
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		return agentrunner.Result{Role: job.Role, Error: err.Error()}
	}
	return agentrunner.Run(ctx, job, agentFor(job), workdir)
}

// agentFor picks the agent: the stand-in for the stand-in harness (or when
// C4AGENT_FAKE is set), else Claude Code with the job's model, or the command in
// C4AGENT_COMMAND.
func agentFor(job agentrunner.Job) agentrunner.Agent {
	if job.Harness == agentrunner.HarnessStandIn || os.Getenv("C4AGENT_FAKE") != "" {
		changesFirst, _ := strconv.ParseBool(os.Getenv("C4AGENT_FAKE_CHANGES_FIRST"))
		followUp, _ := strconv.ParseBool(os.Getenv("C4AGENT_FAKE_FOLLOW_UP"))
		return agentrunner.FakeAgent{Role: job.Role, Round: job.Round, ChangesFirst: changesFirst, FollowUp: followUp}
	}
	agent := agentrunner.ClaudeAgent{Model: job.Model}
	if command := strings.Fields(os.Getenv("C4AGENT_COMMAND")); len(command) > 0 {
		agent.Command = command
	}
	return agent
}
