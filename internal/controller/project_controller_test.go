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
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

var _ = Describe("Project", func() {
	It("must be named after its namespace", func() {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "story-"}}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())

		// The policy takes a moment to become active after it is created.
		Eventually(func() error {
			return k8sClient.Create(ctx, &c4v1alpha1.Project{ObjectMeta: objectMeta(ns.Name, "not-the-namespace")})
		}).Should(MatchError(ContainSubstring("a Project must be named after its namespace")))

		Expect(k8sClient.Create(ctx, &c4v1alpha1.Project{ObjectMeta: objectMeta(ns.Name, ns.Name)})).To(Succeed())
	})

	It("adopts the kinds it owns directly", func() {
		ns := newProjectNamespace()
		person := newPerson(ns, "customer")
		system := newSystem(ns, "webshop-core")
		provider := &c4v1alpha1.GitProvider{
			ObjectMeta: objectMeta(ns, "gitea"),
			Spec: c4v1alpha1.GitProviderSpec{
				URL: "http://gitea:3000", Owner: ns, CredentialsSecretRef: c4v1alpha1.LocalRef{Name: "creds"},
			},
		}
		framework := &c4v1alpha1.Framework{ObjectMeta: objectMeta(ns, "python-fastapi"),
			Spec: newCatalogSpec("http://gitea:3000/templates/python-fastapi", "v1.3.0", c4v1alpha1.ContainerTypeService)}
		compute := &c4v1alpha1.Compute{ObjectMeta: objectMeta(ns, "small"),
			Spec: newCatalogSpec("http://gitea:3000/templates/small", "v1.0.0", c4v1alpha1.ContainerTypeService)}
		deployment := &c4v1alpha1.Deployment{ObjectMeta: objectMeta(ns, "k8s-manifests"),
			Spec: newCatalogSpec("http://gitea:3000/templates/k8s-manifests", "v1.1.0", c4v1alpha1.ContainerTypeService)}
		connector := &c4v1alpha1.Connector{ObjectMeta: objectMeta(ns, "postgres"),
			Spec: newCatalogSpec("http://gitea:3000/templates/postgres", "v1.1.0", c4v1alpha1.ContainerTypeService)}
		mustCreate(person, system, provider, framework, compute, deployment, connector)

		var project c4v1alpha1.Project
		Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
		for _, obj := range []client.Object{person, system, provider, framework, compute, deployment, connector} {
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, key(ns, obj.GetName()), obj)).To(Succeed())
				g.Expect(controlledBy(obj, &project)).To(BeTrue(), "%T %s should be owned by the Project", obj, obj.GetName())
				g.Expect(obj.GetLabels()).To(HaveKeyWithValue(c4v1alpha1.LabelProject, ns))
			}).Should(Succeed())
		}
	})

	It("gives the namespace a ServiceAccount that may only work with Sessions", func() {
		ns := newProjectNamespace()
		var project c4v1alpha1.Project
		Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())

		var account corev1.ServiceAccount
		var role rbacv1.Role
		var binding rbacv1.RoleBinding
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, SessionAccountName), &account)).To(Succeed())
			g.Expect(k8sClient.Get(ctx, key(ns, SessionAccountName), &role)).To(Succeed())
			g.Expect(k8sClient.Get(ctx, key(ns, SessionAccountName), &binding)).To(Succeed())
		}).Should(Succeed())
		Expect(controlledBy(&account, &project)).To(BeTrue())
		Expect(controlledBy(&role, &project)).To(BeTrue())
		Expect(controlledBy(&binding, &project)).To(BeTrue())
		for _, rule := range role.Rules {
			Expect(rule.Resources).To(Or(ConsistOf("sessions"), ConsistOf("sessions/status")))
		}
		Expect(binding.Subjects).To(ConsistOf(HaveField("Name", SessionAccountName)))

		By("repairing a Role someone changed")
		Expect(patch(&role, func() {
			role.Rules = append(role.Rules, rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get"}})
		})).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, SessionAccountName), &role)).To(Succeed())
			g.Expect(role.Rules).To(HaveLen(2))
		}).Should(Succeed())
	})

	It("rolls up containers and sessions", func() {
		ns := newProjectNamespace()
		mustCreate(
			newSystem(ns, "webshop-core"),
			newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService),
			newContainer(ns, "storefront", "webshop-core", c4v1alpha1.ContainerTypeStaticSite),
		)
		Eventually(func(g Gomega) {
			var project c4v1alpha1.Project
			g.Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
			g.Expect(project.Status.SoftwareSystems).To(BeEquivalentTo(1))
			g.Expect(project.Status.Containers).To(BeEquivalentTo(2))
			g.Expect(project.Status.Summary).To(Equal("0 of 2 containers Ready, 0 active sessions"))
			g.Expect(condition(project.Status.Conditions, ConditionNameMatchesNamespace)).
				To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())
	})
})
