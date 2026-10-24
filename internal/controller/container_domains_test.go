/*
Copyright 2026 JairJosafath. All rights reserved.
This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// addDomainTemplate registers the stand-in template newDomain(ns, name) pins,
// and the Domain itself in ns.
func addDomainTemplate(ns, name string, tree scaffold.Tree) {
	GinkgoHelper()
	domain := newDomain(ns, name)
	gitFake.addTemplate(domain.Spec.Template.URL, domain.Spec.Template.Ref, tree)
	mustCreate(domain)
}

// markdownDomain is a sound Domain template: a README and one more page.
func markdownDomain(name string) scaffold.Tree {
	return scaffold.Tree{
		"README.md": {Data: []byte("# " + name + "\n")},
		"carts.md":  {Data: []byte("# Carts\n")},
	}
}

// webshopOnDomains creates the webshop story on a system naming domains.
func webshopOnDomains(ns string, domains ...string) {
	GinkgoHelper()
	system := newSystem(ns, "webshop-core")
	system.Spec.DomainRefs = domains
	createWebshopOn(ns, system, nil)
}

// Step 2 of docs/plans/domains.md (#1757): the Domain layer renders last,
// under docs/agents/domain/<name>/, Markdown only, a README required, and a
// Ready Container reports whether its system still names what it was
// scaffolded with.
var _ = Describe("Container domains", func() {
	It("scaffolds the system's Domains under docs/agents/domain/ and reports DomainsApplied (#1757)", func() {
		ns := newProjectNamespace()
		addDomainTemplate(ns, "webshop", markdownDomain("webshop"))
		addDomainTemplate(ns, "billing", markdownDomain("billing"))
		webshopOnDomains(ns, "webshop")

		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		Expect(container.Status.ScaffoldedFrom).To(Equal(append(append([]string{}, ordersAPIScaffoldedFrom...),
			"webshop@v1.0.0")), "each Domain's name@ref follows the Connectors")
		Expect(container.Status.ScaffoldedDomains).To(Equal([]string{"webshop"}), "the scaffold records its Domains by name")
		files := gitFake.mainFiles(ns, "orders-api")
		Expect(files).To(HaveKeyWithValue("docs/agents/domain/webshop/README.md",
			HaveField("Data", BeEquivalentTo("# webshop\n"))))
		Expect(files).To(HaveKey("docs/agents/domain/webshop/carts.md"))
		Expect(files["README.md"].Data).NotTo(BeEquivalentTo("# webshop\n"),
			"the Domain's files take its prefix, not the root")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionDomainsApplied)
			g.Expect(cond).NotTo(BeNil(), "the Ready report pass sets DomainsApplied")
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonAppliedAtScaffold))
		}).Should(Succeed())

		By("naming a Domain added after the scaffold: scaffolding happens once")
		var system c4v1beta1.SoftwareSystem
		Expect(k8sClient.Get(ctx, key(ns, "webshop-core"), &system)).To(Succeed())
		orig := system.DeepCopy()
		system.Spec.DomainRefs = []string{"webshop", "billing"}
		Expect(k8sClient.Patch(ctx, &system, client.MergeFrom(orig))).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionDomainsApplied)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonAddedAfterScaffold))
			g.Expect(cond.Message).To(Equal("Domains added after the scaffold: billing; scaffolding happens once"))
		}).Should(Succeed())
		Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
		Expect(container.Status.ScaffoldedFrom).To(HaveLen(len(ordersAPIScaffoldedFrom)+1), "nothing is rescaffolded")
		Expect(gitFake.mainFiles(ns, "orders-api")).NotTo(HaveKey("docs/agents/domain/billing/README.md"))
	})

	It("reports a Domain removed from the system since the scaffold (#1757)", func() {
		ns := newProjectNamespace()
		addDomainTemplate(ns, "webshop", markdownDomain("webshop"))
		addDomainTemplate(ns, "billing", markdownDomain("billing"))
		addDomainTemplate(ns, "payments", markdownDomain("payments"))
		webshopOnDomains(ns, "webshop", "billing")

		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		Expect(container.Status.ScaffoldedDomains).To(Equal([]string{"billing", "webshop"}))
		Expect(gitFake.mainFiles(ns, "orders-api")).To(HaveKey("docs/agents/domain/billing/README.md"))

		By("dropping billing from the system: the scaffold's files stay, the condition says so")
		var system c4v1beta1.SoftwareSystem
		Expect(k8sClient.Get(ctx, key(ns, "webshop-core"), &system)).To(Succeed())
		orig := system.DeepCopy()
		system.Spec.DomainRefs = []string{"webshop"}
		Expect(k8sClient.Patch(ctx, &system, client.MergeFrom(orig))).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionDomainsApplied)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonAddedAfterScaffold))
			g.Expect(cond.Message).To(Equal("Domains removed since the scaffold: billing; scaffolding happens once"))
		}).Should(Succeed())
		Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
		Expect(container.Status.ScaffoldedDomains).To(Equal([]string{"billing", "webshop"}), "the record is the scaffold's")
		Expect(gitFake.mainFiles(ns, "orders-api")).To(HaveKey("docs/agents/domain/billing/README.md"),
			"nothing is removed from the repository")

		By("swapping billing for payments: the message names both changes")
		Expect(k8sClient.Get(ctx, key(ns, "webshop-core"), &system)).To(Succeed())
		orig = system.DeepCopy()
		system.Spec.DomainRefs = []string{"webshop", "payments"}
		Expect(k8sClient.Patch(ctx, &system, client.MergeFrom(orig))).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionDomainsApplied)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Message).To(Equal(
				"Domains added after the scaffold: payments; removed since the scaffold: billing; scaffolding happens once"))
		}).Should(Succeed())
	})

	It("sets no DomainsApplied on a Container scaffolded with no Domains whose system names none (#1757)", func() {
		ns := newProjectNamespace()
		addDomainTemplate(ns, "billing", markdownDomain("billing"))
		webshopOnDomains(ns)

		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		Expect(container.Status.ScaffoldedDomains).To(BeEmpty())
		Expect(container.Status.ScaffoldedFrom).To(Equal(ordersAPIScaffoldedFrom), "no Domain layer was rendered")
		Consistently(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(condition(container.Status.Conditions, c4v1beta1.ConditionDomainsApplied)).To(BeNil())
		}, "1s").Should(Succeed(), "the Ready report passes set no DomainsApplied: nothing scaffolded, nothing named")

		By("naming a Domain later: the condition appears, False")
		var system c4v1beta1.SoftwareSystem
		Expect(k8sClient.Get(ctx, key(ns, "webshop-core"), &system)).To(Succeed())
		orig := system.DeepCopy()
		system.Spec.DomainRefs = []string{"billing"}
		Expect(k8sClient.Patch(ctx, &system, client.MergeFrom(orig))).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionDomainsApplied)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Message).To(Equal("Domains added after the scaffold: billing; scaffolding happens once"))
		}).Should(Succeed())

		By("naming none again: the condition is removed")
		Expect(k8sClient.Get(ctx, key(ns, "webshop-core"), &system)).To(Succeed())
		orig = system.DeepCopy()
		system.Spec.DomainRefs = nil
		Expect(k8sClient.Patch(ctx, &system, client.MergeFrom(orig))).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(condition(container.Status.Conditions, c4v1beta1.ConditionDomainsApplied)).To(BeNil())
		}).Should(Succeed())
		Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
	})

	It("fails a Domain whose template renders a file that is not Markdown, naming it (#1757)", func() {
		ns := newProjectNamespace()
		addDomainTemplate(ns, "notes", scaffold.Tree{
			"README.md": {Data: []byte("# Notes\n")},
			"notes.txt": {Data: []byte("plain\n")},
		})
		webshopOnDomains(ns, "notes")

		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerFailed)
		cond := condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(c4v1beta1.ReasonDomainNotMarkdown))
		Expect(cond.Message).To(Equal(
			"Domain notes renders notes.txt, which is not Markdown: a Domain holds only *.md files"))
	})

	It("fails a Domain whose template renders no README.md (#1757)", func() {
		ns := newProjectNamespace()
		addDomainTemplate(ns, "bare", scaffold.Tree{
			"carts.md": {Data: []byte("# Carts\n")},
		})
		webshopOnDomains(ns, "bare")

		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerFailed)
		cond := condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(c4v1beta1.ReasonDomainNoReadme))
		Expect(cond.Message).To(Equal("Domain bare renders no README.md"))
	})

	It("stays Pending until every Domain the system names exists (#1757)", func() {
		ns := newProjectNamespace()
		webshopOnDomains(ns, "missing-one")

		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerPending)
		cond := condition(container.Status.Conditions, c4v1beta1.ConditionReferencesResolved)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Message).To(Equal("Domain missing-one not found"))

		By("resolving once the Domain appears")
		addDomainTemplate(ns, "missing-one", markdownDomain("missing-one"))
		container = eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		from := container.Status.ScaffoldedFrom
		Expect(from[len(from)-1]).To(Equal("missing-one@v1.0.0"))
	})
})
