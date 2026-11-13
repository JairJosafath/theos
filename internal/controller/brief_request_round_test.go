/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
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

	It("takes requests into a round, refuses a plan that answers them wrong, and says on the tracker what each became", func() {
		tr := requestsBrief(ns, c4v1beta1.BriefRequests{Mode: model.RequestsAsk,
			Authors: []string{"ada"}})
		repository := tr.repository
		planRound(ns, 1, 1, releasingLinterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())

		theme := tr.file("ada", "Dark theme", "Please add a dark theme.")
		rules := tr.file("ada", "Lint rules", "Lint my rules.")
		themeName, rulesName := model.RequestName(repository, theme), model.RequestName(repository, rules)
		Eventually(func(g Gomega) {
			g.Expect(requestNumbers(g, ns)).To(Equal(map[int32]string{int32(theme): model.RequestWaiting,
				int32(rules): model.RequestWaiting}))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Round).To(Equal(int32(1)), "a waiting request starts no round")
		}, "1s").Should(Succeed())

		By("starting a round on the accepted requests, with no new prompt")
		setRequests(ns, func(r *c4v1beta1.BriefRequests) { r.Accepted = []int32{int32(theme), int32(rules)} })
		Eventually(func(g Gomega) {
			status := getBrief(g, ns).Status
			g.Expect(status.Round).To(Equal(int32(2)))
			g.Expect(status.RoundRequests).To(Equal([]string{themeName, rulesName}))
			g.Expect(requestNumbers(g, ns)).To(Equal(map[int32]string{int32(theme): model.RequestPlanning,
				int32(rules): model.RequestPlanning}))
			requests := string(gitFake.MainFiles(ns, briefRepo)[model.WorkspaceRequestsFile].Data)
			g.Expect(requests).To(And(ContainSubstring("request: "+themeName), ContainSubstring("author: ada"),
				ContainSubstring("body: Please add a dark theme."), ContainSubstring("request: "+rulesName)))
			g.Expect(string(gitFake.MainFiles(ns, briefRepo)[model.BriefFile].Data)).To(
				ContainSubstring("## Requests (**new**)"))
		}).Should(Succeed())

		By("refusing a plan that leaves a request out")
		commit := planRound(ns, 2, 1, requestsPlan(ns, fmt.Sprintf("- {request: %s, answer: take, issues: [1]}\n",
			themeName)))
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "cel-lens-plan-2")
			g.Expect(task.Annotations).To(HaveKeyWithValue(c4v1beta1.AnnotationPlanChecked, commit))
			g.Expect(task.Annotations[c4v1beta1.AnnotationPlanRefusals]).To(
				ContainSubstring("leaves " + rulesName + " unanswered"))
		}).Should(Succeed())

		By("refusing a plan that answers one not taken and takes one with no issue")
		commit = planRound(ns, 2, 2, requestsPlan(ns, fmt.Sprintf(
			"- {request: %s, answer: take}\n- {request: %s, answer: skip, why: No.}\n- {request: %s#99, answer: skip, why: No.}\n",
			themeName, rulesName, repository)))
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "cel-lens-plan-2")
			g.Expect(task.Annotations).To(HaveKeyWithValue(c4v1beta1.AnnotationPlanChecked, commit))
			g.Expect(task.Annotations[c4v1beta1.AnnotationPlanRefusals]).To(And(
				ContainSubstring("takes "+themeName+" with no issue"),
				ContainSubstring("answers "+repository+"#99, which this round didn't take")))
		}).Should(Succeed())

		By("applying a plan that takes one and skips the other")
		planRound(ns, 2, 3, requestsPlan(ns, fmt.Sprintf(
			"- {request: %s, answer: take, issues: [1]}\n- {request: %s, answer: skip, why: \"The linter lints rules already.\"}\n",
			themeName, rulesName)))
		var filed c4v1beta1.BriefIssue
		Eventually(func(g Gomega) {
			status := getBrief(g, ns).Status
			g.Expect(status.Phase).To(Equal(c4v1beta1.BriefPlanned))
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
		Expect(gitFake.Issue(ns, "linter", int(filed.Number)).Title).To(Equal("Lint rule 1"))

		Eventually(func(g Gomega) {
			issue, comments := tr.issue(theme)
			g.Expect(issue.Closed).To(BeFalse(), "a planned request stays open until it ships")
			g.Expect(comments).To(ConsistOf(And(
				HavePrefix(model.RequestMarker("cel-lens", model.CommentPlanned)),
				ContainSubstring(model.RequestRoundMarker("cel-lens", 2)),
				ContainSubstring(`Planned as "Lint rule 1", in release v0.2.0.`))))
			issue, comments = tr.issue(rules)
			g.Expect(issue.Closed).To(BeTrue())
			g.Expect(issue.Reason).To(Equal(ports.CloseNotPlanned))
			g.Expect(comments).To(ConsistOf(ContainSubstring("Not planned: The linter lints rules already.")))
		}).Should(Succeed())

		By("saying it once, read after read")
		asked, _ := tr.lists()
		Eventually(func(g Gomega) {
			again, _ := tr.lists()
			g.Expect(again).To(BeNumerically(">", asked))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			for _, n := range []int{theme, rules} {
				_, comments := tr.issue(n)
				g.Expect(comments).To(HaveLen(1))
			}
			g.Expect(getBrief(g, ns).Status.Round).To(Equal(int32(2)), "no request waits, so no round starts")
		}, 2*time.Second).Should(Succeed())
		Expect(strings.Join(briefEvents(Default, ns), " ")).NotTo(ContainSubstring(eventRequests))
	})
})
