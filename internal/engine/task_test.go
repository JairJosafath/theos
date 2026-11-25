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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/engine"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// check is the one check main requires in the tests.
const check = "check"

// taskWorld is one Task on the fakes: the host with its repository and
// issue, a runtime that merges nothing (the lead runtime's), and the
// Crew tick's start of the Task.
type taskWorld struct {
	t    *testing.T
	g    *fake.Git
	repo ports.Repository
	rt   *fake.Runtime
	in   engine.TaskInput
}

func newTaskWorld(t *testing.T) *taskWorld {
	t.Helper()
	ctx := context.Background()
	g, repo, rt := crewHost(t)
	rt.Work = fake.NewWorkers()
	rt.Caps = model.LeadCapabilities()
	author := ports.Signature{Name: "theos", Email: "theos@example.com", When: time.Now()}
	head, err := g.PushInitialCommit(ctx, repo, scaffold.Tree{"README.md": scaffold.File{Data: []byte("hi\n")}},
		"Scaffold", author)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.SetCommitStatus(ctx, repo, head, ports.CommitStatus{Context: check,
		State: ports.CheckSuccess}); err != nil {
		t.Fatal(err)
	}
	n := g.AddIssue(owner, container, "Add orders", criteria, []string{model.LabelReady})
	res := tick(t, rt, g, repo, 1)
	if len(res.Step.Start) != 1 {
		t.Fatalf("the Crew tick started %+v, want the issue's Task", res.Step.Start)
	}
	task := res.Step.Start[0].Task
	return &taskWorld{t: t, g: g, repo: repo, rt: rt, in: engine.TaskInput{Task: task, Container: container,
		Repo: repo, Issue: n, Title: "Add orders", Implementer: "implementer", Reviewer: "reviewer",
		ContainerReady: true, Protected: true, CheckContexts: []string{check}, Lease: time.Hour, Now: time.Now()}}
}

// tick runs one Task tick, failing on an error.
func (w *taskWorld) tick() engine.TaskResult {
	w.t.Helper()
	res, err := engine.TaskTick(context.Background(), w.rt, w.g, w.in)
	if err != nil {
		w.t.Fatalf("TaskTick: %v", err)
	}
	return res
}

// implement plays the implementer's session of round: it pushes the
// Task's branch and says done with the commit.
func (w *taskWorld) implement(round int) string {
	w.t.Helper()
	ctx := context.Background()
	base, err := w.g.BranchCommit(ctx, w.repo, ports.DefaultBranch)
	if err != nil {
		w.t.Fatal(err)
	}
	commit, err := w.g.PushBranch(ctx, w.repo, base, model.TaskBranch(w.in.Task),
		scaffold.Tree{"orders.go": scaffold.File{Data: []byte(strings.Repeat("// round\n", round))}}, nil,
		"Add orders", ports.Signature{Name: "agent", Email: "agent@example.com", When: time.Now()})
	if err != nil {
		w.t.Fatal(err)
	}
	w.rt.Work.SetRun(w.in.Task, round, model.RoleImplementer, model.RunState{Started: true, Done: true,
		Result: &model.RunResult{Commit: commit, Summary: "Added the orders."}})
	return commit
}

// review plays the reviewer's session of round.
func (w *taskWorld) review(round int, verdict, summary string) {
	w.rt.Work.SetRun(w.in.Task, round, model.RoleReviewer, model.RunState{Started: true, Done: true,
		Result: &model.RunResult{Verdict: verdict, Summary: summary}})
}

// checksPass sets the check main requires green on commit.
func (w *taskWorld) checksPass(commit string) {
	w.t.Helper()
	if err := w.g.SetCommitStatus(context.Background(), w.repo, commit, ports.CommitStatus{Context: check,
		State: ports.CheckSuccess}); err != nil {
		w.t.Fatal(err)
	}
}

func (w *taskWorld) want(res engine.TaskResult, action model.TaskAction, phase string) {
	w.t.Helper()
	if res.Step.Action != action || res.Record.Progress.Phase != phase {
		w.t.Fatalf("step %s (%s), phase %s; want %s, phase %s", res.Step.Action, res.Step.Message,
			res.Record.Progress.Phase, action, phase)
	}
}

// #334: one Task from its start to a pull request that waits for a person,
// then, merged by the person, Merged with its issue closed.
func TestTaskTickToAPerson334(t *testing.T) {
	w := newTaskWorld(t)
	w.want(w.tick(), model.ActionStartImplement, model.TaskImplementing)
	offers := w.rt.Work.Offers()
	if len(offers) != 1 || offers[0].Role != model.RoleImplementer || offers[0].Round != 1 ||
		offers[0].Agent != "implementer" || offers[0].Branch != model.TaskBranch(w.in.Task) ||
		offers[0].Issue != w.in.Issue || offers[0].Lease != time.Hour {
		t.Fatalf("offers = %+v, want round 1 to the implementer on the Task's branch", offers)
	}
	// Offered and not answered: the next tick waits, and offers nothing more.
	w.want(w.tick(), model.ActionWait, model.TaskImplementing)

	commit := w.implement(1)
	res := w.tick()
	w.want(res, model.ActionOpenPR, model.TaskChecking)
	pr, err := w.g.GetPullRequest(context.Background(), w.repo, res.Record.PullRequest)
	if err != nil || pr.HeadSHA != commit || pr.State != "open" {
		t.Fatalf("pull request = %+v, %v; want open at %s", pr, err, commit)
	}

	w.want(w.tick(), model.ActionWait, model.TaskChecking) // the checks run
	w.checksPass(commit)
	w.want(w.tick(), model.ActionStartReview, model.TaskReviewing)
	if got := w.rt.Work.Offers(); len(got) != 2 || got[1].Role != model.RoleReviewer || got[1].Agent != "reviewer" {
		t.Fatalf("offers = %+v, want the review offered second", got)
	}

	w.review(1, model.VerdictApprove, "Looks right.")
	res = w.tick()
	if res.Step.Action != model.ActionWait || res.Step.Reason != model.ReasonPersonMerges {
		t.Fatalf("step = %+v, want a wait for a person to merge", res.Step)
	}
	if !strings.Contains(res.Step.Message, pr.URL) {
		t.Errorf("message %q names no pull request", res.Step.Message)
	}
	if got, _ := w.g.GetPullRequest(context.Background(), w.repo, pr.Number); got.Merged {
		t.Fatal("the runtime merged; a person merges here")
	}
	w.want(w.tick(), model.ActionWait, model.TaskReviewing) // still the person's

	// The person merges on the host's pages.
	if err := w.g.MergePullRequest(context.Background(), w.repo, pr.Number, commit, "Add orders", ""); err != nil {
		t.Fatal(err)
	}
	w.want(w.tick(), model.ActionMerge, model.TaskMerged)
	if issue := w.g.Issue(owner, container, w.in.Issue); issue.State != "closed" {
		t.Errorf("issue state = %s, want closed", issue.State)
	}
	got, _ := w.rt.TaskStore.Get(container, w.in.Task)
	if !got.Final || got.Failed {
		t.Errorf("the store's Task = %+v, want final, not failed, for the Crew to count", got)
	}
	w.want(w.tick(), model.ActionNone, model.TaskMerged)
}

// #334: a requested change starts round 2 with the reviewer's feedback,
// said on the pull request.
func TestTaskTickRound2334(t *testing.T) {
	w := newTaskWorld(t)
	w.tick()
	commit := w.implement(1)
	pr := w.tick().Record.PullRequest
	w.checksPass(commit)
	w.tick()
	w.review(1, model.VerdictChanges, "Handle an empty order.")
	res := w.tick()
	if res.Step.Action != model.ActionRetry || res.Record.Progress.Round != 2 ||
		!strings.Contains(res.Record.Progress.Feedback, "Handle an empty order.") {
		t.Fatalf("step %+v, record %+v; want round 2 with the review's feedback", res.Step, res.Record.Progress)
	}
	// theos opened the pull request with the account the sessions share, so
	// the host refuses its review: the verdict goes on as a comment.
	if comments := w.g.PullComments(owner, container, pr); len(comments) != 1 ||
		!strings.Contains(comments[0], "reviewer asks for changes in round 1") ||
		!strings.Contains(comments[0], "Handle an empty order.") {
		t.Errorf("pull request comments = %q, want the request for changes", comments)
	}
	w.want(w.tick(), model.ActionStartImplement, model.TaskImplementing)
	offers := w.rt.Work.Offers()
	last := offers[len(offers)-1]
	if last.Round != 2 || last.Role != model.RoleImplementer || !strings.Contains(last.Feedback, "empty order") {
		t.Errorf("last offer = %+v, want round 2 with the feedback", last)
	}
}

// #334: a claim past its lease is ended by the engine, and the turn is
// offered again on its branch, the rerun counted.
func TestTaskTickLeaseRunsOut334(t *testing.T) {
	w := newTaskWorld(t)
	w.tick()
	w.rt.Work.SetRun(w.in.Task, 1, model.RoleImplementer, model.RunState{Started: true,
		Markers: &model.MarkerRun{EndDue: model.EndSilent}})
	res := w.tick()
	if res.Step.Action != model.ActionRerun || res.Record.Progress.Reruns != 1 {
		t.Fatalf("step %+v, reruns %d; want a rerun", res.Step, res.Record.Progress.Reruns)
	}
	ends := w.rt.Work.Ends()
	if len(ends) == 0 || ends[0].Why != model.EndSilent {
		t.Errorf("ends = %+v, want the silent claim ended first", ends)
	}
	offers := w.rt.Work.Offers()
	if len(offers) != 2 || offers[1].Round != 1 || offers[1].Branch != model.TaskBranch(w.in.Task) {
		t.Errorf("offers = %+v, want round 1 offered again on the Task's branch", offers)
	}
	w.want(w.tick(), model.ActionWait, model.TaskImplementing)
}

// #334: a runtime that merges merges an approved change itself.
func TestTaskTickMerges334(t *testing.T) {
	w := newTaskWorld(t)
	w.rt.Caps = model.OperatorCapabilities()
	w.tick()
	commit := w.implement(1)
	pr := w.tick().Record.PullRequest
	w.checksPass(commit)
	w.tick()
	w.review(1, model.VerdictApprove, "Looks right.")
	w.want(w.tick(), model.ActionMerge, model.TaskMerged)
	if got, _ := w.g.GetPullRequest(context.Background(), w.repo, pr); !got.Merged {
		t.Error("the pull request is not merged")
	}
}

// #266: an offer carries the work order's text the runtime renders, from the
// Task's issue as its goal.
func TestTaskTickRendersTheWorkOrder266(t *testing.T) {
	w := newTaskWorld(t)
	w.in.Goal = "Issue #1: Add orders"
	w.in.OrderText = func(o model.WorkOrder) string {
		return fmt.Sprintf("%s %s round %d: %s", o.Task, o.Role, o.Round, o.Goal)
	}
	w.tick()
	offers := w.rt.Work.Offers()
	if len(offers) != 1 || offers[0].Text != w.in.Task+" implement round 1: Issue #1: Add orders" {
		t.Errorf("offers = %+v, want the rendered text", offers)
	}
}
