/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// newAgent returns the Agent "claude" with the given number of rounds.
func newAgent(ns string, rounds int32) *c4v1alpha1.Agent {
	return &c4v1alpha1.Agent{ObjectMeta: objectMeta(ns, "claude"),
		Spec: c4v1alpha1.AgentSpec{Image: "c4factory/agent-go:dev", MaxRounds: rounds}}
}

// newTask returns a Task on the Container orders-api.
func newTask(ns, name, goal string) *c4v1alpha1.Task {
	return &c4v1alpha1.Task{ObjectMeta: objectMeta(ns, name), Spec: c4v1alpha1.TaskSpec{
		ContainerRef: c4v1alpha1.LocalRef{Name: "orders-api"},
		AgentRef:     c4v1alpha1.LocalRef{Name: "claude"},
		Goal:         goal,
	}}
}

// readyContainer creates the factory, a Standard and the Container orders-api with
// it, and waits until the Container is Ready.
func readyContainer(ns string) {
	const name = "orders-api"
	GinkgoHelper()
	createFactory(ns)
	container := newContainer(ns, name, "webshop-core", c4v1alpha1.ContainerTypeService)
	container.Spec.StandardRef = &c4v1alpha1.LocalRef{Name: "python-strict"}
	standard := newStandard(ns, "python-strict", "python-fastapi")
	standard.Spec.ProtectedPaths = []string{"Makefile", "ruff.toml"}
	mustCreate(newSystem(ns, "webshop-core"), standard, container)
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(ns, name), container)).To(Succeed())
		g.Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))
	}).Should(Succeed())
}

func getTask(g Gomega, ns, name string) *c4v1alpha1.Task {
	task := &c4v1alpha1.Task{}
	g.Expect(k8sClient.Get(ctx, key(ns, name), task)).To(Succeed())
	return task
}

// sandboxExists reports whether the operator created a Sandbox.
func sandboxExists(g Gomega, ns, name string) *unstructured.Unstructured {
	sandbox := newSandbox()
	g.Expect(k8sClient.Get(ctx, key(ns, name), sandbox)).To(Succeed())
	return sandbox
}

// finishSandbox plays Agent Sandbox and c4agent: it creates the sandbox's pod,
// owned by the sandbox, and ends it with result as termination message, or with
// reason and no message.
func finishSandbox(ns, name, task string, result *agentrunner.Result, reason string) {
	GinkgoHelper()
	sandbox := newSandbox()
	Expect(k8sClient.Get(ctx, key(ns, name), sandbox)).To(Succeed())
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: map[string]string{LabelTask: task},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(sandbox, SandboxGVK)}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "agent"}}},
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	terminated := &corev1.ContainerStateTerminated{Reason: reason, ExitCode: 0}
	phase := corev1.PodSucceeded
	if result != nil {
		data, err := json.Marshal(result)
		Expect(err).NotTo(HaveOccurred())
		terminated.Message = string(data)
		terminated.Reason = "Completed"
	} else {
		phase, terminated.ExitCode = corev1.PodFailed, 137
	}
	pod.Status = corev1.PodStatus{Phase: phase, ContainerStatuses: []corev1.ContainerStatus{{
		Name: "agent", Image: "agent", State: corev1.ContainerState{Terminated: terminated},
	}}}
	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
}

var _ = Describe("Task", func() {
	It("runs rounds of implement, check and review until it merges", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop\n\nWith an aspect lock."))

		By("starting the implementer in a sandbox without cluster credentials")
		var sandbox *unstructured.Unstructured
		Eventually(func(g Gomega) {
			sandbox = sandboxExists(g, ns, "add-crop-r1-implement")
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskImplementing))
			g.Expect(task.Status.Branch).To(Equal("task/add-crop"))
			g.Expect(task.OwnerReferences).To(ContainElement(HaveField("Kind", "Container")))
		}).Should(Succeed())
		spec, _, _ := unstructured.NestedMap(sandbox.Object, "spec", "podTemplate", "spec")
		Expect(spec["automountServiceAccountToken"]).To(BeFalse())
		Expect(spec["restartPolicy"]).To(Equal("Never"))
		containers, _, _ := unstructured.NestedSlice(sandbox.Object, "spec", "podTemplate", "spec", "containers")
		Expect(containers).To(HaveLen(1))
		env := containers[0].(map[string]any)["env"]
		Expect(env).To(ContainElements(
			map[string]any{"name": "C4_ROUND", "value": "1"},
			map[string]any{"name": "C4_MAX_ROUNDS", "value": "3"},
			map[string]any{"name": "C4_BRANCH", "value": "task/add-crop"},
			map[string]any{"name": "C4_ROLE", "value": "implement"},
		))
		Expect(env).To(ContainElement(map[string]any{"name": "C4_PROTECTED_PATHS",
			"value": ".c4factory.yaml,.devcontainer/,.gitea/,Makefile,ruff.toml"}))
		rule, tags := gitFake.branchRule(ns, "orders-api")
		Expect(rule.ProtectedPaths).To(Equal([]string{".c4factory.yaml", ".devcontainer/", ".gitea/", "Makefile", "ruff.toml"}),
			"the Git host refuses changes to the Standard's and the platform's files")
		Expect(tags).To(Equal([]string{"v*"}), "only the operator tags releases")
		Expect(sandbox.GetOwnerReferences()).To(ContainElement(HaveField("Kind", "Task")))
		var secret corev1.Secret
		Expect(k8sClient.Get(ctx, key(ns, "orders-api-agent-git"), &secret)).To(Succeed())
		Expect(string(secret.Data["username"])).To(Equal(ns + "-orders-api-agent"))

		By("opening a pull request for the pushed branch")
		gitFake.pushBranch(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskChecking))
			g.Expect(task.Status.PullRequest).To(Equal(int32(1)))
			g.Expect(task.Status.Commit).To(Equal("sha-r1"))
			g.Expect(task.Status.Summary).To(Equal("Added crop."))
		}).Should(Succeed())
		Expect(gitFake.pullRequests(ns, "orders-api")).To(HaveLen(1))

		By("reviewing once the checks pass")
		gitFake.setChecks(ns, "orders-api", "sha-r1", "success")
		Eventually(func(g Gomega) {
			sandboxExists(g, ns, "add-crop-r1-review-a1")
			g.Expect(getTask(g, ns, "add-crop").Status.Phase).To(Equal(c4v1alpha1.TaskReviewing))
		}).Should(Succeed())

		By("sending it back when the reviewer asks for changes")
		finishSandbox(ns, "add-crop-r1-review-a1", "add-crop",
			&agentrunner.Result{Role: "review", Verdict: "changes", Summary: "Test an empty image."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskImplementing))
			g.Expect(task.Status.Feedback).To(ContainSubstring("Test an empty image."))
			g.Expect(task.Status.Review).To(HaveField("Verdict", "changes"))
			sandboxExists(g, ns, "add-crop-r2-implement")
		}).Should(Succeed())
		Expect(gitFake.reviews(ns, "orders-api", 1)).To(ConsistOf(And(HaveField("Event", gitprovider.ReviewRequestChanges),
			HaveField("Commit", "sha-r1"), HaveField("Body", ContainSubstring("Test an empty image.")))),
			"the reviewer's verdict is a review in the Git host")

		By("merging the approved second round")
		gitFake.pushBranch(ns, "orders-api", "task/add-crop", "sha-r2")
		finishSandbox(ns, "add-crop-r2-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r2", Summary: "Tested it."}, "")
		gitFake.setChecks(ns, "orders-api", "sha-r2", "success")
		Eventually(func(g Gomega) {
			review := sandboxExists(g, ns, "add-crop-r2-review-a1")
			containers, _, _ := unstructured.NestedSlice(review.Object, "spec", "podTemplate", "spec", "containers")
			g.Expect(containers[0].(map[string]any)["env"]).To(ContainElement(
				And(HaveKeyWithValue("name", "C4_FEEDBACK"), HaveKeyWithValue("value", ContainSubstring("Test an empty image.")))),
				"the reviewer checks the last round's feedback was addressed")
		}).Should(Succeed())
		finishSandbox(ns, "add-crop-r2-review-a1", "add-crop",
			&agentrunner.Result{Role: "review", Verdict: "approve", Summary: "Complete and tested."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskMerged))
			g.Expect(task.Status.CompletedAt).NotTo(BeNil())
			g.Expect(task.Status.Review).To(HaveField("Verdict", "approve"))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).NotTo(BeNil(), "%+v", task.Status.Conditions)
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())
		Expect(gitFake.pullRequests(ns, "orders-api")[0].Merged).To(BeTrue())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "add-crop-r2-review-a1"), newSandbox()))).To(BeTrue())

		By("keeping its timeline")
		var steps []string
		for _, tr := range getTask(Default, ns, "add-crop").Status.History {
			step := fmt.Sprintf("%s r%d", tr.Phase, tr.Round)
			if len(steps) == 0 || steps[len(steps)-1] != step {
				steps = append(steps, step)
			}
		}
		Expect(steps).To(Equal([]string{"Implementing r1", "Checking r1", "Reviewing r1",
			"Implementing r2", "Checking r2", "Reviewing r2", "Merged r2"}))
	})

	It("sends failed checks back to the implementer", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 2), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.pushBranch(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, "add-crop-r1-implement", "add-crop", &agentrunner.Result{Role: "implement", Commit: "sha-r1"}, "")
		gitFake.setCheckLog("--- FAIL: TestA (0.00s)\n    a_test.go:9: got 2, want 1\n")
		gitFake.setChecks(ns, "orders-api", "sha-r1", "failure")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Feedback).To(ContainSubstring("The CI checks failed"))
			g.Expect(task.Status.Feedback).To(ContainSubstring("What failed in CI:\n--- FAIL: TestA (0.00s)"))
		}).Should(Succeed())
	})

	It("fails after the last round, saying why", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 1), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		finishSandbox(ns, "add-crop-r1-implement", "add-crop", nil, "OOMKilled")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskFailed))
			cond := condition(task.Status.Conditions, ConditionSucceeded)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Message).To(ContainSubstring("ended without a result: OOMKilled"))
		}).Should(Succeed())
	})

	It("fails when its pull request is closed without merging", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 2), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.pushBranch(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")
		gitFake.setChecks(ns, "orders-api", "sha-r1", "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-review-a1") }).Should(Succeed())
		gitFake.closePull(ns, "orders-api", 1)
		finishSandbox(ns, "add-crop-r1-review-a1", "add-crop",
			&agentrunner.Result{Role: "review", Verdict: "approve", Summary: "Fine."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskFailed))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).
				To(HaveField("Reason", "PullRequestClosed"))
		}).Should(Succeed())
		Expect(gitFake.reviews(ns, "orders-api", 1)).To(BeEmpty(), "no approval on a closed pull request")
	})

	It("refuses a Container whose default branch isn't protected", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1alpha1.ContainerTypeService)
		mustCreate(newSystem(ns, "webshop-core"), container, newAgent(ns, 1), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskFailed))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).
				To(HaveField("Message", ContainSubstring("has no Standard")))
		}).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "add-crop-r1-implement"), newSandbox()))).To(BeTrue())
	})

	It("waits for its Container, and for earlier Tasks on it", func() {
		ns := newProjectNamespace()
		mustCreate(newAgent(ns, 1),
			newTask(ns, "first", "First change"))
		Eventually(func(g Gomega) {
			g.Expect(condition(getTask(g, ns, "first").Status.Conditions, ConditionProgressing)).
				To(HaveField("Message", "Container orders-api not found"))
		}).Should(Succeed())

		readyContainer(ns)
		Eventually(func(g Gomega) { sandboxExists(g, ns, "first-r1-implement") }).Should(Succeed())
		mustCreate(newTask(ns, "second", "Second change"))
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "second")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskPending))
			g.Expect(condition(task.Status.Conditions, ConditionProgressing)).
				To(HaveField("Message", ContainSubstring("Waiting for Task first")))
		}).Should(Succeed())

		finishSandbox(ns, "first-r1-implement", "first", &agentrunner.Result{Role: "implement",
			Error: "the agent made no changes"}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "first").Status.Phase).To(Equal(c4v1alpha1.TaskFailed))
			sandboxExists(g, ns, "second-r1-implement")
		}).Should(Succeed())
	})

	It("waits for the Agent's credentials", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		agent := newAgent(ns, 1)
		agent.Spec.CredentialsSecretRef = &c4v1alpha1.LocalRef{Name: "claude-credentials"}
		mustCreate(agent, newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) {
			cond := condition(getTask(g, ns, "add-crop").Status.Conditions, ConditionProgressing)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal("CredentialsNotFound"))
		}).Should(Succeed())
		mustCreate(&corev1.Secret{ObjectMeta: objectMeta(ns, "claude-credentials"),
			StringData: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": rand.Text()}})
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }, "30s").Should(Succeed())
	})
})

var _ = Describe("Task with an interrupted run", func() {
	It("reruns the same round, up to MaxReruns times, then spends it", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		killed := &agentrunner.Result{Role: "implement", Error: "claude failed: signal: killed", Interrupted: true}
		for rerun := 1; rerun <= model.MaxReruns+1; rerun++ {
			// Each rerun has a sandbox of its own: add-crop-r1-implement, then -x1, -x2.
			name := model.SandboxName("add-crop", 1, model.RoleImplementer, 1, rerun-1)
			Eventually(func(g Gomega) { sandboxExists(g, ns, name) }, "20s").Should(Succeed())
			finishSandbox(ns, name, "add-crop", killed, "")
			if rerun > model.MaxReruns {
				break
			}
			Eventually(func(g Gomega) {
				task := getTask(g, ns, "add-crop")
				g.Expect(task.Status.Reruns).To(Equal(int32(rerun)))
				g.Expect(task.Status.Round).To(Equal(int32(1)), "an interrupted run costs no round")
			}).Should(Succeed())
		}

		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Round).To(Equal(int32(2)), "past the cap the round is spent")
			g.Expect(task.Status.Reruns).To(BeZero())
		}).Should(Succeed())
	})

	It("reruns a round whose pod was deleted and replaced under the same sandbox", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())

		// Agent Sandbox starts the pod; the operator remembers it.
		startPod := func() *corev1.Pod {
			GinkgoHelper()
			sandbox := newSandbox()
			Expect(k8sClient.Get(ctx, key(ns, "add-crop-r1-implement"), sandbox)).To(Succeed())
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "add-crop-r1-implement",
					Labels:          map[string]string{LabelTask: "add-crop"},
					OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(sandbox, SandboxGVK)}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "agent"}}},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			return pod
		}
		first := startPod()
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "add-crop").Status.SandboxPod).To(Equal(string(first.UID)))
		}).Should(Succeed())

		By("force-deleting the pod, which Agent Sandbox replaces")
		Expect(k8sClient.Delete(ctx, first, client.GracePeriodSeconds(0))).To(Succeed())
		startPod()

		rerun := model.SandboxName("add-crop", 1, model.RoleImplementer, 1, 1)
		Eventually(func(g Gomega) {
			sandboxExists(g, ns, rerun)
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Reruns).To(Equal(int32(1)))
			g.Expect(task.Status.Round).To(Equal(int32(1)), "an interrupted run costs no round")
			g.Expect(task.Status.History).To(ContainElement(And(HaveField("Reason", "Interrupted"),
				HaveField("Message", ContainSubstring("(rerun 1 of 2)")))))
		}, "20s").Should(Succeed())
		Eventually(func(g Gomega) {
			old := newSandbox()
			err := k8sClient.Get(ctx, key(ns, "add-crop-r1-implement"), old)
			g.Expect(apierrors.IsNotFound(err) || err == nil && !old.GetDeletionTimestamp().IsZero()).To(BeTrue(),
				"the replaced run's sandbox goes")
		}).Should(Succeed())
	})

	It("reruns a review lost in a cluster outage, but spends one that ran out its time", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.pushBranch(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")
		gitFake.setChecks(ns, "orders-api", "sha-r1", "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-review-a1") }, "20s").Should(Succeed())

		By("rerunning the review whose pod an outage past its shutdown time took")
		expireSandbox(ns, "add-crop-r1-review-a1", 7*time.Hour)
		rerun := model.SandboxName("add-crop", 1, model.RoleReviewer, 1, 1)
		Eventually(func(g Gomega) {
			sandboxExists(g, ns, rerun)
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Reruns).To(Equal(int32(1)))
			g.Expect(task.Status.ReviewAttempts).To(BeZero(), "an outage costs no review attempt")
		}, "20s").Should(Succeed())

		By("spending an attempt on a review that ran out its time")
		expireSandbox(ns, rerun, 0)
		Eventually(func(g Gomega) {
			sandboxExists(g, ns, model.SandboxName("add-crop", 1, model.RoleReviewer, 2, 1))
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.ReviewAttempts).To(Equal(int32(1)))
			g.Expect(task.Status.Reruns).To(Equal(int32(1)))
		}, "20s").Should(Succeed())
	})
})

var _ = Describe("Task with an unavailable agent", func() {
	It("keeps its round and tries the same step again later", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 1), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		back := time.Now().Add(3 * time.Second).UTC()
		finishSandbox(ns, "add-crop-r1-implement", "add-crop", &agentrunner.Result{Role: "implement",
			Error: "claude ended with an error: usage limit reached", Unavailable: true, RetryAt: &back}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskImplementing))
			g.Expect(task.Status.Round).To(Equal(int32(1)), "a usage limit costs no round")
			g.Expect(task.Status.AgentBackAt).NotTo(BeNil())
			g.Expect(condition(task.Status.Conditions, ConditionProgressing)).To(HaveField("Reason", "AgentUnavailable"))
		}).Should(Succeed())
		// Agent Sandbox would collect the old pod with its sandbox.
		Expect(k8sClient.Delete(ctx, &corev1.Pod{ObjectMeta: objectMeta(ns, "add-crop-r1-implement")})).To(Succeed())

		Eventually(func(g Gomega) {
			sandboxExists(g, ns, "add-crop-r1-implement")
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.AgentBackAt).To(BeNil())
			g.Expect(task.Status.Round).To(Equal(int32(1)))
		}, "20s").Should(Succeed())
	})
})
