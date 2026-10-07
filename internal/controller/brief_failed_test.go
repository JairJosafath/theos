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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
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
		g.Expect(getTask(g, ns, name).Status.Phase).To(Equal(c4v1alpha1.TaskFailed))
		g.Expect(gitFake.issue(ns, "linter", n).Labels).To(ContainElement(model.LabelNeedsHuman))
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
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}).Should(Succeed())

		By("a person handing back an issue of a Container the Brief didn't make")
		mustCreate(newContainer(ns, "orders-api", "cel-lens", c4v1alpha1.ContainerTypeService))
		Eventually(func(g Gomega) {
			var c c4v1alpha1.Container
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &c)).To(Succeed())
			g.Expect(c.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))
		}).Should(Succeed())
		gitFake.addIssue(ns, "orders-api", "A person's question", "What now?", []string{model.LabelNeedsHuman})

		By("the linter's Crew failing an issue: the linter has no Standard to protect it")
		n := gitFake.addIssue(ns, "linter", "Lint rule 1", linterIssue(1), []string{model.LabelReady})
		var message string
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "linter-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskFailed))
			cond := meta.FindStatusCondition(task.Status.Conditions, ConditionSucceeded)
			g.Expect(cond).NotTo(BeNil())
			message = cond.Message
			g.Expect(gitFake.issue(ns, "linter", n).comments).To(ContainElement(
				ContainSubstring("or say in a prompt on Brief `cel-lens`")))
		}).Should(Succeed())
		reason := "Task `linter-1` failed: " + strings.Join(strings.Fields(message), " ")

		By("listing it with the Task's message, in the status, the condition, an Event and the workspace")
		want := []c4v1alpha1.BriefFailedIssue{{Container: "linter", Number: int32(n), Title: "Lint rule 1",
			URL: gitFake.issue(ns, "linter", n).URL, Kind: model.FailedTask, Reason: reason, Tries: 1}}
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.FailedWaiting).To(Equal(int32(1)))
			g.Expect(b.Status.Failed).To(Equal(want))
			g.Expect(b.Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
			cond := meta.FindStatusCondition(b.Status.Conditions, c4v1alpha1.ConditionIssuesFailed)
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
			g.Expect(meta.IsStatusConditionFalse(b.Status.Conditions, c4v1alpha1.ConditionIssuesFailed)).To(BeTrue())
		}).Should(Succeed())
	})

	It("retries one failed issue with a clarification and closes another as a prompt says", func() {
		createFactory(ns)
		briefAgents(ns, 1)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)
		planRound(ns, 1, 1, linterPlan(ns, 0))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
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
			g.Expect(b.Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
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
})
