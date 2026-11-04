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
	"maps"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// ConditionSecretsShared reports the Secrets the operator copies into the
// Project's namespace, the pull Secrets: each one a copy, the project's own,
// or missing at its source. Absent when the operator copies nothing. The
// shared token is never copied: the model proxy reads it in the operator's
// namespace (#1137).
const ConditionSecretsShared = "SecretsShared"

// DefaultSharedSecretsRecheck is how often a Project in an opted-in namespace
// looks at its sources again. The operator doesn't watch Secrets: that would
// cache every Secret in the cluster.
const DefaultSharedSecretsRecheck = 5 * time.Minute

// sharedSecretsOptIn is the value of LabelSharedSecrets that opts a namespace in.
const sharedSecretsOptIn = "true"

// sharedNames are the Secrets the operator copies, sorted and without
// repeats: the pull Secrets alone. The --shared-secret token is read by the
// model proxy in SourceNamespace and never copied (#1137).
func (r *ProjectReconciler) sharedNames() []string {
	names := slices.Clone(r.SharedPullSecrets)
	slices.Sort(names)
	return slices.Compact(names)
}

// retiredNames are the Secrets the operator copied before the model proxy
// and no longer does: the --shared-secret token. A copy it made (labelled
// LabelSharedFrom) is deleted wherever it is found; a project's own Secret
// of that name stays.
func (r *ProjectReconciler) retiredNames() []string {
	var names []string
	for _, name := range r.SharedSecrets {
		if !slices.Contains(r.SharedPullSecrets, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// deleteRetiredCopies deletes the token copies an earlier release made in
// the namespace (#1137).
func (r *ProjectReconciler) deleteRetiredCopies(ctx context.Context, namespace string) error {
	for _, name := range r.retiredNames() {
		if err := r.deleteSharedCopy(ctx, namespace, name); err != nil {
			return err
		}
	}
	return nil
}

// shareSecrets copies the shared pull Secrets from the operator's namespace
// into an opted-in project namespace, keeps the copies in step with their
// sources, and deletes them when the namespace opts out. It deletes the token
// copies an earlier release made, opted in or not: the token stays in the
// operator's namespace, where the model proxy reads it (#1137). A Secret of a
// shared name without the shared-from label is the project's own and is never
// touched. It reports what it copies in the SecretsShared condition, and
// returns when to look again.
func (r *ProjectReconciler) shareSecrets(ctx context.Context, project *c4v1beta1.Project,
	status *c4v1beta1.ProjectStatus) (time.Duration, error) {
	if err := r.deleteRetiredCopies(ctx, project.Namespace); err != nil {
		return 0, err
	}
	names := r.sharedNames()
	if len(names) == 0 {
		meta.RemoveStatusCondition(&status.Conditions, ConditionSecretsShared)
		return 0, nil
	}
	var ns corev1.Namespace
	if err := r.APIReader.Get(ctx, client.ObjectKey{Name: project.Namespace}, &ns); err != nil {
		return 0, err
	}
	optedIn, reports, missing, err := r.shareInto(ctx, &ns, project)
	if err != nil {
		return 0, err
	}
	if !optedIn {
		setCondition(&status.Conditions, project.Generation, ConditionSecretsShared, metav1.ConditionFalse,
			"NotOptedIn", fmt.Sprintf("namespace %s doesn't have the label %s=%s, so the factory shares none of %s",
				project.Namespace, c4v1beta1.LabelSharedSecrets, sharedSecretsOptIn, strings.Join(names, ", ")))
		return 0, nil
	}
	if missing {
		setCondition(&status.Conditions, project.Generation, ConditionSecretsShared, metav1.ConditionFalse,
			"SourceMissing", strings.Join(reports, "; "))
	} else {
		setCondition(&status.Conditions, project.Generation, ConditionSecretsShared, metav1.ConditionTrue,
			"Shared", strings.Join(reports, "; "))
	}
	return r.recheck(), nil
}

// shareBeforeProject shares into an opted-in namespace that has no Project
// yet, as shareSecrets does but with nowhere to report: a Brief there needs
// the pull Secret for the round that makes its Project. The copies have no
// owner until the Project adopts them.
func (r *ProjectReconciler) shareBeforeProject(ctx context.Context, namespace string) (time.Duration, error) {
	if len(r.sharedNames())+len(r.retiredNames()) == 0 {
		return 0, nil
	}
	var ns corev1.Namespace
	if err := r.APIReader.Get(ctx, client.ObjectKey{Name: namespace}, &ns); err != nil {
		return 0, client.IgnoreNotFound(err)
	}
	if !ns.DeletionTimestamp.IsZero() {
		return 0, nil
	}
	if err := r.deleteRetiredCopies(ctx, namespace); err != nil {
		return 0, err
	}
	if len(r.sharedNames()) == 0 {
		return 0, nil
	}
	optedIn, _, _, err := r.shareInto(ctx, &ns, nil)
	if err != nil || !optedIn {
		return 0, err
	}
	return r.recheck(), nil
}

// shareInto copies the shared pull Secrets into ns when it has opted in, and
// deletes the copies when it hasn't. It returns whether ns has opted in, what
// the condition says of each Secret, and whether a source is missing. owner is
// nil before the namespace has a Project.
func (r *ProjectReconciler) shareInto(ctx context.Context, ns *corev1.Namespace,
	owner *c4v1beta1.Project) (bool, []string, bool, error) {
	names := r.sharedNames()
	if ns.Labels[c4v1beta1.LabelSharedSecrets] != sharedSecretsOptIn {
		for _, name := range names {
			if err := r.deleteSharedCopy(ctx, ns.Name, name); err != nil {
				return false, nil, false, err
			}
		}
		return false, nil, false, nil
	}
	var reports []string
	missing := false
	for _, name := range names {
		report, ok, err := r.shareSecret(ctx, ns.Name, owner, name)
		if err != nil {
			return true, nil, false, err
		}
		reports = append(reports, report)
		missing = missing || !ok
	}
	return true, reports, missing, nil
}

// shareSecret makes or updates one copy. It returns what the condition says of
// it, and false when its source is missing.
func (r *ProjectReconciler) shareSecret(ctx context.Context, namespace string, owner *c4v1beta1.Project,
	name string) (string, bool, error) {
	log := logf.FromContext(ctx)
	var copied corev1.Secret
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &copied)
	exists := err == nil
	if err != nil && !apierrors.IsNotFound(err) {
		return "", false, err
	}
	if exists && copied.Labels[c4v1beta1.LabelSharedFrom] == "" {
		return name + ": the project's own", true, nil
	}

	var source corev1.Secret
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: r.SourceNamespace, Name: name}, &source); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Sprintf("%s: missing in namespace %s", name, r.SourceNamespace), false, nil
		}
		return "", false, err
	}
	shared := fmt.Sprintf("%s: shared from namespace %s", name, r.SourceNamespace)

	labels := projectLabels(namespace)
	labels[c4v1beta1.LabelSharedFrom] = name
	if exists && copied.Type != source.Type {
		// A Secret's type can't change: make the copy again.
		if err := r.Delete(ctx, &copied, client.Preconditions{UID: &copied.UID}); client.IgnoreNotFound(err) != nil {
			return "", false, err
		}
		exists = false
	}
	if !exists {
		copied = corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
			Type:       source.Type,
			Data:       source.Data,
		}
		if err := r.own(owner, &copied); err != nil {
			return "", false, err
		}
		if err := r.Create(ctx, &copied); err != nil {
			return "", false, err
		}
		log.Info("Shared Secret", "secret", name, "from", r.SourceNamespace)
		return shared, true, nil
	}

	if maps.EqualFunc(copied.Data, source.Data, slices.Equal) && maps.Equal(copied.Labels, labels) &&
		(owner == nil || metav1.IsControlledBy(&copied, owner)) {
		return shared, true, nil
	}
	copied.Data = source.Data
	copied.StringData = nil
	copied.Labels = labels
	if err := r.own(owner, &copied); err != nil {
		return "", false, err
	}
	if err := r.Update(ctx, &copied); err != nil {
		return "", false, err
	}
	log.Info("Updated shared Secret", "secret", name, "from", r.SourceNamespace)
	return shared, true, nil
}

// own makes the Project, if there is one yet, the copy's controller.
func (r *ProjectReconciler) own(owner *c4v1beta1.Project, copied *corev1.Secret) error {
	if owner == nil {
		return nil
	}
	return controllerutil.SetControllerReference(owner, copied, r.Scheme)
}

// deleteSharedCopy deletes the operator's copy of a shared Secret, if the
// namespace has one; the project's own Secret of that name stays.
func (r *ProjectReconciler) deleteSharedCopy(ctx context.Context, namespace, name string) error {
	var copied corev1.Secret
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &copied); err != nil {
		return client.IgnoreNotFound(err)
	}
	if copied.Labels[c4v1beta1.LabelSharedFrom] == "" {
		return nil
	}
	if err := r.Delete(ctx, &copied, client.Preconditions{UID: &copied.UID}); client.IgnoreNotFound(err) != nil {
		return err
	}
	logf.FromContext(ctx).Info("Deleted shared Secret", "secret", name)
	return nil
}

func (r *ProjectReconciler) recheck() time.Duration {
	if r.SharedSecretsRecheck > 0 {
		return r.SharedSecretsRecheck
	}
	return DefaultSharedSecretsRecheck
}
