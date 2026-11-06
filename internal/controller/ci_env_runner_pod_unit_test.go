/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// envRunner is the pool input of test environment android, which asks for
// KVM, in GitProvider forge's pool.
func envRunner() runnerPodInput {
	env := &c4v1beta1.TestEnvironment{ObjectMeta: objectMeta("shop", "android"), Spec: c4v1beta1.TestEnvironmentSpec{
		Local: &c4v1beta1.TestEnvironmentLocal{Image: "ghcr.io/jairjosafath/theos/env-android:0.31.0",
			Resources:        corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("4Gi")}},
			Devices:          map[corev1.ResourceName]resource.Quantity{"devices.kubevirt.io/kvm": resource.MustParse("1")},
			RuntimeClassName: "kvm", NodeSelector: map[string]string{"kvm": "true"},
			Tolerations: []corev1.Toleration{{Key: "kvm", Operator: corev1.TolerationOpExists}}},
		CI: &c4v1beta1.TestEnvironmentCI{Size: 1}}}
	provider := &c4v1beta1.GitProvider{ObjectMeta: objectMeta("shop", "forge"),
		Spec: c4v1beta1.GitProviderSpec{URL: "http://forgejo.forge.svc.cluster.local:3000"}}
	in, clash := envRunnerPool(provider, []c4v1beta1.CIRunnerSpec{{Label: "base", Image: "theos/ci-runner-base:dev"}},
		env, []string{"ghcr"})
	if clash != "" {
		panic(clash)
	}
	return in
}

// A test environment's pool pod (docs/plans/done/test-environments.md, T8) is
// under the CI fence as every pool pod is: the register init container, on
// the runner image, alone holds the registration token and copies the
// runner in; the jobs run on the environment's image, with its devices, as
// the copied runner, with no ServiceAccount token, not privileged.
func TestBuildEnvRunnerPod(t *testing.T) {
	pod := buildRunnerPod(envRunner())
	if pod.GenerateName != "ci-forge-env-android-" || pod.Labels[LabelCILabel] != model.EnvRunnerLabel("android") {
		t.Errorf("named %q, label %q; want the env-android pool's", pod.GenerateName, pod.Labels[LabelCILabel])
	}
	fence, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{MatchLabels: map[string]string{
		LabelCIRunner: ciRunnerValue}})
	if err != nil {
		t.Fatal(err)
	}
	if !fence.Matches(labels.Set(pod.Labels)) {
		t.Errorf("labels %v, not selected by the project's CI fence", pod.Labels)
	}
	spec := pod.Spec
	if spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken ||
		spec.EnableServiceLinks == nil || *spec.EnableServiceLinks || spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("automount %v, service links %v, restart %q: want no token, no links, never",
			spec.AutomountServiceAccountToken, spec.EnableServiceLinks, spec.RestartPolicy)
	}
	for _, v := range spec.Volumes {
		if v.HostPath != nil || v.Projected != nil || v.Secret != nil {
			t.Errorf("volume %q mounts a node directory, a token or a Secret", v.Name)
		}
	}
	if spec.RuntimeClassName == nil || *spec.RuntimeClassName != "kvm" || spec.NodeSelector["kvm"] != "true" ||
		len(spec.Tolerations) != 1 || len(spec.ImagePullSecrets) != 1 {
		t.Errorf("placement %v %v %v %v, want the environment's and the shared pull Secret",
			spec.RuntimeClassName, spec.NodeSelector, spec.Tolerations, spec.ImagePullSecrets)
	}
}

// Its register init container alone holds the token and copies the runner
// in; the job's container runs the copy on the environment's image, with
// its devices, and may not write it.
func TestBuildEnvRunnerPodContainers(t *testing.T) {
	spec := buildRunnerPod(envRunner()).Spec
	if len(spec.InitContainers) != 1 {
		t.Fatalf("init containers %v, want register alone", spec.InitContainers)
	}
	register := spec.InitContainers[0]
	vars := map[string]corev1.EnvVar{}
	for _, e := range register.Env {
		vars[e.Name] = e
	}
	if register.Image != "theos/ci-runner-base:dev" || vars["RUNNER_TOKEN"].ValueFrom == nil ||
		vars["RUNNER_COPY_TO"].Value != envRunnerDir || vars["RUNNER_LABELS"].Value != "env-android:host" ||
		vars["RUNNER_EPHEMERAL"].Value != "1" {
		t.Errorf("register %s with %+v: want the runner image, the token, the copy and env-android:host", register.Image, vars)
	}
	if !hasMount(register, envRunnerDir) {
		t.Error("register doesn't mount the folder it copies the runner to")
	}
	c := spec.Containers[0]
	if c.Image != "ghcr.io/jairjosafath/theos/env-android:0.31.0" ||
		!reflect.DeepEqual(c.Command, []string{envRunnerDir + "/entrypoint"}) {
		t.Errorf("runner container %s %v, want the copied runner on the environment's image", c.Image, c.Command)
	}
	for _, e := range c.Env {
		if e.Name == "RUNNER_TOKEN" || e.ValueFrom != nil {
			t.Errorf("the job's container gets %s", e.Name)
		}
	}
	if sc := c.SecurityContext; sc == nil || sc.Privileged == nil || *sc.Privileged || sc.Capabilities != nil {
		t.Errorf("securityContext %+v, want not privileged and no capability added", sc)
	}
	kvm := corev1.ResourceName("devices.kubevirt.io/kvm")
	if q := c.Resources.Requests[kvm]; q.Cmp(resource.MustParse("1")) != 0 {
		t.Errorf("requests %v, want the device", c.Resources.Requests)
	}
	if q := c.Resources.Limits[kvm]; q.Cmp(resource.MustParse("1")) != 0 {
		t.Errorf("limits %v, want the device", c.Resources.Limits)
	}
	if q := c.Resources.Requests[corev1.ResourceMemory]; q.Cmp(resource.MustParse("4Gi")) != 0 {
		t.Errorf("memory request %v, want the environment's", q)
	}
	for _, m := range c.VolumeMounts {
		if m.MountPath == envRunnerDir && !m.ReadOnly {
			t.Error("the jobs may write the runner")
		}
	}
}

// The pool takes the environment's ci.runnerImage, else the first runner's,
// and a label the ci block names already is a clash (T8).
func TestEnvRunnerPool(t *testing.T) {
	env := &c4v1beta1.TestEnvironment{ObjectMeta: objectMeta("shop", "browser"), Spec: c4v1beta1.TestEnvironmentSpec{
		Local: &c4v1beta1.TestEnvironmentLocal{Image: "env-browser:1"},
		CI:    &c4v1beta1.TestEnvironmentCI{Size: 2, RunnerImage: "theos/ci-runner-go:dev"}}}
	provider := &c4v1beta1.GitProvider{ObjectMeta: objectMeta("shop", "forge")}
	runners := []c4v1beta1.CIRunnerSpec{{Label: "base", Image: "theos/ci-runner-base:dev"}}
	in, clash := envRunnerPool(provider, runners, env, nil)
	if clash != "" || in.runner.Image != "theos/ci-runner-go:dev" || in.runner.Size != 2 || in.runner.Label != "env-browser" {
		t.Errorf("pool %+v, %q; want the environment's runner image, size 2 and label env-browser", in.runner, clash)
	}
	env.Spec.CI.RunnerImage = ""
	if in, _ := envRunnerPool(provider, runners, env, nil); in.runner.Image != "theos/ci-runner-base:dev" {
		t.Errorf("runner image %q, want the first runner's", in.runner.Image)
	}
	clashing := []c4v1beta1.CIRunnerSpec{runners[0], {Label: "env-browser", Image: "x:1"}}
	if _, clash := envRunnerPool(provider, clashing, env, nil); clash == "" {
		t.Error("a label the ci block names too is no clash")
	}
}
