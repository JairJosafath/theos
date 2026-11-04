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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

var _ = Describe("Container checks", func() {
	It("defaults to EveryChange and takes a change of the field", func() {
		ns := newProjectNamespace()
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		mustCreate(container)
		Expect(container.Spec.Checks).To(Equal(c4v1beta1.ChecksEveryChange))

		Expect(patch(container, func() { container.Spec.Checks = c4v1beta1.ChecksBeforeRelease })).To(Succeed())
		Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
		Expect(container.Spec.Checks).To(Equal(c4v1beta1.ChecksBeforeRelease))
	})

	It("refuses an unknown value", func() {
		ns := newProjectNamespace()
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		container.Spec.Checks = "Nightly"
		Expect(k8sClient.Create(ctx, container)).To(MatchError(ContainSubstring("Unsupported value")))
	})

	It("says when its Standard can't run the full check only before a release", func() {
		ns := newProjectNamespace()
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		container.Spec.StandardRef = &c4v1beta1.LocalRef{Name: "strict"}
		container.Spec.Checks = c4v1beta1.ChecksBeforeRelease
		standard := newStandard(ns, "strict", "python-fastapi")
		mustCreate(newSystem(ns, "webshop-core"), standard, container)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionChecksApplied)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonStandardRunsFullChecks))
			g.Expect(cond.Message).To(ContainSubstring("The Standard strict"))
		}).Should(Succeed())

		// The Standard learning to do it applies the Container's choice.
		Expect(patch(standard, func() { standard.Spec.ChecksBeforeRelease = true })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionChecksApplied)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonBeforeRelease))
		}).Should(Succeed())
	})

	It("applies EveryChange on any Standard, and has no condition without one", func() {
		ns := newProjectNamespace()
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		container.Spec.StandardRef = &c4v1beta1.LocalRef{Name: "strict"}
		mustCreate(newSystem(ns, "webshop-core"), newStandard(ns, "strict", "python-fastapi"), container)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionChecksApplied)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonEveryChange))
		}).Should(Succeed())

		Expect(patch(container, func() { container.Spec.StandardRef = nil })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(condition(container.Status.Conditions, c4v1beta1.ConditionChecksApplied)).To(BeNil())
		}).Should(Succeed())
	})
})
