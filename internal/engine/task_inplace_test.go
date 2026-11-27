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
	"time"

	"github.com/JairJosafath/theos/internal/engine"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

// A run kept in place (#336): suspended, resumed and paused through the
// optional parts of Workers the engine asks for by interface
// (ports.Suspender, ports.Resumer, ports.Pauser), as the operator's
// Sandboxes do. Workers without them, the lead runtime's and the fakes',
// wait there, not built.

// suspension is one Suspend the engine asked for.
type suspension struct {
	round int
	role  string
	why   ports.Suspension
}

// pauseAsk is one pause the engine brought up to date: why asked of a
// running run, or held for a suspended one.
type pauseAsk struct {
	role, why string
	held      bool
}

// inPlaceWorkers are fake Workers that suspend, resume and pause runs in
// place: fresh says whether a Suspend is fresh, resumes whether a Resume
// resumes, and the calls are recorded.
type inPlaceWorkers struct {
	*fake.Workers
	fresh, resumes bool
	suspended      []suspension
	resumed        []ports.WorkOrder
	paused         []pauseAsk
}

var (
	_ ports.Suspender = (*inPlaceWorkers)(nil)
	_ ports.Resumer   = (*inPlaceWorkers)(nil)
	_ ports.Pauser    = (*inPlaceWorkers)(nil)
)

func (w *inPlaceWorkers) Suspend(_ context.Context, _ string, round int, role string,
	why ports.Suspension) (bool, error) {
	w.suspended = append(w.suspended, suspension{round: round, role: role, why: why})
	return w.fresh, nil
}

func (w *inPlaceWorkers) Resume(_ context.Context, order ports.WorkOrder) (bool, error) {
	w.resumed = append(w.resumed, order)
	return w.resumes, nil
}

func (w *inPlaceWorkers) AskPause(_ context.Context, _ string, _ int, role, why string) error {
	w.paused = append(w.paused, pauseAsk{role: role, why: why})
	return nil
}

func (w *inPlaceWorkers) HoldSuspended(_ context.Context, _ string, _ int, role string) error {
	w.paused = append(w.paused, pauseAsk{role: role, held: true})
	return nil
}

// inPlaceRuntime is a runtime on the world's store with inPlaceWorkers.
type inPlaceRuntime struct {
	store   *fake.Tasks
	workers *inPlaceWorkers
}

func (r inPlaceRuntime) Store() ports.TaskStore           { return r.store }
func (r inPlaceRuntime) Workers() ports.Workers           { return r.workers }
func (r inPlaceRuntime) Capabilities() ports.Capabilities { return model.OperatorCapabilities() }

// inPlace starts round 1's implement turn on Workers that keep runs in
// place, and has the runtime's facts, with pause the runtime's pause, in
// place of the host's.
func (w *taskWorld) inPlace(pause *string) (inPlaceRuntime, *inPlaceWorkers) {
	w.t.Helper()
	workers := &inPlaceWorkers{Workers: w.rt.Work, fresh: true, resumes: true}
	rt := inPlaceRuntime{store: w.rt.TaskStore, workers: workers}
	w.in.Facts = func(_ context.Context, facts model.TaskFacts) (model.TaskFacts, error) {
		facts.ContainerReady, facts.Protected, facts.Base = true, true, ports.CheckSuccess
		facts.Pause = *pause
		return facts, nil
	}
	w.tickOnNoHost(rt) // round 1 offered
	return rt, workers
}

// tickOnNoHost runs one Task tick on rt with no host, failing on an error.
func (w *taskWorld) tickOnNoHost(rt ports.Runtime) engine.TaskResult {
	w.t.Helper()
	res, err := engine.TaskTick(context.Background(), rt, nil, w.in)
	if err != nil {
		w.t.Fatalf("TaskTick: %v", err)
	}
	return res
}

// setImplement says what round 1's implement run is now.
func (w *taskWorld) setImplement(run model.RunState) {
	w.rt.Work.SetRun(w.in.Task, 1, model.RoleImplementer, run)
}

// #336: an interrupted run with a checkpoint is suspended in place through
// the Workers, and its resume counted once: a suspend an earlier tick began
// (not fresh) counts none. Once its worker is gone it is resumed, on the
// turn a new run of its role would get; a resume the Workers hold waits, as
// a held offer does. No host is reached.
func TestTaskTickSuspendsAndResumesARunInPlace336(t *testing.T) {
	w := newTaskWorld(t)
	pause := ""
	rt, workers := w.inPlace(&pause)
	interrupted := model.RunState{Started: true, Done: true, Interrupted: true, Checkpoint: true,
		Failure: "the node shut down"}
	w.setImplement(interrupted)

	res := w.tickOnNoHost(rt)
	w.want(res, model.ActionSuspendRun, model.TaskImplementing)
	if len(workers.suspended) != 1 || workers.suspended[0] != (suspension{round: 1, role: model.RoleImplementer,
		why: ports.Suspension{Kind: ports.SuspendInterrupted}}) {
		t.Fatalf("suspended = %+v, want round 1's implement run, interrupted", workers.suspended)
	}
	if res.Record.Progress.Resumes != 1 || res.Record.Progress.Reruns != 0 {
		t.Errorf("progress = %+v, want one resume counted and no rerun", res.Record.Progress)
	}
	if ends := w.rt.Work.Ends(); len(ends) != 0 {
		t.Errorf("ends = %+v, want the run kept, not ended", ends)
	}

	// An earlier tick's suspend, not done yet: suspended again, counted once.
	workers.fresh = false
	res = w.tickOnNoHost(rt)
	w.want(res, model.ActionSuspendRun, model.TaskImplementing)
	if len(workers.suspended) != 2 || res.Record.Progress.Resumes != 1 {
		t.Errorf("suspended %d times, resumes %d; want suspended again, the resume counted once",
			len(workers.suspended), res.Record.Progress.Resumes)
	}

	// Its worker gone, the run resumes in place; held, it waits.
	w.setImplement(model.RunState{Started: true, Suspended: true, Checkpoint: true})
	workers.resumes = false
	res = w.tickOnNoHost(rt)
	if res.Step.Action != model.ActionWait || res.Step.Reason != engine.ReasonOfferHeld {
		t.Fatalf("step = %+v, want a wait, the resume held", res.Step)
	}
	workers.resumes = true
	res = w.tickOnNoHost(rt)
	w.want(res, model.ActionResumeRun, model.TaskImplementing)
	if len(workers.resumed) != 2 || workers.resumed[1].Role != model.RoleImplementer ||
		workers.resumed[1].Round != 1 || workers.resumed[1].Agent != w.in.Implementer {
		t.Errorf("resumed = %+v, want round 1's implement turn, on its Agent", workers.resumed)
	}
	if res.Record.Progress.Resumes != 1 {
		t.Errorf("resumes = %d, want the resume counted at the suspend alone", res.Record.Progress.Resumes)
	}
	if offers := w.rt.Work.Offers(); len(offers) != 1 {
		t.Errorf("offers = %+v, want the run resumed, not offered again", offers)
	}
}

// #336: a run that paused at the runtime's ask, and one whose agent is
// unavailable, are suspended in place with no resume counted; the agent's
// is told when it is due back.
func TestTaskTickSuspendsAPausedRunAndOneWaitingForItsAgent336(t *testing.T) {
	back := time.Now().Add(time.Hour).Truncate(time.Second)
	for _, tc := range []struct {
		name   string
		run    model.RunResult
		action model.TaskAction
		why    ports.Suspension
	}{{
		name:   "paused",
		run:    model.RunResult{Paused: true, PauseReason: "the Crew is paused", Checkpoint: true},
		action: model.ActionPauseRun, why: ports.Suspension{Kind: ports.SuspendPaused},
	}, {
		name: "waiting for its agent",
		run: model.RunResult{Error: "usage limit", Unavailable: true, RetryAt: back,
			Checkpoint: true},
		action: model.ActionSuspendForAgent, why: ports.Suspension{Kind: ports.SuspendForAgent, AgentBack: back},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			w := newTaskWorld(t)
			pause := ""
			rt, workers := w.inPlace(&pause)
			result := tc.run
			w.setImplement(model.RunState{Started: true, Done: true, Checkpoint: true, Result: &result})
			res := w.tickOnNoHost(rt)
			w.want(res, tc.action, model.TaskImplementing)
			if len(workers.suspended) != 1 || workers.suspended[0].why != tc.why {
				t.Fatalf("suspended = %+v, want %+v", workers.suspended, tc.why)
			}
			if p := res.Record.Progress; p.Resumes != 0 || p.Reruns != 0 || p.Round != 1 {
				t.Errorf("progress = %+v, want no resume, rerun or round spent", p)
			}
		})
	}
}

// #336: the engine brings a run's pause up to date before each step, where
// the Workers pause runs: a working run is asked to pause for the
// runtime's pause, and the ask taken back once it ends; a suspended run
// whose resume a pause holds is held.
func TestTaskTickBringsARunsPauseUpToDate336(t *testing.T) {
	w := newTaskWorld(t)
	pause := ""
	rt, workers := w.inPlace(&pause)
	w.setImplement(model.RunState{Started: true, Checkpoint: true})

	pause = "the Crew is paused"
	res := w.tickOnNoHost(rt)
	if res.Step.Pause != model.PausePausing {
		t.Errorf("step = %+v, want the run pausing", res.Step)
	}
	pause = ""
	w.tickOnNoHost(rt)
	want := []pauseAsk{{role: model.RoleImplementer, why: "the Crew is paused"}, {role: model.RoleImplementer}}
	if len(workers.paused) != 2 || workers.paused[0] != want[0] || workers.paused[1] != want[1] {
		t.Fatalf("paused = %+v, want the pause asked, then taken back", workers.paused)
	}

	w.setImplement(model.RunState{Started: true, Suspended: true, Paused: true, Checkpoint: true})
	pause = "the Crew is paused"
	w.tickOnNoHost(rt)
	if last := workers.paused[len(workers.paused)-1]; !last.held || last.role != model.RoleImplementer {
		t.Errorf("paused = %+v, want the suspended run held", workers.paused)
	}
}

// #336: Workers that keep no run in place, as the lead runtime's and the
// fakes' keep none, wait on a suspend or a resume, saying it is not built,
// and count nothing.
func TestTaskTickWaitsWhereTheWorkersKeepNoRunInPlace336(t *testing.T) {
	w := newTaskWorld(t)
	w.in.Facts = func(_ context.Context, facts model.TaskFacts) (model.TaskFacts, error) {
		facts.ContainerReady, facts.Protected, facts.Base = true, true, ports.CheckSuccess
		return facts, nil
	}
	w.tickOnNoHost(w.rt)
	for _, tc := range []struct {
		run    model.RunState
		action model.TaskAction
	}{
		{model.RunState{Started: true, Done: true, Interrupted: true, Checkpoint: true}, model.ActionSuspendRun},
		{model.RunState{Started: true, Suspended: true, Checkpoint: true}, model.ActionResumeRun},
	} {
		w.setImplement(tc.run)
		res := w.tickOnNoHost(w.rt)
		if res.Step.Action != model.ActionWait || res.Step.Reason != engine.ReasonNotBuilt ||
			!strings.Contains(res.Step.Message, string(tc.action)) {
			t.Errorf("step = %+v, want a wait, %s not built", res.Step, tc.action)
		}
		if p := res.Record.Progress; p.Resumes != 0 || p.Reruns != 0 {
			t.Errorf("progress = %+v, want nothing counted", p)
		}
	}
	if offers, ends := w.rt.Work.Offers(), w.rt.Work.Ends(); len(offers) != 1 || len(ends) != 0 {
		t.Errorf("offers %+v, ends %+v; want the first offer alone, nothing ended", offers, ends)
	}
}

// #336 (#1995): a paused run that can't resume ends and its turn is offered
// again with no rerun spent: a restart of its own name (Restarts). On the
// fakes, which ready nothing, it is offered in the same tick.
func TestTaskTickRerunsAPausedRunThatCantResume336(t *testing.T) {
	w := newTaskWorld(t)
	pause := ""
	rt, workers := w.inPlace(&pause)
	w.setImplement(model.RunState{Started: true, Done: true,
		Result: &model.RunResult{Paused: true, PauseReason: "the Crew is paused"}})
	res := w.tickOnNoHost(rt)
	w.want(res, model.ActionRerunPaused, model.TaskImplementing)
	if p := res.Record.Progress; p.Restarts != 1 || p.Reruns != 0 || p.Round != 1 {
		t.Errorf("progress = %+v, want a restart, no rerun spent", p)
	}
	ends := w.rt.Work.Ends()
	if len(ends) != 1 || ends[0].Role != model.RoleImplementer || ends[0].Why != "done" {
		t.Errorf("ends = %+v, want round 1's implement turn ended", ends)
	}
	if offers := w.rt.Work.Offers(); len(offers) != 2 {
		t.Errorf("offers = %+v, want the turn offered again", offers)
	}
	if len(workers.suspended) != 0 {
		t.Errorf("suspended = %+v, want nothing suspended", workers.suspended)
	}
}

// #336: an approved change whose approving review ended while CI ran again
// is checked and reviewed again: the review's turn ends, the Task goes back
// to its checks with no try spent, and the engine says why on the pull
// request, or the runtime does where it says a turn's end (Ending).
func TestTaskTickReviewsAgainAnApprovalThatRanOut336(t *testing.T) {
	for _, ending := range []bool{false, true} {
		w := newTaskWorld(t)
		w.tick()
		commit := w.implement(1)
		pr := w.tick().Record.PullRequest
		w.checksPass(commit)
		w.want(w.tick(), model.ActionStartReview, model.TaskReviewing)
		w.review(1, "", "")
		var said []model.TaskAction
		if ending {
			w.in.Ending = func(_ context.Context, _ model.TaskFacts, step model.TaskStep) error {
				said = append(said, step.Action)
				return nil
			}
		}
		w.in.Facts = func(_ context.Context, facts model.TaskFacts) (model.TaskFacts, error) {
			facts.ContainerReady, facts.Protected, facts.Base = true, true, ports.CheckSuccess
			facts.MergeWaitsForChecks = true
			return facts, nil
		}
		host := engine.TaskHost(w.g)
		if ending {
			host = nil
		}
		res, err := engine.TaskTick(context.Background(), w.rt, host, w.in)
		if err != nil {
			t.Fatal(err)
		}
		w.want(res, model.ActionReviewAgain, model.TaskChecking)
		if p := res.Record.Progress; p.ReviewAgains != 1 || p.ReviewAttempts != 0 || p.Round != 1 {
			t.Errorf("ending %v: progress = %+v, want the review again counted, no try spent", ending, p)
		}
		ends := w.rt.Work.Ends()
		if last := ends[len(ends)-1]; last.Role != model.RoleReviewer || last.Why != "done" {
			t.Errorf("ending %v: ends = %+v, want the review's turn ended", ending, ends)
		}
		comments := w.g.PullComments(owner, container, pr)
		said2 := strings.Join(comments, "\n")
		switch {
		case ending && (len(said) != 1 || said[0] != model.ActionReviewAgain || len(comments) != 0):
			t.Errorf("Ending said %v, the engine %q; want the runtime's words alone", said, comments)
		case !ending && !strings.Contains(said2, "checking and reviewing it again"):
			t.Errorf("pull request comments = %q, want why it is reviewed again", comments)
		}
	}
}
