package scaffold

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/theos/internal/model"
)

// The Standards that can run the full check only before a release
// (docs/plans/done/checks-before-release.md): their check.yaml renders the
// Container's effective checks and their Makefile has check-light.
var checksBeforeReleaseStandards = []string{"go-standard", "go-operator-standard", "ts-standard", "python-standard"}

type workflowJob struct {
	If     string            `yaml:"if"`
	RunsOn string            `yaml:"runs-on"`
	Env    map[string]string `yaml:"env"`
	Steps  []checkStep       `yaml:"steps"`
}

type workflow struct {
	// On maps each trigger to its filters; pull_request has none.
	On   map[string]struct{ Branches []string } `yaml:"on"`
	Jobs map[string]workflowJob                 `yaml:"jobs"`
}

// standardCombo is the first row of templateCombos that picks standard.
func standardCombo(t *testing.T, standard string) (string, []side) {
	t.Helper()
	for _, c := range templateCombos {
		if slices.Contains(c.layers, side{model.SideStandard, standard}) {
			return c.typ, c.layers
		}
	}
	t.Fatalf("no row of templateCombos picks %s", standard)
	return "", nil
}

func renderCheckYAML(t *testing.T, standard, checks string) []byte {
	t.Helper()
	typ, sides := standardCombo(t, standard)
	_, result := renderComboChecks(t, typ, sides, checks)
	file, ok := result.Files[".gitea/workflows/check.yaml"]
	if !ok {
		t.Fatalf("%s writes no .gitea/workflows/check.yaml", standard)
	}
	return file.Data
}

func renderWorkflow(t *testing.T, standard, checks string) workflow {
	t.Helper()
	var w workflow
	if err := yaml.Unmarshal(renderCheckYAML(t, standard, checks), &w); err != nil {
		t.Fatalf("%s: %v", standard, err)
	}
	return w
}

// With EveryChange, or no choice, check.yaml is the file it was before it
// became a template, byte for byte: a Standard bump changes nothing for an
// app that didn't choose.
func TestStandardsRenderEveryChangeAsBefore(t *testing.T) {
	for _, standard := range checksBeforeReleaseStandards {
		want, err := os.ReadFile(filepath.Join("testdata", "check-every-change", standard+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, checks := range []string{"", model.ChecksEveryChange} {
			if got := renderCheckYAML(t, standard, checks); string(got) != string(want) {
				t.Errorf("%s with checks %q renders check.yaml differently from before:\n%s", standard, checks, got)
			}
		}
	}
}

// With BeforeRelease, the job check runs make check-light on pull requests
// and main, keeping the skip of changes that touch no code, and the job full
// runs make check on a push to release-check/**, whatever the push changes,
// with the same setup as check.
func TestStandardsRenderBeforeRelease(t *testing.T) {
	const onRelease = "startsWith(gitea.ref, 'refs/heads/release-check/')"
	const skip = "steps.changes.outputs.code == 'true'"
	for _, standard := range checksBeforeReleaseStandards {
		t.Run(standard, func(t *testing.T) {
			w := renderWorkflow(t, standard, model.ChecksBeforeRelease)
			if got := w.On["push"].Branches; !slices.Equal(got, []string{"main", "release-check/**"}) {
				t.Errorf("push branches %v; want main and release-check/**", got)
			}
			if _, ok := w.On["pull_request"]; !ok || len(w.On) != 2 {
				t.Errorf("triggers %v; want push and pull_request", w.On)
			}
			if len(w.Jobs) != 2 {
				t.Errorf("jobs %v; want check and full", w.Jobs)
			}
			check, full := w.Jobs["check"], w.Jobs["full"]
			if check.If != "${{ !"+onRelease+" }}" {
				t.Errorf("check runs if %q; want everywhere but a release-check branch", check.If)
			}
			if full.If != onRelease {
				t.Errorf("full runs if %q; want only on a release-check branch", full.If)
			}
			if full.RunsOn != check.RunsOn || !equalEnv(full.Env, check.Env) {
				t.Errorf("full runs on %q with env %v; want check's, %q with %v", full.RunsOn, full.Env, check.RunsOn, check.Env)
			}
			if len(check.Steps) < 3 || check.Steps[1].ID != "changes" {
				t.Fatalf("check's steps %+v; want the checkout, then the changes step", check.Steps)
			}
			last := check.Steps[len(check.Steps)-1]
			if last.Name != "make check-light" || lastLine(last.Run) != "make check-light" || last.If != skip {
				t.Errorf("check's last step %+v; want make check-light when the change touches code", last)
			}
			// full is check without the changes step, its skip, and with make check.
			want := slices.Delete(slices.Clone(check.Steps), 1, 2)
			for i := range want {
				want[i].If = ""
			}
			want[len(want)-1].Name = "make check"
			want[len(want)-1].Run = strings.Replace(want[len(want)-1].Run, "make check-light", "make check", 1)
			if !slices.EqualFunc(full.Steps, want, equalStep) {
				t.Errorf("full's steps:\n%+v\nwant check's, without the changes step and skip, ending in make check:\n%+v",
					full.Steps, want)
			}
		})
	}
}

func equalStep(a, b checkStep) bool {
	return a.Name == b.Name && a.ID == b.ID && a.If == b.If && a.Run == b.Run && equalEnv(a.Env, b.Env)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// Each Standard's Makefile, in every combination it is rendered in, has
// check-light: a part of check without its test suites.
func TestStandardsHaveACheckLight(t *testing.T) {
	prereqs := func(makefile, target string) []string {
		m := regexp.MustCompile(`(?m)^` + target + `:([^#\n]*)`).FindStringSubmatch(makefile)
		if m == nil {
			return nil
		}
		return strings.Fields(m[1])
	}
	for _, c := range templateCombos {
		for _, l := range c.layers {
			if l.Side != model.SideStandard || !slices.Contains(checksBeforeReleaseStandards, l.Name) {
				continue
			}
			_, result := renderCombo(t, c.typ, c.layers)
			makefile := string(result.Files["Makefile"].Data)
			check, light := prereqs(makefile, "check"), prereqs(makefile, "check-light")
			if len(light) == 0 {
				t.Errorf("%s (%s): the Makefile has no check-light", l.Name, c.name)
				continue
			}
			if slices.Contains(light, "test") || !slices.Contains(light, "lint") {
				t.Errorf("%s (%s): check-light runs %v; want lint and no test", l.Name, c.name, light)
			}
			for _, p := range light {
				if !slices.Contains(check, p) && p != "build" {
					t.Errorf("%s (%s): check-light runs %s, which check doesn't", l.Name, c.name, p)
				}
			}
			if !strings.Contains(makefile, ".PHONY: check-light") && !regexp.MustCompile(`\.PHONY:.* check-light `).MatchString(makefile) {
				t.Errorf("%s (%s): check-light is not .PHONY", l.Name, c.name)
			}
		}
	}
}
