/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package e2e

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// scaffoldCommits counts the commits on main of apps/<repo> that scaffolded it.
func scaffoldCommits(repo string) int {
	GinkgoHelper()
	var commits []struct {
		Commit struct {
			Message string `json:"message"`
		} `json:"commit"`
	}
	giteaGet(fmt.Sprintf("/api/v1/repos/apps/%s/commits?sha=main&limit=50", repo), &commits)
	n := 0
	for _, c := range commits {
		if strings.Contains(c.Commit.Message, "C4-Factory-Container:") {
			n++
		}
	}
	return n
}

// The Briefs milestone (plans/done/brief.md, step 8): a Brief taken through two
// rounds by the stand-in planner, which plans from the catalog, so the whole
// loop runs without a Claude token: the Plan Task's sandbox, the plan on the
// brief repository, the check, the apply, the scaffold, the Crew and the
// issues. `make e2e` builds everything it uses.
var _ = Describe("Briefs", Ordered, func() {
	var ns, name string
	var container c4v1alpha1.Container

	BeforeAll(func() {
		ns = newNamespace("e2e-brief")
		name = "e2e-" + rand.String(5)
		DeferCleanup(func() {
			giteaDelete("/api/v1/repos/apps/" + name)
			giteaDelete("/api/v1/repos/apps/brief-" + name)
		})
		kubectl("apply", "-n", ns, "-f", "test/e2e/testdata/apps/factory.yaml")
		copySecret(apps, ns, "gitea-credentials")
		Expect(k8s.Create(ctx, &c4v1alpha1.Project{ObjectMeta: objMeta(ns, ns)})).To(Succeed())
		brief := &c4v1alpha1.Brief{ObjectMeta: objMeta(ns, name), Spec: c4v1alpha1.BriefSpec{
			Idea: "A small tool the e2e test asks for, planned by the stand-in.",
			Agents: c4v1alpha1.BriefAgents{
				Planner: ref("stand-in"), Implementer: ref("stand-in"), Reviewer: ref("stand-in"),
			},
		}}
		Expect(k8s.Create(ctx, brief)).To(Succeed())
		// Before the repositories go: the Brief's finalizer removes its bot.
		DeferCleanup(func() {
			_ = k8s.Delete(ctx, brief)
			Eventually(func() bool {
				return apierrors.IsNotFound(k8s.Get(ctx, client.ObjectKeyFromObject(brief), brief))
			}).WithTimeout(2 * time.Minute).Should(BeTrue())
		})
	})

	It("plans the idea: a Container, its Crew and an issue the Crew picks up", func() {
		var brief c4v1alpha1.Brief
		Eventually(func(g Gomega) {
			g.Expect(k8s.Get(ctx, key(ns, name), &brief)).To(Succeed())
			g.Expect(brief.Status.Phase).NotTo(BeElementOf(c4v1alpha1.BriefFailed, c4v1alpha1.BriefNeedsInput),
				"status: %+v", brief.Status)
			g.Expect(brief.Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		Expect(brief.Status.Round).To(Equal(int32(1)))
		Expect(brief.Status.Created).To(ContainElement(c4v1alpha1.BriefObject{Kind: "Container", Name: name}))
		Expect(brief.Status.Choices).NotTo(BeEmpty())
		Expect(brief.Status.Issues).To(ConsistOf(And(
			HaveField("Repository", "apps/"+name), HaveField("Round", int32(1)),
			HaveField("Title", "Stand-in: start "+name))))
		issue := brief.Status.Issues[0].Number

		Expect(k8s.Get(ctx, key(ns, name), &container)).To(Succeed())
		Expect(container.Status.Phase).To(Equal(c4v1alpha1.ContainerReady))
		Expect(container.Spec.FrameworkRef.Name).To(Equal("hello"))
		Expect(scaffoldCommits(name)).To(Equal(1))

		By("the Crew starting a Task for the issue")
		var crew c4v1alpha1.Crew
		Expect(k8s.Get(ctx, key(ns, name), &crew)).To(Succeed())
		Eventually(func(g Gomega) {
			var tasks c4v1alpha1.TaskList
			g.Expect(k8s.List(ctx, &tasks, client.InNamespace(ns), client.MatchingLabels{
				"c4.factory.io/crew": name, "c4.factory.io/issue": fmt.Sprint(issue),
			})).To(Succeed())
			g.Expect(tasks.Items).To(HaveLen(1))
		}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		// The rest is the Crew's loop, which the Agent factory milestone covers.
		kubectl("-n", ns, "patch", "crew", name, "--type=merge", "-p", `{"spec":{"paused":true}}`)
	})

	It("plans a follow-up prompt as an issue, and changes no scaffolded Container", func() {
		prompt := "Support ValidatingAdmissionPolicy files first."
		kubectl("-n", ns, "patch", "brief", name, "--type=merge", "-p",
			fmt.Sprintf(`{"spec":{"prompts":[%q]}}`, prompt))

		var brief c4v1alpha1.Brief
		Eventually(func(g Gomega) {
			g.Expect(k8s.Get(ctx, key(ns, name), &brief)).To(Succeed())
			g.Expect(brief.Status.Phase).NotTo(BeElementOf(c4v1alpha1.BriefFailed, c4v1alpha1.BriefNeedsInput),
				"status: %+v", brief.Status)
			g.Expect(brief.Status.Round).To(Equal(int32(2)))
			g.Expect(brief.Status.Phase).To(Equal(c4v1alpha1.BriefPlanned))
		}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
		Expect(brief.Status.Issues).To(ContainElement(And(
			HaveField("Repository", "apps/"+name), HaveField("Round", int32(2)),
			HaveField("Title", "Stand-in: "+prompt))))

		var containers c4v1alpha1.ContainerList
		Expect(k8s.List(ctx, &containers, client.InNamespace(ns))).To(Succeed())
		Expect(containers.Items).To(HaveLen(1))
		after := containers.Items[0]
		Expect(after.Generation).To(Equal(container.Generation), "round 2 changed the Container's spec")
		Expect(after.Status.ScaffoldedFrom).To(Equal(container.Status.ScaffoldedFrom))
		Expect(scaffoldCommits(name)).To(Equal(1))
	})
})
