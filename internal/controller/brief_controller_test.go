/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

func newBrief(ns, name string, prompts ...string) *c4v1alpha1.Brief {
	return &c4v1alpha1.Brief{
		ObjectMeta: objectMeta(ns, name),
		Spec:       c4v1alpha1.BriefSpec{Idea: "A linter for CEL rules in CRDs.", Prompts: prompts},
	}
}

var _ = Describe("Brief", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("reaches Pending, with the default Agents and GitProvider", func() {
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)
		Expect(brief.Spec.Agents).To(Equal(c4v1alpha1.BriefAgents{
			Planner:     c4v1alpha1.LocalRef{Name: "planner"},
			Implementer: c4v1alpha1.LocalRef{Name: "implementer"},
			Reviewer:    c4v1alpha1.LocalRef{Name: "reviewer"},
			Designer:    c4v1alpha1.LocalRef{Name: "designer"},
		}))
		Expect(brief.Spec.GitProviderRef.Name).To(Equal("gitea"))
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "cel-lens"), brief)).To(Succeed())
			g.Expect(brief.Status.Phase).To(Equal(c4v1alpha1.BriefPending))
		}).Should(Succeed())
	})

	It("keeps Agents it is given", func() {
		brief := newBrief(ns, "own-agents")
		brief.Spec.Agents.Planner.Name = "opus"
		mustCreate(brief)
		Expect(brief.Spec.Agents.Planner.Name).To(Equal("opus"))
		Expect(brief.Spec.Agents.Reviewer.Name).To(Equal("reviewer"))
	})

	Describe("idea", func() {
		It("is required", func() {
			Expect(k8sClient.Create(ctx, &c4v1alpha1.Brief{ObjectMeta: objectMeta(ns, "no-idea")})).
				To(MatchError(ContainSubstring("spec.idea")))
		})

		It("isn't empty", func() {
			brief := newBrief(ns, "empty")
			brief.Spec.Idea = ""
			Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring("spec.idea")))
		})

		It("takes 4000 characters and no more", func() {
			brief := newBrief(ns, "long")
			brief.Spec.Idea = strings.Repeat("a", 4000)
			mustCreate(brief)
			brief = newBrief(ns, "too-long")
			brief.Spec.Idea = strings.Repeat("a", 4001)
			Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring("spec.idea")))
		})

		It("can't change", func() {
			brief := newBrief(ns, "fixed")
			mustCreate(brief)
			Expect(patch(brief, func() { brief.Spec.Idea = "Another app." })).
				To(MatchError(ContainSubstring("idea is immutable")))
		})
	})

	Describe("prompts", func() {
		const appendOnly = "prompts are append-only"

		It("take an appended prompt", func() {
			brief := newBrief(ns, "first")
			mustCreate(brief)
			Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Support VAP files."} })).To(Succeed())
			Expect(patch(brief, func() { brief.Spec.Prompts = append(brief.Spec.Prompts, "And a CLI.") })).To(Succeed())
			Expect(brief.Spec.Prompts).To(Equal([]string{"Support VAP files.", "And a CLI."}))
		})

		It("refuse a changed earlier prompt", func() {
			brief := newBrief(ns, "changed", "one", "two")
			mustCreate(brief)
			Expect(patch(brief, func() { brief.Spec.Prompts = []string{"one", "2", "three"} })).
				To(MatchError(ContainSubstring(appendOnly)))
		})

		It("refuse a removed or reordered prompt", func() {
			brief := newBrief(ns, "removed", "one", "two")
			mustCreate(brief)
			Expect(patch(brief, func() { brief.Spec.Prompts = []string{"one"} })).
				To(MatchError(ContainSubstring(appendOnly)))
			Expect(patch(brief, func() { brief.Spec.Prompts = []string{"two", "one"} })).
				To(MatchError(ContainSubstring(appendOnly)))
			Expect(patch(brief, func() { brief.Spec.Prompts = nil })).
				To(MatchError(ContainSubstring(appendOnly)))
		})

		It("refuse an empty prompt", func() {
			Expect(k8sClient.Create(ctx, newBrief(ns, "empty", ""))).
				To(MatchError(ContainSubstring("spec.prompts[0]")))
		})

		It("may drop an empty list", func() {
			brief := newBrief(ns, "empty-list")
			mustCreate(brief)
			Expect(k8sClient.Patch(ctx, brief, client.RawPatch(types.MergePatchType,
				[]byte(`{"spec":{"prompts":[]}}`)))).To(Succeed())
			Expect(k8sClient.Patch(ctx, brief, client.RawPatch(types.MergePatchType,
				[]byte(`{"spec":{"prompts":null}}`)))).To(Succeed())
		})

		It("take 50 prompts of 2000 characters and no more", func() {
			brief := newBrief(ns, "full", make50(strings.Repeat("p", 2000))...)
			mustCreate(brief)
			Expect(k8sClient.Create(ctx, newBrief(ns, "too-many", append(make50("p"), "p")...))).
				To(MatchError(ContainSubstring("spec.prompts")))
			Expect(k8sClient.Create(ctx, newBrief(ns, "too-long", strings.Repeat("p", 2001)))).
				To(MatchError(ContainSubstring("spec.prompts[0]")))
		})
	})
})

// make50 returns 50 copies of prompt.
func make50(prompt string) []string {
	prompts := make([]string, 50)
	for i := range prompts {
		prompts[i] = prompt
	}
	return prompts
}
