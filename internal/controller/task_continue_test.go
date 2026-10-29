/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// #607: a new try on an issue goes on from the earlier try's open pull request,
// and a round that changes only that pull request can merge it.
var _ = Describe("Task on an issue an earlier try left a pull request for", func() {
	It("continues that branch and pull request, and merges a round with no commit once approved", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 1))
		n := gitFake.AddIssue(ns, "orders-api", "Add crop", clearIssue, []string{model.LabelReady})
		issueTask := func(name string) *c4v1beta1.Task {
			task := newTask(ns, name, "")
			task.Spec.Issue = int32(n)
			return task
		}

		By("failing the first try with its pull request open")
		mustCreate(issueTask("orders-api-1"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/orders-api-1", "sha-try1")
		gitFake.SetChecks(ns, "orders-api", "sha-try1", "success")
		finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1",
			&agentrunner.Result{Role: "implement", Commit: "sha-try1", Summary: "Added crop."}, "")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-review-a1") }).Should(Succeed())
		finishSandbox(ns, "orders-api-1-r1-review-a1", "orders-api-1", &agentrunner.Result{Role: "review",
			Verdict: "changes", Summary: "The code is fine; say in the description how to try it."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1").Status.Phase).To(Equal(c4v1beta1.TaskFailed))
		}).Should(Succeed())
		Expect(gitFake.PullRequests(ns, "orders-api")).To(ConsistOf(HaveField("State", "open")))

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
		Expect(gitFake.PullComments(ns, "orders-api", 1)).To(ContainElement(
			ContainSubstring("Task `orders-api-2` continues this pull request")))

		By("reviewing a round that changed only the pull request, and merging it when approved")
		finishSandbox(ns, "orders-api-2-r1-implement", "orders-api-2",
			&agentrunner.Result{Role: "implement", Commit: "sha-try1", Summary: "Said how to try it in the description."}, "")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-2-r1-review-a1") }).Should(Succeed())
		finishSandbox(ns, "orders-api-2-r1-review-a1", "orders-api-2",
			&agentrunner.Result{Role: "review", Verdict: "approve", Summary: "Complete, and the description says how."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-2")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())
		pulls := gitFake.PullRequests(ns, "orders-api")
		Expect(pulls).To(HaveLen(1), "no second pull request")
		Expect(pulls[0].Merged).To(BeTrue())
		Expect(gitFake.Issue(ns, "orders-api", n).State).To(Equal("closed"))
	})

	It("starts its own branch when the earlier try's pull request is closed", func() {
		ns := newProjectNamespace()
		n := failTryWithOpenPull(ns)
		gitFake.ClosePull(ns, "orders-api", 1)

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

	// #656: a pull request gone for good is like a closed one, not an error to
	// retry forever.
	It("starts its own branch when the earlier try's pull request is gone", func() {
		ns := newProjectNamespace()
		n := failTryWithOpenPull(ns)
		gitFake.DeletePull(ns, "orders-api", 1)

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
		Expect(gitFake.PullComments(ns, "orders-api", 1)).NotTo(ContainElement(ContainSubstring("orders-api-2")))
	})

	// #656: a read that fails for now is retried, and the comments a run starts
	// with are posted once, however often the status write after them fails.
	It("retries a failed read of the earlier pull request and comments once when the status write fails", func() {
		ns := newProjectNamespace()
		n := failTryWithOpenPull(ns)
		gitFake.FailPullReads(ns, "orders-api", 1, 2)
		key := client.ObjectKey{Namespace: ns, Name: "orders-api-2"}
		taskStatusFaults.failNext(key, 2, func(t *c4v1beta1.Task) bool { return t.Status.Sandbox != "" })

		second := newTask(ns, "orders-api-2", "")
		second.Spec.Issue = int32(n)
		mustCreate(second)
		Eventually(func(g Gomega) {
			sandboxExists(g, ns, "orders-api-2-r1-implement")
			task := getTask(g, ns, "orders-api-2")
			g.Expect(task.Status.Sandbox).To(Equal("orders-api-2-r1-implement"))
			g.Expect(task.Status.Continues).To(Equal("orders-api-1"))
			g.Expect(task.Status.Branch).To(Equal("task/orders-api-1"))
		}).Should(Succeed())
		Expect(gitFake.PullReadsToFail(ns, "orders-api", 1)).To(BeZero())
		Expect(taskStatusFaults.pending(key)).To(BeZero())
		continues := 0
		for _, c := range gitFake.PullComments(ns, "orders-api", 1) {
			if strings.Contains(c, "Task `orders-api-2` continues this pull request") {
				continues++
			}
		}
		Expect(continues).To(Equal(1))
		picked := 0
		for _, c := range gitFake.Issue(ns, "orders-api", n).Comments {
			if strings.Contains(c, "Task `orders-api-2` picked this up") {
				picked++
			}
		}
		Expect(picked).To(Equal(1))
	})
})

// #759: a try whose CI never reported leaves finished work; once its checks
// pass, the next try reviews it instead of implementing it again.
var _ = Describe("Task on an issue whose earlier try CI never reported on", func() {
	It("reviews the earlier try's commit with no implement run once its checks pass", func() {
		ns := newProjectNamespace()
		n := failTryNoCI(ns)
		Expect(gitFake.Issue(ns, "orders-api", n).Comments).To(ContainElement(
			ContainSubstring("this try doesn't count towards the limit")))
		gitFake.SetChecks(ns, "orders-api", "sha-try1", gitprovider.CheckSuccess)
		taskKey := client.ObjectKey{Namespace: ns, Name: "orders-api-2"}
		taskStatusFaults.failNext(taskKey, 2, func(t *c4v1beta1.Task) bool {
			return t.Status.Phase == c4v1beta1.TaskChecking
		})

		second := newTask(ns, "orders-api-2", "")
		second.Spec.Issue = int32(n)
		mustCreate(second)
		Eventually(func(g Gomega) {
			sandboxExists(g, ns, "orders-api-2-r1-review-a1")
			task := getTask(g, ns, "orders-api-2")
			g.Expect(task.Status.Continues).To(Equal("orders-api-1"))
			g.Expect(task.Status.Commit).To(Equal("sha-try1"))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
		}).Should(Succeed())
		Expect(k8sClient.Get(ctx, key(ns, "orders-api-2-r1-implement"), newSandbox())).NotTo(Succeed(),
			"no implement run")
		Expect(getTask(Default, ns, "orders-api-2").Status.History).To(ContainElement(And(
			HaveField("Phase", c4v1beta1.TaskChecking),
			HaveField("Message", ContainSubstring("which Task orders-api-1 pushed and no one reviewed")))))
		Expect(taskStatusFaults.pending(taskKey)).To(BeZero())
		Expect(countContaining(gitFake.PullComments(ns, "orders-api", 1),
			"Task `orders-api-2` continues this pull request")).To(Equal(1))
		Expect(countContaining(gitFake.Issue(ns, "orders-api", n).Comments,
			"Task `orders-api-2` picked this up")).To(Equal(1))

		By("merging it once the reviewer approves")
		finishSandbox(ns, "orders-api-2-r1-review-a1", "orders-api-2",
			&agentrunner.Result{Role: "review", Verdict: "approve", Summary: "Complete."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-2").Status.Phase).To(Equal(c4v1beta1.TaskMerged))
		}).Should(Succeed())
	})

	It("starts round 1 when the earlier try's checks failed", func() {
		ns := newProjectNamespace()
		n := failTryNoCI(ns)
		gitFake.SetChecks(ns, "orders-api", "sha-try1", gitprovider.CheckFailure)

		second := newTask(ns, "orders-api-2", "")
		second.Spec.Issue = int32(n)
		mustCreate(second)
		Eventually(func(g Gomega) {
			sandboxExists(g, ns, "orders-api-2-r1-implement")
			task := getTask(g, ns, "orders-api-2")
			g.Expect(task.Status.Continues).To(Equal("orders-api-1"))
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
		}).Should(Succeed())
	})
})

// countContaining counts the comments that contain s.
func countContaining(comments []string, s string) int {
	n := 0
	for _, c := range comments {
		if strings.Contains(c, s) {
			n++
		}
	}
	return n
}

// failTryNoCI fails a first Task on a new issue of orders-api with
// NoCIResult: its run at sha-try1 stays queued with no runner online. Its pull
// request, number 1, stays open. It returns the issue.
func failTryNoCI(ns string) int {
	readyContainer(ns)
	mustCreate(newAgent(ns, 1))
	n := gitFake.AddIssue(ns, "orders-api", "Add crop", clearIssue, []string{model.LabelReady})
	first := newTask(ns, "orders-api-1", "")
	first.Spec.Issue = int32(n)
	mustCreate(first)
	Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())
	gitFake.PushBranchHead(ns, "orders-api", "task/orders-api-1", "sha-try1")
	gitFake.SetChecks(ns, "orders-api", "sha-try1", gitprovider.CheckPending)
	gitFake.SetCommitRuns(ns, "orders-api", "sha-try1", gitprovider.WorkflowRun{ID: 7, Workflow: "check.yaml",
		Event: "pull_request", Status: "queued", URL: "http://gitea/" + ns + "/orders-api/actions/runs/7"})
	setRunnersOnline(0)
	finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1",
		&agentrunner.Result{Role: "implement", Commit: "sha-try1", Summary: "Added crop."}, "")
	Eventually(func(g Gomega) {
		g.Expect(getTask(g, ns, "orders-api-1").Status.Phase).To(Equal(c4v1beta1.TaskChecking))
	}).Should(Succeed())
	skewTaskClock(45 * time.Minute)
	Eventually(func(g Gomega) {
		task := getTask(g, ns, "orders-api-1")
		g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
		g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(HaveField("Reason", model.ReasonNoCIResult))
	}).Should(Succeed())
	taskSkew.Store(0)
	ExpectWithOffset(1, gitFake.PullRequests(ns, "orders-api")).To(ConsistOf(HaveField("State", "open")))
	return n
}

// failTryWithOpenPull fails a first Task on a new issue of orders-api on its
// checks, leaving its pull request, number 1, open. It returns the issue.
func failTryWithOpenPull(ns string) int {
	readyContainer(ns)
	mustCreate(newAgent(ns, 1))
	n := gitFake.AddIssue(ns, "orders-api", "Add crop", clearIssue, []string{model.LabelReady})
	first := newTask(ns, "orders-api-1", "")
	first.Spec.Issue = int32(n)
	mustCreate(first)
	Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())
	gitFake.PushBranchHead(ns, "orders-api", "task/orders-api-1", "sha-try1")
	gitFake.SetChecks(ns, "orders-api", "sha-try1", "failure")
	finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1",
		&agentrunner.Result{Role: "implement", Commit: "sha-try1", Summary: "Added crop."}, "")
	Eventually(func(g Gomega) {
		g.Expect(getTask(g, ns, "orders-api-1").Status.Phase).To(Equal(c4v1beta1.TaskFailed))
	}).Should(Succeed())
	ExpectWithOffset(1, gitFake.PullRequests(ns, "orders-api")).To(ConsistOf(HaveField("State", "open")))
	return n
}
