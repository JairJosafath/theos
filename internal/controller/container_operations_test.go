/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"maps"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// opsFramework is a Framework that renders infra/operations.ts from the block,
// as an AWS Framework does: python-fastapi's template with the file added.
const opsFramework = "python-ops"

// operationsTemplate renders infra/operations.ts from the block.
const operationsTemplate = "export const operations = {\n" +
	"  retentionDays: {{ .Operations.Backups.RetentionDays }},\n" +
	"  rpo: '{{ .Operations.Recovery.RPO }}',\n" +
	"  email: '{{ .Operations.Alerts.Email }}',\n" +
	"};\n"

// operationsFile is infra/operations.ts as operationsTemplate renders it, with
// the default retention.
func operationsFile(rpo, email string) string {
	return "export const operations = {\n  retentionDays: 35,\n  rpo: '" + rpo +
		"',\n  email: '" + email + "',\n};\n"
}

// newOperationsFramework registers opsFramework's template for ns, with
// infra/operations.ts.tmpl when renders, and returns the Framework, which
// says it reads the block either way.
func newOperationsFramework(ns string, renders bool) *c4v1beta1.Framework {
	GinkgoHelper()
	url := templatesURL + ns + "/" + opsFramework
	tree := maps.Clone(gitFake.Template(templatesURL+"python-fastapi", "v1.6.0"))
	Expect(tree).NotTo(BeEmpty())
	if renders {
		tree[model.OperationsFile+".tmpl"] = scaffold.File{Data: []byte(operationsTemplate)}
	}
	gitFake.AddTemplate(url, "v1.3.0", tree)
	spec := newFrameworkSpec(url, "v1.3.0", c4v1beta1.ContainerTypeService)
	spec.ReadsOperations = true
	return &c4v1beta1.Framework{ObjectMeta: objectMeta(ns, opsFramework), Spec: spec}
}

// createOperationsContainer creates orders-api, with the block ops, on the
// Framework opsFramework, in a namespace with a factory, and waits until it is
// scaffolded with OperationsApplied True.
func createOperationsContainer(ns string, ops *c4v1beta1.OperationsSpec) {
	GinkgoHelper()
	createFactory(ns)
	container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
	container.Spec.FrameworkRef = c4v1beta1.LocalRef{Name: opsFramework}
	container.Spec.Operations = ops
	mustCreate(newOperationsFramework(ns, true), newSystem(ns, "webshop-core"), container)
	eventuallyOperationsApplied(ns, metav1.ConditionTrue, c4v1beta1.ReasonAppliedAtScaffold)
}

// changeOperations changes orders-api's spec.operations with mutate.
func changeOperations(ns string, mutate func(*c4v1beta1.OperationsSpec)) {
	GinkgoHelper()
	container := getContainer(Default, ns)
	Expect(patch(container, func() {
		if container.Spec.Operations == nil {
			container.Spec.Operations = &c4v1beta1.OperationsSpec{}
		}
		mutate(container.Spec.Operations)
	})).To(Succeed())
}

// openedOperations waits for an operations update to rpo in flight, and
// returns it.
func openedOperations(ns, rpo string) c4v1beta1.OperationsUpdateStatus {
	GinkgoHelper()
	var update c4v1beta1.OperationsUpdateStatus
	Eventually(func(g Gomega) {
		container := getContainer(g, ns)
		g.Expect(container.Status.OperationsUpdate).NotTo(BeNil())
		update = *container.Status.OperationsUpdate
		g.Expect(update.Operations.Recovery.RPO).To(Equal(rpo))
		g.Expect(update.Stopped).To(BeEmpty())
		g.Expect(update.MergeCommit).To(BeEmpty())
		g.Expect(update.PullRequest).NotTo(BeZero())
		cond := condition(container.Status.Conditions, c4v1beta1.ConditionOperationsApplied)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonUpdating))
		g.Expect(cond.Message).To(Equal("Pull request " + update.PullRequestURL +
			" renders infra/operations.ts from spec.operations; it merges once CI passes"))
	}).Should(Succeed())
	return update
}

// mergedOperations waits for orders-api's operations update to have merged
// update's pull request.
func mergedOperations(ns string, update c4v1beta1.OperationsUpdateStatus) {
	GinkgoHelper()
	container := eventuallyOperationsApplied(ns, metav1.ConditionTrue, c4v1beta1.ReasonUpdateMerged)
	head := gitFake.MainHead(ns, "orders-api")
	Expect(container.Status.OperationsUpdate).To(PointTo(MatchFields(IgnoreExtras, Fields{
		"PullRequest": Equal(update.PullRequest), "MergeCommit": Equal(head.Hash), "Stopped": BeEmpty()})))
	Expect(condition(container.Status.Conditions, c4v1beta1.ConditionOperationsApplied).Message).To(Equal(
		"Pull request " + update.PullRequestURL + " merged spec.operations into infra/operations.ts as " + head.Hash))
	Expect(container.Status.Operations).To(Equal(update.Operations), "status records the block the merge rendered")
}

// eventuallyOperationsApplied waits for orders-api's OperationsApplied and
// returns the Container.
func eventuallyOperationsApplied(ns string, status metav1.ConditionStatus, reason string) *c4v1beta1.Container {
	GinkgoHelper()
	var container c4v1beta1.Container
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key(ns, "orders-api"), &container)).To(Succeed())
		cond := condition(container.Status.Conditions, c4v1beta1.ConditionOperationsApplied)
		g.Expect(cond).NotTo(BeNil())
		g.Expect(cond.Status).To(Equal(status))
		g.Expect(cond.Reason).To(Equal(reason))
		g.Expect(cond.ObservedGeneration).To(Equal(container.Generation))
	}).Should(Succeed())
	return &container
}

var _ = Describe("Container operations", func() {
	It("scaffolds without the block on the defaults, and leaves spec as it was written", func() {
		ns := newProjectNamespace()
		createOperationsContainer(ns, nil)

		container := getContainer(Default, ns)
		Expect(container.Spec.Operations).To(BeNil(), "the defaults are the factory's, never written into spec")
		Expect(container.Status.Operations).To(Equal(&c4v1beta1.OperationsSpec{
			Backups:  &c4v1beta1.OperationsBackups{RetentionDays: new(int32(35))},
			Recovery: &c4v1beta1.OperationsRecovery{RPO: "1h", RTO: "4h"},
			Alerts:   &c4v1beta1.OperationsAlerts{},
		}))
	})

	// #1209: a Framework that doesn't read the block, as the go-cli sample's,
	// reports nothing until the block is set, and then that its repository
	// holds none of it.
	It("reports no OperationsApplied on a Framework that doesn't read the block, and NotReadByFramework once it is set",
		func() {
			ns := newProjectNamespace()
			createWebshop(ns)
			container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
			Expect(condition(container.Status.Conditions, c4v1beta1.ConditionOperationsApplied)).To(BeNil())
			Expect(container.Status.Operations).To(BeNil(), "nothing was rendered, so nothing is recorded")

			By("setting the block")
			pushes := gitFake.PushCount()
			Expect(patch(container, func() {
				container.Spec.Operations = &c4v1beta1.OperationsSpec{
					Recovery: &c4v1beta1.OperationsRecovery{RPO: "5m"}}
			})).To(Succeed())
			container = eventuallyOperationsApplied(ns, metav1.ConditionFalse, c4v1beta1.ReasonNotReadByFramework)
			Expect(condition(container.Status.Conditions, c4v1beta1.ConditionOperationsApplied).Message).To(Equal(
				"Framework python-fastapi doesn't read spec.operations (readsOperations is not set), " +
					"so its repository has none of it; change the repository by a Task"))
			Expect(container.Status.Operations).To(BeNil())
			Expect(container.Status.OperationsUpdate).To(BeNil(), "no update acts")
			Consistently(gitFake.PushCount).WithTimeout(time.Second).Should(Equal(pushes),
				"nothing reaches the repository")
			Expect(gitFake.PullRequests(ns, "orders-api")).To(BeEmpty())

			By("leaving the block out again")
			Expect(patch(container, func() { container.Spec.Operations = nil })).To(Succeed())
			Eventually(func(g Gomega) {
				g.Expect(condition(getContainer(g, ns).Status.Conditions, c4v1beta1.ConditionOperationsApplied)).
					To(BeNil())
			}).Should(Succeed())
		})

	// #1209: the go-cli sample's Container, with the block set from the start.
	It("scaffolds a go-cli Container with the block, and says from the start that go-cli doesn't read it", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeCLI)
		container.Spec.FrameworkRef = c4v1beta1.LocalRef{Name: "go-cli"}
		container.Spec.ComputeRef, container.Spec.DeploymentRef = nil, nil
		container.Spec.Operations = &c4v1beta1.OperationsSpec{Alerts: &c4v1beta1.OperationsAlerts{Email: "ops@example.com"}}
		mustCreate(
			&c4v1beta1.Framework{ObjectMeta: objectMeta(ns, "go-cli"),
				Spec: newFrameworkSpec(templatesURL+"go-cli", "v1.0.0", c4v1beta1.ContainerTypeCLI)},
			newSystem(ns, "webshop-core"), container)

		container = eventuallyOperationsApplied(ns, metav1.ConditionFalse, c4v1beta1.ReasonNotReadByFramework)
		Expect(container.Status.Phase).To(Equal(c4v1beta1.ContainerReady))
		Expect(condition(container.Status.Conditions, c4v1beta1.ConditionOperationsApplied).Message).
			To(HavePrefix("Framework go-cli doesn't read spec.operations"))
		Expect(container.Status.Operations).To(BeNil())
		Expect(gitFake.Repo(ns, "orders-api").Files).NotTo(HaveKey(model.OperationsFile))
	})

	It("scaffolds with the block, then reports a change its Framework says it reads but can't render, until it is undone",
		func() {
			ns := newProjectNamespace()
			createFactory(ns)
			container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
			container.Spec.FrameworkRef = c4v1beta1.LocalRef{Name: opsFramework}
			container.Spec.Operations = &c4v1beta1.OperationsSpec{
				Backups: &c4v1beta1.OperationsBackups{RetentionDays: new(int32(0))},
				Alerts:  &c4v1beta1.OperationsAlerts{Email: "ops@example.com"},
			}
			mustCreate(newOperationsFramework(ns, false), newSystem(ns, "webshop-core"), container)

			container = eventuallyOperationsApplied(ns, metav1.ConditionTrue, c4v1beta1.ReasonAppliedAtScaffold)
			Expect(container.Spec.Operations).To(Equal(&c4v1beta1.OperationsSpec{
				Backups: &c4v1beta1.OperationsBackups{RetentionDays: new(int32(0))},
				Alerts:  &c4v1beta1.OperationsAlerts{Email: "ops@example.com"},
			}), "a client reads back only the fields it set")
			Expect(container.Status.Operations.Backups.RetentionDays).To(HaveValue(BeZero()))
			Expect(container.Status.Operations.Recovery.RPO).To(Equal("1h"))
			Expect(container.Status.Operations.Alerts.Email).To(Equal("ops@example.com"))

			By("changing the block after the scaffold, on a Framework that says it reads it but renders no infra/operations.ts")
			pushes := gitFake.PushCount()
			Expect(patch(container, func() {
				container.Spec.Operations.Recovery = &c4v1beta1.OperationsRecovery{RPO: "5m"}
			})).To(Succeed())
			container = eventuallyOperationsApplied(ns, metav1.ConditionFalse, c4v1beta1.ReasonChangedAfterScaffold)
			Expect(condition(container.Status.Conditions, c4v1beta1.ConditionOperationsApplied).Message).
				To(ContainSubstring("the Framework " + opsFramework + " at v1.3.0 renders no infra/operations.ts"))
			Expect(container.Status.Operations.Recovery.RPO).To(Equal("1h"), "status keeps what the scaffold rendered")
			Expect(container.Status.OperationsUpdate).To(PointTo(HaveField("Stopped",
				c4v1beta1.ReasonChangedAfterScaffold)))
			Consistently(gitFake.PushCount).WithTimeout(time.Second).Should(Equal(pushes),
				"nothing reaches the repository")
			Expect(gitFake.PullRequests(ns, "orders-api")).To(BeEmpty())

			By("writing the default out, which is the block the scaffold rendered")
			Expect(patch(container, func() {
				container.Spec.Operations.Recovery = &c4v1beta1.OperationsRecovery{RPO: "1h"}
			})).To(Succeed())
			container = eventuallyOperationsApplied(ns, metav1.ConditionTrue, c4v1beta1.ReasonAppliedAtScaffold)
			Expect(container.Status.OperationsUpdate).To(BeNil())
		})

	// #1119: a change to the block reaches the repository the way a Standard bump does.
	It("renders infra/operations.ts again from a changed block, and merges it once CI passes", func() {
		ns := newProjectNamespace()
		createOperationsContainer(ns, &c4v1beta1.OperationsSpec{
			Alerts: &c4v1beta1.OperationsAlerts{Email: "ops@example.com"}})
		Expect(mainFile(ns, model.OperationsFile)).To(Equal(operationsFile("1h", "ops@example.com")))

		changeOperations(ns, func(o *c4v1beta1.OperationsSpec) {
			o.Recovery = &c4v1beta1.OperationsRecovery{RPO: "5m"}
		})
		update := openedOperations(ns, "5m")
		Expect(update.Branch).To(HavePrefix("operations-update/"))
		Expect(update.Operations).To(Equal(&c4v1beta1.OperationsSpec{
			Backups:  &c4v1beta1.OperationsBackups{RetentionDays: new(int32(35))},
			Recovery: &c4v1beta1.OperationsRecovery{RPO: "5m", RTO: "4h"},
			Alerts:   &c4v1beta1.OperationsAlerts{Email: "ops@example.com"},
		}), "the update records the block it renders, every field set")
		Expect(gitFake.PullRequests(ns, "orders-api")).To(ConsistOf(MatchFields(IgnoreExtras, Fields{
			"Number": BeEquivalentTo(update.PullRequest), "Head": Equal(update.Branch),
			"HeadSHA": Equal(update.Commit), "State": Equal("open")})))
		Expect(gitFake.BranchEdits(ns, "orders-api", update.Branch)).To(MatchAllKeys(Keys{
			model.OperationsFile: PointTo(HaveField("Data",
				BeEquivalentTo(operationsFile("5m", "ops@example.com"))))}),
			"the pull request writes infra/operations.ts and nothing else")
		title, body := gitFake.PullText(ns, "orders-api", int(update.PullRequest))
		Expect(title).To(Equal("Render infra/operations.ts from spec.operations"))
		Expect(body).To(ContainSubstring("recovery.rpo 1h"))
		Expect(body).To(ContainSubstring("recovery.rpo 5m"))
		Expect(getContainer(Default, ns).Status.Operations.Recovery.RPO).To(Equal("1h"),
			"status keeps the block main renders until the update merges")

		By("waiting while CI runs")
		Consistently(func(g Gomega) {
			g.Expect(gitFake.ForcedMerges(ns, "orders-api")).To(BeEmpty())
			cond := condition(getContainer(g, ns).Status.Conditions, c4v1beta1.ConditionOperationsApplied)
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonUpdating))
		}).WithTimeout(2 * time.Second).Should(Succeed())

		By("merging it as admin once CI passes on its commit")
		gitFake.SetChecks(ns, "orders-api", update.Commit, "success")
		mergedOperations(ns, update)
		Expect(gitFake.ForcedMerges(ns, "orders-api")).To(Equal([]int{int(update.PullRequest)}))
		Expect(mainFile(ns, model.OperationsFile)).To(Equal(operationsFile("5m", "ops@example.com")))
		Expect(gitFake.MainHead(ns, "orders-api").Message).
			To(HavePrefix("Render infra/operations.ts from spec.operations\n\n"))
		Expect(gitFake.BranchExists(ns, "orders-api", update.Branch)).To(BeFalse())

		By("staying merged on the next passes")
		Consistently(func(g Gomega) {
			g.Expect(gitFake.PullRequests(ns, "orders-api")).To(HaveLen(1))
			cond := condition(getContainer(g, ns).Status.Conditions, c4v1beta1.ConditionOperationsApplied)
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonUpdateMerged))
		}).WithTimeout(2 * time.Second).Should(Succeed())
	})

	It("says CI failed on an operations update, with its pull request, and merges once a re-run passes", func() {
		ns := newProjectNamespace()
		createOperationsContainer(ns, nil)

		changeOperations(ns, func(o *c4v1beta1.OperationsSpec) {
			o.Recovery = &c4v1beta1.OperationsRecovery{RPO: "5m"}
		})
		update := openedOperations(ns, "5m")
		gitFake.SetChecks(ns, "orders-api", update.Commit, "failure")
		container := eventuallyOperationsApplied(ns, metav1.ConditionFalse, c4v1beta1.ReasonChecksFailed)
		Expect(condition(container.Status.Conditions, c4v1beta1.ConditionOperationsApplied).Message).To(Equal(
			"CI is failure on http://gitea/" + ns + "/orders-api/actions/runs/1; the update merges once a re-run " +
				"passes, or close " + update.PullRequestURL + " to drop it"))
		Expect(container.Status.Operations.Recovery.RPO).To(Equal("1h"))
		Expect(gitFake.ForcedMerges(ns, "orders-api")).To(BeEmpty())

		gitFake.SetChecks(ns, "orders-api", update.Commit, "success")
		mergedOperations(ns, update)
		Expect(mainFile(ns, model.OperationsFile)).To(Equal(operationsFile("5m", "")))
	})

	It("closes an operations update the block moved past and opens one for the new block", func() {
		ns := newProjectNamespace()
		createOperationsContainer(ns, nil)

		changeOperations(ns, func(o *c4v1beta1.OperationsSpec) {
			o.Recovery = &c4v1beta1.OperationsRecovery{RPO: "5m"}
		})
		first := openedOperations(ns, "5m")

		changeOperations(ns, func(o *c4v1beta1.OperationsSpec) {
			o.Recovery = &c4v1beta1.OperationsRecovery{RPO: "15m"}
		})
		second := openedOperations(ns, "15m")
		Expect(second.Branch).NotTo(Equal(first.Branch))
		Expect(gitFake.PullRequests(ns, "orders-api")).To(ConsistOf(
			MatchFields(IgnoreExtras, Fields{"Number": BeEquivalentTo(first.PullRequest), "State": Equal("closed"),
				"Merged": BeFalse()}),
			MatchFields(IgnoreExtras, Fields{"Number": BeEquivalentTo(second.PullRequest), "State": Equal("open")})))
		Expect(gitFake.BranchExists(ns, "orders-api", first.Branch)).To(BeFalse())

		gitFake.SetChecks(ns, "orders-api", second.Commit, "success")
		mergedOperations(ns, second)
		Expect(mainFile(ns, model.OperationsFile)).To(Equal(operationsFile("15m", "")))
	})

	// #1119: a Container without a Standard has no checks recheck of its own.
	It("retries a Git error before the pull request opens, on a Container without a Standard", func() {
		ns := newProjectNamespace()
		createOperationsContainer(ns, nil)
		Expect(getContainer(Default, ns).Spec.StandardRef).To(BeNil())
		url := templatesURL + ns + "/" + opsFramework
		tree := gitFake.Template(url, "v1.3.0")
		gitFake.DropTemplate(url, "v1.3.0")

		changeOperations(ns, func(o *c4v1beta1.OperationsSpec) {
			o.Recovery = &c4v1beta1.OperationsRecovery{RPO: "5m"}
		})
		eventuallyOperationsApplied(ns, metav1.ConditionFalse, c4v1beta1.ReasonGitError)
		Expect(gitFake.PullRequests(ns, "orders-api")).To(BeEmpty())

		By("opening it at a recheck once the host answers, with no change to the Container")
		gitFake.AddTemplate(url, "v1.3.0", tree)
		openedOperations(ns, "5m")
	})

	// #1119: the merge is recorded when the operator makes it, and a host that
	// names no merge commit leaves main's head, not the branch's pre-squash commit.
	It("records its own merge at main's head when the host names no merge commit", func() {
		ns := newProjectNamespace()
		createOperationsContainer(ns, nil)
		gitFake.LeaveMergeCommitsUnnamed(ns, "orders-api")

		changeOperations(ns, func(o *c4v1beta1.OperationsSpec) {
			o.Recovery = &c4v1beta1.OperationsRecovery{RPO: "5m"}
		})
		update := openedOperations(ns, "5m")
		gitFake.SetChecks(ns, "orders-api", update.Commit, "success")
		mergedOperations(ns, update)
		Expect(gitFake.ForcedMerges(ns, "orders-api")).To(Equal([]int{int(update.PullRequest)}))
	})

	DescribeTable("refuses a value outside the block's patterns and ranges",
		func(ops c4v1beta1.OperationsSpec, field string) {
			ns := newProjectNamespace()
			container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
			container.Spec.Operations = &ops
			Expect(k8sClient.Create(ctx, container)).To(MatchError(ContainSubstring("spec.operations." + field)))
		},
		Entry("a negative retention", c4v1beta1.OperationsSpec{
			Backups: &c4v1beta1.OperationsBackups{RetentionDays: new(int32(-1))}}, "backups.retentionDays"),
		Entry("a retention over ten years", c4v1beta1.OperationsSpec{
			Backups: &c4v1beta1.OperationsBackups{RetentionDays: new(int32(3651))}}, "backups.retentionDays"),
		Entry("a region that isn't one", c4v1beta1.OperationsSpec{
			Backups: &c4v1beta1.OperationsBackups{CopyToRegion: "Europe"}}, "backups.copyToRegion"),
		Entry("an objective in words", c4v1beta1.OperationsSpec{
			Recovery: &c4v1beta1.OperationsRecovery{RPO: "1 hour"}}, "recovery.rpo"),
		Entry("an objective in weeks", c4v1beta1.OperationsSpec{
			Recovery: &c4v1beta1.OperationsRecovery{RTO: "1w"}}, "recovery.rto"),
		Entry("an email that isn't one", c4v1beta1.OperationsSpec{
			Alerts: &c4v1beta1.OperationsAlerts{Email: "the ops team"}}, "alerts.email"),
	)
})
