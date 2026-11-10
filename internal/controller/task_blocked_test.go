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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
)

// #492: an agent that can't start its issue says why, and the Task ends at once.
var _ = Describe("Task whose agent can't start its issue", func() {
	It("fails Blocked after one round, with the agent's words on the Task and the issue", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3))
		n := gitFake.AddIssue(ns, "orders-api", "Use the new engine", clearIssue, []string{model.LabelReady})
		task := newTask(ns, "orders-api-1", "")
		task.Spec.Issue = int32(n)
		mustCreate(task)
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())

		note := "cel-lens-engine#10 must be merged and released before this can use it."
		finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1", &agentrunner.Result{Role: "implement",
			Summary: "I changed nothing.\nBlocked: " + note, Blocked: note}, "")

		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(task.Status.Round).To(Equal(int32(1)), "no more rounds are spent")
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Status": Equal(metav1.ConditionFalse), "Reason": Equal(model.ReasonBlocked),
				"Message": Equal(model.BlockedMessage(note))})))
		}).Should(Succeed())
		issue := gitFake.Issue(ns, "orders-api", n)
		Expect(issue.Labels).To(ContainElement(model.LabelNeedsHuman))
		Expect(issue.Comments).To(ContainElement(And(ContainSubstring("Task `orders-api-1` failed"),
			ContainSubstring(note))))
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "orders-api-1-r2-implement"), newSandbox()))).To(BeTrue())
	})

	// #2037: the agent was given the issue, so a note that it can't see it is
	// no blocker, and the try is free once.
	It("fails IssueUnseen when the agent says it can't see the issue, and says the try is free", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newAgent(ns, 3))
		n := gitFake.AddIssue(ns, "orders-api", "Locate CEL", clearIssue, []string{model.LabelReady})
		task := newTask(ns, "orders-api-1", "")
		task.Spec.Issue = int32(n)
		mustCreate(task)
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())

		note := "the issue text for orders-api-1 isn't available to me."
		finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1", &agentrunner.Result{Role: "implement",
			Summary: "Blocked: " + note, Blocked: note}, "")

		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Reason": Equal(model.ReasonIssueUnseen), "Message": Equal(model.IssueUnseenMessage(note))})))
			g.Expect(failedUnseen(&task.Status)).To(BeTrue())
		}).Should(Succeed())
		Expect(gitFake.Issue(ns, "orders-api", n).Comments).To(ContainElement(And(ContainSubstring(note),
			ContainSubstring("doesn't count towards the limit"))))
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
			// The next round's feedback, which its prompt carries, says so; the
			// Progressing message has moved on to round 2 by now.
			g.Expect(task.Status.Feedback).To(Equal(model.NoChangeNoReason))
			g.Expect(task.Status.Feedback).To(ContainSubstring("gave no reason"))
		}).Should(Succeed())
	})
})
