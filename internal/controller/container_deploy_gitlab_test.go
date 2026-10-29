/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/adapters/gitlab/gitlabtest"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
	"github.com/JairJosafath/c4factory/internal/ports/fake"
)

// readyCLIContainer makes Ready cli Container orders-cli in ns, on Framework
// go-cli, and its Crew, then points it at Deployment gitea-release, as
// selfDeploying does: the fake Gitea holds no gitea-release template to
// scaffold from, and a Container is scaffolded once.
func readyCLIContainer(ns string) {
	GinkgoHelper()
	createFactory(ns)
	container := newContainer(ns, "orders-cli", "webshop-core", c4v1beta1.ContainerTypeCLI)
	container.Spec.FrameworkRef = c4v1beta1.LocalRef{Name: "go-cli"}
	container.Spec.ComputeRef, container.Spec.DeploymentRef = nil, nil
	crew := newCrew(ns)
	crew.Name, crew.Spec.ContainerRef.Name = "orders-cli", "orders-cli"
	mustCreate(
		&c4v1beta1.Framework{ObjectMeta: objectMeta(ns, "go-cli"),
			Spec: newFrameworkSpec(templatesURL+"go-cli", "v1.0.0", c4v1beta1.ContainerTypeCLI)},
		&c4v1beta1.Deployment{ObjectMeta: objectMeta(ns, model.DeploymentGiteaRelease),
			Spec: newCatalogSpec(templatesURL+model.DeploymentGiteaRelease, "v1.0.0", c4v1beta1.ContainerTypeCLI)},
		newSystem(ns, "webshop-core"), container, crew)
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(ns, "orders-cli"), container)).To(Succeed())
		g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady), "%v", container.Status.Conditions)
	}).Should(Succeed())
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(ns, "orders-cli"), container)).To(Succeed())
		container.Spec.DeploymentRef = &c4v1beta1.LocalRef{Name: model.DeploymentGiteaRelease}
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
		gitFake.SetRelease(ns, "orders-cli", "v0.1.0", "Notes of v0.1.0",
			fake.ReleaseFile{Name: "orders-cli-v0.1.0.tar.gz", Data: build})
		crewReleaseOf(ns, "orders-cli", "v0.1.0", model.RunSucceeded)
		setDeployOf(ns, "orders-cli", c4v1beta1.ContainerDeploy{Target: "staging"})

		Eventually(func(g Gomega) {
			d, cond := deployOfContainer(g, ns, "orders-cli", "staging")
			g.Expect(d.Phase).To(Equal(model.DeployPhaseDeployed), d.Message)
			g.Expect(d.Version).To(Equal("v0.1.0"))
			g.Expect(cond.Reason).To(Equal(model.DeployDeployed), cond.Message)
		}).Should(Succeed())
		releases := project.Releases()
		tags := make([]string, 0, len(releases))
		for _, r := range releases {
			tags = append(tags, r.Tag)
		}
		Expect(tags).To(ConsistOf("orders-cli-v0.1.0"))
		Expect(project.Files("orders-cli", "orders-cli-v0.1.0")).To(
			HaveKeyWithValue("orders-cli-v0.1.0.tar.gz", int64(len(build))))
	})
})

// gitlabSourceTarget makes the gitlab target name in ns for a fresh project
// on gitlabFake with source set, Ready, and returns the project.
func gitlabSourceTarget(ns, name string) *gitlabtest.Project {
	GinkgoHelper()
	project := gitlabFake.AddProject("acme/"+ns, "glpat-"+ns)
	dt := newGitLabTarget(ns, name, name+"-gitlab", "acme/"+ns)
	dt.Spec.GitLab.Source = &c4v1beta1.DeployTargetSource{}
	mustCreate(newDeploySecret(ns, name+"-gitlab", map[string]string{"token": "glpat-" + ns}), dt)
	deployReady(dt, metav1.ConditionTrue, model.DeployTargetReady)
	return project
}

var _ = Describe("Container ships its source to a gitlab target (#1805)", func() {
	It("commits the source to its folder, tags it, and names the commit in the release", func() {
		ns := newProjectNamespace()
		readyCLIContainer(ns)
		project := gitlabSourceTarget(ns, "open")
		all, app := appSource("example.com/orders")
		// GitLab's Commits API carries no symlinks; the tree without one.
		all = slices.DeleteFunc(all, func(f fake.SourceFile) bool { return f.Mode == ports.SourceSymlink })
		delete(app, "run")
		gitFake.SetSource(ns, "orders-cli", "v0.1.0", all...)
		gitFake.SetRelease(ns, "orders-cli", "v0.1.0", "Notes of v0.1.0",
			fake.ReleaseFile{Name: "orders-cli-v0.1.0.tar.gz", Data: []byte("the build of v0.1.0")})
		crewReleaseOf(ns, "orders-cli", "v0.1.0", model.RunSucceeded)
		setDeployOf(ns, "orders-cli", c4v1beta1.ContainerDeploy{Target: "open"})

		deployOfIs(ns, "orders-cli", "open", model.DeployPhaseDeployed, model.DeployDeployed, "v0.1.0", model.DeployDeployed)
		source := project.Tag("orders-cli/v0.1.0")
		Expect(source).To(MatchRegexp("^[0-9a-f]{40}$"), "the folder's tag")
		folder := map[string]string{}
		for path, data := range app {
			folder["orders-cli/"+path] = data
		}
		Expect(project.Folder("orders-cli/v0.1.0", "orders-cli")).To(Equal(folder))
		Expect(project.Tag("orders-cli-v0.1.0")).To(Equal(source), "the release's tag names the source commit")
		releases := project.Releases()
		Expect(releases).To(HaveLen(1))
		Expect(releases[0].Tag).To(Equal("orders-cli-v0.1.0"))
	})

	It("publishes no source holding a secret, SecretFound", func() {
		ns := newProjectNamespace()
		readyCLIContainer(ns)
		project := gitlabSourceTarget(ns, "open")
		leaky, leaked := leakedSource("example.com/orders")
		gitFake.SetSource(ns, "orders-cli", "v0.1.0", leaky...)
		gitFake.SetRelease(ns, "orders-cli", "v0.1.0", "Notes of v0.1.0",
			fake.ReleaseFile{Name: "orders-cli-v0.1.0.tar.gz", Data: []byte("the build of v0.1.0")})
		crewReleaseOf(ns, "orders-cli", "v0.1.0", model.RunSucceeded)
		setDeployOf(ns, "orders-cli", c4v1beta1.ContainerDeploy{Target: "open"})

		d := deployOfIs(ns, "orders-cli", "open", model.DeployPhaseFailed, model.DeploySecretFound, "", model.DeployFailed)
		Expect(d.Message).To(ContainSubstring("config/app.env line 2: a GitHub token (ghp_)"))
		Expect(d.Message).NotTo(ContainSubstring(leaked))
		Expect(project.Releases()).To(BeEmpty(), "nothing of the version is written")
		Expect(project.Tag("orders-cli/v0.1.0")).To(BeEmpty())
	})

	It("publishes no source holding a symlink, which the Commits API can't carry, SourceUnsupported", func() {
		ns := newProjectNamespace()
		readyCLIContainer(ns)
		project := gitlabSourceTarget(ns, "open")
		all, _ := appSource("example.com/orders")
		gitFake.SetSource(ns, "orders-cli", "v0.1.0", all...)
		gitFake.SetRelease(ns, "orders-cli", "v0.1.0", "Notes of v0.1.0",
			fake.ReleaseFile{Name: "orders-cli-v0.1.0.tar.gz", Data: []byte("the build of v0.1.0")})
		crewReleaseOf(ns, "orders-cli", "v0.1.0", model.RunSucceeded)
		setDeployOf(ns, "orders-cli", c4v1beta1.ContainerDeploy{Target: "open"})

		d := deployOfIs(ns, "orders-cli", "open", model.DeployPhaseFailed, model.DeployFailed, "", model.DeployFailed)
		Expect(d.Message).To(ContainSubstring("run is a symlink"))
		Expect(project.Releases()).To(BeEmpty(), "nothing of the version is written")
		Expect(project.Tag("orders-cli/v0.1.0")).To(BeEmpty())
	})
})
