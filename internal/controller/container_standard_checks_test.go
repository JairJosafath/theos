/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"maps"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// checkWorkflow is the CI workflow of a Standard that follows the Container's
// checks.
const checkWorkflow = ".gitea/workflows/check.yaml"

// checksRefs are standardRefs with a CI workflow that runs make check-light on
// each change and a full job on release-check/** with checks BeforeRelease,
// and a Makefile with a check-light target.
func checksRefs() map[string]scaffold.Tree {
	refs := standardRefs()
	for ref, tree := range refs {
		tree = maps.Clone(tree)
		tree[makefile] = scaffold.File{Data: append(slices.Clone(tree[makefile].Data), "check-light:\n\ttrue\n"...)}
		tree[checkWorkflow+".tmpl"] = scaffold.File{Data: []byte(
			"check: make {{ if eq .Checks \"BeforeRelease\" }}check-light\nfull: make check{{ else }}check{{ end }}\n")}
		refs[ref] = tree
	}
	return refs
}

// setChecks sets orders-api's spec.checks.
func setChecks(ns string, checks c4v1beta1.ChecksMode) {
	GinkgoHelper()
	container := getContainer(Default, ns)
	Expect(patch(container, func() { container.Spec.Checks = checks })).To(Succeed())
}

var _ = Describe("Container Standard update of the checks", func() {
	everyChange := model.StandardVersion{Ref: "v1.0.0", Checks: model.ChecksEveryChange}
	beforeRelease := model.StandardVersion{Ref: "v1.0.0", Checks: model.ChecksBeforeRelease}

	It("opens a pull request when the Container's checks change, and records them once it merges", func() {
		ns := newProjectNamespace()
		createUpdatableContainer(ns, checksRefs(), true, "")
		eventuallyAtChecks(ns, everyChange)
		Expect(mainFile(ns, checkWorkflow)).To(Equal("check: make check\n"))
		Expect(mainFile(ns, scaffold.IdentityPath)).To(ContainSubstring("checks: EveryChange\n"))

		setChecks(ns, c4v1beta1.ChecksBeforeRelease)
		var update c4v1beta1.StandardUpdateStatus
		Eventually(func(g Gomega) {
			container := getContainer(g, ns)
			g.Expect(container.Status.Standard).NotTo(BeNil())
			g.Expect(container.Status.Standard.Update).NotTo(BeNil())
			update = *container.Status.Standard.Update
			g.Expect(update.PullRequest).NotTo(BeZero())
			cond := standardCondition(g, container)
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonUpdating))
			g.Expect(cond.Message).To(Equal("Pull request " + update.PullRequestURL + " brings python-strict from " +
				"v1.0.0 to v1.0.0 with the full check before release; it merges once CI passes"))
		}).Should(Succeed())
		Expect(update).To(MatchFields(IgnoreExtras, Fields{
			"Ref": Equal("v1.0.0"), "Checks": Equal(c4v1beta1.ChecksBeforeRelease),
			"Branch": Equal("standard-update/python-strict-v1.0.0-before-release"),
			"Paths":  Equal([]string{checkWorkflow}), "LeftOut": BeEmpty(), "Stopped": BeEmpty()}))
		Expect(getContainer(Default, ns).Status.Standard.Checks).To(Equal(c4v1beta1.ChecksEveryChange),
			"the checks are recorded once the update merges")
		Eventually(func(g Gomega) {
			g.Expect(checksApplied(g, ns)).To(MatchFields(IgnoreExtras, Fields{
				"Status": Equal(metav1.ConditionFalse), "Reason": Equal(c4v1beta1.ReasonPending),
				"Message": Equal("CI runs the full check on every change until pull request " +
					update.PullRequestURL + " merges")}))
		}).Should(Succeed())

		gitFake.SetChecks(ns, "orders-api", update.Commit, "success")
		eventuallyAtChecks(ns, beforeRelease)
		Eventually(func(g Gomega) {
			cond := checksApplied(g, ns)
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonBeforeRelease))
		}).Should(Succeed())
		Expect(mainFile(ns, checkWorkflow)).To(Equal("check: make check-light\nfull: make check\n"))
		head := gitFake.MainHead(ns, "orders-api")
		Expect(head.Message).To(HavePrefix(
			"Update the Standard python-strict to v1.0.0, with the full check before release\n\n"))
		Expect(head.Message).To(ContainSubstring("Checks: EveryChange..BeforeRelease\n"))
		Expect(mainFile(ns, scaffold.IdentityPath)).To(ContainSubstring("checks: EveryChange\n"),
			"the identity file is written once")

		By("going back to the full check on every change")
		setChecks(ns, c4v1beta1.ChecksEveryChange)
		back := openedUpdateTo(ns, everyChange)
		Expect(back.Branch).To(Equal("standard-update/python-strict-v1.0.0"))
		gitFake.SetChecks(ns, "orders-api", back.Commit, "success")
		eventuallyAtChecks(ns, everyChange)
		Expect(mainFile(ns, checkWorkflow)).To(Equal("check: make check\n"))
	})

	It("brings a new ref and a change of the checks in one pull request", func() {
		ns := newProjectNamespace()
		createUpdatableContainer(ns, checksRefs(), true, "")
		eventuallyAtChecks(ns, everyChange)

		setChecks(ns, c4v1beta1.ChecksBeforeRelease)
		bumpStandard(ns, "v1.1.0")
		update := openedUpdateTo(ns, model.StandardVersion{Ref: "v1.1.0", Checks: model.ChecksBeforeRelease})
		Expect(update.Paths).To(Equal([]string{checkWorkflow, "AGENTS.md", implementer}))
		gitFake.SetChecks(ns, "orders-api", update.Commit, "success")
		eventuallyAtChecks(ns, model.StandardVersion{Ref: "v1.1.0", Checks: model.ChecksBeforeRelease})
	})

	It("scaffolds a new Container with its checks and records them", func() {
		ns := newProjectNamespace()
		createUpdatableContainer(ns, checksRefs(), true, c4v1beta1.ChecksBeforeRelease)
		eventuallyAtChecks(ns, beforeRelease)
		Expect(mainFile(ns, checkWorkflow)).To(Equal("check: make check-light\nfull: make check\n"))
		Expect(mainFile(ns, scaffold.IdentityPath)).To(ContainSubstring("checks: BeforeRelease\n"))
		Expect(gitFake.MainHead(ns, "orders-api").Message).To(ContainSubstring("Checks: BeforeRelease\n"))
	})

	It("opens nothing when the Container's Standard can't run the full check only before a release", func() {
		ns := newProjectNamespace()
		createUpdatableContainer(ns, checksRefs(), false, "")
		eventuallyAtChecks(ns, everyChange)

		setChecks(ns, c4v1beta1.ChecksBeforeRelease)
		Eventually(func(g Gomega) {
			cond := condition(getContainer(g, ns).Status.Conditions, c4v1beta1.ConditionChecksApplied)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonStandardRunsFullChecks))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.PullRequests(ns, "orders-api")).To(BeEmpty())
			container := getContainer(g, ns)
			g.Expect(container.Status.Standard.Checks).To(Equal(c4v1beta1.ChecksEveryChange))
			g.Expect(container.Status.Standard.Update).To(BeNil())
			g.Expect(standardCondition(g, container).Reason).To(Equal(c4v1beta1.ReasonUpToDate))
		}).WithTimeout(2 * time.Second).Should(Succeed())
		Expect(mainFile(ns, checkWorkflow)).To(Equal("check: make check\n"))
	})

	It("keeps the checks when the repository changed the workflow since", func() {
		ns := newProjectNamespace()
		createUpdatableContainer(ns, checksRefs(), true, "")
		eventuallyAtChecks(ns, everyChange)
		gitFake.CommitToMain(ns, "orders-api", scaffold.Tree{checkWorkflow: {Data: []byte("check: make test\n")}})

		setChecks(ns, c4v1beta1.ChecksBeforeRelease)
		message := stoppedUpdate(ns, c4v1beta1.ReasonChecksLeftOut)
		Expect(message).To(Equal("CI can't change to run the full check only before a release: " + checkWorkflow +
			" changed in the repository since v1.0.0 and doesn't merge with the change; CI keeps running it on " +
			"every change until the Container's checks or the Standard's ref change again"))
		Consistently(func(g Gomega) {
			g.Expect(gitFake.PullRequests(ns, "orders-api")).To(BeEmpty())
			container := getContainer(g, ns)
			g.Expect(container.Status.Standard.Checks).To(Equal(c4v1beta1.ChecksEveryChange))
			g.Expect(standardCondition(g, container).Status).To(Equal(metav1.ConditionFalse))
		}).WithTimeout(2 * time.Second).Should(Succeed())
		Expect(mainFile(ns, checkWorkflow)).To(Equal("check: make test\n"))
		Expect(checksApplied(Default, ns)).To(MatchFields(IgnoreExtras, Fields{
			"Status": Equal(metav1.ConditionFalse), "Reason": Equal(c4v1beta1.ReasonChecksLeftOut),
			"Message": Equal("CI runs the full check on every change: the Standard update that brings it only " +
				"before a release stopped (ChecksLeftOut); StandardUpdated says why")}))

		By("forgetting it when the checks go back")
		setChecks(ns, c4v1beta1.ChecksEveryChange)
		eventuallyAtChecks(ns, everyChange)
		Eventually(func(g Gomega) {
			cond := checksApplied(g, ns)
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonEveryChange))
		}).Should(Succeed())
	})

	It("holds the change to BeforeRelease until the repository's Makefile has a light check", func() {
		ns := newProjectNamespace()
		createUpdatableContainer(ns, checksRefs(), true, "")
		eventuallyAtChecks(ns, everyChange)
		// The app's own Makefile, with its own targets and no check-light.
		gitFake.CommitToMain(ns, "orders-api", scaffold.Tree{makefile: {Data: []byte(
			"check:\n\ttrue\nrelease:\n\ttrue\n")}})

		setChecks(ns, c4v1beta1.ChecksBeforeRelease)
		message := stoppedUpdate(ns, c4v1beta1.ReasonNoLightCheck)
		Expect(message).To(Equal("CI can't change to run the full check only before a release: the repository's " +
			"Makefile has no check-light target, which make check-light on each change needs; CI keeps running " +
			"the full check on every change until the Makefile has one"))
		Eventually(func(g Gomega) {
			g.Expect(checksApplied(g, ns)).To(MatchFields(IgnoreExtras, Fields{
				"Status": Equal(metav1.ConditionFalse), "Reason": Equal(c4v1beta1.ReasonNoLightCheck),
				"Message": Equal(message)}))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.PullRequests(ns, "orders-api")).To(BeEmpty())
			g.Expect(getContainer(g, ns).Status.Standard.Checks).To(Equal(c4v1beta1.ChecksEveryChange))
		}).WithTimeout(2 * time.Second).Should(Succeed())
		Expect(mainFile(ns, checkWorkflow)).To(Equal("check: make check\n"))

		By("letting it through once the Makefile has one")
		gitFake.CommitToMain(ns, "orders-api", scaffold.Tree{makefile: {Data: []byte(
			"check:\n\ttrue\ncheck-light:\n\ttrue\nrelease:\n\ttrue\n")}})
		update := openedUpdateTo(ns, beforeRelease)
		Expect(update.Paths).To(Equal([]string{checkWorkflow}))
		gitFake.SetChecks(ns, "orders-api", update.Commit, "success")
		eventuallyAtChecks(ns, beforeRelease)
		Eventually(func(g Gomega) {
			g.Expect(checksApplied(g, ns).Reason).To(Equal(c4v1beta1.ReasonBeforeRelease))
		}).Should(Succeed())
	})

	It("says CI runs the full check on every change when the Standard came after the scaffold", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		mustCreate(newSystem(ns, "webshop-core"),
			newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService),
			&c4v1beta1.Standard{
				ObjectMeta: objectMeta(ns, updatedStandard),
				Spec: c4v1beta1.StandardSpec{
					CatalogEntrySpec: newCatalogSpec(templatesURL+ns+"/"+updatedStandard, "v1.0.0",
						c4v1beta1.ContainerTypeService),
					SupportedFrameworks: []string{"python-fastapi"},
					ChecksBeforeRelease: true,
				},
			})
		container := eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)

		Expect(patch(container, func() {
			container.Spec.StandardRef = &c4v1beta1.LocalRef{Name: updatedStandard}
			container.Spec.Checks = c4v1beta1.ChecksBeforeRelease
		})).To(Succeed())
		Eventually(func(g Gomega) {
			cond := checksApplied(g, ns)
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonAddedAfterScaffold))
			g.Expect(cond.Message).To(Equal("The Standard " + updatedStandard + " was not part of the scaffold, " +
				"so its updates don't reach this Container; CI runs the full check on every change"))
		}).Should(Succeed())

		By("asking for what CI already runs")
		setChecks(ns, c4v1beta1.ChecksEveryChange)
		Eventually(func(g Gomega) {
			cond := checksApplied(g, ns)
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonEveryChange))
		}).Should(Succeed())
	})
})

// openedUpdateTo waits for an update to the version to in flight, and returns it.
func openedUpdateTo(ns string, to model.StandardVersion) c4v1beta1.StandardUpdateStatus {
	GinkgoHelper()
	var update c4v1beta1.StandardUpdateStatus
	Eventually(func(g Gomega) {
		container := getContainer(g, ns)
		g.Expect(container.Status.Standard).NotTo(BeNil())
		g.Expect(container.Status.Standard.Update).NotTo(BeNil())
		update = *container.Status.Standard.Update
		g.Expect(update.Ref).To(Equal(to.Ref))
		g.Expect(update.Checks).To(BeEquivalentTo(to.Checks))
		g.Expect(update.Stopped).To(BeEmpty())
		g.Expect(update.PullRequest).NotTo(BeZero())
	}).Should(Succeed())
	return update
}

// checksApplied returns orders-api's ChecksApplied condition.
func checksApplied(g Gomega, ns string) metav1.Condition {
	cond := condition(getContainer(g, ns).Status.Conditions, c4v1beta1.ConditionChecksApplied)
	g.Expect(cond).NotTo(BeNil())
	return *cond
}
