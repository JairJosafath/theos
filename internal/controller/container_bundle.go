/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"strings"

	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// reportBundleCurrent sets BundleCurrent on a Ready Container naming a Bundle:
// True while the Bundle still names the entries it scaffolded from, False with
// reason BundleChanged listing the sides that differ, or saying the Bundle is
// gone. It changes nothing else: a changed Bundle is reported, never applied
// (docs/plans/done/catalog-bundles.md, decision 6; #1621). A Container naming its
// sides has no such condition.
func (r *ContainerReconciler) reportBundleCurrent(ctx context.Context, container *c4v1beta1.Container,
	status *c4v1beta1.ContainerStatus) {
	scaffolded := status.Bundle
	if container.Spec.BundleRef == nil || scaffolded == nil {
		meta.RemoveStatusCondition(&status.Conditions, c4v1beta1.ConditionBundleCurrent)
		return
	}
	var bundle c4v1beta1.Bundle
	found, err := r.lookup(ctx, container.Namespace, container.Spec.BundleRef.Name, &bundle)
	if err != nil {
		// Left as it was: the next pass reads it again.
		logf.FromContext(ctx).Error(err, "Could not read the Container's Bundle", "bundle", container.Spec.BundleRef.Name)
		return
	}
	if !found {
		r.condition(container, status, c4v1beta1.ConditionBundleCurrent, metav1.ConditionFalse,
			c4v1beta1.ReasonBundleChanged, "Bundle "+container.Spec.BundleRef.Name+" is gone")
		return
	}
	changes := model.BundleChanges(
		model.Bundle{Name: scaffolded.Name, Framework: scaffolded.Framework, Standard: scaffolded.Standard,
			Compute: scaffolded.Compute, Deployment: scaffolded.Deployment},
		model.Bundle{Name: bundle.Name, Framework: bundle.Spec.FrameworkRef.Name, Standard: refName(bundle.Spec.StandardRef),
			Compute: refName(bundle.Spec.ComputeRef), Deployment: refName(bundle.Spec.DeploymentRef)})
	if len(changes) > 0 {
		r.condition(container, status, c4v1beta1.ConditionBundleCurrent, metav1.ConditionFalse,
			c4v1beta1.ReasonBundleChanged, strings.Join(changes, "; "))
		return
	}
	r.condition(container, status, c4v1beta1.ConditionBundleCurrent, metav1.ConditionTrue, c4v1beta1.ReasonUpToDate, "")
}
