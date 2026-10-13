/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// suggestionsPlan keeps the linter's model, answering the reviewers' suggestions as answers says.
func suggestionsPlan(ns, answers string) scaffold.Tree {
	plan := linterPlan(ns, 0)
	plan[model.PlanSuggestionsFile] = scaffold.File{Data: []byte(answers)}
	return plan
}

// #375: a Brief lists its apps' waiting reviewers' suggestions, and a prompt answers them.
var _ = Describe("Brief suggestions", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("lists the reviewers' suggestions, and takes or closes them as a prompt says", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)
		planRound(ns, 1, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}).Should(Succeed())

		By("listing the suggestions a reviewer filed, and not a person's proposed issue")
		safety := gitFake.addIssue(ns, "linter", "Reject control characters", model.FollowUpIssue(model.FollowUp{
			Body: "A rule's text takes them.", Criteria: []string{"A rule with a control character is refused"}},
			"Agent `reviewer`", "http://gitea/pulls/2", 1, "linter-1"), []string{model.LabelProposed})
		gitFake.setIssueMilestone(ns, "linter", safety, model.FollowUpMilestone)
		caching := gitFake.addIssue(ns, "linter", "Think about caching", model.FollowUpIssue(model.FollowUp{
			Body: "Rules are read often."}, "Agent `reviewer`", "http://gitea/pulls/2", 1, "linter-1"),
			[]string{model.LabelProposed})
		gitFake.addIssue(ns, "linter", "A person's idea", "Dark mode.", []string{model.LabelProposed})
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.SuggestionsWaiting).To(Equal(int32(2)))
			g.Expect(b.Status.Suggestions).To(Equal([]c4v1alpha1.BriefSuggestion{
				{Container: "linter", Number: int32(safety), Title: "Reject control characters",
					URL: gitFake.issue(ns, "linter", safety).URL, Criteria: true},
				{Container: "linter", Number: int32(caching), Title: "Think about caching",
					URL: gitFake.issue(ns, "linter", caching).URL},
			}))
			g.Expect(string(gitFake.mainFiles(ns, briefRepo)[model.WorkspaceSuggestionsFile].Data)).To(
				ContainSubstring("title: Reject control characters"))
		}).Should(Succeed())

		By("refusing a plan that takes a suggestion with no criteria")
		brief = getBrief(Default, ns)
		prompt := "Take the reviewer's suggestions about input safety, skip the rest."
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{prompt} })).To(Succeed())
		planRound(ns, 2, 1, suggestionsPlan(ns,
			fmt.Sprintf("- {container: linter, issue: %d, answer: take, why: Take all.}\n", caching)))
		Eventually(func(g Gomega) {
			g.Expect(briefEventNotes(g, ns, eventPlanRefused)).To(ContainElement(
				ContainSubstring("no acceptance criteria")))
		}).Should(Succeed())

		By("taking the one the prompt takes out of Later, ready for the Crew, and closing the rest with the prompt quoted")
		planRound(ns, 2, 2, suggestionsPlan(ns, fmt.Sprintf(`
- container: linter
  issue: %d
  answer: take
  why: The prompt takes the input-safety suggestions.
- container: linter
  issue: %d
  answer: skip
  why: The prompt skips the rest.
`, safety, caching)))
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
			g.Expect(b.Status.PromptsPlanned).To(Equal(int32(1)))
			g.Expect(b.Status.SuggestionsWaiting).To(BeZero())
			g.Expect(b.Status.Suggestions).To(BeEmpty())
		}).Should(Succeed())
		taken := gitFake.issue(ns, "linter", safety)
		Expect(taken.State).To(Equal("open"))
		Expect(taken.Labels).NotTo(ContainElement(model.LabelProposed))
		Expect(taken.milestone).To(BeEmpty())
		Expect(taken.comments).To(ContainElement(And(ContainSubstring("Taken for Brief `cel-lens` in round 2"),
			ContainSubstring("> "+prompt))))
		skipped := gitFake.issue(ns, "linter", caching)
		Expect(skipped.State).To(Equal("closed"))
		Expect(skipped.comments).To(ConsistOf(And(ContainSubstring("not taken"),
			ContainSubstring("The planner: The prompt skips the rest."), ContainSubstring("> "+prompt))))
		Expect(gitFake.issue(ns, "linter", 4).State).To(Equal("open"), "a person's issue is the person's")
		Expect(briefEventNotes(Default, ns, eventPlanned)).To(ContainElement(
			ContainSubstring("took 1 and skipped 1 reviewers' suggestions")))
	})

	// #1008: the close marks a skipped suggestion answered, so one the host
	// refuses, for a dependency added on the host, must not leave a comment
	// that each try of the round would post again.
	It("comments a skipped suggestion once, after the host takes its close", func() {
		createFactory(ns)
		briefAgents(ns, 2)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)
		planRound(ns, 1, 1, linterPlan(ns, 0))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}).Should(Succeed())
		caching := gitFake.addIssue(ns, "linter", "Think about caching", model.FollowUpIssue(model.FollowUp{
			Body: "Rules are read often."}, "Agent `reviewer`", "http://gitea/pulls/2", 1, "linter-1"),
			[]string{model.LabelProposed})
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Suggestions).To(ConsistOf(HaveField("Number", int32(caching))))
		}).Should(Succeed())

		By("a round whose close the host refuses for a dependency added on the host")
		other := gitFake.addIssue(ns, "linter", "Lint rule 3", linterIssue(3), nil)
		gitFake.blockOnClose(ns, "linter", caching, fmt.Sprintf("#%d", other))
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Skip the caching idea."} })).To(Succeed())
		planRound(ns, 2, 1, suggestionsPlan(ns,
			fmt.Sprintf("- {container: linter, issue: %d, answer: skip, why: The prompt skips it.}\n", caching)))
		Eventually(func(g Gomega) {
			cond := meta.FindStatusCondition(getBrief(g, ns).Status.Conditions, c4v1alpha1.ConditionPlanned)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal(model.ReasonApplyFailed))
			g.Expect(cond.Message).To(ContainSubstring(fmt.Sprintf("answering linter#%d", caching)))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			issue := gitFake.issue(ns, "linter", caching)
			g.Expect(issue.State).To(Equal("open"))
			g.Expect(issue.comments).To(BeEmpty())
		}, "500ms").Should(Succeed())

		By("closing and commenting it once the dependency is closed")
		Expect(gitFake.closeIssue(ns, "linter", other)).To(Succeed())
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.PromptsPlanned).To(Equal(int32(1)))
			g.Expect(b.Status.Suggestions).To(BeEmpty())
		}).Should(Succeed())
		skipped := gitFake.issue(ns, "linter", caching)
		Expect(skipped.State).To(Equal("closed"))
		Expect(skipped.comments).To(ConsistOf(ContainSubstring("not taken")))
	})
})
