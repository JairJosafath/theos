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
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

// The parts of the Task tick a runtime that reads and carries out more than
// the engine does yet gives it (#336): its own facts (TaskInput.Facts), the
// steps it carries out itself (TaskInput.Carry), and Workers that ready a
// turn before it is offered (ports.Preparer), as the operator's do.

// preparingWorkers are fake Workers that ready each turn first: ready says
// whether a turn is, and prepared are the orders asked about.
type preparingWorkers struct {
	*fake.Workers
	ready bool
	// readying runs as each turn is readied, as a runtime says why it holds
	// one, or changes the record readying it.
	readying func()
	prepared []ports.WorkOrder
}

var _ ports.Preparer = (*preparingWorkers)(nil)

func (w *preparingWorkers) Prepare(_ context.Context, order ports.WorkOrder) (bool, error) {
	w.prepared = append(w.prepared, order)
	if w.readying != nil {
		w.readying()
	}
	return w.ready, nil
}

// preparingRuntime is a runtime on the world's store with preparingWorkers.
type preparingRuntime struct {
	store   *fake.Tasks
	workers *preparingWorkers
}

func (r preparingRuntime) Store() ports.TaskStore           { return r.store }
func (r preparingRuntime) Workers() ports.Workers           { return r.workers }
func (r preparingRuntime) Capabilities() ports.Capabilities { return model.OperatorCapabilities() }

func (w *taskWorld) preparing(ready bool) (preparingRuntime, *preparingWorkers) {
	workers := &preparingWorkers{Workers: w.rt.Work, ready: ready}
	return preparingRuntime{store: w.rt.TaskStore, workers: workers}, workers
}

// #336: Workers not ready for a turn hold its offer: the engine saves no
// start and offers nothing, says the offer is held, and reads the record
// back from the store, which the runtime may have changed meanwhile.
func TestTaskTickHoldsAnOfferItsWorkersAreNotReadyFor336(t *testing.T) {
	w := newTaskWorld(t)
	rt, workers := w.preparing(false)
	ctx := context.Background()
	workers.readying = func() {
		// The runtime says why on the Task's record, as the operator's
		// status does; the engine must not write over it.
		rec, err := rt.store.Progress(ctx, w.in.Task)
		if err != nil {
			t.Fatal(err)
		}
		rec.Summary = "waiting for the Agent's credentials"
		if err := rt.store.SaveProgress(ctx, w.in.Task, rec); err != nil {
			t.Fatal(err)
		}
	}
	res, err := engine.TaskTick(ctx, rt, w.g, w.in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Step.Action != model.ActionWait || res.Step.Reason != engine.ReasonOfferHeld {
		t.Fatalf("step = %+v, want a wait, the offer held", res.Step)
	}
	if res.Record.Progress.Phase != model.TaskPending || res.Record.Summary != "waiting for the Agent's credentials" {
		t.Errorf("record = %+v, want the store's: Pending, with the runtime's word", res.Record)
	}
	if offers := w.rt.Work.Offers(); len(offers) != 0 {
		t.Errorf("offers = %+v, want none", offers)
	}
	if len(workers.prepared) != 1 || workers.prepared[0].Round != 1 ||
		workers.prepared[0].Role != model.RoleImplementer {
		t.Fatalf("prepared = %+v, want round 1's implement turn asked about", workers.prepared)
	}

	// Ready on the next tick: the start is saved, and the turn the Workers
	// readied is the one offered.
	workers.ready, workers.readying = true, nil
	res, err = engine.TaskTick(ctx, rt, w.g, w.in)
	if err != nil {
		t.Fatal(err)
	}
	w.want(res, model.ActionStartImplement, model.TaskImplementing)
	offers := w.rt.Work.Offers()
	if len(offers) != 1 || offers[0] != workers.prepared[1] {
		t.Errorf("offers = %+v, want the prepared %+v", offers, workers.prepared[1:])
	}
}

// #336: what readying a turn changed in the Task's record stays: the start
// is saved on the record as the store keeps it then, and the turn offered
// carries it, as the operator takes over an earlier try's pull request and
// feedback before its first run.
func TestTaskTickStartsOnTheRecordTheWorkersReadied336(t *testing.T) {
	w := newTaskWorld(t)
	rt, workers := w.preparing(true)
	ctx := context.Background()
	workers.readying = func() {
		rec, err := rt.store.Progress(ctx, w.in.Task)
		if err != nil {
			t.Fatal(err)
		}
		rec.PullRequest, rec.Progress.Feedback = 7, "Handle an empty order."
		if err := rt.store.SaveProgress(ctx, w.in.Task, rec); err != nil {
			t.Fatal(err)
		}
	}
	res, err := engine.TaskTick(ctx, rt, w.g, w.in)
	if err != nil {
		t.Fatal(err)
	}
	w.want(res, model.ActionStartImplement, model.TaskImplementing)
	if res.Record.PullRequest != 7 || res.Record.Progress.Feedback != "Handle an empty order." {
		t.Errorf("record = %+v, want the earlier try's pull request and feedback kept", res.Record)
	}
	if offers := w.rt.Work.Offers(); len(offers) != 1 || offers[0].Feedback != "Handle an empty order." {
		t.Errorf("offers = %+v, want the turn offered with the feedback readied", offers)
	}
	if prepared := workers.prepared; len(prepared) != 1 || prepared[0].Feedback != "" {
		t.Errorf("prepared = %+v, want the turn asked about as the record was", prepared)
	}
}

// #336: a runtime's Facts replace the engine's reads of the host: they get
// the record's facts and the run the Workers read back, the decision is on
// what they return, and a tick with no host reads none.
func TestTaskTickDecidesOnTheRuntimesFacts336(t *testing.T) {
	w := newTaskWorld(t)
	ctx := context.Background()
	w.tick() // round 1 offered
	w.rt.Work.SetRun(w.in.Task, 1, model.RoleImplementer, model.RunState{Started: true})
	var got model.TaskFacts
	w.in.Facts = func(_ context.Context, facts model.TaskFacts) (model.TaskFacts, error) {
		got = facts
		facts.Pause = "the Crew is paused"
		return facts, nil
	}
	res, err := engine.TaskTick(ctx, w.rt, nil, w.in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != model.TaskImplementing || got.Round != 1 || !got.Run.Started {
		t.Errorf("Facts got %+v, want round 1 implementing, with the started run", got)
	}
	if res.Step.Pause != model.PausePausing || !strings.Contains(res.Step.Message, "the Crew is paused") {
		t.Errorf("step = %+v, want the run pausing, as the runtime's facts say", res.Step)
	}
}

// #336: Carry is asked first: a step it carries out is the runtime's alone,
// and the engine reads its record back; a step it leaves is the engine's.
func TestTaskTickLeavesTheRuntimeItsSteps336(t *testing.T) {
	w := newTaskWorld(t)
	var asked []model.TaskAction
	w.in.Carry = func(ctx context.Context, _ model.TaskFacts, step model.TaskStep) (bool, error) {
		asked = append(asked, step.Action)
		if step.Action != model.ActionOpenPR {
			return false, nil
		}
		// The runtime opens the pull request its own way, and keeps it.
		rec, err := w.rt.TaskStore.Progress(ctx, w.in.Task)
		if err != nil {
			return true, err
		}
		rec.Progress = model.OpenedPR(rec.Progress, "abc1234")
		rec.PullRequest = 42
		return true, w.rt.TaskStore.SaveProgress(ctx, w.in.Task, rec)
	}
	w.want(w.tick(), model.ActionStartImplement, model.TaskImplementing) // left to the engine
	w.answer()
	res := w.tick()
	w.want(res, model.ActionOpenPR, model.TaskChecking)
	if res.Record.PullRequest != 42 {
		t.Errorf("record = %+v, want the runtime's pull request", res.Record)
	}
	if pulls := w.g.PullComments(owner, container, 1); len(pulls) != 0 {
		t.Errorf("the engine commented %q on a pull request the runtime keeps", pulls)
	}
	if ends := w.rt.Work.Ends(); len(ends) != 0 {
		t.Errorf("ends = %+v, want the engine to end no turn the runtime carried on", ends)
	}
	if strings.Join(actions(asked), ",") != "StartImplement,OpenPR" {
		t.Errorf("Carry asked about %v, want each step decided", asked)
	}
}

// #336: a tick with no host carries out a turn's offer, and refuses, not
// panics, a step that needs the host.
func TestTaskTickWithNoHost336(t *testing.T) {
	w := newTaskWorld(t)
	ctx := context.Background()
	w.in.Facts = func(_ context.Context, facts model.TaskFacts) (model.TaskFacts, error) {
		facts.ContainerReady, facts.Protected, facts.Base = true, true, ports.CheckSuccess
		return facts, nil
	}
	res, err := engine.TaskTick(ctx, w.rt, nil, w.in)
	if err != nil {
		t.Fatal(err)
	}
	w.want(res, model.ActionStartImplement, model.TaskImplementing)
	w.answer()
	if _, err := engine.TaskTick(ctx, w.rt, nil, w.in); err == nil ||
		!strings.Contains(err.Error(), "no host") {
		t.Errorf("err = %v, want the pull request refused for want of a host", err)
	}
	w.in.Facts = nil
	if _, err := engine.TaskTick(ctx, w.rt, nil, w.in); err == nil ||
		!strings.Contains(err.Error(), "no host") {
		t.Errorf("err = %v, want the facts refused for want of a host", err)
	}
}

// answer plays the implementer's session of round 1, done with a commit
// the tests' runtimes never push.
func (w *taskWorld) answer() {
	w.rt.Work.SetRun(w.in.Task, 1, model.RoleImplementer, model.RunState{Started: true, Done: true,
		Result: &model.RunResult{Commit: "abc1234", Summary: "Added the orders."}})
}

func actions(steps []model.TaskAction) []string {
	out := make([]string, 0, len(steps))
	for _, a := range steps {
		out = append(out, string(a))
	}
	return out
}

// #336: Workers that ready a turn (ports.Preparer) have a restarted turn
// offered by a later tick: the restart ends the run and saves, and the next
// tick decides the turn's start again and readies it first, as the
// operator's start's gates run for the new Sandbox. The lead runtime's
// Workers ready nothing, and offer it in the same tick
// (TestTaskTickLeaseRunsOut334).
func TestTaskTickRestartsOnALaterTickWhereTheWorkersReadyTurns336(t *testing.T) {
	for _, tc := range []struct {
		name string
		// play brings the Task to the turn that restarts, and says how
		// many turns were offered then.
		play   func(w *taskWorld, rt preparingRuntime) int
		action model.TaskAction
		role   string
		start  model.TaskAction
		phase  string
	}{{
		name: "an interrupted run runs again",
		play: func(w *taskWorld, rt preparingRuntime) int {
			w.tickOn(rt) // round 1 offered
			w.rt.Work.SetRun(w.in.Task, 1, model.RoleImplementer, model.RunState{Started: true,
				Markers: &model.MarkerRun{EndDue: model.EndSilent}})
			return 1
		},
		action: model.ActionRerun, role: model.RoleImplementer, start: model.ActionStartImplement,
		phase: model.TaskImplementing,
	}, {
		name: "a review with no verdict is tried again",
		play: func(w *taskWorld, rt preparingRuntime) int {
			w.tickOn(rt)
			commit := w.implement(1)
			w.tickOn(rt) // the pull request
			w.checksPass(commit)
			w.tickOn(rt) // the review offered
			w.review(1, "", "")
			return 2
		},
		action: model.ActionRetryReview, role: model.RoleReviewer, start: model.ActionStartReview,
		phase: model.TaskReviewing,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			w := newTaskWorld(t)
			rt, workers := w.preparing(true)
			offered := tc.play(w, rt)
			readied := len(workers.prepared)

			res := w.tickOn(rt)
			w.want(res, tc.action, tc.phase)
			if offers := w.rt.Work.Offers(); len(offers) != offered {
				t.Errorf("offers = %+v, want none more in the tick that restarted the turn", offers)
			}
			if len(workers.prepared) != readied {
				t.Errorf("prepared = %+v, want nothing readied in the tick that restarted the turn",
					workers.prepared)
			}
			ends := w.rt.Work.Ends()
			if last := ends[len(ends)-1]; last.Role != tc.role || last.Round != 1 || last.Why != "done" {
				t.Errorf("ends = %+v, want round 1's %s turn ended", ends, tc.role)
			}

			// The next tick starts the turn again, readied first.
			w.want(w.tickOn(rt), tc.start, tc.phase)
			offers := w.rt.Work.Offers()
			if len(offers) != offered+1 || offers[offered].Role != tc.role || offers[offered].Round != 1 {
				t.Fatalf("offers = %+v, want round 1's %s turn offered again", offers, tc.role)
			}
			if len(workers.prepared) != readied+1 || workers.prepared[readied].Role != tc.role {
				t.Errorf("prepared = %+v, want the restarted turn readied before its offer", workers.prepared)
			}
		})
	}
}

// #336: a runtime's Ending says what a turn's end says, in place of the
// engine's words on the pull request, before the Workers end the run; and
// with it, a round not accepted needs no host.
func TestTaskTickLeavesATurnsWordsToTheRuntime336(t *testing.T) {
	w := newTaskWorld(t)
	ctx := context.Background()
	w.tick()
	commit := w.implement(1)
	pr := w.tick().Record.PullRequest
	w.checksPass(commit)
	w.tick()
	w.review(1, model.VerdictChanges, "Handle an empty order.")

	var said []model.TaskStep
	var endsBefore int
	w.in.Ending = func(_ context.Context, facts model.TaskFacts, step model.TaskStep) error {
		said = append(said, step)
		endsBefore = len(w.rt.Work.Ends())
		if facts.Run.Result == nil || facts.Run.Result.Verdict != model.VerdictChanges {
			t.Errorf("Ending got run %+v, want the review's, before it ends", facts.Run)
		}
		return nil
	}
	w.in.Facts = func(_ context.Context, facts model.TaskFacts) (model.TaskFacts, error) { return facts, nil }
	endsAtStart := len(w.rt.Work.Ends())
	res, err := engine.TaskTick(ctx, w.rt, nil, w.in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Step.Action != model.ActionRetry || res.Record.Progress.Round != 2 {
		t.Fatalf("step %+v, record %+v; want round 2", res.Step, res.Record.Progress)
	}
	if len(said) != 1 || said[0].Action != model.ActionRetry {
		t.Fatalf("Ending said %+v, want the round not accepted", said)
	}
	if endsBefore != endsAtStart {
		t.Errorf("Ending ran after %d turns ended, want it before the review's run ends", endsBefore-endsAtStart)
	}
	if ends := w.rt.Work.Ends(); len(ends) != endsAtStart+1 || ends[len(ends)-1].Role != model.RoleReviewer {
		t.Errorf("ends = %+v, want the review's turn ended", ends)
	}
	if comments := w.g.PullComments(owner, container, pr); len(comments) != 0 {
		t.Errorf("pull request comments = %q, want the runtime's words alone", comments)
	}

	// Round 2, on the host again: its request for changes is the runtime's
	// to say too.
	w.in.Facts = nil
	w.want(w.tick(), model.ActionStartImplement, model.TaskImplementing)
	commit = w.implement(2)
	w.tick()
	w.checksPass(commit)
	w.want(w.tick(), model.ActionStartReview, model.TaskReviewing)
	w.review(2, model.VerdictChanges, "Handle a negative quantity.")
	res = w.tick()
	if res.Step.Action != model.ActionRetry || res.Record.Progress.Round != 3 || len(said) != 2 {
		t.Fatalf("step %+v, round %d, Ending said %+v; want round 3, said by the runtime", res.Step,
			res.Record.Progress.Round, said)
	}
	for _, c := range w.g.PullComments(owner, container, pr) {
		if strings.Contains(c, "asks for changes") || strings.Contains(c, "not accepted") {
			t.Errorf("the engine said %q, want the runtime's words alone", c)
		}
	}
}

// tickOn runs one Task tick on rt, failing on an error.
func (w *taskWorld) tickOn(rt ports.Runtime) engine.TaskResult {
	w.t.Helper()
	res, err := engine.TaskTick(context.Background(), rt, w.g, w.in)
	if err != nil {
		w.t.Fatalf("TaskTick: %v", err)
	}
	return res
}
