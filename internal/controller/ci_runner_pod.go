/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"path"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// The per-project CI runner pool (docs/plans/ci-runner-isolation.md, step 3).
const (
	// LabelCIRunner marks the pool's pods; the project's CI NetworkPolicy selects it.
	LabelCIRunner = "c4.factory.io/ci-runner"
	// LabelCIGitProvider names the GitProvider a pool pod registers to, and
	// LabelCILabel the job label it takes.
	LabelCIGitProvider = "c4.factory.io/git-provider"
	LabelCILabel       = "c4.factory.io/ci-label"
	// ciRunnerValue is the value of LabelCIRunner.
	ciRunnerValue = "true"
	// CIRunnerRecordFinalizer keeps an ended pod, with its final status, until
	// the pool has read how it ended: a pod deleted mid-job stays readable by
	// the Task that asks who cancelled its job (model.Canceller).
	CIRunnerRecordFinalizer = "c4.factory.io/ci-runner-record"
	// AnnotationRunnerFate is the pool's verdict on an ended pod: RunnerFateEnded
	// or RunnerFateLost.
	AnnotationRunnerFate = "c4.factory.io/runner-fate"
	// RunnerFateEnded is a pod whose runner ran its one job and exited on its
	// own; RunnerFateLost one deleted, evicted or killed before its job ended.
	RunnerFateEnded = "ended"
	RunnerFateLost  = "lost"
	// CINetworkPolicyName is the NetworkPolicy that fences the pool's pods.
	CINetworkPolicyName = "c4-ci-runner"
	// CIJobEnvConfigMap holds the variables the runner gives every job
	// (env_file in hack/runner/config.yaml): the Go caches' paths.
	CIJobEnvConfigMap = "c4-ci-runner-env"
	// ciRunnerContainer runs the runner daemon and its jobs.
	ciRunnerContainer = "runner"
	// ciGoCacheMount is where a pool pod mounts the project's Go cache.
	ciGoCacheMount = "/cache/go"
	// ciRunnerTokenKey is the key of the registration token in its Secret.
	ciRunnerTokenKey = "token"
)

// CIJobEnv is the job.env of CIJobEnvConfigMap: the caches under ciGoCacheMount.
const CIJobEnv = "GOCACHE=" + ciGoCacheMount + "/build\nGOLANGCI_LINT_CACHE=" + ciGoCacheMount + "/golangci-lint\n"

// runnerPodInput is what buildRunnerPod needs: the project's namespace, the
// GitProvider's name and URL, the Secret holding its runner registration token,
// the label's spec and the node directory under which projects' Go caches
// live (the operator's --ci-go-cache), empty for none.
type runnerPodInput struct {
	namespace, provider, gitURL, tokenSecret string
	runner                                   c4v1alpha1.CIRunnerSpec
	goCacheRoot                              string
}

// ciRunnerTokenSecret names the Secret with a GitProvider's runner registration token.
func ciRunnerTokenSecret(provider string) string { return "c4-ci-runner-" + provider }

// buildRunnerPod is the pod of one single-job runner, a pure function of its
// input. The init container registers it, ephemeral, named after the pod, and
// alone holds the registration token; the runner container, where the job runs,
// has no ServiceAccount token and never restarts: it exits when its job ends and
// the pool replaces the pod. Checkouts and /tmp are in memory; the Go cache,
// when the label has one, is the project's own directory on the node.
func buildRunnerPod(in runnerPodInput) *corev1.Pod {
	goCache := in.runner.GoCache && in.goCacheRoot != ""
	registerEnv := []corev1.EnvVar{
		{Name: "GITEA_INSTANCE", Value: in.gitURL},
		{Name: "RUNNER_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: in.tokenSecret}, Key: "token"}}},
		{Name: "RUNNER_LABELS", Value: in.runner.Label + ":host"},
		{Name: "RUNNER_EPHEMERAL", Value: "1"},
		{Name: "RUNNER_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{
			FieldPath: "metadata.name"}}},
	}
	small := corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("16Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("128Mi")},
	}
	data := corev1.VolumeMount{Name: "data", MountPath: "/data"}
	inits := []corev1.Container{{
		Name: "register", Image: in.runner.Image, Command: []string{"c4-runner-register"},
		Env: registerEnv, Resources: small, VolumeMounts: []corev1.VolumeMount{data},
	}}
	mounts := []corev1.VolumeMount{data,
		{Name: "work", MountPath: "/data/work"},
		{Name: "tmp", MountPath: "/tmp"},
	}
	volumes := []corev1.Volume{
		{Name: "data", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "work", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{
			Medium: corev1.StorageMediumMemory, SizeLimit: new(resource.MustParse("2Gi"))}}},
		{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{
			Medium: corev1.StorageMediumMemory, SizeLimit: new(resource.MustParse("1Gi"))}}},
	}
	if goCache {
		cache := corev1.VolumeMount{Name: goCacheVolume, MountPath: ciGoCacheMount}
		inits = append(inits, corev1.Container{
			Name: goCacheVolume, Image: in.runner.Image, Command: []string{"c4-go-cache", ciGoCacheMount},
			Resources: small, VolumeMounts: []corev1.VolumeMount{cache},
		})
		mounts = append(mounts, cache,
			corev1.VolumeMount{Name: "job-env", MountPath: "/etc/gitea-runner/job.env", SubPath: "job.env"})
		hostPathDir := corev1.HostPathDirectoryOrCreate
		volumes = append(volumes,
			corev1.Volume{Name: goCacheVolume, VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{
				Path: path.Join(in.goCacheRoot, in.namespace), Type: &hostPathDir}}},
			corev1.Volume{Name: "job-env", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: CIJobEnvConfigMap}}}})
	}
	labels := projectLabels(in.namespace)
	labels[LabelCIRunner] = ciRunnerValue
	labels[LabelCIGitProvider] = in.provider
	labels[LabelCILabel] = in.runner.Label
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "ci-" + in.provider + "-" + in.runner.Label + "-",
			Namespace:    in.namespace,
			Labels:       labels,
			Finalizers:   []string{CIRunnerRecordFinalizer},
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			AutomountServiceAccountToken: new(false),
			InitContainers:               inits,
			Containers: []corev1.Container{{
				Name: ciRunnerContainer, Image: in.runner.Image, Resources: in.runner.Resources, VolumeMounts: mounts,
			}},
			Volumes: volumes,
		},
	}
}

// runnerPodFate tells how a pool pod ended: RunnerFateEnded when its runner ran
// its job and exited on its own (phase Succeeded, nobody deleting it);
// RunnerFateLost when someone deleted it, the node disrupted it, or its runner
// container ended non-zero; empty while it still runs. The pool asks once, the
// first time it sees the pod ended or being deleted, and writes the answer as
// AnnotationRunnerFate before it deletes the pod itself.
func runnerPodFate(pod *corev1.Pod) string {
	if pod.DeletionTimestamp != nil || podDisrupted(pod) || pod.Status.Phase == corev1.PodFailed {
		return RunnerFateLost
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if term := cs.State.Terminated; cs.Name == ciRunnerContainer && term != nil && term.ExitCode != 0 {
			return RunnerFateLost
		}
	}
	if pod.Status.Phase == corev1.PodSucceeded {
		return RunnerFateEnded
	}
	return ""
}

// fenceEgress is what a fenced pod may reach: DNS, the in-cluster Git hosts in
// gitNamespaces, and HTTPS to public addresses (the model API, package
// registries). The Kubernetes API and every other in-cluster service stay out
// of reach. The agent sandboxes' and the CI runners' policies share it.
func fenceEgress(gitNamespaces []string) []networkingv1.NetworkPolicyEgressRule {
	tcp, udp := corev1.ProtocolTCP, corev1.ProtocolUDP
	dns, https := intstr.FromInt32(53), intstr.FromInt32(443)
	egress := make([]networkingv1.NetworkPolicyEgressRule, 0, 2+len(gitNamespaces))
	egress = append(egress,
		networkingv1.NetworkPolicyEgressRule{
			To: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
					corev1.LabelMetadataName: metav1.NamespaceSystem}},
				PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"k8s-app": "kube-dns"}},
			}},
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: &udp, Port: &dns}, {Protocol: &tcp, Port: &dns}},
		},
		networkingv1.NetworkPolicyEgressRule{
			To: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{
				CIDR: "0.0.0.0/0", Except: model.PrivateRanges()}}},
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &https}},
		})
	for _, ns := range gitNamespaces {
		egress = append(egress, networkingv1.NetworkPolicyEgressRule{To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{corev1.LabelMetadataName: ns}},
		}}})
	}
	return egress
}

// fencePolicySpec is the spec of a policy that fences the pods with label
// selector: no ingress, fenceEgress out.
func fencePolicySpec(selector map[string]string, gitNamespaces []string) networkingv1.NetworkPolicySpec {
	return networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{MatchLabels: selector},
		PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
		Egress:      fenceEgress(gitNamespaces),
	}
}
