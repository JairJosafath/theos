/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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
		{name: "a sandbox with the volume", sandbox: sandboxWith(checkpointClaims(), ""), withPod: true,
			want: model.RunState{Started: true, Pod: "pod-1", Done: true, Failure: "Evicted", Interrupted: true,
				Checkpoint: true}},
		{name: "suspended, its pod still there", sandbox: sandboxWith(checkpointClaims(), "Suspended"), withPod: true,
			want: model.RunState{Started: true, Pod: "pod-1", Checkpoint: true, Suspended: true}},
		{name: "suspended, its pod gone", sandbox: sandboxWith(checkpointClaims(), "Suspended"),
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
