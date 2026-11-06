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
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// TestEnvironmentReconciler reports whether each TestEnvironment can be
// leased, lists its live leases, and returns the leases whose holder holds
// them no more (docs/plans/test-environments.md, step 1). The Task
// reconciler takes and returns the leases of its runs.
type TestEnvironmentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Providers are the providers by member, as cmd/main.go builds them.
	Providers ports.TestEnvironmentProviders
	// SharedPullSecrets are the shared pull Secrets lease pods list, as
	// agent sandboxes do.
	SharedPullSecrets []string
	// PollInterval is how often the leases are listed; 0 means a minute.
	PollInterval time.Duration
	// OrphanGrace is how old a lease its holder doesn't list must be before
	// it is returned; 0 means a minute.
	OrphanGrace time.Duration
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

// +kubebuilder:rbac:groups=theos.factory.io,resources=testenvironments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=theos.factory.io,resources=testenvironments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=theos.factory.io,resources=testenvironments/finalizers,verbs=update
// +kubebuilder:rbac:groups=theos.factory.io,resources=tasks,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=create;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch

// Reconcile sets Ready, returns the orphaned leases and records the live
// ones and what they cost today.
func (r *TestEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var env c4v1beta1.TestEnvironment
	if err := r.Get(ctx, req.NamespacedName, &env); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	status := env.Status.DeepCopy()
	reason, message, err := r.readiness(ctx, &env)
	if err != nil {
		return ctrl.Result{}, err
	}
	condition := metav1.ConditionFalse
	if reason == model.TestEnvReady {
		condition = metav1.ConditionTrue
	}
	setCondition(&status.Conditions, env.Generation, c4v1beta1.ConditionTestEnvironmentReady, condition, reason, message)

	environment, member := testEnvironment(&env, r.SharedPullSecrets), testEnvMember(&env)
	provider, err := r.Providers.For(member)
	if err != nil {
		logf.FromContext(ctx).Error(err, "Failed to find the test environment's provider", "member", member)
		return ctrl.Result{}, r.writeStatus(ctx, &env, status)
	}
	leases, err := provider.Leases(ctx, environment)
	if err != nil {
		return ctrl.Result{}, err
	}
	live := make([]ports.Lease, 0, len(leases))
	for _, l := range leases {
		held, err := r.held(ctx, env.Namespace, l)
		if err != nil {
			return ctrl.Result{}, err
		}
		if model.OrphanLease(held, l.Started, r.now(), r.orphanGrace()) {
			if err := provider.Return(ctx, environment, l.ID); err != nil {
				return ctrl.Result{}, err
			}
			logf.FromContext(ctx).Info("Returned a lease its holder holds no more", "lease", l.ID, "holder", l.Holder)
			continue
		}
		live = append(live, l)
	}
	status.SpentToday = spentToday(status.SpentToday, status.Leases, live, r.now())
	status.Leases = leaseStatus(live)
	return ctrl.Result{RequeueAfter: r.pollInterval()}, r.writeStatus(ctx, &env, status)
}

// readiness says whether runs can lease env: its image is pinned and the
// project's fence for its pods is there.
func (r *TestEnvironmentReconciler) readiness(ctx context.Context, env *c4v1beta1.TestEnvironment) (string, string, error) {
	if local := env.Spec.Local; local != nil && !model.ImagePinned(local.Image) {
		return model.TestEnvImageNotPinned, fmt.Sprintf("image %q names no tag or digest, or latest", local.Image), nil
	}
	var policy networkingv1.NetworkPolicy
	err := r.Get(ctx, client.ObjectKey{Namespace: env.Namespace, Name: model.TestEnvNetworkPolicy}, &policy)
	if apierrors.IsNotFound(err) {
		return model.TestEnvFenceMissing, fmt.Sprintf("NetworkPolicy %s, which fences the environment's pods, "+
			"is missing: the operator makes it in a namespace that has a Project", model.TestEnvNetworkPolicy), nil
	}
	if err != nil {
		return "", "", err
	}
	return model.TestEnvReady, "", nil
}

// held reports whether lease l's holder, a Task, still holds it.
func (r *TestEnvironmentReconciler) held(ctx context.Context, namespace string, l ports.Lease) (bool, error) {
	if l.Holder == "" {
		return false, nil
	}
	var task c4v1beta1.Task
	err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: l.Holder}, &task)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// spentToday is what the environment's leases cost today: what the leases
// that ended today cost, and what the live ones cost so far. A lease gone
// since before is counted at its last cost.
func spentToday(spent *c4v1beta1.TestEnvironmentSpend, before []c4v1beta1.TestEnvironmentLease, live []ports.Lease,
	now time.Time) *c4v1beta1.TestEnvironmentSpend {
	day := now.UTC().Format(time.DateOnly)
	var ended, liveCost int64
	if spent != nil && spent.Day == day {
		ended = spent.MilliUSD
		for _, l := range before {
			ended -= l.CostMilliUSD
		}
	}
	for _, l := range before {
		if !slices.ContainsFunc(live, func(n ports.Lease) bool { return n.ID == l.ID }) {
			ended += l.CostMilliUSD
		}
	}
	for _, l := range live {
		liveCost += l.CostMilliUSD
	}
	return &c4v1beta1.TestEnvironmentSpend{Day: day, MilliUSD: max(ended, 0) + liveCost}
}

// leaseStatus is the live leases as status keeps them.
func leaseStatus(live []ports.Lease) []c4v1beta1.TestEnvironmentLease {
	if len(live) == 0 {
		return nil
	}
	out := make([]c4v1beta1.TestEnvironmentLease, 0, len(live))
	for _, l := range live {
		lease := c4v1beta1.TestEnvironmentLease{ID: l.ID, Holder: l.Holder, State: l.State, Message: l.Message,
			CostMilliUSD: l.CostMilliUSD}
		if !l.Started.IsZero() {
			lease.Since = &metav1.Time{Time: l.Started}
		}
		out = append(out, lease)
	}
	return out
}

// testEnvMember is the name of env's provider member.
func testEnvMember(env *c4v1beta1.TestEnvironment) string {
	if env.Spec.Local != nil {
		return model.TestEnvMemberLocal
	}
	return ""
}

// testEnvironment is env as its provider reads it, the pull Secrets its
// pods list among its settings.
func testEnvironment(env *c4v1beta1.TestEnvironment, pullSecrets []string) ports.Environment {
	out := ports.Environment{Namespace: env.Namespace, Name: env.Name, UID: string(env.UID), Member: testEnvMember(env)}
	local := env.Spec.Local
	if local == nil {
		return out
	}
	quantities := func(list corev1.ResourceList) map[string]string {
		if len(list) == 0 {
			return nil
		}
		m := make(map[string]string, len(list))
		for name, q := range list {
			m[string(name)] = q.String()
		}
		return m
	}
	tolerations := make([]ports.Toleration, 0, len(local.Tolerations))
	for _, t := range local.Tolerations {
		tolerations = append(tolerations, ports.Toleration{Key: t.Key, Operator: string(t.Operator), Value: t.Value,
			Effect: string(t.Effect), Seconds: t.TolerationSeconds})
	}
	out.Local = &ports.LocalEnvironment{Image: local.Image,
		Requests: quantities(local.Resources.Requests), Limits: quantities(local.Resources.Limits),
		Devices: quantities(local.Devices), RuntimeClassName: local.RuntimeClassName,
		NodeSelector: local.NodeSelector, Tolerations: tolerations, PullSecrets: pullSecrets}
	return out
}

func (r *TestEnvironmentReconciler) writeStatus(ctx context.Context, env *c4v1beta1.TestEnvironment,
	status *c4v1beta1.TestEnvironmentStatus) error {
	if equality.Semantic.DeepEqual(&env.Status, status) {
		return nil
	}
	orig := env.DeepCopy()
	env.Status = *status.DeepCopy()
	return r.Status().Patch(ctx, env, client.MergeFrom(orig))
}

func (r *TestEnvironmentReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *TestEnvironmentReconciler) pollInterval() time.Duration {
	if r.PollInterval > 0 {
		return r.PollInterval
	}
	return time.Minute
}

func (r *TestEnvironmentReconciler) orphanGrace() time.Duration {
	if r.OrphanGrace > 0 {
		return r.OrphanGrace
	}
	return time.Minute
}

// SetupWithManager sets up the controller with the Manager. A lease pod is
// owned by its environment; the project's fence, and a Task that goes,
// wake every environment of the namespace.
func (r *TestEnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	toEnvironments := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var envs c4v1beta1.TestEnvironmentList
		if err := mgr.GetClient().List(ctx, &envs, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(envs.Items))
		for _, e := range envs.Items {
			reqs = append(reqs, requestFor(e.Namespace, e.Name))
		}
		return reqs
	})
	fence := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetName() == model.TestEnvNetworkPolicy
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1beta1.TestEnvironment{}).
		Owns(&corev1.Pod{}).
		Watches(&networkingv1.NetworkPolicy{}, toEnvironments, builder.WithPredicates(fence)).
		Watches(&c4v1beta1.Task{}, toEnvironments, createOrDelete()).
		Named("testenvironment").
		Complete(r)
}
