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

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// readyCLIContainer makes Ready cli Container orders-cli in ns, on Framework
// go-cli, and its Crew, then points it at Deployment gitea-release, as
// selfDeploying does: the fake Gitea holds no gitea-release template to
// scaffold from, and a Container is scaffolded once.
func readyCLIContainer(ns string) {
	GinkgoHelper()
	createFactory(ns)
	container := newContainer(ns, "orders-cli", "webshop-core", c4v1alpha1.ContainerTypeCLI)
	container.Spec.FrameworkRef = c4v1alpha1.LocalRef{Name: "go-cli"}
	container.Spec.ComputeRef, container.Spec.DeploymentRef = nil, nil
	crew := newCrew(ns)
	crew.Name, crew.Spec.ContainerRef.Name = "orders-cli", "orders-cli"
	mustCreate(
		&c4v1alpha1.Framework{ObjectMeta: objectMeta(ns, "go-cli"),
			Spec: newFrameworkSpec(templatesURL+"go-cli", "v1.0.0", c4v1alpha1.ContainerTypeCLI)},
		&c4v1alpha1.Deployment{ObjectMeta: objectMeta(ns, model.DeploymentGiteaRelease),
			Spec: newCatalogSpec(templatesURL+model.DeploymentGiteaRelease, "v1.0.0", c4v1alpha1.ContainerTypeCLI)},
		newSystem(ns, "webshop-core"), container, crew)
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(ns, "orders-cli"), container)).To(Succeed())
		g.Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerReady), "%v", container.Status.Conditions)
	}).Should(Succeed())
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(ns, "orders-cli"), container)).To(Succeed())
		container.Spec.DeploymentRef = &c4v1alpha1.LocalRef{Name: model.DeploymentGiteaRelease}
		g.Expect(k8sClient.Update(ctx, container)).To(Succeed())
	}).Should(Succeed())
}

var _ = Describe("Container deploys to a gitlab target (#1804)", func() {
	It("ships a cli Container's release and its files to the project, and fills status.deploys[]", func() {
		ns := newProjectNamespace()
		readyCLIContainer(ns)
		project := gitlabFake.AddProject("acme/"+ns, "glpat-"+ns)
		dt := newGitLabTarget(ns, "staging", "staging-gitlab", "acme/"+ns)
		mustCreate(newDeploySecret(ns, "staging-gitlab", map[string]string{"token": "glpat-" + ns}), dt)
		deployReady(dt, metav1.ConditionTrue, model.DeployTargetReady)

		build := []byte("the build of v0.1.0")
		gitFake.setGiteaRelease(ns, "orders-cli", "v0.1.0", "Notes of v0.1.0",
			fakeFile{"orders-cli-v0.1.0.tar.gz", build})
		crewReleaseOf(ns, "orders-cli", "v0.1.0", model.RunSucceeded)
		setDeployOf(ns, "orders-cli", c4v1alpha1.ContainerDeploy{Target: "staging"})

		Eventually(func(g Gomega) {
			d, cond := deployOfContainer(g, ns, "orders-cli", "staging")
			g.Expect(d.Phase).To(Equal(model.DeployPhaseDeployed), d.Message)
			g.Expect(d.Version).To(Equal("v0.1.0"))
			g.Expect(cond.Reason).To(Equal(model.DeployDeployed), cond.Message)
		}, deployWait).Should(Succeed())
		var tags []string
		for _, r := range project.Releases() {
			tags = append(tags, r.Tag)
		}
		Expect(tags).To(ConsistOf("orders-cli-v0.1.0"))
		Expect(project.Files("orders-cli", "v0.1.0")).To(
			HaveKeyWithValue("orders-cli-v0.1.0.tar.gz", int64(len(build))))
	})
})
