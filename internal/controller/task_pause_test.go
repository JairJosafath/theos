/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
)

// pauseAsked is the pause annotation on the pod template of a sandbox, and
// whether it is there.
func pauseAsked(g Gomega, ns, name string) (string, bool) {
	annotations, _, err := unstructured.NestedStringMap(sandboxExists(g, ns, name).Object,
		"spec", "podTemplate", "metadata", "annotations")
	g.Expect(err).NotTo(HaveOccurred())
	reason, ok := annotations[c4v1beta1.AnnotationPause]
	return reason, ok
}

// sandboxMode is a sandbox's spec.operatingMode.
func sandboxMode(g Gomega, ns, name string) string {
	mode, _, _ := unstructured.NestedString(sandboxExists(g, ns, name).Object, "spec", "operatingMode")
	return mode
}

// runningPod plays Agent Sandbox starting a sandbox's pod, and waits for the
// Task to take it as its run's.
func runningPod(ns, name, task string) {
	GinkgoHelper()
	var sandbox *unstructured.Unstructured
	Eventually(func(g Gomega) { sandbox = sandboxExists(g, ns, name) }, "20s").Should(Succeed())
	Expect(k8sClient.Create(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name,
		Labels:          map[string]string{LabelTask: task},
		OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(sandbox, SandboxGVK)}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "agent"}}}})).To(Succeed())
	Eventually(func(g Gomega) {
		g.Expect(getTask(g, ns, task).Status.SandboxPod).NotTo(BeEmpty())
	}).Should(Succeed())
}

// deletePod plays Agent Sandbox deleting a suspended sandbox's pod.
func deletePod(ns, name string) {
	GinkgoHelper()
	Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: ns, Name: name}}, client.GracePeriodSeconds(0)))).To(Succeed())
	Eventually(func() bool {
		return apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, name), &corev1.Pod{}))
	}).Should(BeTrue(), "the pod of sandbox %s is gone", name)
}

// taskPaused expects the Task's Paused condition to have status and reason.
func taskPaused(g Gomega, task *c4v1beta1.Task, status metav1.ConditionStatus, reason string) {
	cond := condition(task.Status.Conditions, c4v1beta1.ConditionPaused)
	g.Expect(cond).NotTo(BeNil(), "the Task's Paused condition")
	g.Expect(cond.Status).To(Equal(status))
	g.Expect(cond.Reason).To(Equal(reason))
}

// crewPausedMessage waits for the Crew's Paused condition to be True with message.
func crewPausedMessage(ns, name, message string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var crew c4v1beta1.Crew
		g.Expect(k8sClient.Get(ctx, key(ns, name), &crew)).To(Succeed())
		cond := condition(crew.Status.Conditions, c4v1beta1.ConditionPaused)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		g.Expect(cond.Message).To(Equal(message))
	}, "20s").Should(Succeed())
}

// Step 12 of docs/plans/self-hosting.md (#1995): running Tasks pause at a
// tool boundary.
var _ = Describe("Paused runs", func() {
	It("need paused, on a Crew and on a Brief", func() {
		ns := newProjectNamespace()
		crew := newCrew(ns)
		crew.Spec.PauseRuns = true
		Expect(k8sClient.Create(ctx, crew)).To(MatchError(ContainSubstring("pauseRuns needs paused")))
		brief := newBrief(ns, "cel-lens")
		brief.Spec.PauseRuns = true
		Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring("pauseRuns needs paused")))

		By("taking paused away and leaving pauseRuns")
		crew, brief = newCrew(ns), newBrief(ns, "cel-lens")
		crew.Spec.Paused, crew.Spec.PauseRuns = true, true
		brief.Spec.Paused, brief.Spec.PauseRuns = true, true
		mustCreate(crew, brief)
		Expect(patch(crew, func() { crew.Spec.Paused = false })).
			To(MatchError(ContainSubstring("pauseRuns needs paused")))
		Expect(patch(brief, func() { brief.Spec.Paused = false })).
			To(MatchError(ContainSubstring("pauseRuns needs paused")))
		crew = getCrew(Default, ns)
		Expect(patch(crew, func() { crew.Spec.Paused, crew.Spec.PauseRuns = false, false })).To(Succeed())
	})

	It("asks a Crew's running Task to pause, suspends the run that paused with no round or rerun spent, "+
		"and resumes it when pauseRuns is unset", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 3)
		crew := newCrew(ns)
		task := newTask(ns, "add-crop", "Add crop")
		task.Labels = map[string]string{LabelCrew: crew.Name}
		mustCreate(crew, task)
		const name = "add-crop-r1-implement"
		runningPod(ns, name, "add-crop")

		By("mounting the pod's own annotations for theos-agent's pause guard")
		pod := sandboxPodSpec(Default, sandboxExists(Default, ns, name))
		Expect(pod.Volumes).To(ContainElement(podInfo()))
		Expect(pod.Containers[0].VolumeMounts).To(ContainElement(
			corev1.VolumeMount{Name: podInfoVolume, MountPath: agentrunner.PodInfoDir, ReadOnly: true}))
		_, asked := pauseAsked(Default, ns, name)
		Expect(asked).To(BeFalse(), "no pause before the Crew asks for one")

		By("pausing the Crew's runs: the annotation goes on the running Task's Sandbox's pod template")
		const reason = "Crew orders-api paused its runs"
		Expect(patch(crew, func() { crew.Spec.Paused, crew.Spec.PauseRuns = true, true })).To(Succeed())
		Eventually(func(g Gomega) {
			got, _ := pauseAsked(g, ns, name)
			g.Expect(got).To(Equal(reason))
			g.Expect(sandboxMode(g, ns, name)).To(Equal(operatingModeRunning), "the agent goes on to its tool boundary")
			taskPaused(g, getTask(g, ns, "add-crop"), metav1.ConditionFalse, reasonPausing)
		}, "20s").Should(Succeed())
		crewPausedMessage(ns, crew.Name, "Paused, runs too; 1 Task still stopping: add-crop")

		By("suspending the run that reports Paused, its cost counted, no round, rerun or resume spent")
		finishSandbox(ns, name, "add-crop", &agentrunner.Result{Role: "implement", Paused: true,
			PauseReason: reason, CostMilliUSD: new(int64(400))}, "")
		Eventually(func(g Gomega) {
			sandbox := sandboxExists(g, ns, name)
			mode, _, _ := unstructured.NestedString(sandbox.Object, "spec", "operatingMode")
			g.Expect(mode).To(Equal(operatingModeSuspended))
			g.Expect(sandbox.GetAnnotations()).To(HaveKeyWithValue(annotationPausedRun, reason))
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.Reruns).To(BeZero())
			g.Expect(task.Status.Resumes).To(BeZero())
			g.Expect(task.Status.CostMilliUSD).To(Equal(int64(400)))
			taskPaused(g, task, metav1.ConditionTrue, reasonRunsPaused)
			g.Expect(task.Status.History).To(ContainElement(And(HaveField("Reason", model.ReasonPaused),
				HaveField("Message", ContainSubstring("The run of round 1 paused ("+reason+") and saved its checkpoint")))))
		}, "20s").Should(Succeed())
		crewPausedMessage(ns, crew.Name, "Paused, runs too; every run stopped")

		By("holding the resume while the pause lasts, after the pod went")
		deletePod(ns, name)
		Eventually(func(g Gomega) {
			cond := condition(getTask(g, ns, "add-crop").Status.Conditions, ConditionProgressing)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal(model.ReasonPaused))
			g.Expect(cond.Message).To(Equal("Paused by the factory (" + reason + "); no agent runs until the pause ends"))
		}, "20s").Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(sandboxMode(g, ns, name)).To(Equal(operatingModeSuspended))
		}, "2s").Should(Succeed())

		By("resuming the run in place, counting no resume, once pauseRuns is unset")
		crew = getCrew(Default, ns)
		Expect(patch(crew, func() { crew.Spec.PauseRuns = false })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(sandboxMode(g, ns, name)).To(Equal(operatingModeRunning))
			_, asked := pauseAsked(g, ns, name)
			g.Expect(asked).To(BeFalse(), "the resumed pod is asked for no pause")
			g.Expect(sandboxExists(g, ns, name).GetAnnotations()).NotTo(HaveKey(annotationPausedRun))
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Sandbox).To(Equal(name))
			g.Expect(task.Status.SandboxPod).To(BeEmpty(), "the Task forgets the old pod")
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.Resumes).To(BeZero(), "a pause's resume counts against no cap")
			taskPaused(g, task, metav1.ConditionFalse, reasonRunsResume)
			g.Expect(task.Status.History).To(ContainElement(And(HaveField("Reason", "Running"),
				HaveField("Message", ContainSubstring("Resuming the paused run of round 1 in place")))))
		}, "20s").Should(Succeed())
		crewPausedMessage(ns, crew.Name, "Paused; 1 Task still running: add-crop")
	})

	// #1995: a pause costs no rerun, even when it costs the checkpoint.
	It("reruns a paused run whose sandbox expired before the suspend with no rerun spent", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 3)
		crew := newCrew(ns)
		task := newTask(ns, "add-crop", "Add crop")
		task.Labels = map[string]string{LabelCrew: crew.Name}
		mustCreate(crew, task)
		const name = "add-crop-r1-implement"
		runningPod(ns, name, "add-crop")
		const reason = "Crew orders-api paused its runs"
		Expect(patch(crew, func() { crew.Spec.Paused, crew.Spec.PauseRuns = true, true })).To(Succeed())
		Eventually(func(g Gomega) {
			got, _ := pauseAsked(g, ns, name)
			g.Expect(got).To(Equal(reason))
		}, "20s").Should(Succeed())

		By("expiring the sandbox, then the run reports Paused")
		sandbox := newSandbox()
		Expect(k8sClient.Get(ctx, key(ns, name), sandbox)).To(Succeed())
		Expect(unstructured.SetNestedSlice(sandbox.Object, []any{map[string]any{
			"type": "Ready", "status": "False", "reason": "SandboxExpired", "message": "the sandbox expired",
			"lastTransitionTime": metav1.Now().UTC().Format(time.RFC3339)}}, "status", "conditions")).To(Succeed())
		Expect(k8sClient.Status().Update(ctx, sandbox)).To(Succeed())
		finishSandbox(ns, name, "add-crop", &agentrunner.Result{Role: "implement", Paused: true,
			PauseReason: reason, CostMilliUSD: new(int64(400))}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Reruns).To(BeZero(), "a pause costs no rerun")
			g.Expect(task.Status.Restarts).To(Equal(int32(1)), "the next run gets a sandbox name of its own")
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.CostMilliUSD).To(Equal(int64(400)))
			g.Expect(task.Status.History).To(ContainElement(HaveField("Message",
				ContainSubstring("round 1 runs again, with no rerun spent"))))
			taskPaused(g, task, metav1.ConditionTrue, reasonRunsPaused)
		}, "20s").Should(Succeed())
		rerun := model.SandboxName("add-crop", 1, 1, model.RoleImplementer, 1, 0)
		Consistently(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, rerun), newSandbox()))
		}, "2s").Should(BeTrue(), "no run starts while the pause lasts")

		By("starting the run again once pauseRuns is unset")
		crew = getCrew(Default, ns)
		Expect(patch(crew, func() { crew.Spec.PauseRuns = false })).To(Succeed())
		Eventually(func(g Gomega) {
			sandboxExists(g, ns, rerun)
			g.Expect(getTask(g, ns, "add-crop").Status.Reruns).To(BeZero())
		}, "20s").Should(Succeed())
	})

	It("passes a Brief's pauseRuns on to the Crews it paused and pauses its planner Task's run", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		const planner, name = "cel-lens-plan-1", "cel-lens-plan-1-r1-plan"
		runningPod(ns, name, planner)

		By("a Crew of the Brief's, and one paused by hand, which keeps its own runs")
		crewOf := func(name string, paused bool) *c4v1beta1.Crew {
			om := objectMeta(ns, name)
			om.Labels = map[string]string{c4v1beta1.LabelBrief: "cel-lens"}
			return &c4v1beta1.Crew{ObjectMeta: om, Spec: c4v1beta1.CrewSpec{
				ContainerRef: c4v1beta1.LocalRef{Name: name},
				Implementer:  c4v1beta1.LocalRef{Name: "implementer"}, Reviewer: c4v1beta1.LocalRef{Name: "reviewer"},
				Paused: paused}}
		}
		mustCreate(crewOf("linter", false), crewOf("by-hand", true))

		By("pausing the Brief and its runs")
		const reason = "Brief cel-lens paused its runs"
		brief := getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Paused, brief.Spec.PauseRuns = true, true })).To(Succeed())
		getCrew := func(g Gomega, name string) *c4v1beta1.Crew {
			crew := &c4v1beta1.Crew{}
			g.Expect(k8sClient.Get(ctx, key(ns, name), crew)).To(Succeed())
			return crew
		}
		Eventually(func(g Gomega) {
			linter := getCrew(g, "linter")
			g.Expect(linter.Spec.Paused).To(BeTrue())
			g.Expect(linter.Spec.PauseRuns).To(BeTrue(), "the Brief passes pauseRuns on")
			got, _ := pauseAsked(g, ns, name)
			g.Expect(got).To(Equal(reason), "the planner's run is asked to pause")
			taskPaused(g, getTask(g, ns, planner), metav1.ConditionFalse, reasonPausing)
		}, "20s").Should(Succeed())
		Expect(getCrew(Default, "by-hand").Spec.PauseRuns).To(BeFalse(), "a Crew paused by hand is the person's")
		pausedMessage(ns, "Paused, runs too; 1 Task still stopping: "+planner)

		By("suspending the planner's run that reports Paused")
		finishSandbox(ns, name, planner, &agentrunner.Result{Role: "plan", Paused: true, PauseReason: reason,
			CostMilliUSD: new(int64(300))}, "")
		Eventually(func(g Gomega) {
			g.Expect(sandboxMode(g, ns, name)).To(Equal(operatingModeSuspended))
			task := getTask(g, ns, planner)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.Reruns).To(BeZero())
			g.Expect(task.Status.Resumes).To(BeZero())
			g.Expect(task.Status.CostMilliUSD).To(Equal(int64(300)))
			taskPaused(g, task, metav1.ConditionTrue, reasonRunsPaused)
		}, "20s").Should(Succeed())
		pausedMessage(ns, "Paused, runs too; every run stopped")
		deletePod(ns, name)
		Consistently(func(g Gomega) {
			g.Expect(sandboxMode(g, ns, name)).To(Equal(operatingModeSuspended))
		}, "2s").Should(Succeed())

		By("unsetting pauseRuns: its Crews' runs and its planner's resume, the Crews stay paused")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.PauseRuns = false })).To(Succeed())
		Eventually(func(g Gomega) {
			linter := getCrew(g, "linter")
			g.Expect(linter.Spec.Paused).To(BeTrue())
			g.Expect(linter.Spec.PauseRuns).To(BeFalse())
			g.Expect(sandboxMode(g, ns, name)).To(Equal(operatingModeRunning))
			task := getTask(g, ns, planner)
			g.Expect(task.Status.Resumes).To(BeZero())
			taskPaused(g, task, metav1.ConditionFalse, reasonRunsResume)
		}, "20s").Should(Succeed())
		pausedMessage(ns, "Paused; 1 Task still running: "+planner)
	})
})
