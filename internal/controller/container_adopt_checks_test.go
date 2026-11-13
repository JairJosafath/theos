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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// #95 (docs/plans/adopt-existing.md, step 2): a repository adopted with no
// Standard is checked by its own command and its own CI, which the Container
// names, and agents work on it only once main requires those checks.
var _ = Describe("Container adoption with the repository's own check", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	// adopted seeds a Rust service's repository, creates orders-api adopting
	// it, changed by mutate, and waits for it to be Ready.
	adopted := func(mutate func(*c4v1beta1.Container)) {
		GinkgoHelper()
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", scaffold.Tree{
			scaffold.IdentityPath:      bareIdentity(ns),
			"Cargo.toml":               {Data: []byte("[package]\nname = \"orders-api\"\n")},
			".github/workflows/ci.yml": {Data: []byte("name: ci\n")},
		})
		mustCreate(newSystem(ns, "webshop-core"), createBareAdoption(ns, mutate))
		eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
	}
	// protected waits for the Container's Protected condition to be status,
	// and returns it.
	protected := func(status metav1.ConditionStatus) *metav1.Condition {
		GinkgoHelper()
		var cond *metav1.Condition
		Eventually(func(g Gomega) {
			var container c4v1beta1.Container
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
			cond = condition(container.Status.Conditions, c4v1beta1.ConditionProtected)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(status))
			g.Expect(cond.ObservedGeneration).To(Equal(container.Generation))
		}).Should(Succeed())
		return cond
	}
	mainReports := func(contexts map[string]string) {
		gitFake.SetCheckContexts(ns, "orders-api", ports.DefaultBranch, "success", contexts)
	}

	It("takes checkCommand and checkContexts only on a Container that adopts with no Standard", func() {
		ok := createBareAdoption(ns, func(c *c4v1beta1.Container) {
			c.Spec.CheckCommand, c.Spec.CheckContexts = "cargo test --all", []string{"ci / test*", "ci / lint*"}
		})
		mustCreate(ok)

		const only = "checkCommand and checkContexts are for a Container that adopts its repository with no Standard"
		refused := func(name string, mutate func(*c4v1beta1.Container)) error {
			c := newContainer(ns, name, "webshop-core", c4v1beta1.ContainerTypeService)
			mutate(c)
			return k8sClient.Create(ctx, c)
		}
		Expect(refused("scaffolded", func(c *c4v1beta1.Container) { c.Spec.CheckCommand = "npm test" })).
			To(MatchError(ContainSubstring(only)), "a Container the factory scaffolds has its Standard's check")
		Expect(refused("with-standard", func(c *c4v1beta1.Container) {
			c.Spec.Adopt, c.Spec.StandardRef = true, &c4v1beta1.LocalRef{Name: "python-strict"}
			c.Spec.CheckContexts = []string{"ci / test*"}
		})).To(MatchError(ContainSubstring(only)))
		Expect(refused("padded", func(c *c4v1beta1.Container) {
			c.Spec.FrameworkRef, c.Spec.ComputeRef, c.Spec.DeploymentRef = c4v1beta1.LocalRef{}, nil, nil
			c.Spec.Adopt, c.Spec.CheckCommand = true, " cargo test"
		})).To(MatchError(ContainSubstring("checkCommand starts and ends with a word")))
		Expect(refused("no-contexts", func(c *c4v1beta1.Container) {
			c.Spec.FrameworkRef, c.Spec.ComputeRef, c.Spec.DeploymentRef = c4v1beta1.LocalRef{}, nil, nil
			c.Spec.Adopt, c.Spec.CheckContexts = true, []string{""}
		})).To(MatchError(ContainSubstring("should be at least 1 chars long")))
	})

	It("protects main with the Container's contexts once its head reports them, and never before", func() {
		adopted(func(c *c4v1beta1.Container) { c.Spec.CheckContexts = []string{"ci / test*"} })

		By("saying NoMatchingCheck while main reports nothing, with no rule on the branch")
		cond := protected(metav1.ConditionFalse)
		Expect(cond.Reason).To(Equal(model.ReasonNoMatchingCheck))
		Expect(cond.Message).To(And(ContainSubstring("main's head reports no check matching ci / test*"),
			ContainSubstring("it reports no check at all")))
		Expect(gitFake.ProtectedBranches(ns, "orders-api")).To(BeEmpty())

		By("naming what main does report, so a person can name their own")
		mainReports(map[string]string{"ci / docs (push)": "success", "release / build (push)": "success"})
		Eventually(func() string { return protected(metav1.ConditionFalse).Message }).Should(ContainSubstring(
			"it reports ci / docs (push) and release / build (push): name the repository's own in spec.checkContexts"))
		Expect(gitFake.ProtectedBranches(ns, "orders-api")).To(BeEmpty(),
			"a pattern nothing reports would block every merge")

		By("protecting main once its head reports the check, with the workflows out of an agent's reach")
		mainReports(map[string]string{"ci / docs (push)": "success", "ci / test (push)": "success"})
		cond = protected(metav1.ConditionTrue)
		Expect(cond.Reason).To(Equal(model.ReasonProtected))
		Expect(cond.Message).To(Equal(
			"main requires the checks matching ci / test*, which its head reports (ci / test (push))"))
		Expect(gitFake.ProtectedBranches(ns, "orders-api")).To(Equal([]string{"main"}))
		rule, tags := gitFake.BranchRule(ns, "orders-api")
		Expect(rule.StatusContexts).To(Equal([]string{"ci / test*"}))
		Expect(rule.ProtectedPaths).To(And(HaveLen(4), ContainElements(".github/workflows/", scaffold.IdentityPath,
			".devcontainer/")), "the CI system's own path, where it looks second, and the operator's")
		Expect(tags).To(ContainElement(model.ReleaseTags))
		Expect(gitFake.Repo(ns, "orders-api").Commits).To(HaveLen(1), "an adopted repository is never written")

		By("staying protected when main moves and its new head has no result yet, as after every merge")
		mainReports(nil)
		Consistently(func(g Gomega) {
			var container c4v1beta1.Container
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
			g.Expect(condition(container.Status.Conditions, c4v1beta1.ConditionProtected)).To(
				HaveField("Status", metav1.ConditionTrue))
		}, "3s").Should(Succeed())
		mainReports(map[string]string{"ci / docs (push)": "success", "ci / test (push)": "success"})

		By("following a change of the Container's contexts")
		var container c4v1beta1.Container
		Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
		Expect(patch(&container, func() { container.Spec.CheckContexts = []string{"ci / test*", "ci / docs*"} })).
			To(Succeed())
		Eventually(func() []string {
			rule, _ := gitFake.BranchRule(ns, "orders-api")
			return rule.StatusContexts
		}).Should(Equal([]string{"ci / test*", "ci / docs*"}))
		protected(metav1.ConditionTrue)
	})

	It("has no Protected condition on a Container with a Standard", func() {
		readyContainer(ns)
		var container c4v1beta1.Container
		Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
		Expect(condition(container.Status.Conditions, c4v1beta1.ConditionProtected)).To(BeNil())
		Expect(gitFake.ProtectedBranches(ns, "orders-api")).To(Equal([]string{"main"}))
	})

	It("starts no Task while main isn't protected, and then builds an issue with the Container's command", func() {
		adopted(func(c *c4v1beta1.Container) { c.Spec.CheckCommand = "cargo test --all" })
		crewAgents(ns, 3)
		n := gitFake.AddIssue(ns, "orders-api", "Export orders", clearIssue, []string{model.LabelReady})
		name := fmt.Sprintf("orders-api-%d", n)
		mustCreate(newCrew(ns))

		By("waiting, the Crew saying why, with the issue untouched")
		protected(metav1.ConditionFalse)
		Eventually(func(g Gomega) {
			cond := condition(getCrew(g, ns).Status.Conditions, ConditionReady)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(model.ReasonNotProtected))
			g.Expect(cond.Message).To(And(
				ContainSubstring("Waiting for the main of Container orders-api to be protected"),
				ContainSubstring("main's head reports no check matching check*")))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			var tasks c4v1beta1.TaskList
			g.Expect(k8sClient.List(ctx, &tasks)).To(Succeed())
			for _, t := range tasks.Items {
				g.Expect(t.Namespace).NotTo(Equal(ns), "Task %s started on an unprotected repository", t.Name)
			}
			g.Expect(gitFake.Issue(ns, "orders-api", n).Labels).To(Equal([]string{model.LabelReady}))
		}, "3s").Should(Succeed())

		By("starting once main reports its check, with the Container's command and the workflows protected")
		mainReports(map[string]string{"check / check (push)": "success"})
		protected(metav1.ConditionTrue)
		Eventually(func(g Gomega) {
			env := sandboxEnv(g, ns, name+"-r1-implement")
			g.Expect(env).To(ContainElement(map[string]any{"name": agentrunner.EnvCheckCommand,
				"value": "cargo test --all"}))
			g.Expect(env).To(ContainElement(And(HaveKeyWithValue("name", agentrunner.EnvProtectedPaths),
				HaveKeyWithValue("value", ContainSubstring(".github/workflows/")))))
		}).Should(Succeed())

		By("merging the change after the repository's own checks and the review, as on any Container")
		head, err := gitFake.PushBranch(ctx, ports.Repository{Owner: ns, Name: "orders-api"},
			gitFake.MainHead(ns, "orders-api").Hash, "task/"+name,
			scaffold.Tree{"src/export.rs": {Data: []byte("pub fn export() {}\n")}}, nil, "", ports.Signature{})
		Expect(err).NotTo(HaveOccurred())
		finishSandbox(ns, name+"-r1-implement", name,
			&agentrunner.Result{Role: "implement", Commit: head, Summary: "Exported the orders."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, name).Status.Phase).To(Equal(c4v1beta1.TaskChecking))
		}).Should(Succeed())
		gitFake.SetCheckContexts(ns, "orders-api", head, "success",
			map[string]string{"check / check (pull_request)": "success"})
		finishSandbox(ns, name+"-r1-review-a1", name,
			&agentrunner.Result{Role: "review", Verdict: model.VerdictApprove, Summary: "Fine."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, name).Status.Phase).To(Equal(c4v1beta1.TaskMerged))
		}).Should(Succeed())
		Expect(gitFake.MainFiles(ns, "orders-api")).To(HaveKey("src/export.rs"))
	})

	It("has a Task made by hand wait for the protection, where one on a Container that adopts nothing fails", func() {
		adopted(nil)
		crewAgents(ns, 3)
		protected(metav1.ConditionFalse)
		mustCreate(newTask(ns, "by-hand", "Export orders"))
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "by-hand")
			g.Expect(task.Status.Phase).To(Equal(c4v1beta1.TaskPending))
			g.Expect(condition(task.Status.Conditions, ConditionProgressing)).To(And(
				HaveField("Reason", model.ReasonNotProtected),
				HaveField("Message", ContainSubstring("Waiting for the main of Container orders-api to be protected"))))
		}).Should(Succeed())

		mainReports(map[string]string{"check / check (push)": "success"})
		Eventually(func(g Gomega) { sandboxExists(g, ns, "by-hand-r1-implement") }).Should(Succeed())
	})
})
