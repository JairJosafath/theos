/*
Copyright 2026.

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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

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

	It("refuses bundles on a link that isn't Container to Container", func() {
		ns := newProjectNamespace()
		rel := newRelationship(ns, "orders-api-to-db", element(c4v1alpha1.ElementContainer, "orders-api"),
			element(c4v1alpha1.ElementDataStore, "orders-db"))
		rel.Spec.Bundles = true
		Expect(k8sClient.Create(ctx, rel)).To(MatchError(ContainSubstring("only a Container to Container link can bundle")))
	})
})
