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

package controller

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports/contract"
)

// A Merged Task whose merge commit can't be read tries each minute at first,
// then each hour: a pull request that is gone mustn't cost a request a minute.
func TestMergeCommitRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) *metav1.Time { m := metav1.NewTime(now.Add(-ago)); return &m }
	for _, tc := range []struct {
		name      string
		completed *metav1.Time
		want      time.Duration
	}{
		{"no completion time", nil, time.Minute},
		{"just merged", at(time.Minute), time.Minute},
		{"merged within the hour", at(59 * time.Minute), time.Minute},
		{"merged long ago", at(48 * time.Hour), time.Hour},
	} {
		if got := mergeCommitRetryAfter(tc.completed, now); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// #336: the operator's Workers keep the judging run past its turn as the
// reconciler keeps the review's Sandbox through the branch's update, and keep
// the contract internal/engine relies on of Workers that do.
func TestSandboxWorkersKeepingContract336(t *testing.T) {
	var c client.Client
	r := &TaskReconciler{Scheme: checkpointScheme(t)}
	contract.KeepingWorkers(t, contract.KeepingWorkersEnv{WorkersEnv: sandboxWorkersEnv(r,
		func() client.Client { return c }, func(t *testing.T) *c4v1beta1.Task {
			c = workersClient(t)
			r.Client, r.APIReader = c, c
			task := workersTask("apps")
			if err := c.Create(context.Background(), task); err != nil {
				t.Fatal(err)
			}
			return task
		})})
}

// reviewed offers round 1's review on the Workers' Task, in Reviewing, and
// has its pod answer with verdict.
func reviewed(t *testing.T, w *sandboxWorkers, c client.Client, verdict string) {
	t.Helper()
	w.status.Phase, w.status.Round = c4v1beta1.TaskReviewing, 1
	review := implementTurn(w)
	review.Role, review.Agent = model.RoleReviewer, "reviewer"
	if err := w.Offer(context.Background(), review); err != nil {
		t.Fatal(err)
	}
	message, err := json.Marshal(agentrunner.Result{Role: model.RoleReviewer, Verdict: verdict,
		Summary: "Looks right."})
	if err != nil {
		t.Fatal(err)
	}
	playPod(t, c, "apps", w.status.Sandbox, corev1.PodStatus{Phase: corev1.PodSucceeded,
		ContainerStatuses: []corev1.ContainerStatus{{Name: agentContainer, Image: "agent",
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Completed",
				Message: string(message)}}}}})
}

// #336: an approved change's merge and its branch's update are the engine's
// where it acts on the run the reconciler would (endsAsTheEngine): in review,
// and in Checking with the review's Sandbox kept for the merge; a round no
// agent judged drops an earlier verdict first, as apply does.
func TestTaskTurnLeavesTheEngineTheMerge336(t *testing.T) {
	for action, engines := range map[model.TaskAction]bool{
		model.ActionMerge: true, model.ActionUpdateBranch: true, model.ActionOpenPR: false,
		model.ActionPropose: false, model.ActionResume: false, model.ActionDoneByStandardChange: false,
		model.ActionNothingToDo: false, model.ActionAwaitApproval: false, model.ActionWait: false,
	} {
		if got := mergeStep(action); got != engines {
			t.Errorf("mergeStep(%s) = %v, want %v", action, got, engines)
		}
	}
	ctx := context.Background()
	task := workersTask("apps")
	reviewing := &c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskReviewing, Round: 1}
	review := sandboxName(task, reviewing, model.RoleReviewer)
	for _, tc := range []struct {
		name    string
		phase   c4v1beta1.TaskPhase
		sandbox string
	}{
		{"in review", c4v1beta1.TaskReviewing, review},
		{"checking, the review kept for the merge", c4v1beta1.TaskChecking, review},
		{"checking, no run kept", c4v1beta1.TaskChecking, ""},
	} {
		for _, step := range []model.TaskStep{{Action: model.ActionMerge}, {Action: model.ActionUpdateBranch},
			{Action: model.ActionMerge, Unreviewed: true}} {
			status := reviewing.DeepCopy()
			status.Phase, status.Sandbox = tc.phase, tc.sandbox
			status.Review = &c4v1beta1.TaskReview{Verdict: model.VerdictApprove}
			turn := &taskTurn{r: &TaskReconciler{}, task: task, status: status}
			carried, err := turn.carry(ctx, model.TaskFacts{}, step)
			if err != nil || carried || !turn.merges {
				t.Errorf("%s, %s: carry() = %v, %v, merges %v; want it left to the engine", tc.name, step.Action,
					carried, err, turn.merges)
			}
			if (status.Review == nil) != step.Unreviewed {
				t.Errorf("%s, %s (unreviewed %v): review %+v", tc.name, step.Action, step.Unreviewed, status.Review)
			}
		}
	}
}

// #336: the reconciler's half of a merge on the tick keeps the review's
// verdict in the status before its run ends, as merge and updateBranch do:
// a Benchmark's Task's hold, and an update's words (ending).
func TestTaskTurnKeepsTheVerdictForTheMerge336(t *testing.T) {
	ctx := context.Background()
	for _, what := range []string{"a Benchmark's hold", "an update"} {
		w, c := workersOn(t)
		reviewed(t, w, c, model.VerdictApprove)
		turn := &taskTurn{r: w.r, task: w.task, status: w.status, workers: w}
		var err error
		if what == "an update" {
			err = turn.ending(ctx, model.TaskFacts{}, model.TaskStep{Action: model.ActionUpdateBranch,
				Message: "Approved, but main moved"})
		} else {
			w.task.Spec.Benchmark = &c4v1beta1.TaskBenchmarkSpec{}
			var hold *model.TaskStep
			hold, err = turn.HoldMerge(ctx, model.TaskFacts{}, model.TaskStep{Action: model.ActionMerge})
			if hold != nil {
				t.Errorf("%s: held %+v, want none: the engine ends it Approved", what, hold)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		if r := w.status.Review; r == nil || r.Verdict != model.VerdictApprove || r.Summary != "Looks right." {
			t.Errorf("%s: review %+v, want the review run's verdict", what, r)
		}
		if role, err := w.Kept(ctx, w.task.Name, 1); err != nil || role != model.RoleReviewer {
			t.Errorf("%s: kept %q, %v; want the review's run kept until the engine ends it", what, role, err)
		}
	}
}

// #336: what a merge or a branch's update the engine carried out says is the
// reconciler's, after the tick, as apply's merge and updateBranch say it.
func TestTaskTurnSaysWhatAMergeSays336(t *testing.T) {
	ctx := context.Background()
	task := workersTask("apps")
	task.Spec.Issue = 0 // no issue to hand back
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	r := &TaskReconciler{Now: func() time.Time { return now }}
	earlier := metav1.NewTime(now.Add(-time.Hour))
	for _, tc := range []struct {
		name         string
		phase        c4v1beta1.TaskPhase
		done         model.TaskStep
		reason, says string
		requeue      bool
	}{
		{"a branch updated", c4v1beta1.TaskChecking, model.TaskStep{Action: model.ActionUpdateBranch,
			Message: "main merged in"}, "UpdatingBranch", "main merged in", true},
		{"a branch the host has not seen updated", c4v1beta1.TaskReviewing,
			model.TaskStep{Action: model.ActionWait, Message: "main merged in"}, "", "", true},
		{"a gated merge waiting for CI", c4v1beta1.TaskReviewing, model.TaskStep{Action: model.ActionWait,
			Reason: reasonWaitingForChecks, Message: "CI runs again"}, reasonWaitingForChecks, "CI runs again", true},
		{"a conflict with main", c4v1beta1.TaskImplementing, model.TaskStep{Action: model.ActionResolveConflicts,
			Message: "conflicts"}, "ResolvingConflicts", "conflicts", false},
		{"a branch moved after the review", c4v1beta1.TaskChecking, model.TaskStep{Action: model.ActionMerge},
			"BranchMoved", "The branch moved after the review; checking abc1234", true},
		{"a merge", c4v1beta1.TaskMerged, model.TaskStep{Action: model.ActionMerge}, "", "", false},
	} {
		status := &c4v1beta1.TaskStatus{Phase: tc.phase, Round: 1, Commit: "abc1234", PhaseChangedAt: &earlier}
		turn := &taskTurn{r: r, task: task, status: status, merges: true,
			step: model.TaskStep{Action: model.ActionMerge}}
		turn.mergeCarried(ctx, tc.done)
		cond := apimeta.FindStatusCondition(status.Conditions, ConditionProgressing)
		switch {
		case tc.reason == "" && cond != nil:
			t.Errorf("%s: Progressing %+v, want none", tc.name, cond)
		case tc.reason != "" && (cond == nil || cond.Reason != tc.reason || cond.Message != tc.says):
			t.Errorf("%s: Progressing %+v, want %s, %q", tc.name, cond, tc.reason, tc.says)
		}
		if requeued := turn.result.RequeueAfter > 0; requeued != tc.requeue {
			t.Errorf("%s: requeue after %v, want one %v", tc.name, turn.result.RequeueAfter, tc.requeue)
		}
		if stamped := status.PhaseChangedAt.Equal(&metav1.Time{Time: now}); stamped !=
			(tc.done.Action == model.ActionUpdateBranch) {
			t.Errorf("%s: phase changed at %v; want it stamped for an update alone", tc.name, status.PhaseChangedAt)
		}
	}

	// A failure the engine carried out goes back to a person, as fail does;
	// a step apply carried out says nothing more here.
	status := &c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskFailed, Round: 1}
	failed := model.TaskStep{Action: model.ActionFail, Reason: "GatedMergeRefused", Message: "refused"}
	turn := &taskTurn{r: r, task: task, status: status, merges: true, failed: &failed}
	turn.mergeCarried(ctx, failed)
	if len(status.Conditions) != 0 || turn.result.RequeueAfter != 0 {
		t.Errorf("a failure handed back: conditions %+v, result %+v; want what the end wrote alone",
			status.Conditions, turn.result)
	}
	status = &c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskChecking, Round: 1}
	turn = &taskTurn{r: r, task: task, status: status}
	turn.mergeCarried(ctx, model.TaskStep{Action: model.ActionUpdateBranch})
	if len(status.Conditions) != 0 || status.PhaseChangedAt != nil {
		t.Errorf("a step apply carried out: conditions %+v, phase changed at %v; want nothing more",
			status.Conditions, status.PhaseChangedAt)
	}
}

// #336: a Benchmark's Task the engine ends Approved is finished as merge
// finished it: Approved, succeeded, its completion stamped.
func TestStatusStoreEndsABenchmarksTaskApproved336(t *testing.T) {
	task := workersTask("apps")
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	status := &c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskReviewing, Round: 1}
	store := &statusStore{r: &TaskReconciler{Now: func() time.Time { return now }}, task: task, status: status}
	rec := recordOf(status)
	end := model.BenchmarkApproved()
	rec.Progress.Phase, rec.Reason, rec.Message = end.Phase, end.Reason, end.Message
	if err := store.SaveProgress(context.Background(), task.Name, rec); err != nil {
		t.Fatal(err)
	}
	cond := apimeta.FindStatusCondition(status.Conditions, ConditionSucceeded)
	if status.Phase != c4v1beta1.TaskApproved || status.CompletedAt == nil || cond == nil ||
		cond.Status != metav1.ConditionTrue || cond.Reason != model.TaskApproved || cond.Message != end.Message {
		t.Errorf("status phase %s, completed %v, Succeeded %+v; want Approved, succeeded", status.Phase,
			status.CompletedAt, cond)
	}
}
