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
	"strconv"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// operatorCapabilities is what the operator runtime does: everything a Crew
// and its Agents ask (docs/plans/lead-runtime.md, decision 6).
var operatorCapabilities = model.OperatorCapabilities()

// crewRuntime is the operator's ports.Runtime for one Crew's reconcile: the
// Crew's Task objects are its TaskStore.
type crewRuntime struct {
	store *crewTaskStore
}

var _ ports.Runtime = crewRuntime{}

func (r crewRuntime) Store() ports.TaskStore { return r.store }

// Workers is nil: a Crew's tick runs no turn. A Task's turns are its
// reconcile's Sandboxes (sandboxWorkers, #336).
func (crewRuntime) Workers() ports.Workers { return nil }

func (crewRuntime) Capabilities() ports.Capabilities { return operatorCapabilities }

// crewTaskStore is the operator's TaskStore for one Crew: Task objects in the
// Crew's namespace, read live, and started with the Agents the Crew and its
// routes name.
type crewTaskStore struct {
	// reader reads the Tasks without the cache: a Task created a moment ago
	// must count, or the Crew starts two.
	reader    client.Reader
	writer    client.Client
	crew      *c4v1beta1.Crew
	container *c4v1beta1.Container
}

var (
	_ ports.TaskStore   = (*crewTaskStore)(nil)
	_ ports.TriageStore = (*crewTaskStore)(nil)
)

// Tasks are container's Tasks but its Triage Tasks, which the Crew reads
// apart (triageTasks).
func (s *crewTaskStore) Tasks(ctx context.Context, container string) ([]model.CrewTask, error) {
	var tasks c4v1beta1.TaskList
	if err := s.reader.List(ctx, &tasks, client.InNamespace(s.crew.Namespace)); err != nil {
		return nil, err
	}
	var out []model.CrewTask
	for i := range tasks.Items {
		if t := &tasks.Items[i]; t.Spec.ContainerRef.Name == container && !isTriage(t) {
			out = append(out, crewTask(t))
		}
	}
	return out, nil
}

// Start creates the Task the Crew starts for an issue (crewIssueTask); one
// that exists already is left as it is.
func (s *crewTaskStore) Start(ctx context.Context, container string, start model.IssueStart) error {
	if container != s.container.Name {
		return fmt.Errorf("the Crew %s starts Tasks on %s, not %s", s.crew.Name, s.container.Name, container)
	}
	task := crewIssueTask(s.crew, s.container, start)
	if err := s.writer.Create(ctx, task); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	logf.FromContext(ctx).Info("Started Task for issue", "task", start.Task, "issue", start.Issue,
		"attempt", start.Attempt, "spec", start.Spec, "allowedPaths", start.AllowedPaths,
		"implementer", task.Spec.AgentRef.Name, "reviewer", task.Spec.ReviewerRef.Name)
	return nil
}

// TriageTasks are container's Triage Tasks, read live as Tasks are
// (triageTasks).
func (s *crewTaskStore) TriageTasks(ctx context.Context, container string) ([]model.TriageTask, error) {
	var tasks c4v1beta1.TaskList
	if err := s.reader.List(ctx, &tasks, client.InNamespace(s.crew.Namespace)); err != nil {
		return nil, err
	}
	var on []c4v1beta1.Task
	for _, t := range tasks.Items {
		if t.Spec.ContainerRef.Name == container {
			on = append(on, t)
		}
	}
	triage, _ := triageTasks(on)
	return triage, nil
}

// StartTriage creates the Triage Task model.NextTriage starts: the Crew's
// triager on the issue, with no reviewer.
func (s *crewTaskStore) StartTriage(ctx context.Context, container string, start model.TriageStart,
	triager string) error {
	if container != s.container.Name {
		return fmt.Errorf("the Crew %s starts Tasks on %s, not %s", s.crew.Name, s.container.Name, container)
	}
	task := &c4v1beta1.Task{
		ObjectMeta: metav1.ObjectMeta{Namespace: s.crew.Namespace, Name: start.Task, Labels: map[string]string{
			LabelCrew: s.crew.Name, LabelIssue: strconv.Itoa(start.Issue)}},
		Spec: c4v1beta1.TaskSpec{
			Kind:         c4v1beta1.TaskTriage,
			ContainerRef: c4v1beta1.LocalRef{Name: container},
			AgentRef:     c4v1beta1.LocalRef{Name: triager},
			Issue:        int32(start.Issue),
		},
	}
	if err := s.writer.Create(ctx, task); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	logf.FromContext(ctx).Info("Started Triage Task for issue", "task", start.Task, "issue", start.Issue,
		"attempt", start.Attempt, "triager", triager)
	return nil
}

// Progress is the Task's record from its status.
func (s *crewTaskStore) Progress(ctx context.Context, task string) (ports.TaskRecord, error) {
	var t c4v1beta1.Task
	if err := s.reader.Get(ctx, client.ObjectKey{Namespace: s.crew.Namespace, Name: task}, &t); err != nil {
		return ports.TaskRecord{}, err
	}
	return recordOf(&t.Status), nil
}

// SaveProgress writes r into the Task's status.
func (s *crewTaskStore) SaveProgress(ctx context.Context, task string, r ports.TaskRecord) error {
	var t c4v1beta1.Task
	if err := s.reader.Get(ctx, client.ObjectKey{Namespace: s.crew.Namespace, Name: task}, &t); err != nil {
		return err
	}
	setRecord(&t.Status, r)
	if !r.PhaseSince.IsZero() {
		t.Status.PhaseChangedAt = &metav1.Time{Time: r.PhaseSince}
	}
	return s.writer.Status().Update(ctx, &t)
}
