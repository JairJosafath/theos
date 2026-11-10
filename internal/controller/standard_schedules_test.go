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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

// A Standard proposes schedules to the Crews of its Containers (#72,
// docs/plans/crew-schedules.md, step 5): spec.schedules, each in the shape of
// a Crew's schedule. A proposal turns nothing on: the operator copies none
// into a Crew, and a Crew files an issue only for a schedule of its own.
var _ = Describe("Standard schedules (#72)", func() {
	It("keeps a Standard's proposals and files nothing for them, until the Crew has the schedule itself", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		standard := newStandard(ns, "python-strict", "python-fastapi")
		standard.Spec.Schedules = []c4v1beta1.CrewSchedule{weeklySchedule()}
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		container.Spec.StandardRef = &c4v1beta1.LocalRef{Name: "python-strict"}
		mustCreate(newSystem(ns, "webshop-core"), standard, container)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), container)).To(Succeed())
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
		}).Should(Succeed())

		By("storing the proposal as a Crew's schedule is stored, the time zone defaulted")
		Expect(k8sClient.Get(ctx, key(ns, "python-strict"), standard)).To(Succeed())
		want := weeklySchedule()
		want.TimeZone = "UTC"
		Expect(standard.Spec.Schedules).To(Equal([]c4v1beta1.CrewSchedule{want}))

		By("leaving the Crew of its Container without the schedule, past the proposal's time")
		crewAgents(ns, 1)
		crewClockAt(scheduleMonday.Add(-time.Hour))
		mustCreate(newCrew(ns))
		crewClockAt(scheduleMonday.Add(time.Minute))
		// The ready issue shows the Crew polled after the proposal's time.
		gitFake.AddIssue(ns, "orders-api", "Validate orders", clearIssue, []string{model.LabelReady})
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.ActiveTasks).To(HaveLen(1))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Spec.Schedules).To(BeEmpty(), "the operator copies no proposal into the Crew")
			g.Expect(crew.Status.Schedules).To(BeEmpty())
			g.Expect(condition(crew.Status.Conditions, ConditionScheduled)).To(BeNil())
			g.Expect(scheduledIssues(ns)).To(BeEmpty(), "a proposal files no issue")
		}, 2*time.Second).Should(Succeed())

		By("filing the issue at its next time once the Crew takes the proposal as it is written")
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			crew.Spec.Schedules = standard.Spec.Schedules
			g.Expect(k8sClient.Update(ctx, crew)).To(Succeed())
		}).Should(Succeed())
		nextMonday := scheduleMonday.AddDate(0, 0, 7)
		Eventually(func(g Gomega) {
			st := scheduleStatus(g, ns, "dependencies")
			g.Expect(st.Next).NotTo(BeNil())
			g.Expect(st.Next.Time).To(BeTemporally("==", nextMonday), "a schedule taken after a time waits for the next")
		}).Should(Succeed())
		crewClockAt(nextMonday.Add(time.Minute))
		var filed []fake.Issue
		Eventually(func(g Gomega) {
			filed = scheduledIssues(ns)
			g.Expect(filed).To(HaveLen(1))
		}).Should(Succeed())
		Expect(filed[0].Title).To(Equal("dependencies, 2030-01-14"))
		Expect(filed[0].Body).To(ContainSubstring("## Goal\n\n" + want.Goal))
	})

	It("refuses a proposal the CRD can tell is wrong, as it refuses a Crew's schedule", func() {
		ns := newProjectNamespace()
		for why, c := range map[string]struct {
			change func(*c4v1beta1.CrewSchedule)
			want   string
		}{
			"a cron with names":       {func(s *c4v1beta1.CrewSchedule) { s.Cron = "0 6 * * MON" }, "spec.schedules[0].cron in body should match"},
			"a cron of four fields":   {func(s *c4v1beta1.CrewSchedule) { s.Cron = "0 6 * *" }, "spec.schedules[0].cron in body should match"},
			"a cron macro":            {func(s *c4v1beta1.CrewSchedule) { s.Cron = "@daily" }, "spec.schedules[0].cron in body should match"},
			"no goal":                 {func(s *c4v1beta1.CrewSchedule) { s.Goal = "" }, "spec.schedules[0].goal in body should be at least 1 chars long"},
			"a name that is no label": {func(s *c4v1beta1.CrewSchedule) { s.Name = "Weekly Update" }, "spec.schedules[0].name in body should match"},
		} {
			standard := newStandard(ns, "python-strict", "python-fastapi")
			s := weeklySchedule()
			c.change(&s)
			standard.Spec.Schedules = []c4v1beta1.CrewSchedule{s}
			Expect(k8sClient.Create(ctx, standard)).To(MatchError(ContainSubstring(c.want)), why)
		}

		// A time zone of no characters, and a proposal with no goal at all:
		// the typed client leaves an empty one out, so these go as written.
		for why, c := range map[string]struct {
			schedule map[string]any
			want     string
		}{
			"an empty time zone": {map[string]any{"name": "dependencies", "cron": "0 6 * * 1", "timeZone": "", "goal": "Update the dependencies."},
				"spec.schedules[0].timeZone in body should be at least 1 chars long"},
			"a proposal without a goal": {map[string]any{"name": "dependencies", "cron": "0 6 * * 1"},
				"spec.schedules[0].goal: Required value"},
		} {
			raw := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": c4v1beta1.GroupVersion.String(),
				"kind":       "Standard",
				"metadata":   map[string]any{"name": "python-strict", "namespace": ns},
				"spec": map[string]any{
					"supportedTypes":      []any{"service"},
					"supportedFrameworks": []any{"python-fastapi"},
					"template":            map[string]any{"url": templatesURL + "python-strict", "ref": "v1.0.0"},
					"schedules":           []any{c.schedule},
				},
			}}
			Expect(k8sClient.Create(ctx, raw)).To(MatchError(ContainSubstring(c.want)), why)
		}

		two := newStandard(ns, "python-strict", "python-fastapi")
		two.Spec.Schedules = []c4v1beta1.CrewSchedule{weeklySchedule(), weeklySchedule()}
		Expect(k8sClient.Create(ctx, two)).To(MatchError(ContainSubstring(
			`spec.schedules[1]: Duplicate value: {"name":"dependencies"}`)), "two proposals of one name")

		nine := newStandard(ns, "python-strict", "python-fastapi")
		for i := range 9 {
			s := weeklySchedule()
			s.Name = fmt.Sprintf("upkeep-%d", i+1)
			nine.Spec.Schedules = append(nine.Spec.Schedules, s)
		}
		Expect(k8sClient.Create(ctx, nine)).To(MatchError(ContainSubstring(
			"spec.schedules: Too many: 9: must have at most 8 items")), "nine proposals")

		By("taking eight")
		nine.Spec.Schedules = nine.Spec.Schedules[:8]
		Expect(k8sClient.Create(ctx, nine)).To(Succeed())
	})
})
