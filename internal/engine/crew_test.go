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

package engine_test

import (
	"context"
	"errors"
	"go/build"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/engine"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

const (
	owner     = "acme"
	container = "orders-api"
	// criteria is a body agents may take: it has acceptance criteria.
	criteria = "## Goal\n\nDo it.\n\n## Acceptance criteria\n\n- [ ] It works\n"
)

// crewLabels are the labels the tests' Crew keeps.
var crewLabels = []ports.Label{{Name: model.LabelReady}, {Name: model.LabelNeedsHuman},
	{Name: model.LabelNeedsSpec}}

// crewHost is a fake host with the Container's repository, and a runtime on
// a fresh fake store.
func crewHost(t *testing.T) (*fake.Git, ports.Repository, *fake.Runtime) {
	t.Helper()
	g := fake.New().For(ports.Config{URL: "http://host.test", Owner: owner,
		Credentials: ports.Credentials{Username: "operator", Token: "operator-token"}})
	repo, err := g.EnsureRepository(context.Background(), container, "the engine's tests")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.EnsureLabels(context.Background(), repo, crewLabels); err != nil {
		t.Fatal(err)
	}
	return g, repo, fake.NewRuntime()
}

// tick runs one Crew tick with parallel, failing t on an error.
func tick(t *testing.T, rt ports.Runtime, host ports.Issues, repo ports.Repository,
	parallel int) engine.CrewResult {
	t.Helper()
	res, err := engine.CrewTick(context.Background(), rt, host, crewInput(repo, parallel))
	if err != nil {
		t.Fatalf("CrewTick: %v", err)
	}
	return res
}

func crewInput(repo ports.Repository, parallel int) engine.CrewInput {
	return engine.CrewInput{Container: container, Repo: repo, Labels: crewLabels,
		Facts: model.CrewFacts{ContainerReady: true, Parallel: parallel}}
}

func started(rt *fake.Runtime) []int {
	starts := rt.TaskStore.Starts()
	issues := make([]int, 0, len(starts))
	for _, s := range starts {
		issues = append(issues, s.Issue)
	}
	return issues
}

// #332: a ready issue with acceptance criteria starts a Task in the store.
func TestCrewTickStarts332(t *testing.T) {
	g, repo, rt := crewHost(t)
	n := g.AddIssue(owner, container, "Add orders", criteria, []string{model.LabelReady})
	res := tick(t, rt, g, repo, 1)
	if len(res.Step.Start) != 1 || res.Step.Start[0].Issue != n {
		t.Fatalf("Start = %+v, want issue #%d", res.Step.Start, n)
	}
	tasks, _ := rt.TaskStore.Tasks(context.Background(), container)
	if len(tasks) != 1 || tasks[0].Name != res.Step.Start[0].Task || tasks[0].Issue != n {
		t.Errorf("the store holds %+v, want the Task started for #%d", tasks, n)
	}
	if res.Step.Phase != model.CrewWorking {
		t.Errorf("Phase = %s, want %s", res.Step.Phase, model.CrewWorking)
	}
}

// #332: parallel caps the Tasks at work, across ticks; the issue left over
// waits for a free place.
func TestCrewTickParallel332(t *testing.T) {
	g, repo, rt := crewHost(t)
	issues := make([]int, 0, 3)
	for _, title := range []string{"one", "two", "three"} {
		issues = append(issues, g.AddIssue(owner, container, title, criteria, []string{model.LabelReady}))
	}
	res := tick(t, rt, g, repo, 2)
	if got := started(rt); !slices.Equal(got, issues[:2]) {
		t.Fatalf("started %v, want the two lowest, %v", got, issues[:2])
	}
	if !slices.Equal(res.Step.Ready, issues[2:]) {
		t.Errorf("Ready = %v, want %v waiting for a place", res.Step.Ready, issues[2:])
	}
	// The issues stay labelled ready: the store's Tasks hold them.
	res = tick(t, rt, g, repo, 2)
	if len(res.Step.Start) != 0 || len(rt.TaskStore.Starts()) != 2 {
		t.Errorf("a second tick started %+v, want nothing while two Tasks work", res.Step.Start)
	}
	// One ends: its place goes to the third.
	tasks, _ := rt.TaskStore.Tasks(context.Background(), container)
	done := tasks[0]
	done.Final = true
	rt.TaskStore.Set(container, done)
	if err := g.CloseIssueByHand(owner, container, done.Issue); err != nil {
		t.Fatal(err)
	}
	tick(t, rt, g, repo, 2)
	if got := started(rt); !slices.Equal(got, issues) {
		t.Errorf("started %v, want %v once a place came free", got, issues)
	}
}

// #332: an issue whose dependency is open waits, and starts nothing.
func TestCrewTickBlocked332(t *testing.T) {
	g, repo, rt := crewHost(t)
	blocker := g.AddIssue(owner, container, "First", "", nil)
	n := g.AddIssue(owner, container, "Then", criteria, []string{model.LabelReady})
	if err := g.AddIssueDependency(context.Background(), repo, n, repo, blocker); err != nil {
		t.Fatal(err)
	}
	res := tick(t, rt, g, repo, 1)
	if len(res.Step.Start) != 0 || !slices.Equal(res.Step.Waiting, []int{n}) {
		t.Errorf("Start = %+v, Waiting = %v, want #%d waiting", res.Step.Start, res.Step.Waiting, n)
	}
}

// #332: an issue with no acceptance criteria goes back to a person:
// status/needs-human with the reason, out of status/ready.
func TestCrewTickHandsBack332(t *testing.T) {
	g, repo, rt := crewHost(t)
	n := g.AddIssue(owner, container, "Vague", "Make it better.", []string{model.LabelReady})
	res := tick(t, rt, g, repo, 1)
	if len(res.Step.Start) != 0 || len(res.HandBack) != 0 {
		t.Fatalf("Start = %+v, HandBack = %+v, want no start and no failure", res.Step.Start, res.HandBack)
	}
	issue := g.Issue(owner, container, n)
	if !slices.Contains(issue.Labels, model.LabelNeedsHuman) || slices.Contains(issue.Labels, model.LabelReady) {
		t.Errorf("labels = %v, want %s and not %s", issue.Labels, model.LabelNeedsHuman, model.LabelReady)
	}
	if len(issue.Comments) != 1 || !strings.Contains(issue.Comments[0], "Acceptance criteria") {
		t.Errorf("comments = %q, want the reason", issue.Comments)
	}
	// Handed back, it is no longer read: a second tick writes nothing more.
	tick(t, rt, g, repo, 1)
	if got := g.Issue(owner, container, n).Comments; len(got) != 1 {
		t.Errorf("comments after a second tick = %q, want the one", got)
	}
}

// #332: a second tick on the same facts starts nothing new, though the host
// still labels the issue ready, as a Task started a moment ago finds it.
func TestCrewTickRepeats332(t *testing.T) {
	g, repo, rt := crewHost(t)
	g.AddIssue(owner, container, "Add orders", criteria, []string{model.LabelReady})
	tick(t, rt, g, repo, 3)
	res := tick(t, rt, g, repo, 3)
	if len(res.Step.Start) != 0 || len(rt.TaskStore.Starts()) != 1 {
		t.Errorf("the second tick started %+v, want nothing new", res.Step.Start)
	}
	if len(res.Step.Active) != 1 {
		t.Errorf("Active = %v, want the one Task", res.Step.Active)
	}
}

// #332: while the Container isn't Ready nothing is read, the host may be
// nil, and Before doesn't run; the store's Tasks are still counted.
func TestCrewTickContainerNotReady332(t *testing.T) {
	rt := fake.NewRuntime()
	rt.TaskStore.Set(container, model.CrewTask{Name: container + "-1", Issue: 1})
	in := crewInput(ports.Repository{}, 1)
	in.Facts.ContainerReady = false
	in.Before = func(context.Context, *model.CrewFacts) { t.Error("Before ran while the Container isn't Ready") }
	res, err := engine.CrewTick(context.Background(), rt, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Step.Phase != model.CrewWaiting || !slices.Equal(res.Step.Active, []string{container + "-1"}) {
		t.Errorf("step = %+v, want Waiting with the store's Task active", res.Step)
	}
}

// #332: Before adds to the facts the decision takes, as the operator's
// releases add the prepare issue they file.
func TestCrewTickBefore332(t *testing.T) {
	g, repo, rt := crewHost(t)
	in := crewInput(repo, 1)
	in.Before = func(_ context.Context, f *model.CrewFacts) {
		f.Issues = append(f.Issues, model.Issue{Number: 42, Title: "Prepare", Body: criteria})
	}
	res, err := engine.CrewTick(context.Background(), rt, g, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Step.Start) != 1 || res.Step.Start[0].Issue != 42 {
		t.Errorf("Start = %+v, want the issue Before added", res.Step.Start)
	}
}

// #332: a host that fails to list the issues is a HostError, and starts
// nothing; a store that fails to start is the store's error.
func TestCrewTickErrors332(t *testing.T) {
	g, repo, rt := crewHost(t)
	g.AddIssue(owner, container, "Add orders", criteria, []string{model.LabelReady})
	down := errors.New("host down")
	g.FailListIssues(owner, container, down)
	_, err := engine.CrewTick(context.Background(), rt, g, crewInput(repo, 1))
	if hostErr, ok := errors.AsType[*engine.HostError](err); !ok || !errors.Is(hostErr, down) {
		t.Errorf("err = %v, want a HostError of %v", err, down)
	}
	if got := rt.TaskStore.Starts(); len(got) != 0 {
		t.Errorf("started %+v while the host was down", got)
	}

	g.FailListIssues(owner, container, nil)
	full := errors.New("store full")
	rt.TaskStore.FailStart(full)
	_, err = engine.CrewTick(context.Background(), rt, g, crewInput(repo, 1))
	if _, ok := errors.AsType[*engine.HostError](err); ok || !errors.Is(err, full) {
		t.Errorf("err = %v, want the store's %v", err, full)
	}
}

// #332: the engine is what both runtimes link (docs/plans/lead-runtime.md,
// decision 2), so it imports the business rules and the ports alone, and
// depends on nothing of controller-runtime, client-go, the API's types or
// net/http.
func TestImports(t *testing.T) {
	const module = "github.com/JairJosafath/theos/"
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Imports) == 0 {
		t.Fatal("the package's imports were not read")
	}
	for _, p := range pkg.Imports {
		switch {
		case p == module+"internal/model", p == module+"internal/ports":
		case strings.HasPrefix(p, module), strings.Contains(p, "k8s.io"), p == "net/http":
			t.Errorf("internal/engine imports %s: it takes the business rules and the ports alone "+
				"(docs/architecture.md)", p)
		}
	}
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for dep := range strings.FieldsSeq(string(out)) {
		if strings.Contains(dep, "k8s.io/") || dep == "net/http" || strings.HasPrefix(dep, module+"api/") {
			t.Errorf("internal/engine depends on %s", dep)
		}
	}
}
