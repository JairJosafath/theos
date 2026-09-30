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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// newProjectNamespace creates a fresh namespace with a Project named after it, so
// every test has its own story.
func newProjectNamespace() string {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "story-"}}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	Expect(k8sClient.Create(ctx, &c4v1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: ns.Name, Namespace: ns.Name},
		Spec:       c4v1alpha1.ProjectSpec{DisplayName: "Test story"},
	})).To(Succeed())
	return ns.Name
}

func key(ns, name string) types.NamespacedName {
	return types.NamespacedName{Namespace: ns, Name: name}
}

func mustCreate(objs ...client.Object) {
	GinkgoHelper()
	for _, obj := range objs {
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
	}
}

func objectMeta(ns, name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: ns, Name: name}
}

func newSystem(ns, name string) *c4v1alpha1.SoftwareSystem {
	return &c4v1alpha1.SoftwareSystem{
		ObjectMeta: objectMeta(ns, name),
		Spec:       c4v1alpha1.SoftwareSystemSpec{DisplayName: name},
	}
}

func newPerson(ns, name string) *c4v1alpha1.Person {
	return &c4v1alpha1.Person{ObjectMeta: objectMeta(ns, name)}
}

func newContainer(ns, name, system string, typ c4v1alpha1.ContainerType) *c4v1alpha1.Container {
	return &c4v1alpha1.Container{
		ObjectMeta: objectMeta(ns, name),
		Spec: c4v1alpha1.ContainerSpec{
			SoftwareSystemRef: c4v1alpha1.LocalRef{Name: system},
			Type:              typ,
			GitProviderRef:    c4v1alpha1.LocalRef{Name: "gitea"},
			FrameworkRef:      c4v1alpha1.LocalRef{Name: "python-fastapi"},
			ComputeRef:        &c4v1alpha1.LocalRef{Name: "small"},
			DeploymentRef:     &c4v1alpha1.LocalRef{Name: "k8s-manifests"},
		},
	}
}

// newOrdersDB returns the sample story's orders-db: a Postgres DataStore in webshop-core.
func newOrdersDB(ns string) *c4v1alpha1.DataStore {
	return &c4v1alpha1.DataStore{
		ObjectMeta: objectMeta(ns, "orders-db"),
		Spec: c4v1alpha1.DataStoreSpec{
			SoftwareSystemRef: c4v1alpha1.LocalRef{Name: "webshop-core"},
			ConnectorRef:      c4v1alpha1.LocalRef{Name: "postgres"},
		},
	}
}

func newRelationship(ns, name string, source, target c4v1alpha1.ElementRef) *c4v1alpha1.Relationship {
	return &c4v1alpha1.Relationship{
		ObjectMeta: objectMeta(ns, name),
		Spec:       c4v1alpha1.RelationshipSpec{Source: source, Target: target},
	}
}

func element(kind c4v1alpha1.ElementKind, name string) c4v1alpha1.ElementRef {
	return c4v1alpha1.ElementRef{Kind: kind, Name: name}
}

func newSession(ns, name, container string) *c4v1alpha1.Session {
	return &c4v1alpha1.Session{
		ObjectMeta: objectMeta(ns, name),
		Spec: c4v1alpha1.SessionSpec{
			ContainerRef: c4v1alpha1.LocalRef{Name: container},
			Actor:        c4v1alpha1.ActorAgent,
			Summary:      "Adding order validation",
		},
	}
}

func newCatalogSpec(url, ref string, supported ...c4v1alpha1.ContainerType) c4v1alpha1.CatalogEntrySpec {
	return c4v1alpha1.CatalogEntrySpec{
		SupportedTypes: supported,
		Template:       c4v1alpha1.TemplateSource{URL: url, Ref: ref},
	}
}

// controlledBy asserts that obj's controller ownerReference points at owner.
func controlledBy(obj, owner client.Object) bool {
	return metav1.IsControlledBy(obj, owner)
}

// condition returns the condition of a type, or nil.
func condition(conds []metav1.Condition, typ string) *metav1.Condition {
	return meta.FindStatusCondition(conds, typ)
}
