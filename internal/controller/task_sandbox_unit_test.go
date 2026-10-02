package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

func TestBuildSandboxWorkspace(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := c4v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	size := resource.MustParse("3Gi")
	for _, tt := range []struct {
		name       string
		memory     *resource.Quantity
		wantMedium corev1.StorageMedium
	}{
		{name: "on disk by default", wantMedium: corev1.StorageMediumDefault},
		{name: "in memory when asked, capped", memory: &size, wantMedium: corev1.StorageMediumMemory},
	} {
		t.Run(tt.name, func(t *testing.T) {
			run := sandboxRun{
				task:      &c4v1alpha1.Task{ObjectMeta: objectMeta("toolbox", "add-crop")},
				status:    &c4v1alpha1.TaskStatus{Round: 1},
				container: &c4v1alpha1.Container{ObjectMeta: objectMeta("toolbox", "image-shrink")},
				agent:     &c4v1alpha1.Agent{ObjectMeta: objectMeta("toolbox", "claude")},
				name:      "add-crop-r1-implement", role: "implement",
				workspaceMemory: tt.memory,
			}
			sandbox, err := buildSandbox(run, scheme, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			var pod corev1.PodSpec
			spec := sandbox.Object["spec"].(map[string]any)["podTemplate"].(map[string]any)["spec"].(map[string]any)
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(spec, &pod); err != nil {
				t.Fatal(err)
			}
			dir := pod.Volumes[0].EmptyDir
			if dir == nil || dir.Medium != tt.wantMedium {
				t.Fatalf("workspace volume %+v, want medium %q", pod.Volumes[0], tt.wantMedium)
			}
			if tt.memory != nil && (dir.SizeLimit == nil || !dir.SizeLimit.Equal(*tt.memory)) {
				t.Errorf("size limit %v, want %v", dir.SizeLimit, tt.memory)
			}
		})
	}
}

func TestRunStateReadsOnlyItsSandboxsPod(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	scheme.AddKnownTypeWithName(SandboxGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(SandboxGVK.GroupVersion().WithKind("SandboxList"), &unstructured.UnstructuredList{})
	created := metav1.NewTime(time.Now().Truncate(time.Second))
	for _, tt := range []struct {
		name     string
		podOwner types.UID
		wantDone bool
	}{
		{name: "the pod this sandbox started", podOwner: "sandbox-2", wantDone: true},
		// A conflict round starts a sandbox with the name of an earlier one, maybe
		// in the same second; its pod is not collected yet.
		{name: "the pod of an earlier sandbox with this name", podOwner: "sandbox-1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sandbox := newSandbox()
			sandbox.SetNamespace("toolbox")
			sandbox.SetName("add-crop-r1-implement")
			sandbox.SetUID("sandbox-2")
			sandbox.SetCreationTimestamp(created)
			owner := *metav1.NewControllerRef(sandbox, SandboxGVK)
			owner.UID = tt.podOwner
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "toolbox", Name: "add-crop-r1-implement",
					CreationTimestamp: created, OwnerReferences: []metav1.OwnerReference{owner}},
				Status: corev1.PodStatus{Phase: corev1.PodSucceeded, ContainerStatuses: []corev1.ContainerStatus{{
					Name: "agent", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
						Reason: "Completed", Message: `{"role":"implement","commit":"sha-2"}`}},
				}}},
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sandbox, pod).Build()
			state, err := runState(context.Background(), c, "toolbox", "add-crop-r1-implement")
			if err != nil {
				t.Fatal(err)
			}
			if !state.Started || state.Done != tt.wantDone {
				t.Fatalf("started %v, done %v; want started, done %v", state.Started, state.Done, tt.wantDone)
			}
		})
	}
}
