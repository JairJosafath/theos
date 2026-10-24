/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// failedPlan is the linter's plan with answers to failed issues.
func failedPlan(ns, answers string) scaffold.Tree {
	plan := linterPlan(ns, 0)
	plan[model.PlanFailedFile] = scaffold.File{Data: []byte(answers)}
	return plan
}

// failLinterTask waits for Task name on the linter's issue n to fail and hand
// the issue back.
func failLinterTask(ns, name string, n int) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		g.Expect(getTask(g, ns, name).Status.Phase).To(Equal(c4v1beta1.TaskFailed))
		g.Expect(gitFake.issue(ns, "linter", n).Labels).To(ContainElement(model.LabelNeedsHuman))
	}).Should(Succeed())
}

// endedTask records a Task of the linter on issue n as ended in phase at
// completed. Its Agent doesn't exist, so the Task does nothing first.
func endedTask(ns, name string, n int, phase c4v1beta1.TaskPhase, completed time.Time) {
	GinkgoHelper()
	task := newTask(ns, name, "")
	task.Spec.ContainerRef = c4v1beta1.LocalRef{Name: "linter"}
	task.Spec.AgentRef = c4v1beta1.LocalRef{Name: "nobody"}
	task.Spec.Issue = int32(n)
	mustCreate(task)
	Eventually(func(g Gomega) {
		cond := condition(getTask(g, ns, name).Status.Conditions, ConditionProgressing)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Reason).To(Equal("AgentNotFound"))
	}).Should(Succeed())
	Eventually(func(g Gomega) {
		task := getTask(g, ns, name)
		task.Status.Phase, task.Status.CompletedAt = phase, &metav1.Time{Time: completed}
		g.Expect(k8sClient.Status().Update(ctx, task)).To(Succeed())
	}).Should(Succeed())
}

// #454: a Brief lists the issues of its apps handed back to a person, and why.
var _ = Describe("Brief failed issues", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("lists an issue whose Task failed with the Task's message, and not a person's in another app", func() {
		createFactory(ns)
		briefAgents(ns, 1)
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, linterPlan(ns, 0))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())

		By("a person handing back an issue of a Container the Brief didn't make")
		mustCreate(newContainer(ns, "orders-api", "cel-lens", c4v1beta1.ContainerTypeService))
		Eventually(func(g Gomega) {
			var c c4v1beta1.Container
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &c)).To(Succeed())
			g.Expect(c.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
		}).Should(Succeed())
		gitFake.addIssue(ns, "orders-api", "A person's question", "What now?", []string{model.LabelNeedsHuman})

		By("the linter's Crew failing an issue: the linter has no Standard to protect it")
		n := gitFake.addIssue(ns, "linter", "Lint rule 1", linterIssue(1), []string{model.LabelReady})
		var message string
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "linter-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			cond := meta.FindStatusCondition(task.Status.Conditions, ConditionSucceeded)
			g.Expect(cond).NotTo(BeNil())
			message = cond.Message
			g.Expect(gitFake.issue(ns, "linter", n).comments).To(ContainElement(
				ContainSubstring("or say in a prompt on Brief `cel-lens`")))
		}).Should(Succeed())
		reason := "Task `linter-1` failed: " + strings.Join(strings.Fields(message), " ")

		By("listing it with the Task's message, in the status, the condition, an Event and the workspace")
		want := []c4v1beta1.BriefFailedIssue{{Container: "linter", Number: int32(n), Title: "Lint rule 1",
			URL: gitFake.issue(ns, "linter", n).URL, Kind: model.FailedTask, Reason: reason, Tries: 1}}
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.FailedWaiting).To(Equal(int32(1)))
			g.Expect(b.Status.Failed).To(Equal(want))
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
			cond := meta.FindStatusCondition(b.Status.Conditions, c4v1beta1.ConditionIssuesFailed)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Message).To(And(ContainSubstring(fmt.Sprintf("Issue linter#%d \"Lint rule 1\" was handed back: %s", n, reason)),
				HaveSuffix("Say in a prompt whether to try again.")))
			g.Expect(briefEventNotes(g, ns, eventIssueFailed)).To(ConsistOf(ContainSubstring(fmt.Sprintf("Issue linter#%d", n))))
			failed := string(gitFake.mainFiles(ns, briefRepo)[model.WorkspaceFailedFile].Data)
			g.Expect(failed).To(And(ContainSubstring("container: linter"), ContainSubstring("kind: failed"),
				ContainSubstring("Rule 1 is linted.")))
			g.Expect(failed).NotTo(ContainSubstring("orders-api"))
		}).Should(Succeed())

		By("keeping the failed issues last seen while the repository can't be read")
		gitFake.failListIssues(ns, "linter", errors.New("the host is down"))
		DeferCleanup(func() { gitFake.failListIssues(ns, "linter", nil) })
		Eventually(func(g Gomega) {
			g.Expect(string(gitFake.mainFiles(ns, briefRepo)[model.WorkspaceContainersFile].Data)).To(
				ContainSubstring("issuesUnread: true"))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.FailedWaiting).To(Equal(int32(1)))
			g.Expect(b.Status.Failed).To(Equal(want))
		}, "2s").Should(Succeed())
		Expect(briefEventNotes(Default, ns, eventIssueFailed)).To(HaveLen(1), "one Event per failed issue")

		By("clearing them once the issue is no longer handed back")
		gitFake.failListIssues(ns, "linter", nil)
		Expect(gitFake.closeIssue(ns, "linter", n)).To(Succeed())
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.FailedWaiting).To(BeZero())
			g.Expect(b.Status.Failed).To(BeEmpty())
			g.Expect(meta.IsStatusConditionFalse(b.Status.Conditions, c4v1beta1.ConditionIssuesFailed)).To(BeTrue())
		}).Should(Succeed())
	})

	// #474: a by-hand issue needs a person, so a plan may close it but never retry it.
	It("lists a by-hand issue as byHand, refuses a plan that retries it and closes it when one closes it", func() {
		createFactory(ns)
		briefAgents(ns, 2)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)
		planRound(ns, 1, 1, linterPlan(ns, 0))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())

		title, body := model.ByHandIssue("Lint rule 1", 1, "http://gitea/pulls/2", "linter-1", "Agent `reviewer`",
			[]model.OpenCriterion{{Criterion: "The README has a screenshot of the editor", Why: "No desktop editor."}})
		n := gitFake.addIssue(ns, "linter", title, body, []string{model.LabelByHand, model.LabelNeedsHuman})
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Failed).To(ConsistOf(And(HaveField("Number", int32(n)),
				HaveField("Kind", model.FailedByHand), HaveField("Reason", "Needs a person: No desktop editor."))))
			failed := string(gitFake.mainFiles(ns, briefRepo)[model.WorkspaceFailedFile].Data)
			g.Expect(failed).To(ContainSubstring("kind: byHand"))
		}).Should(Succeed())

		By("refusing a plan that retries it")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{"I added the screenshot."} })).To(Succeed())
		planRound(ns, 2, 1, failedPlan(ns,
			fmt.Sprintf("- {container: linter, issue: %d, answer: retry, why: Try it again.}\n", n)))
		Eventually(func(g Gomega) {
			g.Expect(briefEventNotes(g, ns, eventPlanRefused)).To(ContainElement(And(
				ContainSubstring(fmt.Sprintf("retries linter#%d", n)), ContainSubstring("needs a person, not agents"))))
		}).Should(Succeed())
		Expect(gitFake.issue(ns, "linter", n).State).To(Equal("open"))

		By("closing it when a plan closes it")
		planRound(ns, 2, 2, failedPlan(ns,
			fmt.Sprintf("- {container: linter, issue: %d, answer: close, why: The prompt says the screenshot is in.}\n", n)))
		Eventually(func(g Gomega) {
			issue := gitFake.issue(ns, "linter", n)
			g.Expect(issue.State).To(Equal("closed"))
			g.Expect(issue.comments).To(ContainElement(ContainSubstring("Closed for Brief `cel-lens` in round 2.")))
			g.Expect(getBrief(g, ns).Status.Failed).To(BeEmpty())
		}).Should(Succeed())
	})

	// #976: Gitea won't close an issue while one it depends on is open, so a
	// round closes the issues it closes in dependency order, whatever order
	// the plan lists them in, and one that still waits on an issue the round
	// doesn't close is reported once, not retried forever.
	It("closes an issue and the Standard change it waits on in either order, and reports a refused close", func() {
		createFactory(ns)
		briefAgents(ns, 2)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)
		planRound(ns, 1, 1, linterPlan(ns, 0))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())

		change := func(run string) int {
			proposal := model.Proposal{Branch: model.ProposalBranch(run), Paths: []string{"Makefile"},
				Reason: "make dist must build the packages"}
			title, body := model.StandardChangeIssue(1, run, proposal)
			gitFake.pushBranch(ns, "linter", proposal.Branch, "sha-"+run)
			return gitFake.addIssue(ns, "linter", title, body, []string{model.LabelStandardChange, model.LabelNeedsHuman})
		}
		byHand := func(rule int, blocker int) int {
			title, body := model.ByHandIssue(fmt.Sprintf("Lint rule %d", rule), rule, "http://gitea/pulls/2",
				"linter-1", "Agent `reviewer`", []model.OpenCriterion{{Criterion: "A screenshot", Why: "No editor."}})
			return gitFake.addIssue(ns, "linter", title, body, []string{model.LabelByHand, model.LabelNeedsHuman},
				fmt.Sprintf("#%d", blocker))
		}
		firstChange := change("linter-1")
		first := byHand(1, firstChange)
		secondChange := change("linter-2")
		second := byHand(2, secondChange)
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Failed).To(HaveLen(4))
		}).Should(Succeed())

		By("a round closing one issue before its Standard change and another after it")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Drop rules 1 and 2 and their Makefile changes."} })).
			To(Succeed())
		closeAnswer := func(n int) string {
			return fmt.Sprintf("- {container: linter, issue: %d, answer: close, why: The prompt drops it.}\n", n)
		}
		planRound(ns, 2, 1, failedPlan(ns, closeAnswer(first)+closeAnswer(firstChange)+closeAnswer(secondChange)+
			closeAnswer(second)))
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
			g.Expect(b.Status.PromptsPlanned).To(Equal(int32(1)))
			cond := meta.FindStatusCondition(b.Status.Conditions, c4v1beta1.ConditionPlanned)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Message).To(Equal("Applied round 2"))
		}).Should(Succeed())
		for _, n := range []int{first, firstChange, secondChange, second} {
			issue := gitFake.issue(ns, "linter", n)
			Expect(issue.State).To(Equal("closed"), "issue #%d", n)
			Expect(issue.comments).To(ContainElement(ContainSubstring("Closed for Brief `cel-lens` in round 2.")))
		}
		Expect(briefEventNotes(Default, ns, eventPlanned)).To(ContainElement(
			ContainSubstring("retried 0 and closed 4 failed issues")))

		By("a round closing an issue that waits on one the round doesn't close")
		other := gitFake.addIssue(ns, "linter", "Lint rule 3", linterIssue(3), nil)
		blocked := byHand(4, other)
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Failed).To(ConsistOf(HaveField("Number", int32(blocked))))
		}).Should(Succeed())
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() {
			brief.Spec.Prompts = append(brief.Spec.Prompts, "Drop rule 4 too.")
		})).To(Succeed())
		planRound(ns, 3, 1, failedPlan(ns, closeAnswer(blocked)))
		refused := fmt.Sprintf("could not close linter#%d (waits on #%d), which stays handed back", blocked, other)
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
			g.Expect(b.Status.PromptsPlanned).To(Equal(int32(2)))
			cond := meta.FindStatusCondition(b.Status.Conditions, c4v1beta1.ConditionPlanned)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Message).To(And(HavePrefix("Applied round 3; "), ContainSubstring(refused)))
			g.Expect(briefEventNotes(g, ns, eventPlanned)).To(ContainElement(ContainSubstring(refused)))
			g.Expect(b.Status.Failed).To(ConsistOf(HaveField("Number", int32(blocked))), "it stays handed back")
		}).Should(Succeed())
		issue := gitFake.issue(ns, "linter", blocked)
		Expect(issue.State).To(Equal("open"))
		Expect(issue.comments).To(ContainElement(And(
			ContainSubstring("Brief `cel-lens`'s round 3 answered this with close, but it can't be closed"),
			ContainSubstring(fmt.Sprintf("it waits on #%d", other)))))
		Expect(issue.comments).NotTo(ContainElement(ContainSubstring("Closed for Brief")))
		Expect(briefEventNotes(Default, ns, eventApplyFailed)).To(BeEmpty())
	})

	// #889: a retry of a Standard change merges its proposal branch, so one
	// whose branch is gone can't start; a close deletes the branch.
	It("refuses retrying a Standard change whose proposal is gone, and deletes the proposal when one closes it", func() {
		createFactory(ns)
		briefAgents(ns, 2)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)
		planRound(ns, 1, 1, linterPlan(ns, 0))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())

		proposal := model.Proposal{Branch: model.ProposalBranch("linter-1"), Paths: []string{"Makefile"},
			Reason: "make dist must build the packages"}
		title, body := model.StandardChangeIssue(1, "linter-1", proposal)
		n := gitFake.addIssue(ns, "linter", title, body, []string{model.LabelStandardChange, model.LabelNeedsHuman})
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Failed).To(ConsistOf(And(HaveField("Number", int32(n)),
				HaveField("Reason", HavePrefix(model.ProposalGoneNote(proposal.Branch))))))
			failed := string(gitFake.mainFiles(ns, briefRepo)[model.WorkspaceFailedFile].Data)
			g.Expect(failed).To(ContainSubstring("is gone, so a retry would have nothing to merge"))
		}).Should(Succeed())

		By("refusing a plan that retries it")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Try the Makefile change again."} })).To(Succeed())
		planRound(ns, 2, 1, failedPlan(ns,
			fmt.Sprintf("- {container: linter, issue: %d, answer: retry, why: Try it again.}\n", n)))
		Eventually(func(g Gomega) {
			g.Expect(briefEventNotes(g, ns, eventPlanRefused)).To(ContainElement(And(
				ContainSubstring(fmt.Sprintf("retries linter#%d", n)),
				ContainSubstring(fmt.Sprintf("proposal branch `%s` is gone", proposal.Branch)))))
		}).Should(Succeed())
		Expect(gitFake.issue(ns, "linter", n).State).To(Equal("open"))

		By("deleting its proposal when a plan closes it")
		gitFake.pushBranch(ns, "linter", proposal.Branch, "sha-proposal")
		planRound(ns, 2, 2, failedPlan(ns,
			fmt.Sprintf("- {container: linter, issue: %d, answer: close, why: The prompt drops it.}\n", n)))
		Eventually(func(g Gomega) {
			g.Expect(gitFake.issue(ns, "linter", n).State).To(Equal("closed"))
		}).Should(Succeed())
		Expect(gitFake.hasBranch(ns, "linter", proposal.Branch)).To(BeFalse())
	})

	// #994: a blocker opened after the round read the blockers makes the
	// host refuse the close itself; the issue stays as it was.
	It("leaves an issue whose close the host refuses as it was, and closes it once on a later round", func() {
		createFactory(ns)
		briefAgents(ns, 2)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)
		planRound(ns, 1, 1, linterPlan(ns, 0))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())

		proposal := model.Proposal{Branch: model.ProposalBranch("linter-1"), Paths: []string{"Makefile"},
			Reason: "make dist must build the packages"}
		title, body := model.StandardChangeIssue(1, "linter-1", proposal)
		gitFake.pushBranch(ns, "linter", proposal.Branch, "sha-proposal")
		n := gitFake.addIssue(ns, "linter", title, body, []string{model.LabelStandardChange, model.LabelNeedsHuman})
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Failed).To(ConsistOf(HaveField("Number", int32(n))))
		}).Should(Succeed())

		By("a round whose close the host refuses for a blocker opened since it was read")
		other := gitFake.addIssue(ns, "linter", "Lint rule 3", linterIssue(3), nil)
		gitFake.blockOnClose(ns, "linter", n, fmt.Sprintf("#%d", other))
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Drop the Makefile change."} })).To(Succeed())
		closeAnswer := fmt.Sprintf("- {container: linter, issue: %d, answer: close, why: The prompt drops it.}\n", n)
		planRound(ns, 2, 1, failedPlan(ns, closeAnswer))
		// The host refused the close; the round's next pass read the blocker.
		refused := fmt.Sprintf("could not close linter#%d (waits on #%d), which stays handed back", n, other)
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.PromptsPlanned).To(Equal(int32(1)))
			cond := meta.FindStatusCondition(b.Status.Conditions, c4v1beta1.ConditionPlanned)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Message).To(And(HavePrefix("Applied round 2; "), ContainSubstring(refused)))
			g.Expect(b.Status.Failed).To(ConsistOf(HaveField("Number", int32(n))), "it stays handed back")
		}).Should(Succeed())
		issue := gitFake.issue(ns, "linter", n)
		Expect(issue.State).To(Equal("open"))
		Expect(issue.comments).To(ConsistOf(
			ContainSubstring("Brief `cel-lens`'s round 2 answered this with close, but it can't be closed")))
		Expect(gitFake.hasBranch(ns, "linter", proposal.Branch)).To(BeTrue(), "its proposal is kept")

		By("a later round closing it once its blocker is closed")
		Expect(gitFake.closeIssue(ns, "linter", other)).To(Succeed())
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() {
			brief.Spec.Prompts = append(brief.Spec.Prompts, "Drop it again.")
		})).To(Succeed())
		planRound(ns, 3, 1, failedPlan(ns, closeAnswer))
		Eventually(func(g Gomega) {
			g.Expect(gitFake.issue(ns, "linter", n).State).To(Equal("closed"))
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Failed).To(BeEmpty())
		}).Should(Succeed())
		Expect(gitFake.issue(ns, "linter", n).comments).To(HaveLen(2))
		Expect(gitFake.issue(ns, "linter", n).comments).To(ContainElement(
			ContainSubstring("Closed for Brief `cel-lens` in round 3.")))
		Expect(gitFake.issue(ns, "linter", n).comments).NotTo(ContainElement(
			ContainSubstring("Closed for Brief `cel-lens` in round 2.")))
		Expect(gitFake.hasBranch(ns, "linter", proposal.Branch)).To(BeFalse())
	})

	// #624: a release step's last try found its milestone empty; work merged
	// into it since, so it has something to release now.
	It("shows a failed release step's milestone as it is now and refuses closing it after new merges", func() {
		createFactory(ns)
		briefAgents(ns, 2)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)
		planRound(ns, 1, 1, linterPlan(ns, 0))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())

		due := time.Now().AddDate(0, 0, 7)
		inRelease := func(title string, labels ...string) int {
			body := linterIssue(1)
			if slices.Contains(labels, model.LabelRelease) {
				_, body = model.PrepareIssue("v0.3.0", nil, nil)
			}
			n := gitFake.addIssue(ns, "linter", title, body, labels)
			gitFake.setIssueRelease(ns, "linter", n, "v0.3.0", due)
			return n
		}
		stopped := time.Now().Add(-time.Hour).Truncate(time.Second)
		shipped := inRelease("Lint rule 136")
		Expect(gitFake.closeIssue(ns, "linter", shipped)).To(Succeed())
		endedTask(ns, "linter-shipped", shipped, c4v1beta1.TaskMerged, stopped.Add(-time.Hour))
		step := inRelease("Prepare v0.3.0", model.LabelRelease, model.LabelNeedsHuman)
		endedTask(ns, "linter-step", step, c4v1beta1.TaskFailed, stopped)
		fresh := inRelease("Lint rule 147")
		Expect(gitFake.closeIssue(ns, "linter", fresh)).To(Succeed())
		endedTask(ns, "linter-fresh", fresh, c4v1beta1.TaskMerged, stopped.Add(30*time.Minute))

		By("giving the planner the milestone's merges, and which came after the try stopped")
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Failed).To(ContainElement(HaveField("Number", int32(step))))
			failed := string(gitFake.mainFiles(ns, briefRepo)[model.WorkspaceFailedFile].Data)
			g.Expect(failed).To(And(ContainSubstring("milestone: v0.3.0"),
				ContainSubstring(fmt.Sprintf("stopped: \"%s\"", stopped.UTC().Format(time.RFC3339))),
				ContainSubstring(fmt.Sprintf("issue: %d\n          title: Lint rule 136\n", shipped)),
				ContainSubstring(fmt.Sprintf("issue: %d\n          title: Lint rule 147\n", fresh)),
				ContainSubstring("afterStop: true")))
			g.Expect(strings.Count(failed, "afterStop: true")).To(Equal(1))
		}).Should(Succeed())

		By("refusing a plan that closes it as empty")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Tidy up the release."} })).To(Succeed())
		planRound(ns, 2, 1, failedPlan(ns, fmt.Sprintf(
			"- {container: linter, issue: %d, answer: close, why: Its only change shipped in v0.2.0.}\n", step)))
		Eventually(func(g Gomega) {
			g.Expect(briefEventNotes(g, ns, eventPlanRefused)).To(ContainElement(And(
				ContainSubstring(fmt.Sprintf("closes linter#%d, the release step of v0.3.0, but #%d merged", step,
					fresh)), ContainSubstring("retry it"))))
		}).Should(Succeed())
		Expect(gitFake.issue(ns, "linter", step).State).To(Equal("open"))

		By("closing it once a person tagged the release: nothing is left to prepare")
		gitFake.setTag(ns, "linter", "v0.3.0", "c-by-hand")
		planRound(ns, 2, 2, failedPlan(ns, fmt.Sprintf(
			"- {container: linter, issue: %d, answer: close, why: A person released v0.3.0.}\n", step)))
		Eventually(func(g Gomega) {
			g.Expect(gitFake.issue(ns, "linter", step).State).To(Equal("closed"))
		}).Should(Succeed())
	})

	It("retries one failed issue with a clarification and closes another as a prompt says", func() {
		createFactory(ns)
		briefAgents(ns, 2)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)
		planRound(ns, 1, 1, linterPlan(ns, 0))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())

		By("an issue failing three Tasks, and another failing one: the linter has no Standard to protect it")
		tired := gitFake.addIssue(ns, "linter", "Lint rule 1", linterIssue(1), []string{model.LabelReady})
		failLinterTask(ns, model.IssueTaskName("linter", tired, 1), tired)
		for attempt := 2; attempt <= model.MaxIssueAttempts; attempt++ {
			Expect(gitFake.labelIssue(ns, "linter", tired, model.LabelReady)).To(Succeed())
			failLinterTask(ns, model.IssueTaskName("linter", tired, attempt), tired)
		}
		cli := gitFake.addIssue(ns, "linter", "Lint rule 2", linterIssue(2), []string{model.LabelReady})
		failLinterTask(ns, model.IssueTaskName("linter", cli, 1), cli)
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.FailedWaiting).To(Equal(int32(2)))
			g.Expect(b.Status.Failed).To(HaveLen(2))
			g.Expect(b.Status.Failed[0].Kind).To(Equal(model.FailedExhausted))
			g.Expect(b.Status.Failed[1].Kind).To(Equal(model.FailedTask))
		}).Should(Succeed())

		By("refusing a plan that retries the exhausted one, with the reason")
		brief = getBrief(Default, ns)
		prompt := "The engine's checker is merged now: try rule 2 again, and drop rule 1."
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{prompt} })).To(Succeed())
		planRound(ns, 2, 1, failedPlan(ns,
			fmt.Sprintf("- {container: linter, issue: %d, answer: retry, why: Try it all again.}\n", tired)))
		Eventually(func(g Gomega) {
			g.Expect(briefEventNotes(g, ns, eventPlanRefused)).To(ContainElement(And(
				ContainSubstring(model.PlanFailedFile), ContainSubstring(fmt.Sprintf("retries linter#%d", tired)),
				ContainSubstring("agents tried 3 times"))))
		}).Should(Succeed())
		Expect(gitFake.issue(ns, "linter", tired).State).To(Equal("open"))

		By("retrying one with its clarification and closing the other, both with the prompt quoted")
		planRound(ns, 2, 2, failedPlan(ns, fmt.Sprintf(`
- container: linter
  issue: %d
  answer: retry
  why: The prompt says the engine's checker is merged now.
  clarify: |
    The engine's checker is merged; import it.
- container: linter
  issue: %d
  answer: close
  why: The prompt drops rule 1.
`, cli, tired)))
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
			g.Expect(b.Status.PromptsPlanned).To(Equal(int32(1)))
		}).Should(Succeed())
		retried := gitFake.issue(ns, "linter", cli)
		Expect(retried.State).To(Equal("open"))
		Expect(retried.Body).To(And(HavePrefix(strings.TrimRight(linterIssue(2), "\n")),
			HaveSuffix("\n\n## Clarification (Brief round 2)\n\nThe engine's checker is merged; import it.\n")))
		Expect(retried.comments).To(ContainElement(And(ContainSubstring("Tried again for Brief `cel-lens` in round 2"),
			ContainSubstring("The planner: The prompt says the engine's checker is merged now."),
			ContainSubstring("> "+prompt))))
		closed := gitFake.issue(ns, "linter", tired)
		Expect(closed.State).To(Equal("closed"))
		Expect(closed.comments).To(ContainElement(And(ContainSubstring("Closed for Brief `cel-lens` in round 2."),
			ContainSubstring("The planner: The prompt drops rule 1."), ContainSubstring("> "+prompt))))
		Expect(briefEventNotes(Default, ns, eventPlanned)).To(ContainElement(
			ContainSubstring("retried 1 and closed 1 failed issues")))

		By("the Crew starting the retried issue's next Task")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, model.IssueTaskName("linter", cli, 2))
			g.Expect(task.Spec.Issue).To(Equal(int32(cli)))
		}).Should(Succeed())
	})

	// #568: a wait or an ask leaves a failed issue handed back, for a person.
	It("leaves a failed issue handed back when a prompt round answers wait or ask", func() {
		createFactory(ns)
		briefAgents(ns, 1)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)
		planRound(ns, 1, 1, linterPlan(ns, 0))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())

		By("two issues failing a Task each: the linter has no Standard to protect it")
		waited := gitFake.addIssue(ns, "linter", "Lint rule 1", linterIssue(1), []string{model.LabelReady})
		failLinterTask(ns, model.IssueTaskName("linter", waited, 1), waited)
		asked := gitFake.addIssue(ns, "linter", "Lint rule 2", linterIssue(2), []string{model.LabelReady})
		failLinterTask(ns, model.IssueTaskName("linter", asked, 1), asked)
		// Settled: the Brief may list an issue as a person's before it sees
		// its failed Task, so wait for both reasons to name their Task.
		var failed []c4v1beta1.BriefFailedIssue
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.FailedWaiting).To(Equal(int32(2)))
			g.Expect(b.Status.Failed).To(HaveLen(2))
			for _, f := range b.Status.Failed {
				g.Expect(f.Kind).To(Equal(model.FailedTask))
			}
			failed = b.Status.Failed
		}).Should(Succeed())
		comments := map[int]int{waited: len(gitFake.issue(ns, "linter", waited).comments),
			asked: len(gitFake.issue(ns, "linter", asked).comments)}

		By("a prompt round waiting on one and asking about the other, recommending a retry")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() {
			brief.Spec.Prompts = []string{"Hold rule 1 until the engine lands; what about rule 2?"}
		})).To(Succeed())
		planRound(ns, 2, 1, failedPlan(ns, fmt.Sprintf(`
- container: linter
  issue: %d
  answer: wait
  why: The prompt holds rule 1 until the engine lands.
- container: linter
  issue: %d
  answer: ask
  why: The prompt asks what to do about rule 2.
  recommend:
    answer: retry
    why: The checker may be merged by now.
    clarify: |
      Import the engine's checker.
`, waited, asked)))
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
			g.Expect(b.Status.PromptsPlanned).To(Equal(int32(1)))
		}).Should(Succeed())

		By("leaving both open, handed back, untouched and on the Brief's failed list, with no new Task")
		Consistently(func(g Gomega) {
			for _, n := range []int{waited, asked} {
				issue := gitFake.issue(ns, "linter", n)
				g.Expect(issue.State).To(Equal("open"))
				g.Expect(issue.Labels).To(ContainElement(model.LabelNeedsHuman))
				g.Expect(issue.Labels).NotTo(ContainElement(model.LabelReady))
				g.Expect(issue.Body).NotTo(ContainSubstring("Clarification"))
				g.Expect(issue.comments).To(HaveLen(comments[n]))
				var task c4v1beta1.Task
				g.Expect(k8sClient.Get(ctx, key(ns, model.IssueTaskName("linter", n, 2)), &task)).NotTo(Succeed())
			}
			b := getBrief(g, ns)
			g.Expect(b.Status.FailedWaiting).To(Equal(int32(2)))
			g.Expect(b.Status.Failed).To(Equal(failed))
		}, "2s").Should(Succeed())
		Expect(briefEventNotes(Default, ns, eventPlanned)).NotTo(ContainElement(ContainSubstring("failed issues")))
	})
})
