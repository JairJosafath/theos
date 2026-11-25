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

// Package harnesstest is the conformance suite of a harness
// (docs/harness-authoring.md, #338): it checks the parts a harness
// implements, and the role boundaries every rendering must keep.
package harnesstest

import (
	"bytes"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/harness"
	"github.com/JairJosafath/theos/internal/method"
	"github.com/JairJosafath/theos/internal/model"
)

// Project is the project the suite renders: one Crew with an implementer
// and a reviewer, each on a model of its own.
func Project() method.Project {
	return method.Project{Name: "shop", Host: "github", Crews: []method.Crew{{Name: "orders", Container: "orders-api",
		Parallel: 2, Agents: []method.Agent{
			{Name: "builder", Role: model.RoleImplementer, Model: "model-for-building", Description: "Builds."},
			{Name: "critic", Role: model.RoleReviewer, Model: "model-for-reviewing"},
		}}}}
}

// Run runs the suite on h and fails t with each problem Check finds.
func Run(t *testing.T, h harness.Harness) {
	t.Helper()
	for _, p := range Check(h) {
		t.Error(p)
	}
}

// Check is the suite's problems with h, none for a harness that conforms:
// a name; and, for LeadFiles, files at local paths, each once, the same at
// every render, the playbook whole in one of them, each Agent's boundaries
// whole in one that names its subagent (and its model, where the harness
// runs each role on its own), theos mcp named where the harness speaks
// MCP, and the generated marker in each file but JSON. Where the harness
// enforces each role's tools (RoleGrants), the reviewer neither starts
// agents nor writes, and the implementer writes.
func Check(h harness.Harness) []string {
	c := &checker{}
	if h.Name() == "" || strings.ContainsAny(h.Name(), " \t\n") {
		c.fail("its name %q is no Agent.spec.harness word", h.Name())
	}
	if g, ok := h.(harness.RoleGrants); ok {
		c.grants(g)
	}
	lead, ok := h.(harness.LeadFiles)
	if !ok {
		return c.problems
	}
	p := Project()
	files, err := lead.Render(p)
	if err != nil {
		c.fail("Render: %v", err)
		return c.problems
	}
	again, err := lead.Render(p)
	if err != nil || len(again) != len(files) {
		c.fail("a second render differs: %v", err)
		again = nil
	}
	c.files(files, again)
	c.roles(p, files, h.Capabilities())
	if h.Capabilities().MCP && !slices.ContainsFunc(files, func(f harness.File) bool {
		return bytes.Contains(f.Data, []byte(`"theos"`)) && bytes.Contains(f.Data, []byte(`"mcp"`))
	}) {
		c.fail("no file starts theos mcp, though the harness speaks MCP")
	}
	return c.problems
}

// checker gathers a harness's problems.
type checker struct{ problems []string }

func (c *checker) fail(format string, args ...any) {
	c.problems = append(c.problems, fmt.Sprintf(format, args...))
}

// grants checks the roles' enforced tools.
func (c *checker) grants(g harness.RoleGrants) {
	if r := g.Grants(model.RoleReviewer); r.Spawn || r.Write {
		c.fail("the reviewer may start agents (%v) or write (%v): its verdict is its own, and it changes nothing",
			r.Spawn, r.Write)
	}
	if i := g.Grants(model.RoleImplementer); !i.Write {
		c.fail("the implementer can't write")
	}
}

// files checks the files' paths, their marker and that a second render,
// again (nil when it failed), is the same.
func (c *checker) files(files, again []harness.File) {
	seen := map[string]bool{}
	for i, f := range files {
		if f.Path == "" || path.IsAbs(f.Path) || !filepath.IsLocal(f.Path) || path.Clean(f.Path) != f.Path {
			c.fail("%q is no local path of the project repository", f.Path)
		}
		if seen[f.Path] {
			c.fail("%s is rendered twice", f.Path)
		}
		seen[f.Path] = true
		if again != nil && (again[i].Path != f.Path || !bytes.Equal(again[i].Data, f.Data)) {
			c.fail("a second render of %s differs", f.Path)
		}
		if path.Ext(f.Path) != ".json" && !bytes.Contains(f.Data, []byte(harness.Generated)) {
			c.fail("%s carries no generated marker", f.Path)
		}
	}
}

// roles checks the playbook and each Agent's boundaries in the files.
func (c *checker) roles(p method.Project, files []harness.File, caps harness.Capabilities) {
	holding := func(text string) []harness.File {
		var out []harness.File
		for _, f := range files {
			if bytes.Contains(f.Data, []byte(text)) {
				out = append(out, f)
			}
		}
		return out
	}
	if len(holding(method.Playbook(p))) == 0 {
		c.fail("no file holds the lead's playbook whole")
	}
	word := map[string]string{model.RoleImplementer: "implementer", model.RoleReviewer: "reviewer"}
	for _, a := range p.Crews[0].Agents {
		boundaries := method.Boundaries(a.Role)
		for _, want := range []string{"AGENTS.md", method.CommandHeartbeat} {
			if !strings.Contains(boundaries, want) {
				c.fail("the %s's boundaries don't name %s", word[a.Role], want)
			}
		}
		found := slices.ContainsFunc(holding(boundaries), func(f harness.File) bool {
			return bytes.Contains(f.Data, []byte(method.SubagentName(a.Name))) &&
				(!caps.ModelPerRole || bytes.Contains(f.Data, []byte(a.Model)))
		})
		if !found {
			on := ""
			if caps.ModelPerRole {
				on = " on its model"
			}
			c.fail("no file holds the %s's boundaries whole for %s%s", word[a.Role], method.SubagentName(a.Name), on)
		}
	}
}
