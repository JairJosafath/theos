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
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

var _ = Describe("Session", func() {
	It("is owned by its Container, counts while Active and turns Stale without heartbeats", func() {
		ns := newProjectNamespace()
		container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
		session := newSession(ns, "orders-api-k7f3q", "orders-api")
		mustCreate(newSystem(ns, "webshop-core"), container, session)

		By("the client reporting in")
		orig := session.DeepCopy()
		session.Status = c4v1alpha1.SessionStatus{
			Phase:         c4v1alpha1.SessionActive,
			LastHeartbeat: &metav1.Time{Time: time.Now()},
		}
		Expect(k8sClient.Status().Patch(ctx, session, client.MergeFrom(orig))).To(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, session.Name), session)).To(Succeed())
			g.Expect(controlledBy(session, container)).To(BeTrue())
			g.Expect(session.Labels).To(HaveKeyWithValue(c4v1alpha1.LabelContainer, "orders-api"))
			g.Expect(session.Labels).To(HaveKeyWithValue(c4v1alpha1.LabelSoftwareSystem, "webshop-core"))
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.ActiveSessions).To(BeEquivalentTo(1))
		}).Should(Succeed())

		By("the heartbeats stopping")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, session.Name), session)).To(Succeed())
			g.Expect(session.Status.Phase).To(Equal(c4v1alpha1.SessionStale))
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.ActiveSessions).To(BeZero())
		}).Should(Succeed())
	})

	It("keeps its Container for life", func() {
		ns := newProjectNamespace()
		session := newSession(ns, "s", "orders-api")
		mustCreate(session)
		Expect(patch(session, func() { session.Spec.ContainerRef.Name = "other" })).
			To(MatchError(ContainSubstring("containerRef is immutable")))
	})
})
