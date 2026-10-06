/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// BriefReconciler turns a Brief's idea and prompts into the model. For now it
// only marks a new Brief Pending: nothing plans yet (docs/plans/brief.md).
type BriefReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=c4.factory.io,resources=briefs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=c4.factory.io,resources=briefs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=c4.factory.io,resources=briefs/finalizers,verbs=update

// Reconcile marks a Brief that has no phase yet Pending.
func (r *BriefReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var brief c4v1alpha1.Brief
	if err := r.Get(ctx, req.NamespacedName, &brief); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if brief.Status.Phase != "" {
		return ctrl.Result{}, nil
	}

	orig := brief.DeepCopy()
	brief.Status.Phase = c4v1alpha1.BriefPending
	brief.Status.ObservedGeneration = brief.Generation
	if err := r.Status().Patch(ctx, &brief, client.MergeFrom(orig)); err != nil {
		return ctrl.Result{}, err
	}
	log.Info("Marked Brief Pending")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BriefReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1alpha1.Brief{}).
		Named("brief").
		Complete(r)
}
