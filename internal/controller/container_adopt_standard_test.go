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
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
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

// #97 (docs/plans/done/adopt-existing.md, step 4): a Standard is something an
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

	It("holds a Standard named after the adoption to the Frameworks it lists, as at creation, and opens "+
		"no pull request until the Container names one of them (#97)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", ownRepository(ns))
		mustCreate(newStandard(ns, "python-strict", "python-fastapi"), newSystem(ns, "webshop-core"),
			createBareAdoption(ns, nil))
		eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		// unfit holds that the Standard is refused with message, the Container
		// still Ready and nothing opened, recorded or protected.
		unfit := func(message string) {
			GinkgoHelper()
			refused := func(g Gomega) {
				container := getContainer(g, ns)
				g.Expect(container.Status.ObservedGeneration).To(Equal(container.Generation))
				g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
				g.Expect(container.Status.Standard).To(BeNil())
				g.Expect(container.Status.StandardAdoption).To(BeNil())
				cond := standardCondition(g, container)
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonUnsupportedFramework))
				g.Expect(cond.Message).To(Equal(message))
				g.Expect(gitFake.PullRequests(ns, "orders-api")).To(BeEmpty())
				g.Expect(gitFake.ProtectedBranches(ns, "orders-api")).NotTo(ContainElement("main"))
			}
			Eventually(refused).Should(Succeed())
			Consistently(refused).WithTimeout(2 * time.Second).Should(Succeed())
		}
		nameFramework := func(name string) {
			GinkgoHelper()
			container := getContainer(Default, ns)
			Expect(patch(container, func() { container.Spec.FrameworkRef = c4v1beta1.LocalRef{Name: name} })).To(Succeed())
		}

		By("naming the Standard on a Container that names no Framework")
		nameStandard(ns, "python-strict")
		unfit("python-strict supports only the Frameworks it lists, and the Container names no frameworkRef")

		By("naming a Framework the Standard doesn't list")
		nameFramework("go-cli")
		unfit("python-strict does not support go-cli")

		By("naming one it lists: the pull request opens")
		nameFramework("python-fastapi")
		adoption := openedAdoption(ns)
		Expect(adoption.Paths).To(Equal([]string{"AGENTS.md", "Makefile"}))
		Expect(gitFake.CommitCount(ns, "orders-api")).To(Equal(1), "nothing is written to main")
	})

	It("keeps the pull request of the Standard named before while the one named now doesn't fit or "+
		"doesn't exist, and closes it once a Standard that fits is named (#97)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", ownRepository(ns))
		adoptWithStandard(ns)
		mustCreate(newStandard(ns, "go-strict", "go-cli"), newStandard(ns, "python-loose", "python-fastapi"))
		strict := openedAdoption(ns)
		// kept holds that the Standard named now is refused with reason and
		// message, and that python-strict's pull request is as it was: open,
		// still the adoption in status, the only one, nothing recorded or
		// protected.
		kept := func(reason, message string) {
			GinkgoHelper()
			waiting := func(g Gomega) {
				container := getContainer(g, ns)
				g.Expect(container.Status.ObservedGeneration).To(Equal(container.Generation))
				g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
				g.Expect(container.Status.Standard).To(BeNil())
				g.Expect(container.Status.StandardAdoption).To(PointTo(Equal(strict)))
				cond := standardCondition(g, container)
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(reason))
				g.Expect(cond.Message).To(Equal(message))
				g.Expect(pullState(ns, strict.PullRequest)).To(Equal("open"))
				g.Expect(gitFake.BranchExists(ns, "orders-api", strict.Branch)).To(BeTrue())
				g.Expect(gitFake.PullRequests(ns, "orders-api")).To(HaveLen(1))
				g.Expect(gitFake.ProtectedBranches(ns, "orders-api")).NotTo(ContainElement("main"))
			}
			Eventually(waiting).Should(Succeed())
			Consistently(waiting).WithTimeout(2 * time.Second).Should(Succeed())
		}

		By("naming a Standard that doesn't list the Container's Framework")
		nameStandard(ns, "go-strict")
		kept(c4v1beta1.ReasonUnsupportedFramework, "go-strict does not support python-fastapi")

		By("naming one that doesn't exist")
		nameStandard(ns, "rust-strict")
		kept(c4v1beta1.ReasonNotFound, notFoundMessage([]string{"Standard rust-strict"}))

		By("naming one that fits: the first's pull request closes, and the named one's opens")
		nameStandard(ns, "python-loose")
		loose := openedAdoptionOf(ns, "python-loose")
		Expect(loose.PullRequest).NotTo(Equal(strict.PullRequest))
		Expect(pullState(ns, strict.PullRequest)).To(Equal("closed"))
		Expect(gitFake.BranchExists(ns, "orders-api", strict.Branch)).To(BeFalse())
		Expect(gitFake.PullRequests(ns, "orders-api")).To(HaveLen(2))
		Expect(gitFake.CommitCount(ns, "orders-api")).To(Equal(1), "nothing is written to main")
	})

	It("keeps the pull request while the host doesn't answer: an unreachable host is not a closed pull "+
		"request (#97)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", ownRepository(ns))
		adoptWithStandard(ns)
		adoption := openedAdoption(ns)

		gitFake.FailPullReads(ns, "orders-api", int(adoption.PullRequest), 1000)
		retrying := func(g Gomega) {
			container := getContainer(g, ns)
			g.Expect(standardCondition(g, container)).To(And(
				HaveField("Reason", c4v1beta1.ReasonGitError), HaveField("Message", HaveSuffix("; retrying"))))
			g.Expect(container.Status.Standard).To(BeNil())
			g.Expect(container.Status.StandardAdoption).To(PointTo(And(
				HaveField("PullRequest", adoption.PullRequest), HaveField("Stopped", ""))))
			g.Expect(pullState(ns, adoption.PullRequest)).To(Equal("open"))
			g.Expect(gitFake.BranchExists(ns, "orders-api", adoption.Branch)).To(BeTrue())
		}
		Eventually(retrying).Should(Succeed())
		Consistently(retrying).WithTimeout(2 * time.Second).Should(Succeed())

		By("answering again: the same pull request is open, and no second one")
		gitFake.FailPullReads(ns, "orders-api", int(adoption.PullRequest), 0)
		Expect(openedAdoption(ns)).To(And(
			HaveField("PullRequest", adoption.PullRequest), HaveField("Branch", adoption.Branch)))
		Expect(gitFake.PullRequests(ns, "orders-api")).To(HaveLen(1))
		Expect(gitFake.CommitCount(ns, "orders-api")).To(Equal(1))
	})

	It("keeps saying GitError while the host refuses to close the pull request of a Standard the "+
		"Container names no more, and closes it at the retry (#97)", func() {
		ns := newProjectNamespace()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", ownRepository(ns))
		adoptWithStandard(ns)
		adoption := openedAdoption(ns)

		gitFake.FailPullReads(ns, "orders-api", int(adoption.PullRequest), 1000)
		nameStandard(ns, "")
		retrying := func(g Gomega) {
			container := getContainer(g, ns)
			g.Expect(container.Status.ObservedGeneration).To(Equal(container.Generation),
				"the pass that saw the Standard gone")
			g.Expect(standardCondition(g, container)).To(And(
				HaveField("Reason", c4v1beta1.ReasonGitError), HaveField("Message", HaveSuffix("; retrying"))))
			g.Expect(container.Status.StandardAdoption).To(PointTo(HaveField("PullRequest", adoption.PullRequest)))
			g.Expect(pullState(ns, adoption.PullRequest)).To(Equal("open"))
		}
		Eventually(retrying).Should(Succeed())
		Consistently(retrying).WithTimeout(2 * time.Second).Should(Succeed())

		gitFake.FailPullReads(ns, "orders-api", int(adoption.PullRequest), 0)
		Eventually(func(g Gomega) {
			container := getContainer(g, ns)
			g.Expect(container.Status.StandardAdoption).To(BeNil())
			g.Expect(condition(container.Status.Conditions, c4v1beta1.ConditionStandardUpdated)).To(BeNil())
			g.Expect(pullState(ns, adoption.PullRequest)).To(Equal("closed"))
			g.Expect(gitFake.BranchExists(ns, "orders-api", adoption.Branch)).To(BeFalse())
		}).Should(Succeed())
	})

	// #184: between two passes of a stopped adoption, another writer can
	// replace its condition: a Git error, its Standard not found, or one that
	// doesn't fit the Container's Framework. Once the cause is gone, with
	// main still at the commit the adoption stopped at, the condition says
	// again why it stopped, as the stop wrote it.
	DescribeTable("says again why a stopped adoption stopped once another writer's cause is gone (#184)",
		func(stop func(ns string) (reason, message string), replaced string, replace func(ns string) (restore func())) {
			ns := newProjectNamespace()
			gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", ownRepository(ns))
			reason, message := stop(ns)
			stopped := *getContainer(Default, ns).Status.StandardAdoption
			commits := gitFake.CommitCount(ns, "orders-api")

			restore := replace(ns)
			Eventually(func(g Gomega) {
				g.Expect(standardCondition(g, getContainer(g, ns)).Reason).To(Equal(replaced))
			}).Should(Succeed())
			restore()

			Eventually(func(g Gomega) {
				container := getContainer(g, ns)
				g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
				g.Expect(container.Status.Standard).To(BeNil())
				g.Expect(container.Status.StandardAdoption).To(PointTo(Equal(stopped)))
				cond := standardCondition(g, container)
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(reason))
				g.Expect(cond.Message).To(Equal(message))
			}).Should(Succeed())
			// It stays so: no churn, nothing rendered a second time.
			Consistently(func(g Gomega) {
				cond := standardCondition(g, getContainer(g, ns))
				g.Expect(cond.Reason).To(Equal(reason))
				g.Expect(cond.Message).To(Equal(message))
			}).WithTimeout(3 * time.Second).Should(Succeed())
			Expect(gitFake.PullRequests(ns, "orders-api")).To(HaveLen(1))
			Expect(gitFake.CommitCount(ns, "orders-api")).To(Equal(commits), "main is as the adoption stopped at")
			Expect(gitFake.ProtectedBranches(ns, "orders-api")).NotTo(ContainElement("main"))
		},
		Entry("UpdateClosed, replaced by GitError", stopByClosing, c4v1beta1.ReasonGitError, dropCredentials),
		Entry("UpdateClosed, replaced by NotFound", stopByClosing, c4v1beta1.ReasonNotFound, recreateStandard),
		Entry("UpdateClosed, replaced by UnsupportedFramework", stopByClosing, c4v1beta1.ReasonUnsupportedFramework,
			unlistFramework),
		Entry("StandardFilesDiffer, replaced by GitError", stopByDiffering, c4v1beta1.ReasonGitError, dropCredentials),
		Entry("StandardFilesDiffer, replaced by NotFound", stopByDiffering, c4v1beta1.ReasonNotFound,
			recreateStandard),
		Entry("StandardFilesDiffer, replaced by UnsupportedFramework", stopByDiffering,
			c4v1beta1.ReasonUnsupportedFramework, unlistFramework),
	)
})

// stopByClosing has orders-api adopt its repository with python-strict and a
// person close the pull request, and returns the stop's reason and message.
func stopByClosing(ns string) (string, string) {
	GinkgoHelper()
	adoptWithStandard(ns)
	adoption := openedAdoption(ns)
	gitFake.ClosePull(ns, "orders-api", int(adoption.PullRequest))
	return c4v1beta1.ReasonUpdateClosed, stoppedAdoption(ns, c4v1beta1.ReasonUpdateClosed)
}

// stopByDiffering has orders-api adopt its repository with python-strict,
// which protects the Makefile, and a person merge the pull request without
// it, and returns the stop's reason and message.
func stopByDiffering(ns string) (string, string) {
	GinkgoHelper()
	adoptWithStandard(ns, "Makefile")
	adoption := openedAdoption(ns)
	pushToAdoption(ns, adoption, scaffold.Tree{"AGENTS.md": {Data: []byte(strictGuide)}})
	mergeByHand(ns, adoption.PullRequest)
	return c4v1beta1.ReasonStandardFilesDiffer, stoppedAdoption(ns, c4v1beta1.ReasonStandardFilesDiffer)
}

// dropCredentials deletes the GitProvider's credentials, so the operator
// can't reach the Git host, and returns what puts them back.
func dropCredentials(ns string) func() {
	GinkgoHelper()
	var provider c4v1beta1.GitProvider
	Expect(k8sClient.Get(ctx, key(ns, getContainer(Default, ns).Spec.GitProviderRef.Name), &provider)).To(Succeed())
	name := provider.Spec.CredentialsSecretRef.Name
	var secret corev1.Secret
	Expect(k8sClient.Get(ctx, key(ns, name), &secret)).To(Succeed())
	Expect(k8sClient.Delete(ctx, &secret)).To(Succeed())
	return func() {
		GinkgoHelper()
		mustCreate(&corev1.Secret{ObjectMeta: objectMeta(ns, name), Data: secret.Data})
	}
}

// recreateStandard deletes python-strict, and returns what creates it again
// with the same spec.
func recreateStandard(ns string) func() {
	GinkgoHelper()
	var standard c4v1beta1.Standard
	Expect(k8sClient.Get(ctx, key(ns, "python-strict"), &standard)).To(Succeed())
	Expect(k8sClient.Delete(ctx, &standard)).To(Succeed())
	return func() {
		GinkgoHelper()
		mustCreate(&c4v1beta1.Standard{ObjectMeta: objectMeta(ns, "python-strict"), Spec: standard.Spec})
	}
}

// unlistFramework has orders-api name a Framework python-strict doesn't
// list, and returns what names python-fastapi again.
func unlistFramework(ns string) func() {
	GinkgoHelper()
	name := func(framework string) {
		GinkgoHelper()
		container := getContainer(Default, ns)
		Expect(patch(container, func() { container.Spec.FrameworkRef = c4v1beta1.LocalRef{Name: framework} })).
			To(Succeed())
	}
	name("go-cli")
	return func() { name("python-fastapi") }
}

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

// #97: a Container that names no Standard any more, its adoption still in
// status. Nothing else reconciles such a Container, so a pass that could not
// close the pull request asks for the next one and leaves the condition.
func TestDropStandardAdoption(t *testing.T) {
	const open = "Pull request http://git.example/apps/orders-api/pulls/1 brings the files"
	adoption := &c4v1beta1.StandardAdoptionStatus{Name: "python-strict", Ref: "v1.0.0", PullRequest: 1}
	tests := []struct {
		name     string
		adoption *c4v1beta1.StandardAdoptionStatus
		// kept says the condition StandardUpdated stays, and the pass asks for
		// the next recheck.
		kept bool
	}{
		{name: "#97: the adoption in status, the cached Container not the live one", adoption: adoption, kept: true},
		{name: "no adoption in status: the condition goes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &ContainerReconciler{}
			container := &c4v1beta1.Container{ObjectMeta: objectMeta("apps", "orders-api"),
				Spec: c4v1beta1.ContainerSpec{Adopt: true}}
			status := &c4v1beta1.ContainerStatus{Phase: c4v1beta1.ContainerReady, StandardAdoption: tt.adoption}
			setCondition(&status.Conditions, 1, c4v1beta1.ConditionStandardUpdated, metav1.ConditionFalse,
				c4v1beta1.ReasonPullRequestOpen, open)

			result := r.dropStandardAdoption(context.Background(), container, status, func() bool { return false })

			want := time.Duration(0)
			if tt.kept {
				want = r.checksRecheck()
			}
			if result.RequeueAfter != want {
				t.Errorf("RequeueAfter = %v, want %v", result.RequeueAfter, want)
			}
			cond := apimeta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionStandardUpdated)
			if kept := cond != nil && cond.Message == open; kept != tt.kept {
				t.Errorf("StandardUpdated is %+v, kept = %v, want %v", cond, kept, tt.kept)
			}
			if status.StandardAdoption != tt.adoption {
				t.Errorf("status.standardAdoption = %+v, want it untouched", status.StandardAdoption)
			}
		})
	}
}

// #97: a Standard that doesn't fit is refused on every pass, every recheck
// while the host delivers nothing; the log says so once per message, as the
// condition does.
func TestStandardFitsLogsARefusalOnce(t *testing.T) {
	const refusal = "python-strict does not support go-cli"
	standard := &c4v1beta1.Standard{ObjectMeta: objectMeta("apps", "python-strict"),
		Spec: c4v1beta1.StandardSpec{SupportedFrameworks: []string{"python-fastapi"}}}
	tests := []struct {
		name string
		// was is the condition StandardUpdated of the Container as read: its
		// reason and message, or none.
		was    [2]string
		logged int
	}{
		{name: "#97: the first pass that refuses it", logged: 1},
		{name: "#97: a pass after it, the condition saying so already",
			was: [2]string{c4v1beta1.ReasonUnsupportedFramework, refusal}},
		{name: "#97: the Framework changed, and the message with it",
			was: [2]string{c4v1beta1.ReasonUnsupportedFramework, "python-strict does not support rust-cli"}, logged: 1},
		{name: "#97: its pull request was open before", was: [2]string{c4v1beta1.ReasonPullRequestOpen, refusal},
			logged: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			container := &c4v1beta1.Container{ObjectMeta: objectMeta("apps", "orders-api"),
				Spec: c4v1beta1.ContainerSpec{Adopt: true, FrameworkRef: c4v1beta1.LocalRef{Name: "go-cli"},
					StandardRef: &c4v1beta1.LocalRef{Name: standard.Name}},
				Status: c4v1beta1.ContainerStatus{Phase: c4v1beta1.ContainerReady}}
			if tt.was[0] != "" {
				setCondition(&container.Status.Conditions, 1, c4v1beta1.ConditionStandardUpdated,
					metav1.ConditionFalse, tt.was[0], tt.was[1])
			}
			status := container.Status.DeepCopy()
			var lines []string
			ctx := logr.NewContext(context.Background(), funcr.New(func(_, args string) {
				lines = append(lines, args)
			}, funcr.Options{}))

			if (&ContainerReconciler{}).standardFits(ctx, container, status, standard) {
				t.Fatal("standardFits() = true for a Framework the Standard doesn't list")
			}
			cond := apimeta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionStandardUpdated)
			if cond == nil || cond.Reason != c4v1beta1.ReasonUnsupportedFramework || cond.Message != refusal {
				t.Errorf("StandardUpdated is %+v, want %s: %s", cond, c4v1beta1.ReasonUnsupportedFramework, refusal)
			}
			if len(lines) != tt.logged {
				t.Errorf("logged %d lines, want %d: %q", len(lines), tt.logged, lines)
			}
		})
	}
}
