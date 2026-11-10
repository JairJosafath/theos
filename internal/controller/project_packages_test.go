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
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

var _ = Describe("Package registry Actions", func() {
	It("sets the owner's Actions variables and secrets for a spec.packages of its own, and deletes them for the default (#1230)", func() {
		ns := newProjectNamespace()
		mustCreate(
			&corev1.Secret{ObjectMeta: objectMeta(ns, "gitea-credentials"),
				StringData: map[string]string{"username": "c4admin", "token": "admin-token"}},
			&corev1.Secret{ObjectMeta: objectMeta(ns, "acme-npm"),
				StringData: map[string]string{"token": "npm-publish", "readToken": "npm-read"}},
			&corev1.Secret{ObjectMeta: objectMeta(ns, "acme-pypi"), StringData: map[string]string{"token": "pypi-publish"}},
		)
		provider := &c4v1beta1.GitProvider{ObjectMeta: objectMeta(ns, "gitea"), Spec: c4v1beta1.GitProviderSpec{
			URL: "http://gitea.gitea.svc.cluster.local:3000", Owner: ns,
			CredentialsSecretRef: c4v1beta1.LocalRef{Name: "gitea-credentials"},
			Packages: &c4v1beta1.PackagesSpec{
				NPM: &c4v1beta1.NpmPackagesSpec{Scope: "@acme", Registry: "https://npm.example.com/registry/",
					CredentialsSecretRef: &c4v1beta1.LocalRef{Name: "acme-npm"}},
				PyPI: &c4v1beta1.PyPIPackagesSpec{CredentialsSecretRef: &c4v1beta1.LocalRef{Name: "acme-pypi"}},
			},
		}}
		mustCreate(provider)

		By("setting the three variables, the PyPI index defaulted, and the one secret with a readToken")
		Eventually(func() map[string]string { return gitFake.ActionsVariables(ns) }).Should(Equal(map[string]string{
			ActionsVarNpmScope:    "@acme",
			ActionsVarNpmRegistry: "https://npm.example.com/registry/",
			ActionsVarPyPIIndex:   "http://gitea.gitea.svc.cluster.local:3000/api/packages/" + ns + "/pypi/simple",
		}))
		Expect(gitFake.ActionsSecrets(ns)).To(Equal(map[string]string{ActionsSecretNpmToken: "npm-read"}))

		By("deleting all five when spec.packages returns to the default")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(provider), provider)).To(Succeed())
		provider.Spec.Packages = nil
		Expect(k8sClient.Update(ctx, provider)).To(Succeed())
		Eventually(func() map[string]string { return gitFake.ActionsVariables(ns) }).Should(BeEmpty())
		Expect(gitFake.ActionsSecrets(ns)).To(BeEmpty())
	})
})
