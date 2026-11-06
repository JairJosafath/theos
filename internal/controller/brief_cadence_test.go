/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
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

// dailyBrief is Brief cel-lens releasing its apps every day, at the CRD's
// default hour.
func dailyBrief(ns string) *c4v1beta1.Brief {
	brief := newBrief(ns, "cel-lens")
	brief.Spec.ReleaseCadence = &c4v1beta1.ReleaseCadence{Every: model.CadenceDay}
	return brief
}

// planRoundOf pushes round's plan once the Brief starts it, after the prompt
// that asks for it, and waits for the round to be planned.
func planRoundOf(ns string, round int32, prompt string, plan scaffold.Tree) {
	GinkgoHelper()
	if round > 1 {
		brief := getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = append(brief.Spec.Prompts, prompt) })).To(Succeed())
	}
	planRound(ns, int(round), 1, plan)
	Eventually(func(g Gomega) {
		b := getBrief(g, ns)
		g.Expect(b.Status.Round).To(Equal(round))
		g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
	}).Should(Succeed())
}

// Step 5 of docs/plans/done/release-cadence.md: under a cadence a Brief's rounds
// file a released app's work into the release in progress.
var _ = Describe("Brief release cadence", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("files a day's rounds, a fix and a feature too, in one release, and a round after its cut in the next", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(dailyBrief(ns))
		planRoundOf(ns, 1, "", releasingLinterPlan(ns, 1))
		Expect(roundIssueMilestones(Default, ns, 1)).To(Equal([]string{"v0.1.0"}))
		milestones := gitFake.AllMilestones(ns, "linter")
		Expect(milestones).To(HaveLen(1))
		Expect(model.HasReleaseMilestoneMarker(milestones[0].Description, "cel-lens", 1)).To(BeTrue())
		next, err := model.NextCut(model.ReleaseCadence{Every: model.CadenceDay}, time.Time{}, time.Now())
		Expect(err).NotTo(HaveOccurred())
		Expect(milestones[0].Due).To(BeTemporally("~", next, time.Minute), "due at the next cut")
		Eventually(func(g Gomega) {
			release := briefApp(g, ns, "linter").NextRelease
			g.Expect(release).NotTo(BeNil(), "the Brief copies the Crew's next cut")
			g.Expect(release.Tag).To(Equal("v0.1.0"))
			g.Expect(release.Time.Time).To(BeTemporally("~", next, time.Minute))
			g.Expect(release.TimeZone).To(Equal("UTC"))
			g.Expect(release.Merged).To(BeZero())
		}).Should(Succeed())

		By("filing the next round's fix and feature in the same release, with no patch release")
		planRoundOf(ns, 2, "Fix the crash, and lint VAPs.", mixedLinterPlan(ns))
		Expect(roundIssueMilestones(Default, ns, 2)).To(Equal([]string{"v0.1.0", "v0.1.0"}))
		Expect(gitFake.AllMilestones(ns, "linter")).To(HaveLen(1))

		By("filing a round after the cut in the next release")
		repo := ports.Repository{Owner: ns, Name: "linter"}
		Expect(gitFake.DescribeMilestone(context.Background(), repo, milestones[0].ID,
			model.AddMarker(milestones[0].Description, model.CutMarker("linter", time.Now())))).To(Succeed())
		planRoundOf(ns, 3, "Lint one more rule.", linterPlan(ns, 1))
		Expect(roundIssueMilestones(Default, ns, 3)).To(Equal([]string{"v0.2.0"}))
		Expect(gitFake.AllMilestones(ns, "linter")).To(HaveLen(2))
	})

	It("labels the work a plan marks release: now, and a suggestion it takes now and as a fix", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(dailyBrief(ns))
		plan := releasingLinterPlan(ns, 1)
		plan["plan/issues/01-linter.md"] = scaffold.File{Data: []byte(strings.Replace(linterIssue(1),
			"labels: [kind/feature, status/proposed]\n", "labels: [kind/bug, status/proposed]\nrelease: now\n", 1))}
		planRoundOf(ns, 1, "", plan)
		crash := getBrief(Default, ns).Status.Issues[0]
		Expect(gitFake.Issue(ns, "linter", int(crash.Number)).Labels).To(ContainElement(model.LabelReleaseNow))

		By("taking a reviewer's suggestion now, labelled as a fix for the cut")
		crew := linterCrew(Default, ns)
		Expect(patch(crew, func() { crew.Spec.Paused = true })).To(Succeed())
		flaky := gitFake.AddIssue(ns, "linter", "Test the empty rule", model.FollowUpIssue(model.FollowUp{
			Body: "No test covers it.", Criteria: []string{"A test lints the empty rule"}},
			"Agent `reviewer`", "http://git.test/pulls/2", 1, "linter-1"), []string{model.LabelProposed})
		gitFake.PutInMilestone(ns, "linter", flaky, model.FollowUpMilestone)
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.SuggestionsWaiting).To(Equal(int32(1)))
		}).Should(Succeed())
		plan = linterPlan(ns, 0)
		plan[model.PlanSuggestionsFile] = scaffold.File{Data: []byte(fmt.Sprintf(
			"- {container: linter, issue: %d, answer: take, fix: true, now: true, why: The prompt ships it today.}\n",
			flaky))}
		planRoundOf(ns, 2, "Take the test of the empty rule, and ship it today.", plan)
		taken := gitFake.Issue(ns, "linter", flaky)
		Expect(taken.Labels).To(ContainElements(model.LabelReady, model.LabelReleaseNow, model.LabelBug))
		Expect(taken.InMilestone).To(Equal("v0.1.0"), "with the rest of the day's work")
	})

	It("writes its cadence onto the Crews it made, follows a change, and leaves a Crew it didn't make alone", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mine := c4v1beta1.ReleaseCadence{Every: model.CadenceWeek, At: "09:30"}
		mustCreate(&c4v1beta1.Crew{ObjectMeta: objectMeta(ns, "linter"), Spec: c4v1beta1.CrewSpec{
			ContainerRef: c4v1beta1.LocalRef{Name: "linter"},
			Implementer:  c4v1beta1.LocalRef{Name: "implementer"}, Reviewer: c4v1beta1.LocalRef{Name: "reviewer"},
			TagReleases: true, ReleaseCadence: &mine}})
		orig := linterCrew(Default, ns)
		brief := dailyBrief(ns)
		brief.Spec.Prompts = []string{"Add a web front end."}
		mustCreate(brief)
		plan := releasingLinterPlan(ns, 1)
		plan[model.PlanModelFile] = scaffold.File{Data: []byte(projectDoc(ns) + linterDocs + engineContainer("web", "Web"))}
		plan["plan/issues/02-web.md"] = scaffold.File{Data: []byte(strings.Replace(linterIssue(2), "Lint rule 2",
			"Show the lint", 1))}
		planRoundOf(ns, 1, "", plan)

		By("leaving the linter's Crew, which a person made, with its own")
		Expect(linterCrew(Default, ns).Spec).To(Equal(orig.Spec))

		By("giving the web Crew it made its own cadence, and following a change of it")
		Eventually(func(g Gomega) {
			var crew c4v1beta1.Crew
			g.Expect(k8sClient.Get(ctx, key(ns, "web"), &crew)).To(Succeed())
			g.Expect(crew.Spec.ReleaseCadence).NotTo(BeNil())
			g.Expect(crew.Spec.ReleaseCadence.Every).To(Equal(model.CadenceDay))
			g.Expect(crew.Spec.ReleaseCadence.At).To(Equal("18:00"))
		}).Should(Succeed())
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() {
			brief.Spec.ReleaseCadence = &c4v1beta1.ReleaseCadence{Every: model.CadenceWeek, Weekday: "Monday",
				TimeZone: "Europe/Amsterdam"}
		})).To(Succeed())
		Eventually(func(g Gomega) {
			var crew c4v1beta1.Crew
			g.Expect(k8sClient.Get(ctx, key(ns, "web"), &crew)).To(Succeed())
			g.Expect(crew.Spec.ReleaseCadence).To(Equal(&c4v1beta1.ReleaseCadence{Every: model.CadenceWeek,
				At: "18:00", TimeZone: "Europe/Amsterdam", Weekday: "Monday"}))
		}).Should(Succeed())
		Expect(linterCrew(Default, ns).Spec.ReleaseCadence).To(Equal(orig.Spec.ReleaseCadence))
	})

	It("reads every: Day at 18:00 UTC for a Brief stored without a cadence", func() {
		brief := newBrief(ns, "cel-lens")
		brief.Spec.ReleaseCadence = nil
		mustCreate(brief)
		Expect(getBrief(Default, ns).Spec.ReleaseCadence).To(Equal(&c4v1beta1.ReleaseCadence{
			Every: model.CadenceDay, At: "18:00", TimeZone: "UTC"}))
	})
})
