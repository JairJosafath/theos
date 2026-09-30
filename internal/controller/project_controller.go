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

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// ConditionNameMatchesNamespace is False when a Project isn't named after its
// namespace. The admission policy normally stops that; this is the fallback.
const ConditionNameMatchesNamespace = "NameMatchesNamespace"

// ProjectReconciler adopts the kinds a Project owns directly (Person,
// SoftwareSystem, GitProvider and the catalog entries) and rolls up status.
type ProjectReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=c4.factory.io,resources=projects,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=c4.factory.io,resources=projects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=c4.factory.io,resources=projects/finalizers,verbs=update
// +kubebuilder:rbac:groups=c4.factory.io,resources=people;gitproviders;frameworks;computes;deployments;connectors,verbs=get;list;watch;update;patch

// Reconcile adopts the Project's direct children and updates the roll-up.
func (r *ProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var project c4v1alpha1.Project
	if err := r.Get(ctx, req.NamespacedName, &project); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	status := project.Status.DeepCopy()

	if project.Name != project.Namespace {
		setCondition(&status.Conditions, project.Generation, ConditionNameMatchesNamespace, metav1.ConditionFalse,
			"NameMismatch", fmt.Sprintf("a Project must be named after its namespace, %q", project.Namespace))
		log.Info("Skipped Project not named after its namespace", "namespace", project.Namespace)
		return ctrl.Result{}, r.writeStatus(ctx, &project, status)
	}
	setCondition(&status.Conditions, project.Generation, ConditionNameMatchesNamespace, metav1.ConditionTrue,
		"NameMatches", "")

	if err := r.adoptChildren(ctx, &project); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.rollUp(ctx, &project, status); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, r.writeStatus(ctx, &project, status)
}

// adoptChildren sets the Project as owner of every kind it owns directly. These
// kinds have no parent field: the Project is found by namespace.
func (r *ProjectReconciler) adoptChildren(ctx context.Context, project *c4v1alpha1.Project) error {
	lists := []client.ObjectList{
		&c4v1alpha1.PersonList{},
		&c4v1alpha1.SoftwareSystemList{},
		&c4v1alpha1.GitProviderList{},
		&c4v1alpha1.FrameworkList{},
		&c4v1alpha1.ComputeList{},
		&c4v1alpha1.DeploymentList{},
		&c4v1alpha1.ConnectorList{},
	}
	labels := projectLabels(project.Namespace)
	for _, list := range lists {
		if err := r.List(ctx, list, client.InNamespace(project.Namespace)); err != nil {
			return err
		}
		items, err := metaItems(list)
		if err != nil {
			return err
		}
		for _, obj := range items {
			if err := adopt(ctx, r.Client, r.Scheme, project, obj, labels); err != nil {
				return fmt.Errorf("adopt %T %s: %w", obj, obj.GetName(), err)
			}
		}
	}
	return nil
}

func (r *ProjectReconciler) rollUp(ctx context.Context, project *c4v1alpha1.Project, status *c4v1alpha1.ProjectStatus) error {
	var systems c4v1alpha1.SoftwareSystemList
	if err := r.List(ctx, &systems, client.InNamespace(project.Namespace)); err != nil {
		return err
	}
	var containers c4v1alpha1.ContainerList
	if err := r.List(ctx, &containers, client.InNamespace(project.Namespace)); err != nil {
		return err
	}
	var sessions c4v1alpha1.SessionList
	if err := r.List(ctx, &sessions, client.InNamespace(project.Namespace)); err != nil {
		return err
	}
	ready := 0
	for _, c := range containers.Items {
		if c.Status.Phase == c4v1alpha1.ContainerReady {
			ready++
		}
	}
	active := countActive(sessions.Items)
	status.SoftwareSystems = int32(len(systems.Items))
	status.Containers = int32(len(containers.Items))
	status.ReadyContainers = int32(ready)
	status.ActiveSessions = int32(active)
	status.Summary = model.Summary(ready, len(containers.Items), active)
	return nil
}

func (r *ProjectReconciler) writeStatus(ctx context.Context, project *c4v1alpha1.Project, status *c4v1alpha1.ProjectStatus) error {
	if equality.Semantic.DeepEqual(&project.Status, status) {
		return nil
	}
	orig := project.DeepCopy()
	project.Status = *status
	return r.Status().Patch(ctx, project, client.MergeFrom(orig))
}

// countActive counts Sessions in phase Active.
func countActive(sessions []c4v1alpha1.Session) int {
	n := 0
	for _, s := range sessions {
		if s.Status.Phase == c4v1alpha1.SessionActive {
			n++
		}
	}
	return n
}

// metaItems returns the items of a typed list as client.Objects.
func metaItems(list client.ObjectList) ([]client.Object, error) {
	objs, err := meta.ExtractList(list)
	if err != nil {
		return nil, err
	}
	out := make([]client.Object, 0, len(objs))
	for _, o := range objs {
		obj, ok := o.(client.Object)
		if !ok {
			return nil, fmt.Errorf("unexpected item type %T", o)
		}
		out = append(out, obj)
	}
	return out, nil
}

// SetupWithManager sets up the controller with the Manager. Anything in the
// namespace can change the adoption or the roll-up, so it all maps to the Project
// named after the namespace.
func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	toProject := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
		return []reconcile.Request{requestFor(obj.GetNamespace(), obj.GetNamespace())}
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1alpha1.Project{}).
		Watches(&c4v1alpha1.Person{}, toProject).
		Watches(&c4v1alpha1.SoftwareSystem{}, toProject).
		Watches(&c4v1alpha1.GitProvider{}, toProject).
		Watches(&c4v1alpha1.Framework{}, toProject).
		Watches(&c4v1alpha1.Compute{}, toProject).
		Watches(&c4v1alpha1.Deployment{}, toProject).
		Watches(&c4v1alpha1.Connector{}, toProject).
		Watches(&c4v1alpha1.Container{}, toProject).
		Watches(&c4v1alpha1.Session{}, toProject, sessionPhaseChanges()).
		Named("project").
		Complete(r)
}
