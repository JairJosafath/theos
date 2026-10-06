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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

	It("finishes one release before it starts the next", func() {
		ns := newProjectNamespace()
		readyContainer(ns)
		crewAgents(ns, 1)
		later := gitFake.addIssue(ns, "orders-api", "Price orders", clearIssue, []string{model.LabelReady})
		sooner := gitFake.addIssue(ns, "orders-api", "Validate orders", clearIssue, []string{model.LabelReady})
		gitFake.setIssueRelease(ns, "orders-api", later, "v0.2.0", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
		gitFake.setIssueRelease(ns, "orders-api", sooner, "v0.1.1", time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC))
		crew := newCrew(ns)
		crew.Spec.Parallel = 2
		mustCreate(crew)

		By("starting only the release due first, and holding the next while its issue is in progress")
		task := fmt.Sprintf("orders-api-%d", sooner)
		Eventually(func(g Gomega) {
			g.Expect(gitFake.issue(ns, "orders-api", sooner).Labels).To(Equal([]string{model.LabelInProgress}))
			crew := getCrew(g, ns)
			g.Expect(crew.Status.ActiveTasks).To(Equal([]string{task}))
			g.Expect(crew.Status.Waiting).To(Equal([]int32{int32(later)}))
			g.Expect(condition(crew.Status.Conditions, ConditionReady).Message).
				To(ContainSubstring("1 ready issue waits for release v0.1.1"))
		}).Should(Succeed())
		Consistently(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, fmt.Sprintf("orders-api-%d", later)), &c4v1alpha1.Task{}))
		}, 3*time.Second).Should(BeTrue(), "the next release waits while the first one's issue is in progress")

		By("starting the next release once the first has no work left")
		finishSandbox(ns, task+"-r1-implement", task, nil, "OOMKilled")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, fmt.Sprintf("orders-api-%d", later)).Spec.Issue).To(Equal(int32(later)))
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

		By("merging once the implementer merged main")
		Expect(k8sClient.Delete(ctx, &corev1.Pod{ObjectMeta: objectMeta(ns, "orders-api-1-r1-implement")})).To(Succeed())
		Eventually(func(g Gomega) {
			sandbox := sandboxExists(g, ns, "orders-api-1-r1-implement")
			g.Expect(sandbox.GetDeletionTimestamp()).To(BeNil())
		}, "30s").Should(Succeed())
		gitFake.pushBranch(ns, "orders-api", "task/orders-api-1", "sha-2")
		gitFake.conflict(ns, "orders-api", pr.Number, false)
		finishSandbox(ns, "orders-api-1-r1-implement", "orders-api-1", &agentrunner.Result{Role: "implement", Commit: "sha-2"}, "")
		gitFake.setChecks(ns, "orders-api", "sha-2", "success")
		Eventually(func(g Gomega) { sandboxExists(g, ns, "orders-api-1-r1-review-a1") }, "30s").Should(Succeed())
		Expect(k8sClient.Delete(ctx, &corev1.Pod{ObjectMeta: objectMeta(ns, "orders-api-1-r1-review-a1")})).To(Succeed())
		finishSandbox(ns, "orders-api-1-r1-review-a1", "orders-api-1",
			&agentrunner.Result{Role: "review", Verdict: "approve", Summary: "Conflicts resolved."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "orders-api-1")
			g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskMerged))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
		}, "30s").Should(Succeed())
		Expect(gitFake.issue(ns, "orders-api", n).State).To(Equal("closed"))
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
	})
})
