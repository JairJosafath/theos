/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
	"github.com/JairJosafath/c4factory/internal/model"
)

// A run continues from a checkpoint (docs/design.md, "A run continues from a
// checkpoint"). Each sandbox has a small volume of its own, from a
// volumeClaimTemplates entry, that outlives its pod. Told to stop, c4agent
// saves the run there. When the infrastructure interrupts a run, the operator
// suspends the sandbox (spec.operatingMode Suspended): Agent Sandbox deletes
// the pod and keeps the volume. Once the pod is gone, the operator writes the
// current Agent's pod template, with the run's remaining time, and sets the
// sandbox Running again: the new pod's c4agent goes on from the checkpoint.

// The checkpoint volume: its claim template's name, where the agent
// container mounts it, and its size.
const (
	checkpointVolume = "checkpoint"
	checkpointMount  = agentrunner.CheckpointDir
	checkpointSize   = "1Gi"
)

// checkpointGrace is the sandbox pod's terminationGracePeriodSeconds: the
// time c4agent, told to stop, has to end its session and save the run.
const checkpointGrace int64 = 60

// Agent Sandbox's spec.operatingMode values.
const (
	operatingModeRunning   = "Running"
	operatingModeSuspended = "Suspended"
)

// fieldSpec is a Sandbox's spec, in its unstructured form.
const fieldSpec = "spec"

// annotationRanSeconds on a Sandbox is how long its run ran, in seconds, on
// the pods before the current one: a resumed run gets what is left of its
// timeout.
const annotationRanSeconds = "c4.factory.io/ran-seconds"

// annotationSuspendedAt on a suspended Sandbox is when the operator suspended
// it, in RFC 3339: its run waits model.MaxSuspendWait at most for the old pod
// to go.
const annotationSuspendedAt = "c4.factory.io/suspended-at"

// NoCheckpointClass, as the checkpoint StorageClass (the operator's
// --sandbox-checkpoint-storage-class), gives sandboxes no checkpoint volume,
// for a cluster with no class to provision one: an interrupted run is then
// rerun, as one in a sandbox made before checkpoints is.
const NoCheckpointClass = "none"

// checkpointClaims is the sandbox's volumeClaimTemplates: the checkpoint
// volume, of the StorageClass class, the cluster's default when empty.
func checkpointClaims(class string) []any {
	spec := map[string]any{
		"accessModes": []any{string(corev1.ReadWriteOnce)},
		"resources":   map[string]any{"requests": map[string]any{"storage": checkpointSize}},
	}
	if class != "" {
		spec["storageClassName"] = class
	}
	return []any{map[string]any{"metadata": map[string]any{"name": checkpointVolume}, fieldSpec: spec}}
}

// hasCheckpoint reports whether the sandbox has the checkpoint volume; one
// made before checkpoints has none, and the field is immutable.
func hasCheckpoint(sandbox *unstructured.Unstructured) bool {
	claims, _, _ := unstructured.NestedSlice(sandbox.Object, fieldSpec, "volumeClaimTemplates")
	for _, c := range claims {
		claim, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if name, _, _ := unstructured.NestedString(claim, "metadata", "name"); name == checkpointVolume {
			return true
		}
	}
	return false
}

// sandboxSuspended reports whether the sandbox's operatingMode is Suspended.
func sandboxSuspended(sandbox *unstructured.Unstructured) bool {
	mode, _, _ := unstructured.NestedString(sandbox.Object, fieldSpec, "operatingMode")
	return mode == operatingModeSuspended
}

// ranBefore is how long the sandbox's run ran on its earlier pods.
func ranBefore(sandbox *unstructured.Unstructured) time.Duration {
	seconds, err := strconv.ParseInt(sandbox.GetAnnotations()[annotationRanSeconds], 10, 64)
	if err != nil || seconds < 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// applyInterrupted ends an interrupted run and runs its step again, or
// suspends its sandbox to resume it in place, or resumes a suspended one.
func (r *TaskReconciler) applyInterrupted(ctx context.Context, task *c4v1beta1.Task,
	status *c4v1beta1.TaskStatus, container *c4v1beta1.Container, agents taskAgents,
	step model.TaskStep) (ctrl.Result, error) {
	switch step.Action {
	case model.ActionSuspendRun:
		return ctrl.Result{}, r.suspendRun(ctx, task, status, step) // the status write triggers the next step
	case model.ActionResumeRun:
		return r.resumeRun(ctx, task, status, container, agents, step)
	}
	if err := r.endRun(ctx, task, status); err != nil {
		return ctrl.Result{}, err
	}
	setProgress(status, model.Rerun(progressOf(status)))
	r.progress(task, status, metav1.ConditionTrue, "Interrupted", step.Message)
	logf.FromContext(ctx).Info("Rerunning an interrupted run", "round", status.Round, "reruns", status.Reruns)
	return ctrl.Result{}, nil // the status write triggers the next step
}

// resumable reports whether an interrupted run in the sandbox can resume in
// place: it has the checkpoint volume, and has not expired, which leaves
// Agent Sandbox done with it.
func resumable(sandbox *unstructured.Unstructured, expired bool) bool {
	return hasCheckpoint(sandbox) && !expired
}

// suspendedState is the state of a suspended sandbox's run: it waits for
// the old pod to go.
func suspendedState(ctx context.Context, c client.Client, sandbox *unstructured.Unstructured,
	state model.RunState) (model.RunState, error) {
	state.Suspended = true
	state.SuspendedAt = parseTime(sandbox.GetAnnotations()[annotationSuspendedAt])
	var pod corev1.Pod
	err := c.Get(ctx, client.ObjectKey{Namespace: sandbox.GetNamespace(), Name: sandbox.GetName()}, &pod)
	switch {
	case apierrors.IsNotFound(err):
	case err != nil:
		return state, err
	case metav1.IsControlledBy(&pod, sandbox):
		state.Pod = string(pod.UID)
	}
	return state, nil
}

// withSuspendedFor adds to a suspended run how long ago its sandbox was
// suspended.
func (r *TaskReconciler) withSuspendedFor(run model.RunState) model.RunState {
	if run.Suspended && !run.SuspendedAt.IsZero() {
		run.SuspendedFor = r.now().Sub(run.SuspendedAt)
	}
	return run
}

// suspendRun counts what the interrupted run cost and the resume, and
// suspends its sandbox once the status that counts them is written: a
// failed write leaves the run to be found and counted again. A run counted
// already, whose suspend failed after the write, is only suspended again.
func (r *TaskReconciler) suspendRun(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	step model.TaskStep) error {
	run, err := runState(ctx, r.Client, task.Namespace, status.Sandbox, status.SandboxPod)
	if err != nil {
		return err
	}
	if !runCounted(status, run) {
		r.countRun(status, run)
		status.CountedPod = runPod(status, run)
		setProgress(status, model.SuspendRun(progressOf(status)))
		r.progress(task, status, metav1.ConditionTrue, "Interrupted", step.Message)
	}
	logf.FromContext(ctx).Info("Suspending an interrupted run to resume it in place", "sandbox", status.Sandbox,
		"round", status.Round, "resumes", status.Resumes)
	return r.afterStatusWrite(ctx, task, endedRun{sandbox: status.Sandbox, round: status.Round,
		result: run.Result, suspend: true})
}

// runPod is the pod the sandbox's current run started on: the one the
// operator saw, or the one there now.
func runPod(status *c4v1beta1.TaskStatus, run model.RunState) string {
	if status.SandboxPod != "" {
		return status.SandboxPod
	}
	return run.Pod
}

// runCounted reports whether the sandbox's current run is counted already:
// its sandbox is suspended, which suspendRun counted, or the pod it started
// on is the one suspendRun counted before its suspend failed.
func runCounted(status *c4v1beta1.TaskStatus, run model.RunState) bool {
	if run.Suspended {
		return true
	}
	pod := runPod(status, run)
	return pod != "" && pod == status.CountedPod
}

// suspendSandbox adds the time the run ran on its pod to the sandbox and
// suspends it: Agent Sandbox deletes the pod and keeps the volume. A
// sandbox already suspended stays as it is.
func (r *TaskReconciler) suspendSandbox(ctx context.Context, ns, name string) error {
	sandbox := newSandbox()
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, sandbox); err != nil {
		return client.IgnoreNotFound(err)
	}
	if sandboxSuspended(sandbox) {
		return nil
	}
	ran, err := r.podRan(ctx, sandbox)
	if err != nil {
		return err
	}
	orig := sandbox.DeepCopy()
	annotations := sandbox.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[annotationRanSeconds] = strconv.FormatInt(int64((ranBefore(sandbox) + ran).Seconds()), 10)
	annotations[annotationSuspendedAt] = r.now().UTC().Format(time.RFC3339)
	sandbox.SetAnnotations(annotations)
	if err := unstructured.SetNestedField(sandbox.Object, operatingModeSuspended, fieldSpec, "operatingMode"); err != nil {
		return err
	}
	// A merge patch, not an update: the resume is counted already, and a
	// conflict must not count it again.
	return r.Patch(ctx, sandbox, client.MergeFrom(orig))
}

// podRan is how long the run ran on the sandbox's pod: from its start to
// when the agent container ended, or to now while it runs. A container lost
// with its node ended when the node came back, so its end, and a pod that
// is gone, are unknown, and count nothing: MaxResumes bounds such runs.
func (r *TaskReconciler) podRan(ctx context.Context, sandbox *unstructured.Unstructured) (time.Duration, error) {
	var pod corev1.Pod
	err := r.Get(ctx, client.ObjectKey{Namespace: sandbox.GetNamespace(), Name: sandbox.GetName()}, &pod)
	if apierrors.IsNotFound(err) || err == nil && !metav1.IsControlledBy(&pod, sandbox) {
		return 0, nil
	}
	if err != nil || pod.Status.StartTime == nil {
		return 0, err
	}
	ended := r.now()
	for _, cs := range pod.Status.ContainerStatuses {
		if term := cs.State.Terminated; cs.Name == agentContainer && term != nil {
			if containerLost(term) {
				return 0, nil
			}
			ended = term.FinishedAt.Time
		}
	}
	return model.PodRunTime(pod.Status.StartTime.Time, ended), nil
}

// resumeRun resumes a Container's Task's suspended run with what a new run
// of its role would get now: the current Agent, runner and fence.
func (r *TaskReconciler) resumeRun(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents, step model.TaskStep) (ctrl.Result, error) {
	role := runRole(status.Phase)
	in, result, err := r.runInputs(ctx, task, status, container, agents, role)
	if in == nil {
		return result, err
	}
	fresh, err := r.runSandbox(ctx, task, status, container, agents, role, status.Sandbox, in)
	if err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, r.resumeWith(ctx, task, status, agents.forRole(role), fresh, step)
}

// resumeWith resumes the Task's suspended sandbox with fresh's pod template,
// given the run's remaining time. The status, which forgets the old pod, is
// written first: the new pod must not count as a replacement of it.
func (r *TaskReconciler) resumeWith(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	agent *c4v1beta1.Agent, fresh *unstructured.Unstructured, step model.TaskStep) error {
	sandbox := newSandbox()
	if err := r.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: status.Sandbox}, sandbox); err != nil {
		return err
	}
	template, _, err := unstructured.NestedMap(fresh.Object, fieldSpec, "podTemplate")
	if err != nil {
		return err
	}
	deadline := model.ResumeDeadline(runTimeout(agent), ranBefore(sandbox))
	if err := unstructured.SetNestedField(template, int64(deadline.Seconds()), fieldSpec, "activeDeadlineSeconds"); err != nil {
		return err
	}
	status.SandboxPod, status.AgentBackAt = "", nil
	r.progress(task, status, metav1.ConditionTrue, "Running", fmt.Sprintf("%s in sandbox %s", step.Message, status.Sandbox))
	logf.FromContext(ctx).Info("Resuming an interrupted run in place", "sandbox", status.Sandbox,
		"round", status.Round, "resumes", status.Resumes, "deadline", deadline)
	return r.afterStatusWrite(ctx, task, endedRun{sandbox: status.Sandbox, round: status.Round,
		resume: &sandboxResume{podTemplate: template, deadline: deadline}})
}

// sandboxResume is the pod template and time a suspended sandbox resumes with.
type sandboxResume struct {
	podTemplate map[string]any
	deadline    time.Duration
}

// resumeSandbox writes the resume's pod template and a shutdown time past
// its deadline into the suspended sandbox, and sets it Running: Agent
// Sandbox starts a new pod on the kept volume. A sandbox that runs already
// stays as it is.
func (r *TaskReconciler) resumeSandbox(ctx context.Context, ns, name string, resume *sandboxResume) error {
	sandbox := newSandbox()
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, sandbox); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !sandboxSuspended(sandbox) {
		return nil
	}
	shutdown := r.now().Add(resume.deadline + sandboxGrace).UTC().Format(time.RFC3339)
	annotations := sandbox.GetAnnotations()
	delete(annotations, annotationSuspendedAt)
	sandbox.SetAnnotations(annotations)
	for _, field := range []struct {
		value any
		path  []string
	}{
		{resume.podTemplate, []string{fieldSpec, "podTemplate"}},
		{shutdown, []string{fieldSpec, "shutdownTime"}},
		{operatingModeRunning, []string{fieldSpec, "operatingMode"}},
	} {
		if err := unstructured.SetNestedField(sandbox.Object, field.value, field.path...); err != nil {
			return err
		}
	}
	// An update: on a conflict the sandbox is still suspended, and the next
	// pass resumes it.
	return r.Update(ctx, sandbox)
}
