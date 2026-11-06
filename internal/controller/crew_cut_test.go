/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"slices"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// cutDay is today's date at 18:00 UTC, a cut of every: Day.
func cutDay() time.Time {
	return time.Now().UTC().Truncate(24 * time.Hour).Add(18 * time.Hour)
}

// crewClockAt moves the Crew's clock to at, from now on until the spec ends.
func crewClockAt(at time.Time) {
	skewCrewClock(time.Until(at))
}

// cadenceCrew creates orders-api's Crew, tagging releases on cadence.
func cadenceCrew(ns string, cadence c4v1beta1.ReleaseCadence, prepare bool) {
	GinkgoHelper()
	crew := newCrew(ns)
	crew.Spec.TagReleases, crew.Spec.PrepareReleases = true, prepare
	crew.Spec.ReleaseCadence = &cadence
	mustCreate(crew)
}

// releaseIn makes orders-api's milestone title, due at due, with description.
func releaseIn(ns, title, description string, due time.Time) {
	gitFake.AddMilestone(ns, "orders-api", title, description, due)
}

// issueIn files an issue with labels in milestone title of orders-api.
func issueIn(ns, title, release string, labels ...string) int {
	GinkgoHelper()
	n := gitFake.AddIssue(ns, "orders-api", title, clearIssue, labels)
	gitFake.PutInMilestone(ns, "orders-api", n, release)
	return n
}

// mergedIn files a closed issue with labels in v0.15.0, merged at commit by a
// Task.
func mergedIn(ns, commit string, labels ...string) {
	GinkgoHelper()
	n := issueIn(ns, "Merged "+commit, "v0.15.0", labels...)
	Expect(gitFake.CloseIssueByHand(ns, "orders-api", n)).To(Succeed())
	mergedTask(ns, "orders-api-"+commit, n, commit, time.Now())
}

// milestonesTitled are orders-api's milestones titled title, open or closed.
func milestonesTitled(ns, title string) []ports.Milestone {
	var out []ports.Milestone
	for _, m := range gitFake.AllMilestones(ns, "orders-api") {
		if m.Title == title {
			out = append(out, m)
		}
	}
	return out
}

// waitForLastCut waits until the Crew went on its cadence.
func waitForLastCut(ns string) {
	GinkgoHelper()
	Eventually(func(g Gomega) { g.Expect(getCrew(g, ns).Status.LastCut).NotTo(BeNil()) }).Should(Succeed())
}

var _ = Describe("Crew release cadence", func() {
	It("holds a done release until its cut, then carries its open issue into a new release and tags it", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		cut := cutDay()
		releaseIn(ns, "v0.15.0", "", cut)
		mergedIn(ns, "5a-merge")
		open := issueIn(ns, "Paginate orders", "v0.15.0")
		crewClockAt(cut.Add(-time.Hour))
		cadenceCrew(ns, c4v1beta1.ReleaseCadence{Every: model.CadenceDay}, false)

		By("waiting for 18:00, saying what the release holds")
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Spec.ReleaseCadence.At).To(Equal("18:00"), "the CRD defaults at")
			g.Expect(crew.Spec.ReleaseCadence.TimeZone).To(Equal("UTC"), "and the time zone")
			cond := condition(crew.Status.Conditions, ConditionReleased)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(model.ReleaseWaitingForCut))
			g.Expect(cond.Message).To(Equal("v0.15.0 holds 1 merged issue and goes out at 18:00 UTC"))
			g.Expect(crew.Status.NextCut).NotTo(BeNil())
			g.Expect(crew.Status.NextCut.Time.Time).To(BeTemporally("==", cut))
			g.Expect(crew.Status.NextCut.Release).To(Equal("v0.15.0"))
			g.Expect(crew.Status.NextCut.Merged).To(Equal(int32(1)))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.15.0")).To(BeEmpty())
			g.Expect(crewEvents(g, ns)).NotTo(ContainElement(HaveField("Type", "Warning")), "a wait for the cut is no warning")
		}, 2*time.Second).Should(Succeed())

		By("cutting it once the clock passes 18:00")
		crewClockAt(cut.Add(time.Minute))
		Eventually(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.15.0")).To(Equal("5a-merge"))
			g.Expect(gitFake.MilestoneClosed(ns, "orders-api", "v0.15.0")).To(BeTrue())
		}).Should(Succeed())
		carried := milestonesTitled(ns, "v0.16.0")
		Expect(carried).To(HaveLen(1))
		Expect(carried[0].Closed).To(BeFalse())
		Expect(carried[0].Description).To(ContainSubstring(model.CarryMarker("orders-api", "v0.15.0")))
		Expect(carried[0].Due).To(BeTemporally("==", cut.AddDate(0, 0, 1)), "due at the next cut")
		issue := gitFake.Issue(ns, "orders-api", open)
		Expect(issue.InMilestone).To(Equal("v0.16.0"))
		Expect(issue.Comments).To(ContainElement(HavePrefix("Carried from v0.15.0 to v0.16.0")))
		Expect(milestonesTitled(ns, "v0.15.0")[0].Description).To(ContainSubstring("/cut "))
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Status.LastRelease).NotTo(BeNil())
			g.Expect(crew.Status.LastRelease.Tag).To(Equal("v0.15.0"))
			g.Expect(crew.Status.LastCut.Time).To(BeTemporally(">=", cut))
			g.Expect(crew.Status.NextCut).NotTo(BeNil())
			g.Expect(crew.Status.NextCut.Time.Time).To(BeTemporally("==", cut.AddDate(0, 0, 1)))
			g.Expect(crew.Status.NextCut.Release).To(Equal("v0.16.0"))
			g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Normal"),
				HaveField("Reason", reasonReleaseCut), HaveField("Note", "Cut v0.15.0; carried #"+
					strconv.Itoa(open)+" into v0.16.0"))))
		}).Should(Succeed())
	})

	It("finishes a cut a restart broke off, and makes no second release to carry into", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		cut := cutDay()
		releaseIn(ns, "v0.15.0", "The day's work.\n\n"+model.CutMarker("orders-api", cut.Add(30*time.Second)), cut)
		mergedIn(ns, "5a-merge")
		open := issueIn(ns, "Paginate orders", "v0.15.0")
		releaseIn(ns, "v0.16.0", model.CarryMilestoneDescription("orders-api", "v0.15.0"), cut.AddDate(0, 0, 1))
		crewClockAt(cut.Add(5 * time.Minute))
		cadenceCrew(ns, c4v1beta1.ReleaseCadence{Every: model.CadenceDay}, false)

		Eventually(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.15.0")).To(Equal("5a-merge"))
		}).Should(Succeed())
		Expect(gitFake.Issue(ns, "orders-api", open).InMilestone).To(Equal("v0.16.0"))
		Expect(milestonesTitled(ns, "v0.16.0")).To(HaveLen(1))
		Expect(milestonesTitled(ns, "v0.17.0")).To(BeEmpty())
		Consistently(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Status.NextCut).NotTo(BeNil())
			g.Expect(crew.Status.NextCut.Time.Time).To(BeTemporally("==", cut.AddDate(0, 0, 1)),
				"the cut was made: the next is tomorrow's")
			g.Expect(gitFake.Issue(ns, "orders-api", open).InMilestone).To(Equal("v0.16.0"))
		}, 2*time.Second).Should(Succeed())
	})

	It("cuts at once for a merged release/now issue, whatever the time", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		// A weekly cut three days from now is far off.
		weekday := time.Now().UTC().AddDate(0, 0, 3).Weekday().String()
		releaseIn(ns, "v0.15.0", "", time.Now().AddDate(0, 0, 3))
		mergedIn(ns, "5a-crash", "kind/bug", model.LabelReleaseNow)
		cadenceCrew(ns, c4v1beta1.ReleaseCadence{Every: model.CadenceWeek, Weekday: weekday}, false)

		Eventually(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.15.0")).To(Equal("5a-crash"))
			g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Reason", reasonReleaseCut),
				HaveField("Note", "Cut v0.15.0 at once for its merged release/now work"))))
		}).Should(Succeed())
		another := gitFake.AddIssue(ns, "orders-api", "Another crash", clearIssue, nil)
		Expect(gitFake.LabelIssue(ns, "orders-api", another, model.LabelReleaseNow)).To(Succeed(),
			"the Crew keeps the release/now label in its repository")
	})

	It("tags a day of fixes as the next patch after the last release", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		cut := cutDay()
		releaseIn(ns, "v0.14.0", "", cut.AddDate(0, 0, -7))
		gitFake.MarkMilestoneClosed(ns, "orders-api", "v0.14.0")
		gitFake.SetTag(ns, "orders-api", "v0.14.0", "5a-old")
		releaseIn(ns, "v0.15.0", "", cut)
		mergedIn(ns, "5a-fix", "kind/bug")
		mergedIn(ns, "5a-test", "kind/test")
		crewClockAt(cut.Add(-time.Hour))
		cadenceCrew(ns, c4v1beta1.ReleaseCadence{Every: model.CadenceDay}, false)
		waitForLastCut(ns)

		crewClockAt(cut.Add(time.Minute))
		Eventually(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.14.1")).To(Equal("5a-test"))
			g.Expect(gitFake.MilestoneClosed(ns, "orders-api", "v0.14.1")).To(BeTrue())
		}).Should(Succeed())
		Expect(gitFake.Tag(ns, "orders-api", "v0.15.0")).To(BeEmpty())
		Expect(milestonesTitled(ns, "v0.15.0")).To(BeEmpty(), "the release took the patch title")
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastRelease.Tag).To(Equal("v0.14.1"))
		}).Should(Succeed())
	})

	It("holds a Task ready to merge while the cut release is prepared, then merges it", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 3)
		cut := cutDay()
		// v0.15.0 was cut; its prepare issue is at work (here, by hand).
		releaseIn(ns, "v0.15.0", model.CutMarker("orders-api", cut.Add(30*time.Second)), cut)
		mergedIn(ns, "5a-merge")
		prepare := issueIn(ns, "Prepare v0.15.0", "v0.15.0", model.LabelRelease)
		releaseIn(ns, "v0.16.0", "", cut.AddDate(0, 0, 1))
		next := issueIn(ns, "Paginate orders", "v0.16.0")
		crewClockAt(cut.Add(time.Hour))
		cadenceCrew(ns, c4v1beta1.ReleaseCadence{Every: model.CadenceDay}, true)
		Eventually(func(g Gomega) { g.Expect(getCrew(g, ns).Status.CutRelease).To(Equal("v0.15.0")) }).Should(Succeed())

		By("waiting at its merge, costing no round and taking no place")
		task := newTask(ns, "ship-next", "")
		task.Labels = map[string]string{LabelCrew: "orders-api"}
		task.Spec.Issue = int32(next)
		task.Spec.ReviewerRef = &c4v1beta1.LocalRef{Name: "reviewer"}
		mustCreate(task)
		Eventually(func(g Gomega) { sandboxExists(g, ns, "ship-next-r1-implement") }).Should(Succeed())
		head, err := gitFake.PushBranch(ctx, ports.Repository{Owner: ns, Name: "orders-api"},
			gitFake.MainHead(ns, "orders-api").Hash, "task/ship-next",
			scaffold.Tree{"pages.go": {Data: []byte("package pages\n")}}, nil, "", ports.Signature{})
		Expect(err).NotTo(HaveOccurred())
		finishSandbox(ns, "ship-next-r1-implement", "ship-next",
			&agentrunner.Result{Role: "implement", Commit: head, Summary: "Paginated."}, "")
		gitFake.SetChecks(ns, "orders-api", head, "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "ship-next-r1-review-a1") }).Should(Succeed())
		finishSandbox(ns, "ship-next-r1-review-a1", "ship-next",
			&agentrunner.Result{Role: "review", Verdict: "approve", Summary: "Good."}, "")
		Eventually(func(g Gomega) {
			cond := condition(getTask(g, ns, "ship-next").Status.Conditions, ConditionProgressing)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal(model.ReasonWaitingForRelease))
			g.Expect(cond.Message).To(ContainSubstring("merging once v0.15.0"))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			t := getTask(g, ns, "ship-next")
			g.Expect(t.Status.Phase).NotTo(Equal(c4v1beta1.TaskMerged))
			g.Expect(t.Status.Round).To(Equal(int32(1)))
		}, 2*time.Second).Should(Succeed())

		By("merging once the prepare issue merged and the release is tagged")
		// Recorded before its issue closes: a prepare issue closed by hand
		// would be a person's call to tag without it.
		mergedTask(ns, "orders-api-prepare", prepare, "5a-prepare", time.Now())
		Expect(gitFake.CloseIssueByHand(ns, "orders-api", prepare)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.15.0")).To(Equal("5a-prepare"))
			g.Expect(getCrew(g, ns).Status.CutRelease).To(BeEmpty())
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "ship-next").Status.Phase).To(Equal(c4v1beta1.TaskMerged))
		}, 30*time.Second).Should(Succeed())
		Expect(gitFake.Issue(ns, "orders-api", next).InMilestone).To(Equal("v0.16.0"),
			"merged after the tag, it stays in the next release")
	})

	It("stops at a time zone the operator can't load, and tags nothing", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		cadenceCrew(ns, c4v1beta1.ReleaseCadence{Every: model.CadenceDay, TimeZone: "Mars/Olympus"}, false)

		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			cond := condition(crew.Status.Conditions, ConditionReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(model.ReasonInvalidTimeZone))
			g.Expect(cond.Message).To(ContainSubstring(`time zone "Mars/Olympus"`))
			g.Expect(crew.Status.Phase).To(Equal(c4v1beta1.CrewWaiting))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(BeEmpty())
		}, 2*time.Second).Should(Succeed())
	})

	It("tags each release once it is done with every: Release, with no cut", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		cadenceCrew(ns, c4v1beta1.ReleaseCadence{Every: model.CadenceRelease}, false)

		Eventually(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(Equal("5a-merge"))
			crew := getCrew(g, ns)
			g.Expect(crew.Status.NextCut).To(BeNil())
			g.Expect(crew.Status.LastCut).To(BeNil())
		}).Should(Succeed())
		Expect(slices.ContainsFunc(gitFake.AllMilestones(ns, "orders-api"), func(m ports.Milestone) bool {
			return strings.Contains(m.Description, "theos-crew:")
		})).To(BeFalse(), "nothing is marked cut")
	})
})
