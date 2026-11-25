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

package hostrun_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/adapters/github"
	"github.com/JairJosafath/theos/internal/adapters/github/githubtest"
	"github.com/JairJosafath/theos/internal/adapters/hostrun"
	"github.com/JairJosafath/theos/internal/engine"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
	"github.com/JairJosafath/theos/internal/ports/fake"
	"github.com/JairJosafath/theos/internal/scaffold"
)

const (
	container = "orders-api"
	session   = "laptop-1"
	// issues is how many issues each world's repository has: the
	// contracts' Tasks are of issues 3 to 9.
	issues = 10
	// criteria is a body agents may take.
	criteria = "## Goal\n\nDo it.\n\n## Acceptance criteria\n\n- [ ] It works\n"
)

// labels are the factory's labels, status/ exclusive by scope.
func labels() []ports.Label {
	out := make([]ports.Label, 0, 8)
	for _, l := range append(model.StatusLabels(), model.UrgentLabel()) {
		out = append(out, ports.Label{Name: l.Name, Color: l.Color, Description: l.Description, Exclusive: l.Exclusive})
	}
	return out
}

// fullHost is what the engine reaches through the lead runtime's host: its
// CI too.
type fullHost interface {
	hostrun.Host
	ports.CI
}

// world is one repository on a host, the lead runtime on it, and a test's
// clock: the fake host's stamps follow it; on the fake GitHub, whose
// stamps are its own, the lease is short and the test waits it out.
type world struct {
	t      *testing.T
	host   fullHost
	login  string
	repo   ports.Repository
	rt     *hostrun.Runtime
	rules  model.ClaimRules
	mu     sync.Mutex
	now    time.Time
	stamps bool // the host stamps by the test's clock
}

func (w *world) clock() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.stamps {
		return time.Now()
	}
	return w.now
}

// pass lets d go by.
func (w *world) pass(d time.Duration) {
	if !w.stamps {
		time.Sleep(d)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.now = w.now.Add(d)
}

// ready makes the repository: main with one commit, the labels and
// issues issues, each ready with acceptance criteria.
func (w *world) ready() {
	w.t.Helper()
	ctx := context.Background()
	var err error
	if w.repo, err = w.host.EnsureRepository(ctx, container, "hostrun's tests"); err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.host.PushInitialCommit(ctx, w.repo, scaffold.Tree{"README.md": scaffold.File{Data: []byte("hi\n")}},
		"Scaffold", ports.Signature{Name: "theos", Email: "theos@example.com", When: time.Now()}); err != nil {
		w.t.Fatal(err)
	}
	if err := w.host.EnsureLabels(ctx, w.repo, labels()); err != nil {
		w.t.Fatal(err)
	}
	for i := 1; i <= issues; i++ {
		if _, err := w.host.CreateIssue(ctx, w.repo, fmt.Sprintf("Issue %d", i), criteria,
			[]string{model.LabelReady}, 0); err != nil {
			w.t.Fatal(err)
		}
	}
	w.rt = hostrun.New(w.host, container, w.rules, w.clock)
}

// fakeWorld is a world on the ports' fake host.
func fakeWorld(t *testing.T) *world {
	t.Helper()
	g := fake.New().For(ports.Config{URL: "http://host.test", Owner: "acme",
		Credentials: ports.Credentials{Username: "operator", Token: "operator-token"}})
	w := &world{t: t, host: g, login: "operator", rules: model.DefaultClaimRules("operator"),
		now: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC), stamps: true}
	g.SetClock(w.clock)
	w.ready()
	return w
}

// gitHubWorld is a world on the fake GitHub: its own stamps, so a lease of
// moments.
func gitHubWorld(t *testing.T) *world {
	t.Helper()
	const token = "t0ken"
	gh := githubtest.New(t, "ada", token)
	gh.AddOrg("acme")
	h, err := (github.Client{}).Host(ports.Config{URL: gh.URL(), Owner: "acme",
		Credentials: ports.Credentials{Token: token}})
	if err != nil {
		t.Fatal(err)
	}
	host, ok := h.(fullHost)
	if !ok {
		t.Fatalf("%T is no hostrun.Host with a CI", h)
	}
	rules := model.DefaultClaimRules("ada")
	rules.Lease, rules.Grace = 200*time.Millisecond, 100*time.Millisecond
	w := &world{t: t, host: host, login: "ada", rules: rules}
	w.ready()
	return w
}

// post comments on issue as the sessions do, with the one account.
func (w *world) post(issue int, body string) {
	w.t.Helper()
	if _, err := w.host.PostIssueComment(context.Background(), w.repo, issue, body); err != nil {
		w.t.Fatal(err)
	}
}

// issueOf is task's issue.
func issueOf(t *testing.T, task string) int {
	t.Helper()
	var n int
	rest := strings.TrimPrefix(task, container+"-")
	if _, err := fmt.Sscanf(rest, "%d", &n); err != nil {
		t.Fatalf("task %s: %v", task, err)
	}
	return n
}

// claim posts session's claim of task's offered turn.
func (w *world) claim(task string) {
	w.t.Helper()
	line, err := model.SessionMarker(model.MarkerClaim, session)
	if err != nil {
		w.t.Fatal(err)
	}
	w.post(issueOf(w.t, task), line)
}

// implement pushes the Task's branch, as the implementer's session does,
// and returns the commit. The fake host pushes a branch from a commit of
// main, so each round's commit is on main's head.
func (w *world) implement(task string, round int) string {
	w.t.Helper()
	ctx := context.Background()
	branch := model.TaskBranch(task)
	base, err := w.host.BranchCommit(ctx, w.repo, ports.DefaultBranch)
	if err != nil {
		w.t.Fatal(err)
	}
	commit, err := w.host.PushBranch(ctx, w.repo, base, branch,
		scaffold.Tree{"orders.go": scaffold.File{Data: []byte(strings.Repeat("// round\n", round))}}, nil,
		"Add orders", ports.Signature{Name: "agent", Email: "agent@example.com", When: time.Now()})
	if err != nil {
		w.t.Fatal(err)
	}
	return commit
}

// done posts session's done of task's turn.
func (w *world) done(task, commit, verdict, summary string) {
	w.t.Helper()
	body, err := model.DoneMarker(session, commit, verdict, summary)
	if err != nil {
		w.t.Fatal(err)
	}
	w.post(issueOf(w.t, task), body)
}

func taskStoreEnv(newWorld func(*testing.T) *world) contract.TaskStoreEnv {
	var w *world
	return contract.TaskStoreEnv{
		New: func(t *testing.T) (ports.TaskStore, string, string) {
			w = newWorld(t)
			return w.rt.Store(), container, "web"
		},
		Finish: func(t *testing.T, _, task string, failed bool) {
			rec, err := w.rt.Store().Progress(context.Background(), task)
			if err != nil {
				t.Fatal(err)
			}
			rec.Progress.Phase = model.TaskMerged
			if failed {
				rec.Progress.Phase = model.TaskFailed
			}
			if err := w.rt.Store().SaveProgress(context.Background(), task, rec); err != nil {
				t.Fatal(err)
			}
		},
		NoSpecs: true,
	}
}

func workersEnv(newWorld func(*testing.T) *world) contract.WorkersEnv {
	var w *world
	return contract.WorkersEnv{
		New: func(t *testing.T) (ports.Workers, string, int) {
			w = newWorld(t)
			task := model.IssueTaskName(container, 3, 1)
			if err := w.rt.Store().Start(context.Background(), container,
				model.IssueStart{Issue: 3, Task: task, Attempt: 1}); err != nil {
				t.Fatal(err)
			}
			return w.rt.Workers(), task, 3
		},
		Answer: func(t *testing.T, task string, _ int, _ string, result model.RunResult) {
			w.claim(task)
			w.done(task, result.Commit, result.Verdict, result.Summary)
		},
		Lapse: func(t *testing.T, task string, _ int, _ string) {
			w.claim(task)
			w.pass(w.rules.Lease + w.rules.Grace + 50*time.Millisecond)
		},
	}
}

// #335: the lead runtime's TaskStore and Workers keep the contracts the
// engine relies on, on the ports' fake.
func TestTaskStoreContract(t *testing.T) { contract.TaskStore(t, taskStoreEnv(fakeWorld)) }

func TestWorkersContract(t *testing.T) { contract.Workers(t, workersEnv(fakeWorld)) }

// #335: and on GitHub's REST API, the fake GitHub.
func TestTaskStoreContractGitHub(t *testing.T) { contract.TaskStore(t, taskStoreEnv(gitHubWorld)) }

func TestWorkersContractGitHub(t *testing.T) { contract.Workers(t, workersEnv(gitHubWorld)) }

// crewTick runs one Crew tick with the lead runtime.
func (w *world) crewTick(parallel int) engine.CrewResult {
	w.t.Helper()
	res, err := engine.CrewTick(context.Background(), w.rt, w.host, engine.CrewInput{Container: container,
		Repo: w.repo, Labels: labels(), Facts: model.CrewFacts{ContainerReady: true, Parallel: parallel}})
	if err != nil {
		w.t.Fatalf("CrewTick: %v", err)
	}
	return res
}

// #335: two leads racing to start one issue start it once: one branch, one
// Task, one first progress.
func TestRacingStartsStartOnce335(t *testing.T) {
	w := fakeWorld(t)
	start := model.IssueStart{Issue: 1, Task: model.IssueTaskName(container, 1, 1), Attempt: 1}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() { errs[i] = w.rt.Store().Start(context.Background(), container, start) })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Errorf("a racing start: %v", err)
		}
	}
	tasks, err := w.rt.Store().Tasks(context.Background(), container)
	if err != nil || len(tasks) != 1 || tasks[0].Name != start.Task {
		t.Fatalf("Tasks = %+v, %v; want the one", tasks, err)
	}
	// The Crew tick finds issue 1 at work: of parallel 3, it starts two more.
	res := w.crewTick(3)
	if len(res.Step.Start) != 2 || res.Step.Start[0].Issue != 2 || res.Step.Start[1].Issue != 3 {
		t.Errorf("the next tick started %+v; want issues 2 and 3, not issue 1 again", res.Step.Start)
	}
}

// #335: a task/ branch left with no offer, the engine's own start that
// stopped halfway, gets its offer on the next tick.
func TestStartLeftHalfwayGetsItsOffer335(t *testing.T) {
	w := fakeWorld(t)
	ctx := context.Background()
	task := model.IssueTaskName(container, 1, 1)
	base, err := w.host.BranchCommit(ctx, w.repo, ports.DefaultBranch)
	if err != nil {
		t.Fatal(err)
	}
	// The branch was made, and the lead went away before anything else.
	if err := w.host.CreateBranch(ctx, w.repo, model.TaskBranch(task), base); err != nil {
		t.Fatal(err)
	}
	if res := w.crewTick(1); len(res.Step.Start) != 1 || res.Step.Start[0].Task != task {
		t.Fatalf("the Crew tick started %+v, want %s", res.Step.Start, task)
	}
	res := w.taskTick(task)
	if res.Step.Action != model.ActionStartImplement {
		t.Fatalf("step %+v, want the offer", res.Step)
	}
	run, err := w.rt.Workers().Run(ctx, task, 1, model.RoleImplementer)
	if err != nil || !run.Started || run.Done || run.Markers == nil || !run.Markers.Unclaimed {
		t.Errorf("run = %+v, %v; want offered, unclaimed", run, err)
	}
}

// taskTick runs one Task tick with the lead runtime.
func (w *world) taskTick(task string) engine.TaskResult {
	w.t.Helper()
	res, err := engine.TaskTick(context.Background(), w.rt, w.host, engine.TaskInput{Task: task,
		Container: container, Repo: w.repo, Issue: issueOf(w.t, task), Title: "Issue", Implementer: "implementer",
		Reviewer: "reviewer", ContainerReady: true, Protected: true, Lease: w.rules.Lease, Now: w.clock()})
	if err != nil {
		w.t.Fatalf("TaskTick: %v", err)
	}
	return res
}

// #335, the plan's end-to-end test: the engine on the lead runtime on the
// fake host takes an issue from status/ready to a pull request waiting for
// a person, a round sent back by the reviewer included, and, merged by the
// person, to Merged with the issue closed; the sessions are this test,
// posting their markers.
func TestOneTaskEndToEnd335(t *testing.T) {
	w := fakeWorld(t)
	ctx := context.Background()
	res := w.crewTick(1)
	if len(res.Step.Start) != 1 || res.Step.Start[0].Issue != 1 {
		t.Fatalf("the Crew tick started %+v, want issue 1", res.Step.Start)
	}
	task := res.Step.Start[0].Task
	issue := w.host.(*fake.Git).Issue("acme", container, 1)
	if !strings.Contains(strings.Join(issue.Labels, " "), model.LabelInProgress) {
		t.Fatalf("issue 1 labels %v, want %s", issue.Labels, model.LabelInProgress)
	}
	want := func(res engine.TaskResult, action model.TaskAction, phase string) {
		t.Helper()
		if res.Step.Action != action || res.Record.Progress.Phase != phase {
			t.Fatalf("step %s (%s), phase %s; want %s, %s", res.Step.Action, res.Step.Message,
				res.Record.Progress.Phase, action, phase)
		}
	}

	// Round 1: offered, claimed, pushed, done.
	want(w.taskTick(task), model.ActionStartImplement, model.TaskImplementing)
	w.claim(task)
	want(w.taskTick(task), model.ActionWait, model.TaskImplementing)
	commit := w.implement(task, 1)
	w.done(task, commit, "", "Added the orders.")
	res2 := w.taskTick(task)
	want(res2, model.ActionOpenPR, model.TaskChecking)
	pull := res2.Record.PullRequest
	// main requires no check here: the checks pass with none.
	if err := w.host.SetCommitStatus(ctx, w.repo, commit, ports.CommitStatus{Context: "check",
		State: ports.CheckSuccess}); err != nil {
		t.Fatal(err)
	}
	want(w.taskTick(task), model.ActionStartReview, model.TaskReviewing)
	w.claim(task)
	w.done(task, "", model.VerdictChanges, "Handle an empty order.")
	res3 := w.taskTick(task)
	if res3.Step.Action != model.ActionRetry || res3.Record.Progress.Round != 2 {
		t.Fatalf("step %+v, round %d; want round 2", res3.Step, res3.Record.Progress.Round)
	}

	// Round 2: the feedback reaches the offer; the session fixes and pushes.
	want(w.taskTick(task), model.ActionStartImplement, model.TaskImplementing)
	comments, err := w.host.ListIssueComments(ctx, w.repo, 1)
	if err != nil {
		t.Fatal(err)
	}
	if last := comments[len(comments)-1].Body; !strings.Contains(last, "<!-- theos:offer ") ||
		!strings.Contains(last, "round=2") || !strings.Contains(last, "Handle an empty order.") {
		t.Errorf("the last comment = %q, want round 2's offer with the feedback", last)
	}
	w.claim(task)
	commit = w.implement(task, 2)
	w.done(task, commit, "", "Handled an empty order.")
	want(w.taskTick(task), model.ActionOpenPR, model.TaskChecking)
	if err := w.host.SetCommitStatus(ctx, w.repo, commit, ports.CommitStatus{Context: "check",
		State: ports.CheckSuccess}); err != nil {
		t.Fatal(err)
	}
	want(w.taskTick(task), model.ActionStartReview, model.TaskReviewing)
	w.claim(task)
	w.done(task, "", model.VerdictApprove, "Looks right.")
	res4 := w.taskTick(task)
	if res4.Step.Reason != model.ReasonPersonMerges {
		t.Fatalf("step %+v, want a wait for a person to merge", res4.Step)
	}

	// A lead that went away and came back reads the same Task off the host.
	fresh := hostrun.New(w.host, container, w.rules, w.clock)
	rec, err := fresh.Store().Progress(ctx, task)
	if err != nil || rec.Progress.Phase != model.TaskReviewing || rec.Progress.Round != 2 || rec.PullRequest != pull {
		t.Fatalf("a fresh lead reads %+v, %v; want Reviewing, round 2, pull request %d", rec, err, pull)
	}

	// The person merges on the host's pages.
	if err := w.host.MergePullRequest(ctx, w.repo, pull, commit, "Issue 1", ""); err != nil {
		t.Fatal(err)
	}
	want(w.taskTick(task), model.ActionMerge, model.TaskMerged)
	issue = w.host.(*fake.Git).Issue("acme", container, 1)
	if issue.State != "closed" || strings.Contains(strings.Join(issue.Labels, " "), model.LabelInProgress) {
		t.Errorf("issue 1 = %s %v, want closed, not in progress", issue.State, issue.Labels)
	}
	if tasks, _ := w.rt.Store().Tasks(ctx, container); len(tasks) != 0 {
		t.Errorf("Tasks = %+v, want none at work", tasks)
	}
	// The Crew picks up the next issue.
	if res := w.crewTick(1); len(res.Step.Start) != 1 || res.Step.Start[0].Issue != 2 {
		t.Errorf("the Crew tick started %+v, want issue 2", res.Step.Start)
	}
}
