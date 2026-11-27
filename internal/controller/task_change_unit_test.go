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

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// #336: the reconciler leaves the engine a Task's own ends, the Standard
// change and a pull request put up for a person, where the engine acts on
// the run the reconciler would (endsAsTheEngine): in Implementing, Reviewing
// and Verifying on the status's Sandbox, and Blocked or AwaitingApproval
// with none named.
func TestTaskTurnLeavesTheEngineTheTasksOwnEnds336(t *testing.T) {
	for action, engines := range map[model.TaskAction]bool{
		model.ActionPropose: true, model.ActionResume: true, model.ActionDoneByStandardChange: true,
		model.ActionNothingToDo: true, model.ActionAwaitApproval: true, model.ActionSpecDeclined: true,
		model.ActionMerge: false, model.ActionFail: false, model.ActionWait: false, model.ActionRetry: false,
		model.ActionStartImplement: false,
	} {
		if got := changeStep(action); got != engines {
			t.Errorf("changeStep(%s) = %v, want %v", action, got, engines)
		}
	}
	ctx := context.Background()
	task := workersTask("apps")
	for _, tc := range []struct {
		phase  c4v1beta1.TaskPhase
		role   string
		action model.TaskAction
	}{
		{c4v1beta1.TaskImplementing, model.RoleImplementer, model.ActionPropose},
		{c4v1beta1.TaskImplementing, model.RoleImplementer, model.ActionDoneByStandardChange},
		{c4v1beta1.TaskImplementing, model.RoleImplementer, model.ActionNothingToDo},
		{c4v1beta1.TaskReviewing, model.RoleReviewer, model.ActionAwaitApproval},
		{c4v1beta1.TaskVerifying, model.RoleVerifier, model.ActionAwaitApproval},
		{c4v1beta1.TaskBlocked, "", model.ActionResume},
		{c4v1beta1.TaskAwaitingApproval, "", model.ActionSpecDeclined},
	} {
		status := &c4v1beta1.TaskStatus{Phase: tc.phase, Round: 1}
		if tc.role != "" {
			status.Sandbox = sandboxName(task, status, tc.role)
		}
		turn := &taskTurn{r: &TaskReconciler{}, task: task, status: status}
		carried, err := turn.carry(ctx, model.TaskFacts{}, model.TaskStep{Action: tc.action})
		if err != nil || carried || !turn.changes {
			t.Errorf("%s in %s: carry() = %v, %v, changes %v; want it left to the engine", tc.action, tc.phase,
				carried, err, turn.changes)
		}
	}
}

// #336: the operator's Proposer refuses a proposal as propose does
// (model.ProposeRefusal), the failure the engine carries out, and errs on a
// run that reported none.
func TestTaskTurnProposesAsTheReconciler336(t *testing.T) {
	ctx := context.Background()
	task := workersTask("apps") // no Crew: a Standard change needs one
	status := &c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskImplementing, Round: 1}
	turn := &taskTurn{r: &TaskReconciler{}, task: task, status: status}
	run := model.RunState{Started: true, Done: true, Result: &model.RunResult{Commit: "abc1234",
		Proposal: &model.Proposal{Branch: "proposal/orders", Paths: []string{"Makefile"}}}}
	out, err := turn.Propose(ctx, model.TaskFacts{Run: run}, model.TaskStep{Action: model.ActionPropose})
	want := model.ProposeRefusal(int(task.Spec.Issue), false, []string{"Makefile"})
	if err != nil || out.Instead == nil || out.Instead.Action != model.ActionFail ||
		out.Instead.Reason != model.ReasonFailed || out.Instead.Message != want || out.End != nil {
		t.Errorf("Propose() = %+v, %v; want the refusal %q as a failure", out, err, want)
	}
	if status.StandardChange != nil {
		t.Errorf("standard change %+v, want none kept for a refused proposal", status.StandardChange)
	}
	if _, err := turn.Propose(ctx, model.TaskFacts{Run: model.RunState{Started: true, Done: true,
		Result: &model.RunResult{}}}, model.TaskStep{}); err == nil {
		t.Error("Propose() of a run with no proposal: want an error")
	}
}

// #336: what a Task's own end or the Standard change the engine carried out
// says in the status is the reconciler's, after the tick, as apply's say it:
// a Task Blocked on its change waits for it, a resumed Task goes on, and a
// pull request put up for a person waits for them, an earlier head's
// approval dropped, and looks again; another step says nothing here.
func TestTaskTurnSaysWhatATasksOwnEndSays336(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	r := &TaskReconciler{Now: func() time.Time { return now }}
	reason := func(status *c4v1beta1.TaskStatus) string {
		if cond := apimeta.FindStatusCondition(status.Conditions, ConditionProgressing); cond != nil {
			return cond.Reason
		}
		return ""
	}
	change := &c4v1beta1.TaskStandardChange{Issue: 9, Paths: []string{"Makefile"}}
	for _, tc := range []struct {
		name    string
		phase   c4v1beta1.TaskPhase
		decided model.TaskAction
		done    model.TaskAction
		reason  string
		requeue bool
	}{
		{"blocked on its change", c4v1beta1.TaskBlocked, model.ActionPropose, model.ActionPropose,
			"WaitingForStandardChange", false},
		{"resumed", c4v1beta1.TaskImplementing, model.ActionResume, model.ActionResume, "Resumed", false},
		{"up for a person", c4v1beta1.TaskAwaitingApproval, model.ActionAwaitApproval, model.ActionAwaitApproval,
			model.ReasonAwaitingApproval, true},
		{"another step", c4v1beta1.TaskImplementing, model.ActionPropose, model.ActionWait, "", false},
	} {
		task := workersTask("apps")
		status := &c4v1beta1.TaskStatus{Phase: tc.phase, Round: 1, StandardChange: change.DeepCopy(),
			Approval: &c4v1beta1.TaskApproval{Require: c4v1beta1.ApprovalPerson, ApprovedBy: "ana"}}
		turn := &taskTurn{r: r, task: task, status: status, changes: true,
			step: model.TaskStep{Action: tc.decided}}
		turn.changeCarried(ctx, model.TaskStep{Action: tc.done, Message: "said"})
		if got := reason(status); got != tc.reason {
			t.Errorf("%s: reason %q, want %q", tc.name, got, tc.reason)
		}
		if requeue := turn.result.RequeueAfter > 0; requeue != tc.requeue {
			t.Errorf("%s: requeue after %v, want a requeue %v", tc.name, turn.result.RequeueAfter, tc.requeue)
		}
		if tc.done == model.ActionAwaitApproval && status.Approval.ApprovedBy != "" {
			t.Errorf("%s: approved by %q, want an earlier head's approval dropped", tc.name, status.Approval.ApprovedBy)
		}
	}
}
