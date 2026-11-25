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

package claudecode_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/JairJosafath/theos/internal/harness"
	"github.com/JairJosafath/theos/internal/harness/claudecode"
	"github.com/JairJosafath/theos/internal/method"
	"github.com/JairJosafath/theos/internal/model"
)

func project() method.Project {
	return method.Project{Name: "shop", Host: "github", Crews: []method.Crew{{Name: "orders", Container: "orders-api",
		Agents: []method.Agent{
			{Name: "implementer", Role: model.RoleImplementer, Model: "claude-sonnet-5-5", Description: "Builds."},
			{Name: "reviewer", Role: model.RoleReviewer, Model: "claude-opus-5-5"},
		}}}}
}

func byPath(t *testing.T, files []harness.File) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, f := range files {
		out[f.Path] = string(f.Data)
	}
	return out
}

// frontmatter is a subagent file's YAML frontmatter.
type frontmatter struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Model       string `json:"model"`
	Tools       string `json:"tools"`
}

func readFrontmatter(t *testing.T, file string) frontmatter {
	t.Helper()
	rest, ok := strings.CutPrefix(file, "---\n")
	head, _, closed := strings.Cut(rest, "\n---\n")
	if !ok || !closed {
		t.Fatalf("no frontmatter in %q", file)
	}
	var f frontmatter
	if err := yaml.UnmarshalStrict([]byte(head), &f); err != nil {
		t.Fatalf("frontmatter %q: %v", head, err)
	}
	return f
}

// #266: for one Crew, CLAUDE.md imports AGENTS.md, the playbook, a subagent
// per Agent with its model and its role's tools (the reviewer's without
// Agent, Edit or Write), and .mcp.json starting theos mcp; each file but
// the JSON one marked generated.
func TestRender266(t *testing.T) {
	files, err := claudecode.Harness{}.Render(project())
	if err != nil {
		t.Fatal(err)
	}
	got := byPath(t, files)
	paths := make([]string, 0, len(got))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	want := []string{"CLAUDE.md", "AGENTS.md", ".claude/agents/theos-implementer.md", ".claude/agents/theos-reviewer.md",
		".mcp.json"}
	if !slices.Equal(paths, want) {
		t.Fatalf("files %v, want %v", paths, want)
	}
	if !strings.HasSuffix(got["CLAUDE.md"], "\n@AGENTS.md\n") {
		t.Errorf("CLAUDE.md = %q, want one line importing AGENTS.md", got["CLAUDE.md"])
	}
	if !strings.Contains(got["AGENTS.md"], method.Playbook(project())) {
		t.Error("AGENTS.md is not the playbook")
	}
	for _, p := range want[:4] {
		if !strings.Contains(got[p], harness.Generated) {
			t.Errorf("%s carries no generated marker", p)
		}
	}
	impl := readFrontmatter(t, got[want[2]])
	if impl.Name != "theos-implementer" || impl.Model != "claude-sonnet-5-5" || !strings.Contains(impl.Description, "Builds.") {
		t.Errorf("implementer's frontmatter %+v", impl)
	}
	rev := readFrontmatter(t, got[want[3]])
	if rev.Name != "theos-reviewer" || rev.Model != "claude-opus-5-5" {
		t.Errorf("reviewer's frontmatter %+v", rev)
	}
	tools := func(f frontmatter) []string { return strings.Split(f.Tools, ", ") }
	for _, forbidden := range []string{"Agent", "Edit", "Write"} {
		if slices.Contains(tools(rev), forbidden) {
			t.Errorf("the reviewer has %s: %s", forbidden, rev.Tools)
		}
	}
	if !slices.Contains(tools(impl), "Edit") || !slices.Contains(tools(impl), "mcp__theos__*") ||
		!slices.Contains(tools(rev), "mcp__theos__*") || slices.Contains(tools(impl), "Agent") {
		t.Errorf("tools: implementer %q, reviewer %q", impl.Tools, rev.Tools)
	}
	if !strings.Contains(got[want[2]], method.Boundaries(model.RoleImplementer)) ||
		!strings.Contains(got[want[3]], method.Boundaries(model.RoleReviewer)) {
		t.Error("a subagent's prompt is not its role's boundaries")
	}
	var mcp struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(got[".mcp.json"]), &mcp); err != nil {
		t.Fatal(err)
	}
	if s := mcp.MCPServers["theos"]; s.Command != "theos" || !slices.Equal(s.Args, []string{"mcp"}) {
		t.Errorf(".mcp.json = %s, want theos mcp over stdio", got[".mcp.json"])
	}
	// The same project renders the same files.
	again, _ := claudecode.Harness{}.Render(project())
	if !slices.EqualFunc(files, again, func(a, b harness.File) bool { return a.Path == b.Path && string(a.Data) == string(b.Data) }) {
		t.Error("a second render differs")
	}
}

// #266: an Agent two Crews give two roles is refused: its one subagent
// can't have both roles' tools.
func TestRenderRefusesAnAgentInTwoRoles266(t *testing.T) {
	p := project()
	p.Crews = append(p.Crews, method.Crew{Name: "web", Container: "storefront", Agents: []method.Agent{
		{Name: "reviewer", Role: model.RoleImplementer}, {Name: "implementer", Role: model.RoleReviewer}}})
	if _, err := (claudecode.Harness{}).Render(p); err == nil || !strings.Contains(err.Error(), "both roles' tools") {
		t.Errorf("err = %v, want the two roles refused", err)
	}
}
