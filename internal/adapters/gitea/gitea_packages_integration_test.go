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

package gitea

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// TestGiteaPackagesIntegration publishes a release's npm tarball, wheel and
// sdist to the local Gitea's registries and installs each with the real
// tools, as decision 7 of docs/plans/done/app-packages.md has an app do: npm with
// the scope routed to the owner's registry, uv with the owner's index declared
// explicit and the package pinned to it. A repeat publishes nothing. The
// publish runs with or without the tools; only an install subtest skips when
// its tool is not on PATH, and says so (#1702).
func TestGiteaPackagesIntegration(t *testing.T) {
	base := os.Getenv("THEOS_GITEA_URL")
	if base == "" {
		t.Skip("THEOS_GITEA_URL not set")
	}
	creds := ports.Credentials{Username: os.Getenv("THEOS_GITEA_USERNAME"), Token: os.Getenv("THEOS_GITEA_TOKEN")}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	p, err := Client{}.Provider(ports.Config{URL: base, Owner: "webshop", Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("it-pkg-%d", time.Now().UnixNano())
	t.Cleanup(func() { deleteRepo(t, base, "webshop", name, creds.Token) })
	repo, err := p.EnsureRepository(ctx, name, "integration test")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := p.PushInitialCommit(ctx, repo, scaffold.Tree{
		"README.md": {Data: []byte("# " + name + "\n")},
	}, "Scaffold\n", testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	// The release waits for Gitea to see the push, as the release run does.
	id := createRelease(t, base, creds.Token, repo, "v0.1.0", commit)

	// The release's files, named as the build tools name them. The npm
	// package has no dependencies, so the install reaches the local Gitea
	// alone. The packages are the organisation's, not the repository's:
	// deleting the repository leaves them, so the test deletes them itself.
	npmName := "@theos/" + name
	npmFile := name + "-0.1.0.tgz"
	pyName := name
	wheelFile := strings.ReplaceAll(pyName, "-", "_") + "-0.1.0-py3-none-any.whl"
	sdistFile := strings.ReplaceAll(pyName, "-", "_") + "-0.1.0.tar.gz"
	manifest := fmt.Sprintf(`{"name":%q,"version":"0.1.0","description":"The shop's UI","main":"index.js",`+
		`"repository":"%s/webshop/%s.git"}`, npmName, strings.TrimSuffix(base, "/"), name)
	uploadAsset(t, base, creds.Token, repo, id, npmFile, string(tarGz(t, map[string][]byte{
		"package/package.json": []byte(manifest),
		"package/index.js":     []byte("export const ui = 1;\n"),
	})))
	uploadAsset(t, base, creds.Token, repo, id, wheelFile, string(wheel(t, pyName, "0.1.0")))
	uploadAsset(t, base, creds.Token, repo, id, sdistFile, string(sdist(t, pyName, "0.1.0")))
	uploadAsset(t, base, creds.Token, repo, id, name+"-linux-amd64.tar.gz", string(tarGz(t, map[string][]byte{
		name: []byte("#!/bin/sh\necho hello\n"),
	})))
	t.Cleanup(func() {
		for _, target := range []string{
			"/api/v1/packages/webshop/npm/" + url.PathEscape(npmName) + "/0.1.0",
			"/api/v1/packages/webshop/pypi/" + pyName + "/0.1.0",
		} {
			if status := packageCall(t, context.Background(), http.MethodDelete, strings.TrimSuffix(base, "/")+target,
				creds); status != http.StatusNoContent {
				t.Logf("DELETE %s: HTTP %d", target, status)
			}
		}
	})

	want := map[string]ports.Package{
		npmFile:   {Outcome: ports.PackagePublished, Ecosystem: ports.EcosystemNpm, Name: npmName, Version: "0.1.0", File: npmFile},
		wheelFile: {Outcome: ports.PackagePublished, Ecosystem: ports.EcosystemPyPI, Name: pyName, Version: "0.1.0", File: wheelFile},
		sdistFile: {Outcome: ports.PackagePublished, Ecosystem: ports.EcosystemPyPI, Name: pyName, Version: "0.1.0", File: sdistFile},
	}
	got, err := p.PublishReleasePackages(ctx, repo, "v0.1.0")
	if err != nil {
		t.Fatalf("PublishReleasePackages() = %+v, %v", got, err)
	}
	checkPackages(t, got, want)

	t.Run("npm installs it", func(t *testing.T) {
		npmBin, err := exec.LookPath("npm")
		if err != nil {
			t.Skip("npm is not on PATH; the install check needs it (#1702)")
		}
		project := t.TempDir()
		u, _ := url.Parse(base)
		npmrc := fmt.Sprintf("@theos:registry=%s\n//%s/api/packages/webshop/npm/:_authToken=%s\n",
			repo.NpmRegistry, u.Host, creds.Token)
		if err := os.WriteFile(filepath.Join(project, ".npmrc"), []byte(npmrc), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(project, "package.json"), []byte(`{"name":"app","private":true}`),
			0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, npmBin, "install", "--save-exact", "--no-audit", "--no-fund",
			"--ignore-scripts", npmName+"@0.1.0")
		cmd.Dir = project
		cmd.Env = append(os.Environ(), "HOME="+project, "npm_config_cache="+filepath.Join(project, ".npm-cache"),
			"npm_config_update_notifier=false")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("npm install: %v\n%s", err, out)
		}
		installed, err := os.ReadFile(filepath.Join(project, "node_modules", npmName, "package.json"))
		if err != nil {
			t.Fatalf("the package is not installed: %v", err)
		}
		if !bytes.Contains(installed, []byte(`"version":"0.1.0"`)) &&
			!bytes.Contains(installed, []byte(`"version": "0.1.0"`)) {
			t.Errorf("the installed package.json is %s", installed)
		}
		var app struct {
			Dependencies map[string]string `json:"dependencies"`
		}
		manifest, _ := os.ReadFile(filepath.Join(project, "package.json"))
		if err := json.Unmarshal(manifest, &app); err != nil || app.Dependencies[npmName] != "0.1.0" {
			t.Errorf("package.json after the install is %s; want %s pinned to 0.1.0", manifest, npmName)
		}
	})

	t.Run("uv installs it", func(t *testing.T) {
		uvBin, err := exec.LookPath("uv")
		if err != nil {
			t.Skip("uv is not on PATH; the install check needs it (#1702)")
		}
		project := t.TempDir()
		pyproject := fmt.Sprintf("[project]\nname = \"app\"\nversion = \"0.0.0\"\nrequires-python = \">=3.12\"\n"+
			"dependencies = []\n\n[[tool.uv.index]]\nname = \"theos\"\nurl = %q\nexplicit = true\n\n"+
			"[tool.uv.sources]\n%s = { index = \"theos\" }\n", repo.PyPIIndex, pyName)
		if err := os.WriteFile(filepath.Join(project, "pyproject.toml"), []byte(pyproject), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, uvBin, "add", "--no-progress", pyName+"==0.1.0")
		cmd.Dir = project
		cmd.Env = append(os.Environ(), "HOME="+project, "UV_CACHE_DIR="+filepath.Join(project, ".uv-cache"),
			"UV_PYTHON_PREFERENCE=only-system", "UV_NO_CONFIG=1",
			"UV_INDEX_THEOS_USERNAME="+creds.Username, "UV_INDEX_THEOS_PASSWORD="+creds.Token)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("uv add: %v\n%s", err, out)
		}
		lock, err := os.ReadFile(filepath.Join(project, "uv.lock"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(lock, []byte("name = \""+normalizeProjectName(pyName)+"\"\nversion = \"0.1.0\"")) ||
			!bytes.Contains(lock, []byte(repo.PyPIIndex)) {
			t.Errorf("uv.lock does not pin %s 0.1.0 to %s:\n%s", pyName, repo.PyPIIndex, lock)
		}
		if _, err := os.Stat(filepath.Join(project, ".venv", "lib")); err != nil {
			t.Errorf("uv add made no virtual environment: %v", err)
		}
		if found, _ := filepath.Glob(filepath.Join(project, ".venv", "lib", "python3*", "site-packages",
			strings.ReplaceAll(pyName, "-", "_"), "__init__.py")); len(found) != 1 {
			t.Errorf("the package is not in the virtual environment: found %v", found)
		}
	})

	// A repeat publishes nothing: every package exists.
	for file, pkg := range want {
		pkg.Outcome = ports.PackageExists
		want[file] = pkg
	}
	got, err = p.PublishReleasePackages(ctx, repo, "v0.1.0")
	if err != nil {
		t.Fatalf("PublishReleasePackages() again = %+v, %v", got, err)
	}
	checkPackages(t, got, want)
}

// checkPackages checks that got holds exactly the packages want, by file.
func checkPackages(t *testing.T, got ports.ReleasePackages, want map[string]ports.Package) {
	t.Helper()
	if got.Tag != "v0.1.0" || len(got.Packages) != len(want) {
		t.Fatalf("PublishReleasePackages() = %+v; want %d packages of v0.1.0", got, len(want))
	}
	for _, pkg := range got.Packages {
		if pkg != want[pkg.File] {
			t.Errorf("package %s = %+v; want %+v", pkg.File, pkg, want[pkg.File])
		}
	}
}
