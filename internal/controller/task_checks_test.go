/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
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
})
