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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
)

// envVar is the sandbox pod's variable name, or nil.
func envVar(pod corev1.PodSpec, name string) *corev1.EnvVar {
	for _, e := range pod.Containers[0].Env {
		if e.Name == name {
			return &e
		}
	}
	return nil
}

// issueTask is a Task named name on a new issue of orders-api whose labels
// are labels.
func issueTask(ns, name string, labels ...string) *c4v1beta1.Task {
	n := gitFake.AddIssue(ns, "orders-api", "Take the extension's pictures", clearIssue,
		append([]string{model.LabelReady}, labels...))
	task := newTask(ns, name, "")
	task.Spec.Issue = int32(n)
	return task
}

// waitingFor waits until Task name waits for a test environment, why first in
// its message, with no sandbox and no round spent.
func waitingFor(ns, name, why string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		task := getTask(g, ns, name)
		cond := condition(task.Status.Conditions, ConditionProgressing)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Reason).To(Equal(model.ReasonWaitingForEnvironment), cond.Message)
		g.Expect(cond.Message).To(HavePrefix(why + ": "))
		g.Expect(task.Status.Round).To(BeZero(), "a wait for an environment spends no round")
		g.Expect(task.Status.Sandbox).To(BeEmpty())
	}, "20s").Should(Succeed())
}

// docs/plans/done/test-environments.md, step 2: a run leases the test
// environments it needs before its sandbox, and returns them.
var _ = Describe("Task with a test environment", func() {
	It("leases the environment its issue's env/ label asks for, gives the sandbox its variables, "+
		"returns it when the run ends, and gives the reviewer none", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		env := newBrowserEnvironment(ns)
		mustCreate(newAgent(ns, 3), env, issueTask(ns, "pictures-1", "env/browser"))
		const run = "pictures-1-r1-implement"
		id := model.LeaseID("browser", run)

		var pod corev1.PodSpec
		Eventually(func(g Gomega) {
			pod = sandboxPodSpec(g, sandboxExists(g, ns, run))
		}, "20s").Should(Succeed())
		Expect(envVar(pod, "THEOS_ENV_BROWSER_URL")).To(HaveField("Value", "http://"+id+"."+ns+":7070"))
		token := envVar(pod, "THEOS_ENV_BROWSER_TOKEN")
		Expect(token).NotTo(BeNil())
		Expect(token.Value).To(BeEmpty(), "the token comes from a Secret, never the pod spec")
		Expect(token.ValueFrom.SecretKeyRef.Name).To(Equal("pictures-1-test-environments"))
		Expect(token.ValueFrom.SecretKeyRef.Key).To(Equal(id))
		Expect(envVar(pod, agentrunner.EnvEnvironments)).To(HaveField("Value", ContainSubstring(`"name":"browser"`)))

		By("leasing it for the run, with the Task's own token for it")
		req, ok := envFake.Request(testEnvironment(env, nil), id)
		Expect(ok).To(BeTrue())
		Expect(req.Holder).To(Equal("pictures-1"))
		Expect(req.Deadline).To(Equal(180 * time.Minute))
		var secret corev1.Secret
		Expect(k8sClient.Get(ctx, key(ns, "pictures-1-test-environments"), &secret)).To(Succeed())
		Expect(string(secret.Data[id])).To(Equal(req.Token))
		Expect(secret.OwnerReferences).To(ContainElement(HaveField("Kind", "Task")))
		// #214: the status that lists the lease is written before the sandbox
		// is made, so the TestEnvironment reconciler never finds a started
		// run's lease unlisted (#197 had the status trail the sandbox).
		Expect(getTask(Default, ns, "pictures-1").Status.Environments).To(ConsistOf(HaveField("Lease", id)))

		By("returning it when the run ends")
		gitFake.PushBranchHead(ns, "orders-api", "task/pictures-1", "sha-r1")
		finishSandbox(ns, run, "pictures-1",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Took the pictures."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "pictures-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(task.Status.Environments).To(BeEmpty())
			g.Expect(envFake.Returned()).To(ContainElement(ns + "/" + id))
		}).Should(Succeed())

		By("leasing none for the reviewer")
		gitFake.SetChecks(ns, "orders-api", "sha-r1", "success")
		Eventually(func(g Gomega) {
			pod = sandboxPodSpec(g, sandboxExists(g, ns, "pictures-1-r1-review-a1"))
		}, "20s").Should(Succeed())
		for _, e := range pod.Containers[0].Env {
			Expect(e.Name).NotTo(HavePrefix("THEOS_ENV"), "the reviewer's run leases no environment")
		}
		Expect(getTask(Default, ns, "pictures-1").Status.Environments).To(BeEmpty())
		Expect(envFake.Held(testEnvironment(env, nil))).To(BeZero())
	})

	It("returns the lease when the run is suspended, and leases again when it resumes", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		env := newBrowserEnvironment(ns)
		mustCreate(newAgent(ns, 3), env, issueTask(ns, "add-crop", "env/browser"))
		const run = "add-crop-r1-implement"
		id := model.LeaseID("browser", run)
		Eventually(func(g Gomega) { sandboxExists(g, ns, run) }, "20s").Should(Succeed())
		Expect(envFake.Held(testEnvironment(env, nil))).To(Equal(1))

		finishSandbox(ns, run, "add-crop", &agentrunner.Result{Role: "implement",
			Error: "claude failed: signal: killed", Interrupted: true}, "")
		playSuspension(ns, run)
		Expect(envFake.Returned()).To(ContainElement(ns+"/"+id), "the suspended run held no lease")
		Eventually(func(g Gomega) {
			g.Expect(envFake.Held(testEnvironment(env, nil))).To(Equal(1), "the resumed run leases again")
			g.Expect(getTask(g, ns, "add-crop").Status.Environments).To(ConsistOf(HaveField("Lease", id)))
			pod := sandboxPodSpec(g, sandboxExists(g, ns, run))
			g.Expect(envVar(pod, "THEOS_ENV_BROWSER_URL")).NotTo(BeNil())
		}).Should(Succeed())
	})

	It("waits for an environment its issue asks for that the project lacks, at no cost, and starts once one is added",
		func() {
			ns := newProjectNamespace()
			readyContainer(ns)
			mustCreate(newAgent(ns, 3), issueTask(ns, "android-1", "env/android"))
			waitingFor(ns, "android-1", model.EnvNotInProject)
			Consistently(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "android-1-r1-implement"), newSandbox()))
			}, "2s").Should(BeTrue(), "no sandbox starts without the environment")

			android := newBrowserEnvironment(ns)
			android.Name = "android"
			mustCreate(android)
			Eventually(func(g Gomega) {
				pod := sandboxPodSpec(g, sandboxExists(g, ns, "android-1-r1-implement"))
				g.Expect(envVar(pod, "THEOS_ENV_ANDROID_URL")).NotTo(BeNil())
				g.Expect(getTask(g, ns, "android-1").Status.Round).To(Equal(int32(1)))
			}, "20s").Should(Succeed())
		})

	It("goes on without an environment its Standard asks for that the project lacks, and says so on the Container",
		func() {
			ns := newProjectNamespace()
			readyContainer(ns)
			var standard c4v1beta1.Standard
			Expect(k8sClient.Get(ctx, key(ns, "python-strict"), &standard)).To(Succeed())
			Expect(patch(&standard, func() { standard.Spec.TestEnvironments = []string{"godot"} })).To(Succeed())
			Eventually(func(g Gomega) {
				var container c4v1beta1.Container
				g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
				cond := condition(container.Status.Conditions, c4v1beta1.ConditionTestEnvironments)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(model.EnvNotInProject))
				g.Expect(cond.Message).To(ContainSubstring("no TestEnvironment godot"))
			}, "20s").Should(Succeed())

			mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
			Eventually(func(g Gomega) {
				pod := sandboxPodSpec(g, sandboxExists(g, ns, "add-crop-r1-implement"))
				for _, e := range pod.Containers[0].Env {
					g.Expect(e.Name).NotTo(HavePrefix("THEOS_ENV"))
				}
			}, "20s").Should(Succeed())

			By("turning True once the project has it")
			godot := newBrowserEnvironment(ns)
			godot.Name = "godot"
			mustCreate(godot)
			Eventually(func(g Gomega) {
				var container c4v1beta1.Container
				g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
				cond := condition(container.Status.Conditions, c4v1beta1.ConditionTestEnvironments)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Reason).To(Equal(model.EnvPresent))
			}, "20s").Should(Succeed())
		})

	It("makes a third run wait at the environment's maxLeases of 2", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		env := newBrowserEnvironment(ns)
		mustCreate(newAgent(ns, 3), env)
		// A Crew that is gone holds no turn: the three run side by side.
		for _, name := range []string{"pictures-1", "pictures-2", "pictures-3"} {
			task := issueTask(ns, name, "env/browser")
			task.Labels = map[string]string{LabelCrew: "side-by-side"}
			mustCreate(task)
			if name != "pictures-3" {
				Eventually(func(g Gomega) { sandboxExists(g, ns, name+"-r1-implement") }, "20s").Should(Succeed())
			}
		}
		waitingFor(ns, "pictures-3", model.EnvAllLeased)
		Expect(envFake.Held(testEnvironment(env, nil))).To(Equal(2))

		By("starting once a lease is returned")
		gitFake.PushBranchHead(ns, "orders-api", "task/pictures-1", "sha-1")
		finishSandbox(ns, "pictures-1-r1-implement", "pictures-1",
			&agentrunner.Result{Role: "implement", Commit: "sha-1", Summary: "Took the pictures."}, "")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "pictures-3-r1-implement") }, "20s").Should(Succeed())
		Expect(strings.Join(envFake.Returned(), " ")).To(ContainSubstring(ns + "/" + model.LeaseID("browser",
			"pictures-1-r1-implement")))
	})
})
