/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// releasingLinterPlan is linterPlan with releases.yaml naming the linter.
func releasingLinterPlan(ns string, issues int) scaffold.Tree {
	files := linterPlan(ns, issues)
	files[model.PlanReleasesFile] = scaffold.File{Data: []byte("containers: [linter]\n")}
	return files
}

// allMilestones returns the fake host's milestones of owner/name, open and closed.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) allMilestones(owner, name string) []gitprovider.Milestone {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.milestones(owner+"/"+name, true)
}

// addMilestone makes a milestone of owner/name as CreateMilestone would.
func (g *fakeGit) addMilestone(owner, name, title, description string, due time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := owner + "/" + name
	s := g.issueState()
	s.milestones[key] = append(s.milestones[key], title)
	id := int64(len(s.milestones[key]))
	g.releaseState().created[fmt.Sprintf("%s#%d", key, id)] = gitprovider.Milestone{ID: id, Title: title,
		Description: description, Due: due}
}

// roundIssueMilestones returns the milestone titles of the issues Brief
// cel-lens recorded for round.
func roundIssueMilestones(g Gomega, ns string, round int32) []string {
	var out []string
	for _, issue := range getBrief(g, ns).Status.Issues {
		if issue.Round == round {
			out = append(out, gitFake.issue(ns, "linter", int(issue.Number)).milestone)
		}
	}
	return out
}

func linterRelease(repository string) c4v1alpha1.BriefRelease {
	return c4v1alpha1.BriefRelease{Container: "linter", CrewPublishReleases: c4v1alpha1.CrewPublishReleases{
		GitHub: c4v1alpha1.CrewPublishGitHub{Repository: repository,
			TokenSecretRef: c4v1alpha1.LocalRef{Name: "cel-lens-github"}}}}
}

// linterCrew reads the Crew of the linter.
func linterCrew(g Gomega, ns string) *c4v1alpha1.Crew {
	crew := &c4v1alpha1.Crew{}
	g.Expect(k8sClient.Get(ctx, key(ns, "linter"), crew)).To(Succeed())
	return crew
}

// Step 4 of docs/plans/brief-releases.md: a released app's Crew tags, prepares
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
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}).Should(Succeed())
		crew := linterCrew(Default, ns)
		Expect(crew.Spec.TagReleases).To(BeTrue())
		Expect(crew.Spec.PrepareReleases).To(BeTrue())
		Expect(crew.Spec.PublishReleases).To(BeNil(), "no spec.releases entry names it")
		Expect(roundIssueMilestones(Default, ns, 1)).To(Equal([]string{"v0.1.0", "v0.1.0"}))
		milestones := gitFake.allMilestones(ns, "linter")
		Expect(milestones).To(HaveLen(1))
		Expect(model.HasReleaseMilestoneMarker(milestones[0].Description, "cel-lens", 1)).To(BeTrue())
		Expect(milestones[0].Due).To(BeTemporally("~", before.Add(model.ReleaseMilestoneDue), time.Minute))

		By("telling the planner the linter ships releases")
		Eventually(func(g Gomega) {
			g.Expect(string(gitFake.mainFiles(ns, briefRepo)[model.WorkspaceContainersFile].Data)).
				To(ContainSubstring("released: true"))
		}).Should(Succeed())

		By("filing round 2's issues in v0.2.0, though its plan doesn't name the linter again (R2)")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Lint VAP files too."} })).To(Succeed())
		planRound(ns, 2, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Round).To(Equal(int32(2)))
			g.Expect(b.Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}).Should(Succeed())
		Expect(roundIssueMilestones(Default, ns, 2)).To(Equal([]string{"v0.2.0"}))
		Expect(gitFake.allMilestones(ns, "linter")).To(HaveLen(2))
		Expect(linterCrew(Default, ns).Spec.TagReleases).To(BeTrue())
	})

	It("finds the round's release milestone again after a restart, making no second one", func() {
		createFactory(ns)
		briefAgents(ns, 3)

		By("finding the milestone of round 1 there already, titled as a restart left it")
		gitFake.addMilestone(ns, "linter", "v0.1.0", model.ReleaseMilestoneDescription("cel-lens", 1),
			time.Now().Add(model.ReleaseMilestoneDue))
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, releasingLinterPlan(ns, 2))

		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}).Should(Succeed())
		Expect(gitFake.allMilestones(ns, "linter")).To(HaveLen(1))
		Expect(roundIssueMilestones(Default, ns, 1)).To(Equal([]string{"v0.1.0", "v0.1.0"}))
	})

	It("files no release milestone for a Container the round doesn't release (B11)", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}).Should(Succeed())
		Expect(gitFake.allMilestones(ns, "linter")).To(BeEmpty())
		Expect(roundIssueMilestones(Default, ns, 1)).To(Equal([]string{""}))
	})

	It("sets the Crew's publishReleases from a spec.releases entry, follows a change and keeps it on removal", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		brief := newBrief(ns, "cel-lens")
		brief.Spec.Releases = []c4v1alpha1.BriefRelease{linterRelease("acme/cel-lens")}
		mustCreate(brief)

		By("listing the entry in brief.md for the planner")
		Eventually(func(g Gomega) { getTask(g, ns, "cel-lens-plan-1") }).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(string(gitFake.mainFiles(ns, briefRepo)[model.BriefFile].Data)).
				To(ContainSubstring("`linter`, published to github.com/acme/cel-lens"))
		}).Should(Succeed())

		By("making the linter's Crew release and publish, though the plan doesn't name it")
		planRound(ns, 1, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
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
			g.Expect(string(gitFake.mainFiles(ns, briefRepo)[model.BriefFile].Data)).NotTo(ContainSubstring("## Releases"))
		}).Should(Succeed())
		crew = linterCrew(Default, ns)
		Expect(crew.Spec.TagReleases).To(BeTrue())
		Expect(crew.Spec.PublishReleases.GitHub.Repository).To(Equal("acme/lens"))
	})

	It("leaves alone a Crew the Brief didn't make", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(&c4v1alpha1.Crew{ObjectMeta: objectMeta(ns, "linter"), Spec: c4v1alpha1.CrewSpec{
			ContainerRef: c4v1alpha1.LocalRef{Name: "linter"},
			Implementer:  c4v1alpha1.LocalRef{Name: "implementer"}, Reviewer: c4v1alpha1.LocalRef{Name: "reviewer"},
			Parallel: 2}})
		orig := linterCrew(Default, ns)
		brief := newBrief(ns, "cel-lens")
		brief.Spec.Releases = []c4v1alpha1.BriefRelease{linterRelease("acme/cel-lens")}
		mustCreate(brief)
		planRound(ns, 1, 1, releasingLinterPlan(ns, 1))

		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
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
			brief.Spec.Releases = []c4v1alpha1.BriefRelease{linterRelease("acme/a"), linterRelease("acme/b")}
			Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring("Duplicate value")))
		})

		It("names a Container by a DNS label", func() {
			brief := newBrief(ns, "label")
			entry := linterRelease("acme/cel-lens")
			entry.Container = "Linter_CLI"
			brief.Spec.Releases = []c4v1alpha1.BriefRelease{entry}
			Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring("container must be a DNS label")))
		})

		It("names a repository as owner/name", func() {
			brief := newBrief(ns, "url")
			brief.Spec.Releases = []c4v1alpha1.BriefRelease{linterRelease("https://github.com/acme/cel-lens")}
			Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring("repository must be owner/name")))
		})

		It("refuses a tag prefix with a space", func() {
			brief := newBrief(ns, "prefix")
			entry := linterRelease("acme/cel-lens")
			entry.TagPrefix = "linter v"
			brief.Spec.Releases = []c4v1alpha1.BriefRelease{entry}
			Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring("spec.releases[0].tagPrefix")))
		})
	})
})

// briefApp returns the line of container in Brief cel-lens's status.apps.
func briefApp(g Gomega, ns, container string) c4v1alpha1.BriefApp {
	for _, app := range getBrief(g, ns).Status.Apps {
		if app.Container == container {
			return app
		}
	}
	g.Expect(container).To(BeEmpty(), "Brief cel-lens has no app line for it")
	return c4v1alpha1.BriefApp{}
}

// setLinterRelease sets the linter Crew's latest release as tagging it would.
func setLinterRelease(ns, tag string) {
	Eventually(func(g Gomega) {
		crew := linterCrew(g, ns)
		crew.Status.LastRelease = &c4v1alpha1.CrewRelease{Tag: tag, Commit: "abc123", Time: metav1.Now(),
			Run: "Succeeded"}
		g.Expect(k8sClient.Status().Update(ctx, crew)).To(Succeed())
	}).Should(Succeed())
}

// Step 5 of docs/plans/brief-releases.md: the Brief's status shows each app's
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
		brief.Spec.Releases = []c4v1alpha1.BriefRelease{linterRelease("acme/cel-lens"), server}
		mustCreate(brief)
		planRound(ns, 1, 1, releasingLinterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}).Should(Succeed())

		Eventually(func(g Gomega) {
			apps := getBrief(g, ns).Status.Apps
			g.Expect(apps).To(HaveLen(2))
			g.Expect(apps[0]).To(Equal(c4v1alpha1.BriefApp{Container: "linter", Crew: "linter", Releases: "On",
				PublishTo: "acme/cel-lens",
				Message:   "Releases are on: the next one follows once the issues of its milestone are merged."}))
			g.Expect(apps[1]).To(Equal(c4v1alpha1.BriefApp{Container: "server", Releases: "Off",
				Message: "The Brief hasn't made a Container named server: its releases start once a round makes it."}))
		}).Should(Succeed())

		By("showing the Crew's new release on the Brief")
		var repoURL string
		Eventually(func(g Gomega) {
			var c c4v1alpha1.Container
			g.Expect(k8sClient.Get(ctx, key(ns, "linter"), &c)).To(Succeed())
			repoURL = c.Status.RepositoryURL
			g.Expect(repoURL).NotTo(BeEmpty())
		}).Should(Succeed())
		setLinterRelease(ns, "v0.1.0")
		Eventually(func(g Gomega) {
			release := briefApp(g, ns, "linter").LastRelease
			g.Expect(release).NotTo(BeNil())
			g.Expect(release.Tag).To(Equal("v0.1.0"))
			g.Expect(release.URL).To(Equal(repoURL + "/releases/tag/v0.1.0"))
			g.Expect(release.Time).NotTo(BeNil())
		}).Should(Succeed())

		By("following the Crew's next release")
		setLinterRelease(ns, "v0.2.0")
		Eventually(func(g Gomega) {
			app := briefApp(g, ns, "linter")
			g.Expect(app.LastRelease).NotTo(BeNil())
			g.Expect(app.LastRelease.Tag).To(Equal("v0.2.0"))
			crew := linterCrew(g, ns).Status.LastRelease
			g.Expect(app.LastRelease.Run).To(Equal(crew.Run))
		}).Should(Succeed())
	})

	It("says how to turn releases on for an app whose Crew doesn't release", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Apps).To(Equal([]c4v1alpha1.BriefApp{{Container: "linter",
				Crew: "linter", Releases: "Off", Message: MessageReleasesOff}}))
		}).Should(Succeed())
		Expect(MessageReleasesOff).To(Equal(
			"Releases are off: ask for them in a prompt, or name this app in spec.releases."))
	})
})

var _ = DescribeTable("appMessage",
	func(app c4v1alpha1.BriefApp, briefsCrew bool, want string) {
		Expect(appMessage(&app, briefsCrew)).To(Equal(want))
	},
	Entry("off on a Crew another owner made", c4v1alpha1.BriefApp{Crew: "linter", Releases: "Off"}, false,
		"Releases are off, and they are for the owner of Crew linter to turn on: the Brief didn't make it."),
	Entry("a failed run", c4v1alpha1.BriefApp{Releases: "On",
		LastRelease: &c4v1alpha1.BriefAppRelease{Tag: "v0.1.0", Run: "Failed"}}, true,
		"Release v0.1.0 was tagged, but its CI run failed."),
	Entry("published", c4v1alpha1.BriefApp{Releases: "On", PublishTo: "acme/lens",
		LastRelease: &c4v1alpha1.BriefAppRelease{Tag: "v0.1.0", Run: "Succeeded", PublishState: "Published"}}, true,
		"Released v0.1.0 and published it to acme/lens."),
	Entry("publishing failed", c4v1alpha1.BriefApp{Releases: "On", PublishTo: "acme/lens",
		LastRelease: &c4v1alpha1.BriefAppRelease{Tag: "v0.1.0", PublishState: "Failed"}}, true,
		"Release v0.1.0 could not be published to acme/lens; the Crew says why and tries again."),
	Entry("released, not published", c4v1alpha1.BriefApp{Releases: "On",
		LastRelease: &c4v1alpha1.BriefAppRelease{Tag: "v0.1.0", Run: "Running"}}, true, "Released v0.1.0."),
)
