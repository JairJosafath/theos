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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// The shape of a Crew's approval rules (docs/plans/done/approval-rules.md, #62).
var _ = Describe("Crew approval", func() {
	It("takes rules by path, label and size, and refuses one that asks nothing it knows", func() {
		ns := newProjectNamespace()
		crew := newCrew(ns)
		crew.Spec.Approval = []c4v1beta1.ApprovalRule{
			{Paths: []string{"migrations/", "internal/auth/", "*.sql"}, Require: c4v1beta1.ApprovalPerson},
			{Labels: []string{"risk/high"}, ChangedLinesOver: 400, Require: c4v1beta1.ApprovalPerson},
			{Labels: []string{"kind/docs"}, Require: c4v1beta1.ApprovalNone},
			{Require: c4v1beta1.ApprovalReviewer},
		}
		mustCreate(crew)

		with := func(rule c4v1beta1.ApprovalRule) error {
			bad := newCrewOf(ns, "cart-api")
			bad.Spec.Approval = []c4v1beta1.ApprovalRule{rule}
			return k8sClient.Create(ctx, bad)
		}
		Expect(with(c4v1beta1.ApprovalRule{Paths: []string{"docs/"}})).To(MatchError(ContainSubstring(
			`supported values: "Person", "Reviewer", "None"`)), "a rule says what it requires")
		Expect(with(c4v1beta1.ApprovalRule{Require: "Team"})).To(MatchError(ContainSubstring(
			`supported values: "Person", "Reviewer", "None"`)))
		Expect(with(c4v1beta1.ApprovalRule{Paths: []string{"/migrations/"}, Require: c4v1beta1.ApprovalPerson})).
			To(MatchError(ContainSubstring("paths are relative to the repository's root: no leading /")))
		Expect(with(c4v1beta1.ApprovalRule{Paths: []string{""}, Require: c4v1beta1.ApprovalPerson})).
			To(MatchError(ContainSubstring("should be at least 1 chars long")))
		Expect(with(c4v1beta1.ApprovalRule{ChangedLinesOver: -1, Require: c4v1beta1.ApprovalPerson})).
			To(MatchError(ContainSubstring("should be greater than or equal to 1")))

		many := newCrewOf(ns, "cart-api")
		for range 17 {
			many.Spec.Approval = append(many.Spec.Approval, c4v1beta1.ApprovalRule{Require: c4v1beta1.ApprovalPerson})
		}
		Expect(k8sClient.Create(ctx, many)).To(MatchError(ContainSubstring("must have at most 16 items")))
	})
})

// A Crew's approval rules at work (docs/plans/done/approval-rules.md, #62): a
// change the rules hold for a person waits, after its agents approved, until
// a person approves its pull request; one they ask no review of merges on
// its checks; and one no rule matches is reviewed and merged as ever.
var _ = Describe("Crew approval rules", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	personForMigrations := []c4v1beta1.ApprovalRule{
		{Paths: []string{"migrations/"}, Require: c4v1beta1.ApprovalPerson}}
	const whyMigrations = "approval rule 1 (paths migrations/) asks for a person for migrations/001.sql"

	// start makes orders-api Ready and the Crew's Agents, files a ready issue
	// with labels, creates the Crew with rules, and waits for the issue's
	// Task to implement; it returns the Task's name.
	start := func(rules []c4v1beta1.ApprovalRule, mutate func(*c4v1beta1.Crew), labels ...string) string {
		GinkgoHelper()
		readyContainer(ns)
		crewAgents(ns, 3)
		n := gitFake.AddIssue(ns, "orders-api", "Export orders", clearIssue, append([]string{model.LabelReady}, labels...))
		crew := newCrew(ns)
		crew.Spec.Approval = rules
		if mutate != nil {
			mutate(crew)
		}
		mustCreate(crew)
		name := fmt.Sprintf("orders-api-%d", n)
		Eventually(func(g Gomega) { sandboxExists(g, ns, name+"-r1-implement") }).Should(Succeed())
		return name
	}
	// push has round's implementer push files on the Task's branch and end
	// its run, and passes the head's checks once its pull request is open.
	push := func(name string, round int, files ...string) string {
		GinkgoHelper()
		tree := scaffold.Tree{}
		for _, f := range files {
			tree[f] = scaffold.File{Data: []byte("round " + fmt.Sprint(round) + "\n")}
		}
		head, err := gitFake.PushBranch(ctx, ports.Repository{Owner: ns, Name: "orders-api"},
			gitFake.MainHead(ns, "orders-api").Hash, "task/"+name, tree, nil, "", ports.Signature{})
		Expect(err).NotTo(HaveOccurred())
		finishSandbox(ns, fmt.Sprintf("%s-r%d-implement", name, round), name,
			&agentrunner.Result{Role: "implement", Commit: head, Summary: "Exported the orders."}, "")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(task.Status.Commit).To(Equal(head))
		}).Should(Succeed())
		gitFake.SetChecks(ns, "orders-api", head, "success")
		return head
	}
	// approve has round's reviewer approve the change with result's findings.
	approve := func(name string, round int, result agentrunner.Result) {
		GinkgoHelper()
		result.Role, result.Verdict = "review", model.VerdictApprove
		finishSandbox(ns, fmt.Sprintf("%s-r%d-review-a1", name, round), name, &result, "")
	}
	// awaiting waits for the Task to wait for a person, for why.
	awaiting := func(name, why string) *c4v1beta1.Task {
		GinkgoHelper()
		var task *c4v1beta1.Task
		Eventually(func(g Gomega) {
			task = getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskAwaitingApproval))
			g.Expect(condition(task.Status.Conditions, c4v1beta1.ConditionNeedsYou)).To(And(
				Not(BeNil()), HaveField("Status", metav1.ConditionTrue),
				HaveField("Reason", model.ReasonAwaitingApproval),
				HaveField("Message", And(ContainSubstring("waits for a person because "+why),
					ContainSubstring("approve pull request "+task.Status.PullRequestURL)))))
			g.Expect(condition(task.Status.Conditions, ConditionProgressing)).To(
				HaveField("Reason", model.ReasonAwaitingApproval))
		}).Should(Succeed())
		return task
	}
	reviewerRuns := func(task *c4v1beta1.Task) []c4v1beta1.TaskAgentRun {
		var runs []c4v1beta1.TaskAgentRun
		for _, run := range task.Status.Runs {
			if run.Role == model.RoleReviewer {
				runs = append(runs, run)
			}
		}
		return runs
	}

	It("holds a change under a Person path until a person approves it, not an agent", func() {
		name := start(personForMigrations, nil)
		head := push(name, 1, "migrations/001.sql", "app.py")

		By("reviewing it as ever, then waiting for a person with the reviewer's approval on the pull request")
		approve(name, 1, agentrunner.Result{Summary: "The migration is safe.",
			FollowUps: []model.FollowUp{{Title: "Drop the old column", Body: "Once nothing reads it.",
				Criteria: []string{"orders.legacy_total is gone"}}}})
		task := awaiting(name, whyMigrations)
		Expect(task.Status.Approval).To(Equal(&c4v1beta1.TaskApproval{Require: c4v1beta1.ApprovalPerson, Rule: 1,
			Reason: whyMigrations, Commit: head, Rules: task.Status.Approval.Rules}))
		Expect(task.Status.Approval.Rules).NotTo(BeEmpty())
		Expect(task.Status.Review).To(And(HaveField("Verdict", model.VerdictApprove),
			HaveField("FollowUps", HaveLen(1))), "what the review left for the merge outlives its run")
		expectSandboxGone(ns, name+"-r1-review-a1")
		pr := gitFake.PullRequests(ns, "orders-api")[0]
		Expect(gitFake.Reviews(ns, "orders-api", pr.Number)).To(ConsistOf(And(
			HaveField("Event", ports.ReviewApprove), HaveField("Commit", head), HaveField("User", "c4admin"))))

		By("taking no place from parallel while it waits: the Crew, of one Task at a time, starts the next issue")
		next := gitFake.AddIssue(ns, "orders-api", "Export customers", clearIssue, []string{model.LabelReady})
		Eventually(func(g Gomega) {
			sandboxExists(g, ns, fmt.Sprintf("orders-api-%d-r1-implement", next))
		}).Should(Succeed())

		By("not merging on the factory's own approvals, nor a person's of an older commit")
		gitFake.AddReview(ns, "orders-api", pr.Number, fake.Review{User: ns + "-orders-api-agent",
			Event: ports.ReviewApprove, Commit: head})
		gitFake.AddReview(ns, "orders-api", pr.Number, fake.Review{User: "ada", Event: ports.ReviewApprove,
			Commit: "an-older-commit"})
		Consistently(func(g Gomega) {
			g.Expect(getTask(g, ns, name).Status.Phase).To(Equal(c4v1beta1.TaskAwaitingApproval))
			g.Expect(gitFake.PullRequests(ns, "orders-api")[0].Merged).To(BeFalse())
		}, "3s").Should(Succeed())

		By("merging once a person approved the reviewed commit, recording who, and filing the review's follow-up")
		gitFake.AddReview(ns, "orders-api", pr.Number, fake.Review{User: "ada", Event: ports.ReviewApprove,
			Commit: head, Body: "Go."})
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.Approval).To(And(HaveField("Require", c4v1beta1.ApprovalPerson),
				HaveField("ApprovedBy", "ada"), HaveField("ApprovedInChat", false)))
			g.Expect(condition(task.Status.Conditions, c4v1beta1.ConditionNeedsYou)).To(BeNil())
			g.Expect(task.Status.FollowUps).To(HaveLen(1))
			g.Expect(reviewerRuns(task)).To(HaveLen(1))
		}).Should(Succeed())
		Expect(gitFake.PullRequests(ns, "orders-api")[0].Merged).To(BeTrue())
		Expect(gitFake.Reviews(ns, "orders-api", pr.Number)).To(HaveLen(4),
			"the reviewer's approval is posted once, as the change began to wait")
		issue := gitFake.Issue(ns, "orders-api", int(task.Spec.Issue))
		Expect(issue.State).To(Equal("closed"))

		By("counting it among the merged changes a person approved (#92)")
		Eventually(func(g Gomega) {
			g.Expect(factoryMetricValue(g, managerClient, model.MetricTasksMerged, map[string]string{
				"namespace": ns, "container": "orders-api", "kind": "Implement", "code": model.CodeFactory,
				"approved": model.ApprovedPerson})).To(Equal(1.0))
		}).Should(Succeed())
		Expect(issue.Comments).To(ContainElement(And(ContainSubstring("Merged in"), ContainSubstring(
			"ada approved it, as the Crew's approval rules ask: "+whyMigrations+"."))))
	})

	It("starts the next round with the changes a person asked for, and holds that round too", func() {
		name := start(personForMigrations, nil)
		head := push(name, 1, "migrations/001.sql")
		approve(name, 1, agentrunner.Result{Summary: "Fine."})
		awaiting(name, whyMigrations)
		pr := gitFake.PullRequests(ns, "orders-api")[0]
		gitFake.AddReview(ns, "orders-api", pr.Number, fake.Review{User: "ada", Event: ports.ReviewRequestChanges,
			Commit: head, Body: "Keep the old column for one release."})
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
			g.Expect(task.Status.Feedback).To(ContainSubstring(
				"ada asked for changes on its pull request:\nKeep the old column for one release."))
			g.Expect(condition(task.Status.Conditions, c4v1beta1.ConditionNeedsYou)).To(BeNil())
			sandboxExists(g, ns, name+"-r2-implement")
		}).Should(Succeed())

		By("asking the rules again of round 2's head, which waits for a person as round 1's did")
		head2 := push(name, 2, "migrations/001.sql", "migrations/002.sql")
		approve(name, 2, agentrunner.Result{Summary: "The old column stays."})
		task := awaiting(name, whyMigrations)
		Expect(task.Status.Approval).To(And(HaveField("Commit", head2), HaveField("ApprovedBy", "")))
		Expect(gitFake.PullRequests(ns, "orders-api")[0].Merged).To(BeFalse())
	})

	It("merges a change the rules ask no review of on its checks, with no reviewer run", func() {
		name := start([]c4v1beta1.ApprovalRule{
			{Paths: []string{"migrations/"}, Require: c4v1beta1.ApprovalPerson},
			{Labels: []string{"kind/docs"}, Require: c4v1beta1.ApprovalNone},
		}, nil, "kind/docs")
		push(name, 1, "docs/export.md", "README.md")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.Approval).To(And(HaveField("Require", c4v1beta1.ApprovalNone),
				HaveField("Rule", int32(2)),
				HaveField("Reason", "approval rule 2 (labels kind/docs) asks for no review")))
			g.Expect(task.Status.Review).To(BeNil())
			g.Expect(reviewerRuns(task)).To(BeEmpty())
			g.Expect(condition(task.Status.Conditions, ConditionProgressing)).To(HaveField("Message",
				ContainSubstring("asks for no review, so it merges with no review")))
		}).Should(Succeed())
		pr := gitFake.PullRequests(ns, "orders-api")[0]
		Expect(pr.Merged).To(BeTrue())
		Expect(gitFake.Reviews(ns, "orders-api", pr.Number)).To(BeEmpty(), "no agent's approval is made up for it")
		Expect(gitFake.MainFiles(ns, "orders-api")).To(HaveKey("docs/export.md"))
		Expect(gitFake.Issue(ns, "orders-api", 1).Comments).To(ContainElement(ContainSubstring(
			"No agent reviewed it, as the Crew's approval rules ask: approval rule 2 (labels kind/docs) asks for no review.")))
		Eventually(func(g Gomega) {
			g.Expect(factoryMetricValue(g, managerClient, model.MetricTasksMerged, map[string]string{
				"namespace": ns, "container": "orders-api", "kind": "Implement", "code": model.CodeFactory,
				"approved": model.ApprovedNone})).To(Equal(1.0), "nobody approved it (#92)")
		}).Should(Succeed())
	})

	It("doesn't let a label that asks no review loosen a file a rule holds for a person", func() {
		name := start([]c4v1beta1.ApprovalRule{
			{Paths: []string{"migrations/"}, Require: c4v1beta1.ApprovalPerson},
			{Labels: []string{"kind/docs"}, Require: c4v1beta1.ApprovalNone},
		}, nil, "kind/docs")
		push(name, 1, "docs/export.md", "migrations/001.sql")
		approve(name, 1, agentrunner.Result{Summary: "Fine."})
		awaiting(name, whyMigrations)
	})

	It("reviews and merges a change no rule matches as a Crew without rules does", func() {
		name := start(personForMigrations, nil)
		push(name, 1, "app.py")
		approve(name, 1, agentrunner.Result{Summary: "Fine."})
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.Approval).To(And(HaveField("Require", c4v1beta1.ApprovalReviewer),
				HaveField("Rule", int32(0)), HaveField("Reason", "no approval rule matches app.py")))
			g.Expect(reviewerRuns(task)).To(HaveLen(1))
		}).Should(Succeed())
		Expect(gitFake.PullRequests(ns, "orders-api")[0].Merged).To(BeTrue())
	})

	// #92: the reviewer may raise what a change needs, never lower it. A
	// change the Crew's rules left to the reviewer waits for a person when the
	// reviewer that approves it calls it high-risk.
	It("waits for a person when the reviewer that approves a change calls it high-risk", func() {
		name := start(personForMigrations, nil)
		head := push(name, 1, "app.py")
		approve(name, 1, agentrunner.Result{Summary: "Right, but look at the rounding.",
			Risk: "It changes how every order's total is rounded."})
		const why = `the reviewer called the change high-risk ("It changes how every order's total is rounded")`
		task := awaiting(name, why)
		Expect(task.Status.Approval).To(Equal(&c4v1beta1.TaskApproval{Require: c4v1beta1.ApprovalPerson,
			Reason: why, Commit: head, Rules: task.Status.Approval.Rules, RaisedByReviewer: true}))

		By("keeping the reviewer's word when the Crew's rules change and still leave the change to the reviewer")
		crew := getCrew(Default, ns)
		Expect(patch(crew, func() {
			crew.Spec.Approval = append(crew.Spec.Approval,
				c4v1beta1.ApprovalRule{Paths: []string{"docs/"}, Require: c4v1beta1.ApprovalNone})
		})).To(Succeed())
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskAwaitingApproval))
			g.Expect(task.Status.Approval).To(And(HaveField("Require", c4v1beta1.ApprovalPerson),
				HaveField("RaisedByReviewer", true), HaveField("Reason", why),
				HaveField("Rules", Not(Equal(model.ApprovalRulesKey(approvalRules(&c4v1beta1.Crew{
					Spec: c4v1beta1.CrewSpec{Approval: personForMigrations}})))))))
		}).Should(Succeed())

		By("merging once a person approved, and saying on the issue whose word held it")
		pr := gitFake.PullRequests(ns, "orders-api")[0]
		gitFake.AddReview(ns, "orders-api", pr.Number, fake.Review{User: "ada", Event: ports.ReviewApprove, Commit: head})
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.Approval).To(And(HaveField("ApprovedBy", "ada"), HaveField("RaisedByReviewer", true)))
		}).Should(Succeed())
		Expect(gitFake.Issue(ns, "orders-api", int(task.Spec.Issue)).Comments).To(ContainElement(
			ContainSubstring("ada approved it, because " + why + ".")))
	})

	It("holds what the reviewer raised through the verification", func() {
		verifierAgent(ns)
		name := start(personForMigrations, func(crew *c4v1beta1.Crew) {
			crew.Spec.Verifier = &c4v1beta1.LocalRef{Name: "verifier"}
		})
		head := push(name, 1, "app.py")
		approve(name, 1, agentrunner.Result{Summary: "Right.", Risk: "It deletes the archived orders"})
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, name).Status.Phase).To(Equal(c4v1beta1.TaskVerifying))
			sandboxExists(g, ns, name+"-r1-verify-a1")
		}).Should(Succeed())
		finishSandbox(ns, name+"-r1-verify-a1", name, &agentrunner.Result{Role: "verify", Commit: head,
			Verdict: model.VerdictApprove, Summary: "The archive is gone and the rest is whole."}, "")
		awaiting(name, `the reviewer called the change high-risk ("It deletes the archived orders")`)
	})

	It("lets no reviewer's word lower a rule, nor put a person in the loop of a Crew without rules", func() {
		By("a change its rule holds for a person keeps the rule's reason")
		name := start(personForMigrations, nil)
		push(name, 1, "migrations/001.sql")
		approve(name, 1, agentrunner.Result{Summary: "Safe.", Risk: "It is a migration."})
		task := awaiting(name, whyMigrations)
		Expect(task.Status.Approval).To(And(HaveField("Rule", int32(1)), HaveField("RaisedByReviewer", false)))

		By("a Crew without rules merges on its reviewer's approval, whatever the reviewer calls the change")
		crew := getCrew(Default, ns)
		Expect(patch(crew, func() { crew.Spec.Approval = nil })).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, name).Status.Phase).To(Equal(c4v1beta1.TaskMerged))
		}).Should(Succeed())
		next := gitFake.AddIssue(ns, "orders-api", "Export customers", clearIssue, []string{model.LabelReady})
		other := fmt.Sprintf("orders-api-%d", next)
		Eventually(func(g Gomega) { sandboxExists(g, ns, other+"-r1-implement") }).Should(Succeed())
		push(other, 1, "customers.py")
		approve(other, 1, agentrunner.Result{Summary: "Fine.", Risk: "It reads every customer."})
		Eventually(func(g Gomega) {
			task := getTask(g, ns, other)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.Approval).To(BeNil())
		}).Should(Succeed())
	})

	It("holds a big change by its size, and lets it go when the Crew's rule is taken away", func() {
		name := start([]c4v1beta1.ApprovalRule{{ChangedLinesOver: 400, Require: c4v1beta1.ApprovalPerson}}, nil)
		tree := scaffold.Tree{"app.py": {Data: []byte("export\n")}}
		head, err := gitFake.PushBranch(ctx, ports.Repository{Owner: ns, Name: "orders-api"},
			gitFake.MainHead(ns, "orders-api").Hash, "task/"+name, tree, nil, "", ports.Signature{})
		Expect(err).NotTo(HaveOccurred())
		finishSandbox(ns, name+"-r1-implement", name,
			&agentrunner.Result{Role: "implement", Commit: head, Summary: "Exported the orders."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, name).Status.Phase).To(Equal(c4v1beta1.TaskChecking))
		}).Should(Succeed())
		pr := gitFake.PullRequests(ns, "orders-api")[0]
		gitFake.SetPullSize(ns, "orders-api", pr.Number, 350, 51, 1)
		gitFake.SetChecks(ns, "orders-api", head, "success")
		approve(name, 1, agentrunner.Result{Summary: "Long, but right."})
		awaiting(name, "approval rule 1 (more than 400 changed lines) asks for a person")

		By("merging on the agents' approval once no rule asks for a person any more")
		crew := getCrew(Default, ns)
		Expect(patch(crew, func() { crew.Spec.Approval = nil })).To(Succeed())
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.Approval).To(BeNil())
			g.Expect(condition(task.Status.Conditions, ConditionProgressing)).To(HaveField("Message",
				ContainSubstring("The Crew's approval rules no longer ask for a person; merging")))
		}).Should(Succeed())
		Expect(gitFake.PullRequests(ns, "orders-api")[0].Merged).To(BeTrue())
	})

	// theos-chat on chatFake, as its own ServiceAccount, beside the operator
	// (#92): a waiting change is a thread in reviews, as a waiting spec is
	// one in specs.
	It("puts a waiting change up in chat, once, and merges it when a person approves it there, as them", func() {
		name := start(personForMigrations, nil)
		head := push(name, 1, "migrations/001.sql")
		approve(name, 1, agentrunner.Result{Summary: "The migration is safe."})
		awaiting(name, whyMigrations)

		By("making the project's Chat, with one person who may write Tasks")
		bot := chatBot(ns)
		adaID, _ := chatFake.NewUser("ada-" + ns)
		malloryID, _ := chatFake.NewUser("mallory-" + ns)
		ada := "ada-" + ns + "@example.com"
		grant(ada, roleChatPeople, roleTaskWord)
		chat := newChat(ns, "chat", "matrix-bot", c4v1beta1.ChatPerson{MatrixID: adaID, User: ada})
		Expect(asUser(ada).Create(ctx, chat)).To(Succeed())
		chatReady(chat, metav1.ConditionTrue, model.ChatReady)
		reviews := chatRoom(chat, model.ChatReviews)

		By("posting one thread in reviews for the change, with the rule that holds it")
		theosChat := newTheosChat()
		chatPass(theosChat, chat)
		var root string
		for id, body := range botPosts(reviews, bot) {
			if strings.Contains(body, "Change for issue #1") {
				root = id
				Expect(body).To(And(ContainSubstring("waits for your approval of commit "+head[:7]),
					ContainSubstring(whyMigrations), ContainSubstring("say approve in this thread")))
			}
		}
		Expect(root).NotTo(BeEmpty(), "no thread for the waiting change among %v", botPosts(reviews, bot))
		posted := len(botPosts(reviews, bot))
		chatPass(theosChat, chat)
		chatPass(newTheosChat(), chat)
		Expect(botPosts(reviews, bot)).To(HaveLen(posted), "nothing more on the next pass, nor after a restart")

		By("refusing a stranger's approve, which changes nothing")
		chatFake.Post(reviews, malloryID, "approve", root)
		chatPass(theosChat, chat)
		Expect(related(reviews, bot, "m.thread", root)).To(ConsistOf(ContainSubstring(
			malloryID + " is not one of Chat chat's people, so nothing was done")))
		task := &c4v1beta1.Task{}
		Expect(k8sClient.Get(ctx, key(ns, name), task)).To(Succeed())
		Expect(task.Annotations).NotTo(HaveKey(model.ChatAnnotationApproveSpec))

		By("writing a listed person's approve as that person, and the operator merging the change")
		chatFake.Post(reviews, adaID, "approve", root)
		chatPass(theosChat, chat)
		Expect(related(reviews, bot, "m.thread", root)).To(ContainElement(
			"Done as " + ada + ": approved the change at " + head[:7] + "; the operator merges it."))
		Expect(k8sClient.Get(ctx, key(ns, name), task)).To(Succeed())
		Expect(task.Annotations).To(And(HaveKeyWithValue(model.ChatAnnotationApproveSpec, head),
			HaveKeyWithValue(model.ChatAnnotationBy(model.ChatAnnotationApproveSpec), ada)))
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.Approval).To(And(HaveField("ApprovedBy", ada), HaveField("ApprovedInChat", true)))
		}).Should(Succeed())
		Expect(gitFake.PullRequests(ns, "orders-api")[0].Merged).To(BeTrue())
		Expect(gitFake.Issue(ns, "orders-api", 1).Comments).To(ContainElement(ContainSubstring(
			ada + " approved it in chat, as the Crew's approval rules ask")))

		By("editing the thread's root as the change merged, and marking it done")
		chatPass(theosChat, chat)
		Expect(related(reviews, bot, "m.replace", root)).To(ContainElement(ContainSubstring(
			"Merged: " + ada + " approved it in chat.")))
		Expect(related(reviews, bot, "m.annotation", root)).To(ConsistOf(model.ChatDoneReaction))
	})

	It("starts the next round with the changes a person asked for in the change's thread", func() {
		name := start(personForMigrations, nil)
		head := push(name, 1, "migrations/001.sql")
		approve(name, 1, agentrunner.Result{Summary: "Fine."})
		awaiting(name, whyMigrations)
		bot := chatBot(ns)
		adaID, _ := chatFake.NewUser("ada-" + ns)
		ada := "ada-" + ns + "@example.com"
		grant(ada, roleChatPeople, roleTaskWord)
		chat := newChat(ns, "chat", "matrix-bot", c4v1beta1.ChatPerson{MatrixID: adaID, User: ada})
		Expect(asUser(ada).Create(ctx, chat)).To(Succeed())
		chatReady(chat, metav1.ConditionTrue, model.ChatReady)
		reviews := chatRoom(chat, model.ChatReviews)
		theosChat := newTheosChat()
		chatPass(theosChat, chat)
		var root string
		for id, body := range botPosts(reviews, bot) {
			if strings.Contains(body, "Change for issue #1") {
				root = id
			}
		}
		Expect(root).NotTo(BeEmpty())

		chatFake.Post(reviews, adaID, "changes: Keep the old column for one release.", root)
		chatPass(theosChat, chat)
		Expect(related(reviews, bot, "m.thread", root)).To(ContainElement(
			"Done as " + ada + ": asked for changes at " + head[:7] + "; the next round takes them."))
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Feedback).To(ContainSubstring(
				ada + " asked for changes in chat:\nKeep the old column for one release."))
		}).Should(Succeed())
		Expect(gitFake.PullRequests(ns, "orders-api")[0].Merged).To(BeFalse())
	})

	It("waits for a person after the verifier, with both agents' approvals on the pull request", func() {
		verifierAgent(ns)
		name := start(personForMigrations, func(crew *c4v1beta1.Crew) {
			crew.Spec.Verifier = &c4v1beta1.LocalRef{Name: "verifier"}
		})
		head := push(name, 1, "migrations/001.sql")
		approve(name, 1, agentrunner.Result{Summary: "The migration is safe."})
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, name).Status.Phase).To(Equal(c4v1beta1.TaskVerifying))
			sandboxExists(g, ns, name+"-r1-verify-a1")
		}).Should(Succeed())
		finishSandbox(ns, name+"-r1-verify-a1", name, &agentrunner.Result{Role: "verify", Commit: head,
			Verdict: model.VerdictApprove, Summary: "The migration ran; orders kept their totals."}, "")
		awaiting(name, whyMigrations)
		pr := gitFake.PullRequests(ns, "orders-api")[0]
		Expect(gitFake.Reviews(ns, "orders-api", pr.Number)).To(ConsistOf(
			And(HaveField("Event", ports.ReviewApprove), HaveField("Body", ContainSubstring("Review by Agent `reviewer`"))),
			And(HaveField("Event", ports.ReviewApprove),
				HaveField("Body", ContainSubstring("Verification by Agent `verifier`")))))

		gitFake.AddReview(ns, "orders-api", pr.Number, fake.Review{User: "ada", Event: ports.ReviewApprove, Commit: head})
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.Approval).To(HaveField("ApprovedBy", "ada"))
		}).Should(Succeed())
		Expect(gitFake.Reviews(ns, "orders-api", pr.Number)).To(HaveLen(3),
			"each agent's approval is posted once, as the change began to wait")
	})
})
