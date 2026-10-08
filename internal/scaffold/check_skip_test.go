package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// The Standards whose check workflow skips make check on a change to only the
// changelog, docs/ or images (#600).
var skippingStandards = []string{"go-standard", "ts-standard", "python-standard"}

type checkStep struct {
	Name string            `yaml:"name"`
	ID   string            `yaml:"id"`
	If   string            `yaml:"if"`
	Env  map[string]string `yaml:"env"`
	Run  string            `yaml:"run"`
}

func checkSteps(t *testing.T, standard string) []checkStep {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(templatesDir, standard, ".gitea/workflows/check.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []checkStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("%s: %v", standard, err)
	}
	return workflow.Jobs["check"].Steps
}

// Every Standard runs the same step right after the checkout, compares with
// the pull request's base or the push's previous commit, and makes every step
// after it depend on its answer, so the job, and the required "check" context,
// always reports.
func TestStandardChecksShareTheSkipStep(t *testing.T) {
	var want checkStep
	for _, standard := range skippingStandards {
		steps := checkSteps(t, standard)
		if len(steps) < 3 || steps[1].ID != "changes" {
			t.Fatalf("%s: want the changes step second, after the checkout; got %+v", standard, steps)
		}
		got := steps[1]
		if base := got.Env["BASE"]; base != "${{ gitea.event.pull_request.base.sha || gitea.event.before }}" {
			t.Errorf("%s: BASE is %q; want the pull request's base, else the push's previous commit", standard, base)
		}
		if want.Run == "" {
			want = got
		} else if got.Run != want.Run || got.Name != want.Name || !equalEnv(got.Env, want.Env) {
			t.Errorf("%s's changes step differs from %s's", standard, skippingStandards[0])
		}
		for _, step := range steps[2:] {
			if step.If != "steps.changes.outputs.code == 'true'" {
				t.Errorf("%s: step %q runs whatever the change touches (if: %q)", standard, step.Name, step.If)
			}
		}
	}
}

func equalEnv(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// The step's script, run against a temporary repository: the base commit has
// code, a README, a changelog and docs; each case commits a change on top,
// checks it out the way the workflow does (one commit deep) and reads the
// step's answer.
func TestStandardCheckSkipsChangesThatTouchNoCode(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	script := checkSteps(t, skippingStandards[0])[1].Run

	cases := []struct {
		name   string
		change func(t *testing.T, dir string)
		base   string // "" is the base commit; otherwise used as is
		code   bool
	}{
		{"changelog, docs and an image", func(t *testing.T, dir string) {
			write(t, dir, "CHANGELOG.md", "## v0.1.0\n")
			write(t, dir, "docs/x.md", "x\n")
			write(t, dir, "img.png", "png\n")
		}, "", false},
		{"a deleted doc and an image under src", func(t *testing.T, dir string) {
			git(t, dir, "rm", "-q", "docs/guide.md")
			write(t, dir, "src/logo.svg", "<svg/>\n")
		}, "", false},
		{"the README too", func(t *testing.T, dir string) {
			write(t, dir, "CHANGELOG.md", "## v0.1.0\n")
			write(t, dir, "README.md", "# app, changed\n")
		}, "", true},
		{"a source file too", func(t *testing.T, dir string) {
			write(t, dir, "docs/x.md", "x\n")
			write(t, dir, "main.go", "package main // changed\n")
		}, "", true},
		{"a source file moved into docs", func(t *testing.T, dir string) {
			git(t, dir, "mv", "main.go", "docs/main.go")
		}, "", true},
		{"a changelog below the root", func(t *testing.T, dir string) {
			write(t, dir, "pkg/CHANGELOG.md", "x\n")
		}, "", true},
		{"nothing", func(t *testing.T, dir string) {}, "", true},
		{"a first push", func(t *testing.T, dir string) {
			write(t, dir, "CHANGELOG.md", "## v0.1.0\n")
		}, strings.Repeat("0", 40), true},
		{"no base", func(t *testing.T, dir string) {
			write(t, dir, "CHANGELOG.md", "## v0.1.0\n")
		}, "-", true},
		{"a base the server lacks", func(t *testing.T, dir string) {
			write(t, dir, "CHANGELOG.md", "## v0.1.0\n")
		}, strings.Repeat("ab", 20), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			origin := filepath.Join(root, "origin")
			git(t, root, "init", "-q", "-b", "main", origin)
			git(t, origin, "config", "uploadpack.allowAnySHA1InWant", "true")
			write(t, origin, "main.go", "package main\n")
			write(t, origin, "README.md", "# app\n")
			write(t, origin, "CHANGELOG.md", "# Changelog\n")
			write(t, origin, "docs/guide.md", "guide\n")
			commit(t, origin)
			base := strings.TrimSpace(git(t, origin, "rev-parse", "HEAD"))
			c.change(t, origin)
			commit(t, origin)
			head := strings.TrimSpace(git(t, origin, "rev-parse", "HEAD"))

			work := filepath.Join(root, "work")
			git(t, root, "init", "-q", work)
			url := "file://" + origin
			git(t, work, "fetch", "-q", "--depth", "1", url, head)
			git(t, work, "checkout", "-q", "FETCH_HEAD")

			switch c.base {
			case "":
			case "-":
				base = ""
			default:
				base = c.base
			}
			output := filepath.Join(root, "output")
			cmd := exec.Command("sh", "-e", "-c", script)
			cmd.Dir = work
			cmd.Env = append(os.Environ(), "TOKEN=token", "URL="+url, "BASE="+base, "GITHUB_OUTPUT="+output)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("the step failed, which fails the check: %v\n%s", err, out)
			}
			got, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			want := "code=false\n"
			if c.code {
				want = "code=true\n"
			}
			if string(got) != want {
				t.Errorf("the step wrote %q; want %q", got, want)
			}
		})
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "change")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_ALLOW_PROTOCOL=file")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
