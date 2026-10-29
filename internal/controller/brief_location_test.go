/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// paymentsPlan is linterPlan with a payment provider the story only names,
// at location and with description.
func paymentsPlan(ns, location, description string) scaffold.Tree {
	files := linterPlan(ns, 1)
	files[model.PlanModelFile] = scaffold.File{Data: append(files[model.PlanModelFile].Data, []byte(`---
apiVersion: c4.factory.io/v1beta1
kind: SoftwareSystem
metadata:
  name: payments
spec:
  description: `+description+`
  location: `+location+`
---
apiVersion: c4.factory.io/v1beta1
kind: Relationship
metadata:
  name: linter-bills
spec:
  source: {kind: Container, name: linter}
  target: {kind: SoftwareSystem, name: payments}
  description: Bills a rule author
`)...)}
	return files
}

func paymentsSystem(g Gomega, ns string) *c4v1beta1.SoftwareSystem {
	s := &c4v1beta1.SoftwareSystem{}
	g.Expect(k8sClient.Get(ctx, key(ns, "payments"), s)).To(Succeed())
	return s
}

// A Brief's planner marks a system the story only names External, and a
// later round keeps the location the first one set (#1150).
var _ = Describe("Brief location", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("creates a planned External system with nothing to build, shows it, and keeps it External", func() {
		createFactory(ns)
		briefAgents(ns, 3)
		mustCreate(newBrief(ns, "cel-lens"))
		planRound(ns, 1, 1, paymentsPlan(ns, "External", "Takes the money"))
		Eventually(func(g Gomega) {
			g.Expect(getBrief(g, ns).Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		Expect(paymentsSystem(Default, ns).Spec.Location).To(Equal(c4v1beta1.SystemExternal))
		cel := &c4v1beta1.SoftwareSystem{}
		Expect(k8sClient.Get(ctx, key(ns, "cel-lens"), cel)).To(Succeed())
		Expect(cel.Spec.Location).To(Equal(c4v1beta1.SystemInternal))

		By("building nothing for it")
		var containers c4v1beta1.ContainerList
		Expect(k8sClient.List(ctx, &containers, client.InNamespace(ns))).To(Succeed())
		for _, c := range containers.Items {
			Expect(c.Spec.SoftwareSystemRef.Name).NotTo(Equal("payments"), "Container %s", c.Name)
		}
		var stores c4v1beta1.DataStoreList
		Expect(k8sClient.List(ctx, &stores, client.InNamespace(ns))).To(Succeed())
		for _, d := range stores.Items {
			Expect(d.Spec.SoftwareSystemRef.Name).NotTo(Equal("payments"), "DataStore %s", d.Name)
		}

		By("showing the planner its location")
		Eventually(func(g Gomega) {
			g.Expect(string(gitFake.MainFiles(ns, briefRepo)[model.WorkspaceModelFile].Data)).
				To(MatchRegexp(`name: payments\nspec:\n +description: Takes the money\n +location: External\n`))
		}).Should(Succeed())

		By("keeping it External when round 2's plan calls it Internal")
		brief := getBrief(Default, ns)
		Expect(patch(brief, func() {
			brief.Spec.Prompts = []string{"Say the payment provider takes cards."}
		})).To(Succeed())
		planRound(ns, 2, 1, paymentsPlan(ns, "Internal", "Takes the money by card"))
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Round).To(Equal(int32(2)))
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
		}).Should(Succeed())
		payments := paymentsSystem(Default, ns)
		Expect(payments.Spec.Description).To(Equal("Takes the money by card"))
		Expect(payments.Spec.Location).To(Equal(c4v1beta1.SystemExternal))
	})
})
