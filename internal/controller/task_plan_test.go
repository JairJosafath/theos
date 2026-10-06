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

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// newPlanTask is a planning round of the Brief cel-lens.
func newPlanTask(ns, name string) *c4v1alpha1.Task {
	return &c4v1alpha1.Task{ObjectMeta: objectMeta(ns, name), Spec: c4v1alpha1.TaskSpec{
		Kind:     c4v1alpha1.TaskPlan,
		BriefRef: &c4v1alpha1.LocalRef{Name: "cel-lens"},
		AgentRef: c4v1alpha1.LocalRef{Name: "planner"},
	}}
}

// A Plan Task's CEL rules (docs/plans/brief.md, B3).
var _ = Describe("Plan Task", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("defaults a Task's kind to Implement", func() {
		task := newTask(ns, "implement", "Do it")
		mustCreate(task)
		Expect(task.Spec.Kind).To(Equal(c4v1alpha1.TaskImplement))
	})

	It("names a Brief and no Container", func() {
		mustCreate(newPlanTask(ns, "round-1"))
		withGoal := newPlanTask(ns, "with-goal")
		withGoal.Spec.Goal = "Plan the idea."
		mustCreate(withGoal)

		both := newPlanTask(ns, "both")
		both.Spec.ContainerRef = c4v1alpha1.LocalRef{Name: "orders-api"}
		Expect(k8sClient.Create(ctx, both)).To(MatchError(ContainSubstring("set exactly one of containerRef and briefRef")))

		neither := newPlanTask(ns, "neither")
		neither.Spec.BriefRef = nil
		Expect(k8sClient.Create(ctx, neither)).To(MatchError(ContainSubstring("set exactly one of containerRef and briefRef")))
		Expect(k8sClient.Create(ctx, neither)).To(MatchError(ContainSubstring("a Plan Task needs a briefRef")))

		noParent := newTask(ns, "no-parent", "Do it")
		noParent.Spec.ContainerRef = c4v1alpha1.LocalRef{}
		Expect(k8sClient.Create(ctx, noParent)).To(MatchError(ContainSubstring("set exactly one of containerRef and briefRef")))
	})

	It("takes a briefRef only as kind Plan", func() {
		implement := newPlanTask(ns, "implement-brief")
		implement.Spec.Kind = c4v1alpha1.TaskImplement
		implement.Spec.Goal = "Do it"
		Expect(k8sClient.Create(ctx, implement)).To(MatchError(ContainSubstring("briefRef needs kind Plan")))

		defaulted := newPlanTask(ns, "default-brief")
		defaulted.Spec.Kind = ""
		defaulted.Spec.Goal = "Do it"
		Expect(k8sClient.Create(ctx, defaulted)).To(MatchError(ContainSubstring("briefRef needs kind Plan")))
	})

	It("has no reviewer and no issue", func() {
		reviewed := newPlanTask(ns, "reviewed")
		reviewed.Spec.ReviewerRef = &c4v1alpha1.LocalRef{Name: "reviewer"}
		Expect(k8sClient.Create(ctx, reviewed)).To(MatchError(ContainSubstring("a Plan Task has no reviewerRef")))

		issue := newPlanTask(ns, "issue")
		issue.Spec.Issue = 2
		Expect(k8sClient.Create(ctx, issue)).To(MatchError(ContainSubstring("a Plan Task has no issue")))
	})

	It("keeps its kind and its Brief for life", func() {
		task := newPlanTask(ns, "keep")
		task.Spec.Goal = "Plan the idea."
		mustCreate(task)
		Expect(patch(task, func() { task.Spec.Kind = c4v1alpha1.TaskImplement })).
			To(MatchError(ContainSubstring("kind is immutable; create a new Task")))
		task = newPlanTask(ns, "keep-brief")
		mustCreate(task)
		Expect(patch(task, func() { task.Spec.BriefRef.Name = "another" })).
			To(MatchError(ContainSubstring("briefRef is immutable")))
		Expect(patch(task, func() { task.Spec.BriefRef, task.Spec.ContainerRef = nil, c4v1alpha1.LocalRef{Name: "orders-api"} })).
			To(MatchError(ContainSubstring("containerRef and briefRef are immutable; create a new Task")))

		implement := newTask(ns, "keep-implement", "Do it")
		mustCreate(implement)
		Expect(patch(implement, func() { implement.Spec.Kind = c4v1alpha1.TaskPlan })).
			To(MatchError(ContainSubstring("kind is immutable; create a new Task")))
	})

	It("is left alone by the Task controller", func() {
		task := newPlanTask(ns, "untouched")
		mustCreate(task)
		Consistently(func(g Gomega) {
			var got c4v1alpha1.Task
			g.Expect(k8sClient.Get(ctx, key(ns, task.Name), &got)).To(Succeed())
			g.Expect(got.Status).To(Equal(c4v1alpha1.TaskStatus{}))
			g.Expect(got.OwnerReferences).To(BeEmpty())
			g.Expect(got.Labels).To(BeEmpty())
		}, 3*time.Second, 200*time.Millisecond).Should(Succeed())
	})
})
