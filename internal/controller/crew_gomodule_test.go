/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"crypto/rand"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
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
		// The Crew polls each second; publishing each poll, or a second Event,
		// would show. A Crew read from a cache that lags its status may ask once
		// more, and finds the version there.
		Consistently(func(g Gomega) {
			g.Expect(goProxyFake.asks(ordersModule(ns), "v0.1.0")).To(BeZero(), "the registry is the publish")
			g.Expect(gitFake.moduleUploads(ordersModule(ns), "v0.1.0")).To(Equal(1))
			g.Expect(gitFake.modulePublishes(ns, "orders-api", "v0.1.0")).To(BeNumerically("<=", 2))
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
			g.Expect(gitFake.modulePublishes(ns, "orders-api", "v0.1.0")).To(BeNumerically("<=", 2))
		}, 3*time.Second).Should(Succeed())
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
			g.Expect(gitFake.modulePublishes(ns, "orders-api", "v2.0.0")).To(BeNumerically("<=", 2))
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

	It("waits for the source tag of a module in its GitHub folder, then asks the Go proxy (#1001)", func() {
		ns := newProjectNamespace()
		repository := "acme/" + ns
		module := "github.com/" + repository + "/engine"
		token := rand.Text()
		githubFake.addRepository(repository, token)
		gitFake.setGoMod(ns, "orders-api", module)
		// The proxy would find it: only the missing tag holds it back.
		goProxyFake.setFound(module, "v0.1.0", true)
		mustCreate(publishSecret(ns, rand.Text()))
		app := sourceCrew(ns, repository, "engine", module)

		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Status.LastRelease).NotTo(BeNil())
			g.Expect(crew.Status.LastRelease.GoModule).NotTo(BeNil())
			g.Expect(crew.Status.LastRelease.GoModule.State).To(Equal("Waiting"))
			g.Expect(crew.Status.LastRelease.GoModule.CheckedTime).To(BeNil())
			cond := condition(crew.Status.Conditions, ConditionModulePublished)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("WaitsForRelease"))
			g.Expect(cond.Message).To(And(
				HavePrefix("Uploaded "+module+" v0.1.0 to the registry"),
				ContainSubstring("where "+repository+" has no tag engine/v0.1.0 yet: Published is Unauthorized, ")))
		}).Should(Succeed())
		Expect(goProxyFake.asks(module, "v0.1.0")).To(BeZero(), "a miss asked early is cached by the proxy")
		Expect(moduleEvents(Default, ns, "ModulePublished")).To(BeEmpty())

		var secret corev1.Secret
		Expect(k8sClient.Get(ctx, key(ns, "orders-api-github"), &secret)).To(Succeed())
		secret.Data = map[string][]byte{"token": []byte(token)}
		Expect(k8sClient.Update(ctx, &secret)).To(Succeed())
		sourcePublished(ns, repository, "engine", app)

		message := "Published " + module + " v0.1.0; " + goProxyFake.server.URL + " has it"
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			m := crew.Status.LastRelease.GoModule
			g.Expect(m.State).To(Equal("Published"))
			g.Expect(m.Message).To(Equal(message))
			g.Expect(m.CheckedTime).NotTo(BeNil())
			cond := condition(crew.Status.Conditions, ConditionModulePublished)
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal("ModulePublished"))
			g.Expect(moduleEvents(g, ns, "ModulePublished")).To(ConsistOf(And(HaveField("Type", "Normal"),
				HaveField("Note", message))))
		}, 20*time.Second).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(moduleEvents(g, ns, "ModulePublished")).To(HaveLen(1))
			// Found is found: never asked again.
			g.Expect(goProxyFake.asks(module, "v0.1.0")).To(BeNumerically("<=", 2))
		}, 3*time.Second).Should(Succeed())
	})

	It("reports a module the Go proxy can't fetch, with one Warning, and asks again every 5 minutes", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		module := "github.com/someone/" + ns
		gitFake.setGoMod(ns, "orders-api", module)
		mergedIssue(ns)
		taggingCrew(ns)

		message := "Uploaded " + module + " v0.1.0 to the registry, which go get reads only for c4factory.local/ " +
			"paths; " + goProxyFake.server.URL + " can't fetch it (not found: " + module +
			"@v0.1.0: invalid version: unknown revision v0.1.0): nothing in this Crew publishes to github.com; " +
			"push the tag there by hand"
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Status.LastRelease).NotTo(BeNil())
			m := crew.Status.LastRelease.GoModule
			g.Expect(m).NotTo(BeNil())
			g.Expect(m.State).To(Equal("NotFetchable"))
			g.Expect(m.CheckedTime).NotTo(BeNil())
			cond := condition(crew.Status.Conditions, ConditionModulePublished)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("NotFetchable"))
			g.Expect(cond.Message).To(Equal(message))
			g.Expect(moduleEvents(g, ns, "ModuleNotFetchable")).To(ConsistOf(And(HaveField("Type", "Warning"),
				HaveField("Note", message))))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(goProxyFake.asks(module, "v0.1.0")).To(Equal(1), "asked at most every 5 minutes")
		}, 3*time.Second).Should(Succeed())

		skewCrewClock(6 * time.Minute)
		Eventually(func() int { return goProxyFake.asks(module, "v0.1.0") }).Should(Equal(2))
		Consistently(func(g Gomega) {
			g.Expect(goProxyFake.asks(module, "v0.1.0")).To(Equal(2))
			events := moduleEvents(g, ns, "ModuleNotFetchable")
			g.Expect(events).To(HaveLen(1), "one Warning per release")
			g.Expect(events[0].Series).To(BeNil())
			g.Expect(getCrew(g, ns).Status.LastRelease.GoModule.State).To(Equal("NotFetchable"))
		}, 3*time.Second).Should(Succeed())
	})

	It("checks again an old Published module of a path outside the factory, never checked", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		module := "github.com/someone/" + ns
		gitFake.setGoMod(ns, "orders-api", module)
		goProxyFake.setFound(module, "v0.1.0", true)
		mergedIssue(ns)
		taggingCrew(ns)
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Status.LastRelease).NotTo(BeNil())
			g.Expect(crew.Status.LastRelease.GoModule).NotTo(BeNil())
			g.Expect(crew.Status.LastRelease.GoModule.State).To(Equal("Published"))
		}).Should(Succeed())

		// As a factory before #1001 left it: Published on the upload alone.
		goProxyFake.setFound(module, "v0.1.0", false)
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			crew.Status.LastRelease.GoModule = &c4v1alpha1.CrewReleaseGoModule{State: "Published", Path: module,
				Version: "v0.1.0", Message: "Published " + module + " v0.1.0"}
			g.Expect(k8sClient.Status().Update(ctx, crew)).To(Succeed())
		}).Should(Succeed())

		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			m := crew.Status.LastRelease.GoModule
			g.Expect(m.State).To(Equal("NotFetchable"))
			g.Expect(m.CheckedTime).NotTo(BeNil())
			cond := condition(crew.Status.Conditions, ConditionModulePublished)
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("NotFetchable"))
		}).Should(Succeed())
		Expect(goProxyFake.asks(module, "v0.1.0")).To(Equal(2))
	})
})

var _ = Describe("Crew Go modules with no Go proxy", func() {
	It("takes the Crew's source tag for a module in its GitHub folder (--go-proxy=off)", func() {
		module := "github.com/acme/cel-lens/engine"
		crew := &c4v1alpha1.Crew{Spec: c4v1alpha1.CrewSpec{TagReleases: true,
			PublishReleases: &c4v1alpha1.CrewPublishReleases{
				GitHub: c4v1alpha1.CrewPublishGitHub{Repository: "acme/cel-lens"},
				Source: &c4v1alpha1.CrewPublishSource{Path: "engine"}}}}
		status := &c4v1alpha1.CrewStatus{LastRelease: &c4v1alpha1.CrewRelease{Tag: "v0.15.1",
			GoModule: &c4v1alpha1.CrewReleaseGoModule{State: "Published", Path: module, Version: "v0.15.1"}}}
		r := &CrewReconciler{}

		r.publishGoModule(ctx, crew, status, nil, gitprovider.Repository{})
		Expect(status.LastRelease.GoModule.State).To(Equal("Waiting"))
		Expect(condition(status.Conditions, ConditionModulePublished).Reason).To(Equal("WaitsForRelease"))

		status.LastRelease.Publish = &c4v1alpha1.CrewReleasePublish{State: "Published",
			Source: &c4v1alpha1.CrewReleaseSource{Commit: "abc", Tag: "engine/v0.15.1"}}
		r.publishGoModule(ctx, crew, status, nil, gitprovider.Repository{})
		m := status.LastRelease.GoModule
		Expect(m.State).To(Equal("Published"))
		Expect(m.CheckedTime).To(BeNil(), "no proxy was asked")
		Expect(m.Message).To(Equal("Published " + module + " v0.15.1, tagged engine/v0.15.1 on GitHub; " +
			"no Go proxy was asked"))
		cond := condition(status.Conditions, ConditionModulePublished)
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("ModulePublished"))
	})
})
