/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

const briefRepo = "brief-cel-lens"

// briefAgents makes the Agents a Brief names by default: the planner with
// rounds tries, the implementer and the reviewer.
func briefAgents(ns string, rounds int32) {
	GinkgoHelper()
	for _, name := range []string{"planner", "implementer", "reviewer"} {
		mustCreate(&c4v1alpha1.Agent{ObjectMeta: objectMeta(ns, name),
			Spec: c4v1alpha1.AgentSpec{Image: "c4factory/agent-go:dev", MaxRounds: rounds}})
	}
}

// projectDoc is the namespace's Project as a plan holds it, unchanged.
func projectDoc(ns string) string {
	return fmt.Sprintf(`apiVersion: c4.factory.io/v1alpha1
kind: Project
metadata:
  name: %s
spec:
  displayName: Test story
`, ns)
}

// linterDocs are the objects of the cel-lens plan: its system, the Container
// linter, the DataStore rules-db and the Relationship between them.
const linterDocs = `---
apiVersion: c4.factory.io/v1alpha1
kind: SoftwareSystem
metadata:
  name: cel-lens
spec:
  displayName: CEL lens
---
apiVersion: c4.factory.io/v1alpha1
kind: Container
metadata:
  name: linter
spec:
  softwareSystemRef: {name: cel-lens}
  displayName: Linter
  type: service
  gitProviderRef: {name: gitea}
  frameworkRef: {name: python-fastapi}
  computeRef: {name: small}
  deploymentRef: {name: k8s-manifests}
---
apiVersion: c4.factory.io/v1alpha1
kind: DataStore
metadata:
  name: rules-db
spec:
  softwareSystemRef: {name: cel-lens}
  connectorRef: {name: postgres}
---
apiVersion: c4.factory.io/v1alpha1
kind: Relationship
metadata:
  name: linter-to-rules-db
spec:
  source: {kind: Container, name: linter}
  target: {kind: DataStore, name: rules-db}
  description: Keeps the rules
`

// linterIssue is issue n of the plan, for the linter.
func linterIssue(n int) string {
	return fmt.Sprintf(`---
title: Lint rule %d
labels: [kind/feature, status/proposed]
---
## Goal

Rule %d is linted.

## Acceptance criteria

- [ ] The linter reports rule %d
`, n, n, n)
}

// linterPlan is the plan that makes the linter with issues issues.
func linterPlan(ns string, issues int) scaffold.Tree {
	files := scaffold.Tree{
		model.PlanModelFile:   {Data: []byte(projectDoc(ns) + linterDocs)},
		model.PlanChoicesFile: {Data: []byte("- A service lints the rules\n- Postgres keeps them\n")},
	}
	for n := 1; n <= issues; n++ {
		files[fmt.Sprintf("plan/issues/%02d-linter.md", n)] = scaffold.File{Data: []byte(linterIssue(n))}
	}
	return files
}

// quietPlan is a plan that changes nothing, with one choice.
func quietPlan(ns string) scaffold.Tree {
	return scaffold.Tree{
		model.PlanModelFile:   {Data: []byte(projectDoc(ns))},
		model.PlanChoicesFile: {Data: []byte("- Nothing to change\n")},
	}
}

func getBrief(g Gomega, ns string) *c4v1alpha1.Brief {
	brief := &c4v1alpha1.Brief{}
	g.Expect(k8sClient.Get(ctx, key(ns, "cel-lens"), brief)).To(Succeed())
	return brief
}

// planRound plays the planner of Brief cel-lens's round on its try: it pushes
// files to plan/<round> and ends the try's sandbox with the commit.
func planRound(ns string, round, try int, files scaffold.Tree) string {
	GinkgoHelper()
	task := fmt.Sprintf("cel-lens-plan-%d", round)
	sandbox := fmt.Sprintf("%s-r%d-plan", task, try)
	Eventually(func(g Gomega) { sandboxExists(g, ns, sandbox) }).Should(Succeed())
	sha := gitFake.pushPlan(ns, briefRepo, round, files)
	finishSandbox(ns, sandbox, task, &agentrunner.Result{Role: "plan", Commit: sha,
		Summary: fmt.Sprintf("Planned round %d.", round)}, "")
	return sha
}

// briefEvents returns the reasons of the Events about Brief cel-lens.
func briefEvents(g Gomega, ns string) []string {
	var events eventsv1.EventList
	g.Expect(k8sClient.List(ctx, &events, client.InNamespace(ns))).To(Succeed())
	var out []string
	for _, e := range events.Items {
		if e.Regarding.Kind == "Brief" && e.Regarding.Name == "cel-lens" {
			out = append(out, e.Reason)
		}
	}
	return out
}

// ownedByBrief reports whether obj has an ownerReference to a Brief.
func ownedByBrief(obj client.Object) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Kind == "Brief" {
			return true
		}
	}
	return false
}

// Step 5 of docs/plans/brief.md: rounds, applying and filing.
var _ = Describe("Brief rounds", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("plans the idea, applies the model, makes the Crew and files the issues", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)

		By("starting Task cel-lens-plan-1, owned by the Brief")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "cel-lens-plan-1")
			g.Expect(controlledBy(task, getBrief(g, ns))).To(BeTrue())
			g.Expect(task.Spec.Kind).To(Equal(c4v1alpha1.TaskPlan))
			g.Expect(task.Spec.AgentRef.Name).To(Equal("planner"))
			g.Expect(task.Labels).To(And(HaveKeyWithValue(c4v1alpha1.LabelBrief, "cel-lens"),
				HaveKeyWithValue(c4v1alpha1.LabelBriefRound, "1")))
			b := getBrief(g, ns)
			g.Expect(b.Status.Round).To(Equal(int32(1)))
			g.Expect(b.Status.Phase).To(Equal(c4v1alpha1.BriefPlanning))
		}).Should(Succeed())

		sha := planRound(ns, 1, 1, linterPlan(ns, 2))

		By("applying the plan, waiting for the linter to be Ready, then making its Crew and filing its issues")
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
			g.Expect(condition(b.Status.Conditions, c4v1alpha1.ConditionPlanned)).To(And(
				HaveField("Status", metav1.ConditionTrue), HaveField("Reason", model.ReasonApplied)))
		}).Should(Succeed())
		brief = getBrief(Default, ns)
		Expect(brief.Status.History).To(Equal([]c4v1alpha1.BriefRound{{Round: 1, Summary: "Planned round 1.",
			Choices: []string{"A service lints the rules", "Postgres keeps them"}}}))
		Expect(brief.Status.Choices).To(Equal([]string{"A service lints the rules", "Postgres keeps them"}))
		Expect(brief.Status.PromptsPlanned).To(BeZero())
		Expect(brief.Status.Created).To(ConsistOf(
			c4v1alpha1.BriefObject{Kind: "SoftwareSystem", Name: "cel-lens"},
			c4v1alpha1.BriefObject{Kind: "Container", Name: "linter"},
			c4v1alpha1.BriefObject{Kind: "DataStore", Name: "rules-db"},
			c4v1alpha1.BriefObject{Kind: "Relationship", Name: "linter-to-rules-db"},
			c4v1alpha1.BriefObject{Kind: "Crew", Name: "linter"},
		))
		Expect(brief.Status.Issues).To(HaveLen(2))
		for i, issue := range brief.Status.Issues {
			Expect(issue.Repository).To(Equal(ns + "/linter"))
			Expect(issue.Title).To(Equal(fmt.Sprintf("Lint rule %d", i+1)))
			filed := gitFake.issue(ns, "linter", int(issue.Number))
			Expect(model.HasIssueMarker(filed.Body, "cel-lens", 1, i+1)).To(BeTrue())
			Expect(filed.Labels).To(ContainElement("kind/feature"))
			Expect(filed.Labels).NotTo(ContainElement("status/proposed"))
			Expect(filed.milestone).To(BeEmpty())
		}

		By("accepting the plan on the Task, and merging it into the repository's main")
		task := getTask(Default, ns, "cel-lens-plan-1")
		Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskMerged))
		Expect(task.Annotations).To(And(HaveKeyWithValue(c4v1alpha1.AnnotationPlanChecked, sha),
			Not(HaveKey(c4v1alpha1.AnnotationPlanRefusals))))
		main := gitFake.mainFiles(ns, briefRepo)
		Expect(main).To(HaveKey("plan/issues/02-linter.md"))
		Expect(string(main[model.PlanChoicesFile].Data)).To(ContainSubstring("Postgres keeps them"))

		By("scaffolding the linter with its DataStore's Connector, the Relationship being there first")
		container := &c4v1alpha1.Container{}
		Expect(k8sClient.Get(ctx, key(ns, "linter"), container)).To(Succeed())
		Expect(container.Status.AppliedConnectors).To(ConsistOf(c4v1alpha1.AppliedConnector{
			Relationship: "linter-to-rules-db", DataStore: "rules-db", Connector: "postgres"}))
		rel := &c4v1alpha1.Relationship{}
		Expect(k8sClient.Get(ctx, key(ns, "linter-to-rules-db"), rel)).To(Succeed())
		Expect(condition(rel.Status.Conditions, c4v1alpha1.ConditionApplied)).
			NotTo(HaveField("Reason", c4v1alpha1.ReasonAddedAfterScaffold))

		By("giving the Crew the Brief's Agents, one Task at a time")
		crew := &c4v1alpha1.Crew{}
		Expect(k8sClient.Get(ctx, key(ns, "linter"), crew)).To(Succeed())
		Expect(crew.Spec.ContainerRef.Name).To(Equal("linter"))
		Expect(crew.Spec.Implementer.Name).To(Equal("implementer"))
		Expect(crew.Spec.Reviewer.Name).To(Equal("reviewer"))
		Expect(crew.Spec.Parallel).To(Equal(int32(1)))
		Expect(crew.Spec.TagReleases || crew.Spec.PrepareReleases).To(BeFalse())

		By("labelling what it made with the Brief and the round, owned by none of it (B16)")
		system := &c4v1alpha1.SoftwareSystem{}
		Expect(k8sClient.Get(ctx, key(ns, "cel-lens"), system)).To(Succeed())
		store := &c4v1alpha1.DataStore{}
		Expect(k8sClient.Get(ctx, key(ns, "rules-db"), store)).To(Succeed())
		for _, obj := range []client.Object{system, container, store, rel, crew} {
			Expect(obj.GetLabels()).To(And(HaveKeyWithValue(c4v1alpha1.LabelBrief, "cel-lens"),
				HaveKeyWithValue(c4v1alpha1.LabelBriefRound, "1")), obj.GetName())
			Expect(ownedByBrief(obj)).To(BeFalse(), obj.GetName())
		}
		project := &c4v1alpha1.Project{}
		Expect(k8sClient.Get(ctx, key(ns, ns), project)).To(Succeed())
		Expect(project.Labels).NotTo(HaveKey(c4v1alpha1.LabelBrief), "an object it didn't make")

		Eventually(func(g Gomega) { g.Expect(briefEvents(g, ns)).To(ContainElement("Planned")) }).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Round).To(Equal(int32(1)), "no prompt waits")
		}, "1s").Should(Succeed())
	})

	It("takes a prompt appended during a round in the next round, and one appended while idle at once", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)
		Eventually(func(g Gomega) { getTask(g, ns, "cel-lens-plan-1") }).Should(Succeed())

		By("leaving a prompt appended during round 1 for round 2")
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Support VAP files first."} })).To(Succeed())
		Consistently(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Round).To(Equal(int32(1)))
			g.Expect(b.Status.RoundPrompts).To(BeZero())
		}, "1s").Should(Succeed())
		planRound(ns, 1, 1, quietPlan(ns))

		By("starting round 2 with it once round 1 applied")
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.History).To(HaveLen(1))
			g.Expect(b.Status.PromptsPlanned).To(BeZero())
			g.Expect(b.Status.Round).To(Equal(int32(2)))
			g.Expect(b.Status.RoundPrompts).To(Equal(int32(1)))
			g.Expect(getTask(g, ns, "cel-lens-plan-2").Labels).To(HaveKeyWithValue(c4v1alpha1.LabelBriefRound, "2"))
		}).Should(Succeed())
		planRound(ns, 2, 1, quietPlan(ns))
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
			g.Expect(b.Status.PromptsPlanned).To(Equal(int32(1)))
		}).Should(Succeed())
		Expect(string(gitFake.mainFiles(ns, briefRepo)[model.BriefFile].Data)).
			To(ContainSubstring("1. (planned) Support VAP files first.\n"), "brief.md marks it planned")

		By("starting round 3 at once for a prompt appended while idle")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = append(brief.Spec.Prompts, "And a CLI.") })).To(Succeed())
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Round).To(Equal(int32(3)))
			g.Expect(b.Status.RoundPrompts).To(Equal(int32(2)))
			g.Expect(b.Status.Phase).To(Equal(c4v1alpha1.BriefPlanning))
			getTask(g, ns, "cel-lens-plan-3")
		}).Should(Succeed())
	})

	It("refuses a plan with the reasons, and fails the round after the planner's rounds", func() {
		createFactory(ns)
		briefAgents(ns, 2)
		mustCreate(newBrief(ns, "cel-lens"))
		bad := linterPlan(ns, 1)
		bad[model.PlanChoicesFile] = scaffold.File{Data: []byte("\n")}
		bad["plan/issues/01-linter.md"] = scaffold.File{Data: []byte("---\ntitle: Lint\n---\nJust lint.\n")}

		planRound(ns, 1, 1, bad)
		By("starting the Task's next try with every reason")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "cel-lens-plan-1")
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Feedback).To(And(ContainSubstring("plan/choices.md: is empty"),
				ContainSubstring("MissingAcceptanceCriteria")))
		}).Should(Succeed())
		Eventually(func(g Gomega) { g.Expect(briefEvents(g, ns)).To(ContainElement("PlanRefused")) }).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "linter"), &c4v1alpha1.Container{}))).
			To(BeTrue(), "a refused plan applies nothing")

		By("failing the Brief when the last try is refused too")
		delete(bad, model.PlanModelFile)
		planRound(ns, 1, 2, bad)
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1alpha1.BriefFailed))
			g.Expect(condition(b.Status.Conditions, c4v1alpha1.ConditionPlanned)).To(And(
				HaveField("Status", metav1.ConditionFalse), HaveField("Reason", model.ReasonRoundFailed),
				HaveField("Message", And(HavePrefix("Round 1 failed: "), ContainSubstring("Failed after 2 rounds")))))
			g.Expect(b.Status.History).To(HaveLen(1))
		}).Should(Succeed())

		By("starting round 2 for a new prompt")
		brief := getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Keep it simple."} })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanning))
			getTask(g, ns, "cel-lens-plan-2")
		}).Should(Succeed())
	})

	It("asks the person when the planner needs input, applying nothing until a prompt answers", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		asks := linterPlan(ns, 1)
		asks[model.PlanNeedsInputFile] = scaffold.File{Data: []byte("Which CRDs should it lint first?\n")}
		planRound(ns, 1, 1, asks)

		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1alpha1.BriefNeedsInput))
			g.Expect(condition(b.Status.Conditions, c4v1alpha1.ConditionNeedsInput)).To(And(
				HaveField("Status", metav1.ConditionTrue), HaveField("Reason", model.ReasonPlannerNeedsInput),
				HaveField("Message", "Which CRDs should it lint first?")))
			g.Expect(briefEvents(g, ns)).To(ContainElement("NeedsInput"))
		}).Should(Succeed())
		Expect(getTask(Default, ns, "cel-lens-plan-1").Status.Phase).To(Equal(c4v1alpha1.TaskMerged))
		Consistently(func(g Gomega) {
			err := k8sClient.Get(ctx, key(ns, "linter"), &c4v1alpha1.Container{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "nothing is applied")
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefNeedsInput), "the operator keeps the planner's question")
		}, "2s").Should(Succeed())

		By("starting the next round on a prompt, which clears the question")
		brief := getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{"The Gateway API CRDs."} })).To(Succeed())
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1alpha1.BriefPlanning))
			g.Expect(condition(b.Status.Conditions, c4v1alpha1.ConditionNeedsInput)).
				To(HaveField("Status", metav1.ConditionFalse))
		}).Should(Succeed())
	})

	It("waits for a missing Agent or catalog entry, applying nothing, and resumes once it is there", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		brief := newBrief(ns, "cel-lens")
		brief.Spec.Agents.Implementer.Name = "late-implementer"
		mustCreate(brief)
		planRound(ns, 1, 1, linterPlan(ns, 0))

		By("naming the missing implementer, with nothing applied")
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1alpha1.BriefNeedsInput))
			g.Expect(condition(b.Status.Conditions, c4v1alpha1.ConditionNeedsInput)).To(And(
				HaveField("Reason", c4v1alpha1.ReasonNotFound),
				HaveField("Message", HavePrefix("Agent late-implementer is missing"))))
		}).Should(Succeed())
		Expect(getTask(Default, ns, "cel-lens-plan-1").Status.Phase).To(Equal(c4v1alpha1.TaskMerged))
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "cel-lens"), &c4v1alpha1.SoftwareSystem{}))).
			To(BeTrue(), "nothing is applied")

		By("naming a catalog entry the plan needs that went since it was checked")
		Expect(k8sClient.Delete(ctx, &c4v1alpha1.Connector{ObjectMeta: objectMeta(ns, "postgres")})).To(Succeed())
		mustCreate(&c4v1alpha1.Agent{ObjectMeta: objectMeta(ns, "late-implementer"),
			Spec: c4v1alpha1.AgentSpec{Image: "c4factory/agent-go:dev"}})
		Eventually(func(g Gomega) {
			g.Expect(condition(getBrief(g, ns).Status.Conditions, c4v1alpha1.ConditionNeedsInput)).
				To(HaveField("Message", HavePrefix("The plan of round 1 needs Connector postgres, missing")))
		}).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "cel-lens"), &c4v1alpha1.SoftwareSystem{}))).
			To(BeTrue(), "nothing is applied")

		By("resuming the round once it is there")
		mustCreate(&c4v1alpha1.Connector{ObjectMeta: objectMeta(ns, "postgres"),
			Spec: newCatalogSpec(templatesURL+"postgres", "v1.1.0", c4v1alpha1.ContainerTypeService)})
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
			g.Expect(condition(b.Status.Conditions, c4v1alpha1.ConditionNeedsInput)).
				To(HaveField("Status", metav1.ConditionFalse))
		}).Should(Succeed())
		crew := &c4v1alpha1.Crew{}
		Expect(k8sClient.Get(ctx, key(ns, "linter"), crew)).To(Succeed())
		Expect(crew.Spec.Implementer.Name).To(Equal("late-implementer"))
	})

	It("finishes a round a restart left halfway, with no duplicate issue or Crew", func() {
		createFactory(ns)
		briefAgents(ns, 3)

		By("finding two of the four issues and the Crew already there, as a restart would")
		for n := 1; n <= 2; n++ {
			issue := model.PlanIssue{N: n, Body: "Filed before the restart."}
			gitFake.addIssue(ns, "linter", fmt.Sprintf("Lint rule %d", n), model.BriefIssueBody(issue, "cel-lens", 1),
				[]string{"status/ready"})
		}
		mustCreate(&c4v1alpha1.Crew{ObjectMeta: objectMeta(ns, "linter"), Spec: c4v1alpha1.CrewSpec{
			ContainerRef: c4v1alpha1.LocalRef{Name: "linter"},
			Implementer:  c4v1alpha1.LocalRef{Name: "implementer"}, Reviewer: c4v1alpha1.LocalRef{Name: "reviewer"},
			Parallel: 2}})
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, linterPlan(ns, 4))

		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}).Should(Succeed())
		brief := getBrief(Default, ns)
		Expect(brief.Status.Issues).To(HaveLen(4))
		issues := gitFake.openIssues(ns, "linter")
		Expect(issues).To(HaveLen(4))
		for n := 1; n <= 4; n++ {
			marked := 0
			for _, issue := range issues {
				if model.HasIssueMarker(issue.Body, "cel-lens", 1, n) {
					marked++
				}
			}
			Expect(marked).To(Equal(1), "issue %d", n)
		}
		var crews c4v1alpha1.CrewList
		Expect(k8sClient.List(ctx, &crews, client.InNamespace(ns))).To(Succeed())
		Expect(crews.Items).To(HaveLen(1))
		Expect(crews.Items[0].Spec.Parallel).To(Equal(int32(2)), "the Crew there is left alone")
	})
})
