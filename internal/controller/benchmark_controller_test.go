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
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
)

// docs/plans/done/measure-and-improve.md, step 6: a Benchmark runs the same goal
// from a pinned commit under each variant; its Tasks never merge, its
// Scorers judge each, it cleans up after them and compares the variants.
var _ = Describe("Benchmark", func() {
	agentNamed := func(ns, name string, rounds int32) *c4v1beta1.Agent {
		a := newAgent(ns, rounds)
		a.Name = name
		return a
	}
	newBenchmark := func(ns, name, base string, mutate ...func(*c4v1beta1.Benchmark)) *c4v1beta1.Benchmark {
		b := &c4v1beta1.Benchmark{ObjectMeta: objectMeta(ns, name), Spec: c4v1beta1.BenchmarkSpec{
			ContainerRef: c4v1beta1.LocalRef{Name: "orders-api"}, Base: base,
			Goals: []c4v1beta1.BenchmarkGoal{{Goal: "Add crop"}},
			Variants: []c4v1beta1.BenchmarkVariant{
				{Name: "slow", ImplementerRef: c4v1beta1.LocalRef{Name: "claude"}},
				{Name: "fast", ImplementerRef: c4v1beta1.LocalRef{Name: "fast"}},
			},
			Repetitions: 1, Parallel: 2, BudgetUSD: 5,
		}}
		for _, m := range mutate {
			m(b)
		}
		return b
	}
	getBenchmark := func(g Gomega, ns, name string) *c4v1beta1.Benchmark {
		var b c4v1beta1.Benchmark
		g.Expect(k8sClient.Get(ctx, key(ns, name), &b)).To(Succeed())
		return &b
	}
	done := func(b *c4v1beta1.Benchmark) *metav1.Condition {
		return apimeta.FindStatusCondition(b.Status.Conditions, c4v1beta1.ConditionBenchmarkDone)
	}

	It("runs each variant from the base, never merges, has its Scorers judge each run, cleans up and compares", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		base := gitFake.MainHead(ns, "orders-api").Hash
		mustCreate(newAgent(ns, 3), agentNamed(ns, "fast", 1),
			&c4v1beta1.Scorer{ObjectMeta: objectMeta(ns, "tests"), Spec: c4v1beta1.ScorerSpec{
				Criteria: []string{"Has a test"}, JudgeRef: c4v1beta1.LocalRef{Name: "claude"},
				SamplePercent: new(int32(0))}},
			newBenchmark(ns, "cmp", base, func(b *c4v1beta1.Benchmark) {
				b.Spec.ScorerRefs = []c4v1beta1.LocalRef{{Name: "tests"}}
			}))

		By("making a Task per variant, each on a branch made at the base, which merges nothing")
		slow, fast := "cmp-slow-g1-r1", "cmp-fast-g1-r1"
		Eventually(func(g Gomega) {
			task := getTask(g, ns, slow)
			g.Expect(task.Spec.Benchmark).To(Equal(&c4v1beta1.TaskBenchmarkSpec{
				BenchmarkRef: c4v1beta1.LocalRef{Name: "cmp"}, Variant: "slow", Repetition: 1, Base: base}))
			g.Expect(task.Spec.Goal).To(Equal("Add crop"))
			g.Expect(task.OwnerReferences).To(ConsistOf(HaveField("Kind", "Benchmark")))
			g.Expect(getTask(g, ns, fast).Spec.AgentRef.Name).To(Equal("fast"))
			sandboxExists(g, ns, slow+"-r1-implement")
			sandboxExists(g, ns, fast+"-r1-implement")
		}).Should(Succeed())
		Expect(gitFake.BranchHead(ns, "orders-api", "task/"+slow)).To(Equal(base))

		By("ending the approved run Approved, its pull request open and unmerged")
		gitFake.PushBranchHead(ns, "orders-api", "task/"+slow, "sha-slow")
		finishSandbox(ns, slow+"-r1-implement", slow, &agentrunner.Result{Role: "implement", Commit: "sha-slow"}, "")
		gitFake.SetChecks(ns, "orders-api", "sha-slow", "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, slow+"-r1-review-a1") }).Should(Succeed())
		finishSandbox(ns, slow+"-r1-review-a1", slow, &agentrunner.Result{Role: "review", Verdict: "approve",
			Summary: "Good."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, slow).Status.Phase).To(Equal(c4v1beta1.TaskApproved))
		}).Should(Succeed())
		pulls := gitFake.PullRequests(ns, "orders-api")
		Expect(pulls).NotTo(BeEmpty())
		for _, pr := range pulls {
			Expect(pr.Merged).To(BeFalse(), "a Benchmark's Task never merges")
		}

		By("failing the other variant's run after its one round")
		gitFake.PushBranchHead(ns, "orders-api", "task/"+fast, "sha-fast")
		finishSandbox(ns, fast+"-r1-implement", fast, &agentrunner.Result{Role: "implement", Commit: "sha-fast",
			CostMilliUSD: new(int64(400))}, "")
		gitFake.SetChecks(ns, "orders-api", "sha-fast", "failure")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, fast).Status.Phase).To(Equal(c4v1beta1.TaskFailed))
		}).Should(Succeed())

		By("having its Scorer judge both runs, though the Scorer samples nothing, then cleaning up")
		for _, judged := range []string{slow, fast} {
			name := model.ScoreTaskName("tests", judged)
			Eventually(func(g Gomega) { sandboxExists(g, ns, name+"-r1-score") }).Should(Succeed())
			Expect(getTask(Default, ns, name).Spec.Goal).To(ContainSubstring("from " + base + ": read it with git diff"))
			verdict := model.ScorePass
			if judged == fast {
				verdict = model.ScoreFail
			}
			finishSandbox(ns, name+"-r1-score", name, &agentrunner.Result{Role: "score",
				Score: []model.ScoreCriterion{{Criterion: "Has a test", Verdict: verdict}}}, "")
		}
		Eventually(func(g Gomega) {
			b := getBenchmark(g, ns, "cmp")
			g.Expect(b.Status.Cleaned).To(ConsistOf(slow, fast))
			g.Expect(done(b)).To(And(HaveField("Status", metav1.ConditionTrue), HaveField("Reason", model.BenchmarkFinished)))
			g.Expect(b.Status.Comparison).To(Equal(
				"No clear difference at 2 finished runs: the approval rates' 95% intervals overlap."))
			g.Expect(b.Status.Variants).To(HaveLen(2))
			g.Expect(b.Status.Variants[0]).To(And(HaveField("Name", "slow"), HaveField("Finished", int32(1)),
				HaveField("Approval", c4v1beta1.BenchmarkRate{Of: 1, Percent: 100, Low: 21, High: 100}),
				HaveField("Criteria", ConsistOf(HaveField("Rate", HaveField("Percent", int32(100)))))))
			g.Expect(b.Status.Variants[1]).To(And(HaveField("Name", "fast"),
				HaveField("Approval", c4v1beta1.BenchmarkRate{Of: 1, Percent: 0, Low: 0, High: 79}),
				HaveField("MedianCostMilliUSD", int64(400))))
		}).Should(Succeed())
		Expect(gitFake.BranchExists(ns, "orders-api", "task/"+slow)).To(BeFalse())
		Expect(gitFake.BranchExists(ns, "orders-api", "task/"+fast)).To(BeFalse())
		for _, pr := range gitFake.PullRequests(ns, "orders-api") {
			Expect(pr.State).To(Equal(stateClosed))
			Expect(pr.Merged).To(BeFalse())
		}
	})

	It("starts no run past its budget", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		base := gitFake.MainHead(ns, "orders-api").Hash
		mustCreate(agentNamed(ns, "claude", 1), agentNamed(ns, "fast", 1),
			newBenchmark(ns, "cheap", base, func(b *c4v1beta1.Benchmark) { b.Spec.Parallel, b.Spec.BudgetUSD = 1, 1 }))
		first := "cheap-slow-g1-r1"
		Eventually(func(g Gomega) { sandboxExists(g, ns, first+"-r1-implement") }).Should(Succeed())
		finishSandbox(ns, first+"-r1-implement", first, &agentrunner.Result{Role: "implement", Error: "boom",
			CostMilliUSD: new(int64(1500))}, "")
		Eventually(func(g Gomega) {
			b := getBenchmark(g, ns, "cheap")
			g.Expect(done(b)).To(And(HaveField("Status", metav1.ConditionFalse),
				HaveField("Reason", model.BenchmarkOverBudget)))
			g.Expect(b.Status.CostMilliUSD).To(Equal(int64(1500)))
		}).Should(Succeed())
		var tasks c4v1beta1.TaskList
		Expect(k8sClient.List(ctx, &tasks, client.InNamespace(ns), client.MatchingLabels{LabelBenchmark: "cheap"})).
			To(Succeed())
		Expect(tasks.Items).To(HaveLen(1))
	})

	It("refuses what CEL refuses", func() {
		ns := newProjectNamespace()
		base := "5a00000000000000000000000000000000000001"
		for _, b := range []*c4v1beta1.Benchmark{
			newBenchmark(ns, "branch", "main"),
			newBenchmark(ns, "one", base, func(b *c4v1beta1.Benchmark) { b.Spec.Variants = b.Spec.Variants[:1] }),
			newBenchmark(ns, "twice", base, func(b *c4v1beta1.Benchmark) { b.Spec.Variants[1].Name = "slow" }),
			newBenchmark(ns, "both", base, func(b *c4v1beta1.Benchmark) { b.Spec.Goals[0].Issue = 4 }),
		} {
			Expect(k8sClient.Create(ctx, b)).NotTo(Succeed(), b.Name)
		}
		b := newBenchmark(ns, "fixed", base)
		mustCreate(b)
		// #26 (theos): the reconciler writes the Benchmark meanwhile, so an
		// Update of the object read at create could fail on its stale
		// resourceVersion instead; a merge patch carries none, so only the
		// CEL rule can refuse it.
		orig := b.DeepCopy()
		b.Spec.Repetitions = 5
		Expect(k8sClient.Patch(ctx, b, client.MergeFrom(orig))).To(MatchError(ContainSubstring("a Benchmark can't change")))
		issue := newTask(ns, "bench-issue", "x")
		issue.Spec.Goal, issue.Spec.Issue = "", 4
		issue.Spec.Benchmark = &c4v1beta1.TaskBenchmarkSpec{BenchmarkRef: c4v1beta1.LocalRef{Name: "fixed"},
			Variant: "slow", Repetition: 1, Base: base}
		Expect(k8sClient.Create(ctx, issue)).To(MatchError(ContainSubstring("an Implement Task with a goal")))
	})
})
