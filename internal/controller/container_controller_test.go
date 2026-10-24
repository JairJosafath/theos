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
		// through the garbage collector, which envtest lacks and Go tests trust.
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

	It("accepts the operator type", func() {
		ns := newProjectNamespace()
		mustCreate(newContainer(ns, "backup-operator", "webshop-core", c4v1alpha1.ContainerTypeOperator))
	})

	It("accepts the function type", func() {
		ns := newProjectNamespace()
		mustCreate(newContainer(ns, "orders-fn", "webshop-core", c4v1alpha1.ContainerTypeFunction))
	})

	It("rejects a type outside the fixed list", func() {
		ns := newProjectNamespace()
		container := newContainer(ns, "orders-api", "webshop-core", "lambda")
		Expect(k8sClient.Create(ctx, container)).To(MatchError(ContainSubstring("Unsupported value")))
	})

	It("takes a license only from the fixed set of SPDX ids (#1859)", func() {
		ns := newProjectNamespace()
		container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
		container.Spec.License = "WTFPL"
		Expect(k8sClient.Create(ctx, container)).To(MatchError(ContainSubstring("license")))
		container.Spec.License = "MIT"
		Expect(k8sClient.Create(ctx, container)).To(Succeed())
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
