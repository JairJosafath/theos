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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/model"
)

// harnessContract is what the contract needs to know of one harness.
type harnessContract struct {
	// cli is false for a harness that runs no program: the stand-in.
	cli bool
	// model is what the CLI is given for the job's model, contractModel.
	model []string
	// failure is the CLI's own word in failed.jsonl, which the error keeps.
	failure string
}

// contractModel is the model of the contract's jobs.
const contractModel = "contract-model"

// harnessContracts has a case for every harness: a harness without one
// fails the contract (#58). A CLI harness brings its transcripts in
// testdata/harness/<name>/.
var harnessContracts = map[string]harnessContract{
	HarnessClaudeCode: {cli: true, model: []string{"--model", contractModel}, failure: "the tool crashed"},
	HarnessStandIn:    {},
}

// standInCLI makes a program that stands in for h's CLI: it keeps its
// arguments and what it was sent, prints the scene's transcript and exits
// with code. It returns the harness's agent on that program, and where the
// program keeps its arguments and its input.
func standInCLI(t *testing.T, h Harness, scene string, code int) (agent Agent, args, stdin string) {
	t.Helper()
	transcript, err := filepath.Abs(filepath.Join("testdata", "harness", h.Name, scene+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(transcript); err != nil {
		t.Fatalf("harness %s has no transcript of the scene %q: %v", h.Name, scene, err)
	}
	dir := t.TempDir()
	args, stdin = filepath.Join(dir, "args"), filepath.Join(dir, "stdin")
	script := filepath.Join(dir, "cli")
	body := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" >'%s'\ncat >'%s'\ncat '%s'\nexit %d\n", args, stdin, transcript, code)
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil { //nolint:gosec // a test's own script
		t.Fatal(err)
	}
	env := func(name string) string {
		if name == EnvAgentCommand {
			return script
		}
		return ""
	}
	return h.Agent(Job{Role: RoleImplement, Harness: h.Name, Model: contractModel}, env, ""), args, stdin
}

// TestEveryHarnessMeetsTheContract holds each harness in the table to the
// same answers on a stand-in for its CLI (#58): the prompt reaches it and
// the model is named; a change's answer and its tokens come back; a
// review's verdict and a Blocked line survive; a failed call is an error in
// the CLI's words; and an unavailable service is told from the agent's own
// failure.
func TestEveryHarnessMeetsTheContract(t *testing.T) {
	const prompt = "Add the handler.\nRead AGENTS.md first."
	for _, h := range harnesses {
		c, ok := harnessContracts[h.Name]
		if !ok {
			t.Errorf("harness %s has no case in harnessContracts", h.Name)
			continue
		}
		t.Run(h.Name, func(t *testing.T) {
			if !c.cli {
				scriptedContract(t, h)
				return
			}
			job := Job{Harness: h.Name}

			agent, args, stdin := standInCLI(t, h, "change", 0)
			reporter, ok := agent.(UsageReporter)
			if !ok {
				t.Fatalf("the agent of a harness with a CLI reports what a call used; %T doesn't", agent)
			}
			answer, usage, err := reporter.RunReportingUsage(context.Background(), t.TempDir(), prompt)
			if err != nil || answer != "Added the handler and its test." {
				t.Errorf("a change: answer %q, error %v; want the CLI's last words", answer, err)
			}
			if usage.Tokens == nil || usage.Tokens.Output == 0 ||
				usage.Tokens.Input+usage.Tokens.CacheRead+usage.Tokens.CacheWrite == 0 {
				t.Errorf("a change: tokens %+v; want what the call read and wrote", usage.Tokens)
			}
			if sent, _ := os.ReadFile(stdin); string(sent) != prompt { //nolint:gosec // the test's own file
				t.Errorf("the CLI was sent %q, want the prompt", sent)
			}
			got, _ := os.ReadFile(args) //nolint:gosec // the test's own file
			if given := strings.Fields(string(got)); !containsRun(given, c.model) {
				t.Errorf("the CLI's arguments are %q, want %q among them", given, c.model)
			}

			agent, _, _ = standInCLI(t, h, "review", 0)
			answer, err = agent.Run(context.Background(), t.TempDir(), prompt)
			if verdict, verr := ParseVerdict(answer); err != nil || verr != nil ||
				verdict.Verdict != VerdictApprove || verdict.Summary != "Reads well." {
				t.Errorf("a review: answer %q (%v) gave the verdict %+v (%v); want an approval", answer, err, verdict, verr)
			}

			agent, _, _ = standInCLI(t, h, "blocked", 0)
			answer, err = agent.Run(context.Background(), t.TempDir(), prompt)
			if note := model.BlockedNote(answer); err != nil ||
				note != "the staging database isn't reachable from the sandbox." {
				t.Errorf("a blocked issue: answer %q (%v) gave the note %q", answer, err, note)
			}

			agent, _, _ = standInCLI(t, h, "failed", 1)
			_, err = agent.Run(context.Background(), t.TempDir(), prompt)
			if err == nil || !strings.Contains(err.Error(), c.failure) {
				t.Fatalf("a failed call: error %v, want one with the CLI's %q", err, c.failure)
			}
			if down, _ := unavailable(job, err.Error(), time.Now()); down {
				t.Errorf("a failed call (%v) reads as an unavailable service", err)
			}

			agent, _, _ = standInCLI(t, h, "unavailable", 1)
			_, err = agent.Run(context.Background(), t.TempDir(), prompt)
			if err == nil {
				t.Fatal("an unavailable service: no error")
			}
			if down, _ := unavailable(job, err.Error(), time.Now()); !down {
				t.Errorf("an unavailable service (%v) reads as the agent's own failure", err)
			}
		})
	}
	for name := range harnessContracts {
		if _, ok := harnessOf(name); !ok {
			t.Errorf("harnessContracts has a case for %s, which is no harness", name)
		}
	}
}

// scriptedContract is the contract of a harness that runs no program: it
// changes the checkout when it implements and approves when it reviews, and
// nothing it says reads as an unavailable service.
func scriptedContract(t *testing.T, h Harness) {
	t.Helper()
	none := func(string) string { return "" }
	dir := t.TempDir()
	answer, err := h.Agent(Job{Role: RoleImplement, Harness: h.Name, Round: 1}, none, "").
		Run(context.Background(), dir, "")
	if err != nil || answer == "" {
		t.Errorf("a change: answer %q, error %v", answer, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) == 0 {
		t.Error("a change left the checkout as it was")
	}
	answer, err = h.Agent(Job{Role: RoleReview, Harness: h.Name, Round: 1}, none, "").
		Run(context.Background(), dir, "")
	if verdict, verr := ParseVerdict(answer); err != nil || verr != nil || verdict.Verdict != VerdictApprove {
		t.Errorf("a review: answer %q (%v) gave the verdict %+v (%v); want an approval", answer, err, verdict, verr)
	}
	if down, _ := unavailable(Job{Harness: h.Name}, "usage limit reached|1759417200", time.Now()); down {
		t.Error("a harness that calls no service reads its output as an unavailable one")
	}
}

// containsRun reports whether run is in s, in order and side by side.
func containsRun(s, run []string) bool {
	for i := 0; i+len(run) <= len(s); i++ {
		if slices.Equal(s[i:i+len(run)], run) {
			return true
		}
	}
	return false
}

// AgentFor picks what theos-agent picked before the table (#58, step 1):
// nothing a sandbox runs changed.
func TestAgentFor(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(name string) string { return vars[name] }
	}
	job := Job{Role: RoleReview, Round: 2, Goal: "the goal", Issue: 7, Model: "claude-sonnet-5-5",
		SessionTokenLimit: 130000, Harness: HarnessClaudeCode}
	fake := FakeAgent{Role: RoleReview, Round: 2, Goal: "the goal", Issue: 7}
	tests := []struct {
		name    string
		harness string
		vars    map[string]string
		self    string
		want    Agent
	}{
		{"Claude Code, guarded by theos-agent itself", HarnessClaudeCode, nil, "/usr/local/bin/theos-agent",
			ClaudeAgent{Model: "claude-sonnet-5-5", SessionIDs: true, SessionTokenLimit: 130000,
				Guard: "/usr/local/bin/theos-agent"}},
		{"Claude Code when theos-agent can't find itself: unguarded", HarnessClaudeCode, nil, "",
			ClaudeAgent{Model: "claude-sonnet-5-5", SessionIDs: true}},
		{"a command of the user's: one session, no IDs", HarnessClaudeCode,
			map[string]string{EnvAgentCommand: "my-agent  --flag"}, "/usr/local/bin/theos-agent",
			ClaudeAgent{Model: "claude-sonnet-5-5", Command: []string{"my-agent", "--flag"}}},
		{"no harness named: the default", "", nil, "",
			ClaudeAgent{Model: "claude-sonnet-5-5", SessionIDs: true}},
		{"the stand-in", HarnessStandIn, nil, "/usr/local/bin/theos-agent", fake},
		{"the stand-in's scripted review", HarnessStandIn,
			map[string]string{EnvAgentFakeChangesFirst: "true", EnvAgentFakeFollowUp: "1"}, "",
			FakeAgent{Role: RoleReview, Round: 2, Goal: "the goal", Issue: 7, ChangesFirst: true, FollowUp: true}},
		{"THEOS_AGENT_FAKE on any harness: the stand-in", HarnessClaudeCode,
			map[string]string{EnvAgentFake: "1"}, "/usr/local/bin/theos-agent", fake},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := job
			job.Harness = tt.harness
			got := AgentFor(job, env(tt.vars), tt.self)
			if fmt.Sprintf("%#v", got) != fmt.Sprintf("%#v", tt.want) {
				t.Errorf("AgentFor() = %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

func TestJobFromEnvRefusesAHarnessThatIsNone(t *testing.T) {
	vars := map[string]string{EnvRole: RoleImplement, EnvRepoURL: "http://git/o/r.git", EnvBranch: "task/1",
		EnvGoal: "g", EnvHarness: "copilot"}
	_, err := JobFromEnv(func(name string) string { return vars[name] })
	want := `THEOS_HARNESS="copilot": want ` + oneOf(Harnesses())
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("JobFromEnv() error = %v, want %q", err, want)
	}
	if oneOf([]string{"a"}) != "a" || oneOf([]string{"a", "b"}) != "a or b" || oneOf([]string{"a", "b", "c"}) != "a, b or c" {
		t.Error("oneOf() doesn't list one, two and three names as a, a or b, and a, b or c")
	}
}
