/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// newBrowserEnvironment is a local browser TestEnvironment in ns.
func newBrowserEnvironment(ns string) *c4v1beta1.TestEnvironment {
	return &c4v1beta1.TestEnvironment{ObjectMeta: objectMeta(ns, "browser"), Spec: c4v1beta1.TestEnvironmentSpec{
		Description: "Chromium, Firefox and WebKit with Playwright, and a virtual display",
		Local:       &c4v1beta1.TestEnvironmentLocal{Image: "ghcr.io/jairjosafath/theos/env-browser:0.31.0"}}}
}

// testEnvReady waits for env's Ready to have status and reason, and returns it.
func testEnvReady(env *c4v1beta1.TestEnvironment, status metav1.ConditionStatus, reason string) *metav1.Condition {
	GinkgoHelper()
	var cond *metav1.Condition
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(env.Namespace, env.Name), env)).To(Succeed())
		cond = condition(env.Status.Conditions, c4v1beta1.ConditionTestEnvironmentReady)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Reason).To(Equal(reason), cond.Message)
		g.Expect(cond.Status).To(Equal(status))
	}).Should(Succeed())
	return cond
}

var _ = Describe("TestEnvironment", func() {
	It("defaults its limits", func() {
		env := newBrowserEnvironment(newProjectNamespace())
		mustCreate(env)
		Expect(env.Spec.Limits.MaxLeases).To(Equal(int32(model.DefaultMaxLeases)))
		Expect(env.Spec.Limits.MaxLeaseMinutes).To(Equal(int32(model.DefaultMaxLeaseMinutes)))
	})

	It("is Ready in a Project's namespace, whose fence for its pods the operator makes", func() {
		env := newBrowserEnvironment(newProjectNamespace())
		mustCreate(env)
		testEnvReady(env, metav1.ConditionTrue, model.TestEnvReady)
	})

	It("is not Ready without the project's fence for its pods", func() {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "no-project-"}}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		env := newBrowserEnvironment(ns.Name)
		mustCreate(env)
		cond := testEnvReady(env, metav1.ConditionFalse, model.TestEnvFenceMissing)
		Expect(cond.Message).To(ContainSubstring(model.TestEnvNetworkPolicy))
		Expect(k8sClient.Delete(ctx, env)).To(Succeed())
	})

	It("returns a lease whose holder is gone or no longer lists it, and lists the ones held", func() {
		ns := newProjectNamespace()
		env := newBrowserEnvironment(ns)
		mustCreate(env)
		testEnvReady(env, metav1.ConditionTrue, model.TestEnvReady)
		held := newTask(ns, "add-crop", "Crop the image")
		mustCreate(held)
		kept, dropped := model.LeaseID(env.Name, "add-crop-r1-implement"), model.LeaseID(env.Name, "add-crop-r1-verify")
		// The Task reconciler writes the Task's status too: try again on a conflict.
		Eventually(func() error {
			if err := k8sClient.Get(ctx, key(ns, held.Name), held); err != nil {
				return err
			}
			held.Status.Environments = []c4v1beta1.TaskEnvironmentLease{{Name: env.Name, Lease: kept,
				Sandbox: "add-crop-r1-implement"}}
			return k8sClient.Status().Update(ctx, held)
		}).Should(Succeed())
		environment := testEnvironment(env, nil)
		lease := func(holder, run string) string {
			l, err := envFake.Lease(ctx, ports.LeaseRequest{ID: model.LeaseID(env.Name, run), Env: environment,
				Holder: holder, Token: "t", Deadline: time.Hour})
			Expect(err).NotTo(HaveOccurred())
			return l.ID
		}
		lease(held.Name, "add-crop-r1-implement")
		lease(held.Name, "add-crop-r1-verify")
		orphan := lease("gone", "gone-r1-implement")
		Eventually(func(g Gomega) {
			g.Expect(envFake.Returned()).To(ContainElements(ns+"/"+orphan, ns+"/"+dropped))
			g.Expect(k8sClient.Get(ctx, key(ns, env.Name), env)).To(Succeed())
			g.Expect(env.Status.Leases).To(HaveLen(1))
			g.Expect(env.Status.Leases[0].ID).To(Equal(kept))
			g.Expect(env.Status.Leases[0].Holder).To(Equal(held.Name))
			g.Expect(env.Status.Leases[0].State).To(Equal(model.LeaseReady))
			g.Expect(env.Status.SpentToday).NotTo(BeNil())
		}).WithTimeout(30 * time.Second).Should(Succeed())
		Expect(envFake.Returned()).NotTo(ContainElement(ns + "/" + kept))
	})

	DescribeTable("refuses a spec its CEL rules or schema reject",
		func(mutate func(*c4v1beta1.TestEnvironment), message string) {
			env := newBrowserEnvironment(newProjectNamespace())
			mutate(env)
			Expect(k8sClient.Create(ctx, env)).To(MatchError(ContainSubstring(message)))
		},
		Entry("no member", func(env *c4v1beta1.TestEnvironment) { env.Spec.Local = nil },
			"set exactly one provider member: local"),
		Entry("an image by latest", func(env *c4v1beta1.TestEnvironment) {
			env.Spec.Local.Image = "mcr.microsoft.com/playwright:latest"
		}, "image must be pinned"),
		Entry("an image without a tag or digest", func(env *c4v1beta1.TestEnvironment) {
			env.Spec.Local.Image = "localhost:5000/env-browser"
		}, "image must be pinned"),
		Entry("a device that is no extended resource", func(env *c4v1beta1.TestEnvironment) {
			env.Spec.Local.Devices = map[corev1.ResourceName]resource.Quantity{corev1.ResourceCPU: resource.MustParse("1")}
		}, "devices are extended resources"),
		Entry("a name a job can't ask for", func(env *c4v1beta1.TestEnvironment) {
			env.Name = "2d-engine"
		}, "a DNS label of at most 32 characters"),
		Entry("too many live leases", func(env *c4v1beta1.TestEnvironment) {
			env.Spec.Limits.MaxLeases = 17
		}, "spec.limits.maxLeases"),
	)
})
