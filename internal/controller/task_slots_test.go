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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// agentSandbox creates an agent sandbox and, unless phase is empty, its pod in
// that phase, the way Agent Sandbox would.
func agentSandbox(ns, name string, phase corev1.PodPhase) {
	GinkgoHelper()
	labels := map[string]string{LabelAgentSandbox: agentSandboxValue}
	sandbox := newSandbox()
	sandbox.SetNamespace(ns)
	sandbox.SetName(name)
	sandbox.SetLabels(labels)
	Expect(unstructured.SetNestedMap(sandbox.Object, map[string]any{"spec": map[string]any{
		"containers": []any{map[string]any{"name": "agent", "image": "agent"}}}},
		"spec", "podTemplate")).To(Succeed())
	Expect(k8sClient.Create(ctx, sandbox)).To(Succeed())
	if phase == "" {
		return
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(sandbox, SandboxGVK)}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "agent", Image: "agent"}}},
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	pod.Status.Phase = phase
	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
}

// suspendAgentSandbox suspends an agent sandbox as the operator would, marked
// with annotations.
func suspendAgentSandbox(ns, name string, annotations map[string]string) {
	GinkgoHelper()
	sandbox := newSandbox()
	Expect(k8sClient.Get(ctx, key(ns, name), sandbox)).To(Succeed())
	sandbox.SetAnnotations(annotations)
	Expect(unstructured.SetNestedField(sandbox.Object, operatingModeSuspended, "spec", "operatingMode")).To(Succeed())
	Expect(k8sClient.Update(ctx, sandbox)).To(Succeed())
}

var _ = Describe("Agent slots", func() {
	It("are held by sandboxes whose run hasn't ended, not by finished ones kept around", func() {
		ns := newProjectNamespace()
		agentSandbox(ns, "starting", "")
		agentSandbox(ns, "implementer", corev1.PodRunning)
		agentSandbox(ns, "approved-reviewer", corev1.PodSucceeded)
		agentSandbox(ns, "failed-run", corev1.PodFailed)
		By("recreating a sandbox while its predecessor's ended pod is still around")
		agentSandbox(ns, "next-round", corev1.PodSucceeded)
		old := newSandbox()
		Expect(k8sClient.Get(ctx, key(ns, "next-round"), old)).To(Succeed())
		Expect(k8sClient.Delete(ctx, old)).To(Succeed())
		agentSandbox(ns, "next-round", "")

		By("suspending runs: one waiting for its agent and one paused, their pods gone, give their slots up (#1996)")
		agentSandbox(ns, "waits-for-agent", "")
		suspendAgentSandbox(ns, "waits-for-agent", map[string]string{annotationAgentBack: "2026-11-03T15:00:00Z"})
		agentSandbox(ns, "paused", "")
		suspendAgentSandbox(ns, "paused", map[string]string{annotationPausedRun: "Crew linter paused its runs"})
		agentSandbox(ns, "still-saving", corev1.PodRunning)
		suspendAgentSandbox(ns, "still-saving", map[string]string{annotationAgentBack: "2026-11-03T15:00:00Z"})
		agentSandbox(ns, "interrupted", "")
		suspendAgentSandbox(ns, "interrupted", nil)

		By("counting them as the operator, under its generated role")
		operator, err := client.New(managerCfg, client.Options{Scheme: k8sClient.Scheme()})
		Expect(err).NotTo(HaveOccurred())
		r := &TaskReconciler{Client: operator, APIReader: operator}
		held, err := r.slotHolders(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(ContainElements(ns+"/starting", ns+"/implementer", ns+"/next-round"))
		Expect(held).NotTo(ContainElement(ns + "/approved-reviewer"))
		Expect(held).NotTo(ContainElement(ns + "/failed-run"))
		Expect(held).NotTo(ContainElement(ns + "/waits-for-agent"))
		Expect(held).NotTo(ContainElement(ns + "/paused"))
		Expect(held).To(ContainElements(ns+"/still-saving", ns+"/interrupted"),
			"a pod still saving the run, and an interrupted run that resumes at once, keep their slots")
	})
})
