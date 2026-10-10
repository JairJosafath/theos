/*
Copyright 2026.

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
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/JairJosafath/c4factory/internal/model"
)

// numbered returns prompts "p<from>" to "p<to>".
func numbered(from, to int) []string {
	var prompts []string
	for i := from; i <= to; i++ {
		prompts = append(prompts, fmt.Sprintf("p%d", i))
	}
	return prompts
}

var _ = Describe("Brief prompts (#835)", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	It("retires the planned prompts past the newest ten, so the Brief takes prompts past 50", func() {
		brief := newBrief(ns, "cel-lens", numbered(1, 50)...)
		mustCreate(brief)
		Expect(k8sClient.Status().Patch(ctx, brief, client.RawPatch("application/merge-patch+json",
			[]byte(`{"status":{"round":9,"roundPrompts":49,"promptsPlanned":49}}`)))).To(Succeed())

		By("keeping the ten newest planned prompts and the one no round planned")
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Spec.PromptsRetired).To(Equal(int32(49 - model.KeptPlannedPrompts)))
			g.Expect(b.Spec.Prompts).To(Equal(numbered(40, 50)))
		}).Should(Succeed())

		By("taking the 51st prompt")
		brief = getBrief(Default, ns)
		Expect(patch(brief, func() { brief.Spec.Prompts = append(brief.Spec.Prompts, "p51") })).To(Succeed())
		Expect(briefPrompts(brief)).To(HaveLen(51))
		Expect(takenPrompts(brief)).To(BeEmpty(), "the running round took none of them")
		Expect(model.RoundPrompts(briefPrompts(brief), int(brief.Status.PromptsPlanned))).
			To(Equal([]string{"p50", "p51"}), "the next round takes the two no round planned")
	})

	It("never loses a prompt appended while it retires", func() {
		brief := newBrief(ns, "cel-lens", numbered(1, 12)...)
		brief.Spec.Paused = true
		mustCreate(brief)
		stale := brief.DeepCopy()
		stale.Status.PromptsPlanned = 12
		Expect(patch(brief, func() { brief.Spec.Prompts = append(brief.Spec.Prompts, "p13") })).To(Succeed())

		r := &BriefReconciler{Client: k8sClient}
		Expect(r.retirePrompts(ctx, stale)).To(MatchError(errStaleBrief))
		Expect(getBrief(Default, ns).Spec.Prompts).To(Equal(numbered(1, 13)))
	})
})
