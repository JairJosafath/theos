/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"crypto/rand"
	"path"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// poolPods lists a namespace's CI runner pool pods, the kept records included.
func poolPods(ns string) []corev1.Pod {
	GinkgoHelper()
	var pods corev1.PodList
	Expect(k8sClient.List(ctx, &pods, client.InNamespace(ns), client.MatchingLabels{LabelCIRunner: ciRunnerValue})).To(Succeed())
	return pods.Items
}

// livePoolPods are the pool pods not ended: no fate recorded, nobody deleting them.
func livePoolPods(ns string) []corev1.Pod {
	GinkgoHelper()
	var live []corev1.Pod
	for _, pod := range poolPods(ns) {
		if pod.DeletionTimestamp == nil && pod.Annotations[AnnotationRunnerFate] == "" {
			live = append(live, pod)
		}
	}
	return live
}

// poolPod is the pool pod name, kept record or not; nil once it is gone.
func poolPod(ns, name string) *corev1.Pod {
	GinkgoHelper()
	var pod corev1.Pod
	err := k8sClient.Get(ctx, key(ns, name), &pod)
	if client.IgnoreNotFound(err) != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	if err != nil {
		return nil
	}
	return &pod
}

// setPodPhase sets a pool pod's phase, as the kubelet envtest lacks would.
func setPodPhase(pod *corev1.Pod, phase corev1.PodPhase) {
	GinkgoHelper()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), pod)).To(Succeed())
	pod.Status.Phase = phase
	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
}

// newCIProvider is a Gitea GitProvider for ns, with credentials, whose ci block
// asks for a pool of two go runners with the Go cache.
func newCIProvider(ns string) *c4v1alpha1.GitProvider {
	GinkgoHelper()
	mustCreate(&corev1.Secret{
		ObjectMeta: objectMeta(ns, "gitea-credentials"),
		StringData: map[string]string{"username": "c4admin", "token": rand.Text()},
	})
	provider := &c4v1alpha1.GitProvider{
		ObjectMeta: objectMeta(ns, "gitea"),
		Spec: c4v1alpha1.GitProviderSpec{
			URL:                  "http://gitea.gitea.svc.cluster.local:3000",
			Owner:                ns,
			CredentialsSecretRef: c4v1alpha1.LocalRef{Name: "gitea-credentials"},
			CI: &c4v1alpha1.CISpec{GiteaActions: &c4v1alpha1.GiteaActionsSpec{Runners: []c4v1alpha1.CIRunnerSpec{
				{Label: "go", Image: "c4factory/gitea-runner-go:dev", Size: 2, GoCache: true},
			}}},
		},
	}
	mustCreate(provider)
	return provider
}

var _ = Describe("CI runner pool", func() {
	It("keeps a pool per GitProvider label, replaces ended pods and keeps their fate (#1140)", func() {
		ns := newProjectNamespace()
		var project c4v1alpha1.Project
		Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
		provider := newCIProvider(ns)

		By("filling the pool to its size, each pod registered with the provider's token")
		Eventually(func() []corev1.Pod { return livePoolPods(ns) }).Should(HaveLen(2))
		for _, pod := range livePoolPods(ns) {
			Expect(controlledBy(&pod, &project)).To(BeTrue())
			Expect(pod.Finalizers).To(ConsistOf(CIRunnerRecordFinalizer))
			Expect(pod.Labels).To(HaveKeyWithValue(LabelCIGitProvider, "gitea"))
			Expect(pod.Labels).To(HaveKeyWithValue(LabelCILabel, "go"))
			Expect(pod.Spec.RestartPolicy).To(Equal(corev1.RestartPolicyNever))
			Expect(pod.Spec.AutomountServiceAccountToken).To(HaveValue(BeFalse()))
			Expect(pod.Spec.InitContainers[0].Env).To(ContainElement(corev1.EnvVar{
				Name: "RUNNER_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "c4-ci-runner-gitea"}, Key: "token"}}}))
			Expect(pod.Spec.Volumes).To(ContainElement(HaveField("HostPath.Path", path.Join(testCIGoCache, ns))))
		}
		var token corev1.Secret
		Expect(k8sClient.Get(ctx, key(ns, "c4-ci-runner-gitea"), &token)).To(Succeed())
		Expect(controlledBy(&token, &project)).To(BeTrue())
		Expect(string(token.Data["token"])).To(Equal(runnerTokenPrefix + ns))

		By("fencing the pool and giving it the job.env")
		var env corev1.ConfigMap
		Expect(k8sClient.Get(ctx, key(ns, CIJobEnvConfigMap), &env)).To(Succeed())
		Expect(env.Data).To(HaveKeyWithValue("job.env", CIJobEnv))
		var policy networkingv1.NetworkPolicy
		Expect(k8sClient.Get(ctx, key(ns, CINetworkPolicyName), &policy)).To(Succeed())
		Expect(controlledBy(&policy, &project)).To(BeTrue())
		Expect(policy.Spec).To(Equal(fencePolicySpec(map[string]string{LabelCIRunner: ciRunnerValue}, nil, []string{"gitea"})))
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
			g.Expect(condition(project.Status.Conditions, ConditionCIRunners)).To(And(
				HaveField("Status", metav1.ConditionTrue), HaveField("Reason", "PoolKept")))
		}).Should(Succeed())

		By("replacing a pod whose runner ran its one job and exited, recorded as ended")
		ended := livePoolPods(ns)[0]
		setPodPhase(&ended, corev1.PodSucceeded)
		Eventually(func(g Gomega) {
			record := poolPod(ns, ended.Name)
			g.Expect(record).NotTo(BeNil())
			g.Expect(record.Annotations).To(HaveKeyWithValue(AnnotationRunnerFate, RunnerFateEnded))
			g.Expect(record.DeletionTimestamp).NotTo(BeNil())
			g.Expect(livePoolPods(ns)).To(HaveLen(2))
		}).Should(Succeed())
		Expect(livePoolPods(ns)).NotTo(ContainElement(HaveField("Name", ended.Name)))

		By("recording a pod deleted mid-job as lost, and replacing it")
		lost := livePoolPods(ns)[0]
		setPodPhase(&lost, corev1.PodRunning)
		Expect(k8sClient.Delete(ctx, &lost)).To(Succeed())
		Eventually(func(g Gomega) {
			record := poolPod(ns, lost.Name)
			g.Expect(record).NotTo(BeNil())
			g.Expect(record.Annotations).To(HaveKeyWithValue(AnnotationRunnerFate, RunnerFateLost))
			g.Expect(livePoolPods(ns)).To(HaveLen(2))
		}).Should(Succeed())

		By("letting the records go once a Task has had time to read them")
		Eventually(func(g Gomega) {
			g.Expect(poolPod(ns, ended.Name)).To(BeNil())
			g.Expect(poolPod(ns, lost.Name)).To(BeNil())
		}, 3*testRunnerRecordKept).Should(Succeed())
		Expect(livePoolPods(ns)).To(HaveLen(2))

		By("taking the pool down with the provider's ci block, its idle pods lost")
		Expect(patch(provider, func() { provider.Spec.CI = nil })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(livePoolPods(ns)).To(BeEmpty())
			for _, pod := range poolPods(ns) {
				g.Expect(pod.Annotations).To(HaveKeyWithValue(AnnotationRunnerFate, RunnerFateLost))
			}
			g.Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
			g.Expect(condition(project.Status.Conditions, ConditionCIRunners)).To(BeNil())
		}).Should(Succeed())
		Eventually(func() []corev1.Pod { return poolPods(ns) }, 3*testRunnerRecordKept).Should(BeEmpty())
	})

	It("reports a provider whose credentials it cannot read instead of a pool", func() {
		ns := newProjectNamespace()
		mustCreate(&c4v1alpha1.GitProvider{
			ObjectMeta: objectMeta(ns, "gitea"),
			Spec: c4v1alpha1.GitProviderSpec{
				URL: "http://gitea.gitea.svc.cluster.local:3000", Owner: ns,
				CredentialsSecretRef: c4v1alpha1.LocalRef{Name: "missing"},
				CI: &c4v1alpha1.CISpec{GiteaActions: &c4v1alpha1.GiteaActionsSpec{Runners: []c4v1alpha1.CIRunnerSpec{
					{Label: "go", Image: "c4factory/gitea-runner-go:dev"},
				}}},
			},
		})
		Eventually(func(g Gomega) {
			var project c4v1alpha1.Project
			g.Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
			g.Expect(condition(project.Status.Conditions, ConditionCIRunners)).To(And(
				HaveField("Status", metav1.ConditionFalse), HaveField("Reason", "ProviderUnavailable"),
				HaveField("Message", "gitea: secret missing of GitProvider gitea not found")))
		}).Should(Succeed())
		Expect(poolPods(ns)).To(BeEmpty())
	})
})
