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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// Running Tasks pause at a tool boundary (step 12 of
// docs/plans/self-hosting.md). A Crew's or Brief's spec.pauseRuns makes the
// operator set c4.factory.io/pause on the pod template of each working run's
// Sandbox; Agent Sandbox copies a template's annotations onto the running
// pod, and the pod sees its own through the downward API. c4agent's pause
// guard then stops the agent, saves a clean checkpoint and reports Paused;
// the operator suspends the Sandbox as it does an interrupted run's, with no
// round, rerun or resume spent. Unset, each paused run resumes in place.

// The sandbox pod's downward API volume: its name, and the file at
// agentrunner.PodInfoDir that holds the pod's annotations.
const (
	podInfoVolume = "podinfo"
	podInfoFile   = "annotations"
)

// podInfo is the downward API volume with the pod's annotations.
func podInfo() corev1.Volume {
	return corev1.Volume{Name: podInfoVolume, VolumeSource: corev1.VolumeSource{
		DownwardAPI: &corev1.DownwardAPIVolumeSource{Items: []corev1.DownwardAPIVolumeFile{{
			Path: podInfoFile, FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.annotations"},
		}}},
	}}
}

// The Task's Paused condition's reasons: an agent still runs toward its
// pause, none runs, or the pause ended.
const (
	reasonPausing    = "Pausing"
	reasonRunsPaused = "Paused"
	reasonRunsResume = "Resumed"
)

// crewPause is why the Crew pauses its Tasks' runs; empty while it doesn't.
// A Crew its Brief paused names the Brief.
func crewPause(crew *c4v1beta1.Crew) string {
	switch {
	case !crew.Spec.Paused || !crew.Spec.PauseRuns:
		return ""
	case pausedByBrief(crew) && crew.Labels[c4v1beta1.LabelBrief] != "":
		return briefPauseReason(crew.Labels[c4v1beta1.LabelBrief])
	}
	return fmt.Sprintf("Crew %s paused its runs", crew.Name)
}

// briefPause is why the Brief pauses its planner's run; empty while it doesn't.
func briefPause(brief *c4v1beta1.Brief) string {
	if !brief.Spec.Paused || !brief.Spec.PauseRuns {
		return ""
	}
	return briefPauseReason(brief.Name)
}

func briefPauseReason(brief string) string {
	return fmt.Sprintf("Brief %s paused its runs", brief)
}

// syncPause brings the Task's current sandbox in line with the pause of its
// runs: a working run's pod template carries the pause annotation exactly
// while model.PauseRequest says, and a suspended run whose resume the pause
// holds loses its shutdown time, so the pause can last past it. name is the
// sandbox of the Task's phase and round; another one is not the Task's run.
func (r *TaskReconciler) syncPause(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	name string, facts model.TaskFacts) error {
	run := facts.Run
	if name == "" || name != status.Sandbox || !run.Started {
		return nil
	}
	switch {
	case run.Suspended && facts.Pause != "":
		return r.holdSandbox(ctx, task.Namespace, name)
	case !run.Done && !run.Suspended:
		return r.askPause(ctx, task.Namespace, name, model.PauseRequest(facts))
	}
	return nil
}

// askPause sets the pause annotation on the sandbox's pod template to pause,
// or takes it off when pause is empty. Only the annotation changes: it is the
// one thing the operator changes under a running agent.
func (r *TaskReconciler) askPause(ctx context.Context, ns, name, pause string) error {
	sandbox := newSandbox()
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, sandbox); err != nil {
		return client.IgnoreNotFound(err)
	}
	annotations, _, _ := unstructured.NestedStringMap(sandbox.Object, fieldSpec, "podTemplate", "metadata", "annotations")
	if annotations[c4v1beta1.AnnotationPause] == pause {
		return nil
	}
	orig := sandbox.DeepCopy()
	if annotations == nil {
		annotations = map[string]string{}
	}
	if pause == "" {
		delete(annotations, c4v1beta1.AnnotationPause)
	} else {
		annotations[c4v1beta1.AnnotationPause] = pause
	}
	if err := unstructured.SetNestedField(sandbox.Object, toAnyMap(annotations),
		fieldSpec, "podTemplate", "metadata", "annotations"); err != nil {
		return err
	}
	if err := r.Patch(ctx, sandbox, client.MergeFrom(orig)); err != nil {
		return client.IgnoreNotFound(err)
	}
	if pause == "" {
		logf.FromContext(ctx).Info("Took back the pause of a running agent", "sandbox", name)
	} else {
		logf.FromContext(ctx).Info("Asked a running agent to pause at its next tool boundary", "sandbox", name,
			"reason", pause)
	}
	return nil
}

// holdSandbox takes the shutdown time off a suspended sandbox whose resume a
// pause holds: Agent Sandbox would expire it, and its checkpoint with it.
// resumeSandbox sets a new one.
func (r *TaskReconciler) holdSandbox(ctx context.Context, ns, name string) error {
	sandbox := newSandbox()
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, sandbox); err != nil {
		return client.IgnoreNotFound(err)
	}
	if _, found, _ := unstructured.NestedString(sandbox.Object, fieldSpec, "shutdownTime"); !found {
		return nil
	}
	orig := sandbox.DeepCopy()
	unstructured.RemoveNestedField(sandbox.Object, fieldSpec, "shutdownTime")
	return client.IgnoreNotFound(r.Patch(ctx, sandbox, client.MergeFrom(orig)))
}

// setPaused records on the Task's Paused condition where the step leaves it
// in a pause of its runs: False while its agent still runs toward the pause,
// True once none runs, and False again when the pause ends. A Task never
// paused has none. The Crew and the Brief count the Tasks not yet True.
func (r *TaskReconciler) setPaused(task *c4v1beta1.Task, status *c4v1beta1.TaskStatus, step model.TaskStep,
	pause string) {
	switch step.Pause {
	case model.PausePausing:
		setCondition(&status.Conditions, task.Generation, c4v1beta1.ConditionPaused, metav1.ConditionFalse,
			reasonPausing, step.Message)
	case model.PausePaused:
		setCondition(&status.Conditions, task.Generation, c4v1beta1.ConditionPaused, metav1.ConditionTrue,
			reasonRunsPaused, fmt.Sprintf("No agent of the Task runs: %s", pause))
	default:
		if meta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionPaused) != nil {
			setCondition(&status.Conditions, task.Generation, c4v1beta1.ConditionPaused, metav1.ConditionFalse,
				reasonRunsResume, "Not paused")
		}
	}
}

// runsStopped reports whether no agent of the Task runs while its runs are
// paused: its Paused condition is True.
func runsStopped(task *c4v1beta1.Task) bool {
	return meta.IsStatusConditionTrue(task.Status.Conditions, c4v1beta1.ConditionPaused)
}

// stillRunning names, sorted as given, the Tasks among tasks that are not
// final, and of those the ones whose agent still runs when runs pauses them
// too: the Tasks a paused Crew's or Brief's Paused condition counts.
func stillRunning(tasks []*c4v1beta1.Task, runs bool) []string {
	var names []string
	for _, t := range tasks {
		if isFinal(t.Status.Phase) || runs && runsStopped(t) {
			continue
		}
		names = append(names, t.Name)
	}
	return names
}

// pausedConditionMessage is a paused Crew's or Brief's Paused condition's message.
func pausedConditionMessage(tasks []*c4v1beta1.Task, runs bool) string {
	if runs {
		return model.RunsPausedMessage(stillRunning(tasks, true))
	}
	return model.BriefPausedMessage(stillRunning(tasks, false))
}
