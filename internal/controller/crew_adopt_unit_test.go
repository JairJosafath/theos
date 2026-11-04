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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// #1455: a release written between the Crew's read and its adoption stays.
// The adoption's patch reads the Crew back, release included; a status
// computed from the first read would patch the release away.
func TestAdoptingACrewKeepsAReleaseWrittenSinceTheRead(t *testing.T) {
	scheme := briefScheme(t)
	crew := newCrew("apps") // its Agents are missing: a step writes AgentNotFound
	container := &c4v1beta1.Container{ObjectMeta: objectMeta("apps", "orders-api")}
	released := false
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&c4v1beta1.Crew{}).
		WithObjects(crew, container).
		WithInterceptorFuncs(interceptor.Funcs{Patch: func(ctx context.Context, c client.WithWatch,
			obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if _, ok := obj.(*c4v1beta1.Crew); ok && !released {
				released = true
				var current c4v1beta1.Crew
				if err := c.Get(ctx, client.ObjectKeyFromObject(obj), &current); err != nil {
					return err
				}
				current.Status.LastRelease = &c4v1beta1.CrewRelease{Tag: "v0.1.0", Commit: "c-v0.1.0",
					Time: metav1.Now(), Run: model.RunSucceeded}
				if err := c.Status().Update(ctx, &current); err != nil {
					return err
				}
			}
			return c.Patch(ctx, obj, patch, opts...)
		}}).Build()
	r := &CrewReconciler{Client: c, APIReader: c, Scheme: scheme}
	ctx := context.Background()

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(crew)}); err != nil {
		t.Fatal(err)
	}
	if !released {
		t.Fatal("the reconcile didn't adopt the Crew")
	}
	var got c4v1beta1.Crew
	if err := c.Get(ctx, client.ObjectKeyFromObject(crew), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.LastRelease == nil || got.Status.LastRelease.Tag != "v0.1.0" {
		t.Errorf("lastRelease is %+v, want v0.1.0: the reconcile patched the newer release away",
			got.Status.LastRelease)
	}
	if got.Status.Phase != c4v1beta1.CrewWaiting {
		t.Errorf("phase is %q, want %q", got.Status.Phase, c4v1beta1.CrewWaiting)
	}
}
