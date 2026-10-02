/*
Copyright 2026.

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
	"slices"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

var _ = Describe("Task allowedProtectedPaths", func() {
	It("takes eligible paths on an issue Task only, and never changes", func() {
		ns := newProjectNamespace()
		gated := func(name string, paths ...string) *c4v1alpha1.Task {
			t := newTask(ns, name, "")
			t.Spec.Issue = 2
			t.Spec.AllowedProtectedPaths = paths
			return t
		}
		Expect(k8sClient.Create(ctx, gated("ok", "Makefile", ".gitea/workflows/release.yaml"))).To(Succeed())
		for _, never := range model.AgentNeverPaths {
			err := k8sClient.Create(ctx, gated("never", never))
			Expect(err).To(MatchError(ContainSubstring("may not include AGENTS.md")), never)
		}
		goal := newTask(ns, "goal", "Do it")
		goal.Spec.AllowedProtectedPaths = []string{"Makefile"}
		Expect(k8sClient.Create(ctx, goal)).To(MatchError(ContainSubstring("needs an issue")))
		task := getTask(Default, ns, "ok")
		Expect(patch(task, func() { task.Spec.AllowedProtectedPaths = []string{"Makefile", ".golangci.yml"} })).
			To(MatchError(ContainSubstring("immutable")))
	})
})

// proposingTask creates an issue Task on orders-api for a new ready issue in the
// milestone v0.1.0, and waits for its first implement sandbox. It returns the issue.
func proposingTask(ns string) int {
	GinkgoHelper()
	readyContainer(ns)
	mustCreate(newAgent(ns, 3))
	n := gitFake.addIssue(ns, "orders-api", "Package it", clearIssue, []string{model.LabelReady})
	gitFake.setIssueMilestone(ns, "orders-api", n, "v0.1.0")
	task := newTask(ns, "orders-api-1", "")
	task.Spec.Issue = int32(n)
	mustCreate(task)
	Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())
	gitFake.pushBranch(ns, "orders-api", "task/orders-api-1", "sha-1")
	return n
}

// makefileProposal is what the runner reports when the agent changed the Makefile.
var makefileProposal = model.Proposal{Branch: "standard/orders-api-1", Paths: []string{"Makefile"},
	Reason: "make dist must build the packages"}

func finishWithProposal(ns string) {
	GinkgoHelper()
	p := makefileProposal
	finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1", &agentrunner.Result{Role: "implement",
		Commit: "sha-1", Summary: "Wrote scripts/dist.mjs.", Proposal: &p}, "")
}

// standardChangeIssues are the open issues labelled kind/standard-change.
func standardChangeIssues(ns string) []int {
	var out []int
	for _, i := range gitFake.openIssues(ns, "orders-api") {
		if slices.Contains(i.Labels, model.LabelStandardChange) {
			out = append(out, i.Number)
		}
	}
	return out
}

var _ = Describe("Task with a Standard change", func() {
	It("files a Standard change for a proposal and blocks", func() {
		ns := newProjectNamespace()
		n := proposingTask(ns)
		finishWithProposal(ns)

		m := n + 1 // the next issue the fake files
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskBlocked))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.Sandbox).To(BeEmpty())
			g.Expect(task.Status.Commit).To(Equal("sha-1"))
			g.Expect(task.Status.Summary).To(Equal("Wrote scripts/dist.mjs."))
			g.Expect(task.Status.StandardChange).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Issue": Equal(int32(m)), "IssueURL": HaveSuffix(fmt.Sprintf("/issues/%d", m)),
				"Paths": Equal([]string{"Makefile"}), "Branch": Equal("standard/orders-api-1"), "Count": Equal(int32(1))})))
			g.Expect(condition(task.Status.Conditions, ConditionProgressing).Reason).To(Equal("WaitingForStandardChange"))
			g.Expect(task.Status.History).To(ContainElement(MatchFields(IgnoreExtras, Fields{
				"Phase": Equal(c4v1alpha1.TaskBlocked), "Reason": Equal("WaitingForStandardChange")})))
		}).Should(Succeed())

		By("filing the issue as the operator, in the waiting issue's milestone")
		filed := gitFake.issue(ns, "orders-api", m)
		Expect(filed.Title).To(Equal(fmt.Sprintf("Standard change for #%d: Makefile", n)))
		Expect(filed.Labels).To(ConsistOf(model.LabelStandardChange, model.LabelReady))
		Expect(filed.milestone).To(Equal("v0.1.0"))
		Expect(filed.Body).To(ContainSubstring("make dist must build the packages"))
		Expect(model.HasProposalMarker(filed.Body, "standard/orders-api-1")).To(BeTrue())
		Expect(standardChangeIssues(ns)).To(Equal([]int{m}))

		By("blocking the waiting issue on it, and saying so there")
		waiting := gitFake.issue(ns, "orders-api", n)
		Expect(waiting.blockers).To(ContainElement(fmt.Sprintf("#%d", m)))
		Expect(waiting.comments).To(ContainElement(And(ContainSubstring("Task `orders-api-1` needs a Standard change"),
			ContainSubstring(fmt.Sprintf("#%d", m)))))
		Expect(waiting.State).To(Equal("open"))
		Expect(gitFake.pullRequests(ns, "orders-api")).To(BeEmpty())

		By("running no agent while it waits")
		Consistently(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskBlocked))
			g.Expect(task.Status.Sandbox).To(BeEmpty())
			g.Expect(condition(task.Status.Conditions, ConditionProgressing).Reason).To(Equal("WaitingForStandardChange"))
		}, "3s").Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "orders-api-1-r1-implement"), newSandbox()))).To(BeTrue())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "orders-api-1-r2-implement"), newSandbox()))).To(BeTrue())
	})

	It("reuses the open Standard change it filed when the step repeats", func() {
		ns := newProjectNamespace()
		n := proposingTask(ns)
		// A first try filed the issue, but its status write was lost.
		title, body := model.StandardChangeIssue(n, "orders-api-1", makefileProposal)
		filed := gitFake.addIssue(ns, "orders-api", title, body, []string{model.LabelStandardChange, model.LabelReady})
		finishWithProposal(ns)

		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskBlocked))
			g.Expect(task.Status.StandardChange).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Issue": Equal(int32(filed)), "Count": Equal(int32(1))})))
		}).Should(Succeed())
		Expect(standardChangeIssues(ns)).To(Equal([]int{filed}))
		Expect(gitFake.issue(ns, "orders-api", n).blockers).To(ContainElement(fmt.Sprintf("#%d", filed)))
	})
})

func TestStandardChangeToReuse(t *testing.T) {
	const branch = "standard/orders-api-1"
	marker := model.ProposalMarker(branch)
	issues := []gitprovider.Issue{
		{Number: 2, Body: "Why\n\n" + marker + "\n"},
		{Number: 3, Body: "Quoting " + marker + " inline\n"},
		{Number: 4, Body: "Why\n" + model.ProposalMarker("standard/orders-api-10") + "\n"},
		{Number: 5, Body: "Why\r\n" + marker + "\r\n"},
	}
	for _, tt := range []struct {
		name   string
		issues []gitprovider.Issue
		skip   int
		want   int // 0 means none
	}{
		{name: "the first with the branch's marker on its own line", issues: issues, want: 2},
		{name: "never the change the Task waited on before", issues: issues, skip: 2, want: 5},
		{name: "none: a quoted marker or another branch's", issues: issues[1:3]},
		{name: "none: no issues"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := standardChangeToReuse(tt.issues, branch, tt.skip)
			if tt.want == 0 && ok {
				t.Errorf("reuses #%d, want none", got.Number)
			}
			if tt.want != 0 && (!ok || got.Number != tt.want) {
				t.Errorf("reuses #%d (%v), want #%d", got.Number, ok, tt.want)
			}
		})
	}
}
