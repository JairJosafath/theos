/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"crypto/rand"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
)

const templatesURL = "http://gitea.gitea.svc.cluster.local:3000/templates/"

// createFactory creates the sample GitProvider and catalog: the smallest set that
// exercises every layer, with the supported types from the design.
func createFactory(ns string) {
	GinkgoHelper()
	service, worker, job := c4v1beta1.ContainerTypeService, c4v1beta1.ContainerTypeWorker, c4v1beta1.ContainerTypeJob
	operator := c4v1beta1.ContainerTypeOperator
	mustCreate(
		&corev1.Secret{
			ObjectMeta: objectMeta(ns, "gitea-credentials"),
			StringData: map[string]string{"username": "c4admin", "token": rand.Text()},
		},
		&c4v1beta1.GitProvider{
			ObjectMeta: objectMeta(ns, "gitea"),
			Spec: c4v1beta1.GitProviderSpec{
				URL:                  "http://gitea.gitea.svc.cluster.local:3000",
				Owner:                ns,
				CredentialsSecretRef: c4v1beta1.LocalRef{Name: "gitea-credentials"},
			},
		},
		&c4v1beta1.Framework{ObjectMeta: objectMeta(ns, "python-fastapi"),
			Spec: newFrameworkSpec(templatesURL+"python-fastapi", "v1.6.0", service)},
		&c4v1beta1.Framework{ObjectMeta: objectMeta(ns, "go-kubebuilder"),
			Spec: newFrameworkSpec(templatesURL+"go-kubebuilder", "v1.0.0", operator)},
		&c4v1beta1.Compute{ObjectMeta: objectMeta(ns, "small"),
			Spec: newCatalogSpec(templatesURL+"small", "v1.1.0", service, worker, job, operator)},
		&c4v1beta1.Deployment{ObjectMeta: objectMeta(ns, "k8s-manifests"),
			Spec: newCatalogSpec(templatesURL+"k8s-manifests", "v1.9.1", service, worker, job)},
		&c4v1beta1.Connector{ObjectMeta: objectMeta(ns, "postgres"),
			Spec: newCatalogSpec(templatesURL+"postgres", "v1.2.0", service, worker)},
	)
}

var _ = Describe("Container catalog choices", func() {
	It("stays Pending until every reference exists, naming the missing ones", func() {
		ns := newProjectNamespace()
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		mustCreate(newSystem(ns, "webshop-core"), container)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerPending))
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionReferencesResolved)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Message).To(Equal(
				"GitProvider gitea, Framework python-fastapi, Compute small, Deployment k8s-manifests not found"))
		}).Should(Succeed())

		createFactory(ns)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(condition(container.Status.Conditions, c4v1beta1.ConditionReferencesResolved)).
				To(HaveField("Status", metav1.ConditionTrue))
			g.Expect(condition(container.Status.Conditions, c4v1beta1.ConditionChoicesValid)).
				To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())
	})

	It("fails a static-site that uses python-fastapi, with a clear message", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "storefront", "webshop-core", c4v1beta1.ContainerTypeStaticSite)
		container.Spec.ComputeRef, container.Spec.DeploymentRef = nil, nil
		mustCreate(newSystem(ns, "webshop-core"), container)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "storefront"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerFailed))
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionChoicesValid)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonUnsupportedType))
			g.Expect(cond.Message).To(Equal("python-fastapi does not support static-site"))
		}).Should(Succeed())
	})

	It("fails a Container on a Framework that deploys itself when it names a Compute or Deployment", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		fargate := &c4v1beta1.Framework{ObjectMeta: objectMeta(ns, "aws-fargate-ts"),
			Spec: newFrameworkSpec(templatesURL+"aws-fargate-ts", "v1.0.0", c4v1beta1.ContainerTypeService)}
		fargate.Spec.DeploysItself = true
		onFargate := func(name string) *c4v1beta1.Container {
			container := newContainer(ns, name, "webshop-core", c4v1beta1.ContainerTypeService)
			container.Spec.FrameworkRef.Name = "aws-fargate-ts"
			return container
		}
		deployed, own := onFargate("orders-api"), onFargate("orders-ecs")
		own.Spec.ComputeRef, own.Spec.DeploymentRef = nil, nil
		mustCreate(newSystem(ns, "webshop-core"), fargate, deployed, own)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), deployed)).To(Succeed())
			g.Expect(deployed.Status.Phase).To(Equal(c4v1beta1.ContainerFailed))
			cond := condition(deployed.Status.Conditions, c4v1beta1.ConditionChoicesValid)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonDeploysItself))
			g.Expect(cond.Message).To(Equal("aws-fargate-ts deploys itself: its template's app (on AWS, its CDK app) " +
				"is the Container's deployment, so it takes no Compute and no Deployment " +
				"but gitea-release, which ships its cloud assembly; " +
				"remove computeRef small and deploymentRef k8s-manifests"))

			g.Expect(k8sClient.Get(ctx, key(ns, "orders-ecs"), own)).To(Succeed())
			g.Expect(condition(own.Status.Conditions, c4v1beta1.ConditionChoicesValid)).
				To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())
	})

	It("accepts an operator on go-kubebuilder and rejects it on a Framework without operator", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		mustCreate(newSystem(ns, "webshop-core"))
		operator := func(name, framework string) *c4v1beta1.Container {
			container := newContainer(ns, name, "webshop-core", c4v1beta1.ContainerTypeOperator)
			container.Spec.FrameworkRef.Name = framework
			container.Spec.DeploymentRef = nil
			return container
		}
		accepted, rejected := operator("orders-operator", "go-kubebuilder"), operator("orders-sync", "python-fastapi")
		mustCreate(accepted, rejected)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-operator"), accepted)).To(Succeed())
			g.Expect(condition(accepted.Status.Conditions, c4v1beta1.ConditionChoicesValid)).
				To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-sync"), rejected)).To(Succeed())
			g.Expect(rejected.Status.Phase).To(Equal(c4v1beta1.ContainerFailed))
			cond := condition(rejected.Status.Conditions, c4v1beta1.ConditionChoicesValid)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonUnsupportedType))
			g.Expect(cond.Message).To(Equal("python-fastapi does not support operator"))
		}).Should(Succeed())
	})

	It("checks Connectors reached through Relationships too", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "nightly-export", "webshop-core", c4v1beta1.ContainerTypeJob)
		container.Spec.FrameworkRef.Name = "python-fastapi"
		container.Spec.DeploymentRef = nil
		mustCreate(
			newSystem(ns, "webshop-core"),
			newOrdersDB(ns),
			newRelationship(ns, "export-reads-orders",
				element(c4v1beta1.ElementContainer, "nightly-export"),
				element(c4v1beta1.ElementDataStore, "orders-db")),
			container,
		)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "nightly-export"), container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionChoicesValid)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Message).To(Equal("python-fastapi and postgres do not support job"))
		}).Should(Succeed())
	})

	It("recovers when the spec is fixed", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "storefront", "webshop-core", c4v1beta1.ContainerTypeStaticSite)
		mustCreate(newSystem(ns, "webshop-core"), container)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "storefront"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerFailed))
		}).Should(Succeed())

		Expect(patch(container, func() { container.Spec.Type = c4v1beta1.ContainerTypeService })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "storefront"), container)).To(Succeed())
			g.Expect(container.Status.Phase).NotTo(Equal(c4v1beta1.ContainerFailed))
			g.Expect(condition(container.Status.Conditions, c4v1beta1.ConditionChoicesValid)).
				To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())
	})

	It("waits for a DataStore named by a Relationship", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		mustCreate(
			newSystem(ns, "webshop-core"),
			newRelationship(ns, "orders-api-to-orders-db",
				element(c4v1beta1.ElementContainer, "orders-api"),
				element(c4v1beta1.ElementDataStore, "orders-db")),
			container,
		)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionReferencesResolved)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Message).To(Equal("DataStore orders-db not found"))
		}).Should(Succeed())
	})
})
