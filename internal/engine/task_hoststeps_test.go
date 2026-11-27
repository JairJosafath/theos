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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/engine"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// The host steps the operator's Task reconcile leaves to the engine (#336,
// slice 5): the round's pull request opened as the runtime writes it, the
// failed jobs of its checks run again as the operator runs them, a failure
// whose words are the runtime's, an agent's outage kept in the record, and
// a host reached only for a step that needs it.

// failedRun is a CI run at a commit that ended failed.
const failedRun = 7

// checkingPushedAgain is the world's Task waiting for the checks of a commit
// its round pushed again, which failed, with the commit's one run ended
// failed: the run's failed jobs are due to run again (model.PushedAgain).
func (w *taskWorld) checkingPushedAgain() string {
	w.t.Helper()
	ctx := context.Background()
	w.tick()
	commit := w.implement(1)
	w.want(w.tick(), model.ActionOpenPR, model.TaskChecking)
	rec, err := w.rt.TaskStore.Progress(ctx, w.in.Task)
	if err != nil {
		w.t.Fatal(err)
	}
	rec.Progress = model.PushedAgain(rec.Progress, commit)
	if err := w.rt.TaskStore.SaveProgress(ctx, w.in.Task, rec); err != nil {
		w.t.Fatal(err)
	}
	if err := w.g.SetCommitStatus(ctx, w.repo, commit, ports.CommitStatus{Context: check,
		State: ports.CheckFailure}); err != nil {
		w.t.Fatal(err)
	}
	w.g.SetCommitRuns(owner, container, commit, ports.WorkflowRun{ID: failedRun, Workflow: "ci", Event: "push",
		Status: model.CIRunCompleted, Conclusion: "failure", URL: "http://host.test/runs/7"})
	return commit
}

// #336: the failed jobs of a commit the round pushed again run again, as the
// operator runs them: the step is the rerun's, the record waits for their
// result, and nothing more is due.
func TestTaskTickRerunsTheChecksOfACommitPushedAgain336(t *testing.T) {
	w := newTaskWorld(t)
	w.checkingPushedAgain()
	res := w.tick()
	w.want(res, model.ActionRerunChecks, model.TaskChecking)
	p := res.Record.Progress
	if p.ChecksRerunDue || !p.ChecksRerunAt.Equal(w.in.Now) {
		t.Errorf("progress = %+v, want the rerun asked for now and none due", p)
	}
	if got := w.g.Reruns(owner, container, failedRun); got != 1 {
		t.Errorf("reruns of run %d = %d, want 1", failedRun, got)
	}
}

// #336: with no failed run to run again, a commit pushed again has its
// failure read as it stands (a wait that reads the checks at once), and any
// other check fails the Task at no try, as the operator's rerunChecks did.
func TestTaskTickReadsAFailureWithNoRunToRunAgain336(t *testing.T) {
	w := newTaskWorld(t)
	commit := w.checkingPushedAgain()
	w.g.SetCommitRuns(owner, container, commit, ports.WorkflowRun{ID: failedRun, Workflow: "ci", Event: "push",
		Status: model.CIRunCompleted, Conclusion: "success"})
	res := w.tick()
	if res.Step.Action != model.ActionWait || res.Step.Recheck ||
		!strings.Contains(res.Step.Message, "no failed CI run at "+commit) {
		t.Fatalf("step = %+v, want a wait that reads the checks as they stand", res.Step)
	}
	if res.Record.Progress.ChecksRerunDue || res.Record.Progress.Phase != model.TaskChecking {
		t.Errorf("progress = %+v, want checking, no rerun due", res.Record.Progress)
	}

	// No rerun due: the runner killed the checks for lack of memory, and the
	// host lists no failed run to run again. The Task fails at no try.
	rec, _ := w.rt.TaskStore.Progress(context.Background(), w.in.Task)
	rec.Progress.ChecksRerunDue = false
	if err := w.rt.TaskStore.SaveProgress(context.Background(), w.in.Task, rec); err != nil {
		t.Fatal(err)
	}
	w.g.SetCheckLog("go test ./...\nsignal: killed\n")
	res = w.tick()
	if res.Step.Action != model.ActionFail || res.Step.Reason != model.ReasonNoCIResult ||
		!strings.Contains(res.Step.Message, "the Git host lists no failed CI run at "+commit+" to run again") ||
		res.Record.Progress.Phase != model.TaskFailed || res.Record.Reason != model.ReasonNoCIResult {
		t.Fatalf("step %+v, record %+v; want the Task failed, NoCIResult", res.Step, res.Record)
	}
}

// #336: a rerun while a run at the commit still runs waits for it to end,
// and looks again (Recheck), as the Git host runs a run's jobs again only
// once it ended.
func TestTaskTickWaitsForTheRunsBeforeARerun336(t *testing.T) {
	w := newTaskWorld(t)
	commit := w.checkingPushedAgain()
	// The facts read the run ended; the host has a run at the commit running
	// by the time the step is carried out.
	w.in.Facts = func(_ context.Context, facts model.TaskFacts) (model.TaskFacts, error) {
		facts.Checks, facts.ChecksRerunDue = ports.CheckFailure, true
		facts.CIRun = model.CIRun{Commit: commit, State: model.CIRunCompleted}
		return facts, nil
	}
	w.g.SetCommitRuns(owner, container, commit, ports.WorkflowRun{ID: failedRun, Workflow: "ci", Event: "push",
		Status: "running"})
	res := w.tick()
	if res.Step.Action != model.ActionWait || !res.Step.Recheck ||
		!strings.Contains(res.Step.Message, "waiting for the CI runs at the commit to end first") {
		t.Fatalf("step = %+v, want a wait for the runs that rechecks", res.Step)
	}
	if !res.Record.Progress.ChecksRerunDue || w.g.Reruns(owner, container, failedRun) != 0 {
		t.Errorf("progress %+v, reruns %d; want the rerun still due, none asked", res.Record.Progress,
			w.g.Reruns(owner, container, failedRun))
	}
}

// #336: a rerun the host refuses fails the Task at no try. Its words are the
// runtime's where it has them (Ending), which may keep why in the record the
// engine then ends; else the engine says it on the pull request and hands
// the issue back.
func TestTaskTickFailsWhereTheHostRefusesARerun336(t *testing.T) {
	for _, words := range []string{"the engine's", "the runtime's"} {
		t.Run(words, func(t *testing.T) {
			w := newTaskWorld(t)
			ctx := context.Background()
			w.checkingPushedAgain()
			rec, err := w.rt.TaskStore.Progress(ctx, w.in.Task)
			if err != nil {
				t.Fatal(err)
			}
			pr := rec.PullRequest
			w.g.RefuseRerun(owner, container, failedRun, true)
			var said []model.TaskStep
			if words == "the runtime's" {
				w.in.Ending = func(ctx context.Context, _ model.TaskFacts, step model.TaskStep) error {
					said = append(said, step)
					rec, err := w.rt.TaskStore.Progress(ctx, w.in.Task)
					if err != nil {
						return err
					}
					rec.Progress.Feedback = "why, as the runtime keeps it"
					return w.rt.TaskStore.SaveProgress(ctx, w.in.Task, rec)
				}
			}
			res := w.tick()
			if res.Step.Action != model.ActionFail || res.Step.Reason != model.ReasonNoCIResult ||
				!strings.Contains(res.Step.Message, "the Git host refused to run its failed jobs again") ||
				!strings.Contains(res.Step.Message, "failed at a commit the agent pushed again") {
				t.Fatalf("step = %+v, want the Task failed: the host refused the rerun", res.Step)
			}
			if res.Record.Progress.Phase != model.TaskFailed || res.Record.Reason != model.ReasonNoCIResult {
				t.Fatalf("record = %+v, want Failed, NoCIResult", res.Record)
			}
			comments := w.g.PullComments(owner, container, pr)
			issue := w.g.Issue(owner, container, w.in.Issue)
			if words == "the runtime's" {
				if len(said) != 1 || said[0].Action != model.ActionFail || len(comments) != 0 ||
					slices.Contains(issue.Labels, model.LabelNeedsHuman) {
					t.Errorf("Ending said %+v; comments %q, labels %v; want the runtime's words alone", said,
						comments, issue.Labels)
				}
				if res.Record.Progress.Feedback != "why, as the runtime keeps it" {
					t.Errorf("feedback = %q, want the record the runtime kept as it said why",
						res.Record.Progress.Feedback)
				}
				return
			}
			if len(comments) != 1 || !slices.Contains(issue.Labels, model.LabelNeedsHuman) {
				t.Errorf("comments %q, labels %v; want the failure said and the issue handed back", comments,
					issue.Labels)
			}
		})
	}
}

// #336: the round's pull request is the runtime's to write where it writes
// one (Pull): the branch it is opened from, its title and its description.
func TestTaskTickOpensThePullRequestTheRuntimeWrites336(t *testing.T) {
	w := newTaskWorld(t)
	var asked []model.TaskFacts
	w.in.Pull = func(facts model.TaskFacts) engine.PullDraft {
		asked = append(asked, facts)
		return engine.PullDraft{Head: model.TaskBranch(w.in.Task), Title: "The runtime's title",
			Body: "The runtime's description."}
	}
	w.tick()
	w.implement(1)
	res := w.tick()
	w.want(res, model.ActionOpenPR, model.TaskChecking)
	if len(asked) != 1 || asked[0].Run.Result == nil || asked[0].Run.Result.Summary != "Added the orders." {
		t.Fatalf("Pull asked with %+v, want the round's facts with its run's result", asked)
	}
	title, body := w.g.PullText(owner, container, res.Record.PullRequest)
	if title != "The runtime's title" || body != "The runtime's description." {
		t.Errorf("pull request %q, %q; want the runtime's", title, body)
	}
}

// #336: a tick given no host reaches it through Host only for a step that
// needs it, once, and on the repository Host names.
func TestTaskTickReachesTheHostOnlyForAStepThatNeedsIt336(t *testing.T) {
	w := newTaskWorld(t)
	ctx := context.Background()
	reached := 0
	w.in.Host = func(context.Context) (engine.TaskHost, ports.Repository, error) {
		reached++
		return w.g, w.repo, nil
	}
	w.in.Repo = ports.Repository{} // Host names it
	w.in.Facts = func(_ context.Context, facts model.TaskFacts) (model.TaskFacts, error) { return facts, nil }
	tickWithNoHost := func() engine.TaskResult {
		t.Helper()
		res, err := engine.TaskTick(ctx, w.rt, nil, w.in)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	w.want(tickWithNoHost(), model.ActionStartImplement, model.TaskImplementing)
	if reached != 0 {
		t.Fatalf("Host reached %d times for an offer, want none", reached)
	}
	w.implement(1)
	res := tickWithNoHost()
	w.want(res, model.ActionOpenPR, model.TaskChecking)
	if reached != 1 {
		t.Errorf("Host reached %d times for the pull request, want once", reached)
	}
	if pr, err := w.g.GetPullRequest(ctx, w.repo, res.Record.PullRequest); err != nil || pr.State != "open" {
		t.Errorf("pull request = %+v, %v; want it open on Host's repository", pr, err)
	}
}

// #336: a run whose agent is unavailable ends, and its turn waits for the
// agent in the record (AgentBack); once it is due back the turn is offered
// again, which forgets when.
func TestTaskTickWaitsForAnUnavailableAgent336(t *testing.T) {
	w := newTaskWorld(t)
	w.tick()
	back := w.in.Now.Add(30 * time.Minute).Truncate(time.Second)
	w.rt.Work.SetRun(w.in.Task, 1, model.RoleImplementer, model.RunState{Started: true, Done: true,
		Result: &model.RunResult{Unavailable: true, Error: "usage limit reached", RetryAt: back}})
	res := w.tick()
	w.want(res, model.ActionAgentUnavailable, model.TaskImplementing)
	if !res.Record.AgentBack.Equal(back) {
		t.Errorf("agent back = %v, want %v", res.Record.AgentBack, back)
	}
	ends := w.rt.Work.Ends()
	if len(ends) != 1 || ends[0].Role != model.RoleImplementer || ends[0].Round != 1 {
		t.Errorf("ends = %+v, want the implementer's run ended", ends)
	}

	res = w.tick()
	if res.Step.Action != model.ActionWait || !strings.Contains(res.Step.Message, "The agent is unavailable") {
		t.Fatalf("step = %+v, want a wait for the agent", res.Step)
	}
	if offers := w.rt.Work.Offers(); len(offers) != 1 {
		t.Errorf("offers = %+v, want none more while the agent is away", offers)
	}

	w.in.Now = back.Add(time.Second)
	res = w.tick()
	w.want(res, model.ActionStartImplement, model.TaskImplementing)
	if !res.Record.AgentBack.IsZero() || len(w.rt.Work.Offers()) != 2 {
		t.Errorf("agent back %v, offers %d; want the turn offered again, when forgotten", res.Record.AgentBack,
			len(w.rt.Work.Offers()))
	}
}
