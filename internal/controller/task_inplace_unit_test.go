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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/engine"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
)

// sandboxSuspendingEnv is contract.SuspendingWorkersEnv on the operator's
// Sandboxes, as sandboxWorkersEnv: play plays Agent Sandbox and the node,
// which deletes a suspended Sandbox's pod (Vacate).
func sandboxSuspendingEnv(r *TaskReconciler, play func() client.Client,
	newTask func(t *testing.T) *c4v1beta1.Task) contract.SuspendingWorkersEnv {
	var task *c4v1beta1.Task
	env := sandboxWorkersEnv(r, play, func(t *testing.T) *c4v1beta1.Task {
		task = newTask(t)
		return task
	})
	return contract.SuspendingWorkersEnv{WorkersEnv: env,
		Vacate: func(t *testing.T, _ string, round int, role string) {
			name := sandboxName(task, &c4v1beta1.TaskStatus{Round: int32(round)}, role)
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: task.Namespace, Name: name}}
			if err := play().Delete(context.Background(), pod, client.GracePeriodSeconds(0)); err != nil &&
				!apierrors.IsNotFound(err) {
				t.Fatal(err)
			}
		}}
}

// #336: the operator's Workers keep a run in place as the reconciler does,
// and keep the contract internal/engine relies on of Workers that do: on
// controller-runtime's fake client here, and in envtest under the
// operator's role (task_workers_test.go).
func TestSandboxWorkersSuspendingContract336(t *testing.T) {
	var c client.Client
	r := &TaskReconciler{Scheme: checkpointScheme(t)}
	contract.SuspendingWorkers(t, sandboxSuspendingEnv(r, func() client.Client { return c },
		func(t *testing.T) *c4v1beta1.Task {
			c = workersClient(t)
			r.Client, r.APIReader = c, c
			task := workersTask("apps")
			if err := c.Create(context.Background(), task); err != nil {
				t.Fatal(err)
			}
			return task
		}))
}

// sandboxOf reads the Sandbox called name.
func sandboxOf(t *testing.T, c client.Client, name string) *unstructured.Unstructured {
	t.Helper()
	sandbox := newSandbox()
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "apps", Name: name}, sandbox); err != nil {
		t.Fatal(err)
	}
	return sandbox
}

// #336: a suspend counts the run in the status once, keeps a run waiting
// for its agent until the agent is due back, and says it was fresh; a
// resume held by its readying resumes nothing; and only the Task's current
// run is suspended or resumed, as suspendRun and resumeRun act on the
// status's Sandbox alone.
func TestSandboxWorkersSuspendAndResumeTheCurrentRunAlone336(t *testing.T) {
	w, c := workersOn(t)
	ctx := context.Background()
	order := implementTurn(w)
	if err := w.Offer(ctx, order); err != nil {
		t.Fatal(err)
	}
	name := w.status.Sandbox
	review := order
	review.Role = model.RoleReviewer
	if _, err := w.Suspend(ctx, w.task.Name, 1, model.RoleReviewer,
		ports.Suspension{Kind: ports.SuspendInterrupted}); err == nil {
		t.Error("Suspend() of a turn not the current run = nil, want an error")
	}
	if _, err := w.Resume(ctx, review); err == nil {
		t.Error("Resume() of a turn not the current run = nil, want an error")
	}
	other := order
	other.Task = "orders-api-4-1"
	if _, err := w.Resume(ctx, other); err == nil {
		t.Error("Resume() of another Task's turn = nil, want an error")
	}

	back := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	why := ports.Suspension{Kind: ports.SuspendForAgent, AgentBack: back}
	fresh, err := w.Suspend(ctx, w.task.Name, 1, model.RoleImplementer, why)
	if err != nil || !fresh || w.suspended != name {
		t.Fatalf("Suspend() = %v, %v, suspended %q; want a fresh suspend of %s", fresh, err, w.suspended, name)
	}
	if w.status.AgentBackAt == nil || !w.status.AgentBackAt.Time.Equal(back) || w.status.Sandbox != name {
		t.Errorf("status = %+v, want the run kept, waiting for its agent until %s", w.status, back)
	}
	if !sandboxSuspended(sandboxOf(t, c, name)) {
		t.Errorf("sandbox %s not suspended, want it suspended at once outside a reconcile", name)
	}
	if fresh, err = w.Suspend(ctx, w.task.Name, 1, model.RoleImplementer, why); err != nil || fresh ||
		w.suspended != "" {
		t.Errorf("Suspend() again = %v, %v, suspended %q; want it not fresh", fresh, err, w.suspended)
	}

	// A resume its readying holds resumes nothing.
	w.resume = func(context.Context, ports.WorkOrder, string) (bool, error) { return false, nil }
	if resumed, err := w.Resume(ctx, order); err != nil || resumed || w.resumed != "" {
		t.Fatalf("Resume() held = %v, %v, resumed %q; want nothing resumed", resumed, err, w.resumed)
	}
	if !sandboxSuspended(sandboxOf(t, c, name)) {
		t.Errorf("sandbox %s resumed, want it still suspended while its resume is held", name)
	}
	var readied string
	w.resume = func(_ context.Context, _ ports.WorkOrder, n string) (bool, error) { readied = n; return true, nil }
	if resumed, err := w.Resume(ctx, order); err != nil || !resumed || w.resumed != name || readied != name {
		t.Fatalf("Resume() = %v, %v, resumed %q, readied %q; want %s resumed", resumed, err, w.resumed,
			readied, name)
	}
	if sandboxSuspended(sandboxOf(t, c, name)) || w.status.AgentBackAt != nil {
		t.Errorf("sandbox %s suspended, agentBackAt %v; want it running, the wait for the agent over", name,
			w.status.AgentBackAt)
	}
}

// #336: the Workers pause the Task's current run alone, as syncPause does:
// the pause annotation on its pod template asked and taken back, and the
// shutdown time of a suspended one a pause holds taken off.
func TestSandboxWorkersPauseTheCurrentRunAlone336(t *testing.T) {
	w, c := workersOn(t)
	ctx := context.Background()
	if err := w.Offer(ctx, implementTurn(w)); err != nil {
		t.Fatal(err)
	}
	name := w.status.Sandbox
	pauseAsked := func() string {
		annotations, _, _ := unstructured.NestedStringMap(sandboxOf(t, c, name).Object, fieldSpec, "podTemplate",
			"metadata", "annotations")
		return annotations[c4v1beta1.AnnotationPause]
	}
	if err := w.AskPause(ctx, w.task.Name, 1, model.RoleReviewer, "the Crew is paused"); err != nil {
		t.Fatal(err)
	}
	if got := pauseAsked(); got != "" {
		t.Errorf("pause = %q after another turn's ask, want none", got)
	}
	if err := w.AskPause(ctx, w.task.Name, 1, model.RoleImplementer, "the Crew is paused"); err != nil {
		t.Fatal(err)
	}
	if got := pauseAsked(); got != "the Crew is paused" {
		t.Errorf("pause = %q, want the Crew's asked", got)
	}
	if err := w.AskPause(ctx, w.task.Name, 1, model.RoleImplementer, ""); err != nil {
		t.Fatal(err)
	}
	if got := pauseAsked(); got != "" {
		t.Errorf("pause = %q, want the ask taken back", got)
	}
	if _, found, _ := unstructured.NestedString(sandboxOf(t, c, name).Object, fieldSpec, "shutdownTime"); !found {
		t.Fatal("the offered sandbox has no shutdown time to hold")
	}
	if err := w.HoldSuspended(ctx, w.task.Name, 1, model.RoleImplementer); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := unstructured.NestedString(sandboxOf(t, c, name).Object, fieldSpec, "shutdownTime"); found {
		t.Error("the held sandbox keeps its shutdown time, want it taken off")
	}
	if err := w.AskPause(ctx, "orders-api-4-1", 1, model.RoleImplementer, "x"); err == nil {
		t.Error("AskPause() of another Task's turn = nil, want an error")
	}
}

// #336: the Task's turn leaves the engine a run kept in place on the run
// the reconciler would keep (endsAsTheEngine), and says after the tick what
// suspendRun and resumeWith say: the Progressing reason of a fresh suspend,
// with when an agent is due back, and Running for a resume; nothing for a
// suspend that was not fresh, a held resume or another step.
func TestTaskTurnSaysWhatARunKeptInPlaceSays336(t *testing.T) {
	task := workersTask("apps")
	for action, ends := range map[model.TaskAction]bool{
		model.ActionSuspendRun: true, model.ActionPauseRun: true, model.ActionSuspendForAgent: true,
		model.ActionResumeRun: true, model.ActionRerun: false, model.ActionAgentUnavailable: false,
		model.ActionStartImplement: false,
	} {
		if got := engine.InPlace(action); got != ends {
			t.Errorf("engine.InPlace(%s) = %v, want %v", action, got, ends)
		}
	}
	back := metav1.NewTime(time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC))
	for _, tc := range []struct {
		action          model.TaskAction
		suspended       bool
		resumed         bool
		reason, message string
	}{
		{model.ActionSuspendRun, true, false, "Interrupted", "the step's message"},
		{model.ActionPauseRun, true, false, model.ReasonPaused, "the step's message"},
		{model.ActionSuspendForAgent, true, false, reasonAgentUnavailable,
			"the step's message, at 2026-11-27T10:00:00Z"},
		{model.ActionSuspendRun, false, false, "", ""},
		{model.ActionResumeRun, false, true, "Running", "the step's message in sandbox orders-api-3-1-x"},
		{model.ActionResumeRun, false, false, "", ""},
	} {
		status := &c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskImplementing, Round: 1, Sandbox: "orders-api-3-1-x",
			AgentBackAt: &back}
		step := model.TaskStep{Action: tc.action, Message: "the step's message"}
		w := &sandboxWorkers{task: task, status: status}
		if tc.suspended {
			w.suspended = status.Sandbox
		}
		if tc.resumed {
			w.resumed = status.Sandbox
		}
		turn := &taskTurn{r: &TaskReconciler{}, task: task, status: status, workers: w, step: step, inPlace: true}
		turn.keptInPlace(model.TaskStep{Action: model.ActionWait, Reason: engine.ReasonOfferHeld})
		if cond := apimeta.FindStatusCondition(status.Conditions, ConditionProgressing); cond != nil {
			t.Errorf("%s: Progressing = %+v after another step, want none", tc.action, cond)
		}
		turn.keptInPlace(step)
		cond := apimeta.FindStatusCondition(status.Conditions, ConditionProgressing)
		switch {
		case tc.reason == "" && cond != nil:
			t.Errorf("%s: Progressing = %+v, want none", tc.action, cond)
		case tc.reason != "" && (cond == nil || cond.Reason != tc.reason || cond.Message != tc.message):
			t.Errorf("%s: Progressing = %+v, want %s: %q", tc.action, cond, tc.reason, tc.message)
		}
	}
	status := &c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskImplementing, Round: 1}
	turn := &taskTurn{r: &TaskReconciler{}, task: task, status: status, workers: &sandboxWorkers{suspended: "x"},
		step: model.TaskStep{Action: model.ActionSuspendRun}}
	turn.keptInPlace(turn.step)
	if len(status.Conditions) != 0 {
		t.Errorf("conditions = %+v, want none for a suspend apply carried out", status.Conditions)
	}
}
