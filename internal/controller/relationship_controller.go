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
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// RelationshipReconciler makes a Relationship a child of its source element,
// checks both ends exist, and reports whether a Container to DataStore link was
// applied at scaffold time.
type RelationshipReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=c4.factory.io,resources=relationships,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=c4.factory.io,resources=relationships/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=c4.factory.io,resources=relationships/finalizers,verbs=update

// Reconcile adopts the Relationship and updates its conditions.
func (r *RelationshipReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var rel c4v1alpha1.Relationship
	if err := r.Get(ctx, req.NamespacedName, &rel); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	source, err := r.getElement(ctx, rel.Namespace, rel.Spec.Source)
	if err != nil {
		return ctrl.Result{}, err
	}
	target, err := r.getElement(ctx, rel.Namespace, rel.Spec.Target)
	if err != nil {
		return ctrl.Result{}, err
	}

	var missing []string
	if source == nil {
		missing = append(missing, fmt.Sprintf("%s %s", rel.Spec.Source.Kind, rel.Spec.Source.Name))
	} else if err := adopt(ctx, r.Client, r.Scheme, source, &rel, projectLabels(rel.Namespace)); err != nil {
		return ctrl.Result{}, err
	}
	if target == nil {
		missing = append(missing, fmt.Sprintf("%s %s", rel.Spec.Target.Kind, rel.Spec.Target.Name))
	}

	status := rel.Status.DeepCopy()
	if len(missing) > 0 {
		setCondition(&status.Conditions, rel.Generation, c4v1alpha1.ConditionReferencesResolved,
			metav1.ConditionFalse, c4v1alpha1.ReasonNotFound, notFoundMessage(missing))
	} else {
		setCondition(&status.Conditions, rel.Generation, c4v1alpha1.ConditionReferencesResolved,
			metav1.ConditionTrue, c4v1alpha1.ReasonResolved, "")
	}

	if toModel(rel).ChangesScaffold() {
		container, _ := source.(*c4v1alpha1.Container)
		setApplied(status, &rel, container)
	} else {
		meta.RemoveStatusCondition(&status.Conditions, c4v1alpha1.ConditionApplied)
	}

	if equality.Semantic.DeepEqual(&rel.Status, status) {
		return ctrl.Result{}, nil
	}
	orig := rel.DeepCopy()
	rel.Status = *status
	return ctrl.Result{}, r.Status().Patch(ctx, &rel, client.MergeFrom(orig))
}

// setApplied reports whether the source Container's scaffold included this link.
// A link added after scaffolding is not applied, by design, and says so.
func setApplied(status *c4v1alpha1.RelationshipStatus, rel *c4v1alpha1.Relationship, container *c4v1alpha1.Container) {
	switch {
	case container == nil || container.Status.Phase != c4v1alpha1.ContainerReady:
		setCondition(&status.Conditions, rel.Generation, c4v1alpha1.ConditionApplied, metav1.ConditionUnknown,
			c4v1alpha1.ReasonWaiting, "the Connector is applied when the Container is scaffolded")
	case slices.Contains(container.Status.AppliedRelationships, rel.Name):
		setCondition(&status.Conditions, rel.Generation, c4v1alpha1.ConditionApplied, metav1.ConditionTrue,
			c4v1alpha1.ReasonAppliedAtScaffold, "the Connector was part of the scaffold")
	default:
		setCondition(&status.Conditions, rel.Generation, c4v1alpha1.ConditionApplied, metav1.ConditionFalse,
			c4v1alpha1.ReasonAddedAfterScaffold,
			fmt.Sprintf("%s was scaffolded before this Relationship existed; scaffolding happens once",
				container.Name))
	}
}

// getElement returns the story element a ref points to, or nil if it doesn't exist.
func (r *RelationshipReconciler) getElement(ctx context.Context, namespace string, ref c4v1alpha1.ElementRef) (client.Object, error) {
	var obj client.Object
	switch ref.Kind {
	case c4v1alpha1.ElementPerson:
		obj = &c4v1alpha1.Person{}
	case c4v1alpha1.ElementSoftwareSystem:
		obj = &c4v1alpha1.SoftwareSystem{}
	case c4v1alpha1.ElementContainer:
		obj = &c4v1alpha1.Container{}
	case c4v1alpha1.ElementDataStore:
		obj = &c4v1alpha1.DataStore{}
	default:
		return nil, fmt.Errorf("unknown element kind %q", ref.Kind)
	}
	err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, obj)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return obj, nil
}

// toModel converts a Relationship into the plain value the business rules use.
func toModel(rel c4v1alpha1.Relationship) model.Relationship {
	return model.Relationship{
		Name:   rel.Name,
		Source: model.Ref{Kind: string(rel.Spec.Source.Kind), Name: rel.Spec.Source.Name},
		Target: model.Ref{Kind: string(rel.Spec.Target.Kind), Name: rel.Spec.Target.Name},
	}
}

// SetupWithManager sets up the controller with the Manager. Any element appearing,
// disappearing or (for Containers) finishing its scaffold affects the links to it.
func (r *RelationshipReconciler) SetupWithManager(mgr ctrl.Manager) error {
	toRelationships := func(kind c4v1alpha1.ElementKind) handler.EventHandler {
		return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
			var rels c4v1alpha1.RelationshipList
			if err := mgr.GetClient().List(ctx, &rels, client.InNamespace(obj.GetNamespace()),
				client.MatchingFields{indexElement: elementKey(kind, obj.GetName())}); err != nil {
				return nil
			}
			reqs := make([]reconcile.Request, 0, len(rels.Items))
			for _, rel := range rels.Items {
				reqs = append(reqs, requestFor(rel.Namespace, rel.Name))
			}
			return reqs
		})
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1alpha1.Relationship{}).
		Watches(&c4v1alpha1.Person{}, toRelationships(c4v1alpha1.ElementPerson), createOrDelete()).
		Watches(&c4v1alpha1.SoftwareSystem{}, toRelationships(c4v1alpha1.ElementSoftwareSystem), createOrDelete()).
		Watches(&c4v1alpha1.DataStore{}, toRelationships(c4v1alpha1.ElementDataStore), createOrDelete()).
		Watches(&c4v1alpha1.Container{}, toRelationships(c4v1alpha1.ElementContainer)).
		Named("relationship").
		Complete(r)
}
