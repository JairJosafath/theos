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

package contract

import (
	"context"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// summaryAdded is the implementer's summary the suites answer with.
const summaryAdded = "Added the orders."

// agentImplementer is the Agent the suites offer an implement turn to.
const agentImplementer = "implementer"

// WorkersEnv builds the Workers under test.
type WorkersEnv struct {
	// New returns fresh Workers and a Task they may offer turns of, whose
	// work goes on its branch.
	New func(t *testing.T) (w ports.Workers, task string, issue int)
	// Answer plays the session of an offered turn: it claims the turn and
	// answers it with result, the implementer's commit on the Task's
	// branch or the reviewer's verdict.
	Answer func(t *testing.T, task string, round int, role string, result model.RunResult)
	// Lapse lets the claim of an offered turn run out unanswered: a session
	// claims it and goes away past its lease and grace.
	Lapse func(t *testing.T, task string, round int, role string)
	// Cannot names the subtests the Workers can't run, with why.
	Cannot map[string]string
}

// Workers states what internal/engine relies on of every Workers: a turn
// not offered has not started; an offered turn has, unanswered, and
// offering it again offers nothing more; an answered turn is done with its
// result; a turn whose claim ran out reads as due to end, or interrupted,
// and ended it reads interrupted until it is offered again, which starts
// it afresh.
func Workers(t *testing.T, env WorkersEnv) {
	t.Helper()
	ctx, cancel := suiteContext()
	defer cancel()
	runSubtests(t, workersSuite{ctx: ctx, env: env}, env.Cannot, []subtest[workersSuite]{
		{"offers a turn once, unanswered until a session answers it", workersSuite.offers},
		{"ends a turn whose claim ran out, and offers it afresh", workersSuite.lapses},
	})
}

// workersSuite holds what every subtest of Workers shares.
type workersSuite struct {
	ctx context.Context
	env WorkersEnv
}

// run is the run of role's turn in round 1 of task.
func (s workersSuite) run(t *testing.T, w ports.Workers, task, role string) model.RunState {
	t.Helper()
	return must(w.Run(s.ctx, task, 1, role))(t)
}

func (s workersSuite) offers(t *testing.T) {
	w, task, issue := s.env.New(t)
	if run := s.run(t, w, task, model.RoleImplementer); run.Started {
		t.Fatalf("a turn never offered = %+v, want not started", run)
	}
	order := ports.WorkOrder{Task: task, Issue: issue, Branch: model.TaskBranch(task), Round: 1,
		Role: model.RoleImplementer, Agent: agentImplementer}
	for range 2 {
		if err := w.Offer(s.ctx, order); err != nil {
			t.Fatal(err)
		}
	}
	if run := s.run(t, w, task, model.RoleImplementer); !run.Started || run.Done {
		t.Fatalf("an offered turn = %+v, want started, not done", run)
	}
	if run := s.run(t, w, task, model.RoleReviewer); run.Started {
		t.Errorf("the review, not offered = %+v, want not started", run)
	}
	s.env.Answer(t, task, 1, model.RoleImplementer, model.RunResult{Summary: summaryAdded})
	run := s.run(t, w, task, model.RoleImplementer)
	if !run.Done || run.Interrupted || run.Result == nil || run.Result.Summary != summaryAdded {
		t.Fatalf("an answered turn = %+v, want done with its result", run)
	}
	// Ended once answered, it stays answered until the next offer.
	if err := w.End(s.ctx, task, 1, model.RoleImplementer, "done"); err != nil {
		t.Fatal(err)
	}
	review := order
	review.Role, review.Agent = model.RoleReviewer, "reviewer"
	if err := w.Offer(s.ctx, review); err != nil {
		t.Fatal(err)
	}
	if run := s.run(t, w, task, model.RoleReviewer); !run.Started || run.Done {
		t.Errorf("the review offered = %+v, want started, not done", run)
	}
}

func (s workersSuite) lapses(t *testing.T) {
	w, task, issue := s.env.New(t)
	order := ports.WorkOrder{Task: task, Issue: issue, Branch: model.TaskBranch(task), Round: 1,
		Role: model.RoleImplementer, Agent: agentImplementer}
	if err := w.Offer(s.ctx, order); err != nil {
		t.Fatal(err)
	}
	s.env.Lapse(t, task, 1, model.RoleImplementer)
	run := s.run(t, w, task, model.RoleImplementer)
	due := run.Markers != nil && run.Markers.EndDue == model.EndSilent
	if interrupted := run.Done && run.Interrupted; !due && !interrupted {
		t.Fatalf("a turn whose claim ran out = %+v, want an end due or interrupted", run)
	}
	if err := w.End(s.ctx, task, 1, model.RoleImplementer, model.EndSilent); err != nil {
		t.Fatal(err)
	}
	if run := s.run(t, w, task, model.RoleImplementer); !run.Done || !run.Interrupted {
		t.Fatalf("an ended turn = %+v, want done and interrupted", run)
	}
	if err := w.Offer(s.ctx, order); err != nil {
		t.Fatal(err)
	}
	if run := s.run(t, w, task, model.RoleImplementer); !run.Started || run.Done {
		t.Errorf("the turn offered again = %+v, want started afresh, not done", run)
	}
}

// SuspendingWorkersEnv builds Workers that keep a turn's run in place
// (ports.Suspender, ports.Resumer), as the operator's Sandboxes do (#336).
type SuspendingWorkersEnv struct {
	WorkersEnv
	// Vacate plays the runtime letting a suspended turn's worker go, as
	// Agent Sandbox deletes a suspended Sandbox's pod: only then is the run
	// resumed. Gone already, it does nothing.
	Vacate func(t *testing.T, task string, round int, role string)
}

// SuspendingWorkers states what internal/engine relies on of Workers that
// keep a run in place: a working turn suspended reads suspended, and why
// (paused, or waiting for its agent), not done; suspended again, the suspend
// is not fresh, so the engine counts one resume. Its worker gone, the turn
// resumes as the same turn, not offered again: it reads started, not
// suspended and not done, and its answer reads as any turn's.
func SuspendingWorkers(t *testing.T, env SuspendingWorkersEnv) {
	t.Helper()
	ctx, cancel := suiteContext()
	defer cancel()
	runSubtests(t, suspendingSuite{workersSuite{ctx: ctx, env: env.WorkersEnv}, env}, env.Cannot,
		[]subtest[suspendingSuite]{
			{"suspends a working turn in place, once", suspendingSuite.suspends},
			{"resumes a suspended turn in place, as the same turn", suspendingSuite.resumes},
		})
}

// suspendingSuite holds what every subtest of SuspendingWorkers shares.
type suspendingSuite struct {
	workersSuite
	env SuspendingWorkersEnv
}

// working offers round 1's implement turn on fresh Workers, which must
// keep runs in place.
func (s suspendingSuite) working(t *testing.T) (ports.Workers, ports.Suspender, ports.Resumer, ports.WorkOrder) {
	t.Helper()
	w, task, issue := s.env.New(t)
	suspender, ok := w.(ports.Suspender)
	resumer, ok2 := w.(ports.Resumer)
	if !ok || !ok2 {
		t.Fatalf("Workers %T suspend or resume no run in place", w)
	}
	order := ports.WorkOrder{Task: task, Issue: issue, Branch: model.TaskBranch(task), Round: 1,
		Role: model.RoleImplementer, Agent: agentImplementer}
	if err := w.Offer(s.ctx, order); err != nil {
		t.Fatal(err)
	}
	return w, suspender, resumer, order
}

func (s suspendingSuite) suspends(t *testing.T) {
	for _, why := range []ports.Suspension{
		{Kind: ports.SuspendInterrupted},
		{Kind: ports.SuspendPaused},
		{Kind: ports.SuspendForAgent, AgentBack: time.Now().Add(time.Hour)},
	} {
		w, suspender, _, order := s.working(t)
		for i, want := range []bool{true, false} {
			fresh, err := suspender.Suspend(s.ctx, order.Task, 1, order.Role, why)
			if err != nil {
				t.Fatal(err)
			}
			if fresh != want {
				t.Errorf("%s suspend %d: fresh = %v, want %v: a repeat suspends again, counted once",
					why.Kind, i+1, fresh, want)
			}
		}
		run := s.run(t, w, order.Task, order.Role)
		if !run.Started || !run.Suspended || run.Done {
			t.Errorf("%s: a suspended turn = %+v, want started and suspended, not done", why.Kind, run)
		}
		if run.Paused != (why.Kind == ports.SuspendPaused) || run.AgentWait != (why.Kind == ports.SuspendForAgent) {
			t.Errorf("%s: a suspended turn = %+v, want it to say why it waits", why.Kind, run)
		}
	}
}

func (s suspendingSuite) resumes(t *testing.T) {
	w, suspender, resumer, order := s.working(t)
	if _, err := suspender.Suspend(s.ctx, order.Task, 1, order.Role,
		ports.Suspension{Kind: ports.SuspendInterrupted}); err != nil {
		t.Fatal(err)
	}
	s.env.Vacate(t, order.Task, 1, order.Role)
	resumed, err := resumer.Resume(s.ctx, order)
	if err != nil || !resumed {
		t.Fatalf("Resume = %v, %v; want the suspended turn resumed", resumed, err)
	}
	if run := s.run(t, w, order.Task, order.Role); !run.Started || run.Suspended || run.Done {
		t.Fatalf("a resumed turn = %+v, want started, not suspended or done", run)
	}
	s.env.Answer(t, order.Task, 1, order.Role, model.RunResult{Summary: summaryAdded})
	run := s.run(t, w, order.Task, order.Role)
	if !run.Done || run.Interrupted || run.Result == nil || run.Result.Summary != summaryAdded {
		t.Errorf("a resumed turn answered = %+v, want done with its result", run)
	}
}

// KeepingWorkersEnv builds Workers that keep a judging run past its turn
// (ports.Keeper), as the operator's Sandboxes do (#336).
type KeepingWorkersEnv struct {
	WorkersEnv
}

// KeepingWorkers states what internal/engine relies on of Workers that keep
// a judging run past its turn: before any review none is kept; a review
// answered and not ended is kept for its round, its run read back done with
// its verdict, so the merge reads what it left; and once ended, it is kept
// no more.
func KeepingWorkers(t *testing.T, env KeepingWorkersEnv) {
	t.Helper()
	ctx, cancel := suiteContext()
	defer cancel()
	runSubtests(t, workersSuite{ctx: ctx, env: env.WorkersEnv}, env.Cannot, []subtest[workersSuite]{
		{"keeps a review answered until it is ended", workersSuite.keeps},
	})
}

func (s workersSuite) keeps(t *testing.T) {
	w, task, issue := s.env.New(t)
	keeper, ok := w.(ports.Keeper)
	if !ok {
		t.Fatalf("Workers %T keep no judging run", w)
	}
	kept := func() string {
		t.Helper()
		return must(keeper.Kept(s.ctx, task, 1))(t)
	}
	if role := kept(); role != "" {
		t.Fatalf("before any turn, kept %q, want none", role)
	}
	review := ports.WorkOrder{Task: task, Issue: issue, Branch: model.TaskBranch(task), Round: 1,
		Role: model.RoleReviewer, Agent: "reviewer"}
	if err := w.Offer(s.ctx, review); err != nil {
		t.Fatal(err)
	}
	s.env.Answer(t, task, 1, model.RoleReviewer, model.RunResult{Verdict: model.VerdictApprove,
		Summary: "Looks right."})
	if role := kept(); role != model.RoleReviewer {
		t.Fatalf("a review answered and not ended: kept %q, want the reviewer's", role)
	}
	if role := must(keeper.Kept(s.ctx, task, 2))(t); role != "" {
		t.Errorf("round 2 keeps %q, want none: the review is round 1's", role)
	}
	if run := s.run(t, w, task, model.RoleReviewer); !run.Done || run.Result == nil ||
		run.Result.Verdict != model.VerdictApprove {
		t.Errorf("the kept review = %+v, want done with its verdict", run)
	}
	if err := w.End(s.ctx, task, 1, model.RoleReviewer, "done"); err != nil {
		t.Fatal(err)
	}
	if role := kept(); role != "" {
		t.Errorf("the review ended: kept %q, want none", role)
	}
}
