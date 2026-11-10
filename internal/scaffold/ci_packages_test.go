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

package scaffold

import (
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/JairJosafath/theos/internal/model"
)

// packagesStep is the step of a GitHub workflow that tells go, npm and uv
// where the owner's own packages are; checkWorkflow is the workflow that
// has it.
const (
	packagesStep  = "Where the owner's packages are"
	checkWorkflow = ".github/workflows/check.yaml"
)

// standardsWithPackages are the Standards whose check installs packages.
var standardsWithPackages = []string{"aws-standard", "go-operator-standard", "go-standard", "python-standard",
	"ts-standard"}

// steppedWorkflow is what the test reads of a workflow: what its token may
// do, and each job's steps.
type steppedWorkflow struct {
	Permissions map[string]string `json:"permissions"`
	Jobs        map[string]struct {
		Steps []struct {
			Name string `json:"name"`
			If   string `json:"if"`
			Run  string `json:"run"`
		} `json:"steps"`
	} `json:"jobs"`
}

// ownersPackagesScript renders every Standard's check workflow for GitHub
// Actions, with either choice of checks, and returns the script of the
// step that says where the owner's packages are. It fails t unless every
// job has that step before its make step, skipped with the make step when
// the change touches no code, the token may read packages, and the script
// is one and the same everywhere.
func ownersPackagesScript(t *testing.T) string {
	t.Helper()
	ci, err := model.CIByName(model.CIGitHubActions)
	if err != nil {
		t.Fatal(err)
	}
	script := ""
	for _, standard := range standardsWithPackages {
		typ, sides := standardCombo(t, standard)
		for _, checks := range []string{model.ChecksEveryChange, model.ChecksBeforeRelease} {
			in, layers := loadCombo(t, typ, sides, checks)
			in.CI = ci
			files, err := RenderLayer(in, layers[slices.IndexFunc(layers, func(l Layer) bool { return l.Name == standard })])
			if err != nil {
				t.Fatal(err)
			}
			var w steppedWorkflow
			if err := yaml.Unmarshal(files[checkWorkflow].Data, &w); err != nil || len(w.Jobs) == 0 {
				t.Fatalf("%s with %s: %s is no workflow with jobs: %v", standard, checks, checkWorkflow, err)
			}
			if w.Permissions["packages"] != "read" || w.Permissions["contents"] != "read" || len(w.Permissions) != 2 {
				t.Errorf("%s with %s: the job's token may %v, want it to read the repository and its packages and no more",
					standard, checks, w.Permissions)
			}
			for id, job := range w.Jobs {
				at, makes := -1, -1
				for i, step := range job.Steps {
					switch {
					case step.Name == packagesStep:
						at = i
					case strings.HasPrefix(step.Run, "make "):
						makes = i
					}
				}
				if at < 0 || makes < at {
					t.Fatalf("%s with %s: the job %s has no step %q before its make step", standard, checks, id, packagesStep)
				}
				if job.Steps[at].If != job.Steps[makes].If {
					t.Errorf("%s with %s: in the job %s the packages step runs if %q and make if %q, want the same",
						standard, checks, id, job.Steps[at].If, job.Steps[makes].If)
				}
				if script == "" {
					script = job.Steps[at].Run
				}
				if job.Steps[at].Run != script {
					t.Errorf("%s with %s: the job %s has a packages step of its own, want the one every Standard has",
						standard, checks, id)
				}
			}
		}
	}
	return script
}

// TestGitHubJobsFindTheOwnersPackages covers #80: the environment a CI job
// on GitHub Actions gets for the owner's own packages. The step's script is
// run as the job runs it, with what GitHub and the operator give it, and
// what it writes is read back: Git's credential and GOPRIVATE for the
// owner's Go modules, ~/.npmrc for the owner's npm scope, and a login for a
// Python index only when the GitProvider names one. The job's token goes
// to GitHub and nowhere else.
func TestGitHubJobsFindTheOwnersPackages(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh on PATH: %v", err)
	}
	script := ownersPackagesScript(t)
	job, npm, pypi := rand.Text(), rand.Text(), rand.Text()
	const enterprise = "https://github.example.com:8443"
	netrc := func(host string) string { return "machine " + host + "\nlogin x-access-token\npassword " + job + "\n" }
	for _, c := range []struct {
		name               string
		env                map[string]string
		netrc, npmrc, goTo string
	}{
		{name: "github.com, nothing set: GitHub's own registry under the owner's scope, in lower case",
			env:   map[string]string{"SERVER": "https://github.com", "OWNER": "Acme"},
			netrc: netrc("github.com"),
			npmrc: "@acme:registry=https://npm.pkg.github.com/\n//npm.pkg.github.com/:_authToken=" + job + "\n",
			goTo:  "github.com/Acme"},
		{name: "an Enterprise Server: its npm registry at a subdomain, and a module path without the port",
			env:   map[string]string{"SERVER": enterprise, "OWNER": "acme"},
			netrc: netrc("github.example.com"),
			npmrc: "@acme:registry=https://npm.github.example.com:8443/\n//npm.github.example.com:8443/:_authToken=" + job + "\n",
			goTo:  "github.example.com/acme"},
		{name: "a token of the GitProvider's for GitHub's own registry installs, not the job's",
			env:   map[string]string{"SERVER": "https://github.com", "OWNER": "acme", "NPM_TOKEN": npm},
			netrc: netrc("github.com"),
			npmrc: "@acme:registry=https://npm.pkg.github.com/\n//npm.pkg.github.com/:_authToken=" + npm + "\n",
			goTo:  "github.com/acme"},
		{name: "a registry and a scope the GitProvider names, with its token",
			env: map[string]string{"SERVER": "https://github.com", "OWNER": "acme", "NPM_SCOPE": "@shop",
				"NPM_REGISTRY": "https://registry.example.com/npm", "NPM_TOKEN": npm},
			netrc: netrc("github.com"),
			npmrc: "@shop:registry=https://registry.example.com/npm/\n//registry.example.com/npm/:_authToken=" + npm + "\n",
			goTo:  "github.com/acme"},
		{name: "a registry the GitProvider names with no token gets none: the job's token is GitHub's alone",
			env: map[string]string{"SERVER": "https://github.com", "OWNER": "acme", "NPM_SCOPE": "@shop",
				"NPM_REGISTRY": "https://registry.example.com/npm/"},
			netrc: netrc("github.com"),
			npmrc: "@shop:registry=https://registry.example.com/npm/\n",
			goTo:  "github.com/acme"},
		{name: "a Python index the GitProvider names, with its token",
			env: map[string]string{"SERVER": "https://github.com", "OWNER": "acme",
				"PYPI_INDEX": "https://pypi.example.com:8080/simple", "PYPI_TOKEN": pypi},
			netrc: netrc("github.com") + "machine pypi.example.com\nlogin __token__\npassword " + pypi + "\n",
			npmrc: "@acme:registry=https://npm.pkg.github.com/\n//npm.pkg.github.com/:_authToken=" + job + "\n",
			goTo:  "github.com/acme"},
		{name: "a Python index with no token has no login",
			env: map[string]string{"SERVER": "https://github.com", "OWNER": "acme",
				"PYPI_INDEX": "https://pypi.example.com/simple"},
			netrc: netrc("github.com"),
			npmrc: "@acme:registry=https://npm.pkg.github.com/\n//npm.pkg.github.com/:_authToken=" + job + "\n",
			goTo:  "github.com/acme"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			exported := filepath.Join(home, "github-env")
			// GitHub gives a variable or a secret that isn't set as "".
			env := map[string]string{"NPM_SCOPE": "", "NPM_REGISTRY": "", "NPM_TOKEN": "", "PYPI_INDEX": "",
				"PYPI_TOKEN": "", "TOKEN": job, "HOME": home, "GITHUB_ENV": exported, "PATH": os.Getenv("PATH")}
			for name, value := range c.env {
				env[name] = value
			}
			//nolint:gosec // the script of the repository's own template, run in a temporary home
			cmd := exec.Command(sh, "-eu", "-c", script)
			for name, value := range env {
				cmd.Env = append(cmd.Env, name+"="+value)
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("the step failed: %v: %s", err, out)
			}
			for file, want := range map[string]string{filepath.Join(home, ".netrc"): c.netrc,
				filepath.Join(home, ".npmrc"): c.npmrc, exported: "GOPRIVATE=" + c.goTo + "\n"} {
				got, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				// The tokens are this run's own, made a moment ago: showing them is no leak.
				if string(got) != want {
					t.Errorf("%s holds\n%s\nwant\n%s", filepath.Base(file), got, want)
				}
			}
			for _, file := range []string{".netrc", ".npmrc"} {
				info, err := os.Stat(filepath.Join(home, file))
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != 0o600 {
					t.Errorf("%s is %v, want it readable by its owner alone", file, info.Mode().Perm())
				}
			}
		})
	}
}
