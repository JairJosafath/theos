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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// newAlertSource is an alertmanager AlertSource alerts in ns, checked by
// Secret alerts-webhook.
func newAlertSource(ns string) *c4v1beta1.AlertSource {
	return &c4v1beta1.AlertSource{ObjectMeta: objectMeta(ns, "alerts"), Spec: c4v1beta1.AlertSourceSpec{
		Type: c4v1beta1.AlertSourceAlertmanager, SecretRef: c4v1beta1.LocalRef{Name: "alerts-webhook"}}}
}

// alertSourceReady waits for src's Ready to be False with reason, and returns it.
func alertSourceReady(src *c4v1beta1.AlertSource, reason string) *metav1.Condition {
	GinkgoHelper()
	var cond *metav1.Condition
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(src.Namespace, src.Name), src)).To(Succeed())
		cond = condition(src.Status.Conditions, c4v1beta1.ConditionAlertSourceReady)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Reason).To(Equal(reason), cond.Message)
		g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(cond.ObservedGeneration).To(Equal(src.Generation))
	}).Should(Succeed())
	return cond
}

// createAlertSourceRaw creates src with edit applied to its spec as the API
// server reads it: for a value the typed client leaves out, such as "".
func createAlertSourceRaw(src *c4v1beta1.AlertSource, edit func(spec map[string]any)) error {
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(src)
	Expect(err).NotTo(HaveOccurred())
	u := &unstructured.Unstructured{Object: obj}
	u.SetGroupVersionKind(c4v1beta1.GroupVersion.WithKind("AlertSource"))
	spec, _, err := unstructured.NestedMap(u.Object, "spec")
	Expect(err).NotTo(HaveOccurred())
	edit(spec)
	Expect(unstructured.SetNestedMap(u.Object, spec, "spec")).To(Succeed())
	return k8sClient.Create(ctx, u)
}

// updateAlertSource applies mutate to the stored src and updates it,
// returning the update's error, a conflict tried again.
func updateAlertSource(src *c4v1beta1.AlertSource, mutate func(*c4v1beta1.AlertSource)) error {
	GinkgoHelper()
	var err error
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(src.Namespace, src.Name), src)).To(Succeed())
		mutate(src)
		err = k8sClient.Update(ctx, src)
		g.Expect(apierrors.IsConflict(err)).To(BeFalse())
	}).Should(Succeed())
	return err
}

var _ = Describe("AlertSource", func() {
	It("defaults its labels, comment interval and daily limit", func() {
		src := newAlertSource(newProjectNamespace())
		mustCreate(src)
		Expect(src.Spec.ContainerLabel).To(Equal(model.DefaultAlertContainerLabel))
		Expect(src.Spec.NamespaceLabel).To(Equal(model.DefaultAlertNamespaceLabel))
		Expect(src.Spec.CommentInterval).NotTo(BeNil())
		Expect(src.Spec.CommentInterval.Duration).To(Equal(model.DefaultAlertCommentInterval))
		Expect(src.Spec.DailyLimit).To(Equal(int32(model.DefaultAlertDailyLimit)))
	})

	It("is SecretMissing without its Secret, and ReceiverOff once the Secret holds the token", func() {
		ns := newProjectNamespace()
		src := newAlertSource(ns)
		mustCreate(src)
		cond := alertSourceReady(src, c4v1beta1.ReasonAlertSourceSecretMissing)
		Expect(cond.Message).To(ContainSubstring("Secret alerts-webhook is missing"))

		mustCreate(&corev1.Secret{ObjectMeta: objectMeta(ns, "alerts-webhook"),
			Data: map[string][]byte{model.AlertSecretKeyToken: []byte("a-token")}})
		cond = alertSourceReady(src, c4v1beta1.ReasonAlertSourceReceiverOff)
		Expect(cond.Message).To(ContainSubstring("takes no deliveries"))
	})

	It("is SecretMissing while the key its type reads is empty, and ReceiverOff once it has a value", func() {
		ns := newProjectNamespace()
		secret := &corev1.Secret{ObjectMeta: objectMeta(ns, "alerts-webhook"),
			Data: map[string][]byte{model.AlertSecretKeySecret: {}, model.AlertSecretKeyToken: []byte("a-token")}}
		mustCreate(secret)
		// A sentry source reads "secret": the token beside it is not its key.
		src := newAlertSource(ns)
		src.Spec.Type = c4v1beta1.AlertSourceSentry
		mustCreate(src)
		cond := alertSourceReady(src, c4v1beta1.ReasonAlertSourceSecretMissing)
		Expect(cond.Message).To(ContainSubstring(`has an empty "secret", which is no credential`))

		secret.Data[model.AlertSecretKeySecret] = []byte("a-client-secret")
		Expect(k8sClient.Update(ctx, secret)).To(Succeed())
		alertSourceReady(src, c4v1beta1.ReasonAlertSourceReceiverOff)
	})

	It("is SecretMissing when its Secret has none of the keys its type reads", func() {
		ns := newProjectNamespace()
		mustCreate(&corev1.Secret{ObjectMeta: objectMeta(ns, "alerts-webhook"),
			Data: map[string][]byte{"password": []byte("a-password")}})
		src := newAlertSource(ns)
		src.Spec.Type = c4v1beta1.AlertSourceGeneric
		mustCreate(src)
		cond := alertSourceReady(src, c4v1beta1.ReasonAlertSourceSecretMissing)
		Expect(cond.Message).To(ContainSubstring(`has no "secret" or "token"`))
	})

	DescribeTable("refuses a spec its CEL rules or schema reject",
		func(mutate func(*c4v1beta1.AlertSource), message string) {
			src := newAlertSource(newProjectNamespace())
			mutate(src)
			Expect(k8sClient.Create(ctx, src)).To(MatchError(ContainSubstring(message)))
		},
		Entry("a type that is none of the three", func(src *c4v1beta1.AlertSource) {
			src.Spec.Type = "nagios"
		}, `spec.type: Unsupported value: "nagios"`),
		Entry("a route with both namespace and project", func(src *c4v1beta1.AlertSource) {
			src.Spec.Routes = []c4v1beta1.AlertSourceRoute{{Container: "storefront", Namespace: "shop", Project: "web"}}
		}, "a route has exactly one of namespace and project"),
		Entry("a route with neither namespace nor project", func(src *c4v1beta1.AlertSource) {
			src.Spec.Routes = []c4v1beta1.AlertSourceRoute{{Container: "storefront"}}
		}, "a route has exactly one of namespace and project"),
		Entry("more than 32 routes", func(src *c4v1beta1.AlertSource) {
			for i := range 33 {
				src.Spec.Routes = append(src.Spec.Routes,
					c4v1beta1.AlertSourceRoute{Container: "storefront", Namespace: fmt.Sprintf("ns-%d", i)})
			}
		}, "spec.routes: Too many: 33: must have at most 32 items"),
		Entry("a comment interval under a minute", func(src *c4v1beta1.AlertSource) {
			src.Spec.CommentInterval = &metav1.Duration{Duration: 30 * time.Second}
		}, "commentInterval must be a duration of at least 1m"),
		Entry("a daily limit over 100", func(src *c4v1beta1.AlertSource) {
			src.Spec.DailyLimit = 101
		}, "spec.dailyLimit: Invalid value: 101: spec.dailyLimit in body should be less than or equal to 100"),
		Entry("a containerLabel of a space, which the parsers would find on no alert", func(src *c4v1beta1.AlertSource) {
			src.Spec.ContainerLabel = " "
		}, "spec.containerLabel: Invalid value"),
		Entry("a namespaceLabel with a control character", func(src *c4v1beta1.AlertSource) {
			src.Spec.NamespaceLabel = "name\tspace"
		}, "spec.namespaceLabel: Invalid value"),
		Entry("a containerLabel over 100 characters", func(src *c4v1beta1.AlertSource) {
			src.Spec.ContainerLabel = fmt.Sprintf("%0101d", 0)
		}, "spec.containerLabel: Too long"),
		Entry("#187: containerLabel and namespaceLabel both set to one name", func(src *c4v1beta1.AlertSource) {
			src.Spec.ContainerLabel, src.Spec.NamespaceLabel = "app", "app"
		}, "containerLabel and namespaceLabel must differ"),
		Entry("#187: containerLabel set to namespaceLabel's default", func(src *c4v1beta1.AlertSource) {
			src.Spec.ContainerLabel = model.DefaultAlertNamespaceLabel
		}, "containerLabel and namespaceLabel must differ"),
		Entry("#187: namespaceLabel set to containerLabel's default", func(src *c4v1beta1.AlertSource) {
			src.Spec.NamespaceLabel = model.DefaultAlertContainerLabel
		}, "containerLabel and namespaceLabel must differ"),
	)

	DescribeTable("refuses a value the typed client can't send",
		func(edit func(spec map[string]any), message string) {
			err := createAlertSourceRaw(newAlertSource(newProjectNamespace()), edit)
			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("an empty containerLabel", func(spec map[string]any) { spec["containerLabel"] = "" },
			"spec.containerLabel: Invalid value"),
		Entry("an empty namespaceLabel", func(spec map[string]any) { spec["namespaceLabel"] = "" },
			"spec.namespaceLabel: Invalid value"),
		Entry("a comment interval that is no duration", func(spec map[string]any) { spec["commentInterval"] = "soon" },
			"spec.commentInterval"),
	)

	It("keeps its type", func() {
		src := newAlertSource(newProjectNamespace())
		mustCreate(src)
		err := updateAlertSource(src, func(s *c4v1beta1.AlertSource) { s.Spec.Type = c4v1beta1.AlertSourceGeneric })
		Expect(err).To(MatchError(ContainSubstring("type is immutable")))
	})

	DescribeTable("#187: refuses an update that makes containerLabel and namespaceLabel one name",
		func(mutate func(*c4v1beta1.AlertSource)) {
			src := newAlertSource(newProjectNamespace())
			src.Spec.ContainerLabel, src.Spec.NamespaceLabel = "app", "exported_namespace"
			mustCreate(src)
			err := updateAlertSource(src, mutate)
			Expect(err).To(MatchError(ContainSubstring("containerLabel and namespaceLabel must differ")))
		},
		Entry("both set to one name", func(src *c4v1beta1.AlertSource) {
			src.Spec.NamespaceLabel = "app"
		}),
		Entry("containerLabel set to namespaceLabel's default, namespaceLabel left at it", func(src *c4v1beta1.AlertSource) {
			src.Spec.ContainerLabel, src.Spec.NamespaceLabel = model.DefaultAlertNamespaceLabel, ""
		}),
		Entry("namespaceLabel set to containerLabel's default, containerLabel left at it", func(src *c4v1beta1.AlertSource) {
			src.Spec.ContainerLabel, src.Spec.NamespaceLabel = "", model.DefaultAlertContainerLabel
		}),
	)
})
