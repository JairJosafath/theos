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
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// The pool's pod (#1140): registration in an init container that alone holds the
// token, no ServiceAccount token, never restarted, named after the pod so the
// job's runner_name finds it, the project's own Go cache when the label has one.
func TestBuildRunnerPod(t *testing.T) {
	in := runnerPodInput{namespace: "shop", provider: "gitea", gitURL: "http://gitea.gitea.svc.cluster.local:3000",
		tokenSecret: ciRunnerTokenSecret("gitea"),
		runner:      c4v1beta1.CIRunnerSpec{Label: "go", Image: "theos/gitea-runner-go:dev", Size: 1, GoCache: true},
		goCacheRoot: "/var/cache/theos/go/ci"}
	pod := buildRunnerPod(in)
	if pod.GenerateName != "ci-gitea-go-" || pod.Namespace != "shop" {
		t.Errorf("named %q in %q, want ci-gitea-go- in shop", pod.GenerateName, pod.Namespace)
	}
	for k, v := range map[string]string{LabelCIRunner: ciRunnerValue, LabelCIGitProvider: "gitea", LabelCILabel: "go",
		c4v1beta1.LabelProject: "shop"} {
		if pod.Labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, pod.Labels[k], v)
		}
	}
	if len(pod.Finalizers) != 1 || pod.Finalizers[0] != CIRunnerRecordFinalizer {
		t.Errorf("finalizers %v, want the record finalizer", pod.Finalizers)
	}
	if pod.Spec.RestartPolicy != corev1.RestartPolicyNever || pod.Spec.AutomountServiceAccountToken == nil ||
		*pod.Spec.AutomountServiceAccountToken {
		t.Errorf("restartPolicy %q, automount %v: want Never and false", pod.Spec.RestartPolicy, pod.Spec.AutomountServiceAccountToken)
	}
	env := map[string]corev1.EnvVar{}
	for _, c := range pod.Spec.Containers {
		for _, e := range c.Env {
			if e.Name == "RUNNER_TOKEN" {
				t.Errorf("container %s holds RUNNER_TOKEN; only the init container may", c.Name)
			}
		}
		if c.Name == ciRunnerContainer && !hasMount(c, "/etc/gitea-runner/job.env") {
			t.Error("the runner doesn't mount job.env, so jobs wouldn't use the cache")
		}
	}
	if len(pod.Spec.InitContainers) != 2 || pod.Spec.InitContainers[0].Name != "register" {
		t.Fatalf("init containers %v, want register and go-cache", pod.Spec.InitContainers)
	}
	for _, e := range pod.Spec.InitContainers[0].Env {
		env[e.Name] = e
	}
	if env["RUNNER_TOKEN"].ValueFrom == nil || env["RUNNER_TOKEN"].ValueFrom.SecretKeyRef.Name != "c4-ci-runner-gitea" {
		t.Errorf("RUNNER_TOKEN %+v, want from Secret c4-ci-runner-gitea", env["RUNNER_TOKEN"])
	}
	if env["RUNNER_EPHEMERAL"].Value != "1" || env["RUNNER_LABELS"].Value != "go:host" ||
		env["RUNNER_NAME"].ValueFrom == nil || env["RUNNER_NAME"].ValueFrom.FieldRef.FieldPath != "metadata.name" {
		t.Errorf("register env %+v: want RUNNER_EPHEMERAL=1, RUNNER_LABELS=go:host and RUNNER_NAME from metadata.name", env)
	}
}

// The Go cache is the project's own directory under --ci-go-cache; without the
// flag or the label's goCache, no cache and no job.env.
func TestBuildRunnerPodGoCache(t *testing.T) {
	in := runnerPodInput{namespace: "shop", provider: "gitea", tokenSecret: ciRunnerTokenSecret("gitea"),
		runner:      c4v1beta1.CIRunnerSpec{Label: "go", Image: "theos/gitea-runner-go:dev", GoCache: true},
		goCacheRoot: "/var/cache/theos/go/ci"}
	pod := buildRunnerPod(in)
	for _, v := range pod.Spec.Volumes {
		switch v.Name {
		case goCacheVolume:
			if v.HostPath == nil || v.HostPath.Path != "/var/cache/theos/go/ci/shop" {
				t.Errorf("go-cache volume %+v, want the project's directory under the root", v.VolumeSource)
			}
		case "work", "tmp":
			if v.EmptyDir == nil || v.EmptyDir.Medium != corev1.StorageMediumMemory {
				t.Errorf("%s volume %+v, want an emptyDir in memory", v.Name, v.VolumeSource)
			}
		}
	}

	in.runner.GoCache = false
	plain := buildRunnerPod(in)
	if len(plain.Spec.InitContainers) != 1 || len(plain.Spec.Volumes) != 3 || hasMount(plain.Spec.Containers[0], ciGoCacheMount) {
		t.Errorf("without goCache: %d init containers, %d volumes, want 1 and 3 with no cache mount",
			len(plain.Spec.InitContainers), len(plain.Spec.Volumes))
	}
	in.runner.GoCache, in.goCacheRoot = true, ""
	if noRoot := buildRunnerPod(in); hasMount(noRoot.Spec.Containers[0], ciGoCacheMount) {
		t.Error("with no --ci-go-cache the pod mounts a cache anyway")
	}
}

// The pool pod lists the shared pull Secrets as the sandbox pods do, and none
// without --shared-pull-secret: it runs as the namespace's default
// ServiceAccount, which names none, so private images never pulled (#2032).
func TestBuildRunnerPodSharedPullSecrets(t *testing.T) {
	for _, tt := range []struct {
		name    string
		secrets []string
		want    []corev1.LocalObjectReference
	}{
		{name: "none without --shared-pull-secret"},
		{name: "lists them (#2032)", secrets: []string{"ghcr", "quay"},
			want: []corev1.LocalObjectReference{{Name: "ghcr"}, {Name: "quay"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := runnerPodInput{namespace: "cel-lens", provider: "gitea", tokenSecret: ciRunnerTokenSecret("gitea"),
				runner:      c4v1beta1.CIRunnerSpec{Label: "go", Image: "ghcr.io/jairjosafath/theos/gitea-runner-go:0.30.0"},
				pullSecrets: tt.secrets}
			if got := buildRunnerPod(in).Spec.ImagePullSecrets; len(got)+len(tt.want) > 0 && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("imagePullSecrets %v, want %v", got, tt.want)
			}
		})
	}
}

// The go-release pod (#1141): a release builds signed artifacts, so its pod
// mounts no node directory, the project's cache least of all; its Go cache is
// an emptyDir of its own, seeded from the image's warm cache, which the jobs
// reach through the same job.env as the go pods'.
func TestBuildRunnerPodGoReleaseMountsNoNodeCache(t *testing.T) {
	in := runnerPodInput{namespace: "shop", provider: "gitea", tokenSecret: ciRunnerTokenSecret("gitea"),
		runner:      c4v1beta1.CIRunnerSpec{Label: "go-release", Image: "theos/gitea-runner-go:dev", FreshGoCache: true},
		goCacheRoot: "/var/cache/theos/go/projects"}
	pod := buildRunnerPod(in)
	if pod.GenerateName != "ci-gitea-go-release-" || pod.Labels[LabelCILabel] != "go-release" {
		t.Errorf("named %q, label %q: want ci-gitea-go-release- and go-release", pod.GenerateName, pod.Labels[LabelCILabel])
	}
	var cache *corev1.Volume
	for i, v := range pod.Spec.Volumes {
		if v.HostPath != nil {
			t.Errorf("volume %s mounts %s from the node; a release pod shares nothing", v.Name, v.HostPath.Path)
		}
		if v.Name == goCacheVolume {
			cache = &pod.Spec.Volumes[i]
		}
	}
	if cache == nil || cache.EmptyDir == nil || cache.EmptyDir.Medium != corev1.StorageMediumDefault {
		t.Fatalf("go-cache volume %+v, want an emptyDir on the node's disk", cache)
	}
	runner := pod.Spec.Containers[0]
	if !hasMount(runner, ciGoCacheMount) || !hasMount(runner, "/etc/gitea-runner/job.env") {
		t.Errorf("runner mounts %v, want the cache at %s and job.env", runner.VolumeMounts, ciGoCacheMount)
	}
	if len(pod.Spec.InitContainers) != 2 || pod.Spec.InitContainers[1].Name != goCacheVolume ||
		!reflect.DeepEqual(pod.Spec.InitContainers[1].Command, []string{"c4-go-cache", ciGoCacheMount}) {
		t.Errorf("init containers %v, want register and go-cache seeding %s from the image", pod.Spec.InitContainers,
			ciGoCacheMount)
	}

	// Without --ci-go-cache the release pod still gets its fresh cache: it
	// needs no node directory.
	in.goCacheRoot = ""
	if noRoot := buildRunnerPod(in); !hasMount(noRoot.Spec.Containers[0], ciGoCacheMount) {
		t.Error("with no --ci-go-cache the release pod has no Go cache")
	}
}

func hasMount(c corev1.Container, path string) bool {
	for _, m := range c.VolumeMounts {
		if m.MountPath == path {
			return true
		}
	}
	return false
}

// Who cancelled a job (#1261, folded into #1140): a pod whose runner ended on
// its own was not told to stop; one deleted, disrupted or killed before was.
func TestRunnerPodFate(t *testing.T) {
	now := metav1.NewTime(time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC))
	runner := func(exit int32) []corev1.ContainerStatus {
		return []corev1.ContainerStatus{{Name: ciRunnerContainer, State: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{ExitCode: exit}}}}
	}
	for _, tc := range []struct {
		name string
		pod  corev1.Pod
		want string
	}{
		{name: "still running", pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}}},
		{name: "pending", pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodPending}}},
		{name: "ended on its own", pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodSucceeded,
			ContainerStatuses: runner(0)}}, want: RunnerFateEnded},
		{name: "deleted before its job ended", pod: corev1.Pod{ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
			Status: corev1.PodStatus{Phase: corev1.PodRunning}}, want: RunnerFateLost},
		{name: "evicted", pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed, Reason: "Evicted"}},
			want: RunnerFateLost},
		{name: "a disruption target", pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.DisruptionTarget, Status: corev1.ConditionTrue}}}},
			want: RunnerFateLost},
		{name: "the runner killed", pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed,
			ContainerStatuses: runner(137)}}, want: RunnerFateLost},
	} {
		if got := runnerPodFate(&tc.pod); got != tc.want {
			t.Errorf("%s: runnerPodFate() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A pool pod that failed on its own names how, so the pool backs off from it;
// one that ended its job, still runs or is being deleted is no failure (#1699).
func TestRunnerPodFailure(t *testing.T) {
	now := metav1.NewTime(time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC))
	exited := func(name string, exit int32) []corev1.ContainerStatus {
		return []corev1.ContainerStatus{{Name: name, State: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{ExitCode: exit}}}}
	}
	for _, tc := range []struct {
		name string
		pod  corev1.Pod
		want string
	}{
		{name: "registration failed", pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed,
			InitContainerStatuses: exited("register", 1)}}, want: "register"},
		{name: "the runner failed", pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed,
			InitContainerStatuses: exited("register", 0), ContainerStatuses: exited(ciRunnerContainer, 2)}},
			want: ciRunnerContainer},
		{name: "evicted", pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed, Reason: "Evicted"}},
			want: "Evicted"},
		{name: "failed with no container status", pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed}},
			want: "Failed"},
		{name: "deleted while running", pod: corev1.Pod{ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
			Status: corev1.PodStatus{Phase: corev1.PodRunning}}},
		{name: "ended its job", pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodSucceeded,
			ContainerStatuses: exited(ciRunnerContainer, 0)}}},
		{name: "still running", pod: corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}}},
	} {
		if got := runnerPodFailure(&tc.pod); got != tc.want {
			t.Errorf("%s: runnerPodFailure() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The pool's wait doubles per failure in a row, capped at MaxRunnerBackoff (#1699).
func TestRunnerBackoff(t *testing.T) {
	for _, tc := range []struct {
		base time.Duration
		n    int
		want time.Duration
	}{
		{base: 10 * time.Second, n: 0, want: 0},
		{base: 10 * time.Second, n: 1, want: 10 * time.Second},
		{base: 10 * time.Second, n: 2, want: 20 * time.Second},
		{base: 10 * time.Second, n: 6, want: MaxRunnerBackoff},
		{base: 10 * time.Second, n: 100, want: MaxRunnerBackoff},
	} {
		if got := runnerBackoff(tc.base, tc.n); got != tc.want {
			t.Errorf("runnerBackoff(%v, %d) = %v, want %v", tc.base, tc.n, got, tc.want)
		}
	}
}

// The pool counts its failures since its last ended job, and waits from the
// newest (#1699).
func TestCIPoolBackoff(t *testing.T) {
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	for _, tc := range []struct {
		name string
		pool ciPool
		want time.Duration
	}{
		{name: "no failure", pool: ciPool{ended: ago(time.Minute)}, want: 0},
		{name: "one failure", pool: ciPool{failed: []time.Time{ago(4 * time.Second)}}, want: 6 * time.Second},
		{name: "two in a row, from the newest", pool: ciPool{failed: []time.Time{ago(time.Minute), ago(5 * time.Second)}},
			want: 15 * time.Second},
		{name: "a job ended since the first", pool: ciPool{failed: []time.Time{ago(time.Minute), ago(5 * time.Second)},
			ended: ago(30 * time.Second)}, want: 5 * time.Second},
		{name: "a job ended since the last", pool: ciPool{failed: []time.Time{ago(time.Minute)}, ended: ago(time.Second)},
			want: 0},
	} {
		if got := tc.pool.backoff(10*time.Second, now); got != tc.want {
			t.Errorf("%s: backoff() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Both fences share one egress: DNS, the Git hosts' namespaces, public HTTPS.
func TestFenceEgress(t *testing.T) {
	egress := fenceEgress(nil, []string{"gitea", "gitlab"})
	if len(egress) != 4 {
		t.Fatalf("%d rules, want DNS, public HTTPS and one per Git namespace", len(egress))
	}
	if egress[2].To[0].NamespaceSelector.MatchLabels[corev1.LabelMetadataName] != "gitea" ||
		egress[3].To[0].NamespaceSelector.MatchLabels[corev1.LabelMetadataName] != "gitlab" {
		t.Errorf("Git rules %+v, want gitea then gitlab", egress[2:])
	}
	if spec := fencePolicySpec(map[string]string{LabelCIRunner: ciRunnerValue}, fenceEgress(nil, nil)); len(spec.PolicyTypes) != 2 ||
		len(spec.Egress) != 2 || spec.PodSelector.MatchLabels[LabelCIRunner] != ciRunnerValue {
		t.Errorf("fencePolicySpec() = %+v, want ingress and egress types, two rules, the CI selector", spec)
	}
}

// TestFenceEgressDNSPeers: no DNS peer means the default, never every address,
// and the rule holds copies of the peers, so a policy edited in place leaves
// the reconciler's --cluster-dns alone (#1257, review).
func TestFenceEgressDNSPeers(t *testing.T) {
	want, _ := ClusterDNSPeers(nil)
	for _, dns := range [][]networkingv1.NetworkPolicyPeer{nil, {}} {
		if got := fenceEgress(dns, nil)[0].To; !reflect.DeepEqual(got, want) {
			t.Errorf("fenceEgress(%v).To = %+v, want the default %+v", dns, got, want)
		}
	}
	peers, _ := ClusterDNSPeers([]string{"dns/app=coredns"})
	fenceEgress(peers, nil)[0].To[0].PodSelector.MatchLabels["app"] = "changed"
	if peers[0].PodSelector.MatchLabels["app"] != "coredns" {
		t.Errorf("fenceEgress() shares the peers with its caller: %+v", peers[0].PodSelector.MatchLabels)
	}
}
