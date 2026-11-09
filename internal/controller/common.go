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
	"maps"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// adopt makes owner the controller of obj and merges the ancestor labels, then
// patches obj if anything changed. Children name their parent in spec; this turns
// that into the ownerReference, so deletion follows the tree. The patch
// replaces obj with what the API server holds, status included, newer than
// the cache: a reconciler copies the status it writes after adopt, never
// before, or a status written since its read is patched away (#591, #1455).
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
