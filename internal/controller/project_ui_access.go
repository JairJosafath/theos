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

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// UIPodReaderName is the Role and RoleBinding that let the UI read the pods,
// and their metrics, of a project namespace: its agent sandboxes, for the
// Factory view's health. The UI reads pods nowhere else but in the factory's
// own namespaces, where the chart binds the same rules.
const UIPodReaderName = "theos-ui-pods"

// UIPodReaderRules are the rules of UIPodReaderName. The operator holds them
// itself, which is what lets it grant them.
var UIPodReaderRules = []rbacv1.PolicyRule{
	{APIGroups: []string{"", "metrics.k8s.io"}, Resources: []string{"pods"}, Verbs: []string{verbGet, "list"}},
}

// verbGet is the RBAC verb the project namespace's Roles grant most.
const verbGet = "get"

// +kubebuilder:rbac:groups=metrics.k8s.io,resources=pods,verbs=get;list

// ensureUIPodReader lets the UI's ServiceAccount read the project namespace's
// pods. Without a UI ServiceAccount (--ui-service-account) it does nothing.
func (r *ProjectReconciler) ensureUIPodReader(ctx context.Context, project *c4v1beta1.Project) error {
	if r.UIServiceAccount.Name == "" {
		return nil
	}
	meta := func() metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: UIPodReaderName, Namespace: project.Namespace}
	}
	labels := projectLabels(project.Namespace)

	role := &rbacv1.Role{ObjectMeta: meta()}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Labels = labels
		role.Rules = UIPodReaderRules
		return controllerutil.SetControllerReference(project, role, r.Scheme)
	}); err != nil {
		return err
	}

	binding := &rbacv1.RoleBinding{ObjectMeta: meta()}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, binding, func() error {
		binding.Labels = labels
		binding.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: UIPodReaderName}
		binding.Subjects = []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      r.UIServiceAccount.Name,
			Namespace: r.UIServiceAccount.Namespace,
		}}
		return controllerutil.SetControllerReference(project, binding, r.Scheme)
	})
	return err
}
