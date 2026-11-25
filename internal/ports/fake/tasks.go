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

package fake

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// Tasks is a TaskStore in memory: the Tasks of each Container, in the order
// they started. A test moves a Task on with Set.
type Tasks struct {
	mu    sync.Mutex
	tasks map[string][]model.CrewTask // by container
	// records are the Tasks' records, by name.
	records map[string]ports.TaskRecord
	// starts are the starts asked for, repeats included, in order.
	starts []model.IssueStart
	// startErr makes Start fail.
	startErr error
}

var _ ports.TaskStore = (*Tasks)(nil)

// NewTasks returns an empty TaskStore.
func NewTasks() *Tasks {
	return &Tasks{tasks: map[string][]model.CrewTask{}, records: map[string]ports.TaskRecord{}}
}

// Tasks are container's Tasks, the oldest first.
func (s *Tasks) Tasks(_ context.Context, container string) ([]model.CrewTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.tasks[container]), nil
}

// Start records a Task for s's issue, unless one of its name is there.
func (s *Tasks) Start(_ context.Context, container string, start model.IssueStart) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts = append(s.starts, start)
	if s.startErr != nil {
		return s.startErr
	}
	for _, tasks := range s.tasks {
		if slices.ContainsFunc(tasks, func(t model.CrewTask) bool { return t.Name == start.Task }) {
			return nil
		}
	}
	s.tasks[container] = append(s.tasks[container], model.CrewTask{Name: start.Task, Issue: start.Issue,
		Spec: start.Spec})
	s.records[start.Task] = ports.TaskRecord{Progress: model.TaskProgress{Phase: model.TaskPending}}
	return nil
}

// Progress is the Task's record.
func (s *Tasks) Progress(_ context.Context, task string) (ports.TaskRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[task]
	if !ok {
		return ports.TaskRecord{}, fmt.Errorf("no Task %s", task)
	}
	return r, nil
}

// SaveProgress keeps r, and ends the Task in Tasks once r is final.
func (s *Tasks) SaveProgress(_ context.Context, task string, r ports.TaskRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[task]; !ok {
		return fmt.Errorf("no Task %s", task)
	}
	s.records[task] = r
	for _, tasks := range s.tasks {
		if i := slices.IndexFunc(tasks, func(t model.CrewTask) bool { return t.Name == task }); i >= 0 {
			phase := r.Progress.Phase
			tasks[i].Final = phase == model.TaskMerged || phase == model.TaskFailed
			tasks[i].Failed = phase == model.TaskFailed
		}
	}
	return nil
}

// Starts are the starts asked for, repeats included, in order.
func (s *Tasks) Starts() []model.IssueStart {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.starts)
}

// Set replaces the Task named t.Name on container, or adds it.
func (s *Tasks) Set(container string, t model.CrewTask) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks := s.tasks[container]
	if i := slices.IndexFunc(tasks, func(c model.CrewTask) bool { return c.Name == t.Name }); i >= 0 {
		tasks[i] = t
		return
	}
	s.tasks[container] = append(tasks, t)
}

// Get is the Task named name on container.
func (s *Tasks) Get(container, name string) (model.CrewTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := slices.IndexFunc(s.tasks[container], func(c model.CrewTask) bool { return c.Name == name }); i >= 0 {
		return s.tasks[container][i], nil
	}
	return model.CrewTask{}, fmt.Errorf("no Task %s on %s", name, container)
}

// FailStart makes Start fail with err from now on; nil lets it succeed.
func (s *Tasks) FailStart(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startErr = err
}

// Runtime is a ports.Runtime on a Tasks and, when set, a Workers, with the
// capabilities it is given.
type Runtime struct {
	TaskStore *Tasks
	Work      *Workers
	Caps      ports.Capabilities
}

var _ ports.Runtime = (*Runtime)(nil)

// NewRuntime is a Runtime on a fresh Tasks that can do everything.
func NewRuntime() *Runtime {
	return &Runtime{TaskStore: NewTasks(), Caps: model.OperatorCapabilities()}
}

// Store is the Runtime's Tasks.
func (r *Runtime) Store() ports.TaskStore { return r.TaskStore }

// Workers is the Runtime's Work; nil when it has none.
func (r *Runtime) Workers() ports.Workers {
	if r.Work == nil {
		return nil
	}
	return r.Work
}

// Capabilities are the Runtime's Caps.
func (r *Runtime) Capabilities() ports.Capabilities { return r.Caps }
