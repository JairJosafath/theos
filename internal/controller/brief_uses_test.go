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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports/fake"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// The package files a release of the core attaches, and what they hold.
var (
	coreTarball = fake.ReleaseFile{Name: "c4factory-core-0.1.0.tgz", Data: []byte("tgz")}
	coreWheel   = fake.ReleaseFile{Name: "core-0.1.0-py3-none-any.whl", Data: []byte("whl")}
	coreNpm     = fake.Package{Ecosystem: gitprovider.EcosystemNpm, Name: "@c4factory/core", Version: "0.1.0",
		File: coreTarball.Name}
	corePyPI = fake.Package{Ecosystem: gitprovider.EcosystemPyPI, Name: "core", Version: "0.1.0", File: coreWheel.Name}
)

// usesPlan is the cel-lens plan of a core and a CLI that uses the core's
// released module: the CLI's issue 2 says uses: [core], and the round
// releases the core.
func usesPlan(ns string) scaffold.Tree {
	return scaffold.Tree{
		model.PlanModelFile: {Data: []byte(projectDoc(ns) + `---
apiVersion: c4.factory.io/v1beta1
kind: SoftwareSystem
metadata:
  name: cel-lens
spec:
  displayName: CEL lens
` + engineContainer("core", "Core") + engineContainer("cli", "CLI") + `---
apiVersion: c4.factory.io/v1beta1
kind: Relationship
metadata:
  name: cli-to-core
spec:
  source: {kind: Container, name: cli}
  target: {kind: Container, name: core}
  description: Checks files with the core's checker
`)},
		model.PlanChoicesFile:  {Data: []byte("- The CLI uses the core's released module\n")},
		model.PlanReleasesFile: {Data: []byte("containers: [core]\n")},
		"plan/issues/01-core.md": {Data: []byte(`---
title: The checker
labels: [kind/feature]
---
## Goal

The core checks a rule.

## Acceptance criteria

- [ ] The checker reports a broken rule
`)},
		"plan/issues/02-cli.md": {Data: []byte(`---
title: Check a file from the command line
labels: [kind/feature]
uses: [core]
---
## Goal

The CLI checks a file with the core's checker.

## Acceptance criteria

- [ ] The CLI reports a broken rule in a file
`)},
	}
}

// coreModule is the core's module path in namespace ns.
func coreModule(ns string) string {
	return "c4factory.local/" + ns + "/core"
}

// releaseCore sets the core Crew's latest release, v0.1.0, as tagging it would, its
// release runs succeeded; the Crew then publishes its Go module, if it has
// one, and the packages the Gitea release holds.
func releaseCore(ns string) {
	GinkgoHelper()
	releaseCoreRun(ns, "v0.1.0", model.RunSucceeded)
}

// releaseCoreRun is releaseCore with the release runs in state run.
func releaseCoreRun(ns, tag, run string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		crew := &c4v1beta1.Crew{}
		g.Expect(k8sClient.Get(ctx, key(ns, "core"), crew)).To(Succeed())
		crew.Status.LastRelease = &c4v1beta1.CrewRelease{Tag: tag, Commit: "abc123", Time: metav1.Now(), Run: run}
		g.Expect(k8sClient.Status().Update(ctx, crew)).To(Succeed())
	}).Should(Succeed())
}

// coreReleaseRun sets the core's release run of v0.1.0 to status and conclusion.
func coreReleaseRun(ns, status, conclusion string) {
	gitFake.SetRuns(ns, "core", "v0.1.0", "abc123", gitprovider.WorkflowRun{ID: 5, Workflow: "release.yaml",
		Status: status, Conclusion: conclusion, URL: "http://gitea/" + ns + "/core/actions/runs/5"})
}

// corePyPIIndex is the PyPI index the core's packages are installed from, the
// fake GitProvider's.
func corePyPIIndex(ns string) string {
	return "http://gitea.gitea.svc.cluster.local:3000/api/packages/" + ns + "/pypi/simple"
}

// cliTaskStarted reports whether the CLI's Crew took its issue 1, which it
// does only once the issue is labelled ready; the Crew may hand it back
// before a spec reads the label.
func cliTaskStarted(ns string) bool {
	return k8sClient.Get(ctx, key(ns, "cli-1"), &c4v1beta1.Task{}) == nil
}

// Step 4 of docs/plans/done/app-modules.md: an issue that uses another app is
// readied once that app's release has published its Go module; step 5 of
// docs/plans/done/app-packages.md (#1233): or its npm or PyPI package.
var _ = Describe("Brief issues that use another app", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("files the CLI's issue unready until the core's module is published, then readies it pinned to it", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		gitFake.SetGoMod(ns, "core", coreModule(ns))
		gitFake.SetRelease(ns, "core", "v0.1.0", "")
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, usesPlan(ns))

		By("filing the CLI's issue without status/ready while the core has no release")
		Eventually(func(g Gomega) {
			g.Expect(gitFake.OpenIssues(ns, "core")).To(HaveLen(1))
			g.Expect(gitFake.OpenIssues(ns, "cli")).To(HaveLen(1))
			cond := condition(getBrief(g, ns).Status.Conditions, c4v1beta1.ConditionPlanned)
			g.Expect(cond).To(And(HaveField("Reason", "Applying"),
				HaveField("Message", ContainSubstring("issue "+ns+"/cli#1 waits for core's release v0.1.0"))))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			cli := gitFake.Issue(ns, "cli", 1)
			g.Expect(cli.Labels).NotTo(ContainElement(model.LabelReady), "no Crew may take it before the core's release")
			g.Expect(cliTaskStarted(ns)).To(BeFalse())
			g.Expect(cli.Body).NotTo(ContainSubstring("Uses "))
			g.Expect(getBrief(g, ns).Status.Issues).To(ConsistOf(HaveField("Repository", ns+"/core")))
			g.Expect(getBrief(g, ns).Status.Phase).NotTo(Equal(c4v1beta1.BriefPlanned))
		}, "1s").Should(Succeed())

		By("readying it with the module's version once the core's release publishes its module")
		releaseCore(ns)
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
			g.Expect(cliTaskStarted(ns)).To(BeTrue(), "readied, the CLI's Crew takes it")
		}, "30s").Should(Succeed())
		cli := gitFake.Issue(ns, "cli", 1)
		Expect(cli.Labels).To(ContainElement("kind/feature"))
		Expect(cli.Body).To(ContainSubstring("\nUses " + coreModule(ns) + " v0.1.0: go get it at that version.\n"))
		Expect(model.HasIssueMarker(cli.Body, "cel-lens", 1, 2)).To(BeTrue())
		Expect(gitFake.OpenIssues(ns, "cli")).To(HaveLen(1))
		Expect(getBrief(Default, ns).Status.Issues).To(ConsistOf(
			HaveField("Repository", ns+"/core"), HaveField("Repository", ns+"/cli")))
	})

	It("says so while the core's release published nothing other apps install", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		gitFake.SetRelease(ns, "core", "v0.1.0", "")
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, usesPlan(ns))
		Eventually(func(g Gomega) {
			g.Expect(gitFake.OpenIssues(ns, "cli")).To(HaveLen(1))
		}).Should(Succeed())

		releaseCore(ns)
		Eventually(func(g Gomega) {
			cond := condition(getBrief(g, ns).Status.Conditions, c4v1beta1.ConditionPlanned)
			g.Expect(cond).To(And(HaveField("Reason", "Applying"),
				HaveField("Message", ContainSubstring("issue "+ns+"/cli#1 waits for core, whose release v0.1.0 "+
					"published nothing other apps install: v0.1.0 has no go.mod at the repository's root; no Go module to publish; "+
					"v0.1.0 attached no npm tarball, wheel or sdist; no package to publish"))))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.Issue(ns, "cli", 1).Labels).NotTo(ContainElement(model.LabelReady))
			g.Expect(cliTaskStarted(ns)).To(BeFalse())
		}, "1s").Should(Succeed())
	})

	It("finishes an issue a restart left filed but not ready, filing nothing twice", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		gitFake.SetGoMod(ns, "core", coreModule(ns))
		gitFake.SetRelease(ns, "core", "v0.1.0", "")

		By("finding the core's issue in the round's milestone and the CLI's filed but not ready")
		plan, err := model.ParsePlan(planFiles(usesPlan(ns)))
		Expect(err).NotTo(HaveOccurred())
		gitFake.AddMilestone(ns, "core", "v0.1.0", model.ReleaseMilestoneDescription("cel-lens", 1),
			time.Now().Add(model.ReleaseMilestoneDue))
		gitFake.AddIssue(ns, "core", "The checker", model.BriefIssueBody(plan.Issues[0], "cel-lens", 1),
			[]string{model.LabelReady, "kind/feature"})
		gitFake.AddIssue(ns, "cli", "Check a file from the command line",
			model.BriefIssueBody(plan.Issues[1], "cel-lens", 1), []string{"kind/feature"})
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, usesPlan(ns))
		Eventually(func(g Gomega) {
			cond := condition(getBrief(g, ns).Status.Conditions, c4v1beta1.ConditionPlanned)
			g.Expect(cond).To(HaveField("Message", ContainSubstring("issue "+ns+"/cli#1 waits for core's release v0.1.0")))
		}).Should(Succeed())

		releaseCore(ns)
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
			g.Expect(cliTaskStarted(ns)).To(BeTrue(), "readied, the CLI's Crew takes it")
		}, "30s").Should(Succeed())
		Expect(gitFake.OpenIssues(ns, "core")).To(HaveLen(1))
		Expect(gitFake.OpenIssues(ns, "cli")).To(HaveLen(1))
		cli := gitFake.Issue(ns, "cli", 1)
		Expect(cli.Body).To(ContainSubstring("\nUses " + coreModule(ns) + " v0.1.0: go get it at that version.\n"))
		Expect(gitFake.AllMilestones(ns, "core")).To(HaveLen(1))
		Expect(getBrief(Default, ns).Status.Issues).To(HaveLen(2))
	})

	// #1233: a TypeScript CLI on a TypeScript core, published as an npm package.
	It("readies a TypeScript CLI's issue once the core's npm package is published, pinned to it", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		gitFake.SetRelease(ns, "core", "v0.1.0", "", coreTarball)
		gitFake.SetReleasePackages(ns, "core", "v0.1.0", coreNpm)
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, usesPlan(ns))
		Eventually(func(g Gomega) {
			g.Expect(gitFake.OpenIssues(ns, "cli")).To(HaveLen(1))
		}).Should(Succeed())

		By("keeping it unready while the core's release runs, its packages Waiting")
		coreReleaseRun(ns, "in_progress", "")
		releaseCoreRun(ns, "v0.1.0", model.RunRunning)
		Eventually(func(g Gomega) {
			cond := condition(getBrief(g, ns).Status.Conditions, c4v1beta1.ConditionPlanned)
			g.Expect(cond).To(And(HaveField("Reason", "Applying"),
				HaveField("Message", ContainSubstring("issue "+ns+"/cli#1 waits for core's release v0.1.0 "+
					"to publish its packages"))))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			cli := gitFake.Issue(ns, "cli", 1)
			g.Expect(cli.Labels).NotTo(ContainElement(model.LabelReady), "no Crew may take it before the package is out")
			g.Expect(cliTaskStarted(ns)).To(BeFalse())
			g.Expect(cli.Body).NotTo(ContainSubstring("Uses "))
		}, "1s").Should(Succeed())

		By("readying it with the npm line once the runs succeed and the package is published")
		coreReleaseRun(ns, "completed", "success")
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
			g.Expect(cliTaskStarted(ns)).To(BeTrue(), "readied, the CLI's Crew takes it")
		}, "30s").Should(Succeed())
		cli := gitFake.Issue(ns, "cli", 1)
		Expect(cli.Body).To(ContainSubstring("\nUses npm package @c4factory/core 0.1.0: " +
			"npm install --save-exact @c4factory/core@0.1.0.\n"))
		Expect(cli.Body).NotTo(ContainSubstring("go get"), "the core is no Go module")
		Expect(model.HasIssueMarker(cli.Body, "cel-lens", 1, 2)).To(BeTrue())
		Expect(getBrief(Default, ns).Status.Issues).To(ConsistOf(
			HaveField("Repository", ns+"/core"), HaveField("Repository", ns+"/cli")))
	})

	// #1233: a Python CLI on a Python core, published as a PyPI package, with
	// a restart in between: the issues are already filed, the CLI's not ready.
	It("readies a Python CLI's issue a restart left unready once the core's PyPI package is published", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		gitFake.SetRelease(ns, "core", "v0.1.0", "", coreWheel)
		gitFake.SetReleasePackages(ns, "core", "v0.1.0", corePyPI)
		plan, err := model.ParsePlan(planFiles(usesPlan(ns)))
		Expect(err).NotTo(HaveOccurred())
		gitFake.AddMilestone(ns, "core", "v0.1.0", model.ReleaseMilestoneDescription("cel-lens", 1),
			time.Now().Add(model.ReleaseMilestoneDue))
		gitFake.AddIssue(ns, "core", "The checker", model.BriefIssueBody(plan.Issues[0], "cel-lens", 1),
			[]string{model.LabelReady, "kind/feature"})
		gitFake.AddIssue(ns, "cli", "Check a file from the command line",
			model.BriefIssueBody(plan.Issues[1], "cel-lens", 1), []string{"kind/feature"})
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, usesPlan(ns))
		Eventually(func(g Gomega) {
			cond := condition(getBrief(g, ns).Status.Conditions, c4v1beta1.ConditionPlanned)
			g.Expect(cond).To(HaveField("Message", ContainSubstring("issue "+ns+"/cli#1 waits for core's release v0.1.0")))
		}).Should(Succeed())

		releaseCore(ns)
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
			g.Expect(cliTaskStarted(ns)).To(BeTrue(), "readied, the CLI's Crew takes it")
		}, "30s").Should(Succeed())
		Expect(gitFake.OpenIssues(ns, "core")).To(HaveLen(1))
		Expect(gitFake.OpenIssues(ns, "cli")).To(HaveLen(1))
		cli := gitFake.Issue(ns, "cli", 1)
		Expect(cli.Body).To(ContainSubstring("\nUses PyPI package core 0.1.0 from the c4factory index " +
			corePyPIIndex(ns) + ": uv add core==0.1.0, pinned to that index as AGENTS.md says.\n"))
		Expect(cli.Body).NotTo(ContainSubstring("go get"), "the core is no Go module")
		Expect(getBrief(Default, ns).Status.Issues).To(HaveLen(2))
	})
})
