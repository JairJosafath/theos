/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// newSpecTask is the spec of issue 4 of orders-api.
func newSpecTask(ns, name string) *c4v1beta1.Task {
	return &c4v1beta1.Task{ObjectMeta: objectMeta(ns, name), Spec: c4v1beta1.TaskSpec{
		Kind:         c4v1beta1.TaskKindSpec,
		ContainerRef: c4v1beta1.LocalRef{Name: "orders-api"},
		AgentRef:     c4v1beta1.LocalRef{Name: "writer"},
		ReviewerRef:  &c4v1beta1.LocalRef{Name: "reviewer"},
		Issue:        4,
	}}
}

// specCrew makes orders-api Ready, its Crew's Agents and the spec writer, and
// returns the Crew, with the spec writer named and approval as its
// specApproval, for the test to create.
func specCrew(ns string, approval c4v1beta1.SpecApproval) *c4v1beta1.Crew {
	GinkgoHelper()
	readyContainer(ns)
	crewAgents(ns, 3)
	mustCreate(&c4v1beta1.Agent{ObjectMeta: objectMeta(ns, "writer"),
		Spec: c4v1beta1.AgentSpec{Image: "theos/agent-go:dev", Model: "claude-opus-5-5", MaxRounds: 3}})
	crew := newCrew(ns)
	crew.Spec.SpecWriter = &c4v1beta1.LocalRef{Name: "writer"}
	crew.Spec.SpecApproval = approval
	return crew
}

// testSpec is a spec with two acceptance criteria.
const testSpec = "# Export orders\n\n## Problem\n\nOrders are copied by hand.\n\n## Approach\n\nA CSV endpoint.\n\n" +
	"## Acceptance criteria\n\n- [ ] GET /orders.csv lists every order\n- [ ] A test covers an empty shop\n"

// A Spec Task's CEL rules (docs/plans/done/sdlc-loop.md, step 3).
var _ = Describe("Spec Task", func() {
	It("needs an issue and has no allowance, and a Crew's specApproval needs a spec writer", func() {
		ns := newProjectNamespace()
		mustCreate(newSpecTask(ns, "ok"))

		noIssue := newSpecTask(ns, "no-issue")
		noIssue.Spec.Issue, noIssue.Spec.Goal = 0, "Write a spec"
		Expect(k8sClient.Create(ctx, noIssue)).To(MatchError(ContainSubstring(
			"a Spec Task needs an issue and has no allowedProtectedPaths")))

		allowed := newSpecTask(ns, "allowed")
		allowed.Spec.AllowedProtectedPaths = []string{"Makefile"}
		Expect(k8sClient.Create(ctx, allowed)).To(MatchError(ContainSubstring(
			"a Spec Task needs an issue and has no allowedProtectedPaths")))

		crew := newCrew(ns)
		crew.Spec.SpecApproval = c4v1beta1.SpecApprovalReviewer
		Expect(k8sClient.Create(ctx, crew)).To(MatchError(ContainSubstring("specApproval needs specWriter")))
	})
})

// A Crew's specs (docs/plans/done/sdlc-loop.md, step 3): a Spec Task for an issue
// labelled status/needs-spec writes docs/specs/<issue>.md in a pull request,
// which merges after CI, the reviewer and its review point; the issue then
// gets the spec's criteria and status/ready, and stays open.
var _ = Describe("Crew spec", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	// readyOrTaken is a ready issue's labels: status/ready, or
	// status/in-progress once the Crew took it up, which it may do at once.
	readyOrTaken := BeElementOf([]string{model.LabelReady}, []string{model.LabelInProgress})

	// specOne files an issue that needs a spec, makes the Crew with approval,
	// and waits for the issue's Spec Task to run its writer; it returns the
	// issue and the Task's name.
	specOne := func(approval c4v1beta1.SpecApproval) (int, string) {
		GinkgoHelper()
		crew := specCrew(ns, approval)
		n := gitFake.AddIssue(ns, "orders-api", "Export orders", "Orders as CSV.", []string{model.LabelNeedsSpec})
		Expect(n).To(Equal(1), "the specs below are docs/specs/1.md")
		mustCreate(crew)
		name := fmt.Sprintf("orders-api-%d-spec", n)
		Eventually(func(g Gomega) { sandboxExists(g, ns, name+"-r1-implement") }).Should(Succeed())
		return n, name
	}
	// pushSpec pushes files on the Spec Task's branch, as its writer would,
	// and ends its implement run of round with the head.
	pushSpec := func(name string, round int, files scaffold.Tree) string {
		GinkgoHelper()
		head, err := gitFake.PushBranch(ctx, ports.Repository{Owner: ns, Name: "orders-api"},
			gitFake.MainHead(ns, "orders-api").Hash, "task/"+name, files, nil, "", ports.Signature{})
		Expect(err).NotTo(HaveOccurred())
		finishSandbox(ns, fmt.Sprintf("%s-r%d-implement", name, round), name,
			&agentrunner.Result{Role: "implement", Commit: head, Summary: "Wrote the spec."}, "")
		return head
	}
	// reviewed passes the spec's checks and has the reviewer approve it.
	reviewed := func(name, head string) {
		GinkgoHelper()
		gitFake.SetChecks(ns, "orders-api", head, "success")
		Eventually(func(g Gomega) {
			g.Expect(sandboxEnv(g, ns, name+"-r1-review-a1")).To(ContainElement(
				map[string]any{"name": "THEOS_SPEC_PATH", "value": "docs/specs/1.md"}))
		}).Should(Succeed())
		finishSandbox(ns, name+"-r1-review-a1", name,
			&agentrunner.Result{Role: "review", Verdict: "approve", Summary: "A clear plan."}, "")
	}
	// awaiting waits for the Spec Task to wait for a person.
	awaiting := func(name string) {
		GinkgoHelper()
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskAwaitingApproval))
			g.Expect(condition(task.Status.Conditions, c4v1beta1.ConditionNeedsYou)).To(And(
				Not(BeNil()), HaveField("Status", metav1.ConditionTrue),
				HaveField("Reason", model.ReasonAwaitingApproval),
				HaveField("Message", ContainSubstring("approve pull request "+task.Status.PullRequestURL))))
			g.Expect(condition(task.Status.Conditions, ConditionProgressing)).To(
				HaveField("Reason", model.ReasonAwaitingApproval))
		}).Should(Succeed())
	}
	// readyFromSpec checks the issue once its spec merged: open, ready (or
	// taken up already), the spec's criteria and a link to it in its body.
	readyFromSpec := func(g Gomega, n int) {
		issue := gitFake.Issue(ns, "orders-api", n)
		g.Expect(issue.State).To(Equal("open"), "the issue stays open to be built")
		g.Expect(issue.Labels).To(readyOrTaken)
		g.Expect(issue.Body).To(Equal("Orders as CSV.\n\n## Acceptance criteria\n\nFrom the spec " +
			"[docs/specs/1.md](http://gitea.gitea.svc.cluster.local:3000/" + ns +
			"/orders-api/src/branch/main/docs/specs/1.md).\n\n" +
			"- [ ] GET /orders.csv lists every order\n- [ ] A test covers an empty shop\n"))
		g.Expect(issue.Comments).To(ContainElement(ContainSubstring("Its 2 acceptance criteria are now in the description")))
		g.Expect(gitFake.MainFiles(ns, "orders-api")).To(HaveKey("docs/specs/1.md"))
	}

	It("starts no Spec Task for a Crew without a spec writer", func() {
		readyContainer(ns)
		crewAgents(ns, 2)
		n := gitFake.AddIssue(ns, "orders-api", "Export orders", "Orders as CSV.", []string{model.LabelNeedsSpec})
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
				g.Expect(t.Namespace).NotTo(Equal(ns), "Task %s started", t.Name)
			}
			g.Expect(gitFake.Issue(ns, "orders-api", n).Labels).To(Equal([]string{model.LabelNeedsSpec}))
		}, "3s").Should(Succeed())
	})

	It("writes a spec, merges it only once a person approved it, and makes the issue ready", func() {
		n, name := specOne("")

		By("running the spec writer on the issue, with the spec's path, and marking the issue taken")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Spec.Kind).To(Equal(c4v1beta1.TaskKindSpec))
			g.Expect(task.Spec.AgentRef.Name).To(Equal("writer"))
			g.Expect(task.Spec.ReviewerRef).To(Equal(&c4v1beta1.LocalRef{Name: "reviewer"}))
			g.Expect(sandboxEnv(g, ns, name+"-r1-implement")).To(ContainElements(
				map[string]any{"name": "THEOS_ROLE", "value": "implement"},
				map[string]any{"name": "THEOS_SPEC_PATH", "value": "docs/specs/1.md"},
				map[string]any{"name": "THEOS_ISSUE", "value": fmt.Sprint(n)},
			))
			issue := gitFake.Issue(ns, "orders-api", n)
			g.Expect(issue.Labels).To(Equal([]string{model.LabelInProgress}))
			g.Expect(issue.Comments).To(ContainElement(ContainSubstring(
				"Task `" + name + "` picked this up to write its spec, `docs/specs/1.md`")))
		}).Should(Succeed())

		By("opening the spec's pull request, which leaves the issue open, and labelling the issue spec-review")
		head := pushSpec(name, 1, scaffold.Tree{"docs/specs/1.md": {Data: []byte(testSpec)}})
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, name).Status.Phase).To(Equal(c4v1beta1.TaskChecking))
			g.Expect(gitFake.Issue(ns, "orders-api", n).Labels).To(Equal([]string{model.LabelSpecReview}))
		}).Should(Succeed())
		pr := gitFake.PullRequests(ns, "orders-api")[0]
		title, body := gitFake.PullText(ns, "orders-api", pr.Number)
		Expect(title).To(Equal("Spec: Export orders"))
		Expect(body).To(ContainSubstring("The spec of issue #1, in `docs/specs/1.md`"))
		Expect(body).NotTo(ContainSubstring("Closes"), "the spec's merge must not close its issue")

		By("waiting for a person once CI and the reviewer passed it, the reviewer's approval on the pull request")
		reviewed(name, head)
		awaiting(name)
		Expect(gitFake.Reviews(ns, "orders-api", pr.Number)).To(ConsistOf(And(
			HaveField("Event", ports.ReviewApprove), HaveField("Commit", head), HaveField("User", "c4admin"))))

		By("not merging on the factory's own approvals, nor a person's of an older commit")
		gitFake.AddReview(ns, "orders-api", pr.Number, fake.Review{User: ns + "-orders-api-agent",
			Event: ports.ReviewApprove, Commit: head})
		gitFake.AddReview(ns, "orders-api", pr.Number, fake.Review{User: "ada", Event: ports.ReviewApprove,
			Commit: "an-older-commit"})
		Consistently(func(g Gomega) {
			g.Expect(getTask(g, ns, name).Status.Phase).To(Equal(c4v1beta1.TaskAwaitingApproval))
			g.Expect(gitFake.PullRequests(ns, "orders-api")[0].Merged).To(BeFalse())
			g.Expect(gitFake.Issue(ns, "orders-api", n).Labels).To(Equal([]string{model.LabelSpecReview}))
		}, "3s").Should(Succeed())

		By("merging once a person approved it, copying its criteria into the issue and labelling it ready")
		gitFake.AddReview(ns, "orders-api", pr.Number, fake.Review{User: "ada", Event: ports.ReviewApprove,
			Commit: head, Body: "Agreed."})
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.Spec).To(Equal(&c4v1beta1.TaskSpecResult{Path: "docs/specs/1.md",
				Approval: c4v1beta1.SpecApprovalPerson, ApprovedBy: "ada",
				Criteria: []string{"GET /orders.csv lists every order", "A test covers an empty shop"},
				Applied:  "Wrote 2 acceptance criteria from docs/specs/1.md into the description and labelled status/ready"}))
			g.Expect(condition(task.Status.Conditions, c4v1beta1.ConditionNeedsYou)).To(BeNil())
			g.Expect(gitFake.PullRequests(ns, "orders-api")[0].Merged).To(BeTrue())
			readyFromSpec(g, n)
		}).Should(Succeed())
		Expect(gitFake.Reviews(ns, "orders-api", pr.Number)).To(HaveLen(4),
			"the reviewer's approval is posted once, as the spec began to wait")

		By("building the issue from its criteria: the spec was no try at it")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, fmt.Sprintf("orders-api-%d", n)).Spec.Kind).To(Equal(c4v1beta1.TaskImplement))
		}).Should(Succeed())
	})

	It("merges a spec a person approved from chat, and counts no approval of another commit", func() {
		n, name := specOne("")
		head := pushSpec(name, 1, scaffold.Tree{"docs/specs/1.md": {Data: []byte(testSpec)}})
		reviewed(name, head)
		awaiting(name)
		ada := "ada-" + ns + "@example.com"
		grant(ada, roleTaskWord)
		task := &c4v1beta1.Task{}
		approve := model.ChatAnnotationApproveSpec

		By("leaving an approval of an older commit out, said in the Task's condition")
		Expect(k8sClient.Get(ctx, key(ns, name), task)).To(Succeed())
		Expect(patchAs(asUser(ada), task, chatWord(task, approve, "an-older-commit", ada))).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(condition(getTask(g, ns, name).Status.Conditions, ConditionProgressing)).To(HaveField("Message",
				ContainSubstring(ada+"'s approval from chat is of commit an-olde, not the head "+head[:7]+
					", so it doesn't count")))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(getTask(g, ns, name).Status.Phase).To(Equal(c4v1beta1.TaskAwaitingApproval))
			g.Expect(gitFake.PullRequests(ns, "orders-api")[0].Merged).To(BeFalse())
		}, "2s").Should(Succeed())

		By("merging once the person approved the head from chat, recording who")
		Expect(k8sClient.Get(ctx, key(ns, name), task)).To(Succeed())
		Expect(patchAs(asUser(ada), task, chatWord(task, approve, head, ada))).To(Succeed())
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.Spec).To(And(HaveField("ApprovedBy", ada), HaveField("ApprovedInChat", true)))
			g.Expect(gitFake.PullRequests(ns, "orders-api")[0].Merged).To(BeTrue())
			readyFromSpec(g, n)
		}).Should(Succeed())
	})

	It("starts the next round with the changes a person asked from chat", func() {
		_, name := specOne("")
		head := pushSpec(name, 1, scaffold.Tree{"docs/specs/1.md": {Data: []byte(testSpec)}})
		reviewed(name, head)
		awaiting(name)
		ada := "ada-" + ns + "@example.com"
		grant(ada, roleTaskWord)
		task := &c4v1beta1.Task{}
		Expect(k8sClient.Get(ctx, key(ns, name), task)).To(Succeed())
		Expect(patchAs(asUser(ada), task, chatWord(task, model.ChatAnnotationSpecChanges, head+" Name the tokens.",
			ada))).To(Succeed())
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
			g.Expect(task.Status.Feedback).To(ContainSubstring(ada + " asked for changes to the spec in chat:\n" +
				"Name the tokens."))
			sandboxExists(g, ns, name+"-r2-implement")
		}).Should(Succeed())
		Expect(gitFake.PullRequests(ns, "orders-api")[0].Merged).To(BeFalse())
	})

	// theos-chat (docs/plans/chat-matrix.md, step 2) on chatFake, as its own
	// ServiceAccount, beside the operator.
	It("puts a waiting spec up in chat, once, and merges it when a person approves it there, as them", func() {
		n, name := specOne("")
		head := pushSpec(name, 1, scaffold.Tree{"docs/specs/1.md": {Data: []byte(testSpec)}})
		reviewed(name, head)
		awaiting(name)

		By("making the project's Chat, each of its people adding themselves")
		bot := chatBot(ns)
		adaID, _ := chatFake.NewUser("ada-" + ns)
		carolID, _ := chatFake.NewUser("carol-" + ns)
		malloryID, _ := chatFake.NewUser("mallory-" + ns)
		// Only ada may write Tasks: the -by the policy checks, and this RBAC,
		// say the write was hers.
		ada, carol := "ada-"+ns+"@example.com", "carol-"+ns+"@example.com"
		grant(ada, roleChatPeople, roleTaskWord)
		grant(carol, roleChatPeople)
		chat := newChat(ns, "chat", "matrix-bot", c4v1beta1.ChatPerson{MatrixID: adaID, User: ada})
		Expect(asUser(ada).Create(ctx, chat)).To(Succeed())
		Expect(patchAs(asUser(carol), chat, func() {
			chat.Spec.People = append(chat.Spec.People, c4v1beta1.ChatPerson{MatrixID: carolID, User: carol})
		})).To(Succeed())
		chatReady(chat, metav1.ConditionTrue, model.ChatReady)
		specs := chatRoom(chat, model.ChatSpecs)

		By("posting one thread in specs for the spec waiting for a person")
		chatPass(newTheosChat(), chat)
		posts := botPosts(specs, bot)
		Expect(posts).To(HaveLen(1))
		var root string
		for id, body := range posts {
			root = id
			Expect(body).To(And(ContainSubstring("Spec of issue #1"),
				ContainSubstring("Waits for your approval of commit "+head[:7]),
				ContainSubstring("say approve in this thread")))
		}

		By("posting nothing more on the next pass, nor after a restart")
		theosChat := newTheosChat()
		chatPass(theosChat, chat)
		chatPass(newTheosChat(), chat)
		Expect(botPosts(specs, bot)).To(HaveLen(1))
		Expect(related(specs, bot, "m.replace", root)).To(BeEmpty())

		By("refusing a stranger's approve, which changes nothing")
		chatFake.Post(specs, malloryID, "approve", root)
		chatPass(newTheosChat(), chat)
		Expect(related(specs, bot, "m.thread", root)).To(ConsistOf(ContainSubstring(
			malloryID + " is not one of Chat chat's people, so nothing was done")))
		task := &c4v1beta1.Task{}
		Expect(k8sClient.Get(ctx, key(ns, name), task)).To(Succeed())
		Expect(task.Annotations).NotTo(HaveKey(model.ChatAnnotationApproveSpec))

		By("posting the API server's refusal of a person whose RBAC forbids it")
		chatFake.Post(specs, carolID, "approve", root)
		chatPass(theosChat, chat)
		Expect(related(specs, bot, "m.thread", root)).To(ContainElement(And(
			ContainSubstring("The cluster refused it for "+carol+" (403)"),
			ContainSubstring(`User "`+carol+`" cannot patch resource "tasks"`))))
		Expect(k8sClient.Get(ctx, key(ns, name), task)).To(Succeed())
		Expect(task.Annotations).NotTo(HaveKey(model.ChatAnnotationApproveSpec))

		By("writing a listed person's approve as that person, and the operator merging the spec")
		chatFake.Post(specs, adaID, "approve", root)
		chatPass(theosChat, chat)
		Expect(related(specs, bot, "m.thread", root)).To(ContainElement(
			"Done as " + ada + ": approved the spec at " + head[:7] + "; the operator merges it."))
		Expect(k8sClient.Get(ctx, key(ns, name), task)).To(Succeed())
		Expect(task.Annotations).To(And(HaveKeyWithValue(model.ChatAnnotationApproveSpec, head),
			HaveKeyWithValue(model.ChatAnnotationBy(model.ChatAnnotationApproveSpec), ada)))
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.Spec).To(And(HaveField("ApprovedBy", ada), HaveField("ApprovedInChat", true)))
			readyFromSpec(g, n)
		}).Should(Succeed())

		By("editing the thread's root as the spec merged, and marking it done")
		chatPass(theosChat, chat)
		Expect(related(specs, bot, "m.replace", root)).To(ConsistOf(ContainSubstring(
			"Merged: " + ada + " approved it in chat.")))
		Expect(related(specs, bot, "m.annotation", root)).To(Equal([]string{model.ChatDoneReaction}))
		chatPass(newTheosChat(), chat)
		Expect(related(specs, bot, "m.replace", root)).To(HaveLen(1))
		Expect(related(specs, bot, "m.annotation", root)).To(HaveLen(1))
		Expect(botPosts(specs, bot)).To(HaveLen(1))
	})

	It("merges on the reviewer's approval alone with specApproval Reviewer", func() {
		n, name := specOne(c4v1beta1.SpecApprovalReviewer)
		head := pushSpec(name, 1, scaffold.Tree{"docs/specs/1.md": {Data: []byte(testSpec)}})
		reviewed(name, head)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(task.Status.Spec).To(And(HaveField("Approval", c4v1beta1.SpecApprovalReviewer),
				HaveField("ApprovedBy", "")))
			for _, h := range task.Status.History {
				g.Expect(h.Phase).NotTo(Equal(c4v1beta1.TaskAwaitingApproval), "nobody is waited for")
			}
			readyFromSpec(g, n)
		}).Should(Succeed())
	})

	It("hands the issue back when a person closes the spec's pull request", func() {
		n, name := specOne("")
		head := pushSpec(name, 1, scaffold.Tree{"docs/specs/1.md": {Data: []byte(testSpec)}})
		reviewed(name, head)
		awaiting(name)
		gitFake.ClosePull(ns, "orders-api", gitFake.PullRequests(ns, "orders-api")[0].Number)
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskFailed))
			g.Expect(condition(task.Status.Conditions, ConditionSucceeded)).To(And(
				HaveField("Reason", model.ReasonSpecDeclined), HaveField("Message", ContainSubstring("spec was declined"))))
			g.Expect(condition(task.Status.Conditions, c4v1beta1.ConditionNeedsYou)).To(BeNil())
			issue := gitFake.Issue(ns, "orders-api", n)
			g.Expect(issue.State).To(Equal("open"))
			g.Expect(issue.Labels).To(Equal([]string{model.LabelNeedsHuman}))
			g.Expect(issue.Comments).To(ContainElement(And(ContainSubstring("spec was declined"),
				ContainSubstring("label it "+model.LabelNeedsSpec))))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			err := k8sClient.Get(ctx, key(ns, name+"-2"), &c4v1beta1.Task{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "a declined spec waits for a person")
		}, "2s").Should(Succeed())
	})

	It("refuses a spec round that changes files beside its spec file", func() {
		_, name := specOne("")
		head := pushSpec(name, 1, scaffold.Tree{"docs/specs/1.md": {Data: []byte(testSpec)},
			"app/export.py": {Data: []byte("print('csv')\n")}})
		gitFake.SetChecks(ns, "orders-api", head, "success")
		Eventually(func(g Gomega) {
			task := getTask(g, ns, name)
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskImplementing))
			g.Expect(task.Status.Feedback).To(And(ContainSubstring("the change touches app/export.py"),
				ContainSubstring("a spec changes only docs/specs/1.md")))
			sandboxExists(g, ns, name+"-r2-implement")
		}).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, name+"-r1-review-a1"), newSandbox()))).To(BeTrue(),
			"the refused round is never reviewed")
		Expect(gitFake.PullComments(ns, "orders-api", 1)).To(ContainElement(ContainSubstring("Round 1 was not accepted")))
	})
})
