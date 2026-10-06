/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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
// SoftwareSystem, GitProvider, the catalog entries and Agents), gives the namespace the
// Session client's ServiceAccount, and rolls up status.
type ProjectReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=c4.factory.io,resources=projects,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=c4.factory.io,resources=projects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=c4.factory.io,resources=projects/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=c4.factory.io,resources=people;gitproviders;frameworks;standards;computes;deployments;connectors;agents,verbs=get;list;watch;update;patch

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
	if err := r.ensureSessionAccess(ctx, &project); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.ensureAgentNetwork(ctx, &project); err != nil {
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
		&c4v1alpha1.StandardList{},
		&c4v1alpha1.ComputeList{},
		&c4v1alpha1.DeploymentList{},
		&c4v1alpha1.ConnectorList{},
		&c4v1alpha1.AgentList{},
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
// named after the namespace. Only what adoption and the roll-up read passes:
// creations and deletions, and a Container or Session changing phase.
func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	toProject := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
		return []reconcile.Request{requestFor(obj.GetNamespace(), obj.GetNamespace())}
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1alpha1.Project{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Watches(&c4v1alpha1.Person{}, toProject, createOrDelete()).
		Watches(&c4v1alpha1.SoftwareSystem{}, toProject, createOrDelete()).
		Watches(&c4v1alpha1.GitProvider{}, toProject, specChanged()).
		Watches(&c4v1alpha1.Framework{}, toProject, createOrDelete()).
		Watches(&c4v1alpha1.Compute{}, toProject, createOrDelete()).
		Watches(&c4v1alpha1.Deployment{}, toProject, createOrDelete()).
		Watches(&c4v1alpha1.Connector{}, toProject, createOrDelete()).
		Watches(&c4v1alpha1.Standard{}, toProject, createOrDelete()).
		Watches(&c4v1alpha1.Agent{}, toProject, createOrDelete()).
		Watches(&c4v1alpha1.Container{}, toProject, containerPhaseChanges()).
		Watches(&c4v1alpha1.Session{}, toProject, sessionPhaseChanges()).
		Named("project").
		Complete(r)
}
