/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"maps"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// A test environment's CI pool (docs/plans/done/test-environments.md, T8): a
// local TestEnvironment with ci.size keeps single-job runner pods for the
// label env-<name> in each GitProvider's pool, built from the environment's
// image and devices, so a Standard's workflow runs on the same tools the
// agent ran its commands on with theos-env.

// envRunnerInput is what a pool pod of a test environment takes from it.
type envRunnerInput struct {
	// image has the environment's tools: the runner container's image.
	image string
	// runtimeClassName, nodeSelector and tolerations place the pod as the
	// environment's lease pods are placed.
	runtimeClassName string
	nodeSelector     map[string]string
	tolerations      []corev1.Toleration
}

// Where an environment's pool pod puts the runner, its configuration, and
// the runner image's entrypoint and /tmp sweep, which the register init
// container copies from the runner image (RUNNER_COPY_TO,
// hack/runner/register.sh), beside the environment's tools; the
// entrypoint finds them there (RUNNER_BIN_DIR, hack/runner/entrypoint.sh).
const (
	envRunnerVolume = "runner"
	envRunnerDir    = "/theos-runner"
)

// buildEnvRunnerPod is the pod of one single-job runner of a test
// environment's pool, a pure function of its input: the register init
// container, on the runner image, alone holds the registration token, as
// in buildRunnerPod, and copies the runner into a volume; the runner
// container runs that copy on the environment's image, with its devices,
// under the same CI fence: no ServiceAccount token, no service links, not
// privileged, never restarted. Checkouts and /tmp are in memory.
func buildEnvRunnerPod(in runnerPodInput) *corev1.Pod {
	pod := buildRunnerPod(runnerPodInput{namespace: in.namespace, provider: in.provider, gitURL: in.gitURL,
		tokenSecret: in.tokenSecret, runner: in.runner, pullSecrets: in.pullSecrets})
	env := in.env
	runner := corev1.VolumeMount{Name: envRunnerVolume, MountPath: envRunnerDir}
	register := &pod.Spec.InitContainers[0]
	register.Env = append(register.Env, corev1.EnvVar{Name: "RUNNER_COPY_TO", Value: envRunnerDir})
	register.VolumeMounts = append(register.VolumeMounts, runner)
	runnerRO := runner
	runnerRO.ReadOnly = true
	c := &pod.Spec.Containers[0]
	c.Image = env.image
	c.Command = []string{envRunnerDir + "/entrypoint"}
	c.Env = []corev1.EnvVar{{Name: "RUNNER_CONFIG", Value: envRunnerDir + "/config.yaml"},
		{Name: "RUNNER_BIN_DIR", Value: envRunnerDir}}
	c.SecurityContext = &corev1.SecurityContext{Privileged: new(false)}
	c.VolumeMounts = append(c.VolumeMounts, runnerRO, corev1.VolumeMount{Name: "shm", MountPath: "/dev/shm"})
	pod.Spec.EnableServiceLinks = new(false)
	pod.Spec.Volumes = append(pod.Spec.Volumes,
		corev1.Volume{Name: envRunnerVolume, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		// A browser shares memory through /dev/shm, 64Mi in a pod unless given more.
		corev1.Volume{Name: "shm", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{
			Medium: corev1.StorageMediumMemory, SizeLimit: new(resource.MustParse("1Gi"))}}})
	if env.runtimeClassName != "" {
		pod.Spec.RuntimeClassName = new(env.runtimeClassName)
	}
	pod.Spec.NodeSelector = maps.Clone(env.nodeSelector)
	pod.Spec.Tolerations = env.tolerations
	return pod
}

// withDevices is resources with each device both requested and limited, as
// an extended resource must be.
func withDevices(resources corev1.ResourceRequirements,
	devices map[corev1.ResourceName]resource.Quantity) corev1.ResourceRequirements {
	out := *resources.DeepCopy()
	for name, q := range devices {
		if out.Requests == nil {
			out.Requests = corev1.ResourceList{}
		}
		if out.Limits == nil {
			out.Limits = corev1.ResourceList{}
		}
		out.Requests[name], out.Limits[name] = q, q
	}
	return out
}
