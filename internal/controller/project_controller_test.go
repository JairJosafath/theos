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
	corev1 "k8s.io/api/core/v1"
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
			Spec: newCatalogSpec("http://gitea:3000/templates/python-fastapi", "v1.2.0", c4v1alpha1.ContainerTypeService)}
		compute := &c4v1alpha1.Compute{ObjectMeta: objectMeta(ns, "small"),
			Spec: newCatalogSpec("http://gitea:3000/templates/small", "v1.0.0", c4v1alpha1.ContainerTypeService)}
		deployment := &c4v1alpha1.Deployment{ObjectMeta: objectMeta(ns, "k8s-manifests"),
			Spec: newCatalogSpec("http://gitea:3000/templates/k8s-manifests", "v1.0.0", c4v1alpha1.ContainerTypeService)}
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
			g.Expect(condition(project.Status.Conditions, ConditionNameMatchesNamespace).Status).
				To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())
	})
})
