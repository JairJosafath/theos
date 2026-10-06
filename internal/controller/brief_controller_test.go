/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

func newBrief(ns, name string, prompts ...string) *c4v1alpha1.Brief {
	return &c4v1alpha1.Brief{
		ObjectMeta: objectMeta(ns, name),
		Spec:       c4v1alpha1.BriefSpec{Idea: "A linter for CEL rules in CRDs.", Prompts: prompts},
	}
}

var _ = Describe("Brief", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("reaches Pending, with the default Agents and GitProvider", func() {
		createFactory(ns)
		brief := newBrief(ns, "cel-lens")
		mustCreate(brief)
		Expect(brief.Spec.Agents).To(Equal(c4v1alpha1.BriefAgents{
			Planner:     c4v1alpha1.LocalRef{Name: "planner"},
			Implementer: c4v1alpha1.LocalRef{Name: "implementer"},
			Reviewer:    c4v1alpha1.LocalRef{Name: "reviewer"},
			Designer:    c4v1alpha1.LocalRef{Name: "designer"},
		}))
		Expect(brief.Spec.GitProviderRef.Name).To(Equal("gitea"))
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key(ns, "cel-lens"), brief)).To(Succeed())
			g.Expect(brief.Status.Phase).To(Equal(c4v1alpha1.BriefPending))
		}).Should(Succeed())
	})

	It("keeps Agents it is given", func() {
		brief := newBrief(ns, "own-agents")
		brief.Spec.Agents.Planner.Name = "opus"
		mustCreate(brief)
		Expect(brief.Spec.Agents.Planner.Name).To(Equal("opus"))
		Expect(brief.Spec.Agents.Reviewer.Name).To(Equal("reviewer"))
	})

	Describe("repository", func() {
		const repo = "brief-cel-lens"
		file := func(name string) string { return string(gitFake.mainFiles(ns, repo)[name].Data) }

		It("gets the brief, the model, the catalog and the guide on main, once", func() {
			readyContainer(ns)
			gitFake.addIssue(ns, "orders-api", "Add a health check", "", []string{"status/ready"})
			brief := newBrief(ns, "cel-lens")
			mustCreate(brief)

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, key(ns, "cel-lens"), brief)).To(Succeed())
				g.Expect(brief.Status.RepositoryURL).To(Equal(
					"http://gitea.gitea.svc.cluster.local:3000/" + ns + "/" + repo))
			}).Should(Succeed())
			Expect(brief.Status.Phase).To(Equal(c4v1alpha1.BriefPending))
			Expect(condition(brief.Status.Conditions, c4v1alpha1.ConditionNeedsInput)).
				To(HaveField("Status", metav1.ConditionFalse))

			Expect(gitFake.commitCount(ns, repo)).To(Equal(1))
			Expect(file(model.BriefFile)).To(ContainSubstring("## Idea (**new**)\n\nA linter for CEL rules in CRDs.\n"))
			Expect(file(model.PlannerFile)).To(ContainSubstring("Plan the story kinds only"))
			Expect(file(model.WorkspaceModelFile)).To(And(
				ContainSubstring("kind: Project\nmetadata:\n    name: "+ns),
				ContainSubstring("kind: Container\nmetadata:\n    name: orders-api"),
				ContainSubstring("standardRef:\n        name: python-strict")))
			Expect(file(model.WorkspaceCatalogFile)).To(And(
				ContainSubstring("- name: python-fastapi"), ContainSubstring("- name: python-strict")))
			Expect(file(model.WorkspaceContainersFile)).To(And(
				ContainSubstring("- name: orders-api\n  repository: http://gitea.gitea.svc.cluster.local:3000/"+ns+"/orders-api"),
				ContainSubstring("'#1 Add a health check'")))

			By("letting only the operator push main, and the bot write the repository")
			rule, _ := gitFake.branchRule(ns, repo)
			Expect(rule.OperatorPushes).To(BeTrue())
			Expect(gitFake.agentUser(ns, repo)).To(Equal(model.AgentUsername(ns, repo)))
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, key(ns, "brief-cel-lens-git"), secret)).To(Succeed())
			Expect(controlledBy(secret, brief)).To(BeTrue())
			Expect(secret.Data["username"]).To(BeEquivalentTo(model.AgentUsername(ns, repo)))
			Expect(secret.Data["token"]).NotTo(BeEmpty())
			Expect(secret.Labels).To(HaveKeyWithValue(c4v1alpha1.LabelBrief, "cel-lens"))

			By("writing nothing new when nothing changed")
			token := string(secret.Data["token"])
			reads := gitFake.historyCount(ns, repo)
			mustCreate(&c4v1alpha1.GitProvider{ObjectMeta: objectMeta(ns, "another"), Spec: c4v1alpha1.GitProviderSpec{
				URL: "http://gitea.invalid", Owner: ns, CredentialsSecretRef: c4v1alpha1.LocalRef{Name: "none"}}})
			Eventually(func() int { return gitFake.historyCount(ns, repo) }).Should(BeNumerically(">", reads),
				"the Brief was not reconciled again")
			Consistently(func() int { return gitFake.commitCount(ns, repo) }, "1s").Should(Equal(1))
			Expect(k8sClient.Get(ctx, key(ns, "brief-cel-lens-git"), secret)).To(Succeed())
			Expect(string(secret.Data["token"])).To(Equal(token), "the bot's token was rotated")

			By("writing a new prompt as one more commit")
			Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Support VAP files first."} })).To(Succeed())
			Eventually(func() int { return gitFake.commitCount(ns, repo) }).Should(Equal(2))
			Expect(file(model.BriefFile)).To(ContainSubstring("1. (**new**) Support VAP files first.\n"))
		})

		It("protects and gives its bot the repository on a GitProvider it moves to", func() {
			createFactory(ns)
			brief := newBrief(ns, "cel-lens")
			mustCreate(brief)
			Eventually(func() int { return gitFake.commitCount(ns, repo) }).Should(Equal(1))

			moved := ns + "-moved"
			mustCreate(&c4v1alpha1.GitProvider{ObjectMeta: objectMeta(ns, "moved"), Spec: c4v1alpha1.GitProviderSpec{
				URL: "http://gitea.gitea.svc.cluster.local:3000", Owner: moved,
				CredentialsSecretRef: c4v1alpha1.LocalRef{Name: "gitea-credentials"}}})
			Expect(patch(brief, func() { brief.Spec.GitProviderRef.Name = "moved" })).To(Succeed())
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, key(ns, "cel-lens"), brief)).To(Succeed())
				g.Expect(brief.Status.RepositoryURL).To(HaveSuffix("/" + moved + "/" + repo))
			}).Should(Succeed())
			Expect(gitFake.commitCount(moved, repo)).To(Equal(1))
			rule, _ := gitFake.branchRule(moved, repo)
			Expect(rule.OperatorPushes).To(BeTrue())
			Expect(gitFake.agentUser(moved, repo)).To(Equal(model.AgentUsername(ns, repo)))
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, key(ns, "brief-cel-lens-git"), secret)).To(Succeed())
			Expect(string(secret.Data["repository"])).To(ContainSubstring("/" + moved + "/" + repo))
		})

		It("won't share a Container's repository", func() {
			createFactory(ns)
			mustCreate(newSystem(ns, "webshop-core"),
				newContainer(ns, repo, "webshop-core", c4v1alpha1.ContainerTypeService), newBrief(ns, "cel-lens"))
			brief := &c4v1alpha1.Brief{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, key(ns, "cel-lens"), brief)).To(Succeed())
				g.Expect(brief.Status.Phase).To(Equal(c4v1alpha1.BriefNeedsInput))
				g.Expect(condition(brief.Status.Conditions, c4v1alpha1.ConditionNeedsInput)).
					To(HaveField("Message", HavePrefix("Container "+repo+" has the repository the Brief needs")))
			}).Should(Succeed())
			Expect(gitFake.agentUser(ns, repo)).To(BeEmpty())
		})

		It("waits for a missing GitProvider, naming it", func() {
			brief := newBrief(ns, "cel-lens")
			brief.Spec.GitProviderRef.Name = "forgejo"
			mustCreate(brief)
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, key(ns, "cel-lens"), brief)).To(Succeed())
				g.Expect(brief.Status.Phase).To(Equal(c4v1alpha1.BriefNeedsInput))
				cond := condition(brief.Status.Conditions, c4v1alpha1.ConditionNeedsInput)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Message).To(HavePrefix("GitProvider forgejo is missing"))
			}).Should(Succeed())
			Expect(gitFake.commitCount(ns, repo)).To(BeZero())

			By("going on once it exists, waiting for its Secret first")
			mustCreate(&c4v1alpha1.GitProvider{ObjectMeta: objectMeta(ns, "forgejo"), Spec: c4v1alpha1.GitProviderSpec{
				URL: "http://forgejo.invalid", Owner: ns, CredentialsSecretRef: c4v1alpha1.LocalRef{Name: "forgejo-token"}}})
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, key(ns, "cel-lens"), brief)).To(Succeed())
				g.Expect(condition(brief.Status.Conditions, c4v1alpha1.ConditionNeedsInput)).
					To(HaveField("Message", HavePrefix("Secret forgejo-token of GitProvider forgejo is missing")))
			}).Should(Succeed())
			mustCreate(&corev1.Secret{ObjectMeta: objectMeta(ns, "forgejo-token"),
				StringData: map[string]string{"username": "c4admin", "token": "t"}})
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, key(ns, "cel-lens"), brief)).To(Succeed())
				g.Expect(brief.Status.Phase).To(Equal(c4v1alpha1.BriefPending))
				g.Expect(brief.Status.RepositoryURL).To(Equal("http://forgejo.invalid/" + ns + "/" + repo))
				g.Expect(condition(brief.Status.Conditions, c4v1alpha1.ConditionNeedsInput)).
					To(HaveField("Status", metav1.ConditionFalse))
			}).Should(Succeed())
			Expect(gitFake.commitCount(ns, repo)).To(Equal(1))
		})
	})

	Describe("idea", func() {
		It("is required", func() {
			Expect(k8sClient.Create(ctx, &c4v1alpha1.Brief{ObjectMeta: objectMeta(ns, "no-idea")})).
				To(MatchError(ContainSubstring("spec.idea")))
		})

		It("isn't empty", func() {
			brief := newBrief(ns, "empty")
			brief.Spec.Idea = ""
			Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring("spec.idea")))
		})

		It("takes 4000 characters and no more", func() {
			brief := newBrief(ns, "long")
			brief.Spec.Idea = strings.Repeat("a", 4000)
			mustCreate(brief)
			brief = newBrief(ns, "too-long")
			brief.Spec.Idea = strings.Repeat("a", 4001)
			Expect(k8sClient.Create(ctx, brief)).To(MatchError(ContainSubstring("spec.idea")))
		})

		It("can't change", func() {
			brief := newBrief(ns, "fixed")
			mustCreate(brief)
			Expect(patch(brief, func() { brief.Spec.Idea = "Another app." })).
				To(MatchError(ContainSubstring("idea is immutable")))
		})
	})

	Describe("prompts", func() {
		const appendOnly = "prompts are append-only"

		It("take an appended prompt", func() {
			brief := newBrief(ns, "first")
			mustCreate(brief)
			Expect(patch(brief, func() { brief.Spec.Prompts = []string{"Support VAP files."} })).To(Succeed())
			Expect(patch(brief, func() { brief.Spec.Prompts = append(brief.Spec.Prompts, "And a CLI.") })).To(Succeed())
			Expect(brief.Spec.Prompts).To(Equal([]string{"Support VAP files.", "And a CLI."}))
		})

		It("refuse a changed earlier prompt", func() {
			brief := newBrief(ns, "changed", "one", "two")
			mustCreate(brief)
			Expect(patch(brief, func() { brief.Spec.Prompts = []string{"one", "2", "three"} })).
				To(MatchError(ContainSubstring(appendOnly)))
		})

		It("refuse a removed or reordered prompt", func() {
			brief := newBrief(ns, "removed", "one", "two")
			mustCreate(brief)
			Expect(patch(brief, func() { brief.Spec.Prompts = []string{"one"} })).
				To(MatchError(ContainSubstring(appendOnly)))
			Expect(patch(brief, func() { brief.Spec.Prompts = []string{"two", "one"} })).
				To(MatchError(ContainSubstring(appendOnly)))
			Expect(patch(brief, func() { brief.Spec.Prompts = nil })).
				To(MatchError(ContainSubstring(appendOnly)))
		})

		It("refuse an empty prompt", func() {
			Expect(k8sClient.Create(ctx, newBrief(ns, "empty", ""))).
				To(MatchError(ContainSubstring("spec.prompts[0]")))
		})

		It("may drop an empty list", func() {
			brief := newBrief(ns, "empty-list")
			mustCreate(brief)
			Expect(k8sClient.Patch(ctx, brief, client.RawPatch(types.MergePatchType,
				[]byte(`{"spec":{"prompts":[]}}`)))).To(Succeed())
			Expect(k8sClient.Patch(ctx, brief, client.RawPatch(types.MergePatchType,
				[]byte(`{"spec":{"prompts":null}}`)))).To(Succeed())
		})

		It("take 50 prompts of 2000 characters and no more", func() {
			brief := newBrief(ns, "full", make50(strings.Repeat("p", 2000))...)
			mustCreate(brief)
			Expect(k8sClient.Create(ctx, newBrief(ns, "too-many", append(make50("p"), "p")...))).
				To(MatchError(ContainSubstring("spec.prompts")))
			Expect(k8sClient.Create(ctx, newBrief(ns, "too-long", strings.Repeat("p", 2001)))).
				To(MatchError(ContainSubstring("spec.prompts[0]")))
		})
	})
})

// make50 returns 50 copies of prompt.
func make50(prompt string) []string {
	prompts := make([]string, 50)
	for i := range prompts {
		prompts[i] = prompt
	}
	return prompts
}
