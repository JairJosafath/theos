/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
	"github.com/JairJosafath/c4factory/internal/model"
)

// Step 11 of docs/plans/self-hosting.md: every sandbox has the checkpoint
// volume, mounted where c4agent saves the run, and gives c4agent a minute to
// save it when told to stop.
func TestBuildSandboxHasTheCheckpointVolume(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := c4v1beta1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	sandbox, err := buildSandbox(sandboxRun{
		task:      &c4v1beta1.Task{ObjectMeta: objectMeta("apps", "add-crop")},
		status:    &c4v1beta1.TaskStatus{Round: 1, Branch: "task/add-crop"},
		container: &c4v1beta1.Container{ObjectMeta: objectMeta("apps", "image-shrink")},
		agent:     &c4v1beta1.Agent{ObjectMeta: objectMeta("apps", "claude")},
		name:      "add-crop-r1-implement", role: model.RoleImplementer,
	}, scheme, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheckpoint(sandbox) {
		t.Errorf("volumeClaimTemplates = %v, want the checkpoint claim", sandbox.Object["spec"].(map[string]any)["volumeClaimTemplates"])
	}
	claims, _, _ := unstructured.NestedSlice(sandbox.Object, "spec", "volumeClaimTemplates")
	size, _, _ := unstructured.NestedString(claims[0].(map[string]any), "spec", "resources", "requests", "storage")
	if size != "1Gi" {
		t.Errorf("checkpoint claim requests %q, want 1Gi", size)
	}
	pod := podOf(t, sandbox)
	if pod.TerminationGracePeriodSeconds == nil || *pod.TerminationGracePeriodSeconds != 60 {
		t.Errorf("terminationGracePeriodSeconds = %v, want 60", pod.TerminationGracePeriodSeconds)
	}
	mounted := false
	for _, m := range pod.Containers[0].VolumeMounts {
		mounted = mounted || m.Name == checkpointVolume && m.MountPath == agentrunner.CheckpointDir
	}
	env := map[string]string{}
	for _, e := range pod.Containers[0].Env {
		env[e.Name] = e.Value
	}
	if !mounted || env[agentrunner.EnvCheckpointDir] != agentrunner.CheckpointDir {
		t.Errorf("mounts %v, %s=%q; want the checkpoint volume at %s, and c4agent told so", pod.Containers[0].VolumeMounts,
			agentrunner.EnvCheckpointDir, env[agentrunner.EnvCheckpointDir], agentrunner.CheckpointDir)
	}
	for _, v := range pod.Volumes {
		if v.Name == checkpointVolume {
			t.Errorf("volume %v in the pod template; Agent Sandbox adds it from the claim template", v)
		}
	}
}

// The review of step 11: the checkpoint volume's StorageClass is a setting
// (--sandbox-checkpoint-storage-class). Empty is the cluster's default class,
// a name that class, and none gives the sandbox no volume, so a cluster
// without a default class still schedules sandbox pods.
func TestBuildSandboxCheckpointClass(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := c4v1beta1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		class, wantClass string
		wantVolume       bool
	}{
		{class: "", wantVolume: true},
		{class: "fast-ssd", wantClass: "fast-ssd", wantVolume: true},
		{class: NoCheckpointClass},
	} {
		t.Run("class "+tt.class, func(t *testing.T) {
			sandbox, err := buildSandbox(sandboxRun{
				task:      &c4v1beta1.Task{ObjectMeta: objectMeta("apps", "add-crop")},
				status:    &c4v1beta1.TaskStatus{Round: 1, Branch: "task/add-crop"},
				container: &c4v1beta1.Container{ObjectMeta: objectMeta("apps", "image-shrink")},
				agent:     &c4v1beta1.Agent{ObjectMeta: objectMeta("apps", "claude")},
				name:      "add-crop-r1-implement", role: model.RoleImplementer, checkpointClass: tt.class,
			}, scheme, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if hasCheckpoint(sandbox) != tt.wantVolume {
				t.Fatalf("hasCheckpoint() = %v, want %v", hasCheckpoint(sandbox), tt.wantVolume)
			}
			pod := podOf(t, sandbox)
			mounted, env := false, false
			for _, m := range pod.Containers[0].VolumeMounts {
				mounted = mounted || m.Name == checkpointVolume
			}
			for _, e := range pod.Containers[0].Env {
				env = env || e.Name == agentrunner.EnvCheckpointDir
			}
			if mounted != tt.wantVolume || env != tt.wantVolume {
				t.Errorf("checkpoint mounted %v, %s set %v; want both %v", mounted, agentrunner.EnvCheckpointDir,
					env, tt.wantVolume)
			}
			if !tt.wantVolume {
				return
			}
			claims, _, _ := unstructured.NestedSlice(sandbox.Object, "spec", "volumeClaimTemplates")
			class, found, _ := unstructured.NestedString(claims[0].(map[string]any), "spec", "storageClassName")
			if class != tt.wantClass || found != (tt.wantClass != "") {
				t.Errorf("storageClassName = %q (set %v), want %q", class, found, tt.wantClass)
			}
		})
	}
}

func TestRunStateTellsACheckpointAndASuspension(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	scheme.AddKnownTypeWithName(SandboxGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(SandboxGVK.GroupVersion().WithKind("SandboxList"), &unstructured.UnstructuredList{})
	const name = "add-crop-r1-implement"
	sandboxWith := func(claims []any, mode string) *unstructured.Unstructured {
		sandbox := newSandbox()
		sandbox.SetNamespace("apps")
		sandbox.SetName(name)
		sandbox.SetUID("sandbox-1")
		sandbox.Object["spec"] = map[string]any{"podTemplate": map[string]any{"spec": map[string]any{}}}
		if claims != nil {
			_ = unstructured.SetNestedSlice(sandbox.Object, claims, "spec", "volumeClaimTemplates")
		}
		if mode != "" {
			_ = unstructured.SetNestedField(sandbox.Object, mode, "spec", "operatingMode")
		}
		return sandbox
	}
	podOn := func(sandbox *unstructured.Unstructured) *corev1.Pod {
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: name, UID: "pod-1",
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(sandbox, SandboxGVK)}},
			Status: corev1.PodStatus{Phase: corev1.PodFailed, Reason: "Evicted"}}
	}
	for _, tt := range []struct {
		name    string
		sandbox *unstructured.Unstructured
		withPod bool
		want    model.RunState
	}{
		{name: "a sandbox made before checkpoints", sandbox: sandboxWith(nil, ""), withPod: true,
			want: model.RunState{Started: true, Pod: "pod-1", Done: true, Failure: "Evicted", Interrupted: true}},
		{name: "a sandbox with the volume", sandbox: sandboxWith(checkpointClaims(""), ""), withPod: true,
			want: model.RunState{Started: true, Pod: "pod-1", Done: true, Failure: "Evicted", Interrupted: true,
				Checkpoint: true}},
		{name: "suspended, its pod still there", sandbox: sandboxWith(checkpointClaims(""), "Suspended"), withPod: true,
			want: model.RunState{Started: true, Pod: "pod-1", Checkpoint: true, Suspended: true}},
		{name: "suspended, its pod gone", sandbox: sandboxWith(checkpointClaims(""), "Suspended"),
			want: model.RunState{Started: true, Checkpoint: true, Suspended: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{tt.sandbox}
			if tt.withPod {
				objects = append(objects, podOn(tt.sandbox))
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			state, err := runState(context.Background(), c, "apps", name, "pod-1")
			if err != nil {
				t.Fatal(err)
			}
			if state.Started != tt.want.Started || state.Pod != tt.want.Pod || state.Done != tt.want.Done ||
				state.Interrupted != tt.want.Interrupted || state.Checkpoint != tt.want.Checkpoint ||
				state.Suspended != tt.want.Suspended {
				t.Errorf("runState() = %+v, want %+v", state, tt.want)
			}
		})
	}
}

// checkpointScheme knows Tasks, pods and Sandboxes.
func checkpointScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := c4v1beta1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	scheme.AddKnownTypeWithName(SandboxGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(SandboxGVK.GroupVersion().WithKind("SandboxList"), &unstructured.UnstructuredList{})
	return scheme
}

// interruptedSandbox is a running sandbox with the checkpoint volume and its
// pod, pod-1, evicted.
func interruptedSandbox(name string) (*unstructured.Unstructured, *corev1.Pod) {
	sandbox := newSandbox()
	sandbox.SetNamespace("apps")
	sandbox.SetName(name)
	sandbox.SetUID("sandbox-1")
	sandbox.Object["spec"] = map[string]any{"podTemplate": map[string]any{"spec": map[string]any{}},
		"volumeClaimTemplates": checkpointClaims(""), "operatingMode": operatingModeRunning}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: name, UID: "pod-1",
		OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(sandbox, SandboxGVK)}},
		Status: corev1.PodStatus{Phase: corev1.PodFailed, Reason: "Evicted"}}
	return sandbox, pod
}

// The review of step 11: when the suspend's patch failed after the status that
// counted the run was written, the next pass found the same evicted pod and
// counted its cost, its run record and the resume again.
func TestSuspendRunCountsARunOnce(t *testing.T) {
	const name = "add-crop-r1-implement"
	sandbox, pod := interruptedSandbox(name)
	failPatch := true
	c := fake.NewClientBuilder().WithScheme(checkpointScheme(t)).WithObjects(sandbox, pod).
		WithInterceptorFuncs(interceptor.Funcs{Patch: func(ctx context.Context, c client.WithWatch,
			obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if failPatch {
				return errors.New("the API server went away")
			}
			return c.Patch(ctx, obj, patch, opts...)
		}}).Build()
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	r := &TaskReconciler{Client: c, Now: func() time.Time { return now }}
	task := &c4v1beta1.Task{ObjectMeta: objectMeta("apps", "add-crop")}
	status := &c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskImplementing, Round: 1, Sandbox: name, SandboxPod: "pod-1"}
	step := model.TaskStep{Action: model.ActionSuspendRun, Message: "resumes in place"}

	if err := r.suspendRun(context.Background(), task, status, step, suspendInterrupted); err == nil {
		t.Fatal("suspendRun() = nil, want the failed patch's error")
	}
	failPatch = false
	if err := r.suspendRun(context.Background(), task, status, step, suspendInterrupted); err != nil {
		t.Fatal(err)
	}
	if status.Resumes != 1 || status.RunsWithoutCost != 1 || len(status.Runs) != 1 || status.CountedPod != "pod-1" {
		t.Errorf("after a failed suspend and its retry: resumes %d, runs without cost %d, %d run records, "+
			"counted pod %q; want 1, 1, 1 and pod-1", status.Resumes, status.RunsWithoutCost, len(status.Runs),
			status.CountedPod)
	}
	got := newSandbox()
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "apps", Name: name}, got); err != nil {
		t.Fatal(err)
	}
	if !sandboxSuspended(got) || got.GetAnnotations()[annotationSuspendedAt] != now.Format(time.RFC3339) {
		t.Errorf("sandbox spec %v, annotations %v; want Suspended at %s", got.Object["spec"], got.GetAnnotations(),
			now.Format(time.RFC3339))
	}

	// A run suspendRun counted is not counted again when it ends: its suspended
	// sandbox could not resume, and is rerun.
	if _, err := r.recordRun(context.Background(), task, status); err != nil {
		t.Fatal(err)
	}
	if status.RunsWithoutCost != 1 || len(status.Runs) != 1 || status.CountedPod != "" {
		t.Errorf("after the rerun's recordRun: runs without cost %d, %d run records, counted pod %q; "+
			"want 1, 1 and none", status.RunsWithoutCost, len(status.Runs), status.CountedPod)
	}
}

// The review of step 11: a suspended sandbox's run knows how long it has been
// suspended, so it waits model.MaxSuspendWait at most for its old pod to go.
func TestSuspendedRunKnowsWhenItWasSuspended(t *testing.T) {
	const name = "add-crop-r1-implement"
	sandbox, pod := interruptedSandbox(name)
	suspendedAt := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	_ = unstructured.SetNestedField(sandbox.Object, operatingModeSuspended, "spec", "operatingMode")
	sandbox.SetAnnotations(map[string]string{annotationSuspendedAt: suspendedAt.Format(time.RFC3339)})
	c := fake.NewClientBuilder().WithScheme(checkpointScheme(t)).WithObjects(sandbox, pod).Build()
	r := &TaskReconciler{Client: c, Now: func() time.Time { return suspendedAt.Add(11 * time.Minute) }}
	run, err := runState(context.Background(), c, "apps", name, "pod-1")
	if err != nil {
		t.Fatal(err)
	}
	run = r.withSuspendedFor(run)
	if !run.Suspended || run.Pod != "pod-1" || run.SuspendedFor != 11*time.Minute {
		t.Fatalf("run = %+v, want suspended 11m ago, its pod still there", run)
	}
	if step := model.NextTaskStep(model.TaskFacts{Phase: model.TaskImplementing, Round: 1, MaxRounds: 3,
		Resumes: 1, Run: run}); step.Action != model.ActionRerun {
		t.Errorf("NextTaskStep() = %+v, want a rerun past MaxSuspendWait", step)
	}
}

// #1996: a Task waiting for its agent looks again when the agent is due
// back, also when that time came while the pass ran: a requeue after zero
// would be none. One whose wait has a reason of its own (a pause, the cost
// cap) is woken by that.
func TestWaitForTheAgentLooksAgain(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	r := &TaskReconciler{Now: func() time.Time { return now }}
	task := &c4v1beta1.Task{ObjectMeta: objectMeta("apps", "add-crop")}
	tests := []struct {
		name string
		back time.Duration
		step model.TaskStep
		want time.Duration
	}{
		{"due back later", 10 * time.Minute, model.TaskStep{Action: model.ActionWait}, 10 * time.Minute},
		{"due back as the pass ran", -time.Second, model.TaskStep{Action: model.ActionWait}, agentBackRecheck},
		{"due back now", 0, model.TaskStep{Action: model.ActionWait}, agentBackRecheck},
		{"paused", -time.Second, model.TaskStep{Action: model.ActionWait, Reason: model.ReasonPaused}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskImplementing,
				AgentBackAt: &metav1.Time{Time: now.Add(tt.back)}}
			if got := r.wait(task, status, tt.step); got.RequeueAfter != tt.want {
				t.Errorf("wait() = %+v, want a requeue after %s", got, tt.want)
			}
		})
	}
}
