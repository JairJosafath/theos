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

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

var _ = Describe("Project sharing Secrets", Ordered, func() {
	// The suite's operator shares sharedToken and sharedPullSecret from
	// operatorNamespace; the sources are made here and kept for the suite.
	BeforeAll(func() {
		mustCreate(
			&corev1.Secret{ObjectMeta: objectMeta(operatorNamespace, sharedToken),
				Data: map[string][]byte{"token": []byte("first")}},
			&corev1.Secret{ObjectMeta: objectMeta(operatorNamespace, sharedPullSecret),
				Type: corev1.SecretTypeDockerConfigJson,
				Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)}},
		)
	})

	optIn := func(ns string, in bool) {
		GinkgoHelper()
		var namespace corev1.Namespace
		Expect(k8sClient.Get(ctx, key("", ns), &namespace)).To(Succeed())
		Expect(patch(&namespace, func() {
			if namespace.Labels == nil {
				namespace.Labels = map[string]string{}
			}
			if in {
				namespace.Labels[c4v1alpha1.LabelSharedSecrets] = "true"
			} else {
				delete(namespace.Labels, c4v1alpha1.LabelSharedSecrets)
			}
		})).To(Succeed())
	}
	secretsShared := func(g Gomega, ns string) *metav1.Condition {
		var project c4v1alpha1.Project
		g.Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
		cond := condition(project.Status.Conditions, ConditionSecretsShared)
		g.Expect(cond).NotTo(BeNil())
		return cond
	}

	It("copies the shared Secrets into an opted-in namespace and keeps them in step", func() {
		ns := newProjectNamespace()
		optIn(ns, true)

		var copied corev1.Secret
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, sharedToken), &copied)).To(Succeed())
			g.Expect(copied.Data).To(HaveKeyWithValue("token", []byte("first")))
			g.Expect(copied.Labels).To(HaveKeyWithValue(c4v1alpha1.LabelSharedFrom, sharedToken))
			var pull corev1.Secret
			g.Expect(k8sClient.Get(ctx, key(ns, sharedPullSecret), &pull)).To(Succeed())
			g.Expect(pull.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			cond := secretsShared(g, ns)
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Message).To(ContainSubstring(sharedToken + ": shared from namespace " + operatorNamespace))
		}).Should(Succeed())
		var project c4v1alpha1.Project
		Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
		Expect(controlledBy(&copied, &project)).To(BeTrue())

		By("following a rotated source")
		source := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, key(operatorNamespace, sharedToken), source)).To(Succeed())
		Expect(patch(source, func() { source.Data["token"] = []byte("second") })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, sharedToken), &copied)).To(Succeed())
			g.Expect(copied.Data).To(HaveKeyWithValue("token", []byte("second")))
		}).Should(Succeed())

		By("repairing a copy someone changed")
		Expect(patch(&copied, func() { copied.Data["token"] = []byte("tampered") })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, sharedToken), &copied)).To(Succeed())
			g.Expect(copied.Data).To(HaveKeyWithValue("token", []byte("second")))
		}).Should(Succeed())

		By("deleting the copies when the namespace opts out")
		optIn(ns, false)
		Eventually(func(g Gomega) {
			g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, sharedToken), &corev1.Secret{}))).To(BeTrue())
			g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, sharedPullSecret), &corev1.Secret{}))).To(BeTrue())
			cond := secretsShared(g, ns)
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("NotOptedIn"))
		}).Should(Succeed())
	})

	It("shares into a namespace made opted in before it has a Project, which then adopts the copies", func() {
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "early-",
			Labels: map[string]string{c4v1alpha1.LabelSharedSecrets: "true"}}}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		ns := namespace.Name
		DeferCleanup(stopPolling, ns)

		Eventually(func(g Gomega) {
			var copied corev1.Secret
			g.Expect(k8sClient.Get(ctx, key(ns, sharedToken), &copied)).To(Succeed())
			g.Expect(copied.Labels).To(HaveKeyWithValue(c4v1alpha1.LabelSharedFrom, sharedToken))
			g.Expect(copied.OwnerReferences).To(BeEmpty())
		}).Should(Succeed())

		mustCreate(&c4v1alpha1.Project{ObjectMeta: objectMeta(ns, ns),
			Spec: c4v1alpha1.ProjectSpec{DisplayName: "Early"}})
		Eventually(func(g Gomega) {
			g.Expect(secretsShared(g, ns).Reason).To(Equal("Shared"))
			var project c4v1alpha1.Project
			g.Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
			for _, name := range []string{sharedToken, sharedPullSecret} {
				var copied corev1.Secret
				g.Expect(k8sClient.Get(ctx, key(ns, name), &copied)).To(Succeed())
				g.Expect(metav1.IsControlledBy(&copied, &project)).To(BeTrue(), name)
			}
		}).Should(Succeed())
		optIn(ns, false)
	})

	It("leaves the project's own Secret of a shared name alone", func() {
		ns := newProjectNamespace()
		own := &corev1.Secret{ObjectMeta: objectMeta(ns, sharedToken), Data: map[string][]byte{"token": []byte("own")}}
		mustCreate(own)
		optIn(ns, true)

		Eventually(func(g Gomega) {
			cond := secretsShared(g, ns)
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Message).To(ContainSubstring(sharedToken + ": the project's own"))
			g.Expect(cond.Message).To(ContainSubstring(sharedPullSecret + ": shared from"))
		}).Should(Succeed())
		Expect(k8sClient.Get(ctx, key(ns, sharedToken), own)).To(Succeed())
		Expect(own.Data).To(HaveKeyWithValue("token", []byte("own")))
		Expect(own.Labels).NotTo(HaveKey(c4v1alpha1.LabelSharedFrom))

		By("keeping it when the namespace opts out")
		optIn(ns, false)
		Eventually(func(g Gomega) {
			g.Expect(secretsShared(g, ns).Reason).To(Equal("NotOptedIn"))
			g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, sharedPullSecret), &corev1.Secret{}))).To(BeTrue())
		}).Should(Succeed())
		Expect(k8sClient.Get(ctx, key(ns, sharedToken), own)).To(Succeed())
		Expect(own.Data).To(HaveKeyWithValue("token", []byte("own")))
	})

	It("shares nothing into a namespace without the label", func() {
		ns := newProjectNamespace()
		Eventually(func(g Gomega) {
			cond := secretsShared(g, ns)
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("NotOptedIn"))
			g.Expect(cond.Message).To(ContainSubstring(c4v1alpha1.LabelSharedSecrets))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, sharedToken), &corev1.Secret{}))).To(BeTrue())
		}, "2s").Should(Succeed())
	})

	It("reports a missing source", func() {
		ns := newProjectNamespace()
		source := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, key(operatorNamespace, sharedPullSecret), source)).To(Succeed())
		Expect(k8sClient.Delete(ctx, source)).To(Succeed())
		DeferCleanup(func() {
			mustCreate(&corev1.Secret{ObjectMeta: objectMeta(operatorNamespace, sharedPullSecret),
				Type: source.Type, Data: source.Data})
		})
		optIn(ns, true)

		Eventually(func(g Gomega) {
			cond := secretsShared(g, ns)
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("SourceMissing"))
			g.Expect(cond.Message).To(ContainSubstring(sharedPullSecret + ": missing in namespace " + operatorNamespace))
			g.Expect(cond.Message).To(ContainSubstring(sharedToken + ": shared from"))
		}).Should(Succeed())
		optIn(ns, false)
	})
})
