/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// requestsPlan is linterPlan with one issue and plan/requests.yaml as given.
func requestsPlan(ns, answers string) scaffold.Tree {
	files := linterPlan(ns, 1)
	files[model.PlanRequestsFile] = scaffold.File{Data: []byte(answers)}
	return files
}

// Step 4 of docs/plans/done/brief-requests.md: a round takes requests, and says
// on each issue what it became.
var _ = Describe("Brief requests in rounds", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("takes requests into a round, refuses a plan that answers them wrong, and says on GitHub what each became", func() {
		repository, _ := requestsBrief(ns, c4v1alpha1.BriefRequests{Mode: model.RequestsAsk,
			Authors: []string{"gray-lenovo"}})
		planRound(ns, 1, 1, releasingLinterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}).Should(Succeed())

		theme := githubFake.openIssue(repository, "gray-lenovo", "Dark theme", "Please add a dark theme.")
		rules := githubFake.openIssue(repository, "gray-lenovo", "Lint rules", "Lint my rules.")
		themeName, rulesName := model.RequestName(repository, theme), model.RequestName(repository, rules)
		Eventually(func(g Gomega) {
			g.Expect(requestNumbers(g, ns)).To(Equal(map[int32]string{int32(theme): model.RequestWaiting,
				int32(rules): model.RequestWaiting}))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Round).To(Equal(int32(1)), "a waiting request starts no round")
		}, "1s").Should(Succeed())

		By("starting a round on the accepted requests, with no new prompt")
		setRequests(ns, func(r *c4v1alpha1.BriefRequests) { r.Accepted = []int32{int32(theme), int32(rules)} })
		Eventually(func(g Gomega) {
			status := getBrief(g, ns).Status
			g.Expect(status.Round).To(Equal(int32(2)))
			g.Expect(status.RoundRequests).To(Equal([]string{themeName, rulesName}))
			g.Expect(requestNumbers(g, ns)).To(Equal(map[int32]string{int32(theme): model.RequestPlanning,
				int32(rules): model.RequestPlanning}))
			requests := string(gitFake.mainFiles(ns, briefRepo)[model.WorkspaceRequestsFile].Data)
			g.Expect(requests).To(And(ContainSubstring("request: "+themeName), ContainSubstring("author: gray-lenovo"),
				ContainSubstring("body: Please add a dark theme."), ContainSubstring("request: "+rulesName)))
			g.Expect(string(gitFake.mainFiles(ns, briefRepo)[model.BriefFile].Data)).To(
				ContainSubstring("## Requests (**new**)"))
		}).Should(Succeed())

		By("refusing a plan that leaves a request out")
		commit := planRound(ns, 2, 1, requestsPlan(ns, fmt.Sprintf("- {request: %s, answer: take, issues: [1]}\n",
			themeName)))
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "cel-lens-plan-2")
			g.Expect(task.Annotations).To(HaveKeyWithValue(c4v1alpha1.AnnotationPlanChecked, commit))
			g.Expect(task.Annotations[c4v1alpha1.AnnotationPlanRefusals]).To(
				ContainSubstring("leaves " + rulesName + " unanswered"))
		}).Should(Succeed())

		By("refusing a plan that answers one not taken and takes one with no issue")
		commit = planRound(ns, 2, 2, requestsPlan(ns, fmt.Sprintf(
			"- {request: %s, answer: take}\n- {request: %s, answer: skip, why: No.}\n- {request: %s#99, answer: skip, why: No.}\n",
			themeName, rulesName, repository)))
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "cel-lens-plan-2")
			g.Expect(task.Annotations).To(HaveKeyWithValue(c4v1alpha1.AnnotationPlanChecked, commit))
			g.Expect(task.Annotations[c4v1alpha1.AnnotationPlanRefusals]).To(And(
				ContainSubstring("takes "+themeName+" with no issue"),
				ContainSubstring("answers "+repository+"#99, which this round didn't take")))
		}).Should(Succeed())

		By("applying a plan that takes one and skips the other")
		planRound(ns, 2, 3, requestsPlan(ns, fmt.Sprintf(
			"- {request: %s, answer: take, issues: [1]}\n- {request: %s, answer: skip, why: \"The linter lints rules already.\"}\n",
			themeName, rulesName)))
		var filed c4v1alpha1.BriefIssue
		Eventually(func(g Gomega) {
			status := getBrief(g, ns).Status
			g.Expect(status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
			g.Expect(status.Round).To(Equal(int32(2)))
			for _, i := range status.Issues {
				if i.Round == 2 {
					filed = i
				}
			}
			g.Expect(filed.Number).NotTo(BeZero())
			g.Expect(status.Requests).To(ConsistOf(And(HaveField("Number", int32(theme)),
				HaveField("Stage", model.RequestPlanned), HaveField("Round", int32(2)),
				HaveField("Issues", []string{fmt.Sprintf("linter#%d", filed.Number)}),
				HaveField("Release", "v0.2.0"))), "the skipped one is closed, so it leaves the list")
		}).Should(Succeed())
		Expect(gitFake.issue(ns, "linter", int(filed.Number)).Title).To(Equal("Lint rule 1"))

		Eventually(func(g Gomega) {
			issue, comments := githubFake.gitHubIssue(repository, theme)
			g.Expect(issue.State).To(Equal("open"), "a planned request stays open until it ships")
			g.Expect(comments).To(ConsistOf(And(
				HavePrefix(model.RequestMarker("cel-lens", model.CommentPlanned)),
				ContainSubstring(model.RequestRoundMarker("cel-lens", 2)),
				ContainSubstring(`Planned as "Lint rule 1", in release v0.2.0.`))))
			issue, comments = githubFake.gitHubIssue(repository, rules)
			g.Expect(issue.State).To(Equal("closed"))
			g.Expect(issue.StateReason).To(Equal("not_planned"))
			g.Expect(comments).To(ConsistOf(ContainSubstring("Not planned: The linter lints rules already.")))
		}).Should(Succeed())

		By("saying it once, read after read")
		asked, _ := githubFake.issueLists(repository)
		Eventually(func(g Gomega) {
			again, _ := githubFake.issueLists(repository)
			g.Expect(again).To(BeNumerically(">", asked))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			for _, n := range []int{theme, rules} {
				_, comments := githubFake.gitHubIssue(repository, n)
				g.Expect(comments).To(HaveLen(1))
			}
			g.Expect(getBrief(g, ns).Status.Round).To(Equal(int32(2)), "no request waits, so no round starts")
		}, 2*time.Second).Should(Succeed())
		Expect(strings.Join(briefEvents(Default, ns), " ")).NotTo(ContainSubstring(eventRequests))
	})
})
