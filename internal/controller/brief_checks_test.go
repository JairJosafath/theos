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

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// checksPlan is linterPlan with the linter Container's checks set.
func checksPlan(ns string, checks c4v1beta1.ChecksMode) scaffold.Tree {
	files := linterPlan(ns, 1)
	const anchor = "  deploymentRef: {name: k8s-manifests}\n"
	files[model.PlanModelFile] = scaffold.File{Data: []byte(strings.Replace(
		string(files[model.PlanModelFile].Data), anchor, anchor+"  checks: "+string(checks)+"\n", 1))}
	return files
}

func linterContainer(g Gomega, ns string) *c4v1beta1.Container {
	c := &c4v1beta1.Container{}
	g.Expect(k8sClient.Get(ctx, key(ns, "linter"), c)).To(Succeed())
	return c
}

// A prompt sets when an app's CI runs the full check (#977).
var _ = Describe("Brief checks", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("sets a prompt's checks on its Container and leaves a hand-set one alone when the plan says nothing", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(linterContainer(Default, ns).Spec.Checks).To(Equal(c4v1beta1.ChecksEveryChange))

		By("showing the planner each Container's checks")
		Eventually(func(g Gomega) {
			g.Expect(string(gitFake.mainFiles(ns, briefRepo)[model.WorkspaceContainersFile].Data)).
				To(ContainSubstring("checks: EveryChange"))
		}).Should(Succeed())

		By("setting BeforeRelease when round 2's prompt asks")
		brief := getBrief(Default, ns)
		Expect(patch(brief, func() {
			brief.Spec.Prompts = []string{"Run the linter's heavy checks only when a release is prepared."}
		})).To(Succeed())
		planRound(ns, 2, 1, checksPlan(ns, c4v1beta1.ChecksBeforeRelease))
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Round).To(Equal(int32(2)))
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(linterContainer(Default, ns).Spec.Checks).To(Equal(c4v1beta1.ChecksBeforeRelease))

		By("saying in the round's summary that it can't take effect without a Standard")
		history := getBrief(Default, ns).Status.History
		Expect(history[len(history)-1].Summary).To(ContainSubstring(
			"linter asks for checks BeforeRelease, but has no Standard"))

		By("leaving a hand-set value alone when round 3's plan leaves checks out")
		container := linterContainer(Default, ns)
		Expect(patch(container, func() { container.Spec.Checks = c4v1beta1.ChecksEveryChange })).To(Succeed())
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() {
			brief.Spec.Prompts = append(brief.Spec.Prompts, "Lint one more rule.")
		})).To(Succeed())
		planRound(ns, 3, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Round).To(Equal(int32(3)))
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(linterContainer(Default, ns).Spec.Checks).To(Equal(c4v1beta1.ChecksEveryChange))
	})

	It("refuses a plan that sets the checks of a Container the Brief didn't make", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		container := linterContainer(Default, ns)
		Expect(patch(container, func() { delete(container.Labels, c4v1beta1.LabelBrief) })).To(Succeed())

		brief := getBrief(Default, ns)
		Expect(patch(brief, func() {
			brief.Spec.Prompts = []string{"Run the heavy checks only before a release."}
		})).To(Succeed())
		planRound(ns, 2, 1, checksPlan(ns, c4v1beta1.ChecksBeforeRelease))
		Eventually(func(g Gomega) {
			g.Expect(briefEventNotes(g, ns, eventPlanRefused)).To(ContainElement(And(
				ContainSubstring("Container/linter"), ContainSubstring("the Brief didn't make this Container"))))
		}).Should(Succeed())
		Expect(linterContainer(Default, ns).Spec.Checks).To(Equal(c4v1beta1.ChecksEveryChange))
	})
})
