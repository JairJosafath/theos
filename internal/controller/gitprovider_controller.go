/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
// (docs/plans/git-events.md, decisions 3, 4 and 8).
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
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create

// Reconcile ensures the GitProvider's event hook: with no --git-events-url
// Events is Disabled, with an adapter that has no events Unsupported;
// otherwise the delivery Secret is made when missing, the hook is set to the
// receiver's URL with its value, and Events is Registered, or RegisterFailed
// with why. It runs again every EnsureInterval.
func (r *GitProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var gp c4v1beta1.GitProvider
	if err := r.Get(ctx, req.NamespacedName, &gp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	status := gp.Status.DeepCopy()
	if r.GitEventsURL == "" {
		r.setEvents(ctx, &gp, status, c4v1beta1.ReasonEventsDisabled,
			"The operator has no --git-events-url, so no hook is registered; the operator polls the host")
		return ctrl.Result{}, r.writeStatus(ctx, &gp, status)
	}
	ensure := ctrl.Result{RequeueAfter: r.ensureInterval()}

	events, err := r.events(ctx, &gp)
	if err != nil {
		r.setEvents(ctx, &gp, status, c4v1beta1.ReasonEventsRegisterFailed, err.Error())
		return ensure, r.writeStatus(ctx, &gp, status)
	}
	if events == nil {
		r.setEvents(ctx, &gp, status, c4v1beta1.ReasonEventsUnsupported,
			fmt.Sprintf("The %s adapter has no event hooks; the operator polls the host", gp.Spec.Provider))
		return ctrl.Result{}, r.writeStatus(ctx, &gp, status)
	}

	secret, err := r.eventsSecret(ctx, &gp)
	if err != nil {
		return ctrl.Result{}, err
	}
	hookURL := model.GitEventsURL(r.GitEventsURL, gp.Namespace, gp.Name)
	hostCtx, cancel := context.WithTimeout(ctx, eventsHostTimeout)
	defer cancel()
	changed, err := events.EnsureEventHook(hostCtx, ports.EventHook{URL: hookURL, Secret: secret})
	if err != nil {
		r.setEvents(ctx, &gp, status, c4v1beta1.ReasonEventsRegisterFailed,
			fmt.Sprintf("Could not register the hook to %s on %s: %v", hookURL, gp.Spec.Owner, err))
		return ensure, r.writeStatus(ctx, &gp, status)
	}
	if changed {
		logf.FromContext(ctx).Info("Registered the event hook", "owner", gp.Spec.Owner, "url", hookURL)
	}
	r.setEvents(ctx, &gp, status, c4v1beta1.ReasonEventsRegistered,
		fmt.Sprintf("Registered the hook to %s on %s; no delivery has been verified since", hookURL, gp.Spec.Owner))
	return ensure, r.writeStatus(ctx, &gp, status)
}

// events is the GitProvider's adapter as ports.Events, nil when the adapter
// has none.
func (r *GitProviderReconciler) events(ctx context.Context, gp *c4v1beta1.GitProvider) (ports.Events, error) {
	creds, found, err := readCredentials(ctx, r.APIReader, gp)
	if err != nil {
		return nil, fmt.Errorf("could not read Secret %s: %w", gp.Spec.CredentialsSecretRef.Name, err)
	}
	if !found {
		return nil, fmt.Errorf("secret %s of GitProvider %s not found", gp.Spec.CredentialsSecretRef.Name, gp.Name)
	}
	p, err := r.Git.Provider(ports.Config{Kind: string(gp.Spec.Provider), URL: gp.Spec.URL,
		Owner: gp.Spec.Owner, Credentials: creds})
	if err != nil {
		return nil, err
	}
	events, _ := p.(ports.Events)
	return events, nil
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

// setEvents sets the Events condition, False for every reason this step
// knows, and logs when its reason changes.
func (r *GitProviderReconciler) setEvents(ctx context.Context, gp *c4v1beta1.GitProvider,
	status *c4v1beta1.GitProviderStatus, reason, message string) {
	if cur := apimeta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionGitProviderEvents); cur == nil ||
		cur.Reason != reason {
		logf.FromContext(ctx).Info("Set the Events condition", "reason", reason, "message", message)
	}
	setCondition(&status.Conditions, gp.Generation, c4v1beta1.ConditionGitProviderEvents,
		metav1.ConditionFalse, reason, message)
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
// at the next ensure.
func (r *GitProviderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1beta1.GitProvider{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("gitprovider").
		Complete(r)
}
