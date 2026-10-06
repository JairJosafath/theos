/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// SessionReconciler makes a Session a child of its Container and marks it Stale
// when heartbeats stop. The client writes the rest of the status.
type SessionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// StaleAfter is how long a Session may go without a heartbeat.
	StaleAfter time.Duration
	// Now returns the current time; tests replace it.
	Now func() time.Time
}

// +kubebuilder:rbac:groups=c4.factory.io,resources=sessions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=c4.factory.io,resources=sessions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=c4.factory.io,resources=sessions/finalizers,verbs=update

// Reconcile adopts the Session and marks it Stale once its heartbeat is too old.
func (r *SessionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var session c4v1alpha1.Session
	if err := r.Get(ctx, req.NamespacedName, &session); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var container c4v1alpha1.Container
	err := r.Get(ctx, types.NamespacedName{Namespace: session.Namespace, Name: session.Spec.ContainerRef.Name}, &container)
	switch {
	case apierrors.IsNotFound(err):
		// The Container watch brings the Session back when it appears.
	case err != nil:
		return ctrl.Result{}, err
	default:
		labels := projectLabels(session.Namespace)
		labels[c4v1alpha1.LabelSoftwareSystem] = container.Spec.SoftwareSystemRef.Name
		labels[c4v1alpha1.LabelContainer] = container.Name
		if err := adopt(ctx, r.Client, r.Scheme, &container, &session, labels); err != nil {
			return ctrl.Result{}, err
		}
	}

	if session.Status.Phase == c4v1alpha1.SessionStale {
		return ctrl.Result{}, nil
	}
	var last time.Time
	if session.Status.LastHeartbeat != nil {
		last = session.Status.LastHeartbeat.Time
	}
	stale, remaining := model.SessionFreshness(last, session.CreationTimestamp.Time, r.now(), r.staleAfter())
	if !stale {
		return ctrl.Result{RequeueAfter: remaining + time.Second}, nil
	}

	// Optimistic lock: a heartbeat that lands meanwhile wins, and we look again.
	orig := session.DeepCopy()
	session.Status.Phase = c4v1alpha1.SessionStale
	if err := r.Status().Patch(ctx, &session, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		return ctrl.Result{}, err
	}
	log.Info("Marked Session Stale", "session", session.Name, "container", session.Spec.ContainerRef.Name,
		"lastHeartbeat", last)
	return ctrl.Result{}, nil
}

func (r *SessionReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *SessionReconciler) staleAfter() time.Duration {
	if r.StaleAfter > 0 {
		return r.StaleAfter
	}
	return model.DefaultStaleAfter
}

// SetupWithManager sets up the controller with the Manager.
func (r *SessionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// A Container appearing lets its waiting Sessions be adopted.
	toSessions := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var sessions c4v1alpha1.SessionList
		if err := mgr.GetClient().List(ctx, &sessions, client.InNamespace(obj.GetNamespace()),
			client.MatchingFields{indexContainer: obj.GetName()}); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(sessions.Items))
		for _, s := range sessions.Items {
			reqs = append(reqs, requestFor(s.Namespace, s.Name))
		}
		return reqs
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1alpha1.Session{}).
		Watches(&c4v1alpha1.Container{}, toSessions, createOrDelete()).
		Named("session").
		Complete(r)
}
