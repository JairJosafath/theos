/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"fmt"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
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

// setRunnersOnline makes the host say n runners are online until the spec ends.
func (g *fakeGit) setRunnersOnline(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.releaseState().runners = &n
	DeferCleanup(func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.releaseState().runners = nil
	})
}

// setRunJobs sets the jobs of run id, and marks the runners online until the
// spec ends.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) setRunJobs(owner, name string, id int64, online []int64, jobs ...gitprovider.WorkflowJob) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.releaseState()
	s.runJobs[fmt.Sprintf("%s/%s#%d", owner, name, id)] = jobs
	for _, runner := range online {
		s.onlineRunners[runner] = true
	}
	DeferCleanup(func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		for _, runner := range online {
			delete(g.releaseState().onlineRunners, runner)
		}
	})
}

var _ = Describe("A Task's checks", func() {
	It("waits while its run is queued on an online runner, and fails when no runner is online", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.pushBranch(ns, "orders-api", "task/add-crop", "sha-r1")
		gitFake.setChecks(ns, "orders-api", "sha-r1", gitprovider.CheckPending)
		gitFake.setCommitRuns(ns, "orders-api", "sha-r1", gitprovider.WorkflowRun{ID: 7, Workflow: "check.yaml",
			Event: "pull_request", Status: "queued", URL: "http://gitea/" + ns + "/orders-api/actions/runs/7"})
		gitFake.setRunnersOnline(1)
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "add-crop").Status.Phase).To(Equal(c4v1alpha1.TaskChecking))
		}).Should(Succeed())

		By("waiting past 30 minutes while the run is queued behind other jobs")
		skewTaskClock(45 * time.Minute)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskChecking))
			g.Expect(condition(task.Status.Conditions, ConditionProgressing)).
				To(HaveField("Message", HavePrefix("Waiting for checks: queued behind other jobs since ")))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(getTask(g, ns, "add-crop").Status.Phase).To(Equal(c4v1alpha1.TaskChecking))
		}, 3*time.Second).Should(Succeed(), "the checks are rechecked every second, and keep waiting")

		By("failing once no runner is online")
		gitFake.setRunnersOnline(0)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskFailed))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(And(
				HaveField("Reason", model.ReasonNoCIResult),
				HaveField("Message", ContainSubstring("is queued and no runner is online"))))
		}).Should(Succeed())
	})

	// killedChecks starts Task add-crop and fails its round 1 checks with a
	// log of a job killed for lack of memory, with runs at sha-r1.
	killedChecks := func(ns string, runs ...gitprovider.WorkflowRun) {
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.pushBranch(ns, "orders-api", "task/add-crop", "sha-r1")
		gitFake.setCommitRuns(ns, "orders-api", "sha-r1", runs...)
		gitFake.setCheckLog("go build example.com/app: signal: killed\nFAIL\texample.com/app\t613.2s\n")
		DeferCleanup(gitFake.setCheckLog, "")
		gitFake.setChecks(ns, "orders-api", "sha-r1", gitprovider.CheckFailure)
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")
	}

	It("runs the failed jobs the machine killed again, in the same round, and doesn't read the killed run", func() {
		ns := newProjectNamespace()
		killedChecks(ns, killedRun(ns, time.Now().Add(-20*time.Minute)))
		Eventually(func(g Gomega) {
			g.Expect(gitFake.reruns(ns, "orders-api", 962)).To(Equal(1))
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskChecking))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.ChecksReruns).To(Equal(int32(1)))
			g.Expect(task.Status.ChecksRerunAt).NotTo(BeNil())
			g.Expect(task.Status.Feedback).To(BeEmpty(), "the agent is told nothing")
		}).Should(Succeed())

		By("waiting while the commit's status is still the killed run's failure")
		Consistently(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskChecking))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.ChecksReruns).To(Equal(int32(1)))
			g.Expect(gitFake.reruns(ns, "orders-api", 962)).To(Equal(1))
		}, 3*time.Second).Should(Succeed(), "the failure from before the rerun is not read")

		By("reviewing once the rerun passes")
		rerun := killedRun(ns, time.Now().Add(time.Second))
		rerun.Conclusion = "success"
		gitFake.setCommitRuns(ns, "orders-api", "sha-r1", rerun)
		gitFake.setChecks(ns, "orders-api", "sha-r1", gitprovider.CheckSuccess)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskReviewing))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
		}).Should(Succeed())
	})

	It("runs the failed jobs its runner abandoned again, in the same round, at no round", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.pushBranch(ns, "orders-api", "task/add-crop", "sha-r1")
		// The runner's pod is replaced mid-job: it cancels the job on its way
		// down, and its successor registers as a new runner.
		cancelled := killedRun(ns, time.Now().Add(-20*time.Minute))
		cancelled.Conclusion = "cancelled"
		gitFake.setCommitRuns(ns, "orders-api", "sha-r1", cancelled)
		gitFake.setRunJobs(ns, "orders-api", 962, []int64{32}, gitprovider.WorkflowJob{ID: 1, Status: "completed",
			Conclusion: "cancelled", RunnerID: 31, Completed: time.Now().Add(-10 * time.Minute)})
		gitFake.setCheckLog("2026-10-15T12:00:00.1234567Z go test ./...\n2026-10-15T12:00:09.1234567Z Job cancelled\n")
		DeferCleanup(gitFake.setCheckLog, "")
		gitFake.setChecks(ns, "orders-api", "sha-r1", gitprovider.CheckFailure)
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")
		var at time.Time
		Eventually(func(g Gomega) {
			g.Expect(gitFake.reruns(ns, "orders-api", 962)).To(Equal(1))
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskChecking))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.ChecksReruns).To(Equal(int32(1)))
			g.Expect(task.Status.ChecksRerunAt).NotTo(BeNil())
			g.Expect(task.Status.Feedback).To(BeEmpty(), "the agent is told nothing")
			at = task.Status.ChecksRerunAt.Time
		}).Should(Succeed())

		By("running them again when the rerun's runner dies with no word, and Gitea fails the job")
		gitFake.setCheckLog("2026-10-15T12:30:00.1234567Z go test ./...\n2026-10-15T12:30:01.1234567Z === RUN   TestCrop\n")
		gitFake.setCommitRuns(ns, "orders-api", "sha-r1", killedRun(ns, at.Add(time.Second)))
		Eventually(func(g Gomega) {
			g.Expect(gitFake.reruns(ns, "orders-api", 962)).To(Equal(2))
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.ChecksReruns).To(Equal(int32(2)))
			at = task.Status.ChecksRerunAt.Time
		}).Should(Succeed())

		By("reviewing once the rerun passes")
		rerun := killedRun(ns, at.Add(time.Second))
		rerun.Conclusion = "success"
		gitFake.setCommitRuns(ns, "orders-api", "sha-r1", rerun)
		gitFake.setChecks(ns, "orders-api", "sha-r1", gitprovider.CheckSuccess)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskReviewing))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
		}).Should(Succeed())
	})

	It("costs the round when a person cancelled its run, telling the agent so (#1143)", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		gitFake.pushBranch(ns, "orders-api", "task/add-crop", "sha-r1")
		// A person cancels the run in Gitea: its runner stays online, taking the
		// next job, and leaves the log a runner's own cancel would.
		cancelled := killedRun(ns, time.Now().Add(-20*time.Minute))
		cancelled.Conclusion = "cancelled"
		gitFake.setCommitRuns(ns, "orders-api", "sha-r1", cancelled)
		gitFake.setRunJobs(ns, "orders-api", 962, []int64{31},
			gitprovider.WorkflowJob{ID: 1, Status: "completed", Conclusion: "cancelled", RunnerID: 31,
				Completed: time.Now()},
			gitprovider.WorkflowJob{ID: 2, Status: "completed", Conclusion: "cancelled"})
		gitFake.setCheckLog("2026-10-16T12:00:00.1234567Z go test ./...\n2026-10-16T12:00:09.1234567Z Job cancelled\n")
		DeferCleanup(gitFake.setCheckLog, "")
		gitFake.setChecks(ns, "orders-api", "sha-r1", gitprovider.CheckFailure)
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")

		By("waiting while a runner that went away wouldn't show offline yet")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskChecking))
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
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskImplementing))
			g.Expect(task.Status.Feedback).To(ContainSubstring("Someone cancelled the CI checks"))
			sandboxExists(g, ns, "add-crop-r2-implement")
		}).Should(Succeed())
		Expect(gitFake.reruns(ns, "orders-api", 962)).To(BeZero(), "a person's cancel is not run again")
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
			Expect(gitFake.reruns(ns, "orders-api", 962)).To(Equal(rerun))
			// The rerun starts, and the machine kills it again.
			gitFake.setCommitRuns(ns, "orders-api", "sha-r1", killedRun(ns, at.Add(time.Second)))
		}
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskFailed))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(
				HaveField("Reason", model.ReasonRunnerOutOfMemory))
			g.Expect(failedNoCI(&task.Status)).To(BeTrue(), "it costs its issue no try")
		}).Should(Succeed())
		Expect(gitFake.reruns(ns, "orders-api", 962)).To(Equal(model.MaxChecksReruns))
	})

	It("waits for the commit's other runs to end before running the killed jobs again", func() {
		ns := newProjectNamespace()
		lint := gitprovider.WorkflowRun{ID: 963, Workflow: "lint.yaml", Event: "pull_request", Status: "in_progress",
			Started: time.Now().Add(-10 * time.Minute), URL: "http://gitea/" + ns + "/orders-api/actions/runs/963"}
		killedChecks(ns, lint, killedRun(ns, time.Now().Add(-20*time.Minute)))
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskChecking))
			g.Expect(condition(task.Status.Conditions, ConditionProgressing)).
				To(HaveField("Message", HavePrefix("Waiting for checks: running since ")))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.reruns(ns, "orders-api", 962)).To(BeZero())
			g.Expect(getTask(g, ns, "add-crop").Status.Phase).To(Equal(c4v1alpha1.TaskChecking))
		}, 2*time.Second).Should(Succeed(), "the Git host runs a run's jobs again only once it ended")

		By("running the killed jobs again once the other run ended")
		lint.Status, lint.Conclusion = "completed", "success"
		gitFake.setCommitRuns(ns, "orders-api", "sha-r1", lint, killedRun(ns, time.Now().Add(-20*time.Minute)))
		Eventually(func(g Gomega) {
			g.Expect(gitFake.reruns(ns, "orders-api", 962)).To(Equal(1))
			g.Expect(gitFake.reruns(ns, "orders-api", 963)).To(BeZero())
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskChecking))
			g.Expect(task.Status.ChecksReruns).To(Equal(int32(1)))
		}).Should(Succeed())
	})

	It("fails a Task whose killed checks the Git host refuses to run again, costing no try", func() {
		ns := newProjectNamespace()
		gitFake.refuseRerun(ns, "orders-api", 962, true)
		killedChecks(ns, killedRun(ns, time.Now().Add(-20*time.Minute)))
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskFailed))
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
		gitFake.pushBranch(ns, "orders-api", "task/add-crop", "sha-r1")
		gitFake.setCommitRuns(ns, "orders-api", "sha-r1", killedRun(ns, time.Now().Add(-20*time.Minute)))
		gitFake.setChecks(ns, "orders-api", "sha-r1", gitprovider.CheckFailure)
		finishSandbox(ns, "add-crop-r1-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Added crop."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskImplementing))
			sandboxExists(g, ns, "add-crop-r2-implement")
		}).Should(Succeed())
		Expect(gitFake.reruns(ns, "orders-api", 962)).To(BeZero(), "a real failure goes back to the agent")

		By("pushing the same commit again in round 2")
		finishSandbox(ns, "add-crop-r2-implement", "add-crop",
			&agentrunner.Result{Role: "implement", Commit: "sha-r1", Summary: "Found nothing to fix."}, "")
		Eventually(func(g Gomega) {
			g.Expect(gitFake.reruns(ns, "orders-api", 962)).To(Equal(1))
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskChecking))
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.ChecksRerunAt).NotTo(BeNil())
			g.Expect(task.Status.ChecksRerunDue).To(BeFalse())
			g.Expect(task.Status.ChecksReruns).To(BeZero(), "it doesn't count towards MaxChecksReruns")
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskChecking))
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(gitFake.reruns(ns, "orders-api", 962)).To(Equal(1))
		}, 3*time.Second).Should(Succeed(), "the failure from round 1 is not read again")

		By("reviewing once the rerun passes")
		rerun := killedRun(ns, time.Now().Add(time.Second))
		rerun.Conclusion = "success"
		gitFake.setCommitRuns(ns, "orders-api", "sha-r1", rerun)
		gitFake.setChecks(ns, "orders-api", "sha-r1", gitprovider.CheckSuccess)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskReviewing))
			g.Expect(task.Status.Round).To(Equal(int32(2)))
		}).Should(Succeed())
	})
})

// killedRun is CI run 962 at sha-r1, failed after starting at started.
func killedRun(ns string, started time.Time) gitprovider.WorkflowRun {
	return gitprovider.WorkflowRun{ID: 962, Workflow: "check.yaml", Event: "pull_request", Status: "completed",
		Conclusion: "failure", Started: started, URL: "http://gitea/" + ns + "/orders-api/actions/runs/962"}
}
