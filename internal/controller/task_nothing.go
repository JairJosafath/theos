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

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// "Nothing to do" is a result (docs/design.md, "Agents and Tasks"; #72). An
// implementer that finds its issue needs no change says so and changes
// nothing: a weekly dependency update with nothing to update is the common
// case. The Task ends done, with no pull request, no review and no merge,
// and its issue closes with the agent's words.

// withoutNothing is run with a "nothing to do" the Task can't end on turned
// into the failed round it was before: a Benchmark's Task, whose change is
// what is measured; a Task with no issue to close; and one with a pull
// request, whose branch a reviewer judges as it is.
func withoutNothing(task *c4v1beta1.Task, status *c4v1beta1.TaskStatus, run model.RunState) model.RunState {
	if run.Result == nil || run.Result.Nothing == "" ||
		task.Spec.Issue > 0 && status.PullRequest == 0 && task.Spec.Benchmark == nil {
		return run
	}
	result := *run.Result
	result.Error, result.Nothing = model.NoChangeError(result.Summary), ""
	run.Result = &result
	return run
}

// nothingToDo ends a Task whose agent found its issue needs no change: it
// closes the issue with the agent's reason and ends the Task Merged, reason
// NothingToDo, with nothing merged.
func (r *TaskReconciler) nothingToDo(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, step model.TaskStep) error {
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return err
	}
	if err := r.endRun(ctx, task, status); err != nil {
		return err
	}
	note := step.Feedback
	status.Summary = model.Clip(note, 2000)
	logNotes(ctx, task, r.endIssue(ctx, provider, repo, task, model.NothingComment(task.Name, note),
		"its agent found nothing to do"))
	r.finish(task, status, c4v1beta1.TaskMerged, metav1.ConditionTrue, model.ReasonNothingToDo, step.Message)
	logf.FromContext(ctx).Info("Ended Task with nothing to do", "issue", task.Spec.Issue, "why", note)
	return nil
}

// endedWithNoChange reports whether the Task ended done with nothing merged:
// its agent found its issue needs no change. No release, no count of merged
// changes and no judge takes it for a change.
func endedWithNoChange(task *c4v1beta1.Task) bool {
	done := apimeta.FindStatusCondition(task.Status.Conditions, ConditionSucceeded)
	return task.Status.Phase == c4v1beta1.TaskMerged && done != nil && done.Reason == model.ReasonNothingToDo
}
