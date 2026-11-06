/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// reportTestEnvironments sets TestEnvironments on a Ready Container whose
// Standard or Framework asks for test environments: True when the project
// has each, False, NotInProject, naming the ones it lacks, whose runs go on
// without them (docs/plans/test-environments.md, T2). A Container asking
// for none has no such condition.
func (r *ContainerReconciler) reportTestEnvironments(ctx context.Context, container *c4v1beta1.Container,
	status *c4v1beta1.ContainerStatus) {
	asked, err := askedEnvironments(ctx, r.Client, container)
	if err != nil {
		logf.FromContext(ctx).Error(err, "Failed to read the test environments the Container's Standard and Framework ask for")
		return
	}
	if len(asked) == 0 {
		meta.RemoveStatusCondition(&status.Conditions, c4v1beta1.ConditionTestEnvironments)
		return
	}
	var list c4v1beta1.TestEnvironmentList
	if err := r.List(ctx, &list, client.InNamespace(container.Namespace)); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list the project's test environments")
		return
	}
	names := make([]string, 0, len(list.Items))
	for _, e := range list.Items {
		names = append(names, e.Name)
	}
	if skipped := model.EnvironmentsOf(nil, asked, names).Skipped; len(skipped) > 0 {
		setCondition(&status.Conditions, container.Generation, c4v1beta1.ConditionTestEnvironments,
			metav1.ConditionFalse, model.EnvNotInProject, model.MissingEnvironmentsMessage(skipped))
		return
	}
	setCondition(&status.Conditions, container.Generation, c4v1beta1.ConditionTestEnvironments,
		metav1.ConditionTrue, model.EnvPresent, "")
}

// namespaceContainers are the requests of every Container in obj's
// namespace: a TestEnvironment made or deleted there changes what their
// Standards' asks find.
func namespaceContainers(c client.Client) func(context.Context, client.Object) []reconcile.Request {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		var containers c4v1beta1.ContainerList
		if err := c.List(ctx, &containers, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(containers.Items))
		for _, ct := range containers.Items {
			reqs = append(reqs, requestFor(ct.Namespace, ct.Name))
		}
		return reqs
	}
}
