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
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

var _ = Describe("Crew with a Standard change", func() {
	It("runs a Standard change beside the Task it blocks, with its allowance", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 2)
		n := gitFake.addIssue(ns, "orders-api", "Package it", clearIssue, []string{model.LabelReady})
		crew := newCrew(ns)
		crew.Spec.Parallel = 1
		mustCreate(crew)
		Eventually(func(g Gomega) {
			g.Expect(sandboxEnv(g, ns, "orders-api-1-r1-implement")).
				NotTo(ContainElement(HaveKeyWithValue("name", "C4_ALLOWED_PATHS")), "an ordinary issue is ungated")
		}).Should(Succeed())
		Expect(getTask(Default, ns, "orders-api-1").Spec.AllowedProtectedPaths).To(BeEmpty())
		gitFake.pushBranch(ns, "orders-api", "task/orders-api-1", "sha-1")
		finishWithProposal(ns)

		m := n + 1 // the Standard change the Task files
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1").Status.Phase).To(Equal(c4v1alpha1.TaskBlocked))
			gated := getTask(g, ns, "orders-api-2")
			g.Expect(gated.Spec.Issue).To(Equal(int32(m)))
			g.Expect(gated.Spec.AllowedProtectedPaths).To(Equal([]string{"Makefile"}))
			g.Expect(gated.Labels).To(HaveKeyWithValue(LabelCrew, "orders-api"))
			g.Expect(sandboxEnv(g, ns, "orders-api-2-r1-implement")).
				To(ContainElement(map[string]any{"name": "C4_ALLOWED_PATHS", "value": "Makefile"}))
			g.Expect(getCrew(g, ns).Status.ActiveTasks).To(ConsistOf("orders-api-1", "orders-api-2"))
		}).Should(Succeed())
	})

	It("leaves a kind/standard-change issue no Task filed waiting", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		n := gitFake.addIssue(ns, "orders-api", "Standard change for #9: Makefile", clearIssue,
			[]string{model.LabelReady, model.LabelStandardChange})
		mustCreate(newCrew(ns))
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Status.Waiting).To(ContainElement(int32(n)))
			g.Expect(crew.Status.Ready).To(BeEmpty())
			g.Expect(crew.Status.ActiveTasks).To(BeEmpty())
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			var tasks c4v1alpha1.TaskList
			g.Expect(k8sClient.List(ctx, &tasks, client.InNamespace(ns))).To(Succeed())
			g.Expect(tasks.Items).To(BeEmpty())
		}, "3s").Should(Succeed())
	})
})

// gatedTaskInReview creates the Standard change Task orders-api-1, allowed the
// Makefile, for a new issue, and runs its round up to the review of pull request
// 1, which changes files. It returns the issue.
func gatedTaskInReview(ns string, files ...string) int {
	GinkgoHelper()
	readyContainer(ns)
	crewAgents(ns, 2)
	n := gitFake.addIssue(ns, "orders-api", "Standard change for #9: Makefile", clearIssue,
		[]string{model.LabelStandardChange, model.LabelReady})
	task := newTask(ns, "orders-api-1", "")
	task.Spec.Issue = int32(n)
	task.Spec.ReviewerRef = &c4v1alpha1.LocalRef{Name: "reviewer"}
	task.Spec.AllowedProtectedPaths = []string{"Makefile"}
	mustCreate(task)
	Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())
	gitFake.pushBranch(ns, "orders-api", "task/orders-api-1", "sha-1")
	finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1",
		&agentrunner.Result{Role: "implement", Commit: "sha-1", Summary: "Added the dist target."}, "")
	Eventually(func(g Gomega) {
		g.Expect(getTask(g, ns, "orders-api-1").Status.PullRequest).To(Equal(int32(1)))
	}).Should(Succeed())
	gitFake.setPullFiles(ns, "orders-api", 1, files...)
	gitFake.setChecks(ns, "orders-api", "sha-1", "success")
	Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-review-a1") }).Should(Succeed())
	return n
}

// approveGated has the reviewer approve round 1 of orders-api-1.
func approveGated(ns string) {
	GinkgoHelper()
	finishSandbox(ns, "orders-api-1-r1-review-a1", "orders-api-1",
		&agentrunner.Result{Role: "review", Verdict: model.VerdictApprove, Summary: "Only the dist target."}, "")
}

// expireSandbox plays Agent Sandbox at a sandbox's shutdown time: it marks the
// sandbox expired and deletes its pod, and with it the run's result.
func expireSandbox(ns, name string) {
	GinkgoHelper()
	sandbox := newSandbox()
	Expect(k8sClient.Get(ctx, key(ns, name), sandbox)).To(Succeed())
	Expect(unstructured.SetNestedSlice(sandbox.Object, []any{map[string]any{
		"type": "Ready", "status": "False", "reason": "SandboxExpired", "message": "the sandbox expired",
		"lastTransitionTime": time.Now().UTC().Format(time.RFC3339)}}, "status", "conditions")).To(Succeed())
	Expect(k8sClient.Status().Update(ctx, sandbox)).To(Succeed())
	Expect(k8sClient.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}},
		client.GracePeriodSeconds(0))).To(Succeed())
}

var _ = Describe("Task with a gated merge", func() {
	It("force-merges a Standard change after checking CI, the approval and every path", func() {
		ns := newProjectNamespace()
		n := gatedTaskInReview(ns, "Makefile")

		By("waiting, neither refusing nor merging, while CI runs again on the reviewed commit")
		gitFake.setChecks(ns, "orders-api", "sha-1", "pending")
		approveGated(ns)
		Eventually(func(g Gomega) {
			g.Expect(condition(getTask(g, ns, "orders-api-1").Status.Conditions, ConditionProgressing).Reason).
				To(Equal("WaitingForChecks"))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskReviewing))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(BeNil())
			g.Expect(gitFake.pullRequests(ns, "orders-api")[0].Merged).To(BeFalse())
		}, "2s").Should(Succeed())
		Expect(gitFake.forcedMerges(ns, "orders-api")).To(BeEmpty())
		Expect(gitFake.reviews(ns, "orders-api", 1)).To(BeEmpty(), "the approval is posted once, with the merge")

		By("merging past the protected-file rule once CI passed")
		gitFake.setChecks(ns, "orders-api", "sha-1", "success")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskMerged))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Status": Equal(metav1.ConditionTrue), "Reason": Equal("Merged")})))
		}).Should(Succeed())
		Expect(gitFake.pullRequests(ns, "orders-api")[0].Merged).To(BeTrue())
		Expect(gitFake.forcedMerges(ns, "orders-api")).To(Equal([]int{1}))
		Expect(gitFake.issue(ns, "orders-api", n).State).To(Equal("closed"))
		Expect(gitFake.reviews(ns, "orders-api", 1)).To(ConsistOf(And(HaveField("Event", gitprovider.ReviewApprove),
			HaveField("Commit", "sha-1"))))
	})

	It("refuses the merge, not reviews again, when the review expires while CI is pending", func() {
		ns := newProjectNamespace()
		n := gatedTaskInReview(ns, "Makefile")
		gitFake.setChecks(ns, "orders-api", "sha-1", "pending")
		approveGated(ns)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(condition(task.Status.Conditions, ConditionProgressing).Reason).To(Equal("WaitingForChecks"))
			g.Expect(task.Status.Checks).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Commit": Equal("sha-1"), "State": Equal("pending")})))
		}).Should(Succeed())

		By("expiring the review sandbox, with CI still pending")
		expireSandbox(ns, "orders-api-1-r1-review-a1")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskFailed))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Status": Equal(metav1.ConditionFalse), "Reason": Equal("GatedMergeRefused"),
				"Message": ContainSubstring("CI is still pending on the reviewed commit")})))
			g.Expect(task.Status.ReviewAttempts).To(BeZero(), "no reviewer run is spent on a CI wait")
		}).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "orders-api-1-r1-review-a2"), newSandbox()))).
			To(BeTrue(), "no second reviewer run")
		Expect(gitFake.pullRequests(ns, "orders-api")[0].Merged).To(BeFalse())
		Expect(gitFake.forcedMerges(ns, "orders-api")).To(BeEmpty())
		Expect(gitFake.issue(ns, "orders-api", n).Labels).To(ContainElement(model.LabelNeedsHuman))
	})

	It("refuses a Standard change that touches a path it may not", func() {
		ns := newProjectNamespace()
		n := gatedTaskInReview(ns, "Makefile", "README.md")
		approveGated(ns)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskFailed))
			g.Expect(task.Status.Sandbox).To(BeEmpty())
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(PointTo(MatchFields(IgnoreExtras, Fields{
				"Status": Equal(metav1.ConditionFalse), "Reason": Equal("GatedMergeRefused"),
				"Message": ContainSubstring("README.md")})))
		}).Should(Succeed())
		Expect(gitFake.pullRequests(ns, "orders-api")[0].Merged).To(BeFalse())
		Expect(gitFake.forcedMerges(ns, "orders-api")).To(BeEmpty())
		Expect(gitFake.reviews(ns, "orders-api", 1)).To(BeEmpty(), "a refused change gets no approval")
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "orders-api-1-r1-review-a1"), newSandbox()))).To(BeTrue())

		By("handing the issue back")
		issue := gitFake.issue(ns, "orders-api", n)
		Expect(issue.State).To(Equal("open"))
		Expect(issue.Labels).To(ContainElement(model.LabelNeedsHuman))
		Expect(issue.comments).To(ContainElement(And(ContainSubstring("Task `orders-api-1` failed"),
			ContainSubstring("README.md"))))
	})
})

// gateProvider is a Git host for gatedMerge's checks alone; any other call panics.
type gateProvider struct {
	gitprovider.Provider
	checks   string
	files    []string
	filesErr error
	merges   int
}

func (p *gateProvider) CombinedStatus(_ context.Context, _ gitprovider.Repository, ref string) (gitprovider.CheckStatus,
	error) {
	return gitprovider.CheckStatus{Commit: ref, State: p.checks}, nil
}

func (p *gateProvider) PullRequestFiles(_ context.Context, _ gitprovider.Repository, _ int) ([]string, error) {
	return p.files, p.filesErr
}

func (p *gateProvider) ForceMergePullRequest(_ context.Context, _ gitprovider.Repository, _ int, _, _, _ string) error {
	p.merges++
	return nil
}

func TestGatedMergeMergesNothingItCouldNotCheck(t *testing.T) {
	makefile := []string{"Makefile"}
	approved := &c4v1alpha1.TaskReview{Verdict: model.VerdictApprove}
	for _, tt := range []struct {
		name     string
		provider gateProvider
		review   *c4v1alpha1.TaskReview
		pending  bool
		refusal  string // in the refusal, if any
		err      string // in the error, if any
	}{
		{name: "waits while CI runs", provider: gateProvider{checks: "pending", files: makefile}, review: approved,
			pending: true},
		{name: "refuses failed CI", provider: gateProvider{checks: "failure", files: makefile}, review: approved,
			refusal: "CI is failure"},
		{name: "refuses CI that never reported", provider: gateProvider{files: makefile}, review: approved,
			refusal: "CI is unknown"},
		{name: "fails, to retry, on a file list it could not read", review: approved,
			provider: gateProvider{checks: "success", filesErr: errors.New("connection refused")}, err: "connection refused"},
		{name: "refuses a change too large to check every path", review: approved,
			provider: gateProvider{checks: "success", filesErr: fmt.Errorf("pull request 1 changes 1000 files or more: %w",
				gitprovider.ErrTooManyFiles)}, refusal: "1000 files or more"},
		{name: "refuses a path outside the allowance", review: approved,
			provider: gateProvider{checks: "success", files: []string{"Makefile", "README.md"}}, refusal: "README.md"},
		{name: "refuses without the reviewer's approval", provider: gateProvider{checks: "success", files: makefile},
			refusal: "verdict"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.provider
			task := &c4v1alpha1.Task{Spec: c4v1alpha1.TaskSpec{Issue: 2, AllowedProtectedPaths: makefile}}
			status := &c4v1alpha1.TaskStatus{Commit: "sha-1", PullRequest: 1, Review: tt.review}
			refusal, err := (&TaskReconciler{}).gatedMerge(context.Background(), &p, gitprovider.Repository{}, task,
				status, &c4v1alpha1.Container{}, nil)
			if p.merges != 0 {
				t.Errorf("merged %d times, want none", p.merges)
			}
			if got := status.Checks; got == nil || got.Commit != "sha-1" || got.State != p.checks {
				t.Errorf("status.checks = %+v, want CI's %q on sha-1 recorded", got, p.checks)
			}
			switch {
			case tt.pending && !errors.Is(err, errChecksPending):
				t.Errorf("gatedMerge() error = %v, want errChecksPending", err)
			case tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)):
				t.Errorf("gatedMerge() error = %v, want one with %q", err, tt.err)
			case !tt.pending && tt.err == "" && err != nil:
				t.Errorf("gatedMerge() error = %v", err)
			}
			if !strings.Contains(refusal, tt.refusal) || (tt.refusal == "") != (refusal == "") {
				t.Errorf("gatedMerge() refusal = %q, want one with %q", refusal, tt.refusal)
			}
		})
	}
}

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
