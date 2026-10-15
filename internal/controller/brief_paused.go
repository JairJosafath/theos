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
	"slices"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// A Brief pauses and resumes every Crew it made in one step (decisions 4 and
// 5 of docs/plans/done/upgrade-in-place.md).

const (
	reasonBriefPaused  = "Paused"
	reasonBriefResumed = "Resumed"
)

// pausedByBrief reports whether the Brief paused the Crew, so its resume
// unpauses it.
func pausedByBrief(crew *c4v1alpha1.Crew) bool {
	return crew.Annotations[c4v1alpha1.AnnotationPausedBy] == c4v1alpha1.PausedByBrief
}

// markPaused pauses a Crew for its Brief, marking it so.
func markPaused(crew *c4v1alpha1.Crew) {
	crew.Spec.Paused = true
	if crew.Annotations == nil {
		crew.Annotations = map[string]string{}
	}
	crew.Annotations[c4v1alpha1.AnnotationPausedBy] = c4v1alpha1.PausedByBrief
}

// syncPaused brings the Crews the Brief made in line with its spec.paused,
// whatever its round is doing, and says on the Paused condition what still
// runs. A paused Brief pauses and marks each Crew that isn't paused, and sets
// one it marked back to paused; a Crew already paused without its mark was
// paused by hand and is left unmarked. A resumed Brief unpauses only the
// Crews it marked and removes the mark. A Crew the Brief didn't make is never
// touched.
func (r *BriefReconciler) syncPaused(ctx context.Context, brief *c4v1alpha1.Brief) error {
	var crews c4v1alpha1.CrewList
	if err := r.List(ctx, &crews, client.InNamespace(brief.Namespace),
		client.MatchingLabels{c4v1alpha1.LabelBrief: brief.Name}); err != nil {
		return err
	}
	paused := brief.Spec.Paused
	for i := range crews.Items {
		crew := &crews.Items[i]
		orig := crew.DeepCopy()
		switch {
		case paused && !crew.Spec.Paused:
			markPaused(crew)
		case !paused && pausedByBrief(crew):
			crew.Spec.Paused = false
			delete(crew.Annotations, c4v1alpha1.AnnotationPausedBy)
		default:
			continue
		}
		if err := r.Patch(ctx, crew, client.MergeFrom(orig)); err != nil {
			return fmt.Errorf("pausing or resuming Crew %s: %w", crew.Name, err)
		}
		logf.FromContext(ctx).Info("Set the Crew's paused from the Brief", "crew", crew.Name, "paused", paused)
	}
	return r.setPausedCondition(ctx, brief, crews.Items)
}

// setPausedCondition sets the Paused condition of a paused Brief to the Tasks
// still running in its Crews and its planner Task, and to False once the
// Brief is resumed. A Brief that was never paused has none.
func (r *BriefReconciler) setPausedCondition(ctx context.Context, brief *c4v1alpha1.Brief,
	crews []c4v1alpha1.Crew) error {
	status := &brief.Status
	if !brief.Spec.Paused {
		if meta.FindStatusCondition(status.Conditions, c4v1alpha1.ConditionPaused) != nil {
			setCondition(&status.Conditions, brief.Generation, c4v1alpha1.ConditionPaused,
				metav1.ConditionFalse, reasonBriefResumed, "Not paused")
		}
		return nil
	}
	running, err := r.runningTasks(ctx, brief, crews)
	if err != nil {
		return err
	}
	setCondition(&status.Conditions, brief.Generation, c4v1alpha1.ConditionPaused,
		metav1.ConditionTrue, reasonBriefPaused, model.BriefPausedMessage(running))
	return nil
}

// runningTasks names the Tasks that are not final in the Brief's Crews and
// its planner's, sorted.
func (r *BriefReconciler) runningTasks(ctx context.Context, brief *c4v1alpha1.Brief,
	crews []c4v1alpha1.Crew) ([]string, error) {
	mine := make(map[string]bool, len(crews))
	for _, crew := range crews {
		mine[crew.Name] = true
	}
	var tasks c4v1alpha1.TaskList
	if err := r.List(ctx, &tasks, client.InNamespace(brief.Namespace)); err != nil {
		return nil, err
	}
	var running []string
	for _, t := range tasks.Items {
		if t.Status.Phase == c4v1alpha1.TaskMerged || t.Status.Phase == c4v1alpha1.TaskFailed {
			continue
		}
		planner := t.Labels[c4v1alpha1.LabelBrief] == brief.Name && t.Spec.Kind == c4v1alpha1.TaskPlan
		if planner || mine[t.Labels[LabelCrew]] {
			running = append(running, t.Name)
		}
	}
	slices.Sort(running)
	return running, nil
}
