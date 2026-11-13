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

	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// releasingLinterPlan is linterPlan with releases.yaml naming the linter.
func releasingLinterPlan(ns string, issues int) scaffold.Tree {
	files := linterPlan(ns, issues)
	files[model.PlanReleasesFile] = scaffold.File{Data: []byte("containers: [linter]\n")}
	return files
}

// bugLinterPlan is linterPlan whose issues are all fixes.
func bugLinterPlan(ns string, issues int) scaffold.Tree {
	files := linterPlan(ns, 0)
	for n := 1; n <= issues; n++ {
		files[fmt.Sprintf("plan/issues/%02d-linter.md", n)] = scaffold.File{
			Data: []byte(strings.Replace(linterIssue(n), "kind/feature", model.LabelBug, 1))}
	}
	return files
}

// mixedLinterPlan is releasingLinterPlan with two issues: a kind/bug fix and a
// feature.
func mixedLinterPlan(ns string) scaffold.Tree {
	files := releasingLinterPlan(ns, 2)
	files["plan/issues/01-linter.md"] = scaffold.File{
		Data: []byte(strings.Replace(linterIssue(1), "kind/feature", model.LabelBug, 1))}
	return files
}

// milestoneDue returns the due date of the linter's milestone titled title.
func milestoneDue(ns, title string) time.Time {
	GinkgoHelper()
	for _, m := range gitFake.AllMilestones(ns, "linter") {
		if m.Title == title {
			return m.Due
		}
	}
	Fail("no milestone " + title)
	return time.Time{}
}

// roundIssueMilestones returns the milestone titles of the issues Brief
// cel-lens recorded for round.
func roundIssueMilestones(g Gomega, ns string, round int32) []string {
	var out []string
	for _, issue := range getBrief(g, ns).Status.Issues {
		if issue.Round == round {
			out = append(out, gitFake.Issue(ns, "linter", int(issue.Number)).InMilestone)
		}
	}
	return out
}

func linterRelease(repository string) c4v1beta1.BriefRelease {
	return c4v1beta1.BriefRelease{Container: "linter", CrewPublishReleases: c4v1beta1.CrewPublishReleases{
		GitHub: c4v1beta1.CrewPublishGitHub{Repository: repository,
			TokenSecretRef: c4v1beta1.LocalRef{Name: "cel-lens-github"}}}}
}

// linterCrew reads the Crew of the linter.
func linterCrew(g Gomega, ns string) *c4v1beta1.Crew {
	crew := &c4v1beta1.Crew{}
	g.Expect(k8sClient.Get(ctx, key(ns, "linter"), crew)).To(Succeed())
	return crew
}

// setLinterTarget changes the repository the linter's Crew publishes to, as a
// person would on the Crew.
func setLinterTarget(ns, repository string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		crew := linterCrew(g, ns)
		crew.Spec.PublishReleases.GitHub.Repository = repository
		g.Expect(k8sClient.Update(ctx, crew)).To(Succeed())
	}).Should(Succeed())
}

// markLinter removes the Brief's mark from the linter's Crew, as on a Crew
// made before it, changed by change when given, in one write.
func markLinter(ns string, change func(*c4v1beta1.Crew)) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		crew := linterCrew(g, ns)
		delete(crew.Annotations, c4v1beta1.AnnotationBriefPublishReleases)
		if change != nil {
			change(crew)
		}
		g.Expect(k8sClient.Update(ctx, crew)).To(Succeed())
	}).Should(Succeed())
}

// linterTargetEvents are the ReleaseTargetDiffers Events of the linter's Crew.
func linterTargetEvents(g Gomega, ns string) []eventsv1.Event {
	var events eventsv1.EventList
	g.Expect(k8sClient.List(ctx, &events, client.InNamespace(ns))).To(Succeed())
	var out []eventsv1.Event
	for _, e := range events.Items {
		if e.Regarding.Kind == "Crew" && e.Regarding.Name == "linter" && e.Reason == "ReleaseTargetDiffers" {
			out = append(out, e)
		}
	}
	return out
}

// targetSetByBrief checks that the linter's Crew publishes to repository,
// marked so, says Brief cel-lens sets it, and the Brief needs nothing of it.
func targetSetByBrief(g Gomega, ns, repository string) {
	crew := linterCrew(g, ns)
	g.Expect(crew.Spec.PublishReleases.GitHub.Repository).To(Equal(repository))
	g.Expect(crew.Annotations[c4v1beta1.AnnotationBriefPublishReleases]).
		To(ContainSubstring(`"repository":"` + repository + `"`))
	cond := condition(crew.Status.Conditions, c4v1beta1.ConditionReleaseTarget)
	g.Expect(cond).NotTo(BeNil())
	g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	g.Expect(cond.Reason).To(Equal("SetByBrief"))
	g.Expect(cond.Message).To(Equal("Brief cel-lens sets this in spec.releases; change it there"))
	g.Expect(getBrief(g, ns).Status.NeedsYou).NotTo(ContainElement(HaveField("Kind", "Crew")))
}

// targetHeld checks that the linter's Crew, changed to crewRepo under a Brief
// that says briefRepo, is held: the condition, the Event and the Brief's
// needsYou item say so.
func targetHeld(g Gomega, ns, crewRepo, briefRepo string) {
	crew := linterCrew(g, ns)
	g.Expect(crew.Spec.PublishReleases.GitHub.Repository).To(Equal(crewRepo))
	cond := condition(crew.Status.Conditions, c4v1beta1.ConditionReleaseTarget)
	g.Expect(cond).NotTo(BeNil())
	g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(cond.Reason).To(Equal("DiffersFromBrief"))
	g.Expect(cond.Message).To(And(ContainSubstring(crewRepo), ContainSubstring(briefRepo)))
	g.Expect(linterTargetEvents(g, ns)).To(ContainElement(HaveField("Note", cond.Message)))
	g.Expect(getBrief(g, ns).Status.NeedsYou).To(ContainElement(And(HaveField("Kind", "Crew"),
		HaveField("Name", "linter"), HaveField("Why", ContainSubstring(crewRepo)), HaveField("Since", Not(BeNil())),
		HaveField("Recommend", "Change the Brief's spec.releases entry for linter to the Crew's target."))))
}

// Step 4 of docs/plans/done/brief-releases.md: a released app's Crew tags, prepares
// and publishes, and its rounds' issues go into release milestones.
var _ = Describe("Brief releases", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("makes a releasing Crew for a plan that releases its Container, filing round 1 in v0.1.0 and round 2 in v0.2.0", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)
		before := time.Now()
		planRound(ns, 1, 1, releasingLinterPlan(ns, 2))

		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		crew := linterCrew(Default, ns)
		Expect(crew.Spec.TagReleases).To(BeTrue())
		Expect(crew.Spec.PrepareReleases).To(BeTrue())
		Expect(crew.Spec.PublishReleases).To(BeNil(), "no spec.releases entry names it")
		Expect(roundIssueMilestones(Default, ns, 1)).To(Equal([]string{"v0.1.0", "v0.1.0"}))
		milestones := gitFake.AllMilestones(ns, "linter")
		Expect(milestones).To(HaveLen(1))
		Expect(model.HasReleaseMilestoneMarker(milestones[0].Description, "cel-lens", 1)).To(BeTrue())
		Expect(milestones[0].Due).To(BeTemporally("~", before.Add(model.ReleaseMilestoneDue), time.Minute))

		By("telling the planner the linter ships releases")
		Eventually(func(g Gomega) {
			g.Expect(string(gitFake.MainFiles(ns, briefRepo)[model.WorkspaceContainersFile].Data)).
				To(ContainSubstring("released: true"))
		}).Should(Succeed())

		By("filing round 2's issues in v0.2.0, though its plan doesn't name the linter again (R2)")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Lint VAP files too."} })).To(Succeed())
		planRound(ns, 2, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Round).To(Equal(int32(2)))
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(roundIssueMilestones(Default, ns, 2)).To(Equal([]string{"v0.2.0"}))
		Expect(gitFake.AllMilestones(ns, "linter")).To(HaveLen(2))
		Expect(linterCrew(Default, ns).Spec.TagReleases).To(BeTrue())
	})

	It("finds the round's release milestone again after a restart, making no second one", func() {
		createFactory(ns)
		briefAgents(ns, 3)

		By("finding the milestone of round 1 there already, titled as a restart left it")
		gitFake.AddMilestone(ns, "linter", "v0.1.0", model.ReleaseMilestoneDescription("cel-lens", 1),
			time.Now().Add(model.ReleaseMilestoneDue))
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, releasingLinterPlan(ns, 2))

		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(gitFake.AllMilestones(ns, "linter")).To(HaveLen(1))
		Expect(roundIssueMilestones(Default, ns, 1)).To(Equal([]string{"v0.1.0", "v0.1.0"}))
	})

	// #2025: an earlier round's v0.2.0 that nothing went into is the next
	// minor release, so the round's issue and the suggestion it takes go into
	// it, not into a v0.3.0 after it.
	It("files a round's issue and a suggestion it takes in an empty release milestone an earlier round made (#2025)", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)
		planRound(ns, 1, 1, releasingLinterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(roundIssueMilestones(Default, ns, 1)).To(Equal([]string{"v0.1.0"}))
		gitFake.AddMilestone(ns, "linter", "v0.2.0", model.ReleaseMilestoneDescription("cel-lens", 0),
			time.Now().Add(2*model.ReleaseMilestoneDue))

		crew := linterCrew(Default, ns)
		Expect(patch(crew, func() { crew.Spec.Paused = true })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(linterCrew(g, ns).Status.Phase).To(Equal(c4v1beta1.CrewPaused))
		}).Should(Succeed())
		safety := gitFake.AddIssue(ns, "linter", "Reject control characters", model.FollowUpIssue(model.FollowUp{
			Body: "A rule's text takes them.", Criteria: []string{"A rule with a control character is refused"}},
			"Agent `reviewer`", "http://gitea/pulls/2", 1, "linter-1"), []string{model.LabelProposed})
		gitFake.PutInMilestone(ns, "linter", safety, model.FollowUpMilestone)
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.SuggestionsWaiting).To(Equal(int32(1)))
		}).Should(Succeed())
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() {
			brief.Spec.Prompts = []string{"Lint VAP files too, and take the input-safety suggestion."}
		})).To(Succeed())
		plan := linterPlan(ns, 1)
		plan[model.PlanSuggestionsFile] = scaffold.File{Data: []byte(fmt.Sprintf(
			"- {container: linter, issue: %d, answer: take, why: The prompt takes it.}\n", safety))}
		planRound(ns, 2, 1, plan)
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Round).To(Equal(int32(2)))
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(roundIssueMilestones(Default, ns, 2)).To(Equal([]string{"v0.2.0"}))
		Expect(gitFake.Issue(ns, "linter", safety).InMilestone).To(Equal("v0.2.0"))
		milestones := gitFake.AllMilestones(ns, "linter")
		titles := make([]string, 0, len(milestones))
		for _, m := range milestones {
			titles = append(titles, m.Title)
			if m.Title == "v0.2.0" {
				Expect(model.HasReleaseMilestoneMarker(m.Description, "cel-lens", 2)).To(BeTrue())
			}
		}
		Expect(titles).To(ConsistOf("v0.1.0", model.FollowUpMilestone, "v0.2.0"), "no v0.3.0")
	})

	// #601: after the first release, a round of fixes is a patch release, and
	// a suggestion a prompt takes lands in the round's release; #884: one
	// with fix: true lands in the patch release, which later fixes join.
	It("files a round of fixes in v0.1.1, then a fix and a fix suggestion in it and a taken suggestion in v0.2.0", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)
		planRound(ns, 1, 1, releasingLinterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(roundIssueMilestones(Default, ns, 1)).To(Equal([]string{"v0.1.0"}))

		By("filing a round whose issues are all fixes in the next patch version")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Fix the crashes."} })).To(Succeed())
		planRound(ns, 2, 1, bugLinterPlan(ns, 2))
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Round).To(Equal(int32(2)))
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(roundIssueMilestones(Default, ns, 2)).To(Equal([]string{"v0.1.1", "v0.1.1"}))
		milestones := gitFake.AllMilestones(ns, "linter")
		Expect(milestones).To(HaveLen(2))
		Expect(milestones[1].Due).To(BeTemporally(">", milestones[0].Due), "lined up after v0.1.0")

		By("taking a reviewer's fix into v0.1.1 with the round's fix, and its other suggestion into v0.2.0")
		// Paused, the Crew doesn't start a Task on the taken suggestion,
		// which under load could fail and hand it back before the labels are
		// read (#845).
		crew := linterCrew(Default, ns)
		Expect(patch(crew, func() { crew.Spec.Paused = true })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(linterCrew(g, ns).Status.Phase).To(Equal(c4v1beta1.CrewPaused))
		}).Should(Succeed())
		safety := gitFake.AddIssue(ns, "linter", "Reject control characters", model.FollowUpIssue(model.FollowUp{
			Body: "A rule's text takes them.", Criteria: []string{"A rule with a control character is refused"}},
			"Agent `reviewer`", "http://gitea/pulls/2", 1, "linter-1"), []string{model.LabelProposed})
		gitFake.PutInMilestone(ns, "linter", safety, model.FollowUpMilestone)
		flaky := gitFake.AddIssue(ns, "linter", "Test the empty rule", model.FollowUpIssue(model.FollowUp{
			Body: "No test covers it.", Criteria: []string{"A test lints the empty rule"}},
			"Agent `reviewer`", "http://gitea/pulls/2", 1, "linter-1"), []string{model.LabelProposed})
		gitFake.PutInMilestone(ns, "linter", flaky, model.FollowUpMilestone)
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.SuggestionsWaiting).To(Equal(int32(2)))
		}).Should(Succeed())
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() {
			brief.Spec.Prompts = append(brief.Spec.Prompts, "Fix one more crash, and take the input-safety suggestion.")
		})).To(Succeed())
		plan := bugLinterPlan(ns, 1)
		plan[model.PlanSuggestionsFile] = scaffold.File{Data: []byte(fmt.Sprintf(
			"- {container: linter, issue: %d, answer: take, why: The prompt takes it.}\n"+
				"- {container: linter, issue: %d, answer: take, fix: true, why: It adds a test.}\n", safety, flaky))}
		planRound(ns, 3, 1, plan)
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Round).To(Equal(int32(3)))
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(roundIssueMilestones(Default, ns, 3)).To(Equal([]string{"v0.1.1"}))
		taken := gitFake.Issue(ns, "linter", safety)
		Expect(taken.Labels).To(ContainElement(model.LabelReady))
		Expect(taken.InMilestone).To(Equal("v0.2.0"))
		fix := gitFake.Issue(ns, "linter", flaky)
		Expect(fix.Labels).To(ContainElement(model.LabelReady))
		Expect(fix.InMilestone).To(Equal("v0.1.1"))
		Expect(gitFake.AllMilestones(ns, "linter")).To(HaveLen(4), "v0.1.0, v0.1.1, Later and v0.2.0")

		By("leaving no issue the Brief filed or took in no milestone")
		for _, issue := range getBrief(Default, ns).Status.Issues {
			Expect(gitFake.Issue(ns, "linter", int(issue.Number)).InMilestone).To(MatchRegexp(`^v\d+\.\d+\.\d+$`))
		}
	})

	// #633: a tending round files a released app's issues by the same rule
	// as a planned one (P5).
	It("files a tending round's fix on a released app in the next patch release", func() {
		createFactory(ns)
		briefAgents(ns, 1)
		brief := newBrief(ns, "cel-lens")
		brief.Spec.Autopilot = model.AutopilotOn
		mustCreate(brief)
		planRound(ns, 1, 1, releasingLinterPlan(ns, 1))
		// Round 1's linter Task fails at once, so the tending round 2 can
		// start in the second round 1 goes Planned (#671): wait on its
		// History entry, which holds after that.
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.History).NotTo(BeEmpty())
			g.Expect(b.Status.History[0].Outcome).To(Equal(model.ReasonApplied))
		}).Should(Succeed())
		Expect(roundIssueMilestones(Default, ns, 1)).To(Equal([]string{"v0.1.0"}))
		n := int(getBrief(Default, ns).Status.Issues[0].Number)
		failLinterTask(ns, model.IssueTaskName("linter", n, 1), n)

		By("filing the tending round's new fix in v0.1.1 and its feature in v0.2.0")
		awaitTendRound(ns, 2)
		plan := tendPlan(fmt.Sprintf(
			"- {container: linter, issue: %d, answer: close, why: The fix below replaces it.}\n", n))
		plan["plan/issues/01-linter.md"] = scaffold.File{
			Data: []byte(strings.Replace(linterIssue(2), "kind/feature", model.LabelBug, 1))}
		plan["plan/issues/02-linter.md"] = scaffold.File{Data: []byte(linterIssue(3))}
		planRound(ns, 2, 1, plan)
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.History).To(HaveLen(2))
			g.Expect(b.Status.History[1].Outcome).To(Equal(model.ReasonTended))
		}).Should(Succeed())
		Expect(roundIssueMilestones(Default, ns, 2)).To(Equal([]string{"v0.1.1", "v0.2.0"}))
		Expect(gitFake.AllMilestones(ns, "linter")).To(HaveLen(3), "v0.1.0, v0.1.1 and v0.2.0")
		Expect(milestoneDue(ns, "v0.1.1")).To(BeTemporally("<", milestoneDue(ns, "v0.2.0")))
	})

	It("finds a tending round's patch and release milestones again after a restart, making neither twice", func() {
		createFactory(ns)
		briefAgents(ns, 1)
		brief := newBrief(ns, "cel-lens")
		brief.Spec.Autopilot = model.AutopilotOn
		mustCreate(brief)
		planRound(ns, 1, 1, releasingLinterPlan(ns, 1))
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.History).NotTo(BeEmpty())
			g.Expect(b.Status.History[0].Outcome).To(Equal(model.ReasonApplied))
		}).Should(Succeed())
		n := int(getBrief(Default, ns).Status.Issues[0].Number)
		failLinterTask(ns, model.IssueTaskName("linter", n, 1), n)

		By("finding round 2's milestones there already, titled as no new round would title them")
		awaitTendRound(ns, 2)
		gitFake.AddMilestone(ns, "linter", "v0.1.7", model.PatchMilestoneDescription("cel-lens", 2),
			time.Now().Add(time.Hour))
		gitFake.AddMilestone(ns, "linter", "v0.9.0", model.ReleaseMilestoneDescription("cel-lens", 2),
			time.Now().Add(model.ReleaseMilestoneDue))
		plan := tendPlan(fmt.Sprintf(
			"- {container: linter, issue: %d, answer: close, why: The fix below replaces it.}\n", n))
		plan["plan/issues/01-linter.md"] = scaffold.File{
			Data: []byte(strings.Replace(linterIssue(2), "kind/feature", model.LabelBug, 1))}
		plan["plan/issues/02-linter.md"] = scaffold.File{Data: []byte(linterIssue(3))}
		planRound(ns, 2, 1, plan)
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.History).To(HaveLen(2))
			g.Expect(b.Status.History[1].Outcome).To(Equal(model.ReasonTended))
		}).Should(Succeed())
		Expect(roundIssueMilestones(Default, ns, 2)).To(Equal([]string{"v0.1.7", "v0.9.0"}))
		Expect(gitFake.AllMilestones(ns, "linter")).To(HaveLen(3), "v0.1.0, v0.1.7 and v0.9.0")
	})

	// #884: a mixed round files its fixes in the patch release right after
	// the release in progress, and its other work in the next minor release.
	It("files a mixed round's fix in a new v0.13.1 and its feature in v0.15.0", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		inProgress, planned := time.Now().Add(time.Hour), time.Now().Add(48*time.Hour)
		gitFake.AddMilestone(ns, "linter", "v0.13.0", "", inProgress)
		gitFake.AddMilestone(ns, "linter", "v0.14.0", "", planned)
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, mixedLinterPlan(ns))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(roundIssueMilestones(Default, ns, 1)).To(Equal([]string{"v0.13.1", "v0.15.0"}))
		Expect(gitFake.AllMilestones(ns, "linter")).To(HaveLen(4))
		Expect(milestoneDue(ns, "v0.13.1")).To(BeTemporally(">", inProgress))
		Expect(milestoneDue(ns, "v0.13.1")).To(BeTemporally("<", planned))
		Expect(milestoneDue(ns, "v0.15.0")).To(BeTemporally(">", planned))
	})

	It("files a round of only fixes in the open v0.13.1, making no milestone", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		gitFake.AddMilestone(ns, "linter", "v0.13.0", "", time.Now().Add(time.Hour))
		gitFake.AddMilestone(ns, "linter", "v0.13.1", "", time.Now().Add(2*time.Hour))
		gitFake.AddMilestone(ns, "linter", "v0.14.0", "", time.Now().Add(48*time.Hour))
		mustCreate(newBrief(ns, "cel-lens"))
		plan := bugLinterPlan(ns, 2)
		plan[model.PlanReleasesFile] = scaffold.File{Data: []byte("containers: [linter]\n")}
		planRound(ns, 1, 1, plan)
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(roundIssueMilestones(Default, ns, 1)).To(Equal([]string{"v0.13.1", "v0.13.1"}))
		Expect(gitFake.AllMilestones(ns, "linter")).To(HaveLen(3))
	})

	It("files a feature filed before a fix in v0.2.0, and the fix in a v0.1.1 due before it, after a closed v0.1.0", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		gitFake.AddMilestone(ns, "linter", "v0.1.0", "", time.Now().Add(-time.Hour))
		gitFake.MarkMilestoneClosed(ns, "linter", "v0.1.0")
		mustCreate(newBrief(ns, "cel-lens"))
		plan := releasingLinterPlan(ns, 2)
		plan["plan/issues/02-linter.md"] = scaffold.File{
			Data: []byte(strings.Replace(linterIssue(2), "kind/feature", model.LabelBug, 1))}
		planRound(ns, 1, 1, plan)
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(roundIssueMilestones(Default, ns, 1)).To(Equal([]string{"v0.2.0", "v0.1.1"}))
		Expect(gitFake.AllMilestones(ns, "linter")).To(HaveLen(3))
		Expect(milestoneDue(ns, "v0.1.1")).To(BeTemporally("<", milestoneDue(ns, "v0.2.0")))
	})

	It("files a mixed round's fix with its feature in v0.1.0 while the app has no release", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, mixedLinterPlan(ns))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(roundIssueMilestones(Default, ns, 1)).To(Equal([]string{"v0.1.0", "v0.1.0"}))
		Expect(gitFake.AllMilestones(ns, "linter")).To(HaveLen(1))
	})

	It("finds a mixed round's patch and release milestones again after a restart, making neither twice", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		gitFake.AddMilestone(ns, "linter", "v0.13.0", "", time.Now().Add(time.Hour))
		gitFake.AddMilestone(ns, "linter", "v0.14.0", "", time.Now().Add(48*time.Hour))
		By("finding round 1's milestones there already, titled as no new round would title them")
		gitFake.AddMilestone(ns, "linter", "v0.13.4", model.PatchMilestoneDescription("cel-lens", 1),
			time.Now().Add(2*time.Hour))
		gitFake.AddMilestone(ns, "linter", "v0.19.0", model.ReleaseMilestoneDescription("cel-lens", 1),
			time.Now().Add(72*time.Hour))
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, mixedLinterPlan(ns))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(roundIssueMilestones(Default, ns, 1)).To(Equal([]string{"v0.13.4", "v0.19.0"}))
		Expect(gitFake.AllMilestones(ns, "linter")).To(HaveLen(4))
	})

	It("puts a released app's ready work in no milestone in its first open release, before the round's (#599)", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)
		planRound(ns, 1, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(roundIssueMilestones(Default, ns, 1)).To(Equal([]string{""}), "filed before releases were on")

		By("pausing the Crew, so the issues keep their labels, and a person making v0.1.0")
		crew := linterCrew(Default, ns)
		Expect(patch(crew, func() { crew.Spec.Paused = true })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(linterCrew(g, ns).Status.Phase).To(Equal(c4v1beta1.CrewPaused))
		}).Should(Succeed())
		older := gitFake.AddIssue(ns, "linter", "Bundle the engine", "", []string{model.LabelReady})
		gitFake.AddMilestone(ns, "linter", "v0.1.0", "", time.Now().Add(time.Hour))
		proposed := gitFake.AddIssue(ns, "linter", "Follow-up", "", []string{model.LabelProposed})
		gitFake.PutInMilestone(ns, "linter", proposed, model.FollowUpMilestone)
		byHand := gitFake.AddIssue(ns, "linter", "Try it in an editor", "",
			[]string{model.LabelNeedsHuman, model.LabelByHand})
		gitFake.PutInMilestone(ns, "linter", byHand, model.FollowUpMilestone)
		handedBack := gitFake.AddIssue(ns, "linter", "Failed thrice", "", []string{model.LabelNeedsHuman})

		By("applying round 2, which releases the linter")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Ship it."} })).To(Succeed())
		planRound(ns, 2, 1, releasingLinterPlan(ns, 1))
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Round).To(Equal(int32(2)))
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(roundIssueMilestones(Default, ns, 2)).To(Equal([]string{"v0.2.0"}))
		Expect(gitFake.Issue(ns, "linter", older).InMilestone).To(Equal("v0.1.0"))
		Expect(gitFake.Issue(ns, "linter", proposed).InMilestone).To(Equal(model.FollowUpMilestone))
		Expect(gitFake.Issue(ns, "linter", byHand).InMilestone).To(Equal(model.FollowUpMilestone))
		Expect(gitFake.Issue(ns, "linter", handedBack).InMilestone).To(BeEmpty())
	})

	It("files no release milestone for a Container the round doesn't release (B11)", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(gitFake.AllMilestones(ns, "linter")).To(BeEmpty())
		Expect(roundIssueMilestones(Default, ns, 1)).To(Equal([]string{""}))
	})

	It("sets the Crew's publishReleases from a spec.releases entry, follows a change and keeps it on removal", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		brief := newBrief(ns, "cel-lens")
		brief.Spec.Releases = []c4v1beta1.BriefRelease{linterRelease("acme/cel-lens")}
		mustCreate(brief)

		By("listing the entry in brief.md for the planner")
		Eventually(func(g Gomega) { getTask(g, ns, "cel-lens-plan-1") }).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(string(gitFake.MainFiles(ns, briefRepo)[model.BriefFile].Data)).
				To(ContainSubstring("`linter`, published by the factory to github.com/acme/cel-lens"))
		}).Should(Succeed())

		By("making the linter's Crew release and publish, though the plan doesn't name it")
		planRound(ns, 1, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		crew := linterCrew(Default, ns)
		Expect(crew.Spec.TagReleases && crew.Spec.PrepareReleases).To(BeTrue())
		want := linterRelease("acme/cel-lens").CrewPublishReleases
		Expect(crew.Spec.PublishReleases).To(Equal(&want))
		Expect(roundIssueMilestones(Default, ns, 1)).To(Equal([]string{"v0.1.0"}))

		By("following a changed entry without a round")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() {
			brief.Spec.Releases[0].GitHub.Repository = "acme/lens"
			brief.Spec.Releases[0].TagPrefix = "linter-"
		})).To(Succeed())
		Eventually(func(g Gomega) {
			publish := linterCrew(g, ns).Spec.PublishReleases
			g.Expect(publish).NotTo(BeNil())
			g.Expect(publish.GitHub.Repository).To(Equal("acme/lens"))
			g.Expect(publish.TagPrefix).To(Equal("linter-"))
		}).Should(Succeed())

		By("leaving the Crew as it is once the entry is removed (R5)")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Releases = nil })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(string(gitFake.MainFiles(ns, briefRepo)[model.BriefFile].Data)).NotTo(ContainSubstring("## Releases"))
		}).Should(Succeed())
		crew = linterCrew(Default, ns)
		Expect(crew.Spec.TagReleases).To(BeTrue())
		Expect(crew.Spec.PublishReleases.GitHub.Repository).To(Equal("acme/lens"))
		Eventually(func(g Gomega) {
			crew := linterCrew(g, ns)
			g.Expect(crew.Annotations).NotTo(HaveKey(c4v1beta1.AnnotationBriefPublishReleases))
			g.Expect(condition(crew.Status.Conditions, c4v1beta1.ConditionReleaseTarget)).To(BeNil())
		}).Should(Succeed())
	})

	It("holds a Crew whose target was changed under it, until the Crew or the entry settles it (#991)", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		brief := newBrief(ns, "cel-lens")
		brief.Spec.Releases = []c4v1beta1.BriefRelease{linterRelease("acme/cel-lens")}
		mustCreate(brief)
		planRound(ns, 1, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())

		By("marking the Crew with the target the Brief set, and saying the Brief sets it")
		Eventually(func(g Gomega) { targetSetByBrief(g, ns, "acme/cel-lens") }).Should(Succeed())

		By("leaving a Crew changed under the Brief, held and flagged")
		setLinterTarget(ns, "acme/private-cel-lens")
		Eventually(func(g Gomega) { targetHeld(g, ns, "acme/private-cel-lens", "acme/cel-lens") }).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(linterCrew(g, ns).Spec.PublishReleases.GitHub.Repository).To(Equal("acme/private-cel-lens"))
		}, 2*time.Second).Should(Succeed())
		Expect(linterTargetEvents(Default, ns)).To(HaveLen(1))

		By("settling it by putting the Crew back")
		setLinterTarget(ns, "acme/cel-lens")
		Eventually(func(g Gomega) { targetSetByBrief(g, ns, "acme/cel-lens") }).Should(Succeed())

		By("settling it by changing the entry to the Crew's target")
		setLinterTarget(ns, "acme/private-cel-lens")
		Eventually(func(g Gomega) { targetHeld(g, ns, "acme/private-cel-lens", "acme/cel-lens") }).Should(Succeed())
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Releases[0].GitHub.Repository = "acme/private-cel-lens" })).
			To(Succeed())
		Eventually(func(g Gomega) { targetSetByBrief(g, ns, "acme/private-cel-lens") }).Should(Succeed())

		By("adopting a Crew from before the mark that publishes where the entry says")
		markLinter(ns, nil)
		Eventually(func(g Gomega) { targetSetByBrief(g, ns, "acme/private-cel-lens") }).Should(Succeed())

		By("holding a Crew from before the mark that publishes elsewhere")
		markLinter(ns, func(crew *c4v1beta1.Crew) { crew.Spec.PublishReleases.GitHub.Repository = "acme/other" })
		Eventually(func(g Gomega) { targetHeld(g, ns, "acme/other", "acme/private-cel-lens") }).Should(Succeed())
	})

	It("leaves alone a Crew the Brief didn't make", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(&c4v1beta1.Crew{ObjectMeta: objectMeta(ns, "linter"), Spec: c4v1beta1.CrewSpec{
			ContainerRef: c4v1beta1.LocalRef{Name: "linter"},
			Implementer:  c4v1beta1.LocalRef{Name: "implementer"}, Reviewer: c4v1beta1.LocalRef{Name: "reviewer"},
			Parallel: 2}})
		orig := linterCrew(Default, ns)
		brief := newBrief(ns, "cel-lens")
		brief.Spec.Releases = []c4v1beta1.BriefRelease{linterRelease("acme/cel-lens")}
		mustCreate(brief)
		planRound(ns, 1, 1, releasingLinterPlan(ns, 1))

		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		crew := linterCrew(Default, ns)
		Expect(crew.Spec).To(Equal(orig.Spec))
	})

	Describe("spec.releases", func() {
		It("takes 10 entries and no more", func() {
			brief := newBrief(ns, "ten")
			for i := range 10 {
				entry := linterRelease("acme/cel-lens")
				entry.Container = fmt.Sprintf("app-%d", i)
				brief.Spec.Releases = append(brief.Spec.Releases, entry)
			}
			mustCreate(brief)
			brief = newBrief(ns, "eleven")
			for i := range 11 {
				entry := linterRelease("acme/cel-lens")
				entry.Container = fmt.Sprintf("app-%d", i)
				brief.Spec.Releases = append(brief.Spec.Releases, entry)
			}
			Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring("spec.releases")))
		})

		It("names each Container once", func() {
			brief := newBrief(ns, "twice")
			brief.Spec.Releases = []c4v1beta1.BriefRelease{linterRelease("acme/a"), linterRelease("acme/b")}
			Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring("Duplicate value")))
		})

		It("names a Container by a DNS label", func() {
			brief := newBrief(ns, "label")
			entry := linterRelease("acme/cel-lens")
			entry.Container = "Linter_CLI"
			brief.Spec.Releases = []c4v1beta1.BriefRelease{entry}
			Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring("container must be a DNS label")))
		})

		It("names a repository as owner/name", func() {
			brief := newBrief(ns, "url")
			brief.Spec.Releases = []c4v1beta1.BriefRelease{linterRelease("https://github.com/acme/cel-lens")}
			Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring("repository must be owner/name")))
		})

		It("refuses a tag prefix with a space", func() {
			brief := newBrief(ns, "prefix")
			entry := linterRelease("acme/cel-lens")
			entry.TagPrefix = "linter v"
			brief.Spec.Releases = []c4v1beta1.BriefRelease{entry}
			Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring("spec.releases[0].tagPrefix")))
		})
	})

	// Step 1 of docs/plans/done/brief-requests.md: decision 1's rules.
	Describe("spec.releases[].requests", func() {
		requesting := func(repository string, requests c4v1beta1.BriefRequests) c4v1beta1.BriefRelease {
			entry := linterRelease(repository)
			entry.Requests = &requests
			return entry
		}

		It("is Off when unset, and takes Ask with authors and teams", func() {
			brief := newBrief(ns, "ask")
			brief.Spec.Releases = []c4v1beta1.BriefRelease{requesting("acme/cel-lens", c4v1beta1.BriefRequests{
				Mode: "Ask", Authors: []string{"ada", "a1"}, Teams: []string{"acme/maintainers"},
				Accepted: []int32{12}, Declined: []int32{13}})}
			mustCreate(brief)
			brief = newBrief(ns, "off")
			brief.Spec.Releases = []c4v1beta1.BriefRelease{requesting("acme/cel-lens", c4v1beta1.BriefRequests{})}
			mustCreate(brief)
			Expect(k8sClient.Get(ctx, key(ns, "off"), brief)).To(Succeed())
			Expect(brief.Spec.Releases[0].Requests.Mode).To(Equal("Off"))
		})

		It("needs authors or teams unless Off", func() {
			brief := newBrief(ns, "nobody")
			brief.Spec.Releases = []c4v1beta1.BriefRelease{requesting("acme/cel-lens",
				c4v1beta1.BriefRequests{Mode: "On"})}
			Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring(
				"requests need authors or teams unless mode is Off")))
		})

		It("takes requests in one entry per repository", func() {
			brief := newBrief(ns, "twice")
			other := requesting("Acme/Cel-Lens", c4v1beta1.BriefRequests{Mode: "Ask", Teams: []string{"acme/devs"}})
			other.Container = "cli"
			other.TagPrefix = "cli-"
			brief.Spec.Releases = []c4v1beta1.BriefRelease{
				requesting("acme/cel-lens", c4v1beta1.BriefRequests{Mode: "On", Authors: []string{"ada"}}), other}
			Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring(
				"at most one releases entry per GitHub repository may take requests")))
			other.Requests.Mode = "Off"
			brief.Spec.Releases[1] = other
			mustCreate(brief)
		})

		It("refuses a number both accepted and declined", func() {
			brief := newBrief(ns, "both")
			brief.Spec.Releases = []c4v1beta1.BriefRelease{requesting("acme/cel-lens", c4v1beta1.BriefRequests{
				Mode: "Ask", Authors: []string{"ada"}, Accepted: []int32{12, 14}, Declined: []int32{14}})}
			Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring(
				"an issue number can't be both accepted and declined")))
		})

		It("refuses a login GitHub wouldn't give", func() {
			for _, login := range []string{"-gray", "gray--lenovo", "gray_lenovo", strings.Repeat("a", 40)} {
				brief := newBrief(ns, "login")
				brief.Spec.Releases = []c4v1beta1.BriefRelease{requesting("acme/cel-lens", c4v1beta1.BriefRequests{
					Mode: "On", Authors: []string{login}})}
				Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring(
					"spec.releases[0].requests.authors[0]")), login)
			}
		})

		It("refuses a team that isn't org/slug", func() {
			for _, team := range []string{"maintainers", "acme/", "acme/main tainers", "https://github.com/orgs/acme/teams/x"} {
				brief := newBrief(ns, "team")
				brief.Spec.Releases = []c4v1beta1.BriefRelease{requesting("acme/cel-lens", c4v1beta1.BriefRequests{
					Mode: "On", Teams: []string{team}})}
				Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring(
					"spec.releases[0].requests.teams[0]")), team)
			}
		})
	})
})

// briefApp returns the line of container in Brief cel-lens's status.apps.
//
//nolint:unparam // names the container like the other helpers
func briefApp(g Gomega, ns, container string) c4v1beta1.BriefApp {
	for _, app := range getBrief(g, ns).Status.Apps {
		if app.Container == container {
			return app
		}
	}
	g.Expect(container).To(BeEmpty(), "Brief cel-lens has no app line for it")
	return c4v1beta1.BriefApp{}
}

// setLinterRelease sets the linter Crew's latest release as tagging it would.
func setLinterRelease(ns, tag string) {
	Eventually(func(g Gomega) {
		crew := linterCrew(g, ns)
		crew.Status.LastRelease = &c4v1beta1.CrewRelease{Tag: tag, Commit: "abc123", Time: metav1.Now(),
			Run: "Succeeded"}
		g.Expect(k8sClient.Status().Update(ctx, crew)).To(Succeed())
	}).Should(Succeed())
}

// Step 5 of docs/plans/done/brief-releases.md: the Brief's status shows each app's
// releases, from the Crew it made.
var _ = Describe("Brief apps", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("copies a Crew's latest release to the Brief, and says an entry's app isn't made", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		brief := newBrief(ns, "cel-lens")
		server := linterRelease("acme/cel-lens-server")
		server.Container = "server"
		brief.Spec.Releases = []c4v1beta1.BriefRelease{linterRelease("acme/cel-lens"), server}
		mustCreate(brief)
		planRound(ns, 1, 1, releasingLinterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())

		Eventually(func(g Gomega) {
			apps := getBrief(g, ns).Status.Apps
			g.Expect(apps).To(HaveLen(2))
			g.Expect(apps[0]).To(Equal(c4v1beta1.BriefApp{Container: "linter", Crew: "linter", Releases: "On",
				Parallel: model.BriefParallelDefault, PublishTo: "acme/cel-lens",
				Message: "Releases are on: the next one follows once the issues of its milestone are merged."}))
			g.Expect(apps[1]).To(Equal(c4v1beta1.BriefApp{Container: "server", Releases: "Off",
				Message: "The Brief hasn't made a Container named server: its releases start once a round makes it."}))
		}).Should(Succeed())

		By("showing the Crew's new release on the Brief")
		var repoURL string
		Eventually(func(g Gomega) {
			var c c4v1beta1.Container
			g.Expect(k8sClient.Get(ctx, key(ns, "linter"), &c)).To(Succeed())
			repoURL = c.Status.RepositoryURL
			g.Expect(repoURL).NotTo(BeEmpty())
		}).Should(Succeed())
		// A busy Gitea: the Crew's poll still publishes a release's module when
		// the spec moves on (#1202).
		gitFake.SlowModulePublishes(ns, "linter", time.Second)
		setLinterRelease(ns, "v0.1.0")
		Eventually(func(g Gomega) {
			release := briefApp(g, ns, "linter").LastRelease
			g.Expect(release).NotTo(BeNil())
			g.Expect(release.Tag).To(Equal("v0.1.0"))
			g.Expect(release.URL).To(Equal(repoURL + "/releases/tag/v0.1.0"))
			g.Expect(release.Time).NotTo(BeNil())
		}).Should(Succeed())
		// The spec tags the next release once the Crew has decided this one's
		// module: the poll still deciding it would patch it onto v0.2.0, where
		// a NotGo stays (#1202).
		Eventually(func(g Gomega) {
			g.Expect(linterCrew(g, ns).Status.LastRelease.GoModule).NotTo(BeNil())
		}).Should(Succeed())

		By("following the Crew's next release")
		// Its go.mod names a v2 module, which Go can't take at v0.2.0.
		gitFake.SetGoMod(ns, "linter", "github.com/acme/cel-lens/v2")
		setLinterRelease(ns, "v0.2.0")
		Eventually(func(g Gomega) {
			app := briefApp(g, ns, "linter")
			g.Expect(app.LastRelease).NotTo(BeNil())
			g.Expect(app.LastRelease.Tag).To(Equal("v0.2.0"))
			crew := linterCrew(g, ns).Status.LastRelease
			g.Expect(app.LastRelease.Run).To(Equal(crew.Run))
		}).Should(Succeed())

		By("carrying the module's state and message, so the Brief points at a module nobody can fetch (#1015)")
		Eventually(func(g Gomega) {
			module := linterCrew(g, ns).Status.LastRelease.GoModule
			g.Expect(module).NotTo(BeNil())
			g.Expect(module.State).To(Equal(model.ModuleStateInvalid))
			release := briefApp(g, ns, "linter").LastRelease
			g.Expect(release).NotTo(BeNil())
			g.Expect(release.ModuleState).To(Equal(model.ModuleStateInvalid))
			g.Expect(release.ModulePath).To(Equal("github.com/acme/cel-lens/v2"))
			g.Expect(release.ModuleMessage).To(Equal(module.Message))
			g.Expect(release.ModuleMessage).To(HavePrefix("Did not publish github.com/acme/cel-lens/v2 v0.2.0: "))
		}).Should(Succeed())
	})

	It("says how to turn releases on for an app whose Crew doesn't release", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Apps).To(Equal([]c4v1beta1.BriefApp{{Container: "linter",
				Crew: "linter", Parallel: model.BriefParallelDefault, Releases: "Off",
				Message: MessageReleasesOff}}))
		}).Should(Succeed())
		Expect(MessageReleasesOff).To(Equal(
			"Releases are off: ask for them in a prompt, or name this app in spec.releases."))
	})
})

var _ = DescribeTable("appMessage",
	func(app c4v1beta1.BriefApp, briefsCrew bool, want string) {
		Expect(appMessage(&app, briefsCrew)).To(Equal(want))
	},
	Entry("off on a Crew another owner made", c4v1beta1.BriefApp{Crew: "linter", Releases: "Off"}, false,
		"Releases are off, and they are for the owner of Crew linter to turn on: the Brief didn't make it."),
	Entry("a failed run", c4v1beta1.BriefApp{Releases: "On",
		LastRelease: &c4v1beta1.BriefAppRelease{Tag: "v0.1.0", Run: "Failed"}}, true,
		"Release v0.1.0 was tagged, but its CI run failed."),
	Entry("published", c4v1beta1.BriefApp{Releases: "On", PublishTo: "acme/lens",
		LastRelease: &c4v1beta1.BriefAppRelease{Tag: "v0.1.0", Run: "Succeeded", PublishState: "Published"}}, true,
		"Released v0.1.0 and published it to acme/lens."),
	Entry("publishing failed", c4v1beta1.BriefApp{Releases: "On", PublishTo: "acme/lens",
		LastRelease: &c4v1beta1.BriefAppRelease{Tag: "v0.1.0", PublishState: "Failed"}}, true,
		"Release v0.1.0 could not be published to acme/lens; the Crew says why and tries again."),
	Entry("a module Go can't fetch", c4v1beta1.BriefApp{Releases: "On", PublishTo: "acme/lens",
		LastRelease: &c4v1beta1.BriefAppRelease{Tag: "v0.1.0", PublishState: "Published", ModuleState: "NotFetchable",
			ModulePath: "github.com/acme/lens/engine"}}, true,
		"Released v0.1.0, but Go can't fetch its module github.com/acme/lens/engine; the Crew says why."),
	Entry("a module waiting on a failed publish says the publish", c4v1beta1.BriefApp{Releases: "On",
		PublishTo: "acme/lens", LastRelease: &c4v1beta1.BriefAppRelease{Tag: "v0.1.0", PublishState: "Failed",
			ModuleState: "Waiting"}}, true,
		"Release v0.1.0 could not be published to acme/lens; the Crew says why and tries again."),
	Entry("released, not published", c4v1beta1.BriefApp{Releases: "On",
		LastRelease: &c4v1beta1.BriefAppRelease{Tag: "v0.1.0", Run: "Running"}}, true, "Released v0.1.0."),
)
