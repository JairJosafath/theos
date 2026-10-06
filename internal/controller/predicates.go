/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// taskPhaseChanges passes a Task's creation, deletion and phase changes: a Task
// finishing lets the next one on its Container start.
func taskPhaseChanges() builder.Predicates {
	return builder.WithPredicates(predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldTask, ok1 := e.ObjectOld.(*c4v1alpha1.Task)
			newTask, ok2 := e.ObjectNew.(*c4v1alpha1.Task)
			return !ok1 || !ok2 || oldTask.Status.Phase != newTask.Status.Phase
		},
	})
}

// podFinished passes agent pods only, and of their updates only the ones that
// finish them: a sandbox's result is read once its pod is done.
func podFinished() builder.WatchesOption {
	isAgent := func(obj client.Object) bool { return obj.GetLabels()[LabelTask] != "" }
	return builder.WithPredicates(predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return isAgent(e.Object) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return isAgent(e.Object) },
		GenericFunc: func(e event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldPod, ok1 := e.ObjectOld.(*corev1.Pod)
			newPod, ok2 := e.ObjectNew.(*corev1.Pod)
			return ok1 && ok2 && isAgent(newPod) && oldPod.Status.Phase != newPod.Status.Phase
		},
	})
}
