/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// #1616: the migrator rewrites what etcd holds at an older version once the
// storage version moves, and brings the CRD's storedVersions down to the
// storage version. Every kind serves v1beta1 alone (#1963), so the spec adds
// a next version beside it, as the release that brings one will, and moves
// the storage there.
var _ = Describe("Storage migrator", func() {
	const crdName = "people.theos.factory.io"
	hub := c4v1beta1.GroupVersion.Version
	const next = "v1"

	getCRD := func() *apiextensionsv1.CustomResourceDefinition {
		GinkgoHelper()
		var crd apiextensionsv1.CustomResourceDefinition
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: crdName}, &crd)).To(Succeed())
		return &crd
	}
	// setStorage makes version the CRD's storage version.
	setStorage := func(version string) {
		GinkgoHelper()
		crd := getCRD()
		for i := range crd.Spec.Versions {
			crd.Spec.Versions[i].Storage = crd.Spec.Versions[i].Name == version
		}
		Expect(k8sClient.Update(ctx, crd)).To(Succeed())
	}

	// #1884: a CRD at strategy None converts in the API server, so the
	// migrator rewrites its objects without waiting on the webhook, which
	// never serves when the chart has webhook.enabled=false.
	It("rewrites the objects stored at the older version and brings storedVersions down under the generated role, "+
		"never waiting on the webhook for a CRD at strategy None", func() {
		operator, err := client.New(managerCfg, client.Options{Scheme: scheme.Scheme})
		Expect(err).NotTo(HaveOccurred())

		By("adding the next version beside v1beta1, with its schema, at strategy None")
		crd := getCRD()
		Expect(crd.Spec.Versions).To(HaveLen(1))
		if crd.Spec.Conversion != nil {
			Expect(crd.Spec.Conversion.Strategy).To(Equal(apiextensionsv1.NoneConverter))
		}
		added := *crd.Spec.Versions[0].DeepCopy()
		added.Name, added.Storage = next, false
		crd.Spec.Versions = append(crd.Spec.Versions, added)
		Expect(k8sClient.Update(ctx, crd)).To(Succeed())
		DeferCleanup(func() {
			// Back to v1beta1 alone: the objects are rewritten there first, since
			// a version storedVersions still names can't be removed.
			setStorage(hub)
			Expect((&StorageMigrator{Client: operator, CRDs: []string{crdName}}).Migrate(ctx)).To(Succeed())
			crd := getCRD()
			Expect(crd.Status.StoredVersions).To(Equal([]string{hub}))
			crd.Spec.Versions = crd.Spec.Versions[:1]
			Expect(crd.Spec.Versions[0].Name).To(Equal(hub))
			Expect(k8sClient.Update(ctx, crd)).To(Succeed())
		})

		By("storing objects at v1beta1, more than one page's worth")
		ns := newProjectNamespace()
		const people = 3
		stored := map[string]string{}
		for i := range people {
			p := &c4v1beta1.Person{ObjectMeta: metav1.ObjectMeta{GenerateName: "reader-", Namespace: ns},
				Spec: c4v1beta1.PersonSpec{DisplayName: "Reader"}}
			Expect(k8sClient.Create(ctx, p)).To(Succeed(), i)
			stored[p.Name] = p.ResourceVersion
		}

		By("moving the storage version to the next, as its release does")
		setStorage(next)
		Expect(getCRD().Status.StoredVersions).To(ConsistOf(hub, next))

		By("running the migrator as the operator, against a webhook that never serves")
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		m := &StorageMigrator{Client: operator, CRDs: []string{crdName}, PageSize: 2, Retry: 1,
			ReadyPoll: 10 * time.Millisecond, Ready: func(*http.Request) error { return errors.New("the webhook is off") }}
		Expect(m.Start(bounded)).To(Succeed())
		Expect(bounded.Err()).To(Succeed(), "the migrator waited on the webhook for a CRD at strategy None")
		Expect(getCRD().Status.StoredVersions).To(Equal([]string{next}))

		By("rewriting every object, kept and readable at the next version")
		var list unstructured.UnstructuredList
		list.SetAPIVersion(c4v1beta1.GroupVersion.Group + "/" + next)
		list.SetKind("PersonList")
		Expect(operator.List(ctx, &list, client.InNamespace(ns))).To(Succeed())
		Expect(list.Items).To(HaveLen(people))
		for _, p := range list.Items {
			name, _, _ := unstructured.NestedString(p.Object, "spec", "displayName")
			Expect(name).To(Equal("Reader"))
			Expect(p.GetResourceVersion()).NotTo(Equal(stored[p.GetName()]), "%s was not rewritten", p.GetName())
		}

		By("doing nothing on a later pass")
		Expect(m.Migrate(ctx)).To(Succeed())
		Expect(getCRD().Status.StoredVersions).To(Equal([]string{next}))
	})
})
