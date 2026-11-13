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

// Package taskfacts reads the API's Task into the plain values the factory's
// numbers are computed from (#142). The operator's Prometheus series (#59,
// internal/controller) and theos-session report (#66, cmd/theos-session)
// both call it, so the two count a Task alike: a new fact, or a new way a
// Task ends, is written here once.
//
// It imports the API's types, apimachinery and internal/model, and nothing
// else of the module or of controller-runtime (TestImports), so a command in
// a devcontainer links it without the operator.
package taskfacts

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// ConditionSucceeded is the Task condition that is True once the Task ended
// done and False once it failed. Its reason says how it ended: Merged,
// PlanAccepted, Scored, Triaged, DoneByStandardChange or NothingToDo.
const ConditionSucceeded = "Succeeded"

// EndedWithNoChange reports whether the Task ended done with nothing merged:
// its agent found its issue needs no change (#72). Only the Succeeded
// condition's reason says so; every Task that ends done has the condition.
// No release, no count of merged changes and no judge takes such a Task for
// a change.
func EndedWithNoChange(task *c4v1beta1.Task) bool {
	done := apimeta.FindStatusCondition(task.Status.Conditions, ConditionSucceeded)
	return task.Status.Phase == c4v1beta1.TaskMerged && done != nil && done.Reason == model.ReasonNothingToDo
}

// Metric is what the factory's numbers read of task.
func Metric(task *c4v1beta1.Task) model.TaskMetricFacts {
	status := &task.Status
	f := model.TaskMetricFacts{
		Namespace:    task.Namespace,
		Container:    task.Spec.ContainerRef.Name,
		Kind:         string(task.Spec.Kind),
		Benchmark:    task.Spec.Benchmark != nil,
		Phase:        string(status.Phase),
		Round:        int(status.Round),
		CostMilliUSD: status.CostMilliUSD,
	}
	if f.Kind == "" {
		f.Kind = string(c4v1beta1.TaskImplement)
	}
	if n := status.PersonCommits; n != nil {
		commits := int(*n)
		f.PersonCommits = &commits
	}
	// Who let the change in (#92): a person, at a spec's review point or
	// where the Crew's approval rules held it, or nobody, where they asked
	// for no review and none was made.
	spec, approval := status.Spec, status.Approval
	f.ApprovedByPerson = spec != nil && spec.ApprovedBy != "" || approval != nil && approval.ApprovedBy != ""
	f.Unreviewed = approval != nil && approval.Require == c4v1beta1.ApprovalNone && status.Review == nil
	f.NoChange = EndedWithNoChange(task)
	for _, step := range status.History {
		f.History = append(f.History, model.TaskMetricStep{At: step.At.Time, Phase: string(step.Phase), Reason: step.Reason})
	}
	for _, run := range status.Runs {
		r := model.TaskMetricRun{Agent: run.Agent, Harness: run.Harness, Model: run.Model, Role: run.Role,
			CostMilliUSD: run.CostMilliUSD}
		if t := run.Tokens; t != nil {
			r.Input, r.Output, r.CacheRead, r.CacheWrite = t.Input, t.Output, t.CacheRead, t.CacheWrite
		}
		f.Runs = append(f.Runs, r)
	}
	return f
}
