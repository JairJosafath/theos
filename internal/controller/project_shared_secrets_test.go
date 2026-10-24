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

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
)

var _ = Describe("Project sharing Secrets", Ordered, func() {
	// The suite's operator shares sharedPullSecret from operatorNamespace and
	// names sharedToken as the token the model proxy reads there; the sources
	// are made here and kept for the suite.
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
				namespace.Labels[c4v1beta1.LabelSharedSecrets] = "true"
			} else {
				delete(namespace.Labels, c4v1beta1.LabelSharedSecrets)
			}
		})).To(Succeed())
	}
	secretsShared := func(g Gomega, ns string) *metav1.Condition {
		var project c4v1beta1.Project
		g.Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
		cond := condition(project.Status.Conditions, ConditionSecretsShared)
		g.Expect(cond).NotTo(BeNil())
		return cond
	}
	// oldTokenCopy is the copy of the token an earlier release made in ns.
	oldTokenCopy := func(ns string) *corev1.Secret {
		labels := projectLabels(ns)
		labels[c4v1beta1.LabelSharedFrom] = sharedToken
		return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: sharedToken, Namespace: ns, Labels: labels},
			Data: map[string][]byte{"token": []byte("stale")}}
	}
	noToken := func(ns string) {
		GinkgoHelper()
		Consistently(func(g Gomega) {
			g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, sharedToken), &corev1.Secret{}))).To(BeTrue())
		}, "2s").Should(Succeed())
	}

	It("copies the pull Secret into an opted-in namespace and keeps it in step; the token stays home (#1137)", func() {
		ns := newProjectNamespace()
		mustCreate(oldTokenCopy(ns))
		optIn(ns, true)

		var copied corev1.Secret
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, sharedPullSecret), &copied)).To(Succeed())
			g.Expect(copied.Type).To(Equal(corev1.SecretTypeDockerConfigJson))
			g.Expect(copied.Data).To(HaveKeyWithValue(corev1.DockerConfigJsonKey, []byte(`{"auths":{}}`)))
			g.Expect(copied.Labels).To(HaveKeyWithValue(c4v1beta1.LabelSharedFrom, sharedPullSecret))
			cond := secretsShared(g, ns)
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Message).To(Equal(sharedPullSecret + ": shared from namespace " + operatorNamespace))
			g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, sharedToken), &corev1.Secret{}))).
				To(BeTrue(), "the old token copy is deleted")
		}).Should(Succeed())
		var project c4v1beta1.Project
		Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
		Expect(controlledBy(&copied, &project)).To(BeTrue())
		noToken(ns)

		By("following a rotated source")
		source := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, key(operatorNamespace, sharedPullSecret), source)).To(Succeed())
		Expect(patch(source, func() { source.Data[corev1.DockerConfigJsonKey] = []byte(`{"auths":{"a":{}}}`) })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, sharedPullSecret), &copied)).To(Succeed())
			g.Expect(copied.Data).To(HaveKeyWithValue(corev1.DockerConfigJsonKey, []byte(`{"auths":{"a":{}}}`)))
		}).Should(Succeed())

		By("repairing a copy someone changed")
		Expect(patch(&copied, func() { copied.Data[corev1.DockerConfigJsonKey] = []byte(`{"auths":{"x":{}}}`) })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, sharedPullSecret), &copied)).To(Succeed())
			g.Expect(copied.Data).To(HaveKeyWithValue(corev1.DockerConfigJsonKey, []byte(`{"auths":{"a":{}}}`)))
		}).Should(Succeed())

		By("deleting the copy when the namespace opts out")
		optIn(ns, false)
		Eventually(func(g Gomega) {
			g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, sharedPullSecret), &corev1.Secret{}))).To(BeTrue())
			cond := secretsShared(g, ns)
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("NotOptedIn"))
		}).Should(Succeed())
	})

	It("deletes the token copy an earlier release made in a namespace that hasn't opted in, "+
		"and leaves a project's own Secret of that name (#1137)", func() {
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "stale-"}}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		ns := namespace.Name
		DeferCleanup(stopPolling, ns)
		mustCreate(oldTokenCopy(ns))

		mustCreate(&c4v1beta1.Project{ObjectMeta: objectMeta(ns, ns),
			Spec: c4v1beta1.ProjectSpec{DisplayName: "Stale"}})
		Eventually(func(g Gomega) {
			g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, sharedToken), &corev1.Secret{}))).To(BeTrue())
			g.Expect(secretsShared(g, ns).Reason).To(Equal("NotOptedIn"))
		}).Should(Succeed())

		By("keeping the project's own Secret of the token's name, opted in or not")
		own := &corev1.Secret{ObjectMeta: objectMeta(ns, sharedToken), Data: map[string][]byte{"token": []byte("own")}}
		mustCreate(own)
		optIn(ns, true)
		Eventually(func(g Gomega) {
			g.Expect(secretsShared(g, ns).Reason).To(Equal("Shared"))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, sharedToken), own)).To(Succeed())
			g.Expect(own.Data).To(HaveKeyWithValue("token", []byte("own")))
			g.Expect(own.Labels).NotTo(HaveKey(c4v1beta1.LabelSharedFrom))
		}, "2s").Should(Succeed())
		optIn(ns, false)
	})

	It("shares into a namespace made opted in before it has a Project, which then adopts the copy", func() {
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "early-",
			Labels: map[string]string{c4v1beta1.LabelSharedSecrets: "true"}}}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		ns := namespace.Name
		DeferCleanup(stopPolling, ns)

		Eventually(func(g Gomega) {
			var copied corev1.Secret
			g.Expect(k8sClient.Get(ctx, key(ns, sharedPullSecret), &copied)).To(Succeed())
			g.Expect(copied.Labels).To(HaveKeyWithValue(c4v1beta1.LabelSharedFrom, sharedPullSecret))
			g.Expect(copied.OwnerReferences).To(BeEmpty())
		}).Should(Succeed())
		noToken(ns)

		mustCreate(&c4v1beta1.Project{ObjectMeta: objectMeta(ns, ns),
			Spec: c4v1beta1.ProjectSpec{DisplayName: "Early"}})
		Eventually(func(g Gomega) {
			g.Expect(secretsShared(g, ns).Reason).To(Equal("Shared"))
			var project c4v1beta1.Project
			g.Expect(k8sClient.Get(ctx, key(ns, ns), &project)).To(Succeed())
			var copied corev1.Secret
			g.Expect(k8sClient.Get(ctx, key(ns, sharedPullSecret), &copied)).To(Succeed())
			g.Expect(metav1.IsControlledBy(&copied, &project)).To(BeTrue())
		}).Should(Succeed())
		optIn(ns, false)
	})

	It("keeps the copy in a namespace without a Project in step and deletes it on opt-out", func() {
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "projectless-",
			Labels: map[string]string{c4v1beta1.LabelSharedSecrets: "true"}}}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		ns := namespace.Name
		DeferCleanup(stopPolling, ns)

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, sharedPullSecret), &corev1.Secret{})).To(Succeed())
		}).Should(Succeed())

		By("following a rotated source")
		source := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, key(operatorNamespace, sharedPullSecret), source)).To(Succeed())
		Expect(patch(source, func() { source.Data[corev1.DockerConfigJsonKey] = []byte(`{"auths":{"p":{}}}`) })).To(Succeed())
		Eventually(func(g Gomega) {
			var copied corev1.Secret
			g.Expect(k8sClient.Get(ctx, key(ns, sharedPullSecret), &copied)).To(Succeed())
			g.Expect(copied.Data).To(HaveKeyWithValue(corev1.DockerConfigJsonKey, []byte(`{"auths":{"p":{}}}`)))
			g.Expect(copied.OwnerReferences).To(BeEmpty())
		}).Should(Succeed())

		By("deleting the copy when the namespace opts out")
		optIn(ns, false)
		Eventually(func(g Gomega) {
			g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, sharedPullSecret), &corev1.Secret{}))).To(BeTrue())
		}).Should(Succeed())
		var project c4v1beta1.Project
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, ns), &project))).To(BeTrue())
	})

	It("leaves the project's own Secret of a shared name alone", func() {
		ns := newProjectNamespace()
		own := &corev1.Secret{ObjectMeta: objectMeta(ns, sharedPullSecret),
			Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte("own")}}
		mustCreate(own)
		optIn(ns, true)

		Eventually(func(g Gomega) {
			cond := secretsShared(g, ns)
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Message).To(Equal(sharedPullSecret + ": the project's own"))
		}).Should(Succeed())
		Expect(k8sClient.Get(ctx, key(ns, sharedPullSecret), own)).To(Succeed())
		Expect(own.Data).To(HaveKeyWithValue(corev1.DockerConfigJsonKey, []byte("own")))
		Expect(own.Labels).NotTo(HaveKey(c4v1beta1.LabelSharedFrom))

		By("keeping it when the namespace opts out")
		optIn(ns, false)
		Eventually(func(g Gomega) {
			g.Expect(secretsShared(g, ns).Reason).To(Equal("NotOptedIn"))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, sharedPullSecret), own)).To(Succeed())
			g.Expect(own.Data).To(HaveKeyWithValue(corev1.DockerConfigJsonKey, []byte("own")))
		}, "2s").Should(Succeed())
	})

	It("shares nothing into a namespace without the label", func() {
		ns := newProjectNamespace()
		Eventually(func(g Gomega) {
			cond := secretsShared(g, ns)
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("NotOptedIn"))
			g.Expect(cond.Message).To(ContainSubstring(c4v1beta1.LabelSharedSecrets))
			g.Expect(cond.Message).NotTo(ContainSubstring(sharedToken))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			for _, name := range []string{sharedToken, sharedPullSecret} {
				g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, name), &corev1.Secret{}))).To(BeTrue(), name)
			}
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
			g.Expect(cond.Message).To(Equal(sharedPullSecret + ": missing in namespace " + operatorNamespace))
		}).Should(Succeed())
		optIn(ns, false)
	})
})
