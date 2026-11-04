/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"testing"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// #1573: a status written between the Container's read and its adoption
// stays. The adoption's patch reads the Container back, status included; a
// status computed from the first read would patch the newer one away.
func TestAdoptingAContainerKeepsAStatusWrittenSinceTheRead(t *testing.T) {
	scheme := briefScheme(t)
	system := newSystem("apps", "shop")
	system.Spec.Location = c4v1beta1.SystemExternal // the reconcile writes Pending and stops
	container := &c4v1beta1.Container{ObjectMeta: objectMeta("apps", "orders-api"),
		Spec: c4v1beta1.ContainerSpec{SoftwareSystemRef: c4v1beta1.LocalRef{Name: system.Name}}}
	const url = "http://gitea/apps/orders-api.git"
	written := false
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&c4v1beta1.Container{}).
		WithObjects(system, container).
		WithIndex(&c4v1beta1.Session{}, indexContainer, func(o client.Object) []string {
			return []string{o.(*c4v1beta1.Session).Spec.ContainerRef.Name}
		}).
		WithInterceptorFuncs(interceptor.Funcs{Patch: func(ctx context.Context, c client.WithWatch,
			obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if _, ok := obj.(*c4v1beta1.Container); ok && !written {
				written = true
				var current c4v1beta1.Container
				if err := c.Get(ctx, client.ObjectKeyFromObject(obj), &current); err != nil {
					return err
				}
				current.Status.RepositoryURL = url
				if err := c.Status().Update(ctx, &current); err != nil {
					return err
				}
			}
			return c.Patch(ctx, obj, patch, opts...)
		}}).Build()
	r := &ContainerReconciler{Client: c, Scheme: scheme}
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(container)}); err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Fatal("the reconcile didn't adopt the Container")
	}
	var got c4v1beta1.Container
	if err := c.Get(ctx, client.ObjectKeyFromObject(container), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.RepositoryURL != url {
		t.Errorf("repositoryURL is %q, want %q: the reconcile patched the newer status away",
			got.Status.RepositoryURL, url)
	}
	if got.Status.Phase != c4v1beta1.ContainerPending {
		t.Errorf("phase is %q, want %q", got.Status.Phase, c4v1beta1.ContainerPending)
	}
}
