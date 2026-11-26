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

package harnesstest_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/harness"
	"github.com/JairJosafath/theos/internal/harness/claudecode"
	"github.com/JairJosafath/theos/internal/harness/codex"
	"github.com/JairJosafath/theos/internal/harness/harnesstest"
	"github.com/JairJosafath/theos/internal/method"
	"github.com/JairJosafath/theos/internal/model"
)

// #338: both harnesses built conform.
func TestClaudeCode338(t *testing.T) { harnesstest.Run(t, claudecode.Harness{}) }

func TestCodex338(t *testing.T) { harnesstest.Run(t, codex.Harness{}) }

// loose is Claude Code with the reviewer's boundary dropped: it gives the
// reviewer Claude Code's implementer tools, or drops its boundaries.
type loose struct {
	claudecode.Harness
	grants   bool
	dropText bool
}

func (l loose) Grants(role string) harness.Grants {
	if l.grants {
		return l.Harness.Grants(model.RoleImplementer)
	}
	return l.Harness.Grants(role)
}

func (l loose) Render(p method.Project) ([]harness.File, error) {
	files, err := l.Harness.Render(p)
	if !l.dropText {
		return files, err
	}
	for i, f := range files {
		files[i].Data = bytes.ReplaceAll(f.Data, []byte(method.Boundaries(model.RoleReviewer)), []byte("Review it."))
	}
	return files, err
}

// #338: a harness that drops the reviewer's boundary fails the suite, by its
// grants or by its text.
func TestDroppedBoundaryFails338(t *testing.T) {
	for name, h := range map[string]loose{"the reviewer writes": {grants: true},
		"the reviewer's boundaries are gone": {dropText: true}} {
		problems := harnesstest.Check(h)
		if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "reviewer") {
			t.Errorf("%s: problems %q, want the reviewer's boundary named", name, problems)
		}
	}
}

// #338: Codex's files, as its documentation says it reads them.
func TestCodexFiles338(t *testing.T) {
	files, err := codex.Harness{}.Render(harnesstest.Project())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = string(f.Data)
	}
	if !strings.Contains(got[".codex/config.toml"], "[mcp_servers.theos]\ncommand = \"theos\"\nargs = [\"mcp\"]\n") {
		t.Errorf(".codex/config.toml = %q", got[".codex/config.toml"])
	}
	critic := got[".codex/agents/theos-critic.toml"]
	for _, want := range []string{`name = "theos-critic"`, `model = "model-for-reviewing"`,
		"developer_instructions = '''\n" + method.Boundaries(model.RoleReviewer) + "'''\n"} {
		if !strings.Contains(critic, want) {
			t.Errorf("the reviewer's agent file lacks %q:\n%s", want, critic)
		}
	}
	if _, ok := any(codex.Harness{}).(harness.RoleGrants); ok {
		t.Error("Codex sets no tools per agent; it must not claim to")
	}
}

// #337: a harness that lets the triager or the verifier change files fails
// the suite: they change nothing on main.
func TestTriagerAndVerifierChangeNothing337(t *testing.T) {
	problems := strings.Join(harnesstest.Check(loose{grants: true}), "\n")
	for _, role := range []string{"triager", "verifier"} {
		if !strings.Contains(problems, "the "+role+" may start agents") {
			t.Errorf("problems %q, want the %s's boundary named", problems, role)
		}
	}
}
