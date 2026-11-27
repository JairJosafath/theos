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

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/engine"
	"github.com/JairJosafath/theos/internal/model"
)

// The operator's half of a Task's own ends and the Standard change on the
// engine's Task tick (#336, slice 7): the engine ends the run and saves the
// Task Blocked, back in its round, Merged with nothing merged, or waiting
// for a person; the reconciler files the proposal as a Standard change and
// keeps it on the status (Propose), says on the issue and the pull request
// what each step says (taskTurn.ending), and says in the status what the
// step did, after the tick (changeCarried), as apply's propose, resume,
// doneByStandardChange, nothingToDo and applySpec do.

// Propose files the proposal the implementer's run reports as a Standard
// change, as propose does (fileProposal), and keeps the change on the
// status, which the engine reads back before it blocks the Task. A refused
// proposal fails the Task, in ending's words; a Task whose issue a person
// closed during the round ends, IssueClosed, with nothing more said.
func (t *taskTurn) Propose(ctx context.Context, facts model.TaskFacts, _ model.TaskStep) (engine.Proposed, error) {
	filed, err := t.r.fileProposal(ctx, t.task, t.status, t.container, facts.Run)
	switch {
	case err != nil:
		return engine.Proposed{}, err
	case filed.refusal != "":
		return engine.Proposed{Instead: failure(model.TaskEnd{Phase: model.TaskFailed, Reason: model.ReasonFailed,
			Message: filed.refusal})}, nil
	case filed.closed != nil:
		return engine.Proposed{End: filed.closed}, nil
	}
	t.status.StandardChange = filed.change
	return engine.Proposed{}, nil
}

// changeCarried says in the status what a step of changeStep the tick
// carried out did (step is the tick's result): a failure's issue goes back
// to a person (a refused proposal, a declined spec); a Task Blocked on the
// Standard change it filed waits for it; a resumed Task goes on with its
// round; a Task done by its change, or with nothing to do, is logged; and a
// pull request put up for a person waits, and looks again.
func (t *taskTurn) changeCarried(ctx context.Context, step model.TaskStep) {
	if !t.changes || t.handBack(ctx) || step.Action != t.step.Action {
		return
	}
	r, task, status := t.r, t.task, t.status
	switch step.Action {
	case model.ActionPropose:
		if status.Phase == c4v1beta1.TaskBlocked {
			r.proposed(ctx, task, status, step)
		}
	case model.ActionResume:
		r.resumed(ctx, task, status, step)
	case model.ActionDoneByStandardChange:
		logDoneByChange(ctx, status)
	case model.ActionNothingToDo:
		logNothing(ctx, task, step)
	case model.ActionAwaitApproval:
		t.result = r.awaitingPerson(ctx, task, status, step)
	}
}
