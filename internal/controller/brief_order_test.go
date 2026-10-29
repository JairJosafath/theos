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

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// engineContainer is a Container of the cel-lens system, as a plan holds it.
func engineContainer(name, displayName string) string {
	return fmt.Sprintf(`---
apiVersion: c4.factory.io/v1beta1
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
apiVersion: c4.factory.io/v1beta1
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
// 90 s ran out too, cli still not made, in a whole shard though never with the
// spec alone (#1990): the first wait says where the Brief stood, so the next
// failure names the stall.
const orderWait = 90 * time.Second

// briefStand says where Brief cel-lens and its round 1 plan Task stand, for a
// wait that times out before the round applied (#1990).
func briefStand(ns string) string {
	var b c4v1beta1.Brief
	if err := k8sClient.Get(ctx, key(ns, "cel-lens"), &b); err != nil {
		return "Brief: " + err.Error()
	}
	out := fmt.Sprintf("Brief phase %s round %d", b.Status.Phase, b.Status.Round)
	for _, c := range b.Status.Conditions {
		out += fmt.Sprintf("; %s=%s %s: %s", c.Type, c.Status, c.Reason, c.Message)
	}
	var t c4v1beta1.Task
	if err := k8sClient.Get(ctx, key(ns, "cel-lens-plan-1"), &t); err != nil {
		out += "; Task: " + err.Error()
	} else {
		out += fmt.Sprintf("; Task phase %s try %d, annotations %v", t.Status.Phase, t.Status.Round, t.Annotations)
	}
	return out + fmt.Sprintf("; Brief events %v", briefEvents(Default, ns))
}

var _ = Describe("Brief issue order", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	// #1848, #1990: under a whole suite's load the round's Containers came late.
	It("files an issue after the ones it builds on, depending on them, and labels it ready last", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		release := gitFake.Hang(ns, "core")
		DeferCleanup(release)
		gitFake.FailDependencies(ns, "cli", errors.New("gitea: dependencies are down"))
		DeferCleanup(func() { gitFake.FailDependencies(ns, "cli", nil) })
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, orderedPlan(ns))

		By("holding the CLI's issue while the core it builds on isn't Ready, though the CLI is")
		stand := func() string { return briefStand(ns) }
		Eventually(func(g Gomega) {
			var cli c4v1beta1.Container
			g.Expect(k8sClient.Get(ctx, key(ns, "cli"), &cli)).To(Succeed(), stand)
			g.Expect(cli.Status.Phase).To(Equal(c4v1beta1.ContainerReady), stand)
			cond := condition(getBrief(g, ns).Status.Conditions, c4v1beta1.ConditionPlanned)
			g.Expect(cond).To(And(HaveField("Reason", "Applying"),
				HaveField("Message", ContainSubstring("waits for Container core"))))
		}, orderWait).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.OpenIssues(ns, "cli")).To(BeEmpty())
			g.Expect(getBrief(g, ns).Status.Issues).To(BeEmpty())
		}, "1s").Should(Succeed())

		By("filing the core's issue once the core is Ready, then the CLI's, not ready while its dependency fails")
		release()
		Eventually(func(g Gomega) {
			g.Expect(gitFake.OpenIssues(ns, "core")).To(HaveLen(1))
			g.Expect(gitFake.OpenIssues(ns, "cli")).To(HaveLen(1))
			cond := condition(getBrief(g, ns).Status.Conditions, c4v1beta1.ConditionPlanned)
			g.Expect(cond).To(And(HaveField("Reason", model.ReasonApplyFailed),
				HaveField("Message", ContainSubstring("dependencies are down"))))
		}, orderWait).Should(Succeed())
		cli := gitFake.Issue(ns, "cli", 1)
		Expect(cli.Labels).NotTo(ContainElement(model.LabelReady), "no Crew may take it before it depends on the core's")
		Expect(cli.Blockers).To(BeEmpty())
		Expect(getBrief(Default, ns).Status.Issues).To(HaveLen(1), "the CLI's isn't recorded until it is done")

		By("making it depend on the core's issue, then labelling it ready")
		gitFake.FailDependencies(ns, "cli", nil)
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}, orderWait).Should(Succeed())
		cli = gitFake.Issue(ns, "cli", 1)
		Expect(cli.Blockers).To(Equal([]string{ns + "/core#1"}))
		Expect(cli.Labels).To(ContainElements(model.LabelReady, "kind/feature"))
		Expect(cli.Body).To(ContainSubstring("\nBuilds on " + ns + "/core#1 (The checker).\n"))
		Expect(model.HasIssueMarker(cli.Body, "cel-lens", 1, 2)).To(BeTrue())
		Expect(gitFake.OpenIssues(ns, "cli")).To(HaveLen(1))
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
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}, orderWait).Should(Succeed())
		core, cli := gitFake.Issue(ns, "core", 1), gitFake.Issue(ns, "cli", 1)
		Expect(core.InMilestone).To(BeEmpty())
		Expect(cli.Labels).To(ConsistOf(model.LabelNeedsHuman, "kind/docs", model.LabelByHand),
			"no Crew may pick a person's work")
		Expect(cli.InMilestone).To(Equal(model.FollowUpMilestone))
		Expect(cli.Blockers).To(Equal([]string{ns + "/core#1"}))

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
		gitFake.AddIssue(ns, "core", "The checker", model.BriefIssueBody(plan.Issues[0], "cel-lens", 1),
			[]string{model.LabelReady, "kind/feature"})
		gitFake.AddIssue(ns, "cli", "Check a file from the command line",
			model.BriefIssueBody(plan.Issues[1], "cel-lens", 1,
				model.BuildsOn{Repository: ns + "/core", Number: 1, Title: "The checker"}),
			[]string{"kind/feature"}, ns+"/core#1")
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, orderedPlan(ns))

		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
			g.Expect(condition(b.Status.Conditions, c4v1beta1.ConditionPlanned)).
				To(HaveField("Status", metav1.ConditionTrue))
		}, orderWait).Should(Succeed())
		Expect(gitFake.OpenIssues(ns, "core")).To(HaveLen(1))
		Expect(gitFake.OpenIssues(ns, "cli")).To(HaveLen(1))
		cli := gitFake.Issue(ns, "cli", 1)
		Expect(cli.Blockers).To(Equal([]string{ns + "/core#1"}))
		Expect(cli.Labels).To(ContainElement(model.LabelReady))
		Expect(getBrief(Default, ns).Status.Issues).To(HaveLen(2))
	})
})
