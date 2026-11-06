/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"encoding/json"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

// publicGitURL is the Git host's address from outside the cluster in the
// specs of remote CI.
const publicGitURL = "https://git.example.com"

// newRemoteCIProvider is newCIProvider whose ci block gives the Git host's
// public address, publicURL ("" for none).
func newRemoteCIProvider(ns, publicURL string) *c4v1beta1.GitProvider {
	GinkgoHelper()
	provider := newCIProvider(ns)
	if publicURL != "" {
		Expect(patch(provider, func() { runnerPool(provider).PublicURL = publicURL })).To(Succeed())
	}
	return provider
}

// newCIGPUEnvironment is newGPUEnvironment with a ci block, at most
// maxLeases leases, its credential valid.
func newCIGPUEnvironment(ns string, maxLeases int32) *c4v1beta1.TestEnvironment {
	GinkgoHelper()
	env := newGPUEnvironment(ns)
	env.Spec.CI = &c4v1beta1.TestEnvironmentCI{}
	env.Spec.Limits.MaxLeases = maxLeases
	mustCreate(env)
	gpuCredential(ns, "123456789012")
	credentialsValid(env, metav1.ConditionTrue, model.TestEnvCredentialsValid)
	return env
}

// ciLeases are envAWSFake's leases of env that a CI job's runner holds.
func ciLeases(env *c4v1beta1.TestEnvironment) []ports.Lease {
	GinkgoHelper()
	leases, err := envAWSFake.Leases(ctx, testEnvironment(env, nil))
	Expect(err).NotTo(HaveOccurred())
	return slices.DeleteFunc(leases, func(l ports.Lease) bool {
		_, _, ok := model.ParseCIHolder(l.Holder)
		return !ok
	})
}

// ciCondition waits for env's CI condition to have status and reason, and
// returns it.
func ciCondition(env *c4v1beta1.TestEnvironment, status metav1.ConditionStatus, reason string) *metav1.Condition {
	GinkgoHelper()
	var cond *metav1.Condition
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(env.Namespace, env.Name), env)).To(Succeed())
		cond = condition(env.Status.Conditions, c4v1beta1.ConditionTestEnvironmentCI)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Reason).To(Equal(reason), cond.Message)
		g.Expect(cond.Status).To(Equal(status))
	}, "20s").Should(Succeed())
	return cond
}

// waitingJob is a job of ns's owner that waits for a runner of env-gpu.
func waitingJob(id int64) ports.WaitingJob {
	return ports.WaitingJob{ID: id, RunID: id / 10, Name: "pictures", Labels: []string{model.EnvRunnerLabel("gpu")},
		Created: time.Now()}
}

// docs/plans/test-environments.md, step 8: remote environments in CI, on
// the fake provider and the fake Git host; the EC2 machine and its user data
// are tested on a fake EC2 in internal/adapters/awsenv, and the runner's
// registration on a real Git host in the CI contract's integration test.
var _ = Describe("TestEnvironment on AWS in CI", func() {
	It("leases one machine for a waiting env-<name> job, which runs a runner registered for it, and returns "+
		"the lease when the job ends", func() {
		ns := newProjectNamespace()
		provider := newRemoteCIProvider(ns, publicGitURL)
		env := newCIGPUEnvironment(ns, 2)
		owner := gitFake.For(ports.Config{Owner: ns})

		gitFake.SetWaitingJobs(ns, waitingJob(7001))
		Eventually(func() []ports.Lease { return ciLeases(env) }, "20s").Should(HaveLen(1))
		lease := ciLeases(env)[0]
		gp, runnerID, _ := model.ParseCIHolder(lease.Holder)
		Expect(gp).To(Equal(provider.Name))

		By("registering a runner for the job, whose file holds its own token and the public address")
		runner, ok := envAWSFake.Runner(testEnvironment(env, nil), lease.ID)
		Expect(ok).To(BeTrue(), "the machine runs a CI runner")
		Expect(runner.ID).To(Equal(runnerID))
		var file fake.JobRunnerFile
		Expect(json.Unmarshal(runner.Files["runner.json"], &file)).To(Succeed())
		Expect(file.Address).To(Equal(publicGitURL))
		Expect(file.Labels).To(Equal([]string{"env-gpu:host"}))
		Expect(file.Name).To(Equal(lease.ID))
		for _, data := range runner.Files {
			Expect(string(data)).NotTo(ContainSubstring(fake.RunnerTokenPrefix),
				"the registration token never reaches the machine")
		}
		registered, err := owner.Runners(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(registered).To(ContainElement(And(HaveField("ID", runnerID), HaveField("Ephemeral", true))))
		req, _ := envAWSFake.Request(testEnvironment(env, nil), lease.ID)
		Expect(req.Deadline).To(Equal(model.LeaseDeadline(int(env.Spec.Limits.MaxLeaseMinutes))))
		cond := ciCondition(env, metav1.ConditionTrue, model.TestEnvCIServing)
		Expect(cond.Message).To(ContainSubstring("env-gpu"))

		By("taking no second machine for the one job")
		Consistently(func() []ports.Lease { return ciLeases(env) }, "3s").Should(HaveLen(1))

		By("keeping the lease while its runner runs the job")
		gitFake.SetRunnerOnline(ns, lease.ID, true, true)
		gitFake.SetWaitingJobs(ns)
		Consistently(func() []string { return envAWSFake.Returned() }, "3s").ShouldNot(ContainElement(ns + "/" + lease.ID))
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, env.Name), env)).To(Succeed())
			g.Expect(env.Status.Leases).To(ContainElement(HaveField("Holder", lease.Holder)))
		}, "20s").Should(Succeed())

		By("returning it once the job ended and the host removed the runner")
		Expect(owner.DeleteRunner(ctx, runnerID)).To(Succeed())
		Eventually(func() []string { return envAWSFake.Returned() }, "20s").Should(ContainElement(ns + "/" + lease.ID))
		Expect(ciLeases(env)).To(BeEmpty())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, env.Name), env)).To(Succeed())
			g.Expect(env.Status.Leases).To(BeEmpty())
		}, "20s").Should(Succeed())
	})

	It("returns a machine whose job another runner took, and deletes its runner", func() {
		ns := newProjectNamespace()
		newRemoteCIProvider(ns, publicGitURL)
		env := newCIGPUEnvironment(ns, 2)
		gitFake.SetWaitingJobs(ns, waitingJob(7101))
		Eventually(func() []ports.Lease { return ciLeases(env) }, "20s").Should(HaveLen(1))
		lease := ciLeases(env)[0]
		_, runnerID, _ := model.ParseCIHolder(lease.Holder)

		gitFake.SetWaitingJobs(ns)
		Eventually(func() []string { return envAWSFake.Returned() }, "20s").Should(ContainElement(ns + "/" + lease.ID))
		registered, err := gitFake.For(ports.Config{Owner: ns}).Runners(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(registered).NotTo(ContainElement(HaveField("ID", runnerID)),
			"the runner goes with its machine, its credential with it")
	})

	It("takes a machine per waiting job within maxLeases and the day's budget, which it shares with the runs",
		func() {
			ns := newProjectNamespace()
			newRemoteCIProvider(ns, publicGitURL)
			env := newCIGPUEnvironment(ns, 1)
			gitFake.SetWaitingJobs(ns, waitingJob(7201), waitingJob(7202))
			Eventually(func() []ports.Lease { return ciLeases(env) }, "20s").Should(HaveLen(1))
			Consistently(func() []ports.Lease { return ciLeases(env) }, "3s").Should(HaveLen(1),
				"maxLeases 1 holds the second job back")
			cond := ciCondition(env, metav1.ConditionTrue, model.TestEnvCIServing)
			Expect(cond.Message).To(ContainSubstring("every lease is taken"))

			By("starting none past the budget")
			lease := ciLeases(env)[0]
			envAWSFake.SetCost(testEnvironment(env, nil), lease.ID, 1000) // the day's dollar
			Expect(patch(env, func() { env.Spec.Limits.MaxLeases = 2 })).To(Succeed())
			cond = ciCondition(env, metav1.ConditionFalse, model.TestEnvCIOverBudget)
			Expect(cond.Message).To(ContainSubstring("2 jobs labelled env-gpu wait"))
			Expect(ciLeases(env)).To(HaveLen(1))
		})

	It("runs no job with no public address of the Git host: the in-cluster one turns remote CI off", func() {
		ns := newProjectNamespace()
		newRemoteCIProvider(ns, "")
		env := newCIGPUEnvironment(ns, 2)
		gitFake.SetWaitingJobs(ns, waitingJob(7301))
		cond := ciCondition(env, metav1.ConditionFalse, model.TestEnvCINoPublicAddress)
		Expect(cond.Message).To(And(ContainSubstring("no publicURL"), ContainSubstring("inside the cluster")))
		Consistently(func() []ports.Lease { return ciLeases(env) }, "3s").Should(BeEmpty())
	})

	It("refuses a public address a remote machine can't reach", func() {
		provider := newCIProvider(newProjectNamespace())
		err := patch(provider, func() { runnerPool(provider).PublicURL = "https://git.example.com:3000" })
		Expect(err).To(MatchError(ContainSubstring("publicURL must be https:// on port 443")))
	})
})
