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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// DataStoreReconciler makes a DataStore a child of its SoftwareSystem and checks
// its Connector exists.
type DataStoreReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=theos.factory.io,resources=datastores,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=theos.factory.io,resources=datastores/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=theos.factory.io,resources=datastores/finalizers,verbs=update
// +kubebuilder:rbac:groups=theos.factory.io,resources=connectors,verbs=get;list;watch

// Reconcile adopts the DataStore and reports whether its references resolve.
func (r *DataStoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var store c4v1beta1.DataStore
	if err := r.Get(ctx, req.NamespacedName, &store); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var missing []string
	external := false
	var system c4v1beta1.SoftwareSystem
	err := r.Get(ctx, types.NamespacedName{Namespace: store.Namespace, Name: store.Spec.SoftwareSystemRef.Name}, &system)
	switch {
	case apierrors.IsNotFound(err):
		missing = append(missing, "SoftwareSystem "+store.Spec.SoftwareSystemRef.Name)
	case err != nil:
		return ctrl.Result{}, err
	default:
		labels := projectLabels(store.Namespace)
		labels[c4v1beta1.LabelSoftwareSystem] = system.Name
		if err := adopt(ctx, r.Client, r.Scheme, &system, &store, labels); err != nil {
			return ctrl.Result{}, err
		}
		external = system.Spec.Location == c4v1beta1.SystemExternal
	}

	var connector c4v1beta1.Connector
	err = r.Get(ctx, types.NamespacedName{Namespace: store.Namespace, Name: store.Spec.ConnectorRef.Name}, &connector)
	switch {
	case apierrors.IsNotFound(err):
		missing = append(missing, "Connector "+store.Spec.ConnectorRef.Name)
	case err != nil:
		return ctrl.Result{}, err
	}

	status := store.Status.DeepCopy()
	if len(missing) > 0 {
		setCondition(&status.Conditions, store.Generation, c4v1beta1.ConditionReferencesResolved,
			metav1.ConditionFalse, c4v1beta1.ReasonNotFound, notFoundMessage(missing))
	} else if external {
		setCondition(&status.Conditions, store.Generation, c4v1beta1.ConditionReferencesResolved,
			metav1.ConditionFalse, c4v1beta1.ReasonExternalSystem, externalMessage(system.Name))
	} else {
		setCondition(&status.Conditions, store.Generation, c4v1beta1.ConditionReferencesResolved,
			metav1.ConditionTrue, c4v1beta1.ReasonResolved, "")
	}
	if equality.Semantic.DeepEqual(&store.Status, status) {
		return ctrl.Result{}, nil
	}
	orig := store.DeepCopy()
	store.Status = *status
	return ctrl.Result{}, r.Status().Patch(ctx, &store, client.MergeFrom(orig))
}

// externalMessage says why a Container or DataStore of an External system
// doesn't resolve.
func externalMessage(system string) string {
	return "SoftwareSystem " + system + " is External: the factory builds nothing for it"
}

// notFoundMessage lists missing references, such as "SoftwareSystem webshop-core not found".
func notFoundMessage(missing []string) string {
	return fmt.Sprintf("%s not found", strings.Join(missing, ", "))
}

// SetupWithManager sets up the controller with the Manager.
func (r *DataStoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// A SoftwareSystem or Connector appearing can resolve a waiting DataStore.
	allInNamespace := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var stores c4v1beta1.DataStoreList
		if err := mgr.GetClient().List(ctx, &stores, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(stores.Items))
		for _, s := range stores.Items {
			reqs = append(reqs, requestFor(s.Namespace, s.Name))
		}
		return reqs
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1beta1.DataStore{}).
		Watches(&c4v1beta1.SoftwareSystem{}, allInNamespace, specChanged()).
		Watches(&c4v1beta1.Connector{}, allInNamespace, createOrDelete()).
		Named("datastore").
		Complete(r)
}
