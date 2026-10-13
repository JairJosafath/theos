/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"fmt"
	"slices"
	"strings"

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

// extensionDoc is a second Container of cel-lens, the editor extension, which
// sorts after the linter (the engine), as cel-lens-vscode does after cel-lens.
const extensionDoc = `---
apiVersion: c4.factory.io/v1alpha1
kind: Container
metadata:
  name: linter-vscode
spec:
  softwareSystemRef: {name: cel-lens}
  displayName: Editor extension
  type: service
  gitProviderRef: {name: gitea}
  frameworkRef: {name: python-fastapi}
  computeRef: {name: small}
  deploymentRef: {name: k8s-manifests}
`

// twoContainersPlan is linterPlan with the extension too, answering the
// reviewers' suggestions as answers says.
func twoContainersPlan(ns string, issues int, answers string) scaffold.Tree {
	plan := linterPlan(ns, issues)
	plan[model.PlanModelFile] = scaffold.File{Data: append(plan[model.PlanModelFile].Data, extensionDoc...)}
	if answers != "" {
		plan[model.PlanSuggestionsFile] = scaffold.File{Data: []byte(answers)}
	}
	return plan
}

// fileSuggestions fills Container name's repository up to the highest of
// numbers: a reviewer's suggestion at each of numbers, with acceptance
// criteria, and a closed issue at every other number not yet filed.
func fileSuggestions(ns, name string, numbers []int) {
	GinkgoHelper()
	for n := gitFake.issueCount(ns, name) + 1; n <= slices.Max(numbers); n++ {
		if !slices.Contains(numbers, n) {
			Expect(gitFake.closeIssue(ns, name, gitFake.addIssue(ns, name, fmt.Sprintf("Done %d", n), "", nil))).
				To(Succeed())
			continue
		}
		Expect(gitFake.addIssue(ns, name, fmt.Sprintf("Suggestion %d", n), model.FollowUpIssue(model.FollowUp{
			Body: "A reviewer's idea.", Criteria: []string{fmt.Sprintf("Suggestion %d is done", n)}},
			"Agent `reviewer`", "http://gitea/pulls/2", 1, "linter-1"), []string{model.LabelProposed})).To(Equal(n))
	}
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

// #1019: the cel-lens Brief on 0.22.0 had 62 suggestions waiting across the
// engine and the extension; a prompt named the extension's six newest, which
// the status's 50 didn't list, and the operator refused the plan.
var _ = Describe("Brief suggestions past the status's list", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("lets a prompt answer any waiting suggestion, telling two Containers' numbers apart", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, twoContainersPlan(ns, 1, ""))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}).WithTimeout(wholeRound).Should(Succeed())

		By("filing 31 suggestions in each, #364 and #371 in both")
		var engine, extension []int
		for n := 341; n <= 371; n++ {
			engine = append(engine, n)
		}
		for n := 300; n <= 324; n++ {
			extension = append(extension, n)
		}
		newest := []int{364, 365, 369, 371, 376, 385}
		extension = append(extension, newest...)
		fileSuggestions(ns, "linter", engine)
		fileSuggestions(ns, "linter-vscode", extension)

		By("listing 50 in the status and all 62 in the workspace, each with its repository and address")
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.SuggestionsWaiting).To(Equal(int32(62)))
			g.Expect(b.Status.Suggestions).To(HaveLen(model.MaxSuggestions))
			g.Expect(b.Status.Suggestions).NotTo(ContainElement(And(HaveField("Container", "linter-vscode"),
				HaveField("Number", int32(385)))))
			listed := string(gitFake.mainFiles(ns, briefRepo)[model.WorkspaceSuggestionsFile].Data)
			g.Expect(strings.Count(listed, "- container: ")).To(Equal(62))
			g.Expect(listed).To(ContainSubstring("- container: linter-vscode\n  repository: linter-vscode\n" +
				"  issue: 385\n  title: Suggestion 385\n  url: " + gitFake.issue(ns, "linter-vscode", 385).URL))
			g.Expect(listed).To(ContainSubstring("- container: linter\n  repository: linter\n  issue: 364\n"))
		}).Should(Succeed())

		By("refusing an answer to a number that waits in the other Container, naming where it waits")
		brief := getBrief(Default, ns)
		prompt := "Take the extension's six newest suggestions: #385 #376 #371 #369 #365 #364 in linter-vscode."
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{prompt} })).To(Succeed())
		answers := func(container string, numbers ...int) string {
			var b strings.Builder
			for _, n := range numbers {
				fmt.Fprintf(&b, "- {container: %s, issue: %d, answer: take, why: The prompt takes it.}\n", container, n)
			}
			return b.String()
		}
		planRound(ns, 2, 1, twoContainersPlan(ns, 0, answers("linter-vscode", 370)))
		Eventually(func(g Gomega) {
			g.Expect(briefEventNotes(g, ns, eventPlanRefused)).To(ContainElement(
				ContainSubstring("#370 waits in Container linter")))
		}).Should(Succeed())

		By("taking the six the status doesn't list, and leaving the engine's #364 and #371 as they are")
		planRound(ns, 2, 2, twoContainersPlan(ns, 0, answers("linter-vscode", newest...)))
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
			g.Expect(b.Status.PromptsPlanned).To(Equal(int32(1)))
			g.Expect(b.Status.SuggestionsWaiting).To(Equal(int32(56)))
		}).WithTimeout(wholeRound).Should(Succeed())
		for _, n := range newest {
			taken := gitFake.issue(ns, "linter-vscode", n)
			Expect(taken.Labels).To(ConsistOf(model.LabelReady), "linter-vscode#%d", n)
			Expect(taken.comments).To(ContainElement(ContainSubstring("Taken for Brief `cel-lens` in round 2")))
		}
		for _, n := range []int{364, 371} {
			untouched := gitFake.issue(ns, "linter", n)
			Expect(untouched.State).To(Equal("open"))
			Expect(untouched.Labels).To(ConsistOf(model.LabelProposed), "linter#%d", n)
			Expect(untouched.comments).To(BeEmpty())
		}
	})
})
