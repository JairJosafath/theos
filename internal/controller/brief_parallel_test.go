/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// parallelPlan is linterPlan with parallel.yaml relaying a prompt's n.
func parallelPlan(ns string, issues int, n int32) scaffold.Tree {
	files := linterPlan(ns, issues)
	files[model.PlanParallelFile] = scaffold.File{Data: []byte(fmt.Sprintf(
		"parallel: %d\nwhy: The prompt says \"build up to %d at a time\".\n", n, n))}
	return files
}

// Step 1 of docs/plans/done/brief-parallel.md: the Brief sets the parallel of the
// Crews it made.
var _ = Describe("Brief parallel", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("makes its Crews with the default, follows spec.parallel and sets a hand edit back", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, linterPlan(ns, 1))

		By("making the Crew with parallel 3 when the Brief says nothing")
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}).Should(Succeed())
		Expect(linterCrew(Default, ns).Spec.Parallel).To(Equal(model.BriefParallelDefault))
		Eventually(func(g Gomega) {
			app := briefApp(g, ns, "linter")
			g.Expect(app.Crew).To(Equal("linter"))
			g.Expect(app.Parallel).To(Equal(model.BriefParallelDefault))
		}).Should(Succeed())

		By("patching the earlier round's Crew once spec.parallel is set")
		brief := getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Parallel = 2 })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(linterCrew(g, ns).Spec.Parallel).To(Equal(int32(2)))
			g.Expect(briefApp(g, ns, "linter").Parallel).To(Equal(int32(2)))
		}).Should(Succeed())

		By("setting a hand edit of the Crew back")
		crew := linterCrew(Default, ns)
		Expect(patch(crew, func() { crew.Spec.Parallel = 5 })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(linterCrew(g, ns).Spec.Parallel).To(Equal(int32(2)))
		}).Should(Succeed())

		By("going back to the default once spec.parallel is removed")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Parallel = 0 })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(linterCrew(g, ns).Spec.Parallel).To(Equal(model.BriefParallelDefault))
		}).Should(Succeed())
	})

	It("makes new Crews with spec.parallel and leaves a Crew it didn't make alone", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(&c4v1alpha1.Crew{ObjectMeta: objectMeta(ns, "other"), Spec: c4v1alpha1.CrewSpec{
			ContainerRef: c4v1alpha1.LocalRef{Name: "other"},
			Implementer:  c4v1alpha1.LocalRef{Name: "implementer"}, Reviewer: c4v1alpha1.LocalRef{Name: "reviewer"},
			Parallel: 4}})
		brief := newBrief(ns, "cel-lens")
		brief.Spec.Parallel = 2
		mustCreate(brief)
		planRound(ns, 1, 1, linterPlan(ns, 1))

		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}).Should(Succeed())
		Expect(linterCrew(Default, ns).Spec.Parallel).To(Equal(int32(2)))
		other := &c4v1alpha1.Crew{}
		Expect(k8sClient.Get(ctx, key(ns, "other"), other)).To(Succeed())
		Expect(other.Spec.Parallel).To(Equal(int32(4)))
	})

	// Step 2: a prompt says it, and the planner relays it in parallel.yaml.
	It("applies a prompt's parallel to every Crew the Brief made, earlier rounds' too", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}).Should(Succeed())
		Expect(linterCrew(Default, ns).Spec.Parallel).To(Equal(model.BriefParallelDefault))

		By("applying round 2's parallel.yaml of 2, from its new prompt, to round 1's Crew")
		brief := getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Build up to 2 at a time."} })).To(Succeed())
		planRound(ns, 2, 1, parallelPlan(ns, 1, 2))
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Round).To(Equal(int32(2)))
			g.Expect(b.Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
			g.Expect(b.Status.Parallel).To(Equal(int32(2)))
			g.Expect(b.Status.ParallelRound).To(Equal(int32(2)))
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(linterCrew(g, ns).Spec.Parallel).To(Equal(int32(2)))
			g.Expect(briefApp(g, ns, "linter").Parallel).To(Equal(int32(2)))
		}).Should(Succeed())

		By("telling the planner where the number comes from")
		Eventually(func(g Gomega) {
			g.Expect(string(gitFake.mainFiles(ns, briefRepo)[model.BriefFile].Data)).
				To(ContainSubstring("up to 2 issues at once, as a prompt of round 2 asked"))
		}).Should(Succeed())

		By("letting spec.parallel win over the prompt's number")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Parallel = 4 })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(linterCrew(g, ns).Spec.Parallel).To(Equal(int32(4)))
		}).Should(Succeed())

		By("refusing a parallel.yaml while spec.parallel is set")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() {
			brief.Spec.Prompts = append(brief.Spec.Prompts, "One at a time now.")
		})).To(Succeed())
		planRound(ns, 3, 1, parallelPlan(ns, 1, 1))
		Eventually(func(g Gomega) {
			g.Expect(briefEventNotes(g, ns, eventPlanRefused)).To(ContainElement(And(
				ContainSubstring(model.PlanParallelFile), ContainSubstring("spec.parallel sets it to 4"))))
		}).Should(Succeed())
		Expect(getBrief(Default, ns).Status.Parallel).To(Equal(int32(2)))

		By("going back to the prompt's number once spec.parallel is removed")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Parallel = 0 })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(linterCrew(g, ns).Spec.Parallel).To(Equal(int32(2)))
		}).Should(Succeed())
	})

	It("makes the first round's Crews with the number its plan relays", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, parallelPlan(ns, 1, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}).Should(Succeed())
		Expect(linterCrew(Default, ns).Spec.Parallel).To(Equal(int32(1)))
		Expect(getBrief(Default, ns).Status.Parallel).To(Equal(int32(1)))
	})

	DescribeTable("spec.parallel",
		func(parallel int32, valid bool) {
			brief := newBrief(newProjectNamespace(), "cel-lens")
			brief.Spec.Parallel = parallel
			err := k8sClient.Create(ctx, brief)
			if valid {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(err).To(MatchError(ContainSubstring("spec.parallel")))
			}
		},
		Entry("takes 1", int32(1), true),
		Entry("takes 5", int32(5), true),
		Entry("refuses 6", int32(6), false),
		Entry("refuses a negative number", int32(-1), false),
	)
})
