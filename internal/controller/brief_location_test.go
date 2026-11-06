/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// paymentsPlan plans cel-lens and a payment provider the story only names,
// at location and with description. It plans no Container: the spec waits
// for the round to be Planned, and a Container's scaffold and Crew, which it
// doesn't test, took that wait past its budget on a loaded host (#1992).
func paymentsPlan(ns, location, description string) scaffold.Tree {
	return scaffold.Tree{
		model.PlanModelFile: {Data: []byte(projectDoc(ns) + `---
apiVersion: theos.factory.io/v1beta1
kind: SoftwareSystem
metadata:
  name: cel-lens
spec:
  displayName: CEL lens
---
apiVersion: theos.factory.io/v1beta1
kind: SoftwareSystem
metadata:
  name: payments
spec:
  description: ` + description + `
  location: ` + location + `
---
apiVersion: theos.factory.io/v1beta1
kind: Relationship
metadata:
  name: cel-lens-bills
spec:
  source: {kind: SoftwareSystem, name: cel-lens}
  target: {kind: SoftwareSystem, name: payments}
  description: Bills a rule author
`)},
		model.PlanChoicesFile: {Data: []byte("- A payment provider takes the money\n")},
		model.PlanSummaryFile: {Data: []byte("Planned cel-lens and the payment provider it bills through.\n")},
	}
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
		Expect(containers.Items).To(BeEmpty())
		var stores c4v1beta1.DataStoreList
		Expect(k8sClient.List(ctx, &stores, client.InNamespace(ns))).To(Succeed())
		Expect(stores.Items).To(BeEmpty())

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
