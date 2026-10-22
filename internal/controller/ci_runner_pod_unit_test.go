/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// The pool's pod (#1140): registration in an init container that alone holds the
// token, no ServiceAccount token, never restarted, named after the pod so the
// job's runner_name finds it, the project's own Go cache when the label has one.
func TestBuildRunnerPod(t *testing.T) {
	in := runnerPodInput{namespace: "shop", provider: "gitea", gitURL: "http://gitea.gitea.svc.cluster.local:3000",
		tokenSecret: ciRunnerTokenSecret("gitea"),
		runner:      c4v1alpha1.CIRunnerSpec{Label: "go", Image: "c4factory/gitea-runner-go:dev", Size: 1, GoCache: true},
		goCacheRoot: "/var/cache/c4factory/go/ci"}
	pod := buildRunnerPod(in)
	if pod.GenerateName != "ci-gitea-go-" || pod.Namespace != "shop" {
		t.Errorf("named %q in %q, want ci-gitea-go- in shop", pod.GenerateName, pod.Namespace)
	}
	for k, v := range map[string]string{LabelCIRunner: ciRunnerValue, LabelCIGitProvider: "gitea", LabelCILabel: "go",
		c4v1alpha1.LabelProject: "shop"} {
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
		runner:      c4v1alpha1.CIRunnerSpec{Label: "go", Image: "c4factory/gitea-runner-go:dev", GoCache: true},
		goCacheRoot: "/var/cache/c4factory/go/ci"}
	pod := buildRunnerPod(in)
	for _, v := range pod.Spec.Volumes {
		switch v.Name {
		case goCacheVolume:
			if v.HostPath == nil || v.HostPath.Path != "/var/cache/c4factory/go/ci/shop" {
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
	if spec := fencePolicySpec(map[string]string{LabelCIRunner: ciRunnerValue}, nil, nil); len(spec.PolicyTypes) != 2 ||
		len(spec.Egress) != 2 || spec.PodSelector.MatchLabels[LabelCIRunner] != ciRunnerValue {
		t.Errorf("fencePolicySpec() = %+v, want ingress and egress types, two rules, the CI selector", spec)
	}
}
