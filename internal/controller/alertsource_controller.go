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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// AlertSourceReconciler reports in the condition Ready whether each
// AlertSource takes deliveries (docs/plans/alerts.md, decision 1). This build
// has no receiver of alerts (step 3), so a source whose Secret checks
// deliveries is ReceiverOff; one whose Secret can't is SecretMissing.
type AlertSourceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader reads the source's Secret live: the operator caches no
	// Secrets, so it reads the Secret on each poll.
	APIReader client.Reader
	// PollInterval is how often the Secret is read; 0 means a minute.
	PollInterval time.Duration
}

// +kubebuilder:rbac:groups=theos.factory.io,resources=alertsources,verbs=get;list;watch
// +kubebuilder:rbac:groups=theos.factory.io,resources=alertsources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

// Reconcile sets Ready from the source's Secret: SecretMissing when there is
// none, or it has no value under the key the source's type checks deliveries
// with (an empty value included), else ReceiverOff. It reads the Secret again
// every PollInterval.
func (r *AlertSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var src c4v1beta1.AlertSource
	if err := r.Get(ctx, req.NamespacedName, &src); err != nil {
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
		return poll, r.writeStatus(ctx, &src, status)
	case err != nil:
		return ctrl.Result{}, fmt.Errorf("reading Secret %s: %w", name, err)
	}
	if why := model.AlertSecretMissing(string(src.Spec.Type), name, secret.Data); why != "" {
		r.setReady(ctx, &src, status, metav1.ConditionFalse, c4v1beta1.ReasonAlertSourceSecretMissing, why)
		return poll, r.writeStatus(ctx, &src, status)
	}
	r.setReady(ctx, &src, status, metav1.ConditionFalse, c4v1beta1.ReasonAlertSourceReceiverOff,
		"The operator's alert receiver is off: this build takes no deliveries of alerts")
	return poll, r.writeStatus(ctx, &src, status)
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

// writeStatus patches src's status to status when it changed.
func (r *AlertSourceReconciler) writeStatus(ctx context.Context, src *c4v1beta1.AlertSource,
	status *c4v1beta1.AlertSourceStatus) error {
	if equality.Semantic.DeepEqual(&src.Status, status) {
		return nil
	}
	orig := src.DeepCopy()
	src.Status = *status
	if err := r.Status().Patch(ctx, src, client.MergeFrom(orig)); err != nil {
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

// SetupWithManager sets up the controller with the Manager. A status write
// starts no reconcile; a spec change does. The Secret is not watched, since
// the operator caches no Secrets: it is read again every PollInterval.
func (r *AlertSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1beta1.AlertSource{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("alertsource").
		Complete(r)
}
