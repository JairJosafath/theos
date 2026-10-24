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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// takeAll accepts every request of Brief cel-lens, waits for round 2 to take
// them, and applies plan there, then waits for each to be planned with its
// planned comment. It returns the issues each became, by request number.
func takeAll(ns, repository string, numbers []int, plan scaffold.Tree) map[int][]string {
	GinkgoHelper()
	accepted := make([]int32, 0, len(numbers))
	for _, n := range numbers {
		accepted = append(accepted, int32(n))
	}
	Eventually(func(g Gomega) {
		g.Expect(getBrief(g, ns).Status.Requests).To(HaveLen(len(numbers)))
	}).Should(Succeed())
	setRequests(ns, func(r *c4v1beta1.BriefRequests) { r.Accepted = accepted })
	Eventually(func(g Gomega) {
		g.Expect(getBrief(g, ns).Status.RoundRequests).To(HaveLen(len(numbers)))
	}).Should(Succeed())
	planRound(ns, 2, 1, plan)
	became := map[int][]string{}
	Eventually(func(g Gomega) {
		status := getBrief(g, ns).Status
		g.Expect(status.Requests).To(HaveLen(len(numbers)))
		for _, r := range status.Requests {
			g.Expect(r.Stage).To(Equal(model.RequestPlanned))
			became[int(r.Number)] = r.Issues
		}
	}).Should(Succeed())
	for _, n := range numbers {
		Eventually(func(g Gomega) {
			_, comments := githubFake.gitHubIssue(repository, n)
			g.Expect(comments).To(ConsistOf(HavePrefix(model.RequestMarker("cel-lens", model.CommentPlanned))))
		}).Should(Succeed())
	}
	return became
}

// closeFiled closes, in Gitea, the issues named container#number.
func closeFiled(ns string, issues ...string) {
	GinkgoHelper()
	for _, name := range issues {
		container, number, _ := strings.Cut(name, "#")
		var n int
		_, err := fmt.Sscan(number, &n)
		Expect(err).NotTo(HaveOccurred())
		Expect(gitFake.closeIssue(ns, container, n)).To(Succeed())
	}
}

// publishLinter sets the linter Crew's latest release as tagging and
// publishing it would, and returns its published page.
func publishLinter(ns, repository, tag string) string {
	GinkgoHelper()
	url := "https://github.com/" + repository + "/releases/tag/" + tag
	Eventually(func(g Gomega) {
		crew := linterCrew(g, ns)
		crew.Status.LastRelease = &c4v1beta1.CrewRelease{Tag: tag, Commit: "abc123", Time: metav1.Now(),
			Run: model.RunSucceeded, Publish: &c4v1beta1.CrewReleasePublish{State: model.PublishPublished, URL: url}}
		g.Expect(k8sClient.Status().Update(ctx, crew)).To(Succeed())
	}).Should(Succeed())
	return url
}

// Step 5 of docs/plans/done/brief-requests.md: a request hears when it ships.
var _ = Describe("Brief requests shipped", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("says where a planned request shipped once its release is published, once, and closes it as completed", func() {
		repository, _ := requestsBrief(ns, c4v1beta1.BriefRequests{Mode: model.RequestsAsk,
			Authors: []string{"gray-lenovo"}})
		planRound(ns, 1, 1, releasingLinterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())

		theme := githubFake.openIssue(repository, "gray-lenovo", "Dark theme", "Please.")
		early := githubFake.openIssue(repository, "gray-lenovo", "Light theme", "Too.")
		byHand := githubFake.openIssue(repository, "gray-lenovo", "Sepia theme", "Also.")
		split := githubFake.openIssue(repository, "gray-lenovo", "Server rules", "Lint them on the server.")
		plan := linterPlan(ns, 4)
		plan[model.PlanModelFile] = scaffold.File{Data: append(plan[model.PlanModelFile].Data,
			[]byte(engineContainer("server", "Server"))...)}
		plan["plan/issues/05-server.md"] = scaffold.File{Data: []byte(linterIssue(5))}
		plan[model.PlanRequestsFile] = scaffold.File{Data: fmt.Appendf(nil,
			"- {request: %[1]s#%[2]d, answer: take, issues: [1]}\n- {request: %[1]s#%[3]d, answer: take, issues: [2]}\n"+
				"- {request: %[1]s#%[4]d, answer: take, issues: [3]}\n- {request: %[1]s#%[5]d, answer: take, issues: [4, 5]}\n",
			repository, theme, early, byHand, split)}
		became := takeAll(ns, repository, []int{theme, early, byHand, split}, plan)
		Expect(became[split]).To(HaveLen(2))
		Expect(became[split][1]).To(HavePrefix("server#"))

		// The second was said before a restart that came before its close;
		// the third a person closed on GitHub.
		githubFake.mu.Lock()
		githubFake.issueState().issues[repository][early-1].comments = append(
			githubFake.issueState().issues[repository][early-1].comments,
			fakeGitHubComment{ID: 999, Body: "Shipped.\n" + model.RequestReleasedMarker("cel-lens", 2),
				User: fakeGitHubUser{"c4factory-bot"}})
		githubFake.mu.Unlock()
		githubFake.closeIssue(repository, byHand)

		By("waiting while the issues are closed but their release isn't published")
		closeFiled(ns, became[theme][0], became[early][0], became[byHand][0], became[split][0])
		Consistently(func(g Gomega) {
			_, comments := githubFake.gitHubIssue(repository, theme)
			g.Expect(comments).To(HaveLen(1))
			g.Expect(requestNumbers(g, ns)).To(HaveKeyWithValue(int32(theme), model.RequestPlanned))
		}, "2s").Should(Succeed())

		By("saying so once v0.2.0 is published")
		url := publishLinter(ns, repository, "v0.2.0")
		Eventually(func(g Gomega) {
			issue, comments := githubFake.gitHubIssue(repository, theme)
			g.Expect(issue.State).To(Equal("closed"))
			g.Expect(issue.StateReason).To(Equal("completed"))
			g.Expect(comments).To(HaveLen(2))
			g.Expect(comments[1]).To(Equal(model.ReleasedComment("cel-lens", []model.Shipped{{Tag: "v0.2.0", URL: url}}) +
				model.RequestReleasedMarker("cel-lens", 2) + "\n"))
			g.Expect(comments[1]).To(ContainSubstring("Shipped in v0.2.0: " + url))
			issue, comments = githubFake.gitHubIssue(repository, early)
			g.Expect(issue.State).To(Equal("closed"))
			g.Expect(issue.StateReason).To(Equal("completed"))
			g.Expect(comments).To(HaveLen(2), "said before the restart, not again")
			g.Expect(requestNumbers(g, ns)).To(Equal(map[int32]string{int32(split): model.RequestPlanned}),
				"the shipped ones and the one closed by hand leave the list; the split one waits for its server issue")
		}).Should(Succeed())
		issue, comments := githubFake.gitHubIssue(repository, byHand)
		Expect(issue.StateReason).To(BeEmpty(), "closed by a person, and left so")
		Expect(comments).To(HaveLen(1))
		_, comments = githubFake.gitHubIssue(repository, split)
		Expect(comments).To(HaveLen(1))

		By("saying where a request split over two Containers went once both are done")
		closeFiled(ns, became[split][1])
		_, server, _ := strings.Cut(became[split][1], "#")
		Eventually(func(g Gomega) {
			issue, comments := githubFake.gitHubIssue(repository, split)
			g.Expect(issue.State).To(Equal("closed"))
			g.Expect(issue.StateReason).To(Equal("completed"))
			g.Expect(comments).To(HaveLen(2))
			g.Expect(comments[1]).To(ContainSubstring(fmt.Sprintf("Shipped in v0.2.0: %s\nDone in http://gitea/%s/server/issues/%s.\n",
				url, ns, server)))
			g.Expect(getBrief(g, ns).Status.Requests).To(BeEmpty())
		}).Should(Succeed())

		Consistently(func(g Gomega) {
			for n, want := range map[int]int{theme: 2, early: 2, byHand: 1, split: 2} {
				_, comments := githubFake.gitHubIssue(repository, n)
				g.Expect(comments).To(HaveLen(want))
			}
		}, 2*time.Second).Should(Succeed())
	})

	It("says a request was done in its issue when its Container publishes nowhere", func() {
		repository, _ := requestsBrief(ns, c4v1beta1.BriefRequests{Mode: model.RequestsAsk,
			Authors: []string{"gray-lenovo"}})
		Eventually(func(g Gomega) {
			brief := getBrief(g, ns)
			g.Expect(patch(brief, func() { brief.Spec.Releases[0].Container = "elsewhere" })).To(Succeed())
		}).Should(Succeed())
		planRound(ns, 1, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())

		theme := githubFake.openIssue(repository, "gray-lenovo", "Dark theme", "Please.")
		became := takeAll(ns, repository, []int{theme},
			requestsPlan(ns, fmt.Sprintf("- {request: %s#%d, answer: take, issues: [1]}\n", repository, theme)))
		Expect(linterCrew(Default, ns).Spec.PublishReleases).To(BeNil())

		closeFiled(ns, became[theme]...)
		_, number, _ := strings.Cut(became[theme][0], "#")
		Eventually(func(g Gomega) {
			issue, comments := githubFake.gitHubIssue(repository, theme)
			g.Expect(issue.State).To(Equal("closed"))
			g.Expect(issue.StateReason).To(Equal("completed"))
			g.Expect(comments).To(HaveLen(2))
			g.Expect(comments[1]).To(HavePrefix(model.RequestMarker("cel-lens", model.CommentReleased) +
				fmt.Sprintf("\nDone in http://gitea/%s/linter/issues/%s.\n", ns, number)))
			g.Expect(getBrief(g, ns).Status.Requests).To(BeEmpty())
		}).Should(Succeed())
	})
})
