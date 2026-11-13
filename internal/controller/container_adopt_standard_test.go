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
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// The Standard python-strict's files as it renders them for orders-api.
const (
	strictGuide    = "Rules for orders-api, checked by python-strict\n"
	strictMakefile = "check:\n\ttrue\n"
)

// ownRepository is a repository of a person's own that project ns may adopt
// as orders-api: the identity, and none of the Standard's files as it
// renders them.
func ownRepository(ns string) scaffold.Tree {
	GinkgoHelper()
	return scaffold.Tree{
		scaffold.IdentityPath: bareIdentity(ns),
		"main.py":             {Data: []byte("print('orders')\n")},
		"Makefile":            {Data: []byte("check:\n\tpytest\n")},
	}
}

// adoptWithStandard creates the webshop with orders-api adopting its
// repository under the Standard python-strict, which protects protected.
func adoptWithStandard(ns string, protected ...string) {
	GinkgoHelper()
	standard := newStandard(ns, "python-strict", "python-fastapi")
	standard.Spec.ProtectedPaths = protected
	mustCreate(standard)
	createWebshopWith(ns, func(c *c4v1beta1.Container) {
		c.Spec.StandardRef = &c4v1beta1.LocalRef{Name: "python-strict"}
		c.Spec.Adopt = true
	})
}

// openedAdoption waits for orders-api to be Ready with the pull request that
// brings its Standard's files open, and returns it.
func openedAdoption(ns string) c4v1beta1.StandardAdoptionStatus {
	GinkgoHelper()
	return openedAdoptionOf(ns, "python-strict")
}

// openedAdoptionOf is openedAdoption for the Standard name.
func openedAdoptionOf(ns, name string) c4v1beta1.StandardAdoptionStatus {
	GinkgoHelper()
	var adoption c4v1beta1.StandardAdoptionStatus
	Eventually(func(g Gomega) {
		container := getContainer(g, ns)
		g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
		g.Expect(container.Status.Standard).To(BeNil(), "the Standard is recorded when its files are on main")
		g.Expect(container.Status.StandardAdoption).NotTo(BeNil())
		adoption = *container.Status.StandardAdoption
		g.Expect(adoption.Name).To(Equal(name))
		g.Expect(adoption.PullRequest).NotTo(BeZero())
		g.Expect(adoption.Stopped).To(BeEmpty())
		cond := standardCondition(g, container)
		g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonPullRequestOpen))
		g.Expect(cond.Message).To(Equal("Pull request " + adoption.PullRequestURL + " brings the files of " +
			name + " at v1.0.0 into the repository; a person merges it, or closes it to leave the repository " +
			"without them. No agent works on the repository until it merges"))
	}).Should(Succeed())
	return adoption
}

// stoppedAdoption waits for the adoption to stop with reason, the Container
// still Ready and its Standard unrecorded, and returns the condition's
// message.
func stoppedAdoption(ns, reason string) string {
	GinkgoHelper()
	var message string
	Eventually(func(g Gomega) {
		container := getContainer(g, ns)
		g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
		g.Expect(container.Status.Standard).To(BeNil())
		g.Expect(container.Status.StandardAdoption).To(PointTo(HaveField("Stopped", reason)))
		cond := standardCondition(g, container)
		g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(cond.Reason).To(Equal(reason))
		message = cond.Message
	}).Should(Succeed())
	return message
}

// nameStandard has orders-api name the Standard name, or none when empty.
func nameStandard(ns, name string) {
	GinkgoHelper()
	container := getContainer(Default, ns)
	Expect(patch(container, func() {
		container.Spec.StandardRef = nil
		if name != "" {
			container.Spec.StandardRef = &c4v1beta1.LocalRef{Name: name}
		}
	})).To(Succeed())
}

// noTaskIn holds that no Task exists in project ns.
func noTaskIn(g Gomega, ns string) {
	var tasks c4v1beta1.TaskList
	g.Expect(k8sClient.List(ctx, &tasks)).To(Succeed())
	for _, t := range tasks.Items {
		g.Expect(t.Namespace).NotTo(Equal(ns), "Task %s started before the Standard is on main", t.Name)
	}
}

// mergeByHand merges pull request number of orders-api as a person does.
func mergeByHand(ns string, number int32) {
	GinkgoHelper()
	owner := gitFake.For(ports.Config{Owner: ns})
	repo := owner.Repository("orders-api")
	pr, err := owner.GetPullRequest(ctx, repo, int(number))
	Expect(err).NotTo(HaveOccurred())
	Expect(owner.MergePullRequest(ctx, repo, int(number), pr.HeadSHA, "Bring in the Standard", "Merged by a person")).To(Succeed())
}

// pushToAdoption replaces what the adoption's branch changes with files, as a
// person who pushes to the pull request before merging it.
func pushToAdoption(ns string, adoption c4v1beta1.StandardAdoptionStatus, files scaffold.Tree) {
	GinkgoHelper()
	owner := gitFake.For(ports.Config{Owner: ns})
	_, err := owner.PushBranch(ctx, owner.Repository("orders-api"), adoption.Base, adoption.Branch, files, nil,
		"Keep what the repository needs", ports.Signature{Name: "A person"})
	Expect(err).NotTo(HaveOccurred())
}

// recordedStandard waits for orders-api's Standard to be recorded at v1.0.0
// and main protected, the adoption gone.
func recordedStandard(ns string) {
	GinkgoHelper()
	eventuallyAt(ns, "v1.0.0")
	Eventually(func(g Gomega) {
		g.Expect(getContainer(g, ns).Status.StandardAdoption).To(BeNil())
		g.Expect(gitFake.ProtectedBranches(ns, "orders-api")).To(ContainElement("main"))
	}).Should(Succeed())
}

// #97 (docs/plans/adopt-existing.md, step 4): a Standard is something an
// adopted repository grows into.
var _ = Describe("Container adoption with a Standard the repository doesn't hold", func() {
	It("opens one pull request with exactly the Standard's files and is Ready; merging it records the "+
		"Standard and protects main (#97)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", ownRepository(ns))
		adoptWithStandard(ns)

		adoption := openedAdoption(ns)
		Expect(adoption.Ref).To(Equal("v1.0.0"))
		Expect(adoption.Branch).To(Equal("standard-adopt/python-strict-v1.0.0"))
		Expect(adoption.Paths).To(Equal([]string{"AGENTS.md", "Makefile"}))
		Expect(gitFake.BranchEdits(ns, "orders-api", adoption.Branch)).To(And(HaveLen(2),
			HaveKeyWithValue("AGENTS.md", PointTo(HaveField("Data", BeEquivalentTo(strictGuide)))),
			HaveKeyWithValue("Makefile", PointTo(HaveField("Data", BeEquivalentTo(strictMakefile))))))
		title, body := gitFake.PullText(ns, "orders-api", int(adoption.PullRequest))
		Expect(title).To(Equal("Bring in the Standard python-strict at v1.0.0"))
		Expect(body).To(ContainSubstring("The operator never merges this pull request."))

		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		scaffolded := condition(container.Status.Conditions, c4v1beta1.ConditionScaffolded)
		Expect(scaffolded).NotTo(BeNil())
		Expect(scaffolded.Message).To(ContainSubstring("the files of its Standard python-strict arrive as a pull request"))
		Expect(condition(container.Status.Conditions, c4v1beta1.ConditionChecksApplied)).To(BeNil(),
			"the Standard's check runs nowhere yet")
		// Nothing is written to main, nothing protected, and nothing merged or
		// opened a second time while a person reads the pull request.
		Consistently(func(g Gomega) {
			g.Expect(gitFake.PullRequests(ns, "orders-api")).To(HaveLen(1))
			g.Expect(gitFake.CommitCount(ns, "orders-api")).To(Equal(1))
			g.Expect(gitFake.ProtectedBranches(ns, "orders-api")).NotTo(ContainElement("main"))
			g.Expect(gitFake.ForcedMerges(ns, "orders-api")).To(BeEmpty())
		}).WithTimeout(2 * time.Second).Should(Succeed())

		mergeByHand(ns, adoption.PullRequest)
		recordedStandard(ns)
		Expect(mainFile(ns, "Makefile")).To(Equal(strictMakefile))
		Expect(mainFile(ns, "AGENTS.md")).To(Equal(strictGuide))
		Expect(mainFile(ns, "main.py")).To(Equal("print('orders')\n"))
		Expect(gitFake.PullRequests(ns, "orders-api")).To(HaveLen(1))
	})

	It("stays Ready without the Standard when a person closes the pull request, and says so; the files "+
		"brought by hand are found (#97)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", ownRepository(ns))
		adoptWithStandard(ns)
		adoption := openedAdoption(ns)

		gitFake.ClosePull(ns, "orders-api", int(adoption.PullRequest))
		message := stoppedAdoption(ns, c4v1beta1.ReasonUpdateClosed)
		Expect(message).To(Equal("Pull request " + adoption.PullRequestURL + " was closed without merging, so the " +
			"repository is without AGENTS.md, Makefile as python-strict renders them at v1.0.0. No agent works on " +
			"the repository until main holds them; a change of the Standard's ref or of the Container's checks " +
			"opens a new pull request"))
		Expect(gitFake.BranchExists(ns, "orders-api", adoption.Branch)).To(BeFalse())
		Consistently(func(g Gomega) {
			container := getContainer(g, ns)
			g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
			g.Expect(standardCondition(g, container).Reason).To(Equal(c4v1beta1.ReasonUpdateClosed))
			g.Expect(gitFake.PullRequests(ns, "orders-api")).To(HaveLen(1))
			g.Expect(gitFake.ProtectedBranches(ns, "orders-api")).NotTo(ContainElement("main"))
		}).WithTimeout(2 * time.Second).Should(Succeed())

		gitFake.CommitToMain(ns, "orders-api", scaffold.Tree{
			"AGENTS.md": {Data: []byte(strictGuide)}, "Makefile": {Data: []byte(strictMakefile)}})
		recordedStandard(ns)
		Expect(gitFake.PullRequests(ns, "orders-api")).To(HaveLen(1))
	})

	It("says StandardFilesDiffer when the pull request merged with a file the Standard protects changed, "+
		"until main holds it as rendered (#97)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", ownRepository(ns))
		adoptWithStandard(ns, "Makefile")
		adoption := openedAdoption(ns)

		// The person keeps the repository's own Makefile: the branch brings the guide alone.
		pushToAdoption(ns, adoption, scaffold.Tree{"AGENTS.md": {Data: []byte(strictGuide)}})
		mergeByHand(ns, adoption.PullRequest)
		message := stoppedAdoption(ns, c4v1beta1.ReasonStandardFilesDiffer)
		Expect(message).To(HavePrefix("Pull request " + adoption.PullRequestURL + " merged, but main holds Makefile " +
			"with other content than python-strict renders at v1.0.0, and python-strict protects them."))
		Expect(gitFake.ProtectedBranches(ns, "orders-api")).NotTo(ContainElement("main"))
		Expect(gitFake.PullRequests(ns, "orders-api")).To(HaveLen(1))

		gitFake.CommitToMain(ns, "orders-api", scaffold.Tree{"Makefile": {Data: []byte(strictMakefile)}})
		recordedStandard(ns)
	})

	It("records the Standard when the pull request merged with a file it doesn't protect changed: the "+
		"file is the repository's own (#97)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", ownRepository(ns))
		adoptWithStandard(ns, "Makefile")
		adoption := openedAdoption(ns)

		pushToAdoption(ns, adoption, scaffold.Tree{
			"AGENTS.md": {Data: []byte(strictGuide + "Ask the orders team before changing a migration.\n")},
			"Makefile":  {Data: []byte(strictMakefile)}})
		mergeByHand(ns, adoption.PullRequest)
		recordedStandard(ns)
		Expect(mainFile(ns, "AGENTS.md")).To(HaveSuffix("Ask the orders team before changing a migration.\n"))
		Expect(gitFake.PullRequests(ns, "orders-api")).To(HaveLen(1))
	})

	It("has a Task wait while the pull request is open: no agent works where the Standard's checks "+
		"aren't on main (#97)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", ownRepository(ns))
		adoptWithStandard(ns)
		adoption := openedAdoption(ns)
		crewAgents(ns, 3)

		mustCreate(newTask(ns, "by-hand", "Export orders"))
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "by-hand")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskPending))
			g.Expect(condition(task.Status.Conditions, ConditionProgressing)).To(And(
				HaveField("Reason", model.ReasonNotProtected),
				HaveField("Message", ContainSubstring("Waiting for the main of Container orders-api to be protected")),
				HaveField("Message", ContainSubstring("Pull request "+adoption.PullRequestURL+" brings the files"))))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(getTask(g, ns, "by-hand").Status.Phase).To(Equal(c4v1beta1.TaskPending))
			g.Expect(gitFake.ProtectedBranches(ns, "orders-api")).NotTo(ContainElement("main"))
		}).WithTimeout(2 * time.Second).Should(Succeed())

		mergeByHand(ns, adoption.PullRequest)
		recordedStandard(ns)
		Eventually(func(g Gomega) {
			cond := condition(getTask(g, ns, "by-hand").Status.Conditions, ConditionProgressing)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).NotTo(Equal(model.ReasonNotProtected), "the Task goes on once main is protected")
		}).Should(Succeed())
	})

	It("has a Triage Task made by hand wait too, and leaves main for the person to merge into (#97)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", ownRepository(ns))
		adoptWithStandard(ns)
		adoption := openedAdoption(ns)
		crewAgents(ns, 3)
		mustCreate(&c4v1beta1.Agent{ObjectMeta: objectMeta(ns, "triager"),
			Spec: c4v1beta1.AgentSpec{Image: "theos/agent-go:dev", Model: "claude-haiku-5-5"}})
		n := gitFake.AddIssue(ns, "orders-api", "Export orders", "As CSV.", []string{model.LabelProposed})

		// A Triage Task has no Pending step of its own to wait in: it goes
		// straight to its run, which is where the Standard's rule reached main.
		triage := newTriageTask(ns, "triage-by-hand")
		triage.Spec.Issue = int32(n)
		mustCreate(triage)
		Eventually(func(g Gomega) {
			g.Expect(condition(getTask(g, ns, "triage-by-hand").Status.Conditions, ConditionProgressing)).To(And(
				HaveField("Reason", model.ReasonNotProtected),
				HaveField("Message", ContainSubstring("Waiting for the main of Container orders-api to be protected")),
				HaveField("Message", ContainSubstring("Pull request "+adoption.PullRequestURL+" brings the files"))))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.ProtectedBranches(ns, "orders-api")).NotTo(ContainElement("main"),
				"under the Standard's rule the person could no longer merge its pull request")
			g.Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "triage-by-hand-r1-triage"), newSandbox()))).
				To(BeTrue(), "the triager's run started")
		}).WithTimeout(2 * time.Second).Should(Succeed())

		mergeByHand(ns, adoption.PullRequest)
		recordedStandard(ns)
		Eventually(func(g Gomega) { sandboxExists(g, ns, "triage-by-hand-r1-triage") }).Should(Succeed())
	})

	It("has the Crew start no Task, its triager's included, while the pull request is open (#97)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", ownRepository(ns))
		adoptWithStandard(ns)
		adoption := openedAdoption(ns)
		crewAgents(ns, 3)
		mustCreate(&c4v1beta1.Agent{ObjectMeta: objectMeta(ns, "triager"),
			Spec: c4v1beta1.AgentSpec{Image: "theos/agent-go:dev", Model: "claude-haiku-5-5"}})
		ready := gitFake.AddIssue(ns, "orders-api", "Validate orders", clearIssue, []string{model.LabelReady})
		proposed := gitFake.AddIssue(ns, "orders-api", "Export orders", "As CSV.", []string{model.LabelProposed})
		crew := newCrew(ns)
		crew.Spec.Triager = &c4v1beta1.LocalRef{Name: "triager"}
		mustCreate(crew)

		Eventually(func(g Gomega) {
			cond := condition(getCrew(g, ns).Status.Conditions, ConditionReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(model.ReasonNotProtected))
			g.Expect(cond.Message).To(And(
				ContainSubstring("Waiting for the main of Container orders-api to be protected"),
				ContainSubstring("Pull request "+adoption.PullRequestURL+" brings the files")))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			noTaskIn(g, ns)
			g.Expect(gitFake.Issue(ns, "orders-api", ready).Labels).To(Equal([]string{model.LabelReady}))
			g.Expect(gitFake.Issue(ns, "orders-api", proposed).Labels).To(Equal([]string{model.LabelProposed}))
			g.Expect(gitFake.ProtectedBranches(ns, "orders-api")).NotTo(ContainElement("main"))
		}, "3s").Should(Succeed())

		mergeByHand(ns, adoption.PullRequest)
		recordedStandard(ns)
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, fmt.Sprintf("orders-api-%d", ready)).Spec.Kind).To(Equal(c4v1beta1.TaskImplement))
			g.Expect(getTask(g, ns, fmt.Sprintf("orders-api-%d-triage", proposed)).Spec.Kind).To(Equal(c4v1beta1.TaskTriage))
		}).Should(Succeed())
	})

	It("opens the pull request for a Standard named after a bare adoption, where a scaffolded Container "+
		"says AddedAfterScaffold (#97)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", ownRepository(ns))
		mustCreate(newStandard(ns, "python-strict", "python-fastapi"))
		createWebshopWith(ns, func(c *c4v1beta1.Container) { c.Spec.Adopt = true })
		bare := eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		Expect(condition(bare.Status.Conditions, c4v1beta1.ConditionStandardUpdated)).To(BeNil())
		Expect(gitFake.PullRequests(ns, "orders-api")).To(BeEmpty())

		nameStandard(ns, "python-strict")
		adoption := openedAdoption(ns)
		Expect(adoption.Paths).To(Equal([]string{"AGENTS.md", "Makefile"}))
		Consistently(func(g Gomega) {
			g.Expect(standardCondition(g, getContainer(g, ns)).Reason).To(Equal(c4v1beta1.ReasonPullRequestOpen))
			g.Expect(gitFake.CommitCount(ns, "orders-api")).To(Equal(1), "nothing is written to main")
		}).WithTimeout(2 * time.Second).Should(Succeed())

		mergeByHand(ns, adoption.PullRequest)
		recordedStandard(ns)
		Expect(mainFile(ns, "AGENTS.md")).To(Equal(strictGuide))
	})

	It("keeps the pull request at its ref when the Standard's ref moves, and brings the new ref as an "+
		"ordinary update once it merged (#97)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", ownRepository(ns))
		adoptWithStandard(ns)
		adoption := openedAdoption(ns)

		gitFake.AddTemplate(templatesURL+"python-strict", "v1.1.0", scaffold.Tree{
			"AGENTS.md.tmpl": {Data: []byte("Rules for {{ .Container }}, checked by {{ .Standard }}\n")},
			"Makefile":       {Data: []byte("check:\n\tmake lint test\n")},
		})
		bumpStandard(ns, "v1.1.0")
		Consistently(func(g Gomega) {
			container := getContainer(g, ns)
			g.Expect(container.Status.Standard).To(BeNil())
			g.Expect(container.Status.StandardAdoption).To(PointTo(And(
				HaveField("Ref", "v1.0.0"), HaveField("PullRequest", adoption.PullRequest), HaveField("Stopped", ""))))
			g.Expect(standardCondition(g, container).Message).To(ContainSubstring("python-strict at v1.0.0"))
			g.Expect(gitFake.PullRequests(ns, "orders-api")).To(HaveLen(1))
		}).WithTimeout(2 * time.Second).Should(Succeed())

		By("recording the Standard at the pull request's ref, and updating from there")
		mergeByHand(ns, adoption.PullRequest)
		update := openedUpdate(ns, "v1.1.0")
		Expect(update.Branch).To(Equal("standard-update/python-strict-v1.1.0"))
		Expect(update.Paths).To(Equal([]string{"Makefile"}))
		Expect(getContainer(Default, ns).Status.StandardAdoption).To(BeNil())
		gitFake.SetChecks(ns, "orders-api", update.Commit, "success")
		eventuallyAt(ns, "v1.1.0")
		Expect(mainFile(ns, "Makefile")).To(Equal("check:\n\tmake lint test\n"))
		Expect(gitFake.ForcedMerges(ns, "orders-api")).To(Equal([]int{int(update.PullRequest)}),
			"the operator merges the update, never the adoption")
	})

	It("closes the pull request of a Standard the Container names no more, and plans the one it names: "+
		"an adoption is for one Standard (#97)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", ownRepository(ns))
		adoptWithStandard(ns)
		mustCreate(newStandard(ns, "python-loose", "python-fastapi"))
		strict := openedAdoption(ns)

		By("naming another Standard, pinned at the same ref, while the pull request is open")
		nameStandard(ns, "python-loose")
		loose := openedAdoptionOf(ns, "python-loose")
		Expect(loose.PullRequest).NotTo(Equal(strict.PullRequest))
		Expect(loose.Branch).To(Equal("standard-adopt/python-loose-v1.0.0"))
		Expect(pullState(ns, strict.PullRequest)).To(Equal("closed"))
		Expect(gitFake.BranchExists(ns, "orders-api", strict.Branch)).To(BeFalse())
		Expect(gitFake.BranchEdits(ns, "orders-api", loose.Branch)).To(HaveKeyWithValue("AGENTS.md",
			PointTo(HaveField("Data", BeEquivalentTo("Rules for orders-api, checked by python-loose\n")))))

		By("naming the first again after a person closed the second's: the stop is not the first's")
		gitFake.ClosePull(ns, "orders-api", int(loose.PullRequest))
		stoppedAdoption(ns, c4v1beta1.ReasonUpdateClosed)
		nameStandard(ns, "python-strict")
		again := openedAdoption(ns)
		Expect(again.PullRequest).NotTo(BeElementOf(strict.PullRequest, loose.PullRequest))
		Expect(gitFake.PullRequests(ns, "orders-api")).To(HaveLen(3))

		By("merging it: the Standard recorded is the one the files on main are of")
		mergeByHand(ns, again.PullRequest)
		recordedStandard(ns)
		Expect(mainFile(ns, "AGENTS.md")).To(Equal(strictGuide))
	})

	It("closes the pull request when the Container names no Standard any more (#97)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", ownRepository(ns))
		adoptWithStandard(ns)
		adoption := openedAdoption(ns)

		nameStandard(ns, "")
		Eventually(func(g Gomega) {
			container := getContainer(g, ns)
			g.Expect(container.Status.StandardAdoption).To(BeNil())
			g.Expect(condition(container.Status.Conditions, c4v1beta1.ConditionStandardUpdated)).To(BeNil())
			g.Expect(pullState(ns, adoption.PullRequest)).To(Equal("closed"))
			g.Expect(gitFake.BranchExists(ns, "orders-api", adoption.Branch)).To(BeFalse())
		}).Should(Succeed())
		Expect(gitFake.CommitCount(ns, "orders-api")).To(Equal(1))
	})

	It("reads a pull request gone from the host as closed, not as a Git error for ever (#97)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", ownRepository(ns))
		adoptWithStandard(ns)
		adoption := openedAdoption(ns)

		gitFake.DeletePull(ns, "orders-api", int(adoption.PullRequest))
		Expect(stoppedAdoption(ns, c4v1beta1.ReasonUpdateClosed)).To(HavePrefix(
			"Pull request " + adoption.PullRequestURL + " was closed without merging"))
		gitFake.CommitToMain(ns, "orders-api", scaffold.Tree{
			"AGENTS.md": {Data: []byte(strictGuide)}, "Makefile": {Data: []byte(strictMakefile)}})
		recordedStandard(ns)
	})
})

// #97: no agent works on an adopted repository whose Standard isn't on main:
// main has none of the Standard's checks to be protected by.
func TestNoAgentWorksUntilTheStandardIsOnMain(t *testing.T) {
	adopting := func(mutate func(*c4v1beta1.Container)) *c4v1beta1.Container {
		c := &c4v1beta1.Container{ObjectMeta: objectMeta("apps", "orders-api"),
			Spec:   c4v1beta1.ContainerSpec{Adopt: true, StandardRef: &c4v1beta1.LocalRef{Name: "python-strict"}},
			Status: c4v1beta1.ContainerStatus{Phase: c4v1beta1.ContainerReady}}
		if mutate != nil {
			mutate(c)
		}
		return c
	}
	const open = "Pull request http://git.example/apps/orders-api/pulls/1 brings the files"
	tests := []struct {
		name      string
		container *c4v1beta1.Container
		onMain    bool
		why       string
	}{
		{name: "adopted, the Standard's pull request open",
			container: adopting(func(c *c4v1beta1.Container) {
				setCondition(&c.Status.Conditions, 1, c4v1beta1.ConditionStandardUpdated, metav1.ConditionFalse,
					c4v1beta1.ReasonPullRequestOpen, open)
			}),
			why: open},
		{name: "adopted, before the first pass over its Standard",
			container: adopting(nil), why: "the files of its Standard python-strict are not on main yet"},
		{name: "adopted, the Standard recorded",
			container: adopting(func(c *c4v1beta1.Container) {
				c.Status.Standard = &c4v1beta1.ContainerStandardStatus{Name: "python-strict", Ref: "v1.0.0"}
			}),
			onMain: true},
		{name: "adopted, not Ready yet: the Task waits for that",
			container: adopting(func(c *c4v1beta1.Container) { c.Status.Phase = c4v1beta1.ContainerPending }),
			onMain:    true},
		{name: "scaffolded with the Standard",
			container: adopting(func(c *c4v1beta1.Container) { c.Spec.Adopt = false }), onMain: true},
		{name: "adopted with no Standard",
			container: adopting(func(c *c4v1beta1.Container) { c.Spec.StandardRef = nil })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := standardOnMain(tt.container); got != tt.onMain {
				t.Errorf("standardOnMain() = %v, want %v", got, tt.onMain)
			}
			if got := standardNotOnMain(tt.container); got != tt.why {
				t.Errorf("standardNotOnMain() = %q, want %q", got, tt.why)
			}
		})
	}
}
