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
	"strconv"
	"strings"
	"time"
)

// Harnesses: the agent programs theos-agent can run, by the names
// Agent.spec.harness gives them.
const (
	HarnessClaudeCode = "claude-code"
	HarnessStandIn    = "stand-in"
)

// What theos-agent reads besides the job, to pick and set up its agent.
const (
	// EnvAgentFake, when set, runs the stand-in whatever the harness.
	EnvAgentFake = "THEOS_AGENT_FAKE"
	// EnvAgentFakeChangesFirst and EnvAgentFakeFollowUp script the stand-in's review.
	EnvAgentFakeChangesFirst = "THEOS_AGENT_FAKE_CHANGES_FIRST"
	EnvAgentFakeFollowUp     = "THEOS_AGENT_FAKE_FOLLOW_UP"
	// EnvAgentCommand replaces the harness's own command; its one session
	// runs to its end.
	EnvAgentCommand = "THEOS_AGENT_COMMAND"
)

// Harness is an agent program theos-agent can run: how its Agent is made for
// a job, and what its output says when its service can't go on. What an
// agent can do beyond one call is an interface it has or hasn't
// (UsageReporter, sessionKeeper, sessionResumer); the runner asks for the
// interface, never for the harness.
type Harness struct {
	// Name is the harness's value in Agent.spec.harness.
	Name string
	// Agent makes the harness's agent for a job. env reads the sandbox's
	// environment, and self is theos-agent's own program, "" when it
	// couldn't find itself.
	Agent func(job Job, env func(string) string, self string) Agent
	// Unavailable reports whether a failed call's output says the harness's
	// service, not the agent, can't go on, and when it is back, if it said.
	// Such a run costs no round. Nil: never.
	Unavailable func(output string, now time.Time) (bool, time.Time)
}

// harnesses is every harness, the default first. A new one is an entry
// here, a value in Agent.spec.harness's enum, and a case of the contract
// test (harness_test.go), which fails until all three agree.
var harnesses = []Harness{claudeCode, standIn}

// claudeCode is Claude Code, headless: the job's model, and with its own
// command a session ID per session, which a checkpoint records and a resume
// goes on with, and theos-agent itself as the guard of each session. With
// EnvAgentCommand it runs that command instead, whose one session runs to
// its end.
var claudeCode = Harness{
	Name: HarnessClaudeCode,
	Agent: func(job Job, env func(string) string, self string) Agent {
		agent := ClaudeAgent{Model: job.Model}
		if command := strings.Fields(env(EnvAgentCommand)); len(command) > 0 {
			agent.Command = command
			return agent
		}
		// Claude Code's own command takes a --session-id, which a checkpoint
		// records, and a --resume of a session the pause ended.
		agent.SessionIDs = true
		if self != "" {
			agent.SessionTokenLimit, agent.Guard = job.SessionTokenLimit, self
		}
		return agent
	},
	Unavailable: claudeUnavailable,
}

// standIn is the scripted agent of tests and demos, which calls no service.
var standIn = Harness{
	Name: HarnessStandIn,
	Agent: func(job Job, env func(string) string, _ string) Agent {
		changesFirst, _ := strconv.ParseBool(env(EnvAgentFakeChangesFirst))
		followUp, _ := strconv.ParseBool(env(EnvAgentFakeFollowUp))
		return FakeAgent{Role: job.Role, Round: job.Round, Goal: job.Goal, Issue: job.Issue,
			Criteria: job.Criteria, Spec: job.SpecPath, ChangesFirst: changesFirst, FollowUp: followUp}
	},
}

// Harnesses returns every harness's name, the default first.
func Harnesses() []string {
	names := make([]string, 0, len(harnesses))
	for _, h := range harnesses {
		names = append(names, h.Name)
	}
	return names
}

// harnessOf returns the harness of that name.
func harnessOf(name string) (Harness, bool) {
	for _, h := range harnesses {
		if h.Name == name {
			return h, true
		}
	}
	return Harness{}, false
}

// AgentFor picks the job's agent: its harness's, or the stand-in when
// EnvAgentFake is set, whatever the harness. JobFromEnv has refused a
// harness that isn't one, and named the default for none.
func AgentFor(job Job, env func(string) string, self string) Agent {
	if env(EnvAgentFake) != "" {
		return standIn.Agent(job, env, self)
	}
	h, ok := harnessOf(job.Harness)
	if !ok {
		h = harnesses[0]
	}
	return h.Agent(job, env, self)
}

// unavailable reports whether a failed call's output says the service of
// the job's harness is unavailable, and when it is back, if it said.
func unavailable(job Job, output string, now time.Time) (bool, time.Time) {
	h, ok := harnessOf(job.Harness)
	if !ok {
		// A job that names none runs on the default, as AgentFor has it.
		h = harnesses[0]
	}
	if h.Unavailable == nil {
		return false, time.Time{}
	}
	return h.Unavailable(output, now)
}

// sessionKeeper is an Agent whose sessions the run records, guards with the
// pause and checkpoints between their tool calls.
type sessionKeeper interface {
	keepingSessions(log *sessionLog, pause *pauseWatch, periodic *periodicCheckpoints) Agent
}

// sessionResumer is an Agent that can go on with a session a checkpoint
// recorded.
type sessionResumer interface {
	resumesSessions() bool
}

// oneOf lists names for a message: "a", "a or b", "a, b or c".
func oneOf(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}
