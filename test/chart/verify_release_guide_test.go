/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package chart

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/theos/internal/model"
)

const (
	guideBriefSection = "### Without a token"
	verifyBriefStep   = "Getting started: a Brief without a token"
)

// TestVerifyReleaseFollowsTheGuidesBrief holds Verify Release's Brief step to
// docs/getting-started.md's "Without a token" (#1083), so a device's Brief
// reaches a release with the published chart and the guide alone: the step
// runs every command of the section, in order, kubectl as $KUBECTL, from the
// guide's clone; it changes the cluster and Gitea with nothing else; the
// sample the guide applies is the Brief and the Crew it waits on; and the
// script parses.
func TestVerifyReleaseFollowsTheGuidesBrief(t *testing.T) {
	root := filepath.Join("..", "..")
	commands := guideCommands(t, readFile(t, root, "docs", "getting-started.md"), guideBriefSection)
	workflow := readFile(t, root, ".github", "workflows", "verify-release.yml")
	step := workflowStep(t, workflow, "verify-release", verifyBriefStep)
	if step.WorkingDirectory != "guide" {
		t.Errorf("step %q runs in %q; want guide, the guide's clone, where its paths hold",
			verifyBriefStep, step.WorkingDirectory)
	}

	// A line continued with a backslash is checked whole.
	lines := strings.Split(strings.ReplaceAll(step.Run, "\\\n", " "), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	// runs reports whether line runs command, kubectl as $KUBECTL, alone or
	// with what to print when it fails.
	runs := func(line, command string) bool {
		if rest, ok := strings.CutPrefix(command, "kubectl "); ok {
			command = "$KUBECTL " + rest
		}
		rest, ok := strings.CutPrefix(line, command)
		return ok && (rest == "" || strings.HasPrefix(rest, " ||"))
	}
	at := 0
	for _, c := range commands {
		i := slices.IndexFunc(lines[at:], func(l string) bool { return runs(l, c) })
		if i < 0 {
			t.Errorf("step %q doesn't run the guide's %q, as $KUBECTL, after the commands before it", verifyBriefStep, c)
			continue
		}
		at += i + 1
	}

	for _, line := range lines {
		if strings.Contains(" "+line, " kubectl ") {
			t.Errorf("step %q runs kubectl itself, not $KUBECTL: %s", verifyBriefStep, line)
		}
		if curlWrite.MatchString(line) {
			t.Errorf("step %q writes to Gitea, which the guide doesn't: %s", verifyBriefStep, line)
		}
		if !kubectlWrites(line) {
			continue
		}
		if !slices.ContainsFunc(commands, func(c string) bool { return runs(line, c) }) {
			t.Errorf("step %q changes the cluster with a command the guide's %q doesn't have: %s",
				verifyBriefStep, guideBriefSection, line)
		}
	}

	var sample, brief, crew string
	for _, c := range commands {
		if m := regexp.MustCompile(`^kubectl apply -f (\S+)$`).FindStringSubmatch(c); m != nil {
			sample = m[1]
		}
		if m := regexp.MustCompile(` wait brief/(\S+) `).FindStringSubmatch(c); m != nil {
			brief = m[1]
		}
		if m := regexp.MustCompile(` wait crew/(\S+) `).FindStringSubmatch(c); m != nil {
			crew = m[1]
		}
	}
	if sample == "" || brief == "" || crew == "" {
		t.Fatalf("%q applies %q, waits for Brief %q and Crew %q; want all three", guideBriefSection, sample, brief, crew)
	}
	var obj struct {
		Kind     string `yaml:"kind"`
		Metadata struct{ Name, Namespace string }
		Spec     struct {
			Agents map[string]struct{ Name string } `yaml:"agents"`
		}
	}
	if err := yaml.Unmarshal([]byte(readFile(t, root, sample)), &obj); err != nil {
		t.Fatalf("%s: %v", sample, err)
	}
	if obj.Kind != "Brief" || obj.Metadata.Name != brief {
		t.Errorf("%s is %s %s; the guide waits for Brief %s", sample, obj.Kind, obj.Metadata.Name, brief)
	}
	// The stand-in names its Container, and the Crew with it, after the Brief.
	if want := strings.TrimPrefix(brief, model.BriefRepository("")); crew != want {
		t.Errorf("the guide waits for Crew %s; the stand-in names Brief %s's Container and Crew %s", crew, brief, want)
	}
	for _, role := range []string{"planner", "implementer", "reviewer"} {
		if obj.Spec.Agents[role].Name != "stand-in" {
			t.Errorf("%s names Agent %q as its %s; want stand-in, as the guide says", sample, obj.Spec.Agents[role].Name, role)
		}
	}

	bash := exec.Command("bash", "-n")
	bash.Stdin = strings.NewReader(step.Run)
	if out, err := bash.CombinedOutput(); err != nil {
		t.Errorf("step %q doesn't parse: %v\n%s", verifyBriefStep, err, out)
	}
}

// TestStandInReleasesOnTheChartsCatalog holds the guide's "Without a token" to
// what the stand-in plans on the chart's catalog: the Framework and the
// Deployment the guide names, and a Container it releases (#1083).
func TestStandInReleasesOnTheChartsCatalog(t *testing.T) {
	root := filepath.Join("..", "..")
	var values struct {
		Catalog struct {
			Entries []struct {
				Kind string
				Name string
				Spec struct {
					SupportedTypes      []string `yaml:"supportedTypes"`
					SupportedFrameworks []string `yaml:"supportedFrameworks"`
					DeploysItself       bool     `yaml:"deploysItself"`
				}
			}
		}
	}
	if err := yaml.Unmarshal([]byte(readFile(t, root, "dist", "chart", "values.yaml")), &values); err != nil {
		t.Fatal(err)
	}
	catalog := make([]model.CatalogEntry, 0, len(values.Catalog.Entries))
	for _, e := range values.Catalog.Entries {
		catalog = append(catalog, model.CatalogEntry{Side: e.Kind, Name: e.Name, SupportedTypes: e.Spec.SupportedTypes,
			SupportedFrameworks: e.Spec.SupportedFrameworks, DeploysItself: e.Spec.DeploysItself})
	}
	workspace, err := model.BriefWorkspaceFiles(model.BriefWorkspace{Brief: "stand-in-demo", Idea: "A small tool.",
		Namespace: "webshop", GitProvider: "gitea", Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	files, err := model.StandInPlan(workspace)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := model.ParsePlan(files)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(plan.Objects, func(o model.PlanObject) bool { return o.Kind == model.KindContainer })
	if i < 0 {
		t.Fatalf("the stand-in adds no Container on the chart's catalog: %v", files)
	}
	container := plan.Objects[i]
	if container.Deployment == "" || !slices.Contains(plan.Releases, container.Name) {
		t.Fatalf("on the chart's catalog the stand-in adds %s on %s with Deployment %q and releases %v; "+
			"want a Deployment and the Container released",
			container.Name, container.Framework, container.Deployment, plan.Releases)
	}
	section := guideSection(t, readFile(t, root, "docs", "getting-started.md"), guideBriefSection)
	if says := "`" + container.Framework + "` with\n`" + container.Deployment + "`"; !strings.Contains(section, says) &&
		!strings.Contains(section, strings.ReplaceAll(says, "\n", " ")) {
		t.Errorf("%q doesn't say the stand-in takes %s with %s, as it does on the chart's catalog",
			guideBriefSection, container.Framework, container.Deployment)
	}
}

// TestVerifyReleaseMakesNoSharedPullSecret holds Verify Release to
// docs/getting-started.md's "Registry login and the pull secret": a project
// needs no pull secret of its own, the operator copies the chart's into it.
// No step makes a pull secret of the installed values in a project namespace,
// for loops included; one that did failed v0.24.0's check once the
// operator's copy was there first (#1271).
func TestVerifyReleaseMakesNoSharedPullSecret(t *testing.T) {
	root := filepath.Join("..", "..")
	workflow := readFile(t, root, ".github", "workflows", "verify-release.yml")
	install := workflowStep(t, workflow, "verify-release", "Install the chart")
	_, values, ok := strings.Cut(install.Run, "<<'EOF'\n")
	if ok {
		values, _, ok = strings.Cut(values, "\nEOF")
	}
	if !ok {
		t.Fatalf("step %q writes no values with <<'EOF'", install.Name)
	}
	var v struct {
		Gitea struct {
			Projects []string `yaml:"projects"`
		} `yaml:"gitea"`
		Global struct {
			ImagePullSecrets []struct {
				Name string `yaml:"name"`
			} `yaml:"imagePullSecrets"`
		} `yaml:"global"`
	}
	if err := yaml.Unmarshal([]byte(values), &v); err != nil {
		t.Fatal(err)
	}
	secrets := make([]string, 0, len(v.Global.ImagePullSecrets))
	for _, s := range v.Global.ImagePullSecrets {
		secrets = append(secrets, s.Name)
	}
	if len(v.Gitea.Projects) == 0 || len(secrets) == 0 {
		t.Fatalf("step %q's values name no projects or no global.imagePullSecrets", install.Name)
	}

	forLoop := regexp.MustCompile(`\bfor (\w+) in ([^;]*); do`)
	namespace := regexp.MustCompile(`(?:-n|--namespace)[ =](\S+)`)
	createSecret := regexp.MustCompile(`\$KUBECTL\b.*\bcreate secret \S+ (\S+)`)
	for _, step := range workflowSteps(t, workflow, "verify-release") {
		loops := map[string][]string{}
		for line := range strings.SplitSeq(strings.ReplaceAll(step.Run, "\\\n", " "), "\n") {
			if m := forLoop.FindStringSubmatch(line); m != nil {
				loops[m[1]] = strings.Fields(m[2])
			}
			m := createSecret.FindStringSubmatch(line)
			n := namespace.FindStringSubmatch(line)
			if m == nil || n == nil || !slices.Contains(secrets, strings.Trim(m[1], `"'`)) {
				continue
			}
			ns := strings.Trim(n[1], `"'`)
			namespaces := []string{ns}
			if name, isVar := strings.CutPrefix(ns, "$"); isVar {
				namespaces = loops[strings.Trim(name, "{}")]
			}
			for _, ns := range namespaces {
				if slices.Contains(v.Gitea.Projects, ns) {
					t.Errorf("step %q makes pull secret %s in project namespace %s; the operator copies it there: %s",
						step.Name, m[1], ns, strings.TrimSpace(line))
				}
			}
		}
	}
}

// curlWrite is a curl that sends something.
var curlWrite = regexp.MustCompile(`\bcurl\b.*\s` +
	`(-X\S*|--request|-d\S*|--data\S*|-F\S*|--form\S*|-T\S*|--upload-file)(\s|$)`)

// kubectlWrites reports whether a $KUBECTL on line runs a verb that changes
// the cluster.
func kubectlWrites(line string) bool {
	reads := []string{"get", "wait", "describe", "logs", "top", "diff", "explain", "api-resources", "version",
		"port-forward"}
	_, after, found := strings.Cut(line, "$KUBECTL")
	for found {
		fields := strings.Fields(after)
		for j := 0; j < len(fields); j++ {
			f := strings.Trim(fields[j], `"'`)
			if strings.HasPrefix(f, "-") {
				if f == "-n" || f == "--namespace" || f == "--context" {
					j++
				}
				continue
			}
			if !slices.Contains(reads, f) {
				return true
			}
			break
		}
		_, after, found = strings.Cut(after, "$KUBECTL")
	}
	return false
}

// guideSection is the guide's section under heading, up to the next heading
// of any level outside a code block.
func guideSection(t *testing.T, guide, heading string) string {
	t.Helper()
	_, section, ok := strings.Cut(guide, "\n"+heading+"\n")
	if !ok {
		t.Fatalf("docs/getting-started.md has no %q", heading)
	}
	var kept []string
	fenced := false
	for line := range strings.SplitSeq(section, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		} else if !fenced && strings.HasPrefix(line, "#") {
			break
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// guideCommands are the lines of the section's bash blocks.
func guideCommands(t *testing.T, guide, heading string) []string {
	t.Helper()
	var commands []string
	blocks := regexp.MustCompile("(?s)```bash\n(.*?)```").FindAllStringSubmatch(guideSection(t, guide, heading), -1)
	for _, block := range blocks {
		for line := range strings.SplitSeq(strings.TrimSpace(block[1]), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				commands = append(commands, line)
			}
		}
	}
	if len(commands) == 0 {
		t.Fatalf("%q has no commands", heading)
	}
	return commands
}

type workflowStepYAML struct {
	Name             string `yaml:"name"`
	WorkingDirectory string `yaml:"working-directory"`
	Run              string `yaml:"run"`
}

// workflowStep is the step named name of the workflow's job.
func workflowStep(t *testing.T, workflow, job, name string) workflowStepYAML {
	t.Helper()
	for _, s := range workflowSteps(t, workflow, job) {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("job %s has no step %q", job, name)
	return workflowStepYAML{}
}

// workflowSteps are the steps of the workflow's job.
func workflowSteps(t *testing.T, workflow, job string) []workflowStepYAML {
	t.Helper()
	var w struct {
		Jobs map[string]struct {
			Steps []workflowStepYAML `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(workflow), &w); err != nil {
		t.Fatal(err)
	}
	if len(w.Jobs[job].Steps) == 0 {
		t.Fatalf("workflow has no job %s", job)
	}
	return w.Jobs[job].Steps
}

func readFile(t *testing.T, root string, path ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{root}, path...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestVerifyReleaseFailsAStandInThatStoppedReleasing runs the Brief step's
// script on a stub kubectl whose Brief plans at once and whose Crew lacks
// spec.tagReleases (#1240). Before v0.24.0 the stand-in released nothing, so
// the step stops at the Crew's first Task and passes; from v0.24.0 on it
// fails and prints the Crew's spec, so a regression that loses the Crew's
// tagReleases (the sample, the stand-in's plan or the Brief's Crew settings)
// can't pass Verify Release at the first Task. A Crew that releases is
// followed to its release run.
func TestVerifyReleaseFailsAStandInThatStoppedReleasing(t *testing.T) {
	root := filepath.Join("..", "..")
	workflow := readFile(t, root, ".github", "workflows", "verify-release.yml")
	step := workflowStep(t, workflow, "verify-release", verifyBriefStep)
	dir := t.TempDir()
	script := filepath.Join(dir, "step.sh")
	if err := os.WriteFile(script, []byte(step.Run), 0o600); err != nil {
		t.Fatal(err)
	}
	// The step's reads, answered: the Brief is Planned with Container and Crew
	// stand-in-demo and issue 7, the Crew's tagReleases is $STUB_TAG_RELEASES,
	// its first Task exists, and its release run has Failed. Writes and waits
	// succeed silently.
	kubectl := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(kubectl, []byte(`#!/usr/bin/env bash
case "$*" in
  *'{.status.phase}'*) printf Planned ;;
  *'kind=="Container"'*|*'kind=="Crew"'*) printf stand-in-demo ;;
  *'issues[0].number'*) printf 7 ;;
  *'{.spec.tagReleases}'*) printf '%s' "$STUB_TAG_RELEASES" ;;
  *'{.spec}'*) printf '{"repository":"webshop/stand-in-demo","tagReleases":false}' ;;
  *'{.status.lastRelease.run}'*) printf Failed ;;
  *'get tasks'*) echo task.theos.factory.io/stand-in-demo-7 ;;
esac
`), 0o700); err != nil {
		t.Fatal(err)
	}
	guide := filepath.Join(dir, "guide")
	if err := os.MkdirAll(filepath.Join(guide, "config", "samples", "brief"), 0o755); err != nil {
		t.Fatal(err)
	}
	sample := filepath.Join(guide, "config", "samples", "brief", "stand-in.yaml")
	if err := os.WriteFile(sample, []byte("kind: Brief\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		tag, tagReleases string
		fails            bool
		says             string
	}{
		{"v0.23.0", "", false, "stops at the Crew's first Task"},
		{"v0.24.0", "", true, "::error::v0.24.0's stand-in releases"},
		{"v0.24.1", "", true, "::error::v0.24.1's stand-in releases"},
		{"v0.30.0", "", true, `"repository": "webshop/stand-in-demo"`},
		{"v1.0.0", "", true, "::error::v1.0.0's stand-in releases"},
		{"v0.30.0", "true", true, "crew/stand-in-demo is Failed"},
	}
	for _, c := range cases {
		t.Run(c.tag+"/tagReleases="+c.tagReleases, func(t *testing.T) {
			cmd := exec.Command("bash", "-eo", "pipefail", script)
			cmd.Dir = guide
			cmd.Env = append(os.Environ(), "KUBECTL="+kubectl, "TAG="+c.tag, "RUNNER_TEMP="+t.TempDir(),
				"STUB_TAG_RELEASES="+c.tagReleases)
			out, err := cmd.CombinedOutput()
			if (err != nil) != c.fails {
				t.Errorf("tag %s, Crew tagReleases %q: exit %v; want failure %v\n%s", c.tag, c.tagReleases, err, c.fails, out)
			}
			if !strings.Contains(string(out), c.says) {
				t.Errorf("tag %s, Crew tagReleases %q: the step doesn't say %q\n%s", c.tag, c.tagReleases, c.says, out)
			}
			if c.tag != "v0.23.0" && strings.Contains(string(out), "stops at the Crew's first Task") {
				t.Errorf("tag %s: the step stops at the first Task, which only a tag before v0.24.0 may\n%s", c.tag, out)
			}
		})
	}
}
