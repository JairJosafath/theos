/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
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

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// checkWorkflow is the CI workflow of a Standard that follows the Container's
// checks.
const checkWorkflow = ".gitea/workflows/check.yaml"

// checksRefs are standardRefs with a CI workflow that runs make check-light on
// each change and a full job on release-check/** with checks BeforeRelease.
func checksRefs() map[string]scaffold.Tree {
	refs := standardRefs()
	for ref, tree := range refs {
		tree = maps.Clone(tree)
		tree[checkWorkflow+".tmpl"] = scaffold.File{Data: []byte(
			"check: make {{ if eq .Checks \"BeforeRelease\" }}check-light\nfull: make check{{ else }}check{{ end }}\n")}
		refs[ref] = tree
	}
	return refs
}

// setChecks sets orders-api's spec.checks.
func setChecks(ns string, checks c4v1alpha1.ChecksMode) {
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

		setChecks(ns, c4v1alpha1.ChecksBeforeRelease)
		var update c4v1alpha1.StandardUpdateStatus
		Eventually(func(g Gomega) {
			container := getContainer(g, ns)
			g.Expect(container.Status.Standard).NotTo(BeNil())
			g.Expect(container.Status.Standard.Update).NotTo(BeNil())
			update = *container.Status.Standard.Update
			g.Expect(update.PullRequest).NotTo(BeZero())
			cond := standardCondition(g, container)
			g.Expect(cond.Reason).To(Equal(c4v1alpha1.ReasonUpdating))
			g.Expect(cond.Message).To(Equal("Pull request " + update.PullRequestURL + " brings python-strict from " +
				"v1.0.0 to v1.0.0 with the full check before release; it merges once CI passes"))
		}).WithTimeout(standardSteps).Should(Succeed())
		Expect(update).To(MatchFields(IgnoreExtras, Fields{
			"Ref": Equal("v1.0.0"), "Checks": Equal(c4v1alpha1.ChecksBeforeRelease),
			"Branch": Equal("standard-update/python-strict-v1.0.0-before-release"),
			"Paths":  Equal([]string{checkWorkflow}), "LeftOut": BeEmpty(), "Stopped": BeEmpty()}))
		Expect(getContainer(Default, ns).Status.Standard.Checks).To(Equal(c4v1alpha1.ChecksEveryChange),
			"the checks are recorded once the update merges")

		gitFake.setChecks(ns, "orders-api", update.Commit, "success")
		eventuallyAtChecks(ns, beforeRelease)
		Expect(mainFile(ns, checkWorkflow)).To(Equal("check: make check-light\nfull: make check\n"))
		head := gitFake.mainHead(ns, "orders-api")
		Expect(head.Message).To(HavePrefix(
			"Update the Standard python-strict to v1.0.0, with the full check before release\n\n"))
		Expect(head.Message).To(ContainSubstring("Checks: EveryChange..BeforeRelease\n"))
		Expect(mainFile(ns, scaffold.IdentityPath)).To(ContainSubstring("checks: EveryChange\n"),
			"the identity file is written once")

		By("going back to the full check on every change")
		setChecks(ns, c4v1alpha1.ChecksEveryChange)
		back := openedUpdateTo(ns, everyChange)
		Expect(back.Branch).To(Equal("standard-update/python-strict-v1.0.0"))
		gitFake.setChecks(ns, "orders-api", back.Commit, "success")
		eventuallyAtChecks(ns, everyChange)
		Expect(mainFile(ns, checkWorkflow)).To(Equal("check: make check\n"))
	})

	It("brings a new ref and a change of the checks in one pull request", func() {
		ns := newProjectNamespace()
		createUpdatableContainer(ns, checksRefs(), true, "")
		eventuallyAtChecks(ns, everyChange)

		setChecks(ns, c4v1alpha1.ChecksBeforeRelease)
		bumpStandard(ns, "v1.1.0")
		update := openedUpdateTo(ns, model.StandardVersion{Ref: "v1.1.0", Checks: model.ChecksBeforeRelease})
		Expect(update.Paths).To(Equal([]string{checkWorkflow, "AGENTS.md", implementer}))
		gitFake.setChecks(ns, "orders-api", update.Commit, "success")
		eventuallyAtChecks(ns, model.StandardVersion{Ref: "v1.1.0", Checks: model.ChecksBeforeRelease})
	})

	It("scaffolds a new Container with its checks and records them", func() {
		ns := newProjectNamespace()
		createUpdatableContainer(ns, checksRefs(), true, c4v1alpha1.ChecksBeforeRelease)
		eventuallyAtChecks(ns, beforeRelease)
		Expect(mainFile(ns, checkWorkflow)).To(Equal("check: make check-light\nfull: make check\n"))
		Expect(mainFile(ns, scaffold.IdentityPath)).To(ContainSubstring("checks: BeforeRelease\n"))
		Expect(gitFake.mainHead(ns, "orders-api").Message).To(ContainSubstring("Checks: BeforeRelease\n"))
	})

	It("opens nothing when the Container's Standard can't run the full check only before a release", func() {
		ns := newProjectNamespace()
		createUpdatableContainer(ns, checksRefs(), false, "")
		eventuallyAtChecks(ns, everyChange)

		setChecks(ns, c4v1alpha1.ChecksBeforeRelease)
		Eventually(func(g Gomega) {
			cond := condition(getContainer(g, ns).Status.Conditions, c4v1alpha1.ConditionChecksApplied)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Reason).To(Equal(c4v1alpha1.ReasonStandardRunsFullChecks))
		}).WithTimeout(standardSteps).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.pullRequests(ns, "orders-api")).To(BeEmpty())
			container := getContainer(g, ns)
			g.Expect(container.Status.Standard.Checks).To(Equal(c4v1alpha1.ChecksEveryChange))
			g.Expect(container.Status.Standard.Update).To(BeNil())
			g.Expect(standardCondition(g, container).Reason).To(Equal(c4v1alpha1.ReasonUpToDate))
		}).WithTimeout(2 * time.Second).Should(Succeed())
		Expect(mainFile(ns, checkWorkflow)).To(Equal("check: make check\n"))
	})

	It("keeps the checks when the repository changed the workflow since", func() {
		ns := newProjectNamespace()
		createUpdatableContainer(ns, checksRefs(), true, "")
		eventuallyAtChecks(ns, everyChange)
		gitFake.commitToMain(ns, "orders-api", scaffold.Tree{checkWorkflow: {Data: []byte("check: make test\n")}})

		setChecks(ns, c4v1alpha1.ChecksBeforeRelease)
		message := stoppedUpdate(ns, c4v1alpha1.ReasonChecksLeftOut)
		Expect(message).To(Equal("CI can't change to run the full check only before a release: " + checkWorkflow +
			" changed in the repository since v1.0.0 and doesn't merge with the change; CI keeps running it on " +
			"every change until the Container's checks or the Standard's ref change again"))
		Consistently(func(g Gomega) {
			g.Expect(gitFake.pullRequests(ns, "orders-api")).To(BeEmpty())
			container := getContainer(g, ns)
			g.Expect(container.Status.Standard.Checks).To(Equal(c4v1alpha1.ChecksEveryChange))
			g.Expect(standardCondition(g, container).Status).To(Equal(metav1.ConditionFalse))
		}).WithTimeout(2 * time.Second).Should(Succeed())
		Expect(mainFile(ns, checkWorkflow)).To(Equal("check: make test\n"))

		By("forgetting it when the checks go back")
		setChecks(ns, c4v1alpha1.ChecksEveryChange)
		eventuallyAtChecks(ns, everyChange)
	})
})

// openedUpdateTo waits for an update to the version to in flight, and returns it.
func openedUpdateTo(ns string, to model.StandardVersion) c4v1alpha1.StandardUpdateStatus {
	GinkgoHelper()
	var update c4v1alpha1.StandardUpdateStatus
	Eventually(func(g Gomega) {
		container := getContainer(g, ns)
		g.Expect(container.Status.Standard).NotTo(BeNil())
		g.Expect(container.Status.Standard.Update).NotTo(BeNil())
		update = *container.Status.Standard.Update
		g.Expect(update.Ref).To(Equal(to.Ref))
		g.Expect(update.Checks).To(BeEquivalentTo(to.Checks))
		g.Expect(update.Stopped).To(BeEmpty())
		g.Expect(update.PullRequest).NotTo(BeZero())
	}).WithTimeout(standardSteps).Should(Succeed())
	return update
}
