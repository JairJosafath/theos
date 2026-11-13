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
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
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
	var adoption c4v1beta1.StandardAdoptionStatus
	Eventually(func(g Gomega) {
		container := getContainer(g, ns)
		g.Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
		g.Expect(container.Status.Standard).To(BeNil(), "the Standard is recorded when its files are on main")
		g.Expect(container.Status.StandardAdoption).NotTo(BeNil())
		adoption = *container.Status.StandardAdoption
		g.Expect(adoption.PullRequest).NotTo(BeZero())
		g.Expect(adoption.Stopped).To(BeEmpty())
		cond := standardCondition(g, container)
		g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonPullRequestOpen))
		g.Expect(cond.Message).To(Equal("Pull request " + adoption.PullRequestURL + " brings the files of " +
			"python-strict at v1.0.0 into the repository; a person merges it, or closes it to leave the repository " +
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
