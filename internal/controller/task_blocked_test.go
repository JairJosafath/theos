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
	. "github.com/onsi/gomega/gstruct"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
	"github.com/JairJosafath/c4factory/internal/model"
)

// #492: an agent that can't start its issue says why, and the Task ends at once.
var _ = Describe("Task whose agent can't start its issue", func() {
	It("fails Blocked after one round, with the agent's words on the Task and the issue", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3))
		n := gitFake.addIssue(ns, "orders-api", "Use the new engine", clearIssue, []string{model.LabelReady})
		task := newTask(ns, "orders-api-1", "")
		task.Spec.Issue = int32(n)
		mustCreate(task)
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())

		note := "cel-lens-engine#10 must be merged and released before this can use it."
		finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1", &agentrunner.Result{Role: "implement",
			Summary: "I changed nothing.\nBlocked: " + note, Blocked: note}, "")

		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskFailed))
			g.Expect(task.Status.Round).To(Equal(int32(1)), "no more rounds are spent")
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Status": Equal(metav1.ConditionFalse), "Reason": Equal(model.ReasonBlocked),
				"Message": Equal(model.BlockedMessage(note))})))
		}).Should(Succeed())
		issue := gitFake.issue(ns, "orders-api", n)
		Expect(issue.Labels).To(ContainElement(model.LabelNeedsHuman))
		Expect(issue.comments).To(ContainElement(And(ContainSubstring("Task `orders-api-1` failed"),
			ContainSubstring(note))))
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "orders-api-1-r2-implement"), newSandbox()))).To(BeTrue())
	})

	It("says a round with no change and no note gave no reason", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3), newTask(ns, "add-crop", "Add crop"))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "add-crop-r1-implement") }).Should(Succeed())
		finishSandbox(ns, "add-crop-r1-implement", "add-crop", &agentrunner.Result{Role: "implement",
			Error: model.NoChangeNoReason}, "")

		Eventually(func(g Gomega) {
			task := getTask(g, ns, "add-crop")
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Feedback).To(ContainSubstring("gave no reason"))
			g.Expect(condition(task.Status.Conditions, ConditionProgressing)).To(
				HaveField("Message", ContainSubstring("Round 1 was not accepted (the agent made no changes and gave no reason")))
		}).Should(Succeed())
	})
})
