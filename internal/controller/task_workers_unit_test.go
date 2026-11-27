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
	"encoding/json"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
)

// workersTask is a Task on issue 3 the Workers tests run turns of, in ns.
func workersTask(ns string) *c4v1beta1.Task {
	return &c4v1beta1.Task{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: model.IssueTaskName("orders-api", 3, 1)},
		Spec: c4v1beta1.TaskSpec{ContainerRef: c4v1beta1.LocalRef{Name: "orders-api"},
			AgentRef: c4v1beta1.LocalRef{Name: "implementer"}, Issue: 3}}
}

// workersRun is the run a test's turn gets: the Agent the order names, on
// the Container orders-api, with no checkpoint volume.
func workersRun(task *c4v1beta1.Task, status *c4v1beta1.TaskStatus) sandboxBuild {
	return func(_ context.Context, order ports.WorkOrder, name string) (sandboxRun, error) {
		return sandboxRun{task: task, status: status, name: name, role: order.Role, goal: order.Goal,
			container: &c4v1beta1.Container{ObjectMeta: metav1.ObjectMeta{Namespace: task.Namespace, Name: "orders-api"}},
			agent: &c4v1beta1.Agent{ObjectMeta: metav1.ObjectMeta{Namespace: task.Namespace, Name: order.Agent},
				Spec: c4v1beta1.AgentSpec{Image: "theos/agent-go:dev"}},
			repoURL: "https://git.example/apps/orders-api.git", gitSecret: agentGitSecret("orders-api"),
			maxRounds: 3, checkpointClass: NoCheckpointClass}, nil
	}
}

// sandboxWorkersEnv is contract.WorkersEnv on the operator's Sandboxes: the
// Workers of a Task newTask makes, on r's client; play plays Agent Sandbox
// and the node, writing the turns' pods.
func sandboxWorkersEnv(r *TaskReconciler, play func() client.Client,
	newTask func(t *testing.T) *c4v1beta1.Task) contract.WorkersEnv {
	var w *sandboxWorkers
	return contract.WorkersEnv{
		New: func(t *testing.T) (ports.Workers, string, int) {
			task := newTask(t)
			status := task.Status.DeepCopy()
			w = r.workers(task, status, workersRun(task, status))
			return w, task.Name, int(task.Spec.Issue)
		},
		// The session that answers is theos-agent: its pod ends with the
		// result as its termination message.
		Answer: func(t *testing.T, _ string, round int, role string, result model.RunResult) {
			message, err := json.Marshal(agentrunner.Result{Role: role, Commit: result.Commit,
				Summary: result.Summary, Verdict: result.Verdict})
			if err != nil {
				t.Fatal(err)
			}
			playPod(t, play(), w.task.Namespace, w.name(round, role), corev1.PodStatus{Phase: corev1.PodSucceeded,
				ContainerStatuses: []corev1.ContainerStatus{{Name: agentContainer, Image: "agent",
					State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Completed",
						Message: string(message)}}}}})
		},
		// A claim that runs out is a pod that goes away with no answer: its
		// node drained it.
		Lapse: func(t *testing.T, _ string, round int, role string) {
			playPod(t, play(), w.task.Namespace, w.name(round, role),
				corev1.PodStatus{Phase: corev1.PodFailed, Reason: "Evicted"})
		},
	}
}

// playPod plays Agent Sandbox and the kubelet: the pod of the Sandbox
// called name, owned by it, with status.
func playPod(t *testing.T, c client.Client, ns, name string, status corev1.PodStatus) {
	t.Helper()
	ctx := context.Background()
	sandbox := newSandbox()
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, sandbox); err != nil {
		t.Fatalf("the sandbox of the turn: %v", err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name,
		OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(sandbox, SandboxGVK)}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: agentContainer, Image: "agent"}}}}
	if err := c.Create(ctx, pod); err != nil {
		t.Fatal(err)
	}
	pod.Status = status
	if err := c.Status().Update(ctx, pod); err != nil {
		t.Fatal(err)
	}
}

// workersClient is controller-runtime's fake client with the API server's
// UIDs, which tell a sandbox's pod from an earlier sandbox's of its name.
func workersClient(t *testing.T) client.Client {
	t.Helper()
	uids := 0
	return fake.NewClientBuilder().WithScheme(checkpointScheme(t)).
		WithStatusSubresource(&c4v1beta1.Task{}, &corev1.Pod{}).
		WithInterceptorFuncs(interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object,
			opts ...client.CreateOption) error {
			if obj.GetUID() == "" {
				uids++
				obj.SetUID(types.UID(fmt.Sprintf("uid-%d", uids)))
			}
			return c.Create(ctx, obj, opts...)
		}}).Build()
}

// #336: the operator's Workers, a turn a Sandbox, keep the contract
// internal/engine relies on, as fake.Workers and hostrun's do: here on
// controller-runtime's fake client, and in envtest under the operator's role
// (task_workers_test.go).
func TestSandboxWorkersContract336(t *testing.T) {
	var c client.Client
	r := &TaskReconciler{Scheme: checkpointScheme(t)}
	contract.Workers(t, sandboxWorkersEnv(r, func() client.Client { return c }, func(t *testing.T) *c4v1beta1.Task {
		c = workersClient(t)
		r.Client, r.APIReader = c, c
		task := workersTask("apps")
		if err := c.Create(context.Background(), task); err != nil {
			t.Fatal(err)
		}
		return task
	}))
}

// workersOn returns the Workers of a Task made on a fake client, for the
// adapter's own cases.
func workersOn(t *testing.T) (*sandboxWorkers, client.Client) {
	t.Helper()
	c := workersClient(t)
	task := workersTask("apps")
	if err := c.Create(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	r := &TaskReconciler{Client: c, APIReader: c, Scheme: checkpointScheme(t)}
	status := task.Status.DeepCopy()
	return r.workers(task, status, workersRun(task, status)), c
}

// implementTurn is the implementer's turn in round 1 of the workers' Task.
func implementTurn(w *sandboxWorkers) ports.WorkOrder {
	return ports.WorkOrder{Task: w.task.Name, Issue: 3, Branch: model.TaskBranch(w.task.Name), Round: 1,
		Role: model.RoleImplementer, Agent: "implementer"}
}

// sandboxGone reports whether the Sandbox called name is gone.
func sandboxGone(t *testing.T, c client.Client, name string) bool {
	t.Helper()
	err := c.Get(context.Background(), client.ObjectKey{Namespace: "apps", Name: name}, newSandbox())
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatal(err)
	}
	return apierrors.IsNotFound(err)
}

// #336: a Sandbox is named as the reconciler names it, from the Task's
// counters (sandboxName), so a turn offered again after a rerun is a
// Sandbox of its own, and the Task's status names the current one.
func TestSandboxWorkersNameTheTurnAsTheReconciler336(t *testing.T) {
	w, c := workersOn(t)
	ctx := context.Background()
	order := implementTurn(w)
	if err := w.Offer(ctx, order); err != nil {
		t.Fatal(err)
	}
	first := w.status.Sandbox
	if want := sandboxName(w.task, &c4v1beta1.TaskStatus{Round: 1}, model.RoleImplementer); first != want {
		t.Fatalf("the offered turn's sandbox = %q, want %q", first, want)
	}
	w.status.Reruns = 1
	if err := w.Offer(ctx, order); err != nil {
		t.Fatal(err)
	}
	rerun := sandboxName(w.task, &c4v1beta1.TaskStatus{Round: 1, Reruns: 1}, model.RoleImplementer)
	if w.status.Sandbox != rerun {
		t.Fatalf("the rerun's sandbox = %q, want %q", w.status.Sandbox, rerun)
	}
	if sandboxGone(t, c, first) || sandboxGone(t, c, w.status.Sandbox) {
		t.Errorf("sandboxes %s and %s, want both made", first, w.status.Sandbox)
	}
	if err := w.Offer(ctx, order); err != nil {
		t.Fatal(err)
	}
	sandboxes := &unstructured.UnstructuredList{}
	sandboxes.SetGroupVersionKind(SandboxGVK.GroupVersion().WithKind("SandboxList"))
	if err := c.List(ctx, sandboxes); err != nil || len(sandboxes.Items) != 2 {
		t.Errorf("sandboxes = %d (%v), want 2: a repeated offer makes none", len(sandboxes.Items), err)
	}
}

// #336 (#922): ending the current run counts and records it in the status
// as endRun does, and its Sandbox goes only once that status is written:
// in a reconcile, when writeStatus drops the ended runs.
func TestSandboxWorkersDeleteTheSandboxAfterTheStatus336(t *testing.T) {
	w, c := workersOn(t)
	ctx := withEndedRuns(context.Background())
	if err := w.Offer(ctx, implementTurn(w)); err != nil {
		t.Fatal(err)
	}
	name := w.status.Sandbox
	message, _ := json.Marshal(agentrunner.Result{Role: model.RoleImplementer, Commit: "abc1234",
		Summary: "Added the orders.", CostMilliUSD: new(int64(1200))})
	playPod(t, c, "apps", name, corev1.PodStatus{Phase: corev1.PodSucceeded,
		ContainerStatuses: []corev1.ContainerStatus{{Name: agentContainer, Image: "agent",
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Completed",
				Message: string(message)}}}}})
	run, err := w.Run(ctx, w.task.Name, 1, model.RoleImplementer)
	if err != nil {
		t.Fatal(err)
	}
	if !run.Done || run.Result == nil || run.Result.Commit != "abc1234" || w.status.SandboxPod != run.Pod ||
		run.Pod == "" {
		t.Fatalf("the answered run = %+v, status pod %q; want done with its commit, its pod kept", run,
			w.status.SandboxPod)
	}
	if err := w.End(ctx, w.task.Name, 1, model.RoleImplementer, "done"); err != nil {
		t.Fatal(err)
	}
	if w.status.Sandbox != "" || len(w.status.Runs) != 1 || w.status.CostMilliUSD != 1200 {
		t.Fatalf("status after the end = sandbox %q, runs %+v, cost %d; want no sandbox, the run recorded and "+
			"its cost counted", w.status.Sandbox, w.status.Runs, w.status.CostMilliUSD)
	}
	if sandboxGone(t, c, name) {
		t.Fatal("the sandbox went before the status that records its run was written")
	}
	if err := w.r.dropEndedRuns(ctx, w.task); err != nil {
		t.Fatal(err)
	}
	if !sandboxGone(t, c, name) {
		t.Error("the sandbox is there after the status write")
	}
}

// #336: a run ended for its claim keeps its Sandbox, marked and suspended,
// so no pod runs it on and it reads back interrupted; the next end of the
// turn deletes it, and an offer of it starts afresh.
func TestSandboxWorkersKeepARunEndedForItsClaim336(t *testing.T) {
	w, c := workersOn(t)
	ctx := context.Background()
	if err := w.Offer(ctx, implementTurn(w)); err != nil {
		t.Fatal(err)
	}
	name := w.status.Sandbox
	if err := w.End(ctx, w.task.Name, 1, model.RoleImplementer, model.EndReleased); err != nil {
		t.Fatal(err)
	}
	sandbox := newSandbox()
	if err := c.Get(ctx, client.ObjectKey{Namespace: "apps", Name: name}, sandbox); err != nil {
		t.Fatal(err)
	}
	if !sandboxSuspended(sandbox) || sandbox.GetAnnotations()[annotationEndedRun] != model.EndReleased {
		t.Fatalf("the sandbox of a run ended for its claim = %v, want suspended and marked released", sandbox.Object)
	}
	run, err := w.Run(ctx, w.task.Name, 1, model.RoleImplementer)
	if err != nil || !run.Done || !run.Interrupted || run.Suspended {
		t.Fatalf("the run ended for its claim = %+v, %v; want done and interrupted", run, err)
	}
	if w.status.Sandbox != "" {
		t.Errorf("status sandbox = %q, want none: the run ended", w.status.Sandbox)
	}
	if err := w.End(ctx, w.task.Name, 1, model.RoleImplementer, "done"); err != nil {
		t.Fatal(err)
	}
	if !sandboxGone(t, c, name) {
		t.Error("the sandbox of the ended run is there after the turn's next end")
	}
}

// #336: the Workers of one Task's reconcile run that Task's turns alone.
func TestSandboxWorkersRunTheirTaskAlone336(t *testing.T) {
	w, _ := workersOn(t)
	ctx := context.Background()
	order := implementTurn(w)
	order.Task = "orders-api-4-1"
	if err := w.Offer(ctx, order); err == nil {
		t.Error("Offer() of another Task's turn = nil, want an error")
	}
	if _, err := w.Run(ctx, order.Task, 1, model.RoleImplementer); err == nil {
		t.Error("Run() of another Task's turn = nil, want an error")
	}
	if err := w.End(ctx, order.Task, 1, model.RoleImplementer, "done"); err == nil {
		t.Error("End() of another Task's turn = nil, want an error")
	}
}
