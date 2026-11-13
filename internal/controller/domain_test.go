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

	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// newDomain is a Domain catalog entry pinned to a stand-in template.
func newDomain(ns, name string) *c4v1beta1.Domain {
	return &c4v1beta1.Domain{
		ObjectMeta: objectMeta(ns, name),
		Spec: c4v1beta1.DomainSpec{
			DisplayName: name,
			Template:    c4v1beta1.TemplateSource{URL: templatesURL + name + "-domain", Ref: "v1.0.0"},
		},
	}
}

var _ = Describe("Domain", func() {
	It("needs a template with a url and a ref", func() {
		ns := newProjectNamespace()
		domain := newDomain(ns, "webshop")
		domain.Spec.Template.Ref = ""
		Expect(k8sClient.Create(ctx, domain)).To(MatchError(ContainSubstring("spec.template.ref")))
		domain.Spec.Template = c4v1beta1.TemplateSource{URL: "gitea/templates/webshop-domain", Ref: "v1.0.0"}
		Expect(k8sClient.Create(ctx, domain)).To(MatchError(ContainSubstring("spec.template.url")))
		mustCreate(newDomain(ns, "webshop"))
	})
})

var _ = Describe("TemplateSource path", func() {
	// #35: a relative directory of the repository, written one way only.
	DescribeTable("refuses a path that isn't a clean relative directory",
		func(path, message string) {
			domain := newDomain(newProjectNamespace(), "ops")
			domain.Spec.Template.Path = path
			Expect(k8sClient.Create(ctx, domain)).To(MatchError(ContainSubstring(message)))
		},
		Entry("absolute", "/domains/ops", "path is relative to the repository's root, not absolute"),
		Entry("a trailing /", "domains/ops/", "path has no trailing or doubled /"),
		Entry("a doubled /", "domains//ops", "path has no trailing or doubled /"),
		Entry("a .. segment", "domains/../ops", "path has no . or .. segments"),
		Entry("a leading ..", "../ops", "path has no . or .. segments"),
		Entry("a . segment", "./domains/ops", "path has no . or .. segments"),
	)

	It("takes a relative directory, names with dots included", func() {
		ns := newProjectNamespace()
		domain := newDomain(ns, "ops")
		domain.Spec.Template.Path = "domains/ops.v2/..hidden"
		mustCreate(domain)
	})
})

var _ = Describe("SoftwareSystem domainRefs", func() {
	It("names the Domains an Internal system is about", func() {
		ns := newProjectNamespace()
		system := newSystem(ns, "webshop-core")
		system.Spec.DomainRefs = []string{"webshop", "loyalty"}
		mustCreate(system)
	})

	It("names at most 5 Domains", func() {
		ns := newProjectNamespace()
		system := newSystem(ns, "webshop-core")
		system.Spec.DomainRefs = []string{"a", "b", "c", "d", "e", "f"}
		Expect(k8sClient.Create(ctx, system)).To(MatchError(ContainSubstring("must have at most 5 items")))
	})

	It("names each Domain once", func() {
		ns := newProjectNamespace()
		system := newSystem(ns, "webshop-core")
		system.Spec.DomainRefs = []string{"webshop", "webshop"}
		Expect(k8sClient.Create(ctx, system)).To(MatchError(ContainSubstring("Duplicate value")))
	})

	It("refuses an empty name", func() {
		ns := newProjectNamespace()
		system := newSystem(ns, "webshop-core")
		system.Spec.DomainRefs = []string{""}
		Expect(k8sClient.Create(ctx, system)).To(MatchError(ContainSubstring("spec.domainRefs[0]")))
	})

	It("is refused on an External system, on create and on update", func() {
		ns := newProjectNamespace()
		provider := newSystem(ns, "payment-provider")
		provider.Spec.Location = c4v1beta1.SystemExternal
		provider.Spec.DomainRefs = []string{"payments"}
		Expect(k8sClient.Create(ctx, provider)).To(MatchError(ContainSubstring(
			"an External system names no domainRefs")))

		provider.Spec.DomainRefs = nil
		mustCreate(provider)
		// A patch sends no resourceVersion, so the Project adopting the
		// system meanwhile cannot fail it with a conflict.
		orig := provider.DeepCopy()
		provider.Spec.DomainRefs = []string{"payments"}
		Expect(k8sClient.Patch(ctx, provider, client.MergeFrom(orig))).To(MatchError(ContainSubstring(
			"an External system names no domainRefs")))
	})
})
