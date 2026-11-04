/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

var _ = Describe("Task allowedProtectedPaths", func() {
	It("takes eligible paths on an issue Task only, and never changes", func() {
		ns := newProjectNamespace()
		gated := func(name string, paths ...string) *c4v1beta1.Task {
			t := newTask(ns, name, "")
			t.Spec.Issue = 2
			t.Spec.AllowedProtectedPaths = paths
			return t
		}
		Expect(k8sClient.Create(ctx, gated("ok", "Makefile", ".gitea/workflows/release.yaml"))).To(Succeed())
		for _, never := range model.AgentNeverPaths {
			err := k8sClient.Create(ctx, gated("never", never))
			Expect(err).To(MatchError(ContainSubstring("may not include AGENTS.md")), never)
		}
		goal := newTask(ns, "goal", "Do it")
		goal.Spec.AllowedProtectedPaths = []string{"Makefile"}
		Expect(k8sClient.Create(ctx, goal)).To(MatchError(ContainSubstring("needs an issue")))
		task := getTask(Default, ns, "ok")
		Expect(patch(task, func() { task.Spec.AllowedProtectedPaths = []string{"Makefile", ".golangci.yml"} })).
			To(MatchError(ContainSubstring("immutable")))
	})
})

// proposingTask creates an issue Task on orders-api for a new ready issue in the
// milestone v0.1.0, and waits for its first implement sandbox. It returns the issue.
func proposingTask(ns string) int {
	GinkgoHelper()
	readyContainer(ns)
	mustCreate(newAgent(ns, 3))
	n := gitFake.AddIssue(ns, "orders-api", "Package it", clearIssue, []string{model.LabelReady})
	gitFake.PutInMilestone(ns, "orders-api", n, "v0.1.0")
	task := newTask(ns, "orders-api-1", "")
	task.Spec.Issue = int32(n)
	task.Labels = map[string]string{LabelCrew: "orders-api"} // only a Crew's Task may propose
	mustCreate(task)
	Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())
	gitFake.PushBranchHead(ns, "orders-api", "task/orders-api-1", "sha-1")
	return n
}

// makefileProposal is what the runner reports when the agent changed the Makefile.
var makefileProposal = model.Proposal{Branch: "standard/orders-api-1", Paths: []string{"Makefile"},
	Reason: "make dist must build the packages"}

func finishWithProposal(ns string) {
	GinkgoHelper()
	p := makefileProposal
	gitFake.PushBranchHead(ns, "orders-api", p.Branch, "sha-proposal") // as the runner does
	finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1", &agentrunner.Result{Role: "implement",
		Commit: "sha-1", Summary: "Wrote scripts/dist.mjs.", Proposal: &p}, "")
}

// standardChangeIssues are the open issues labelled kind/standard-change.
func standardChangeIssues(ns string) []int {
	var out []int
	for _, i := range gitFake.OpenIssues(ns, "orders-api") {
		if slices.Contains(i.Labels, model.LabelStandardChange) {
			out = append(out, i.Number)
		}
	}
	return out
}

var _ = Describe("Task with a Standard change", func() {
	It("files a Standard change for a proposal and blocks", func() {
		ns := newProjectNamespace()
		n := proposingTask(ns)
		finishWithProposal(ns)

		m := n + 1 // the next issue the fake files
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskBlocked))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.Sandbox).To(BeEmpty())
			g.Expect(task.Status.Commit).To(Equal("sha-1"))
			g.Expect(task.Status.Summary).To(Equal("Wrote scripts/dist.mjs."))
			g.Expect(task.Status.StandardChange).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Issue": Equal(int32(m)), "IssueURL": HaveSuffix(fmt.Sprintf("/issues/%d", m)),
				"Paths": Equal([]string{"Makefile"}), "Branch": Equal("standard/orders-api-1"), "Count": Equal(int32(1))})))
			g.Expect(condition(task.Status.Conditions, ConditionProgressing).Reason).To(Equal("WaitingForStandardChange"))
			g.Expect(task.Status.History).To(ContainElement(MatchFields(IgnoreExtras, Fields{
				"Phase": Equal(c4v1beta1.TaskBlocked), "Reason": Equal("WaitingForStandardChange")})))
		}).Should(Succeed())

		By("filing the issue as the operator, in the waiting issue's milestone")
		filed := gitFake.Issue(ns, "orders-api", m)
		Expect(filed.Title).To(Equal(fmt.Sprintf("Standard change for #%d: Makefile", n)))
		Expect(filed.Labels).To(ConsistOf(model.LabelStandardChange, model.LabelReady))
		Expect(filed.InMilestone).To(Equal("v0.1.0"))
		Expect(filed.Body).To(ContainSubstring("make dist must build the packages"))
		Expect(model.HasProposalMarker(filed.Body, "standard/orders-api-1")).To(BeTrue())
		Expect(standardChangeIssues(ns)).To(Equal([]int{m}))

		By("blocking the waiting issue on it, and saying so there")
		waiting := gitFake.Issue(ns, "orders-api", n)
		Expect(waiting.Blockers).To(ContainElement(fmt.Sprintf("#%d", m)))
		Expect(waiting.Comments).To(ContainElement(And(ContainSubstring("Task `orders-api-1` needs a Standard change"),
			ContainSubstring(fmt.Sprintf("#%d", m)))))
		Expect(waiting.State).To(Equal("open"))
		Expect(gitFake.PullRequests(ns, "orders-api")).To(BeEmpty())

		By("running no agent while it waits")
		Consistently(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskBlocked))
			g.Expect(task.Status.Sandbox).To(BeEmpty())
			g.Expect(condition(task.Status.Conditions, ConditionProgressing).Reason).To(Equal("WaitingForStandardChange"))
		}, "3s").Should(Succeed())
		expectSandboxGone(ns, "orders-api-1-r1-implement")
		expectSandboxGone(ns, "orders-api-1-r2-implement")
	})

	It("reuses the open Standard change it filed when the step repeats", func() {
		ns := newProjectNamespace()
		n := proposingTask(ns)
		// A first try filed the issue, but its status write was lost.
		title, body := model.StandardChangeIssue(n, "orders-api-1", makefileProposal)
		filed := gitFake.AddIssue(ns, "orders-api", title, body, []string{model.LabelStandardChange, model.LabelReady})
		finishWithProposal(ns)

		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskBlocked))
			g.Expect(task.Status.StandardChange).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Issue": Equal(int32(filed)), "Count": Equal(int32(1))})))
		}).Should(Succeed())
		Expect(standardChangeIssues(ns)).To(Equal([]int{filed}))
		Expect(gitFake.Issue(ns, "orders-api", n).Blockers).To(ContainElement(fmt.Sprintf("#%d", filed)))
	})

	It("fails, filing nothing, when its issue was closed during the round", func() {
		ns := newProjectNamespace()
		n := proposingTask(ns)
		Expect(gitFake.CloseIssueByHand(ns, "orders-api", n)).To(Succeed())
		finishWithProposal(ns)

		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(task.Status.Sandbox).To(BeEmpty())
			g.Expect(task.Status.StandardChange).To(BeNil())
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Status": Equal(metav1.ConditionFalse), "Reason": Equal("IssueClosed"),
				"Message": Equal(fmt.Sprintf("Issue #%d was closed before the Task finished", n))})))
		}).Should(Succeed())
		Expect(standardChangeIssues(ns)).To(BeEmpty())
		Expect(gitFake.Issue(ns, "orders-api", n).Blockers).To(BeEmpty())
		expectSandboxGone(ns, "orders-api-1-r1-implement")
	})

	It("fails a Task with a goal that proposes, filing nothing", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "orders-api-1", "Package it"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/orders-api-1", "sha-1")
		finishWithProposal(ns)

		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(task.Status.Sandbox).To(BeEmpty())
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Status": Equal(metav1.ConditionFalse), "Reason": Equal("Failed"),
				"Message": And(ContainSubstring("only an issue's Task can propose"), ContainSubstring("Makefile"))})))
		}).Should(Succeed())
		Expect(standardChangeIssues(ns)).To(BeEmpty())
		expectSandboxGone(ns, "orders-api-1-r1-implement")
	})

	It("fails an issue Task no Crew started that proposes, filing nothing", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3))
		n := gitFake.AddIssue(ns, "orders-api", "Package it", clearIssue, []string{model.LabelReady})
		task := newTask(ns, "orders-api-1", "")
		task.Spec.Issue = int32(n)
		mustCreate(task)
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/orders-api-1", "sha-1")
		finishWithProposal(ns)

		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(task.Status.Sandbox).To(BeEmpty())
			g.Expect(task.Status.StandardChange).To(BeNil())
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Status": Equal(metav1.ConditionFalse), "Reason": Equal("Failed"),
				"Message": And(ContainSubstring("only a Crew's Task can propose"), ContainSubstring("Makefile"))})))
		}).Should(Succeed())
		Expect(standardChangeIssues(ns)).To(BeEmpty())
		Expect(gitFake.Issue(ns, "orders-api", n).Blockers).To(BeEmpty())
	})

	It("fails when its Standard change is closed without a merge", func() {
		// No Crew: it would start the change within its poll.
		ns := newProjectNamespace()
		n := proposingTask(ns)
		finishWithProposal(ns)
		m := n + 1 // the Standard change the Task files
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1").Status.Phase).To(Equal(c4v1beta1.TaskBlocked))
		}).Should(Succeed())

		Expect(gitFake.CloseIssueByHand(ns, "orders-api", m)).To(Succeed())
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Status": Equal(metav1.ConditionFalse), "Reason": Equal("Failed"),
				"Message": And(ContainSubstring(fmt.Sprintf("Standard change #%d will not merge", m)),
					ContainSubstring("closed without a merge"))})))
		}).Should(Succeed())
		issue := gitFake.Issue(ns, "orders-api", n)
		Expect(issue.State).To(Equal("open"))
		Expect(issue.Labels).To(ContainElement(model.LabelNeedsHuman))
		Expect(issue.Comments).To(ContainElement(And(ContainSubstring("Task `orders-api-1` failed"),
			ContainSubstring("closed without a merge"))))
		Expect(gitFake.BranchExists(ns, "orders-api", makefileProposal.Branch)).To(BeFalse())
	})

	It("resumes when its Standard change merges, and ends when that did the whole issue", func() {
		ns := newProjectNamespace()
		n, m := blockedBesideChange(ns, 2)
		mergeChange(ns, 1)
		Expect(gitFake.Issue(ns, "orders-api", m).State).To(Equal("closed"))

		By("resuming the round it was in, told the change merged")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.Feedback).To(ContainSubstring(fmt.Sprintf("#%d", m)))
			g.Expect(task.Status.StandardChange).To(PointTo(HaveField("ResumedRound", int32(1))))
			g.Expect(task.Status.History).To(ContainElement(HaveField("Reason", "Resumed")))
		}).Should(Succeed())
		Expect(gitFake.Issue(ns, "orders-api", n).Comments).
			To(ContainElement(ContainSubstring(fmt.Sprintf("Standard change #%d merged", m))))
		Expect(gitFake.BranchExists(ns, "orders-api", makefileProposal.Branch)).To(BeFalse(), "the proposal is merged")

		By("ending it Merged when the resumed run finds the branch equals main")
		finishResumed(ns, 1)
		var task *c4v1beta1.Task
		Eventually(func(g Gomega) {
			task = getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.Sandbox).To(BeEmpty())
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Status": Equal(metav1.ConditionTrue), "Reason": Equal("DoneByStandardChange"),
				"Message": ContainSubstring(fmt.Sprintf("Standard change #%d did the whole issue", m))})))
		}).Should(Succeed())
		issue := gitFake.Issue(ns, "orders-api", n)
		Expect(issue.State).To(Equal("closed"))
		Expect(issue.Labels).NotTo(ContainElement(model.LabelInProgress))
		Expect(issue.Comments).To(ContainElement(fmt.Sprintf(
			"Done by Standard change #%d (%s): with it merged, this issue needed nothing more.", m,
			task.Status.StandardChange.IssueURL)))
		Expect(gitFake.PullRequests(ns, "orders-api")).To(HaveLen(1), "the resumed Task opened no pull request")
		expectSandboxGone(ns, "orders-api-1-r1-implement-s1")
	})

	// #1008: a close the host refuses, for a dependency opened since, leaves
	// no comment saying the open issue is done. #1042: nor leaves it silent;
	// it goes to a person, saying why.
	It("hands its issue to a person, saying why, when the host refuses the close", func() {
		ns := newProjectNamespace()
		n, m := blockedBesideChange(ns, 2)
		mergeChange(ns, 1)
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1").Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
		}).Should(Succeed())

		other := gitFake.AddIssue(ns, "orders-api", "Sign the package", clearIssue, nil)
		gitFake.BlockOnClose(ns, "orders-api", n, fmt.Sprintf("#%d", other))
		finishResumed(ns, 1)
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1").Status.Phase).To(Equal(c4v1beta1.TaskMerged))
		}).Should(Succeed())
		issue := gitFake.Issue(ns, "orders-api", n)
		Expect(issue.State).To(Equal("open"))
		Expect(issue.Labels).To(ContainElement(model.LabelNeedsHuman))
		Expect(issue.Labels).NotTo(ContainElement(model.LabelInProgress))
		var refusals []string
		for _, c := range issue.Comments {
			if strings.Contains(c, "refused") {
				refusals = append(refusals, c)
			}
		}
		Expect(refusals).To(ConsistOf(And(
			ContainSubstring("Task `orders-api-1` ended"),
			ContainSubstring(fmt.Sprintf("Standard change #%d", m)),
			ContainSubstring("The host refused to close this issue"),
			ContainSubstring(fmt.Sprintf("#%d", other)))))
		Expect(issue.Comments).NotTo(ContainElement(ContainSubstring("Done by Standard change")))
	})

	It("closes its pull request when its Standard change did the whole issue", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 2)
		n := gitFake.AddIssue(ns, "orders-api", "Package it", clearIssue, []string{model.LabelReady})
		crew := newCrew(ns)
		crew.Spec.Parallel = 1
		mustCreate(crew)

		By("opening a pull request in round 1, whose checks fail")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/orders-api-1", "sha-1")
		finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1",
			&agentrunner.Result{Role: "implement", Commit: "sha-1", Summary: "Wrote scripts/dist.mjs."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1").Status.PullRequest).To(Equal(int32(1)))
		}).Should(Succeed())
		gitFake.SetChecks(ns, "orders-api", "sha-1", "failure")

		By("proposing a Standard change in round 2")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r2-implement") }).Should(Succeed())
		p := makefileProposal
		finishSandbox(ns, "orders-api-1-r2-implement", "orders-api-1", &agentrunner.Result{Role: "implement",
			Commit: "sha-1", Summary: "make dist needs the Makefile.", Proposal: &p}, "")
		m := n + 1
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1").Status.Phase).To(Equal(c4v1beta1.TaskBlocked))
			g.Expect(getTask(g, ns, "orders-api-2").Spec.Issue).To(Equal(int32(m)))
			sandboxExists(g, ns, "orders-api-2-r1-implement")
		}).Should(Succeed())
		mergeChange(ns, 2)

		By("closing the round's pull request, unmerged, once the change did the whole issue")
		finishResumed(ns, 2)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded).Reason).To(Equal("DoneByStandardChange"))
		}).Should(Succeed())
		pulls := gitFake.PullRequests(ns, "orders-api")
		Expect(pulls).To(HaveLen(2))
		Expect(pulls[0]).To(And(HaveField("State", "closed"), HaveField("Merged", false)))
		Expect(pulls[1].Merged).To(BeTrue(), "the Standard change")
		Expect(gitFake.Issue(ns, "orders-api", n).State).To(Equal("closed"))
	})

	It("counts only the first run after the resume as resumed, not a conflict-resolution run", func() {
		ns := newProjectNamespace()
		_, m := blockedBesideChange(ns, 2)
		mergeChange(ns, 1)
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1").Status.StandardChange).To(PointTo(HaveField("ResumedRound", int32(1))))
			g.Expect(sandboxEnv(g, ns, "orders-api-1-r1-implement-s1")).To(ContainElement(
				map[string]any{"name": "THEOS_RESUMED", "value": "true"}))
		}).Should(Succeed(), fmt.Sprintf("resumed after Standard change #%d", m))

		By("the resumed run changing the branch, so a pull request is opened and reviewed")
		gitFake.PushBranchHead(ns, "orders-api", "task/orders-api-1", "sha-r1")
		finishSandbox(ns, "orders-api-1-r1-implement-s1", "orders-api-1",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Uses the dist target."}, "")
		gitFake.SetChecks(ns, "orders-api", "sha-r1", "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-review-s1-a1") }).Should(Succeed())

		By("sending the approved, conflicting change back to resolve conflicts")
		pulls := gitFake.PullRequests(ns, "orders-api")
		gitFake.Conflict(ns, "orders-api", pulls[len(pulls)-1].Number, true)
		finishSandbox(ns, "orders-api-1-r1-review-s1-a1", "orders-api-1",
			&agentrunner.Result{Role: "review", Verdict: model.VerdictApprove, Summary: "Good."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.History).To(ContainElement(HaveField("Reason", "ResolvingConflicts")))
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
			g.Expect(task.Status.StandardChange).To(PointTo(HaveField("ResumedRound", int32(0))))
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(sandboxEnv(g, ns, "orders-api-1-r1-implement-s2")).NotTo(ContainElement(
				map[string]any{"name": "THEOS_RESUMED", "value": "true"}))
		}).Should(Succeed())
	})

	It("fails when its Standard change fails", func() {
		ns := newProjectNamespace()
		n, m := blockedBesideChange(ns, 1)
		finishSandbox(ns, "orders-api-2-r1-implement", "orders-api-2",
			&agentrunner.Result{Role: "implement", Error: "make dist fails on a clean checkout"}, "")

		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-2").Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(task.Status.Sandbox).To(BeEmpty())
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Status": Equal(metav1.ConditionFalse), "Reason": Equal("Failed"),
				"Message": And(ContainSubstring(fmt.Sprintf("Standard change #%d will not merge", m)),
					ContainSubstring("Task orders-api-2 failed"), ContainSubstring("make dist fails"))})))
		}).Should(Succeed())
		issue := gitFake.Issue(ns, "orders-api", n)
		Expect(issue.State).To(Equal("open"))
		Expect(issue.Labels).To(ContainElement(model.LabelNeedsHuman))
		Expect(issue.Comments).To(ContainElement(And(ContainSubstring("Task `orders-api-1` failed"),
			ContainSubstring("will not merge"))))
		Expect(gitFake.BranchExists(ns, "orders-api", makefileProposal.Branch)).To(BeTrue(),
			"the Standard change is open, and a retry merges its proposal")

		By("retrying the Standard change, which starts with its proposal still there")
		// #889: what answering a failed issue with a retry does.
		Expect(gitFake.LabelIssue(ns, "orders-api", m, model.LabelReady)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-2-2").Spec.Issue).To(Equal(int32(m)))
			sandboxExists(g, ns, "orders-api-2-2-r1-implement")
		}).Should(Succeed())
		Expect(gitFake.BranchExists(ns, "orders-api", makefileProposal.Branch)).To(BeTrue())

		By("deleting the proposal once the retry merged the Standard change")
		mergeChangeTask(ns, "orders-api-2-2", 1)
		Expect(gitFake.Issue(ns, "orders-api", m).State).To(Equal("closed"))
		Expect(getTask(Default, ns, "orders-api-1").Status.Phase).To(Equal(c4v1beta1.TaskFailed), "nothing resumed it")
		Expect(gitFake.BranchExists(ns, "orders-api", makefileProposal.Branch)).To(BeFalse())
	})

	It("stops both Tasks, blocked, when its Standard change is turned back for scope twice", func() {
		// #578: two rounds refused for the same files would only spend the third.
		ns := newProjectNamespace()
		n, m := blockedBesideChange(ns, 3)
		refusal := model.ScopeRefusal([]string{"ruff.toml"}, []string{"Makefile"})
		finishSandbox(ns, "orders-api-2-r1-implement", "orders-api-2",
			&agentrunner.Result{Role: "implement", Error: refusal}, "")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-2-r2-implement") }).Should(Succeed())
		finishSandbox(ns, "orders-api-2-r2-implement", "orders-api-2",
			&agentrunner.Result{Role: "implement", Error: refusal}, "")

		Eventually(func(g Gomega) {
			change := getTask(g, ns, "orders-api-2")
			g.Expect(change.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(change.Status.Round).To(Equal(int32(2)))
			g.Expect(condition(change.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Reason":  Equal(model.ReasonBlocked),
				"Message": And(ContainSubstring("Two rounds in a row"), ContainSubstring("ruff.toml"))})))
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Reason": Equal(model.ReasonBlocked),
				"Message": And(ContainSubstring(fmt.Sprintf("Standard change #%d will not merge", m)),
					ContainSubstring("ruff.toml"))})))
		}).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "orders-api-2-r3-implement"), newSandbox()))).
			To(BeTrue(), "no third round")
		Expect(gitFake.Issue(ns, "orders-api", n).Labels).To(ContainElement(model.LabelNeedsHuman))
		Expect(gitFake.Issue(ns, "orders-api", m).Labels).To(ContainElement(model.LabelNeedsHuman))
	})

	It("stops both Tasks, blocked, when a round leaves its Standard change's paths as on main", func() {
		// #929: an emptied change has nothing to review or merge.
		ns := newProjectNamespace()
		_, m := blockedBesideChange(ns, 3)
		finishSandbox(ns, "orders-api-2-r1-implement", "orders-api-2",
			&agentrunner.Result{Role: "implement", Empty: true, Summary: "A Standard change may not skip a CI job."}, "")

		Eventually(func(g Gomega) {
			change := getTask(g, ns, "orders-api-2")
			g.Expect(change.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(condition(change.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Reason":  Equal(model.ReasonBlocked),
				"Message": ContainSubstring("leaves Makefile as on main")})))
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Reason": Equal(model.ReasonBlocked),
				"Message": And(ContainSubstring(fmt.Sprintf("Standard change #%d will not merge", m)),
					ContainSubstring("leaves Makefile as on main"))})))
		}).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "orders-api-2-r1-review-a1"), newSandbox()))).
			To(BeTrue(), "no review")
	})
})

// blockedBesideChange runs a Crew whose Task orders-api-1 proposes a Standard
// change in round 1, its Agents allowed rounds rounds, and waits until the Crew
// runs the change as orders-api-2. It returns the issue and the change's.
func blockedBesideChange(ns string, rounds int32) (n, m int) {
	GinkgoHelper()
	readyContainer(ns)
	crewAgents(ns, rounds)
	n = gitFake.AddIssue(ns, "orders-api", "Package it", clearIssue, []string{model.LabelReady})
	crew := newCrew(ns)
	crew.Spec.Parallel = 1
	mustCreate(crew)
	Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())
	gitFake.PushBranchHead(ns, "orders-api", "task/orders-api-1", "sha-1")
	finishWithProposal(ns)
	m = n + 1 // the Standard change the Task files
	Eventually(func(g Gomega) {
		g.Expect(getTask(g, ns, "orders-api-1").Status.Phase).To(Equal(c4v1beta1.TaskBlocked))
		g.Expect(getTask(g, ns, "orders-api-2").Spec.Issue).To(Equal(int32(m)))
		sandboxExists(g, ns, "orders-api-2-r1-implement")
	}).Should(Succeed())
	Expect(gitFake.BranchExists(ns, "orders-api", makefileProposal.Branch)).To(BeTrue(), "the change still reads it")
	return n, m
}

// mergeChange drives the Crew's Standard change Task orders-api-2 through its
// gated merge: its pull request, number pr, changes the Makefile, CI passes and
// the reviewer approves.
func mergeChange(ns string, pr int) {
	GinkgoHelper()
	mergeChangeTask(ns, "orders-api-2", pr)
}

// mergeChangeTask is mergeChange for the Standard change Task named task.
func mergeChangeTask(ns, task string, pr int) {
	GinkgoHelper()
	gitFake.PushBranchHead(ns, "orders-api", "task/"+task, "sha-change")
	finishSandbox(ns, task+"-r1-implement", task,
		&agentrunner.Result{Role: "implement", Commit: "sha-change", Summary: "Added the dist target."}, "")
	Eventually(func(g Gomega) {
		g.Expect(getTask(g, ns, task).Status.PullRequest).To(Equal(int32(pr)))
	}).Should(Succeed())
	gitFake.SetPullFiles(ns, "orders-api", pr, "Makefile")
	gitFake.SetChecks(ns, "orders-api", "sha-change", "success")
	Eventually(func(g Gomega) { sandboxExists(g, ns, task+"-r1-review-a1") }).Should(Succeed())
	finishSandbox(ns, task+"-r1-review-a1", task,
		&agentrunner.Result{Role: "review", Verdict: model.VerdictApprove, Summary: "Only the dist target."}, "")
	Eventually(func(g Gomega) {
		g.Expect(getTask(g, ns, task).Status.Phase).To(Equal(c4v1beta1.TaskMerged))
	}).Should(Succeed())
	Expect(gitFake.ForcedMerges(ns, "orders-api")).To(Equal([]int{pr}))
}

// finishResumed has the resumed implement run of orders-api-1 in round find its
// branch equal to main: the Standard change did the whole issue. The resume
// restarts the round, so the run's sandbox is named apart from the one that
// proposed, whose pod is still there (envtest collects no garbage).
func finishResumed(ns string, round int) {
	GinkgoHelper()
	name := fmt.Sprintf("orders-api-1-r%d-implement-s1", round)
	Eventually(func(g Gomega) {
		g.Expect(sandboxEnv(g, ns, name)).To(ContainElement(map[string]any{"name": "THEOS_RESUMED", "value": "true"}))
	}).Should(Succeed())
	finishSandbox(ns, name, "orders-api-1", &agentrunner.Result{Role: "implement", Empty: true}, "")
}

var _ = Describe("Crew with a Standard change", func() {
	It("runs a Standard change beside the Task it blocks, with its allowance", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 2)
		n := gitFake.AddIssue(ns, "orders-api", "Package it", clearIssue, []string{model.LabelReady})
		crew := newCrew(ns)
		crew.Spec.Parallel = 1
		mustCreate(crew)
		Eventually(func(g Gomega) {
			g.Expect(sandboxEnv(g, ns, "orders-api-1-r1-implement")).
				NotTo(ContainElement(HaveKeyWithValue("name", "THEOS_ALLOWED_PATHS")), "an ordinary issue is ungated")
		}).Should(Succeed())
		Expect(getTask(Default, ns, "orders-api-1").Spec.AllowedProtectedPaths).To(BeEmpty())
		gitFake.PushBranchHead(ns, "orders-api", "task/orders-api-1", "sha-1")
		finishWithProposal(ns)

		m := n + 1 // the Standard change the Task files
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1").Status.Phase).To(Equal(c4v1beta1.TaskBlocked))
			gated := getTask(g, ns, "orders-api-2")
			g.Expect(gated.Spec.Issue).To(Equal(int32(m)))
			g.Expect(gated.Spec.AllowedProtectedPaths).To(Equal([]string{"Makefile"}))
			g.Expect(gated.Labels).To(HaveKeyWithValue(LabelCrew, "orders-api"))
			g.Expect(sandboxEnv(g, ns, "orders-api-2-r1-implement")).
				To(ContainElement(map[string]any{"name": "THEOS_ALLOWED_PATHS", "value": "Makefile"}))
			g.Expect(getCrew(g, ns).Status.ActiveTasks).To(ConsistOf("orders-api-1", "orders-api-2"))
		}).Should(Succeed())
	})

	It("leaves a kind/standard-change issue no Task filed waiting", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		n := gitFake.AddIssue(ns, "orders-api", "Standard change for #9: Makefile", clearIssue,
			[]string{model.LabelReady, model.LabelStandardChange})
		mustCreate(newCrew(ns))
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Status.Waiting).To(ContainElement(int32(n)))
			g.Expect(crew.Status.Ready).To(BeEmpty())
			g.Expect(crew.Status.ActiveTasks).To(BeEmpty())
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			var tasks c4v1beta1.TaskList
			g.Expect(k8sClient.List(ctx, &tasks, client.InNamespace(ns))).To(Succeed())
			g.Expect(tasks.Items).To(BeEmpty())
		}, "3s").Should(Succeed())
	})
})

// gatedTaskInReview creates the Standard change Task orders-api-1, allowed the
// Makefile, for a new issue, and runs its round up to the review of pull request
// 1, which changes files. It returns the issue.
func gatedTaskInReview(ns string, files ...string) int {
	GinkgoHelper()
	readyContainer(ns)
	crewAgents(ns, 2)
	n := gitFake.AddIssue(ns, "orders-api", "Standard change for #9: Makefile", clearIssue,
		[]string{model.LabelStandardChange, model.LabelReady})
	task := newTask(ns, "orders-api-1", "")
	task.Spec.Issue = int32(n)
	task.Spec.ReviewerRef = &c4v1beta1.LocalRef{Name: "reviewer"}
	task.Spec.AllowedProtectedPaths = []string{"Makefile"}
	mustCreate(task)
	Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())
	gitFake.PushBranchHead(ns, "orders-api", "task/orders-api-1", "sha-1")
	finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1",
		&agentrunner.Result{Role: "implement", Commit: "sha-1", Summary: "Added the dist target."}, "")
	Eventually(func(g Gomega) {
		g.Expect(getTask(g, ns, "orders-api-1").Status.PullRequest).To(Equal(int32(1)))
	}).Should(Succeed())
	gitFake.SetPullFiles(ns, "orders-api", 1, files...)
	gitFake.SetChecks(ns, "orders-api", "sha-1", "success")
	Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-review-a1") }).Should(Succeed())
	return n
}

// approveGated has the reviewer approve round 1 of orders-api-1.
func approveGated(ns string) {
	GinkgoHelper()
	finishSandbox(ns, "orders-api-1-r1-review-a1", "orders-api-1",
		&agentrunner.Result{Role: "review", Verdict: model.VerdictApprove, Summary: "Only the dist target."}, "")
}

// expireSandbox plays Agent Sandbox at a sandbox's shutdown time: it marks the
// sandbox expired and deletes its pod, if any, and with it the run's result.
// After an outage of the whole cluster it notices that time late, once the
// cluster is back.
func expireSandbox(ns, name string, outage time.Duration) {
	GinkgoHelper()
	sandbox := newSandbox()
	Expect(k8sClient.Get(ctx, key(ns, name), sandbox)).To(Succeed())
	shutdown, _, _ := unstructured.NestedString(sandbox.Object, "spec", "shutdownTime")
	at, err := time.Parse(time.RFC3339, shutdown)
	Expect(err).NotTo(HaveOccurred())
	Expect(unstructured.SetNestedSlice(sandbox.Object, []any{map[string]any{
		"type": "Ready", "status": "False", "reason": "SandboxExpired", "message": "the sandbox expired",
		"lastTransitionTime": at.Add(outage).Format(time.RFC3339)}}, "status", "conditions")).To(Succeed())
	Expect(k8sClient.Status().Update(ctx, sandbox)).To(Succeed())
	Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: ns, Name: name}}, client.GracePeriodSeconds(0)))).To(Succeed())
}

var _ = Describe("Task with a gated merge", func() {
	It("force-merges a Standard change after checking CI, the approval and every path", func() {
		ns := newProjectNamespace()
		n := gatedTaskInReview(ns, "Makefile")

		By("waiting, neither refusing nor merging, while CI runs again on the reviewed commit")
		gitFake.SetChecks(ns, "orders-api", "sha-1", "pending")
		approveGated(ns)
		Eventually(func(g Gomega) {
			g.Expect(condition(getTask(g, ns, "orders-api-1").Status.Conditions, ConditionProgressing).Reason).
				To(Equal("WaitingForChecks"))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskReviewing))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(BeNil())
			g.Expect(gitFake.PullRequests(ns, "orders-api")[0].Merged).To(BeFalse())
		}, "2s").Should(Succeed())
		Expect(gitFake.ForcedMerges(ns, "orders-api")).To(BeEmpty())
		Expect(gitFake.Reviews(ns, "orders-api", 1)).To(BeEmpty(), "the approval is posted once, with the merge")

		By("merging past the protected-file rule once CI passed")
		gitFake.SetChecks(ns, "orders-api", "sha-1", "success")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Status": Equal(metav1.ConditionTrue), "Reason": Equal("Merged")})))
		}).Should(Succeed())
		Expect(gitFake.PullRequests(ns, "orders-api")[0].Merged).To(BeTrue())
		Expect(gitFake.ForcedMerges(ns, "orders-api")).To(Equal([]int{1}))
		Expect(gitFake.Issue(ns, "orders-api", n).State).To(Equal("closed"))
		Expect(gitFake.Reviews(ns, "orders-api", 1)).To(ConsistOf(And(HaveField("Event", ports.ReviewApprove),
			HaveField("Commit", "sha-1"))))
	})

	It("checks and reviews it again, not fails, when the review expires while CI is pending", func() {
		ns := newProjectNamespace()
		n := gatedTaskInReview(ns, "Makefile")
		gitFake.SetChecks(ns, "orders-api", "sha-1", "pending")
		approveGated(ns)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(condition(task.Status.Conditions, ConditionProgressing).Reason).To(Equal("WaitingForChecks"))
			g.Expect(task.Status.Checks).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Commit": Equal("sha-1"), "State": Equal("pending")})))
		}).Should(Succeed())

		By("expiring the review sandbox, with CI still pending")
		expireSandbox(ns, "orders-api-1-r1-review-a1", 0)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(task.Status.Round).To(Equal(int32(1)), "an approval that ran out costs no round")
			g.Expect(task.Status.Commit).To(Equal("sha-1"))
			g.Expect(task.Status.History).To(ContainElement(HaveField("Reason", "ApprovalEnded")))
			g.Expect(task.Status.ReviewAttempts).To(BeZero(), "an approval that ran out costs no review attempt")
			g.Expect(task.Status.ReviewAgains).To(Equal(int32(1)))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(BeNil())
		}, "2s").Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "orders-api-1-r1-review-a2"), newSandbox()))).
			To(BeTrue(), "no reviewer run is spent on a CI wait")
		Expect(gitFake.PullComments(ns, "orders-api", 1)).To(ContainElement(
			ContainSubstring("ended while CI ran again on the reviewed commit")))

		By("reviewing it again once CI passed, and merging on that approval")
		gitFake.SetChecks(ns, "orders-api", "sha-1", "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-review-a2") }).Should(Succeed())
		Expect(gitFake.ForcedMerges(ns, "orders-api")).To(BeEmpty(), "never merged without a lasting approval")
		finishSandbox(ns, "orders-api-1-r1-review-a2", "orders-api-1",
			&agentrunner.Result{Role: "review", Verdict: model.VerdictApprove, Summary: "Still only the dist target."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1").Status.Phase).To(Equal(c4v1beta1.TaskMerged))
		}).Should(Succeed())
		Expect(gitFake.ForcedMerges(ns, "orders-api")).To(Equal([]int{1}))
		Expect(gitFake.Issue(ns, "orders-api", n).State).To(Equal("closed"))
	})

	It("checks and reviews it again, not fails, when the review expires while CI runs with main merged in", func() {
		// #812: main kept moving while the shared runner was busy.
		ns := newProjectNamespace()
		n := gatedTaskInReview(ns, "Makefile")
		gitFake.FallBehind(ns, "orders-api", 1)
		approveGated(ns)
		var head string
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(task.Status.MergeWhenChecksPass).To(BeTrue())
			g.Expect(task.Status.Commit).NotTo(Equal("sha-1"))
			head = task.Status.Commit
		}).Should(Succeed())

		By("expiring the review sandbox before the checks with main merged in pass")
		expireSandbox(ns, "orders-api-1-r1-review-a1", 0)
		gitFake.SetChecks(ns, "orders-api", head, "success")
		// The reconciler creates the sandbox before it writes the Task's status (#829).
		Eventually(func(g Gomega) {
			sandboxExists(g, ns, "orders-api-1-r1-review-a2")
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskReviewing))
			g.Expect(task.Status.Round).To(Equal(int32(1)), "an approval that ran out costs no round")
			g.Expect(task.Status.Commit).To(Equal(head))
			g.Expect(task.Status.History).To(ContainElement(HaveField("Reason", "ApprovalEnded")))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(BeNil())
		}).Should(Succeed())
		Expect(gitFake.ForcedMerges(ns, "orders-api")).To(BeEmpty(), "never merged without a lasting approval")

		By("merging on the new approval")
		finishSandbox(ns, "orders-api-1-r1-review-a2", "orders-api-1",
			&agentrunner.Result{Role: "review", Verdict: model.VerdictApprove, Summary: "Still only the dist target."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1").Status.Phase).To(Equal(c4v1beta1.TaskMerged))
		}).Should(Succeed())
		Expect(gitFake.ForcedMerges(ns, "orders-api")).To(Equal([]int{1}))
		Expect(gitFake.Issue(ns, "orders-api", n).State).To(Equal("closed"))
	})

	It("refuses a Standard change whose CI no longer reports the check main requires", func() {
		ns := newProjectNamespace()
		n := gatedTaskInReview(ns, "Makefile")
		By("renaming the workflow, so CI passes under a context main's protection doesn't require")
		gitFake.SetCheckContexts(ns, "orders-api", "sha-1", "success",
			map[string]string{"CI / check (pull_request)": "success"})
		approveGated(ns)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Status": Equal(metav1.ConditionFalse), "Reason": Equal("GatedMergeRefused"),
				"Message": ContainSubstring(`no passing check matches "check*"`)})))
		}).Should(Succeed())
		Expect(gitFake.PullRequests(ns, "orders-api")[0].Merged).To(BeFalse())
		Expect(gitFake.ForcedMerges(ns, "orders-api")).To(BeEmpty())
		Expect(gitFake.Issue(ns, "orders-api", n).Labels).To(ContainElement(model.LabelNeedsHuman))
	})

	It("merges a Standard change with the unprotected files its checks need", func() {
		// #578: a test that pins what the Makefile builds changes with it.
		ns := newProjectNamespace()
		n := gatedTaskInReview(ns, "Makefile", "tests/test_release.py")
		approveGated(ns)
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1").Status.Phase).To(Equal(c4v1beta1.TaskMerged))
		}).Should(Succeed())
		Expect(gitFake.ForcedMerges(ns, "orders-api")).To(Equal([]int{1}))
		Expect(gitFake.Issue(ns, "orders-api", n).State).To(Equal("closed"))
	})

	It("names an unprotected file a later round adds on the open pull request", func() {
		ns := newProjectNamespace()
		gatedTaskInReview(ns, "Makefile")
		finishSandbox(ns, "orders-api-1-r1-review-a1", "orders-api-1",
			&agentrunner.Result{Role: "review", Verdict: model.VerdictChanges, Summary: "CI must pass."}, "")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r2-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/orders-api-1", "sha-2")
		finishSandbox(ns, "orders-api-1-r2-implement", "orders-api-1", &agentrunner.Result{Role: "implement",
			Commit: "sha-2", Summary: "Updated the test.", Extra: []string{"tests/test_release.py"}}, "")
		Eventually(func(g Gomega) {
			g.Expect(gitFake.PullComments(ns, "orders-api", 1)).To(ContainElement(And(HavePrefix("Round 2: "),
				ContainSubstring("It also changes `tests/test_release.py`, which no Standard protects"))))
		}).Should(Succeed())
	})

	// #906: a failed status write after the pull request opens doesn't post the
	// round's note twice.
	It("names a later round's unprotected file once when the status write after it fails", func() {
		ns := newProjectNamespace()
		gatedTaskInReview(ns, "Makefile")
		taskKey := client.ObjectKey{Namespace: ns, Name: "orders-api-1"}
		finishSandbox(ns, "orders-api-1-r1-review-a1", "orders-api-1",
			&agentrunner.Result{Role: "review", Verdict: model.VerdictChanges, Summary: "CI must pass."}, "")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r2-implement") }).Should(Succeed())
		taskStatusFaults.failNext(taskKey, 1, func(t *c4v1beta1.Task) bool {
			return t.Status.Phase == c4v1beta1.TaskChecking && t.Status.Round == 2
		})
		gitFake.PushBranchHead(ns, "orders-api", "task/orders-api-1", "sha-2")
		finishSandbox(ns, "orders-api-1-r2-implement", "orders-api-1", &agentrunner.Result{Role: "implement",
			Commit: "sha-2", Summary: "Updated the test.", Extra: []string{"tests/test_release.py"}}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).NotTo(Equal(c4v1beta1.TaskImplementing))
			g.Expect(task.Status.Round).To(Equal(int32(2)))
		}).Should(Succeed())
		Expect(taskStatusFaults.pending(taskKey)).To(BeZero())
		// The note follows the status write (#906), so the status can show
		// before it is posted (#1254): wait for it, then make sure it stays one.
		notes := func() int {
			return countContaining(gitFake.PullComments(ns, "orders-api", 1),
				"It also changes `tests/test_release.py`, which no Standard protects")
		}
		Eventually(notes).Should(Equal(1))
		Consistently(notes, "1s").Should(Equal(1))
		Expect(gitFake.PullRequests(ns, "orders-api")).To(HaveLen(1))
	})

	It("refuses a Standard change that touches a path it may not", func() {
		ns := newProjectNamespace()
		n := gatedTaskInReview(ns, "Makefile", "ruff.toml")
		approveGated(ns)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(task.Status.Sandbox).To(BeEmpty())
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Status": Equal(metav1.ConditionFalse), "Reason": Equal("GatedMergeRefused"),
				"Message": ContainSubstring("ruff.toml")})))
		}).Should(Succeed())
		Expect(gitFake.PullRequests(ns, "orders-api")[0].Merged).To(BeFalse())
		Expect(gitFake.ForcedMerges(ns, "orders-api")).To(BeEmpty())
		Expect(gitFake.Reviews(ns, "orders-api", 1)).To(BeEmpty(), "a refused change gets no approval")
		expectSandboxGone(ns, "orders-api-1-r1-review-a1") // #1293

		By("handing the issue back")
		issue := gitFake.Issue(ns, "orders-api", n)
		Expect(issue.State).To(Equal("open"))
		Expect(issue.Labels).To(ContainElement(model.LabelNeedsHuman))
		Expect(issue.Comments).To(ContainElement(And(ContainSubstring("Task `orders-api-1` failed"),
			ContainSubstring("ruff.toml"))))
	})
})

// gateProvider is a Git host for gatedMerge's checks alone; any other call panics.
type gateProvider struct {
	ports.Provider
	checks   string
	contexts map[string]string // nil means one "check / check (pull_request)" in the checks' state
	files    []string
	filesErr error
	merges   int
}

func (p *gateProvider) CombinedStatus(_ context.Context, _ ports.Repository, ref string) (ports.CheckStatus,
	error) {
	contexts := p.contexts
	if contexts == nil {
		contexts = map[string]string{"check / check (pull_request)": p.checks}
	}
	return ports.CheckStatus{Commit: ref, State: p.checks, Contexts: contexts}, nil
}

func (p *gateProvider) PullRequestFiles(_ context.Context, _ ports.Repository, _ int) ([]string, error) {
	return p.files, p.filesErr
}

func (p *gateProvider) ForceMergePullRequest(_ context.Context, _ ports.Repository, _ int, _, _, _ string) error {
	p.merges++
	return nil
}

func TestGatedMergeMergesNothingItCouldNotCheck(t *testing.T) {
	makefile := []string{"Makefile"}
	approved := &c4v1beta1.TaskReview{Verdict: model.VerdictApprove}
	for _, tt := range []struct {
		name     string
		provider gateProvider
		review   *c4v1beta1.TaskReview
		pending  bool
		refusal  string // in the refusal, if any
		err      string // in the error, if any
	}{
		{name: "waits while CI runs", provider: gateProvider{checks: "pending", files: makefile}, review: approved,
			pending: true},
		{name: "refuses failed CI", provider: gateProvider{checks: "failure", files: makefile}, review: approved,
			refusal: "CI is failure"},
		{name: "refuses CI that never reported", provider: gateProvider{files: makefile}, review: approved,
			refusal: "CI is unknown"},
		{name: "refuses CI without the check main requires", review: approved, provider: gateProvider{checks: "success",
			contexts: map[string]string{"CI / check (pull_request)": "success"}, files: makefile}, refusal: `"check*"`},
		{name: "fails, to retry, on a file list it could not read", review: approved,
			provider: gateProvider{checks: "success", filesErr: errors.New("connection refused")}, err: "connection refused"},
		{name: "refuses a change too large to check every path", review: approved,
			provider: gateProvider{checks: "success", filesErr: fmt.Errorf("pull request 1 changes 1000 files or more: %w",
				ports.ErrTooManyFiles)}, refusal: "1000 files or more"},
		{name: "refuses a protected path outside the allowance", review: approved,
			provider: gateProvider{checks: "success", files: []string{"Makefile", ".gitea/workflows/check.yaml"}},
			refusal:  ".gitea/workflows/check.yaml"},
		{name: "refuses without the reviewer's approval", provider: gateProvider{checks: "success", files: makefile},
			refusal: "verdict"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.provider
			task := &c4v1beta1.Task{Spec: c4v1beta1.TaskSpec{Issue: 2, AllowedProtectedPaths: makefile}}
			status := &c4v1beta1.TaskStatus{Commit: "sha-1", PullRequest: 1, Review: tt.review}
			scheme := runtime.NewScheme()
			if err := c4v1beta1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&c4v1beta1.GitProvider{
				ObjectMeta: objectMeta("apps", "gitea"), Spec: c4v1beta1.GitProviderSpec{Provider: c4v1beta1.GitProviderGitea}}).
				Build()
			container := &c4v1beta1.Container{ObjectMeta: objectMeta("apps", "orders-api"),
				Spec: c4v1beta1.ContainerSpec{GitProviderRef: c4v1beta1.LocalRef{Name: "gitea"}}}
			refusal, err := (&TaskReconciler{Client: c}).gatedMerge(context.Background(), &p, ports.Repository{},
				task, status, container, nil)
			if p.merges != 0 {
				t.Errorf("merged %d times, want none", p.merges)
			}
			if got := status.Checks; got == nil || got.Commit != "sha-1" || got.State != p.checks {
				t.Errorf("status.checks = %+v, want CI's %q on sha-1 recorded", got, p.checks)
			}
			switch {
			case tt.pending && !errors.Is(err, errChecksPending):
				t.Errorf("gatedMerge() error = %v, want errChecksPending", err)
			case tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)):
				t.Errorf("gatedMerge() error = %v, want one with %q", err, tt.err)
			case !tt.pending && tt.err == "" && err != nil:
				t.Errorf("gatedMerge() error = %v", err)
			}
			if !strings.Contains(refusal, tt.refusal) || (tt.refusal == "") != (refusal == "") {
				t.Errorf("gatedMerge() refusal = %q, want one with %q", refusal, tt.refusal)
			}
		})
	}
}

func TestStandardChangeToReuse(t *testing.T) {
	const branch = "standard/orders-api-1"
	marker := model.ProposalMarker(branch)
	issues := []ports.Issue{
		{Number: 2, Body: "Why\n\n" + marker + "\n"},
		{Number: 3, Body: "Quoting " + marker + " inline\n"},
		{Number: 4, Body: "Why\n" + model.ProposalMarker("standard/orders-api-10") + "\n"},
		{Number: 5, Body: "Why\r\n" + marker + "\r\n"},
	}
	for _, tt := range []struct {
		name   string
		issues []ports.Issue
		skip   int
		want   int // 0 means none
	}{
		{name: "the first with the branch's marker on its own line", issues: issues, want: 2},
		{name: "never the change the Task waited on before", issues: issues, skip: 2, want: 5},
		{name: "none: a quoted marker or another branch's", issues: issues[1:3]},
		{name: "none: no issues"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := standardChangeToReuse(tt.issues, branch, tt.skip)
			if tt.want == 0 && ok {
				t.Errorf("reuses #%d, want none", got.Number)
			}
			if tt.want != 0 && (!ok || got.Number != tt.want) {
				t.Errorf("reuses #%d (%v), want #%d", got.Number, ok, tt.want)
			}
		})
	}
}

func TestResumedRun(t *testing.T) {
	resumedIn := func(round int32) *c4v1beta1.TaskStandardChange {
		return &c4v1beta1.TaskStandardChange{Issue: 2, Count: 1, ResumedRound: round}
	}
	for _, tt := range []struct {
		name   string
		status c4v1beta1.TaskStatus
		want   bool
	}{
		{name: "an implement run of the round it resumed in", want: true,
			status: c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskImplementing, Round: 2, StandardChange: resumedIn(2)}},
		{name: "not the next round", status: c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskImplementing, Round: 3,
			StandardChange: resumedIn(2)}},
		{name: "not a review", status: c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskReviewing, Round: 2,
			StandardChange: resumedIn(2)}},
		{name: "not before it resumed", status: c4v1beta1.TaskStatus{
			Phase: c4v1beta1.TaskImplementing, Round: 1, StandardChange: resumedIn(0)}},
		{name: "not without a Standard change", status: c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskImplementing,
			Round: 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := resumedRun(&tt.status); got != tt.want {
				t.Errorf("resumedRun() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPullBodyNamesAStandardChangesExtras(t *testing.T) {
	agents := taskAgents{implementer: &c4v1beta1.Agent{}, reviewer: &c4v1beta1.Agent{}}
	task := &c4v1beta1.Task{Spec: c4v1beta1.TaskSpec{Issue: 53, AllowedProtectedPaths: []string{"Makefile"}}}
	body := pullBody(task, agents, 1, "Renamed the dist outputs.", []string{"release_test.go"})
	want := "This Standard change may change `Makefile`. It also changes `release_test.go`, which no Standard " +
		"protects, so the repository's checks pass with it."
	if !strings.Contains(body, want) {
		t.Errorf("pullBody() = %q, want it to contain %q", body, want)
	}
	if body := pullBody(task, agents, 1, "Done.", nil); strings.Contains(body, "It also changes") {
		t.Errorf("pullBody() = %q names extras it has none of", body)
	}
}
