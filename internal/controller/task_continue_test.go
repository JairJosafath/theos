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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
	"github.com/JairJosafath/c4factory/internal/model"
)

// #607: a new try on an issue goes on from the earlier try's open pull request,
// and a round that changes only that pull request can merge it.
var _ = Describe("Task on an issue an earlier try left a pull request for", func() {
	It("continues that branch and pull request, and merges a round with no commit once approved", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 1))
		n := gitFake.addIssue(ns, "orders-api", "Add crop", clearIssue, []string{model.LabelReady})
		issueTask := func(name string) *c4v1alpha1.Task {
			task := newTask(ns, name, "")
			task.Spec.Issue = int32(n)
			return task
		}

		By("failing the first try with its pull request open")
		mustCreate(issueTask("orders-api-1"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())
		gitFake.pushBranch(ns, "orders-api", "task/orders-api-1", "sha-try1")
		gitFake.setChecks(ns, "orders-api", "sha-try1", "success")
		finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1",
			&agentrunner.Result{Role: "implement", Commit: "sha-try1", Summary: "Added crop."}, "")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-review-a1") }).Should(Succeed())
		finishSandbox(ns, "orders-api-1-r1-review-a1", "orders-api-1", &agentrunner.Result{Role: "review",
			Verdict: "changes", Summary: "The code is fine; say in the description how to try it."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1").Status.Phase).To(Equal(c4v1alpha1.TaskFailed))
		}).Should(Succeed())
		Expect(gitFake.pullRequests(ns, "orders-api")).To(ConsistOf(HaveField("State", "open")))

		By("starting the next try on the earlier try's branch, with its feedback")
		mustCreate(issueTask("orders-api-2"))
		var sandbox *unstructured.Unstructured
		Eventually(func(g Gomega) {
			sandbox = sandboxExists(g, ns, "orders-api-2-r1-implement")
			task := getTask(g, ns, "orders-api-2")
			g.Expect(task.Status.Continues).To(Equal("orders-api-1"))
			g.Expect(task.Status.Branch).To(Equal("task/orders-api-1"))
			g.Expect(task.Status.PullRequest).To(Equal(int32(1)))
			g.Expect(task.Status.Feedback).To(ContainSubstring("say in the description how to try it"))
		}).Should(Succeed())
		containers, _, _ := unstructured.NestedSlice(sandbox.Object, "spec", "podTemplate", "spec", "containers")
		Expect(containers[0].(map[string]any)["env"]).To(ContainElements(
			map[string]any{"name": "C4_BRANCH", "value": "task/orders-api-1"},
			map[string]any{"name": "C4_PULL_REQUEST", "value": "1"},
			And(HaveKeyWithValue("name", "C4_FEEDBACK"), HaveKeyWithValue("value", ContainSubstring("how to try it"))),
		))
		Expect(gitFake.pullComments(ns, "orders-api", 1)).To(ContainElement(
			ContainSubstring("Task `orders-api-2` continues this pull request")))

		By("reviewing a round that changed only the pull request, and merging it when approved")
		finishSandbox(ns, "orders-api-2-r1-implement", "orders-api-2",
			&agentrunner.Result{Role: "implement", Commit: "sha-try1", Summary: "Said how to try it in the description."}, "")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-2-r1-review-a1") }).Should(Succeed())
		finishSandbox(ns, "orders-api-2-r1-review-a1", "orders-api-2",
			&agentrunner.Result{Role: "review", Verdict: "approve", Summary: "Complete, and the description says how."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-2")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskMerged))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())
		pulls := gitFake.pullRequests(ns, "orders-api")
		Expect(pulls).To(HaveLen(1), "no second pull request")
		Expect(pulls[0].Merged).To(BeTrue())
		Expect(gitFake.issue(ns, "orders-api", n).State).To(Equal("closed"))
	})

	It("starts its own branch when the earlier try's pull request is closed", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 1))
		n := gitFake.addIssue(ns, "orders-api", "Add crop", clearIssue, []string{model.LabelReady})
		first := newTask(ns, "orders-api-1", "")
		first.Spec.Issue = int32(n)
		mustCreate(first)
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())
		gitFake.pushBranch(ns, "orders-api", "task/orders-api-1", "sha-try1")
		gitFake.setChecks(ns, "orders-api", "sha-try1", "failure")
		finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1",
			&agentrunner.Result{Role: "implement", Commit: "sha-try1", Summary: "Added crop."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1").Status.Phase).To(Equal(c4v1alpha1.TaskFailed))
		}).Should(Succeed())
		gitFake.closePull(ns, "orders-api", 1)

		second := newTask(ns, "orders-api-2", "")
		second.Spec.Issue = int32(n)
		mustCreate(second)
		Eventually(func(g Gomega) {
			sandboxExists(g, ns, "orders-api-2-r1-implement")
			task := getTask(g, ns, "orders-api-2")
			g.Expect(task.Status.Branch).To(Equal("task/orders-api-2"))
			g.Expect(task.Status.Continues).To(BeEmpty())
			g.Expect(task.Status.PullRequest).To(BeZero())
		}).Should(Succeed())
	})
})
