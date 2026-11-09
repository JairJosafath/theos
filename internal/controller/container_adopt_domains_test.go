/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"maps"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// domainsStandard is a Standard whose agent guides name the system's Domains,
// as the real Standards' do (#1758), registered in ns.
func domainsStandard(ns string) *c4v1beta1.Standard {
	const guide = "Rules for {{ .Container }}\n{{ range .Domains }}- docs/agents/domain/{{ . }}/README.md\n{{ end }}"
	standard := newStandard(ns, "domains-standard", "python-fastapi")
	gitFake.AddTemplate(standard.Spec.Template.URL, standard.Spec.Template.Ref, scaffold.Tree{
		"AGENTS.md.tmpl":               {Data: []byte(guide)},
		"docs/agents/reviewer.md.tmpl": {Data: []byte("Review " + guide)},
		"Makefile":                     {Data: []byte("check:\n\ttrue\n")},
	})
	return standard
}

// onDomainsStandard puts orders-api on domainsStandard.
func onDomainsStandard(c *c4v1beta1.Container) {
	c.Spec.StandardRef = &c4v1beta1.LocalRef{Name: "domains-standard"}
}

// scaffoldedWithDomain is what orders-api's repository holds when another
// factory scaffolded it on a system naming the Domain webshop: exactly what
// the scaffold wrote, its .theos.yaml naming project ns and keeping its
// scaffoldedFrom, as a repository moved between factories does.
func scaffoldedWithDomain(ns string) scaffold.Tree {
	GinkgoHelper()
	source := newProjectNamespace()
	addDomainTemplate(source, "webshop", markdownDomain("webshop"))
	mustCreate(domainsStandard(source))
	system := newSystem(source, "webshop-core")
	system.Spec.DomainRefs = []string{"webshop"}
	createWebshopOn(source, system, onDomainsStandard)
	from := eventuallyOrdersAPI(source, c4v1beta1.ContainerReady).Status.ScaffoldedFrom
	Expect(from).To(ContainElement("webshop@v1.0.0"))
	files := maps.Clone(gitFake.Repo(source, "orders-api").Files)
	for _, guide := range []string{"AGENTS.md", "docs/agents/reviewer.md"} {
		Expect(string(files[guide].Data)).To(ContainSubstring("docs/agents/domain/webshop/README.md"),
			"the Standard's guides name the Domain")
	}
	id, err := scaffold.ParseIdentity(files[scaffold.IdentityPath].Data)
	Expect(err).NotTo(HaveOccurred())
	Expect(id.ScaffoldedFrom).To(Equal(from))
	id.Project = ns
	data, err := id.Marshal()
	Expect(err).NotTo(HaveOccurred())
	files[scaffold.IdentityPath] = scaffold.File{Data: data}
	return files
}

// adoptOnDomains creates the webshop in ns on a system naming domains, the
// Domain webshop in its catalog, with orders-api adopting its repository.
func adoptOnDomains(ns string, domains ...string) {
	GinkgoHelper()
	addDomainTemplate(ns, "webshop", markdownDomain("webshop"))
	system := newSystem(ns, "webshop-core")
	system.Spec.DomainRefs = domains
	mustCreate(domainsStandard(ns))
	createWebshopOn(ns, system, func(c *c4v1beta1.Container) {
		onDomainsStandard(c)
		c.Spec.Adopt = true
	})
}

// #42: a repository theos scaffolded with Domains renders its Standard's
// agent guides with them, so adoption checks them rendered with the same
// Domains, and another factory can adopt it.
var _ = Describe("Container adoption with Domains", func() {
	It("adopts a repository scaffolded with a Domain, and records it, DomainsApplied (#42)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", scaffoldedWithDomain(ns))
		adoptOnDomains(ns, "webshop")

		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		Expect(condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded).Message).To(
			ContainSubstring("Adopted repository"))
		Expect(gitFake.Repo(ns, "orders-api").Commits).To(HaveLen(1), "nothing pushed")
		Expect(container.Status.ScaffoldedFrom).To(BeEmpty(), "nothing was scaffolded")
		Expect(container.Status.ScaffoldedDomains).To(Equal([]string{"webshop"}),
			"the Domains the Standard's files were rendered with")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionDomainsApplied)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonAppliedAtScaffold))
		}).Should(Succeed())
	})

	It("finds the Domains in .theos.yaml's scaffoldedFrom when the system names none now (#42)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", scaffoldedWithDomain(ns))
		adoptOnDomains(ns)

		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		Expect(container.Status.ScaffoldedDomains).To(Equal([]string{"webshop"}))
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionDomainsApplied)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Message).To(Equal("Domains removed since the scaffold: webshop; scaffolding happens once"))
		}).Should(Succeed())
	})

	It("still fails StandardFilesDiffer when the repository's domain section differs (#42)", func() {
		ns := newProjectNamespace()
		files := scaffoldedWithDomain(ns)
		agents := files["AGENTS.md"]
		agents.Data = []byte(strings.ReplaceAll(string(agents.Data), "docs/agents/domain/webshop/README.md",
			"docs/agents/domain/stock/README.md"))
		files["AGENTS.md"] = agents
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", files)
		adoptOnDomains(ns, "webshop")

		cond := failedAdoption(ns, c4v1beta1.ReasonStandardFilesDiffer)
		Expect(cond.Message).To(ContainSubstring("in AGENTS.md;"), "only the changed guide, against the closest render")
	})
})
