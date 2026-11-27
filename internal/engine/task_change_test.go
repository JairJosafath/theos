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
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/engine"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

// A Task's own ends and the Standard change on the Task tick (#336, slice
// 7): a proposal filed through the runtime's Proposer, which blocks the
// Task, the round resumed once the change merged and a resumed round the
// change left nothing to do ended done; an issue that needs no change closed;
// and a pull request put up for a person, in the runtime's words where it
// has them (TaskInput.Ending).

// proposer is a runtime's half of a Standard change: out is what Propose
// returns, and a filing (out zero) keeps the change in the store, which
// resumes it in no round yet, as the operator's new change does.
type proposer struct {
	store *fake.Tasks
	task  string
	out   engine.Proposed
	calls int
}

var _ engine.Proposer = (*proposer)(nil)

func (p *proposer) Propose(ctx context.Context, _ model.TaskFacts, _ model.TaskStep) (engine.Proposed, error) {
	p.calls++
	if p.out != (engine.Proposed{}) {
		return p.out, nil
	}
	rec, err := p.store.Progress(ctx, p.task)
	if err != nil {
		return engine.Proposed{}, err
	}
	rec.Progress.ResumedRound = 0
	return engine.Proposed{}, p.store.SaveProgress(ctx, p.task, rec)
}

// proposal is round 1's implement run done with a proposal.
var proposal = model.RunState{Started: true, Done: true, Result: &model.RunResult{Commit: "abc1234",
	Summary: "Needs the Makefile changed.", Proposal: &model.Proposal{Branch: "proposal/orders",
		Paths: []string{"Makefile"}}}}

// saying records the steps the runtime was asked to say (TaskInput.Ending).
func (w *taskWorld) saying() *[]model.TaskAction {
	var said []model.TaskAction
	w.in.Ending = func(_ context.Context, _ model.TaskFacts, step model.TaskStep) error {
		said = append(said, step.Action)
		return nil
	}
	return &said
}

// implementEnded reports whether the world's Workers ended round's implement
// turn.
func (w *taskWorld) implementEnded(round int) bool {
	for _, e := range w.rt.Work.Ends() {
		if e.Round == round && e.Role == model.RoleImplementer {
			return true
		}
	}
	return false
}

// #336: a runtime that files Standard changes (engine.Proposer) has the
// implementer's proposal filed, and the engine ends the run and blocks the
// Task on the record the Proposer kept, the round's summary with it; a
// proposal the Proposer refuses fails the Task in the runtime's words, and
// one whose issue closed ends it with nothing said.
func TestTaskTickBlocksATaskOnTheStandardChangeItsRuntimeFiles336(t *testing.T) {
	for _, tc := range []struct {
		name   string
		out    engine.Proposed
		phase  string
		reason string
		said   []model.TaskAction
	}{
		{name: "filed", phase: model.TaskBlocked},
		{name: "refused", out: engine.Proposed{Instead: &model.TaskStep{Action: model.ActionFail,
			Reason: model.ReasonFailed, Message: "a Task with no Crew can't propose a Standard change"}},
			phase: model.TaskFailed, reason: model.ReasonFailed, said: []model.TaskAction{model.ActionFail}},
		{name: "issue closed", out: engine.Proposed{End: &model.TaskEnd{Phase: model.TaskFailed,
			Reason: "IssueClosed", Message: "Issue #1 was closed before the Task finished"}},
			phase: model.TaskFailed, reason: "IssueClosed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newTaskWorld(t)
			ctx := context.Background()
			w.want(w.tick(), model.ActionStartImplement, model.TaskImplementing)
			// The round resumed after an earlier change: the new one is
			// resumed in no round, as the Proposer keeps it.
			rec, err := w.rt.TaskStore.Progress(ctx, w.in.Task)
			if err != nil {
				t.Fatal(err)
			}
			rec.Progress.ResumedRound = 1
			if err := w.rt.TaskStore.SaveProgress(ctx, w.in.Task, rec); err != nil {
				t.Fatal(err)
			}
			p := &proposer{store: w.rt.TaskStore, task: w.in.Task, out: tc.out}
			w.in.Proposer = p
			said := w.saying()
			w.setImplement(proposal)
			res := w.tick()
			if res.Record.Progress.Phase != tc.phase || res.Record.Reason != tc.reason || p.calls != 1 {
				t.Fatalf("step %+v, record %+v, proposed %d times; want phase %s, reason %q", res.Step, res.Record,
					p.calls, tc.phase, tc.reason)
			}
			if !w.implementEnded(1) {
				t.Error("the implementer's run was not ended")
			}
			if strings.Join(actions(*said), " ") != strings.Join(actions(tc.said), " ") {
				t.Errorf("the runtime said %v, want %v", *said, tc.said)
			}
			if tc.phase != model.TaskBlocked {
				return
			}
			if got := res.Record.Progress; got.Commit != "abc1234" || got.ResumedRound != 0 ||
				res.Record.Summary != "Needs the Makefile changed." {
				t.Errorf("record %+v, want the round's commit and summary, resumed in no round", res.Record)
			}
			w.want(w.tick(), model.ActionWait, model.TaskBlocked)
		})
	}
}

// #336: without a Proposer the Standard change is not built: its steps wait,
// saying so, and the run is left as it is, as every runtime did before.
func TestTaskTickWaitsOnAStandardChangeWhereNoRuntimeFilesOne336(t *testing.T) {
	w := newTaskWorld(t)
	w.want(w.tick(), model.ActionStartImplement, model.TaskImplementing)
	w.setImplement(proposal)
	res := w.tick()
	if res.Step.Action != model.ActionWait || res.Step.Reason != engine.ReasonNotBuilt ||
		!strings.Contains(res.Step.Message, string(model.ActionPropose)) {
		t.Fatalf("step %+v, want Propose not built", res.Step)
	}
	if w.implementEnded(1) {
		t.Error("the implementer's run was ended")
	}
	w.in.Facts = func(_ context.Context, facts model.TaskFacts) (model.TaskFacts, error) {
		facts.Resumed = true
		facts.Run.Result = &model.RunResult{Empty: true}
		return facts, nil
	}
	if res := w.tick(); res.Step.Reason != engine.ReasonNotBuilt ||
		!strings.Contains(res.Step.Message, string(model.ActionDoneByStandardChange)) {
		t.Errorf("step %+v, want DoneByStandardChange not built", res.Step)
	}
}

// #336: with the change merged (the runtime's facts, model.ChangeFacts) the
// engine resumes the round in place, telling the implementer, in the
// runtime's words; the round's next run is offered, and one that finds
// nothing beyond main ends the Task Merged, done by the change.
func TestTaskTickResumesAndEndsATaskByItsStandardChange336(t *testing.T) {
	w := newTaskWorld(t)
	w.want(w.tick(), model.ActionStartImplement, model.TaskImplementing)
	w.in.Proposer = &proposer{store: w.rt.TaskStore, task: w.in.Task}
	said := w.saying()
	w.setImplement(proposal)
	w.want(w.tick(), model.ActionPropose, model.TaskBlocked)
	merged := false
	w.in.Facts = func(_ context.Context, facts model.TaskFacts) (model.TaskFacts, error) {
		facts.Change = model.ChangeFacts{Issue: 9, Paths: []string{"Makefile"}, Merged: merged}
		facts.Resumed = facts.Phase == model.TaskImplementing && facts.Round == 1
		return facts, nil
	}
	w.want(w.tick(), model.ActionWait, model.TaskBlocked)
	merged = true
	res := w.tick()
	w.want(res, model.ActionResume, model.TaskImplementing)
	if p := res.Record.Progress; p.ResumedRound != 1 || p.Restarts != 1 || !strings.Contains(p.Feedback, "#9") {
		t.Errorf("progress %+v, want round 1 resumed afresh, told of change #9", p)
	}
	w.want(w.tick(), model.ActionStartImplement, model.TaskImplementing)
	w.rt.Work.SetRun(w.in.Task, 1, model.RoleImplementer, model.RunState{Started: true, Done: true,
		Result: &model.RunResult{Empty: true, Summary: "Nothing left."}})
	res = w.tick()
	w.want(res, model.ActionDoneByStandardChange, model.TaskMerged)
	if res.Record.Reason != model.ReasonDoneByStandardChange {
		t.Errorf("reason %q, want %s", res.Record.Reason, model.ReasonDoneByStandardChange)
	}
	if got := strings.Join(actions(*said), " "); got != "Resume DoneByStandardChange" {
		t.Errorf("the runtime said %q, want the resume and the end", got)
	}
}

// #336: an implementer that finds its issue needs no change ends the Task
// Merged, NothingToDo, its reason the Task's summary: the engine closes the
// issue with the agent's words, or the runtime does where it says what a
// step says. Before, the engine waited there, not built. A "nothing to do"
// on a branch with a pull request is the round with no change it was (#72).
func TestTaskTickEndsATaskWithNothingToDo336(t *testing.T) {
	nothing := model.RunState{Started: true, Done: true, Result: &model.RunResult{
		Summary: "Looked.\nNothing to do: all is current.", Nothing: "all is current."}}
	for _, runtimeSays := range []bool{false, true} {
		w := newTaskWorld(t)
		w.want(w.tick(), model.ActionStartImplement, model.TaskImplementing)
		var said *[]model.TaskAction
		if runtimeSays {
			said = w.saying()
		}
		w.setImplement(nothing)
		res := w.tick()
		w.want(res, model.ActionNothingToDo, model.TaskMerged)
		if res.Record.Reason != model.ReasonNothingToDo || res.Record.Summary != "all is current." {
			t.Errorf("runtime says %v: record %+v, want NothingToDo with the agent's reason", runtimeSays, res.Record)
		}
		if !w.implementEnded(1) {
			t.Errorf("runtime says %v: the implementer's run was not ended", runtimeSays)
		}
		issue := w.g.Issue(owner, container, w.in.Issue)
		if closed := issue.State == "closed"; closed == runtimeSays {
			t.Errorf("runtime says %v: issue closed %v, want it closed by the engine only", runtimeSays, closed)
		}
		if runtimeSays {
			if len(*said) != 1 || (*said)[0] != model.ActionNothingToDo {
				t.Errorf("the runtime said %v, want the end", *said)
			}
			continue
		}
		if last := issue.Comments[len(issue.Comments)-1]; last != model.NothingComment(w.in.Task, "all is current.") {
			t.Errorf("the issue's last comment %q, want the agent's words", last)
		}
	}

	// Round 2, its pull request open: the reviewer judges the branch.
	w := newTaskWorld(t)
	w.tick()
	commit := w.implement(1)
	w.tick()
	w.checksPass(commit)
	w.want(w.tick(), model.ActionStartReview, model.TaskReviewing)
	w.review(1, model.VerdictChanges, "Add a test.")
	w.want(w.tick(), model.ActionRetry, model.TaskImplementing)
	w.tick() // round 2 offered
	w.rt.Work.SetRun(w.in.Task, 2, model.RoleImplementer, nothing)
	if res := w.tick(); res.Step.Action == model.ActionNothingToDo || res.Record.Progress.Phase == model.TaskMerged {
		t.Errorf("step %+v, record %+v; want round 2's no change failed, not the Task ended", res.Step, res.Record)
	}
	if issue := w.g.Issue(owner, container, w.in.Issue); issue.State == "closed" {
		t.Error("the issue was closed")
	}
}

// #336: a pull request put up for a person's approval says so in the
// runtime's words where it has them (TaskInput.Ending), and the engine posts
// no comment of its own; the approving run ends and the Task waits, as
// before.
func TestTaskTickPutsAPullRequestUpForAPersonInTheRuntimesWords336(t *testing.T) {
	for _, runtimeSays := range []bool{false, true} {
		w := newTaskWorld(t)
		rt := mergingRuntime{store: w.rt.TaskStore, workers: w.rt.Work, merges: true}
		pr, _ := w.approved(rt)
		w.in.Spec, w.in.SpecApproval = true, model.SpecApprovalPerson
		var said *[]model.TaskAction
		if runtimeSays {
			said = w.saying()
		}
		w.want(w.tickOn(rt), model.ActionAwaitApproval, model.TaskAwaitingApproval)
		if !w.reviewEnded() {
			t.Errorf("runtime says %v: the review's run was not ended", runtimeSays)
		}
		comments := w.g.PullComments(owner, container, pr)
		if runtimeSays {
			if len(comments) != 0 || len(*said) != 1 || (*said)[0] != model.ActionAwaitApproval {
				t.Errorf("comments %q, the runtime said %v; want the runtime's words alone", comments, *said)
			}
			continue
		}
		if len(comments) != 1 || !strings.Contains(comments[0], "approve") {
			t.Errorf("comments %q, want the engine's", comments)
		}
	}
}
