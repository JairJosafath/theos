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
	"errors"

	"github.com/JairJosafath/theos/internal/model"
)

// A Task's own ends and the Standard change on the Task tick (#336, slice
// 7): a proposal filed as a Standard change, which blocks the Task until it
// merges (Propose), the Task back in its round once it merged (Resume), a
// resumed Task its change left nothing to do ended done
// (DoneByStandardChange), an issue that needs no change closed with the
// agent's words (NothingToDo), and a pull request put up for a person's
// approval (AwaitApproval). The Standard change is a runtime's that files it
// (Proposer); what each step says is the runtime's (TaskInput.Ending) or the
// engine's own.

// Proposer is a runtime's half of a Standard change (TaskInput.Proposer),
// for a runtime that files one: the operator files the implementer's
// proposal as a Standard change issue of its own, which blocks the Task's
// issue, and keeps the change on the Task's status (its issue, paths and
// proposal branch, and how many the Task filed), which the facts it reads
// (TaskInput.Facts) give back as model.ChangeFacts while the Task is Blocked
// (#336). The engine ends the implementer's run and saves the Task Blocked
// (model.Proposed); then, with the change merged, it resumes the round
// (model.ResumeRound), and a resumed round its change left nothing to do
// ends Merged. Without a Proposer those steps wait, not built.
type Proposer interface {
	// Propose files the proposal the implementer's done run reports
	// (facts.Run), and keeps the change's record in the runtime's store,
	// from which the engine reads the record back. It returns what is done
	// in the proposal's place, when the Task does not block on it.
	Propose(ctx context.Context, facts model.TaskFacts, step model.TaskStep) (Proposed, error)
}

// Proposed is what a Proposer did in place of filing the proposal; the zero
// value filed it.
type Proposed struct {
	// Instead is a step carried out in the proposal's place: a failure, the
	// proposal refused (model.ProposeRefusal).
	Instead *model.TaskStep
	// End ends the run and the Task as it says, with nothing more said: its
	// issue closed during the round, so nobody waits for a change
	// (model.IssueClosed).
	End *model.TaskEnd
}

// files reports whether the runtime files Standard changes.
func (t *taskTick) files() bool { return t.in.Proposer != nil }

// propose files the implementer's proposal through the runtime's Proposer,
// ends its run and blocks the Task on the change (model.Proposed), the
// round's summary kept; the Proposer may refuse it or end the Task instead.
func (t *taskTick) propose(ctx context.Context, facts model.TaskFacts, step model.TaskStep,
	role string) (model.TaskStep, error) {
	if !t.files() {
		return notBuilt(step), nil
	}
	res := facts.Run.Result
	if res == nil || res.Proposal == nil {
		return step, errors.New("the implementer's run reported no proposal")
	}
	out, err := t.in.Proposer.Propose(ctx, facts, step)
	if err != nil {
		return step, err
	}
	if out.Instead != nil {
		return t.carry(ctx, facts, *out.Instead)
	}
	// The runtime kept the change in its store: the record is read back, so
	// the progress saved is the one it keeps (a new change is resumed in no
	// round yet).
	if err := t.reread(ctx); err != nil {
		return step, err
	}
	if out.End != nil {
		return step, t.end(ctx, role, *out.End)
	}
	t.rec.Summary = res.Summary
	return step, t.next(ctx, role, model.Proposed(t.rec.Progress, res.Commit))
}

// resumeChange puts a Task whose Standard change merged back to work in the
// round it was in, with what the step tells its implementer
// (model.ResumeRound): what it says on the issue is the runtime's
// (TaskInput.Ending). A Blocked Task has no run to end.
func (t *taskTick) resumeChange(ctx context.Context, facts model.TaskFacts, step model.TaskStep) (model.TaskStep, error) {
	if !t.files() {
		return notBuilt(step), nil
	}
	if err := t.ending(ctx, facts, step, nil); err != nil {
		return step, err
	}
	if err := t.reread(ctx); err != nil {
		return step, err
	}
	return step, t.save(ctx, model.ResumeRound(t.rec.Progress, step.Feedback))
}

// doneByChange ends a resumed Task whose branch has nothing beyond main, as
// its Standard change did the whole issue: Merged, with no merge of its own
// (model.ReasonDoneByStandardChange). What it closes and says is the
// runtime's (TaskInput.Ending): an earlier round's pull request, the
// proposal's branch, and the issue, naming the change.
func (t *taskTick) doneByChange(ctx context.Context, facts model.TaskFacts, step model.TaskStep,
	role string) (model.TaskStep, error) {
	if !t.files() {
		return notBuilt(step), nil
	}
	if err := t.ending(ctx, facts, step, nil); err != nil {
		return step, err
	}
	if err := t.reread(ctx); err != nil {
		return step, err
	}
	return step, t.end(ctx, role, model.TaskEnd{Phase: model.TaskMerged, Reason: model.ReasonDoneByStandardChange,
		Message: step.Message})
}

// nothingToDo ends a Task whose agent found its issue needs no change: Merged,
// reason model.ReasonNothingToDo, with nothing merged, the agent's reason
// kept as the Task's summary. Its issue closes with the agent's words: the
// runtime's (TaskInput.Ending), else the engine's own (closeNothing).
func (t *taskTick) nothingToDo(ctx context.Context, facts model.TaskFacts, step model.TaskStep,
	role string) (model.TaskStep, error) {
	if err := t.ending(ctx, facts, step, func() { t.closeNothing(ctx, step.Feedback) }); err != nil {
		return step, err
	}
	if err := t.reread(ctx); err != nil {
		return step, err
	}
	t.rec.Summary = model.Clip(step.Feedback, maxSummary)
	return step, t.end(ctx, role, model.TaskEnd{Phase: model.TaskMerged, Reason: model.ReasonNothingToDo,
		Message: step.Message})
}

// maxSummary is the most of an agent's words a Task's record keeps as its
// summary.
const maxSummary = 2000

// closeNothing closes the Task's issue with the agent's reason and how to
// reopen it (model.NothingComment), out of status/in-progress. The Task ends
// done whether or not the host takes each write, as a merged Task's issue
// close is best effort.
func (t *taskTick) closeNothing(ctx context.Context, note string) {
	if t.in.Issue == 0 {
		return
	}
	hostCtx, cancel := context.WithTimeout(ctx, t.in.hostTimeout())
	defer cancel()
	n, repo := t.in.Issue, t.in.Repo
	err := errors.Join(
		t.host.CommentIssue(hostCtx, repo, n, model.NothingComment(t.in.Task, note)),
		t.host.RemoveIssueLabel(hostCtx, repo, n, model.LabelInProgress),
		t.host.CloseIssue(hostCtx, repo, n),
	)
	if err != nil {
		t.in.Log.Info("Could not close the issue the Task found nothing to do on", "issue", n,
			"error", err.Error())
	}
}

// changeStepFree reports whether the engine carries out action, a Task's own
// end or the Standard change, without the host: the change's steps are the
// runtime's, or wait not built, and the other steps' words are the runtime's
// where it says them (TaskInput.Ending).
func (t *taskTick) changeStepFree(action model.TaskAction) (free, known bool) {
	switch action {
	case model.ActionPropose:
		// A refused proposal fails the Task, in the runtime's words or the
		// engine's.
		return !t.files() || t.in.Ending != nil, true
	case model.ActionResume, model.ActionDoneByStandardChange:
		return true, true
	case model.ActionNothingToDo, model.ActionAwaitApproval:
		return t.in.Ending != nil, true
	case model.ActionSpecDeclined:
		// A failure, said as one (fail).
		return t.in.Ending != nil && !t.in.Triage, true
	}
	return false, false
}
