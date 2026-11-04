/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

// The package files a release of orders-api attaches, and what they hold.
var (
	ordersTarball = fake.ReleaseFile{Name: "theos-orders-api-0.1.0.tgz", Data: []byte("tgz")}
	ordersWheel   = fake.ReleaseFile{Name: "orders_api-0.1.0-py3-none-any.whl", Data: []byte("whl")}
	ordersNpm     = fake.Package{Ecosystem: ports.EcosystemNpm, Name: "@theos/orders-api", Version: "0.1.0",
		File: ordersTarball.Name}
	ordersPyPI = fake.Package{Ecosystem: ports.EcosystemPyPI, Name: "orders-api", Version: "0.1.0",
		File: ordersWheel.Name}
)

// packagedRelease readies orders-api, merges its issue and makes the Gitea
// release of v0.1.0 with files, holding packages, its release run ended with
// conclusion; then creates the Crew that tags it.
func packagedRelease(ns, status, conclusion string, files []fake.ReleaseFile, packages ...fake.Package) {
	GinkgoHelper()
	readyContainer(ns)
	packagedReleaseOf(ns, status, conclusion, files, packages...)
}

// packagedReleaseOf is packagedRelease once orders-api is ready, for a spec
// that changes its GitProvider first.
func packagedReleaseOf(ns, status, conclusion string, files []fake.ReleaseFile, packages ...fake.Package) {
	GinkgoHelper()
	crewAgents(ns, 1)
	mergedIssue(ns)
	gitFake.SetRelease(ns, "orders-api", "v0.1.0", "", files...)
	gitFake.SetReleasePackages(ns, "orders-api", "v0.1.0", packages...)
	releaseRun(ns, status, conclusion)
	taggingCrew(ns)
}

// setNpmScope sets the npm scope of ns's GitProvider, spec.packages.npm.scope,
// which the Crew reads through the Repository its provider builds.
func setNpmScope(ns, scope string) {
	GinkgoHelper()
	// The GitProvider's reconciler may write its status meanwhile: a
	// conflict reads it again.
	Eventually(func(g Gomega) {
		provider := &c4v1beta1.GitProvider{}
		g.Expect(k8sClient.Get(ctx, key(ns, "gitea"), provider)).To(Succeed())
		provider.Spec.Packages = &c4v1beta1.PackagesSpec{NPM: &c4v1beta1.NpmPackagesSpec{Scope: scope}}
		g.Expect(k8sClient.Update(ctx, provider)).To(Succeed())
	}).Should(Succeed())
}

// releasePackages is the Crew's last release's packages once the state is
// reached, with the condition's value and reason checked.
func releasePackages(g Gomega, ns, state string, value metav1.ConditionStatus, reason string) *c4v1beta1.CrewReleasePackages {
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
		packagedRelease(ns, "in_progress", "", []fake.ReleaseFile{ordersTarball, ordersWheel}, ordersNpm, ordersPyPI)

		Eventually(func(g Gomega) {
			packages := releasePackages(g, ns, "Waiting", metav1.ConditionFalse, "WaitsForReleaseRun")
			g.Expect(packages.Message).To(Equal(
				"Release runs of v0.1.0 are Running; its packages are read from the release once they succeed"))
			g.Expect(packages.Published).To(BeEmpty())
		}).Should(Succeed())
		Expect(gitFake.PackagePublishes(ns+"/orders-api@v0.1.0")).To(BeZero(), "nothing is read before the runs succeed")

		releaseRun(ns, "completed", "success")
		Eventually(func(g Gomega) {
			packages := releasePackages(g, ns, "Published", metav1.ConditionTrue, "PackagesPublished")
			g.Expect(packages.Message).To(Equal("Published npm @theos/orders-api 0.1.0 and PyPI orders-api 0.1.0"))
			g.Expect(packages.Published).To(Equal([]c4v1beta1.CrewReleasePackage{
				{Ecosystem: "npm", Name: "@theos/orders-api", Version: "0.1.0"},
				{Ecosystem: "PyPI", Name: "orders-api", Version: "0.1.0"}}))
			g.Expect(moduleEvents(g, ns, "PackagePublished")).To(ConsistOf(
				And(HaveField("Type", "Normal"), HaveField("Action", "PublishNpmPackage"),
					HaveField("Note", "Published npm @theos/orders-api 0.1.0")),
				And(HaveField("Type", "Normal"), HaveField("Action", "PublishPyPIPackage"),
					HaveField("Note", "Published PyPI orders-api 0.1.0"))))
		}).Should(Succeed())
		// The Crew polls each second; a second upload, or a second Event per
		// package, would show. A Crew read from a cache that lags its status
		// may ask once more, and finds the packages in their registries.
		Consistently(func(g Gomega) {
			g.Expect(gitFake.PackageUploads(ns, "npm", "@theos/orders-api", "0.1.0")).To(Equal(1))
			g.Expect(gitFake.PackageUploads(ns, "PyPI", "orders-api", "0.1.0")).To(Equal(1))
			g.Expect(gitFake.PackagePublishes(ns + "/orders-api@v0.1.0")).To(BeNumerically("<=", 2))
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
		packagedRelease(ns, "completed", "success", []fake.ReleaseFile{{Name: "orders-api-0.1.0.vsix", Data: []byte("vsix")}})

		Eventually(func(g Gomega) {
			packages := releasePackages(g, ns, "NoPackage", metav1.ConditionTrue, "NoPackage")
			g.Expect(packages.Message).To(Equal("v0.1.0 attached no npm tarball, wheel or sdist; no package to publish"))
			g.Expect(packages.Published).To(BeEmpty())
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(moduleEvents(g, ns, "PackagePublished")).To(BeEmpty())
			g.Expect(moduleEvents(g, ns, "InvalidPackage")).To(BeEmpty())
			g.Expect(gitFake.PackagePublishes(ns + "/orders-api@v0.1.0")).To(BeNumerically("<=", 2))
		}, 3*time.Second).Should(Succeed())
	})

	It("leaves an unscoped npm tarball alone and names it, publishing the wheel", func() {
		ns := newProjectNamespace()
		unscoped := fake.Package{Ecosystem: ports.EcosystemNpm, Name: "orders-api", Version: "0.1.0",
			File: "orders-api-0.1.0.tgz"}
		packagedRelease(ns, "completed", "success",
			[]fake.ReleaseFile{{Name: unscoped.File, Data: []byte("tgz")}, ordersWheel}, unscoped, ordersPyPI)

		Eventually(func(g Gomega) {
			packages := releasePackages(g, ns, "Published", metav1.ConditionTrue, "PackagesPublished")
			g.Expect(packages.Message).To(Equal("Published PyPI orders-api 0.1.0; left orders-api-0.1.0.tgz alone: " +
				"orders-api is not under the scope @theos"))
			g.Expect(packages.Published).To(Equal([]c4v1beta1.CrewReleasePackage{
				{Ecosystem: "PyPI", Name: "orders-api", Version: "0.1.0"}}))
			g.Expect(moduleEvents(g, ns, "PackagePublished")).To(HaveLen(1))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.PackageUploads(ns, "npm", "orders-api", "0.1.0")).To(BeZero())
			g.Expect(moduleEvents(g, ns, "InvalidPackage")).To(BeEmpty())
		}, 3*time.Second).Should(Succeed())
	})

	It("reports a tarball whose version isn't the tag's as Invalid, once", func() {
		ns := newProjectNamespace()
		wrong := fake.Package{Ecosystem: ports.EcosystemNpm, Name: "@theos/orders-api", Version: "0.2.0",
			File: "theos-orders-api-0.2.0.tgz"}
		packagedRelease(ns, "completed", "success",
			[]fake.ReleaseFile{{Name: wrong.File, Data: []byte("tgz")}, ordersWheel}, wrong, ordersPyPI)

		message := "Did not publish theos-orders-api-0.2.0.tgz: version 0.2.0 is not the tag's, 0.1.0"
		Eventually(func(g Gomega) {
			packages := releasePackages(g, ns, "Invalid", metav1.ConditionFalse, "InvalidPackage")
			g.Expect(packages.Message).To(Equal(message))
			g.Expect(packages.Published).To(Equal([]c4v1beta1.CrewReleasePackage{
				{Ecosystem: "PyPI", Name: "orders-api", Version: "0.1.0"}}), "the good files are still published")
			g.Expect(moduleEvents(g, ns, "InvalidPackage")).To(ConsistOf(And(HaveField("Type", "Warning"),
				HaveField("Note", message))))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.PackageUploads(ns, "npm", "@theos/orders-api", "0.2.0")).To(BeZero())
			g.Expect(gitFake.PackageUploads(ns, "PyPI", "orders-api", "0.1.0")).To(Equal(1))
			g.Expect(gitFake.PackagePublishes(ns + "/orders-api@v0.1.0")).To(BeNumerically("<=", 2))
			events := moduleEvents(g, ns, "InvalidPackage")
			g.Expect(events).To(HaveLen(1))
			g.Expect(events[0].Series).To(BeNil())
		}, 3*time.Second).Should(Succeed())
	})

	It("tries again when the host can't be reached, then publishes", func() {
		ns := newProjectNamespace()
		gitFake.FailPackagePublishes(ns, "orders-api", 1)
		packagedRelease(ns, "completed", "success", []fake.ReleaseFile{ordersTarball, ordersWheel}, ordersNpm, ordersPyPI)

		Eventually(func(g Gomega) {
			packages := releasePackages(g, ns, "Failed", metav1.ConditionFalse, "GitHostUnavailable")
			g.Expect(packages.Message).To(Equal("Could not publish the packages of v0.1.0: gitea unavailable"))
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			packages := releasePackages(g, ns, "Published", metav1.ConditionTrue, "PackagesPublished")
			g.Expect(packages.Published).To(HaveLen(2))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.PackageUploads(ns, "npm", "@theos/orders-api", "0.1.0")).To(Equal(1))
			g.Expect(gitFake.PackageUploads(ns, "PyPI", "orders-api", "0.1.0")).To(Equal(1))
			g.Expect(moduleEvents(g, ns, "PackagePublished")).To(HaveLen(2))
		}, 3*time.Second).Should(Succeed())
	})

	It("leaves a tarball outside the GitProvider's scope alone, naming the scope it lacks", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		setNpmScope(ns, "@acme")
		other := fake.Package{Ecosystem: ports.EcosystemNpm, Name: "@theos/x", Version: "0.1.0",
			File: "theos-x-0.1.0.tgz"}
		packagedReleaseOf(ns, "completed", "success", []fake.ReleaseFile{{Name: other.File, Data: []byte("tgz")}}, other)

		Eventually(func(g Gomega) {
			packages := releasePackages(g, ns, "NoPackage", metav1.ConditionTrue, "NoPackage")
			g.Expect(packages.Message).To(Equal("v0.1.0 attached no npm tarball, wheel or sdist; no package to publish; " +
				"left theos-x-0.1.0.tgz alone: @theos/x is not under the scope @acme"))
			g.Expect(packages.Published).To(BeEmpty())
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.PackageUploads(ns, "npm", "@theos/x", "0.1.0")).To(BeZero())
			g.Expect(moduleEvents(g, ns, "PackagePublished")).To(BeEmpty())
			g.Expect(moduleEvents(g, ns, "InvalidPackage")).To(BeEmpty())
		}, 3*time.Second).Should(Succeed())
	})
	It("publishes to a registry of its own with its Secret's token, and with none without the key (#1729)", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		const npmToken = "npm-publish-token-1729"
		Expect(k8sClient.Create(ctx, &corev1.Secret{ObjectMeta: objectMeta(ns, "acme-npm"),
			Data: map[string][]byte{"token": []byte(npmToken), "readToken": []byte("npm-read")}})).To(Succeed())
		Expect(k8sClient.Create(ctx, &corev1.Secret{ObjectMeta: objectMeta(ns, "acme-pypi"),
			Data: map[string][]byte{"readToken": []byte("pypi-read")}})).To(Succeed())
		provider := &c4v1beta1.GitProvider{}
		Expect(k8sClient.Get(ctx, key(ns, "gitea"), provider)).To(Succeed())
		provider.Spec.Packages = &c4v1beta1.PackagesSpec{
			NPM: &c4v1beta1.NpmPackagesSpec{Scope: "@theos", Registry: "https://npm.example.com/",
				CredentialsSecretRef: &c4v1beta1.LocalRef{Name: "acme-npm"}},
			PyPI: &c4v1beta1.PyPIPackagesSpec{Index: "https://pypi.example.com/simple",
				Upload: "https://pypi.example.com/legacy/", CredentialsSecretRef: &c4v1beta1.LocalRef{Name: "acme-pypi"}}}
		Expect(k8sClient.Update(ctx, provider)).To(Succeed())
		packagedReleaseOf(ns, "completed", "success", []fake.ReleaseFile{ordersTarball, ordersWheel}, ordersNpm, ordersPyPI)

		Eventually(func(g Gomega) {
			releasePackages(g, ns, "Published", metav1.ConditionTrue, "PackagesPublished")
		}).Should(Succeed())
		Expect(gitFake.PackageUploadToken(ns, "npm", "@theos/orders-api", "0.1.0")).To(Equal(npmToken))
		Expect(gitFake.PackageUploadToken(ns, "PyPI", "orders-api", "0.1.0")).To(BeEmpty(),
			"a Secret without the token key publishes with none")
		crew := getCrew(Default, ns)
		status, err := json.Marshal(crew.Status)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(status)).NotTo(ContainSubstring(npmToken))
		for _, e := range moduleEvents(Default, ns, "PackagePublished") {
			Expect(e.Note).NotTo(ContainSubstring(npmToken))
		}
	})
})
