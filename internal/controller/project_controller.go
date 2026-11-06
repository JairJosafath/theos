/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// ConditionNameMatchesNamespace is False when a Project isn't named after its
// namespace. The admission policy normally stops that; this is the fallback.
const ConditionNameMatchesNamespace = "NameMatchesNamespace"

// ProjectReconciler adopts the kinds a Project owns directly (Person,
// SoftwareSystem, GitProvider, the catalog entries and Agents), gives the namespace the
// Session client's ServiceAccount, shares the operator's Secrets into it when it
// opts in, and rolls up status.
type ProjectReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader reads Secrets and Namespaces without caching them.
	APIReader client.Reader
	// SharedPullSecrets name the pull Secrets in SourceNamespace, the
	// operator's own, that it copies into every opted-in project namespace.
	// SharedSecrets name the token Secrets there, which the model proxy reads
	// and the operator never copies: the copies an earlier release made are
	// deleted (#1137). With neither, it shares nothing.
	SharedSecrets     []string
	SharedPullSecrets []string
	SourceNamespace   string
	// SharedSecretsRecheck is how often an opted-in Project looks at its
	// sources again; DefaultSharedSecretsRecheck when zero.
	SharedSecretsRecheck time.Duration
	// ModelProxyURL, when set, is the model proxy's URL in SourceNamespace: the
	// agent fence then lets sandboxes reach the proxy's pods.
	ModelProxyURL string
	// OperatorVersion and ChartVersion are written to status.factory:
	// buildinfo.Version and the chart's THEOS_CHART_VERSION.
	OperatorVersion string
	ChartVersion    string
	// UIServiceAccount is the UI's ServiceAccount, let read the pods of every
	// project namespace (ensureUIPodReader); none when its name is empty.
	UIServiceAccount types.NamespacedName
	// Git makes the Provider of each GitProvider, for the CI runner pool's
	// registration token.
	Git GitClient
	// CIGoCache is the node directory under which each project's CI Go cache
	// lives (--ci-go-cache); empty, the pool pods mount no cache.
	CIGoCache string
	// ClusterDNS is where cluster DNS runs, the peers the agent and CI fences
	// let port 53 out to (--cluster-dns, ClusterDNSPeers); nil means
	// DefaultClusterDNS, kube-dns in kube-system (#1257).
	ClusterDNS []networkingv1.NetworkPolicyPeer
	// RunnerRecordKept is how long an ended pool pod stays readable;
	// DefaultRunnerRecordKept when zero.
	RunnerRecordKept time.Duration
	// RunnerBackoff is how long a CI runner pool waits after a pod failed on
	// its own, doubled per failure in a row; DefaultRunnerBackoff when zero.
	RunnerBackoff time.Duration
	// packagesApplied is, per namespace/GitProvider, the packagesActions last
	// sent to the owner's host (ensurePackagesActions).
	packagesApplied sync.Map
}

// +kubebuilder:rbac:groups=theos.factory.io,resources=projects,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=theos.factory.io,resources=projects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=theos.factory.io,resources=projects/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;update;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;create;update;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=theos.factory.io,resources=people;gitproviders;frameworks;standards;computes;deployments;connectors;domains;bundles;agents,verbs=get;list;watch;update;patch

// Reconcile adopts the Project's direct children and updates the roll-up.
func (r *ProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var project c4v1beta1.Project
	err := r.Get(ctx, req.NamespacedName, &project)
	if apierrors.IsNotFound(err) && req.Name == req.Namespace {
		// The pool goes with the Project: its pods and their kept records,
		// which garbage collection cannot let go (#1697).
		if err := r.dropPool(ctx, req.Namespace); err != nil {
			return ctrl.Result{}, err
		}
		// A Brief gets its Project from its first round, whose planner needs
		// the shared pull Secret, so an opted-in namespace is shared into before.
		recheck, err := r.shareBeforeProject(ctx, req.Namespace)
		return ctrl.Result{RequeueAfter: recheck}, err
	}
	if err != nil {
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
	if err := r.ensureUIPodReader(ctx, &project); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.ensureAgentNetwork(ctx, &project); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.ensureTestEnvironmentNetwork(ctx, &project); err != nil {
		return ctrl.Result{}, err
	}
	ci, err := r.ensureCIRunners(ctx, &project, status)
	if err != nil {
		return ctrl.Result{}, err
	}
	if ci.retry != nil {
		// What follows may fail on the same host: say it first.
		if err := r.writeStatus(ctx, &project, status); err != nil {
			return ctrl.Result{}, err
		}
	}
	if err := r.ensurePackagesActions(ctx, &project, ci.providers); err != nil {
		return ctrl.Result{}, err
	}
	recheck, err := r.shareSecrets(ctx, &project, status)
	if err != nil {
		return ctrl.Result{}, err
	}
	if ci.recheck > 0 && (recheck == 0 || ci.recheck < recheck) {
		recheck = ci.recheck
	}
	if err := r.rollUp(ctx, &project, status); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.writeStatus(ctx, &project, status); err != nil {
		return ctrl.Result{}, err
	}
	if ci.retry != nil {
		// The queue's backoff retries the host's token.
		return ctrl.Result{}, ci.retry
	}
	return ctrl.Result{RequeueAfter: recheck}, nil
}

// adoptChildren sets the Project as owner of every kind it owns directly. These
// kinds have no parent field: the Project is found by namespace.
func (r *ProjectReconciler) adoptChildren(ctx context.Context, project *c4v1beta1.Project) error {
	lists := []client.ObjectList{
		&c4v1beta1.PersonList{},
		&c4v1beta1.SoftwareSystemList{},
		&c4v1beta1.GitProviderList{},
		&c4v1beta1.FrameworkList{},
		&c4v1beta1.StandardList{},
		&c4v1beta1.ComputeList{},
		&c4v1beta1.DeploymentList{},
		&c4v1beta1.ConnectorList{},
		&c4v1beta1.DomainList{},
		&c4v1beta1.BundleList{},
		&c4v1beta1.AgentList{},
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

func (r *ProjectReconciler) rollUp(ctx context.Context, project *c4v1beta1.Project, status *c4v1beta1.ProjectStatus) error {
	var systems c4v1beta1.SoftwareSystemList
	if err := r.List(ctx, &systems, client.InNamespace(project.Namespace)); err != nil {
		return err
	}
	var containers c4v1beta1.ContainerList
	if err := r.List(ctx, &containers, client.InNamespace(project.Namespace)); err != nil {
		return err
	}
	var sessions c4v1beta1.SessionList
	if err := r.List(ctx, &sessions, client.InNamespace(project.Namespace)); err != nil {
		return err
	}
	ready := 0
	for _, c := range containers.Items {
		if c.Status.Phase == c4v1beta1.ContainerReady {
			ready++
		}
	}
	active := countActive(sessions.Items)
	status.SoftwareSystems = int32(len(systems.Items))
	status.Containers = int32(len(containers.Items))
	status.ReadyContainers = int32(ready)
	status.ActiveSessions = int32(active)
	status.Summary = model.Summary(ready, len(containers.Items), active)
	status.Factory = &c4v1beta1.FactoryVersions{OperatorVersion: r.OperatorVersion, ChartVersion: r.ChartVersion}
	return nil
}

func (r *ProjectReconciler) writeStatus(ctx context.Context, project *c4v1beta1.Project, status *c4v1beta1.ProjectStatus) error {
	if equality.Semantic.DeepEqual(&project.Status, status) {
		return nil
	}
	orig := project.DeepCopy()
	project.Status = *status.DeepCopy()
	return r.Status().Patch(ctx, project, client.MergeFrom(orig))
}

// countActive counts Sessions in phase Active.
func countActive(sessions []c4v1beta1.Session) int {
	n := 0
	for _, s := range sessions {
		if s.Status.Phase == c4v1beta1.SessionActive {
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
// creations and deletions, and a Container or Session changing phase. A
// Namespace maps to the Project named after it when its shared-secrets label
// changes, or when it is made with it: the reconcile shares into an opted-in
// namespace that has no Project yet.
func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	toProject := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
		return []reconcile.Request{requestFor(obj.GetNamespace(), obj.GetNamespace())}
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1beta1.Project{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&corev1.Pod{}).
		Watches(&c4v1beta1.Person{}, toProject, createOrDelete()).
		Watches(&c4v1beta1.SoftwareSystem{}, toProject, createOrDelete()).
		Watches(&c4v1beta1.GitProvider{}, toProject, specChanged()).
		Watches(&c4v1beta1.Framework{}, toProject, createOrDelete()).
		Watches(&c4v1beta1.Compute{}, toProject, createOrDelete()).
		Watches(&c4v1beta1.Deployment{}, toProject, createOrDelete()).
		Watches(&c4v1beta1.Connector{}, toProject, createOrDelete()).
		Watches(&c4v1beta1.Domain{}, toProject, createOrDelete()).
		Watches(&c4v1beta1.Bundle{}, toProject, createOrDelete()).
		Watches(&c4v1beta1.Standard{}, toProject, createOrDelete()).
		Watches(&c4v1beta1.Agent{}, toProject, createOrDelete()).
		Watches(&c4v1beta1.Container{}, toProject, containerPhaseChanges()).
		Watches(&c4v1beta1.Session{}, toProject, sessionPhaseChanges()).
		WatchesMetadata(&corev1.Namespace{}, handler.EnqueueRequestsFromMapFunc(
			func(_ context.Context, obj client.Object) []reconcile.Request {
				return []reconcile.Request{requestFor(obj.GetName(), obj.GetName())}
			}), labelChanged(c4v1beta1.LabelSharedSecrets)).
		Named("project").
		Complete(r)
}
