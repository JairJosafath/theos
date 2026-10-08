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

	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// runningLinterTask makes a Task of the linter's Crew that stays running: its
// Agent doesn't exist, so it waits for one.
func runningLinterTask(ns, name string) {
	GinkgoHelper()
	task := newTask(ns, name, "Add a lint rule.")
	task.Labels = map[string]string{LabelCrew: "linter"}
	task.Spec.ContainerRef = c4v1alpha1.LocalRef{Name: "linter"}
	task.Spec.AgentRef = c4v1alpha1.LocalRef{Name: "nobody"}
	mustCreate(task)
}

// endLinterTask sets the Task's phase to a final one, as its round would.
func endLinterTask(ns, name string, phase c4v1alpha1.TaskPhase) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		task := getTask(g, ns, name)
		task.Status.Phase, task.Status.CompletedAt = phase, &metav1.Time{Time: time.Now()}
		g.Expect(k8sClient.Status().Update(ctx, task)).To(Succeed())
	}).Should(Succeed())
}

// pausedMessage waits for the Brief's Paused condition to be True with message.
func pausedMessage(ns, message string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		cond := condition(getBrief(g, ns).Status.Conditions, c4v1alpha1.ConditionPaused)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		g.Expect(cond.Message).To(Equal(message))
	}, wholeRound).Should(Succeed())
}

// Decisions 4 and 5 of docs/plans/upgrade-in-place.md (#683).
var _ = Describe("Brief paused", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("pauses and marks its Crews, holds a prompt, counts down its Tasks and resumes only what it paused", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, linterPlan(ns, 0))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}, wholeRound).Should(Succeed())

		By("a person pausing one of the Brief's Crews by hand, and keeping a Crew of their own")
		crewOf := func(name string, labels map[string]string, paused bool) *c4v1alpha1.Crew {
			om := objectMeta(ns, name)
			om.Labels = labels
			return &c4v1alpha1.Crew{ObjectMeta: om, Spec: c4v1alpha1.CrewSpec{
				ContainerRef: c4v1alpha1.LocalRef{Name: name},
				Implementer:  c4v1alpha1.LocalRef{Name: "implementer"}, Reviewer: c4v1alpha1.LocalRef{Name: "reviewer"},
				Paused: paused}}
		}
		mustCreate(crewOf("by-hand", map[string]string{c4v1alpha1.LabelBrief: "cel-lens"}, true))
		mustCreate(crewOf("other", nil, false))
		runningLinterTask(ns, "linter-a")
		runningLinterTask(ns, "linter-b")

		By("pausing the Brief")
		brief := getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Paused = true })).To(Succeed())
		getCrew := func(g Gomega, name string) *c4v1alpha1.Crew {
			crew := &c4v1alpha1.Crew{}
			g.Expect(k8sClient.Get(ctx, key(ns, name), crew)).To(Succeed())
			return crew
		}
		Eventually(func(g Gomega) {
			linter := getCrew(g, "linter")
			g.Expect(linter.Spec.Paused).To(BeTrue())
			g.Expect(linter.Annotations).To(HaveKeyWithValue(c4v1alpha1.AnnotationPausedBy, c4v1alpha1.PausedByBrief))
		}).Should(Succeed())
		byHand := getCrew(Default, "by-hand")
		Expect(byHand.Spec.Paused).To(BeTrue())
		Expect(byHand.Annotations).NotTo(HaveKey(c4v1alpha1.AnnotationPausedBy))
		Expect(getCrew(Default, "other").Spec.Paused).To(BeFalse())
		pausedMessage(ns, "Paused; 2 Tasks still running: linter-a, linter-b")

		By("setting a hand unpause of a Crew it paused back")
		linter := getCrew(Default, "linter")
		Expect(patch(linter, func() { linter.Spec.Paused = false })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, "linter").Spec.Paused).To(BeTrue())
		}).Should(Succeed())

		By("holding a new prompt")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Add a rule."} })).To(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Round).To(Equal(int32(1)))
		}, "2s").Should(Succeed())

		By("counting the running Tasks down to nothing")
		endLinterTask(ns, "linter-a", c4v1alpha1.TaskMerged)
		pausedMessage(ns, "Paused; 1 Task still running: linter-b")
		endLinterTask(ns, "linter-b", c4v1alpha1.TaskFailed)
		pausedMessage(ns, "Paused; nothing is running")

		By("resuming: the Crew it paused goes on, the one paused by hand stays paused")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Paused = false })).To(Succeed())
		Eventually(func(g Gomega) {
			linter := getCrew(g, "linter")
			g.Expect(linter.Spec.Paused).To(BeFalse())
			g.Expect(linter.Annotations).NotTo(HaveKey(c4v1alpha1.AnnotationPausedBy))
			b := getBrief(g, ns)
			cond := condition(b.Status.Conditions, c4v1alpha1.ConditionPaused)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(b.Status.Round).To(Equal(int32(2)))
		}, wholeRound).Should(Succeed())
		Expect(getCrew(Default, "by-hand").Spec.Paused).To(BeTrue())
		Expect(getCrew(Default, "other").Spec.Paused).To(BeFalse())
	})

	It("lets a planner Task already started run to its end and makes its Crews paused", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Round).To(Equal(int32(1)))
		}).Should(Succeed())

		brief := getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Paused = true })).To(Succeed())
		pausedMessage(ns, "Paused; 1 Task still running: cel-lens-plan-1")

		planRound(ns, 1, 1, linterPlan(ns, 0))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}, wholeRound).Should(Succeed())
		crew := linterCrew(Default, ns)
		Expect(crew.Spec.Paused).To(BeTrue())
		Expect(crew.Annotations).To(HaveKeyWithValue(c4v1alpha1.AnnotationPausedBy, c4v1alpha1.PausedByBrief))
		pausedMessage(ns, "Paused; nothing is running")
	})

	It("starts no planner for a Brief made paused, until it resumes", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		brief := newBrief(ns, "cel-lens")
		brief.Spec.Paused = true
		mustCreate(brief)
		pausedMessage(ns, "Paused; nothing is running")
		Consistently(func(g Gomega) {
			var tasks c4v1alpha1.TaskList
			g.Expect(k8sClient.List(ctx, &tasks, client.InNamespace(ns))).To(Succeed())
			g.Expect(tasks.Items).To(BeEmpty())
			g.Expect(getBrief(g, ns).Status.Round).To(BeZero())
		}, "2s").Should(Succeed())

		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Paused = false })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "cel-lens-plan-1")).NotTo(BeNil())
		}).Should(Succeed())
	})
})
