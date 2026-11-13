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
	"maps"

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

// #98 (docs/plans/adopt-existing.md, step 5): a Crew's first issue on a
// repository it adopted with no Standard is its own, filed once: write
// AGENTS.md and say what you found.
var _ = Describe("Crew onboarding", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	// adopted seeds a repository with files beside its identity, has
	// orders-api adopt it and waits for it to be Ready.
	adopted := func(files scaffold.Tree) {
		GinkgoHelper()
		tree := scaffold.Tree{scaffold.IdentityPath: bareIdentity(ns), "Cargo.toml": {Data: []byte("[package]\n")}}
		maps.Copy(tree, files)
		gitFake.SeedRepoFiles(ns, "orders-api", "Import orders-api\n", tree)
		mustCreate(newSystem(ns, "webshop-core"), createBareAdoption(ns, nil))
		eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
	}
	// protect has main report its check, and waits for the Container to say
	// main is protected.
	protect := func() {
		GinkgoHelper()
		gitFake.SetCheckContexts(ns, "orders-api", ports.DefaultBranch, "success",
			map[string]string{"check / check (push)": "success"})
		Eventually(func(g Gomega) {
			var container c4v1beta1.Container
			g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
			g.Expect(condition(container.Status.Conditions, c4v1beta1.ConditionProtected)).To(
				HaveField("Status", metav1.ConditionTrue))
		}).Should(Succeed())
	}
	onboarding := func() []fake.Issue {
		var out []fake.Issue
		for _, issue := range gitFake.OpenIssues(ns, "orders-api") {
			if model.HasOnboardingMarker(issue.Body) {
				out = append(out, issue)
			}
		}
		return out
	}

	It("files one issue once main is protected, builds it, and files no second", func() {
		adopted(nil)
		crewAgents(ns, 3)
		mustCreate(newCrew(ns))

		By("filing nothing while no agent may work on the repository")
		Consistently(func() []fake.Issue { return gitFake.OpenIssues(ns, "orders-api") }, "2s").Should(BeEmpty())

		By("filing the issue, ready and with criteria, once main is protected")
		protect()
		var issue fake.Issue
		Eventually(func(g Gomega) {
			issues := onboarding()
			g.Expect(issues).To(HaveLen(1))
			issue = issues[0]
			g.Expect(getCrew(g, ns).Status.Onboarding).To(HaveField("Issue", int32(issue.Number)))
		}).Should(Succeed())
		Expect(issue.Title).To(Equal("Write AGENTS.md for this repository, and say what you found"))
		Expect(issue.Body).To(And(ContainSubstring("This repository has no `AGENTS.md`. Write it."),
			ContainSubstring("## Acceptance criteria"), ContainSubstring("Change no other file.")))

		By("working it like any ready issue")
		name := fmt.Sprintf("orders-api-%d", issue.Number)
		Eventually(func(g Gomega) { sandboxExists(g, ns, name+"-r1-implement") }).Should(Succeed())
		Expect(gitFake.Issue(ns, "orders-api", issue.Number).Labels).To(ContainElement(model.LabelOnboarding))
		head, err := gitFake.PushBranch(ctx, ports.Repository{Owner: ns, Name: "orders-api"},
			gitFake.MainHead(ns, "orders-api").Hash, "task/"+name,
			scaffold.Tree{model.AgentsGuidePath: {Data: []byte("# orders-api\n\nBuild: `cargo build`.\n")}}, nil, "",
			ports.Signature{})
		Expect(err).NotTo(HaveOccurred())
		finishSandbox(ns, name+"-r1-implement", name,
			&agentrunner.Result{Role: "implement", Commit: head, Summary: "Wrote the guide. Nothing found."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, name).Status.Phase).To(Equal(c4v1beta1.TaskChecking))
		}).Should(Succeed())
		gitFake.SetChecks(ns, "orders-api", head, "success")
		finishSandbox(ns, name+"-r1-review-a1", name,
			&agentrunner.Result{Role: "review", Verdict: model.VerdictApprove, Summary: "Each command runs."}, "")
		Eventually(func(g Gomega) {
			g.Expect(getTask(g, ns, name).Status.Phase).To(Equal(c4v1beta1.TaskMerged))
			g.Expect(gitFake.Issue(ns, "orders-api", issue.Number).State).To(Equal("closed"))
		}).Should(Succeed())
		Expect(gitFake.MainFiles(ns, "orders-api")).To(HaveKey(model.AgentsGuidePath))

		By("filing no second one once the first is closed")
		Consistently(func(g Gomega) {
			g.Expect(onboarding()).To(BeEmpty())
			g.Expect(getCrew(g, ns).Status.Onboarding).To(HaveField("Issue", int32(issue.Number)))
		}, "3s").Should(Succeed())
	})

	It("asks to check and complete the guide a repository already has", func() {
		adopted(scaffold.Tree{model.AgentsGuidePath: {Data: []byte("# orders-api\n")}})
		crewAgents(ns, 3)
		mustCreate(newCrew(ns))
		protect()
		Eventually(func(g Gomega) {
			issues := onboarding()
			g.Expect(issues).To(HaveLen(1))
			g.Expect(issues[0].Title).To(Equal("Check and complete AGENTS.md, and say what you found"))
		}).Should(Succeed())
	})

	It("records the open issue it finds, whose filing its status lost, instead of filing a second", func() {
		adopted(nil)
		crewAgents(ns, 3)
		_, body := model.OnboardingIssue(false)
		n := gitFake.AddIssue(ns, "orders-api", "Write AGENTS.md for this repository, and say what you found", body,
			[]string{model.LabelOnboarding, model.LabelNeedsHuman})
		mustCreate(newCrew(ns))
		protect()
		Eventually(func(g Gomega) {
			g.Expect(getCrew(g, ns).Status.Onboarding).To(HaveField("Issue", int32(n)))
		}).Should(Succeed())
		Consistently(func() []fake.Issue { return onboarding() }, "2s").Should(HaveLen(1))
	})

	It("files none on a Container the factory scaffolded, which has its Standard's guides", func() {
		readyContainer(ns)
		crewAgents(ns, 3)
		mustCreate(newCrew(ns))
		Eventually(func(g Gomega) { g.Expect(getCrew(g, ns).Status.LastPolled).NotTo(BeNil()) }).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.OpenIssues(ns, "orders-api")).To(BeEmpty())
			g.Expect(getCrew(g, ns).Status.Onboarding).To(BeNil())
		}, "3s").Should(Succeed())
	})
})
