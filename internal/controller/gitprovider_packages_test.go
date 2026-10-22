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

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

var _ = Describe("GitProvider packages", func() {
	packagesProvider := func(ns string, packages *c4v1alpha1.PackagesSpec) *c4v1alpha1.GitProvider {
		return &c4v1alpha1.GitProvider{ObjectMeta: objectMeta(ns, "packaged"), Spec: c4v1alpha1.GitProviderSpec{
			URL: "http://gitea.invalid", Owner: ns, CredentialsSecretRef: c4v1alpha1.LocalRef{Name: "none"},
			Packages: packages}}
	}

	DescribeTable("refuses a spec.packages its schema rejects (#1230)",
		func(packages *c4v1alpha1.PackagesSpec, message string) {
			ns := newProjectNamespace()
			Expect(k8sClient.Create(ctx, packagesProvider(ns, packages))).To(MatchError(ContainSubstring(message)))
		},
		Entry("a scope without its @", &c4v1alpha1.PackagesSpec{NPM: &c4v1alpha1.NpmPackagesSpec{Scope: "c4factory"}},
			"spec.packages.npm.scope"),
		Entry("a scope with a capital", &c4v1alpha1.PackagesSpec{NPM: &c4v1alpha1.NpmPackagesSpec{Scope: "@C4Factory"}},
			"spec.packages.npm.scope"),
		Entry("an npm registry that is no URL", &c4v1alpha1.PackagesSpec{NPM: &c4v1alpha1.NpmPackagesSpec{
			Registry: "npm.example.com/registry"}}, "spec.packages.npm.registry"),
		Entry("a PyPI index that is no URL", &c4v1alpha1.PackagesSpec{PyPI: &c4v1alpha1.PyPIPackagesSpec{
			Index: "ftp://pypi.example.com/simple"}}, "spec.packages.pypi.index"),
		Entry("a PyPI upload that is no URL", &c4v1alpha1.PackagesSpec{PyPI: &c4v1alpha1.PyPIPackagesSpec{
			Upload: "pypi.example.com"}}, "spec.packages.pypi.upload"),
	)

	It("takes a scope and registries of its own, and none", func() {
		ns := newProjectNamespace()
		Expect(k8sClient.Create(ctx, packagesProvider(ns, &c4v1alpha1.PackagesSpec{
			NPM: &c4v1alpha1.NpmPackagesSpec{Scope: "@acme.io", Registry: "https://npm.example.com/registry",
				CredentialsSecretRef: &c4v1alpha1.LocalRef{Name: "acme-npm"}},
			PyPI: &c4v1alpha1.PyPIPackagesSpec{Index: "http://pypi.example.com/simple",
				Upload: "https://pypi.example.com/legacy/"},
		}))).To(Succeed())
		other := packagesProvider(ns, nil)
		other.Name = "plain"
		Expect(k8sClient.Create(ctx, other)).To(Succeed())
	})
})
