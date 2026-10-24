package controller

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// bundledLinterPlan is linterPlan with the linter on bundle instead of its sides.
func bundledLinterPlan(ns, bundle string) scaffold.Tree {
	GinkgoHelper()
	files := linterPlan(ns, 1)
	sides := "  frameworkRef: {name: python-fastapi}\n  computeRef: {name: small}\n  deploymentRef: {name: k8s-manifests}\n"
	docs := string(files[model.PlanModelFile].Data)
	Expect(docs).To(ContainSubstring(sides))
	files[model.PlanModelFile] = scaffold.File{Data: []byte(strings.Replace(docs, sides, "  bundleRef: {name: "+bundle+"}\n", 1))}
	return files
}

// #1622: the planner may name a Bundle instead of the four sides.
var _ = Describe("Brief rounds on a Bundle", func() {
	It("lists the Bundles in catalog.yaml, refuses one that doesn't support the type, and applies one that does", func() {
		ns := newProjectNamespace()
		createFactory(ns)
		briefAgents(ns, 2)
		mustCreate(newBundle(ns, "python-service", "python-fastapi", "small", "k8s-manifests"),
			newBundle(ns, "go-operator", "go-kubebuilder", "", ""))
		mustCreate(newBrief(ns, "cel-lens"))

		By("listing each Bundle with its entries and the types they all support")
		Eventually(func(g Gomega) {
			getTask(g, ns, "cel-lens-plan-1")
			catalog := string(gitFake.mainFiles(ns, briefRepo)[model.WorkspaceCatalogFile].Data)
			g.Expect(catalog).To(ContainSubstring("bundles:\n    - name: go-operator\n      framework: go-kubebuilder\n" +
				"      supportedTypes:\n        - operator\n    - name: python-service\n      framework: python-fastapi\n" +
				"      compute: small\n      deployment: k8s-manifests\n      supportedTypes:\n        - service\n"))
		}).Should(Succeed())

		By("refusing a service on a Bundle whose Framework doesn't support it, as the Container would")
		planRound(ns, 1, 1, bundledLinterPlan(ns, "go-operator"))
		Eventually(func(g Gomega) {
			task := getTask(g, ns, "cel-lens-plan-1")
			g.Expect(task.Status.Round).To(Equal(int32(2)))
			g.Expect(task.Status.Feedback).To(ContainSubstring(
				"Container/linter: bundleRef: bundle go-operator: go-kubebuilder does not support service"))
		}).Should(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key(ns, "linter"), &c4v1beta1.Container{}))).To(BeTrue())

		By("making the Container on the Bundle, with none of the sides, and expanding it")
		planRound(ns, 1, 2, bundledLinterPlan(ns, "python-service"))
		Eventually(func(g Gomega) {
			b := getBrief(g, ns)
			g.Expect(b.Status.Phase).To(Equal(c4v1beta1.BriefPlanned))
			g.Expect(condition(b.Status.Conditions, c4v1beta1.ConditionPlanned)).To(And(
				HaveField("Status", metav1.ConditionTrue), HaveField("Reason", model.ReasonApplied)))
		}).Should(Succeed())
		container := &c4v1beta1.Container{}
		Expect(k8sClient.Get(ctx, key(ns, "linter"), container)).To(Succeed())
		Expect(container.Spec.BundleRef).To(Equal(&c4v1beta1.LocalRef{Name: "python-service"}))
		Expect(container.Spec.FrameworkRef.Name).To(BeEmpty())
		Expect(container.Spec.ComputeRef).To(BeNil())
		Expect(container.Spec.DeploymentRef).To(BeNil())
		Expect(container.Status.Bundle).To(Equal(&c4v1beta1.ContainerBundleStatus{Name: "python-service",
			Framework: "python-fastapi", Compute: "small", Deployment: "k8s-manifests"}))
		Expect(getBrief(Default, ns).Status.Created).To(ContainElement(
			c4v1beta1.BriefObject{Kind: "Container", Name: "linter"}))
	})
})
