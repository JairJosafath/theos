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

package engine

import (
	"context"
	"fmt"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// An approved change's way into main (#336, slice 6): its branch updated
// with main when main moved, its judging run kept until the merge where the
// Workers keep runs (ports.Keeper), and its merge, the runtime's half of it
// a Merger's.

// Merger is a runtime's half of a merge (TaskInput.Merger), for a runtime
// that merges as more than one account and keeps more of a merge than the
// engine does: the operator's posts each Agent's approval as its own
// account, holds a merge for a cut release's tag, merges a Standard change
// past the protected-file rule only while its approving review run lasts,
// may skip main's push run, and records the merge commit, the pull
// request's size and the follow-ups it filed (#336). The engine reads the
// pull request, decides on it (model.DecideMerge), ends the judging run and
// the Task, and saves the record.
type Merger interface {
	// HoldMerge is asked first, before the pull request is read: a step to
	// carry out in place of the merge (a wait, which the engine returns; a
	// failure, which it carries out), else nil. It keeps what it needs of
	// the judging run, which the merge ends.
	HoldMerge(ctx context.Context, facts model.TaskFacts, step model.TaskStep) (*model.TaskStep, error)
	// Merge merges the approved pull request pr, as the engine read it: what
	// goes on it first and how it merges. A merge that waits (for CI on the
	// reviewed commit) or is refused returns the step that says so, a wait
	// or a failure, else nil once merged.
	Merge(ctx context.Context, facts model.TaskFacts, pr ports.PullRequest) (*model.TaskStep, error)
	// Merged records and files what a merge leaves, in place of the engine's
	// findings filed and issue closed: squashed says the runtime merged pr,
	// else a person did. Called before the judging run ends and the Task is
	// saved Merged, so a failure leaves the Task as it was, for the next
	// tick to find the pull request merged and come back here.
	Merged(ctx context.Context, facts model.TaskFacts, pr ports.PullRequest, squashed bool) error
}

// endRole is the role whose run a step of the Task's phase ends: the
// phase's own (RunRole), else, in a phase that runs none, the judging run
// Workers that keep one past its turn keep (ports.Keeper.Kept).
func (t *taskTick) endRole(ctx context.Context, facts model.TaskFacts) (string, error) {
	if role := t.in.RunRole(facts.Phase); role != "" {
		return role, nil
	}
	keeper, ok := t.rt.Workers().(ports.Keeper)
	if !ok {
		return "", nil
	}
	return keeper.Kept(ctx, t.in.Task, facts.Round)
}

// updateBranch merges main into an approved change's branch, and waits for
// the checks on the new head, their wait counted from now; a conflict sends
// it back to its implementer. What it says on the pull request is the
// runtime's (TaskInput.Ending) or the engine's comment. The judging run
// (role's) is kept for the merge where the Workers keep one (ports.Keeper),
// and ends here elsewhere.
func (t *taskTick) updateBranch(ctx context.Context, facts model.TaskFacts, step model.TaskStep,
	role string) (model.TaskStep, error) {
	hostCtx, cancel := context.WithTimeout(ctx, t.in.hostTimeout())
	defer cancel()
	up, err := UpdateBranch(hostCtx, t.host, t.in.Repo, t.rec.PullRequest)
	if err != nil {
		return step, &HostError{Err: err}
	}
	if up.Conflict {
		conflict := model.ConflictStep(facts.Round)
		return t.carry(ctx, facts, conflict)
	}
	if up.Head == t.rec.Progress.Commit {
		return model.TaskStep{Action: model.ActionWait, Message: step.Message}, nil // the host hasn't seen it yet
	}
	if err := t.ending(ctx, facts, step, func() { t.comment(ctx, step.Message+".") }); err != nil {
		return step, err
	}
	if _, keeps := t.rt.Workers().(ports.Keeper); !keeps && role != "" {
		if err := t.rt.Workers().End(ctx, t.in.Task, facts.Round, role, whyDone); err != nil {
			return step, err
		}
	}
	// The checks' wait starts again on the new head, also when main moved
	// again while the Task already waited for checks.
	t.rec.PhaseSince = t.in.Now
	return step, t.save(ctx, model.UpdatedBranch(t.rec.Progress, up.Head))
}

// merge merges an approved change, or, in a runtime that merges nothing,
// waits for a person's merge and ends the Task Merged once it came. A
// Benchmark's Task ends Approved instead. The runtime's Merger, when it has
// one, holds the merge, merges and records it; a pull request closed unmerged
// declines a spec, and fails any other Task.
func (t *taskTick) merge(ctx context.Context, facts model.TaskFacts, step model.TaskStep,
	role string) (model.TaskStep, error) {
	m := t.in.Merger
	if m != nil {
		instead, err := m.HoldMerge(ctx, facts, step)
		if err != nil || instead != nil {
			return t.instead(ctx, facts, step, instead, err)
		}
		if err := t.reread(ctx); err != nil {
			return step, err
		}
	}
	if t.in.Benchmark {
		return step, t.end(ctx, role, model.BenchmarkApproved())
	}
	p := t.rec.Progress
	hostCtx, cancel := context.WithTimeout(ctx, t.in.hostTimeout())
	defer cancel()
	pr, err := t.host.GetPullRequest(hostCtx, t.in.Repo, t.rec.PullRequest)
	if err != nil {
		return step, &HostError{Err: err}
	}
	action := model.DecideMerge(model.PullFacts{Merged: pr.Merged, Closed: pr.State == stateClosed,
		HeadSHA: pr.HeadSHA}, p.Commit)
	switch action {
	case model.MergeFailClosed:
		if t.in.Spec {
			// A person closed the spec's pull request: the spec is declined.
			return t.carry(ctx, facts, model.TaskStep{Action: model.ActionSpecDeclined,
				Reason: model.ReasonSpecDeclined, Message: "The spec was declined"})
		}
		end := model.PullRequestClosed(t.rec.PullRequestURL)
		return step, t.end(ctx, role, end)
	case model.MergeRecheck:
		// Someone pushed after the review: check and review that commit instead.
		return step, t.next(ctx, role, model.Recheck(p, pr.HeadSHA))
	case model.MergeSquash:
		if !t.rt.Capabilities().Merges {
			return model.TaskStep{Action: model.ActionWait, Reason: model.ReasonPersonMerges, Message: fmt.Sprintf(
				"Approved with green checks; waiting for a person to merge %s", t.rec.PullRequestURL)}, nil
		}
		if m != nil {
			instead, err := m.Merge(ctx, facts, pr)
			if err != nil || instead != nil {
				return t.instead(ctx, facts, step, instead, err)
			}
			if err := t.reread(ctx); err != nil {
				return step, err
			}
		} else if err := t.host.MergePullRequest(hostCtx, t.in.Repo, t.rec.PullRequest, p.Commit,
			t.pullTitle(), fmt.Sprintf("Task %s, round %d.", t.in.Task, p.Round)); err != nil {
			return step, &HostError{Err: err}
		}
	}
	if m != nil {
		if err := m.Merged(ctx, facts, pr, action == model.MergeSquash); err != nil {
			return step, err
		}
		if err := t.reread(ctx); err != nil {
			return step, err
		}
	} else if err := t.merged(hostCtx); err != nil {
		return step, err
	}
	return step, t.end(ctx, role, model.TaskEnd{Phase: model.TaskMerged, Reason: "Merged", Message: step.Message})
}

// reread reads the record back from the store after the runtime's half of a
// merge, which may have changed it there (the checks a gated merge read).
func (t *taskTick) reread(ctx context.Context) error {
	rec, err := t.rt.Store().Progress(ctx, t.in.Task)
	if err != nil {
		return err
	}
	t.rec = rec
	return nil
}

// instead is a merge's outcome when the runtime's Merger held it or its
// merge did not go through: a wait waits, and any other step (a failure) is
// carried out in the merge's place.
func (t *taskTick) instead(ctx context.Context, facts model.TaskFacts, step model.TaskStep,
	instead *model.TaskStep, err error) (model.TaskStep, error) {
	switch {
	case err != nil:
		return step, err
	case instead.Action == model.ActionWait:
		return *instead, nil
	}
	return t.carry(ctx, facts, *instead)
}

// merged is what the engine files once merged, by theos or by a person: a
// spec's criteria go into its issue, which stays open, and it is ready;
// any other issue gets what the review and the verification left, and
// closes with it.
func (t *taskTick) merged(ctx context.Context) error {
	if t.in.Spec {
		if err := t.specMerged(ctx); err != nil {
			return &HostError{Err: err}
		}
		return nil
	}
	if t.in.Issue > 0 {
		t.fileFindings(ctx)
		if err := t.host.CloseIssue(ctx, t.in.Repo, t.in.Issue); err != nil {
			t.in.Log.Info("Could not close the merged Task's issue", "issue", t.in.Issue, "error", err.Error())
		}
	}
	return nil
}
