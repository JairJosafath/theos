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

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// summaryAdded is the implementer's summary the suites answer with.
const summaryAdded = "Added the orders."

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
		Role: model.RoleImplementer, Agent: "implementer"}
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
		Role: model.RoleImplementer, Agent: "implementer"}
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
