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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// #1614: the operator, under the generated role, issues its webhook
// certificate into a Secret in its namespace and patches the CA into every
// c4.factory.io CRD that converts by webhook, reading the others.
var _ = Describe("Webhook certificate", func() {
	It("issues the Secret and patches the CA into the webhook CRDs under the generated role", func() {
		// Two CRDs converting by webhook, as step 3 of docs/plans/api-versions.md
		// makes every one; the rest stay at strategy None and are only read.
		converting := []string{"people.c4.factory.io", "computes.c4.factory.io"}
		for _, name := range converting {
			var crd apiextensionsv1.CustomResourceDefinition
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: name}, &crd)).To(Succeed())
			before := crd.Spec.Conversion.DeepCopy()
			crd.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{
				Strategy: apiextensionsv1.WebhookConverter,
				Webhook: &apiextensionsv1.WebhookConversion{ConversionReviewVersions: []string{"v1"},
					ClientConfig: &apiextensionsv1.WebhookClientConfig{Service: &apiextensionsv1.ServiceReference{
						Namespace: operatorNamespace, Name: "c4-factory-webhook-service"}}},
			}
			Expect(k8sClient.Update(ctx, &crd)).To(Succeed())
			DeferCleanup(func() {
				var crd apiextensionsv1.CustomResourceDefinition
				Expect(k8sClient.Get(ctx, client.ObjectKey{Name: name}, &crd)).To(Succeed())
				crd.Spec.Conversion = before
				Expect(k8sClient.Update(ctx, &crd)).To(Succeed())
			})
		}

		operator, err := client.New(managerCfg, client.Options{Scheme: scheme.Scheme})
		Expect(err).NotTo(HaveOccurred())
		w := &WebhookCert{Client: operator, Namespace: operatorNamespace,
			Secret: "c4-factory-webhook-cert", Service: "c4-factory-webhook-service"}
		Expect(w.Ensure(ctx)).To(Succeed())
		_, err = w.GetCertificate(nil)
		Expect(err).NotTo(HaveOccurred())

		var secret corev1.Secret
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: operatorNamespace, Name: w.Secret}, &secret)).
			To(Succeed())
		Expect(secret.Type).To(Equal(corev1.SecretTypeTLS))
		ca := secret.Data[webhookCAKey]
		Expect(ca).NotTo(BeEmpty())

		for _, name := range WebhookCRDs {
			var crd apiextensionsv1.CustomResourceDefinition
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: name}, &crd)).To(Succeed())
			if crd.Spec.Conversion.Strategy == apiextensionsv1.WebhookConverter {
				Expect(crd.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(ca), name)
			} else {
				Expect(crd.Spec.Conversion.Webhook).To(BeNil(), name)
			}
		}

		By("a second replica reads the Secret the first wrote")
		other := &WebhookCert{Client: operator, Namespace: operatorNamespace, Secret: w.Secret, Service: w.Service}
		Expect(other.Ensure(ctx)).To(Succeed())
		var again corev1.Secret
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(&secret), &again)).To(Succeed())
		Expect(again.ResourceVersion).To(Equal(secret.ResourceVersion))
	})
})
