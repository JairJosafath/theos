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
)

var _ = Describe("Container", func() {
	It("becomes a child of its SoftwareSystem, with ancestor labels", func() {
		ns := newProjectNamespace()
		system := newSystem(ns, "webshop-core")
		container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
		mustCreate(system, container)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(controlledBy(container, system)).To(BeTrue())
			g.Expect(container.Labels).To(HaveKeyWithValue(c4v1alpha1.LabelProject, ns))
			g.Expect(container.Labels).To(HaveKeyWithValue(c4v1alpha1.LabelSoftwareSystem, "webshop-core"))
		}).Should(Succeed())

		// ownerReferences carry blockOwnerDeletion, so deleting the system cascades
		// through the garbage collector (checked for real in the Kind cluster).
		ref := metav1.GetControllerOf(container)
		Expect(ref.BlockOwnerDeletion).To(HaveValue(BeTrue()))
	})

	It("waits for a missing SoftwareSystem, then is adopted when it appears", func() {
		ns := newProjectNamespace()
		container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
		mustCreate(container)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerPending))
			cond := condition(container.Status.Conditions, c4v1alpha1.ConditionReferencesResolved)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Message).To(Equal("SoftwareSystem webshop-core not found"))
		}).Should(Succeed())

		system := newSystem(ns, "webshop-core")
		mustCreate(system)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(controlledBy(container, system)).To(BeTrue())
		}).Should(Succeed())
	})

	It("keeps its SoftwareSystem for life", func() {
		ns := newProjectNamespace()
		container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
		mustCreate(container)

		Expect(patch(container, func() { container.Spec.SoftwareSystemRef.Name = "another-system" })).
			To(MatchError(ContainSubstring("softwareSystemRef is immutable")))
	})

	It("rejects a type outside the fixed list", func() {
		ns := newProjectNamespace()
		container := newContainer(ns, "orders-api", "webshop-core", "lambda")
		Expect(k8sClient.Create(ctx, container)).To(MatchError(ContainSubstring("Unsupported value")))
	})
})

var _ = Describe("SoftwareSystem", func() {
	It("rolls up its Containers and DataStores", func() {
		ns := newProjectNamespace()
		mustCreate(
			newSystem(ns, "webshop-core"),
			newSystem(ns, "back-office"),
			newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService),
			newContainer(ns, "storefront", "webshop-core", c4v1alpha1.ContainerTypeStaticSite),
			newContainer(ns, "reports", "back-office", c4v1alpha1.ContainerTypeJob),
			newOrdersDB(ns),
		)
		Eventually(func(g Gomega) {
			var system c4v1alpha1.SoftwareSystem
			g.Expect(k8sClient.Get(ctx, key(ns, "webshop-core"), &system)).To(Succeed())
			g.Expect(system.Status.Containers).To(BeEquivalentTo(2))
			g.Expect(system.Status.DataStores).To(BeEquivalentTo(1))
			g.Expect(system.Status.Summary).To(Equal("0 of 2 containers Ready, 0 active sessions"))
		}).Should(Succeed())
	})
})
