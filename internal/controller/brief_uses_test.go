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

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// usesPlan is the cel-lens plan of a core and a CLI that uses the core's
// released module: the CLI's issue 2 says uses: [core], and the round
// releases the core.
func usesPlan(ns string) scaffold.Tree {
	return scaffold.Tree{
		model.PlanModelFile: {Data: []byte(projectDoc(ns) + `---
apiVersion: c4.factory.io/v1alpha1
kind: SoftwareSystem
metadata:
  name: cel-lens
spec:
  displayName: CEL lens
` + engineContainer("core", "Core") + engineContainer("cli", "CLI") + `---
apiVersion: c4.factory.io/v1alpha1
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

// releaseCore sets the core Crew's latest release as tagging it would; the
// Crew then publishes its Go module, if it has one.
func releaseCore(ns, tag string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		crew := &c4v1alpha1.Crew{}
		g.Expect(k8sClient.Get(ctx, key(ns, "core"), crew)).To(Succeed())
		crew.Status.LastRelease = &c4v1alpha1.CrewRelease{Tag: tag, Commit: "abc123", Time: metav1.Now(),
			Run: "Succeeded"}
		g.Expect(k8sClient.Status().Update(ctx, crew)).To(Succeed())
	}).Should(Succeed())
}

// cliTaskStarted reports whether the CLI's Crew took its issue 1, which it
// does only once the issue is labelled ready; the Crew may hand it back
// before a spec reads the label.
func cliTaskStarted(ns string) bool {
	return k8sClient.Get(ctx, key(ns, "cli-1"), &c4v1alpha1.Task{}) == nil
}

// Step 4 of docs/plans/done/app-modules.md: an issue that uses another app is
// readied once that app's release has published its Go module.
var _ = Describe("Brief issues that use another app", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("files the CLI's issue unready until the core's module is published, then readies it pinned to it", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		gitFake.setGoMod(ns, "core", coreModule(ns))
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, usesPlan(ns))

		By("filing the CLI's issue without status/ready while the core has no release")
		Eventually(func(g Gomega) {
			g.Expect(gitFake.openIssues(ns, "core")).To(HaveLen(1))
			g.Expect(gitFake.openIssues(ns, "cli")).To(HaveLen(1))
			cond := condition(getBrief(g, ns).Status.Conditions, c4v1alpha1.ConditionPlanned)
			g.Expect(cond).To(And(HaveField("Reason", "Applying"),
				HaveField("Message", ContainSubstring("issue "+ns+"/cli#1 waits for core's release v0.1.0"))))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			cli := gitFake.issue(ns, "cli", 1)
			g.Expect(cli.Labels).NotTo(ContainElement(model.LabelReady), "no Crew may take it before the core's release")
			g.Expect(cliTaskStarted(ns)).To(BeFalse())
			g.Expect(cli.Body).NotTo(ContainSubstring("Uses "))
			g.Expect(getBrief(g, ns).Status.Issues).To(ConsistOf(HaveField("Repository", ns+"/core")))
			g.Expect(getBrief(g, ns).Status.Phase).NotTo(Equal(c4v1alpha1.BriefPlanned))
		}, "1s").Should(Succeed())

		By("readying it with the module's version once the core's release publishes its module")
		releaseCore(ns, "v0.1.0")
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
			g.Expect(cliTaskStarted(ns)).To(BeTrue(), "readied, the CLI's Crew takes it")
		}, "30s").Should(Succeed())
		cli := gitFake.issue(ns, "cli", 1)
		Expect(cli.Labels).To(ContainElement("kind/feature"))
		Expect(cli.Body).To(ContainSubstring("\nUses " + coreModule(ns) + " v0.1.0: go get it at that version.\n"))
		Expect(model.HasIssueMarker(cli.Body, "cel-lens", 1, 2)).To(BeTrue())
		Expect(gitFake.openIssues(ns, "cli")).To(HaveLen(1))
		Expect(getBrief(Default, ns).Status.Issues).To(ConsistOf(
			HaveField("Repository", ns+"/core"), HaveField("Repository", ns+"/cli")))
	})

	It("says so while the core's release published no Go module", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, usesPlan(ns))
		Eventually(func(g Gomega) {
			g.Expect(gitFake.openIssues(ns, "cli")).To(HaveLen(1))
		}).Should(Succeed())

		releaseCore(ns, "v0.1.0")
		Eventually(func(g Gomega) {
			cond := condition(getBrief(g, ns).Status.Conditions, c4v1alpha1.ConditionPlanned)
			g.Expect(cond).To(And(HaveField("Reason", "Applying"),
				HaveField("Message", ContainSubstring("issue "+ns+"/cli#1 waits for core, whose release v0.1.0 "+
					"published no Go module: v0.1.0 has no go.mod at the repository's root"))))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.issue(ns, "cli", 1).Labels).NotTo(ContainElement(model.LabelReady))
			g.Expect(cliTaskStarted(ns)).To(BeFalse())
		}, "1s").Should(Succeed())
	})

	It("finishes an issue a restart left filed but not ready, filing nothing twice", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		gitFake.setGoMod(ns, "core", coreModule(ns))

		By("finding the core's issue in the round's milestone and the CLI's filed but not ready")
		plan, err := model.ParsePlan(planFiles(usesPlan(ns)))
		Expect(err).NotTo(HaveOccurred())
		gitFake.addMilestone(ns, "core", "v0.1.0", model.ReleaseMilestoneDescription("cel-lens", 1),
			time.Now().Add(model.ReleaseMilestoneDue))
		gitFake.addIssue(ns, "core", "The checker", model.BriefIssueBody(plan.Issues[0], "cel-lens", 1),
			[]string{model.LabelReady, "kind/feature"})
		gitFake.addIssue(ns, "cli", "Check a file from the command line",
			model.BriefIssueBody(plan.Issues[1], "cel-lens", 1), []string{"kind/feature"})
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, usesPlan(ns))
		Eventually(func(g Gomega) {
			cond := condition(getBrief(g, ns).Status.Conditions, c4v1alpha1.ConditionPlanned)
			g.Expect(cond).To(HaveField("Message", ContainSubstring("issue "+ns+"/cli#1 waits for core's release v0.1.0")))
		}).Should(Succeed())

		releaseCore(ns, "v0.1.0")
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
			g.Expect(cliTaskStarted(ns)).To(BeTrue(), "readied, the CLI's Crew takes it")
		}, "30s").Should(Succeed())
		Expect(gitFake.openIssues(ns, "core")).To(HaveLen(1))
		Expect(gitFake.openIssues(ns, "cli")).To(HaveLen(1))
		cli := gitFake.issue(ns, "cli", 1)
		Expect(cli.Body).To(ContainSubstring("\nUses " + coreModule(ns) + " v0.1.0: go get it at that version.\n"))
		Expect(gitFake.allMilestones(ns, "core")).To(HaveLen(1))
		Expect(getBrief(Default, ns).Status.Issues).To(HaveLen(2))
	})
})
