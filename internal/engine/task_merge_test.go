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

// An approved change's way into main on the Task tick (#336, slice 6): its
// branch updated with main, the judging run kept until the merge where the
// Workers keep one (ports.Keeper), a Benchmark's Task ended Approved, and the
// runtime's half of the merge (engine.Merger), as the operator's.

// keepingWorkers are fake Workers that keep a done judging run past its
// turn: Kept is the role of round's review or verification whose run is
// done and not ended.
type keepingWorkers struct{ *fake.Workers }

var _ ports.Keeper = keepingWorkers{}

func (w keepingWorkers) Kept(ctx context.Context, task string, round int) (string, error) {
	for _, role := range []string{model.RoleVerifier, model.RoleReviewer} {
		run, err := w.Run(ctx, task, round, role)
		if err != nil || run.Done {
			return role, err
		}
	}
	return "", nil
}

// mergingRuntime is a runtime that merges, on the world's store, with
// workers.
type mergingRuntime struct {
	store   *fake.Tasks
	workers ports.Workers
	merges  bool
}

func (r mergingRuntime) Store() ports.TaskStore { return r.store }
func (r mergingRuntime) Workers() ports.Workers { return r.workers }
func (r mergingRuntime) Capabilities() ports.Capabilities {
	caps := model.OperatorCapabilities()
	caps.Merges = r.merges
	return caps
}

// approved plays round 1 to its approving review on rt and returns its pull
// request and commit: the next tick merges, or updates the branch.
func (w *taskWorld) approved(rt ports.Runtime) (int, string) {
	w.t.Helper()
	w.tickOn(rt)
	commit := w.implement(1)
	pr := w.tickOn(rt).Record.PullRequest
	w.checksPass(commit)
	w.want(w.tickOn(rt), model.ActionStartReview, model.TaskReviewing)
	w.review(1, model.VerdictApprove, "Looks right.")
	return pr, commit
}

// reviewEnded reports whether the world's Workers ended round 1's review.
func (w *taskWorld) reviewEnded() bool {
	for _, e := range w.rt.Work.Ends() {
		if e.Round == 1 && e.Role == model.RoleReviewer {
			return true
		}
	}
	return false
}

// #336: where the Workers keep the judging run (ports.Keeper), the branch's
// update with main keeps the approving review's run, and the merge ends it;
// Workers that keep none have it ended at the update, as before.
func TestTaskTickKeepsTheJudgingRunUntilTheMerge336(t *testing.T) {
	for _, keeps := range []bool{true, false} {
		w := newTaskWorld(t)
		var workers ports.Workers = w.rt.Work
		if keeps {
			workers = keepingWorkers{w.rt.Work}
		}
		rt := mergingRuntime{store: w.rt.TaskStore, workers: workers, merges: true}
		pr, _ := w.approved(rt)
		w.g.FallBehind(owner, container, pr)
		res := w.tickOn(rt)
		w.want(res, model.ActionUpdateBranch, model.TaskChecking)
		if !res.Record.Progress.MergeWhenChecksPass || w.reviewEnded() == keeps {
			t.Fatalf("keeps %v: progress %+v, review ended %v; want the merge waiting for the checks, the review "+
				"kept only where the Workers keep it", keeps, res.Record.Progress, w.reviewEnded())
		}
		if comments := w.g.PullComments(owner, container, pr); len(comments) != 1 ||
			!strings.Contains(comments[0], "merging main into the branch") {
			t.Errorf("keeps %v: pull request comments %q, want the update's", keeps, comments)
		}
		w.checksPass(res.Record.Progress.Commit)
		w.want(w.tickOn(rt), model.ActionMerge, model.TaskMerged)
		if !w.reviewEnded() {
			t.Errorf("keeps %v: the review's run was never ended", keeps)
		}
		if got, _ := w.g.GetPullRequest(context.Background(), w.repo, pr); !got.Merged {
			t.Errorf("keeps %v: the pull request is not merged", keeps)
		}
	}
}

// #336: the checks of a branch updated with main are waited for from the
// update, also when main moved again while the Task already waited for
// checks, as the operator's updateBranch stamps it: the wait's limits
// (model.ChecksStartLimit) count from then. Before, the engine kept the
// first update's stamp in the same phase.
func TestTaskTickWaitsForAnUpdatedBranchsChecksFromTheUpdate336(t *testing.T) {
	w := newTaskWorld(t)
	rt := mergingRuntime{store: w.rt.TaskStore, workers: w.rt.Work, merges: true}
	pr, _ := w.approved(rt)
	w.g.FallBehind(owner, container, pr)
	w.in.Now = w.in.Now.Add(time.Hour)
	res := w.tickOn(rt)
	w.want(res, model.ActionUpdateBranch, model.TaskChecking)
	// Main moves again before the updated branch's checks are read.
	w.checksPass(res.Record.Progress.Commit)
	w.g.FallBehind(owner, container, pr)
	w.in.Now = w.in.Now.Add(time.Hour)
	res = w.tickOn(rt)
	w.want(res, model.ActionUpdateBranch, model.TaskChecking)
	if !res.Record.PhaseSince.Equal(w.in.Now) {
		t.Errorf("phase since %v, want the second update's %v", res.Record.PhaseSince, w.in.Now)
	}
}

// #336: what an update says on the pull request is the runtime's where it
// says what a turn's end says (TaskInput.Ending): the engine posts no
// comment of its own.
func TestTaskTickUpdatesTheBranchInTheRuntimesWords336(t *testing.T) {
	w := newTaskWorld(t)
	rt := mergingRuntime{store: w.rt.TaskStore, workers: keepingWorkers{w.rt.Work}, merges: true}
	pr, _ := w.approved(rt)
	var said []model.TaskAction
	w.in.Ending = func(_ context.Context, _ model.TaskFacts, step model.TaskStep) error {
		said = append(said, step.Action)
		return nil
	}
	w.g.FallBehind(owner, container, pr)
	w.want(w.tickOn(rt), model.ActionUpdateBranch, model.TaskChecking)
	if len(said) != 1 || said[0] != model.ActionUpdateBranch {
		t.Errorf("the runtime said %v, want the update", said)
	}
	if comments := w.g.PullComments(owner, container, pr); len(comments) != 0 {
		t.Errorf("pull request comments %q, want none of the engine's", comments)
	}
}

// merger is a runtime's half of a merge: hold and outcome are what
// HoldMerge and Merge return (Merge merges on the host when it returns
// none), and the calls are recorded, with whether the review's run had
// ended by the time Merged was called.
type merger struct {
	w             *taskWorld
	hold, outcome *model.TaskStep
	calls         []string
	squashed      bool
	endedAtMerged bool
}

var _ engine.Merger = (*merger)(nil)

func (m *merger) HoldMerge(context.Context, model.TaskFacts, model.TaskStep) (*model.TaskStep, error) {
	m.calls = append(m.calls, "hold")
	return m.hold, nil
}

func (m *merger) Merge(ctx context.Context, _ model.TaskFacts, pr ports.PullRequest) (*model.TaskStep, error) {
	m.calls = append(m.calls, "merge")
	if m.outcome != nil {
		return m.outcome, nil
	}
	return nil, m.w.g.MergePullRequest(ctx, m.w.repo, pr.Number, pr.HeadSHA, "Add orders", "")
}

func (m *merger) Merged(_ context.Context, _ model.TaskFacts, _ ports.PullRequest, squashed bool) error {
	m.calls = append(m.calls, "merged")
	m.squashed, m.endedAtMerged = squashed, m.w.reviewEnded()
	return nil
}

// #336: a runtime's Merger holds the merge, merges and records it, in place
// of the engine's own: a hold's wait waits and its failure fails the Task;
// a merge that waits or is refused does so; a merge records what it leaves
// before the judging run ends, and the engine closes no issue of its own.
func TestTaskTickMergesThroughTheRuntimesMerger336(t *testing.T) {
	wait := &model.TaskStep{Action: model.ActionWait, Reason: "Held", Message: "held"}
	refused := &model.TaskStep{Action: model.ActionFail, Reason: "GatedMergeRefused", Message: "refused"}
	for _, tc := range []struct {
		name          string
		hold, outcome *model.TaskStep
		action        model.TaskAction
		phase, reason string
		calls         string
	}{
		{"a hold that waits", wait, nil, model.ActionWait, model.TaskReviewing, "", "hold"},
		{"a hold that fails", refused, nil, model.ActionFail, model.TaskFailed, "GatedMergeRefused", "hold"},
		{"a merge that waits", nil, wait, model.ActionWait, model.TaskReviewing, "", "hold merge"},
		{"a merge refused", nil, refused, model.ActionFail, model.TaskFailed, "GatedMergeRefused", "hold merge"},
		{"a merge", nil, nil, model.ActionMerge, model.TaskMerged, "Merged", "hold merge merged"},
	} {
		w := newTaskWorld(t)
		rt := mergingRuntime{store: w.rt.TaskStore, workers: w.rt.Work, merges: true}
		pr, _ := w.approved(rt)
		m := &merger{w: w, hold: tc.hold, outcome: tc.outcome}
		w.in.Merger = m
		res := w.tickOn(rt)
		w.want(res, tc.action, tc.phase)
		if got := strings.Join(m.calls, " "); got != tc.calls || res.Record.Reason != tc.reason {
			t.Errorf("%s: calls %q, reason %q; want %q, %q", tc.name, got, res.Record.Reason, tc.calls, tc.reason)
		}
		merged, _ := w.g.GetPullRequest(context.Background(), w.repo, pr)
		if merged.Merged != (tc.phase == model.TaskMerged) {
			t.Errorf("%s: pull request merged %v", tc.name, merged.Merged)
		}
		if tc.phase != model.TaskMerged {
			continue
		}
		if !m.squashed || m.endedAtMerged || !w.reviewEnded() {
			t.Errorf("%s: squashed %v, review ended at Merged %v, after %v; want Merged told of the squash "+
				"before the review's run ended", tc.name, m.squashed, m.endedAtMerged, w.reviewEnded())
		}
		if issue := w.g.Issue(owner, container, w.in.Issue); issue.State == "closed" {
			t.Errorf("%s: the engine closed the issue; the Merger files and closes it", tc.name)
		}
	}
}

// #336: a person's merge is recorded by the runtime's Merger too, told that
// the runtime did not squash it.
func TestTaskTickRecordsAPersonsMergeThroughTheMerger336(t *testing.T) {
	w := newTaskWorld(t)
	rt := mergingRuntime{store: w.rt.TaskStore, workers: w.rt.Work}
	pr, commit := w.approved(rt)
	m := &merger{w: w}
	w.in.Merger = m
	if res := w.tickOn(rt); res.Step.Reason != model.ReasonPersonMerges {
		t.Fatalf("step = %+v, want a wait for a person's merge", res.Step)
	}
	if err := w.g.MergePullRequest(context.Background(), w.repo, pr, commit, "Add orders", ""); err != nil {
		t.Fatal(err)
	}
	w.want(w.tickOn(rt), model.ActionMerge, model.TaskMerged)
	if got := strings.Join(m.calls, " "); got != "hold hold merged" || m.squashed {
		t.Errorf("calls %q, squashed %v; want the person's merge recorded, not squashed", got, m.squashed)
	}
}

// #336: a Benchmark's Task never merges: approved, it ends Approved with no
// host, its pull request open for its Benchmark, and the tick after does
// nothing more.
func TestTaskTickEndsABenchmarksTaskApproved336(t *testing.T) {
	w := newTaskWorld(t)
	rt := mergingRuntime{store: w.rt.TaskStore, workers: w.rt.Work, merges: true}
	pr, _ := w.approved(rt)
	w.in.Benchmark = true
	w.in.Facts = func(_ context.Context, facts model.TaskFacts) (model.TaskFacts, error) { return facts, nil }
	res := w.tickOnNoHost(rt)
	w.want(res, model.ActionMerge, model.TaskApproved)
	if want := model.BenchmarkApproved(); res.Record.Reason != want.Reason || res.Record.Message != want.Message {
		t.Errorf("record %+v, want ended %+v", res.Record, want)
	}
	if !w.reviewEnded() {
		t.Error("the review's run was not ended")
	}
	if got, _ := w.g.GetPullRequest(context.Background(), w.repo, pr); got.Merged || got.State != "open" {
		t.Errorf("pull request = %+v, want it open for the Benchmark", got)
	}
	w.want(w.tickOnNoHost(rt), model.ActionNone, model.TaskApproved)
}

// #336: a spec whose pull request a person closed by the time it merges is
// declined (model.SpecDeclined), as when it waits for the person; before,
// the engine ended it PullRequestClosed.
func TestTaskTickDeclinesASpecWhosePullRequestClosed336(t *testing.T) {
	w := newTaskWorld(t)
	rt := mergingRuntime{store: w.rt.TaskStore, workers: w.rt.Work, merges: true}
	pr, _ := w.approved(rt)
	w.in.Spec, w.in.SpecApproval = true, model.SpecApprovalReviewer
	if err := w.g.ClosePullRequest(context.Background(), w.repo, pr); err != nil {
		t.Fatal(err)
	}
	res := w.tickOn(rt)
	if res.Record.Progress.Phase != model.TaskFailed || res.Record.Reason != model.ReasonSpecDeclined {
		t.Fatalf("step %+v, record %+v; want the spec declined", res.Step, res.Record)
	}
}
