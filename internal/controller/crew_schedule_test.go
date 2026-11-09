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
