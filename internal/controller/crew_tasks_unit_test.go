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
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
)

// #332: the operator's TaskStore, Task objects read live, keeps the
// contract internal/engine relies on, as the fake does. A Triage Task on
// the Container is not among its Tasks: the Crew reads them apart.
func TestCrewTaskStoreContract(t *testing.T) {
	scheme := briefScheme(t)
	var c client.Client
	contract.TaskStore(t, contract.TaskStoreEnv{
		New: func(t *testing.T) (ports.TaskStore, string, string) {
			crew := &c4v1beta1.Crew{ObjectMeta: objectMeta("apps", "orders-crew"), Spec: c4v1beta1.CrewSpec{
				ContainerRef: c4v1beta1.LocalRef{Name: "orders-api"},
				Implementer:  c4v1beta1.LocalRef{Name: "implementer"},
				Reviewer:     c4v1beta1.LocalRef{Name: "reviewer"},
				SpecWriter:   &c4v1beta1.LocalRef{Name: "spec-writer"}}}
			container := &c4v1beta1.Container{ObjectMeta: objectMeta("apps", "orders-api")}
			triage := &c4v1beta1.Task{ObjectMeta: objectMeta("apps", "orders-api-triage-1"), Spec: c4v1beta1.TaskSpec{
				Kind: c4v1beta1.TaskTriage, ContainerRef: c4v1beta1.LocalRef{Name: "orders-api"},
				AgentRef: c4v1beta1.LocalRef{Name: "triager"}, Issue: 1}}
			c = fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&c4v1beta1.Task{}).
				WithObjects(crew, container, triage).Build()
			return &crewTaskStore{reader: c, writer: c, crew: crew, container: container}, "orders-api", "web"
		},
		Finish: func(t *testing.T, _, task string, failed bool) {
			var got c4v1beta1.Task
			if err := c.Get(context.Background(), client.ObjectKey{Namespace: "apps", Name: task}, &got); err != nil {
				t.Fatal(err)
			}
			got.Status.Phase = c4v1beta1.TaskMerged
			if failed {
				got.Status.Phase = c4v1beta1.TaskFailed
			}
			if err := c.Status().Update(context.Background(), &got); err != nil {
				t.Fatal(err)
			}
		},
	})
}
