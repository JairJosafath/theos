/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
)

// docs/plans/measure-and-improve.md, step 4: a Scorer makes a Score Task for
// each finished Task it selects and samples, the Score Task runs the judge
// once on a clone that pushes nothing, and the Scorer counts the verdicts.
var _ = Describe("Scorer", func() {
	criteria := []string{"The change has a test that fails without it", "The pull request says why"}

	newScorer := func(ns, name string, mutate ...func(*c4v1beta1.Scorer)) *c4v1beta1.Scorer {
		s := &c4v1beta1.Scorer{ObjectMeta: objectMeta(ns, name), Spec: c4v1beta1.ScorerSpec{
			Criteria: criteria, JudgeRef: c4v1beta1.LocalRef{Name: "claude"}}}
		for _, m := range mutate {
			m(s)
		}
		return s
	}

	// mergeTask runs the Implement Task name on orders-api to its merge, in one
	// approved round.
	mergeTask := func(ns, name string) {
		GinkgoHelper()
		mustCreate(newTask(ns, name, "Add "+name))
		Eventually(func(g Gomega) { sandboxExists(g, ns, name+"-r1-implement") }).Should(Succeed())
		head := "sha-" + name
		gitFake.PushBranchHead(ns, "orders-api", "task/"+name, head)
		finishSandbox(ns, name+"-r1-implement", name, &agentrunner.Result{Role: "implement", Commit: head,
			Summary: "Did " + name + "."}, "")
		gitFake.SetChecks(ns, "orders-api", head, "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, name+"-r1-review-a1") }).Should(Succeed())
		finishSandbox(ns, name+"-r1-review-a1", name, &agentrunner.Result{Role: "review", Verdict: "approve",
			Summary: "Good."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, name).Status.Phase).To(Equal(c4v1beta1.TaskMerged))
		}).Should(Succeed())
	}

	getScorer := func(g Gomega, ns, name string) *c4v1beta1.Scorer {
		var s c4v1beta1.Scorer
		g.Expect(k8sClient.Get(ctx, key(ns, name), &s)).To(Succeed())
		return &s
	}
	ready := func(s *c4v1beta1.Scorer) *metav1.Condition {
		return apimeta.FindStatusCondition(s.Status.Conditions, c4v1beta1.ConditionScorerReady)
	}

	It("judges a merged Task once, on a clone that pushes nothing, and counts the verdicts", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newScorer(ns, "tests"))
		mergeTask(ns, "add-crop")

		By("making the Score Task, owned by the Scorer, with the criteria and the evidence")
		name := model.ScoreTaskName("tests", "add-crop")
		var sandbox *unstructured.Unstructured
		Eventually(func(g Gomega) {
			score := getTask(g, ns, name)
			g.Expect(score.Spec.Kind).To(Equal(c4v1beta1.TaskScore))
			g.Expect(score.Spec.Score.TaskRef.Name).To(Equal("add-crop"))
			g.Expect(score.Spec.Score.Criteria).To(Equal(criteria))
			g.Expect(score.Spec.Score.Config).To(Equal(c4v1beta1.TaskRunConfig{Agent: "claude", Harness: "claude-code"}))
			g.Expect(score.Spec.Goal).To(ContainSubstring("Task add-crop (Implement) on Container orders-api ended Merged"))
			g.Expect(score.Spec.Goal).To(ContainSubstring("The implementer's summary: Did add-crop."))
			g.Expect(score.OwnerReferences).To(ContainElement(HaveField("Kind", "Scorer")))
			sandbox = sandboxExists(g, ns, name+"-r1-score")
		}).Should(Succeed())
		containers, _, _ := unstructured.NestedSlice(sandbox.Object, "spec", "podTemplate", "spec", "containers")
		Expect(containers[0].(map[string]any)["env"]).To(ContainElements(
			map[string]any{"name": agentrunner.EnvRole, "value": "score"},
			map[string]any{"name": agentrunner.EnvCriteria,
				"value": `["The change has a test that fails without it","The pull request says why"]`},
		))

		By("keeping the judge's verdicts and ending the Score Task Merged")
		finishSandbox(ns, name+"-r1-score", name, &agentrunner.Result{Role: "score", Score: []model.ScoreCriterion{
			{Criterion: criteria[0], Verdict: model.ScorePass, Reason: "crop_test.go fails without it."},
			{Criterion: criteria[1], Verdict: model.ScoreFail, Reason: "The body is empty."}}}, "")
		Eventually(func(g Gomega) {
			score := getTask(g, ns, name)
			g.Expect(score.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(score.Status.Score).To(Equal(c4v1beta1.TaskScoreStatus{Criteria: []c4v1beta1.TaskScoreCriterion{
				{Criterion: criteria[0], Verdict: c4v1beta1.ScorePass, Reason: "crop_test.go fails without it."},
				{Criterion: criteria[1], Verdict: c4v1beta1.ScoreFail, Reason: "The body is empty."}}}))
		}).Should(Succeed())
		expectSandboxGone(ns, name+"-r1-score")

		By("counting them on the Scorer, and judging nothing twice")
		Eventually(func(g Gomega) {
			s := getScorer(g, ns, "tests")
			g.Expect(s.Status.Scored).To(Equal(int32(1)))
			g.Expect(s.Status.StartedToday).To(Equal(int32(1)))
			g.Expect(s.Status.Criteria).To(Equal([]c4v1beta1.ScorerCriterionCount{
				{Criterion: criteria[0], Pass: 1}, {Criterion: criteria[1], Fail: 1}}))
			g.Expect(ready(s)).To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			var tasks c4v1beta1.TaskList
			g.Expect(k8sClient.List(ctx, &tasks, client.InNamespace(ns),
				client.MatchingLabels{LabelScorer: "tests"})).To(Succeed())
			g.Expect(tasks.Items).To(HaveLen(1))
		}, "2s").Should(Succeed())
	})

	It("fails a judge run that gives no verdicts, every criterion Unclear", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newScorer(ns, "tests"))
		mergeTask(ns, "add-crop")
		name := model.ScoreTaskName("tests", "add-crop")
		Eventually(func(g Gomega) { sandboxExists(g, ns, name+"-r1-score") }).Should(Succeed())
		finishSandbox(ns, name+"-r1-score", name, &agentrunner.Result{Role: "score",
			Error: "the judge gave no JSON verdicts"}, "")
		Eventually(func(g Gomega) {
			score := getTask(g, ns, name)
			g.Expect(score.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(score.Status.Score.Criteria).To(HaveLen(2))
			for _, c := range score.Status.Score.Criteria {
				g.Expect(c.Verdict).To(Equal(c4v1beta1.ScoreUnclear))
				g.Expect(c.Reason).To(ContainSubstring("the judge gave no JSON verdicts"))
			}
			g.Expect(getScorer(g, ns, "tests").Status.Criteria).To(Equal([]c4v1beta1.ScorerCriterionCount{
				{Criterion: criteria[0], Unclear: 1}, {Criterion: criteria[1], Unclear: 1}}))
		}).Should(Succeed())
	})

	It("stops at its daily cap, judges nothing at a sample of 0, and waits for its judge", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3),
			newScorer(ns, "capped", func(s *c4v1beta1.Scorer) { s.Spec.MaxPerDay = 1 }),
			newScorer(ns, "off", func(s *c4v1beta1.Scorer) { s.Spec.SamplePercent = new(int32(0)) }),
			newScorer(ns, "nobody", func(s *c4v1beta1.Scorer) { s.Spec.JudgeRef.Name = "missing" }))
		mergeTask(ns, "add-crop")
		mergeTask(ns, "add-blur")
		Eventually(func(g Gomega) {
			capped := getScorer(g, ns, "capped")
			g.Expect(capped.Status.StartedToday).To(Equal(int32(1)))
			g.Expect(ready(capped)).To(And(HaveField("Status", metav1.ConditionFalse),
				HaveField("Reason", model.ScorerDailyCapReached)))
			g.Expect(ready(getScorer(g, ns, "off"))).To(HaveField("Reason", model.ScorerPaused))
			g.Expect(ready(getScorer(g, ns, "nobody"))).To(HaveField("Reason", model.ScorerJudgeNotFound))
		}).Should(Succeed())
		var tasks c4v1beta1.TaskList
		Expect(k8sClient.List(ctx, &tasks, client.InNamespace(ns))).To(Succeed())
		var scores []string
		for _, t := range tasks.Items {
			if t.Spec.Kind == c4v1beta1.TaskScore {
				scores = append(scores, t.Labels[LabelScorer])
			}
		}
		Expect(scores).To(Equal([]string{"capped"}))
	})

	It("refuses what CEL refuses", func() {
		ns := newProjectNamespace()
		for _, s := range []*c4v1beta1.Scorer{
			newScorer(ns, "no-criteria", func(s *c4v1beta1.Scorer) { s.Spec.Criteria = nil }),
			newScorer(ns, "over", func(s *c4v1beta1.Scorer) { s.Spec.SamplePercent = new(int32(101)) }),
			newScorer(ns, "plan", func(s *c4v1beta1.Scorer) {
				s.Spec.Select.Kinds = []c4v1beta1.TaskKind{c4v1beta1.TaskPlan}
			}),
			newScorer(ns, "pending", func(s *c4v1beta1.Scorer) {
				s.Spec.Select.Phases = []c4v1beta1.TaskPhase{c4v1beta1.TaskPending}
			}),
		} {
			Expect(k8sClient.Create(ctx, s)).NotTo(Succeed(), s.Name)
		}
		score := &c4v1beta1.TaskScoreSpec{ScorerRef: c4v1beta1.LocalRef{Name: "tests"}, ScorerGeneration: 1,
			TaskRef: c4v1beta1.LocalRef{Name: "add-crop"}, Criteria: criteria}
		noScore := newTask(ns, "no-score", "Judge it")
		noScore.Spec.Kind = c4v1beta1.TaskScore
		Expect(k8sClient.Create(ctx, noScore)).To(MatchError(ContainSubstring("a Score Task needs score")))
		implement := newTask(ns, "implement-with-score", "Judge it")
		implement.Spec.Score = score
		Expect(k8sClient.Create(ctx, implement)).To(MatchError(ContainSubstring("only a Score Task has one")))
		reviewed := newTask(ns, "reviewed-score", "Judge it")
		reviewed.Spec.Kind, reviewed.Spec.Score = c4v1beta1.TaskScore, score
		reviewed.Spec.ReviewerRef = &c4v1beta1.LocalRef{Name: "claude"}
		Expect(k8sClient.Create(ctx, reviewed)).To(MatchError(ContainSubstring("a Score Task has a goal and a containerRef")))
	})
})
