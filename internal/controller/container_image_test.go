/*
Copyright 2026 The theos Authors.

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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
	"github.com/JairJosafath/theos/internal/scaffold"
)

const testDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// imageRegistry is the host of the test Git provider's registry.
const imageRegistry = "gitea.gitea.svc.cluster.local:3000/"

// imageIs waits for orders-api's status.image to have tag and state, and the
// condition ImagePublished to match it.
func imageIs(ns, tag, state string) *c4v1beta1.ContainerImageStatus { //nolint:unparam // the helper names the release, as its callers read; every spec of this file releases v0.1.0
	GinkgoHelper()
	var img *c4v1beta1.ContainerImageStatus
	Eventually(func(g Gomega) {
		container := &c4v1beta1.Container{}
		g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
		img = container.Status.Image
		g.Expect(img).NotTo(BeNil())
		g.Expect(img.Tag).To(Equal(tag))
		g.Expect(img.State).To(Equal(state), img.Message)
		cond := condition(container.Status.Conditions, c4v1beta1.ConditionImagePublished)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Reason).To(Equal(state))
		want := metav1.ConditionFalse
		if state == model.ImageStatePublished {
			want = metav1.ConditionTrue
		}
		g.Expect(cond.Status).To(Equal(want))
	}).Should(Succeed())
	return img
}

var _ = Describe("Container images", func() {
	It("publishes the newest release's image once and ships it by digest", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		readyTarget(ns, "staging", c4v1beta1.DeployTierDevelopment)
		gitFake.SetImage(ns, "orders-api", "v0.1.0", ports.Image{Outcome: ports.ImagePublished,
			Digest: testDigest, Platforms: []string{"linux/amd64", "linux/arm64"}})
		shipRelease(ns, "v0.1.0", true)
		setDeploy(ns, c4v1beta1.ContainerDeploy{Target: "staging"})

		img := imageIs(ns, "v0.1.0", model.ImageStatePublished)
		repository := imageRegistry + ns + "/orders-api"
		Expect(img.Repository).To(Equal(repository))
		Expect(img.Digest).To(Equal(testDigest))
		Expect(img.Platforms).To(Equal([]string{"linux/amd64", "linux/arm64"}))
		Expect(img.PublishedTime).NotTo(BeNil())
		saidOnce(ns, model.ImageEventPublished, repository+":v0.1.0 is "+testDigest)
		Expect(gitFake.ImagePushes(ns + "/orders-api@v0.1.0")).To(Equal(1))

		By("shipping the image by digest")
		Eventually(func(g Gomega) {
			var images []string
			for _, s := range deployFake.Shipments() {
				if s.Target == targetName(ns, "staging") {
					images = append(images, s.Shipment.Image)
				}
			}
			g.Expect(images).To(ContainElement(repository + "@" + testDigest))
		}).Should(Succeed())
	})

	DescribeTable("says why a release's image is left unpublished, once",
		func(img ports.Image, state, event, message string) {
			ns := newProjectNamespace()
			deployingContainer(ns)
			if img.Outcome != "" {
				gitFake.SetImage(ns, "orders-api", "v0.1.0", img)
			}
			shipRelease(ns, "v0.1.0", true)
			got := imageIs(ns, "v0.1.0", state)
			Expect(got.Message).To(ContainSubstring(message))
			Expect(got.PublishedTime).To(BeNil())
			if event != "" {
				saidOnce(ns, event, message)
			}
			Consistently(func() int { return gitFake.ImagePushes(ns + "/orders-api@v0.1.0") },
				3*time.Second).Should(Equal(1))
		},
		Entry("no archive", ports.Image{}, model.ImageStateNoImage, "",
			"Release v0.1.0 has no image archive"),
		Entry("an invalid archive", ports.Image{Outcome: ports.ImageInvalid,
			Reason: "the archive has no index.json"}, model.ImageStateInvalid, model.ImageEventInvalid,
			"the archive has no index.json"),
		Entry("a tag that names another image", ports.Image{Outcome: ports.ImageTagTaken,
			Reason: "v0.1.0 already names sha256:other"}, model.ImageStateTagTaken, model.ImageEventTagTaken,
			"v0.1.0 already names sha256:other"),
	)

	It("tries a failed push again at the next poll", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		gitFake.SetImage(ns, "orders-api", "v0.1.0", ports.Image{Outcome: ports.ImagePublished,
			Digest: testDigest, Platforms: []string{"linux/amd64"}})
		gitFake.FailImagePushes(ns, "orders-api", 2)
		shipRelease(ns, "v0.1.0", true)

		img := imageIs(ns, "v0.1.0", model.ImageStatePublished)
		Expect(img.Digest).To(Equal(testDigest))
		Expect(gitFake.ImagePushes(ns + "/orders-api@v0.1.0")).To(Equal(3))
		saidOnce(ns, model.ImageEventPublished, testDigest)
	})

	It("ships a pinned version's image from the registry", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		readyTarget(ns, "staging", c4v1beta1.DeployTierDevelopment)
		gitFake.SetRelease(ns, "orders-api", "v0.0.9", "Notes of v0.0.9",
			fake.ReleaseFile{Name: "orders-api-v0.0.9.tar.gz", Data: []byte("the build of v0.0.9")})
		gitFake.SetRegistryImage(ns, "orders-api", "v0.0.9", testDigest)
		shipRelease(ns, "v0.1.0", true)
		setDeploy(ns, c4v1beta1.ContainerDeploy{Target: "staging", Version: "v0.0.9"})

		deployIs(ns, "staging", model.DeployPhaseDeploying, model.DeployDeploying, "", model.DeployDeploying)
		deployFake.SetState(targetName(ns, "staging"), ns+"/orders-api", ports.DeployState{Version: "v0.0.9",
			Phase: ports.PhaseDeployed, Health: ports.HealthHealthy})
		var images []string
		for _, s := range deployFake.Shipments() {
			if s.Target == targetName(ns, "staging") {
				images = append(images, s.Shipment.Image)
			}
		}
		Expect(images).To(Equal([]string{imageRegistry + ns + "/orders-api@" + testDigest}))
	})

	// shippedImages are the images of the deploys started at ns's target.
	shippedImages := func(ns, target string) []string { //nolint:unparam // the helper names the target, as its callers read
		var images []string
		for _, s := range deployFake.Shipments() {
			if s.Target == targetName(ns, target) {
				images = append(images, s.Shipment.Image)
			}
		}
		return images
	}

	It("writes Pushing and goes on while a push is held, then the image once it ends (#1572)", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		readyTarget(ns, "staging", c4v1beta1.DeployTierDevelopment)
		setDeploy(ns, c4v1beta1.ContainerDeploy{Target: "staging"})
		gitFake.SetImage(ns, "orders-api", "v0.1.0", ports.Image{Outcome: ports.ImagePublished,
			Digest: testDigest, Platforms: []string{"linux/amd64"}})
		release := gitFake.HoldImagePushes(ns, "orders-api")
		DeferCleanup(release)
		shipRelease(ns, "v0.1.0", true)

		By("writing the Container's status while its push waits")
		img := imageIs(ns, "v0.1.0", model.ImageStatePushing)
		Expect(img.Message).To(Equal("Pushing the image of release v0.1.0."))
		d := deployIs(ns, "staging", model.DeployPhaseWaiting, model.DeployGitHostUnreachable, "", model.DeployDeploying)
		Expect(d.Message).To(ContainSubstring("the image of v0.1.0 is not published yet: Pushing"))
		Expect(shippedImages(ns, "staging")).To(BeEmpty())
		Consistently(func() int { return gitFake.ImagePushes(ns + "/orders-api@v0.1.0") },
			2*time.Second).Should(Equal(1))

		By("writing the image and shipping it once the push ends")
		release()
		img = imageIs(ns, "v0.1.0", model.ImageStatePublished)
		Expect(img.Digest).To(Equal(testDigest))
		deployIs(ns, "staging", model.DeployPhaseDeployed, model.DeployDeployed, "v0.1.0", model.DeployDeployed)
		Expect(shippedImages(ns, "staging")).To(Equal([]string{imageRegistry + ns + "/orders-api@" + testDigest}))
		Expect(gitFake.ImagePushes(ns + "/orders-api@v0.1.0")).To(Equal(1))
		saidOnce(ns, model.ImageEventPublished, testDigest)
	})

	It("holds the ship while the newest release's push fails, then ships by digest (#1570)", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		readyTarget(ns, "staging", c4v1beta1.DeployTierDevelopment)
		setDeploy(ns, c4v1beta1.ContainerDeploy{Target: "staging"})
		gitFake.SetImage(ns, "orders-api", "v0.1.0", ports.Image{Outcome: ports.ImagePublished,
			Digest: testDigest, Platforms: []string{"linux/amd64"}})
		gitFake.FailImagePushes(ns, "orders-api", 5)
		shipRelease(ns, "v0.1.0", true)

		// The push runs beside the reconciler (#1572), so the deploy waits on
		// it Pushing before it waits on it Failed: wait for the failure
		// (#1847).
		Eventually(func(g Gomega) {
			d, cond := deployOf(g, ns, "staging")
			g.Expect(cond.Reason).To(Equal(model.DeployDeploying))
			g.Expect(d.Phase).To(Equal(model.DeployPhaseWaiting))
			g.Expect(d.Reason).To(Equal(model.DeployGitHostUnreachable))
			g.Expect(d.Message).To(ContainSubstring("the image of v0.1.0 is not published yet: gitea unavailable"))
		}).Should(Succeed())
		Expect(shippedImages(ns, "staging")).To(BeEmpty())

		By("shipping once the push succeeds")
		imageIs(ns, "v0.1.0", model.ImageStatePublished)
		deployIs(ns, "staging", model.DeployPhaseDeployed, model.DeployDeployed, "v0.1.0", model.DeployDeployed)
		Expect(shippedImages(ns, "staging")).To(Equal([]string{imageRegistry + ns + "/orders-api@" + testDigest}))
	})

	It("ships no image, not the registry's, when the newest release's tag is taken (#1570)", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		readyTarget(ns, "staging", c4v1beta1.DeployTierDevelopment)
		setDeploy(ns, c4v1beta1.ContainerDeploy{Target: "staging"})
		gitFake.SetImage(ns, "orders-api", "v0.1.0", ports.Image{Outcome: ports.ImageTagTaken,
			Reason: "v0.1.0 already names sha256:other"})
		gitFake.SetRegistryImage(ns, "orders-api", "v0.1.0", "sha256:other")
		shipRelease(ns, "v0.1.0", true)

		imageIs(ns, "v0.1.0", model.ImageStateTagTaken)
		deployIs(ns, "staging", model.DeployPhaseDeployed, model.DeployDeployed, "v0.1.0", model.DeployDeployed)
		Expect(shippedImages(ns, "staging")).To(Equal([]string{""}))
	})

	It("leaves the image and the deploys as they were while the Crew list fails (#1570, #1576)", func() {
		ns := newProjectNamespace()
		deployingContainer(ns)
		readyTarget(ns, "staging", c4v1beta1.DeployTierDevelopment)
		setDeploy(ns, c4v1beta1.ContainerDeploy{Target: "staging"})
		gitFake.SetImage(ns, "orders-api", "v0.1.0", ports.Image{Outcome: ports.ImagePublished,
			Digest: testDigest, Platforms: []string{"linux/amd64"}})
		shipRelease(ns, "v0.1.0", true)
		imageIs(ns, "v0.1.0", model.ImageStatePublished)
		deployIs(ns, "staging", model.DeployPhaseDeployed, model.DeployDeployed, "v0.1.0", model.DeployDeployed)

		By("not reading the target while the list fails")
		crewListFaults.failNext(ns, 1000)
		DeferCleanup(crewListFaults.failNext, ns, 0)
		deployFake.SetState(targetName(ns, "staging"), ns+"/orders-api", ports.DeployState{Version: "v0.1.0",
			Phase: ports.PhaseDeployed, Health: ports.HealthDegraded})
		Consistently(func(g Gomega) {
			d, _ := deployOf(g, ns, "staging")
			g.Expect(d.Health).To(Equal(ports.HealthHealthy))
			container := &c4v1beta1.Container{}
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Image).NotTo(BeNil())
			g.Expect(container.Status.Image.State).To(Equal(model.ImageStatePublished))
		}, 3*time.Second).Should(Succeed())

		By("reading it again once the list succeeds")
		crewListFaults.failNext(ns, 0)
		Eventually(func(g Gomega) {
			d, _ := deployOf(g, ns, "staging")
			g.Expect(d.Health).To(Equal(ports.HealthDegraded))
		}).Should(Succeed())
	})

	It("reports no image for a Container whose choice builds none", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		// A service without a Deployment: no catalog choice of its builds an image.
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		container.Spec.DeploymentRef = nil
		mustCreate(newSystem(ns, "webshop-core"), container, newCrew(ns))
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
		}).Should(Succeed())
		shipRelease(ns, "v0.1.0", true)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.NewestRelease).To(Equal("v0.1.0"))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Image).To(BeNil())
			g.Expect(condition(container.Status.Conditions, c4v1beta1.ConditionImagePublished)).To(BeNil())
		}, 3*time.Second).Should(Succeed())
		Expect(gitFake.ImagePushes(ns + "/orders-api@v0.1.0")).To(BeZero())
	})

	It("reports no image for a Fargate Container scaffolded before the image job (#1797)", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		// aws-fargate-ts v1.3.0 predates the image job (v2.0.0, #1610), so
		// the release workflow it rendered pushes no archive. The tree
		// served under it doesn't matter: only the scaffolded ref counts.
		gitFake.AddTemplate(templatesURL+model.FrameworkAWSFargate, "v1.3.0", scaffold.Tree{
			"README.md.tmpl": {Data: []byte("# {{ .Container }}\n")},
		})
		fargate := &c4v1beta1.Framework{ObjectMeta: objectMeta(ns, model.FrameworkAWSFargate),
			Spec: newFrameworkSpec(templatesURL+model.FrameworkAWSFargate, "v1.3.0", c4v1beta1.ContainerTypeService)}
		fargate.Spec.DeploysItself = true
		giteaRelease := &c4v1beta1.Deployment{ObjectMeta: objectMeta(ns, model.DeploymentGiteaRelease),
			Spec: newCatalogSpec(templatesURL+model.DeploymentGiteaRelease, "v1.9.0", c4v1beta1.ContainerTypeService)}
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		container.Spec.FrameworkRef.Name = model.FrameworkAWSFargate
		container.Spec.ComputeRef = nil
		container.Spec.DeploymentRef = &c4v1beta1.LocalRef{Name: model.DeploymentGiteaRelease}
		mustCreate(newSystem(ns, "webshop-core"), fargate, giteaRelease, container, newCrew(ns))
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
		}).Should(Succeed())
		Expect(container.Status.ScaffoldedFrom).To(ContainElement(model.FrameworkAWSFargate + "@v1.3.0"))
		shipRelease(ns, "v0.1.0", true) // its files: the build's tar.gz, no *.oci.tar

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.NewestRelease).To(Equal("v0.1.0"))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Image).To(BeNil())
			g.Expect(condition(container.Status.Conditions, c4v1beta1.ConditionImagePublished)).To(BeNil())
		}, 3*time.Second).Should(Succeed())
		Expect(gitFake.ImagePushes(ns + "/orders-api@v0.1.0")).To(BeZero())
	})
})
