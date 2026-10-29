/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// newBundle returns a Bundle of framework with an optional compute and deployment.
func newBundle(ns, name, framework, compute, deployment string) *c4v1beta1.Bundle {
	b := &c4v1beta1.Bundle{ObjectMeta: objectMeta(ns, name),
		Spec: c4v1beta1.BundleSpec{FrameworkRef: c4v1beta1.LocalRef{Name: framework}}}
	if compute != "" {
		b.Spec.ComputeRef = &c4v1beta1.LocalRef{Name: compute}
	}
	if deployment != "" {
		b.Spec.DeploymentRef = &c4v1beta1.LocalRef{Name: deployment}
	}
	return b
}

// newBundledContainer returns a webshop-core service naming bundle instead of its four sides.
func newBundledContainer(ns, name, bundle string) *c4v1beta1.Container {
	c := newContainer(ns, name, "webshop-core", c4v1beta1.ContainerTypeService)
	c.Spec.FrameworkRef, c.Spec.ComputeRef, c.Spec.DeploymentRef = c4v1beta1.LocalRef{}, nil, nil
	c.Spec.BundleRef = &c4v1beta1.LocalRef{Name: bundle}
	return c
}

// eventuallyContainer waits for a Container to reach a phase and returns it.
func eventuallyContainer(ns, name string, phase c4v1beta1.ContainerPhase) *c4v1beta1.Container {
	GinkgoHelper()
	var container c4v1beta1.Container
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(ns, name), &container)).To(Succeed())
		g.Expect(container.Status.Phase).To(Equal(phase))
	}).Should(Succeed())
	return &container
}

var _ = Describe("Container naming a Bundle", func() {
	It("names a bundleRef or a frameworkRef, never both, and keeps its bundleRef", func() {
		ns := newProjectNamespace()

		By("naming neither")
		neither := newBundledContainer(ns, "neither", "web-service")
		neither.Spec.BundleRef = nil
		Expect(k8sClient.Create(ctx, neither)).To(MatchError(ContainSubstring("name a bundleRef or a frameworkRef")))

		By("naming both, or a side beside the bundle")
		for side, set := range map[string]func(*c4v1beta1.Container){
			"framework":  func(c *c4v1beta1.Container) { c.Spec.FrameworkRef = c4v1beta1.LocalRef{Name: "python-fastapi"} },
			"standard":   func(c *c4v1beta1.Container) { c.Spec.StandardRef = &c4v1beta1.LocalRef{Name: "python-standard"} },
			"compute":    func(c *c4v1beta1.Container) { c.Spec.ComputeRef = &c4v1beta1.LocalRef{Name: "small"} },
			"deployment": func(c *c4v1beta1.Container) { c.Spec.DeploymentRef = &c4v1beta1.LocalRef{Name: "k8s-manifests"} },
		} {
			c := newBundledContainer(ns, "with-"+side, "web-service")
			set(c)
			Expect(k8sClient.Create(ctx, c)).To(MatchError(ContainSubstring(
				"bundleRef replaces frameworkRef, standardRef, computeRef and deploymentRef")), side)
		}

		By("changing, removing or adding a bundleRef")
		bundled := newBundledContainer(ns, "bundled", "web-service")
		plain := newContainer(ns, "plain", "webshop-core", c4v1beta1.ContainerTypeService)
		mustCreate(bundled, plain)
		Expect(patch(bundled, func() { bundled.Spec.BundleRef.Name = "another" })).
			To(MatchError(ContainSubstring("bundleRef is immutable")))
		Expect(patch(bundled, func() {
			bundled.Spec.BundleRef = nil
			bundled.Spec.FrameworkRef = c4v1beta1.LocalRef{Name: "python-fastapi"}
		})).To(MatchError(ContainSubstring("bundleRef is immutable")))
		Expect(patch(plain, func() {
			plain.Spec.FrameworkRef, plain.Spec.ComputeRef, plain.Spec.DeploymentRef = c4v1beta1.LocalRef{}, nil, nil
			plain.Spec.BundleRef = &c4v1beta1.LocalRef{Name: "web-service"}
		})).To(MatchError(ContainSubstring("bundleRef is immutable")))
	})

	It("waits for a missing Bundle, naming it", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		mustCreate(newSystem(ns, "webshop-core"), newBundledContainer(ns, "orders-api", "web-service"))

		Eventually(func(g Gomega) {
			var container c4v1beta1.Container
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerPending))
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionReferencesResolved)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonNotFound))
			g.Expect(cond.Message).To(Equal("Bundle web-service not found"))
		}).Should(Succeed())

		mustCreate(newBundle(ns, "web-service", "python-fastapi", "small", "k8s-manifests"))
		eventuallyContainer(ns, "orders-api", c4v1beta1.ContainerReady)
	})

	It("scaffolds like its twin naming the four entries, and keeps the expansion it scaffolded from", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		bundle := newBundle(ns, "web-service", "python-fastapi", "medium", "k8s-manifests")
		mustCreate(newSystem(ns, "webshop-core"), bundle,
			newContainer(ns, "twin", "webshop-core", c4v1beta1.ContainerTypeService),
			newBundledContainer(ns, "bundled", "web-service"))

		By("expanding afresh while it waits: medium is no Compute of the catalog")
		Eventually(func(g Gomega) {
			var container c4v1beta1.Container
			g.Expect(k8sClient.Get(ctx, key(ns, "bundled"), &container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerPending))
			g.Expect(container.Status.Bundle).To(Equal(&c4v1beta1.ContainerBundleStatus{Name: "web-service",
				Framework: "python-fastapi", Compute: "medium", Deployment: "k8s-manifests"}))
			g.Expect(condition(container.Status.Conditions, c4v1beta1.ConditionReferencesResolved)).
				To(HaveField("Message", "Compute medium not found"))
		}).Should(Succeed())
		Expect(patch(bundle, func() { bundle.Spec.ComputeRef.Name = "small" })).To(Succeed())

		twin := eventuallyContainer(ns, "twin", c4v1beta1.ContainerReady)
		bundled := eventuallyContainer(ns, "bundled", c4v1beta1.ContainerReady)
		Expect(bundled.Status.ScaffoldedFrom).To(Equal(twin.Status.ScaffoldedFrom))
		Expect(bundled.Status.ScaffoldedFrom).To(Equal([]string{"python-fastapi@v1.6.0", "small@v1.1.0", "k8s-manifests@v1.9.1"}))
		want := &c4v1beta1.ContainerBundleStatus{Name: "web-service",
			Framework: "python-fastapi", Compute: "small", Deployment: "k8s-manifests"}
		Expect(bundled.Status.Bundle).To(Equal(want))
		Expect(bundled.Choices()).To(Equal(c4v1beta1.ContainerChoices{Bundle: "web-service",
			Framework: "python-fastapi", Compute: "small", Deployment: "k8s-manifests"}))
		Expect(gitFake.Repo(ns, "bundled").Files).To(HaveKey("deploy/k8s/compute.yaml"))

		By("a Bundle changed after the scaffold changing nothing")
		pushes := gitFake.PushCount()
		Expect(patch(bundle, func() { bundle.Spec.DeploymentRef = nil })).To(Succeed())
		Expect(patch(bundled, func() {
			bundled.Annotations = map[string]string{c4v1beta1.AnnotationRetry: time.Now().String()}
		})).To(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.PushCount()).To(Equal(pushes))
			g.Expect(k8sClient.Get(ctx, key(ns, "bundled"), bundled)).To(Succeed())
			g.Expect(bundled.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
			g.Expect(bundled.Status.Bundle).To(Equal(want))
		}).WithTimeout(2 * time.Second).Should(Succeed())
		Expect(gitFake.Repo(ns, "bundled").Commits).To(HaveLen(1))
	})

	It("reports a changed or deleted Bundle, never applying it (#1621)", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		bundle := newBundle(ns, "web-service", "python-fastapi", "small", "k8s-manifests")
		mustCreate(newSystem(ns, "webshop-core"), bundle,
			newContainer(ns, "twin", "webshop-core", c4v1beta1.ContainerTypeService),
			newBundledContainer(ns, "bundled", "web-service"))
		bundled := eventuallyContainer(ns, "bundled", c4v1beta1.ContainerReady)
		scaffoldedFrom, scaffoldedBundle := bundled.Status.ScaffoldedFrom, bundled.Status.Bundle.DeepCopy()
		Expect(scaffoldedBundle).To(Equal(&c4v1beta1.ContainerBundleStatus{Name: "web-service",
			Framework: "python-fastapi", Compute: "small", Deployment: "k8s-manifests"}))

		bundleCurrent := func(status metav1.ConditionStatus, reason, message string) {
			GinkgoHelper()
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, key(ns, "bundled"), bundled)).To(Succeed())
				cond := condition(bundled.Status.Conditions, c4v1beta1.ConditionBundleCurrent)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(status))
				g.Expect(cond.Reason).To(Equal(reason))
				g.Expect(cond.Message).To(Equal(message))
			}).Should(Succeed())
			Expect(bundled.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
			Expect(bundled.Status.Bundle).To(Equal(scaffoldedBundle), "status.bundle keeps the expansion it scaffolded from")
			Expect(bundled.Status.ScaffoldedFrom).To(Equal(scaffoldedFrom))
			Expect(gitFake.Repo(ns, "bundled").Commits).To(HaveLen(1), "no second scaffold")
		}

		By("a Bundle still naming what it scaffolded from")
		bundleCurrent(metav1.ConditionTrue, c4v1beta1.ReasonUpToDate, "")

		By("a Bundle changed since, naming the side")
		Expect(patch(bundle, func() { bundle.Spec.ComputeRef.Name = "large" })).To(Succeed())
		bundleCurrent(metav1.ConditionFalse, c4v1beta1.ReasonBundleChanged, "compute: small, now large")

		By("a Bundle deleted since")
		Expect(k8sClient.Delete(ctx, bundle)).To(Succeed())
		bundleCurrent(metav1.ConditionFalse, c4v1beta1.ReasonBundleChanged, "Bundle web-service is gone")

		By("a Container naming its sides reporting none")
		twin := eventuallyContainer(ns, "twin", c4v1beta1.ContainerReady)
		Expect(condition(twin.Status.Conditions, c4v1beta1.ConditionBundleCurrent)).To(BeNil())
	})

	It("recovers the expansion the earlier attempt pushed when the Bundle changed since (#1621)", func() {
		ns := newProjectNamespace()
		in := scaffold.Input{Project: ns, SoftwareSystem: "webshop-core", Container: "orders-api", Bundle: "web-service",
			Framework: "python-fastapi", Standard: "python-standard", Compute: "small", Deployment: "k8s-manifests"}
		from := []string{"python-fastapi@v1.6.0", "python-standard@v1.0.0", "small@v1.1.0", "k8s-manifests@v1.9.1"}
		gitFake.SeedRepo(ns, "orders-api", scaffold.CommitMessage(in, from, nil))
		pushes := gitFake.PushCount()
		createFactory(ns)
		// The Bundle no longer names the Standard the push used.
		mustCreate(newSystem(ns, "webshop-core"), newStandard(ns, "python-standard", "python-fastapi"),
			newBundle(ns, "web-service", "python-fastapi", "small", "k8s-manifests"),
			newBundledContainer(ns, "orders-api", "web-service"))

		container := eventuallyContainer(ns, "orders-api", c4v1beta1.ContainerReady)
		Expect(container.Status.Bundle).To(Equal(&c4v1beta1.ContainerBundleStatus{Name: "web-service",
			Framework: "python-fastapi", Standard: "python-standard", Compute: "small", Deployment: "k8s-manifests"}))
		Expect(container.Status.Standard).To(SatisfyAll(
			HaveField("Name", "python-standard"), HaveField("Ref", "v1.0.0")))
		Expect(container.Status.ScaffoldedFrom).To(Equal(from))
		Expect(gitFake.PushCount()).To(Equal(pushes))
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			cond := condition(container.Status.Conditions, c4v1beta1.ConditionBundleCurrent)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonBundleChanged))
			g.Expect(cond.Message).To(Equal("standard: python-standard, now none"))
		}).Should(Succeed())
		Expect(container.Status.ScaffoldedFrom).To(Equal(from))
		Expect(gitFake.Repo(ns, "orders-api").Commits).To(HaveLen(1))
	})

	It("fails a Bundle whose entries don't fit the Container, naming the bundle", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		mustCreate(newSystem(ns, "webshop-core"), newBundle(ns, "operator-kit", "go-kubebuilder", "small", ""),
			newBundledContainer(ns, "orders-api", "operator-kit"))

		container := eventuallyContainer(ns, "orders-api", c4v1beta1.ContainerFailed)
		cond := condition(container.Status.Conditions, c4v1beta1.ConditionChoicesValid)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(c4v1beta1.ReasonUnsupportedType))
		Expect(strings.HasPrefix(cond.Message, "bundle operator-kit: ")).To(BeTrue(), cond.Message)
		Expect(cond.Message).To(ContainSubstring("go-kubebuilder"))
	})
})
