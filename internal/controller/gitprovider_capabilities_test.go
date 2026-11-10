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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/ports"
)

// setAgentsNeed sets, or with "" removes, what the Secret of ns's GitProvider
// lacks for agents to get a credential (agentsNeedKey).
func setAgentsNeed(ns, secret, need string) {
	GinkgoHelper()
	var s corev1.Secret
	Expect(k8sClient.Get(ctx, key(ns, secret), &s)).To(Succeed())
	orig := s.DeepCopy()
	if need == "" {
		delete(s.Data, agentsNeedKey)
	} else {
		s.Data[agentsNeedKey] = []byte(need)
	}
	Expect(k8sClient.Patch(ctx, &s, client.MergeFrom(orig))).To(Succeed())
}

var _ = Describe("What a GitProvider's host can do (#81)", func() {
	conditionOf := func(g Gomega, gp *c4v1beta1.GitProvider, condition string) *metav1.Condition {
		var got c4v1beta1.GitProvider
		g.Expect(k8sClient.Get(ctx, key(gp.Namespace, gp.Name), &got)).To(Succeed())
		cond := apimeta.FindStatusCondition(got.Status.Conditions, condition)
		g.Expect(cond).NotTo(BeNil(), "no %s condition yet", condition)
		return cond
	}

	It("says agents get a credential, the operator registers runners and Python packages have an index", func() {
		ns := newProjectNamespace()
		gp := newEventsProvider(ns, ns)
		Eventually(func(g Gomega) {
			for _, condition := range []string{c4v1beta1.ConditionGitProviderAgentAccess,
				c4v1beta1.ConditionGitProviderOwnRunners, c4v1beta1.ConditionGitProviderPythonIndex} {
				cond := conditionOf(g, gp, condition)
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue), condition+": "+cond.Message)
				g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonCapabilityAvailable), condition)
			}
		}).Should(Succeed())
	})

	It("says what the Secret lacks when the host gives agents no credential, in the host's own words", func() {
		ns := newProjectNamespace()
		mustCreate(&corev1.Secret{ObjectMeta: objectMeta(ns, "half"), StringData: map[string]string{
			"username": "theos-admin", "token": "t", agentsNeedKey: "an identity for the agents"}})
		gp := &c4v1beta1.GitProvider{ObjectMeta: objectMeta(ns, "half"), Spec: c4v1beta1.GitProviderSpec{
			URL: "https://git.example", Owner: ns, CredentialsSecretRef: c4v1beta1.LocalRef{Name: "half"}}}
		mustCreate(gp)
		Eventually(func(g Gomega) {
			cond := conditionOf(g, gp, c4v1beta1.ConditionGitProviderAgentAccess)
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(reasonNeedsAgentIdentity))
			g.Expect(cond.Message).To(Equal("The Secret lacks an identity for the agents."))
			// The rest of the host is as it was: the provider scaffolds and
			// reads issues all the same.
			g.Expect(conditionOf(g, gp, c4v1beta1.ConditionGitProviderOwnRunners).Status).To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())
	})

	It("says agents get none while the provider's Secret is missing, and nothing of the host it can't reach", func() {
		ns := newProjectNamespace()
		gp := &c4v1beta1.GitProvider{ObjectMeta: objectMeta(ns, "bare"), Spec: c4v1beta1.GitProviderSpec{
			URL: "https://git.example", Owner: ns, CredentialsSecretRef: c4v1beta1.LocalRef{Name: "nowhere"}}}
		mustCreate(gp)
		Eventually(func(g Gomega) {
			cond := conditionOf(g, gp, c4v1beta1.ConditionGitProviderAgentAccess)
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonProviderUnavailable))
			g.Expect(cond.Message).To(ContainSubstring("secret nowhere of GitProvider bare not found"))
			var got c4v1beta1.GitProvider
			g.Expect(k8sClient.Get(ctx, key(ns, gp.Name), &got)).To(Succeed())
			g.Expect(apimeta.FindStatusCondition(got.Status.Conditions, c4v1beta1.ConditionGitProviderOwnRunners)).To(BeNil())
			g.Expect(apimeta.FindStatusCondition(got.Status.Conditions, c4v1beta1.ConditionGitProviderPythonIndex)).To(BeNil())
		}).Should(Succeed())
	})

	It("starts no Task on a host that gives agents no credential, and starts it once the Secret has what it lacked", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		setAgentsNeed(ns, "gitea-credentials", "an identity for the agents")
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))

		By("waiting with the provider's reason, and making no sandbox and no agent credential")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			cond := apimeta.FindStatusCondition(task.Status.Conditions, ConditionProgressing)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(reasonNeedsAgentIdentity))
			g.Expect(cond.Message).To(And(ContainSubstring("GitProvider gitea gives agents no credential"),
				ContainSubstring("The Secret lacks an identity for the agents.")))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "add-crop-r1-implement"), newSandbox())).NotTo(Succeed())
			var minted corev1.Secret
			g.Expect(k8sClient.Get(ctx, key(ns, agentGitSecret("orders-api")), &minted)).NotTo(Succeed())
		}, "2s").Should(Succeed())

		By("starting the run once the Secret has it")
		setAgentsNeed(ns, "gitea-credentials", "")
		Eventually(func(g Gomega) {
			sandboxExists(g, ns, "add-crop-r1-implement")
			g.Expect(getTask(g, ns, "add-crop").Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
		}).Should(Succeed())
	})

	It("is the fake host's own word, as any host's", func() {
		ok, reason, _ := ports.AgentAccessState(withoutAgents{need: "x"})
		Expect(ok).To(BeFalse())
		Expect(reason).To(Equal(reasonNeedsAgentIdentity))
	})
})
