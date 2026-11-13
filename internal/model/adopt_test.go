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
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// adoptRepo is a repository as the detector's tests write it: its files by
// path, with the contents of those it reads.
type adoptRepo map[string]string

func (r adoptRepo) detect() RepoFinding {
	paths := slices.Sorted(maps.Keys(r))
	read := map[string]string{}
	for _, p := range AdoptionReads(paths) {
		read[p] = r[p]
	}
	return DetectRepository(paths, read)
}

// adoptOwnWorkflows is where the host's own CI system looks first.
var adoptOwnWorkflows = WorkflowDirs()[0]

const (
	adoptCIWorkflow = "name: ci\non:\n  push:\n    branches: [main]\n  pull_request:\njobs:\n" +
		"  test:\n    runs-on: ubuntu-latest\n    steps: []\n  lint:\n    name: Lint\n    runs-on: ubuntu-latest\n"
	adoptReleaseWorkflow = "name: release\non:\n  push:\n    tags: ['v*']\njobs:\n  build:\n    runs-on: ubuntu-latest\n"
)

// The repositories of #96's done-when: a Go module, a Node/TypeScript
// package and a Python project, each with and without a Makefile and with a
// workflow, and one the detector can tell nothing about.
var adoptRepos = map[string]adoptRepo{
	"go-service": {
		"go.mod": "module example.com/orders\n", "main.go": "package main\n", "Dockerfile": "FROM scratch\n",
		"Makefile":                 "build:\n\tgo build ./...\n\ncheck: lint\n\tgo test ./...\n",
		".github/workflows/ci.yml": adoptCIWorkflow, ".github/workflows/release.yml": adoptReleaseWorkflow,
	},
	"go-cli": {
		"go.mod": "module example.com/orders\n", "cmd/orders/main.go": "package main\n",
		"Makefile":                     "build:\n\tgo build ./...\n",
		adoptOwnWorkflows + "test.yml": "name: test\non: [push, pull_request]\njobs:\n  unit:\n    runs-on: go\n",
		// A host that has its own workflows reads no others.
		".github/workflows/ci.yml": adoptCIWorkflow,
	},
	"typescript-site": {
		"package.json":  `{"name": "shop", "scripts": {"test": "vitest run"}, "devDependencies": {"vite": "^7"}}`,
		"tsconfig.json": "{}", "src/main.ts": "",
		".github/workflows/ci.yaml": "name: CI\non: pull_request\njobs:\n  build:\n    name: Build ${{ matrix.node }}\n",
	},
	"node-service": {
		"package.json": `{"name": "api", "scripts": {"start": "node ."}}`, "Dockerfile": "FROM node\n",
		"Makefile": "check:\n\tnpm test\n", ".github/workflows/ci.yml": adoptCIWorkflow,
	},
	"python-cli": {
		"pyproject.toml": "[project]\nname = \"orders\"\n\n[project.scripts]\norders = \"orders:main\"\n\n" +
			"[tool.pytest.ini_options]\n",
		"orders/__init__.py":        "",
		".github/workflows/ci.yml":  adoptCIWorkflow,
		".github/workflows/bad.yml": "name: [",
	},
	"python-service": {
		"requirements.txt": "fastapi\n", "Dockerfile": "FROM python\n", "tests/test_app.py": "",
		"Makefile":                   "check:\n\tpytest\n",
		".github/workflows/main.yml": "on: [pull_request]\njobs:\n  test: {}\n",
	},
	"go-released-only": {"go.mod": "", "main.go": "", ".github/workflows/release.yml": adoptReleaseWorkflow},
	"unknown":          {"README.md": "# Something\n", "run.sh": "#!/bin/sh\n"},
}

func TestAdoptionReads(t *testing.T) {
	paths := []string{"README.md", "Makefile", "docs/Makefile", "go.mod", "sub/go.mod", "package.json",
		".github/workflows/ci.yml", ".github/workflows/nested/x.yml", ".github/dependabot.yml",
		adoptOwnWorkflows + "check.yaml", adoptOwnWorkflows + "notes.md"}
	want := []string{adoptOwnWorkflows + "check.yaml", ".github/workflows/ci.yml", "Makefile", "go.mod", "package.json"}
	if got := AdoptionReads(paths); !slices.Equal(got, want) {
		t.Errorf("AdoptionReads() = %v, want %v", got, want)
	}
}

func TestDetectRepository(t *testing.T) {
	tests := []struct {
		repo     string
		language string
		typ      string
		check    string
		noCheck  bool
		contexts []string
		notes    []string
	}{
		{repo: "go-service", language: LanguageGo, typ: TypeService, contexts: []string{"ci / Lint*", "ci / test*"}},
		{repo: "go-cli", language: LanguageGo, typ: TypeCLI, check: "go test ./...", contexts: []string{"test / unit*"}},
		{repo: "typescript-site", language: LanguageTypeScript, typ: TypeStaticSite, check: "npm test",
			contexts: []string{"CI / build*"}},
		{repo: "node-service", language: LanguageJavaScript, typ: TypeService, contexts: []string{"ci / Lint*", "ci / test*"}},
		{repo: "python-cli", language: LanguagePython, typ: TypeCLI, check: "pytest",
			contexts: []string{"ci / Lint*", "ci / test*"},
			notes:    []string{".github/workflows/bad.yml could not be read as YAML"}},
		{repo: "python-service", language: LanguagePython, typ: TypeService,
			notes: []string{".github/workflows/main.yml has no name"}},
		{repo: "go-released-only", language: LanguageGo, typ: TypeCLI, check: "go test ./...",
			notes: []string{"None of the workflows under .github/workflows/ runs on a pull request"}},
		{repo: "unknown", noCheck: true, notes: []string{"the language is not told", "Nothing says what kind of application",
			"No check was found", "It has no workflow under"}},
	}
	for _, tt := range tests {
		t.Run(tt.repo, func(t *testing.T) {
			got := adoptRepos[tt.repo].detect()
			if got.Language != tt.language || got.Type != tt.typ || got.CheckCommand != tt.check ||
				got.NoCheck != tt.noCheck || !slices.Equal(got.CheckContexts, tt.contexts) {
				t.Errorf("DetectRepository() = %+v, want %s %s, check %q (none: %v), contexts %v", got, tt.language,
					tt.typ, tt.check, tt.noCheck, tt.contexts)
			}
			all := strings.Join(got.Notes, "\n")
			for _, want := range tt.notes {
				if !strings.Contains(all, want) {
					t.Errorf("notes = %q, want %q among them", got.Notes, want)
				}
			}
			if len(tt.notes) == 0 && len(got.Notes) > 0 {
				t.Errorf("notes = %q, want none", got.Notes)
			}
		})
	}
}

func TestDetectRepositoryEdges(t *testing.T) {
	t.Run("two languages: the first decides, the other is said", func(t *testing.T) {
		got := adoptRepo{"go.mod": "", "main.go": "", "package.json": "{}", "Makefile": "check:\n"}.detect()
		if got.Language != LanguageGo || !strings.Contains(strings.Join(got.Notes, "\n"), "It also has JavaScript") {
			t.Errorf("DetectRepository() = %+v, want Go with a note of JavaScript", got)
		}
	})
	t.Run("more checks than a Container names", func(t *testing.T) {
		wf := "name: ci\non: pull_request\njobs:\n"
		for job := range strings.FieldsSeq("a b c d e f g h i j") {
			wf += "  " + job + ": {}\n"
		}
		got := adoptRepo{"go.mod": "", ".github/workflows/ci.yml": wf}.detect()
		if len(got.CheckContexts) != 8 || !strings.Contains(strings.Join(got.Notes, "\n"), "report 10 checks") {
			t.Errorf("DetectRepository() = %+v, want 8 contexts and a note of the 10", got)
		}
	})
	t.Run("a Makefile without a check target is no check", func(t *testing.T) {
		got := adoptRepo{"Makefile": "checks:\n\ttrue\nprecheck:\n\ttrue\n"}.detect()
		if !got.NoCheck {
			t.Errorf("DetectRepository() = %+v, want no check", got)
		}
	})
}

// The proposals for the repositories above are the files under
// config/samples/adopt/proposed, which make validate-yaml holds to the CRDs
// and their create-time rules. THEOS_UPDATE_PROPOSALS=1 writes them.
func TestAdoptionProposal(t *testing.T) {
	dir := filepath.Join("..", "..", "config", "samples", "adopt", "proposed")
	for _, name := range slices.Sorted(maps.Keys(adoptRepos)) {
		t.Run(name, func(t *testing.T) {
			manifests, identity := AdoptionProposal(AdoptionInput{Project: "billing", System: "billing-core",
				Container: name, GitProvider: "git"}, adoptRepos[name].detect())
			file := filepath.Join(dir, name+".yaml")
			if os.Getenv("THEOS_UPDATE_PROPOSALS") != "" {
				if err := os.WriteFile(file, []byte(manifests), 0o644); err != nil { //nolint:gosec // a sample, read by all
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if manifests != string(want) {
				t.Errorf("AdoptionProposal() differs from %s; THEOS_UPDATE_PROPOSALS=1 writes it:\n%s", file, manifests)
			}
			if !strings.HasPrefix(identity, "project: billing\nsoftwareSystem: billing-core\ncontainer: "+name+"\ntype: ") ||
				!strings.HasSuffix(identity, "\nscaffoldedFrom: []\n") {
				t.Errorf("identity = %q, want the project, the system, the Container and its type", identity)
			}
		})
	}
}
