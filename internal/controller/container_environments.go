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
// without them (docs/plans/done/test-environments.md, T2). A Container asking
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

// projectEnvironments are the TestEnvironments of namespace ns, as the
// planner and the triager read them (docs/plans/done/test-environments.md, T13).
func projectEnvironments(ctx context.Context, c client.Client, ns string) ([]model.WorkspaceEnvironment, error) {
	var list c4v1beta1.TestEnvironmentList
	if err := c.List(ctx, &list, client.InNamespace(ns)); err != nil {
		return nil, err
	}
	out := make([]model.WorkspaceEnvironment, 0, len(list.Items))
	for _, e := range list.Items {
		out = append(out, model.WorkspaceEnvironment{Name: e.Name, Description: e.Spec.Description})
	}
	return out, nil
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
