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
	"crypto/rand"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// newEventsProvider is a GitProvider named git in ns, with credentials,
// whose organization is owner.
func newEventsProvider(ns, owner string) *c4v1beta1.GitProvider {
	GinkgoHelper()
	mustCreate(&corev1.Secret{
		ObjectMeta: objectMeta(ns, "git-credentials"),
		StringData: map[string]string{"username": "theos-admin", "token": rand.Text()},
	})
	gp := &c4v1beta1.GitProvider{
		ObjectMeta: objectMeta(ns, "git"),
		Spec: c4v1beta1.GitProviderSpec{
			URL:                  "https://git.example",
			Owner:                owner,
			CredentialsSecretRef: c4v1beta1.LocalRef{Name: "git-credentials"},
		},
	}
	mustCreate(gp)
	return gp
}

// eventsCondition is the Events condition of the GitProvider gp names, nil
// when there is none yet.
func eventsCondition(g Gomega, gp *c4v1beta1.GitProvider) *metav1.Condition {
	var got c4v1beta1.GitProvider
	g.Expect(k8sClient.Get(ctx, key(gp.Namespace, gp.Name), &got)).To(Succeed())
	return condition(got.Status.Conditions, c4v1beta1.ConditionGitProviderEvents)
}

// eventsReason waits until gp's Events condition is False with reason.
func eventsReason(gp *c4v1beta1.GitProvider, reason string) *metav1.Condition {
	GinkgoHelper()
	var cond *metav1.Condition
	Eventually(func(g Gomega) {
		cond = eventsCondition(g, gp)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Reason).To(Equal(reason), cond.Message)
		g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
	}).Should(Succeed())
	return cond
}

// reconcileEvents runs one reconcile of gp, under the generated role, with
// the suite's fake host and the given --git-events-url, as a second operator
// would; the suite's own comes back only after its 10-minute ensure.
func reconcileEvents(gp *c4v1beta1.GitProvider, gitEventsURL string) ctrl.Result {
	GinkgoHelper()
	r := &GitProviderReconciler{Client: managerClient, Scheme: managerClient.Scheme(), APIReader: managerAPIReader,
		Git: fakeGitClient{gitFake}, GitEventsURL: gitEventsURL}
	res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: gp.Namespace,
		Name: gp.Name}})
	Expect(err).NotTo(HaveOccurred())
	return res
}

// eventsSecret is gp's delivery Secret.
func eventsSecret(g Gomega, gp *c4v1beta1.GitProvider) *corev1.Secret {
	var secret corev1.Secret
	g.Expect(k8sClient.Get(ctx, key(gp.Namespace, model.GitEventsSecretName(gp.Name)), &secret)).To(Succeed())
	return &secret
}

// noEventsSecret expects gp to have no delivery Secret.
func noEventsSecret(gp *c4v1beta1.GitProvider) {
	GinkgoHelper()
	var secret corev1.Secret
	err := k8sClient.Get(ctx, key(gp.Namespace, model.GitEventsSecretName(gp.Name)), &secret)
	Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the delivery Secret: %v", err)
}

var _ = Describe("GitProvider events", func() {
	It("makes the delivery Secret once, owned, and registers the hook with it (#1220)", func() {
		ns := newProjectNamespace()
		gp := newEventsProvider(ns, ns)
		hookURL := model.GitEventsURL(testGitEventsURL, ns, "git")
		cond := eventsReason(gp, c4v1beta1.ReasonEventsRegistered)
		Expect(cond.Message).To(ContainSubstring(hookURL))

		By("making the Secret from crypto/rand, owned by the GitProvider")
		secret := eventsSecret(Default, gp)
		value := string(secret.Data[model.GitEventsSecretKey])
		Expect(value).To(HaveLen(26)) // rand.Text: 26 base32 digits, 128 bits
		Expect(controlledBy(secret, gp)).To(BeTrue(), "the Secret's owners: %v", secret.OwnerReferences)

		By("setting the hook to the receiver's URL with the Secret's value")
		Expect(gitFake.EventHooks(ns)).To(Equal([]ports.EventHook{{URL: hookURL, Secret: value}}))

		By("keeping the Secret and the hook on the next reconcile")
		Expect(reconcileEvents(gp, testGitEventsURL).RequeueAfter).To(Equal(defaultEventsEnsure))
		kept := eventsSecret(Default, gp)
		Expect(kept.ResourceVersion).To(Equal(secret.ResourceVersion))
		Expect(string(kept.Data[model.GitEventsSecretKey])).To(Equal(value))
		Expect(gitFake.EventHooks(ns)).To(Equal([]ports.EventHook{{URL: hookURL, Secret: value}}))
		Expect(eventsCondition(Default, gp).Reason).To(Equal(c4v1beta1.ReasonEventsRegistered))

		By("making a new Secret, and setting it on the hook, once the old one is deleted")
		Expect(k8sClient.Delete(ctx, kept)).To(Succeed())
		reconcileEvents(gp, testGitEventsURL)
		rotated := string(eventsSecret(Default, gp).Data[model.GitEventsSecretKey])
		Expect(rotated).NotTo(Equal(value))
		Expect(gitFake.EventHooks(ns)).To(Equal([]ports.EventHook{{URL: hookURL, Secret: rotated}}))
	})

	It("reports RegisterFailed with the host's message, and registers once the host takes it (#1220)", func() {
		ns := newProjectNamespace()
		gitFake.FailEventHook(ns, errors.New("403 Forbidden: user should be an owner of the organization"))
		DeferCleanup(func() { gitFake.FailEventHook(ns, nil) })
		gp := newEventsProvider(ns, ns)
		cond := eventsReason(gp, c4v1beta1.ReasonEventsRegisterFailed)
		Expect(cond.Message).To(ContainSubstring("user should be an owner of the organization"))
		Expect(gitFake.EventHooks(ns)).To(BeEmpty())

		gitFake.FailEventHook(ns, nil)
		Expect(reconcileEvents(gp, testGitEventsURL).RequeueAfter).To(Equal(defaultEventsEnsure))
		Expect(eventsCondition(Default, gp).Reason).To(Equal(c4v1beta1.ReasonEventsRegistered))
		value := string(eventsSecret(Default, gp).Data[model.GitEventsSecretKey])
		Expect(gitFake.EventHooks(ns)).To(Equal([]ports.EventHook{
			{URL: model.GitEventsURL(testGitEventsURL, ns, "git"), Secret: value}}))
	})

	It("reports Unsupported, and makes no Secret, for an adapter without events (#1220)", func() {
		ns := newProjectNamespace()
		owner := noEventsOwnerPrefix + ns
		gp := newEventsProvider(ns, owner)
		eventsReason(gp, c4v1beta1.ReasonEventsUnsupported)
		noEventsSecret(gp)
		Expect(gitFake.EventHooks(owner)).To(BeEmpty())
	})

	It("reports Disabled, with no hook and no Secret, without --git-events-url (#1220)", func() {
		ns := newProjectNamespace()
		owner := noEventsOwnerPrefix + ns
		gp := newEventsProvider(ns, owner)
		// The suite's operator has a --git-events-url; once it has reported
		// this GitProvider, one without reconciles it.
		eventsReason(gp, c4v1beta1.ReasonEventsUnsupported)
		// It looks again all the same (#81): what the host can do follows a
		// Secret that gained what it lacked, and the Secret is not watched.
		Expect(reconcileEvents(gp, "").RequeueAfter).To(Equal(defaultEventsEnsure))
		cond := eventsReason(gp, c4v1beta1.ReasonEventsDisabled)
		Expect(cond.Message).To(ContainSubstring("--git-events-url"))
		noEventsSecret(gp)
		Expect(gitFake.EventHooks(owner)).To(BeEmpty())
	})
})
