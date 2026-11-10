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

package model

import (
	"slices"
	"strings"
	"testing"
)

// giteaActions is the Gitea Actions row, the CI system the rules' tests use.
var giteaActions = mustCI(CIGiteaActions)

func mustCI(name string) CI {
	ci, err := CIByName(name)
	if err != nil {
		panic(err)
	}
	return ci
}

func TestHostCI(t *testing.T) {
	tests := []struct {
		host                string
		wantName            string
		wantContexts, paths []string
		wantErr             bool
	}{
		{host: "gitea", wantName: CIGiteaActions, wantContexts: []string{"check*"},
			paths: []string{".devcontainer/", ".gitea/", ".theos.yaml"}},
		// #78: GitHub's own CI system is GitHub Actions.
		{host: "github", wantName: CIGitHubActions, wantContexts: []string{"check*"},
			paths: []string{".devcontainer/", ".github/", ".theos.yaml"}},
		{host: "gitlab", wantErr: true},
		{host: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			ci, err := HostCI(tt.host)
			if (err != nil) != tt.wantErr {
				t.Fatalf("HostCI(%q) error = %v, want error %v", tt.host, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if ci.Name != tt.wantName || !slices.Equal(ci.CheckContexts, tt.wantContexts) {
				t.Errorf("HostCI(%q) = %+v, want %s with contexts %v", tt.host, ci, tt.wantName, tt.wantContexts)
			}
			if got := PlatformProtectedPaths(ci); !slices.Equal(got, tt.paths) {
				t.Errorf("PlatformProtectedPaths(%s) = %v, want %v", ci.Name, got, tt.paths)
			}
		})
	}
}

// #78: CINames lists every CI system of the table, sorted, each by the name
// CIByName finds it under.
func TestCINames(t *testing.T) {
	names := CINames()
	if len(names) != len(ciSystems) || !slices.IsSorted(names) {
		t.Fatalf("CINames() = %v, want the %d systems of the table, sorted", names, len(ciSystems))
	}
	for _, name := range names {
		if ci, err := CIByName(name); err != nil || ci.Name != name {
			t.Errorf("CIByName(%q) = %+v, %v", name, ci, err)
		}
	}
}

func TestCIByNameUnknown(t *testing.T) {
	if _, err := CIByName("jenkins"); err == nil {
		t.Error("CIByName(jenkins) = nil error, want unknown")
	}
}

// TestCIRulesTakeTheCI shows the merge rules read the check contexts and the
// protected paths from the CI system they are given, not from Gitea Actions.
func TestCIRulesTakeTheCI(t *testing.T) {
	other := CI{Name: "other", CheckContexts: []string{"build"}, ProtectedPaths: []string{".other-ci.yml"}}
	passing := map[string]string{"build": "success"}
	giteaPassing := map[string]string{"check / check (pull_request)": "success"}
	tests := []struct {
		name     string
		ci       CI
		contexts map[string]string
		want     string
	}{
		{"gitea actions passes its own context", giteaActions, giteaPassing, ""},
		{"gitea actions refuses another's", giteaActions, passing, `no passing check matches "check*", which main requires`},
		{"another CI passes its own context", other, passing, ""},
		{"another CI refuses gitea actions'", other, giteaPassing, `no passing check matches "build", which main requires`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StandardUpdateRefusal(tt.ci, checkSuccess, tt.contexts, []string{"Makefile"},
				[]string{"Makefile"}); got != tt.want {
				t.Errorf("StandardUpdateRefusal() = %q, want %q", got, tt.want)
			}
			if got := GatedMergeRefusal(tt.ci, checkSuccess, tt.contexts, VerdictApprove, []string{"Makefile"},
				[]string{"Makefile"}, AgentProtectedPaths(tt.ci, []string{"Makefile"})); got != tt.want {
				t.Errorf("GatedMergeRefusal() = %q, want %q", got, tt.want)
			}
		})
	}
	if got := GatedMergeRefusal(CI{}, checkSuccess, giteaPassing, VerdictApprove, []string{"Makefile"},
		[]string{"Makefile"}, nil); got != "no CI system names the checks main requires" {
		t.Errorf("GatedMergeRefusal(no CI) = %q, want a refusal", got)
	}
	if got := FactoryOwnPaths(other); slices.Contains(got, ".gitea/") || !slices.Contains(got, ".other-ci.yml") {
		t.Errorf("FactoryOwnPaths(other) = %v, want .other-ci.yml and no .gitea/", got)
	}
}

// #78: GitHub Actions is a CI system of its own. A job reports as a check
// run under its own name, with no workflow or event in it, and everything
// under .github/ is the platform's: no agent changes a workflow, an issue
// template or the code owners there.
func TestGitHubActionsRow(t *testing.T) {
	ci := mustCI(CIGitHubActions)
	tests := []struct {
		name     string
		contexts map[string]string
		want     string
	}{
		{"the check job passed", map[string]string{"check": "success"}, ""},
		{"the check job and the full one passed", map[string]string{"check": "success", "check (full)": "success"}, ""},
		{"the check job failed", map[string]string{"check": "failure"},
			`no passing check matches "check*", which main requires`},
		{"only another job passed", map[string]string{"build": "success"},
			`no passing check matches "check*", which main requires`},
		{"nothing reported", nil, `no passing check matches "check*", which main requires`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GatedMergeRefusal(ci, checkSuccess, tt.contexts, VerdictApprove, []string{"Makefile"},
				[]string{"Makefile"}, AgentProtectedPaths(ci, []string{"Makefile"}))
			if got != tt.want {
				t.Errorf("GatedMergeRefusal() = %q, want %q", got, tt.want)
			}
		})
	}
	if got, want := AgentProtectedPaths(ci, []string{"Makefile"}), []string{".devcontainer/", ".github/", ".theos.yaml",
		"Makefile"}; !slices.Equal(got, want) {
		t.Errorf("AgentProtectedPaths(githubActions) = %v, want %v", got, want)
	}
	if got := FactoryOwnPaths(ci); !slices.Contains(got, ".github/") || len(got) != len(FactoryOwnPaths(CI{}))+1 {
		t.Errorf("FactoryOwnPaths(githubActions) = %v, want the factory's own and .github/ alone of a CI system", got)
	}
	// It reads workflows from one place, so an adopted repository's are
	// protected already.
	if got := ci.ForAdopted(nil); !slices.Equal(got.ProtectedPaths, []string{".github/"}) ||
		!slices.Equal(got.CheckContexts, []string{"check*"}) {
		t.Errorf("ForAdopted(nil) = %+v, want the row as it is", got)
	}
	// A Standard change for the Makefile may not carry a workflow with it.
	want := "the change touches .github/workflows/check.yaml, protected and outside the allowed paths"
	if got := GatedMergeRefusal(ci, checkSuccess, map[string]string{"check": "success"}, VerdictApprove,
		[]string{"Makefile", ".github/workflows/check.yaml"}, []string{"Makefile"},
		AgentProtectedPaths(ci, []string{"Makefile"})); got != want {
		t.Errorf("GatedMergeRefusal() of a change that carries a workflow = %q, want %q", got, want)
	}
}

// #95: a Container that adopts its repository with no Standard names its own
// check contexts, and its workflows may be where the CI system looks second.
func TestCIForAdopted(t *testing.T) {
	ci := CI{Name: "someActions", CheckContexts: []string{"check*"}, ProtectedPaths: []string{".ci/"},
		FallbackPaths: []string{".github/workflows/"}}
	own := ci.ForAdopted([]string{"ci / test*", "ci / lint*"})
	if !slices.Equal(own.CheckContexts, []string{"ci / test*", "ci / lint*"}) {
		t.Errorf("ForAdopted() contexts = %v, want the Container's", own.CheckContexts)
	}
	if got := ci.ForAdopted(nil).CheckContexts; !slices.Equal(got, []string{"check*"}) {
		t.Errorf("ForAdopted(nil) contexts = %v, want the CI system's", got)
	}
	want := []string{".ci/", ".devcontainer/", ".github/workflows/", ".theos.yaml"}
	if got := AgentProtectedPaths(own, nil); !slices.Equal(got, want) {
		t.Errorf("AgentProtectedPaths(adopted) = %v, want %v", got, want)
	}
	if got := AgentProtectedPaths(ci, nil); slices.Contains(got, ".github/workflows/") {
		t.Errorf("AgentProtectedPaths(scaffolded) = %v: a scaffolded repository's .github/ is its own", got)
	}
	if !slices.Equal(ci.CheckContexts, []string{"check*"}) || !slices.Equal(ci.ProtectedPaths, []string{".ci/"}) {
		t.Errorf("ForAdopted changed the CI system it came from: %+v", ci)
	}
	// Every CI system that reads workflows from a second place says so: an
	// adopted repository's may be there, in an agent's reach otherwise.
	for name, system := range ciSystems {
		if len(system.FallbackPaths) == 0 && !slices.Contains(system.ProtectedPaths, ".github/") {
			t.Errorf("CI system %s names no fallback paths: where else does its host read workflows from?", name)
		}
	}
}

func TestAdoptedChecks(t *testing.T) {
	tests := []struct {
		name     string
		required []string
		contexts map[string]string
		ok       bool
		message  string
	}{
		{"the default pattern, reported", []string{"check*"}, map[string]string{"check / check (push)": "success"},
			true, "main requires the checks matching check*, which its head reports (check / check (push))"},
		{"a failed check is still a check: protection is about what reports", []string{"check*"},
			map[string]string{"check / check (push)": "failure"}, true, ""},
		{"the Container's own pattern", []string{"ci / test*"},
			map[string]string{"ci / test (push)": "success", "ci / docs (push)": "success"}, true,
			"its head reports (ci / test (push))"},
		{"another name than the default", []string{"check*"},
			map[string]string{"ci / test (push)": "success", "ci / lint (push)": "pending"}, false,
			"main's head reports no check matching check*, so main isn't protected and no agent works on the " +
				"repository; it reports ci / lint (push) and ci / test (push): name the repository's own in " +
				"spec.checkContexts"},
		{"one of two patterns unreported", []string{"ci / test*", "ci / e2e*"},
			map[string]string{"ci / test (push)": "success"}, false, "no check matching ci / e2e*, so"},
		{"no check at all", []string{"check*"}, nil, false, "it reports no check at all: add a CI workflow"},
		{"a skipped check reports nothing", []string{"check*"}, map[string]string{"check / check (push)": "skipped"},
			false, "it reports no check at all"},
		{"many checks are told short", []string{"check*"}, map[string]string{"a": "success", "b": "success",
			"c": "success", "d": "success", "e": "success", "f": "success", "g": "success", "h": "success",
			"i": "success", "j": "success"}, false, "g and h and 2 more"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, message := AdoptedChecks(tt.required, tt.contexts)
			if ok != tt.ok || !strings.Contains(message, tt.message) {
				t.Errorf("AdoptedChecks() = %v, %q, want %v with %q", ok, message, tt.ok, tt.message)
			}
		})
	}
}

func TestProtected(t *testing.T) {
	for _, tt := range []struct {
		name                    string
		standard, adoptedChecks bool
		want                    bool
	}{
		{"a Standard brings the checks", true, false, true},
		{"an adopted repository with its own checks", false, true, true},
		{"an adopted repository whose main reports none", false, false, false},
		{"neither", false, false, false},
	} {
		if got := Protected(tt.standard, tt.adoptedChecks); got != tt.want {
			t.Errorf("%s: Protected() = %v, want %v", tt.name, got, tt.want)
		}
	}
}
