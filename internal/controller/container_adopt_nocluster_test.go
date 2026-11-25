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
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/url"
	"testing/fstest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/nocluster"
	"github.com/JairJosafath/theos/internal/ports"
)

// suiteHosts gives each GitProvider its view of the suite's fake host, as
// the theos binary's adapters would.
type suiteHosts struct{}

func (suiteHosts) GitHost(cfg ports.Config) (ports.GitHost, error) { return gitFake.For(cfg), nil }

// modelFile is objs as a model file: each a document, with its kind and
// none of what the API server adds.
func modelFile(objs ...client.Object) []byte {
	GinkgoHelper()
	var b bytes.Buffer
	for _, obj := range objs {
		gvks, _, err := k8sClient.Scheme().ObjectKinds(obj)
		Expect(err).NotTo(HaveOccurred())
		obj.GetObjectKind().SetGroupVersionKind(gvks[0])
		u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
		Expect(err).NotTo(HaveOccurred())
		delete(u, "status")
		unstructured.RemoveNestedField(u, "metadata", "creationTimestamp")
		unstructured.RemoveNestedField(u, "metadata", "namespace")
		data, err := yaml.Marshal(u)
		Expect(err).NotTo(HaveOccurred())
		b.WriteString("---\n")
		b.Write(data)
	}
	return b.Bytes()
}

// applyStream creates each object of an exported stream but the Project,
// which newProjectNamespace made, and the Crews, which need Agents, and
// returns the Containers it created.
func applyStream(stream []byte) map[string]*unstructured.Unstructured {
	GinkgoHelper()
	containers := map[string]*unstructured.Unstructured{}
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(stream)))
	for {
		doc, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return containers
		}
		Expect(err).NotTo(HaveOccurred())
		var obj map[string]any
		Expect(yaml.Unmarshal(doc, &obj)).To(Succeed())
		u := &unstructured.Unstructured{Object: obj}
		if obj == nil || u.GetKind() == "Project" || u.GetKind() == "Crew" {
			continue
		}
		Expect(k8sClient.Create(ctx, u)).To(Succeed(), "creating %s %s", u.GetKind(), u.GetName())
		if u.GetKind() == "Container" {
			containers[u.GetName()] = u
		}
	}
}

// #295 (docs/plans/no-cluster.md, step 7): a project worked without a
// cluster moves to one unchanged. theos scaffold renders orders-api on the
// suite's fake host from the model files, as the operator would; theos
// export gives the Container adopt: true; applied, the operator adopts the
// repository as it is: Ready on its Standard, nothing pushed, no pull
// request opened.
var _ = Describe("Container adoption of a repository theos scaffolded without a cluster", func() {
	It("adopts it with its Standard, pushing nothing and opening no pull request (#295)", func() {
		ns := newProjectNamespace()
		token := rand.Text()
		mustCreate(&corev1.Secret{ObjectMeta: objectMeta(ns, "host-credentials"),
			StringData: map[string]string{"username": "c4admin", "token": token}})
		service := c4v1beta1.ContainerTypeService
		container := newContainer(ns, "orders-api", "webshop-core", service)
		// The suite's fake host serves the templates and the repositories.
		host, err := url.Parse(templatesURL)
		Expect(err).NotTo(HaveOccurred())
		container.Spec.DisplayName = "Orders API"
		container.Spec.StandardRef = &c4v1beta1.LocalRef{Name: "python-strict"}
		story := modelFile(
			&c4v1beta1.Project{ObjectMeta: objectMeta(ns, ns), Spec: c4v1beta1.ProjectSpec{DisplayName: "Test story"}},
			newSystem(ns, "webshop-core"), newOrdersDB(ns),
			newRelationship(ns, "orders-api-to-orders-db", element(c4v1beta1.ElementContainer, "orders-api"),
				element(c4v1beta1.ElementDataStore, "orders-db")),
			container)
		setup := modelFile(
			&c4v1beta1.GitProvider{ObjectMeta: objectMeta(ns, container.Spec.GitProviderRef.Name),
				Spec: c4v1beta1.GitProviderSpec{URL: host.Scheme + "://" + host.Host, Owner: ns,
					CredentialsSecretRef: c4v1beta1.LocalRef{Name: "host-credentials"}}},
			&c4v1beta1.Framework{ObjectMeta: objectMeta(ns, "python-fastapi"),
				Spec: newFrameworkSpec(templatesURL+"python-fastapi", "v1.6.0", service)},
			&c4v1beta1.Compute{ObjectMeta: objectMeta(ns, "small"),
				Spec: newCatalogSpec(templatesURL+"small", "v1.1.0", service)},
			&c4v1beta1.Deployment{ObjectMeta: objectMeta(ns, "k8s-manifests"),
				Spec: newCatalogSpec(templatesURL+"k8s-manifests", "v1.9.1", service)},
			&c4v1beta1.Connector{ObjectMeta: objectMeta(ns, "postgres"),
				Spec: c4v1beta1.ConnectorSpec{CatalogEntrySpec: newCatalogSpec(templatesURL+"postgres", "v1.3.0", service)}},
			newStandard(ns, "python-strict", "python-fastapi"))
		m, err := nocluster.Decode(story, setup, nil)
		Expect(err).NotTo(HaveOccurred())

		var out bytes.Buffer
		env := nocluster.Env{Hosts: suiteHosts{}, Templates: gitFake, Out: &out, Now: time.Now,
			CredentialHost: func(context.Context) (nocluster.Bound, error) {
				return nocluster.Bound{Host: host.Host, HTTP: true, From: "the spec"}, nil
			},
			Credential: func(context.Context, nocluster.Bound, string) (ports.Credentials, error) {
				return ports.Credentials{Username: "c4admin", Token: token}, nil
			}}
		Expect(nocluster.Scaffold(ctx, env, m)).To(Succeed(), out.String())
		Expect(gitFake.Repo(ns, "orders-api").Commits).To(HaveLen(1))

		var stream bytes.Buffer
		Expect(nocluster.Export(ctx, env, fstest.MapFS{nocluster.ModelFile: {Data: story},
			nocluster.SetupFile: {Data: setup}}, &stream)).To(Succeed())
		exported := applyStream(stream.Bytes())
		Expect(exported).To(HaveKey("orders-api"))
		adopt, _, _ := unstructured.NestedBool(exported["orders-api"].Object, "spec", "adopt")
		Expect(adopt).To(BeTrue(), "export sets adopt on a Container whose repository has commits")

		ready := eventuallyOrdersAPI(ns, c4v1beta1.ContainerReady)
		cond := condition(ready.Status.Conditions, c4v1beta1.ConditionScaffolded)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Message).To(Equal("Adopted repository " + ready.Status.RepositoryURL))
		Expect(ready.Status.Standard).NotTo(BeNil(), "main holds the Standard's files as it renders them")
		Expect(ready.Status.Standard.Name).To(Equal("python-strict"))
		Expect(ready.Status.Standard.Ref).To(Equal("v1.0.0"))
		Consistently(func(g Gomega) {
			g.Expect(gitFake.Repo(ns, "orders-api").Commits).To(HaveLen(1), "nothing pushed")
			g.Expect(gitFake.PullRequests(ns, "orders-api")).To(BeEmpty(), "no pull request opened")
		}, 3*time.Second, 200*time.Millisecond).Should(Succeed())
	})
})
