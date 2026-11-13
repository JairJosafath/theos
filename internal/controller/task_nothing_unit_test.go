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
	"testing"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// #72: only the build of an issue, with no pull request yet, ends on its
// agent's "nothing to do". Anywhere else the operator reads it as the round
// with no change it was before: a Benchmark's Task is measured by its
// change, a Task with no issue has none to close, and a branch with a pull
// request is a reviewer's to judge.
func TestWithoutNothing(t *testing.T) {
	nothing := func() model.RunState {
		return model.RunState{Started: true, Done: true,
			Result: &model.RunResult{Summary: "Looked.\nNothing to do: all is current.", Nothing: "all is current."}}
	}
	issue := func(mutate func(*c4v1beta1.Task, *c4v1beta1.TaskStatus)) (*c4v1beta1.Task, *c4v1beta1.TaskStatus) {
		task := &c4v1beta1.Task{Spec: c4v1beta1.TaskSpec{Issue: 7}}
		status := &c4v1beta1.TaskStatus{}
		if mutate != nil {
			mutate(task, status)
		}
		return task, status
	}
	task, status := issue(nil)
	if got := withoutNothing(task, status, nothing()); got.Result.Nothing != "all is current." || got.Result.Error != "" {
		t.Errorf("an issue's build lost its nothing to do: %+v", got.Result)
	}
	for name, mutate := range map[string]func(*c4v1beta1.Task, *c4v1beta1.TaskStatus){
		"a Task with no issue":     func(task *c4v1beta1.Task, _ *c4v1beta1.TaskStatus) { task.Spec.Issue = 0 },
		"a Task with pull request": func(_ *c4v1beta1.Task, status *c4v1beta1.TaskStatus) { status.PullRequest = 12 },
		"a Benchmark's Task": func(task *c4v1beta1.Task, _ *c4v1beta1.TaskStatus) {
			task.Spec.Benchmark = &c4v1beta1.TaskBenchmarkSpec{}
		},
	} {
		task, status := issue(mutate)
		run := nothing()
		got := withoutNothing(task, status, run)
		if got.Result.Nothing != "" || got.Result.Error != model.NoChangeError(run.Result.Summary) {
			t.Errorf("%s: %+v, want the round with no change it was", name, got.Result)
		}
		if run.Result.Nothing == "" {
			t.Errorf("%s: the run's own result was changed", name)
		}
	}
	plain := model.RunState{Started: true, Done: true, Result: &model.RunResult{Commit: "abc"}}
	if got := withoutNothing(task, status, plain); got.Result != plain.Result {
		t.Error("a run with a change was touched")
	}
}
