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

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// ConditionSecretsShared reports the Secrets the operator shares into the
// Project's namespace: each one a copy, the project's own, or missing at its
// source. Absent when the operator shares nothing.
const ConditionSecretsShared = "SecretsShared"

// DefaultSharedSecretsRecheck is how often a Project in an opted-in namespace
// looks at its sources again. The operator doesn't watch Secrets: that would
// cache every Secret in the cluster.
const DefaultSharedSecretsRecheck = 5 * time.Minute

// sharedSecretsOptIn is the value of LabelSharedSecrets that opts a namespace in.
const sharedSecretsOptIn = "true"

// sharedNames are the Secrets the operator shares, sorted and without repeats.
func (r *ProjectReconciler) sharedNames() []string {
	names := slices.Concat(r.SharedSecrets, r.SharedPullSecrets)
	slices.Sort(names)
	return slices.Compact(names)
}

// shareSecrets copies the shared Secrets from the operator's namespace into an
// opted-in project namespace, keeps the copies in step with their sources, and
// deletes them when the namespace opts out. A Secret of a shared name without
// the shared-from label is the project's own and is never touched. It reports
// in the SecretsShared condition, and returns when to look again.
func (r *ProjectReconciler) shareSecrets(ctx context.Context, project *c4v1alpha1.Project,
	status *c4v1alpha1.ProjectStatus) (time.Duration, error) {
	names := r.sharedNames()
	if len(names) == 0 {
		meta.RemoveStatusCondition(&status.Conditions, ConditionSecretsShared)
		return 0, nil
	}
	var ns corev1.Namespace
	if err := r.APIReader.Get(ctx, client.ObjectKey{Name: project.Namespace}, &ns); err != nil {
		return 0, err
	}
	if ns.Labels[c4v1alpha1.LabelSharedSecrets] != sharedSecretsOptIn {
		for _, name := range names {
			if err := r.deleteSharedCopy(ctx, project.Namespace, name); err != nil {
				return 0, err
			}
		}
		setCondition(&status.Conditions, project.Generation, ConditionSecretsShared, metav1.ConditionFalse,
			"NotOptedIn", fmt.Sprintf("namespace %s doesn't have the label %s=%s, so the factory shares none of %s",
				project.Namespace, c4v1alpha1.LabelSharedSecrets, sharedSecretsOptIn, strings.Join(names, ", ")))
		return 0, nil
	}

	var reports, missing []string
	for _, name := range names {
		report, ok, err := r.shareSecret(ctx, project, name)
		if err != nil {
			return 0, err
		}
		reports = append(reports, report)
		if !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		setCondition(&status.Conditions, project.Generation, ConditionSecretsShared, metav1.ConditionFalse,
			"SourceMissing", strings.Join(reports, "; "))
	} else {
		setCondition(&status.Conditions, project.Generation, ConditionSecretsShared, metav1.ConditionTrue,
			"Shared", strings.Join(reports, "; "))
	}
	return r.recheck(), nil
}

// shareSecret makes or updates one copy. It returns what the condition says of
// it, and false when its source is missing.
func (r *ProjectReconciler) shareSecret(ctx context.Context, project *c4v1alpha1.Project,
	name string) (string, bool, error) {
	log := logf.FromContext(ctx)
	var copied corev1.Secret
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: project.Namespace, Name: name}, &copied)
	exists := err == nil
	if err != nil && !apierrors.IsNotFound(err) {
		return "", false, err
	}
	if exists && copied.Labels[c4v1alpha1.LabelSharedFrom] == "" {
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

	labels := projectLabels(project.Namespace)
	labels[c4v1alpha1.LabelSharedFrom] = name
	if exists && copied.Type != source.Type {
		// A Secret's type can't change: make the copy again.
		if err := r.Delete(ctx, &copied, client.Preconditions{UID: &copied.UID}); client.IgnoreNotFound(err) != nil {
			return "", false, err
		}
		exists = false
	}
	if !exists {
		copied = corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: project.Namespace, Labels: labels},
			Type:       source.Type,
			Data:       source.Data,
		}
		if err := controllerutil.SetControllerReference(project, &copied, r.Scheme); err != nil {
			return "", false, err
		}
		if err := r.Create(ctx, &copied); err != nil {
			return "", false, err
		}
		log.Info("Shared Secret", "secret", name, "from", r.SourceNamespace)
		return shared, true, nil
	}

	if maps.EqualFunc(copied.Data, source.Data, slices.Equal) && maps.Equal(copied.Labels, labels) &&
		metav1.IsControlledBy(&copied, project) {
		return shared, true, nil
	}
	copied.Data = source.Data
	copied.StringData = nil
	copied.Labels = labels
	if err := controllerutil.SetControllerReference(project, &copied, r.Scheme); err != nil {
		return "", false, err
	}
	if err := r.Update(ctx, &copied); err != nil {
		return "", false, err
	}
	log.Info("Updated shared Secret", "secret", name, "from", r.SourceNamespace)
	return shared, true, nil
}

// deleteSharedCopy deletes the operator's copy of a shared Secret, if the
// namespace has one; the project's own Secret of that name stays.
func (r *ProjectReconciler) deleteSharedCopy(ctx context.Context, namespace, name string) error {
	var copied corev1.Secret
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &copied); err != nil {
		return client.IgnoreNotFound(err)
	}
	if copied.Labels[c4v1alpha1.LabelSharedFrom] == "" {
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
