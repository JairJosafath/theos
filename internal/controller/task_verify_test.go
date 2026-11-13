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
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// verifierAgent creates the Agent that verifies a Crew's changes.
func verifierAgent(ns string) {
	GinkgoHelper()
	mustCreate(&c4v1beta1.Agent{ObjectMeta: objectMeta(ns, "verifier"),
		Spec: c4v1beta1.AgentSpec{Image: "theos/agent-go:dev", Model: "claude-haiku-5-5", MaxRounds: 3}})
}

// A verifierRef's CEL rules (docs/plans/done/sdlc-loop.md, step 4).
var _ = Describe("Task verifierRef", func() {
	verifier := func() *c4v1beta1.LocalRef { return &c4v1beta1.LocalRef{Name: "verifier"} }
	const onlyImplement = "only an Implement Task without allowedProtectedPaths has a verifierRef"

	It("is an Implement Task's alone, without allowedProtectedPaths", func() {
		ns := newProjectNamespace()
		ok := newTask(ns, "ok", "Export orders")
		ok.Spec.VerifierRef = verifier()
		mustCreate(ok)

		spec := newSpecTask(ns, "spec")
		spec.Spec.VerifierRef = verifier()
		Expect(k8sClient.Create(ctx, spec)).To(MatchError(ContainSubstring(onlyImplement)))

		triage := newTask(ns, "triage", "")
		triage.Spec.Kind, triage.Spec.Issue, triage.Spec.VerifierRef = c4v1beta1.TaskTriage, 2, verifier()
		Expect(k8sClient.Create(ctx, triage)).To(MatchError(ContainSubstring(onlyImplement)))

		gated := newTask(ns, "gated", "")
		gated.Spec.Issue, gated.Spec.AllowedProtectedPaths = 2, []string{"Makefile"}
		gated.Spec.VerifierRef = verifier()
		Expect(k8sClient.Create(ctx, gated)).To(MatchError(ContainSubstring(onlyImplement)))
	})

	It("never changes, nor comes or goes", func() {
		ns := newProjectNamespace()
		renamed := newTask(ns, "renamed", "Export orders")
		renamed.Spec.VerifierRef = verifier()
		mustCreate(renamed)
		Expect(patch(renamed, func() { renamed.Spec.VerifierRef.Name = "another" })).
			To(MatchError(ContainSubstring("verifierRef is immutable")))

		dropped := newTask(ns, "dropped", "Export orders")
		dropped.Spec.VerifierRef = verifier()
		mustCreate(dropped)
		Expect(patch(dropped, func() { dropped.Spec.VerifierRef = nil })).
			To(MatchError(ContainSubstring("verifierRef is immutable")))

		gained := newTask(ns, "gained", "Export orders")
		mustCreate(gained)
		Expect(patch(gained, func() { gained.Spec.VerifierRef = verifier() })).
			To(MatchError(ContainSubstring("verifierRef is immutable")))
	})

	It("lets a Crew's route name a verifier alone", func() {
		ns := newProjectNamespace()
		crew := newCrew(ns)
		crew.Spec.Routes = []c4v1beta1.CrewRoute{{Label: "kind/ui", Verifier: verifier()}}
		mustCreate(crew)
		empty := newCrewOf(ns, "cart-api")
		empty.Spec.Routes = []c4v1beta1.CrewRoute{{Label: "kind/ui"}}
		Expect(k8sClient.Create(ctx, empty)).To(MatchError(ContainSubstring(
			"a route names an implementer, a reviewer, a verifier or more of them")))
	})
})

// A Crew's verifier (docs/plans/done/sdlc-loop.md, step 4): an Implement Task whose
// reviewer approved is Verifying until its verifier, in a sandbox of its own,
// built and ran the reviewed head; the verifier's approval merges it, and its
// changes start the next round with their evidence.
var _ = Describe("Crew verify", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	// start makes orders-api Ready, the Crew's Agents and its verifier, files
	// a ready issue and creates the Crew, with verifier as its verifier when
	// set, and waits for the issue's Task to implement; it returns the issue.
	start := func(verifier string) int {
		GinkgoHelper()
		readyContainer(ns)
		crewAgents(ns, 3)
		verifierAgent(ns)
		n := gitFake.AddIssue(ns, "orders-api", "Export orders", clearIssue, []string{model.LabelReady})
		crew := newCrew(ns)
		if verifier != "" {
			crew.Spec.Verifier = &c4v1beta1.LocalRef{Name: verifier}
		}
		mustCreate(crew)
		Eventually(func(g Gomega) { sandboxExists(g, ns, fmt.Sprintf("orders-api-%d-r1-implement", n)) }).
			Should(Succeed())
		return n
	}
	// reviewed has round's implementer push sha, its checks pass and the
	// reviewer approve it with result's follow-ups and open criteria.
	reviewed := func(name string, round int, sha string, result agentrunner.Result) {
		GinkgoHelper()
		gitFake.PushBranchHead(ns, "orders-api", "task/"+name, sha)
		finishSandbox(ns, fmt.Sprintf("%s-r%d-implement", name, round), name,
			&agentrunner.Result{Role: "implement", Commit: sha, Summary: "Exported the orders."}, "")
		gitFake.SetChecks(ns, "orders-api", sha, "success")
		result.Role, result.Verdict = "review", model.VerdictApprove
		finishSandbox(ns, fmt.Sprintf("%s-r%d-review-a1", name, round), name, &result, "")
	}
	// verifying waits for the Task to run its verifier in round.
	verifying := func(name string, round int) {
		GinkgoHelper()
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskVerifying))
			g.Expect(task.Status.Round).To(Equal(int32(round)))
			g.Expect(sandboxEnv(g, ns, fmt.Sprintf("%s-r%d-verify-a1", name, round))).To(ContainElements(
				map[string]any{"name": "THEOS_ROLE", "value": "verify"},
				map[string]any{"name": "THEOS_MODEL", "value": "claude-haiku-5-5"},
				map[string]any{"name": "THEOS_BRANCH", "value": "task/" + name},
			))
		}).Should(Succeed())
	}

	It("verifies an approved change in a sandbox of its own, and merges it on the verifier's approval", func() {
		n := start("verifier")
		name := fmt.Sprintf("orders-api-%d", n)
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, name).Spec.VerifierRef).To(Equal(&c4v1beta1.LocalRef{Name: "verifier"}))
			g.Expect(gitFake.Issue(ns, "orders-api", n).Comments).To(ContainElement(ContainSubstring(
				"Agent `verifier` (claude-haiku-5-5) builds and runs the approved change")))
		}).Should(Succeed())

		By("putting the review's approval on the pull request, keeping what it left for the merge, and verifying")
		reviewed(name, 1, "sha-1", agentrunner.Result{Summary: "Every criterion met.",
			FollowUps: []model.FollowUp{{Title: "Export customers too", Body: "Same shape.",
				Criteria: []string{"GET /customers.csv lists every customer"}}},
			Open: []model.OpenCriterion{{Criterion: "A screenshot of the export", Why: "No browser."}}})
		verifying(name, 1)
		task := getTask(Default, ns, name)
		Expect(task.Status.Review).To(Equal(&c4v1beta1.TaskReview{Verdict: model.VerdictApprove,
			Summary: "Every criterion met.", FollowUps: []c4v1beta1.TaskFollowUp{{Title: "Export customers too",
				Body: "Same shape.", Criteria: []string{"GET /customers.csv lists every customer"}}},
			Open: []c4v1beta1.TaskOpenCriterion{{Criterion: "A screenshot of the export", Why: "No browser."}}}))
		Expect(task.Status.History).To(ContainElement(And(HaveField("Phase", c4v1beta1.TaskVerifying),
			HaveField("Reason", model.ReasonVerifying))))
		expectSandboxGone(ns, name+"-r1-review-a1")
		pr := gitFake.PullRequests(ns, "orders-api")[0]
		Expect(gitFake.Reviews(ns, "orders-api", pr.Number)).To(ConsistOf(And(HaveField("Event", ports.ReviewApprove),
			HaveField("Commit", "sha-1"), HaveField("Body", ContainSubstring("Review by Agent `reviewer`")))))
		Consistently(func() bool { return gitFake.PullRequests(ns, "orders-api")[0].Merged }, "2s").
			Should(BeFalse(), "nothing merges before the verifier approved")

		By("merging on the verifier's approval, filing the review's follow-up and both open criteria")
		finishSandbox(ns, name+"-r1-verify-a1", name, &agentrunner.Result{Role: "verify", Commit: "sha-1",
			Verdict: model.VerdictApprove, Summary: "GET /orders.csv lists every order: met; curl printed id,total.",
			Open: []model.OpenCriterion{{Criterion: "The CSV opens in a spreadsheet", Why: "No desktop."}}}, "")
		var byHand string
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.Round).To(Equal(int32(1)))
			g.Expect(task.Status.Verification).To(Equal(&c4v1beta1.TaskVerification{Verdict: model.VerdictApprove,
				Summary: "GET /orders.csv lists every order: met; curl printed id,total.", Commit: "sha-1",
				Open: []c4v1beta1.TaskOpenCriterion{{Criterion: "The CSV opens in a spreadsheet", Why: "No desktop."}}}))
			g.Expect(task.Status.FollowUps).To(HaveLen(1))
			g.Expect(task.Status.ByHand).NotTo(BeEmpty())
			g.Expect(task.Status.Runs).To(ContainElement(HaveField("Role", model.RoleVerifier)))
			byHand = task.Status.ByHand
		}).Should(Succeed())
		Expect(gitFake.PullRequests(ns, "orders-api")[0].Merged).To(BeTrue())
		Expect(gitFake.Reviews(ns, "orders-api", pr.Number)).To(ContainElement(And(
			HaveField("Event", ports.ReviewApprove), HaveField("Body", And(
				ContainSubstring("Verification by Agent `verifier` (claude-haiku-5-5), round 1."),
				ContainSubstring("curl printed id,total."))))))
		var filed *fake.Issue
		for _, i := range gitFake.OpenIssues(ns, "orders-api") {
			if i.URL == byHand {
				filed = &i
			}
		}
		Expect(filed).NotTo(BeNil())
		Expect(filed.Body).To(And(ContainSubstring("- [ ] A screenshot of the export"),
			ContainSubstring("- [ ] The CSV opens in a spreadsheet"),
			ContainSubstring("Agent `reviewer` (claude-opus-5-5) and Agent `verifier` (claude-haiku-5-5) approved it")))
		issue := gitFake.Issue(ns, "orders-api", n)
		Expect(issue.State).To(Equal("closed"))
		Expect(issue.Comments).To(ContainElement(And(ContainSubstring("Merged in"), ContainSubstring(byHand))))
	})

	It("starts the next round with the verifier's evidence when it asks for changes", func() {
		n := start("verifier")
		name := fmt.Sprintf("orders-api-%d", n)
		reviewed(name, 1, "sha-1", agentrunner.Result{Summary: "Looks right."})
		verifying(name, 1)
		evidence := "GET /orders.csv lists every order: not met; `curl -s localhost:8000/orders.csv` printed 404."
		finishSandbox(ns, name+"-r1-verify-a1", name, &agentrunner.Result{Role: "verify", Commit: "sha-1",
			Verdict: model.VerdictChanges, Summary: evidence}, "")

		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
			g.Expect(task.Status.Feedback).To(And(ContainSubstring("The verifier built and ran the change and asked "+
				"for changes"), ContainSubstring(evidence)))
			g.Expect(task.Status.Verification).To(And(HaveField("Verdict", model.VerdictChanges),
				HaveField("Summary", evidence)))
			g.Expect(sandboxEnv(g, ns, name+"-r2-implement")).To(ContainElement(HaveKeyWithValue("value",
				ContainSubstring(evidence))))
		}).Should(Succeed())
		pr := gitFake.PullRequests(ns, "orders-api")[0]
		Expect(pr.Merged).To(BeFalse())
		Expect(gitFake.Reviews(ns, "orders-api", pr.Number)).To(ContainElement(And(
			HaveField("Event", ports.ReviewRequestChanges), HaveField("Commit", "sha-1"),
			HaveField("Body", ContainSubstring("Verification by Agent `verifier`")))))

		By("verifying round 2 again, and merging it on the verifier's approval")
		reviewed(name, 2, "sha-2", agentrunner.Result{Summary: "The 404 is fixed."})
		verifying(name, 2)
		finishSandbox(ns, name+"-r2-verify-a1", name, &agentrunner.Result{Role: "verify", Commit: "sha-2",
			Verdict: model.VerdictApprove, Summary: "Every criterion met; curl printed id,total."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Verification).To(HaveField("Verdict", model.VerdictApprove))
		}).Should(Succeed())
	})

	It("merges a change its reviewer approved at once in a Crew without a verifier", func() {
		n := start("")
		name := fmt.Sprintf("orders-api-%d", n)
		Expect(getTask(Default, ns, name).Spec.VerifierRef).To(BeNil())
		reviewed(name, 1, "sha-1", agentrunner.Result{Summary: "Every criterion met."})
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.Verification).To(BeNil())
			for _, h := range task.Status.History {
				g.Expect(h.Phase).NotTo(Equal(c4v1beta1.TaskVerifying), "nothing is verified")
			}
		}).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, name+"-r1-verify-a1"), newSandbox()))).To(BeTrue())
	})

	It("never verifies a Spec Task, and verifies the build of its issue", func() {
		crew := specCrew(ns, c4v1beta1.SpecApprovalReviewer)
		verifierAgent(ns)
		crew.Spec.Verifier = &c4v1beta1.LocalRef{Name: "verifier"}
		n := gitFake.AddIssue(ns, "orders-api", "Export orders", "Orders as CSV.", []string{model.LabelNeedsSpec})
		mustCreate(crew)
		name := fmt.Sprintf("orders-api-%d-spec", n)
		Eventually(func(g Gomega) { sandboxExists(g, ns, name+"-r1-implement") }).Should(Succeed())
		Expect(getTask(Default, ns, name).Spec.VerifierRef).To(BeNil(), "a spec is not verified")

		head, err := gitFake.PushBranch(ctx, ports.Repository{Owner: ns, Name: "orders-api"},
			gitFake.MainHead(ns, "orders-api").Hash, "task/"+name,
			scaffold.Tree{"docs/specs/1.md": {Data: []byte(testSpec)}}, nil, "", ports.Signature{})
		Expect(err).NotTo(HaveOccurred())
		finishSandbox(ns, name+"-r1-implement", name,
			&agentrunner.Result{Role: "implement", Commit: head, Summary: "Wrote the spec."}, "")
		gitFake.SetChecks(ns, "orders-api", head, "success")
		finishSandbox(ns, name+"-r1-review-a1", name,
			&agentrunner.Result{Role: "review", Verdict: model.VerdictApprove, Summary: "A clear plan."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			for _, h := range task.Status.History {
				g.Expect(h.Phase).NotTo(Equal(c4v1beta1.TaskVerifying), "a spec is not verified")
			}
		}).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, name+"-r1-verify-a1"), newSandbox()))).To(BeTrue())

		By("verifying the Implement Task that builds the issue from its spec")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, fmt.Sprintf("orders-api-%d", n)).Spec.VerifierRef).To(
				Equal(&c4v1beta1.LocalRef{Name: "verifier"}))
		}).Should(Succeed())
	})

	It("gives an issue its route's verifier, even in a Crew without one", func() {
		readyContainer(ns)
		crewAgents(ns, 3)
		mustCreate(&c4v1beta1.Agent{ObjectMeta: objectMeta(ns, "browser"),
			Spec: c4v1beta1.AgentSpec{Image: "theos/agent-node:dev", MaxRounds: 3}})
		n := gitFake.AddIssue(ns, "orders-api", "Show orders", clearIssue, []string{model.LabelReady, "kind/ui"})
		crew := newCrew(ns)
		crew.Spec.Routes = []c4v1beta1.CrewRoute{{Label: "kind/ui", Verifier: &c4v1beta1.LocalRef{Name: "browser"}}}
		mustCreate(crew)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, fmt.Sprintf("orders-api-%d", n))
			g.Expect(task.Spec.VerifierRef).To(Equal(&c4v1beta1.LocalRef{Name: "browser"}))
			g.Expect(task.Spec.ReviewerRef).To(Equal(&c4v1beta1.LocalRef{Name: "reviewer"}))
		}).Should(Succeed())
	})
})
