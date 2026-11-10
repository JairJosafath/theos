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
	"crypto/rand"
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// GitProviderReconciler registers the operator's event hook on each
// GitProvider's owner, signed with a delivery Secret it makes, and reports in
// the condition Events whether the host's deliveries reach the operator
// (docs/plans/done/git-events.md, decisions 3, 4 and 8).
type GitProviderReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader reads the credentials and the delivery Secret live: the
	// operator caches no Secrets.
	APIReader client.Reader
	// Git builds the GitProvider's Provider; an adapter with ports.Events
	// takes the hook.
	Git GitClient
	// GitEventsURL is the base address the host delivers to
	// (--git-events-url); "" registers no hook.
	GitEventsURL string
	// EnsureInterval is how often the hook is ensured again; 0 means 10
	// minutes.
	EnsureInterval time.Duration
	// GitEvents is the receiver of the host's deliveries, nil when it is
	// off: it remembers each GitProvider's last verified and last refused
	// delivery and enqueues the GitProvider when either should move Events.
	GitEvents *GitEventsReceiver
}

// defaultEventsEnsure is how often a GitProvider's hook is ensured again: the
// host never returns a hook's secret to compare, so the operator sets it each
// time (decision 4), and a hook removed on the host comes back.
const defaultEventsEnsure = 10 * time.Minute

// eventsHostTimeout bounds the calls to the host, so the status write after
// them still lands.
const eventsHostTimeout = time.Minute

// +kubebuilder:rbac:groups=theos.factory.io,resources=gitproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=theos.factory.io,resources=gitproviders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=theos.factory.io,resources=gitproviders/finalizers,verbs=update
// +kubebuilder:rbac:groups=theos.factory.io,resources=containers;briefs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create

// Reconcile says what the GitProvider's host can do (setCapabilities) and
// ensures its event hook: with no --git-events-url
// Events is Disabled, with an adapter that has no events Unsupported;
// otherwise the delivery Secret is made when missing, the hook is set to the
// receiver's URL with its value, and Events is Registered, or RegisterFailed
// with why; once registered, Receiving after a verified delivery and
// DeliveryRefused after a refused one, whichever came last. It runs again
// every EnsureInterval.
func (r *GitProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var gp c4v1beta1.GitProvider
	if err := r.Get(ctx, req.NamespacedName, &gp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	status := gp.Status.DeepCopy()
	// The Secret is not watched, so every outcome looks again: what the
	// provider can do follows a Secret that gained what it lacked.
	ensure := ctrl.Result{RequeueAfter: r.ensureInterval()}
	host, err := gitProviderHost(ctx, r.APIReader, r.Git, &gp)
	r.setCapabilities(ctx, &gp, status, host, err)
	if r.GitEventsURL == "" {
		r.setEvents(ctx, &gp, status, metav1.ConditionFalse, c4v1beta1.ReasonEventsDisabled,
			"The operator has no --git-events-url, so no hook is registered; the operator polls the host")
		return ensure, r.writeStatus(ctx, &gp, status)
	}
	if err != nil {
		r.setEvents(ctx, &gp, status, metav1.ConditionFalse, c4v1beta1.ReasonEventsRegisterFailed, err.Error())
		return ensure, r.writeStatus(ctx, &gp, status)
	}
	events, _ := host.(ports.Events)
	if events == nil {
		r.setEvents(ctx, &gp, status, metav1.ConditionFalse, c4v1beta1.ReasonEventsUnsupported,
			fmt.Sprintf("The %s adapter has no event hooks; the operator polls the host", gp.Spec.Provider))
		return ensure, r.writeStatus(ctx, &gp, status)
	}

	secret, err := r.eventsSecret(ctx, &gp)
	if err != nil {
		return ctrl.Result{}, err
	}
	hookURL := model.GitEventsURL(r.GitEventsURL, gp.Namespace, gp.Name)
	hostCtx, cancel := context.WithTimeout(ctx, eventsHostTimeout)
	defer cancel()
	repositories, err := r.repositories(ctx, &gp)
	if err != nil {
		return ctrl.Result{}, err
	}
	changed, err := events.EnsureEventHook(hostCtx, ports.EventHook{URL: hookURL, Secret: secret,
		Repositories: repositories})
	if err != nil {
		r.setEvents(ctx, &gp, status, metav1.ConditionFalse, c4v1beta1.ReasonEventsRegisterFailed,
			fmt.Sprintf("Could not register the hook to %s on %s: %v", hookURL, gp.Spec.Owner, err))
		return ensure, r.writeStatus(ctx, &gp, status)
	}
	if changed {
		logf.FromContext(ctx).Info("Registered the event hook", "owner", gp.Spec.Owner, "url", hookURL)
	}
	r.setDeliveries(ctx, &gp, status, changed, hookURL)
	return ensure, r.writeStatus(ctx, &gp, status)
}

// setDeliveries sets Events once the hook is registered: Registered when it
// changed or no delivery was seen, else Receiving or DeliveryRefused after
// whichever of the last verified and the last refused delivery came last.
// With nothing remembered (the operator restarted) a Receiving or
// DeliveryRefused written before stays.
func (r *GitProviderReconciler) setDeliveries(ctx context.Context, gp *c4v1beta1.GitProvider,
	status *c4v1beta1.GitProviderStatus, changed bool, hookURL string) {
	key := types.NamespacedName{Namespace: gp.Namespace, Name: gp.Name}
	registered := fmt.Sprintf("Registered the hook to %s on %s; no delivery has been verified since",
		hookURL, gp.Spec.Owner)
	if changed {
		// Only deliveries to the hook as it is now count.
		r.GitEvents.forgetDeliveries(key)
		r.setEvents(ctx, gp, status, metav1.ConditionFalse, c4v1beta1.ReasonEventsRegistered, registered)
		return
	}
	rec, known := r.GitEvents.deliveries(key)
	switch {
	case known && rec.receiving():
		r.setEvents(ctx, gp, status, metav1.ConditionTrue, c4v1beta1.ReasonEventsReceiving,
			fmt.Sprintf("Deliveries from %s reach the operator; it reconciles what they concern at once", gp.Spec.Owner))
	case known:
		r.setEvents(ctx, gp, status, metav1.ConditionFalse, c4v1beta1.ReasonEventsRefused,
			fmt.Sprintf("A delivery to %s failed its signature after the last verified one: the host's hook "+
				"does not sign with Secret %s, or something else posted it; the operator polls the host",
				hookURL, model.GitEventsSecretName(gp.Name)))
	default:
		if cur := apimeta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionGitProviderEvents); cur != nil &&
			(cur.Reason == c4v1beta1.ReasonEventsReceiving || cur.Reason == c4v1beta1.ReasonEventsRefused) {
			return
		}
		r.setEvents(ctx, gp, status, metav1.ConditionFalse, c4v1beta1.ReasonEventsRegistered, registered)
	}
}

// eventsSecret is the value of the GitProvider's delivery Secret, read live;
// a missing Secret is made, from crypto/rand and owned by the GitProvider, and
// one that is there is never overwritten. Rotation is deleting it.
func (r *GitProviderReconciler) eventsSecret(ctx context.Context, gp *c4v1beta1.GitProvider) (string, error) {
	name := model.GitEventsSecretName(gp.Name)
	var secret corev1.Secret
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: gp.Namespace, Name: name}, &secret)
	switch {
	case err == nil:
		value := string(secret.Data[model.GitEventsSecretKey])
		if value == "" {
			return "", fmt.Errorf("secret %s has no key %q; delete it and the operator makes a new one",
				name, model.GitEventsSecretKey)
		}
		return value, nil
	case !apierrors.IsNotFound(err):
		return "", fmt.Errorf("reading Secret %s: %w", name, err)
	}
	value := rand.Text()
	secret = corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: gp.Namespace},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{model.GitEventsSecretKey: []byte(value)},
	}
	if err := controllerutil.SetControllerReference(gp, &secret, r.Scheme); err != nil {
		return "", err
	}
	if err := r.Create(ctx, &secret); err != nil {
		// Another reconcile made it first: the next one reads it.
		return "", fmt.Errorf("creating Secret %s: %w", name, err)
	}
	logf.FromContext(ctx).Info("Created the delivery Secret", "secret", name)
	return value, nil
}

// repositories are the repositories the operator works in on gp's host: its
// Containers' and its Briefs', sorted. A host whose owner holds the hook
// ignores them; one whose owner can hold none hooks each
// (ports.EventHook.Repositories). A repository made after this ensure is
// hooked at the next.
func (r *GitProviderReconciler) repositories(ctx context.Context, gp *c4v1beta1.GitProvider) ([]string, error) {
	var names []string
	var containers c4v1beta1.ContainerList
	if err := r.List(ctx, &containers, client.InNamespace(gp.Namespace)); err != nil {
		return nil, err
	}
	for _, c := range containers.Items {
		if c.Spec.GitProviderRef.Name == gp.Name {
			names = append(names, c.Name)
		}
	}
	var briefs c4v1beta1.BriefList
	if err := r.List(ctx, &briefs, client.InNamespace(gp.Namespace)); err != nil {
		return nil, err
	}
	for _, b := range briefs.Items {
		if b.Spec.GitProviderRef.Name == gp.Name {
			names = append(names, model.BriefRepository(b.Name))
		}
	}
	slices.Sort(names)
	return names, nil
}

// setCapabilities sets what the GitProvider's host can do, as the provider
// is configured: AgentAccess, OwnRunners and PythonIndex. host is the
// provider's adapter, or err why it could not be built: then nothing is
// known of the host but that agents get no credential there.
func (r *GitProviderReconciler) setCapabilities(ctx context.Context, gp *c4v1beta1.GitProvider,
	status *c4v1beta1.GitProviderStatus, host ports.Provider, err error) {
	set := func(condition string, ok bool, reason, message string) {
		value := metav1.ConditionFalse
		if ok {
			value = metav1.ConditionTrue
		}
		if cur := apimeta.FindStatusCondition(status.Conditions, condition); cur == nil || cur.Reason != reason {
			logf.FromContext(ctx).Info("Set a GitProvider's condition", "condition", condition, "reason", reason,
				"message", message)
		}
		setCondition(&status.Conditions, gp.Generation, condition, value, reason, message)
	}
	if err != nil {
		set(c4v1beta1.ConditionGitProviderAgentAccess, false, c4v1beta1.ReasonProviderUnavailable,
			"Agents get no credential: "+err.Error())
		apimeta.RemoveStatusCondition(&status.Conditions, c4v1beta1.ConditionGitProviderOwnRunners)
		apimeta.RemoveStatusCondition(&status.Conditions, c4v1beta1.ConditionGitProviderPythonIndex)
		return
	}
	available, reason, message := ports.AgentAccessState(host)
	set(c4v1beta1.ConditionGitProviderAgentAccess, available, reason, message)

	if _, registers := host.(ports.OwnerRunners); registers {
		set(c4v1beta1.ConditionGitProviderOwnRunners, true, c4v1beta1.ReasonCapabilityAvailable,
			"The operator registers the project's own runners with the host.")
	} else {
		set(c4v1beta1.ConditionGitProviderOwnRunners, false, c4v1beta1.ReasonCapabilityUnsupported,
			fmt.Sprintf("The %s adapter registers no runners: the host's own take the jobs.", gp.Spec.Provider))
	}
	if host.Repository("").PyPIUpload != "" {
		set(c4v1beta1.ConditionGitProviderPythonIndex, true, c4v1beta1.ReasonCapabilityAvailable,
			"A release's Python packages have an index to go to.")
	} else {
		set(c4v1beta1.ConditionGitProviderPythonIndex, false, c4v1beta1.ReasonPythonNoIndex,
			"The host has no Python package index and spec.packages.pypi names none: a release's Python packages "+
				"are not published.")
	}
}

// setEvents sets the Events condition, and logs when its reason changes.
func (r *GitProviderReconciler) setEvents(ctx context.Context, gp *c4v1beta1.GitProvider,
	status *c4v1beta1.GitProviderStatus, cond metav1.ConditionStatus, reason, message string) {
	if cur := apimeta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionGitProviderEvents); cur == nil ||
		cur.Reason != reason {
		logf.FromContext(ctx).Info("Set the Events condition", "reason", reason, "message", message)
	}
	setCondition(&status.Conditions, gp.Generation, c4v1beta1.ConditionGitProviderEvents,
		cond, reason, message)
}

// writeStatus patches gp's status to status when it changed.
func (r *GitProviderReconciler) writeStatus(ctx context.Context, gp *c4v1beta1.GitProvider,
	status *c4v1beta1.GitProviderStatus) error {
	if equality.Semantic.DeepEqual(&gp.Status, status) {
		return nil
	}
	orig := gp.DeepCopy()
	gp.Status = *status
	if err := r.Status().Patch(ctx, gp, client.MergeFrom(orig)); err != nil {
		return fmt.Errorf("writing the status of GitProvider %s: %w", gp.Name, err)
	}
	return nil
}

func (r *GitProviderReconciler) ensureInterval() time.Duration {
	if r.EnsureInterval > 0 {
		return r.EnsureInterval
	}
	return defaultEventsEnsure
}

// SetupWithManager sets up the controller with the Manager. A status write
// starts no reconcile; a spec change does. The delivery Secret is not
// watched, since the operator caches no Secrets: one deleted is made again
// at the next ensure. The receiver enqueues a GitProvider whose deliveries
// should move Events.
func (r *GitProviderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1beta1.GitProvider{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WatchesRawSource(r.GitEvents.ProviderSource()).
		Named("gitprovider").
		Complete(r)
}
