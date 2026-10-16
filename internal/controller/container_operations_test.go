/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// eventuallyOperationsApplied waits for orders-api's OperationsApplied and
// returns the Container.
func eventuallyOperationsApplied(ns string, status metav1.ConditionStatus, reason string) *c4v1alpha1.Container {
	GinkgoHelper()
	var container c4v1alpha1.Container
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
		cond := condition(container.Status.Conditions, c4v1alpha1.ConditionOperationsApplied)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(status))
		g.Expect(cond.Reason).To(Equal(reason))
		g.Expect(cond.ObservedGeneration).To(Equal(container.Generation))
	}).Should(Succeed())
	return &container
}

var _ = Describe("Container operations", func() {
	It("scaffolds without the block on the defaults, and leaves spec as it was written", func() {
		ns := newProjectNamespace()
		createWebshop(ns)

		container := eventuallyOperationsApplied(ns, metav1.ConditionTrue, c4v1alpha1.ReasonAppliedAtScaffold)
		Expect(container.Spec.Operations).To(BeNil(), "the defaults are the factory's, never written into spec")
		Expect(container.Status.Operations).To(Equal(&c4v1alpha1.OperationsSpec{
			Backups:  &c4v1alpha1.OperationsBackups{RetentionDays: new(int32(35))},
			Recovery: &c4v1alpha1.OperationsRecovery{RPO: "1h", RTO: "4h"},
			Alerts:   &c4v1alpha1.OperationsAlerts{},
		}))
	})

	It("scaffolds with the block, then reports a change after the scaffold until it is undone", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
		container.Spec.Operations = &c4v1alpha1.OperationsSpec{
			Backups: &c4v1alpha1.OperationsBackups{RetentionDays: new(int32(0))},
			Alerts:  &c4v1alpha1.OperationsAlerts{Email: "ops@example.com"},
		}
		mustCreate(newSystem(ns, "webshop-core"), container)

		container = eventuallyOperationsApplied(ns, metav1.ConditionTrue, c4v1alpha1.ReasonAppliedAtScaffold)
		Expect(container.Spec.Operations).To(Equal(&c4v1alpha1.OperationsSpec{
			Backups: &c4v1alpha1.OperationsBackups{RetentionDays: new(int32(0))},
			Alerts:  &c4v1alpha1.OperationsAlerts{Email: "ops@example.com"},
		}), "a client reads back only the fields it set")
		Expect(container.Status.Operations.Backups.RetentionDays).To(HaveValue(BeZero()))
		Expect(container.Status.Operations.Recovery.RPO).To(Equal("1h"))
		Expect(container.Status.Operations.Alerts.Email).To(Equal("ops@example.com"))

		By("changing the block after the scaffold")
		pushes := gitFake.pushCount()
		Expect(patch(container, func() {
			container.Spec.Operations.Recovery = &c4v1alpha1.OperationsRecovery{RPO: "5m"}
		})).To(Succeed())
		container = eventuallyOperationsApplied(ns, metav1.ConditionFalse, c4v1alpha1.ReasonChangedAfterScaffold)
		Expect(condition(container.Status.Conditions, c4v1alpha1.ConditionOperationsApplied).Message).
			To(ContainSubstring("infra/operations.ts"))
		Expect(container.Status.Operations.Recovery.RPO).To(Equal("1h"), "status keeps what the scaffold rendered")
		Consistently(gitFake.pushCount).WithTimeout(time.Second).Should(Equal(pushes),
			"nothing reaches the repository before step 12")

		By("writing the default out, which is the block the scaffold rendered")
		Expect(patch(container, func() {
			container.Spec.Operations.Recovery = &c4v1alpha1.OperationsRecovery{RPO: "1h"}
		})).To(Succeed())
		eventuallyOperationsApplied(ns, metav1.ConditionTrue, c4v1alpha1.ReasonAppliedAtScaffold)
	})

	DescribeTable("refuses a value outside the block's patterns and ranges",
		func(ops c4v1alpha1.OperationsSpec, field string) {
			ns := newProjectNamespace()
			container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
			container.Spec.Operations = &ops
			Expect(k8sClient.Create(ctx, container)).To(MatchError(ContainSubstring("spec.operations." + field)))
		},
		Entry("a negative retention", c4v1alpha1.OperationsSpec{
			Backups: &c4v1alpha1.OperationsBackups{RetentionDays: new(int32(-1))}}, "backups.retentionDays"),
		Entry("a retention over ten years", c4v1alpha1.OperationsSpec{
			Backups: &c4v1alpha1.OperationsBackups{RetentionDays: new(int32(3651))}}, "backups.retentionDays"),
		Entry("a region that isn't one", c4v1alpha1.OperationsSpec{
			Backups: &c4v1alpha1.OperationsBackups{CopyToRegion: "Europe"}}, "backups.copyToRegion"),
		Entry("an objective in words", c4v1alpha1.OperationsSpec{
			Recovery: &c4v1alpha1.OperationsRecovery{RPO: "1 hour"}}, "recovery.rpo"),
		Entry("an objective in weeks", c4v1alpha1.OperationsSpec{
			Recovery: &c4v1alpha1.OperationsRecovery{RTO: "1w"}}, "recovery.rto"),
		Entry("an email that isn't one", c4v1alpha1.OperationsSpec{
			Alerts: &c4v1alpha1.OperationsAlerts{Email: "the ops team"}}, "alerts.email"),
	)
})
