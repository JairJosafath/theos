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
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// taskSkew moves the Task's clock ahead of the real one, so a test sees the
// checks' bounds pass.
var taskSkew atomic.Int64

// taskNow is the TaskReconciler's clock in the suite.
func taskNow() time.Time { return time.Now().Add(time.Duration(taskSkew.Load())) }

// skewTaskClock moves the Task's clock d ahead until the spec ends.
func skewTaskClock(d time.Duration) {
	taskSkew.Store(int64(d))
	DeferCleanup(func() { taskSkew.Store(0) })
}

// setRunnersOnline makes the fake host say n runners are online until the
// spec ends.
func setRunnersOnline(n int) {
	gitFake.SetRunnersOnline(n)
	DeferCleanup(gitFake.ForgetRunnersOnline)
}

// setRunJobs sets the jobs of run id on the fake host, and marks the runners
// online until the spec ends.
//
//nolint:unparam // takes owner and name like the other helpers
func setRunJobs(owner, name string, id int64, online []int64, jobs ...ports.WorkflowJob) {
	gitFake.SetRunJobs(owner, name, id, jobs...)
	gitFake.MarkRunnersOnline(online...)
	DeferCleanup(func() { gitFake.MarkRunnersOffline(online...) })
}

// heldPoolPod gives ns's provider gitea a CI pool of one go runner and returns
// a pod of it, held by a test finalizer so the pool's record of its fate
// outlives the spec's reads (the pool drops its own after
// testRunnerRecordKept).
func heldPoolPod(ns string) *corev1.Pod {
	GinkgoHelper()
	const held = "theos.factory.io/test-held"
	var provider c4v1beta1.GitProvider
	Expect(k8sClient.Get(ctx, key(ns, "gitea"), &provider)).To(Succeed())
	provider.Spec.CI = &c4v1beta1.CISpec{GiteaActions: &c4v1beta1.GiteaActionsSpec{Runners: []c4v1beta1.CIRunnerSpec{
		{Label: "go", Image: "theos/gitea-runner-go:dev", Size: 1},
	}}}
	Expect(k8sClient.Update(ctx, &provider)).To(Succeed())
	var pod corev1.Pod
	Eventually(func(g Gomega) {
		live := livePoolPods(ns)
		g.Expect(live).NotTo(BeEmpty())
		pod = live[0]
	}).Should(Succeed())
	orig := pod.DeepCopy()
	pod.Finalizers = append(pod.Finalizers, held)
	Expect(k8sClient.Patch(ctx, &pod, client.MergeFrom(orig))).To(Succeed())
	DeferCleanup(func() {
		if p := poolPod(ns, pod.Name); p != nil {
			orig := p.DeepCopy()
			if controllerutil.RemoveFinalizer(p, held) {
				Expect(client.IgnoreNotFound(k8sClient.Patch(ctx, p, client.MergeFrom(orig)))).To(Succeed())
			}
		}
	})
	return &pod
}

var _ = Describe("A Task's checks", func() {
	It("waits while its run is queued on an online runner, and fails when no runner is online", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		gitFake.SetChecks(ns, "orders-api", "sha-r1", ports.CheckPending)
		gitFake.SetCommitRuns(ns, "orders-api", "sha-r1", ports.WorkflowRun{ID: 7, Workflow: "check.yaml",
			Event: "pull_request", Status: "queued", URL: "http://gitea/" + ns + "/orders-api/actions/runs/7"})
		setRunnersOnline(1)
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "add-crop").Status.Phase).To(Equal(c4v1beta1.TaskChecking))
		}).Should(Succeed())

		By("waiting past 30 minutes while the run is queued behind other jobs")
		skewTaskClock(45 * time.Minute)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(condition(task.Status.Conditions, ConditionProgressing)).
				To(HaveField("Message", HavePrefix("Waiting for checks: queued behind other jobs since ")))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(getTask(g, ns, "add-crop").Status.Phase).To(Equal(c4v1beta1.TaskChecking))
		}, 3*time.Second).Should(Succeed(), "the checks are rechecked every second, and keep waiting")

		By("failing once no runner is online")
		setRunnersOnline(0)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(And(
				HaveField("Reason", model.ReasonNoCIResult),
				HaveField("Message", ContainSubstring("is queued and no runner is online"))))
		}).Should(Succeed())
	})

	// killedChecks starts Task add-crop and fails its round 1 checks with a
	// log of a job killed for lack of memory, with runs at sha-r1.
	killedChecks := func(ns string, runs ...ports.WorkflowRun) {
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		gitFake.SetCommitRuns(ns, "orders-api", "sha-r1", runs...)
		gitFake.SetCheckLog("go build example.com/app: signal: killed\nFAIL\texample.com/app\t613.2s\n")
		DeferCleanup(gitFake.SetCheckLog, "")
		gitFake.SetChecks(ns, "orders-api", "sha-r1", ports.CheckFailure)
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")
	}

	It("runs the failed jobs the machine killed again, in the same round, and doesn't read the killed run", func() {
		ns := newProjectNamespace()
		killedChecks(ns, killedRun(ns, time.Now().Add(-20*time.Minute)))
		Eventually(func(g Gomega) {
			g.Expect(gitFake.Reruns(ns, "orders-api", 962)).To(Equal(1))
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.ChecksReruns).To(Equal(int32(1)))
			g.Expect(task.Status.ChecksRerunAt).NotTo(BeNil())
			g.Expect(task.Status.Feedback).To(BeEmpty(), "the agent is told nothing")
		}).Should(Succeed())

		By("waiting while the commit's status is still the killed run's failure")
		Consistently(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.ChecksReruns).To(Equal(int32(1)))
			g.Expect(gitFake.Reruns(ns, "orders-api", 962)).To(Equal(1))
		}, 3*time.Second).Should(Succeed(), "the failure from before the rerun is not read")

		By("reviewing once the rerun passes")
		rerun := killedRun(ns, time.Now().Add(time.Second))
		rerun.Conclusion = "success"
		gitFake.SetCommitRuns(ns, "orders-api", "sha-r1", rerun)
		gitFake.SetChecks(ns, "orders-api", "sha-r1", ports.CheckSuccess)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskReviewing))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
		}).Should(Succeed())
	})

	It("runs the failed jobs its runner abandoned again, in the same round, at no round", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		// The runner's pod is replaced mid-job: it cancels the job on its way
		// down, and its successor registers as a new runner.
		cancelled := killedRun(ns, time.Now().Add(-20*time.Minute))
		cancelled.Conclusion = "cancelled"
		gitFake.SetCommitRuns(ns, "orders-api", "sha-r1", cancelled)
		setRunJobs(ns, "orders-api", 962, []int64{32}, ports.WorkflowJob{ID: 1, Status: "completed",
			Conclusion: "cancelled", RunnerID: 31, Completed: time.Now().Add(-10 * time.Minute)})
		gitFake.SetCheckLog("2026-10-15T12:00:00.1234567Z go test ./...\n2026-10-15T12:00:09.1234567Z Job cancelled\n")
		DeferCleanup(gitFake.SetCheckLog, "")
		gitFake.SetChecks(ns, "orders-api", "sha-r1", ports.CheckFailure)
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")
		var at time.Time
		Eventually(func(g Gomega) {
			g.Expect(gitFake.Reruns(ns, "orders-api", 962)).To(Equal(1))
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.ChecksReruns).To(Equal(int32(1)))
			g.Expect(task.Status.ChecksRerunAt).NotTo(BeNil())
			g.Expect(task.Status.Feedback).To(BeEmpty(), "the agent is told nothing")
			at = task.Status.ChecksRerunAt.Time
		}).Should(Succeed())

		By("running them again when the rerun's runner dies with no word, and Gitea fails the job")
		gitFake.SetCheckLog("2026-10-15T12:30:00.1234567Z go test ./...\n2026-10-15T12:30:01.1234567Z === RUN   TestCrop\n")
		gitFake.SetCommitRuns(ns, "orders-api", "sha-r1", killedRun(ns, at.Add(time.Second)))
		Eventually(func(g Gomega) {
			g.Expect(gitFake.Reruns(ns, "orders-api", 962)).To(Equal(2))
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.ChecksReruns).To(Equal(int32(2)))
			at = task.Status.ChecksRerunAt.Time
		}).Should(Succeed())

		By("reviewing once the rerun passes")
		rerun := killedRun(ns, at.Add(time.Second))
		rerun.Conclusion = "success"
		gitFake.SetCommitRuns(ns, "orders-api", "sha-r1", rerun)
		gitFake.SetChecks(ns, "orders-api", "sha-r1", ports.CheckSuccess)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskReviewing))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
		}).Should(Succeed())
	})

	It("costs the round when a person cancelled its run, telling the agent so (#1143)", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		// A person cancels the run in Gitea: its runner stays online, taking the
		// next job, and leaves the log a runner's own cancel would.
		cancelled := killedRun(ns, time.Now().Add(-20*time.Minute))
		cancelled.Conclusion = "cancelled"
		gitFake.SetCommitRuns(ns, "orders-api", "sha-r1", cancelled)
		setRunJobs(ns, "orders-api", 962, []int64{31},
			ports.WorkflowJob{ID: 1, Status: "completed", Conclusion: "cancelled", RunnerID: 31,
				Completed: time.Now()},
			ports.WorkflowJob{ID: 2, Status: "completed", Conclusion: "cancelled"})
		gitFake.SetCheckLog("2026-10-16T12:00:00.1234567Z go test ./...\n2026-10-16T12:00:09.1234567Z Job cancelled\n")
		DeferCleanup(gitFake.SetCheckLog, "")
		gitFake.SetChecks(ns, "orders-api", "sha-r1", ports.CheckFailure)
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")

		By("waiting while a runner that went away wouldn't show offline yet")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(condition(task.Status.Conditions, ConditionProgressing)).
				To(HaveField("Message", ContainSubstring("was cancelled; waiting to tell whether its runner went away")))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(getTask(g, ns, "add-crop").Status.Round).To(Equal(int32(1)))
		}, 2*time.Second).Should(Succeed())

		By("starting a fix round once its runner is still online")
		skewTaskClock(model.RunnerGoneAfter + time.Minute)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
			g.Expect(task.Status.Feedback).To(ContainSubstring("Someone cancelled the checks before they ended"))
			sandboxExists(g, ns, "add-crop-r2-implement")
		}).Should(Succeed())
		Expect(gitFake.Reruns(ns, "orders-api", 962)).To(BeZero(), "a person's cancel is not run again")
	})

	It("runs the job again at no round when its pool pod was lost mid-job (#1140)", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		pod := heldPoolPod(ns)
		// The pod is deleted mid-job: its runner cancels the job on its way
		// down. The runner is still listed online, as a person's cancel would
		// leave it: the pod's fate decides, not the runner.
		cancelled := killedRun(ns, time.Now().Add(-20*time.Minute))
		cancelled.Conclusion = "cancelled"
		gitFake.SetCommitRuns(ns, "orders-api", "sha-r1", cancelled)
		setRunJobs(ns, "orders-api", 962, []int64{31}, ports.WorkflowJob{ID: 1, Status: "completed",
			Conclusion: "cancelled", RunnerID: 31, RunnerName: pod.Name, Completed: time.Now()})
		gitFake.SetCheckLog("2026-10-22T12:00:00.1234567Z go test ./...\n2026-10-22T12:00:09.1234567Z Job cancelled\n")
		DeferCleanup(gitFake.SetCheckLog, "")
		gitFake.SetChecks(ns, "orders-api", "sha-r1", ports.CheckFailure)
		Expect(k8sClient.Delete(ctx, pod)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(poolPod(ns, pod.Name).Annotations).To(HaveKeyWithValue(AnnotationRunnerFate, RunnerFateLost))
		}).Should(Succeed())
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")
		Eventually(func(g Gomega) {
			g.Expect(gitFake.Reruns(ns, "orders-api", 962)).To(Equal(1))
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.ChecksReruns).To(Equal(int32(1)))
			g.Expect(task.Status.Feedback).To(BeEmpty(), "the agent is told nothing")
		}).Should(Succeed())
	})

	It("costs the round when the job's pool pod ended on its own after a cancel (#1140)", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		pod := heldPoolPod(ns)
		// A person cancels the run: the runner ends its one job and the pod
		// exits on its own, and Gitea deletes the ephemeral runner, so it is
		// offline, as a runner told to stop would be.
		cancelled := killedRun(ns, time.Now().Add(-20*time.Minute))
		cancelled.Conclusion = "cancelled"
		gitFake.SetCommitRuns(ns, "orders-api", "sha-r1", cancelled)
		setRunJobs(ns, "orders-api", 962, nil, ports.WorkflowJob{ID: 1, Status: "completed",
			Conclusion: "cancelled", RunnerID: 31, RunnerName: pod.Name, Completed: time.Now()})
		gitFake.SetCheckLog("2026-10-22T12:00:00.1234567Z go test ./...\n2026-10-22T12:00:09.1234567Z Job cancelled\n")
		DeferCleanup(gitFake.SetCheckLog, "")
		gitFake.SetChecks(ns, "orders-api", "sha-r1", ports.CheckFailure)
		setPodPhase(pod, corev1.PodSucceeded)
		Eventually(func(g Gomega) {
			g.Expect(poolPod(ns, pod.Name).Annotations).To(HaveKeyWithValue(AnnotationRunnerFate, RunnerFateEnded))
		}).Should(Succeed())
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
			g.Expect(task.Status.Feedback).To(ContainSubstring("Someone cancelled the checks before they ended"))
			sandboxExists(g, ns, "add-crop-r2-implement")
		}).Should(Succeed())
		Expect(gitFake.Reruns(ns, "orders-api", 962)).To(BeZero(), "a person's cancel is not run again")
	})

	It("fails a Task whose checks are killed a third time in a round, costing no try", func() {
		ns := newProjectNamespace()
		killedChecks(ns, killedRun(ns, time.Now().Add(-20*time.Minute)))
		for rerun := 1; rerun <= model.MaxChecksReruns; rerun++ {
			var at time.Time
			Eventually(func(g Gomega) {
				task := getTask(g, ns, "add-crop")
				g.Expect(task.Status.ChecksReruns).To(Equal(int32(rerun)))
				g.Expect(task.Status.ChecksRerunAt).NotTo(BeNil())
				at = task.Status.ChecksRerunAt.Time
			}).Should(Succeed())
			Expect(gitFake.Reruns(ns, "orders-api", 962)).To(Equal(rerun))
			// The rerun starts, and the machine kills it again.
			gitFake.SetCommitRuns(ns, "orders-api", "sha-r1", killedRun(ns, at.Add(time.Second)))
		}
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(
				HaveField("Reason", model.ReasonRunnerOutOfMemory))
			g.Expect(failedNoCI(&task.Status)).To(BeTrue(), "it costs its issue no try")
		}).Should(Succeed())
		Expect(gitFake.Reruns(ns, "orders-api", 962)).To(Equal(model.MaxChecksReruns))
	})

	It("waits for the commit's other runs to end before running the killed jobs again", func() {
		ns := newProjectNamespace()
		lint := ports.WorkflowRun{ID: 963, Workflow: "lint.yaml", Event: "pull_request", Status: "in_progress",
			Started: time.Now().Add(-10 * time.Minute), URL: "http://gitea/" + ns + "/orders-api/actions/runs/963"}
		killedChecks(ns, lint, killedRun(ns, time.Now().Add(-20*time.Minute)))
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(condition(task.Status.Conditions, ConditionProgressing)).
				To(HaveField("Message", HavePrefix("Waiting for checks: running since ")))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.Reruns(ns, "orders-api", 962)).To(BeZero())
			g.Expect(getTask(g, ns, "add-crop").Status.Phase).To(Equal(c4v1beta1.TaskChecking))
		}, 2*time.Second).Should(Succeed(), "the Git host runs a run's jobs again only once it ended")

		By("running the killed jobs again once the other run ended")
		lint.Status, lint.Conclusion = "completed", "success"
		gitFake.SetCommitRuns(ns, "orders-api", "sha-r1", lint, killedRun(ns, time.Now().Add(-20*time.Minute)))
		Eventually(func(g Gomega) {
			g.Expect(gitFake.Reruns(ns, "orders-api", 962)).To(Equal(1))
			g.Expect(gitFake.Reruns(ns, "orders-api", 963)).To(BeZero())
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(task.Status.ChecksReruns).To(Equal(int32(1)))
		}).Should(Succeed())
	})

	It("fails a Task whose killed checks the Git host refuses to run again, costing no try", func() {
		ns := newProjectNamespace()
		gitFake.RefuseRerun(ns, "orders-api", 962, true)
		killedChecks(ns, killedRun(ns, time.Now().Add(-20*time.Minute)))
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(And(
				HaveField("Reason", model.ReasonNoCIResult),
				HaveField("Message", ContainSubstring("refused to run its failed jobs again"))))
			g.Expect(failedNoCI(&task.Status)).To(BeTrue(), "it costs its issue no try")
		}).Should(Succeed())
	})

	It("runs the failed jobs again when a round pushes the commit whose checks failed, and doesn't read the old run", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.PushBranchHead(ns, "orders-api", "task/add-crop", "sha-r1")
		gitFake.SetCommitRuns(ns, "orders-api", "sha-r1", killedRun(ns, time.Now().Add(-20*time.Minute)))
		gitFake.SetChecks(ns, "orders-api", "sha-r1", ports.CheckFailure)
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
			sandboxExists(g, ns, "add-crop-r2-implement")
		}).Should(Succeed())
		Expect(gitFake.Reruns(ns, "orders-api", 962)).To(BeZero(), "a real failure goes back to the agent")

		By("pushing the same commit again in round 2")
		finishSandbox(ns, "add-crop-r2-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Found nothing to fix."}, "")
		Eventually(func(g Gomega) {
			g.Expect(gitFake.Reruns(ns, "orders-api", 962)).To(Equal(1))
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.ChecksRerunAt).NotTo(BeNil())
			g.Expect(task.Status.ChecksRerunDue).To(BeFalse())
			g.Expect(task.Status.ChecksReruns).To(BeZero(), "it doesn't count towards MaxChecksReruns")
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(gitFake.Reruns(ns, "orders-api", 962)).To(Equal(1))
		}, 3*time.Second).Should(Succeed(), "the failure from round 1 is not read again")

		By("reviewing once the rerun passes")
		rerun := killedRun(ns, time.Now().Add(time.Second))
		rerun.Conclusion = "success"
		gitFake.SetCommitRuns(ns, "orders-api", "sha-r1", rerun)
		gitFake.SetChecks(ns, "orders-api", "sha-r1", ports.CheckSuccess)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskReviewing))
			g.Expect(task.Status.Round).To(Equal(int32(2)))
		}).Should(Succeed())
	})
})

// killedRun is CI run 962 at sha-r1, failed after starting at started.
func killedRun(ns string, started time.Time) ports.WorkflowRun {
	return ports.WorkflowRun{ID: 962, Workflow: "check.yaml", Event: "pull_request", Status: "completed",
		Conclusion: "failure", Started: started, URL: "http://gitea/" + ns + "/orders-api/actions/runs/962"}
}
