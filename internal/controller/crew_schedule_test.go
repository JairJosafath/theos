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
	"errors"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

// scheduleMonday is a Monday at 06:00 UTC, when the specs' weekly schedule is
// due; the Crew's clock is moved around it.
var scheduleMonday = time.Date(2030, 1, 7, 6, 0, 0, 0, time.UTC)

// weeklySchedule is due every Monday at 06:00 UTC.
func weeklySchedule() c4v1beta1.CrewSchedule {
	return c4v1beta1.CrewSchedule{Name: "dependencies", Cron: "0 6 * * 1",
		Goal: "Update the dependencies with a new minor or patch version. One pull request."}
}

// scheduledIssues are orders-api's issues a schedule filed, open or closed.
func scheduledIssues(ns string) []fake.Issue {
	var out []fake.Issue
	for n := 1; n <= gitFake.IssueCount(ns, "orders-api"); n++ {
		if issue := gitFake.Issue(ns, "orders-api", n); slices.Contains(issue.FiledLabels, model.LabelSchedule) {
			out = append(out, issue)
		}
	}
	return out
}

// scheduleStatus is the Crew's status of its schedule name.
func scheduleStatus(g Gomega, ns, name string) c4v1beta1.CrewScheduleStatus {
	crew := getCrew(g, ns)
	i := slices.IndexFunc(crew.Status.Schedules, func(s c4v1beta1.CrewScheduleStatus) bool { return s.Name == name })
	g.Expect(i).To(BeNumerically(">=", 0), "status.schedules has no %s: %+v", name, crew.Status.Schedules)
	return crew.Status.Schedules[i]
}

var _ = Describe("Crew schedules (#63)", func() {
	It("files one issue when a schedule comes due, passes the next time over while it is open, and files again after", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		crewClockAt(scheduleMonday.Add(-time.Hour))
		crew := newCrew(ns)
		crew.Spec.Schedules = []c4v1beta1.CrewSchedule{weeklySchedule()}
		mustCreate(crew)

		By("waiting for Monday at six, saying when that is")
		Eventually(func(g Gomega) {
			st := scheduleStatus(g, ns, "dependencies")
			g.Expect(st.Next).NotTo(BeNil())
			g.Expect(st.Next.Time).To(BeTemporally("==", scheduleMonday))
			g.Expect(st.LastFiled).To(BeNil())
			crew := getCrew(g, ns)
			g.Expect(crew.Spec.Schedules[0].TimeZone).To(Equal("UTC"), "the CRD defaults the time zone")
			cond := condition(crew.Status.Conditions, ConditionScheduled)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal(model.ReasonScheduled))
			g.Expect(cond.Message).To(Equal("1 schedule, the next due at 2030-01-07T06:00:00Z"))
		}).Should(Succeed())
		Consistently(func() []fake.Issue { return scheduledIssues(ns) }, 2*time.Second).Should(BeEmpty(),
			"a schedule first seen files nothing before its time")

		By("filing the issue once the clock passes six, and working it as any ready issue")
		crewClockAt(scheduleMonday.Add(time.Minute))
		var first fake.Issue
		Eventually(func(g Gomega) {
			issues := scheduledIssues(ns)
			g.Expect(issues).To(HaveLen(1))
			first = issues[0]
			g.Expect(first.Title).To(Equal("dependencies, 2030-01-07"))
			g.Expect(first.FiledLabels).To(ConsistOf(model.LabelSchedule, model.LabelReady))
			g.Expect(first.Body).To(ContainSubstring("## Goal\n\nUpdate the dependencies with a new minor or patch version."))
			g.Expect(model.HasScheduleMarker(first.Body, "dependencies")).To(BeTrue())
			g.Expect(model.MissingAcceptanceCriteria(first.Body)).To(BeEmpty())
			st := scheduleStatus(g, ns, "dependencies")
			g.Expect(st.LastIssue).To(Equal(int32(first.Number)))
			g.Expect(st.LastFiled).NotTo(BeNil())
			g.Expect(st.Next.Time).To(BeTemporally("==", scheduleMonday.AddDate(0, 0, 7)))
			g.Expect(getCrew(g, ns).Status.ActiveTasks).To(HaveLen(1), "the Crew took the issue it filed")
		}).Should(Succeed())
		Consistently(func() []fake.Issue { return scheduledIssues(ns) }, 2*time.Second).Should(HaveLen(1),
			"one issue for one due time, however often the Crew polls")

		By("passing next Monday over while that issue is open")
		crewClockAt(scheduleMonday.AddDate(0, 0, 7).Add(time.Minute))
		Eventually(func(g Gomega) {
			st := scheduleStatus(g, ns, "dependencies")
			g.Expect(st.Since.Time).To(BeTemporally(">=", scheduleMonday.AddDate(0, 0, 7)))
			g.Expect(st.Next.Time).To(BeTemporally("==", scheduleMonday.AddDate(0, 0, 14)))
			g.Expect(st.LastIssue).To(Equal(int32(first.Number)))
		}).Should(Succeed())
		Expect(scheduledIssues(ns)).To(HaveLen(1), "a schedule never piles up")

		By("filing again the Monday after, once the first issue is closed")
		Expect(gitFake.CloseIssueByHand(ns, "orders-api", first.Number)).To(Succeed())
		crewClockAt(scheduleMonday.AddDate(0, 0, 14).Add(time.Minute))
		Eventually(func(g Gomega) {
			issues := scheduledIssues(ns)
			g.Expect(issues).To(HaveLen(2))
			g.Expect(issues[1].Title).To(Equal("dependencies, 2030-01-21"))
			g.Expect(scheduleStatus(g, ns, "dependencies").LastIssue).To(Equal(int32(issues[1].Number)))
		}).Should(Succeed())
	})

	It("files one issue, not one per time, for the times that passed while the Git host couldn't be read", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		crewClockAt(scheduleMonday.Add(-time.Hour))
		crew := newCrew(ns)
		crew.Spec.Schedules = []c4v1beta1.CrewSchedule{weeklySchedule()}
		mustCreate(crew)
		Eventually(func(g Gomega) { g.Expect(scheduleStatus(g, ns, "dependencies").Next).NotTo(BeNil()) }).Should(Succeed())

		gitFake.FailListIssues(ns, "orders-api", errors.New("the host is down"))
		crewClockAt(scheduleMonday.AddDate(0, 0, 15))
		Consistently(func() []fake.Issue { return scheduledIssues(ns) }, 2*time.Second).Should(BeEmpty())

		gitFake.FailListIssues(ns, "orders-api", nil)
		Eventually(func(g Gomega) {
			issues := scheduledIssues(ns)
			g.Expect(issues).To(HaveLen(1))
			g.Expect(issues[0].Title).To(Equal("dependencies, 2030-01-07"), "the first time that was missed")
			g.Expect(scheduleStatus(g, ns, "dependencies").Next.Time).To(BeTemporally("==", scheduleMonday.AddDate(0, 0, 21)))
		}).Should(Succeed())
		Consistently(func() []fake.Issue { return scheduledIssues(ns) }, 2*time.Second).Should(HaveLen(1),
			"three Mondays passed; one issue")
	})

	It("files nothing while the Crew is paused, and one issue once it resumes", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		crewClockAt(scheduleMonday.Add(-time.Hour))
		crew := newCrew(ns)
		crew.Spec.Schedules = []c4v1beta1.CrewSchedule{weeklySchedule()}
		crew.Spec.Paused = true
		mustCreate(crew)
		Eventually(func(g Gomega) {
			cond := condition(getCrew(g, ns).Status.Conditions, ConditionScheduled)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Message).To(ContainSubstring("the Crew is paused and files none"))
		}).Should(Succeed())

		crewClockAt(scheduleMonday.Add(time.Minute))
		Consistently(func() []fake.Issue { return scheduledIssues(ns) }, 2*time.Second).Should(BeEmpty())

		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			crew.Spec.Paused = false
			g.Expect(k8sClient.Update(ctx, crew)).To(Succeed())
		}).Should(Succeed())
		Eventually(func(g Gomega) { g.Expect(scheduledIssues(ns)).To(HaveLen(1)) }).Should(Succeed())
	})

	It("says which schedule it can't use, and goes on with the others and with its issues", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		crewClockAt(scheduleMonday.Add(-time.Hour))
		crew := newCrew(ns)
		never := c4v1beta1.CrewSchedule{Name: "never", Cron: "0 0 30 2 *", Goal: "Nothing."}
		nowhere := c4v1beta1.CrewSchedule{Name: "nowhere", Cron: "0 6 * * 1", TimeZone: "Mars/Olympus", Goal: "Nothing."}
		crew.Spec.Schedules = []c4v1beta1.CrewSchedule{never, weeklySchedule(), nowhere}
		mustCreate(crew)
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			cond := condition(crew.Status.Conditions, ConditionScheduled)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(model.ReasonInvalidSchedule))
			g.Expect(cond.Message).To(And(
				ContainSubstring(`schedule never: cron "0 0 30 2 *" names no day that exists`),
				ContainSubstring(`schedule nowhere: time zone "Mars/Olympus" is not one the operator can load`)))
			g.Expect(scheduleStatus(g, ns, "never").Next).To(BeNil())
			g.Expect(scheduleStatus(g, ns, "dependencies").Next).NotTo(BeNil())
			ready := condition(crew.Status.Conditions, ConditionReady)
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionTrue), "a schedule it can't use doesn't stop the Crew")
		}).Should(Succeed())

		crewClockAt(scheduleMonday.Add(time.Minute))
		Eventually(func(g Gomega) {
			issues := scheduledIssues(ns)
			g.Expect(issues).To(HaveLen(1))
			g.Expect(issues[0].Title).To(Equal("dependencies, 2030-01-07"))
		}).Should(Succeed())

		By("dropping the status and the condition with the schedules")
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			crew.Spec.Schedules = nil
			g.Expect(k8sClient.Update(ctx, crew)).To(Succeed())
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Status.Schedules).To(BeEmpty())
			g.Expect(condition(crew.Status.Conditions, ConditionScheduled)).To(BeNil())
		}).Should(Succeed())
	})

	It("refuses a schedule the CRD can tell is wrong", func() {
		ns := newProjectNamespace()
		for why, change := range map[string]func(*c4v1beta1.CrewSchedule){
			"a cron with names":       func(s *c4v1beta1.CrewSchedule) { s.Cron = "0 6 * * MON" },
			"a cron of four fields":   func(s *c4v1beta1.CrewSchedule) { s.Cron = "0 6 * *" },
			"a cron macro":            func(s *c4v1beta1.CrewSchedule) { s.Cron = "@daily" },
			"no goal":                 func(s *c4v1beta1.CrewSchedule) { s.Goal = "" },
			"a name that is no label": func(s *c4v1beta1.CrewSchedule) { s.Name = "Weekly Update" },
		} {
			crew := newCrew(ns)
			s := weeklySchedule()
			change(&s)
			crew.Spec.Schedules = []c4v1beta1.CrewSchedule{s}
			Expect(k8sClient.Create(ctx, crew)).NotTo(Succeed(), why)
		}
		crew := newCrew(ns)
		crew.Spec.Schedules = []c4v1beta1.CrewSchedule{weeklySchedule(), weeklySchedule()}
		Expect(k8sClient.Create(ctx, crew)).To(MatchError(ContainSubstring("Duplicate value")), "two schedules of one name")
	})
})

// askToRun asks the Crew of orders-api to run its schedule name now.
func askToRun(ns, name string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		crew := getCrew(g, ns)
		if crew.Annotations == nil {
			crew.Annotations = map[string]string{}
		}
		crew.Annotations[c4v1beta1.AnnotationRunSchedule] = name
		g.Expect(k8sClient.Update(ctx, crew)).To(Succeed())
	}).Should(Succeed())
}

// A schedule runs now when a person asks (#72): the Crew's
// theos.factory.io/run-schedule annotation names it. The operator files its
// issue and takes the annotation off; an open issue of the schedule, or a
// paused Crew, holds the run as it holds a due one, and the Scheduled
// condition says why.
var _ = Describe("Crew schedules, run now (#72)", func() {
	// saturday is two days before the weekly schedule is next due.
	saturday := scheduleMonday.Add(-48 * time.Hour)
	// scheduled makes orders-api Ready and its Crew, with the weekly schedule,
	// on a Saturday, and waits until the Crew knows the schedule.
	scheduled := func(mutate func(*c4v1beta1.Crew)) string {
		GinkgoHelper()
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		crewClockAt(saturday)
		crew := newCrew(ns)
		crew.Spec.Schedules = []c4v1beta1.CrewSchedule{weeklySchedule()}
		if mutate != nil {
			mutate(crew)
		}
		mustCreate(crew)
		Eventually(func(g Gomega) { g.Expect(scheduleStatus(g, ns, "dependencies").Next).NotTo(BeNil()) }).Should(Succeed())
		Expect(scheduledIssues(ns)).To(BeEmpty(), "nothing is due on a Saturday")
		return ns
	}
	scheduledMessage := func(g Gomega, ns string) string {
		cond := condition(getCrew(g, ns).Status.Conditions, ConditionScheduled)
		g.Expect(cond).NotTo(BeNil())
		return cond.Message
	}

	It("files the schedule's issue at once, takes the request off, and holds a second one while that issue is open", func() {
		ns := scheduled(nil)

		By("filing it now, dated today, with its next time as it was")
		askToRun(ns, "dependencies")
		var first fake.Issue
		Eventually(func(g Gomega) {
			issues := scheduledIssues(ns)
			g.Expect(issues).To(HaveLen(1))
			first = issues[0]
			g.Expect(first.Title).To(Equal("dependencies, 2030-01-05"))
			g.Expect(first.FiledLabels).To(ConsistOf(model.LabelSchedule, model.LabelReady))
			g.Expect(getCrew(g, ns).Annotations).NotTo(HaveKey(c4v1beta1.AnnotationRunSchedule))
			st := scheduleStatus(g, ns, "dependencies")
			g.Expect(st.LastIssue).To(Equal(int32(first.Number)))
			g.Expect(st.Next.Time).To(BeTemporally("==", scheduleMonday))
			g.Expect(scheduledMessage(g, ns)).To(Equal("1 schedule, the next due at 2030-01-07T06:00:00Z"))
		}).Should(Succeed())
		Consistently(func() []fake.Issue { return scheduledIssues(ns) }, 2*time.Second).Should(HaveLen(1),
			"one request, one issue")

		By("holding a second request while that issue is open, and saying why")
		askToRun(ns, "dependencies")
		Eventually(func(g Gomega) {
			g.Expect(scheduledMessage(g, ns)).To(HaveSuffix(
				"; schedule dependencies is asked to run now, and waits for its open issue to close"))
			g.Expect(condition(getCrew(g, ns).Status.Conditions, ConditionScheduled).Status).To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(scheduledIssues(ns)).To(HaveLen(1))
			g.Expect(getCrew(g, ns).Annotations).To(HaveKeyWithValue(c4v1beta1.AnnotationRunSchedule, "dependencies"))
		}, 2*time.Second).Should(Succeed())

		By("running it once the issue closed")
		Expect(gitFake.CloseIssueByHand(ns, "orders-api", first.Number)).To(Succeed())
		crewClockAt(saturday.Add(time.Hour))
		Eventually(func(g Gomega) {
			issues := scheduledIssues(ns)
			g.Expect(issues).To(HaveLen(2))
			g.Expect(scheduleStatus(g, ns, "dependencies").LastIssue).To(Equal(int32(issues[1].Number)))
			g.Expect(getCrew(g, ns).Annotations).NotTo(HaveKey(c4v1beta1.AnnotationRunSchedule))
		}).Should(Succeed())
	})

	It("runs it when a paused Crew resumes", func() {
		ns := scheduled(func(crew *c4v1beta1.Crew) { crew.Spec.Paused = true })
		askToRun(ns, "dependencies")
		Eventually(func(g Gomega) {
			g.Expect(scheduledMessage(g, ns)).To(Equal("1 schedule, the next due at 2030-01-07T06:00:00Z; the Crew is " +
				"paused and files none; schedule dependencies is asked to run now, and runs when the Crew resumes"))
		}).Should(Succeed())
		Consistently(func() []fake.Issue { return scheduledIssues(ns) }, 2*time.Second).Should(BeEmpty())

		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			crew.Spec.Paused = false
			g.Expect(k8sClient.Update(ctx, crew)).To(Succeed())
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(scheduledIssues(ns)).To(HaveLen(1))
			g.Expect(getCrew(g, ns).Annotations).NotTo(HaveKey(c4v1beta1.AnnotationRunSchedule))
		}).Should(Succeed())
	})

	It("says so when asked to run a schedule the Crew doesn't have, and files nothing", func() {
		ns := scheduled(nil)
		askToRun(ns, "dependecies")
		Eventually(func(g Gomega) {
			cond := condition(getCrew(g, ns).Status.Conditions, ConditionScheduled)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(model.ReasonUnknownSchedule))
			g.Expect(cond.Message).To(Equal(`asked to run schedule "dependecies" now, which is none of the Crew's: ` +
				"the Crew's schedules are dependencies"))
		}).Should(Succeed())
		Consistently(func() []fake.Issue { return scheduledIssues(ns) }, 2*time.Second).Should(BeEmpty())

		By("going back to Scheduled once the request names one it has")
		askToRun(ns, "dependencies")
		Eventually(func(g Gomega) {
			g.Expect(scheduledIssues(ns)).To(HaveLen(1))
			g.Expect(scheduledMessage(g, ns)).To(Equal("1 schedule, the next due at 2030-01-07T06:00:00Z"))
		}).Should(Succeed())
	})
})
