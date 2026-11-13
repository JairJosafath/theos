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
	"errors"
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
// them no more (docs/plans/done/test-environments.md, step 1). A remote member's
// credential is checked on each poll (CredentialsValid), and its leases are
// returned before the environment goes (step 7). A test-run environment's
// leases take their runs' requests on the Git host (step 10,
// testenvironment_testruns.go). The Task reconciler takes and returns the
// leases of its runs.
type TestEnvironmentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader reads a remote member's Secret live: the operator caches no
	// Secrets.
	APIReader client.Reader
	// Providers are the providers by member, as cmd/main.go builds them.
	Providers ports.TestEnvironmentProviders
	// Git makes the Provider of each GitProvider, whose CI jobs a remote
	// environment with a ci block runs (testenvironment_ci.go), and where a
	// test-run environment's leases take their requests (step 10,
	// testenvironment_testruns.go); nil leaves both undone.
	Git GitClient
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
// +kubebuilder:rbac:groups=theos.factory.io,resources=gitproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch

// testEnvLeasesFinalizer keeps a remote TestEnvironment until its leases are
// returned and its provider released what it keeps for it: its machines
// would outlive it, costing money, until their deadline.
const testEnvLeasesFinalizer = "theos.factory.io/test-environment-leases"

// testEnvReleaseTimeout is how long a deleted remote environment waits for
// its provider to return its leases and release it; after it, what is left
// ends at its deadline, and the environment goes.
const testEnvReleaseTimeout = 15 * time.Minute

// testEnvReleaseWithin is how long a deleted env waits to be released:
// testEnvReleaseTimeout, and for an EC2 Mac a host's 24 hours more, since
// EC2 releases no Mac host within them and a host has no deadline of its
// own to end at.
func testEnvReleaseWithin(env *c4v1beta1.TestEnvironment) time.Duration {
	if a := env.Spec.AWS; a != nil && model.TestEnvMac(a.InstanceType) {
		return model.MacHostMinimum + testEnvReleaseTimeout
	}
	return testEnvReleaseTimeout
}

// Reconcile sets Ready, and CredentialsValid for a remote member, returns
// the orphaned leases and records the live ones and what they cost today.
func (r *TestEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var env c4v1beta1.TestEnvironment
	if err := r.Get(ctx, req.NamespacedName, &env); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	member := testEnvMember(&env)
	provider, providerErr := r.Providers.For(member)
	if !env.DeletionTimestamp.IsZero() {
		return r.release(ctx, &env, provider)
	}
	if model.TestEnvRemote(member) && !controllerutil.ContainsFinalizer(&env, testEnvLeasesFinalizer) {
		held := env.DeepCopy()
		controllerutil.AddFinalizer(held, testEnvLeasesFinalizer)
		if err := r.Patch(ctx, held, client.MergeFrom(&env)); err != nil {
			return ctrl.Result{}, err
		}
		env.Finalizers, env.ResourceVersion = held.Finalizers, held.ResourceVersion
	}
	status := env.Status.DeepCopy()
	environment, err := providerEnvironment(ctx, r.reader(), &env, r.SharedPullSecrets)
	if err != nil {
		return ctrl.Result{}, err
	}
	credentials := model.TestEnvCredentialsValid
	if model.TestEnvRemote(member) {
		credentials = r.checkCredentials(ctx, &env, status, environment, provider)
	}
	reason, message, err := r.readiness(ctx, &env)
	if err != nil {
		return ctrl.Result{}, err
	}
	if credentials != model.TestEnvCredentialsValid {
		reason, message = model.TestEnvCredentialsInvalid, "its CredentialsValid condition is False: "+credentials
	}
	condition := metav1.ConditionFalse
	if reason == model.TestEnvReady {
		condition = metav1.ConditionTrue
	}
	setCondition(&status.Conditions, env.Generation, c4v1beta1.ConditionTestEnvironmentReady, condition, reason, message)

	if providerErr != nil {
		logf.FromContext(ctx).Error(providerErr, "Failed to find the test environment's provider", "member", member)
		return ctrl.Result{}, r.writeStatus(ctx, &env, status)
	}
	if credentials != model.TestEnvCredentialsValid {
		// The provider takes no call without a credential; the leases are
		// listed once it is valid.
		return ctrl.Result{RequeueAfter: r.pollInterval()}, r.writeStatus(ctx, &env, status)
	}
	leases, err := provider.Leases(ctx, environment)
	if err != nil {
		return ctrl.Result{}, err
	}
	work, err := r.ciWorkFor(ctx, &env, leases)
	if err != nil {
		return ctrl.Result{}, err
	}
	live := make([]ports.Lease, 0, len(leases))
	for _, l := range leases {
		held, err := r.held(ctx, env.Namespace, l, work)
		if err != nil {
			return ctrl.Result{}, err
		}
		if model.OrphanLease(held, l.Started, r.now(), r.orphanGrace()) {
			if err := r.returnLease(ctx, provider, environment, l, work); err != nil {
				return ctrl.Result{}, err
			}
			logf.FromContext(ctx).Info("Returned a lease its holder holds no more", "lease", l.ID, "holder", l.Holder)
			continue
		}
		live = append(live, l)
	}
	soon := false
	if work != nil {
		if live, soon, err = r.serveCI(ctx, &env, status, environment, provider, work, live); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		apimeta.RemoveStatusCondition(&status.Conditions, c4v1beta1.ConditionTestEnvironmentCI)
	}
	status.SpentToday = spentToday(status.SpentToday, status.Leases, live, r.now())
	status.Leases = leaseStatus(live)
	if runner, ok := provider.(ports.TestRuns); ok && r.tendTestRuns(ctx, &env, status, environment, runner, live) {
		soon = true
	}
	return ctrl.Result{RequeueAfter: r.recheck(soon)}, r.writeStatus(ctx, &env, status)
}

// checkCredentials sets a remote env's CredentialsValid: its Secret is there
// with the member's keys, and its provider takes them in the account the
// member names. It returns the condition's reason. The Secret's values
// never reach the condition or the log.
func (r *TestEnvironmentReconciler) checkCredentials(ctx context.Context, env *c4v1beta1.TestEnvironment,
	status *c4v1beta1.TestEnvironmentStatus, environment ports.Environment, provider ports.TestEnvironments) string {
	secret := testEnvSecretName(env)
	var missing []string
	var detail string
	reason := model.TestEnvCredentialsValid
	switch {
	case environment.Secret == nil:
		reason = model.TestEnvSecretMissing
	case len(model.TestEnvMissingKeys(environment.Member, environment.Secret)) > 0:
		reason, missing = model.TestEnvSecretIncomplete, model.TestEnvMissingKeys(environment.Member, environment.Secret)
	case provider == nil:
		reason, detail = model.TestEnvUnreachable, "the operator has no provider for member "+environment.Member
	default:
		checker, ok := provider.(ports.TestEnvironmentCredentials)
		if !ok {
			break
		}
		err := checker.CheckCredentials(ctx, environment)
		switch {
		case err == nil:
		case errors.Is(err, ports.ErrCredentialRefused):
			reason, detail = model.TestEnvCredentialRefused, err.Error()
		default:
			reason, detail = model.TestEnvUnreachable, err.Error()
		}
	}
	value := metav1.ConditionFalse
	if reason == model.TestEnvCredentialsValid {
		value = metav1.ConditionTrue
	}
	if cond := apimeta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionTestEnvironmentCredentialsValid); cond == nil ||
		cond.Reason != reason {
		logf.FromContext(ctx).Info("Checked the test environment's credential", "member", environment.Member,
			"reason", reason)
	}
	setCondition(&status.Conditions, env.Generation, c4v1beta1.ConditionTestEnvironmentCredentialsValid, value, reason,
		model.TestEnvCredentialsMessage(reason, secret, missing, detail))
	return reason
}

// release returns a deleted remote environment's leases and has its
// provider release what it keeps for it, then lets the environment go. With
// no provider, no Secret or a credential refused nothing can be returned,
// and a provider that keeps failing is given up on after
// testEnvReleaseWithin: the machines left end at their deadline.
func (r *TestEnvironmentReconciler) release(ctx context.Context, env *c4v1beta1.TestEnvironment,
	provider ports.TestEnvironments) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(env, testEnvLeasesFinalizer) {
		return ctrl.Result{}, nil
	}
	log := logf.FromContext(ctx)
	let := func(why string) (ctrl.Result, error) {
		if why != "" {
			log.Info("Left a deleted test environment's leases to end at their deadline", "why", why)
		}
		gone := env.DeepCopy()
		controllerutil.RemoveFinalizer(gone, testEnvLeasesFinalizer)
		return ctrl.Result{}, client.IgnoreNotFound(r.Patch(ctx, gone, client.MergeFrom(env)))
	}
	if provider == nil {
		return let("the operator has no provider for its member")
	}
	environment, err := providerEnvironment(ctx, r.reader(), env, r.SharedPullSecrets)
	if err != nil {
		return ctrl.Result{}, err
	}
	if environment.Secret == nil {
		return let("its credentials Secret is gone")
	}
	leases, err := provider.Leases(ctx, environment)
	var work *ciWork
	if err == nil {
		// A CI lease's runner goes with it, as far as its host answers.
		work, err = r.ciWorkFor(ctx, env, leases)
	}
	for i := 0; err == nil && i < len(leases); i++ {
		if err = r.returnLease(ctx, provider, environment, leases[i], work); err == nil {
			log.Info("Returned a deleted test environment's lease", "lease", leases[i].ID, "holder", leases[i].Holder)
		}
	}
	if releaser, ok := provider.(ports.TestEnvironmentReleaser); ok && err == nil {
		err = releaser.Release(ctx, environment)
	}
	switch {
	case err == nil:
		log.Info("Released the deleted test environment", "member", environment.Member)
		return let("")
	case errors.Is(err, ports.ErrCredentialRefused):
		return let("its provider refuses the credential: " + err.Error())
	case time.Since(env.DeletionTimestamp.Time) > testEnvReleaseWithin(env):
		return let("its provider kept failing: " + err.Error())
	}
	log.Info("Could not release the deleted test environment yet; trying again", "error", err.Error())
	return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
}

// readiness says whether runs can lease env: a local member's image is
// pinned and the project's fence for its pods is there; a remote member
// needs neither (its credential is CredentialsValid's).
func (r *TestEnvironmentReconciler) readiness(ctx context.Context, env *c4v1beta1.TestEnvironment) (string, string, error) {
	if local := env.Spec.Local; local != nil && !model.ImagePinned(local.Image) {
		return model.TestEnvImageNotPinned, fmt.Sprintf("image %q names no tag or digest, or latest", local.Image), nil
	}
	if model.TestEnvRemote(testEnvMember(env)) {
		return model.TestEnvReady, "", nil
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

// held reports whether lease l's holder still holds it: a Task that is
// there, unfinished, and lists the lease in status.environments, or a CI
// job's runner while work says it needs the lease (step 8).
func (r *TestEnvironmentReconciler) held(ctx context.Context, namespace string, l ports.Lease,
	work *ciWork) (bool, error) {
	if l.Holder == "" {
		return false, nil
	}
	if _, _, ok := model.ParseCIHolder(l.Holder); ok {
		return work.held(l, r.now()), nil
	}
	var task c4v1beta1.Task
	err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: l.Holder}, &task)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !isFinal(task.Status.Phase) && slices.ContainsFunc(task.Status.Environments,
		func(held c4v1beta1.TaskEnvironmentLease) bool { return held.Lease == l.ID }), nil
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
	switch {
	case env.Spec.Local != nil:
		return model.TestEnvMemberLocal
	case env.Spec.AWS != nil:
		return model.TestEnvMemberAWS
	case env.Spec.DeviceFarm != nil:
		return model.TestEnvMemberDeviceFarm
	}
	return ""
}

// testEnvSecretName is the Secret a remote member's credential is in, ""
// for a member that needs none.
func testEnvSecretName(env *c4v1beta1.TestEnvironment) string {
	if a := env.Spec.AWS; a != nil {
		return a.CredentialsSecretRef.Name
	}
	if d := env.Spec.DeviceFarm; d != nil {
		return d.CredentialsSecretRef.Name
	}
	return ""
}

// providerEnvironment is env as its provider reads it, a remote member's
// credential among it, read live from its Secret: the operator caches no
// Secrets. Secret is nil when the Secret is missing, and never logged or
// written to a status.
func providerEnvironment(ctx context.Context, reader client.Reader, env *c4v1beta1.TestEnvironment,
	pullSecrets []string) (ports.Environment, error) {
	out := testEnvironment(env, pullSecrets)
	name := testEnvSecretName(env)
	if name == "" {
		return out, nil
	}
	var secret corev1.Secret
	err := reader.Get(ctx, client.ObjectKey{Namespace: env.Namespace, Name: name}, &secret)
	switch {
	case apierrors.IsNotFound(err):
		return out, nil
	case err != nil:
		return out, err
	}
	out.Secret = secret.Data
	if out.Secret == nil {
		out.Secret = map[string][]byte{}
	}
	return out, nil
}

// testEnvironment is env as its provider reads it, the pull Secrets its
// pods list among its settings; a remote member's credential is
// providerEnvironment's.
func testEnvironment(env *c4v1beta1.TestEnvironment, pullSecrets []string) ports.Environment {
	out := ports.Environment{Namespace: env.Namespace, Name: env.Name, UID: string(env.UID), Member: testEnvMember(env)}
	if a := env.Spec.AWS; a != nil {
		out.AWS = &ports.AWSEnvironment{Region: a.Region, Account: a.Account, InstanceType: a.InstanceType,
			ImageID: a.ImageID, SubnetID: a.SubnetID}
		if a.Mac != nil {
			out.AWS.KeepHostHours = int(a.Mac.KeepHostHours)
		}
	}
	if d := env.Spec.DeviceFarm; d != nil {
		out.DeviceFarm = &ports.DeviceFarmEnvironment{ProjectARN: d.ProjectARN, DevicePoolARN: d.DevicePoolARN,
			Account: d.Account}
	}
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

// reader reads Secrets live: APIReader, else the client.
func (r *TestEnvironmentReconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
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
