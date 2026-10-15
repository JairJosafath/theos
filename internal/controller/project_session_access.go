/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// SessionAccountName is the ServiceAccount, Role and RoleBinding that the
// Session client uses in every project namespace.
const SessionAccountName = "c4-session"

// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch

// ensureSessionAccess gives the project's namespace a ServiceAccount that may only
// work with Sessions. In v1 any client with it can touch any Session in the
// project; limiting a client to its own Session needs short-lived tokens, later.
func (r *ProjectReconciler) ensureSessionAccess(ctx context.Context, project *c4v1alpha1.Project) error {
	meta := func() metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: SessionAccountName, Namespace: project.Namespace}
	}
	labels := projectLabels(project.Namespace)

	account := &corev1.ServiceAccount{ObjectMeta: meta()}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, account, func() error {
		account.Labels = labels
		return controllerutil.SetControllerReference(project, account, r.Scheme)
	}); err != nil {
		return err
	}

	role := &rbacv1.Role{ObjectMeta: meta()}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Labels = labels
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{c4v1alpha1.SchemeGroupVersion.Group},
				Resources: []string{"sessions"},
				Verbs:     []string{verbGet, "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{c4v1alpha1.SchemeGroupVersion.Group},
				Resources: []string{"sessions/status"},
				Verbs:     []string{verbGet, "update", "patch"},
			},
		}
		return controllerutil.SetControllerReference(project, role, r.Scheme)
	}); err != nil {
		return err
	}

	binding := &rbacv1.RoleBinding{ObjectMeta: meta()}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, binding, func() error {
		binding.Labels = labels
		// roleRef can't change after creation; it is the same every time.
		binding.RoleRef = rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: SessionAccountName}
		binding.Subjects = []rbacv1.Subject{{
			Kind: rbacv1.ServiceAccountKind, Name: SessionAccountName, Namespace: project.Namespace,
		}}
		return controllerutil.SetControllerReference(project, binding, r.Scheme)
	})
	return err
}
