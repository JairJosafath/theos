package controller

import (
	"context"
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
	"github.com/JairJosafath/c4factory/internal/model"
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
				task:      &c4v1alpha1.Task{ObjectMeta: objectMeta("apps", "add-crop")},
				status:    &c4v1alpha1.TaskStatus{Round: 1},
				container: &c4v1alpha1.Container{ObjectMeta: objectMeta("apps", "image-shrink")},
				agent:     &c4v1alpha1.Agent{ObjectMeta: objectMeta("apps", "claude")},
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

func TestBuildSandboxGoCache(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := c4v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name  string
		cache string
	}{
		{name: "none by default"},
		{name: "the node's shared cache when set", cache: "/var/cache/c4factory/go/sandbox"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			agent := &c4v1alpha1.Agent{ObjectMeta: objectMeta("apps", "claude")}
			agent.Spec.Image = "c4factory/agent-go:dev"
			agent.Spec.Env = []corev1.EnvVar{{Name: "GOCACHE", Value: "/agent/own"}}
			run := sandboxRun{
				task:      &c4v1alpha1.Task{ObjectMeta: objectMeta("apps", "add-crop")},
				status:    &c4v1alpha1.TaskStatus{Round: 1},
				container: &c4v1alpha1.Container{ObjectMeta: objectMeta("apps", "image-shrink")},
				agent:     agent,
				name:      "add-crop-r1-implement", role: "implement",
				goCache: tt.cache,
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
			var hostPaths []string
			for _, v := range pod.Volumes {
				if v.HostPath != nil {
					hostPaths = append(hostPaths, v.HostPath.Path)
				}
			}
			env := map[string][]string{}
			for _, e := range pod.Containers[0].Env {
				env[e.Name] = append(env[e.Name], e.Value)
			}
			if tt.cache == "" {
				if len(hostPaths) != 0 || len(pod.InitContainers) != 0 || len(pod.Containers[0].VolumeMounts) != 1 {
					t.Fatalf("host paths %v, init containers %d, mounts %v; want none", hostPaths,
						len(pod.InitContainers), pod.Containers[0].VolumeMounts)
				}
				return
			}
			if !reflect.DeepEqual(hostPaths, []string{tt.cache}) {
				t.Errorf("host paths %v, want only %q", hostPaths, tt.cache)
			}
			want := corev1.VolumeMount{Name: goCacheVolume, MountPath: goCacheMount}
			if mounts := pod.Containers[0].VolumeMounts; len(mounts) != 2 || !reflect.DeepEqual(mounts[1], want) {
				t.Errorf("agent mounts %v, want the workspace and %v", mounts, want)
			}
			// The Agent's own setting comes last, so it wins.
			if got := env["GOCACHE"]; !reflect.DeepEqual(got, []string{"/cache/go/build", "/agent/own"}) {
				t.Errorf("GOCACHE %v, want the shared cache, then the Agent's own", got)
			}
			if got := env["GOLANGCI_LINT_CACHE"]; !reflect.DeepEqual(got, []string{"/cache/go/golangci-lint"}) {
				t.Errorf("GOLANGCI_LINT_CACHE %v", got)
			}
			if len(pod.InitContainers) != 1 {
				t.Fatalf("init containers %v, want the cache's", pod.InitContainers)
			}
			init := pod.InitContainers[0]
			if init.Image != agent.Spec.Image || !reflect.DeepEqual(init.VolumeMounts, []corev1.VolumeMount{want}) ||
				!reflect.DeepEqual(init.Command, []string{"sh", "-c", goCachePrepShell}) {
				t.Errorf("init container %+v, want c4-go-cache in the agent's image with only the cache", init)
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
			sandbox.SetNamespace("apps")
			sandbox.SetName("add-crop-r1-implement")
			sandbox.SetUID("sandbox-2")
			sandbox.SetCreationTimestamp(created)
			owner := *metav1.NewControllerRef(sandbox, SandboxGVK)
			owner.UID = tt.podOwner
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "add-crop-r1-implement",
					CreationTimestamp: created, OwnerReferences: []metav1.OwnerReference{owner}},
				Status: corev1.PodStatus{Phase: corev1.PodSucceeded, ContainerStatuses: []corev1.ContainerStatus{{
					Name: "agent", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
						Reason: "Completed", Message: `{"role":"implement","commit":"sha-2"}`}},
				}}},
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sandbox, pod).Build()
			state, err := runState(context.Background(), c, "apps", "add-crop-r1-implement")
			if err != nil {
				t.Fatal(err)
			}
			if !state.Started || state.Done != tt.wantDone {
				t.Fatalf("started %v, done %v; want started, done %v", state.Started, state.Done, tt.wantDone)
			}
		})
	}
}

func TestRunStateTellsAnInterruptedRun(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	scheme.AddKnownTypeWithName(SandboxGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(SandboxGVK.GroupVersion().WithKind("SandboxList"), &unstructured.UnstructuredList{})
	disrupted := []corev1.PodCondition{{Type: corev1.DisruptionTarget, Status: corev1.ConditionTrue,
		Reason: "TerminationByKubelet"}}
	for _, tt := range []struct {
		name        string
		reason      string
		conditions  []corev1.PodCondition
		term        corev1.ContainerStateTerminated
		interrupted bool
	}{
		{name: "c4agent told to stop", term: corev1.ContainerStateTerminated{Reason: "Completed",
			Message: `{"role":"implement","error":"claude failed: signal: killed","interrupted":true}`},
			interrupted: true},
		{name: "the agent killed on its own", term: corev1.ContainerStateTerminated{Reason: "Completed",
			Message: `{"role":"implement","error":"claude failed: signal: killed"}`}},
		{name: "an evicted pod", reason: "Evicted", term: corev1.ContainerStateTerminated{Reason: "Error",
			ExitCode: 137}, interrupted: true},
		{name: "a pod ended by a node shutdown", conditions: disrupted,
			term: corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 143}, interrupted: true},
		{name: "a container lost across a node restart",
			term: corev1.ContainerStateTerminated{Reason: "ContainerStatusUnknown", ExitCode: 137}, interrupted: true},
		{name: "a container containerd lost across a node restart",
			term: corev1.ContainerStateTerminated{Reason: "Unknown", ExitCode: 255}, interrupted: true},
		{name: "c4agent stopped at the pod's deadline", reason: "DeadlineExceeded",
			term: corev1.ContainerStateTerminated{Reason: "Completed",
				Message: `{"role":"implement","error":"claude failed: signal: killed","interrupted":true}`}},
		{name: "an out-of-memory kill", term: corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137}},
		{name: "an out-of-memory kill, then evicted", reason: "Evicted",
			term: corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sandbox := newSandbox()
			sandbox.SetNamespace("apps")
			sandbox.SetName("add-crop-r1-implement")
			sandbox.SetUID("sandbox-1")
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "add-crop-r1-implement",
					OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(sandbox, SandboxGVK)}},
				Status: corev1.PodStatus{Phase: corev1.PodFailed, Reason: tt.reason, Conditions: tt.conditions,
					ContainerStatuses: []corev1.ContainerStatus{{
						Name: "agent", State: corev1.ContainerState{Terminated: &tt.term},
					}}},
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sandbox, pod).Build()
			state, err := runState(context.Background(), c, "apps", "add-crop-r1-implement")
			if err != nil {
				t.Fatal(err)
			}
			if !state.Done || state.Interrupted != tt.interrupted {
				t.Errorf("runState() = %+v, want done, interrupted %v", state, tt.interrupted)
			}
		})
	}
}

func TestRunStateTellsAClusterOutage(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	scheme.AddKnownTypeWithName(SandboxGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(SandboxGVK.GroupVersion().WithKind("SandboxList"), &unstructured.UnstructuredList{})
	shutdown := time.Date(2026, 10, 3, 9, 58, 49, 0, time.UTC)
	onTime, late := shutdown.Add(2*time.Second), shutdown.Add(7*time.Hour)
	lost := &corev1.ContainerStateTerminated{Reason: "ContainerStatusUnknown", ExitCode: 137}
	for _, tt := range []struct {
		name        string
		expired     time.Time
		reason      string
		term        *corev1.ContainerStateTerminated // nil: the pod is gone
		interrupted bool
		failure     string
	}{
		{name: "the pod gone at the shutdown time", expired: onTime,
			failure: "the sandbox reached its shutdown time"},
		// The cluster was down at the shutdown time; Agent Sandbox noticed it, and
		// deleted the pod, once the cluster was back.
		{name: "the pod gone in an outage past the shutdown time", expired: late, interrupted: true,
			failure: "the cluster was down at the sandbox's shutdown time"},
		{name: "a container lost at the shutdown time", expired: onTime, term: lost,
			failure: "ContainerStatusUnknown (exit code 137)"},
		{name: "a container lost in an outage past the shutdown time", expired: late, term: lost, interrupted: true,
			failure: "ContainerStatusUnknown (exit code 137)"},
		{name: "c4agent stopped by Agent Sandbox after an outage", expired: late,
			term: &corev1.ContainerStateTerminated{Reason: "Completed",
				Message: `{"role":"review","error":"claude failed: signal: terminated","interrupted":true}`},
			interrupted: true},
		{name: "the pod's deadline before an outage", expired: late, reason: "DeadlineExceeded",
			term:    &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 143},
			failure: "Error (exit code 143)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sandbox := newSandbox()
			sandbox.SetNamespace("apps")
			sandbox.SetName("add-crop-r1-review-a1")
			sandbox.SetUID("sandbox-1")
			sandbox.Object["spec"] = map[string]any{"shutdownTime": shutdown.Format(time.RFC3339)}
			sandbox.Object["status"] = map[string]any{"conditions": []any{map[string]any{
				"type": "Ready", "status": "False", "reason": "SandboxExpired",
				"lastTransitionTime": tt.expired.Format(time.RFC3339)}}}
			objects := []client.Object{sandbox}
			if tt.term != nil {
				objects = append(objects, &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "add-crop-r1-review-a1",
						OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(sandbox, SandboxGVK)}},
					Status: corev1.PodStatus{Phase: corev1.PodFailed, Reason: tt.reason,
						ContainerStatuses: []corev1.ContainerStatus{{
							Name: "agent", State: corev1.ContainerState{Terminated: tt.term},
						}}},
				})
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			state, err := runState(context.Background(), c, "apps", "add-crop-r1-review-a1")
			if err != nil {
				t.Fatal(err)
			}
			if !state.Done || state.Interrupted != tt.interrupted || tt.failure != "" && state.Failure != tt.failure {
				t.Errorf("runState() = %+v, want done, interrupted %v, failure %q", state, tt.interrupted, tt.failure)
			}
		})
	}
}

func TestRunStateTellsADeadlineAcrossAnOutage(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	scheme.AddKnownTypeWithName(SandboxGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(SandboxGVK.GroupVersion().WithKind("SandboxList"), &unstructured.UnstructuredList{})
	started := time.Date(2026, 10, 3, 8, 48, 49, 0, time.UTC)
	deadline := started.Add(time.Hour)
	stopped := `{"role":"implement","error":"claude failed: signal: terminated","interrupted":true}`
	for _, tt := range []struct {
		name        string
		term        corev1.ContainerStateTerminated
		interrupted bool
	}{
		{name: "c4agent stopped at the deadline", interrupted: false,
			term: corev1.ContainerStateTerminated{Reason: "Completed", Message: stopped,
				FinishedAt: metav1.NewTime(deadline.Add(3 * time.Second))}},
		{name: "killed after its grace period", interrupted: false,
			term: corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 137,
				FinishedAt: metav1.NewTime(deadline.Add(31 * time.Second))}},
		{name: "c4agent stopped when the node came back", interrupted: true,
			term: corev1.ContainerStateTerminated{Reason: "Completed", Message: stopped,
				FinishedAt: metav1.NewTime(deadline.Add(5 * time.Minute))}},
		{name: "died with the node before the deadline", interrupted: true,
			term: corev1.ContainerStateTerminated{Reason: "Unknown", ExitCode: 255,
				FinishedAt: metav1.NewTime(deadline.Add(-20 * time.Minute))}},
		{name: "lost by containerd, finished when the node came back", interrupted: true,
			term: corev1.ContainerStateTerminated{Reason: "Unknown", ExitCode: 255, Message: stopped,
				FinishedAt: metav1.NewTime(deadline.Add(76 * time.Second))}},
		{name: "lost by containerd without a result", interrupted: true,
			term: corev1.ContainerStateTerminated{Reason: "Unknown", ExitCode: 255,
				FinishedAt: metav1.NewTime(deadline.Add(76 * time.Second))}},
		{name: "a container lost across the node restart", interrupted: true,
			term: corev1.ContainerStateTerminated{Reason: "ContainerStatusUnknown", ExitCode: 137}},
		{name: "a result written before the outage stands", interrupted: false,
			term: corev1.ContainerStateTerminated{Reason: "Completed",
				Message:    `{"role":"implement","commit":"abc123","summary":"done"}`,
				FinishedAt: metav1.NewTime(deadline.Add(-20 * time.Minute))}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sandbox := newSandbox()
			sandbox.SetNamespace("apps")
			sandbox.SetName("add-crop-r1-implement")
			sandbox.SetUID("sandbox-1")
			seconds := int64(time.Hour / time.Second)
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "add-crop-r1-implement",
					OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(sandbox, SandboxGVK)}},
				Spec: corev1.PodSpec{ActiveDeadlineSeconds: &seconds},
				Status: corev1.PodStatus{Phase: corev1.PodFailed, Reason: "DeadlineExceeded",
					StartTime: &metav1.Time{Time: started},
					ContainerStatuses: []corev1.ContainerStatus{{
						Name: "agent", State: corev1.ContainerState{Terminated: &tt.term},
					}}},
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sandbox, pod).Build()
			state, err := runState(context.Background(), c, "apps", "add-crop-r1-implement")
			if err != nil {
				t.Fatal(err)
			}
			if !state.Done || state.Interrupted != tt.interrupted {
				t.Errorf("runState() = %+v, want done, interrupted %v", state, tt.interrupted)
			}
		})
	}
}

func TestBuildSandboxStandardChangeEnv(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := c4v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	gated := []string{"Makefile", ".gitea/"}
	for _, tt := range []struct {
		name    string
		role    string
		allowed []string
		resumed bool
		want    map[string]string // name to value; "" means absent
	}{
		{name: "a gated implementer", role: "implement", allowed: gated,
			want: map[string]string{agentrunner.EnvAllowedPaths: "Makefile,.gitea/", agentrunner.EnvResumed: ""}},
		{name: "a gated reviewer", role: "review", allowed: gated,
			want: map[string]string{agentrunner.EnvAllowedPaths: "Makefile,.gitea/", agentrunner.EnvResumed: ""}},
		{name: "a resumed implementer", role: "implement", resumed: true,
			want: map[string]string{agentrunner.EnvAllowedPaths: "", agentrunner.EnvResumed: "true"}},
		{name: "neither", role: "implement",
			want: map[string]string{agentrunner.EnvAllowedPaths: "", agentrunner.EnvResumed: ""}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			task := &c4v1alpha1.Task{ObjectMeta: objectMeta("apps", "add-crop")}
			task.Spec.Issue, task.Spec.AllowedProtectedPaths = 2, tt.allowed
			run := sandboxRun{
				task:      task,
				status:    &c4v1alpha1.TaskStatus{Round: 1},
				container: &c4v1alpha1.Container{ObjectMeta: objectMeta("apps", "image-shrink")},
				agent:     &c4v1alpha1.Agent{ObjectMeta: objectMeta("apps", "claude")},
				name:      "add-crop-r1-" + tt.role, role: tt.role,
				allowedPaths: task.Spec.AllowedProtectedPaths, resumed: tt.resumed,
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
			env := map[string]string{}
			for _, e := range pod.Containers[0].Env {
				env[e.Name] = e.Value
			}
			for name, want := range tt.want {
				got, ok := env[name]
				if want == "" && ok {
					t.Errorf("%s = %q, want it unset", name, got)
				}
				if want != "" && got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
		})
	}
}

func TestRunStateReadsTheProposal(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	scheme.AddKnownTypeWithName(SandboxGVK, &unstructured.Unstructured{})
	sandbox := newSandbox()
	sandbox.SetNamespace("apps")
	sandbox.SetName("add-crop-r1-implement")
	sandbox.SetUID("sandbox-1")
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "add-crop-r1-implement",
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(sandbox, SandboxGVK)}},
		Status: corev1.PodStatus{Phase: corev1.PodSucceeded, ContainerStatuses: []corev1.ContainerStatus{{
			Name: "agent", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Reason: "Completed", Message: `{"role":"implement","commit":"sha-1","empty":true,` +
					`"proposal":{"branch":"standard/add-crop","paths":["Makefile"],"reason":"make dist"}}`}},
		}}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sandbox, pod).Build()
	state, err := runState(context.Background(), c, "apps", "add-crop-r1-implement")
	if err != nil {
		t.Fatal(err)
	}
	want := &model.Proposal{Branch: "standard/add-crop", Paths: []string{"Makefile"}, Reason: "make dist"}
	if state.Result == nil || !reflect.DeepEqual(state.Result.Proposal, want) || !state.Result.Empty {
		t.Fatalf("result %+v; want proposal %+v and empty", state.Result, want)
	}
}
