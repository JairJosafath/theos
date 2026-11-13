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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// takeAll accepts every request of Brief cel-lens, waits for round 2 to take
// them, and applies plan there, then waits for each to be planned with its
// planned comment on tr. It returns the issues each became, by request number.
func takeAll(ns string, tr *specTracker, numbers []int, plan scaffold.Tree) map[int][]string {
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
			_, comments := tr.issue(n)
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
		Expect(gitFake.CloseIssueByHand(ns, container, n)).To(Succeed())
	}
}

// mergedFiledTask records the issue named container#number as merged by a
// Task through a pull request, as the Task's round would, and returns the
// pull request's page. A merged Task is read by its Crew's release logic
// too, which would hold, tag or prepare a release on it; a Brief spec keeps
// the two apart by a Crew that neither tags nor prepares releases (a plan
// with no releases file), which the helper checks first. The Task's Agent
// doesn't exist, so it does nothing before it is Merged, and it records no
// merge commit.
func mergedFiledTask(ns, issue string) string {
	GinkgoHelper()
	container, number, _ := strings.Cut(issue, "#")
	var n int
	_, err := fmt.Sscan(number, &n)
	Expect(err).NotTo(HaveOccurred())
	Eventually(func(g Gomega) {
		crew := &c4v1beta1.Crew{}
		g.Expect(k8sClient.Get(ctx, key(ns, container), crew)).To(Succeed())
		g.Expect(crew.Spec.TagReleases).To(BeFalse(), "a Crew that tags releases reads the merged Task")
		g.Expect(crew.Spec.PrepareReleases).To(BeFalse(), "a Crew that prepares releases reads the merged Task")
	}).Should(Succeed())
	name := fmt.Sprintf("%s-%d-merged", container, n)
	task := newTask(ns, name, "")
	task.Spec.ContainerRef = c4v1beta1.LocalRef{Name: container}
	task.Spec.AgentRef = c4v1beta1.LocalRef{Name: "nobody"}
	task.Spec.Issue = int32(n)
	mustCreate(task)
	// Once it waits for its Agent, the Task reconciler writes nothing more.
	Eventually(func(g Gomega) {
		cond := condition(getTask(g, ns, name).Status.Conditions, ConditionProgressing)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Reason).To(Equal("AgentNotFound"))
	}).Should(Succeed())
	pull := 900 + n
	url := fmt.Sprintf("http://gitea/%s/%s/pulls/%d", ns, container, pull)
	Eventually(func(g Gomega) {
		task := getTask(g, ns, name)
		task.Status.Phase, task.Status.CompletedAt = c4v1beta1.TaskMerged, &metav1.Time{Time: time.Now()}
		task.Status.PullRequest, task.Status.PullRequestURL = int32(pull), url
		g.Expect(k8sClient.Status().Update(ctx, task)).To(Succeed())
	}).Should(Succeed())
	return url
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
		tr := requestsBrief(ns, c4v1beta1.BriefRequests{Mode: model.RequestsAsk,
			Authors: []string{"ada"}})
		repository := tr.repository
		planRound(ns, 1, 1, releasingLinterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())

		theme := tr.file("ada", "Dark theme", "Please.")
		early := tr.file("ada", "Light theme", "Too.")
		byHand := tr.file("ada", "Sepia theme", "Also.")
		split := tr.file("ada", "Server rules", "Lint them on the server.")
		plan := linterPlan(ns, 4)
		plan[model.PlanModelFile] = scaffold.File{Data: append(plan[model.PlanModelFile].Data,
			[]byte(engineContainer("server", "Server"))...)}
		plan["plan/issues/05-server.md"] = scaffold.File{Data: []byte(linterIssue(5))}
		plan[model.PlanRequestsFile] = scaffold.File{Data: fmt.Appendf(nil,
			"- {request: %[1]s#%[2]d, answer: take, issues: [1]}\n- {request: %[1]s#%[3]d, answer: take, issues: [2]}\n"+
				"- {request: %[1]s#%[4]d, answer: take, issues: [3]}\n- {request: %[1]s#%[5]d, answer: take, issues: [4, 5]}\n",
			repository, theme, early, byHand, split)}
		became := takeAll(ns, tr, []int{theme, early, byHand, split}, plan)
		Expect(became[split]).To(HaveLen(2))
		Expect(became[split][1]).To(HavePrefix("server#"))

		// The second was said before a restart that came before its close;
		// the third a person closed on the tracker.
		tr.say(early, trackerLogin, "Shipped.\n"+model.RequestReleasedMarker("cel-lens", 2))
		tr.closeByHand(byHand)
		closes := tr.Calls("CloseIssue")

		By("waiting while the issues are closed but their release isn't published")
		closeFiled(ns, became[theme][0], became[early][0], became[byHand][0], became[split][0])
		Consistently(func(g Gomega) {
			_, comments := tr.issue(theme)
			g.Expect(comments).To(HaveLen(1))
			g.Expect(requestNumbers(g, ns)).To(HaveKeyWithValue(int32(theme), model.RequestPlanned))
		}, "2s").Should(Succeed())

		By("saying so once v0.2.0 is published")
		url := publishLinter(ns, repository, "v0.2.0")
		Eventually(func(g Gomega) {
			issue, comments := tr.issue(theme)
			g.Expect(issue.Closed).To(BeTrue())
			g.Expect(issue.Reason).To(Equal(ports.CloseCompleted))
			g.Expect(comments).To(HaveLen(2))
			g.Expect(comments[1]).To(Equal(model.ReleasedComment("cel-lens", []model.Shipped{{Tag: "v0.2.0", URL: url}}, nil) +
				model.RequestReleasedMarker("cel-lens", 2) + "\n"))
			g.Expect(comments[1]).To(ContainSubstring("Shipped in v0.2.0: " + url))
			issue, comments = tr.issue(early)
			g.Expect(issue.Closed).To(BeTrue())
			g.Expect(issue.Reason).To(Equal(ports.CloseCompleted))
			g.Expect(comments).To(HaveLen(2), "said before the restart, not again")
			g.Expect(requestNumbers(g, ns)).To(Equal(map[int32]string{int32(split): model.RequestPlanned}),
				"the shipped ones and the one closed by hand leave the list; the split one waits for its server issue")
		}).Should(Succeed())
		// The theme and the early one: the person's close is the only other.
		Expect(tr.Calls("CloseIssue")-closes).To(Equal(2), "closed by a person, and left so")
		_, comments := tr.issue(byHand)
		Expect(comments).To(HaveLen(1))
		_, comments = tr.issue(split)
		Expect(comments).To(HaveLen(1))

		By("saying where a request split over two Containers went once both are done")
		closeFiled(ns, became[split][1])
		_, server, _ := strings.Cut(became[split][1], "#")
		Eventually(func(g Gomega) {
			issue, comments := tr.issue(split)
			g.Expect(issue.Closed).To(BeTrue())
			g.Expect(issue.Reason).To(Equal(ports.CloseCompleted))
			g.Expect(comments).To(HaveLen(2))
			g.Expect(comments[1]).To(ContainSubstring(fmt.Sprintf("Shipped in v0.2.0: %s\nDone in http://gitea/%s/server/issues/%s.\n",
				url, ns, server)))
			g.Expect(getBrief(g, ns).Status.Requests).To(BeEmpty())
		}).Should(Succeed())

		Consistently(func(g Gomega) {
			for n, want := range map[int]int{theme: 2, early: 2, byHand: 1, split: 2} {
				_, comments := tr.issue(n)
				g.Expect(comments).To(HaveLen(want))
			}
		}, 2*time.Second).Should(Succeed())
	})

	It("says a request was done in its issue when its Container publishes nowhere", func() {
		tr := requestsBrief(ns, c4v1beta1.BriefRequests{Mode: model.RequestsAsk,
			Authors: []string{"ada"}})
		repository := tr.repository
		Eventually(func(g Gomega) {
			brief := getBrief(g, ns)
			g.Expect(patch(brief, func() { brief.Spec.Releases[0].Container = "elsewhere" })).To(Succeed())
		}).Should(Succeed())
		planRound(ns, 1, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())

		theme := tr.file("ada", "Dark theme", "Please.")
		became := takeAll(ns, tr, []int{theme},
			requestsPlan(ns, fmt.Sprintf("- {request: %s#%d, answer: take, issues: [1]}\n", repository, theme)))
		Expect(linterCrew(Default, ns).Spec.PublishReleases).To(BeNil())

		closeFiled(ns, became[theme]...)
		_, number, _ := strings.Cut(became[theme][0], "#")
		Eventually(func(g Gomega) {
			issue, comments := tr.issue(theme)
			g.Expect(issue.Closed).To(BeTrue())
			g.Expect(issue.Reason).To(Equal(ports.CloseCompleted))
			g.Expect(comments).To(HaveLen(2))
			g.Expect(comments[1]).To(HavePrefix(model.RequestMarker("cel-lens", model.CommentReleased) +
				fmt.Sprintf("\nDone in http://gitea/%s/linter/issues/%s.\n", ns, number)))
			g.Expect(getBrief(g, ns).Status.Requests).To(BeEmpty())
		}).Should(Succeed())
	})

	// #245: the line of the merged pull requests, end to end, at tier 4.
	It("names the pull request a Task merged for each issue of a request, and none for an issue closed by hand", func() {
		tr := requestsBrief(ns, c4v1beta1.BriefRequests{Mode: model.RequestsAsk,
			Authors: []string{"ada"}})
		repository := tr.repository
		Eventually(func(g Gomega) {
			brief := getBrief(g, ns)
			g.Expect(patch(brief, func() { brief.Spec.Releases[0].Container = "elsewhere" })).To(Succeed())
		}).Should(Succeed())
		planRound(ns, 1, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())

		theme := tr.file("ada", "Dark theme", "Please.")
		plan := linterPlan(ns, 2)
		plan[model.PlanRequestsFile] = scaffold.File{Data: fmt.Appendf(nil,
			"- {request: %s#%d, answer: take, issues: [1, 2]}\n", repository, theme)}
		became := takeAll(ns, tr, []int{theme}, plan)
		Expect(became[theme]).To(HaveLen(2))
		merged, byHand := became[theme][0], became[theme][1]

		pull := mergedFiledTask(ns, merged)
		closeFiled(ns, merged, byHand)
		page := func(issue string) string {
			_, number, _ := strings.Cut(issue, "#")
			return fmt.Sprintf("http://gitea/%s/linter/issues/%s", ns, number)
		}
		Eventually(func(g Gomega) {
			issue, comments := tr.issue(theme)
			g.Expect(issue.Closed).To(BeTrue())
			g.Expect(issue.Reason).To(Equal(ports.CloseCompleted))
			g.Expect(comments).To(HaveLen(2))
			g.Expect(comments[1]).To(HavePrefix(model.RequestMarker("cel-lens", model.CommentReleased) +
				fmt.Sprintf("\nDone in %s and %s.\nMerged in %s for %s.\n", page(merged), page(byHand), pull,
					page(merged))))
			g.Expect(getBrief(g, ns).Status.Requests).To(BeEmpty())
		}).Should(Succeed())
	})
})
