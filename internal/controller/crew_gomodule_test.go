/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// ordersModule is the module path of orders-api in namespace ns: the fake
// host's registry is shared by every spec.
func ordersModule(ns string) string {
	return "c4factory.local/" + ns + "/orders-api"
}

// moduleEvents lists the Crew's Events of reason.
func moduleEvents(g Gomega, ns, reason string) []eventsv1.Event {
	var out []eventsv1.Event
	for _, e := range crewEvents(g, ns) {
		if e.Reason == reason {
			out = append(out, e)
		}
	}
	return out
}

// taggingCrew creates the Crew of orders-api with spec.tagReleases set.
func taggingCrew(ns string) {
	GinkgoHelper()
	crew := newCrew(ns)
	crew.Spec.TagReleases = true
	mustCreate(crew)
}

var _ = Describe("Crew Go modules", func() {
	It("publishes a released Go Container's module once", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		gitFake.setGoMod(ns, "orders-api", ordersModule(ns))
		mergedIssue(ns)
		taggingCrew(ns)

		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Status.LastRelease).NotTo(BeNil())
			g.Expect(crew.Status.LastRelease.GoModule).To(Equal(&c4v1alpha1.CrewReleaseGoModule{
				State: "Published", Path: ordersModule(ns), Version: "v0.1.0",
				Message: "Published " + ordersModule(ns) + " v0.1.0"}))
			cond := condition(crew.Status.Conditions, ConditionModulePublished)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal("ModulePublished"))
			g.Expect(moduleEvents(g, ns, "ModulePublished")).To(ConsistOf(And(HaveField("Type", "Normal"),
				HaveField("Note", "Published "+ordersModule(ns)+" v0.1.0"))))
		}).Should(Succeed())
		// The Crew polls each second; a second upload or Event would show.
		Consistently(func(g Gomega) {
			g.Expect(gitFake.moduleUploads(ordersModule(ns), "v0.1.0")).To(Equal(1))
			events := moduleEvents(g, ns, "ModulePublished")
			g.Expect(events).To(HaveLen(1))
			g.Expect(events[0].Series).To(BeNil())
		}, 3*time.Second).Should(Succeed())
	})

	It("skips a released Container with no go.mod", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		taggingCrew(ns)

		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Status.LastRelease).NotTo(BeNil())
			module := crew.Status.LastRelease.GoModule
			g.Expect(module).NotTo(BeNil())
			g.Expect(module.State).To(Equal("NotGo"))
			g.Expect(module.Path).To(BeEmpty())
			cond := condition(crew.Status.Conditions, ConditionModulePublished)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal("NotGo"))
			g.Expect(cond.Message).To(Equal("v0.1.0 has no go.mod at the repository's root; no Go module to publish"))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(moduleEvents(g, ns, "ModulePublished")).To(BeEmpty())
			g.Expect(moduleEvents(g, ns, "InvalidModule")).To(BeEmpty())
		}, 2*time.Second).Should(Succeed())
	})

	It("reports a v2.0.0 whose module path lacks /v2, once, and publishes nothing", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		gitFake.setGoMod(ns, "orders-api", ordersModule(ns))
		mergedIssueIn(ns, "v2.0.0", "5a-merge")
		taggingCrew(ns)

		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Status.LastRelease).NotTo(BeNil())
			g.Expect(crew.Status.LastRelease.Tag).To(Equal("v2.0.0"))
			module := crew.Status.LastRelease.GoModule
			g.Expect(module).NotTo(BeNil())
			g.Expect(module.State).To(Equal("Invalid"))
			g.Expect(module.Path).To(Equal(ordersModule(ns)))
			cond := condition(crew.Status.Conditions, ConditionModulePublished)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("InvalidModule"))
			g.Expect(cond.Message).To(And(HavePrefix("Did not publish "+ordersModule(ns)+" v2.0.0: "),
				ContainSubstring("v2")))
			g.Expect(moduleEvents(g, ns, "InvalidModule")).To(ConsistOf(HaveField("Type", "Warning")))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.moduleUploads(ordersModule(ns), "v2.0.0")).To(BeZero())
			events := moduleEvents(g, ns, "InvalidModule")
			g.Expect(events).To(HaveLen(1))
			g.Expect(events[0].Series).To(BeNil())
		}, 3*time.Second).Should(Succeed())
	})

	It("tries a module the Git host failed to take again on the next poll", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		gitFake.setGoMod(ns, "orders-api", ordersModule(ns))
		gitFake.failModulePublishes(ns, "orders-api", 2)
		mergedIssue(ns)
		taggingCrew(ns)

		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Status.LastRelease).NotTo(BeNil())
			g.Expect(crew.Status.LastRelease.GoModule).NotTo(BeNil())
			g.Expect(crew.Status.LastRelease.GoModule.State).To(Equal("Published"))
			cond := condition(crew.Status.Conditions, ConditionModulePublished)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())
		Expect(gitFake.modulePublishFailuresLeft(ns, "orders-api")).To(BeZero())
		Expect(gitFake.moduleUploads(ordersModule(ns), "v0.1.0")).To(Equal(1))
	})
})
