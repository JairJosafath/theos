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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
)

// "Nothing to do" is a result (#72): an implementer that finds its issue
// needs no change, as a scheduled update with nothing to update, says so
// and changes nothing. The Task ends done and its issue closes with the
// agent's words, where a round with no change failed the Task and handed
// the issue to a person every quiet week.
var _ = Describe("Task with nothing to do", func() {
	const note = "every module is at its newest version."
	nothing := &agentrunner.Result{Role: "implement",
		Summary: "Ran go list -m -u all.\nNothing to do: " + note, Nothing: note}

	It("closes its issue with the agent's reason and ends done, with no pull request and no reviewer", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		n := gitFake.AddIssue(ns, "orders-api", "Update the modules", clearIssue, []string{model.LabelReady})
		mustCreate(newCrew(ns))
		name := fmt.Sprintf("orders-api-%d", n)
		Eventually(func(g Gomega) { sandboxExists(g, ns, name+"-r1-implement") }).Should(Succeed())
		finishSandbox(ns, name+"-r1-implement", name, nothing, "")

		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(And(Not(BeNil()),
				HaveField("Status", metav1.ConditionTrue), HaveField("Reason", model.ReasonNothingToDo),
				HaveField("Message", "The agent found the issue needs no change, and it is closed: "+note)))
			g.Expect(task.Status.Summary).To(Equal(note))
			g.Expect(task.Status.PullRequest).To(BeZero())
			g.Expect(task.Status.MergeCommit).To(BeEmpty())
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			for _, run := range task.Status.Runs {
				g.Expect(run.Role).NotTo(Equal(model.RoleReviewer), "there is no change to review")
			}
		}).Should(Succeed())
		expectSandboxGone(ns, name+"-r1-implement")
		issue := gitFake.Issue(ns, "orders-api", n)
		Expect(issue.State).To(Equal("closed"))
		Expect(issue.Labels).NotTo(ContainElement(model.LabelInProgress))
		Expect(issue.Labels).NotTo(ContainElement(model.LabelNeedsHuman), "nobody is asked to look")
		Expect(issue.Comments).To(ContainElement(model.NothingComment(name, note)))
		Expect(gitFake.PullRequests(ns, "orders-api")).To(BeEmpty())

		By("going on to the Crew's next issue")
		next := gitFake.AddIssue(ns, "orders-api", "Export customers", clearIssue, []string{model.LabelReady})
		Eventually(func(g Gomega) {
			sandboxExists(g, ns, fmt.Sprintf("orders-api-%d-r1-implement", next))
		}).Should(Succeed())
	})

	It("lets its release go out at the newest merge: no release waits for a merge there never was", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		mergedIssue(ns)
		n := gitFake.AddIssue(ns, "orders-api", "Update the modules", clearIssue, []string{model.LabelReady})
		gitFake.SetIssueRelease(ns, "orders-api", n, "v0.1.0", releaseDue)
		crew := newCrew(ns)
		crew.Spec.TagReleases = true
		mustCreate(crew)
		name := fmt.Sprintf("orders-api-%d", n)
		Eventually(func(g Gomega) { sandboxExists(g, ns, name+"-r1-implement") }).Should(Succeed())
		Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(BeEmpty(), "the release has an open issue")
		finishSandbox(ns, name+"-r1-implement", name, nothing, "")

		Eventually(func(g Gomega) {
			g.Expect(gitFake.Tag(ns, "orders-api", "v0.1.0")).To(Equal("5a-merge"))
			cond := condition(getCrew(g, ns).Status.Conditions, ConditionReleased)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Message).To(Equal("Released v0.1.0 at 5a-merge"))
		}).Should(Succeed())
	})
})
