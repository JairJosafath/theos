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
	"context"
	"fmt"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// briefPrompts returns the Brief's prompts where its status counts them:
// every prompt written, the retired ones empty (model.PlacedPrompts).
func briefPrompts(brief *c4v1alpha1.Brief) []string {
	return model.PlacedPrompts(int(brief.Spec.PromptsRetired), brief.Spec.Prompts)
}

// takenPrompts returns the prompts the running or last round took that the
// last applied round hadn't planned.
func takenPrompts(brief *c4v1alpha1.Brief) []string {
	prompts := briefPrompts(brief)
	return model.RoundPrompts(prompts[:min(max(int(brief.Status.RoundPrompts), 0), len(prompts))],
		int(brief.Status.PromptsPlanned))
}

// retirePrompts takes the oldest planned prompts out of spec.prompts, past
// the newest model.KeptPlannedPrompts, and counts them in
// spec.promptsRetired, so the list never fills up (#835). The patch carries
// the resourceVersion read, so a prompt appended meanwhile is never lost:
// a conflict says the Brief is stale.
func (r *BriefReconciler) retirePrompts(ctx context.Context, brief *c4v1alpha1.Brief) error {
	n := model.PromptsToRetire(int(brief.Spec.PromptsRetired), len(brief.Spec.Prompts),
		int(brief.Status.PromptsPlanned))
	if n == 0 {
		return nil
	}
	orig := brief.DeepCopy()
	brief.Spec.Prompts = slices.Clone(brief.Spec.Prompts[n:])
	brief.Spec.PromptsRetired += int32(n)
	if err := r.Patch(ctx, brief, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})); err != nil {
		if apierrors.IsConflict(err) {
			return errStaleBrief
		}
		return fmt.Errorf("retiring planned prompts: %w", err)
	}
	logf.FromContext(ctx).Info("Retired planned prompts", "count", n, "retired", brief.Spec.PromptsRetired)
	return nil
}
