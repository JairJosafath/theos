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
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/scaffold"
)

// TestRenderIdentityFlags264 checks that -project and -system reach the
// rendered .theos.yaml, and that without them it names webshop and shop, as
// make test-templates expects (#264).
func TestRenderIdentityFlags264(t *testing.T) {
	cases := []struct {
		name            string
		flags           []string
		project, system string
	}{
		{"no flags give the defaults", nil, "webshop", "shop"},
		{"both flags are written", []string{"-project", "customs", "-system", "mcp"}, "customs", "mcp"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "app")
			// A one-file layer of its own: a test that reads the repository's
			// templates would make this package one of their readers.
			templates := t.TempDir()
			layer := filepath.Join(templates, "tiny")
			if err := os.MkdirAll(layer, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(layer, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			args := append([]string{"-templates", templates, "-out", out, "-container", "checker"}, c.flags...)
			args = append(args, "Framework=tiny")
			if err := run("render", args, io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(out, scaffold.IdentityPath))
			if err != nil {
				t.Fatal(err)
			}
			id, err := scaffold.ParseIdentity(data)
			if err != nil {
				t.Fatal(err)
			}
			if id.Project != c.project || id.SoftwareSystem != c.system || id.Container != "checker" {
				t.Errorf("identity = %q/%q/%q, want %q/%q/checker",
					id.Project, id.SoftwareSystem, id.Container, c.project, c.system)
			}
		})
	}
}

// TestRenderBadFlag264 checks that a flag that does not parse is the usage
// exit (main maps errFlags to 2, as the old global flag set did) and that the
// error is printed once, under the program's name (#264).
func TestRenderBadFlag264(t *testing.T) {
	for _, args := range [][]string{{"-bogus"}, {"-retention-days", "x"}} {
		var stdout, stderr bytes.Buffer
		err := run("prog", args, &stdout, &stderr)
		if !errors.Is(err, errFlags) {
			t.Fatalf("%v: err = %v, want errFlags", args, err)
		}
		got := strings.Count(stderr.String(), "invalid value") + strings.Count(stderr.String(), "not defined")
		if got != 1 {
			t.Errorf("%v: error printed %d times, want once:\n%s", args, got, stderr.String())
		}
		if !strings.Contains(stderr.String(), "Usage of prog:") {
			t.Errorf("%v: usage does not name the program:\n%s", args, stderr.String())
		}
	}
	var stderr bytes.Buffer
	if err := run("prog", []string{"-h"}, io.Discard, &stderr); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h: err = %v, want flag.ErrHelp", err)
	}
}
