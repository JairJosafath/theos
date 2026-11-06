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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// #1614: the operator, under the generated role, issues its webhook
// certificate into a Secret in its namespace and patches the CA into every
// theos.factory.io CRD that converts by webhook, and leaves the rest alone.
var _ = Describe("Webhook certificate", func() {
	It("issues the Secret and patches the CA into the webhook CRDs under the generated role", func() {
		// Every kind serves v1beta1 alone (#1963), so envtest installs every
		// CRD at strategy None. One is set to convert by the operator's
		// webhook, as the chart renders a CRD whose kind's versions differ,
		// and comes back after.
		const webhookCRD, noneCRD = "people.theos.factory.io", "projects.theos.factory.io"
		Expect(WebhookCRDs).To(ContainElements(webhookCRD, noneCRD))
		var crd apiextensionsv1.CustomResourceDefinition
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: webhookCRD}, &crd)).To(Succeed())
		before := crd.Spec.Conversion.DeepCopy()
		DeferCleanup(func() {
			var crd apiextensionsv1.CustomResourceDefinition
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: webhookCRD}, &crd)).To(Succeed())
			crd.Spec.Conversion = before
			Expect(k8sClient.Update(ctx, &crd)).To(Succeed())
		})
		crd.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{
			Strategy: apiextensionsv1.WebhookConverter,
			Webhook: &apiextensionsv1.WebhookConversion{ConversionReviewVersions: []string{"v1"},
				ClientConfig: &apiextensionsv1.WebhookClientConfig{Service: &apiextensionsv1.ServiceReference{
					Namespace: operatorNamespace, Name: "theos-webhook-service", Path: new("/convert")}}},
		}
		Expect(k8sClient.Update(ctx, &crd)).To(Succeed())

		operator, err := client.New(managerCfg, client.Options{Scheme: scheme.Scheme})
		Expect(err).NotTo(HaveOccurred())
		w := &WebhookCert{Client: operator, Namespace: operatorNamespace,
			Secret: "theos-webhook-cert", Service: "theos-webhook-service"}
		Expect(w.Ensure(ctx)).To(Succeed())
		_, err = w.GetCertificate(nil)
		Expect(err).NotTo(HaveOccurred())

		var secret corev1.Secret
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: operatorNamespace, Name: w.Secret}, &secret)).
			To(Succeed())
		Expect(secret.Type).To(Equal(corev1.SecretTypeTLS))
		ca := secret.Data[webhookCAKey]
		Expect(ca).NotTo(BeEmpty())

		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: webhookCRD}, &crd)).To(Succeed())
		Expect(crd.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(ca))
		var none apiextensionsv1.CustomResourceDefinition
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: noneCRD}, &none)).To(Succeed())
		if none.Spec.Conversion != nil {
			Expect(none.Spec.Conversion.Strategy).To(Equal(apiextensionsv1.NoneConverter))
			Expect(none.Spec.Conversion.Webhook).To(BeNil())
		}

		By("a second replica reads the Secret the first wrote")
		other := &WebhookCert{Client: operator, Namespace: operatorNamespace, Secret: w.Secret, Service: w.Service}
		Expect(other.Ensure(ctx)).To(Succeed())
		var again corev1.Secret
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&secret), &again)).To(Succeed())
		Expect(again.ResourceVersion).To(Equal(secret.ResourceVersion))
	})
})
