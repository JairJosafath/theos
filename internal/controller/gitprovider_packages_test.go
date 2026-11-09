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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

var _ = Describe("GitProvider packages", func() {
	packagesProvider := func(ns string, packages *c4v1beta1.PackagesSpec) *c4v1beta1.GitProvider {
		return &c4v1beta1.GitProvider{ObjectMeta: objectMeta(ns, "packaged"), Spec: c4v1beta1.GitProviderSpec{
			URL: "http://gitea.invalid", Owner: ns, CredentialsSecretRef: c4v1beta1.LocalRef{Name: "none"},
			Packages: packages}}
	}

	DescribeTable("refuses a spec.packages its schema rejects (#1230)",
		func(packages *c4v1beta1.PackagesSpec, message string) {
			ns := newProjectNamespace()
			Expect(k8sClient.Create(ctx, packagesProvider(ns, packages))).To(MatchError(ContainSubstring(message)))
		},
		Entry("a scope without its @", &c4v1beta1.PackagesSpec{NPM: &c4v1beta1.NpmPackagesSpec{Scope: "theos"}},
			"spec.packages.npm.scope"),
		Entry("a scope with a capital", &c4v1beta1.PackagesSpec{NPM: &c4v1beta1.NpmPackagesSpec{Scope: "@Theos"}},
			"spec.packages.npm.scope"),
		Entry("an npm registry that is no URL", &c4v1beta1.PackagesSpec{NPM: &c4v1beta1.NpmPackagesSpec{
			Registry: "npm.example.com/registry"}}, "spec.packages.npm.registry"),
		Entry("a PyPI index that is no URL", &c4v1beta1.PackagesSpec{PyPI: &c4v1beta1.PyPIPackagesSpec{
			Index: "ftp://pypi.example.com/simple"}}, "spec.packages.pypi.index"),
		Entry("a PyPI upload that is no URL", &c4v1beta1.PackagesSpec{PyPI: &c4v1beta1.PyPIPackagesSpec{
			Upload: "pypi.example.com"}}, "spec.packages.pypi.upload"),
	)

	It("takes a scope and registries of its own, and none", func() {
		ns := newProjectNamespace()
		Expect(k8sClient.Create(ctx, packagesProvider(ns, &c4v1beta1.PackagesSpec{
			NPM: &c4v1beta1.NpmPackagesSpec{Scope: "@acme.io", Registry: "https://npm.example.com/registry",
				CredentialsSecretRef: &c4v1beta1.LocalRef{Name: "acme-npm"}},
			PyPI: &c4v1beta1.PyPIPackagesSpec{Index: "http://pypi.example.com/simple",
				Upload: "https://pypi.example.com/legacy/"},
		}))).To(Succeed())
		other := packagesProvider(ns, nil)
		other.Name = "plain"
		Expect(k8sClient.Create(ctx, other)).To(Succeed())
	})
})
