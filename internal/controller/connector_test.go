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
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// newWiredConnector returns a Connector for services that the named Frameworks
// wire, served by the fake Git host with one file.
func newWiredConnector(ns, name string, wiredBy ...string) *c4v1beta1.Connector {
	gitFake.addTemplate(templatesURL+name, "v1.0.0", scaffold.Tree{
		"infra/connector_" + name + ".ts": {Data: []byte("export const " + name + " = true\n")},
	})
	return &c4v1beta1.Connector{
		ObjectMeta: objectMeta(ns, name),
		Spec: c4v1beta1.ConnectorSpec{
			CatalogEntrySpec: newCatalogSpec(templatesURL+name, "v1.0.0", c4v1beta1.ContainerTypeService),
			WiredBy:          wiredBy,
		},
	}
}

// linkToStore creates a DataStore on the Connector and the Relationship from
// orders-api to it; it runs before the Container exists, so the scaffold
// sees the Connector (a Relationship added later is AddedAfterScaffold).
func linkToStore(ns, connector string) {
	mustCreate(
		&c4v1beta1.DataStore{
			ObjectMeta: objectMeta(ns, "orders-db"),
			Spec: c4v1beta1.DataStoreSpec{
				SoftwareSystemRef: c4v1beta1.LocalRef{Name: "webshop-core"},
				ConnectorRef:      c4v1beta1.LocalRef{Name: connector},
			},
		},
		newRelationship(ns, "orders-api-to-orders-db",
			element(c4v1beta1.ElementContainer, "orders-api"),
			element(c4v1beta1.ElementDataStore, "orders-db")),
	)
}

// The plan's decision 7 (docs/plans/aws-rds.md, #1920): a Connector that lists
// the Frameworks wiring it is refused for a Container on any other Framework.
var _ = Describe("Container Connector wiring", func() {
	It("fails a Container whose Framework is not among the Connector's wiredBy", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		mustCreate(newSystem(ns, "webshop-core"), newWiredConnector(ns, "dynamodb", "aws-lambda-ts", "aws-lambda-go"))
		linkToStore(ns, "dynamodb")
		mustCreate(container)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerFailed))
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionChoicesValid)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonNotWired))
			g.Expect(cond.Message).To(Equal("dynamodb is not wired by python-fastapi, only by aws-lambda-ts and aws-lambda-go"))
		}).Should(Succeed())
	})

	It("accepts a Container whose Framework is among the Connector's wiredBy", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		mustCreate(newSystem(ns, "webshop-core"), newWiredConnector(ns, "dynamodb", "aws-lambda-ts", "python-fastapi"))
		linkToStore(ns, "dynamodb")
		mustCreate(container)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(condition(container.Status.Conditions, c4v1beta1.ConditionChoicesValid)).
				To(HaveField("Status", metav1.ConditionTrue))
			g.Expect(container.Status.AppliedConnectors).To(HaveLen(1))
		}).Should(Succeed())
	})

	It("accepts a Container on any Framework when wiredBy is unset", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		mustCreate(newSystem(ns, "webshop-core"), newWiredConnector(ns, "redis"))
		linkToStore(ns, "redis")
		mustCreate(container)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(condition(container.Status.Conditions, c4v1beta1.ConditionChoicesValid)).
				To(HaveField("Status", metav1.ConditionTrue))
			g.Expect(container.Status.AppliedConnectors).To(HaveLen(1))
		}).Should(Succeed())
	})
})
