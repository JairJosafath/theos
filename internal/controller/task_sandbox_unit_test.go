package controller

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"

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
