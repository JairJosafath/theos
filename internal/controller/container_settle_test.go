/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// #43: a model applied in one go may create a Container's Relationships after
// it. The scaffold waits out a settle window after the Container's creation,
// so they count, and the Connector they name is scaffolded.
var _ = Describe("Container scaffold settle window", func() {
	It("scaffolds the Connector of a Relationship created just after the Container (#43)", func() {
		scaffoldSettle.Store(int64(3 * time.Second))
		DeferCleanup(func() { scaffoldSettle.Store(0) })
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		container.Spec.DisplayName = "Orders API"
		mustCreate(newSystem(ns, "webshop-core"), newOrdersDB(ns), container)

		By("waiting, Settling, while the window is open")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerPending))
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonSettling))
		}).Should(Succeed())
		Expect(gitFake.Repo(ns, "orders-api").Commits).To(BeEmpty(), "nothing is scaffolded inside the window")

		By("creating its Relationship to the DataStore a second later, as kubectl apply does")
		time.Sleep(time.Second)
		mustCreate(newRelationship(ns, "orders-api-to-orders-db",
			element(c4v1beta1.ElementContainer, "orders-api"),
			element(c4v1beta1.ElementDataStore, "orders-db")))

		container = eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		Expect(container.Status.ScaffoldedFrom).To(Equal(ordersAPIScaffoldedFrom),
			"the Connector the Relationship names is scaffolded")
		Expect(container.CreationTimestamp.Add(3*time.Second)).To(BeTemporally("<=",
			condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded).LastTransitionTime.Time.Add(time.Second)),
			"not before the window closed")
		Expect(container.Status.AppliedRelationships).To(ContainElement("orders-api-to-orders-db"))
	})
})
