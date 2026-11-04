/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"crypto/rand"
	"errors"
	"path"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/ports/fake"
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

// failRegistration fails a pool pod as a registration that ended non-zero
// does: its register init container exits 1 and the pod is Failed.
func failRegistration(pod *corev1.Pod) {
	GinkgoHelper()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), pod)).To(Succeed())
	pod.Status.Phase = corev1.PodFailed
	pod.Status.InitContainerStatuses = []corev1.ContainerStatus{{
		Name: "register", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}}}
	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
}

// newCIProvider is a Gitea GitProvider for ns, with credentials, whose ci block
// asks for a pool of two go runners with the Go cache.
func newCIProvider(ns string) *c4v1beta1.GitProvider {
	GinkgoHelper()
	mustCreate(&corev1.Secret{
		ObjectMeta: objectMeta(ns, "gitea-credentials"),
		StringData: map[string]string{"username": "c4admin", "token": rand.Text()},
	})
	provider := &c4v1beta1.GitProvider{
		ObjectMeta: objectMeta(ns, "gitea"),
		Spec: c4v1beta1.GitProviderSpec{
			URL:                  "http://gitea.gitea.svc.cluster.local:3000",
			Owner:                ns,
			CredentialsSecretRef: c4v1beta1.LocalRef{Name: "gitea-credentials"},
			CI: &c4v1beta1.CISpec{GiteaActions: &c4v1beta1.GiteaActionsSpec{Runners: []c4v1beta1.CIRunnerSpec{
				{Label: "go", Image: "theos/gitea-runner-go:dev", Size: 2, GoCache: true},
			}}},
		},
	}
	mustCreate(provider)
	return provider
}

var _ = Describe("CI runner pool", func() {
	It("keeps a pool per GitProvider label, replaces ended pods and keeps their fate (#1140)", func() {
		ns := newProjectNamespace()
		var project c4v1beta1.Project
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
		Expect(string(token.Data["token"])).To(Equal(fake.RunnerTokenPrefix + ns))

		By("fencing the pool and giving it the job.env")
		var env corev1.ConfigMap
		Expect(k8sClient.Get(ctx, key(ns, CIJobEnvConfigMap), &env)).To(Succeed())
		Expect(env.Data).To(HaveKeyWithValue("job.env", CIJobEnv))
		var policy networkingv1.NetworkPolicy
		Expect(k8sClient.Get(ctx, key(ns, CINetworkPolicyName), &policy)).To(Succeed())
		Expect(controlledBy(&policy, &project)).To(BeTrue())
		Expect(policy.Spec).To(Equal(fencePolicySpec(map[string]string{LabelCIRunner: ciRunnerValue}, fenceEgress(nil, []string{"gitea"}))))
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

	It("follows the ci block: a lowered size trims idle pods, a new image rolls them, a dropped block removes the pool's objects (#1700)", func() {
		ns := newProjectNamespace()
		var project c4v1beta1.Project
		Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
		provider := newCIProvider(ns)
		Eventually(func() []corev1.Pod { return livePoolPods(ns) }).Should(HaveLen(2))
		// busyPod registers the pod's runner to the project's organization and
		// gives it a job, as its init container and the host would.
		busyPod := func(pod corev1.Pod) {
			GinkgoHelper()
			_, err := gitFake.RegisterRunner(fake.RunnerTokenPrefix+ns, pod.Name, "go:host")
			Expect(err).NotTo(HaveOccurred())
			gitFake.SetRunnerOnline(ns, pod.Name, true, true)
		}
		names := func(pods []corev1.Pod) []string {
			out := make([]string, 0, len(pods))
			for _, pod := range pods {
				out = append(out, pod.Name)
			}
			return out
		}

		By("growing the pool to a raised size")
		Expect(patch(provider, func() { provider.Spec.CI.GiteaActions.Runners[0].Size = 3 })).To(Succeed())
		Eventually(func() []corev1.Pod { return livePoolPods(ns) }).Should(HaveLen(3))
		before := livePoolPods(ns)
		busy := before[0]
		busyPod(busy)

		By("trimming idle pods above a lowered size, the busy one kept")
		Expect(patch(provider, func() { provider.Spec.CI.GiteaActions.Runners[0].Size = 1 })).To(Succeed())
		// The pool records a pod lost before it deletes it, so a pod is out of
		// livePoolPods a moment before it is deleting: wait for both (#1886).
		Eventually(func(g Gomega) {
			g.Expect(names(livePoolPods(ns))).To(ConsistOf(busy.Name))
			for _, pod := range poolPods(ns) {
				if pod.Name != busy.Name {
					g.Expect(pod.Annotations).To(HaveKeyWithValue(AnnotationRunnerFate, RunnerFateLost))
					g.Expect(pod.DeletionTimestamp).NotTo(BeNil())
				}
			}
		}).Should(Succeed())
		Consistently(func() []corev1.Pod { return livePoolPods(ns) }).Should(HaveLen(1))

		By("not replacing the busy pod once it ends, the pool being at size")
		setPodPhase(&busy, corev1.PodSucceeded)
		Eventually(func(g Gomega) {
			g.Expect(livePoolPods(ns)).To(HaveLen(1))
			g.Expect(names(livePoolPods(ns))).NotTo(ContainElement(busy.Name))
			g.Expect(poolPod(ns, busy.Name).Annotations).To(HaveKeyWithValue(AnnotationRunnerFate, RunnerFateEnded))
		}).Should(Succeed())

		By("rolling the idle pods to a new image, the busy one only once it ends")
		Expect(patch(provider, func() { provider.Spec.CI.GiteaActions.Runners[0].Size = 2 })).To(Succeed())
		Eventually(func() []corev1.Pod { return livePoolPods(ns) }).Should(HaveLen(2))
		old := livePoolPods(ns)
		busy = old[0]
		busyPod(busy)
		Expect(patch(provider, func() { provider.Spec.CI.GiteaActions.Runners[0].Image = "theos/gitea-runner-go:next" })).To(Succeed())
		Eventually(func(g Gomega) {
			live := livePoolPods(ns)
			g.Expect(live).To(HaveLen(2))
			g.Expect(names(live)).To(ContainElement(busy.Name))
			g.Expect(names(live)).NotTo(ContainElement(old[1].Name))
			for _, pod := range live {
				if pod.Name != busy.Name {
					g.Expect(pod.Spec.Containers[0].Image).To(Equal("theos/gitea-runner-go:next"))
					g.Expect(pod.Spec.InitContainers[0].Image).To(Equal("theos/gitea-runner-go:next"))
				}
			}
		}).Should(Succeed())
		Expect(poolPod(ns, old[1].Name).Annotations).To(HaveKeyWithValue(AnnotationRunnerFate, RunnerFateLost))
		Consistently(func() []string { return names(livePoolPods(ns)) }).Should(ContainElement(busy.Name))
		setPodPhase(&busy, corev1.PodSucceeded)
		Eventually(func(g Gomega) {
			live := livePoolPods(ns)
			g.Expect(live).To(HaveLen(2))
			for _, pod := range live {
				g.Expect(pod.Spec.Containers[0].Image).To(Equal("theos/gitea-runner-go:next"))
				g.Expect(pod.Annotations).To(HaveKey(AnnotationRunnerSpec))
			}
		}).Should(Succeed())

		By("removing the pool's objects and condition with the ci block")
		Expect(patch(provider, func() { provider.Spec.CI = nil })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(livePoolPods(ns)).To(BeEmpty())
			g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "c4-ci-runner-gitea"), &corev1.Secret{}))).To(BeTrue())
			g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, CIJobEnvConfigMap), &corev1.ConfigMap{}))).To(BeTrue())
			g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, CINetworkPolicyName), &networkingv1.NetworkPolicy{}))).To(BeTrue())
			g.Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
			g.Expect(condition(project.Status.Conditions, ConditionCIRunners)).To(BeNil())
		}).Should(Succeed())
	})

	It("takes the pool down with its Project, kept records included (#1697)", func() {
		ns := newProjectNamespace()
		var project c4v1beta1.Project
		Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
		newCIProvider(ns)
		Eventually(func() []corev1.Pod { return livePoolPods(ns) }).Should(HaveLen(2))

		By("keeping an ended pod's record under its finalizer")
		ended := livePoolPods(ns)[0]
		setPodPhase(&ended, corev1.PodSucceeded)
		Eventually(func(g Gomega) {
			record := poolPod(ns, ended.Name)
			g.Expect(record).NotTo(BeNil())
			g.Expect(record.DeletionTimestamp).NotTo(BeNil())
			g.Expect(record.Finalizers).To(ConsistOf(CIRunnerRecordFinalizer))
		}).Should(Succeed())

		By("deleting the Project: the record and the live pods go with it")
		Expect(k8sClient.Delete(ctx, &project)).To(Succeed())
		Eventually(func() []corev1.Pod { return poolPods(ns) }).Should(BeEmpty())
		Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(MatchError(apierrors.IsNotFound, "IsNotFound"))
	})

	It("reports a provider whose credentials it cannot read instead of a pool", func() {
		ns := newProjectNamespace()
		mustCreate(&c4v1beta1.GitProvider{
			ObjectMeta: objectMeta(ns, "gitea"),
			Spec: c4v1beta1.GitProviderSpec{
				URL: "http://gitea.gitea.svc.cluster.local:3000", Owner: ns,
				CredentialsSecretRef: c4v1beta1.LocalRef{Name: "missing"},
				CI: &c4v1beta1.CISpec{GiteaActions: &c4v1beta1.GiteaActionsSpec{Runners: []c4v1beta1.CIRunnerSpec{
					{Label: "go", Image: "theos/gitea-runner-go:dev"},
				}}},
			},
		})
		Eventually(func(g Gomega) {
			var project c4v1beta1.Project
			g.Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
			g.Expect(condition(project.Status.Conditions, ConditionCIRunners)).To(And(
				HaveField("Status", metav1.ConditionFalse), HaveField("Reason", "ProviderUnavailable"),
				HaveField("Message", "gitea: secret missing of GitProvider gitea not found")))
		}).Should(Succeed())
		Expect(poolPods(ns)).To(BeEmpty())
	})

	It("backs off from a pod that failed at registration until a pod runs its job (#1699)", func() {
		ns := newProjectNamespace()
		newCIProvider(ns)
		live := func() []corev1.Pod { return livePoolPods(ns) }
		Eventually(live).Should(HaveLen(2))
		failOne := func() {
			GinkgoHelper()
			failed := livePoolPods(ns)[0]
			failRegistration(&failed)
			Eventually(func(g Gomega) {
				record := poolPod(ns, failed.Name)
				g.Expect(record).NotTo(BeNil())
				g.Expect(record.Annotations).To(HaveKeyWithValue(AnnotationRunnerFate, RunnerFateLost))
				g.Expect(record.Annotations).To(HaveKeyWithValue(AnnotationRunnerFailure, "register"))
			}).Should(Succeed())
		}

		By("waiting the first backoff before replacing the first failed pod")
		failOne()
		Consistently(live, 250*time.Millisecond).Should(HaveLen(1))
		Eventually(live).Should(HaveLen(2))

		By("doubling the wait for the second failure in a row")
		failOne()
		Consistently(live, 600*time.Millisecond).Should(HaveLen(1))
		Eventually(live).Should(HaveLen(2))

		By("replacing a pod that ran its job at once, which ends the streak")
		ended := livePoolPods(ns)[0]
		setPodPhase(&ended, corev1.PodSucceeded)
		Eventually(func(g Gomega) {
			g.Expect(livePoolPods(ns)).To(HaveLen(2))
			g.Expect(livePoolPods(ns)).NotTo(ContainElement(HaveField("Name", ended.Name)))
		}).Should(Succeed())

		By("waiting only the first backoff again after the streak ended")
		failOne()
		Consistently(live, 250*time.Millisecond).Should(HaveLen(1))
		Eventually(live, 1500*time.Millisecond).Should(HaveLen(2))
	})

	It("reports a provider whose token call fails, keeps its pods and retries (#1699)", func() {
		ns := newProjectNamespace()
		provider := newCIProvider(ns)
		Eventually(func() []corev1.Pod { return livePoolPods(ns) }).Should(HaveLen(2))
		ciRunners := func(g Gomega) *metav1.Condition {
			var project c4v1beta1.Project
			g.Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
			return condition(project.Status.Conditions, ConditionCIRunners)
		}

		By("saying the host gave no token, keeping the live pod and adding none")
		gitFake.FailRunnerToken(ns, errors.New("403 Forbidden"))
		DeferCleanup(func() { gitFake.FailRunnerToken(ns, nil) })
		ended := livePoolPods(ns)[0]
		setPodPhase(&ended, corev1.PodSucceeded)
		Eventually(func(g Gomega) {
			g.Expect(ciRunners(g)).To(And(
				HaveField("Status", metav1.ConditionFalse), HaveField("Reason", "ProviderUnavailable"),
				HaveField("Message", "gitea: runner registration token: 403 Forbidden")))
			g.Expect(livePoolPods(ns)).To(HaveLen(1))
		}).Should(Succeed())
		Consistently(func() []corev1.Pod { return livePoolPods(ns) }, 500*time.Millisecond).Should(HaveLen(1))

		By("keeping a pod of an old image, since the host says no runner's job")
		kept := livePoolPods(ns)[0]
		Expect(patch(provider, func() { provider.Spec.CI.GiteaActions.Runners[0].Image = "theos/gitea-runner-go:next" })).To(Succeed())
		Consistently(func() []string {
			names := make([]string, 0, 1)
			for _, pod := range livePoolPods(ns) {
				names = append(names, pod.Name)
			}
			return names
		}, 500*time.Millisecond).Should(ConsistOf(kept.Name))

		By("filling the pool again once the host answers")
		gitFake.FailRunnerToken(ns, nil)
		Eventually(func(g Gomega) {
			g.Expect(ciRunners(g)).To(And(
				HaveField("Status", metav1.ConditionTrue), HaveField("Reason", "PoolKept")))
			g.Expect(livePoolPods(ns)).To(HaveLen(2))
		}).Should(Succeed())
	})
})
