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
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// The operator's Workers (docs/plans/lead-runtime.md, decision 3; #336): a
// turn is a Sandbox, which theos pushes the work to. Offer makes the
// Sandbox (buildSandbox), Run reads it and its pod (runState) as the
// reconciler reads its run, and End deletes it once the Task's status that
// records the run is written (#922). A Sandbox's name is the turn's
// (model.SandboxName): its round and role, and the Task's restarts, review
// attempts and reruns, so a turn offered again after one of those counts
// moved is a Sandbox of its own.

// annotationEndedRun on a Sandbox says its run was ended for its claim
// (model.EndSilent or model.EndReleased, its value) and is kept, suspended,
// so the run reads back interrupted until its turn is offered again, as the
// Workers contract asks. A Sandbox has no claim of its own; an outside agent
// on a cluster (#260) would have one.
const annotationEndedRun = "theos.factory.io/ended-run"

// sandboxBuild is what an offered turn's Sandbox is made from: the run's
// inputs as the reconciler reads them (runSandbox's), for the turn order
// names, in the Sandbox called name.
type sandboxBuild func(ctx context.Context, order ports.WorkOrder, name string) (sandboxRun, error)

// sandboxPrepare readies the turn order names, in the Sandbox called name,
// before the engine saves its start (ports.Preparer): false holds the
// offer, the reason in the Task's status (taskTurn.prepare).
type sandboxPrepare func(ctx context.Context, order ports.WorkOrder, name string) (bool, error)

// sandboxWorkers is the operator's ports.Workers for one Task's reconcile:
// the Task's Sandboxes, named from status, the Task's status as this
// reconcile leaves it, which Offer, Run and End keep as the reconciler
// does (the current sandbox, the pod its run started on, the runs it
// counted).
type sandboxWorkers struct {
	r      *TaskReconciler
	task   *c4v1beta1.Task
	status *c4v1beta1.TaskStatus
	build  sandboxBuild
	// prepare, when set, readies a turn before it is offered; nil readies
	// every turn at once.
	prepare sandboxPrepare
	// created is the Sandbox the last Offer made, empty when it made none:
	// a first run's comments go with the Sandbox that starts it, not with
	// an offer that found it made (#906).
	created string
}

var (
	_ ports.Workers  = (*sandboxWorkers)(nil)
	_ ports.Preparer = (*sandboxWorkers)(nil)
)

// workers returns the Workers of task's reconcile, its status as the
// reconcile leaves it, its Sandboxes made with build.
func (r *TaskReconciler) workers(task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	build sandboxBuild) *sandboxWorkers {
	return &sandboxWorkers{r: r, task: task, status: status, build: build}
}

// own refuses a turn of a Task other than the one these Workers run.
func (w *sandboxWorkers) own(task string) error {
	if task != w.task.Name {
		return fmt.Errorf("the Workers of Task %s run no turn of Task %s", w.task.Name, task)
	}
	return nil
}

// name is the Sandbox of role's turn in round, as the Task's counters stand.
func (w *sandboxWorkers) name(round int, role string) string {
	s := w.status
	return model.SandboxName(w.task.Name, round, int(s.Restarts), role, int(s.ReviewAttempts+s.ReviewAgains)+1,
		int(s.Reruns))
}

// sandbox reads the Sandbox called name; nil when there is none.
func (w *sandboxWorkers) sandbox(ctx context.Context, name string) (*unstructured.Unstructured, error) {
	sandbox := newSandbox()
	err := w.r.Get(ctx, client.ObjectKey{Namespace: w.task.Namespace, Name: name}, sandbox)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return sandbox, nil
}

// Prepare readies the turn order names, as the Task's reconcile readies a
// run before its Sandbox (taskTurn.prepare); with no prepare, every turn is
// ready.
func (w *sandboxWorkers) Prepare(ctx context.Context, order ports.WorkOrder) (bool, error) {
	if err := w.own(order.Task); err != nil {
		return false, err
	}
	if w.prepare == nil {
		return true, nil
	}
	return w.prepare(ctx, order, w.name(order.Round, order.Role))
}

// Offer makes the Sandbox of the turn order names, unless it is there: a
// repeated offer makes nothing more. A Sandbox whose run was ended for its
// claim goes, and the turn starts afresh in a new one.
func (w *sandboxWorkers) Offer(ctx context.Context, order ports.WorkOrder) error {
	if err := w.own(order.Task); err != nil {
		return err
	}
	w.created = ""
	name := w.name(order.Round, order.Role)
	existing, err := w.sandbox(ctx, name)
	if err != nil {
		return err
	}
	afresh := false
	switch {
	case existing == nil:
	case existing.GetDeletionTimestamp() != nil:
		return fmt.Errorf("sandbox %s is still being deleted; its turn is offered again on the next pass", name)
	case existing.GetAnnotations()[annotationEndedRun] == "":
		// Offered already: a status whose write failed forgot it, as a
		// repeated start finds it.
		if w.status.Sandbox != name {
			w.status.Sandbox, w.status.SandboxPod, w.status.AgentBackAt = name, "", nil
		}
		return nil
	default:
		afresh = true
		if err := w.r.Delete(ctx, existing, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil &&
			!apierrors.IsNotFound(err) {
			return err
		}
	}
	run, err := w.build(ctx, order, name)
	if err != nil {
		return err
	}
	sandbox, err := buildSandbox(run, w.r.Scheme, w.r.now())
	if err != nil {
		return err
	}
	switch err := w.r.Create(ctx, sandbox); {
	case err == nil:
		w.created = name
	case !apierrors.IsAlreadyExists(err):
		return err
	case afresh:
		return fmt.Errorf("sandbox %s is still being deleted; its turn is offered again on the next pass", name)
	}
	w.status.Sandbox, w.status.SandboxPod, w.status.AgentBackAt = name, "", nil
	logf.FromContext(ctx).Info("Started agent sandbox", "sandbox", name, "role", order.Role, "round", order.Round)
	return nil
}

// Run reads the run of role's turn in round as the reconciler reads it:
// not started without its Sandbox, and interrupted when its Sandbox's run
// was ended for its claim. The current sandbox's first pod is kept in the
// status, so a pod that replaces it reads as an interrupted run.
func (w *sandboxWorkers) Run(ctx context.Context, task string, round int, role string) (model.RunState, error) {
	if err := w.own(task); err != nil {
		return model.RunState{}, err
	}
	name := w.name(round, role)
	sandbox, err := w.sandbox(ctx, name)
	if err != nil || sandbox == nil {
		return model.RunState{}, err
	}
	if why := sandbox.GetAnnotations()[annotationEndedRun]; why != "" {
		return model.RunState{Started: true, Done: true, Interrupted: true, Config: runConfigOf(sandbox),
			Failure: "the run ended with no answer: " + why}, nil
	}
	startedOn := ""
	if name == w.status.Sandbox {
		startedOn = w.status.SandboxPod
	}
	run, err := runState(ctx, w.r.Client, w.task.Namespace, name, startedOn)
	if err != nil {
		return run, err
	}
	if name == w.status.Sandbox && startedOn == "" {
		w.status.SandboxPod = run.Pod // the pod the run started on
	}
	return withoutNothing(w.task, w.status, w.r.withSuspendedFor(run)), nil
}

// End ends the run of role's turn in round. The current run is counted and
// recorded in the status, as endRun does; its Sandbox is deleted once the
// status is written, or now outside a reconcile. A run ended for its claim
// (model.EndSilent, model.EndReleased) keeps its Sandbox, marked and
// suspended at once, so it reads back interrupted until its turn is offered
// again.
func (w *sandboxWorkers) End(ctx context.Context, task string, round int, role, why string) error {
	if err := w.own(task); err != nil {
		return err
	}
	name := w.name(round, role)
	if why != model.EndSilent && why != model.EndReleased {
		if name == w.status.Sandbox {
			return w.r.endRun(ctx, w.task, w.status)
		}
		return w.r.afterStatusWrite(ctx, w.task, endedRun{sandbox: name, round: int32(round)})
	}
	var leases []c4v1beta1.TaskEnvironmentLease
	if name == w.status.Sandbox {
		ended, err := w.r.recordRun(ctx, w.task, w.status)
		if err != nil {
			return err
		}
		leases = ended.leases
	}
	if err := w.endSandbox(ctx, name, why); err != nil {
		return err
	}
	w.r.returnLeases(ctx, w.task, leases)
	return nil
}

// endSandbox marks the Sandbox called name ended for why and suspends it:
// Agent Sandbox deletes its pod and starts no other. One that is gone, or
// marked already, stays as it is.
func (w *sandboxWorkers) endSandbox(ctx context.Context, name, why string) error {
	sandbox, err := w.sandbox(ctx, name)
	if err != nil || sandbox == nil || sandbox.GetAnnotations()[annotationEndedRun] != "" {
		return err
	}
	orig := sandbox.DeepCopy()
	annotations := sandbox.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[annotationEndedRun] = why
	sandbox.SetAnnotations(annotations)
	if err := unstructured.SetNestedField(sandbox.Object, operatingModeSuspended, fieldSpec, "operatingMode"); err != nil {
		return err
	}
	if err := w.r.Patch(ctx, sandbox, client.MergeFrom(orig)); err != nil {
		return err
	}
	logf.FromContext(ctx).Info("Ended a run for its claim", "sandbox", name, "why", why)
	return nil
}
