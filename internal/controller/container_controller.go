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
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// ContainerReconciler makes a Container a child of its SoftwareSystem, checks its
// catalog choices, scaffolds its repository once and counts its active Sessions.
type ContainerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader reads Secrets without caching them.
	APIReader client.Reader
	// Git reaches the Git hosts.
	Git GitClient
	// ScaffoldOptions configure the parts of a scaffold the operator owns.
	ScaffoldOptions scaffold.Options
}

// +kubebuilder:rbac:groups=c4.factory.io,resources=containers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=c4.factory.io,resources=containers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=c4.factory.io,resources=containers/finalizers,verbs=update

// +kubebuilder:rbac:groups=c4.factory.io,resources=gitproviders;frameworks;computes;deployments;connectors,verbs=get;list;watch
// +kubebuilder:rbac:groups=c4.factory.io,resources=relationships;datastores;sessions,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

// secretRecheck is how often a Container waiting for its credentials Secret looks again.
const secretRecheck = 15 * time.Second

// Reconcile adopts the Container, resolves its references, checks its catalog
// choices and scaffolds its repository once.
func (r *ContainerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var container c4v1alpha1.Container
	if err := r.Get(ctx, req.NamespacedName, &container); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !container.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
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

	// Scaffolding happens once. After that the factory only reports.
	if status.Phase == c4v1alpha1.ContainerReady {
		return ctrl.Result{}, r.writeStatus(ctx, &container, status)
	}

	res, missing, err := r.resolve(ctx, &container)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(missing) > 0 {
		status.Phase = c4v1alpha1.ContainerPending
		setCondition(&status.Conditions, container.Generation, c4v1alpha1.ConditionReferencesResolved,
			metav1.ConditionFalse, c4v1alpha1.ReasonNotFound, notFoundMessage(missing))
		// Secrets aren't watched (they're read uncached), so look again for one.
		var result ctrl.Result
		if slices.ContainsFunc(missing, func(m string) bool { return strings.HasPrefix(m, "Secret ") }) {
			result.RequeueAfter = secretRecheck
		}
		return result, r.writeStatus(ctx, &container, status)
	}
	setCondition(&status.Conditions, container.Generation, c4v1alpha1.ConditionReferencesResolved,
		metav1.ConditionTrue, c4v1alpha1.ReasonResolved, "")

	if err := model.CheckChoices(string(container.Spec.Type), res.entries); err != nil {
		status.Phase = c4v1alpha1.ContainerFailed
		setCondition(&status.Conditions, container.Generation, c4v1alpha1.ConditionChoicesValid,
			metav1.ConditionFalse, c4v1alpha1.ReasonUnsupportedType, err.Error())
		log.Info("Rejected catalog choices", "reason", err.Error())
		return ctrl.Result{}, r.writeStatus(ctx, &container, status)
	}
	setCondition(&status.Conditions, container.Generation, c4v1alpha1.ConditionChoicesValid,
		metav1.ConditionTrue, c4v1alpha1.ReasonValid, "")

	return r.scaffold(ctx, &container, res, status)
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
	// A catalog entry, GitProvider or DataStore appearing can unblock any Container
	// in the namespace that isn't scaffolded yet.
	waitingContainers := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var containers c4v1alpha1.ContainerList
		if err := mgr.GetClient().List(ctx, &containers, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		var reqs []reconcile.Request
		for _, c := range containers.Items {
			if c.Status.Phase != c4v1alpha1.ContainerReady {
				reqs = append(reqs, requestFor(c.Namespace, c.Name))
			}
		}
		return reqs
	})
	sourceContainer := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
		rel, ok := obj.(*c4v1alpha1.Relationship)
		if !ok || rel.Spec.Source.Kind != c4v1alpha1.ElementContainer {
			return nil
		}
		return []reconcile.Request{requestFor(rel.Namespace, rel.Spec.Source.Name)}
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1alpha1.Container{}).
		Watches(&c4v1alpha1.Session{}, toContainer, sessionPhaseChanges()).
		Watches(&c4v1alpha1.SoftwareSystem{}, systemContainers, createOrDelete()).
		Watches(&c4v1alpha1.Relationship{}, sourceContainer).
		Watches(&c4v1alpha1.GitProvider{}, waitingContainers).
		Watches(&c4v1alpha1.Framework{}, waitingContainers).
		Watches(&c4v1alpha1.Compute{}, waitingContainers).
		Watches(&c4v1alpha1.Deployment{}, waitingContainers).
		Watches(&c4v1alpha1.Connector{}, waitingContainers).
		Watches(&c4v1alpha1.DataStore{}, waitingContainers).
		Named("container").
		Complete(r)
}
