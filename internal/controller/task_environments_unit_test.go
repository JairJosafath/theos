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
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	portsfake "github.com/JairJosafath/theos/internal/ports/fake"
)

// #213: a run that takes a lease goes on to its sandbox only once the status
// that lists the lease is written, in the next pass. Made in the same pass,
// the sandbox, its Git calls and a status write that may conflict came
// before the lease was listed, and the TestEnvironment reconciler returned
// it as its holder's no more under the running agent.
func TestLeaseEnvironmentsWaitsUntilANewLeaseIsRecorded(t *testing.T) {
	ctx := context.Background()
	scheme := briefScheme(t)
	ns := "apps"
	env := newBrowserEnvironment(ns)
	container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
	task := newTask(ns, "pictures-1", "Take the extension's pictures")
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(env, container, task).Build()
	provider := &portsfake.TestEnvironments{}
	r := &TaskReconciler{Client: c, Scheme: scheme, APIReader: c,
		TestEnvironments: ports.TestEnvironmentProviders{model.TestEnvMemberLocal: provider}}
	const sandbox = "pictures-1-r1-implement"
	labels := []string{model.LabelReady, "env/browser"}
	status := task.Status.DeepCopy()

	ready, wait, err := r.leaseEnvironments(ctx, task, status, container, model.RoleImplementer, sandbox, labels)
	if err != nil {
		t.Fatal(err)
	}
	id := model.LeaseID(env.Name, sandbox)
	if _, ok := provider.Request(testEnvironment(env, nil), id); !ok {
		t.Fatalf("lease %s not taken", id)
	}
	if len(status.Environments) != 1 || status.Environments[0].Lease != id {
		t.Fatalf("status.environments = %v, want lease %s listed", status.Environments, id)
	}
	if wait == nil || !wait.record || len(ready) != 0 {
		t.Fatalf("got ready %v, wait %+v; want no environment ready and a wait for the status that records %s",
			ready, wait, id)
	}
	if res := r.waitForEnvironment(task, status, wait); res.RequeueAfter != 0 ||
		condition(status.Conditions, ConditionProgressing) != nil {
		t.Errorf("a wait to record a lease set Progressing or a recheck (%v): the written status brings the next pass",
			res)
	}

	ready, wait, err = r.leaseEnvironments(ctx, task, status, container, model.RoleImplementer, sandbox, labels)
	if err != nil {
		t.Fatal(err)
	}
	if wait != nil || len(ready) != 1 || ready[0].lease.ID != id {
		t.Fatalf("once listed, got ready %v, wait %+v; want lease %s ready", ready, wait, id)
	}
}

// #213: the TestEnvironment reconciler's cache may not have the Task status
// that lists a lease just taken; it reads the Task from the API server before
// it returns the lease as its holder's no more.
func TestTestEnvironmentKeepsALeaseItsCacheSeesUnlisted(t *testing.T) {
	ctx := context.Background()
	scheme := briefScheme(t)
	ns := "apps"
	now := time.Date(2026, 11, 13, 12, 29, 37, 0, time.UTC)
	const id = "gpu-39ded3f7fe"
	lease := ports.Lease{ID: id, Holder: "shadows-1", State: model.LeaseReady, Started: now.Add(-time.Hour)}

	stale := newTask(ns, "shadows-1", "")
	stale.Status.Phase = c4v1beta1.TaskImplementing
	listed := stale.DeepCopy()
	listed.Status.Environments = []c4v1beta1.TaskEnvironmentLease{{Name: "gpu", Lease: id,
		Sandbox: "shadows-1-r1-implement"}}
	cache := fake.NewClientBuilder().WithScheme(scheme).WithObjects(stale).WithStatusSubresource(stale).Build()
	for _, tt := range []struct {
		name      string
		apiServer *c4v1beta1.Task
		want      bool
	}{
		{"the API server's Task lists it", listed, false},
		{"the API server's Task doesn't list it either", stale.DeepCopy(), true},
	} {
		apiServer := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.apiServer).Build()
		r := &TestEnvironmentReconciler{Client: cache, Scheme: scheme, APIReader: apiServer,
			Now: func() time.Time { return now }}
		got, err := r.orphan(ctx, ns, lease, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Errorf("%s: orphan = %v, want %v", tt.name, got, tt.want)
		}
	}
}
