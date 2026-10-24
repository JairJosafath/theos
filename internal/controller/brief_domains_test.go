package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// #1760 (docs/plans/domains.md, decision 9): the planner picks a new system's
// Domains from the catalog's installed ones.
var _ = Describe("Brief rounds with Domains", func() {
	It("lists the installed Domains in catalog.yaml, and the stand-in planner names the one the idea says", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		briefAgents(ns, 2)
		// A Standard that fits python-fastapi, so the stand-in picks the
		// Framework whose template the fake pins, and k8s-manifests ships it.
		mustCreate(newStandard(ns, "python-standard", "python-fastapi"))
		// addDomainTemplate creates each Domain and registers the template it
		// pins with the fake; without the templates the Domain layer failed
		// UnpinnedRef and the Brief stayed Applying (seen landing #1617).
		addDomainTemplate(ns, "webshop", markdownDomain("webshop"))
		addDomainTemplate(ns, "payments", markdownDomain("payments"))
		brief := newBrief(ns, "cel-lens")
		brief.Spec.Idea = "A webshop for plants."
		mustCreate(brief)

		By("listing each Domain with its name in catalog.yaml")
		var workspace scaffold.Tree
		Eventually(func(g Gomega) {
			getTask(g, ns, "cel-lens-plan-1")
			workspace = gitFake.mainFiles(ns, briefRepo)
			g.Expect(string(workspace[model.WorkspaceCatalogFile].Data)).To(ContainSubstring(
				"domains:\n    - name: payments\n    - name: webshop\n"))
		}).Should(Succeed())

		By("planning the round with the stand-in planner, from the workspace the operator wrote")
		files := make(map[string]string, len(workspace))
		for path, f := range workspace {
			files[path] = string(f.Data)
		}
		planned, err := model.StandInPlan(files)
		Expect(err).NotTo(HaveOccurred())
		plan := make(scaffold.Tree, len(planned))
		for path, data := range planned {
			plan[path] = scaffold.File{Data: []byte(data)}
		}
		planRound(ns, 1, 1, plan)

		By("applying it: the new system names the webshop Domain, the Brief's idea's word, and the summary says so")
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
			g.Expect(condition(b.Status.Conditions, c4v1beta1.ConditionPlanned)).To(And(
				HaveField("Status", metav1.ConditionTrue), HaveField("Reason", model.ReasonApplied)))
		}).Should(Succeed())
		system := &c4v1beta1.SoftwareSystem{}
		Expect(k8sClient.Get(ctx, key(ns, "cel-lens"), system)).To(Succeed())
		Expect(system.Spec.DomainRefs).To(Equal([]string{"webshop"}))
		brief = getBrief(Default, ns)
		Expect(brief.Status.Created).To(ContainElement(c4v1beta1.BriefObject{Kind: "SoftwareSystem", Name: "cel-lens"}))
		Expect(brief.Status.History).To(HaveLen(1))
		Expect(brief.Status.History[0].Summary).To(ContainSubstring("SoftwareSystem cel-lens is about the one Domain webshop."))
	})
})
