/*
Copyright 2026 The theos Authors.

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
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/scaffold"
)

const briefRepo = "brief-cel-lens"

// briefAgents makes the Agents a Brief names by default: the planner with
// rounds tries, the implementer and the reviewer.
func briefAgents(ns string, rounds int32) {
	GinkgoHelper()
	for _, name := range []string{"planner", "implementer", "reviewer"} {
		mustCreate(&c4v1beta1.Agent{ObjectMeta: objectMeta(ns, name),
			Spec: c4v1beta1.AgentSpec{Image: "theos/agent-go:dev", MaxRounds: rounds}})
	}
}

// projectDoc is the namespace's Project as a plan holds it, unchanged.
func projectDoc(ns string) string {
	return fmt.Sprintf(`apiVersion: theos.factory.io/v1beta1
kind: Project
metadata:
  name: %s
spec:
  displayName: Test story
`, ns)
}

// linterDocs are the objects of the cel-lens plan: the Person rule-author, its
// system, the Container linter, the DataStore rules-db and the Relationship
// between them. A real planner's model starts with a Person, which the
// operator's role must let it create (#371).
const linterDocs = `---
apiVersion: theos.factory.io/v1beta1
kind: Person
metadata:
  name: rule-author
spec:
  displayName: Rule author
---
apiVersion: theos.factory.io/v1beta1
kind: SoftwareSystem
metadata:
  name: cel-lens
spec:
  displayName: CEL lens
---
apiVersion: theos.factory.io/v1beta1
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
apiVersion: theos.factory.io/v1beta1
kind: DataStore
metadata:
  name: rules-db
spec:
  softwareSystemRef: {name: cel-lens}
  connectorRef: {name: postgres}
---
apiVersion: theos.factory.io/v1beta1
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
		model.PlanSummaryFile: {Data: []byte("Planned cel-lens, a linter for CEL rules:\none Container and its first issues.\n")},
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

func getBrief(g Gomega, ns string) *c4v1beta1.Brief {
	brief := &c4v1beta1.Brief{}
	g.Expect(k8sClient.Get(ctx, key(ns, "cel-lens"), brief)).To(Succeed())
	return brief
}

// planRound plays the planner of Brief cel-lens's round on its try: it pushes
// files to plan/<round> and ends the try's sandbox with the commit.
func planRound(ns string, round, try int, files scaffold.Tree) string {
	GinkgoHelper()
	return planRoundCosting(ns, round, try, files, new(planCost))
}

// planCost is what planRound's planner says each try cost, and planTokens
// what it used.
const planCost int64 = 120

var planTokens = model.TokenUsage{Input: 6, Output: 900, CacheRead: 40000, CacheWrite: 12000, LargestSession: 35000}

// planRoundCosting is planRound with a planner that says the try cost
// milliUSD and used planTokens, or says neither when milliUSD is nil.
func planRoundCosting(ns string, round, try int, files scaffold.Tree, milliUSD *int64) string {
	GinkgoHelper()
	task := fmt.Sprintf("cel-lens-plan-%d", round)
	sandbox := fmt.Sprintf("%s-r%d-plan", task, try)
	// The plan's Sandbox comes at the end of a chain of reconciles (the Brief's
	// repo, its plan Task, the Task's Sandbox): give it a whole round, as the
	// Brief specs do, not the suite's 10s default, which a loaded device ran out (#942).
	Eventually(func(g Gomega) { sandboxExists(g, ns, sandbox) }).Should(Succeed())
	sha := gitFake.PushPlan(ns, briefRepo, round, files)
	result := &agentrunner.Result{Role: "plan", Commit: sha,
		Summary:      "I've rewritten the plan in `plan/` with the refusal fixed. Nothing is committed or pushed.",
		CostMilliUSD: milliUSD}
	if milliUSD != nil {
		result.Tokens = new(planTokens)
	}
	finishSandbox(ns, sandbox, task, result, "")
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

// briefEventNotes returns the notes of the Events about Brief cel-lens with reason.
func briefEventNotes(g Gomega, ns, reason string) []string {
	var events eventsv1.EventList
	g.Expect(k8sClient.List(ctx, &events, client.InNamespace(ns))).To(Succeed())
	var out []string
	for _, e := range events.Items {
		if e.Regarding.Kind == "Brief" && e.Regarding.Name == "cel-lens" && e.Reason == reason {
			out = append(out, e.Note)
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

// Step 5 of docs/plans/done/brief.md: rounds, applying and filing.
var _ = Describe("Brief rounds", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("says why a round's Container stopped while it waits for it, and goes on once it is fixed (#1941)", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		var framework c4v1beta1.Framework
		Expect(k8sClient.Get(ctx, key(ns, "python-fastapi"), &framework)).To(Succeed())
		pinned := framework.Spec.Template.Ref
		Expect(patch(&framework, func() { framework.Spec.Template.Ref = "main" })).To(Succeed())
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, linterPlan(ns, 1))

		By("naming the linter's reason and message after its phase, and keeping the round waiting")
		Eventually(func(g Gomega) {
			cond := condition(getBrief(g, ns).Status.Conditions, c4v1beta1.ConditionPlanned)
			g.Expect(cond).To(And(HaveField("Reason", "Applying"), HaveField("Message", And(
				ContainSubstring("Round 1 waits for Container linter (Failed: UnpinnedRef, "),
				ContainSubstring("Framework python-fastapi")))))
		}, orderWait).Should(Succeed())
		Expect(getBrief(Default, ns).Status.Phase).To(Equal(c4v1beta1.BriefApplying), "the round isn't Failed")

		By("going on once the Framework's ref is pinned again")
		Expect(k8sClient.Get(ctx, key(ns, "python-fastapi"), &framework)).To(Succeed())
		Expect(patch(&framework, func() { framework.Spec.Template.Ref = pinned })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}, orderWait).Should(Succeed())
		Expect(gitFake.OpenIssues(ns, "linter")).To(HaveLen(1))
	})

	It("plans the idea, applies the model, makes the Crew and files the issues", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)

		By("starting Task cel-lens-plan-1, owned by the Brief")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "cel-lens-plan-1")
			g.Expect(controlledBy(task, getBrief(g, ns))).To(BeTrue())
			g.Expect(task.Spec.Kind).To(Equal(c4v1beta1.TaskPlan))
			g.Expect(task.Spec.AgentRef.Name).To(Equal("planner"))
			g.Expect(task.Labels).To(And(HaveKeyWithValue(c4v1beta1.LabelBrief, "cel-lens"),
				HaveKeyWithValue(c4v1beta1.LabelBriefRound, "1")))
			b := getBrief(g, ns)
			g.Expect(b.Status.Round).To(Equal(int32(1)))
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanning))
		}).Should(Succeed())

		sha := planRound(ns, 1, 1, linterPlan(ns, 2))

		By("applying the plan, waiting for the linter to be Ready, then making its Crew and filing its issues")
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
			g.Expect(condition(b.Status.Conditions, c4v1beta1.ConditionPlanned)).To(And(
				HaveField("Status", metav1.ConditionTrue), HaveField("Reason", model.ReasonApplied)))
		}).Should(Succeed())
		brief = getBrief(Default, ns)
		Expect(brief.Status.History).To(Equal([]c4v1beta1.BriefRound{{Round: 1,
			Summary: "Planned cel-lens, a linter for CEL rules: one Container and its first issues.",
			Choices: []string{"A service lints the rules", "Postgres keeps them"}, CostMilliUSD: planCost,
			Tokens: new(c4v1beta1.TokenUsage(planTokens)), Outcome: model.ReasonApplied}}))
		Expect(brief.Status.CostMilliUSD).To(Equal(planCost), "what the planner said the round cost")
		Expect(brief.Status.RunsWithoutCost).To(BeZero())
		Expect(brief.Status.Tokens).To(Equal(new(c4v1beta1.TokenUsage(planTokens))), "and used")
		Expect(brief.Status.Choices).To(Equal([]string{"A service lints the rules", "Postgres keeps them"}))
		Expect(brief.Status.PromptsPlanned).To(BeZero())
		Expect(brief.Status.Created).To(ConsistOf(
			c4v1beta1.BriefObject{Kind: "Person", Name: "rule-author"},
			c4v1beta1.BriefObject{Kind: "SoftwareSystem", Name: "cel-lens"},
			c4v1beta1.BriefObject{Kind: "Container", Name: "linter"},
			c4v1beta1.BriefObject{Kind: "DataStore", Name: "rules-db"},
			c4v1beta1.BriefObject{Kind: "Relationship", Name: "linter-to-rules-db"},
			c4v1beta1.BriefObject{Kind: "Crew", Name: "linter"},
		))
		Expect(brief.Status.Issues).To(HaveLen(2))
		for i, issue := range brief.Status.Issues {
			Expect(issue.Repository).To(Equal(ns + "/linter"))
			Expect(issue.Title).To(Equal(fmt.Sprintf("Lint rule %d", i+1)))
			filed := gitFake.Issue(ns, "linter", int(issue.Number))
			Expect(model.HasIssueMarker(filed.Body, "cel-lens", 1, i+1)).To(BeTrue())
			Expect(filed.Labels).To(ContainElement("kind/feature"))
			Expect(filed.Labels).NotTo(ContainElement("status/proposed"))
			Expect(filed.InMilestone).To(BeEmpty())
		}

		By("accepting the plan on the Task, and merging it into the repository's main")
		task := getTask(Default, ns, "cel-lens-plan-1")
		Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
		Expect(task.Annotations).To(And(HaveKeyWithValue(c4v1beta1.AnnotationPlanChecked, sha),
			Not(HaveKey(c4v1beta1.AnnotationPlanRefusals))))
		main := gitFake.MainFiles(ns, briefRepo)
		Expect(main).To(HaveKey("plan/issues/02-linter.md"))
		Expect(string(main[model.PlanChoicesFile].Data)).To(ContainSubstring("Postgres keeps them"))

		By("scaffolding the linter with its DataStore's Connector, the Relationship being there first")
		container := &c4v1beta1.Container{}
		Expect(k8sClient.Get(ctx, key(ns, "linter"), container)).To(Succeed())
		Expect(container.Status.AppliedConnectors).To(ConsistOf(c4v1beta1.AppliedConnector{
			Relationship: "linter-to-rules-db", DataStore: "rules-db", Connector: "postgres"}))
		rel := &c4v1beta1.Relationship{}
		Expect(k8sClient.Get(ctx, key(ns, "linter-to-rules-db"), rel)).To(Succeed())
		Expect(condition(rel.Status.Conditions, c4v1beta1.ConditionApplied)).
			NotTo(HaveField("Reason", c4v1beta1.ReasonAddedAfterScaffold))

		By("giving the Crew the Brief's Agents, three Tasks at a time by default")
		crew := &c4v1beta1.Crew{}
		Expect(k8sClient.Get(ctx, key(ns, "linter"), crew)).To(Succeed())
		Expect(crew.Spec.ContainerRef.Name).To(Equal("linter"))
		Expect(crew.Spec.Implementer.Name).To(Equal("implementer"))
		Expect(crew.Spec.Reviewer.Name).To(Equal("reviewer"))
		Expect(crew.Spec.Parallel).To(Equal(model.BriefParallelDefault))
		Expect(crew.Spec.TagReleases || crew.Spec.PrepareReleases).To(BeFalse())

		By("labelling what it made with the Brief and the round, owned by none of it (B16)")
		person := &c4v1beta1.Person{}
		Expect(k8sClient.Get(ctx, key(ns, "rule-author"), person)).To(Succeed())
		system := &c4v1beta1.SoftwareSystem{}
		Expect(k8sClient.Get(ctx, key(ns, "cel-lens"), system)).To(Succeed())
		store := &c4v1beta1.DataStore{}
		Expect(k8sClient.Get(ctx, key(ns, "rules-db"), store)).To(Succeed())
		for _, obj := range []client.Object{person, system, container, store, rel, crew} {
			Expect(obj.GetLabels()).To(And(HaveKeyWithValue(c4v1beta1.LabelBrief, "cel-lens"),
				HaveKeyWithValue(c4v1beta1.LabelBriefRound, "1")), obj.GetName())
			Expect(ownedByBrief(obj)).To(BeFalse(), obj.GetName())
		}
		project := &c4v1beta1.Project{}
		Expect(k8sClient.Get(ctx, key(ns, ns), project)).To(Succeed())
		Expect(project.Labels).NotTo(HaveKey(c4v1beta1.LabelBrief), "an object it didn't make")

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
		reads := gitFake.HistoryCount(ns, briefRepo)
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Support VAP files first."} })).To(Succeed())
		Eventually(func() int { return gitFake.HistoryCount(ns, briefRepo) }).Should(BeNumerically(">", reads),
			"the workspace was not written again")
		Consistently(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Round).To(Equal(int32(1)))
			g.Expect(b.Status.RoundPrompts).To(BeZero())
			g.Expect(string(gitFake.MainFiles(ns, briefRepo)[model.BriefFile].Data)).NotTo(
				ContainSubstring("Support VAP"), "a try of round 1 that starts now must not plan it")
		}, "1s").Should(Succeed())
		planRound(ns, 1, 1, quietPlan(ns))

		By("starting round 2 with it once round 1 applied")
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.History).To(HaveLen(1))
			g.Expect(b.Status.PromptsPlanned).To(BeZero())
			g.Expect(b.Status.Round).To(Equal(int32(2)))
			g.Expect(b.Status.RoundPrompts).To(Equal(int32(1)))
			g.Expect(getTask(g, ns, "cel-lens-plan-2").Labels).To(HaveKeyWithValue(c4v1beta1.LabelBriefRound, "2"))
		}).Should(Succeed())
		planRound(ns, 2, 1, quietPlan(ns))
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
			g.Expect(b.Status.PromptsPlanned).To(Equal(int32(1)))
		}).Should(Succeed())
		Expect(getBrief(Default, ns).Status.History).To(HaveEach(HaveField("Summary",
			"The model has no Containers; the round files no issues.")), "a plan without summary.md says what it holds")
		// The status says Planned first; the next reconcile rewrites brief.md (#1293).
		Eventually(func() string { return string(gitFake.MainFiles(ns, briefRepo)[model.BriefFile].Data) }).
			Should(ContainSubstring("1. (planned) Support VAP files first.\n"), "brief.md marks it planned")

		By("starting round 3 at once for a prompt appended while idle")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = append(brief.Spec.Prompts, "And a CLI.") })).To(Succeed())
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Round).To(Equal(int32(3)))
			g.Expect(b.Status.RoundPrompts).To(Equal(int32(2)))
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanning))
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
		bad[model.PlanModelFile] = scaffold.File{Data: []byte(projectDoc(ns) + fmt.Sprintf(`---
apiVersion: theos.factory.io/v1beta1
kind: Person
metadata:
  name: rule-author
spec:
  description: %s
`, strings.Repeat("x", 1001)) + linterDocs[strings.Index(linterDocs, "---\napiVersion: theos.factory.io/v1beta1\nkind: SoftwareSystem"):])}

		By("refusing the first try, whose planner doesn't say what it cost")
		planRoundCosting(ns, 1, 1, bad, nil)
		By("starting the Task's next try with every reason")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "cel-lens-plan-1")
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Feedback).To(And(ContainSubstring("plan/choices.md: is empty"),
				ContainSubstring("MissingAcceptanceCriteria"),
				ContainSubstring("Person/rule-author: spec.description is 1001 characters; it may be at most 1000")))
		}).Should(Succeed())
		Eventually(func(g Gomega) { g.Expect(briefEvents(g, ns)).To(ContainElement("PlanRefused")) }).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "linter"), &c4v1beta1.Container{}))).
			To(BeTrue(), "a refused plan applies nothing")

		By("failing the Brief when the last try is refused too")
		delete(bad, model.PlanModelFile)
		planRound(ns, 1, 2, bad)
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefFailed))
			g.Expect(condition(b.Status.Conditions, c4v1beta1.ConditionPlanned)).To(And(
				HaveField("Status", metav1.ConditionFalse), HaveField("Reason", model.ReasonRoundFailed),
				HaveField("Message", And(HavePrefix("Round 1 failed: "), ContainSubstring("Failed after 2 rounds")))))
			tokens := c4v1beta1.TokenUsage(planTokens)
			tokens.RunsWithout = 1
			g.Expect(b.Status.History).To(ConsistOf(And(HaveField("CostMilliUSD", planCost),
				HaveField("RunsWithoutCost", int32(1)), HaveField("Tokens", &tokens))),
				"the second try's cost and tokens, and the first's not reported")
			g.Expect(b.Status.CostMilliUSD).To(Equal(planCost))
			g.Expect(b.Status.RunsWithoutCost).To(Equal(int32(1)))
			g.Expect(b.Status.Tokens).To(Equal(&tokens))
		}).Should(Succeed())

		By("starting round 2 for a new prompt")
		brief := getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Keep it simple."} })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanning))
			getTask(g, ns, "cel-lens-plan-2")
		}).Should(Succeed())
		Expect(string(gitFake.MainFiles(ns, briefRepo)[model.BriefFile].Data)).To(And(
			ContainSubstring("## Idea (**new**)"), ContainSubstring("1. (**new**) Keep it simple.")),
			"a failed round planned nothing, the idea neither")
	})

	It("refuses a Project not named after the namespace before the admission policy would", func() {
		createFactory(ns)
		briefAgents(ns, 2)
		mustCreate(newBrief(ns, "cel-lens"))
		bad := linterPlan(ns, 1)
		bad[model.PlanModelFile] = scaffold.File{Data: []byte(projectDoc("other") + linterDocs)}

		planRound(ns, 1, 1, bad)
		Expect(string(gitFake.MainFiles(ns, briefRepo)[model.BriefFile].Data)).
			To(ContainSubstring("a Project the plan makes is named `"+ns+"`"), "brief.md names the namespace")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "cel-lens-plan-1")
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Feedback).To(ContainSubstring(
				"Project/other: a Project is named after its namespace; name it " + ns))
		}).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "other"), &c4v1beta1.Project{}))).
			To(BeTrue(), "a refused plan applies nothing")
		Expect(getBrief(Default, ns).Status.Phase).NotTo(Equal(c4v1beta1.BriefApplying))
	})

	It("refuses a plan the brief repository doesn't show once the Task waited long enough", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "cel-lens-plan-1-r1-plan") }).Should(Succeed())
		finishSandbox(ns, "cel-lens-plan-1-r1-plan", "cel-lens-plan-1",
			&agentrunner.Result{Role: "plan", Commit: "5a-never-pushed"}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "cel-lens-plan-1")
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Feedback).To(ContainSubstring(
				"The plan isn't on plan/1 at 5a-never-pushed; push it again"))
		}, "15s").Should(Succeed())
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
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefNeedsInput))
			g.Expect(condition(b.Status.Conditions, c4v1beta1.ConditionNeedsInput)).To(And(
				HaveField("Status", metav1.ConditionTrue), HaveField("Reason", model.ReasonPlannerNeedsInput),
				HaveField("Message", "Which CRDs should it lint first?")))
			g.Expect(briefEvents(g, ns)).To(ContainElement("NeedsInput"))
		}).Should(Succeed())
		Expect(getTask(Default, ns, "cel-lens-plan-1").Status.Phase).To(Equal(c4v1beta1.TaskMerged))
		Expect(getBrief(Default, ns).Status.History).To(ConsistOf(HaveField("Summary", "Asked for input before planning.")))
		Consistently(func(g Gomega) {
			err := k8sClient.Get(ctx, key(ns, "linter"), &c4v1beta1.Container{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "nothing is applied")
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefNeedsInput), "the operator keeps the planner's question")
		}, "2s").Should(Succeed())

		By("showing the operator's own wait over the question, then the question again")
		provider := &c4v1beta1.GitProvider{}
		Expect(k8sClient.Get(ctx, key(ns, "gitea"), provider)).To(Succeed())
		Expect(k8sClient.Delete(ctx, provider)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(condition(getBrief(g, ns).Status.Conditions, c4v1beta1.ConditionNeedsInput)).To(And(
				HaveField("Reason", c4v1beta1.ReasonNotFound), HaveField("Message", HavePrefix("GitProvider gitea"))))
		}).Should(Succeed())
		mustCreate(&c4v1beta1.GitProvider{ObjectMeta: objectMeta(ns, "gitea"), Spec: provider.Spec})
		Eventually(func(g Gomega) {
			g.Expect(condition(getBrief(g, ns).Status.Conditions, c4v1beta1.ConditionNeedsInput)).To(And(
				HaveField("Status", metav1.ConditionTrue), HaveField("Reason", model.ReasonPlannerNeedsInput),
				HaveField("Message", "Which CRDs should it lint first?")))
		}).Should(Succeed())

		By("starting the next round on a prompt, which clears the question")
		brief := getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = []string{"The Gateway API CRDs."} })).To(Succeed())
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanning))
			g.Expect(condition(b.Status.Conditions, c4v1beta1.ConditionNeedsInput)).
				To(HaveField("Status", metav1.ConditionFalse))
		}).Should(Succeed())
	})

	It("waits for its planner's token Secret, and plans once the person makes it", func() {
		// claude-credentials is no Secret the suite's operator shares, so the
		// message is the one for an operator that shares nothing.
		createFactory(ns)
		for _, name := range []string{"planner", "implementer", "reviewer"} {
			mustCreate(&c4v1beta1.Agent{ObjectMeta: objectMeta(ns, name), Spec: c4v1beta1.AgentSpec{
				Image: "theos/agent-go:dev", MaxRounds: 3,
				CredentialsSecretRef: &c4v1beta1.LocalRef{Name: "claude-credentials"}}})
		}
		mustCreate(newBrief(ns, "cel-lens"))

		By("naming the missing Secret, with no round started")
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefNeedsInput))
			g.Expect(condition(b.Status.Conditions, c4v1beta1.ConditionNeedsInput)).To(HaveField("Message",
				Equal("Secret claude-credentials of Agent planner is missing: make it in namespace "+ns+
					" with the Agents' token, so the Brief's planner can start")))
			g.Expect(b.Status.Round).To(BeZero())
		}).Should(Succeed())

		By("starting round 1 once the Secret is there")
		mustCreate(&corev1.Secret{ObjectMeta: objectMeta(ns, "claude-credentials"),
			StringData: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "test-token"}})
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Round).To(Equal(int32(1)))
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanning))
			g.Expect(condition(b.Status.Conditions, c4v1beta1.ConditionNeedsInput)).
				To(HaveField("Status", metav1.ConditionFalse))
		}).Should(Succeed())
	})

	It("says where to give a token the operator shares, and gets no copy of it (#1137)", func() {
		// Agents on the shared token reach Claude through the model proxy
		// (#1136): the Brief asks for the label, then for the source Secret in the
		// operator's namespace, and waits for no copy; the token never leaves the
		// operator's namespace (#1137). The source starts missing; the suite's
		// other specs use it, so it is put back as it was.
		source := &corev1.Secret{}
		if err := k8sClient.Get(ctx, key(operatorNamespace, sharedToken), source); err == nil {
			Expect(k8sClient.Delete(ctx, source)).To(Succeed())
			DeferCleanup(func() {
				mustCreate(&corev1.Secret{ObjectMeta: objectMeta(operatorNamespace, sharedToken),
					Type: source.Type, Data: source.Data})
			})
		} else {
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "shared-"}}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		shared := namespace.Name
		DeferCleanup(stopPolling, shared)
		createFactory(shared)
		for _, name := range []string{"planner", "implementer", "reviewer"} {
			mustCreate(&c4v1beta1.Agent{ObjectMeta: objectMeta(shared, name), Spec: c4v1beta1.AgentSpec{
				Image: "theos/agent-go:dev", MaxRounds: 3,
				CredentialsSecretRef: &c4v1beta1.LocalRef{Name: sharedToken}}})
		}
		mustCreate(newBrief(shared, "cel-lens"))

		By("naming the label a namespace that hasn't opted in needs")
		Eventually(func(g Gomega) {
			b := getBrief(g, shared)
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefNeedsInput))
			g.Expect(condition(b.Status.Conditions, c4v1beta1.ConditionNeedsInput)).To(HaveField("Message",
				Equal("Agent planner is on the factory's shared Secret "+sharedToken+": label namespace "+shared+" "+
					c4v1beta1.LabelSharedSecrets+"=true, so its agents reach the factory's model proxy")))
		}).Should(Succeed())

		By("naming the operator's namespace once it has opted in, while the source is missing")
		Expect(k8sClient.Get(ctx, key("", shared), namespace)).To(Succeed())
		Expect(patch(namespace, func() {
			namespace.Labels = map[string]string{c4v1beta1.LabelSharedSecrets: "true"}
		})).To(Succeed())
		Eventually(func(g Gomega) {
			b := getBrief(g, shared)
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefNeedsInput))
			g.Expect(condition(b.Status.Conditions, c4v1beta1.ConditionNeedsInput)).To(HaveField("Message",
				Equal("Secret "+sharedToken+" of Agent planner is missing: make it in namespace "+operatorNamespace+
					" with the Agents' token, which the factory's model proxy uses for every project")))
			g.Expect(b.Status.Round).To(BeZero())
		}).Should(Succeed())

		By("starting round 1 once the source is there, copying nothing in")
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(shared, shared), &c4v1beta1.Project{}))).To(BeTrue())
		mine := &corev1.Secret{ObjectMeta: objectMeta(operatorNamespace, sharedToken),
			Data: map[string][]byte{"token": []byte("first")}}
		Expect(k8sClient.Create(ctx, mine)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, mine))).To(Succeed()) })
		Eventually(func(g Gomega) {
			b := getBrief(g, shared)
			g.Expect(b.Status.Round).To(Equal(int32(1)))
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanning))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(shared, sharedToken), &corev1.Secret{}))).To(BeTrue())
		}, "2s").Should(Succeed())
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
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefNeedsInput))
			g.Expect(condition(b.Status.Conditions, c4v1beta1.ConditionNeedsInput)).To(And(
				HaveField("Reason", c4v1beta1.ReasonNotFound),
				HaveField("Message", HavePrefix("Agent late-implementer is missing"))))
		}).Should(Succeed())
		Expect(getTask(Default, ns, "cel-lens-plan-1").Status.Phase).To(Equal(c4v1beta1.TaskMerged))
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "cel-lens"), &c4v1beta1.SoftwareSystem{}))).
			To(BeTrue(), "nothing is applied")

		By("naming a catalog entry the plan needs that went since it was checked")
		Expect(k8sClient.Delete(ctx, &c4v1beta1.Connector{ObjectMeta: objectMeta(ns, "postgres")})).To(Succeed())
		mustCreate(&c4v1beta1.Agent{ObjectMeta: objectMeta(ns, "late-implementer"),
			Spec: c4v1beta1.AgentSpec{Image: "theos/agent-go:dev"}})
		Eventually(func(g Gomega) {
			g.Expect(condition(getBrief(g, ns).Status.Conditions, c4v1beta1.ConditionNeedsInput)).
				To(HaveField("Message", HavePrefix("The plan of round 1 needs Connector postgres, missing")))
		}).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "cel-lens"), &c4v1beta1.SoftwareSystem{}))).
			To(BeTrue(), "nothing is applied")

		By("resuming the round once it is there")
		mustCreate(&c4v1beta1.Connector{ObjectMeta: objectMeta(ns, "postgres"),
			Spec: c4v1beta1.ConnectorSpec{CatalogEntrySpec: newCatalogSpec(templatesURL+"postgres", "v1.3.0", c4v1beta1.ContainerTypeService)}})
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
			g.Expect(condition(b.Status.Conditions, c4v1beta1.ConditionNeedsInput)).
				To(HaveField("Status", metav1.ConditionFalse))
		}).Should(Succeed())
		crew := &c4v1beta1.Crew{}
		Expect(k8sClient.Get(ctx, key(ns, "linter"), crew)).To(Succeed())
		Expect(crew.Spec.Implementer.Name).To(Equal("late-implementer"))
	})

	It("never files a recorded issue again, even once it is closed", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		// checker's template is missing, so it never gets Ready and the round
		// keeps applying, reconcile after reconcile.
		mustCreate(&c4v1beta1.Framework{ObjectMeta: objectMeta(ns, "broken"),
			Spec: newFrameworkSpec(templatesURL+"missing", "v1.0.0", c4v1beta1.ContainerTypeService)})
		mustCreate(newBrief(ns, "cel-lens"))
		plan := linterPlan(ns, 1)
		plan[model.PlanModelFile] = scaffold.File{Data: append(plan[model.PlanModelFile].Data, []byte(`---
apiVersion: theos.factory.io/v1beta1
kind: Container
metadata:
  name: checker
spec:
  softwareSystemRef: {name: cel-lens}
  type: service
  gitProviderRef: {name: gitea}
  frameworkRef: {name: broken}
`)...)}
		planRound(ns, 1, 1, plan)

		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Issues).To(HaveLen(1))
			g.Expect(b.Status.Issues[0]).To(And(HaveField("Round", int32(1)), HaveField("N", int32(1))))
			g.Expect(condition(b.Status.Conditions, c4v1beta1.ConditionPlanned)).To(
				HaveField("Message", ContainSubstring("Container checker")))
		}).Should(Succeed())
		Expect(gitFake.CloseIssueByHand(ns, "linter", int(getBrief(Default, ns).Status.Issues[0].Number))).To(Succeed())

		By("a reconcile reading a cached Brief that doesn't show the recorded issue yet (#501)")
		stale := getBrief(Default, ns)
		recorded := stale.Status.Issues
		stale.Status.Issues = nil
		linter := &c4v1beta1.Container{}
		Expect(k8sClient.Get(ctx, key(ns, "linter"), linter)).To(Succeed())
		r := &BriefReconciler{Client: k8sClient, APIReader: k8sClient, Git: fakeGitClient{gitFake}}
		run := &roundApply{brief: stale, orig: stale.DeepCopy(), round: "1"}
		Expect(r.fileIssues(ctx, run, map[string]*c4v1beta1.Container{"linter": linter}, []model.PlanIssue{
			{N: 1, Container: "linter", Title: "Lint rule 1", Body: linterIssue(1)}})).To(Succeed())
		Expect(gitFake.OpenIssues(ns, "linter")).To(BeEmpty(), "the closed issue was filed again")
		Expect(getBrief(Default, ns).Status.Issues).To(Equal(recorded))

		Consistently(func(g Gomega) {
			g.Expect(gitFake.OpenIssues(ns, "linter")).To(BeEmpty(), "the closed issue was filed again")
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefApplying))
		}, "3s").Should(Succeed())
	})

	It("fails the round when the namespace changed since the plan was checked", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		brief := newBrief(ns, "cel-lens")
		brief.Spec.Agents.Implementer.Name = "late-implementer"
		mustCreate(brief)
		planRound(ns, 1, 1, linterPlan(ns, 1))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefNeedsInput))
		}).Should(Succeed())

		By("a person making a Container of the planned name meanwhile")
		mustCreate(newContainer(ns, "linter", "cel-lens", c4v1beta1.ContainerTypeWorker))
		mustCreate(&c4v1beta1.Agent{ObjectMeta: objectMeta(ns, "late-implementer"),
			Spec: c4v1beta1.AgentSpec{Image: "theos/agent-go:dev"}})
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefFailed))
			g.Expect(condition(b.Status.Conditions, c4v1beta1.ConditionPlanned)).To(And(
				HaveField("Reason", model.ReasonRoundFailed),
				HaveField("Message", And(ContainSubstring("no longer fits the namespace"),
					ContainSubstring("Container/linter: type can't change")))))
			g.Expect(b.Status.History).To(ConsistOf(HaveField("Outcome", model.ReasonRoundFailed)))
		}).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "cel-lens"), &c4v1beta1.SoftwareSystem{}))).
			To(BeTrue(), "nothing is applied")
	})

	It("says in its status and an Event why applying stopped, and stays Applying while it tries again (#371)", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		By("a ResourceQuota that allows no Person, which the plan check can't see")
		quota := &corev1.ResourceQuota{ObjectMeta: objectMeta(ns, "no-people"), Spec: corev1.ResourceQuotaSpec{
			Hard: corev1.ResourceList{"count/people.theos.factory.io": resource.MustParse("0")}}}
		mustCreate(quota)
		quota.Status = corev1.ResourceQuotaStatus{Hard: quota.Spec.Hard,
			Used: corev1.ResourceList{"count/people.theos.factory.io": resource.MustParse("0")}}
		Expect(k8sClient.Status().Update(ctx, quota)).To(Succeed())
		planRound(ns, 1, 1, linterPlan(ns, 1))

		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefApplying))
			g.Expect(condition(b.Status.Conditions, c4v1beta1.ConditionPlanned)).To(And(
				HaveField("Status", metav1.ConditionFalse), HaveField("Reason", model.ReasonApplyFailed),
				HaveField("Message", And(ContainSubstring("Round 1 could not be applied at Person/rule-author"),
					ContainSubstring("exceeded quota: no-people"), ContainSubstring("tries again")))))
			g.Expect(briefEventNotes(g, ns, "ApplyFailed")).To(ContainElement(
				ContainSubstring("at Person/rule-author")))
		}).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "cel-lens"), &c4v1beta1.SoftwareSystem{}))).
			To(BeTrue(), "the objects after the Person wait for it")
		Consistently(func(g Gomega) {
			g.Expect(briefEventNotes(g, ns, "ApplyFailed")).To(HaveLen(1), "one Event while the message holds")
		}, "2s").Should(Succeed())
	})

	It("finishes a round a restart left halfway, with no duplicate issue or Crew", func() {
		createFactory(ns)
		briefAgents(ns, 3)

		By("finding two of the four issues and the Crew already there, as a restart would")
		for n := 1; n <= 2; n++ {
			issue := model.PlanIssue{N: n, Body: "Filed before the restart."}
			gitFake.AddIssue(ns, "linter", fmt.Sprintf("Lint rule %d", n), model.BriefIssueBody(issue, "cel-lens", 1),
				[]string{"status/ready"})
		}
		mustCreate(&c4v1beta1.Crew{ObjectMeta: objectMeta(ns, "linter"), Spec: c4v1beta1.CrewSpec{
			ContainerRef: c4v1beta1.LocalRef{Name: "linter"},
			Implementer:  c4v1beta1.LocalRef{Name: "implementer"}, Reviewer: c4v1beta1.LocalRef{Name: "reviewer"},
			Parallel: 2}})
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, linterPlan(ns, 4))

		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		brief := getBrief(Default, ns)
		Expect(brief.Status.Issues).To(HaveLen(4))
		issues := gitFake.OpenIssues(ns, "linter")
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
		var crews c4v1beta1.CrewList
		Expect(k8sClient.List(ctx, &crews, client.InNamespace(ns))).To(Succeed())
		Expect(crews.Items).To(HaveLen(1))
		Expect(crews.Items[0].Spec.Parallel).To(Equal(int32(2)), "the Crew there is left alone")
	})
})
