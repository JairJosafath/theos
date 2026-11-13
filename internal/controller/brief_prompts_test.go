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
	"encoding/json"
	"fmt"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// numbered returns prompts "p<from>" to "p<to>".
func numbered(from, to int) []string {
	var prompts []string
	for i := from; i <= to; i++ {
		prompts = append(prompts, fmt.Sprintf("p%d", i))
	}
	return prompts
}

// appendRetiring is the JSON patch the UI server's append sends to a full
// list (ui/server.mjs): guarded by the prompts it read, not the
// resourceVersion the operator's status writes move, it drops the planned
// prompts past the newest ten and appends prompt in one write.
func appendRetiring(brief *c4v1beta1.Brief, prompt string) client.Patch {
	GinkgoHelper()
	n := model.PromptsToRetire(int(brief.Spec.PromptsRetired), len(brief.Spec.Prompts),
		int(brief.Status.PromptsPlanned))
	ops, err := json.Marshal([]map[string]any{
		{"op": "test", "path": "/spec/prompts", "value": brief.Spec.Prompts},
		{"op": "replace", "path": "/spec/prompts", "value": append(slices.Clone(brief.Spec.Prompts[n:]), prompt)},
		{"op": "add", "path": "/spec/promptsRetired", "value": int(brief.Spec.PromptsRetired) + n},
	})
	Expect(err).NotTo(HaveOccurred())
	return client.RawPatch(types.JSONPatchType, ops)
}

var _ = Describe("Brief prompts (#835, #855)", func() {
	var ns string
	BeforeEach(func() { ns = newProjectNamespace() })

	// planned49 makes Brief cel-lens with 50 prompts, 49 of them planned.
	planned49 := func() *c4v1beta1.Brief {
		GinkgoHelper()
		brief := newBrief(ns, "cel-lens", numbered(1, 50)...)
		mustCreate(brief)
		Expect(k8sClient.Status().Patch(ctx, brief, client.RawPatch(types.MergePatchType,
			[]byte(`{"status":{"round":9,"roundPrompts":49,"promptsPlanned":49}}`)))).To(Succeed())
		return getBrief(Default, ns)
	}

	It("never writes the spec, even once rounds planned more than ten prompts", func() {
		brief := planned49()
		Expect(patch(brief, func() { brief.Spec.Paused = true })).To(Succeed())
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.ObservedGeneration).To(Equal(b.Generation))
		}).Should(Succeed(), "a reconcile read the Brief with 49 prompts planned")
		Consistently(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Spec.Prompts).To(Equal(numbered(1, 50)))
			g.Expect(b.Spec.PromptsRetired).To(BeZero())
			g.Expect(b.Generation).To(Equal(brief.Generation))
		}, 2*time.Second).Should(Succeed())
	})

	It("refuses a 51st prompt that retires none, saying how to retire", func() {
		brief := planned49()
		Expect(patch(brief, func() { brief.Spec.Prompts = append(brief.Spec.Prompts, "p51") })).
			To(MatchError(ContainSubstring("drop the oldest prompts rounds have planned")))
	})

	It("takes the 51st prompt when the append retires the planned ones in the same write", func() {
		brief := planned49()
		Expect(k8sClient.Patch(ctx, brief, appendRetiring(brief, "p51"))).To(Succeed())
		Expect(brief.Spec.PromptsRetired).To(Equal(int32(49 - model.KeptPlannedPrompts)))
		Expect(brief.Spec.Prompts).To(Equal(numbered(40, 51)))
		Expect(briefPrompts(brief)).To(HaveLen(51))
		Expect(model.RoundPrompts(briefPrompts(brief), int(brief.Status.PromptsPlanned))).
			To(Equal([]string{"p50", "p51"}), "the next round takes the two no round planned")
	})

	It("never loses a prompt appended meanwhile", func() {
		stale := planned49()
		brief := stale.DeepCopy()
		Expect(k8sClient.Patch(ctx, brief, appendRetiring(brief, "p51"))).To(Succeed())
		Expect(k8sClient.Patch(ctx, stale, appendRetiring(stale, "other"))).NotTo(Succeed())
		Expect(getBrief(Default, ns).Spec.Prompts).To(Equal(numbered(40, 51)))
	})
})
