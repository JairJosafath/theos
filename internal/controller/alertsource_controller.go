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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// AlertSourceReconciler reports in the condition Ready whether each
// AlertSource takes deliveries, and files what its Receiver's inbox holds
// (docs/plans/done/alerts.md, decisions 1, 2 and 8 to 11): for each alert its
// Container, the facts, model.AlertStep, the call to the Git host, then one
// status write, and only then is the alert dropped from the inbox. It makes
// the comments its alerts' issues and their Containers' days are owed, and
// comes back when the next one is due.
type AlertSourceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader reads the source's Secret live: the operator caches no
	// Secrets, so it reads the Secret on each poll. It reads the source
	// live before a drain too, so a status write starts from its latest.
	APIReader client.Reader
	// Git builds the Provider of a Container's GitProvider, where its
	// alerts are filed.
	Git GitClient
	// Receiver holds the inboxes; nil when the operator's listener is off,
	// and every source is ReceiverOff.
	Receiver *AlertReceiver
	// EventsURL is the operator's --git-events-url, which status.url starts
	// with.
	EventsURL string
	// PollInterval is how often the Secret is read; 0 means a minute.
	PollInterval time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// +kubebuilder:rbac:groups=theos.factory.io,resources=alertsources,verbs=get;list;watch
// +kubebuilder:rbac:groups=theos.factory.io,resources=alertsources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=theos.factory.io,resources=containers,verbs=get;list;watch
// +kubebuilder:rbac:groups=theos.factory.io,resources=deploytargets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

// errAlertSourceStale is a status write refused because the source changed
// since it was read: the reconcile is tried again.
var errAlertSourceStale = errors.New("the AlertSource changed since it was read")

// alertSourceStaleRetry is how soon a source found stale is read again.
const alertSourceStaleRetry = 100 * time.Millisecond

func (r *AlertSourceReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Reconcile sets Ready from the source's Secret and the receiver:
// SecretMissing when there is no Secret, or it has no value under the key the
// source's type checks deliveries with (an empty value included);
// ReceiverOff when the operator's listener is off; else Receiving, with
// status.url. Then it drains the source's inbox and makes what is owed. It
// reads the Secret again every PollInterval, and comes back sooner when a
// comment is due sooner.
func (r *AlertSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var src c4v1beta1.AlertSource
	if err := r.Get(ctx, req.NamespacedName, &src); err != nil {
		if apierrors.IsNotFound(err) && r.Receiver != nil {
			r.Receiver.forget(req.NamespacedName)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	status := src.Status.DeepCopy()
	poll := ctrl.Result{RequeueAfter: r.pollInterval()}

	name := src.Spec.SecretRef.Name
	var secret corev1.Secret
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: src.Namespace, Name: name}, &secret)
	switch {
	case apierrors.IsNotFound(err):
		r.setReady(ctx, &src, status, metav1.ConditionFalse, c4v1beta1.ReasonAlertSourceSecretMissing,
			fmt.Sprintf("Secret %s is missing: until it is there, every delivery is refused", name))
	case err != nil:
		return ctrl.Result{}, fmt.Errorf("reading Secret %s: %w", name, err)
	default:
		if why := model.AlertSecretMissing(string(src.Spec.Type), name, secret.Data); why != "" {
			r.setReady(ctx, &src, status, metav1.ConditionFalse, c4v1beta1.ReasonAlertSourceSecretMissing, why)
		} else if r.Receiver == nil {
			r.setReady(ctx, &src, status, metav1.ConditionFalse, c4v1beta1.ReasonAlertSourceReceiverOff,
				"The operator's alert receiver is off: it listens with --git-events-bind-address")
		} else {
			r.setReady(ctx, &src, status, metav1.ConditionTrue, c4v1beta1.ReasonAlertSourceReceiving,
				"The operator takes this source's deliveries at "+model.AlertsURL(r.EventsURL, src.Namespace,
					src.Name))
		}
	}
	if r.Receiver != nil {
		status.URL = model.AlertsURL(r.EventsURL, src.Namespace, src.Name)
	} else {
		status.URL = ""
	}
	if err := r.writeStatus(ctx, &src, status); err != nil {
		return r.retry(err)
	}
	if r.Receiver == nil {
		return poll, nil
	}
	wait, err := r.drain(ctx, req.NamespacedName)
	if err != nil {
		return r.retry(err)
	}
	if wait > 0 && wait < poll.RequeueAfter {
		poll.RequeueAfter = wait
	}
	return poll, nil
}

// retry is the result of a reconcile that failed with err: a stale source
// is read again at once, any other error backs off.
func (r *AlertSourceReconciler) retry(err error) (ctrl.Result, error) {
	if errors.Is(err, errAlertSourceStale) {
		return ctrl.Result{RequeueAfter: alertSourceStaleRetry}, nil
	}
	return ctrl.Result{}, err
}

// setReady sets the Ready condition, and logs when its reason changes.
func (r *AlertSourceReconciler) setReady(ctx context.Context, src *c4v1beta1.AlertSource,
	status *c4v1beta1.AlertSourceStatus, cond metav1.ConditionStatus, reason, message string) {
	if cur := apimeta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionAlertSourceReady); cur == nil ||
		cur.Reason != reason {
		logf.FromContext(ctx).Info("Set the AlertSource's Ready condition", "reason", reason, "message", message)
	}
	setCondition(&status.Conditions, src.Generation, c4v1beta1.ConditionAlertSourceReady, cond, reason, message)
}

// writeStatus patches src's status to status when it changed, under an
// optimistic lock: status holds counts, and a write from a stale read would
// lose some. A conflict is errAlertSourceStale.
func (r *AlertSourceReconciler) writeStatus(ctx context.Context, src *c4v1beta1.AlertSource,
	status *c4v1beta1.AlertSourceStatus) error {
	if equality.Semantic.DeepEqual(&src.Status, status) {
		return nil
	}
	orig := src.DeepCopy()
	src.Status = *status
	err := r.Status().Patch(ctx, src, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{}))
	if err != nil {
		src.Status = orig.Status
		if apierrors.IsConflict(err) {
			return fmt.Errorf("%w: %w", errAlertSourceStale, err)
		}
		return fmt.Errorf("writing the status of AlertSource %s: %w", src.Name, err)
	}
	return nil
}

func (r *AlertSourceReconciler) pollInterval() time.Duration {
	if r.PollInterval > 0 {
		return r.PollInterval
	}
	return time.Minute
}

// DrainAlerts files what key's inbox holds and makes what is owed: the
// receiver's drain on SIGTERM.
func (r *AlertSourceReconciler) DrainAlerts(ctx context.Context, key types.NamespacedName) error {
	_, err := r.drain(ctx, key)
	return err
}

// SetupWithManager sets up the controller with the Manager. A status write
// starts no reconcile; a spec change does, and so does a delivery. The Secret
// is not watched, since the operator caches no Secrets: it is read again
// every PollInterval.
func (r *AlertSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&c4v1beta1.AlertSource{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("alertsource")
	if r.Receiver != nil {
		b = b.WatchesRawSource(r.Receiver.Source())
	}
	return b.Complete(r)
}
