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

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
)

var _ = Describe("Relationship", func() {
	It("can't point to itself", func() {
		ns := newProjectNamespace()
		rel := newRelationship(ns, "loop",
			element(c4v1beta1.ElementContainer, "orders-api"),
			element(c4v1beta1.ElementContainer, "orders-api"))
		Expect(k8sClient.Create(ctx, rel)).To(MatchError(ContainSubstring("a Relationship cannot point to itself")))
	})

	It("keeps its source for life", func() {
		ns := newProjectNamespace()
		rel := newRelationship(ns, "uses",
			element(c4v1beta1.ElementPerson, "customer"),
			element(c4v1beta1.ElementSoftwareSystem, "webshop-core"))
		mustCreate(rel)
		Expect(patch(rel, func() { rel.Spec.Source.Name = "admin" })).
			To(MatchError(ContainSubstring("source is immutable")))
	})

	// A link changes what it means when its target changes; recreate it instead,
	// and Applied compares the new target with what was scaffolded.
	It("keeps its target for life", func() {
		ns := newProjectNamespace()
		rel := newRelationship(ns, "orders-api-to-orders-db",
			element(c4v1beta1.ElementContainer, "orders-api"),
			element(c4v1beta1.ElementDataStore, "orders-db"))
		mustCreate(rel)
		Expect(patch(rel, func() { rel.Spec.Target.Name = "cache-db" })).
			To(MatchError(ContainSubstring("target is immutable")))
	})

	It("is owned by its source and tells the story", func() {
		ns := newProjectNamespace()
		person := newPerson(ns, "customer")
		rel := newRelationship(ns, "customer-uses-webshop",
			element(c4v1beta1.ElementPerson, "customer"),
			element(c4v1beta1.ElementSoftwareSystem, "webshop-core"))
		mustCreate(person, rel)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, rel.Name), rel)).To(Succeed())
			g.Expect(controlledBy(rel, person)).To(BeTrue())
			cond := condition(rel.Status.Conditions, c4v1beta1.ConditionReferencesResolved)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Message).To(Equal("SoftwareSystem webshop-core not found"))
			g.Expect(condition(rel.Status.Conditions, c4v1beta1.ConditionApplied)).To(BeNil(),
				"story-only links have no Applied condition")
		}).Should(Succeed())

		mustCreate(newSystem(ns, "webshop-core"))
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, rel.Name), rel)).To(Succeed())
			g.Expect(condition(rel.Status.Conditions, c4v1beta1.ConditionReferencesResolved)).
				To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())
	})

	It("waits for the scaffold before a Container to DataStore link counts as applied", func() {
		ns := newProjectNamespace()
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		rel := newRelationship(ns, "orders-api-to-orders-db",
			element(c4v1beta1.ElementContainer, "orders-api"),
			element(c4v1beta1.ElementDataStore, "orders-db"))
		mustCreate(newSystem(ns, "webshop-core"), container, newOrdersDB(ns), rel)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, rel.Name), rel)).To(Succeed())
			g.Expect(controlledBy(rel, container)).To(BeTrue())
			cond := condition(rel.Status.Conditions, c4v1beta1.ConditionApplied)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonWaiting))
		}).Should(Succeed())
	})
})

var _ = Describe("DataStore", func() {
	It("keeps its SoftwareSystem for life", func() {
		ns := newProjectNamespace()
		store := newOrdersDB(ns)
		mustCreate(store)
		Expect(patch(store, func() { store.Spec.SoftwareSystemRef.Name = "another-system" })).
			To(MatchError(ContainSubstring("softwareSystemRef is immutable")))
	})

	It("keeps its Connector for life", func() {
		ns := newProjectNamespace()
		store := newOrdersDB(ns)
		mustCreate(store)
		Expect(patch(store, func() { store.Spec.ConnectorRef.Name = "redis" })).
			To(MatchError(ContainSubstring("connectorRef is immutable")))
	})

	It("is a child of its SoftwareSystem and resolves its Connector", func() {
		ns := newProjectNamespace()
		system := newSystem(ns, "webshop-core")
		store := newOrdersDB(ns)
		mustCreate(system, store)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-db"), store)).To(Succeed())
			g.Expect(controlledBy(store, system)).To(BeTrue())
			cond := condition(store.Status.Conditions, c4v1beta1.ConditionReferencesResolved)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Message).To(Equal("Connector postgres not found"))
		}).Should(Succeed())

		mustCreate(&c4v1beta1.Connector{ObjectMeta: objectMeta(ns, "postgres"),
			Spec: newCatalogSpec("http://gitea:3000/templates/postgres", "v1.1.0", c4v1beta1.ContainerTypeService)})
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-db"), store)).To(Succeed())
			g.Expect(condition(store.Status.Conditions, c4v1beta1.ConditionReferencesResolved)).
				To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())
	})
})
