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
	DeferCleanup(stopPolling, ns.Name)
	return ns.Name
}

// stopPolling deletes the kinds that requeue on a timer once a test is done.
// Envtest never deletes a namespace's objects, and each one left behind would
// keep polling on its controller's single worker, every second, for the rest
// of the suite; on a loaded machine they crowd out the running test's reconciles.
// Briefs go first, so none makes a Crew or a Plan Task again, then Crews, so
// none makes a Task again after its Tasks are gone.
func stopPolling(ns string) {
	GinkgoHelper()
	for _, obj := range []client.Object{
		&c4v1alpha1.Brief{}, &c4v1alpha1.Crew{}, &c4v1alpha1.Task{}, &c4v1alpha1.Session{}, &c4v1alpha1.Container{},
	} {
		Expect(k8sClient.DeleteAllOf(ctx, obj, client.InNamespace(ns))).To(Succeed())
	}
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

// patch applies mutate to obj and sends the change as a merge patch, so a test's
// write can't collide with the operator adopting or labelling the object meanwhile.
func patch(obj client.Object, mutate func()) error {
	orig, _ := obj.DeepCopyObject().(client.Object)
	mutate()
	return k8sClient.Patch(ctx, obj, client.MergeFrom(orig))
}

// controlledBy asserts that obj's controller ownerReference points at owner.
func controlledBy(obj, owner client.Object) bool {
	return metav1.IsControlledBy(obj, owner)
}

// condition returns the condition of a type, or nil.
func condition(conds []metav1.Condition, typ string) *metav1.Condition {
	return meta.FindStatusCondition(conds, typ)
}
