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

// clone writes files into a folder named name, as a repository's clone, and
// returns it.
func clone(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	for p, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// #96: theos-session adopt reads a clone and proposes the model that adopts
// it, applying nothing.
func TestAdopt(t *testing.T) {
	dir := clone(t, "Invoices", map[string]string{
		"Cargo.toml": "[package]\nname = \"invoices\"\n", "src/main.rs": "fn main() {}\n", "Dockerfile": "FROM scratch\n",
		".github/workflows/ci.yml": "name: ci\non: [push, pull_request]\njobs:\n  test:\n    runs-on: ubuntu-latest\n",
		".git/config":              "[core]\n", "node_modules/x/package.json": "{}",
	})
	var stdout, stderr bytes.Buffer
	args := []string{"--dir", dir, "--project", "billing", "--git-provider", "git"}
	if code := adoptMain(args, &stdout, &stderr); code != 0 {
		t.Fatalf("adopt exited %d: %s", code, stderr.String())
	}
	for _, want := range []string{
		"kind: Container\nmetadata:\n  name: invoices\n  namespace: billing\n",
		"  softwareSystemRef:\n    name: billing\n", "  type: service\n", "    name: git\n  adopt: true\n",
		"  checkCommand: cargo test\n", "  checkContexts:\n  - \"ci / test*\"\n",
		"kind: Crew\n", "  approval:\n  - require: Person\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("the model lacks %q:\n%s", want, stdout.String())
		}
	}
	for _, want := range []string{
		"Read 4 files of ", "     project: billing\n     softwareSystem: billing\n     container: invoices\n",
		"kubectl apply -f model.yaml", "kubectl -n billing get container invoices",
		"each job's runs-on must be a label of\n   the project's CI runners, which GitProvider git lists in its spec.ci.",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the steps lack %q:\n%s", want, stderr.String())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".theos.yaml")); err == nil {
		t.Error("adopt wrote .theos.yaml without --write-identity")
	}
}

func TestAdoptWritesTheIdentityWhenAsked(t *testing.T) {
	dir := clone(t, "orders", map[string]string{"go.mod": "module example.com/orders\n", "main.go": "package main\n",
		"Makefile": "check:\n\tgo test ./...\n"})
	var stdout, stderr bytes.Buffer
	args := []string{"--dir", dir, "--project", "shop", "--system", "shop-core", "--name", "orders-cli",
		"--git-provider", "git", "--write-identity"}
	if code := adoptMain(args, &stdout, &stderr); code != 0 {
		t.Fatalf("adopt exited %d: %s", code, stderr.String())
	}
	identity, err := os.ReadFile(filepath.Join(dir, ".theos.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := "project: shop\nsoftwareSystem: shop-core\ncontainer: orders-cli\ntype: cli\nscaffoldedFrom: []\n"
	if string(identity) != want {
		t.Errorf(".theos.yaml = %q, want %q", identity, want)
	}
	if strings.Contains(stdout.String(), "checkCommand") {
		t.Errorf("the model names a check command, and the Makefile has a check target:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "It has no workflow under") {
		t.Errorf("the steps don't say the repository has no CI:\n%s", stderr.String())
	}
}

func TestAdoptRefusesWhatTheClusterWould(t *testing.T) {
	dir := clone(t, "orders", map[string]string{"go.mod": ""})
	for name, tt := range map[string]struct {
		args []string
		want string
	}{
		"no project":     {[]string{"--dir", dir, "--git-provider", "git"}, "--project is required"},
		"no GitProvider": {[]string{"--dir", dir, "--project", "shop"}, "--git-provider is required"},
		"a name in caps": {[]string{"--dir", dir, "--project", "shop", "--git-provider", "git", "--name", "Orders_API"},
			`--name "Orders_API" is no name the cluster takes`},
		"an empty folder": {[]string{"--dir", t.TempDir(), "--project", "shop", "--git-provider", "git", "--name", "x"},
			"has no files"},
	} {
		var stdout, stderr bytes.Buffer
		if code := adoptMain(tt.args, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), tt.want) {
			t.Errorf("%s: adopt exited %d with %q, want 1 with %q", name, code, stderr.String(), tt.want)
		}
		if stdout.Len() > 0 {
			t.Errorf("%s: adopt printed a model: %s", name, stdout.String())
		}
	}
}
