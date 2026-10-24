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

	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
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
