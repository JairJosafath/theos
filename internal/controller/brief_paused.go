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
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// A Brief pauses and resumes every Crew it made in one step (decisions 4 and
// 5 of docs/plans/done/upgrade-in-place.md).

const (
	reasonBriefPaused  = "Paused"
	reasonBriefResumed = "Resumed"
)

// pausedByBrief reports whether the Brief paused the Crew, so its resume
// unpauses it.
func pausedByBrief(crew *c4v1beta1.Crew) bool {
	return crew.Annotations[c4v1beta1.AnnotationPausedBy] == c4v1beta1.PausedByBrief
}

// markPaused pauses a Crew for its Brief, marking it so, and its runs too
// when runs says so.
func markPaused(crew *c4v1beta1.Crew, runs bool) {
	crew.Spec.Paused, crew.Spec.PauseRuns = true, runs
	if crew.Annotations == nil {
		crew.Annotations = map[string]string{}
	}
	crew.Annotations[c4v1beta1.AnnotationPausedBy] = c4v1beta1.PausedByBrief
}

// syncPaused brings the Crews the Brief made in line with its spec.paused,
// whatever its round is doing, and says on the Paused condition what still
// runs. A paused Brief pauses and marks each Crew that isn't paused, and sets
// one it marked back to paused; a Crew already paused without its mark was
// paused by hand and is left unmarked. The Crews it marked get its pauseRuns
// too. A resumed Brief unpauses only the Crews it marked, their runs
// included, and removes the mark. A Crew the Brief didn't make is never
// touched.
func (r *BriefReconciler) syncPaused(ctx context.Context, brief *c4v1beta1.Brief) error {
	var crews c4v1beta1.CrewList
	if err := r.List(ctx, &crews, client.InNamespace(brief.Namespace),
		client.MatchingLabels{c4v1beta1.LabelBrief: brief.Name}); err != nil {
		return err
	}
	paused, runs := brief.Spec.Paused, brief.Spec.Paused && brief.Spec.PauseRuns
	for i := range crews.Items {
		crew := &crews.Items[i]
		orig := crew.DeepCopy()
		switch {
		case paused && !crew.Spec.Paused,
			paused && pausedByBrief(crew) && crew.Spec.PauseRuns != runs:
			markPaused(crew, runs)
		case !paused && pausedByBrief(crew):
			crew.Spec.Paused, crew.Spec.PauseRuns = false, false
			delete(crew.Annotations, c4v1beta1.AnnotationPausedBy)
		default:
			continue
		}
		if err := r.Patch(ctx, crew, client.MergeFrom(orig)); err != nil {
			return fmt.Errorf("pausing or resuming Crew %s: %w", crew.Name, err)
		}
		logf.FromContext(ctx).Info("Set the Crew's paused from the Brief", "crew", crew.Name, "paused", paused,
			"pauseRuns", runs)
	}
	return r.setPausedCondition(ctx, brief, crews.Items)
}

// setPausedCondition sets the Paused condition of a paused Brief to the Tasks
// still running in its Crews and its planner Task, and to False once the
// Brief is resumed. A Brief that was never paused has none.
func (r *BriefReconciler) setPausedCondition(ctx context.Context, brief *c4v1beta1.Brief,
	crews []c4v1beta1.Crew) error {
	status := &brief.Status
	if !brief.Spec.Paused {
		if meta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionPaused) != nil {
			setCondition(&status.Conditions, brief.Generation, c4v1beta1.ConditionPaused,
				metav1.ConditionFalse, reasonBriefResumed, "Not paused")
		}
		return nil
	}
	tasks, err := r.briefTasks(ctx, brief, crews, brief.Spec.PauseRuns)
	if err != nil {
		return err
	}
	setCondition(&status.Conditions, brief.Generation, c4v1beta1.ConditionPaused,
		metav1.ConditionTrue, reasonBriefPaused, pausedConditionMessage(tasks, brief.Spec.PauseRuns))
	return nil
}

// briefTasks are the Tasks that are not final in the Brief's Crews and its
// planner's, sorted by name. With runs, only those whose runs the pause
// stops (#1995): a Crew paused by hand without pauseRuns, which the Brief
// leaves as it is, lets its Tasks' runs go on to their end, and they would
// never count as stopped.
func (r *BriefReconciler) briefTasks(ctx context.Context, brief *c4v1beta1.Brief,
	crews []c4v1beta1.Crew, runs bool) ([]*c4v1beta1.Task, error) {
	mine := make(map[string]bool, len(crews))
	for i := range crews {
		mine[crews[i].Name] = !runs || crewPause(&crews[i]) != ""
	}
	var tasks c4v1beta1.TaskList
	if err := r.List(ctx, &tasks, client.InNamespace(brief.Namespace)); err != nil {
		return nil, err
	}
	var running []*c4v1beta1.Task
	for i, t := range tasks.Items {
		if t.Status.Phase == c4v1beta1.TaskMerged || t.Status.Phase == c4v1beta1.TaskFailed {
			continue
		}
		planner := t.Labels[c4v1beta1.LabelBrief] == brief.Name && t.Spec.Kind == c4v1beta1.TaskPlan
		if planner || mine[t.Labels[LabelCrew]] {
			running = append(running, &tasks.Items[i])
		}
	}
	slices.SortFunc(running, func(a, b *c4v1beta1.Task) int { return strings.Compare(a.Name, b.Name) })
	return running, nil
}
