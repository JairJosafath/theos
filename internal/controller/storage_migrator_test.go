/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"errors"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
)

// #1616: the migrator rewrites what was stored at v1alpha1 once v1beta1 is the
// storage version, and brings the CRD's storedVersions down to it.
var _ = Describe("Storage migrator", func() {
	const crdName = "people.c4.factory.io"

	// setStorage makes version the CRD's storage version.
	setStorage := func(version string) {
		GinkgoHelper()
		var crd apiextensionsv1.CustomResourceDefinition
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: crdName}, &crd)).To(Succeed())
		for i := range crd.Spec.Versions {
			crd.Spec.Versions[i].Storage = crd.Spec.Versions[i].Name == version
		}
		Expect(k8sClient.Update(ctx, &crd)).To(Succeed())
	}
	storedVersions := func() []string {
		GinkgoHelper()
		var crd apiextensionsv1.CustomResourceDefinition
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: crdName}, &crd)).To(Succeed())
		return crd.Status.StoredVersions
	}
	setStoredVersions := func(versions ...string) {
		GinkgoHelper()
		var crd apiextensionsv1.CustomResourceDefinition
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: crdName}, &crd)).To(Succeed())
		base := crd.DeepCopy()
		crd.Status.StoredVersions = versions
		Expect(k8sClient.Status().Patch(ctx, &crd, client.MergeFrom(base))).To(Succeed())
	}

	It("rewrites the objects stored at v1alpha1 and brings storedVersions down to v1beta1 under the generated role",
		func() {
			By("installing the CRD as a release storing v1alpha1 had")
			DeferCleanup(func() {
				setStorage("v1beta1")
				setStoredVersions("v1beta1")
			})
			setStorage("v1alpha1")
			setStoredVersions("v1alpha1")

			By("storing objects at v1alpha1, more than one page's worth")
			ns := newProjectNamespace()
			const people = 3
			stored := map[string]string{}
			for i := range people {
				p := &c4v1alpha1.Person{ObjectMeta: metav1.ObjectMeta{GenerateName: "reader-", Namespace: ns},
					Spec: c4v1alpha1.PersonSpec{DisplayName: "Reader"}}
				Expect(k8sClient.Create(ctx, p)).To(Succeed(), i)
				stored[p.Name] = p.ResourceVersion
			}

			By("moving the storage version to v1beta1, as the upgrade does")
			setStorage("v1beta1")
			Expect(storedVersions()).To(ConsistOf("v1alpha1", "v1beta1"))

			By("running the migrator as the operator")
			operator, err := client.New(managerCfg, client.Options{Scheme: scheme.Scheme})
			Expect(err).NotTo(HaveOccurred())
			ready := 0
			m := &StorageMigrator{Client: operator, CRDs: []string{crdName}, PageSize: 2, Retry: 1,
				Ready: func(*http.Request) error {
					// Not before the webhook is ready: it says so once.
					ready++
					if ready == 1 {
						return errors.New("not yet")
					}
					return nil
				}}
			Expect(m.Start(ctx)).To(Succeed())
			Expect(ready).To(BeNumerically(">=", 2))
			Expect(storedVersions()).To(Equal([]string{"v1beta1"}))

			By("rewriting every object, kept and readable at v1beta1")
			var list c4v1beta1.PersonList
			Expect(k8sClient.List(ctx, &list, client.InNamespace(ns))).To(Succeed())
			Expect(list.Items).To(HaveLen(people))
			for _, p := range list.Items {
				Expect(p.Spec.DisplayName).To(Equal("Reader"))
				Expect(p.ResourceVersion).NotTo(Equal(stored[p.Name]), "%s was not rewritten", p.Name)
			}

			By("doing nothing on a later pass")
			Expect(m.Migrate(ctx)).To(Succeed())
			Expect(storedVersions()).To(Equal([]string{"v1beta1"}))
		})

	// #1884: a chart with webhook.enabled=false has every CRD at strategy
	// None and no webhook server; the migrator still rewrites what etcd holds
	// at v1alpha1, without waiting on a webhook that never comes.
	It("rewrites a kind at strategy None without waiting on the webhook", func() {
		By("installing the CRD as a release with the webhook off has it")
		var crd apiextensionsv1.CustomResourceDefinition
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: crdName}, &crd)).To(Succeed())
		conversion := crd.Spec.Conversion.DeepCopy()
		DeferCleanup(func() {
			var crd apiextensionsv1.CustomResourceDefinition
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: crdName}, &crd)).To(Succeed())
			crd.Spec.Conversion = conversion
			for i := range crd.Spec.Versions {
				crd.Spec.Versions[i].Storage = crd.Spec.Versions[i].Name == "v1beta1"
			}
			Expect(k8sClient.Update(ctx, &crd)).To(Succeed())
			setStoredVersions("v1beta1")
		})
		crd.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{Strategy: apiextensionsv1.NoneConverter}
		for i := range crd.Spec.Versions {
			crd.Spec.Versions[i].Storage = crd.Spec.Versions[i].Name == "v1alpha1"
		}
		Expect(k8sClient.Update(ctx, &crd)).To(Succeed())
		setStoredVersions("v1alpha1")

		By("storing an object at v1alpha1, then moving the storage version to v1beta1")
		ns := newProjectNamespace()
		p := &c4v1alpha1.Person{ObjectMeta: metav1.ObjectMeta{GenerateName: "reader-", Namespace: ns},
			Spec: c4v1alpha1.PersonSpec{DisplayName: "Reader"}}
		Expect(k8sClient.Create(ctx, p)).To(Succeed())
		setStorage("v1beta1")
		Expect(storedVersions()).To(ConsistOf("v1alpha1", "v1beta1"))

		By("running the migrator as the operator, against a webhook that never serves")
		operator, err := client.New(managerCfg, client.Options{Scheme: scheme.Scheme})
		Expect(err).NotTo(HaveOccurred())
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		m := &StorageMigrator{Client: operator, CRDs: []string{crdName}, Retry: 1, ReadyPoll: 10 * time.Millisecond,
			Ready: func(*http.Request) error { return errors.New("the webhook is off") }}
		Expect(m.Start(bounded)).To(Succeed())
		Expect(bounded.Err()).To(Succeed(), "the migrator waited on the webhook for a CRD at strategy None")
		Expect(storedVersions()).To(Equal([]string{"v1beta1"}))

		var got c4v1beta1.Person
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(p), &got)).To(Succeed())
		Expect(got.ResourceVersion).NotTo(Equal(p.ResourceVersion), "%s was not rewritten", p.Name)
	})
})
