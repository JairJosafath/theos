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

// Package codex is OpenAI's Codex as a harness of the lead runtime at
// level 1 (docs/harness-authoring.md, #338): the lead's playbook in
// AGENTS.md, which Codex reads from the project root; a custom agent in
// .codex/agents/theos-<agent>.toml for each Agent a Crew names, on its
// Agent's model, its role's boundaries its developer instructions; and
// theos mcp in .codex/config.toml, which Codex loads for a trusted project.
// Written from Codex's documentation of 2026-11 (learn.chatgpt.com/docs:
// agents-md, subagents, extend/mcp, config-reference).
//
// Codex sets no tools per custom agent, and an agent file can't set its
// sandbox: a role's boundary is its instructions alone, so this harness has
// no RoleGrants.
package codex

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/JairJosafath/theos/internal/harness"
	"github.com/JairJosafath/theos/internal/method"
)

// Harness is Codex.
type Harness struct{}

// Name is the harness's name, as theos agents -harness takes it.
const Name = "codex"

var (
	_ harness.Harness   = Harness{}
	_ harness.LeadFiles = Harness{}
)

// Name is "codex".
func (Harness) Name() string { return Name }

// Capabilities: custom agents, at once, each on its own model, with MCP.
func (Harness) Capabilities() harness.Capabilities {
	return harness.Capabilities{Subagents: true, Parallel: true, ModelPerRole: true, MCP: true}
}

// The files Render writes.
const (
	FileAgents = "AGENTS.md"
	FileConfig = ".codex/config.toml"
	agentsDir  = ".codex/agents/"
)

// Render is the lead's files for p in Codex's formats. An Agent two Crews
// give two roles is refused, as one agent file has one role's boundaries.
func (Harness) Render(p method.Project) ([]harness.File, error) {
	files := []harness.File{
		{Path: FileAgents, Data: []byte("<!-- " + harness.Generated + " -->\n\n" + method.Playbook(p))},
		{Path: FileConfig, Data: []byte("# " + harness.Generated + "\n\n[mcp_servers.theos]\ncommand = \"theos\"\n" +
			"args = [\"mcp\"]\n")},
	}
	roles := map[string]method.Agent{}
	for _, c := range p.Crews {
		for _, a := range c.Agents {
			if was, ok := roles[a.Name]; ok && was.Role != a.Role {
				return nil, fmt.Errorf("the Agent %s plays the %s in one Crew and the %s in another: its agent file "+
					"has one role's boundaries; give each role an Agent of its own", a.Name, was.Role, a.Role)
			}
			roles[a.Name] = a
		}
	}
	for _, name := range slices.Sorted(maps.Keys(roles)) {
		f, err := agentFile(roles[name])
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

// agentFile is a's custom agent: its name, when to use it, its model, and
// its role's boundaries as its developer instructions.
func agentFile(a method.Agent) (harness.File, error) {
	instructions := method.Boundaries(a.Role)
	if strings.Contains(instructions, "'''") {
		return harness.File{}, fmt.Errorf("the Agent %s: its instructions can't be a TOML literal string", a.Name)
	}
	name := method.SubagentName(a.Name)
	description := fmt.Sprintf("theos's %s: takes one work order of theos's lead, word for word.", a.Role)
	if a.Description != "" {
		description = fmt.Sprintf("theos's %s, Agent %s: %s Takes one work order of theos's lead, word for word.",
			a.Role, a.Name, strings.TrimSpace(a.Description))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", harness.Generated)
	fmt.Fprintf(&b, "name = %s\ndescription = %s\n", strconv.Quote(name), strconv.Quote(description))
	if a.Model != "" {
		fmt.Fprintf(&b, "model = %s\n", strconv.Quote(a.Model))
	}
	fmt.Fprintf(&b, "developer_instructions = '''\n%s'''\n", instructions)
	return harness.File{Path: agentsDir + name + ".toml", Data: []byte(b.String())}, nil
}
