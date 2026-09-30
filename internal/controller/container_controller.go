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
	"context"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// ContainerReconciler makes a Container a child of its SoftwareSystem and counts
// its active Sessions.
type ContainerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=c4.factory.io,resources=containers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=c4.factory.io,resources=containers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=c4.factory.io,resources=containers/finalizers,verbs=update

// Reconcile adopts the Container and updates its status.
func (r *ContainerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var container c4v1alpha1.Container
	if err := r.Get(ctx, req.NamespacedName, &container); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	status := container.Status.DeepCopy()

	active, err := r.countSessions(ctx, &container)
	if err != nil {
		return ctrl.Result{}, err
	}
	status.ActiveSessions = int32(active)

	var system c4v1alpha1.SoftwareSystem
	err = r.Get(ctx, types.NamespacedName{Namespace: container.Namespace, Name: container.Spec.SoftwareSystemRef.Name}, &system)
	switch {
	case apierrors.IsNotFound(err):
		status.Phase = c4v1alpha1.ContainerPending
		setCondition(&status.Conditions, container.Generation, c4v1alpha1.ConditionReferencesResolved,
			metav1.ConditionFalse, c4v1alpha1.ReasonNotFound,
			notFoundMessage([]string{"SoftwareSystem " + container.Spec.SoftwareSystemRef.Name}))
		return ctrl.Result{}, r.writeStatus(ctx, &container, status)
	case err != nil:
		return ctrl.Result{}, err
	}
	labels := projectLabels(container.Namespace)
	labels[c4v1alpha1.LabelSoftwareSystem] = system.Name
	if err := adopt(ctx, r.Client, r.Scheme, &system, &container, labels); err != nil {
		return ctrl.Result{}, err
	}

	if status.Phase == "" {
		status.Phase = c4v1alpha1.ContainerPending
	}
	return ctrl.Result{}, r.writeStatus(ctx, &container, status)
}

func (r *ContainerReconciler) countSessions(ctx context.Context, container *c4v1alpha1.Container) (int, error) {
	var sessions c4v1alpha1.SessionList
	if err := r.List(ctx, &sessions, client.InNamespace(container.Namespace),
		client.MatchingFields{indexContainer: container.Name}); err != nil {
		return 0, err
	}
	return countActive(sessions.Items), nil
}

func (r *ContainerReconciler) writeStatus(ctx context.Context, container *c4v1alpha1.Container, status *c4v1alpha1.ContainerStatus) error {
	status.ObservedGeneration = container.Generation
	if equality.Semantic.DeepEqual(&container.Status, status) {
		return nil
	}
	orig := container.DeepCopy()
	container.Status = *status
	return r.Status().Patch(ctx, container, client.MergeFrom(orig))
}

// SetupWithManager sets up the controller with the Manager.
func (r *ContainerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	toContainer := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
		session, ok := obj.(*c4v1alpha1.Session)
		if !ok {
			return nil
		}
		return []reconcile.Request{requestFor(session.Namespace, session.Spec.ContainerRef.Name)}
	})
	systemContainers := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var containers c4v1alpha1.ContainerList
		if err := mgr.GetClient().List(ctx, &containers, client.InNamespace(obj.GetNamespace()),
			client.MatchingFields{indexSoftwareSystem: obj.GetName()}); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(containers.Items))
		for _, c := range containers.Items {
			reqs = append(reqs, requestFor(c.Namespace, c.Name))
		}
		return reqs
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1alpha1.Container{}).
		Watches(&c4v1alpha1.Session{}, toContainer, sessionPhaseChanges()).
		Watches(&c4v1alpha1.SoftwareSystem{}, systemContainers, createOrDelete()).
		Named("container").
		Complete(r)
}
