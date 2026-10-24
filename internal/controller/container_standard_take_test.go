/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// makefileBehind gives orders-api a Makefile of its own in the lines v1.2.0
// changes, and brings it to v1.2.0: the Makefile is left out, behind at v1.0.0.
func makefileBehind(ns string) {
	GinkgoHelper()
	updatableContainer(ns)
	gitFake.commitToMain(ns, "orders-api", scaffold.Tree{makefile: {Data: []byte("# Ours.\ncheck:\n\tpytest\n")}})
	bumpStandard(ns, "v1.2.0")
	update := openedUpdate(ns, "v1.2.0")
	gitFake.setChecks(ns, "orders-api", update.Commit, "success")
	eventuallyAt(ns, "v1.2.0", makefile)
}

// answer sets the annotation key on orders-api to value.
func answer(ns, key, value string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		container := getContainer(g, ns)
		g.Expect(patch(container, func() {
			if container.Annotations == nil {
				container.Annotations = map[string]string{}
			}
			container.Annotations[key] = value
		})).To(Succeed())
	}).Should(Succeed())
}

// eventuallyAnswered waits until the operator removed the answers from orders-api.
func eventuallyAnswered(ns string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		annotations := getContainer(g, ns).Annotations
		g.Expect(annotations).NotTo(HaveKey(c4v1beta1.AnnotationStandardTake))
		g.Expect(annotations).NotTo(HaveKey(c4v1beta1.AnnotationStandardKeep))
	}).Should(Succeed())
}

// openedTake waits for a take in flight, and returns it.
func openedTake(ns string) c4v1beta1.StandardUpdateStatus {
	GinkgoHelper()
	var take c4v1beta1.StandardUpdateStatus
	Eventually(func(g Gomega) {
		container := getContainer(g, ns)
		g.Expect(container.Status.Standard.Update).To(PointTo(MatchFields(IgnoreExtras, Fields{
			"Take": BeTrue(), "Stopped": BeEmpty(), "PullRequest": Not(BeZero())})))
		take = *container.Status.Standard.Update
		cond := standardCondition(g, container)
		g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonUpdating))
		g.Expect(cond.Message).To(Equal("Pull request " + take.PullRequestURL +
			" takes the version of python-strict at v1.2.0 of Makefile; it merges once CI passes"))
	}).Should(Succeed())
	return take
}

var _ = Describe("Container Standard files behind", func() {
	It("takes the Standard's version from one annotation, through a pull request merged once CI passes", func() {
		ns := newProjectNamespace()
		makefileBehind(ns)

		answer(ns, c4v1beta1.AnnotationStandardTake, "v1.2.0")
		take := openedTake(ns)
		Expect(take.Ref).To(Equal("v1.2.0"))
		Expect(take.Branch).To(Equal("standard-take/python-strict-v1.2.0"))
		Expect(take.Paths).To(Equal([]string{makefile}))
		eventuallyAnswered(ns)
		title, body := gitFake.pullText(ns, "orders-api", int(take.PullRequest))
		Expect(title).To(Equal("Bring Makefile up to date with python-strict v1.2.0"))
		Expect(body).To(ContainSubstring("## `Makefile`\n\nThe repository's lines it drops:\n\n```diff\n-\tpytest\n```\n"))

		By("waiting while CI runs")
		Consistently(func(g Gomega) {
			g.Expect(gitFake.forcedMerges(ns, "orders-api")).To(HaveLen(1), "only the update's")
		}).WithTimeout(2 * time.Second).Should(Succeed())

		By("merging it as admin once CI passes, the repository's other lines kept")
		gitFake.setChecks(ns, "orders-api", take.Commit, "success")
		eventuallyAt(ns, "v1.2.0")
		Expect(mainFile(ns, makefile)).To(Equal("# Ours.\ncheck:\n\tmake lint test\n"))
		head := gitFake.mainHead(ns, "orders-api")
		Expect(head.Message).To(HavePrefix("Bring Makefile up to date with python-strict v1.2.0\n\n"))
		Expect(head.Message).To(ContainSubstring("Standard-Take: python-strict@v1.2.0"))
		Expect(gitFake.hasBranch(ns, "orders-api", take.Branch)).To(BeFalse())
	})

	It("leaves the files behind when a person closes the take, ignores a stale answer and records a keep", func() {
		ns := newProjectNamespace()
		makefileBehind(ns)

		answer(ns, c4v1beta1.AnnotationStandardTake, "v1.2.0")
		take := openedTake(ns)
		gitFake.closePull(ns, "orders-api", int(take.PullRequest))
		eventuallyAt(ns, "v1.2.0", makefile)
		Expect(gitFake.hasBranch(ns, "orders-api", take.Branch)).To(BeFalse())
		Consistently(func(g Gomega) {
			g.Expect(gitFake.pullRequests(ns, "orders-api")).To(HaveLen(2), "the update's and the take's")
		}).WithTimeout(2 * time.Second).Should(Succeed())

		By("ignoring an answer for another ref")
		answer(ns, c4v1beta1.AnnotationStandardKeep, "v1.0.0")
		eventuallyAnswered(ns)
		eventuallyAt(ns, "v1.2.0", makefile)

		By("keeping the repository's version")
		answer(ns, c4v1beta1.AnnotationStandardKeep, "v1.2.0")
		eventuallyAnswered(ns)
		Eventually(func(g Gomega) {
			container := getContainer(g, ns)
			g.Expect(container.Status.Standard.Behind).To(BeEmpty())
			g.Expect(container.Status.Standard.LeftOut).To(BeEmpty())
			g.Expect(container.Status.Standard.Kept).To(Equal([]c4v1beta1.StandardFile{{Path: makefile, Ref: "v1.2.0"}}))
			cond := standardCondition(g, container)
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			g.Expect(cond.Reason).To(Equal(c4v1beta1.ReasonUpToDate))
		}).Should(Succeed())
		Expect(mainFile(ns, makefile)).To(Equal("# Ours.\ncheck:\n\tpytest\n"))

		By("planning a kept file again once a ref changes it")
		bumpStandard(ns, "v1.4.0")
		update := openedUpdate(ns, "v1.4.0")
		Expect(update.LeftOut).To(Equal([]string{makefile}))
		gitFake.setChecks(ns, "orders-api", update.Commit, "success")
		eventuallyAt(ns, "v1.4.0", makefile)
		Expect(getContainer(Default, ns).Status.Standard.Behind).To(Equal([]c4v1beta1.StandardFile{
			{Path: makefile, Ref: "v1.2.0", PullRequestURL: update.PullRequestURL,
				Change: "--- Makefile (v1.2.0)\n+++ Makefile (v1.4.0)\n@@ -1,2 +1,5 @@\n+lint:\n+\truff check\n+\n" +
					" check:\n-\tmake lint test\n+\ttrue\n\nThe repository's lines taking it drops:\n-# Ours.\n@@\n-\tpytest"}}),
			"a kept file planned again carries the Standard's change from the ref it was kept at")
	})
})
