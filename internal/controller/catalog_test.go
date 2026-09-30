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

const templatesURL = "http://gitea.gitea.svc.cluster.local:3000/templates/"

// createFactory creates the sample GitProvider and catalog: the smallest set that
// exercises every layer, with the supported types from the design.
func createFactory(ns string) {
	GinkgoHelper()
	service, worker, job := c4v1alpha1.ContainerTypeService, c4v1alpha1.ContainerTypeWorker, c4v1alpha1.ContainerTypeJob
	mustCreate(
		&c4v1alpha1.GitProvider{
			ObjectMeta: objectMeta(ns, "gitea"),
			Spec: c4v1alpha1.GitProviderSpec{
				URL:                  "http://gitea.gitea.svc.cluster.local:3000",
				Owner:                ns,
				CredentialsSecretRef: c4v1alpha1.LocalRef{Name: "gitea-credentials"},
			},
		},
		&c4v1alpha1.Framework{ObjectMeta: objectMeta(ns, "python-fastapi"),
			Spec: newCatalogSpec(templatesURL+"python-fastapi", "v1.2.0", service)},
		&c4v1alpha1.Compute{ObjectMeta: objectMeta(ns, "small"),
			Spec: newCatalogSpec(templatesURL+"small", "v1.0.0", service, worker, job)},
		&c4v1alpha1.Deployment{ObjectMeta: objectMeta(ns, "k8s-manifests"),
			Spec: newCatalogSpec(templatesURL+"k8s-manifests", "v1.0.0", service, worker)},
		&c4v1alpha1.Connector{ObjectMeta: objectMeta(ns, "postgres"),
			Spec: newCatalogSpec(templatesURL+"postgres", "v1.1.0", service, worker)},
	)
}

var _ = Describe("Container catalog choices", func() {
	It("stays Pending until every reference exists, naming the missing ones", func() {
		ns := newProjectNamespace()
		container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
		mustCreate(newSystem(ns, "webshop-core"), container)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerPending))
			cond := condition(container.Status.Conditions, c4v1alpha1.ConditionReferencesResolved)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Message).To(Equal(
				"GitProvider gitea, Framework python-fastapi, Compute small, Deployment k8s-manifests not found"))
		}).Should(Succeed())

		createFactory(ns)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(condition(container.Status.Conditions, c4v1alpha1.ConditionReferencesResolved)).
				To(HaveField("Status", metav1.ConditionTrue))
			g.Expect(condition(container.Status.Conditions, c4v1alpha1.ConditionChoicesValid)).
				To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())
	})

	It("fails a static-site that uses python-fastapi, with a clear message", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "storefront", "webshop-core", c4v1alpha1.ContainerTypeStaticSite)
		container.Spec.ComputeRef, container.Spec.DeploymentRef = nil, nil
		mustCreate(newSystem(ns, "webshop-core"), container)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "storefront"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerFailed))
			cond := condition(container.Status.Conditions, c4v1alpha1.ConditionChoicesValid)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(c4v1alpha1.ReasonUnsupportedType))
			g.Expect(cond.Message).To(Equal("python-fastapi does not support static-site"))
		}).Should(Succeed())
	})

	It("checks Connectors reached through Relationships too", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "nightly-export", "webshop-core", c4v1alpha1.ContainerTypeJob)
		container.Spec.FrameworkRef.Name = "python-fastapi"
		container.Spec.DeploymentRef = nil
		mustCreate(
			newSystem(ns, "webshop-core"),
			newDataStore(ns, "orders-db", "webshop-core", "postgres"),
			newRelationship(ns, "export-reads-orders",
				element(c4v1alpha1.ElementContainer, "nightly-export"),
				element(c4v1alpha1.ElementDataStore, "orders-db")),
			container,
		)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "nightly-export"), container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1alpha1.ConditionChoicesValid)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Message).To(Equal("python-fastapi and postgres do not support job"))
		}).Should(Succeed())
	})

	It("recovers when the spec is fixed", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "storefront", "webshop-core", c4v1alpha1.ContainerTypeStaticSite)
		mustCreate(newSystem(ns, "webshop-core"), container)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "storefront"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerFailed))
		}).Should(Succeed())

		container.Spec.Type = c4v1alpha1.ContainerTypeService
		Expect(k8sClient.Update(ctx, container)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "storefront"), container)).To(Succeed())
			g.Expect(container.Status.Phase).NotTo(Equal(c4v1alpha1.ContainerFailed))
			g.Expect(condition(container.Status.Conditions, c4v1alpha1.ConditionChoicesValid)).
				To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())
	})

	It("waits for a DataStore named by a Relationship", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
		mustCreate(
			newSystem(ns, "webshop-core"),
			newRelationship(ns, "orders-api-to-orders-db",
				element(c4v1alpha1.ElementContainer, "orders-api"),
				element(c4v1alpha1.ElementDataStore, "orders-db")),
			container,
		)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1alpha1.ConditionReferencesResolved)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Message).To(Equal("DataStore orders-db not found"))
		}).Should(Succeed())
	})
})
