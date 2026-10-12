/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

const clearIssue = "## Goal\n\nValidate orders.\n\n## Acceptance criteria\n\n- [ ] Rejects an empty order\n"

// newCrew returns the Crew of orders-api: Agent claude implements, Agent
// reviewer reviews.
func newCrew(ns string) *c4v1alpha1.Crew {
	return &c4v1alpha1.Crew{ObjectMeta: objectMeta(ns, "orders-api"), Spec: c4v1alpha1.CrewSpec{
		ContainerRef: c4v1alpha1.LocalRef{Name: "orders-api"},
		Implementer:  c4v1alpha1.LocalRef{Name: "claude"},
		Reviewer:     c4v1alpha1.LocalRef{Name: "reviewer"},
	}}
}

// crewAgents creates the implementer (Sonnet, maxRounds rounds) and the reviewer (Opus).
func crewAgents(ns string, rounds int32) {
	GinkgoHelper()
	implementer := newAgent(ns, rounds)
	implementer.Spec.Model = "claude-sonnet-5-5"
	reviewer := &c4v1alpha1.Agent{ObjectMeta: objectMeta(ns, "reviewer"),
		Spec: c4v1alpha1.AgentSpec{Image: "c4factory/agent-go:dev", Model: "claude-opus-5-5", MaxRounds: rounds}}
	mustCreate(implementer, reviewer)
}

func sandboxEnv(g Gomega, ns, name string) []any {
	sandbox := sandboxExists(g, ns, name)
	containers, _, _ := unstructured.NestedSlice(sandbox.Object, "spec", "podTemplate", "spec", "containers")
	g.Expect(containers).To(HaveLen(1))
	env, _ := containers[0].(map[string]any)["env"].([]any)
	return env
}

// getCrew reads the Crew of orders-api.
func getCrew(g Gomega, ns string) *c4v1alpha1.Crew {
	crew := &c4v1alpha1.Crew{}
	g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), crew)).To(Succeed())
	return crew
}

var _ = Describe("Crew", func() {
	It("keeps its Container for life", func() {
		ns := newProjectNamespace()
		crew := newCrew(ns)
		mustCreate(crew)
		Expect(patch(crew, func() { crew.Spec.ContainerRef.Name = "cart-api" })).
			To(MatchError(ContainSubstring("containerRef is immutable")))
	})

	It("works through ready issues in order, each one reviewed by its own Agent and closed on merge", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 2)
		vague := gitFake.addIssue(ns, "orders-api", "Make it better", "Somehow.", []string{model.LabelReady})
		blocked := gitFake.addIssue(ns, "orders-api", "Report invalid orders", clearIssue, []string{model.LabelReady}, "#3")
		first := gitFake.addIssue(ns, "orders-api", "Validate orders", clearIssue, []string{model.LabelReady})
		gitFake.addIssue(ns, "orders-api", "Someday", clearIssue, []string{model.LabelProposed})
		mustCreate(newCrew(ns))

		By("starting a Task for the lowest ready issue without open dependencies")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-3")
			g.Expect(task.Spec.Issue).To(Equal(int32(first)))
			g.Expect(task.Spec.ReviewerRef).To(Equal(&c4v1alpha1.LocalRef{Name: "reviewer"}))
			g.Expect(task.Labels).To(HaveKeyWithValue(LabelCrew, "orders-api"))
			crew := getCrew(g, ns)
			g.Expect(crew.Status.Phase).To(Equal(c4v1alpha1.CrewWorking))
			g.Expect(crew.Status.ActiveTasks).To(Equal([]string{"orders-api-3"}))
			g.Expect(crew.Status.Waiting).To(Equal([]int32{int32(blocked)}))
			g.Expect(crew.OwnerReferences).To(ContainElement(HaveField("Kind", "Container")))
		}).Should(Succeed())

		By("handing an issue without acceptance criteria back to a person")
		Eventually(func(g Gomega) {
			issue := gitFake.issue(ns, "orders-api", vague)
			g.Expect(issue.Labels).To(Equal([]string{model.LabelNeedsHuman}))
			g.Expect(issue.comments).To(ContainElement(ContainSubstring(`no "Acceptance criteria" section`)))
		}).Should(Succeed())

		By("running the implementer on the issue, with its model, and marking the issue taken")
		Eventually(func(g Gomega) {
			env := sandboxEnv(g, ns, "orders-api-3-r1-implement")
			g.Expect(env).To(ContainElements(
				map[string]any{"name": "C4_ISSUE", "value": "3"},
				map[string]any{"name": "C4_MODEL", "value": "claude-sonnet-5-5"},
				map[string]any{"name": "C4_HARNESS", "value": "claude-code"},
			))
			g.Expect(env).To(ContainElement(And(HaveKeyWithValue("name", "C4_GOAL"),
				HaveKeyWithValue("value", ContainSubstring("Issue #3: Validate orders")))))
			issue := gitFake.issue(ns, "orders-api", first)
			g.Expect(issue.Labels).To(Equal([]string{model.LabelInProgress}))
			g.Expect(issue.assignees).To(Equal([]string{ns + "-orders-api-agent"}))
			g.Expect(issue.comments).To(ContainElement(ContainSubstring("Task `orders-api-3` picked this up")))
			g.Expect(getTask(g, ns, "orders-api-3").Status.IssueTitle).To(Equal("Validate orders"))
		}).Should(Succeed())

		By("opening a pull request that closes the issue")
		gitFake.pushBranch(ns, "orders-api", "task/orders-api-3", "sha-1")
		finishSandbox(ns, "orders-api-3-r1-implement", "orders-api-3",
			&agentrunner.Result{Role: "implement", Commit: "sha-1", Summary: "Validated orders."}, "")
		gitFake.setChecks(ns, "orders-api", "sha-1", "success")

		By("reviewing with the reviewer Agent's model")
		Eventually(func(g Gomega) {
			g.Expect(sandboxEnv(g, ns, "orders-api-3-r1-review-a1")).
				To(ContainElement(map[string]any{"name": "C4_MODEL", "value": "claude-opus-5-5"}))
		}).Should(Succeed())
		pr := gitFake.pullRequests(ns, "orders-api")[0]
		Expect(gitFake.pullAuthor(ns, "orders-api", pr.Number)).To(Equal(ns+"-orders-api-agent"),
			"the implementer opens its pull request, so the operator may review it")
		finishSandbox(ns, "orders-api-3-r1-review-a1", "orders-api-3", &agentrunner.Result{Role: "review",
			Verdict: "approve", Summary: "Every criterion met.", FollowUps: []model.FollowUp{
				{Title: "Validate addresses", Body: "Postcodes are not checked.",
					Criteria: []string{"An order with an unknown postcode is rejected"}}}}, "")

		By("approving in the Git host, merging, filing the follow-up and closing the issue")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-3")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskMerged))
			g.Expect(task.Status.FollowUps).To(HaveLen(1))
		}).Should(Succeed())
		mergeCommit := gitFake.pullRequests(ns, "orders-api")[0].MergeCommit
		Expect(mergeCommit).NotTo(BeEmpty())
		Expect(getTask(Default, ns, "orders-api-3").Status.MergeCommit).To(Equal(mergeCommit),
			"the Task records the commit its merge made, where a release is tagged")

		By("reading the merge commit again when a Merged Task lacks it")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-3")
			task.Status.MergeCommit = ""
			g.Expect(k8sClient.Status().Update(ctx, task)).To(Succeed())
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-3").Status.MergeCommit).To(Equal(mergeCommit))
		}).Should(Succeed())
		Expect(gitFake.reviews(ns, "orders-api", pr.Number)).To(ConsistOf(And(
			HaveField("Event", gitprovider.ReviewApprove), HaveField("Commit", "sha-1"),
			HaveField("Body", And(ContainSubstring("Agent `reviewer` (claude-opus-5-5)"), ContainSubstring("Every criterion met."))))))
		issue := gitFake.issue(ns, "orders-api", first)
		Expect(issue.State).To(Equal("closed"))
		Expect(issue.Labels).To(BeEmpty())
		Expect(issue.comments).To(ContainElement(ContainSubstring("Merged in")))
		var followUp *fakeIssue
		for _, i := range gitFake.openIssues(ns, "orders-api") {
			if i.Title == "Validate addresses" {
				followUp = &i
			}
		}
		Expect(followUp).NotTo(BeNil())
		Expect(followUp.Labels).To(Equal([]string{model.LabelProposed}))
		Expect(followUp.milestone).To(Equal("Later"), "follow-ups are filed outside any release")
		Expect(followUp.Body).To(ContainSubstring("Postcodes are not checked."))
		Expect(followUp.Body).To(ContainSubstring("- [ ] An order with an unknown postcode is rejected"))
		Expect(model.MissingAcceptanceCriteria(followUp.Body)).To(BeEmpty(),
			"a follow-up moved into a release is ready as it stands")

		By("starting the blocked issue once its dependency is closed")
		gitFake.setBlockers(ns, "orders-api", blocked)
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-2").Spec.Issue).To(Equal(int32(blocked)))
		}).Should(Succeed())
	})

	It("gives a failed issue back, and tries again when it is ready again", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		n := gitFake.addIssue(ns, "orders-api", "Validate orders", clearIssue, []string{model.LabelReady})
		mustCreate(newCrew(ns))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())
		finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1", nil, "OOMKilled")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1").Status.Phase).To(Equal(c4v1alpha1.TaskFailed))
			issue := gitFake.issue(ns, "orders-api", n)
			g.Expect(issue.Labels).To(Equal([]string{model.LabelNeedsHuman}))
			g.Expect(issue.comments).To(ContainElement(ContainSubstring("Task `orders-api-1` failed")))
			g.Expect(getCrew(g, ns).Status.Phase).To(Equal(c4v1alpha1.CrewIdle))
		}).Should(Succeed())

		Expect(gitFake.labelIssue(ns, "orders-api", n, model.LabelReady)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1-2").Spec.Issue).To(Equal(int32(n)))
		}).Should(Succeed())
	})

	// #474: an approved review's open criteria go to a person as their own issue.
	It("files the criteria an approved review left open as a by-hand issue for a person, and closes the issue", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		n := gitFake.addIssue(ns, "orders-api", "Show orders in the README", clearIssue, []string{model.LabelReady})
		mustCreate(newCrew(ns))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }).Should(Succeed())
		gitFake.pushBranch(ns, "orders-api", "task/orders-api-1", "sha-1")
		finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1",
			&agentrunner.Result{Role: "implement", Commit: "sha-1",
				Summary: "Listed the orders.\n\nOpen: a screenshot of the dashboard: no browser here."}, "")
		gitFake.setChecks(ns, "orders-api", "sha-1", "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-review-a1") }).Should(Succeed())
		finishSandbox(ns, "orders-api-1-r1-review-a1", "orders-api-1", &agentrunner.Result{Role: "review",
			Verdict: "approve", Summary: "Every criterion met but the screenshot.", Open: []model.OpenCriterion{
				{Criterion: "The README has a screenshot of the dashboard", Why: "No browser in the sandbox."}}}, "")

		var byHand string
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskMerged))
			g.Expect(task.Status.ByHand).NotTo(BeEmpty())
			byHand = task.Status.ByHand
		}).Should(Succeed())
		var filed []fakeIssue
		for _, i := range gitFake.openIssues(ns, "orders-api") {
			if model.IsByHand(i.Labels) {
				filed = append(filed, i)
			}
		}
		Expect(filed).To(HaveLen(1), "one by-hand issue")
		issue := filed[0]
		Expect(issue.URL).To(Equal(byHand))
		Expect(issue.Title).To(Equal("By hand: Show orders in the README"))
		Expect(issue.Labels).To(ConsistOf(model.LabelByHand, model.LabelNeedsHuman), "no Crew picks it")
		Expect(issue.milestone).To(Equal("Later"), "no release waits on it")
		pr := gitFake.pullRequests(ns, "orders-api")[0]
		Expect(issue.Body).To(And(ContainSubstring(pr.URL), ContainSubstring(fmt.Sprintf("for #%d", n)),
			ContainSubstring("- [ ] The README has a screenshot of the dashboard\n  Needs a person: No browser in the sandbox."),
			ContainSubstring(model.ByHandMarker("orders-api-1"))))

		merged := gitFake.issue(ns, "orders-api", n)
		Expect(merged.State).To(Equal("closed"), "its pull request did the agents' part")
		Expect(merged.comments).To(ContainElement(And(ContainSubstring("Merged in"), ContainSubstring(byHand))))
	})

	It("works on several issues at once, each on its own branch", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 2)
		first := gitFake.addIssue(ns, "orders-api", "Validate orders", clearIssue, []string{model.LabelReady})
		second := gitFake.addIssue(ns, "orders-api", "Report invalid orders", clearIssue, []string{model.LabelReady})
		gitFake.addIssue(ns, "orders-api", "Price orders", clearIssue, []string{model.LabelReady})
		crew := newCrew(ns)
		crew.Spec.Parallel = 2
		mustCreate(crew)

		Eventually(func(g Gomega) {
			sandboxExists(g, ns, fmt.Sprintf("orders-api-%d-r1-implement", first))
			sandboxExists(g, ns, fmt.Sprintf("orders-api-%d-r1-implement", second))
			crew := getCrew(g, ns)
			g.Expect(crew.Status.ActiveTasks).To(ConsistOf("orders-api-1", "orders-api-2"))
			g.Expect(crew.Status.Ready).To(Equal([]int32{3}), "the third waits for a free place")
		}).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "orders-api-3"), &c4v1alpha1.Task{}))).To(BeTrue())
	})

	It("gives the release due first its places, and the next release the place it can't use", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		later := gitFake.addIssue(ns, "orders-api", "Price orders", clearIssue, []string{model.LabelReady})
		sooner := gitFake.addIssue(ns, "orders-api", "Validate orders", clearIssue, []string{model.LabelReady})
		chained := gitFake.addIssue(ns, "orders-api", "Report invalid orders", clearIssue, []string{model.LabelReady},
			fmt.Sprintf("#%d", sooner))
		gitFake.setIssueRelease(ns, "orders-api", later, "v0.2.0", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
		for _, n := range []int{sooner, chained} {
			gitFake.setIssueRelease(ns, "orders-api", n, "v0.1.1", time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC))
		}
		crew := newCrew(ns)
		crew.Spec.Parallel = 2
		mustCreate(crew)

		By("starting the release due first, and the next release in the place its chain can't use")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, fmt.Sprintf("orders-api-%d", sooner)).Spec.Issue).To(Equal(int32(sooner)))
			g.Expect(getTask(g, ns, fmt.Sprintf("orders-api-%d", later)).Spec.Issue).To(Equal(int32(later)))
			crew := getCrew(g, ns)
			g.Expect(crew.Status.ActiveTasks).To(ConsistOf(fmt.Sprintf("orders-api-%d", sooner),
				fmt.Sprintf("orders-api-%d", later)))
			g.Expect(crew.Status.Waiting).To(Equal([]int32{int32(chained)}))
		}).Should(Succeed())
	})

	It("sends an approved change that conflicts with main back to its implementer, in the same round", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 2)
		n := gitFake.addIssue(ns, "orders-api", "Validate orders", clearIssue, []string{model.LabelReady})
		mustCreate(newCrew(ns))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }, "30s").Should(Succeed())
		gitFake.pushBranch(ns, "orders-api", "task/orders-api-1", "sha-1")
		finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1", &agentrunner.Result{Role: "implement", Commit: "sha-1"}, "")
		gitFake.setChecks(ns, "orders-api", "sha-1", "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-review-a1") }, "30s").Should(Succeed())

		By("finding the pull request conflicts once approved")
		pr := gitFake.pullRequests(ns, "orders-api")[0]
		gitFake.conflict(ns, "orders-api", pr.Number, true)
		finishSandbox(ns, "orders-api-1-r1-review-a1", "orders-api-1",
			&agentrunner.Result{Role: "review", Verdict: "approve", Summary: "Good."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskImplementing))
			g.Expect(task.Status.Round).To(Equal(int32(1)), "a conflict with another change costs no round")
			g.Expect(task.Status.Feedback).To(ContainSubstring("Merge main into your branch"))
			g.Expect(task.Status.History).To(ContainElement(HaveField("Reason", "ResolvingConflicts")))
		}, "30s").Should(Succeed())
		Expect(gitFake.pullRequests(ns, "orders-api")[0].Merged).To(BeFalse())

		By("merging once the implementer merged main, its runs in sandboxes named afresh")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1").Status.Restarts).To(Equal(int32(1)))
			sandboxExists(g, ns, "orders-api-1-r1-implement-s1")
		}, "30s").Should(Succeed())
		gitFake.pushBranch(ns, "orders-api", "task/orders-api-1", "sha-2")
		gitFake.conflict(ns, "orders-api", pr.Number, false)
		finishSandbox(ns, "orders-api-1-r1-implement-s1", "orders-api-1", &agentrunner.Result{Role: "implement", Commit: "sha-2"}, "")
		gitFake.setChecks(ns, "orders-api", "sha-2", "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-review-s1-a1") }, "30s").Should(Succeed())
		finishSandbox(ns, "orders-api-1-r1-review-s1-a1", "orders-api-1",
			&agentrunner.Result{Role: "review", Verdict: "approve", Summary: "Conflicts resolved."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskMerged))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
		}, "30s").Should(Succeed())
		Expect(gitFake.issue(ns, "orders-api", n).State).To(Equal("closed"))
	})

	// approvedBehindMain runs a Crew's Task to its approval while main moved since
	// its checks ran, and returns the head the operator merged main into.
	approvedBehindMain := func(ns string) (pr gitprovider.PullRequest, head string) {
		readyContainer(ns)
		crewAgents(ns, 2)
		gitFake.addIssue(ns, "orders-api", "Validate orders", clearIssue, []string{model.LabelReady})
		mustCreate(newCrew(ns))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }, "30s").Should(Succeed())
		gitFake.pushBranch(ns, "orders-api", "task/orders-api-1", "sha-1")
		finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1", &agentrunner.Result{Role: "implement", Commit: "sha-1"}, "")
		gitFake.setChecks(ns, "orders-api", "sha-1", "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-review-a1") }, "30s").Should(Succeed())

		By("merging main into the branch once approved, as another change merged since its checks ran")
		pr = gitFake.pullRequests(ns, "orders-api")[0]
		gitFake.fallBehind(ns, "orders-api", pr.Number)
		finishSandbox(ns, "orders-api-1-r1-review-a1", "orders-api-1",
			&agentrunner.Result{Role: "review", Verdict: "approve", Summary: "Good."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskChecking))
			g.Expect(task.Status.Round).To(Equal(int32(1)), "main moving costs no round")
			g.Expect(task.Status.MergeWhenChecksPass).To(BeTrue())
			g.Expect(task.Status.Commit).NotTo(Equal("sha-1"))
			g.Expect(task.Status.Commit).To(Equal(gitFake.pullRequests(ns, "orders-api")[0].HeadSHA))
			g.Expect(task.Status.History).To(ContainElement(HaveField("Reason", "UpdatingBranch")))
			head = task.Status.Commit
		}, "30s").Should(Succeed())
		Consistently(func() bool { return gitFake.pullRequests(ns, "orders-api")[0].Merged }, "2s").
			Should(BeFalse(), "it waits for the checks on the new head")
		return pr, head
	}

	It("merges an approved change whose checks ran on an older main only once they pass with main merged in", func() {
		ns := newProjectNamespace()
		pr, first := approvedBehindMain(ns)

		By("merging main in again when main moved again while the checks ran")
		gitFake.fallBehind(ns, "orders-api", pr.Number)
		gitFake.setChecks(ns, "orders-api", first, "success")
		var head string
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskChecking))
			g.Expect(task.Status.Commit).NotTo(Equal(first))
			g.Expect(task.Status.MergeWhenChecksPass).To(BeTrue())
			head = task.Status.Commit
		}, "30s").Should(Succeed())
		Expect(gitFake.pullRequests(ns, "orders-api")[0].Merged).To(BeFalse())

		By("merging once the new head's checks pass, with no new review")
		gitFake.setChecks(ns, "orders-api", head, "success")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskMerged))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.Review).NotTo(BeNil())
			g.Expect(task.Status.Review.Verdict).To(Equal(model.VerdictApprove))
		}, "30s").Should(Succeed())
		Expect(gitFake.issue(ns, "orders-api", 1).State).To(Equal("closed"))
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "orders-api-1-r1-review-a2"), newSandbox()))).
			To(BeTrue(), "no reviewer runs again")
	})

	It("starts the next round when an approved change's checks fail with main merged in", func() {
		ns := newProjectNamespace()
		pr, head := approvedBehindMain(ns)

		gitFake.setChecks(ns, "orders-api", head, "failure")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskImplementing))
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.MergeWhenChecksPass).To(BeFalse())
			g.Expect(task.Status.Feedback).To(ContainSubstring("main moved since its checks ran"))
		}, "30s").Should(Succeed())
		Expect(gitFake.pullRequests(ns, "orders-api")[pr.Number-1].Merged).To(BeFalse())
	})

	It("merges a cleanly updated branch the Git host is still testing, not sending it back for conflicts", func() {
		ns := newProjectNamespace()
		pr, head := approvedBehindMain(ns)

		By("waiting while the Git host says the pull request doesn't merge, as it tests it against main")
		gitFake.retest(ns, "orders-api", pr.Number, true)
		gitFake.setChecks(ns, "orders-api", head, "success")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			cond := meta.FindStatusCondition(task.Status.Conditions, ConditionProgressing)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Message).To(ContainSubstring("waiting for the Git host"))
		}, "30s").Should(Succeed())
		Consistently(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskChecking))
			g.Expect(task.Status.History).NotTo(ContainElement(HaveField("Reason", "ResolvingConflicts")))
		}, "2s").Should(Succeed())

		By("merging once the Git host found it merges")
		gitFake.retest(ns, "orders-api", pr.Number, false)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskMerged))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
		}, "30s").Should(Succeed())
	})

	It("merges an approved change whose implementer, sent to resolve conflicts, finds none", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 2)
		gitFake.addIssue(ns, "orders-api", "Validate orders", clearIssue, []string{model.LabelReady})
		mustCreate(newCrew(ns))
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-implement") }, "30s").Should(Succeed())
		gitFake.pushBranch(ns, "orders-api", "task/orders-api-1", "sha-1")
		finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1", &agentrunner.Result{Role: "implement", Commit: "sha-1"}, "")
		gitFake.setChecks(ns, "orders-api", "sha-1", "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-review-a1") }, "30s").Should(Succeed())
		pr := gitFake.pullRequests(ns, "orders-api")[0]
		gitFake.conflict(ns, "orders-api", pr.Number, true)
		finishSandbox(ns, "orders-api-1-r1-review-a1", "orders-api-1",
			&agentrunner.Result{Role: "review", Verdict: "approve", Summary: "Good."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, "orders-api-1").Status.History).To(ContainElement(HaveField("Reason", "ResolvingConflicts")))
		}, "30s").Should(Succeed())

		By("the implementer finding no conflict and committing nothing")
		Eventually(func(g Gomega) {
			g.Expect(sandboxEnv(g, ns, "orders-api-1-r1-implement-s1")).To(ContainElement(
				map[string]any{"name": agentrunner.EnvPullRequest, "value": fmt.Sprint(pr.Number)}))
		}, "30s").Should(Succeed())
		gitFake.conflict(ns, "orders-api", pr.Number, false)
		finishSandbox(ns, "orders-api-1-r1-implement-s1", "orders-api-1",
			&agentrunner.Result{Role: "implement", Commit: "sha-1", Summary: "Main merges cleanly; nothing to resolve."}, "")

		By("reviewing the branch as it is and merging it, in the same round")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-review-s1-a1") }, "30s").Should(Succeed())
		finishSandbox(ns, "orders-api-1-r1-review-s1-a1", "orders-api-1",
			&agentrunner.Result{Role: "review", Verdict: "approve", Summary: "Still good."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskMerged))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
		}, "30s").Should(Succeed())
	})

	It("starts nothing while paused", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		gitFake.addIssue(ns, "orders-api", "Validate orders", clearIssue, []string{model.LabelReady})
		crew := newCrew(ns)
		crew.Spec.Paused = true
		mustCreate(crew)
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Status.Phase).To(Equal(c4v1alpha1.CrewPaused))
			g.Expect(crew.Status.Ready).To(Equal([]int32{1}))
		}).Should(Succeed())
		Consistently(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "orders-api-1"), &c4v1alpha1.Task{}))
		}, "3s").Should(BeTrue())
	})

	It("waits for its Agents", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		mustCreate(newCrew(ns))
		Eventually(func(g Gomega) {
			cond := condition(getCrew(g, ns).Status.Conditions, ConditionReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal("AgentNotFound"))
		}).Should(Succeed())

		By("starting once its Agents exist, without waiting for a poll")
		crewAgents(ns, 1)
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Status.Phase).To(Equal(c4v1alpha1.CrewIdle))
			g.Expect(condition(crew.Status.Conditions, ConditionReady).Status).To(Equal(metav1.ConditionTrue))
		}).Should(Succeed())
	})
})
