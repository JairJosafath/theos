/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// newStandard returns a Standard that fits the named Frameworks, served by the
// fake Git host with an AGENTS.md and a Makefile.
func newStandard(ns, name string, frameworks ...string) *c4v1beta1.Standard {
	gitFake.AddTemplate(templatesURL+name, "v1.0.0", scaffold.Tree{
		"AGENTS.md.tmpl": {Data: []byte("Rules for {{ .Container }}, checked by {{ .Standard }}\n")},
		"Makefile":       {Data: []byte("check:\n\ttrue\n")},
	})
	return &c4v1beta1.Standard{
		ObjectMeta: objectMeta(ns, name),
		Spec: c4v1beta1.StandardSpec{
			CatalogEntrySpec:    newCatalogSpec(templatesURL+name, "v1.0.0", c4v1beta1.ContainerTypeService),
			SupportedFrameworks: frameworks,
		},
	}
}

var _ = Describe("Container Standard", func() {
	It("fails a Container whose Standard does not list its Framework", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		container.Spec.StandardRef = &c4v1beta1.LocalRef{Name: "strict"}
		mustCreate(newSystem(ns, "webshop-core"), newStandard(ns, "strict", "go-cli"), container)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerFailed))
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionChoicesValid)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonUnsupportedFramework))
			g.Expect(cond.Message).To(Equal("strict does not support python-fastapi"))
		}).Should(Succeed())
	})

	It("waits for a missing Standard", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		container.Spec.StandardRef = &c4v1beta1.LocalRef{Name: "python-strict"}
		mustCreate(newSystem(ns, "webshop-core"), container)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerPending))
			g.Expect(condition(container.Status.Conditions, c4v1beta1.ConditionReferencesResolved)).
				To(HaveField("Message", "Standard python-strict not found"))
		}).Should(Succeed())

		mustCreate(newStandard(ns, "python-strict", "python-fastapi"))
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
		}).Should(Succeed())
	})

	It("scaffolds a fitting Standard as the second layer", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		container.Spec.StandardRef = &c4v1beta1.LocalRef{Name: "python-strict"}
		mustCreate(newSystem(ns, "webshop-core"), newStandard(ns, "python-strict", "python-fastapi"), container)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
			g.Expect(container.Status.ScaffoldedFrom).To(HaveLen(4))
			g.Expect(container.Status.ScaffoldedFrom[1]).To(Equal("python-strict@v1.0.0"))
		}).Should(Succeed())
		files := gitFake.Repo(ns, "orders-api").Files
		Expect(string(files["AGENTS.md"].Data)).To(Equal("Rules for orders-api, checked by python-strict\n"))
	})

	It("protects main and reports its checks when the Container has a Standard", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		container.Spec.StandardRef = &c4v1beta1.LocalRef{Name: "python-strict"}
		plain := newContainer(ns, "orders-worker", "webshop-core", c4v1beta1.ContainerTypeService)
		mustCreate(newSystem(ns, "webshop-core"), newStandard(ns, "python-strict", "python-fastapi"), container, plain)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-worker"), plain)).To(Succeed())
			g.Expect(plain.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
		}).Should(Succeed())
		Expect(gitFake.ProtectedBranches(ns, "orders-api")).To(Equal([]string{"main"}))
		Expect(gitFake.ProtectedBranches(ns, "orders-worker")).To(BeEmpty())

		// Checks are polled, from the scaffold on, with nothing else touching the
		// Container (a nudge here once hid that polling never started).
		gitFake.SetChecks(ns, "orders-api", "main", "success")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Checks).NotTo(BeNil())
			g.Expect(container.Status.Checks.State).To(Equal("success"))
		}).Should(Succeed())
		Expect(plain.Status.Checks).To(BeNil())
	})

	It("is adopted by the Project, like the other catalog entries", func() {
		ns := newProjectNamespace()
		standard := newStandard(ns, "python-strict", "python-fastapi")
		agent := &c4v1beta1.Agent{ObjectMeta: objectMeta(ns, "claude"),
			Spec: c4v1beta1.AgentSpec{Image: "theos/agent-go:dev"}}
		mustCreate(standard, agent)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "python-strict"), standard)).To(Succeed())
			g.Expect(standard.OwnerReferences).To(ContainElement(HaveField("Kind", "Project")))
			g.Expect(k8sClient.Get(ctx, key(ns, "claude"), agent)).To(Succeed())
			g.Expect(agent.OwnerReferences).To(ContainElement(HaveField("Kind", "Project")))
			g.Expect(agent.Spec.MaxRounds).To(Equal(int32(3)))
		}).Should(Succeed())
	})
})

var _ = Describe("Agent network fence", func() {
	It("gives each project a NetworkPolicy for its agent sandboxes", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		var policy networkingv1.NetworkPolicy
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, AgentNetworkPolicyName), &policy)).To(Succeed())
			g.Expect(policy.Spec.Egress).To(HaveLen(4))
		}).Should(Succeed())
		Expect(policy.Spec.PodSelector.MatchLabels).To(HaveKeyWithValue(LabelAgentSandbox, "true"))
		Expect(policy.Spec.Ingress).To(BeEmpty())
		Expect(policy.Spec.Egress[1].To[0].IPBlock.Except).To(ContainElement("10.0.0.0/8"))
		Expect(policy.Spec.Egress[2].To[0].NamespaceSelector.MatchLabels).
			To(HaveKeyWithValue("kubernetes.io/metadata.name", "gitea"))
		// The model proxy (#1136): the sandboxes reach it, and nothing else in
		// the release namespace.
		proxy := policy.Spec.Egress[3]
		Expect(proxy.To).To(HaveLen(1))
		Expect(proxy.To[0].NamespaceSelector.MatchLabels).
			To(Equal(map[string]string{"kubernetes.io/metadata.name": operatorNamespace}))
		Expect(proxy.To[0].PodSelector.MatchLabels).To(Equal(map[string]string{"app.kubernetes.io/name": ModelProxyAppName}))
		Expect(proxy.Ports).To(HaveLen(1))
		Expect(proxy.Ports[0].Port.IntValue()).To(Equal(ModelProxyPort))
		Expect(policy.OwnerReferences).To(ContainElement(HaveField("Kind", "Project")))
	})
})
