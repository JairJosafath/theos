/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
	"github.com/JairJosafath/c4factory/internal/ports/fake"
)

// checkBranch is the branch whose push runs v0.1.0's full check.
const checkBranch = "release-check/v0.1.0"

// beforeReleaseContainer is readyContainer whose repository runs its full
// check only before a release: it asks for it, its Standard can, and its
// Standard files were rendered so.
func beforeReleaseContainer(ns string) {
	GinkgoHelper()
	readyContainer(ns)
	Eventually(func(g Gomega) {
		standard := &c4v1beta1.Standard{}
		g.Expect(k8sClient.Get(ctx, key(ns, "python-strict"), standard)).To(Succeed())
		standard.Spec.ChecksBeforeRelease = true
		g.Expect(k8sClient.Update(ctx, standard)).To(Succeed())
	}).Should(Succeed())
	Eventually(func(g Gomega) {
		container := &c4v1beta1.Container{}
		g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
		container.Spec.Checks = c4v1beta1.ChecksBeforeRelease
		g.Expect(k8sClient.Update(ctx, container)).To(Succeed())
	}).Should(Succeed())
	Eventually(func(g Gomega) {
		container := &c4v1beta1.Container{}
		g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
		g.Expect(container.Status.Standard).NotTo(BeNil())
		if container.Status.Standard.Checks != c4v1beta1.ChecksBeforeRelease {
			container.Status.Standard.Checks = c4v1beta1.ChecksBeforeRelease
			g.Expect(k8sClient.Status().Update(ctx, container)).To(Succeed())
		}
	}).Should(Succeed())
}

// fullCheckRun is a run of check.yaml the push of v0.1.0's release-check
// branch at 5a-merge started.
func fullCheckRun(ns string, id int64, status, conclusion string) ports.WorkflowRun {
	return ports.WorkflowRun{ID: id, Workflow: "check.yaml", Event: "push", Ref: "refs/heads/" + checkBranch,
		Status: status, Conclusion: conclusion, URL: "http://gitea/" + ns + "/orders-api/actions/runs/7"}
}

// fullCheckIssues lists the issues of orders-api, open or closed, titled as
// v0.1.0's full check failing.
func fullCheckIssues(ns string) []fake.Issue {
	var out []fake.Issue
	for _, i := range gitFake.AllIssues(ns, "orders-api") {
		if i.Title == "Full check fails for v0.1.0" {
			out = append(out, i)
		}
	}
	return out
}

// releasedCondition expects the Crew's Released condition at reason, and
// returns it.
func releasedCondition(g Gomega, ns, reason string) *metav1.Condition {
	cond := condition(getCrew(g, ns).Status.Conditions, ConditionReleased)
	g.Expect(cond).NotTo(BeNil())
	g.Expect(cond.Reason).To(Equal(reason))
	return cond
}

var _ = Describe("Crew release full checks", func() {
	It("runs the full check at the commit before tagging, tags once it passes and deletes the branch", func() {
		ns := newProjectNamespace()
		beforeReleaseContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			g.Expect(gitFake.BranchHead(ns, "orders-api", checkBranch)).To(Equal("5a-merge"))
			cond := releasedCondition(g, ns, model.FullCheckRunning)
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Message).To(Equal("v0.1.0 waits for its full check at 5a-merge on " + checkBranch))
			check := getCrew(g, ns).Status.FullCheck
			g.Expect(check).NotTo(BeNil())
			g.Expect(check.Tag).To(Equal("v0.1.0"))
			g.Expect(check.Commit).To(Equal("5a-merge"))
			g.Expect(check.Run).To(Equal(model.RunWaiting))
			g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Normal"),
				HaveField("Reason", model.FullCheckRunning))))
		}).Should(Succeed())

		// The light check of main failed at that commit: only the branch's runs count.
		mainRun := ports.WorkflowRun{ID: 3, Workflow: "check.yaml", Event: "push", Ref: "refs/heads/main",
			Status: "completed", Conclusion: "failure"}
		gitFake.SetCommitRuns(ns, "orders-api", "5a-merge", fullCheckRun(ns, 7, "in_progress", ""), mainRun)
		runURL := "http://gitea/" + ns + "/orders-api/actions/runs/7"
		Eventually(func(g Gomega) {
			cond := releasedCondition(g, ns, model.FullCheckRunning)
			g.Expect(cond.Message).To(HaveSuffix(": " + runURL))
			g.Expect(getCrew(g, ns).Status.FullCheck.Run).To(Equal(model.RunRunning))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(BeEmpty())
			g.Expect(gitFake.MilestoneClosed(ns, "orders-api", "v0.1.0")).To(BeFalse())
		}, 2*time.Second).Should(Succeed())

		gitFake.SetCommitRuns(ns, "orders-api", "5a-merge", fullCheckRun(ns, 7, "completed", "success"), mainRun)
		Eventually(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(Equal("5a-merge"))
			g.Expect(gitFake.MilestoneClosed(ns, "orders-api", "v0.1.0")).To(BeTrue())
			g.Expect(gitFake.BranchExists(ns, "orders-api", checkBranch)).To(BeFalse())
			crew := getCrew(g, ns)
			g.Expect(crew.Status.FullCheck).To(BeNil())
			g.Expect(crew.Status.LastRelease).NotTo(BeNil())
			g.Expect(crew.Status.LastRelease.Commit).To(Equal("5a-merge"))
			g.Expect(releasedCondition(g, ns, "Released").Message).To(Equal("Released v0.1.0 at 5a-merge"))
		}).Should(Succeed())
		Expect(fullCheckIssues(ns)).To(BeEmpty())
	})

	It("reuses a branch already at the commit, and moves one at another", func() {
		ns := newProjectNamespace()
		beforeReleaseContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		gitFake.PushBranchHead(ns, "orders-api", checkBranch, "5a-older")
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			g.Expect(gitFake.BranchHead(ns, "orders-api", checkBranch)).To(Equal("5a-merge"))
			g.Expect(getCrew(g, ns).Status.FullCheck).NotTo(BeNil())
		}).Should(Succeed())
	})

	It("tags nothing when the full check fails, and files one issue to fix it in the release", func() {
		ns := newProjectNamespace()
		beforeReleaseContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		gitFake.SetCommitRuns(ns, "orders-api", "5a-merge", fullCheckRun(ns, 7, "completed", "failure"))
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		runURL := "http://gitea/" + ns + "/orders-api/actions/runs/7"
		var filed fake.Issue
		Eventually(func(g Gomega) {
			issues := fullCheckIssues(ns)
			g.Expect(issues).To(HaveLen(1))
			filed = issues[0]
			// The Crew may take the issue before this reads it, so check the labels it was filed with.
			g.Expect(filed.FiledLabels).To(ConsistOf(model.LabelBug, model.LabelReady))
			g.Expect(filed.InMilestone).To(Equal("v0.1.0"))
			g.Expect(filed.Body).To(ContainSubstring(runURL))
			cond := releasedCondition(g, ns, model.FullCheckFailed)
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Warning"),
				HaveField("Reason", model.FullCheckFailed))))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(BeEmpty())
			g.Expect(fullCheckIssues(ns)).To(HaveLen(1))
			// The issue reopened the release; the condition keeps saying why.
			releasedCondition(g, ns, model.FullCheckFailed)
			var warnings int
			for _, e := range crewEvents(g, ns) {
				if e.Reason == model.FullCheckFailed {
					warnings++
				}
			}
			g.Expect(warnings).To(Equal(1))
		}, 3*time.Second).Should(Succeed())

		// A person re-ran the check and it passed, and closed the issue unmerged:
		// the release is done again at the same commit, and nothing is filed again.
		gitFake.SetCommitRuns(ns, "orders-api", "5a-merge", fullCheckRun(ns, 8, "completed", "success"),
			fullCheckRun(ns, 7, "completed", "failure"))
		Expect(gitFake.CloseIssueByHand(ns, "orders-api", filed.Number)).To(Succeed())
		// The Crew had started a Task on the issue; it ends with it.
		task := &c4v1beta1.Task{}
		Expect(k8sClient.Get(ctx, key(ns, fmt.Sprintf("orders-api-%d", filed.Number)), task)).To(Succeed())
		Expect(k8sClient.Delete(ctx, task)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(Equal("5a-merge"))
			g.Expect(gitFake.BranchExists(ns, "orders-api", checkBranch)).To(BeFalse())
		}).Should(Succeed())
		Expect(fullCheckIssues(ns)).To(HaveLen(1))
	})

	It("tags the fix's merge without holding it as merged after the prepare issue", func() {
		ns := newProjectNamespace()
		beforeReleaseContainer(ns)
		crewAgents(ns, 1)
		prepare := gitFake.AddIssue(ns, "orders-api", "Prepare v0.1.0", clearIssue, []string{model.LabelRelease})
		gitFake.SetIssueRelease(ns, "orders-api", prepare, "v0.1.0", releaseDue)
		Expect(gitFake.CloseIssueByHand(ns, "orders-api", prepare)).To(Succeed())
		mergedTask(ns, "orders-api-prepare", prepare, "5a-prepare", time.Now().Add(-time.Hour))
		_, body := model.FullCheckIssue("v0.1.0", "5a-prepare", model.ReleaseRun{URL: "http://gitea/runs/7"})
		fix := gitFake.AddIssue(ns, "orders-api", "Full check fails for v0.1.0", body, []string{model.LabelBug})
		gitFake.SetIssueRelease(ns, "orders-api", fix, "v0.1.0", releaseDue)
		Expect(gitFake.CloseIssueByHand(ns, "orders-api", fix)).To(Succeed())
		mergedTask(ns, "orders-api-fix", fix, "5a-fix", time.Now())
		crew := newCrew(ns)
		crew.Spec.TagReleases, crew.Spec.PrepareReleases = true, true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			g.Expect(gitFake.BranchHead(ns, "orders-api", checkBranch)).To(Equal("5a-fix"))
			releasedCondition(g, ns, model.FullCheckRunning)
		}).Should(Succeed())
	})

	It("leaves the release to a person when no full check starts within the hour", func() {
		ns := newProjectNamespace()
		beforeReleaseContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.FullCheck).NotTo(BeNil())
		}).Should(Succeed())
		// No runner picks the branch's push up; it was pushed two hours ago.
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			crew.Status.FullCheck.Time = metav1.NewTime(time.Now().Add(-2 * time.Hour))
			g.Expect(k8sClient.Status().Update(ctx, crew)).To(Succeed())
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			cond := releasedCondition(g, ns, model.NoFullCheck)
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Warning"),
				HaveField("Reason", model.NoFullCheck))))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(BeEmpty())
			g.Expect(fullCheckIssues(ns)).To(BeEmpty())
		}, 2*time.Second).Should(Succeed())
	})

	It("tags an EveryChange Container's release as before, with no full check", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(Equal("5a-merge"))
			g.Expect(getCrew(g, ns).Status.FullCheck).To(BeNil())
		}).Should(Succeed())
		Expect(crewEvents(Default, ns)).NotTo(ContainElement(HaveField("Reason", model.FullCheckRunning)))
		Expect(gitFake.BranchExists(ns, "orders-api", checkBranch)).To(BeFalse())
	})
})
