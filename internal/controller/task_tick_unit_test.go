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
	"testing"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// #336: the store of one Task's reconcile is its status in memory: the
// record reads from it and saves into it, a final phase ends the Task as the
// reconciler ends one, and another Task, a list or a start is refused.
func TestStatusStoreKeepsTheReconcilesStatus336(t *testing.T) {
	ctx := context.Background()
	task := workersTask("apps")
	since := metav1.NewTime(time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC))
	status := &c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskChecking, Round: 2, Commit: "abc1234", PullRequest: 7,
		PullRequestURL: "https://git.example/pulls/7", Summary: "Added the orders.", PhaseChangedAt: &since,
		Feedback: "Handle an empty order."}
	r := &TaskReconciler{Now: func() time.Time { return since.Add(time.Hour) }}
	s := &statusStore{r: r, task: task, status: status}

	rec, err := s.Progress(ctx, task.Name)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Progress.Phase != model.TaskChecking || rec.Progress.Round != 2 || rec.Progress.Commit != "abc1234" ||
		rec.PullRequest != 7 || rec.Summary != "Added the orders." || !rec.PhaseSince.Equal(since.Time) ||
		rec.Progress.Feedback != "Handle an empty order." {
		t.Fatalf("record = %+v, want the status's", rec)
	}

	rec.Progress = model.StartRun(rec.Progress, model.RoleReviewer)
	rec.PhaseSince = since.Add(time.Minute)
	if err := s.SaveProgress(ctx, task.Name, rec); err != nil {
		t.Fatal(err)
	}
	if status.Phase != c4v1beta1.TaskReviewing || status.Round != 2 || status.PullRequest != 7 {
		t.Errorf("status = %+v, want reviewing round 2 with its pull request", status)
	}
	if !status.PhaseChangedAt.Equal(&since) {
		t.Errorf("phaseChangedAt = %v, want it left to writeStatus", status.PhaseChangedAt)
	}

	rec.Progress.Phase, rec.Reason, rec.Message = model.TaskFailed, "IssueClosed", "Issue #3 was closed"
	if err := s.SaveProgress(ctx, task.Name, rec); err != nil {
		t.Fatal(err)
	}
	cond := apimeta.FindStatusCondition(status.Conditions, ConditionSucceeded)
	if status.Phase != c4v1beta1.TaskFailed || status.CompletedAt == nil || cond == nil ||
		cond.Status != metav1.ConditionFalse || cond.Reason != "IssueClosed" {
		t.Errorf("status = %+v, want the Task ended Failed, IssueClosed", status)
	}

	if _, err := s.Progress(ctx, "orders-api-4-1"); err == nil {
		t.Error("Progress() of another Task = nil, want an error")
	}
	if err := s.SaveProgress(ctx, "orders-api-4-1", rec); err == nil {
		t.Error("SaveProgress() of another Task = nil, want an error")
	}
	if _, err := s.Tasks(ctx, "orders-api"); err == nil {
		t.Error("Tasks() = nil, want it refused: the Crew's store lists Tasks")
	}
	if err := s.Start(ctx, "orders-api", model.IssueStart{Task: "orders-api-4-1", Issue: 4}); err == nil {
		t.Error("Start() = nil, want it refused: the Crew's store starts Tasks")
	}
}

// #336: the Task's Workers ready a turn before the engine offers it, and say
// which Sandbox an offer made: the reconcile's first-run comments go with a
// Sandbox an offer made, never with one an offer found made (#906).
func TestSandboxWorkersPrepareAndSayWhatTheyMade336(t *testing.T) {
	w, _ := workersOn(t)
	ctx := context.Background()
	order := implementTurn(w)
	if ready, err := w.Prepare(ctx, order); err != nil || !ready {
		t.Fatalf("Prepare() with nothing to ready = %v, %v; want ready", ready, err)
	}
	var asked string
	w.prepare = func(_ context.Context, _ ports.WorkOrder, name string) (bool, error) {
		asked = name
		return false, nil
	}
	if ready, err := w.Prepare(ctx, order); err != nil || ready {
		t.Fatalf("Prepare() held = %v, %v; want not ready", ready, err)
	}
	if want := sandboxName(w.task, &c4v1beta1.TaskStatus{Round: 1}, model.RoleImplementer); asked != want {
		t.Errorf("readied sandbox = %q, want %q, the name the offer gives it", asked, want)
	}
	other := order
	other.Task = "orders-api-4-1"
	if _, err := w.Prepare(ctx, other); err == nil {
		t.Error("Prepare() of another Task's turn = nil, want an error")
	}

	if err := w.Offer(ctx, order); err != nil {
		t.Fatal(err)
	}
	if w.created == "" || w.created != w.status.Sandbox {
		t.Fatalf("created = %q, want the sandbox the offer made, %q", w.created, w.status.Sandbox)
	}
	// The status write after the offer failed: the next pass's offer finds
	// the Sandbox, names it again and makes nothing.
	made := w.status.Sandbox
	w.status.Sandbox, w.status.AgentBackAt = "", &metav1.Time{Time: time.Now()}
	if err := w.Offer(ctx, order); err != nil {
		t.Fatal(err)
	}
	if w.created != "" || w.status.Sandbox != made || w.status.AgentBackAt != nil {
		t.Errorf("created %q, sandbox %q, agentBackAt %v; want none made, %q named, agentBackAt forgotten",
			w.created, w.status.Sandbox, w.status.AgentBackAt, made)
	}
}

// #336: the reconciler carries out every step but a turn's start, which it
// leaves to the engine to offer through the Workers.
func TestTaskTurnLeavesTheEngineTheStarts336(t *testing.T) {
	for action, engines := range map[model.TaskAction]bool{
		model.ActionStartImplement: true, model.ActionStartReview: true, model.ActionStartVerify: true,
		model.ActionWait: false, model.ActionOpenPR: false, model.ActionRerun: false, model.ActionRetry: false,
		model.ActionMerge: false, model.ActionSuspendRun: false, model.ActionResumeRun: false,
		model.ActionFail: false,
	} {
		if got := offers(action); got != engines {
			t.Errorf("offers(%s) = %v, want %v", action, got, engines)
		}
	}
}
