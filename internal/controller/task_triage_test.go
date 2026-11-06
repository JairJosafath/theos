/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
)

// newTriageTask is a triage of issue 4 of orders-api.
func newTriageTask(ns, name string) *c4v1beta1.Task {
	return &c4v1beta1.Task{ObjectMeta: objectMeta(ns, name), Spec: c4v1beta1.TaskSpec{
		Kind:         c4v1beta1.TaskTriage,
		ContainerRef: c4v1beta1.LocalRef{Name: "orders-api"},
		AgentRef:     c4v1beta1.LocalRef{Name: "triager"},
		Issue:        4,
	}}
}

// triagingCrew makes orders-api Ready, its Crew's Agents and the triager, and
// returns the Crew, with the triager named, for the test to create.
func triagingCrew(ns string) *c4v1beta1.Crew {
	GinkgoHelper()
	readyContainer(ns)
	crewAgents(ns, 2)
	mustCreate(&c4v1beta1.Agent{ObjectMeta: objectMeta(ns, "triager"),
		Spec: c4v1beta1.AgentSpec{Image: "theos/agent-go:dev", Model: "claude-haiku-5-5"}})
	crew := newCrew(ns)
	crew.Spec.Triager = &c4v1beta1.LocalRef{Name: "triager"}
	return crew
}

// triageResult is what a triager run reports for decision d.
func triageResult(d model.Triage) *agentrunner.Result {
	return &agentrunner.Result{Role: agentrunner.RoleTriage, Triage: &d}
}

// A Triage Task's CEL rules (docs/plans/done/sdlc-loop.md, step 2).
var _ = Describe("Triage Task", func() {
	It("needs an issue, and has no reviewer and no allowance", func() {
		ns := newProjectNamespace()
		mustCreate(newTriageTask(ns, "ok"))

		noIssue := newTriageTask(ns, "no-issue")
		noIssue.Spec.Issue, noIssue.Spec.Goal = 0, "Triage it"
		Expect(k8sClient.Create(ctx, noIssue)).To(MatchError(ContainSubstring("a Triage Task needs an issue")))

		reviewed := newTriageTask(ns, "reviewed")
		reviewed.Spec.ReviewerRef = &c4v1beta1.LocalRef{Name: "reviewer"}
		Expect(k8sClient.Create(ctx, reviewed)).To(MatchError(ContainSubstring(
			"a Triage Task has no reviewerRef and no allowedProtectedPaths")))

		allowed := newTriageTask(ns, "allowed")
		allowed.Spec.AllowedProtectedPaths = []string{"Makefile"}
		Expect(k8sClient.Create(ctx, allowed)).To(MatchError(ContainSubstring(
			"a Triage Task has no reviewerRef and no allowedProtectedPaths")))
	})
})

// A Crew's triage (docs/plans/done/sdlc-loop.md, step 2): one Triage Task at a time
// for a proposed issue outside Later, whose triager's decision the operator
// applies to the issue.
var _ = Describe("Crew triage", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	// triageOne files one proposed issue with body, makes the triaging Crew,
	// and waits for the issue's Triage Task to run; it returns the issue and
	// the Task's name.
	triageOne := func(body string) (int, string) {
		GinkgoHelper()
		crew := triagingCrew(ns)
		n := gitFake.AddIssue(ns, "orders-api", "Validate orders", body, []string{model.LabelProposed})
		mustCreate(crew)
		name := fmt.Sprintf("orders-api-%d-triage", n)
		Eventually(func(g Gomega) { sandboxExists(g, ns, name+"-r1-triage") }).Should(Succeed())
		return n, name
	}
	triaged := func(g Gomega, name, decision string) *c4v1beta1.Task {
		task := getTask(g, ns, name)
		g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
		g.Expect(task.Status.Triage).NotTo(BeNil())
		g.Expect(task.Status.Triage.Decision).To(Equal(decision))
		g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(And(
			HaveField("Status", metav1.ConditionTrue), HaveField("Reason", reasonTriaged)))
		return task
	}

	// readyOrTaken is a ready issue's labels: status/ready, or status/in-progress
	// once the Crew took it up, which it may do at once.
	readyOrTaken := BeElementOf([]string{model.LabelReady}, []string{model.LabelInProgress})

	It("starts no Triage Task for a Crew without a triager", func() {
		readyContainer(ns)
		crewAgents(ns, 2)
		n := gitFake.AddIssue(ns, "orders-api", "Someday", "Maybe.", []string{model.LabelProposed})
		mustCreate(newCrew(ns))
		Eventually(func(g Gomega) {
			crew := getCrew(g, ns)
			g.Expect(crew.Status.LastPolled).NotTo(BeNil())
			g.Expect(crew.Status.Phase).To(Equal(c4v1beta1.CrewIdle))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			var tasks c4v1beta1.TaskList
			g.Expect(k8sClient.List(ctx, &tasks)).To(Succeed())
			for _, t := range tasks.Items {
				g.Expect(t.Namespace == ns && t.Spec.Kind == c4v1beta1.TaskTriage).To(BeFalse())
			}
			g.Expect(gitFake.Issue(ns, "orders-api", n).Labels).To(Equal([]string{model.LabelProposed}))
		}, "3s").Should(Succeed())
	})

	It("triages one proposed issue at a time, outside Later, beside the ready ones", func() {
		crew := triagingCrew(ns)
		later := gitFake.AddIssue(ns, "orders-api", "A follow-up", "Later.", []string{model.LabelProposed})
		gitFake.PutInMilestone(ns, "orders-api", later, model.FollowUpMilestone)
		first := gitFake.AddIssue(ns, "orders-api", "Export orders", "As CSV.", []string{model.LabelProposed})
		second := gitFake.AddIssue(ns, "orders-api", "Import orders", "From CSV.", []string{model.LabelProposed})
		ready := gitFake.AddIssue(ns, "orders-api", "Validate orders", clearIssue, []string{model.LabelReady})
		mustCreate(crew)

		By("running the triager on the lowest issue, and the implementer on the ready one, at parallel 1")
		firstTask := fmt.Sprintf("orders-api-%d-triage", first)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, firstTask)
			g.Expect(task.Spec.Kind).To(Equal(c4v1beta1.TaskTriage))
			g.Expect(task.Spec.AgentRef.Name).To(Equal("triager"))
			g.Expect(task.Spec.ReviewerRef).To(BeNil())
			g.Expect(task.Labels).To(HaveKeyWithValue(LabelCrew, "orders-api"))
			g.Expect(getTask(g, ns, fmt.Sprintf("orders-api-%d", ready)).Spec.Kind).To(Equal(c4v1beta1.TaskImplement))
			env := sandboxEnv(g, ns, firstTask+"-r1-triage")
			g.Expect(env).To(ContainElements(
				map[string]any{"name": "THEOS_ROLE", "value": "triage"},
				map[string]any{"name": "THEOS_ISSUE", "value": fmt.Sprint(first)},
				map[string]any{"name": "THEOS_MODEL", "value": "claude-haiku-5-5"},
			))
			g.Expect(env).To(ContainElement(And(HaveKeyWithValue("name", "THEOS_GOAL"),
				HaveKeyWithValue("value", ContainSubstring(fmt.Sprintf("Issue #%d: Export orders", first))))))
			crew := getCrew(g, ns)
			g.Expect(crew.Status.ActiveTasks).To(ContainElement(firstTask))
			g.Expect(condition(crew.Status.Conditions, ConditionReady)).To(HaveField("Message",
				ContainSubstring(fmt.Sprintf("Task %s triages issue #%d", firstTask, first))))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			err := k8sClient.Get(ctx, key(ns, fmt.Sprintf("orders-api-%d-triage", second)), &c4v1beta1.Task{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "one triage at a time")
			g.Expect(gitFake.Issue(ns, "orders-api", first).Labels).To(Equal([]string{model.LabelProposed}),
				"the issue stays proposed while it is triaged")
		}, "2s").Should(Succeed())

		By("going on to the next once the first is decided, never to Later's")
		finishSandbox(ns, firstTask+"-r1-triage", firstTask,
			triageResult(model.Triage{Decision: model.TriageSpec, Reason: "Too big."}), "")
		Eventually(func(g Gomega) {
			triaged(g, firstTask, model.TriageSpec)
			sandboxExists(g, ns, fmt.Sprintf("orders-api-%d-triage-r1-triage", second))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			err := k8sClient.Get(ctx, key(ns, fmt.Sprintf("orders-api-%d-triage", later)), &c4v1beta1.Task{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "reviewers' follow-ups stay a person's call")
			err = k8sClient.Get(ctx, key(ns, firstTask+"-2"), &c4v1beta1.Task{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "a decided issue is not triaged again")
		}, "2s").Should(Succeed())
	})

	It("writes a ready issue's criteria into its body, labels it ready, and the Crew builds it", func() {
		n, name := triageOne("Reject orders that have no lines.")
		finishSandbox(ns, name+"-r1-triage", name, triageResult(model.Triage{Decision: model.TriageReady,
			Reason: "Small and clear.", Criteria: []string{"- [ ] An order without lines is rejected", "A test covers it"}}), "")
		Eventually(func(g Gomega) {
			task := triaged(g, name, model.TriageReady)
			g.Expect(task.Status.Triage.Criteria).To(Equal([]string{"An order without lines is rejected", "A test covers it"}))
			g.Expect(task.Status.Triage.Applied).To(Equal("Labelled status/ready and wrote 2 acceptance criteria into it"))
			issue := gitFake.Issue(ns, "orders-api", n)
			g.Expect(issue.Labels).To(readyOrTaken)
			g.Expect(issue.Body).To(Equal("Reject orders that have no lines.\n\n## Acceptance criteria\n\n" +
				"- [ ] An order without lines is rejected\n- [ ] A test covers it\n"))
			g.Expect(issue.Comments).To(ContainElement(ContainSubstring("Triaged by Task " + name + ": ready to build. Small and clear.")))
		}).Should(Succeed())

		By("picking the issue up as ready: the triage was no try at it")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, fmt.Sprintf("orders-api-%d", n)).Spec.Kind).To(Equal(c4v1beta1.TaskImplement))
		}).Should(Succeed())
	})

	// docs/plans/done/test-environments.md, T13: the triager reads the project's
	// test environments and names the ones an issue needs; the operator
	// labels it env/<name>, and the issue's run leases it.
	It("labels a ready issue env/<name> for the test environment the triager names, and its run leases it", func() {
		mustCreate(newBrowserEnvironment(ns))
		n, name := triageOne(clearIssue)
		pod := sandboxPodSpec(Default, sandboxExists(Default, ns, name+"-r1-triage"))
		Expect(envVar(pod, agentrunner.EnvEnvironments)).To(HaveField("Value", ContainSubstring(`"name":"browser"`)))
		Expect(envVar(pod, "THEOS_ENV_BROWSER_URL")).To(BeNil(), "the triager leases nothing")
		finishSandbox(ns, name+"-r1-triage", name, triageResult(model.Triage{Decision: model.TriageReady,
			Reason: "Its pictures need a browser.", Environments: []string{"browser", "android"}}), "")
		Eventually(func(g Gomega) {
			task := triaged(g, name, model.TriageReady)
			g.Expect(task.Status.Triage.Environments).To(Equal([]string{"android", "browser"}))
			g.Expect(task.Status.Triage.Applied).To(Equal("Labelled status/ready and env/browser"))
			g.Expect(gitFake.Issue(ns, "orders-api", n).Labels).To(ContainElement("env/browser"))
		}).Should(Succeed())

		By("leasing the browser for the issue's run")
		Eventually(func(g Gomega) {
			pod := sandboxPodSpec(g, sandboxExists(g, ns, fmt.Sprintf("orders-api-%d-r1-implement", n)))
			g.Expect(envVar(pod, "THEOS_ENV_BROWSER_URL")).NotTo(BeNil())
		}, "20s").Should(Succeed())
	})

	It("keeps a ready issue's own criteria", func() {
		n, name := triageOne(clearIssue)
		finishSandbox(ns, name+"-r1-triage", name, triageResult(model.Triage{Decision: model.TriageReady,
			Reason: "Clear.", Criteria: []string{"Something else"}}), "")
		Eventually(func(g Gomega) {
			task := triaged(g, name, model.TriageReady)
			g.Expect(task.Status.Triage.Applied).To(Equal("Labelled status/ready"))
			issue := gitFake.Issue(ns, "orders-api", n)
			g.Expect(issue.Labels).To(readyOrTaken)
			g.Expect(issue.Body).To(Equal(clearIssue))
		}).Should(Succeed())
	})

	It("labels an issue that needs a spec", func() {
		n, name := triageOne("Rebuild billing.")
		finishSandbox(ns, name+"-r1-triage", name,
			triageResult(model.Triage{Decision: model.TriageSpec, Reason: "Too big for one change."}), "")
		Eventually(func(g Gomega) {
			triaged(g, name, model.TriageSpec)
			issue := gitFake.Issue(ns, "orders-api", n)
			g.Expect(issue.Labels).To(Equal([]string{model.LabelNeedsSpec}))
			g.Expect(issue.Comments).To(ContainElement(ContainSubstring("needs a spec before it is built. Too big")))
		}).Should(Succeed())
	})

	It("parks an issue with its question for a person", func() {
		n, name := triageOne("Make it faster.")
		finishSandbox(ns, name+"-r1-triage", name, triageResult(model.Triage{Decision: model.TriagePark,
			Reason: "No target.", Question: "How fast, measured how?"}), "")
		Eventually(func(g Gomega) {
			task := triaged(g, name, model.TriagePark)
			g.Expect(task.Status.Triage.Question).To(Equal("How fast, measured how?"))
			issue := gitFake.Issue(ns, "orders-api", n)
			g.Expect(issue.Labels).To(Equal([]string{model.LabelNeedsHuman}))
			g.Expect(issue.Comments).To(ContainElement(ContainSubstring("**Question:** How fast, measured how?")))
		}).Should(Succeed())
	})

	It("takes a person's answer from chat to the parked issue, which is triaged again", func() {
		n, name := triageOne("Make it faster.")
		finishSandbox(ns, name+"-r1-triage", name, triageResult(model.Triage{Decision: model.TriagePark,
			Reason: "No target.", Question: "How fast, measured how?"}), "")
		Eventually(func(g Gomega) { triaged(g, name, model.TriagePark) }).Should(Succeed())
		ada := "ada-" + ns + "@example.com"
		grant(ada, roleTaskWord)
		task := &c4v1beta1.Task{}
		Expect(k8sClient.Get(ctx, key(ns, name), task)).To(Succeed())
		Expect(patchAs(asUser(ada), task, chatWord(task, model.ChatAnnotationAnswer, "Under 200 ms at p95.", ada))).
			To(Succeed())
		Eventually(func(g Gomega) {
			task := triaged(g, name, model.TriagePark)
			g.Expect(task.Status.Triage.AnsweredBy).To(Equal(ada))
			g.Expect(task.Status.Triage.Answered).To(ContainSubstring("labelled it status/proposed"))
			issue := gitFake.Issue(ns, "orders-api", n)
			g.Expect(issue.Comments).To(ContainElement(ada + " answered in chat:\n\nUnder 200 ms at p95."))
			g.Expect(issue.Labels).To(Equal([]string{model.LabelProposed}))
		}).Should(Succeed())
		By("triaging it again, with the answer on the issue")
		Eventually(func(g Gomega) { sandboxExists(g, ns, name+"-2-r1-triage") }).Should(Succeed())
		comments := len(gitFake.Issue(ns, "orders-api", n).Comments)
		Consistently(func(g Gomega) {
			g.Expect(gitFake.Issue(ns, "orders-api", n).Comments).To(HaveLen(comments), "the answer is taken once")
		}, "2s").Should(Succeed())
	})

	It("closes a duplicate, naming its original", func() {
		original := gitFake.AddIssue(ns, "orders-api", "Validate orders", clearIssue, nil)
		n, name := triageOne("Check orders.")
		finishSandbox(ns, name+"-r1-triage", name, triageResult(model.Triage{Decision: model.TriageDuplicate,
			Reason: "Same ask.", Duplicate: original}), "")
		Eventually(func(g Gomega) {
			task := triaged(g, name, model.TriageDuplicate)
			g.Expect(task.Status.Triage.Duplicate).To(Equal(int32(original)))
			issue := gitFake.Issue(ns, "orders-api", n)
			g.Expect(issue.State).To(Equal("closed"))
			g.Expect(issue.Comments).To(ContainElement(ContainSubstring(fmt.Sprintf("a duplicate of #%d. Same ask.", original))))
		}).Should(Succeed())
	})

	It("leaves the issue proposed with a comment when the triage fails, and tries again", func() {
		n, name := triageOne("Something.")
		finishSandbox(ns, name+"-r1-triage", name, &agentrunner.Result{Role: agentrunner.RoleTriage,
			Error: "the triager gave no JSON decision"}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(task.Status.Triage).To(BeNil())
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(HaveField("Message",
				"The triage failed: the triager gave no JSON decision"))
			issue := gitFake.Issue(ns, "orders-api", n)
			g.Expect(issue.Labels).To(Equal([]string{model.LabelProposed}))
			g.Expect(issue.Comments).To(ContainElement(And(
				ContainSubstring("Triage by Task "+name+" failed: the triager gave no JSON decision"),
				ContainSubstring("the Crew triages it again (try 1 of 3 failed)"))))
			sandboxExists(g, ns, name+"-2-r1-triage")
		}, 20*time.Second).Should(Succeed())

		By("failing a decision the operator can't apply the same way")
		finishSandbox(ns, name+"-2-r1-triage", name+"-2", triageResult(model.Triage{Decision: model.TriageReady,
			Reason: "Clear."}), "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, name+"-2").Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			issue := gitFake.Issue(ns, "orders-api", n)
			g.Expect(issue.Labels).To(Equal([]string{model.LabelProposed}))
			g.Expect(issue.Body).To(Equal("Something."))
			g.Expect(issue.Comments).To(ContainElement(ContainSubstring("gave no acceptance criteria and the issue has none")))
		}).Should(Succeed())
	})
})
