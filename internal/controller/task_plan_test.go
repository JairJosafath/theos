/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
)

// newPlanTask is a planning round of the Brief cel-lens.
func newPlanTask(ns, name string) *c4v1beta1.Task {
	return &c4v1beta1.Task{ObjectMeta: objectMeta(ns, name), Spec: c4v1beta1.TaskSpec{
		Kind:     c4v1beta1.TaskPlan,
		BriefRef: &c4v1beta1.LocalRef{Name: "cel-lens"},
		AgentRef: c4v1beta1.LocalRef{Name: "planner"},
	}}
}

// A Plan Task's CEL rules (docs/plans/done/brief.md, B3).
var _ = Describe("Plan Task", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("defaults a Task's kind to Implement", func() {
		task := newTask(ns, "implement", "Do it")
		mustCreate(task)
		Expect(task.Spec.Kind).To(Equal(c4v1beta1.TaskImplement))
	})

	It("names a Brief and no Container", func() {
		mustCreate(newPlanTask(ns, "round-1"))
		withGoal := newPlanTask(ns, "with-goal")
		withGoal.Spec.Goal = "Plan the idea."
		mustCreate(withGoal)

		both := newPlanTask(ns, "both")
		both.Spec.ContainerRef = c4v1beta1.LocalRef{Name: "orders-api"}
		Expect(k8sClient.Create(ctx, both)).To(MatchError(ContainSubstring("set exactly one of containerRef and briefRef")))

		neither := newPlanTask(ns, "neither")
		neither.Spec.BriefRef = nil
		Expect(k8sClient.Create(ctx, neither)).To(MatchError(ContainSubstring("set exactly one of containerRef and briefRef")))
		Expect(k8sClient.Create(ctx, neither)).To(MatchError(ContainSubstring("a Plan Task needs a briefRef")))

		noParent := newTask(ns, "no-parent", "Do it")
		noParent.Spec.ContainerRef = c4v1beta1.LocalRef{}
		Expect(k8sClient.Create(ctx, noParent)).To(MatchError(ContainSubstring("set exactly one of containerRef and briefRef")))
	})

	It("takes a briefRef only as kind Plan", func() {
		implement := newPlanTask(ns, "implement-brief")
		implement.Spec.Kind = c4v1beta1.TaskImplement
		implement.Spec.Goal = "Do it"
		Expect(k8sClient.Create(ctx, implement)).To(MatchError(ContainSubstring("briefRef needs kind Plan")))

		defaulted := newPlanTask(ns, "default-brief")
		defaulted.Spec.Kind = ""
		defaulted.Spec.Goal = "Do it"
		Expect(k8sClient.Create(ctx, defaulted)).To(MatchError(ContainSubstring("briefRef needs kind Plan")))
	})

	It("has no reviewer and no issue", func() {
		reviewed := newPlanTask(ns, "reviewed")
		reviewed.Spec.ReviewerRef = &c4v1beta1.LocalRef{Name: "reviewer"}
		Expect(k8sClient.Create(ctx, reviewed)).To(MatchError(ContainSubstring("a Plan Task has no reviewerRef")))

		issue := newPlanTask(ns, "issue")
		issue.Spec.Issue = 2
		Expect(k8sClient.Create(ctx, issue)).To(MatchError(ContainSubstring("a Plan Task has no issue")))
	})

	It("keeps its kind and its Brief for life", func() {
		task := newPlanTask(ns, "keep")
		task.Spec.Goal = "Plan the idea."
		mustCreate(task)
		Expect(patch(task, func() { task.Spec.Kind = c4v1beta1.TaskImplement })).
			To(MatchError(ContainSubstring("kind is immutable; create a new Task")))
		task = newPlanTask(ns, "keep-brief")
		mustCreate(task)
		Expect(patch(task, func() { task.Spec.BriefRef.Name = "another" })).
			To(MatchError(ContainSubstring("briefRef is immutable")))
		Expect(patch(task, func() { task.Spec.BriefRef, task.Spec.ContainerRef = nil, c4v1beta1.LocalRef{Name: "orders-api"} })).
			To(MatchError(ContainSubstring("containerRef and briefRef are immutable; create a new Task")))

		implement := newTask(ns, "keep-implement", "Do it")
		mustCreate(implement)
		Expect(patch(implement, func() { implement.Spec.Kind = c4v1beta1.TaskPlan })).
			To(MatchError(ContainSubstring("kind is immutable; create a new Task")))
	})

})

// A Plan Task's run (docs/plans/done/brief.md, step 5): the planner pushes a plan,
// and the Brief's verdict, two annotations, ends the try.
var _ = Describe("Plan Task run", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	// roundTask makes the Brief cel-lens with its repository and bot, the
	// planner with rounds tries, and the Task planning the Brief's round.
	roundTask := func(name string, rounds int32, round string) *c4v1beta1.Task {
		GinkgoHelper()
		createFactory(ns)
		// The Brief's own planner is missing, so it plans no round of its own
		// and leaves these Tasks to the test.
		brief := newBrief(ns, "cel-lens")
		brief.Spec.Agents.Planner.Name = "absent"
		mustCreate(brief, &c4v1beta1.Agent{ObjectMeta: objectMeta(ns, "planner"),
			Spec: c4v1beta1.AgentSpec{Image: "c4factory/agent-go:dev", MaxRounds: rounds}})
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "brief-cel-lens-git"), &corev1.Secret{})).To(Succeed())
		}).Should(Succeed())
		task := newPlanTask(ns, name)
		task.Labels = map[string]string{c4v1beta1.LabelBrief: "cel-lens", c4v1beta1.LabelBriefRound: round}
		mustCreate(task)
		return task
	}
	sandboxEnv := func(g Gomega, name string) []any {
		sandbox := sandboxExists(g, ns, name)
		containers, _, _ := unstructured.NestedSlice(sandbox.Object, "spec", "podTemplate", "spec", "containers")
		g.Expect(containers).To(HaveLen(1))
		env, _ := containers[0].(map[string]any)["env"].([]any)
		return env
	}
	verdict := func(name, commit, refusals string) {
		GinkgoHelper()
		task := &c4v1beta1.Task{}
		Expect(k8sClient.Get(ctx, key(ns, name), task)).To(Succeed())
		Expect(patch(task, func() {
			task.Annotations = map[string]string{c4v1beta1.AnnotationPlanChecked: commit}
			if refusals != "" {
				task.Annotations[c4v1beta1.AnnotationPlanRefusals] = refusals
			}
		})).To(Succeed())
	}

	It("runs the planner on the Brief's round and ends Merged when the Brief accepts the plan", func() {
		roundTask("cel-lens-plan-2", 3, "2")

		By("starting the planner on the brief repository with the Brief's bot")
		var env []any
		Eventually(func(g Gomega) {
			env = sandboxEnv(g, "cel-lens-plan-2-r1-plan")
			task := getTask(g, ns, "cel-lens-plan-2")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.Branch).To(Equal("plan/2"))
		}).Should(Succeed())
		Expect(env).To(ContainElements(
			map[string]any{"name": "C4_ROLE", "value": "plan"},
			map[string]any{"name": "C4_ROUND", "value": "2"},
			map[string]any{"name": "C4_BRANCH", "value": "plan/2"},
			map[string]any{"name": "C4_BRIEF", "value": "cel-lens"},
			map[string]any{"name": "C4_REPO_URL",
				"value": "http://gitea.gitea.svc.cluster.local:3000/" + ns + "/brief-cel-lens.git"},
			HaveKeyWithValue("valueFrom", HaveKeyWithValue("secretKeyRef",
				HaveKeyWithValue("name", "brief-cel-lens-git"))),
		))
		Expect(env).NotTo(ContainElement(HaveKeyWithValue("name", "C4_CONTAINER")))
		Expect(env).NotTo(ContainElement(HaveKeyWithValue("name", "C4_GOAL")))

		By("waiting for the Brief's verdict on the pushed plan")
		finishSandbox(ns, "cel-lens-plan-2-r1-plan", "cel-lens-plan-2",
			&agentrunner.Result{Role: "plan", Commit: "sha-p1", Summary: "Planned the linter."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "cel-lens-plan-2")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(task.Status.Commit).To(Equal("sha-p1"))
			g.Expect(task.Status.Summary).To(Equal("Planned the linter."))
			g.Expect(condition(task.Status.Conditions, ConditionProgressing)).To(
				HaveField("Message", "Waiting for the Brief to check the plan"))
		}).Should(Succeed())

		By("ignoring a verdict on another commit")
		verdict("cel-lens-plan-2", "sha-other", "")
		Consistently(func(g Gomega) {
			g.Expect(getTask(g, ns, "cel-lens-plan-2").Status.Phase).To(Equal(c4v1beta1.TaskChecking))
		}, "1s").Should(Succeed())

		By("ending Merged when the Brief accepts it")
		verdict("cel-lens-plan-2", "sha-p1", "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "cel-lens-plan-2")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.CompletedAt).NotTo(BeNil())
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(And(
				HaveField("Status", metav1.ConditionTrue), HaveField("Message", "The Brief accepted the plan")))
		}).Should(Succeed())
	})

	// The review of step 11: a planner's interrupted run resumes in place too,
	// through planSandbox, with the current Agent's pod template.
	It("resumes an interrupted planner in place with the current Agent", func() {
		roundTask("cel-lens-plan-3", 3, "3")
		const name = "cel-lens-plan-3-r1-plan"
		Eventually(func(g Gomega) { sandboxExists(g, ns, name) }).Should(Succeed())
		finishSandbox(ns, name, "cel-lens-plan-3", &agentrunner.Result{Role: "plan",
			Error: "claude failed: signal: killed", Interrupted: true}, "")
		Eventually(func(g Gomega) {
			mode, _, _ := unstructured.NestedString(sandboxExists(g, ns, name).Object, "spec", "operatingMode")
			g.Expect(mode).To(Equal(operatingModeSuspended))
			task := getTask(g, ns, "cel-lens-plan-3")
			g.Expect(task.Status.Resumes).To(Equal(int32(1)))
			g.Expect(task.Status.SandboxPod).NotTo(BeEmpty(), "the old pod is the run's until it goes")
		}, "20s").Should(Succeed())

		By("resuming it with the planner's new image once the pod is gone")
		Eventually(func(g Gomega) {
			var agent c4v1beta1.Agent
			g.Expect(k8sClient.Get(ctx, key(ns, "planner"), &agent)).To(Succeed())
			agent.Spec.Image = "c4factory/agent-go:new"
			g.Expect(k8sClient.Update(ctx, &agent)).To(Succeed())
		}).Should(Succeed())
		Expect(k8sClient.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}},
			client.GracePeriodSeconds(0))).To(Succeed())
		Eventually(func(g Gomega) {
			sandbox := sandboxExists(g, ns, name)
			mode, _, _ := unstructured.NestedString(sandbox.Object, "spec", "operatingMode")
			g.Expect(mode).To(Equal(operatingModeRunning))
			pod := sandboxPodSpec(g, sandbox)
			g.Expect(pod.Containers[0].Image).To(Equal("c4factory/agent-go:new"), "the current Agent's image")
			g.Expect(pod.Containers[0].Env).To(ContainElements(
				corev1.EnvVar{Name: "C4_ROLE", Value: "plan"},
				corev1.EnvVar{Name: "C4_ROUND", Value: "3"},
				corev1.EnvVar{Name: "C4_BRANCH", Value: "plan/3"}))
			task := getTask(g, ns, "cel-lens-plan-3")
			g.Expect(task.Status.SandboxPod).To(BeEmpty(), "the Task forgets the old pod")
			g.Expect(task.Status.Sandbox).To(Equal(name))
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.Reruns).To(BeZero())
		}, "20s").Should(Succeed())
	})

	It("tries again with the Brief's reasons, and fails after the planner's rounds", func() {
		roundTask("cel-lens-plan-1", 2, "1")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "cel-lens-plan-1-r1-plan") }).Should(Succeed())
		finishSandbox(ns, "cel-lens-plan-1-r1-plan", "cel-lens-plan-1",
			&agentrunner.Result{Role: "plan", Commit: "sha-p1"}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "cel-lens-plan-1").Status.Commit).To(Equal("sha-p1"))
		}).Should(Succeed())

		By("starting the next try with the reasons as feedback")
		verdict("cel-lens-plan-1", "sha-p1", "Container/web: type is missing")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "cel-lens-plan-1")
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
			g.Expect(task.Status.Feedback).To(Equal("The Brief refused the plan:\nContainer/web: type is missing"))
			g.Expect(sandboxEnv(g, "cel-lens-plan-1-r2-plan")).To(ContainElements(
				map[string]any{"name": "C4_ROUND", "value": "1"},
				map[string]any{"name": "C4_FEEDBACK", "value": task.Status.Feedback},
			), "every try plans the same Brief round")
		}).Should(Succeed())

		By("waiting for a verdict on the new commit, not the old one")
		finishSandbox(ns, "cel-lens-plan-1-r2-plan", "cel-lens-plan-1",
			&agentrunner.Result{Role: "plan", Commit: "sha-p2"}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "cel-lens-plan-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(task.Status.Commit).To(Equal("sha-p2"))
		}).Should(Succeed())

		By("failing when the last try is refused too")
		verdict("cel-lens-plan-1", "sha-p2", "Container/web: still no type")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "cel-lens-plan-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(And(
				HaveField("Status", metav1.ConditionFalse), HaveField("Message", ContainSubstring("Failed after 2 rounds"))))
		}).Should(Succeed())
	})

	It("waits for its Brief, and fails without the Brief's round", func() {
		task := newPlanTask(ns, "orphan")
		mustCreate(task)
		Eventually(func(g Gomega) {
			g.Expect(condition(getTask(g, ns, "orphan").Status.Conditions, ConditionProgressing)).To(And(
				HaveField("Reason", "BriefNotFound"), HaveField("Status", metav1.ConditionFalse)))
		}).Should(Succeed())

		roundTask("unlabelled", 3, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "unlabelled")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(
				HaveField("Message", ContainSubstring(c4v1beta1.LabelBriefRound)))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			sandbox := newSandbox()
			err := k8sClient.Get(ctx, key(ns, "unlabelled-r1-plan"), sandbox)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		}, "1s").Should(Succeed())
	})
})
