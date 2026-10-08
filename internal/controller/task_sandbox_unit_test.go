package controller

import (
	"context"
	"reflect"
	"strings"
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
	"github.com/JairJosafath/c4factory/internal/gitprovider"
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
			state, err := runState(context.Background(), c, "apps", "add-crop-r1-implement", "")
			if err != nil {
				t.Fatal(err)
			}
			if !state.Started || state.Done != tt.wantDone {
				t.Fatalf("started %v, done %v; want started, done %v", state.Started, state.Done, tt.wantDone)
			}
		})
	}
}

func TestRunStateTellsAReplacedPod(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	scheme.AddKnownTypeWithName(SandboxGVK, &unstructured.Unstructured{})
	for _, tt := range []struct {
		name        string
		pod         types.UID // the pod under the sandbox now; empty for none
		startedOn   string
		interrupted bool
	}{
		{name: "the first pod the operator sees", pod: "pod-1"},
		{name: "the pod the run started on", pod: "pod-1", startedOn: "pod-1"},
		// A forced delete: Agent Sandbox starts the round over in a new pod.
		{name: "a replacement pod", pod: "pod-2", startedOn: "pod-1", interrupted: true},
		{name: "the pod gone, not yet replaced", startedOn: "pod-1", interrupted: true},
		{name: "no pod yet"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sandbox := newSandbox()
			sandbox.SetNamespace("apps")
			sandbox.SetName("add-crop-r1-implement")
			sandbox.SetUID("sandbox-1")
			objs := []client.Object{sandbox}
			if tt.pod != "" {
				objs = append(objs, &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "add-crop-r1-implement", UID: tt.pod,
						OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(sandbox, SandboxGVK)}},
					Status: corev1.PodStatus{Phase: corev1.PodRunning},
				})
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
			state, err := runState(context.Background(), c, "apps", "add-crop-r1-implement", tt.startedOn)
			if err != nil {
				t.Fatal(err)
			}
			if state.Pod != string(tt.pod) {
				t.Errorf("pod %q; want %q", state.Pod, tt.pod)
			}
			if state.Done != tt.interrupted || state.Interrupted != tt.interrupted || state.Result != nil {
				t.Errorf("runState() = %+v; want done and interrupted %v, no result", state, tt.interrupted)
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
			state, err := runState(context.Background(), c, "apps", "add-crop-r1-implement", "")
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
			state, err := runState(context.Background(), c, "apps", "add-crop-r1-review-a1", "")
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
			state, err := runState(context.Background(), c, "apps", "add-crop-r1-implement", "")
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
	state, err := runState(context.Background(), c, "apps", "add-crop-r1-implement", "")
	if err != nil {
		t.Fatal(err)
	}
	want := &model.Proposal{Branch: "standard/add-crop", Paths: []string{"Makefile"}, Reason: "make dist"}
	if state.Result == nil || !reflect.DeepEqual(state.Result.Proposal, want) || !state.Result.Empty {
		t.Fatalf("result %+v; want proposal %+v and empty", state.Result, want)
	}
}

// podOf reads the pod spec of a built Sandbox.
func podOf(t *testing.T, sandbox *unstructured.Unstructured) corev1.PodSpec {
	t.Helper()
	var pod corev1.PodSpec
	spec := sandbox.Object["spec"].(map[string]any)["podTemplate"].(map[string]any)["spec"].(map[string]any)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(spec, &pod); err != nil {
		t.Fatal(err)
	}
	return pod
}

func envNames(env []corev1.EnvVar) []string {
	names := make([]string, 0, len(env))
	for _, e := range env {
		names = append(names, e.Name)
	}
	return names
}

func TestBuildSandboxImplementEnv(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := c4v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	sandbox, err := buildSandbox(sandboxRun{
		task:      &c4v1alpha1.Task{ObjectMeta: objectMeta("apps", "add-crop")},
		status:    &c4v1alpha1.TaskStatus{Round: 2, Branch: "task/add-crop"},
		container: &c4v1alpha1.Container{ObjectMeta: objectMeta("apps", "image-shrink")},
		agent:     &c4v1alpha1.Agent{ObjectMeta: objectMeta("apps", "claude")},
		name:      "add-crop-r2-implement", role: model.RoleImplementer, gitSecret: "image-shrink-agent-git",
	}, scheme, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"C4_ROLE", "C4_REPO_URL", "C4_BRANCH", "C4_BASE", "C4_GOAL", "C4_CONTEXT", "C4_PROJECT",
		"C4_CONTAINER", "C4_TASK", "C4_ROUND", "C4_MAX_ROUNDS", "C4_HARNESS", "C4_PROTECTED_PATHS",
		"C4_GIT_USERNAME", "C4_GIT_TOKEN"}
	if got := envNames(podOf(t, sandbox).Containers[0].Env); !reflect.DeepEqual(got, want) {
		t.Errorf("env %v, want %v", got, want)
	}
	if got := sandbox.GetLabels(); got[c4v1alpha1.LabelContainer] != "image-shrink" || got[c4v1alpha1.LabelBrief] != "" {
		t.Errorf("labels %v, want the Container's and no Brief's", got)
	}
}

// The Go package registry comes from the GitProvider's URL and owner, in
// cluster or not, and goes to c4agent; the go command asks only c4agent's
// loopback proxy, never a list with the public proxy after the registry, which
// it would ask for a factory module the registry lacks. The Agent's own setting
// comes last, so it wins.
func TestBuildSandboxGoProxy(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := c4v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, url string
		own       []corev1.EnvVar
		want      []corev1.EnvVar
	}{
		{name: "an in-cluster Gitea", url: "http://gitea.gitea.svc.cluster.local:3000", want: []corev1.EnvVar{
			{Name: "C4_GO_REGISTRY", Value: "http://gitea.gitea.svc.cluster.local:3000/api/packages/apps/go"},
			{Name: "GOPROXY", Value: "http://127.0.0.1:3127,direct"},
			{Name: "GONOSUMDB", Value: "c4factory.local"},
		}},
		{name: "an external Gitea", url: "https://git.example.com/", want: []corev1.EnvVar{
			{Name: "C4_GO_REGISTRY", Value: "https://git.example.com/api/packages/apps/go"},
			{Name: "GOPROXY", Value: "http://127.0.0.1:3127,direct"},
			{Name: "GONOSUMDB", Value: "c4factory.local"},
		}},
		{name: "the Agent's own", url: "https://git.example.com", own: []corev1.EnvVar{{Name: "GOPROXY", Value: "off"}},
			want: []corev1.EnvVar{
				{Name: "C4_GO_REGISTRY", Value: "https://git.example.com/api/packages/apps/go"},
				{Name: "GOPROXY", Value: "http://127.0.0.1:3127,direct"},
				{Name: "GONOSUMDB", Value: "c4factory.local"},
				{Name: "GOPROXY", Value: "off"},
			}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := gitprovider.Client{}.Provider(gitprovider.Config{URL: tt.url, Owner: "apps",
				Credentials: gitprovider.Credentials{Token: "t"}})
			if err != nil {
				t.Fatal(err)
			}
			repo := provider.Repository("image-shrink")
			agent := &c4v1alpha1.Agent{ObjectMeta: objectMeta("apps", "claude")}
			agent.Spec.Env = tt.own
			sandbox, err := buildSandbox(sandboxRun{
				task:      &c4v1alpha1.Task{ObjectMeta: objectMeta("apps", "add-crop")},
				status:    &c4v1alpha1.TaskStatus{Round: 1, Branch: "task/add-crop"},
				container: &c4v1alpha1.Container{ObjectMeta: objectMeta("apps", "image-shrink")},
				agent:     agent, name: "add-crop-r1-implement", role: model.RoleImplementer,
				repoURL: repo.CloneURL, gitSecret: "image-shrink-agent-git", goProxy: repo.GoProxy,
			}, scheme, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			var got []corev1.EnvVar
			for _, e := range podOf(t, sandbox).Containers[0].Env {
				if strings.HasPrefix(e.Name, "GO") || e.Name == "C4_GO_REGISTRY" {
					got = append(got, e)
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Go env %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildSandboxPlanEnv(t *testing.T) {
	if model.RolePlanner != agentrunner.RolePlan {
		t.Fatalf("model.RolePlanner = %q, but c4agent plans as %q", model.RolePlanner, agentrunner.RolePlan)
	}
	scheme := runtime.NewScheme()
	if err := c4v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, goal, feedback string
		want                 []corev1.EnvVar
	}{
		{name: "a first try", want: []corev1.EnvVar{
			{Name: "C4_ROLE", Value: "plan"},
			{Name: "C4_REPO_URL", Value: "http://gitea/apps/brief-cel-lens.git"},
			{Name: "C4_BRANCH", Value: "plan/4"},
			{Name: "C4_BASE", Value: "main"},
			{Name: "C4_BRIEF", Value: "cel-lens"},
			{Name: "C4_PROJECT", Value: "apps"},
			{Name: "C4_TASK", Value: "cel-lens-plan-4"},
			{Name: "C4_ROUND", Value: "4"},
			{Name: "C4_MAX_ROUNDS", Value: "3"},
			{Name: "C4_HARNESS", Value: "stand-in"},
			secretEnv("C4_GIT_USERNAME", "brief-cel-lens-git", "username"),
			secretEnv("C4_GIT_TOKEN", "brief-cel-lens-git", "token"),
			{Name: "C4_MODEL", Value: "opus"},
			{Name: "C4AGENT_SESSION_TOKEN_LIMIT", Value: "130000"},
		}},
		{name: "a try after a refusal, with a goal", goal: "Keep it small.", feedback: "The Brief refused the plan:\nx",
			want: []corev1.EnvVar{
				{Name: "C4_ROLE", Value: "plan"},
				{Name: "C4_REPO_URL", Value: "http://gitea/apps/brief-cel-lens.git"},
				{Name: "C4_BRANCH", Value: "plan/4"},
				{Name: "C4_BASE", Value: "main"},
				{Name: "C4_BRIEF", Value: "cel-lens"},
				{Name: "C4_GOAL", Value: "Keep it small."},
				{Name: "C4_PROJECT", Value: "apps"},
				{Name: "C4_TASK", Value: "cel-lens-plan-4"},
				{Name: "C4_ROUND", Value: "4"},
				{Name: "C4_MAX_ROUNDS", Value: "3"},
				{Name: "C4_HARNESS", Value: "stand-in"},
				secretEnv("C4_GIT_USERNAME", "brief-cel-lens-git", "username"),
				secretEnv("C4_GIT_TOKEN", "brief-cel-lens-git", "token"),
				{Name: "C4_MODEL", Value: "opus"},
				{Name: "C4AGENT_SESSION_TOKEN_LIMIT", Value: "130000"},
				{Name: "C4_FEEDBACK", Value: "The Brief refused the plan:\nx"},
			}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			task := &c4v1alpha1.Task{ObjectMeta: objectMeta("apps", "cel-lens-plan-4")}
			task.Spec.Goal = tt.goal
			agent := &c4v1alpha1.Agent{ObjectMeta: objectMeta("apps", "planner")}
			agent.Spec.Harness, agent.Spec.Model = "stand-in", "opus"
			agent.Spec.SessionTokenLimit = 130000
			sandbox, err := buildSandbox(sandboxRun{
				task:   task,
				status: &c4v1alpha1.TaskStatus{Round: 2, Branch: "plan/4", Feedback: tt.feedback},
				agent:  agent, name: "cel-lens-plan-4-r2-plan", role: model.RolePlanner,
				repoURL: "http://gitea/apps/brief-cel-lens.git", gitSecret: "brief-cel-lens-git", goal: tt.goal,
				maxRounds: 3, brief: "cel-lens", briefRound: 4,
			}, scheme, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if got := podOf(t, sandbox).Containers[0].Env; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("env\n%v\nwant\n%v", got, tt.want)
			}
			want := map[string]string{LabelTask: "cel-lens-plan-4", c4v1alpha1.LabelProject: "apps",
				LabelAgentSandbox: "true", c4v1alpha1.LabelBrief: "cel-lens"}
			if got := sandbox.GetLabels(); !reflect.DeepEqual(got, want) {
				t.Errorf("labels %v, want %v", got, want)
			}
		})
	}
}

func TestBuildSandboxSharedPullSecrets(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := c4v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	implement := sandboxRun{
		task:      &c4v1alpha1.Task{ObjectMeta: objectMeta("apps", "add-crop")},
		status:    &c4v1alpha1.TaskStatus{Round: 1},
		container: &c4v1alpha1.Container{ObjectMeta: objectMeta("apps", "image-shrink")},
		agent:     &c4v1alpha1.Agent{ObjectMeta: objectMeta("apps", "claude")},
		name:      "add-crop-r1-implement", role: model.RoleImplementer,
	}
	plan := sandboxRun{
		task:   &c4v1alpha1.Task{ObjectMeta: objectMeta("apps", "plan-shop")},
		status: &c4v1alpha1.TaskStatus{Round: 1},
		agent:  &c4v1alpha1.Agent{ObjectMeta: objectMeta("apps", "planner")},
		name:   "plan-shop-r1-plan", role: model.RolePlanner, brief: "shop", briefRound: 1,
	}
	for _, tt := range []struct {
		name    string
		run     sandboxRun
		secrets []string
		want    []corev1.LocalObjectReference
	}{
		{name: "none without --shared-pull-secret", run: implement},
		{name: "an implement run lists them", run: implement, secrets: []string{"ghcr", "quay"},
			want: []corev1.LocalObjectReference{{Name: "ghcr"}, {Name: "quay"}}},
		{name: "a plan run lists them", run: plan, secrets: []string{"ghcr"},
			want: []corev1.LocalObjectReference{{Name: "ghcr"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			run := tt.run
			run.pullSecrets = tt.secrets
			sandbox, err := buildSandbox(run, scheme, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if got := podOf(t, sandbox).ImagePullSecrets; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("imagePullSecrets %v, want %v", got, tt.want)
			}
		})
	}
}
