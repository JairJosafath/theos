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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// labelPods are the live pool pods of label.
func labelPods(ns, label string) []corev1.Pod {
	GinkgoHelper()
	var out []corev1.Pod
	for _, pod := range livePoolPods(ns) {
		if pod.Labels[LabelCILabel] == label {
			out = append(out, pod)
		}
	}
	return out
}

// docs/plans/done/test-environments.md, step 3: CI runs on the environment.
var _ = Describe("CI runner pool of a test environment", func() {
	It("keeps ci.size pods for env-<name>, built from the environment's image and devices, and none at 0", func() {
		ns := newProjectNamespace()
		newCIProvider(ns)
		Eventually(func() []corev1.Pod { return labelPods(ns, "go") }).Should(HaveLen(2))
		goImage := labelPods(ns, "go")[0].Spec.Containers[0].Image

		android := newBrowserEnvironment(ns)
		android.Name = "android"
		android.Spec.Local.Image = "ghcr.io/jairjosafath/theos/env-android:0.31.0"
		android.Spec.Local.Devices = map[corev1.ResourceName]resource.Quantity{"devices.kubevirt.io/kvm": resource.MustParse("1")}
		android.Spec.CI = &c4v1beta1.TestEnvironmentCI{Size: 1}
		mustCreate(android)
		label := model.EnvRunnerLabel("android")
		Eventually(func() []corev1.Pod { return labelPods(ns, label) }).Should(HaveLen(1))
		pod := labelPods(ns, label)[0]
		Expect(pod.Spec.Containers[0].Image).To(Equal(android.Spec.Local.Image))
		Expect(pod.Spec.Containers[0].Resources.Limits).To(HaveKey(corev1.ResourceName("devices.kubevirt.io/kvm")))
		Expect(pod.Spec.InitContainers[0].Image).To(Equal(goImage), "registered with the ci block's first runner image")
		Expect(pod.Spec.AutomountServiceAccountToken).To(HaveValue(BeFalse()))
		Expect(labelPods(ns, "go")).To(HaveLen(2), "the ci block's own pool stays")

		By("keeping none at a size of 0")
		Expect(patch(android, func() { android.Spec.CI.Size = 0 })).To(Succeed())
		Eventually(func() []corev1.Pod { return labelPods(ns, label) }).Should(BeEmpty())
		var project c4v1beta1.Project
		Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
		Expect(condition(project.Status.Conditions, ConditionCIRunners)).To(HaveField("Status", metav1.ConditionTrue))
	})

	It("says LabelClash when the ci block names the environment's label too", func() {
		ns := newProjectNamespace()
		newCIProvider(ns, c4v1beta1.CIRunnerSpec{Label: model.EnvRunnerLabel("browser"),
			Image: "theos/ci-runner-base:dev", Size: 1})
		env := newBrowserEnvironment(ns)
		env.Spec.CI = &c4v1beta1.TestEnvironmentCI{Size: 1}
		mustCreate(env)
		Eventually(func(g Gomega) {
			var project c4v1beta1.Project
			g.Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
			cond := condition(project.Status.Conditions, ConditionCIRunners)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(model.ReasonLabelClash))
			g.Expect(cond.Message).To(ContainSubstring("label env-browser is test environment browser's"))
		}).Should(Succeed())
		for _, pod := range labelPods(ns, model.EnvRunnerLabel("browser")) {
			Expect(pod.Spec.Containers[0].Image).NotTo(Equal(env.Spec.Local.Image), "the ci block's runner keeps its label")
		}
	})
})
