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

// Package claudecode is Claude Code as a harness of the lead runtime
// (docs/plans/lead-runtime.md, decision 7): the main session is the lead,
// each Agent a Crew names is a subagent, and theos's tools come from
// theos mcp over stdio.
package claudecode

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/JairJosafath/theos/internal/harness"
	"github.com/JairJosafath/theos/internal/method"
	"github.com/JairJosafath/theos/internal/model"
)

// Harness is Claude Code.
type Harness struct{}

var (
	_ harness.Harness    = Harness{}
	_ harness.LeadFiles  = Harness{}
	_ harness.RoleGrants = Harness{}
)

// Grants are the role's tools: Agent starts a subagent; Edit and Write
// change files; Bash runs commands.
func (Harness) Grants(role string) harness.Grants {
	tools := roleTools[role]
	return harness.Grants{Spawn: slices.Contains(tools, toolAgent),
		Write: slices.Contains(tools, toolEdit) || slices.Contains(tools, toolWrite), Shell: slices.Contains(tools, toolBash)}
}

// Name is Agent.spec.harness for Claude Code.
func (Harness) Name() string { return model.HarnessClaudeCode }

// Capabilities: subagents, at once, each on its Agent's model, with MCP.
func (Harness) Capabilities() harness.Capabilities {
	return harness.Capabilities{Subagents: true, Parallel: true, ModelPerRole: true, MCP: true}
}

// The files Render writes, by path in the project repository.
const (
	FileClaude   = "CLAUDE.md"
	FileAgents   = "AGENTS.md"
	FileMCP      = ".mcp.json"
	agentsDir    = ".claude/agents/"
	mcpServer    = "theos"
	theosTools   = "mcp__" + mcpServer + "__*"
	agentsImport = "@" + FileAgents
)

// roleTools are the tools a role's subagent has. None has Agent, so none
// starts another subagent. The reviewer and the verifier have no Edit or
// Write: a verdict changes nothing, and a verification runs the build
// (Bash) and pushes nothing. The triager has no Bash either: it reads, and
// writes no file (#337).
var roleTools = map[string][]string{
	model.RoleImplementer: {toolRead, toolGrep, toolGlob, toolEdit, toolWrite, toolBash, theosTools},
	method.RoleSpecWriter: {toolRead, toolGrep, toolGlob, toolEdit, toolWrite, toolBash, theosTools},
	model.RoleReviewer:    {toolRead, toolGrep, toolGlob, toolBash, theosTools},
	model.RoleVerifier:    {toolRead, toolGrep, toolGlob, toolBash, theosTools},
	model.RoleTriager:     {toolRead, toolGrep, toolGlob, theosTools},
}

// Claude Code's tools: those that read, and those Grants reads.
const (
	toolRead  = "Read"
	toolGrep  = "Grep"
	toolGlob  = "Glob"
	toolAgent = "Agent"
	toolEdit  = "Edit"
	toolWrite = "Write"
	toolBash  = "Bash"
)

// Render is the lead's files for p: CLAUDE.md importing AGENTS.md, the
// playbook in AGENTS.md, a subagent for each Agent a Crew names, and
// .mcp.json starting theos mcp. An Agent two Crews give two roles is
// refused: its one subagent can't have both roles' tools. .mcp.json is
// JSON, which takes no comment: it is the one file without Generated.
func (Harness) Render(p method.Project) ([]harness.File, error) {
	comment := "<!-- " + harness.Generated + " -->\n"
	files := []harness.File{
		{Path: FileClaude, Data: []byte(comment + agentsImport + "\n")},
		{Path: FileAgents, Data: []byte(comment + "\n" + method.Playbook(p))},
	}
	agents, err := harness.AgentRoles(p)
	if err != nil {
		return nil, err
	}
	for _, r := range agents {
		f, err := subagent(r)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	mcp, err := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{
		mcpServer: map[string]any{"command": "theos", "args": []string{"mcp"}}}}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(files, harness.File{Path: FileMCP, Data: append(mcp, '\n')}), nil
}

// subagent is r's subagent file: its name, when to use it, its model and
// its roles' tools, then each role's boundaries.
func subagent(r harness.Roles) (harness.File, error) {
	a := r.Agent
	tools, ok := roleTools[r.Roles[0]]
	if !ok {
		return harness.File{}, fmt.Errorf("the Agent %s: the role %s has no subagent in this runtime", a.Name,
			r.Roles[0])
	}
	name := method.SubagentName(a.Name)
	role := strings.Join(r.Roles, " and ")
	description := fmt.Sprintf("theos's %s: takes one work order of theos's lead, word for word.", role)
	if a.Description != "" {
		description = fmt.Sprintf("theos's %s, Agent %s: %s Takes one work order of theos's lead, word for word.",
			role, a.Name, strings.TrimSpace(a.Description))
	}
	desc, err := json.Marshal(description) // a YAML double-quoted scalar
	if err != nil {
		return harness.File{}, err
	}
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "# %s\n", harness.Generated)
	fmt.Fprintf(&b, "name: %s\ndescription: %s\n", name, desc)
	if a.Model != "" {
		fmt.Fprintf(&b, "model: %s\n", a.Model)
	}
	fmt.Fprintf(&b, "tools: %s\n", strings.Join(tools, ", "))
	b.WriteString("---\n\n")
	b.WriteString(r.Boundaries())
	return harness.File{Path: agentsDir + name + ".md", Data: []byte(b.String())}, nil
}
