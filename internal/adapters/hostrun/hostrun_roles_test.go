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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/engine"
	"github.com/JairJosafath/theos/internal/method"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// The triager, the spec writer and the verifier in the lead runtime (#337),
// tier 1: the engine on hostrun on the ports' fake host, the sessions played
// by the test posting their markers, as TestOneTaskEndToEnd335 plays them.

// tick runs one Task tick of in, its world's repository, clock, lease and
// accounts filled in, and the lead's work orders rendered.
func (w *world) tick(in engine.TaskInput) engine.TaskResult {
	w.t.Helper()
	in.Container, in.Repo, in.Issue = container, w.repo, issueOf(w.t, in.Task)
	in.ContainerReady, in.Protected, in.Lease, in.Now, in.Ours = true, true, w.rules.Lease, w.clock(), w.rules.Ours
	in.Title = cmpOr(in.Title, "Issue")
	in.OrderText = func(o model.WorkOrder) string { return method.WorkOrder(o, container, "") }
	res, err := engine.TaskTick(context.Background(), w.rt, w.host, in)
	if err != nil {
		w.t.Fatalf("TaskTick(%s): %v", in.Task, err)
	}
	return res
}

func cmpOr(s, or string) string {
	if s != "" {
		return s
	}
	return or
}

// expect fails t unless res took action and left the Task in phase.
func expect(t *testing.T, res engine.TaskResult, action model.TaskAction, phase string) {
	t.Helper()
	if res.Step.Action != action || res.Record.Progress.Phase != phase {
		t.Fatalf("step %s (%s, %s), phase %s; want %s, %s", res.Step.Action, res.Step.Reason, res.Step.Message,
			res.Record.Progress.Phase, action, phase)
	}
}

// push pushes files on the Task's branch, as a writing session does, and
// returns the commit.
func (w *world) push(task string, files scaffold.Tree) string {
	w.t.Helper()
	ctx := context.Background()
	base, err := w.host.BranchCommit(ctx, w.repo, ports.DefaultBranch)
	if err != nil {
		w.t.Fatal(err)
	}
	commit, err := w.host.PushBranch(ctx, w.repo, base, model.TaskBranch(task), files, nil, "Work",
		ports.Signature{Name: "agent", Email: "agent@example.com", When: time.Now()})
	if err != nil {
		w.t.Fatal(err)
	}
	return commit
}

// pass makes the commit's checks pass.
func (w *world) green(commit string) {
	w.t.Helper()
	if err := w.host.SetCommitStatus(context.Background(), w.repo, commit, ports.CommitStatus{Context: "check",
		State: ports.CheckSuccess}); err != nil {
		w.t.Fatal(err)
	}
}

// lastComment is the last comment on issue.
func (w *world) lastComment(issue int) string {
	w.t.Helper()
	comments, err := w.host.ListIssueComments(context.Background(), w.repo, issue)
	if err != nil || len(comments) == 0 {
		w.t.Fatalf("issue %d's comments: %v, %d", issue, err, len(comments))
	}
	return comments[len(comments)-1].Body
}

// said reports whether a comment on issue says text.
func (w *world) said(issue int, text string) bool {
	w.t.Helper()
	comments, err := w.host.ListIssueComments(context.Background(), w.repo, issue)
	if err != nil {
		w.t.Fatal(err)
	}
	return slices.ContainsFunc(comments, func(c ports.IssueComment) bool { return strings.Contains(c.Body, text) })
}

// triageTick runs one triage tick of a Crew whose triager is "triager".
func (w *world) triageTick() model.TriageStep {
	w.t.Helper()
	step, err := engine.TriageTick(context.Background(), w.rt, w.host, engine.TriageInput{Container: container,
		Repo: w.repo, Triager: "triager", ContainerReady: true})
	if err != nil {
		w.t.Fatalf("TriageTick: %v", err)
	}
	return step
}

// #337: a proposed issue is triaged to ready on the host: the triage tick
// starts a Triage Task, which makes no branch and leaves the issue
// proposed; the triager's turn is offered on main; a done with no decision
// fails the try, said on the issue, and the next tick tries again; a done
// whose JSON decides ready writes the criteria in and labels the issue
// status/ready, which the Crew then picks up.
func TestTriageToReady337(t *testing.T) {
	w := fakeWorldWith(t, 0)
	ctx := context.Background()
	g := w.host.(*fake.Git)
	n := g.AddIssue("acme", container, "Validate orders", "Reject an empty order.", []string{model.LabelProposed})
	if n != 1 {
		t.Fatalf("issue %d, want 1", n)
	}
	step := w.triageTick()
	if step.Start == nil || step.Start.Task != model.TriageTaskName(container, 1, 1) {
		t.Fatalf("the triage tick = %+v, want a Triage Task for issue 1", step)
	}
	task := step.Start.Task
	if again := w.triageTick(); again.Start != nil || again.Active != task {
		t.Errorf("a second triage tick = %+v, want %s at work and nothing started", again, task)
	}
	if has, _ := w.host.HasBranch(ctx, w.repo, model.TaskBranch(task)); has {
		t.Error("a Triage Task made a branch; it pushes nothing")
	}
	if tasks, _ := w.rt.Store().Tasks(ctx, container); len(tasks) != 0 {
		t.Errorf("Tasks = %+v; a Triage Task takes no place from parallel", tasks)
	}
	in := engine.TaskInput{Task: task, Triage: true, Implementer: "triager", Title: "Validate orders"}
	expect(t, w.tick(in), model.ActionStartImplement, model.TaskImplementing)
	offer := w.lastComment(1)
	for _, want := range []string{"role=triage", "agent=triager", "the triager's turn", "branch `main`",
		`"decision": "ready", "spec", "park" or "duplicate"`} {
		if !strings.Contains(offer, want) {
			t.Errorf("the triager's offer lacks %q:\n%s", want, offer)
		}
	}
	if labels := g.Issue("acme", container, 1).Labels; !slices.Equal(labels, []string{model.LabelProposed}) {
		t.Errorf("the issue is %v while it is triaged; want %s alone", labels, model.LabelProposed)
	}

	// A done with no decision fails the try; the issue stays proposed.
	w.claim(task)
	w.done(task, "", "", "I looked, and I am not sure.")
	failed := w.tick(in)
	expect(t, failed, model.ActionFail, model.TaskFailed)
	if !w.said(1, "the Crew triages it again (try 1 of 3 failed)") {
		t.Error("the failed try says nothing of the next")
	}
	step = w.triageTick()
	if step.Start == nil || step.Start.Task != model.TriageTaskName(container, 1, 2) {
		t.Fatalf("the next triage tick = %+v, want try 2", step)
	}
	task = step.Start.Task
	in.Task = task
	in.TriageFailed = 1
	expect(t, w.tick(in), model.ActionStartImplement, model.TaskImplementing)
	w.claim(task)
	w.done(task, "", "", "Clear enough.\n\n"+`{"decision": "ready", "reason": "Small and clear.", `+
		`"criteria": ["An empty order is refused with 400"]}`)
	triaged := w.tick(in)
	expect(t, triaged, model.ActionTriaged, model.TaskMerged)
	issue := g.Issue("acme", container, 1)
	if !slices.Contains(issue.Labels, model.LabelReady) || slices.Contains(issue.Labels, model.LabelProposed) ||
		!strings.Contains(issue.Body, "- [ ] An empty order is refused with 400") {
		t.Errorf("the triaged issue = %v %q; want ready with its criteria", issue.Labels, issue.Body)
	}
	if again := w.triageTick(); again.Start != nil || again.Active != "" {
		t.Errorf("a triage tick after the decision = %+v, want nothing", again)
	}
	// The Crew builds it.
	if res := w.crewTick(1); len(res.Step.Start) != 1 || res.Step.Start[0].Issue != 1 {
		t.Errorf("the Crew tick started %+v, want issue 1", res.Step.Start)
	}
}

// #337: a person who labels a triaged issue status/proposed again sends it
// back: the Triage Task that decided counts for nothing, and the next tick
// triages it again.
func TestTriageSentBackByAPerson337(t *testing.T) {
	w := fakeWorldWith(t, 0)
	g := w.host.(*fake.Git)
	g.AddIssue("acme", container, "Validate orders", "Reject an empty order.", []string{model.LabelProposed})
	task := w.triageTick().Start.Task
	in := engine.TaskInput{Task: task, Triage: true, Implementer: "triager"}
	w.tick(in)
	w.claim(task)
	w.done(task, "", "", `{"decision": "park", "reason": "Which status code?", "question": "400 or 422?"}`)
	expect(t, w.tick(in), model.ActionTriaged, model.TaskMerged)
	if labels := g.Issue("acme", container, 1).Labels; !slices.Contains(labels, model.LabelNeedsHuman) {
		t.Fatalf("the parked issue is %v", labels)
	}
	if err := g.LabelIssue("acme", container, 1, model.LabelProposed); err != nil {
		t.Fatal(err)
	}
	if step := w.triageTick(); step.Start == nil || step.Start.Task != model.TriageTaskName(container, 1, 2) {
		t.Errorf("the triage tick after a person sent the issue back = %+v, want try 2", step)
	}
}

// #337: a spec round is approved by a person and then implemented. The
// Crew's spec writer writes docs/specs/1.md on the Spec Task's branch; its
// pull request passes CI and the reviewer, then waits for a person; a
// person's approving review (another account than theos's) lets it merge,
// which a person does; the spec's criteria go into the issue, now ready,
// and the Crew's next tick builds it to a merge.
func TestSpecApprovedThenImplemented337(t *testing.T) {
	w := fakeWorldWith(t, 0)
	ctx := context.Background()
	g := w.host.(*fake.Git)
	g.AddIssue("acme", container, "Refunds", "Customers can get their money back.", []string{model.LabelNeedsSpec})
	res, err := engine.CrewTick(ctx, w.rt, w.host, engine.CrewInput{Container: container, Repo: w.repo,
		Labels: labels(), Specs: true, Facts: model.CrewFacts{ContainerReady: true, Parallel: 1}})
	if err != nil || len(res.Step.Start) != 1 || !res.Step.Start[0].Spec {
		t.Fatalf("the Crew tick started %+v, %v; want the spec of issue 1", res.Step.Start, err)
	}
	task := res.Step.Start[0].Task
	if task != model.SpecTaskName(container, 1, 1) {
		t.Fatalf("the Spec Task is %s", task)
	}
	if tasks, _ := w.rt.Store().Tasks(ctx, container); len(tasks) != 1 || !tasks[0].Spec {
		t.Fatalf("Tasks = %+v, want the Spec Task listed as one", tasks)
	}
	in := engine.TaskInput{Task: task, Spec: true, Implementer: "writer", Reviewer: "reviewer", Title: "Refunds"}

	expect(t, w.tick(in), model.ActionStartImplement, model.TaskImplementing)
	for _, want := range []string{"the spec writer's turn", "Write the spec in docs/specs/1.md, and nothing else",
		"subagent `theos-writer`"} {
		if offer := w.lastComment(1); !strings.Contains(offer, want) {
			t.Errorf("the spec writer's offer lacks %q:\n%s", want, offer)
		}
	}
	w.claim(task)
	spec := "# Refunds\n\n## Problem\n\nNo refunds.\n\n## Approach\n\nAn endpoint.\n\n## Acceptance criteria\n\n" +
		"- [ ] POST /refunds refunds an order\n- [ ] make check passes\n"
	commit := w.push(task, scaffold.Tree{model.SpecPath(1): scaffold.File{Data: []byte(spec)}})
	w.done(task, commit, "", "Wrote the spec.")
	opened := w.tick(in)
	expect(t, opened, model.ActionOpenPR, model.TaskChecking)
	pull := opened.Record.PullRequest
	if title, _ := g.PullText("acme", container, pull); title != "Spec: Refunds" {
		t.Errorf("the spec's pull request is titled %q", title)
	}
	w.green(commit)
	expect(t, w.tick(in), model.ActionStartReview, model.TaskReviewing)
	w.claim(task)
	w.done(task, "", model.VerdictApprove, "A sound spec.")
	expect(t, w.tick(in), model.ActionAwaitApproval, model.TaskAwaitingApproval)
	waiting := w.tick(in)
	if waiting.Step.Reason != model.ReasonAwaitingApproval {
		t.Fatalf("step %+v, want a wait for a person's approval", waiting.Step)
	}
	// theos's own account's approval is no person's.
	g.AddReview("acme", container, pull, fake.Review{Event: ports.ReviewApprove, Commit: commit, User: w.login})
	if again := w.tick(in); again.Step.Reason != model.ReasonAwaitingApproval {
		t.Fatalf("step %+v after theos's own approval, want still waiting", again.Step)
	}
	g.AddReview("acme", container, pull, fake.Review{Event: ports.ReviewApprove, Commit: commit, User: "ada"})
	approved := w.tick(in)
	if approved.Step.Reason != model.ReasonPersonMerges {
		t.Fatalf("step %+v after ada's approval, want a wait for a person to merge", approved.Step)
	}
	if err := w.host.MergePullRequest(ctx, w.repo, pull, commit, "Spec: Refunds", ""); err != nil {
		t.Fatal(err)
	}
	expect(t, w.tick(in), model.ActionMerge, model.TaskMerged)
	issue := g.Issue("acme", container, 1)
	if issue.State == "closed" || !slices.Contains(issue.Labels, model.LabelReady) ||
		!strings.Contains(issue.Body, "- [ ] POST /refunds refunds an order") {
		t.Fatalf("the spec's issue = %s %v %q; want open, ready, with the spec's criteria", issue.State,
			issue.Labels, issue.Body)
	}

	// Then implemented: the Crew starts the issue's build, which merges.
	res, err = engine.CrewTick(ctx, w.rt, w.host, engine.CrewInput{Container: container, Repo: w.repo,
		Labels: labels(), Specs: true, Facts: model.CrewFacts{ContainerReady: true, Parallel: 1}})
	if err != nil || len(res.Step.Start) != 1 || res.Step.Start[0].Spec ||
		res.Step.Start[0].Task != model.IssueTaskName(container, 1, 1) {
		t.Fatalf("the Crew tick started %+v, %v; want issue 1's build", res.Step.Start, err)
	}
	build := engine.TaskInput{Task: res.Step.Start[0].Task, Implementer: "implementer", Reviewer: "reviewer"}
	expect(t, w.tick(build), model.ActionStartImplement, model.TaskImplementing)
	w.claim(build.Task)
	commit = w.push(build.Task, scaffold.Tree{"refunds.go": scaffold.File{Data: []byte("package refunds\n")}})
	w.done(build.Task, commit, "", "Added refunds.")
	built := w.tick(build)
	expect(t, built, model.ActionOpenPR, model.TaskChecking)
	w.green(commit)
	expect(t, w.tick(build), model.ActionStartReview, model.TaskReviewing)
	w.claim(build.Task)
	w.done(build.Task, "", model.VerdictApprove, "Refunds work.")
	if step := w.tick(build).Step; step.Reason != model.ReasonPersonMerges {
		t.Fatalf("step %+v, want a wait for a person to merge", step)
	}
	if err := w.host.MergePullRequest(ctx, w.repo, built.Record.PullRequest, commit, "Refunds", ""); err != nil {
		t.Fatal(err)
	}
	expect(t, w.tick(build), model.ActionMerge, model.TaskMerged)
	if issue := g.Issue("acme", container, 1); issue.State != "closed" {
		t.Errorf("the built issue is %s, want closed", issue.State)
	}
}

// #337: a spec whose pull request changes another file goes back to its
// writer, and one a person closes unmerged is declined.
func TestSpecRefusedAndDeclined337(t *testing.T) {
	w := fakeWorldWith(t, 0)
	ctx := context.Background()
	g := w.host.(*fake.Git)
	g.AddIssue("acme", container, "Refunds", "Customers can get their money back.", []string{model.LabelNeedsSpec})
	task := model.SpecTaskName(container, 1, 1)
	if err := w.rt.Store().Start(ctx, container, model.IssueStart{Issue: 1, Task: task, Attempt: 1,
		Spec: true}); err != nil {
		t.Fatal(err)
	}
	in := engine.TaskInput{Task: task, Spec: true, Implementer: "writer", Reviewer: "reviewer", Title: "Refunds"}
	w.tick(in)
	w.claim(task)
	commit := w.push(task, scaffold.Tree{model.SpecPath(1): scaffold.File{Data: []byte("# Refunds\n")},
		"refunds.go": scaffold.File{Data: []byte("package refunds\n")}})
	w.done(task, commit, "", "Wrote it, and some code.")
	opened := w.tick(in)
	w.green(commit)
	sent := w.tick(in)
	if sent.Step.Action != model.ActionRetry || !strings.Contains(sent.Record.Progress.Feedback, "refunds.go") {
		t.Fatalf("step %+v, feedback %q; want round 2 for the stray file", sent.Step, sent.Record.Progress.Feedback)
	}
	g.ClosePull("acme", container, opened.Record.PullRequest)
	expect(t, w.tick(in), model.ActionStartImplement, model.TaskImplementing)
	w.claim(task)
	commit = w.push(task, scaffold.Tree{model.SpecPath(1): scaffold.File{Data: []byte(
		"# Refunds\n\n## Acceptance criteria\n\n- [ ] It refunds\n")}})
	w.done(task, commit, "", "Only the spec now.")
	reopened := w.tick(in)
	expect(t, reopened, model.ActionOpenPR, model.TaskChecking)
	w.green(commit)
	expect(t, w.tick(in), model.ActionStartReview, model.TaskReviewing)
	w.claim(task)
	w.done(task, "", model.VerdictApprove, "Fine.")
	expect(t, w.tick(in), model.ActionAwaitApproval, model.TaskAwaitingApproval)
	g.ClosePull("acme", container, reopened.Record.PullRequest)
	declined := w.tick(in)
	expect(t, declined, model.ActionSpecDeclined, model.TaskFailed)
	if declined.Record.Reason != model.ReasonSpecDeclined ||
		!slices.Contains(g.Issue("acme", container, 1).Labels, model.LabelNeedsHuman) {
		t.Errorf("the declined spec = %+v, issue %v", declined.Record, g.Issue("acme", container, 1).Labels)
	}
}

// #337: a verifier run after the review, and what it leaves filed after
// the merge. The verifier's first run asks for changes, which start round
// 2 with its evidence; in round 2 the reviewer approves with a follow-up,
// the verifier approves leaving a criterion no session can meet open, a
// person merges, and the tick files the follow-up as a proposed issue and
// the open criterion as a by-hand issue for a person, both in Later, says
// so on the issue, and closes it.
func TestVerifierFilesItsFollowUp337(t *testing.T) {
	w := fakeWorldWith(t, 1)
	ctx := context.Background()
	g := w.host.(*fake.Git)
	task := w.crewTick(1).Step.Start[0].Task
	in := engine.TaskInput{Task: task, Implementer: "implementer", Reviewer: "reviewer", Verifier: "verifier",
		Title: "Issue 1"}

	round := func(n int) string {
		t.Helper()
		expect(t, w.tick(in), model.ActionStartImplement, model.TaskImplementing)
		w.claim(task)
		commit := w.implement(task, n)
		w.done(task, commit, "", "Round done.")
		expect(t, w.tick(in), model.ActionOpenPR, model.TaskChecking)
		w.green(commit)
		expect(t, w.tick(in), model.ActionStartReview, model.TaskReviewing)
		return commit
	}
	round(1)
	w.claim(task)
	w.done(task, "", model.VerdictApprove, "Looks right.")
	expect(t, w.tick(in), model.ActionVerify, model.TaskVerifying)
	expect(t, w.tick(in), model.ActionStartVerify, model.TaskVerifying)
	offer := w.lastComment(1)
	for _, want := range []string{"role=verify", "agent=verifier", "the verifier's turn", "push nothing",
		"branch `task/orders-api-1`"} {
		if !strings.Contains(offer, want) {
			t.Errorf("the verifier's offer lacks %q:\n%s", want, offer)
		}
	}
	w.claim(task)
	w.done(task, "", model.VerdictChanges, "`make verify` printed: panic: empty order.")
	again := w.tick(in)
	if again.Step.Action != model.ActionRetry || again.Record.Progress.Round != 2 ||
		!strings.Contains(again.Record.Progress.Feedback, "panic: empty order") {
		t.Fatalf("step %+v, round %d, feedback %q; want round 2 with the verifier's evidence", again.Step,
			again.Record.Progress.Round, again.Record.Progress.Feedback)
	}

	commit := round(2)
	w.claim(task)
	w.done(task, "", model.VerdictApprove, "Fixed.\n\n"+`{"verdict": "approve", "summary": "Fixed.", `+
		`"followUps": [{"title": "Cache the order totals", "criteria": ["Totals are cached"]}]}`)
	expect(t, w.tick(in), model.ActionVerify, model.TaskVerifying)
	expect(t, w.tick(in), model.ActionStartVerify, model.TaskVerifying)
	w.claim(task)
	w.done(task, "", model.VerdictApprove, "`make verify` printed: ok.\n\n"+`{"verdict": "approve", `+
		`"summary": "It works.", "open": [{"criterion": "It looks right on a phone", "why": "no phone here"}]}`)
	verified := w.tick(in)
	if verified.Step.Reason != model.ReasonPersonMerges {
		t.Fatalf("step %+v, want a wait for a person to merge", verified.Step)
	}
	if err := w.host.MergePullRequest(ctx, w.repo, verified.Record.PullRequest, commit, "Issue 1", ""); err != nil {
		t.Fatal(err)
	}
	expect(t, w.tick(in), model.ActionMerge, model.TaskMerged)

	var followUp, byHand *fake.Issue
	for _, i := range g.AllIssues("acme", container) {
		switch {
		case i.Title == "Cache the order totals":
			followUp = &i
		case strings.HasPrefix(i.Title, "By hand: "):
			byHand = &i
		}
	}
	if followUp == nil || !slices.Equal(followUp.FiledLabels, []string{model.LabelProposed}) ||
		followUp.InMilestone != model.FollowUpMilestone || !strings.Contains(followUp.Body, "- [ ] Totals are cached") {
		t.Errorf("the follow-up = %+v, want a proposed issue in %s with its criterion", followUp, model.FollowUpMilestone)
	}
	if byHand == nil || !slices.Contains(byHand.FiledLabels, model.LabelByHand) ||
		!strings.Contains(byHand.Body, "It looks right on a phone") || !strings.Contains(byHand.Body, "verifier approved it") ||
		!model.HasByHandMarker(byHand.Body, task) {
		t.Errorf("the by-hand issue = %+v, want the verifier's open criterion for a person", byHand)
	}
	if issue := g.Issue("acme", container, 1); issue.State != "closed" || followUp == nil || byHand == nil ||
		!w.said(1, followUp.URL) || !w.said(1, byHand.URL) {
		t.Errorf("issue 1 is %s; want it closed, a comment naming what was filed", issue.State)
	}
}
