/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// engineContainer is a Container of the cel-lens system, as a plan holds it.
func engineContainer(name, displayName string) string {
	return fmt.Sprintf(`---
apiVersion: c4.factory.io/v1alpha1
kind: Container
metadata:
  name: %s
spec:
  softwareSystemRef: {name: cel-lens}
  displayName: %s
  type: service
  gitProviderRef: {name: gitea}
  frameworkRef: {name: python-fastapi}
  computeRef: {name: small}
  deploymentRef: {name: k8s-manifests}
`, name, displayName)
}

// orderedPlan is the cel-lens plan of a core and a CLI on it: the CLI's issue
// 2 is after the core's issue 1.
func orderedPlan(ns string) scaffold.Tree {
	return scaffold.Tree{
		model.PlanModelFile: {Data: []byte(projectDoc(ns) + `---
apiVersion: c4.factory.io/v1alpha1
kind: SoftwareSystem
metadata:
  name: cel-lens
spec:
  displayName: CEL lens
` + engineContainer("core", "Core") + engineContainer("cli", "CLI"))},
		model.PlanChoicesFile: {Data: []byte("- The CLI runs the core's checker\n")},
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
after: [1]
---
## Goal

The CLI checks a file with the core's checker.

## Acceptance criteria

- [ ] The CLI reports a broken rule in a file
`)},
	}
}

// orderWait bounds each wait for a round's issues in these specs. One spans the
// plan Task finished, the plan applied, the SoftwareSystem and both Containers
// made, scaffolded and Ready, the issues filed and a failed apply retried after
// its backoff: a chain of reconciles across the Task, Brief, Container and
// Project controllers. 10 s ran out on a loaded device (#832) and the suite's
// 30 s on a loaded CI runner, before Container cli existed (#1848); a passing
// wait returns as soon as it holds, so only a failing spec waits this long.
const orderWait = 90 * time.Second

var _ = Describe("Brief issue order", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("files an issue after the ones it builds on, depending on them, and labels it ready last", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		release := gitFake.hang(ns, "core")
		DeferCleanup(release)
		gitFake.failDependencies(ns, "cli", errors.New("gitea: dependencies are down"))
		DeferCleanup(func() { gitFake.failDependencies(ns, "cli", nil) })
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, orderedPlan(ns))

		By("holding the CLI's issue while the core it builds on isn't Ready, though the CLI is")
		Eventually(func(g Gomega) {
			var cli c4v1alpha1.Container
			g.Expect(k8sClient.Get(ctx, key(ns, "cli"), &cli)).To(Succeed())
			g.Expect(cli.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))
			cond := condition(getBrief(g, ns).Status.Conditions, c4v1alpha1.ConditionPlanned)
			g.Expect(cond).To(And(HaveField("Reason", "Applying"),
				HaveField("Message", ContainSubstring("waits for Container core"))))
		}, orderWait).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.openIssues(ns, "cli")).To(BeEmpty())
			g.Expect(getBrief(g, ns).Status.Issues).To(BeEmpty())
		}, "1s").Should(Succeed())

		By("filing the core's issue once the core is Ready, then the CLI's, not ready while its dependency fails")
		release()
		Eventually(func(g Gomega) {
			g.Expect(gitFake.openIssues(ns, "core")).To(HaveLen(1))
			g.Expect(gitFake.openIssues(ns, "cli")).To(HaveLen(1))
			cond := condition(getBrief(g, ns).Status.Conditions, c4v1alpha1.ConditionPlanned)
			g.Expect(cond).To(And(HaveField("Reason", model.ReasonApplyFailed),
				HaveField("Message", ContainSubstring("dependencies are down"))))
		}, orderWait).Should(Succeed())
		cli := gitFake.issue(ns, "cli", 1)
		Expect(cli.Labels).NotTo(ContainElement(model.LabelReady), "no Crew may take it before it depends on the core's")
		Expect(cli.blockers).To(BeEmpty())
		Expect(getBrief(Default, ns).Status.Issues).To(HaveLen(1), "the CLI's isn't recorded until it is done")

		By("making it depend on the core's issue, then labelling it ready")
		gitFake.failDependencies(ns, "cli", nil)
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}, orderWait).Should(Succeed())
		cli = gitFake.issue(ns, "cli", 1)
		Expect(cli.blockers).To(Equal([]string{ns + "/core#1"}))
		Expect(cli.Labels).To(ContainElements(model.LabelReady, "kind/feature"))
		Expect(cli.Body).To(ContainSubstring("\nBuilds on " + ns + "/core#1 (The checker).\n"))
		Expect(model.HasIssueMarker(cli.Body, "cel-lens", 1, 2)).To(BeTrue())
		Expect(gitFake.openIssues(ns, "cli")).To(HaveLen(1))
		Expect(getBrief(Default, ns).Status.Issues).To(ConsistOf(
			HaveField("Repository", ns+"/core"), HaveField("Repository", ns+"/cli")))
	})

	It("files a person's work for the person, in Later, never ready, after the work it shows", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		plan := orderedPlan(ns)
		plan["plan/issues/02-cli.md"] = scaffold.File{Data: []byte(`---
title: Record the CLI in a terminal for the README
labels: [kind/docs, kind/by-hand, status/ready]
after: [1]
---
## Goal

The README shows the CLI checking a file.

## Acceptance criteria

- [ ] The README has a recording of the CLI reporting a broken rule
`)}
		planRound(ns, 1, 1, plan)

		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}, orderWait).Should(Succeed())
		core, cli := gitFake.issue(ns, "core", 1), gitFake.issue(ns, "cli", 1)
		Expect(core.milestone).To(BeEmpty())
		Expect(cli.Labels).To(ConsistOf(model.LabelNeedsHuman, "kind/docs", model.LabelByHand),
			"no Crew may pick a person's work")
		Expect(cli.milestone).To(Equal(model.FollowUpMilestone))
		Expect(cli.blockers).To(Equal([]string{ns + "/core#1"}))

		By("listing it with the Brief's failed issues, for the person")
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Failed).To(ContainElement(And(HaveField("Container", "cli"),
				HaveField("Number", int32(1)), HaveField("Kind", model.FailedByHand))))
		}, orderWait).Should(Succeed())
	})

	It("finishes an issue a restart left filed but not ready, adding nothing twice", func() {
		createFactory(ns)
		briefAgents(ns, 3)

		By("finding the core's issue filed, and the CLI's filed with its dependency but not labelled ready")
		plan, err := model.ParsePlan(planFiles(orderedPlan(ns)))
		Expect(err).NotTo(HaveOccurred())
		gitFake.addIssue(ns, "core", "The checker", model.BriefIssueBody(plan.Issues[0], "cel-lens", 1),
			[]string{model.LabelReady, "kind/feature"})
		gitFake.addIssue(ns, "cli", "Check a file from the command line",
			model.BriefIssueBody(plan.Issues[1], "cel-lens", 1,
				model.BuildsOn{Repository: ns + "/core", Number: 1, Title: "The checker"}),
			[]string{"kind/feature"}, ns+"/core#1")
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, orderedPlan(ns))

		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
			g.Expect(condition(b.Status.Conditions, c4v1alpha1.ConditionPlanned)).
				To(HaveField("Status", metav1.ConditionTrue))
		}, orderWait).Should(Succeed())
		Expect(gitFake.openIssues(ns, "core")).To(HaveLen(1))
		Expect(gitFake.openIssues(ns, "cli")).To(HaveLen(1))
		cli := gitFake.issue(ns, "cli", 1)
		Expect(cli.blockers).To(Equal([]string{ns + "/core#1"}))
		Expect(cli.Labels).To(ContainElement(model.LabelReady))
		Expect(getBrief(Default, ns).Status.Issues).To(HaveLen(2))
	})
})
