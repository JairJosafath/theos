package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
)

// conversionSpecs are a minimal valid spec of every kind but Project, which
// projectIn already made, named after its namespace.
var conversionSpecs = map[string]map[string]any{
	"Agent":  {"image": "agent:v1"},
	"Brief":  {"idea": "a shop"},
	"Bundle": {"frameworkRef": map[string]any{"name": "go"}},
	"Compute": {"supportedTypes": []any{"service"},
		"template": map[string]any{"url": "https://x", "ref": "v1"}},
	"Connector": {"supportedTypes": []any{"service"},
		"template": map[string]any{"url": "https://x", "ref": "v1"}},
	"Container": {"type": "service", "frameworkRef": map[string]any{"name": "go"},
		"gitProviderRef": map[string]any{"name": "gitea"}, "softwareSystemRef": map[string]any{"name": "shop"}},
	"Crew": {"containerRef": map[string]any{"name": "api"},
		"implementer": map[string]any{"name": "a"}, "reviewer": map[string]any{"name": "b"}},
	"DataStore": {"connectorRef": map[string]any{"name": "pg"}, "softwareSystemRef": map[string]any{"name": "shop"}},
	"Deployment": {"supportedTypes": []any{"service"},
		"template": map[string]any{"url": "https://x", "ref": "v1"}},
	"Domain":       {"template": map[string]any{"url": "https://x", "ref": "v1"}},
	"DeployTarget": {"argoCD": map[string]any{"destination": map[string]any{"namespace": "shop"}}},
	"Framework": {"supportedTypes": []any{"service"},
		"template": map[string]any{"url": "https://x", "ref": "v1"}},
	"GitProvider": {"credentialsSecretRef": map[string]any{"name": "git"}, "owner": "acme",
		"url": "https://git.example"},
	"Person": {"displayName": "Ada"},
	"Relationship": {"source": map[string]any{"kind": "Person", "name": "ada"},
		"target": map[string]any{"kind": "Person", "name": "bob"}},
	"Session":        {"actor": "human", "containerRef": map[string]any{"name": "api"}},
	"SoftwareSystem": {"displayName": "Shop"},
	"Standard": {"supportedTypes": []any{"service"}, "supportedFrameworks": []any{"go"},
		"template": map[string]any{"url": "https://x", "ref": "v1"}},
	"Task": {"agentRef": map[string]any{"name": "a"}, "containerRef": map[string]any{"name": "api"},
		"goal": "build it"},
}

// readAt reads the object kind/name of ns at the given group version.
func readAt(g Gomega, gv, kind, ns, name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(gv)
	obj.SetKind(kind)
	g.Expect(k8sClient.Get(ctx, key(ns, name), obj)).To(Succeed())
	return obj
}

// withoutEmpty is obj without its empty maps, at any depth. The conversion
// webhook encodes the Go types, which write an empty struct field such as an
// Agent's resources as {} even under omitempty; absent and {} mean the same.
func withoutEmpty(obj map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range obj {
		if m, ok := v.(map[string]any); ok {
			if v = withoutEmpty(m); len(v.(map[string]any)) == 0 {
				continue
			}
		}
		out[k] = v
	}
	return out
}

var _ = Describe("API versions", func() {
	alpha := c4v1alpha1.GroupVersion.String()
	beta := c4v1beta1.GroupVersion.String()

	It("serves every kind made at v1alpha1 at v1beta1 and back unchanged (#1615)", func() {
		ns := newProjectNamespace()
		names := map[string]string{"Project": ns}
		for kind, spec := range conversionSpecs {
			obj := &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
			obj.SetAPIVersion(alpha)
			obj.SetKind(kind)
			obj.SetNamespace(ns)
			obj.SetName("conv")
			Expect(k8sClient.Create(ctx, obj)).To(Succeed(), kind)
			names[kind] = "conv"
		}
		Expect(names).To(HaveLen(20))

		for kind, name := range names {
			// A reconciler may write status between the reads; read again until
			// all three see one resourceVersion.
			Eventually(func(g Gomega) {
				first := readAt(g, alpha, kind, ns, name)
				hub := readAt(g, beta, kind, ns, name)
				back := readAt(g, alpha, kind, ns, name)
				g.Expect(hub.GetResourceVersion()).To(Equal(first.GetResourceVersion()))
				g.Expect(back.GetResourceVersion()).To(Equal(first.GetResourceVersion()))
				hub.SetAPIVersion(alpha)
				g.Expect(equality.Semantic.DeepEqual(withoutEmpty(hub.Object), withoutEmpty(first.Object))).To(BeTrue(),
					"%s at v1beta1 differs:\n%v\n%v", kind, hub.Object, first.Object)
				g.Expect(equality.Semantic.DeepEqual(back.Object, first.Object)).To(BeTrue(), kind)
			}).Should(Succeed(), kind)
		}
	})

	It("stores a change made at v1beta1 so v1alpha1 reads it (#1615)", func() {
		ns := newProjectNamespace()
		person := &c4v1alpha1.Person{}
		person.Namespace, person.Name = ns, "ada"
		person.Spec.DisplayName = "Ada"
		Expect(k8sClient.Create(ctx, person)).To(Succeed())

		Eventually(func(g Gomega) {
			hub := &c4v1beta1.Person{}
			g.Expect(k8sClient.Get(ctx, key(ns, "ada"), hub)).To(Succeed())
			hub.Spec.Description = "Counts the beans"
			g.Expect(k8sClient.Update(ctx, hub)).To(Succeed())
		}).Should(Succeed())

		got := &c4v1alpha1.Person{}
		Expect(k8sClient.Get(ctx, key(ns, "ada"), got)).To(Succeed())
		Expect(got.Spec).To(Equal(c4v1alpha1.PersonSpec{DisplayName: "Ada", Description: "Counts the beans"}))
		Expect(got.Annotations).To(BeEmpty())
	})
})
