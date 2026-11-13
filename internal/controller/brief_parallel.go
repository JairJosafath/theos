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
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// How much a Brief's Crews build at once (docs/plans/done/brief-parallel.md): the
// Brief owns the parallel of every Crew it made.

// briefParallel is how many issues the Brief's Crews work on at once.
func briefParallel(brief *c4v1beta1.Brief) int32 {
	return model.BriefParallel(brief.Spec.Parallel, brief.Status.Parallel)
}

// applyParallel records the number the round's plan relays from a prompt in
// status.parallel (decision 4), before the round makes its Crews, and sets
// the Crews the Brief made already. spec.parallel still wins over it.
func (r *BriefReconciler) applyParallel(ctx context.Context, brief *c4v1beta1.Brief, plan model.Plan) error {
	if plan.Parallel == nil {
		return nil
	}
	status := &brief.Status
	if status.Parallel != plan.Parallel.N || status.ParallelRound != status.Round {
		status.Parallel, status.ParallelRound = plan.Parallel.N, status.Round
		logf.FromContext(ctx).Info("Recorded the parallel a prompt asked for", "round", status.Round,
			"parallel", plan.Parallel.N, "why", plan.Parallel.Why)
	}
	return r.syncParallel(ctx, brief)
}

// syncParallel sets the parallel and the release cadence of every Crew the
// Brief made, earlier rounds' included, whatever its round is doing (decision
// 1; docs/plans/done/release-cadence.md, C2): a change to the Brief reaches them
// without waiting for a round, and a hand edit of one is set back. A Crew the
// Brief didn't make is never touched.
func (r *BriefReconciler) syncParallel(ctx context.Context, brief *c4v1beta1.Brief) error {
	var crews c4v1beta1.CrewList
	if err := r.List(ctx, &crews, client.InNamespace(brief.Namespace),
		client.MatchingLabels{c4v1beta1.LabelBrief: brief.Name}); err != nil {
		return err
	}
	parallel, cadence := briefParallel(brief), briefCadence(brief)
	for i := range crews.Items {
		crew := &crews.Items[i]
		if crew.Spec.Parallel == parallel && equality.Semantic.DeepEqual(crew.Spec.ReleaseCadence, cadence) {
			continue
		}
		orig := crew.DeepCopy()
		crew.Spec.Parallel, crew.Spec.ReleaseCadence = parallel, cadence.DeepCopy()
		if err := r.Patch(ctx, crew, client.MergeFrom(orig)); err != nil {
			return fmt.Errorf("setting the parallel and release cadence of Crew %s: %w", crew.Name, err)
		}
		if orig.Spec.Parallel != parallel {
			logf.FromContext(ctx).Info("Set the Crew's parallel from the Brief", "crew", crew.Name,
				"from", orig.Spec.Parallel, "parallel", parallel)
		}
		if !equality.Semantic.DeepEqual(orig.Spec.ReleaseCadence, cadence) {
			logf.FromContext(ctx).Info("Set the Crew's release cadence from the Brief", "crew", crew.Name,
				"every", cadence.Every, "at", cadence.At, "timeZone", cadence.TimeZone)
		}
	}
	return nil
}
