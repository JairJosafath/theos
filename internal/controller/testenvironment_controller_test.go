/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"crypto/rand"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
				Holder: holder, Token: rand.Text(), Deadline: time.Hour})
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
		Entry("two members", func(env *c4v1beta1.TestEnvironment) {
			env.Spec.AWS = newGPUEnvironment(env.Namespace).Spec.AWS
		}, "set exactly one provider member: local or aws"),
		Entry("an aws member without a budget (step 7)", func(env *c4v1beta1.TestEnvironment) {
			env.Spec.Local, env.Spec.AWS = nil, newGPUEnvironment(env.Namespace).Spec.AWS
		}, "an aws environment needs limits.budgetUSDPerDay"),
		Entry("a Mac's host settings on a type that is no Mac (step 9)", func(env *c4v1beta1.TestEnvironment) {
			*env = *newGPUEnvironment(env.Namespace)
			env.Spec.AWS.Mac = &c4v1beta1.TestEnvironmentMac{KeepHostHours: 24}
		}, "mac is for an EC2 Mac instanceType"),
		Entry("a Mac host kept less than the 24 hours EC2 bills it for (step 9)", func(env *c4v1beta1.TestEnvironment) {
			*env = *newGPUEnvironment(env.Namespace)
			env.Spec.AWS.InstanceType = "mac2.metal"
			env.Spec.AWS.Mac = &c4v1beta1.TestEnvironmentMac{KeepHostHours: 12}
		}, "spec.aws.mac.keepHostHours"),
		Entry("an image that is no AMI", func(env *c4v1beta1.TestEnvironment) {
			*env = *newGPUEnvironment(env.Namespace)
			env.Spec.AWS.ImageID = "ubuntu-24.04"
		}, "imageId must be an AMI id"),
	)
})

// newGPUEnvironment is an aws TestEnvironment gpu in ns, of account
// 123456789012, its credential in Secret gpu-aws, with a budget of a dollar
// a day.
func newGPUEnvironment(ns string) *c4v1beta1.TestEnvironment {
	return &c4v1beta1.TestEnvironment{ObjectMeta: objectMeta(ns, "gpu"), Spec: c4v1beta1.TestEnvironmentSpec{
		Description: "An NVIDIA A10G with Godot and its export templates",
		AWS: &c4v1beta1.TestEnvironmentAWS{Region: "eu-west-1", Account: "123456789012", InstanceType: "g5.xlarge",
			ImageID: "ami-0123456789abcdef0", SubnetID: "subnet-0123456789abcdef0",
			CredentialsSecretRef: c4v1beta1.LocalRef{Name: "gpu-aws"}},
		Limits: c4v1beta1.TestEnvironmentLimits{BudgetUSDPerDay: 1}}}
}

// gpuCredential makes Secret gpu-aws in ns with a new key of account, which
// envAWSFake takes, and returns the key's ID.
func gpuCredential(ns, account string) string {
	GinkgoHelper()
	key := rand.Text()
	envAWSFake.AddAccount(key, account)
	mustCreate(&corev1.Secret{ObjectMeta: objectMeta(ns, "gpu-aws"),
		Data: map[string][]byte{"accessKeyId": []byte(key), "secretAccessKey": []byte(rand.Text())}})
	return key
}

// credentialsValid waits for env's CredentialsValid to have status and
// reason, and returns it.
func credentialsValid(env *c4v1beta1.TestEnvironment, status metav1.ConditionStatus, reason string) *metav1.Condition {
	GinkgoHelper()
	var cond *metav1.Condition
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(env.Namespace, env.Name), env)).To(Succeed())
		cond = condition(env.Status.Conditions, c4v1beta1.ConditionTestEnvironmentCredentialsValid)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Reason).To(Equal(reason), cond.Message)
		g.Expect(cond.Status).To(Equal(status))
	}, "20s").Should(Succeed())
	return cond
}

// docs/plans/test-environments.md, step 7: a remote member's credential and
// budget, on the fake provider; the EC2 adapter itself is tested on a fake
// EC2 in internal/adapters/awsenv.
var _ = Describe("TestEnvironment on AWS", func() {
	It("checks its credential: a missing Secret or key, or a key of another account, is CredentialsValid=False, "+
		"and a run waits until it is valid", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		env := newGPUEnvironment(ns)
		mustCreate(newAgent(ns, 3), env)
		cond := credentialsValid(env, metav1.ConditionFalse, model.TestEnvSecretMissing)
		Expect(cond.Message).To(ContainSubstring("Secret gpu-aws, with the keys accessKeyId and secretAccessKey"))
		testEnvReady(env, metav1.ConditionFalse, model.TestEnvCredentialsInvalid)

		By("a Secret without its secret key")
		accessKey := rand.Text()
		secret := &corev1.Secret{ObjectMeta: objectMeta(ns, "gpu-aws"),
			Data: map[string][]byte{"accessKeyId": []byte(accessKey)}}
		mustCreate(secret)
		cond = credentialsValid(env, metav1.ConditionFalse, model.TestEnvSecretIncomplete)
		Expect(cond.Message).To(ContainSubstring("lacks secretAccessKey"))

		By("a key of another account")
		envAWSFake.AddAccount(accessKey, "210987654321")
		secretKey := rand.Text()
		Expect(patch(secret, func() { secret.Data["secretAccessKey"] = []byte(secretKey) })).To(Succeed())
		cond = credentialsValid(env, metav1.ConditionFalse, model.TestEnvCredentialRefused)
		Expect(cond.Message).To(And(ContainSubstring("account 210987654321"), ContainSubstring("account 123456789012")))
		Expect(cond.Message).NotTo(Or(ContainSubstring(accessKey), ContainSubstring(secretKey)),
			"the credential never reaches a status")
		testEnvReady(env, metav1.ConditionFalse, model.TestEnvCredentialsInvalid)
		Expect(env.Finalizers).To(ContainElement(testEnvLeasesFinalizer))

		By("making a run that needs it wait, at no cost")
		mustCreate(issueTask(ns, "shadows-1", "env/gpu"))
		waitingFor(ns, "shadows-1", model.EnvCredentialRefused)
		Expect(envAWSFake.Held(testEnvironment(env, nil))).To(BeZero())

		By("leasing once a key of its account is there")
		envAWSFake.AddAccount(accessKey, "123456789012")
		credentialsValid(env, metav1.ConditionTrue, model.TestEnvCredentialsValid)
		testEnvReady(env, metav1.ConditionTrue, model.TestEnvReady)
		Eventually(func(g Gomega) {
			pod := sandboxPodSpec(g, sandboxExists(g, ns, "shadows-1-r1-implement"))
			g.Expect(envVar(pod, "THEOS_ENV_GPU_URL")).NotTo(BeNil())
		}, "20s").Should(Succeed())
		req, ok := envAWSFake.Request(testEnvironment(env, nil), model.LeaseID("gpu", "shadows-1-r1-implement"))
		Expect(ok).To(BeTrue())
		Expect(string(req.Env.Secret["accessKeyId"])).To(Equal(accessKey), "the provider gets the Secret's key")

		By("returning its leases and releasing it when it is deleted")
		Expect(k8sClient.Delete(ctx, env)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, env.Name), env))).To(BeTrue())
			g.Expect(envAWSFake.Released()).To(ContainElement(ns + "/gpu"))
		}, "20s").Should(Succeed())
		Expect(envAWSFake.Held(testEnvironment(env, nil))).To(BeZero())
	})

	It("makes a run wait OverBudget past the day's budget, while a run that holds a lease goes on", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		env := newGPUEnvironment(ns)
		mustCreate(newAgent(ns, 3), env)
		gpuCredential(ns, "123456789012")
		credentialsValid(env, metav1.ConditionTrue, model.TestEnvCredentialsValid)
		// A Crew that is gone holds no turn: the two run side by side.
		first := issueTask(ns, "shadows-1", "env/gpu")
		first.Labels = map[string]string{LabelCrew: "side-by-side"}
		mustCreate(first)
		Eventually(func(g Gomega) { sandboxExists(g, ns, "shadows-1-r1-implement") }, "20s").Should(Succeed())
		held := model.LeaseID("gpu", "shadows-1-r1-implement")
		envAWSFake.SetCost(testEnvironment(env, nil), held, 1000) // the day's dollar

		second := issueTask(ns, "shadows-2", "env/gpu")
		second.Labels = map[string]string{LabelCrew: "side-by-side"}
		mustCreate(second)
		waitingFor(ns, "shadows-2", model.EnvOverBudget)
		Expect(envAWSFake.Held(testEnvironment(env, nil))).To(Equal(1), "the lease held stays")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, env.Name), env)).To(Succeed())
			g.Expect(env.Status.SpentToday).NotTo(BeNil())
			g.Expect(env.Status.SpentToday.MilliUSD).To(Equal(int64(1000)))
		}, "20s").Should(Succeed())

		By("starting once the budget is raised")
		Expect(patch(env, func() { env.Spec.Limits.BudgetUSDPerDay = 2 })).To(Succeed())
		Eventually(func(g Gomega) { sandboxExists(g, ns, "shadows-2-r1-implement") }, "20s").Should(Succeed())
	})
})
