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

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
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
	gitFake.PushBranchHead(ns, "orders-api", "task/"+name, sha)
	finishSandbox(ns, name+"-r1-implement", name,
		&agentrunner.Result{Role: "implement", Commit: sha, Summary: "Tried."}, "")
	Eventually(func(g Gomega) {
		g.Expect(getTask(g, ns, name).Status.PullRequest).NotTo(BeZero())
	}).Should(Succeed())
}

// failTry plays a try whose only round fails its checks, and returns its Task
// once Failed, its pull request open.
func failTry(ns, name string, issue int) *c4v1beta1.Task {
	GinkgoHelper()
	sha := "sha-" + name
	startTry(ns, name, issue, sha)
	gitFake.SetChecks(ns, "orders-api", sha, "failure")
	var task *c4v1beta1.Task
	Eventually(func(g Gomega) {
		task = getTask(g, ns, name)
		g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
	}).Should(Succeed())
	return task
}

// waitingTry creates a Task at issue that waits for an absent Agent, so it
// takes no step of its own, with pr as its pull request.
func waitingTry(ns, name string, issue int, pr gitprovider.PullRequest) {
	GinkgoHelper()
	task := newTask(ns, name, "")
	task.Spec.Issue, task.Spec.AgentRef.Name = int32(issue), "absent"
	mustCreate(task)
	Eventually(func(g Gomega) {
		t := getTask(g, ns, name)
		g.Expect(t.Status.Phase).To(Equal(c4v1beta1.TaskPending))
		t.Status.PullRequest, t.Status.PullRequestURL = int32(pr.Number), pr.URL
		g.Expect(k8sClient.Status().Update(ctx, t)).To(Succeed())
	}).Should(Succeed())
}

// pullState is the state of pull request number of orders-api.
func pullState(ns string, number int32) string {
	return gitFake.PullRequests(ns, "orders-api")[number-1].State
}

// #617: a try that ended for good leaves no open pull request behind.
var _ = Describe("Failed Task's pull request", func() {
	It("stays open while a later try may continue it, and closes once its issue is closed", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 1))
		n := gitFake.AddIssue(ns, "orders-api", "Validate orders", clearIssue, []string{model.LabelReady})
		task := failTry(ns, "orders-api-1", n)
		pr := task.Status.PullRequest

		Consistently(func() string { return pullState(ns, pr) }, "3s").Should(Equal(stateOpen))
		Expect(getTask(Default, ns, "orders-api-1").Status.PullRequestEnded).To(BeEmpty())

		Expect(gitFake.CloseIssueByHand(ns, "orders-api", n)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(pullState(ns, pr)).To(Equal(stateClosed))
			g.Expect(getTask(g, ns, "orders-api-1").Status.PullRequestEnded).To(Equal(
				"closed: issue #" + strconv.Itoa(n) + " closed"))
		}).Should(Succeed())
		Expect(gitFake.PullComments(ns, "orders-api", int(pr))).To(ContainElement(And(
			ContainSubstring("Task `orders-api-1` failed"), ContainSubstring("is closed"),
			ContainSubstring("branch stays"))))
		Expect(gitFake.BranchExists(ns, "orders-api", "task/orders-api-1")).To(BeTrue(), "the branch stays")
	})

	It("closes when a newer try at its issue has its own, pointing at it", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 1))
		n := gitFake.AddIssue(ns, "orders-api", "Validate orders", clearIssue, []string{model.LabelReady})
		old := failTry(ns, "orders-api-1", n).Status.PullRequest

		// A newer try with a pull request of its own, as tries before #607 had.
		gitFake.PushBranchHead(ns, "orders-api", "task/orders-api-2", "sha-2")
		own, err := gitFake.OpenPullRequest(ctx,
			gitprovider.Repository{Owner: ns, Name: "orders-api"}, "task/orders-api-2", "main", "Validate orders", "")
		Expect(err).NotTo(HaveOccurred())
		waitingTry(ns, "orders-api-2", n, own)

		Eventually(func(g Gomega) {
			g.Expect(pullState(ns, old)).To(Equal(stateClosed))
			g.Expect(getTask(g, ns, "orders-api-1").Status.PullRequestEnded).To(Equal("superseded by Task orders-api-2"))
		}).Should(Succeed())
		Expect(gitFake.PullComments(ns, "orders-api", int(old))).To(ContainElement(And(
			ContainSubstring("Task `orders-api-2`"), ContainSubstring(own.URL))))
		Expect(pullState(ns, int32(own.Number))).To(Equal(stateOpen), "the newer try's pull request is its own")
	})

	It("stays open when a newer try continues it (#607), its issue closed or not", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 1))
		n := gitFake.AddIssue(ns, "orders-api", "Validate orders", clearIssue, []string{model.LabelReady})
		pr := failTry(ns, "orders-api-1", n).Status.PullRequest

		newer := newTask(ns, "orders-api-2", "")
		newer.Spec.Issue = int32(n)
		mustCreate(newer)
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-2").Status.PullRequest).To(Equal(pr))
			g.Expect(getTask(g, ns, "orders-api-1").Status.PullRequestEnded).To(Equal("continued by Task orders-api-2"))
		}).Should(Succeed())
		Expect(gitFake.CloseIssueByHand(ns, "orders-api", n)).To(Succeed())
		Consistently(func() string { return pullState(ns, pr) }, "3s").Should(Equal(stateOpen))
		Expect(gitFake.PullComments(ns, "orders-api", int(pr))).NotTo(ContainElement(ContainSubstring("Closing")))
	})

	It("closes at once for a Task without an issue", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 1))
		pr := failTry(ns, "add-crop", 0).Status.PullRequest
		Eventually(func(g Gomega) {
			g.Expect(pullState(ns, pr)).To(Equal(stateClosed))
			g.Expect(getTask(g, ns, "add-crop").Status.PullRequestEnded).To(Equal("closed: no issue"))
		}).Should(Succeed())
	})
})
