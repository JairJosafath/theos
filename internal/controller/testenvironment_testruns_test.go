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
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// newDevicesEnvironment is a deviceFarm TestEnvironment devices in ns, its
// credential in Secret devices-aws, with a budget of 20 dollars a day.
func newDevicesEnvironment(ns string) *c4v1beta1.TestEnvironment {
	return &c4v1beta1.TestEnvironment{ObjectMeta: objectMeta(ns, "devices"), Spec: c4v1beta1.TestEnvironmentSpec{
		Description: "A Pixel 8 and a Galaxy S24 on AWS Device Farm",
		DeviceFarm: &c4v1beta1.TestEnvironmentDeviceFarm{
			ProjectARN:           "arn:aws:devicefarm:us-west-2:123456789012:project:0b6f3c1e-0000-4000-8000-000000000001",
			DevicePoolARN:        "arn:aws:devicefarm:us-west-2:123456789012:devicepool:0b6f3c1e-0000-4000-8000-000000000001/5e1f0a9d-0000-4000-8000-000000000001",
			Account:              "123456789012",
			CredentialsSecretRef: c4v1beta1.LocalRef{Name: "devices-aws"}},
		Limits: c4v1beta1.TestEnvironmentLimits{BudgetUSDPerDay: 20}}}
}

// devicesCredential is Secret devices-aws in ns.
func devicesCredential(ns string) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: objectMeta(ns, "devices-aws"),
		Data: map[string][]byte{"accessKeyId": []byte(rand.Text()), "secretAccessKey": []byte(rand.Text())}}
}

// testedCommit is the run's commit its app and tests were built from.
const testedCommit = "0a1b2c3d4e5f60718293a4b5c6d7e8f901234567"

// pushTestRun pushes a request to run an app's instrumentation tests,
// signed with token, to lease's branch of orders-api in ns, as `theos-env
// test` does, and returns the request commit.
func pushTestRun(ns, lease, token string) string {
	GinkgoHelper()
	app, tests := []byte("the app"), []byte("its tests")
	request := model.NewTestRunRequest(token, lease, testedCommit, "instrumentation", "app-debug.apk", app,
		"app-debug-androidTest.apk", tests)
	data, err := json.Marshal(request)
	Expect(err).NotTo(HaveOccurred())
	sha, err := gitFake.PushBranch(ctx, ports.Repository{Owner: ns, Name: "orders-api"},
		gitFake.MainHead(ns, "orders-api").Hash, model.TestRunBranch(lease), scaffold.Tree{
			model.TestRunRequestFile:                            {Data: data},
			model.TestRunAppDir + "app-debug.apk":               {Data: app},
			model.TestRunTestsDir + "app-debug-androidTest.apk": {Data: tests},
		}, nil, "theos-env test devices", ports.Signature{Name: "t", Email: "t@example.com", When: time.Now()})
	Expect(err).NotTo(HaveOccurred())
	return sha
}

// testRunAnswer is the report the operator answered on lease's branch of
// orders-api in ns with, and its files.
func testRunAnswer(g Gomega, ns, lease string) (model.TestRunReport, scaffold.Tree) {
	files, _, err := gitFake.ReadFiles(ctx, ports.Repository{Owner: ns, Name: "orders-api"}, model.TestRunBranch(lease),
		[]string{model.TestRunResultDir})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(files).To(HaveKey(model.TestRunReportFile))
	var report model.TestRunReport
	g.Expect(json.Unmarshal(files[model.TestRunReportFile].Data, &report)).To(Succeed())
	return report, files
}

// testRunOf waits for env's status to list test run id, and returns it.
func testRunOf(env *c4v1beta1.TestEnvironment, id, state string) c4v1beta1.TestEnvironmentTestRun {
	GinkgoHelper()
	var run c4v1beta1.TestEnvironmentTestRun
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(env.Namespace, env.Name), env)).To(Succeed())
		var found bool
		for _, r := range env.Status.TestRuns {
			if r.ID == id {
				run, found = r, true
			}
		}
		g.Expect(found).To(BeTrue(), "test runs %+v", env.Status.TestRuns)
		g.Expect(run.State).To(Equal(state))
	}, "20s").Should(Succeed())
	return run
}

// docs/plans/done/test-environments.md, step 10: a deviceFarm environment's runs
// ask for test runs over the Git host, which the operator starts on the
// provider (here the fake; the Device Farm adapter is tested on a fake
// Device Farm in internal/adapters/awsenv) and answers on the same branch.
var _ = Describe("TestEnvironment that runs test packages", func() {
	It("starts the test run a lease's run pushed to its branch, answers it there with the report and artifacts, "+
		"comments it on the pull request, and deletes the branch once the lease is returned", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		env := newDevicesEnvironment(ns)
		mustCreate(newAgent(ns, 3), devicesCredential(ns), env, issueTask(ns, "share-1", "env/devices"))
		const run = "share-1-r1-implement"
		lease := model.LeaseID("devices", run)
		var pod corev1.PodSpec
		Eventually(func(g Gomega) {
			pod = sandboxPodSpec(g, sandboxExists(g, ns, run))
		}, "20s").Should(Succeed())
		Expect(envVar(pod, "THEOS_ENV_DEVICES_URL")).To(HaveField("Value", "git:theos-env/"+lease))
		Expect(envVar(pod, "THEOS_ENV_DEVICES_CA")).To(BeNil())
		Expect(envVar(pod, agentrunner.EnvEnvironments)).To(HaveField("Value", ContainSubstring(`"testRuns":true`)))
		for _, e := range pod.Containers[0].Env {
			Expect(e.Value).NotTo(ContainSubstring("accessKeyId"), "the credential never reaches the sandbox")
		}
		var secret corev1.Secret
		Expect(k8sClient.Get(ctx, key(ns, "share-1-test-environments"), &secret)).To(Succeed())
		token := string(secret.Data[lease])

		By("starting the run its request asks for, with the app and the test package it pushed")
		request := pushTestRun(ns, lease, token)
		id := model.TestRunID(lease, request)
		provider := testEnvironment(env, nil)
		Eventually(func(g Gomega) {
			started, ok := envDeviceFarmFake.Started(provider, id)
			g.Expect(ok).To(BeTrue())
			g.Expect(started.Type).To(Equal("instrumentation"))
			g.Expect(string(started.App.Data)).To(Equal("the app"))
			g.Expect(string(started.Tests.Data)).To(Equal("its tests"))
			g.Expect(started.Lease).To(Equal(lease))
		}, "20s").Should(Succeed())
		started := testRunOf(env, id, model.TestRunRunning)
		Expect(started.Holder).To(Equal("share-1"))
		Expect(started.Commit).To(Equal(testedCommit))

		By("answering on the lease's branch once it is done")
		envDeviceFarmFake.Finish(provider, id, ports.TestRunReport{Report: model.TestRunReport{
			Result: model.TestRunFailed, Counters: model.TestRunCounters{Total: 2, Passed: 1, Failed: 1},
			Devices: []model.TestRunDevice{{Name: "Google Pixel 8", Result: model.TestRunFailed,
				Counters: model.TestRunCounters{Total: 2, Passed: 1, Failed: 1}}},
			Problems: []model.TestRunProblem{{Device: "Google Pixel 8", Suite: "ShareSheetTest", Test: "sharesTheCart",
				Result: model.TestRunFailed, Message: "expected: <3> but was: <2>"}}, CostMilliUSD: 340},
			Artifacts: []ports.TestRunArtifact{{Path: "Google-Pixel-8/share-sheet.png", Data: []byte("a screenshot")}}})
		Eventually(func(g Gomega) {
			report, files := testRunAnswer(g, ns, lease)
			g.Expect(string(files[model.TestRunAnswerFile].Data)).To(Equal(request + "\n"))
			g.Expect(report.Result).To(Equal(model.TestRunFailed))
			g.Expect(report.Commit).To(Equal(testedCommit))
			g.Expect(report.Environment).To(Equal("devices"))
			g.Expect(string(files[model.TestRunArtifactsDir+"Google-Pixel-8/share-sheet.png"].Data)).To(Equal("a screenshot"))
		}, "20s").Should(Succeed())
		reported := testRunOf(env, id, model.TestRunReported)
		Expect(reported.Result).To(Equal(model.TestRunFailed))
		Expect(reported.Answer).To(Equal(gitFake.BranchHead(ns, "orders-api", model.TestRunBranch(lease))))
		Expect(reported.Outcome).To(ContainSubstring("1 failed of 2 tests"))
		Expect(reported.Comment).To(ContainSubstring("ShareSheetTest.sharesTheCart"), "it waits for the pull request")

		By("commenting it on the pull request once the run ends and the pull request opens")
		gitFake.PushBranchHead(ns, "orders-api", "task/share-1", "sha-r1")
		finishSandbox(ns, run, "share-1",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Shared the cart."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "share-1")
			g.Expect(task.Status.PullRequest).NotTo(BeZero())
			g.Expect(gitFake.PullComments(ns, "orders-api", int(task.Status.PullRequest))).To(ContainElement(And(
				ContainSubstring("test environment `devices`"), ContainSubstring("1 failed of 2 tests"),
				ContainSubstring("ShareSheetTest.sharesTheCart on Google Pixel 8"))))
		}, "20s").Should(Succeed())

		By("deleting the returned lease's branch and forgetting its runs")
		Eventually(func(g Gomega) {
			g.Expect(gitFake.BranchExists(ns, "orders-api", model.TestRunBranch(lease))).To(BeFalse())
			g.Expect(k8sClient.Get(ctx, key(ns, env.Name), env)).To(Succeed())
			g.Expect(env.Status.TestRuns).To(BeEmpty())
		}, "20s").Should(Succeed())
	})

	It("runs nothing for a request its lease's token didn't sign, nor past the day's budget, and says why "+
		"on the branch", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		env := newDevicesEnvironment(ns)
		mustCreate(newAgent(ns, 3), devicesCredential(ns), env, issueTask(ns, "share-2", "env/devices"))
		lease := model.LeaseID("devices", "share-2-r1-implement")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "share-2-r1-implement") }, "20s").Should(Succeed())
		provider := testEnvironment(env, nil)

		By("refusing a request signed with another token")
		unsigned := pushTestRun(ns, lease, rand.Text())
		Eventually(func(g Gomega) {
			report, _ := testRunAnswer(g, ns, lease)
			g.Expect(report.Result).To(Equal(model.TestRunRefused))
			g.Expect(report.Message).To(ContainSubstring("isn't signed with the lease's token"))
		}, "20s").Should(Succeed())
		rejected := testRunOf(env, model.TestRunID(lease, unsigned), model.TestRunRejected)
		Expect(rejected.Comment).To(BeEmpty(), "a rejected request is no outcome for the pull request")

		By("refusing a signed one once the day's budget is spent")
		var secret corev1.Secret
		Expect(k8sClient.Get(ctx, key(ns, "share-2-test-environments"), &secret)).To(Succeed())
		envDeviceFarmFake.SetCost(provider, lease, 20_000)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, env.Name), env)).To(Succeed())
			g.Expect(env.Status.SpentToday).NotTo(BeNil())
			g.Expect(env.Status.SpentToday.MilliUSD).To(Equal(int64(20_000)))
		}, "20s").Should(Succeed())
		signed := pushTestRun(ns, lease, string(secret.Data[lease]))
		Eventually(func(g Gomega) {
			report, files := testRunAnswer(g, ns, lease)
			g.Expect(string(files[model.TestRunAnswerFile].Data)).To(Equal(signed + "\n"))
			g.Expect(report.Result).To(Equal(model.TestRunRefused))
			g.Expect(report.Message).To(ContainSubstring("spent its budget for today"))
		}, "20s").Should(Succeed())
		testRunOf(env, model.TestRunID(lease, signed), model.TestRunRejected)
		Expect(envDeviceFarmFake.Runs(provider)).To(BeZero(), "no run started")
	})

	DescribeTable("refuses a deviceFarm spec its CEL rules reject",
		func(mutate func(*c4v1beta1.TestEnvironment), message string) {
			env := newDevicesEnvironment(newProjectNamespace())
			mutate(env)
			Expect(k8sClient.Create(ctx, env)).To(MatchError(ContainSubstring(message)))
		},
		Entry("no budget", func(env *c4v1beta1.TestEnvironment) { env.Spec.Limits.BudgetUSDPerDay = 0 },
			"a deviceFarm environment needs limits.budgetUSDPerDay"),
		Entry("a project outside us-west-2", func(env *c4v1beta1.TestEnvironment) {
			env.Spec.DeviceFarm.ProjectARN = "arn:aws:devicefarm:eu-west-1:123456789012:project:0b6f3c1e-0000-4000-8000-000000000001"
		}, "projectArn must be a Device Farm project's ARN, in us-west-2"),
		Entry("a pool that is no device pool", func(env *c4v1beta1.TestEnvironment) {
			env.Spec.DeviceFarm.DevicePoolARN = "arn:aws:devicefarm:us-west-2:123456789012:project:0b6f3c1e-0000-4000-8000-000000000001"
		}, "devicePoolArn must be a Device Farm device pool's ARN"),
		Entry("a second member", func(env *c4v1beta1.TestEnvironment) {
			env.Spec.Local = newBrowserEnvironment(env.Namespace).Spec.Local
		}, "set exactly one provider member: local, aws or deviceFarm"),
		Entry("CI, which needs a machine for its runner (step 8)", func(env *c4v1beta1.TestEnvironment) {
			env.Spec.CI = &c4v1beta1.TestEnvironmentCI{}
		}, "a deviceFarm environment runs test packages, not CI jobs"),
	)
})
