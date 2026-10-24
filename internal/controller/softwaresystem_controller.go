/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// SoftwareSystemReconciler rolls up the state of a system's Containers. The
// Project reconciler adopts the SoftwareSystem itself.
type SoftwareSystemReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=c4.factory.io,resources=softwaresystems,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=c4.factory.io,resources=softwaresystems/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=c4.factory.io,resources=softwaresystems/finalizers,verbs=update

// Reconcile counts the system's Containers, DataStores and active Sessions.
func (r *SoftwareSystemReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var system c4v1beta1.SoftwareSystem
	if err := r.Get(ctx, req.NamespacedName, &system); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	inSystem := client.MatchingFields{indexSoftwareSystem: system.Name}
	var containers c4v1beta1.ContainerList
	if err := r.List(ctx, &containers, client.InNamespace(system.Namespace), inSystem); err != nil {
		return ctrl.Result{}, err
	}
	var dataStores c4v1beta1.DataStoreList
	if err := r.List(ctx, &dataStores, client.InNamespace(system.Namespace), inSystem); err != nil {
		return ctrl.Result{}, err
	}

	ready, sessions := 0, 0
	for _, c := range containers.Items {
		if c.Status.Phase == c4v1beta1.ContainerReady {
			ready++
		}
		sessions += int(c.Status.ActiveSessions)
	}
	status := system.Status.DeepCopy()
	status.Containers = int32(len(containers.Items))
	status.ReadyContainers = int32(ready)
	status.DataStores = int32(len(dataStores.Items))
	status.ActiveSessions = int32(sessions)
	status.Summary = model.Summary(ready, len(containers.Items), sessions)

	if equality.Semantic.DeepEqual(&system.Status, status) {
		return ctrl.Result{}, nil
	}
	orig := system.DeepCopy()
	system.Status = *status
	return ctrl.Result{}, r.Status().Patch(ctx, &system, client.MergeFrom(orig))
}

// SetupWithManager sets up the controller with the Manager.
func (r *SoftwareSystemReconciler) SetupWithManager(mgr ctrl.Manager) error {
	toSystem := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
		var name string
		switch o := obj.(type) {
		case *c4v1beta1.Container:
			name = o.Spec.SoftwareSystemRef.Name
		case *c4v1beta1.DataStore:
			name = o.Spec.SoftwareSystemRef.Name
		}
		return []reconcile.Request{requestFor(obj.GetNamespace(), name)}
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1beta1.SoftwareSystem{}).
		Watches(&c4v1beta1.Container{}, toSystem).
		Watches(&c4v1beta1.DataStore{}, toSystem).
		Named("softwaresystem").
		Complete(r)
}
