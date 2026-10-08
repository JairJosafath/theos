/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// crewSkew moves the Crew's clock ahead of the real one, so a test sees
// model.ReleaseRunBound pass.
var crewSkew atomic.Int64

// crewNow is the CrewReconciler's clock in the suite.
func crewNow() time.Time { return time.Now().Add(time.Duration(crewSkew.Load())) }

// skewCrewClock moves the Crew's clock d ahead until the spec ends.
func skewCrewClock(d time.Duration) {
	crewSkew.Store(int64(d))
	DeferCleanup(func() { crewSkew.Store(0) })
}

// releaseRun sets the run orders-api's v0.1.0 tag started to status and
// conclusion.
func releaseRun(ns, status, conclusion string) {
	gitFake.setRuns(ns, "orders-api", "v0.1.0", "5a-merge", gitprovider.WorkflowRun{ID: 5, Workflow: "release.yaml",
		Status: status, Conclusion: conclusion, URL: "http://gitea/" + ns + "/orders-api/actions/runs/5"})
}

// bundlerContainer creates a Container named name in orders-api's system and
// waits until it is Ready.
func bundlerContainer(ns, name string) {
	GinkgoHelper()
	container := newContainer(ns, name, "webshop-core", c4v1alpha1.ContainerTypeService)
	mustCreate(container)
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(ns, name), container)).To(Succeed())
		g.Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))
	}).Should(Succeed())
}

// bundles links source to orders-api, bundling it when bundling is true.
func bundles(ns, source string, bundling bool) *c4v1alpha1.Relationship {
	rel := newRelationship(ns, source+"-to-orders-api", element(c4v1alpha1.ElementContainer, source),
		element(c4v1alpha1.ElementContainer, "orders-api"))
	rel.Spec.Bundles = bundling
	rel.Spec.Description = "Ships the orders API"
	return rel
}

// bumpIssues are the open issues of name labelled kind/bump.
func bumpIssues(ns, name string) []fakeIssue {
	var out []fakeIssue
	for _, issue := range gitFake.openIssues(ns, name) {
		for _, l := range issue.Labels {
			if l == model.LabelBump {
				out = append(out, issue)
			}
		}
	}
	return out
}

var _ = Describe("Crew release bumps", func() {
	It("files a bump issue in each Container that bundles the release, once", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		bundlerContainer(ns, "web")
		bundlerContainer(ns, "client")
		// web's next release is v0.5.0; Later is no release.
		n := gitFake.addIssue(ns, "web", "Later work", clearIssue, nil)
		gitFake.setIssueMilestone(ns, "web", n, model.FollowUpMilestone)
		n = gitFake.addIssue(ns, "web", "Next work", clearIssue, nil)
		gitFake.setIssueRelease(ns, "web", n, "v0.5.0", releaseDue)
		broken := newContainer(ns, "broken", "webshop-core", c4v1alpha1.ContainerTypeService)
		broken.Spec.FrameworkRef = c4v1alpha1.LocalRef{Name: "missing"}
		mustCreate(broken, bundles(ns, "web", true), bundles(ns, "client", false), bundles(ns, "broken", true))
		mergedIssue(ns)
		releaseRun(ns, "completed", "success")
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			issues := bumpIssues(ns, "web")
			g.Expect(issues).To(HaveLen(1))
			g.Expect(issues[0].Title).To(Equal("Bump orders-api to v0.1.0"))
			g.Expect(issues[0].Labels).To(ContainElement(model.LabelReady))
			g.Expect(issues[0].milestone).To(Equal("v0.5.0"))
			g.Expect(issues[0].Body).To(ContainSubstring("orders-api released v0.1.0 at `5a-merge`"))
			release := getCrew(g, ns).Status.LastRelease
			g.Expect(release).NotTo(BeNil())
			g.Expect(release.Bumps).To(ConsistOf(c4v1alpha1.CrewReleaseBump{Container: "web", IssueURL: issues[0].URL}))
			g.Expect(crewEvents(g, ns)).To(ContainElement(And(HaveField("Type", "Normal"),
				HaveField("Reason", "BumpFiled"))))
		}).Should(Succeed())
		// The Crew polls each second.
		Consistently(func(g Gomega) {
			g.Expect(bumpIssues(ns, "web")).To(HaveLen(1))
			g.Expect(bumpIssues(ns, "client")).To(BeEmpty(), "a link that doesn't bundle files nothing")
			g.Expect(getCrew(g, ns).Status.LastRelease.Bumps).To(HaveLen(1), "a bundler not Ready waits")
		}, 3*time.Second).Should(Succeed())
	})

	It("takes the open bump issue it filed before, without a milestone when there is no release", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		bundlerContainer(ns, "web")
		// The status write that recorded it was lost.
		_, body := model.BumpIssue(model.Bump{Bundled: "orders-api", Tag: "v0.1.0", Commit: "5a-merge",
			Bundler: model.Bundler{Container: "web", Relationship: "web-to-orders-api"}})
		filed := gitFake.addIssue(ns, "web", "Bump orders-api to v0.1.0", body,
			[]string{model.LabelBump, model.LabelReady})
		mustCreate(bundles(ns, "web", true))
		mergedIssue(ns)
		releaseRun(ns, "completed", "success")
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			release := getCrew(g, ns).Status.LastRelease
			g.Expect(release).NotTo(BeNil())
			g.Expect(release.Bumps).To(ConsistOf(c4v1alpha1.CrewReleaseBump{Container: "web",
				IssueURL: gitFake.issue(ns, "web", filed).URL}))
		}).Should(Succeed())
		Expect(bumpIssues(ns, "web")).To(HaveLen(1))
		Expect(crewEvents(Default, ns)).NotTo(ContainElement(HaveField("Reason", "BumpFiled")))
	})

	It("files no bump for a link created after the release", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		bundlerContainer(ns, "web")
		mergedIssue(ns)
		releaseRun(ns, "completed", "success")
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.LastRelease).NotTo(BeNil())
		}).Should(Succeed())
		// Creation times are in seconds; the link must be created a second later.
		time.Sleep(1100 * time.Millisecond)
		mustCreate(bundles(ns, "web", true))

		Consistently(func(g Gomega) {
			g.Expect(bumpIssues(ns, "web")).To(BeEmpty())
			g.Expect(getCrew(g, ns).Status.LastRelease.Bumps).To(BeEmpty())
		}, 3*time.Second).Should(Succeed())
	})

	It("files the bumps only once the release's runs have built its files", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		bundlerContainer(ns, "web")
		mustCreate(bundles(ns, "web", true))
		mergedIssue(ns)
		releaseRun(ns, "queued", "")
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		// The Crew polls each second; each state of the run files nothing.
		for _, run := range []struct{ status, conclusion, want string }{
			{"queued", "", "Running"},
			{"in_progress", "", "Running"},
			{"completed", "failure", "Failed"},
		} {
			releaseRun(ns, run.status, run.conclusion)
			Eventually(func(g Gomega) {
				release := getCrew(g, ns).Status.LastRelease
				g.Expect(release).NotTo(BeNil())
				g.Expect(release.Run).To(Equal(run.want))
			}).Should(Succeed())
			Consistently(func(g Gomega) {
				g.Expect(bumpIssues(ns, "web")).To(BeEmpty(), "a bump waits while the run is "+run.want)
				g.Expect(getCrew(g, ns).Status.LastRelease.Bumps).To(BeEmpty())
			}, 2*time.Second).Should(Succeed())
		}

		// A person runs it again and it ends well.
		releaseRun(ns, "completed", "success")
		Eventually(func(g Gomega) {
			issues := bumpIssues(ns, "web")
			g.Expect(issues).To(HaveLen(1))
			g.Expect(getCrew(g, ns).Status.LastRelease.Bumps).To(ConsistOf(
				c4v1alpha1.CrewReleaseBump{Container: "web", IssueURL: issues[0].URL}))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(bumpIssues(ns, "web")).To(HaveLen(1))
		}, 2*time.Second).Should(Succeed())
	})

	It("files the bumps of a release whose tag starts no run once the run bound has passed", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		bundlerContainer(ns, "web")
		mustCreate(bundles(ns, "web", true))
		mergedIssue(ns)
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)

		Eventually(func(g Gomega) {
			release := getCrew(g, ns).Status.LastRelease
			g.Expect(release).NotTo(BeNil())
			g.Expect(release.Run).To(Equal("Waiting"))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(bumpIssues(ns, "web")).To(BeEmpty(), "a bump waits for a run within the bound")
		}, 2*time.Second).Should(Succeed())

		skewCrewClock(model.ReleaseRunBound + time.Minute)
		Eventually(func(g Gomega) {
			issues := bumpIssues(ns, "web")
			g.Expect(issues).To(HaveLen(1))
			g.Expect(getCrew(g, ns).Status.LastRelease.Bumps).To(ConsistOf(
				c4v1alpha1.CrewReleaseBump{Container: "web", IssueURL: issues[0].URL}))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(bumpIssues(ns, "web")).To(HaveLen(1))
		}, 2*time.Second).Should(Succeed())
	})

	It("refuses bundles on a link that isn't Container to Container", func() {
		ns := newProjectNamespace()
		rel := newRelationship(ns, "orders-api-to-db", element(c4v1alpha1.ElementContainer, "orders-api"),
			element(c4v1alpha1.ElementDataStore, "orders-db"))
		rel.Spec.Bundles = true
		Expect(k8sClient.Create(ctx, rel)).To(MatchError(ContainSubstring("only a Container to Container link can bundle")))
	})
})
