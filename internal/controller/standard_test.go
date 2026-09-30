/*
Copyright 2026.

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// newStandard returns a Standard that fits the named Frameworks, served by the
// fake Git host with an AGENTS.md and a Makefile.
func newStandard(ns, name string, frameworks ...string) *c4v1alpha1.Standard {
	gitFake.addTemplate(templatesURL+name, "v1.0.0", scaffold.Tree{
		"AGENTS.md.tmpl": {Data: []byte("Rules for {{ .Container }}, checked by {{ .Standard }}\n")},
		"Makefile":       {Data: []byte("check:\n\ttrue\n")},
	})
	return &c4v1alpha1.Standard{
		ObjectMeta: objectMeta(ns, name),
		Spec: c4v1alpha1.StandardSpec{
			CatalogEntrySpec:    newCatalogSpec(templatesURL+name, "v1.0.0", c4v1alpha1.ContainerTypeService),
			SupportedFrameworks: frameworks,
		},
	}
}

var _ = Describe("Container Standard", func() {
	It("fails a Container whose Standard does not list its Framework", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
		container.Spec.StandardRef = &c4v1alpha1.LocalRef{Name: "flutter-strict"}
		mustCreate(newSystem(ns, "webshop-core"), newStandard(ns, "flutter-strict", "flutter-app"), container)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerFailed))
			cond := condition(container.Status.Conditions, c4v1alpha1.ConditionChoicesValid)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(c4v1alpha1.ReasonUnsupportedFramework))
			g.Expect(cond.Message).To(Equal("flutter-strict does not support python-fastapi"))
		}).Should(Succeed())
	})

	It("waits for a missing Standard", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
		container.Spec.StandardRef = &c4v1alpha1.LocalRef{Name: "python-strict"}
		mustCreate(newSystem(ns, "webshop-core"), container)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerPending))
			g.Expect(condition(container.Status.Conditions, c4v1alpha1.ConditionReferencesResolved)).
				To(HaveField("Message", "Standard python-strict not found"))
		}).Should(Succeed())

		mustCreate(newStandard(ns, "python-strict", "python-fastapi"))
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))
		}).Should(Succeed())
	})

	It("scaffolds a fitting Standard as the second layer", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
		container.Spec.StandardRef = &c4v1alpha1.LocalRef{Name: "python-strict"}
		mustCreate(newSystem(ns, "webshop-core"), newStandard(ns, "python-strict", "python-fastapi"), container)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))
			g.Expect(container.Status.ScaffoldedFrom).To(HaveLen(4))
			g.Expect(container.Status.ScaffoldedFrom[1]).To(Equal("python-strict@v1.0.0"))
		}).Should(Succeed())
		files := gitFake.repo(ns, "orders-api").files
		Expect(string(files["AGENTS.md"].Data)).To(Equal("Rules for orders-api, checked by python-strict\n"))
	})

	It("is adopted by the Project, like the other catalog entries", func() {
		ns := newProjectNamespace()
		standard := newStandard(ns, "python-strict", "python-fastapi")
		agent := &c4v1alpha1.Agent{ObjectMeta: objectMeta(ns, "claude"),
			Spec: c4v1alpha1.AgentSpec{Image: "c4factory/agent-flutter:dev"}}
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
