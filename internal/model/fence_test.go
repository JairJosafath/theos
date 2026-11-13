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
	"os"
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestAgentProtectedPaths(t *testing.T) {
	got := AgentProtectedPaths(giteaActions, []string{"Makefile", ".gitea/", "docs/style/"})
	want := []string{".devcontainer/", ".gitea/", ".theos.yaml", "Makefile", "docs/style/"}
	if !slices.Equal(got, want) {
		t.Errorf("AgentProtectedPaths() = %v, want %v", got, want)
	}
	if got := AgentProtectedPaths(giteaActions, nil); !slices.Equal(got, PlatformProtectedPaths(giteaActions)) {
		t.Errorf("AgentProtectedPaths(nil) = %v, want the platform's", got)
	}
	other := CI{Name: "other", ProtectedPaths: []string{".other-ci.yml"}}
	want = []string{".devcontainer/", ".other-ci.yml", ".theos.yaml", "Makefile"}
	if got := AgentProtectedPaths(other, []string{"Makefile"}); !slices.Equal(got, want) {
		t.Errorf("AgentProtectedPaths(other) = %v, want %v", got, want)
	}
}

// A cluster DNS address block (#1257) is one inside the private ranges the
// fences keep HTTPS from; a public resolver or anywhere is not.
func TestPrivateBlock(t *testing.T) {
	for _, tt := range []struct {
		cidr string
		want bool
	}{
		{"169.254.20.10/32", true}, // NodeLocal DNSCache
		{"10.96.0.10/32", true},    // a kube-dns Service address
		{"172.20.0.0/16", true},
		{"10.0.0.0/8", true},
		{"8.8.8.8/32", false},
		{"0.0.0.0/0", false},
		{"10.0.0.0/7", false}, // wider than 10/8
		{"169.254.20.10", false},
		{"fd00::10/128", false},
		{"", false},
	} {
		if got := PrivateBlock(tt.cidr); got != tt.want {
			t.Errorf("PrivateBlock(%q) = %v, want %v", tt.cidr, got, tt.want)
		}
	}
}

func TestNeverPathNamed(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"Add a note to AGENTS.md", "AGENTS.md"},
		{"Add a note to AGENTS.md.", "AGENTS.md"},
		{"- [ ] `AGENTS.md` says to run make check", "AGENTS.md"},
		{"Write ./AGENTS.md", "./AGENTS.md"},
		{"Update AGENTS.md...", "AGENTS.md"},
		{"(see AGENTS.md..)", "AGENTS.md"},
		{"- [ ] `docs/agents/reviewer.md` says so", "docs/agents/reviewer.md"},
		{"Change docs/agents/reviewer.md.", "docs/agents/reviewer.md"},
		{"A new guide in docs/agents", "docs/agents"},
		{"Set the check in .theos.yaml", ".theos.yaml"},
		{"First AGENTS.md, then .theos.yaml", "AGENTS.md"},
		{"Then .theos.yaml and AGENTS.md", ".theos.yaml"},
		{"Write docs/project-notes.md", ""},
		{"Add pkg/AGENTS.md for the package", ""},
		{"Keep AGENTS.md.bak", ""},
		{"Keep MY_AGENTS.md", ""},
		{"Read mydocs/agents/x.md", ""},
		{"Read docs/agents-old/x.md", ""},
		{"Write a.theos.yaml", ""},
		{"Agents read the notes", ""},
	} {
		if got := NeverPathNamed(tc.text); got != tc.want {
			t.Errorf("NeverPathNamed(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestWithoutNeverPaths(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"Add CI note to `./AGENTS.md`", "Add CI note to a protected file"},
		{"Edit docs/agents/x.md and `.theos.yaml`.", "Edit a protected file and a protected file."},
		{"Write docs/project-notes.md", "Write docs/project-notes.md"},
	} {
		if got := withoutNeverPaths(tc.text); got != tc.want {
			t.Errorf("withoutNeverPaths(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

// TestFactorySelfProtectedPathsAreTheFence pins the chart catalog's factory-self
// Standard to the fence of docs/plans/self-hosting.md, decision 5: its
// protectedPaths are the table's rows, in the table's order (#1890).
func TestFactorySelfProtectedPathsAreTheFence(t *testing.T) {
	data, err := os.ReadFile(chartValues)
	if err != nil {
		t.Fatal(err)
	}
	var values struct {
		Catalog struct {
			Entries []struct {
				Kind string `yaml:"kind"`
				Name string `yaml:"name"`
				Spec struct {
					SupportedTypes      []string `yaml:"supportedTypes"`
					SupportedFrameworks []string `yaml:"supportedFrameworks"`
					ProtectedPaths      []string `yaml:"protectedPaths"`
				} `yaml:"spec"`
			} `yaml:"entries"`
		} `yaml:"catalog"`
	}
	if err := yaml.Unmarshal(data, &values); err != nil {
		t.Fatalf("%s: %v", chartValues, err)
	}
	want := []string{
		"internal/agentrunner/",
		"internal/modelproxy/",
		"internal/model/fence.go",
		"internal/controller/task_sandbox.go",
		"internal/controller/project_agent_network.go",
		"internal/controller/ci_runner_pod.go",
		"internal/controller/protection.go",
		"internal/adapters/gitea/gitea_agents.go",
		"internal/adapters/github/github_host_agents.go",
		"internal/adapters/github/github_host_rules.go",
		"dist/chart/",
		"Makefile",
		".golangci.yml",
		".custom-gcl.yml",
		".claude/",
		"CLAUDE.md",
		"GOVERNANCE.md",
		// Spelled apart: the test-packages test reads a string naming the scripts
		// directory in a Go test as a script the test runs (#1523); this one is
		// catalog data.
		"hack" + "/",
	}
	for _, e := range values.Catalog.Entries {
		if e.Kind != "Standard" || e.Name != "factory-self" {
			continue
		}
		if !slices.Equal(e.Spec.ProtectedPaths, want) {
			t.Errorf("factory-self protectedPaths = %v, want decision 5's table %v", e.Spec.ProtectedPaths, want)
		}
		if !slices.Equal(e.Spec.SupportedFrameworks, []string{"go-kubebuilder"}) {
			t.Errorf("factory-self supportedFrameworks = %v, want [go-kubebuilder]", e.Spec.SupportedFrameworks)
		}
		if !slices.Equal(e.Spec.SupportedTypes, []string{"operator"}) {
			t.Errorf("factory-self supportedTypes = %v, want [operator]", e.Spec.SupportedTypes)
		}
		return
	}
	t.Fatalf("%s has no Standard factory-self", chartValues)
}
