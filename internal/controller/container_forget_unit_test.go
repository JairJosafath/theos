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
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// #1571: a Container that is being deleted leaves nothing behind in the
// reconciler's per-UID maps; another Container's entries stay.
func TestReconcilingADeletedContainerForgetsItsEntries(t *testing.T) {
	scheme := briefScheme(t)
	now := metav1.Now()
	container := &c4v1beta1.Container{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "orders-api",
		UID: types.UID("uid-orders"), DeletionTimestamp: &now, Finalizers: []string{"test/hold"}}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(container).Build()
	r := &ContainerReconciler{Client: c, Scheme: scheme}
	const other = types.UID("uid-other")
	r.imageSaid.Store(container.UID, "v0.1.0 pushed")
	r.imageSaid.Store(other, "v0.2.0 pushed")
	r.imagePushed.Store(container.UID, &c4v1beta1.ContainerImageStatus{Tag: "v0.1.0"})
	r.imagePushed.Store(other, &c4v1beta1.ContainerImageStatus{Tag: "v0.2.0"})
	r.deploySaid.Store(string(container.UID)+"/staging", "deployed")
	r.deploySaid.Store(string(container.UID)+"/prod", "deployed")
	r.deploySaid.Store(string(other)+"/staging", "deployed")
	r.deploySources.Store(string(container.UID)+"/apps/open", sourceRead{tag: "v0.1.0"})
	r.deploySources.Store(string(other)+"/apps/open", sourceRead{tag: "v0.2.0"})

	if _, err := r.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(container)}); err != nil {
		t.Fatal(err)
	}

	keys := func(m interface{ Range(func(any, any) bool) }) []any {
		var got []any
		m.Range(func(key, _ any) bool { got = append(got, key); return true })
		return got
	}
	for name, m := range map[string]interface{ Range(func(any, any) bool) }{
		"imageSaid": &r.imageSaid, "imagePushed": &r.imagePushed, "deploySaid": &r.deploySaid,
		"deploySources": &r.deploySources,
	} {
		got := keys(m)
		if len(got) != 1 {
			t.Errorf("%s keeps %v, want only the other Container's entry", name, got)
		}
	}
	if _, ok := r.imageSaid.Load(other); !ok {
		t.Error("imageSaid lost the other Container's entry")
	}
	if _, ok := r.imagePushed.Load(other); !ok {
		t.Error("imagePushed lost the other Container's entry")
	}
	if _, ok := r.deploySaid.Load(string(other) + "/staging"); !ok {
		t.Error("deploySaid lost the other Container's entry")
	}
}

// #1629: a Container deleted outright, with no finalizer, is gone by the
// next reconcile, whose Get is NotFound with no UID; the reconciler still
// forgets its entries, and another Container's stay.
func TestReconcilingAContainerGoneWithoutAFinalizerForgetsItsEntries(t *testing.T) {
	scheme := briefScheme(t)
	container := &c4v1beta1.Container{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "orders-api",
		UID: types.UID("uid-orders")}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(container).
		WithStatusSubresource(&c4v1beta1.Container{}).Build()
	r := &ContainerReconciler{Client: c, Scheme: scheme}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(container)}
	// The first reconcile sees the Container; whatever it makes of its
	// missing references, it learns the UID.
	_, _ = r.Reconcile(context.Background(), req)

	const other = types.UID("uid-other")
	r.imageSaid.Store(container.UID, "v0.1.0 pushed")
	r.imageSaid.Store(other, "v0.2.0 pushed")
	r.imagePushed.Store(container.UID, &c4v1beta1.ContainerImageStatus{Tag: "v0.1.0"})
	r.imagePushed.Store(other, &c4v1beta1.ContainerImageStatus{Tag: "v0.2.0"})
	r.deploySaid.Store(string(container.UID)+"/staging", "deployed")
	r.deploySaid.Store(string(other)+"/staging", "deployed")
	r.deploySources.Store(string(container.UID)+"/apps/open", sourceRead{tag: "v0.1.0"})
	r.deploySources.Store(string(other)+"/apps/open", sourceRead{tag: "v0.2.0"})

	if err := c.Delete(context.Background(), container); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	for name, m := range map[string]interface{ Range(func(any, any) bool) }{
		"imageSaid": &r.imageSaid, "imagePushed": &r.imagePushed, "deploySaid": &r.deploySaid,
		"deploySources": &r.deploySources,
	} {
		var got []any
		m.Range(func(key, _ any) bool { got = append(got, key); return true })
		if len(got) != 1 {
			t.Errorf("%s keeps %v, want only the other Container's entry", name, got)
		}
	}
	if _, ok := r.imageSaid.Load(other); !ok {
		t.Error("imageSaid lost the other Container's entry")
	}
	if _, ok := r.deploySaid.Load(string(other) + "/staging"); !ok {
		t.Error("deploySaid lost the other Container's entry")
	}
	if _, ok := r.uids.Load(req.NamespacedName); ok {
		t.Error("uids keeps the deleted Container's name")
	}
}
