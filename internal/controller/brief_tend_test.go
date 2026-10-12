/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"fmt"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// briefSkew moves the Brief's clock ahead of the real one, so a test sees a
// heal window or TendInterval pass.
var briefSkew atomic.Int64

// briefNow is the BriefReconciler's clock in the suite.
func briefNow() time.Time { return time.Now().Add(time.Duration(briefSkew.Load())) }

// skewBriefClock moves the Brief's clock d ahead until the spec ends.
func skewBriefClock(d time.Duration) {
	briefSkew.Store(int64(d))
	DeferCleanup(func() { briefSkew.Store(0) })
}

// tendPlan is a tending round's plan: its answers to the failed issues.
func tendPlan(answers string) scaffold.Tree {
	return scaffold.Tree{model.PlanFailedFile: {Data: []byte(answers)}}
}

// autopilotBrief makes Brief cel-lens in mode, with Agents of one try, and
// plans its first round: the linter.
func autopilotBrief(ns, mode string) {
	GinkgoHelper()
	createFactory(ns)
	briefAgents(ns, 1)
	brief := newBrief(ns, "cel-lens")
	brief.Spec.Autopilot = mode
	mustCreate(brief)
	planRound(ns, 1, 1, linterPlan(ns, 0))
	Eventually(func(g Gomega) {
		b := getBrief(g, ns)
		g.Expect(b.Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		// #750: status says the mode in effect, the default when unset.
		g.Expect(b.Status.Autopilot).To(Equal(model.AutopilotMode(mode)))
	}).Should(Succeed())
}

// failedLinterIssue files a linter issue and waits for its first Task to fail
// and the Brief to list it.
func failedLinterIssue(ns string, rule int) int {
	GinkgoHelper()
	n := gitFake.addIssue(ns, "linter", fmt.Sprintf("Lint rule %d", rule), linterIssue(rule), []string{model.LabelReady})
	failLinterTask(ns, model.IssueTaskName("linter", n, 1), n)
	return n
}

// awaitTendRound waits for the Brief to start tending round round with no new
// prompt, its Plan Task labelled so and brief.md saying so.
func awaitTendRound(ns string, round int) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		b := getBrief(g, ns)
		g.Expect(b.Status.Round).To(Equal(int32(round)))
		g.Expect(b.Status.TendRound).To(Equal(int32(round)))
		g.Expect(b.Status.LastTendTime).NotTo(BeNil())
		task := getTask(g, ns, fmt.Sprintf("cel-lens-plan-%d", round))
		g.Expect(task.Labels).To(HaveKeyWithValue(c4v1alpha1.LabelBriefRoundKind, c4v1alpha1.BriefRoundKindTend))
		g.Expect(string(gitFake.mainFiles(ns, briefRepo)[model.BriefFile].Data)).To(ContainSubstring(model.TendingHeading))
		// #871: the planner clones the workspace as it was when its Task was
		// made, and reads it as a tending round's.
		cloned, ok := planBriefs.briefAt(key(ns, task.Name))
		g.Expect(ok).To(BeTrue())
		g.Expect(cloned).To(ContainSubstring(model.TendingHeading))
	}).Should(Succeed())
}

// briefService makes Container orders-api for Brief cel-lens, with a Standard
// so its Tasks run, and waits until it is Ready.
func briefService(ns string) {
	GinkgoHelper()
	standard := newStandard(ns, "python-strict", "python-fastapi")
	container := newContainer(ns, "orders-api", "cel-lens", c4v1alpha1.ContainerTypeService)
	container.Labels = map[string]string{c4v1alpha1.LabelBrief: "cel-lens"}
	container.Spec.StandardRef = &c4v1alpha1.LocalRef{Name: "python-strict"}
	mustCreate(standard, container)
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
		g.Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))
	}).Should(Succeed())
}

// serviceTask makes a Task on orders-api for the Brief's implementer.
func serviceTask(ns, name string) *c4v1alpha1.Task {
	task := &c4v1alpha1.Task{ObjectMeta: objectMeta(ns, name), Spec: c4v1alpha1.TaskSpec{
		ContainerRef: c4v1alpha1.LocalRef{Name: "orders-api"},
		AgentRef:     c4v1alpha1.LocalRef{Name: "implementer"},
		Goal:         "Add crop",
	}}
	mustCreate(task)
	return task
}

// #504: a Brief whose autopilot is Ask or On tends its failed issues.
var _ = Describe("Brief autopilot", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("in On, starts a tending round with no new prompt, applies its retry, and the Crew starts the next Task", func() {
		autopilotBrief(ns, model.AutopilotOn)
		n := failedLinterIssue(ns, 1)

		By("starting a tending round on its own")
		awaitTendRound(ns, 2)
		Expect(getBrief(Default, ns).Spec.Prompts).To(BeEmpty())

		By("applying the planner's retry with its clarification")
		planRound(ns, 2, 1, tendPlan(fmt.Sprintf(`
- container: linter
  issue: %d
  answer: retry
  why: The checker it needs is merged now.
  clarify: |
    Import the checker from the engine.
`, n)))
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.History).To(HaveLen(2))
			last := b.Status.History[1]
			g.Expect(last.Outcome).To(Equal(model.ReasonTended))
			g.Expect(last.Summary).To(Equal(fmt.Sprintf("Tried linter#%d again: The checker it needs is merged now.", n)))
			g.Expect(b.Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
			g.Expect(b.Status.Tended).To(ConsistOf(And(HaveField("Number", int32(n)),
				HaveField("Answer", model.AnswerRetry), HaveField("Round", int32(2)), HaveField("Tries", int32(1)))))
		}).Should(Succeed())
		issue := gitFake.issue(ns, "linter", n)
		Expect(issue.State).To(Equal("open"))
		// #823: no check of the issue's labels here. The Crew starts the next
		// Task once the issue is status/ready, and the linter has no Standard,
		// so that Task can fail and label it status/needs-human before a
		// check sees status/ready. The next Task below shows it was readied.
		Expect(issue.Body).To(HaveSuffix("\n\n## Clarification (Brief round 2)\n\nImport the checker from the engine.\n"))
		Expect(issue.comments).To(ContainElement(And(
			ContainSubstring("Tried again by Brief `cel-lens` in tending round 2, with no prompt"),
			ContainSubstring("The planner: The checker it needs is merged now."))))
		Expect(briefEventNotes(Default, ns, eventTended)).To(ContainElement(
			ContainSubstring("Tending round 2 retried 1, closed 0, waits on 0 and asks you about 0 failed issues")))
		Expect(getBrief(Default, ns).Status.PromptsPlanned).To(BeZero(), "a tending round plans no prompt")

		By("the Crew starting the issue's next Task, which it does only for a status/ready issue")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, model.IssueTaskName("linter", n, 2)).Spec.Issue).To(Equal(int32(n)))
		}).Should(Succeed())
	})

	// #506: a Brief that sets no autopilot is in Ask.
	for mode, set := range map[string]string{"in Ask": model.AutopilotAsk, "with no autopilot set": ""} {
		It(mode+", lists the answer under Needs you and applies nothing until a prompt answers it", func() {
			autopilotBrief(ns, set)
			n := failedLinterIssue(ns, 1)
			awaitTendRound(ns, 2)
			// #630: a why longer than a line, with a comma and a " #", reaches status whole.
			why := "The checker it needs is merged now, #2 on the engine, and the engine released it as v0.3.0 " +
				"an hour after this issue's last try stopped, so the import that failed then resolves now; " +
				"nothing else in its acceptance criteria changed, and no other Task touches the linter's files."
			planRound(ns, 2, 1, tendPlan(fmt.Sprintf(
				"- {container: linter, issue: %d, answer: retry, why: %q}\n", n, why)))

			By("listing it under Needs you with the recommendation, and leaving the issue handed back")
			name := fmt.Sprintf("linter#%d", n)
			Eventually(func(g Gomega) {
				b := getBrief(g, ns)
				g.Expect(b.Status.History).To(HaveLen(2))
				g.Expect(b.Status.History[1].Outcome).To(Equal(model.ReasonTended))
				g.Expect(b.Status.NeedsYou).To(ConsistOf(And(HaveField("Kind", model.AttentionIssue),
					HaveField("Name", name), HaveField("Recommend", "Try it again."),
					HaveField("Why", why))))
				g.Expect(b.Status.NeedsYouWaiting).To(Equal(int32(1)))
				cond := condition(b.Status.Conditions, c4v1alpha1.ConditionNeedsYou)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Message).To(And(ContainSubstring(name), ContainSubstring("Try it again.")))
				g.Expect(briefEventNotes(g, ns, eventNeedsYou)).To(ConsistOf(ContainSubstring(name)))
				g.Expect(briefEventNotes(g, ns, eventTended)).To(ContainElement(ContainSubstring("asks you about 1")))
			}).Should(Succeed())
			Consistently(func(g Gomega) {
				issue := gitFake.issue(ns, "linter", n)
				g.Expect(issue.Labels).To(ContainElement(model.LabelNeedsHuman))
				g.Expect(issue.comments).NotTo(ContainElement(ContainSubstring("Tried again")))
				g.Expect(getBrief(g, ns).Status.Round).To(Equal(int32(2)), "an asked issue starts no other round")
			}, "3s").Should(Succeed())
			Expect(string(gitFake.mainFiles(ns, briefRepo)[model.WorkspaceFailedFile].Data)).To(
				And(ContainSubstring("asked:"), ContainSubstring("recommend:")))

			By("a prompt answering it")
			brief := getBrief(Default, ns)
			Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Do what you suggest."} })).To(Succeed())
			planRound(ns, 3, 1, failedPlan(ns,
				fmt.Sprintf("- {container: linter, issue: %d, answer: retry, why: The prompt says do what you suggest.}\n", n)))
			Eventually(func(g Gomega) {
				b := getBrief(g, ns)
				g.Expect(b.Status.PromptsPlanned).To(Equal(int32(1)))
				g.Expect(b.Status.NeedsYou).To(BeEmpty())
				g.Expect(condition(b.Status.Conditions, c4v1alpha1.ConditionNeedsYou)).To(
					HaveField("Status", metav1.ConditionFalse))
			}).Should(Succeed())
			Expect(gitFake.issue(ns, "linter", n).comments).To(ContainElement(
				ContainSubstring("Tried again for Brief `cel-lens` in round 3")))
		})
	}

	It("leaves a Task waiting for its agent alone inside its window and lists it under Needs you past it", func() {
		autopilotBrief(ns, model.AutopilotOn)
		briefService(ns)
		serviceTask(ns, "add-crop")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		back := time.Now().Add(30 * time.Minute).UTC().Truncate(time.Second)
		finishSandbox(ns, "add-crop-r1-implement", "add-crop", &agentrunner.Result{Role: "implement",
			Error: "claude ended with an error: usage limit reached", Unavailable: true, RetryAt: &back}, "")

		By("listing it as healing until an hour past when its agent is back")
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Attention).To(ContainElement(And(HaveField("Kind", model.AttentionTask),
				HaveField("Name", "add-crop"), HaveField("ActsAt.Time", BeTemporally("~", back.Add(time.Hour), time.Second)))))
			g.Expect(b.Status.NeedsYou).To(BeEmpty())
		}).Should(Succeed())

		By("listing it under Needs you once the window passed")
		skewBriefClock(2 * time.Hour)
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.NeedsYou).To(ContainElement(And(HaveField("Kind", model.AttentionTask),
				HaveField("Name", "add-crop"), HaveField("Question", ContainSubstring("waited")),
				HaveField("Recommend", "Check the Agent's credentials and its plan's usage limit."))))
			g.Expect(b.Status.Attention).NotTo(ContainElement(HaveField("Name", "add-crop")))
			g.Expect(briefEventNotes(g, ns, eventNeedsYou)).To(ContainElement(ContainSubstring("add-crop")))
		}).Should(Succeed())
		Expect(getBrief(Default, ns).Status.Round).To(Equal(int32(1)), "only a failed issue starts a tending round")
	})

	It("starts no second tending round after a wait until a Task merges", func() {
		autopilotBrief(ns, model.AutopilotOn)
		briefService(ns)
		n := failedLinterIssue(ns, 1)
		awaitTendRound(ns, 2)
		planRound(ns, 2, 1, tendPlan(fmt.Sprintf(
			"- {container: linter, issue: %d, answer: wait, why: The engine has no release yet.}\n", n)))
		name := fmt.Sprintf("linter#%d", n)
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.History).To(HaveLen(2))
			g.Expect(b.Status.Attention).To(ContainElement(And(HaveField("Kind", model.AttentionIssue),
				HaveField("Name", name))))
			g.Expect(b.Status.NeedsYou).To(BeEmpty())
		}).Should(Succeed())
		Expect(gitFake.issue(ns, "linter", n).Labels).To(ContainElement(model.LabelNeedsHuman))

		By("starting none past TendInterval while nothing changed")
		skewBriefClock(2 * time.Hour)
		Consistently(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Round).To(Equal(int32(2)))
		}, "3s").Should(Succeed())

		By("starting one once a Task of the Brief's Containers merged")
		merged := serviceTask(ns, "merged-one")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, merged.Name), merged)).To(Succeed())
			merged.Status.Phase = c4v1alpha1.TaskMerged
			merged.Status.PhaseChangedAt = &metav1.Time{Time: time.Now()}
			g.Expect(k8sClient.Status().Update(ctx, merged)).To(Succeed())
		}).Should(Succeed())
		awaitTendRound(ns, 3)
	})

	It("never retries an exhausted issue, and refuses a tending round that changes the story", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, linterPlan(ns, 0))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}).Should(Succeed())

		By("an issue failing three Tasks while autopilot is Off")
		tired := gitFake.addIssue(ns, "linter", "Lint rule 1", linterIssue(1), []string{model.LabelReady})
		failLinterTask(ns, model.IssueTaskName("linter", tired, 1), tired)
		for attempt := 2; attempt <= model.MaxIssueAttempts; attempt++ {
			Expect(gitFake.labelIssue(ns, "linter", tired, model.LabelReady)).To(Succeed())
			failLinterTask(ns, model.IssueTaskName("linter", tired, attempt), tired)
		}
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Failed).To(ConsistOf(HaveField("Kind", model.FailedExhausted)))
			g.Expect(b.Status.NeedsYou).To(BeEmpty(), "Off lists nothing")
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Round).To(Equal(int32(1)), "Off tends nothing")
		}, "2s").Should(Succeed())

		By("turning autopilot On")
		brief := getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Autopilot = model.AutopilotOn })).To(Succeed())
		awaitTendRound(ns, 2)

		By("refusing a retry of the exhausted issue")
		planRound(ns, 2, 1, tendPlan(fmt.Sprintf(
			"- {container: linter, issue: %d, answer: retry, why: Try it all again.}\n", tired)))
		Eventually(func(g Gomega) {
			g.Expect(briefEventNotes(g, ns, eventPlanRefused)).To(ContainElement(And(
				ContainSubstring(fmt.Sprintf("retries linter#%d", tired)), ContainSubstring("agents tried 3 times"))))
		}).Should(Succeed())

		By("refusing a plan that changes the story")
		story := linterPlan(ns, 0)
		story[model.PlanFailedFile] = scaffold.File{Data: []byte(fmt.Sprintf(
			"- {container: linter, issue: %d, answer: close, why: Split it.}\n", tired))}
		commit := planRound(ns, 2, 2, story)
		// The Events of one Brief's refusals are a series: the Task holds each.
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "cel-lens-plan-2")
			g.Expect(task.Annotations).To(HaveKeyWithValue(c4v1alpha1.AnnotationPlanChecked, commit))
			g.Expect(task.Annotations[c4v1alpha1.AnnotationPlanRefusals]).To(And(
				ContainSubstring("a tending round writes only"), ContainSubstring(model.PlanModelFile)))
		}).Should(Succeed())

		By("closing it and filing it again, split")
		planRound(ns, 2, 3, scaffold.Tree{
			model.PlanFailedFile: {Data: []byte(fmt.Sprintf(
				"- {container: linter, issue: %d, answer: close, why: Agents tried it three times; split it in two.}\n",
				tired))},
			model.PlanSummaryFile:      {Data: []byte("Split rule 1 in two.\n")},
			"plan/issues/01-linter.md": {Data: []byte(linterIssue(11))},
			"plan/issues/02-linter.md": {Data: []byte(linterIssue(12))},
		})
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.History).To(HaveLen(2))
			g.Expect(b.Status.History[1]).To(And(HaveField("Outcome", model.ReasonTended),
				HaveField("Summary", "Split rule 1 in two.")))
			g.Expect(b.Status.Issues).To(ContainElement(HaveField("Title", "Lint rule 11")))
		}).Should(Succeed())
		closed := gitFake.issue(ns, "linter", tired)
		Expect(closed.State).To(Equal("closed"))
		Expect(closed.comments).To(ContainElement(ContainSubstring("Closed by Brief `cel-lens` in tending round 2")))
		fourth := model.IssueTaskName("linter", tired, model.MaxIssueAttempts+1)
		Consistently(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, fourth), &c4v1alpha1.Task{})).NotTo(Succeed())
		}, "1s").Should(Succeed(), "the exhausted issue gets no fourth Task")
	})
})
