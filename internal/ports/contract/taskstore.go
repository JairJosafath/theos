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

package contract

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// TaskStoreEnv builds the TaskStore under test.
type TaskStoreEnv struct {
	// New returns a fresh TaskStore that holds no Task, and two Containers
	// it keeps Tasks for. Its Crew has a spec writer: a start with Spec
	// starts a Spec Task.
	New func(t *testing.T) (store ports.TaskStore, container, other string)
	// Finish ends the Task named task on container, failed or merged, as
	// the runtime's own Task tick would.
	Finish func(t *testing.T, container, task string, failed bool)
	// NoSpecs means the store starts no Spec Task: a start with Spec starts
	// one that builds (the lead runtime, whose spec writer is RoleNotBuilt).
	NoSpecs bool
	// Cannot names the subtests the store can't run, with why; they are
	// skipped.
	Cannot map[string]string
}

// TaskStore states what internal/engine relies on of every TaskStore: a
// started Task is listed at once, on its Container alone, with its issue
// and whether it writes a spec; a start repeated is no error and starts
// nothing more; a Task that ended is listed as final, failed or not, or no
// longer listed, as a store that keeps only the Tasks at work does; and a
// Task's record, Pending when it starts, is kept as saved, a final phase
// ending it.
func TaskStore(t *testing.T, env TaskStoreEnv) {
	t.Helper()
	ctx, cancel := suiteContext()
	defer cancel()
	runSubtests(t, taskStoreSuite{ctx: ctx, env: env}, env.Cannot, []subtest[taskStoreSuite]{
		{"lists a started Task at once, on its Container alone", taskStoreSuite.starts},
		{"starts a Task once however often it is asked", taskStoreSuite.idempotent},
		{"lists a Task that ended as final", taskStoreSuite.ends},
		{"keeps a Task's progress, and ends it once it is final", taskStoreSuite.progress},
	})
}

// taskStoreSuite holds what every subtest of TaskStore shares.
type taskStoreSuite struct {
	ctx context.Context
	env TaskStoreEnv
}

// tasks are store's Tasks on container, failing t on an error.
func (s taskStoreSuite) tasks(t *testing.T, store ports.TaskStore, container string) []model.CrewTask {
	t.Helper()
	return must(store.Tasks(s.ctx, container))(t)
}

// named is the Task called name in tasks, failing t when there is none.
func named(t *testing.T, tasks []model.CrewTask, name string) model.CrewTask {
	t.Helper()
	i := slices.IndexFunc(tasks, func(c model.CrewTask) bool { return c.Name == name })
	if i < 0 {
		t.Fatalf("no Task %s among %+v", name, tasks)
	}
	return tasks[i]
}

func (s taskStoreSuite) starts(t *testing.T) {
	store, container, other := s.env.New(t)
	if got := s.tasks(t, store, container); len(got) != 0 {
		t.Fatalf("a fresh store lists %+v, want no Task", got)
	}
	build := model.IssueStart{Issue: 3, Task: container + "-3", Attempt: 1}
	spec := model.IssueStart{Issue: 5, Task: container + "-5", Attempt: 1, Spec: true}
	for _, start := range []model.IssueStart{build, spec} {
		if err := store.Start(s.ctx, container, start); err != nil {
			t.Fatalf("Start(%s): %v", start.Task, err)
		}
	}
	tasks := s.tasks(t, store, container)
	if len(tasks) != 2 {
		t.Fatalf("Tasks = %+v, want the two started", tasks)
	}
	if got := named(t, tasks, build.Task); got.Issue != 3 || got.Final || got.Spec {
		t.Errorf("Task %s = %+v, want issue 3, not final, no spec", build.Task, got)
	}
	if got := named(t, tasks, spec.Task); got.Issue != 5 || got.Final || got.Spec == s.env.NoSpecs {
		t.Errorf("Task %s = %+v, want issue 5, not final, a spec unless the store starts none", spec.Task, got)
	}
	if got := s.tasks(t, store, other); len(got) != 0 {
		t.Errorf("the other Container lists %+v, want no Task", got)
	}
}

func (s taskStoreSuite) idempotent(t *testing.T) {
	store, container, _ := s.env.New(t)
	start := model.IssueStart{Issue: 7, Task: container + "-7", Attempt: 1}
	for range 3 {
		if err := store.Start(s.ctx, container, start); err != nil {
			t.Fatalf("Start(%s) again: %v, want no error", start.Task, err)
		}
	}
	if got := s.tasks(t, store, container); len(got) != 1 || got[0].Name != start.Task {
		t.Errorf("Tasks = %+v, want %s once", got, start.Task)
	}
}

func (s taskStoreSuite) ends(t *testing.T) {
	store, container, _ := s.env.New(t)
	merged := model.IssueStart{Issue: 8, Task: container + "-8", Attempt: 1}
	failed := model.IssueStart{Issue: 9, Task: container + "-9", Attempt: 1}
	for _, start := range []model.IssueStart{merged, failed} {
		if err := store.Start(s.ctx, container, start); err != nil {
			t.Fatalf("Start(%s): %v", start.Task, err)
		}
	}
	s.env.Finish(t, container, merged.Task, false)
	s.env.Finish(t, container, failed.Task, true)
	tasks := s.tasks(t, store, container)
	endedOrGone(t, tasks, merged.Task, false)
	endedOrGone(t, tasks, failed.Task, true)
}

// endedOrGone fails t unless the Task called name is listed final, failed
// as failed says, or not listed.
func endedOrGone(t *testing.T, tasks []model.CrewTask, name string, failed bool) {
	t.Helper()
	i := slices.IndexFunc(tasks, func(c model.CrewTask) bool { return c.Name == name })
	if i >= 0 && (!tasks[i].Final || tasks[i].Failed != failed) {
		t.Errorf("ended Task = %+v, want final, failed %v, or not listed", tasks[i], failed)
	}
}

func (s taskStoreSuite) progress(t *testing.T) {
	store, container, _ := s.env.New(t)
	start := model.IssueStart{Issue: 4, Task: container + "-4", Attempt: 1}
	if err := store.Start(s.ctx, container, start); err != nil {
		t.Fatal(err)
	}
	rec := must(store.Progress(s.ctx, start.Task))(t)
	if rec.Progress.Phase != model.TaskPending || rec.Progress.Round != 0 {
		t.Errorf("a started Task's record = %+v, want Pending, round 0", rec.Progress)
	}
	since := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	rec = ports.TaskRecord{Progress: model.TaskProgress{Phase: model.TaskChecking, Round: 2, Commit: "abc1234",
		Feedback: "Handle an empty order."}, PullRequest: 7, PullRequestURL: "https://host.test/pulls/7",
		Summary: summaryAdded, PhaseSince: since}
	if err := store.SaveProgress(s.ctx, start.Task, rec); err != nil {
		t.Fatal(err)
	}
	got := must(store.Progress(s.ctx, start.Task))(t)
	if got.Progress.Phase != model.TaskChecking || got.Progress.Round != 2 || got.Progress.Commit != "abc1234" ||
		got.Progress.Feedback != rec.Progress.Feedback || got.PullRequest != 7 ||
		got.PullRequestURL != rec.PullRequestURL || got.Summary != rec.Summary || !got.PhaseSince.Equal(since) {
		t.Errorf("record read back = %+v, want %+v", got, rec)
	}
	if named(t, s.tasks(t, store, container), start.Task).Final {
		t.Error("a Checking Task is listed final")
	}
	rec.Progress.Phase = model.TaskMerged
	if err := store.SaveProgress(s.ctx, start.Task, rec); err != nil {
		t.Fatal(err)
	}
	endedOrGone(t, s.tasks(t, store, container), start.Task, false)
	if _, err := store.Progress(s.ctx, "no-such-task"); err == nil {
		t.Error("the record of a Task the store doesn't hold is no error")
	}
}
