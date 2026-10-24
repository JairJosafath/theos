/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"maps"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
)

// adopt makes owner the controller of obj and merges the ancestor labels, then
// patches obj if anything changed. Children name their parent in spec; this turns
// that into the ownerReference, so deletion follows the tree.
func adopt(ctx context.Context, c client.Client, scheme *runtime.Scheme,
	owner, obj client.Object, labels map[string]string) error {
	orig, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return nil
	}
	changed := false
	if !metav1.IsControlledBy(obj, owner) {
		if err := controllerutil.SetControllerReference(owner, obj, scheme); err != nil {
			return err
		}
		changed = true
	}
	current := obj.GetLabels()
	merged := maps.Clone(current)
	if merged == nil {
		merged = map[string]string{}
	}
	maps.Copy(merged, labels)
	if !maps.Equal(current, merged) {
		obj.SetLabels(merged)
		changed = true
	}
	if !changed {
		return nil
	}
	return c.Patch(ctx, obj, client.MergeFrom(orig))
}

// setCondition sets a condition and stamps it with the object's generation.
func setCondition(conds *[]metav1.Condition, generation int64, typ string,
	status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(conds, metav1.Condition{
		Type:               typ,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: generation,
	})
}

// projectLabels are the ancestor labels for anything in a project's namespace.
func projectLabels(namespace string) map[string]string {
	return map[string]string{c4v1beta1.LabelProject: namespace}
}

// requestFor returns a reconcile request for a named object in a namespace.
func requestFor(namespace, name string) reconcile.Request {
	return reconcile.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}}
}
