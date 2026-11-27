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
	"errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// The operator's half of a merge on the engine's Task tick (#336, slice 6):
// the engine reads the approved pull request, decides on it, ends the
// judging run and the Task; the reconciler holds the merge (a Benchmark's
// Task, a cut release's tag, a gated merge whose approving run ended),
// merges as each Agent's own account (merge's mergeReviewed), and records and
// files what the merge leaves (the merge commit, the head's checks carried
// onto it, the pull request's size, the follow-ups and the by-hand issue, a
// spec's criteria), as apply's merge does. What a merge or a branch's update
// says in the status is the reconciler's, after the tick (mergeCarried).

// failure is the step that fails a Task as e says.
func failure(e model.TaskEnd) *model.TaskStep {
	return &model.TaskStep{Action: model.ActionFail, Reason: e.Reason, Message: e.Message}
}

// HoldMerge holds the merge where merge holds it, before the pull request is
// read: a Benchmark's Task keeps the review's verdict and ends Approved (the
// engine's, engine.TaskInput.Benchmark); a Crew's Task waits for its cut
// release's tag (waitForRelease); and a Standard change whose approving
// review run ended while its updated branch was checked fails, as the gated
// merge needs a run that lasts. Otherwise the review's verdict goes into the
// status, and its run's result is kept for the follow-ups Merged files.
func (t *taskTurn) HoldMerge(ctx context.Context, _ model.TaskFacts, _ model.TaskStep) (*model.TaskStep, error) {
	r, task, status := t.r, t.task, t.status
	if task.Spec.Benchmark != nil {
		status.Review = r.reviewFrom(ctx, task, status)
		return nil, nil
	}
	provider, repo, err := r.gitRepo(ctx, t.container)
	if err != nil {
		return nil, err
	}
	hold, err := r.waitForRelease(ctx, task, status, provider, repo)
	if err != nil {
		return nil, err
	}
	if hold != "" {
		return &model.TaskStep{Action: model.ActionWait, Reason: model.ReasonWaitingForRelease, Recheck: true,
			Message: hold}, nil
	}
	reviewRun := r.reviewRun(ctx, task, status)
	if len(task.Spec.AllowedProtectedPaths) > 0 && status.MergeWhenChecksPass && reviewRun == nil {
		// The model reviews such a change again (ApprovalEnded); never merge it
		// without the approval of a review run that lasts.
		return failure(model.GatedMergeRefused(
			"the review run that approved it ended while CI ran again with main merged in")), nil
	}
	status.Review = r.reviewFrom(ctx, task, status)
	t.reviewRun = reviewRun
	return nil, nil
}

// Merge squashes the reviewed commit of pr into main, as mergeReviewed
// does: the Agents' approvals posted first, a Standard change through the
// gated merge, which may wait for CI on the reviewed commit or refuse it,
// and main's push run skipped where the head's checks stand for the merge's
// (planSkip, kept for Merged).
func (t *taskTurn) Merge(ctx context.Context, _ model.TaskFacts, pr ports.PullRequest) (*model.TaskStep, error) {
	r, task, status := t.r, t.task, t.status
	provider, repo, err := r.gitRepo(ctx, t.container)
	if err != nil {
		return nil, err
	}
	refusal, skip, err := r.mergeReviewed(ctx, provider, repo, task, status, t.container, t.agents, pr.Behind)
	pending := errors.Is(err, errChecksPending)
	if err != nil && !pending {
		return nil, err
	}
	switch model.AfterSquash(pending, refusal) {
	case model.SquashWaitForChecks:
		// Waits while the review run lasts; if it ends first, the merge is refused.
		return &model.TaskStep{Action: model.ActionWait, Reason: reasonWaitingForChecks, Recheck: true,
			Message: "Approved; CI runs again on " + status.Commit + ", merging once it passes"}, nil
	case model.SquashRefused:
		return failure(model.GatedMergeRefused(refusal)), nil
	}
	t.skip = skip
	return nil, nil
}

// reasonWaitingForChecks is the Progressing reason of a gated merge that
// waits for CI on the reviewed commit.
const reasonWaitingForChecks = "WaitingForChecks"

// Merged records what the merge of pr leaves, by theos (squashed) or by a
// person, as merge does: the merge commit, and the head's checks carried
// onto it where its push run was skipped; the pull request's size and its
// person commits; and a spec's criteria copied into its issue, or the
// review's follow-ups and open criteria filed and the issue closed.
func (t *taskTurn) Merged(ctx context.Context, _ model.TaskFacts, pr ports.PullRequest, squashed bool) error {
	r, task, status := t.r, t.task, t.status
	provider, repo, err := r.gitRepo(ctx, t.container)
	if err != nil {
		return err
	}
	status.MergeCommit = pr.MergeCommit
	if squashed {
		status.MergeCommit = r.mergeCommit(ctx, provider, repo, status)
		r.carryChecks(ctx, provider, repo, status, t.skip)
	}
	r.recordPull(ctx, provider, repo, status)
	if isSpec(task) {
		return r.specMerged(ctx, provider, repo, task, status)
	}
	if task.Spec.Issue > 0 {
		followUps, open, openBy := mergeFindings(task, status, t.agents, t.reviewRun)
		status.FollowUps = r.closeIssue(ctx, provider, repo, task, status, t.agents.reviewer, openBy, followUps, open)
	}
	return nil
}

// mergeCarried says in the status what a merge or a branch's update the tick
// carried out did (step is the tick's result), as apply's merge and
// updateBranch say it: a failure's issue goes back to a person; a conflict
// with main goes back to the implementer (turnEnded); a wait (a release's
// tag, CI on the reviewed commit, a branch the host has not seen take in
// main) says why and looks again; an updated branch waits for its checks
// afresh, their wait counted from now; a branch moved after the review is
// checked again; a merge, or a Benchmark's approval, is logged.
func (t *taskTurn) mergeCarried(ctx context.Context, step model.TaskStep) {
	if !t.merges || t.handBack(ctx) {
		return
	}
	r, task, status := t.r, t.task, t.status
	log := logf.FromContext(ctx)
	recheck := ctrl.Result{RequeueAfter: hostRecheck(ctx, r.checksRecheck())}
	switch step.Action {
	case model.ActionResolveConflicts:
		r.turnEnded(ctx, task, status, step)
	case model.ActionWait:
		if step.Reason != "" {
			r.progress(task, status, metav1.ConditionTrue, step.Reason, step.Message)
		}
		t.result = recheck
	case model.ActionUpdateBranch:
		// The checks' wait starts again on the new head, also when main moved
		// again while the Task was already waiting for checks.
		now := metav1.NewTime(r.now())
		status.PhaseChangedAt = &now
		r.progress(task, status, metav1.ConditionTrue, "UpdatingBranch", step.Message)
		log.Info("Merged main into an approved Task's branch", "round", status.Round, "commit", status.Commit)
		t.result = recheck
	case model.ActionMerge:
		switch status.Phase {
		case c4v1beta1.TaskMerged:
			log.Info("Merged Task", "url", status.PullRequestURL, "round", status.Round)
		case c4v1beta1.TaskApproved:
			log.Info("Approved Benchmark Task", "url", status.PullRequestURL, "round", status.Round)
		case c4v1beta1.TaskChecking:
			r.progress(task, status, metav1.ConditionTrue, "BranchMoved",
				"The branch moved after the review; checking "+status.Commit)
			t.result = recheck
		}
	}
}
