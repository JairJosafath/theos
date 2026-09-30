/*
Copyright 2026.

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
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// createOrDelete passes only creations and deletions: enough for watches that
// resolve references by name.
func createOrDelete() builder.WatchesOption {
	return builder.WithPredicates(predicate.Funcs{
		UpdateFunc:  func(event.UpdateEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	})
}

// specOrRetry passes a Container's creation, deletion and spec changes, and
// changes to its retry annotation, but not the operator's own status and
// metadata writes: a Failed Container stays failed until one of its inputs
// changes or someone asks for a retry.
func specOrRetry() builder.Predicates {
	retryChanged := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			return e.ObjectOld.GetAnnotations()[c4v1alpha1.AnnotationRetry] !=
				e.ObjectNew.GetAnnotations()[c4v1alpha1.AnnotationRetry]
		},
	}
	return builder.WithPredicates(predicate.Or(predicate.GenerationChangedPredicate{}, retryChanged))
}

// sessionPhaseChanges ignores heartbeats: a Session matters to its ancestors only
// when it appears, disappears or changes phase.
func sessionPhaseChanges() builder.WatchesOption {
	return builder.WithPredicates(predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldSession, ok1 := e.ObjectOld.(*c4v1alpha1.Session)
			newSession, ok2 := e.ObjectNew.(*c4v1alpha1.Session)
			return !ok1 || !ok2 || oldSession.Status.Phase != newSession.Status.Phase
		},
	})
}

// specChanged passes creations, deletions and spec changes, but not status or
// metadata writes such as adoption: resolving a reference reads only spec.
func specChanged() builder.WatchesOption {
	return builder.WithPredicates(predicate.GenerationChangedPredicate{})
}

// containerPhaseChanges passes a Container's creation, deletion and phase
// changes: all the Project roll-up reads from it.
func containerPhaseChanges() builder.WatchesOption {
	return builder.WithPredicates(predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldContainer, ok1 := e.ObjectOld.(*c4v1alpha1.Container)
			newContainer, ok2 := e.ObjectNew.(*c4v1alpha1.Container)
			return !ok1 || !ok2 || oldContainer.Status.Phase != newContainer.Status.Phase
		},
	})
}
