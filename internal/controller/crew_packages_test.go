/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

// The package files a release of orders-api attaches, and what they hold.
var (
	ordersTarball = fakeFile{name: "c4factory-orders-api-0.1.0.tgz", data: []byte("tgz")}
	ordersWheel   = fakeFile{name: "orders_api-0.1.0-py3-none-any.whl", data: []byte("whl")}
	ordersNpm     = fakePackage{ecosystem: gitprovider.EcosystemNpm, name: "@c4factory/orders-api", version: "0.1.0",
		file: ordersTarball.name}
	ordersPyPI = fakePackage{ecosystem: gitprovider.EcosystemPyPI, name: "orders-api", version: "0.1.0",
		file: ordersWheel.name}
)

// packagedRelease readies orders-api, merges its issue and makes the Gitea
// release of v0.1.0 with files, holding packages, its release run ended with
// conclusion; then creates the Crew that tags it.
func packagedRelease(ns, status, conclusion string, files []fakeFile, packages ...fakePackage) {
	GinkgoHelper()
	readyContainer(ns)
	crewAgents(ns, 1)
	mergedIssue(ns)
	gitFake.setGiteaRelease(ns, "orders-api", "v0.1.0", "", files...)
	gitFake.setReleasePackages(ns, "orders-api", "v0.1.0", packages...)
	releaseRun(ns, status, conclusion)
	taggingCrew(ns)
}

// releasePackages is the Crew's last release's packages once the state is
// reached, with the condition's value and reason checked.
func releasePackages(g Gomega, ns, state string, value metav1.ConditionStatus, reason string) *c4v1alpha1.CrewReleasePackages {
	crew := getCrew(g, ns)
	g.Expect(crew.Status.LastRelease).NotTo(BeNil())
	packages := crew.Status.LastRelease.Packages
	g.Expect(packages).NotTo(BeNil())
	g.Expect(packages.State).To(Equal(state))
	cond := condition(crew.Status.Conditions, ConditionPackagesPublished)
	g.Expect(cond).NotTo(BeNil())
	g.Expect(cond.Status).To(Equal(value))
	g.Expect(cond.Reason).To(Equal(reason))
	g.Expect(cond.Message).To(Equal(packages.Message))
	return packages
}

var _ = Describe("Crew release packages", func() {
	It("waits for the release runs, then publishes the npm tarball and the wheel once", func() {
		ns := newProjectNamespace()
		packagedRelease(ns, "in_progress", "", []fakeFile{ordersTarball, ordersWheel}, ordersNpm, ordersPyPI)

		Eventually(func(g Gomega) {
			packages := releasePackages(g, ns, "Waiting", metav1.ConditionFalse, "WaitsForReleaseRun")
			g.Expect(packages.Message).To(Equal(
				"Release runs of v0.1.0 are Running; its packages are read from the release once they succeed"))
			g.Expect(packages.Published).To(BeEmpty())
		}).Should(Succeed())
		Expect(gitFake.packagePublishes(ns, "orders-api", "v0.1.0")).To(BeZero(), "nothing is read before the runs succeed")

		releaseRun(ns, "completed", "success")
		Eventually(func(g Gomega) {
			packages := releasePackages(g, ns, "Published", metav1.ConditionTrue, "PackagesPublished")
			g.Expect(packages.Message).To(Equal("Published npm @c4factory/orders-api 0.1.0 and PyPI orders-api 0.1.0"))
			g.Expect(packages.Published).To(Equal([]c4v1alpha1.CrewReleasePackage{
				{Ecosystem: "npm", Name: "@c4factory/orders-api", Version: "0.1.0"},
				{Ecosystem: "PyPI", Name: "orders-api", Version: "0.1.0"}}))
			g.Expect(moduleEvents(g, ns, "PackagePublished")).To(ConsistOf(
				And(HaveField("Type", "Normal"), HaveField("Action", "PublishNpmPackage"),
					HaveField("Note", "Published npm @c4factory/orders-api 0.1.0")),
				And(HaveField("Type", "Normal"), HaveField("Action", "PublishPyPIPackage"),
					HaveField("Note", "Published PyPI orders-api 0.1.0"))))
		}).Should(Succeed())
		// The Crew polls each second; a second upload, or a second Event per
		// package, would show. A Crew read from a cache that lags its status
		// may ask once more, and finds the packages in their registries.
		Consistently(func(g Gomega) {
			g.Expect(gitFake.packageUploads(ns, "npm", "@c4factory/orders-api", "0.1.0")).To(Equal(1))
			g.Expect(gitFake.packageUploads(ns, "PyPI", "orders-api", "0.1.0")).To(Equal(1))
			g.Expect(gitFake.packagePublishes(ns, "orders-api", "v0.1.0")).To(BeNumerically("<=", 2))
			events := moduleEvents(g, ns, "PackagePublished")
			g.Expect(events).To(HaveLen(2))
			for _, e := range events {
				g.Expect(e.Series).To(BeNil())
			}
			g.Expect(moduleEvents(g, ns, "InvalidPackage")).To(BeEmpty())
		}, 3*time.Second).Should(Succeed())
	})

	It("reports a release with no package file as NoPackage", func() {
		ns := newProjectNamespace()
		packagedRelease(ns, "completed", "success", []fakeFile{{name: "orders-api-0.1.0.vsix", data: []byte("vsix")}})

		Eventually(func(g Gomega) {
			packages := releasePackages(g, ns, "NoPackage", metav1.ConditionTrue, "NoPackage")
			g.Expect(packages.Message).To(Equal("v0.1.0 attached no npm tarball, wheel or sdist; no package to publish"))
			g.Expect(packages.Published).To(BeEmpty())
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(moduleEvents(g, ns, "PackagePublished")).To(BeEmpty())
			g.Expect(moduleEvents(g, ns, "InvalidPackage")).To(BeEmpty())
			g.Expect(gitFake.packagePublishes(ns, "orders-api", "v0.1.0")).To(BeNumerically("<=", 2))
		}, 3*time.Second).Should(Succeed())
	})

	It("leaves an unscoped npm tarball alone and names it, publishing the wheel", func() {
		ns := newProjectNamespace()
		unscoped := fakePackage{ecosystem: gitprovider.EcosystemNpm, name: "orders-api", version: "0.1.0",
			file: "orders-api-0.1.0.tgz"}
		packagedRelease(ns, "completed", "success",
			[]fakeFile{{name: unscoped.file, data: []byte("tgz")}, ordersWheel}, unscoped, ordersPyPI)

		Eventually(func(g Gomega) {
			packages := releasePackages(g, ns, "Published", metav1.ConditionTrue, "PackagesPublished")
			g.Expect(packages.Message).To(Equal("Published PyPI orders-api 0.1.0; left orders-api-0.1.0.tgz alone: " +
				"orders-api is not under the scope @c4factory"))
			g.Expect(packages.Published).To(Equal([]c4v1alpha1.CrewReleasePackage{
				{Ecosystem: "PyPI", Name: "orders-api", Version: "0.1.0"}}))
			g.Expect(moduleEvents(g, ns, "PackagePublished")).To(HaveLen(1))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.packageUploads(ns, "npm", "orders-api", "0.1.0")).To(BeZero())
			g.Expect(moduleEvents(g, ns, "InvalidPackage")).To(BeEmpty())
		}, 3*time.Second).Should(Succeed())
	})

	It("reports a tarball whose version isn't the tag's as Invalid, once", func() {
		ns := newProjectNamespace()
		wrong := fakePackage{ecosystem: gitprovider.EcosystemNpm, name: "@c4factory/orders-api", version: "0.2.0",
			file: "c4factory-orders-api-0.2.0.tgz"}
		packagedRelease(ns, "completed", "success",
			[]fakeFile{{name: wrong.file, data: []byte("tgz")}, ordersWheel}, wrong, ordersPyPI)

		message := "Did not publish c4factory-orders-api-0.2.0.tgz: version 0.2.0 is not the tag's, 0.1.0"
		Eventually(func(g Gomega) {
			packages := releasePackages(g, ns, "Invalid", metav1.ConditionFalse, "InvalidPackage")
			g.Expect(packages.Message).To(Equal(message))
			g.Expect(packages.Published).To(Equal([]c4v1alpha1.CrewReleasePackage{
				{Ecosystem: "PyPI", Name: "orders-api", Version: "0.1.0"}}), "the good files are still published")
			g.Expect(moduleEvents(g, ns, "InvalidPackage")).To(ConsistOf(And(HaveField("Type", "Warning"),
				HaveField("Note", message))))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.packageUploads(ns, "npm", "@c4factory/orders-api", "0.2.0")).To(BeZero())
			g.Expect(gitFake.packageUploads(ns, "PyPI", "orders-api", "0.1.0")).To(Equal(1))
			g.Expect(gitFake.packagePublishes(ns, "orders-api", "v0.1.0")).To(BeNumerically("<=", 2))
			events := moduleEvents(g, ns, "InvalidPackage")
			g.Expect(events).To(HaveLen(1))
			g.Expect(events[0].Series).To(BeNil())
		}, 3*time.Second).Should(Succeed())
	})

	It("tries again when the host can't be reached, then publishes", func() {
		ns := newProjectNamespace()
		gitFake.failPackagePublishes(ns, "orders-api", 1)
		packagedRelease(ns, "completed", "success", []fakeFile{ordersTarball, ordersWheel}, ordersNpm, ordersPyPI)

		Eventually(func(g Gomega) {
			packages := releasePackages(g, ns, "Failed", metav1.ConditionFalse, "GitHostUnavailable")
			g.Expect(packages.Message).To(Equal("Could not publish the packages of v0.1.0: gitea unavailable"))
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			packages := releasePackages(g, ns, "Published", metav1.ConditionTrue, "PackagesPublished")
			g.Expect(packages.Published).To(HaveLen(2))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.packageUploads(ns, "npm", "@c4factory/orders-api", "0.1.0")).To(Equal(1))
			g.Expect(gitFake.packageUploads(ns, "PyPI", "orders-api", "0.1.0")).To(Equal(1))
			g.Expect(moduleEvents(g, ns, "PackagePublished")).To(HaveLen(2))
		}, 3*time.Second).Should(Succeed())
	})

	It("leaves a tarball outside the GitProvider's scope alone, naming the scope it lacks", func() {
		ns := newProjectNamespace()
		gitFake.setNpmScope(ns, "@acme")
		other := fakePackage{ecosystem: gitprovider.EcosystemNpm, name: "@c4factory/x", version: "0.1.0",
			file: "c4factory-x-0.1.0.tgz"}
		packagedRelease(ns, "completed", "success", []fakeFile{{name: other.file, data: []byte("tgz")}}, other)

		Eventually(func(g Gomega) {
			packages := releasePackages(g, ns, "NoPackage", metav1.ConditionTrue, "NoPackage")
			g.Expect(packages.Message).To(Equal("v0.1.0 attached no npm tarball, wheel or sdist; no package to publish; " +
				"left c4factory-x-0.1.0.tgz alone: @c4factory/x is not under the scope @acme"))
			g.Expect(packages.Published).To(BeEmpty())
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.packageUploads(ns, "npm", "@c4factory/x", "0.1.0")).To(BeZero())
			g.Expect(moduleEvents(g, ns, "PackagePublished")).To(BeEmpty())
			g.Expect(moduleEvents(g, ns, "InvalidPackage")).To(BeEmpty())
		}, 3*time.Second).Should(Succeed())
	})
})
