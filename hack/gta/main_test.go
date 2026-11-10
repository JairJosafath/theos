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

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// module is a throwaway module: leaf, which mid imports, which top imports;
// tool, whose tests alone import leaf, and user, which imports tool; suite
// and chart, packages of tests alone, which import leaf and top; embed, which
// embeds a file; data, whose tests read a fixture beside them; a nested
// module and a directory whose only Go file a build constraint excludes.
var module = map[string]string{
	"go.mod":               "module example.com/m\n\ngo 1.22\n",
	"leaf/leaf.go":         "package leaf\n",
	"leaf/leaf_test.go":    "package leaf\n\nimport \"testing\"\n\nfunc TestLeaf(t *testing.T) {}\n",
	"leaf/testdata/in.txt": "in\n",
	"mid/mid.go":           "package mid\n\nimport _ \"example.com/m/leaf\"\n",
	"top/top.go":           "package top\n\nimport _ \"example.com/m/mid\"\n",
	"tool/tool.go":         "package tool\n",
	"tool/tool_test.go": "package tool\n\nimport (\n\t\"testing\"\n\n\t_ \"example.com/m/leaf\"\n)\n\n" +
		"func TestTool(t *testing.T) {}\n",
	"user/user.go": "package user\n\nimport _ \"example.com/m/tool\"\n",
	"suite/suite_test.go": "package suite\n\nimport (\n\t\"testing\"\n\n\t_ \"example.com/m/leaf\"\n)\n\n" +
		"func TestSuite(t *testing.T) {}\n",
	"chart/chart_test.go": "package chart_test\n\nimport (\n\t\"testing\"\n\n\t_ \"example.com/m/top\"\n)\n\n" +
		"func TestChart(t *testing.T) {}\n",
	"embed/embed.go":       "package embed\n\nimport _ \"embed\"\n\n//go:embed assets/a.txt\nvar A string\n",
	"embed/assets/a.txt":   "a\n",
	"data/data.go":         "package data\n",
	"data/fixtures/x.yaml": "x: 1\n",
	"nested/go.mod":        "module example.com/nested\n\ngo 1.22\n",
	"nested/n.go":          "package nested\n",
	"tagged/t.go":          "//go:build never\n\npackage tagged\n",
	"docs/readme.md":       "# docs\n",
}

func writeModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range module {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The module's root as go list reports it, so gta maps a directory to its
	// import path: a temporary directory may lie behind a symlink.
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// TestRunSelectsWhatAChangeAffects pins the selection make test and make check
// rest on (#127): gta's, on this module's packages alone, plus the files a
// package owns.
func TestRunSelectsWhatAChangeAffects(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	t.Chdir(writeModule(t))

	tests := []struct {
		name    string
		changed []string
		want    []string
	}{
		{
			name:    "a Go file: its package, what imports it and what tests with it, and their dependents",
			changed: []string{"leaf/leaf.go"},
			want: []string{
				"dependent chart", "changed leaf", "dependent mid", "dependent suite",
				"dependent tool", "dependent top", "dependent user",
			},
		},
		{
			name:    "a test file alone: its package, since no other links it",
			changed: []string{"leaf/leaf_test.go"},
			want:    []string{"changed leaf"},
		},
		{
			name:    "a package only a package of tests alone imports: both (gta alone misses it)",
			changed: []string{"top/top.go"},
			want:    []string{"dependent chart", "changed top"},
		},
		{
			name:    "a package of tests alone: itself",
			changed: []string{"suite/suite_test.go"},
			want:    []string{"changed suite"},
		},
		{
			name:    "two files: each package once, and what tests their dependents",
			changed: []string{"mid/mid.go", "top/top.go", "docs/readme.md"},
			want:    []string{"dependent chart", "changed mid", "changed top"},
		},
		{
			name:    "a package nothing imports: itself",
			changed: []string{"user/user.go"},
			want:    []string{"changed user"},
		},
		{
			name:    "an embedded file: the package that embeds it",
			changed: []string{"embed/assets/a.txt"},
			want:    []string{"changed embed"},
		},
		{
			name:    "a file a package's tests read beside them: the package that owns it",
			changed: []string{"data/fixtures/x.yaml"},
			want:    []string{"changed data"},
		},
		{
			name:    "a file below testdata: its package alone, whose tests read it",
			changed: []string{"leaf/testdata/in.txt"},
			want:    []string{"changed leaf"},
		},
		{
			name:    "a file that is gone: the package that owned it",
			changed: []string{"data/fixtures/gone.yaml"},
			want:    []string{"changed data"},
		},
		{
			name:    "a Go file of a nested module: no package of this module, and no failure",
			changed: []string{"nested/n.go"},
		},
		{
			name:    "a Go file a build constraint excludes: no package",
			changed: []string{"tagged/t.go"},
		},
		{
			name:    "a Go file of a package that is gone: nothing is left to test",
			changed: []string{"gone/gone.go"},
		},
		{
			name:    "a file in no package",
			changed: []string{"docs/readme.md", "go.mod"},
		},
		{
			name: "no file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			in := strings.NewReader(strings.Join(tt.changed, "\n") + "\n\n")
			if err := run("-", in, &out); err != nil {
				t.Fatalf("run: %v", err)
			}
			var want string
			for _, w := range tt.want {
				kind, pkg, _ := strings.Cut(w, " ")
				want += kind + " example.com/m/" + pkg + "\n"
			}
			if got := out.String(); got != want {
				t.Errorf("changed %v:\ngot:\n%swant:\n%s", tt.changed, got, want)
			}
		})
	}
}

// TestRunReadsTheListFromAFile checks -changed-files FILE, with an absolute
// path among the relative ones.
func TestRunReadsTheListFromAFile(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	root := writeModule(t)
	t.Chdir(root)

	list := filepath.Join(t.TempDir(), "changed")
	content := filepath.Join(root, "top", "top.go") + "\nembed/embed.go\n"
	if err := os.WriteFile(list, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(list, strings.NewReader("leaf/leaf.go\n"), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "dependent example.com/m/chart\nchanged example.com/m/embed\nchanged example.com/m/top\n"
	if got := out.String(); got != want {
		t.Errorf("got:\n%swant:\n%s", got, want)
	}

	if err := run(filepath.Join(root, "missing"), strings.NewReader(""), &out); err == nil {
		t.Error("a list that is not there: want an error")
	}
}
