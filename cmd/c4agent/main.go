/*
Copyright 2026.

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
