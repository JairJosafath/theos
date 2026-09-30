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
