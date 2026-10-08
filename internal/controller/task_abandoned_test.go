/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
	"github.com/JairJosafath/c4factory/internal/model"
)

// startTry creates a Task at issue (or at a goal when issue is 0) and plays its
// first implement round, so it opens its pull request; checks then run on sha.
func startTry(ns, name string, issue int, sha string) {
	GinkgoHelper()
	task := newTask(ns, name, "")
	if issue == 0 {
		task.Spec.Goal = "Add crop"
	}
	task.Spec.Issue = int32(issue)
	mustCreate(task)
	Eventually(func(g Gomega) { sandboxExists(g, ns, name+"-r1-implement") }).Should(Succeed())
	gitFake.pushBranch(ns, "orders-api", "task/"+name, sha)
	finishSandbox(ns, name+"-r1-implement", name,
		&agentrunner.Result{Role: "implement", Commit: sha, Summary: "Tried."}, "")
	Eventually(func(g Gomega) {
		g.Expect(getTask(g, ns, name).Status.PullRequest).NotTo(BeZero())
	}).Should(Succeed())
}

// failTry plays a try whose only round fails its checks, and returns its Task
// once Failed, its pull request open.
func failTry(ns, name string, issue int, sha string) *c4v1alpha1.Task {
	GinkgoHelper()
	startTry(ns, name, issue, sha)
	gitFake.setChecks(ns, "orders-api", sha, "failure")
	var task *c4v1alpha1.Task
	Eventually(func(g Gomega) {
		task = getTask(g, ns, name)
		g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskFailed))
	}).Should(Succeed())
	return task
}

// pullState is the state of pull request number of orders-api.
func pullState(ns string, number int32) string {
	return gitFake.pullRequests(ns, "orders-api")[number-1].State
}

// #617: a try that ended for good leaves no open pull request behind.
var _ = Describe("Failed Task's pull request", func() {
	It("stays open while a later try may continue it, and closes once its issue is closed", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 1))
		n := gitFake.addIssue(ns, "orders-api", "Validate orders", clearIssue, []string{model.LabelReady})
		task := failTry(ns, "orders-api-1", n, "sha-1")
		pr := task.Status.PullRequest

		Consistently(func() string { return pullState(ns, pr) }, "3s").Should(Equal(stateOpen))
		Expect(getTask(Default, ns, "orders-api-1").Status.PullRequestEnded).To(BeEmpty())

		Expect(gitFake.closeIssue(ns, "orders-api", n)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(pullState(ns, pr)).To(Equal(stateClosed))
			g.Expect(getTask(g, ns, "orders-api-1").Status.PullRequestEnded).To(Equal(
				"closed: issue #" + strconv.Itoa(n) + " closed"))
		}).Should(Succeed())
		Expect(gitFake.pullComments(ns, "orders-api", int(pr))).To(ContainElement(And(
			ContainSubstring("Task `orders-api-1` failed"), ContainSubstring("is closed"),
			ContainSubstring("branch stays"))))
		gitFake.mu.Lock()
		_, kept := gitFake.agentState().branches[ns+"/orders-api@task/orders-api-1"]
		gitFake.mu.Unlock()
		Expect(kept).To(BeTrue(), "the branch stays")
	})

	It("closes when a newer try at its issue opens its own, pointing at it", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 1))
		n := gitFake.addIssue(ns, "orders-api", "Validate orders", clearIssue, []string{model.LabelReady})
		old := failTry(ns, "orders-api-1", n, "sha-1").Status.PullRequest

		startTry(ns, "orders-api-2", n, "sha-2")
		newer := getTask(Default, ns, "orders-api-2").Status
		Expect(newer.PullRequest).NotTo(Equal(old))
		Eventually(func(g Gomega) {
			g.Expect(pullState(ns, old)).To(Equal(stateClosed))
			g.Expect(getTask(g, ns, "orders-api-1").Status.PullRequestEnded).To(Equal("superseded by Task orders-api-2"))
		}).Should(Succeed())
		Expect(gitFake.pullComments(ns, "orders-api", int(old))).To(ContainElement(And(
			ContainSubstring("Task `orders-api-2`"), ContainSubstring(newer.PullRequestURL))))
		Expect(pullState(ns, newer.PullRequest)).To(Equal(stateOpen), "the newer try's pull request is its own")
	})

	It("stays open when a newer try continues it (#607)", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 1))
		n := gitFake.addIssue(ns, "orders-api", "Validate orders", clearIssue, []string{model.LabelReady})
		task := failTry(ns, "orders-api-1", n, "sha-1")
		pr := task.Status.PullRequest

		// The newer try waits for an Agent, so it takes no step of its own here.
		newer := newTask(ns, "orders-api-2", "")
		newer.Spec.Issue, newer.Spec.AgentRef.Name = int32(n), "absent"
		mustCreate(newer)
		Eventually(func(g Gomega) {
			t := getTask(g, ns, "orders-api-2")
			g.Expect(t.Status.Phase).To(Equal(c4v1alpha1.TaskPending))
			t.Status.PullRequest, t.Status.PullRequestURL = pr, task.Status.PullRequestURL
			g.Expect(k8sClient.Status().Update(ctx, t)).To(Succeed())
		}).Should(Succeed())
		Expect(gitFake.closeIssue(ns, "orders-api", n)).To(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1").Status.PullRequestEnded).To(Equal("continued by Task orders-api-2"))
		}).Should(Succeed())
		Expect(pullState(ns, pr)).To(Equal(stateOpen))
		Expect(gitFake.pullComments(ns, "orders-api", int(pr))).NotTo(ContainElement(ContainSubstring("Closing")))
	})

	It("closes at once for a Task without an issue", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 1))
		pr := failTry(ns, "add-crop", 0, "sha-1").Status.PullRequest
		Eventually(func(g Gomega) {
			g.Expect(pullState(ns, pr)).To(Equal(stateClosed))
			g.Expect(getTask(g, ns, "add-crop").Status.PullRequestEnded).To(Equal("closed: no issue"))
		}).Should(Succeed())
	})
})
