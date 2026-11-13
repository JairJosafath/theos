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

// A Task's CEL rules: the create-time ones refuse a bad Task, the transition
// rules (oldSelf) refuse a change. The allowedProtectedPaths rules are in
// task_standard_test.go.
var _ = Describe("Task goal and issue", func() {
	It("takes exactly one of them", func() {
		ns := newProjectNamespace()
		both := newTask(ns, "both", "Validate orders.")
		both.Spec.Issue = 2
		Expect(k8sClient.Create(ctx, both)).To(MatchError(ContainSubstring("set exactly one of goal and issue")))
		Expect(k8sClient.Create(ctx, newTask(ns, "neither", ""))).
			To(MatchError(ContainSubstring("set exactly one of goal and issue")))
	})
})

var _ = Describe("Task immutability", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	// created creates a goal Task, or an issue Task when goal is empty.
	created := func(name, goal string, mutate func(*c4v1beta1.Task)) *c4v1beta1.Task {
		GinkgoHelper()
		task := newTask(ns, name, goal)
		if goal == "" {
			task.Spec.Issue = 2
		}
		if mutate != nil {
			mutate(task)
		}
		mustCreate(task)
		return task
	}
	reviewer := func(t *c4v1beta1.Task) { t.Spec.ReviewerRef = &c4v1beta1.LocalRef{Name: "reviewer"} }
	paths := func(t *c4v1beta1.Task) { t.Spec.AllowedProtectedPaths = []string{"Makefile"} }

	It("keeps its Container for life", func() {
		task := created("container", "Do it", nil)
		Expect(patch(task, func() { task.Spec.ContainerRef.Name = "cart-api" })).
			To(MatchError(ContainSubstring("containerRef is immutable")))
	})

	It("keeps its Agent for life", func() {
		task := created("agent", "Do it", nil)
		Expect(patch(task, func() { task.Spec.AgentRef.Name = "codex" })).
			To(MatchError(ContainSubstring("agentRef is immutable")))
	})

	It("keeps its reviewer", func() {
		task := created("reviewer", "Do it", reviewer)
		Expect(patch(task, func() { task.Spec.ReviewerRef.Name = "another" })).
			To(MatchError(ContainSubstring("reviewerRef is immutable")))
	})

	It("neither gains nor drops a reviewer", func() {
		task := created("no-reviewer", "Do it", nil)
		Expect(patch(task, func() { reviewer(task) })).
			To(MatchError(ContainSubstring("reviewerRef is immutable")))
		task = created("drop-reviewer", "Do it", reviewer)
		Expect(patch(task, func() { task.Spec.ReviewerRef = nil })).
			To(MatchError(ContainSubstring("reviewerRef is immutable")))
	})

	It("keeps its goal", func() {
		task := created("goal", "Do it", nil)
		Expect(patch(task, func() { task.Spec.Goal = "Do something else" })).
			To(MatchError(ContainSubstring("goal is immutable; create a new Task")))
	})

	It("keeps its issue", func() {
		task := created("issue", "", nil)
		Expect(patch(task, func() { task.Spec.Issue = 3 })).
			To(MatchError(ContainSubstring("issue is immutable; create a new Task")))
	})

	It("never swaps its goal for an issue, or back", func() {
		task := created("goal-to-issue", "Do it", nil)
		Expect(patch(task, func() { task.Spec.Goal, task.Spec.Issue = "", 2 })).
			To(MatchError(ContainSubstring("goal and issue are immutable; create a new Task")))
		task = created("issue-to-goal", "", nil)
		Expect(patch(task, func() { task.Spec.Goal, task.Spec.Issue = "Do it", 0 })).
			To(MatchError(ContainSubstring("goal and issue are immutable; create a new Task")))
	})

	It("keeps its allowed protected paths", func() {
		task := created("paths", "", paths)
		Expect(patch(task, func() { task.Spec.AllowedProtectedPaths = []string{"Makefile", "ruff.toml"} })).
			To(MatchError(ContainSubstring("allowedProtectedPaths is immutable")))
	})

	It("neither gains nor drops allowed protected paths", func() {
		task := created("no-paths", "", nil)
		Expect(patch(task, func() { paths(task) })).
			To(MatchError(ContainSubstring("allowedProtectedPaths is immutable")))
		task = created("drop-paths", "", paths)
		Expect(patch(task, func() { task.Spec.AllowedProtectedPaths = nil })).
			To(MatchError(ContainSubstring("allowedProtectedPaths is immutable")))
	})
})
