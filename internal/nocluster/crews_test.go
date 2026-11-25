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

package nocluster_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/nocluster"
)

// jsonFields are the JSON names of t's fields.
func jsonFields(t reflect.Type) []string {
	var out []string
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// #333: decision 6's table has a row for each field of CrewSpec and
// AgentSpec, and none for a field they lack: a field added to either fails
// here until someone decides whether each runtime honours it.
func TestEveryFieldHasARow333(t *testing.T) {
	for _, tt := range []struct {
		spec reflect.Type
		rows map[string]string
	}{
		{reflect.TypeFor[c4v1beta1.CrewSpec](), model.CrewFields},
		{reflect.TypeFor[c4v1beta1.AgentSpec](), model.AgentFields},
	} {
		fields := jsonFields(tt.spec)
		for _, f := range fields {
			if _, ok := tt.rows[f]; !ok {
				t.Errorf("%s.%s has no row in internal/model/runtime_fields.go: say whether each runtime "+
					"honours it", tt.spec.Name(), f)
			}
		}
		for f := range tt.rows {
			if !slices.Contains(fields, f) {
				t.Errorf("the row %s names no field of %s", f, tt.spec.Name())
			}
		}
	}
}

// withCrew is the webshop sample with its Crew and Agents
// (config/samples/webshop/crew.yaml) in model/setup.yaml.
func withCrew(t *testing.T) webshop {
	t.Helper()
	w := readWebshop(t)
	data, err := os.ReadFile(filepath.Join("..", "..", "config", "samples", "webshop", "crew.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	w.setup += "\n---\n" + string(data)
	return w
}

// #333: model/setup.yaml holds Crews and Agents, decoded as the other kinds,
// with what each document wrote.
func TestDecodeCrews333(t *testing.T) {
	m := decode(t, withCrew(t), "")
	if len(m.Crews) != 1 || len(m.Agents) != 2 || m.Crews[0].Namespace != "webshop" {
		t.Fatalf("Crews %+v, %d Agents", m.Crews, len(m.Agents))
	}
	// The CRD's defaults are set; what was written is kept apart.
	if m.Agents[1].Spec.TimeoutMinutes == 0 {
		t.Error("the reviewer's timeoutMinutes was not defaulted")
	}
	if got := m.Written["Agent reviewer"]; slices.Contains(got, "timeoutMinutes") ||
		!slices.Contains(got, "image") {
		t.Errorf("the reviewer wrote %v; want image and not the defaulted timeoutMinutes", got)
	}
}

// #333: a Crew naming a Container or an Agent the model files don't hold is
// refused, naming the field.
func TestDecodeRefusesACrewsMissingRefs333(t *testing.T) {
	for _, tt := range []struct {
		name, from, to, want string
	}{
		{"no Container", "  containerRef:\n    name: orders-api", "  containerRef:\n    name: payments",
			"Crew orders-api: spec.containerRef names Container payments, which model/model.yaml does not hold"},
		{"no reviewer", "  reviewer:\n    name: reviewer\n", "  reviewer:\n    name: critic\n",
			"Crew orders-api: spec.reviewer names Agent critic, which model/setup.yaml does not hold"},
		{"no route's implementer", "  parallel: 2\n", "  parallel: 2\n  routes:\n  - label: kind/docs\n" +
			"    implementer:\n      name: writer\n",
			"Crew orders-api: spec.routes[0].implementer names Agent writer"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := withCrew(t)
			if !strings.Contains(w.setup, tt.from) {
				t.Fatalf("the sample has no %q", tt.from)
			}
			w.setup = strings.Replace(w.setup, tt.from, tt.to, 1)
			_, err := nocluster.Decode([]byte(w.story), []byte(w.setup), nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

// #333: theos status names, for each Crew, every field it or its Agents set
// that the lead runtime does not honour, with its reason word, and each
// Container no Crew works.
func TestStatusNamesWhatIsNotHonoured333(t *testing.T) {
	w := newWorld(t)
	w.m = decode(t, withCrew(t), "")
	if err := nocluster.Status(context.Background(), w.env, w.m); err != nil {
		t.Fatal(err)
	}
	out := w.out.String()
	for _, line := range []string{
		"Crew orders-api works orders-api; what this runtime does not honour of it and its Agents:\n",
		"  Crew orders-api: " + model.ReasonPersonMerges + ": ",
		"  Agent implementer: spec.image: " + model.ReasonNoSandbox + ": ",
		"  Agent implementer: spec.timeoutMinutes: " + model.ReasonNoSandbox + ": ",
		"  Agent reviewer: spec.image: " + model.ReasonNoSandbox + ": ",
		"storefront: no Crew works its issues; add one to model/setup.yaml\n",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("status does not say %q:\n%s", line, out)
		}
	}
	for _, honoured := range []string{"spec.model", "spec.maxRounds", "spec.parallel", "spec.harness"} {
		if strings.Contains(out, honoured) {
			t.Errorf("status names %s, which the lead runtime honours:\n%s", honoured, out)
		}
	}
}

// #333: init writes the chart's implementer and reviewer on Claude Code at
// the release's agent image, and a Crew to uncomment once the model has a
// Container; the file decodes, and so does the Crew once uncommented.
func TestInitWritesAgents333(t *testing.T) {
	const theosURL, tag = "https://github.com/JairJosafath/theos", "v0.9.0"
	files, err := nocluster.Init(context.Background(), chartFetcher{t: t, url: theosURL, ref: tag},
		nocluster.InitOptions{Project: "shop", Remote: "git@github.com:acme/theos-shop.git", TheosURL: theosURL,
			Ref: tag})
	if err != nil {
		t.Fatal(err)
	}
	m, err := nocluster.Decode(files[nocluster.ModelFile], files[nocluster.SetupFile], files[nocluster.SettingsFile])
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Agents) != 2 || len(m.Crews) != 0 {
		t.Fatalf("%d Agents, %d Crews; want the two Agents and the Crew left a comment", len(m.Agents), len(m.Crews))
	}
	for i, name := range []string{"implementer", "reviewer"} {
		a := m.Agents[i]
		if a.Name != name || a.Spec.Harness != c4v1beta1.HarnessClaudeCode || a.Spec.Model == "" ||
			a.Spec.Image != "ghcr.io/jairjosafath/theos/agent-go:0.9.0" {
			t.Errorf("Agent %d = %s %+v; want %s on claude-code at the release's agent image", i, a.Name, a.Spec, name)
		}
	}
	// Uncommented, with a Container in the story, the Crew decodes.
	var crew []string
	for line := range strings.Lines(string(files[nocluster.SetupFile])) {
		if after, ok := strings.CutPrefix(line, "# "); ok && len(crew) > 0 || ok && strings.HasPrefix(after, "apiVersion") {
			crew = append(crew, strings.ReplaceAll(after, "<container>", "orders-api"))
		}
	}
	if len(crew) == 0 {
		t.Fatal("init wrote no Crew to uncomment")
	}
	w := readWebshop(t)
	setup := bytes.ReplaceAll(files[nocluster.SetupFile], []byte("namespace: shop"), nil)
	story := strings.ReplaceAll(w.story, "name: webshop\n", "name: shop\n")
	story = strings.ReplaceAll(story, "namespace: webshop\n", "")
	if _, err := nocluster.Decode([]byte(story), append(append(setup, []byte("---\n")...),
		[]byte(strings.Join(crew, ""))...), files[nocluster.SettingsFile]); err != nil {
		t.Errorf("the uncommented Crew does not decode: %v\n%s", err, strings.Join(crew, ""))
	}
}

// #266: theos agents writes the lead's files for the model's Crew; a second
// run writes nothing; theos status names one a person changed, and one
// that's gone. Nothing goes into a Container repository: the files are the
// project repository's.
func TestAgents266(t *testing.T) {
	m := decode(t, withCrew(t), "")
	dir := t.TempDir()
	write := func(path string, data []byte) error {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		return os.WriteFile(full, data, 0o600)
	}
	var out bytes.Buffer
	if err := nocluster.Agents(&out, m, os.DirFS(dir), write); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"CLAUDE.md", "AGENTS.md", ".claude/agents/theos-implementer.md",
		".claude/agents/theos-reviewer.md", ".mcp.json"} {
		if !strings.Contains(out.String(), path+": written\n") {
			t.Errorf("theos agents did not write %s:\n%s", path, out.String())
		}
	}
	impl, err := os.ReadFile(filepath.Join(dir, ".claude", "agents", "theos-implementer.md"))
	if err != nil || !strings.Contains(string(impl), "model: claude-sonnet-5-5\n") {
		t.Errorf("the implementer's subagent = %q, %v; want its Agent's model", impl, err)
	}
	out.Reset()
	if err := nocluster.Agents(&out, m, os.DirFS(dir), write); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "written") || strings.Count(out.String(), ": unchanged\n") != 5 {
		t.Errorf("a second theos agents:\n%s\nwant every file unchanged", out.String())
	}

	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# By hand\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, ".mcp.json")); err != nil {
		t.Fatal(err)
	}
	w := newWorld(t)
	w.m = m
	w.env.Project = os.DirFS(dir)
	if err := nocluster.Status(context.Background(), w.env, w.m); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"AGENTS.md: changed since theos agents wrote it; theos agents writes it again\n",
		".mcp.json: not written; theos agents writes it\n"} {
		if !strings.Contains(w.out.String(), line) {
			t.Errorf("status does not say %q:\n%s", line, w.out.String())
		}
	}
	if strings.Contains(w.out.String(), "CLAUDE.md:") {
		t.Errorf("status names CLAUDE.md, which is as written:\n%s", w.out.String())
	}
}

// #266: with no Crew there is nothing for a lead to hand out.
func TestAgentsNeedACrew266(t *testing.T) {
	err := nocluster.Agents(&bytes.Buffer{}, decode(t, readWebshop(t), ""), os.DirFS(t.TempDir()),
		func(string, []byte) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "holds no Crew") {
		t.Errorf("err = %v, want no Crew refused", err)
	}
}
