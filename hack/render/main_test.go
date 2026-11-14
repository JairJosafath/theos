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
	"io"
	"os"
	"path/filepath"
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
			args := append([]string{"-templates", filepath.Join("..", "..", "templates"), "-out", out, "-container", "checker"}, c.flags...)
			args = append(args, "Framework=hello")
			if err := run(args, io.Discard); err != nil {
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
				t.Errorf("identity = %q/%q/%q, want %q/%q/checker", id.Project, id.SoftwareSystem, id.Container, c.project, c.system)
			}
		})
	}
}
