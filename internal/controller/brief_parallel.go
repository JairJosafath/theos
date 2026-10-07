/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// How much a Brief's Crews build at once (docs/plans/brief-parallel.md): the
// Brief owns the parallel of every Crew it made.

// briefParallel is how many issues the Brief's Crews work on at once.
func briefParallel(brief *c4v1alpha1.Brief) int32 {
	return model.BriefParallel(brief.Spec.Parallel, 0)
}

// syncParallel sets the parallel of every Crew the Brief made, earlier rounds'
// included, whatever its round is doing (decision 1): a change to the Brief
// reaches them without waiting for a round, and a hand edit of one is set
// back. A Crew the Brief didn't make is never touched.
func (r *BriefReconciler) syncParallel(ctx context.Context, brief *c4v1alpha1.Brief) error {
	var crews c4v1alpha1.CrewList
	if err := r.List(ctx, &crews, client.InNamespace(brief.Namespace),
		client.MatchingLabels{c4v1alpha1.LabelBrief: brief.Name}); err != nil {
		return err
	}
	parallel := briefParallel(brief)
	for i := range crews.Items {
		crew := &crews.Items[i]
		if crew.Spec.Parallel == parallel {
			continue
		}
		orig := crew.DeepCopy()
		crew.Spec.Parallel = parallel
		if err := r.Patch(ctx, crew, client.MergeFrom(orig)); err != nil {
			return fmt.Errorf("setting the parallel of Crew %s: %w", crew.Name, err)
		}
		logf.FromContext(ctx).Info("Set the Crew's parallel from the Brief", "crew", crew.Name,
			"from", orig.Spec.Parallel, "parallel", parallel)
	}
	return nil
}
