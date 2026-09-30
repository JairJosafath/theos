/*
Copyright 2026.

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
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
)

// newAgent returns the Agent "claude" with the given number of rounds.
func newAgent(ns string, rounds int32) *c4v1alpha1.Agent {
	return &c4v1alpha1.Agent{ObjectMeta: objectMeta(ns, "claude"),
		Spec: c4v1alpha1.AgentSpec{Image: "c4factory/agent-flutter:dev", MaxRounds: rounds}}
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
	mustCreate(newSystem(ns, "webshop-core"), newStandard(ns, "python-strict", "python-fastapi"), container)
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

// finishSandbox plays Agent Sandbox and c4agent: it creates the sandbox's pod and
// ends it with result as termination message, or with reason and no message.
func finishSandbox(ns, name, task string, result *agentrunner.Result, reason string) {
	GinkgoHelper()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: map[string]string{LabelTask: task}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "agent"}}},
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
		env, _, _ := unstructured.NestedSlice(sandbox.Object, "spec", "podTemplate", "spec", "containers")
		Expect(env).To(HaveLen(1))
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
		Expect(gitFake.pullComments(ns, "orders-api", 1)).To(ContainElement(ContainSubstring("Test an empty image.")))

		By("merging the approved second round")
		gitFake.pushBranch(ns, "orders-api", "task/add-crop", "sha-r2")
		finishSandbox(ns, "add-crop-r2-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r2", Summary: "Tested it."}, "")
		gitFake.setChecks(ns, "orders-api", "sha-r2", "success")
		Eventually(func(g Gomega) {
			sandboxExists(g, ns, "add-crop-r2-review-a1")
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
	})

	It("sends failed checks back to the implementer", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 2), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.pushBranch(ns, "orders-api", "task/add-crop", "sha-r1")
		finishSandbox(ns, "add-crop-r1-implement", "add-crop", &agentrunner.Result{Role: "implement", Commit: "sha-r1"}, "")
		gitFake.setChecks(ns, "orders-api", "sha-r1", "failure")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Feedback).To(ContainSubstring("The CI checks failed"))
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
			StringData: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "x"}})
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }, "30s").Should(Succeed())
	})
})
