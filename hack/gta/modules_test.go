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
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestDiffModulesNamesWhatAChangeToGoModReaches pins what a changed line of
// go.mod or go.sum selects (#127): the module it names, every package for a
// line each is built by, and nothing for a line that builds none.
func TestDiffModulesNamesWhatAChangeToGoModReaches(t *testing.T) {
	const mod = `module example.com/m

go 1.22

toolchain go1.22.3

require (
	example.com/a v1.0.0
	example.com/b v1.0.0 // indirect
)

require example.com/c v1.0.0
`
	const sum = `example.com/a v1.0.0 h1:aaa=
example.com/a v1.0.0/go.mod h1:aam=
example.com/b v1.0.0 h1:bbb=
`
	tests := []struct {
		name    string
		mod     func(string) string
		sum     string
		want    []string
		wantAll string
	}{
		{name: "nothing changed"},
		{
			name: "a version: that module",
			mod:  replace("example.com/a v1.0.0", "example.com/a v1.1.0"),
			want: []string{"example.com/a"},
		},
		{
			name: "a module is added: that module",
			mod:  replace("require example.com/c v1.0.0", "require example.com/c v1.0.0\nrequire example.com/d v0.1.0"),
			want: []string{"example.com/d"},
		},
		{
			name: "a module is gone: that module, which no package can still use",
			mod:  replace("require example.com/c v1.0.0\n", ""),
			want: []string{"example.com/c"},
		},
		{
			name: "two versions: both modules",
			mod: func(s string) string {
				return replace("example.com/b v1.0.0", "example.com/b v1.2.0")(
					replace("example.com/c v1.0.0", "example.com/c v1.0.1")(s))
			},
			want: []string{"example.com/b", "example.com/c"},
		},
		{
			name: "a replace line: the module it replaces",
			mod:  replace("go 1.22\n", "go 1.22\n\nreplace example.com/b => ../b\n"),
			want: []string{"example.com/b"},
		},
		{
			name: "an exclude line: the module it names",
			mod:  replace("go 1.22\n", "go 1.22\n\nexclude example.com/a v0.9.0\n"),
			want: []string{"example.com/a"},
		},
		{
			name: "a comment alone, as // indirect: nothing",
			mod:  replace("example.com/b v1.0.0 // indirect", "example.com/b v1.0.0"),
		},
		{
			name: "a line moves into the block: nothing",
			mod: replace("\texample.com/b v1.0.0 // indirect\n)\n\nrequire example.com/c v1.0.0\n",
				"\texample.com/b v1.0.0 // indirect\n\texample.com/c v1.0.0\n)\n"),
		},
		{
			name: "a retract and a tool line, which build no package: nothing",
			mod:  replace("go 1.22\n", "go 1.22\n\nretract v0.0.1\n\ntool example.com/a/cmd/x\n"),
		},
		{
			name:    "the go line: every package",
			mod:     replace("go 1.22\n", "go 1.23\n"),
			wantAll: "the go line of go.mod changed, which every package is built by",
		},
		{
			name:    "the toolchain line, which holds the standard library: every package",
			mod:     replace("toolchain go1.22.3", "toolchain go1.22.4"),
			wantAll: "the toolchain line of go.mod changed, which every package is built by",
		},
		{
			name:    "a godebug line: every package",
			mod:     replace("go 1.22\n", "go 1.22\n\ngodebug asynctimerchan=1\n"),
			wantAll: "the godebug line of go.mod changed, which every package is built by",
		},
		{
			name:    "the module line: every package",
			mod:     replace("module example.com/m", "module example.com/n"),
			wantAll: "the module line of go.mod changed, which every package is built by",
		},
		{
			name: "go.sum, a module's files: that module",
			sum:  strings.Replace(sum, "h1:bbb=", "h1:ccc=", 1),
			want: []string{"example.com/b"},
		},
		{
			name: "go.sum, a line more for a module's go.mod alone: nothing",
			sum:  sum + "example.com/z v1.0.0/go.mod h1:zzm=\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newMod, newSum := mod, sum
			if tt.mod != nil {
				if newMod = tt.mod(mod); newMod == mod {
					t.Fatal("the case changes nothing in go.mod")
				}
			}
			if tt.sum != "" {
				newSum = tt.sum
			}
			all, modules, err := diffModules([]byte(mod), []byte(newMod), []byte(sum), []byte(newSum))
			if err != nil {
				t.Fatalf("diffModules: %v", err)
			}
			if all != tt.wantAll {
				t.Errorf("all: got %q, want %q", all, tt.wantAll)
			}
			if got := slices.Sorted(maps.Keys(modules)); !slices.Equal(got, tt.want) {
				t.Errorf("modules: got %v, want %v", got, tt.want)
			}
		})
	}

	if _, _, err := diffModules([]byte(mod), []byte("module\n"), nil, nil); err == nil {
		t.Error("a go.mod that doesn't parse: want an error, so the callers take every package")
	}
}

func replace(old, with string) func(string) string {
	return func(s string) string { return strings.Replace(s, old, with, 1) }
}

// modular is a throwaway module that builds on two others, which lie beside
// it: usea imports a's package and viaa imports usea; testa's tests alone
// import a, and usertesta imports testa; useb imports b, and suite, a package
// of tests alone, does too; plain imports nothing, and nothing imports c.
var modular = map[string]string{
	"a/go.mod":  "module example.com/a\n\ngo 1.22\n",
	"a/a.go":    "package a\n",
	"b/go.mod":  "module example.com/b\n\ngo 1.22\n",
	"b/b.go":    "package b\n",
	"b2/go.mod": "module example.com/b\n\ngo 1.22\n",
	"b2/b.go":   "package b\n",
	"c/go.mod":  "module example.com/c\n\ngo 1.22\n",
	"c/c.go":    "package c\n",
	"m/go.mod": "module example.com/m\n\ngo 1.22\n\n" +
		"require (\n\texample.com/a v0.1.0\n\texample.com/b v0.1.0\n)\n\n" +
		"replace example.com/a => ../a\n\nreplace example.com/b => ../b\n",
	"m/usea/usea.go":   "package usea\n\nimport _ \"example.com/a\"\n",
	"m/viaa/viaa.go":   "package viaa\n\nimport _ \"example.com/m/usea\"\n",
	"m/testa/testa.go": "package testa\n",
	"m/testa/testa_test.go": "package testa\n\nimport (\n\t\"testing\"\n\n\t_ \"example.com/a\"\n)\n\n" +
		"func TestA(t *testing.T) {}\n",
	"m/usertesta/usertesta.go": "package usertesta\n\nimport _ \"example.com/m/testa\"\n",
	"m/useb/useb.go":           "package useb\n\nimport _ \"example.com/b\"\n",
	"m/suite/suite_test.go": "package suite_test\n\nimport (\n\t\"testing\"\n\n\t_ \"example.com/b\"\n)\n\n" +
		"func TestSuite(t *testing.T) {}\n",
	"m/plain/plain.go": "package plain\n",
	"m/docs/readme.md": "# docs\n",
}

// TestRunSelectsTheUsersOfAChangedModule pins what a change to go.mod or
// go.sum tests (#127): the packages built from a module whose version
// changed, through their tests too, and not every package. gta's own command
// selects none for such a change.
func TestRunSelectsTheUsersOfAChangedModule(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, who := range []string{"AUTHOR", "COMMITTER"} {
		t.Setenv("GIT_"+who+"_NAME", "t")
		t.Setenv("GIT_"+who+"_EMAIL", "t@t")
	}
	top, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range modular {
		path := filepath.Join(top, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(filepath.Join(top, "m"))
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("add", "-A")
	git("commit", "-q", "-m", "one")

	gomod := modular["m/go.mod"]
	tests := []struct {
		name    string
		gomod   string
		gosum   string
		changed []string
		base    string
		want    string
	}{
		{
			name:    "a version: the packages that import the module, in their tests too, and what imports those",
			gomod:   strings.Replace(gomod, "example.com/a v0.1.0", "example.com/a v0.2.0", 1),
			changed: []string{"go.mod"},
			base:    "HEAD",
			want:    "modules example.com/a\nmodule testa\nmodule usea\nmodule viaa\n",
		},
		{
			name:    "a replacement: the module's users, a package of tests alone among them",
			gomod:   strings.Replace(gomod, "=> ../b\n", "=> ../b2\n", 1),
			changed: []string{"go.mod", "go.sum"},
			base:    "HEAD",
			want:    "modules example.com/b\nmodule suite\nmodule useb\n",
		},
		{
			name:    "a version and a Go file: the module's users, and what gta selects",
			gomod:   strings.Replace(gomod, "example.com/b v0.1.0", "example.com/b v0.2.0", 1),
			changed: []string{"go.mod", "usea/usea.go", "plain/plain.go"},
			base:    "HEAD",
			want:    "modules example.com/b\nchanged plain\nmodule suite\nchanged usea\nmodule useb\ndependent viaa\n",
		},
		{
			name:    "go.sum alone, a module's files: its users",
			gosum:   "example.com/a v0.1.0 h1:aaa=\n",
			changed: []string{"go.sum"},
			base:    "HEAD",
			want:    "modules example.com/a\nmodule testa\nmodule usea\nmodule viaa\n",
		},
		{
			name:    "a comment alone: no package",
			gomod:   gomod + "\n// A note.\n",
			changed: []string{"go.mod", "docs/readme.md"},
			base:    "HEAD",
		},
		{
			name: "a module no package uses: no package",
			gomod: strings.Replace(gomod, "example.com/b v0.1.0\n", "example.com/b v0.1.0\n\texample.com/c v0.1.0\n", 1) +
				"\nreplace example.com/c => ../c\n",
			changed: []string{"go.mod"},
			base:    "HEAD",
		},
		{
			name:    "the go line: every package",
			gomod:   strings.Replace(gomod, "go 1.22\n", "go 1.23\n", 1),
			changed: []string{"go.mod", "plain/plain.go"},
			base:    "HEAD",
			want:    "all the go line of go.mod changed, which every package is built by\n",
		},
		{
			name:    "no base to compare with: every package",
			gomod:   strings.Replace(gomod, "example.com/a v0.1.0", "example.com/a v0.2.0", 1),
			changed: []string{"go.mod"},
			want:    "all go.mod or go.sum changed, and no -base says what they were\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := gomod
			if tt.gomod != "" {
				content = tt.gomod
			}
			if err := os.WriteFile("go.mod", []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_ = os.Remove("go.sum")
			if tt.gosum != "" {
				if err := os.WriteFile("go.sum", []byte(tt.gosum), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			in := strings.NewReader(strings.Join(tt.changed, "\n") + "\n")
			if err := run("-", tt.base, in, &out); err != nil {
				t.Fatalf("run: %v", err)
			}
			got := strings.ReplaceAll(out.String(), " example.com/m/", " ")
			if got != tt.want {
				t.Errorf("changed %v:\ngot:\n%swant:\n%s", tt.changed, got, tt.want)
			}
		})
	}

	t.Run("a base that is no commit: an error, so the callers take every package", func(t *testing.T) {
		var out bytes.Buffer
		if err := run("-", "no-such-revision", strings.NewReader("go.mod\n"), &out); err == nil {
			t.Errorf("want an error, got:\n%s", out.String())
		}
	})
	t.Run("a go.mod the packages can't be built from: an error", func(t *testing.T) {
		broken := strings.Replace(gomod, "\texample.com/a v0.1.0\n", "", 1)
		if err := os.WriteFile("go.mod", []byte(broken), 0o600); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := run("-", "HEAD", strings.NewReader("go.mod\n"), &out); err == nil {
			t.Errorf("want an error, got:\n%s", out.String())
		}
	})
}
