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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/engine"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// A Task's reconcile on the engine's Task tick (docs/plans/lead-runtime.md,
// step 12; #336). The tick reads the Task's record from the reconcile's
// status (statusStore), its run through the Task's Workers (sandboxWorkers),
// and the rest of its facts from the reconciler (TaskReconciler.facts); it
// decides, offers a turn through the Workers, and ends one and moves the
// Task on (engine.TurnEnd: a verification, a round not accepted, a review or
// verification tried again, an interrupted run run again, a conflict to
// resolve), what the end says on the pull request the reconciler's
// (taskTurn.ending). Every other step the reconciler carries out itself
// (apply), as before, until later slices of #336 move them. The status is
// written once, after the tick.

// taskTurn is one reconcile of a Task on the engine's Task tick: the
// ports.Runtime the tick runs on, and what the reconcile keeps between the
// tick's calls.
type taskTurn struct {
	r         *TaskReconciler
	task      *c4v1beta1.Task
	status    *c4v1beta1.TaskStatus
	container *c4v1beta1.Container
	agents    taskAgents
	workers   *sandboxWorkers

	// step is the step decided, facts what it was decided on, and result
	// when the reconcile looks again.
	step   model.TaskStep
	facts  model.TaskFacts
	result ctrl.Result
	// endsTurn says the engine carries out step, a turn's end, whose
	// Progressing reason the reconcile writes after the tick (ended).
	endsTurn bool
	// in and envs are the run's inputs and test environments, read by
	// prepare for the Sandbox the offer makes; firstIssueRun says the run
	// is the Task's first on its issue, which announces the Task.
	in            *runInput
	envs          []runEnvironment
	firstIssueRun bool
}

var _ ports.Runtime = (*taskTurn)(nil)

// newTaskTurn is the reconcile of task on the tick, on status as the
// reconcile leaves it.
func (r *TaskReconciler) newTaskTurn(task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents) *taskTurn {
	t := &taskTurn{r: r, task: task, status: status, container: container, agents: agents}
	t.workers = r.workers(task, status, t.build)
	t.workers.prepare = t.prepare
	return t
}

func (t *taskTurn) Store() ports.TaskStore {
	return &statusStore{r: t.r, task: t.task, status: t.status}
}

func (t *taskTurn) Workers() ports.Workers { return t.workers }

func (t *taskTurn) Capabilities() ports.Capabilities { return operatorCapabilities }

// input is the tick's input: the reconciler reads the facts, carries out
// every step but a turn's offer, end and restart, and says what a turn's end
// says (ending).
func (t *taskTurn) input(ctx context.Context) engine.TaskInput {
	in := engine.TaskInput{Task: t.task.Name, Container: t.container.Name, Issue: int(t.task.Spec.Issue),
		Title: t.status.IssueTitle, Goal: t.task.Spec.Goal, Spec: isSpec(t.task),
		Implementer: t.agents.implementer.Name, Reviewer: t.agents.reviewer.Name,
		MaxRounds: int(t.agents.implementer.Spec.MaxRounds), Now: t.r.now(), Log: logf.FromContext(ctx),
		Facts: t.readFacts, Carry: t.carry, Ending: t.ending}
	if t.agents.verifier != nil {
		in.Verifier = t.agents.verifier.Name
	}
	return in
}

// readFacts reads the Task's facts as the reconciler does, with the run the
// tick read through the Workers.
func (t *taskTurn) readFacts(ctx context.Context, facts model.TaskFacts) (model.TaskFacts, error) {
	read, err := t.r.facts(ctx, t.task, t.status, t.container, t.agents.implementer, facts.Run)
	t.facts = read
	return read, err
}

// offers reports whether the engine carries out action: a turn's start,
// which it offers through the Workers.
func offers(action model.TaskAction) bool {
	switch action {
	case model.ActionStartImplement, model.ActionStartReview, model.ActionStartVerify:
		return true
	}
	return false
}

// carry carries out step, unless it is a turn's start, or a turn's end the
// engine ends as the reconciler would (endsAsTheEngine), which it leaves to
// the engine. It brings the run's pause up to date first, as the step may
// wait on it.
func (t *taskTurn) carry(ctx context.Context, facts model.TaskFacts, step model.TaskStep) (bool, error) {
	t.step = step
	status := t.status
	logf.FromContext(ctx).V(1).Info("Took Task step", "phase", status.Phase, "round", status.Round,
		"action", step.Action, "message", step.Message)
	if role := runRole(status.Phase); role != "" {
		if err := t.r.syncPause(ctx, t.task, status, sandboxName(t.task, status, role), facts); err != nil {
			return true, err
		}
	}
	if offers(step.Action) {
		return false, nil
	}
	if engine.TurnEnd(step.Action) && t.endsAsTheEngine() {
		t.endsTurn = true
		return false, nil
	}
	result, err := t.r.apply(ctx, t.task, status, t.container, t.agents, step)
	t.result = result
	return true, err
}

// endsAsTheEngine reports whether the engine ends the phase's run as the
// reconciler's endRun would: the turn the engine ends (the phase's role, in
// the Sandbox the status's counters name) is the status's current Sandbox,
// or the phase has no run and the status names no Sandbox. A status that
// forgot its Sandbox, or names another, is left to apply, whose endRun ends
// whatever the status names, as it always has.
func (t *taskTurn) endsAsTheEngine() bool {
	role := runRole(t.status.Phase)
	if role == "" {
		return t.status.Sandbox == ""
	}
	return t.status.Sandbox != "" && t.status.Sandbox == sandboxName(t.task, t.status, role)
}

// ending is the reconciler's half of a turn's end the engine carries out
// (engine.TaskInput.Ending), before the Workers end the run: what the step
// says on the pull request, as each Agent's own account, and the verdict the
// status keeps once the review's run is gone. A round no agent judged drops
// an earlier round's verdict first, as apply does.
func (t *taskTurn) ending(ctx context.Context, _ model.TaskFacts, step model.TaskStep) error {
	r, task, status, container := t.r, t.task, t.status, t.container
	if step.Unreviewed {
		status.Review, status.Verification = nil, nil
	}
	switch step.Action {
	case model.ActionVerify:
		r.approveForVerify(ctx, task, status, container, t.agents)
	case model.ActionRetry:
		if review := r.sayNotAccepted(ctx, task, status, container, t.agents, step); review != nil {
			status.Review = review
		}
	case model.ActionResolveConflicts:
		status.Review = r.sayConflicts(ctx, task, status, container, step)
	}
	return nil
}

// ended says in the status that the tick ended a turn and moved the Task on
// (step is the tick's result): the Progressing reason the step writes, as
// apply's own turn ends write it (turnEnded). The engine saved the progress;
// the status write after the tick triggers the next step, and a restarted
// turn is offered then, readied as any other (ports.Preparer).
func (t *taskTurn) ended(ctx context.Context, step model.TaskStep) {
	if !t.endsTurn || step.Action != t.step.Action {
		return
	}
	t.r.turnEnded(ctx, t.task, t.status, step)
}

// prepare readies a run before its Sandbox, as the reconciler always has: the
// Agent's credentials and the Container's agent Git access, the Standard on
// main and the issue open (runInputs); an issue's first run continues an
// earlier try's open pull request, or starts at its review when its commit
// is green and unreviewed; the test environments the run needs are leased,
// under the name the Sandbox will have; and a Benchmark's branch starts at
// its pinned base. Not ready, the status says why and result when to look
// again.
func (t *taskTurn) prepare(ctx context.Context, order ports.WorkOrder, name string) (bool, error) {
	r, task, status, role := t.r, t.task, t.status, order.Role
	in, result, err := r.runInputs(ctx, task, status, t.container, t.agents, role)
	if in == nil {
		t.result = result
		return false, err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	t.firstIssueRun = task.Spec.Issue > 0 && model.FirstRun(progressOf(status))
	if t.firstIssueRun {
		commit, err := r.continueEarlierTry(gitCtx, in.provider, in.repo, task, status)
		if err != nil {
			return false, err
		}
		if commit != "" {
			r.startAtReview(ctx, task, status, commit) // the status write triggers the next step
			return false, nil
		}
	}
	// The run's environments are leased before its sandbox, under the name
	// the sandbox will have: a run waiting for one starts nothing.
	envs, wait, err := r.leaseEnvironments(ctx, task, status, t.container, role, name, in.labels)
	if err != nil {
		return false, err
	}
	if wait != nil {
		t.result = r.waitForEnvironment(task, status, wait)
		return false, nil
	}
	if status.Branch == "" {
		status.Branch = model.TaskBranch(task.Name)
	}
	if b := task.Spec.Benchmark; b != nil && role == model.RoleImplementer {
		// A Benchmark's Task branches from its pinned base: the implementer
		// works on the branch it finds.
		if err := ensureBranchAt(gitCtx, in.provider, in.repo, status.Branch, b.Base); err != nil {
			return false, err
		}
	}
	t.in, t.envs = in, envs
	return true, nil
}

// build is the Sandbox of an offered turn, from the inputs prepare read.
func (t *taskTurn) build(ctx context.Context, order ports.WorkOrder, name string) (sandboxRun, error) {
	if t.in == nil {
		return sandboxRun{}, fmt.Errorf("the turn of sandbox %s was offered before it was readied", name)
	}
	return t.r.sandboxRunOf(ctx, t.task, t.status, t.container, t.agents, order.Role, name, t.in, t.envs)
}

// started says in the status that the tick offered a turn (step is the tick's
// result): its Sandbox runs. The Sandbox that starts the Task's first run
// announces the Task on its issue, and a try that continues an earlier one
// says so on the pull request; an offer that found its Sandbox made says
// nothing, as the status write after the Sandbox's making failed, and the
// comments are there (#906).
func (t *taskTurn) started(ctx context.Context, step model.TaskStep) {
	if !offers(t.step.Action) || step.Action != t.step.Action || t.status.Sandbox == "" {
		return // no offer, or one its Workers held
	}
	r, task, status := t.r, t.task, t.status
	role := startRole(t.step.Action)
	if t.workers.created == status.Sandbox && t.in != nil &&
		(t.firstIssueRun || firstReviewAtStart(status, role)) {
		gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
		defer cancel()
		if status.Continues != "" {
			r.noteContinued(gitCtx, t.in.provider, t.in.repo, task, status)
		}
		r.announce(gitCtx, t.in.provider, t.in.repo, task, t.container, t.agents)
	}
	r.progress(task, status, metav1.ConditionTrue, "Running",
		fmt.Sprintf("%s in sandbox %s", t.step.Message, status.Sandbox))
}

// statusStore is the operator's TaskStore for one Task's reconcile: the
// Task's status as the reconcile leaves it, in memory, which the reconciler
// writes once, after the tick (writeStatus). writeStatus also stamps when the
// phase changed, so the record's PhaseSince is read and never written.
type statusStore struct {
	r      *TaskReconciler
	task   *c4v1beta1.Task
	status *c4v1beta1.TaskStatus
}

var _ ports.TaskStore = (*statusStore)(nil)

// errOneTask is a statusStore's refusal of what a Crew's store does.
var errOneTask = errors.New("the store of one Task's reconcile keeps that Task alone; a Crew's store lists and starts Tasks")

// Tasks is refused: the Crew's store (crewTaskStore) lists a Container's Tasks.
func (s *statusStore) Tasks(context.Context, string) ([]model.CrewTask, error) {
	return nil, errOneTask
}

// Start is refused: the Crew's store (crewTaskStore) starts Tasks.
func (s *statusStore) Start(context.Context, string, model.IssueStart) error { return errOneTask }

func (s *statusStore) own(task string) error {
	if task != s.task.Name {
		return fmt.Errorf("the store of Task %s keeps no Task %s", s.task.Name, task)
	}
	return nil
}

// Progress is the Task's record from the status.
func (s *statusStore) Progress(_ context.Context, task string) (ports.TaskRecord, error) {
	if err := s.own(task); err != nil {
		return ports.TaskRecord{}, err
	}
	return recordOf(s.status), nil
}

// SaveProgress keeps r in the status; a final phase ends the Task, as the
// reconciler ends one (end).
func (s *statusStore) SaveProgress(_ context.Context, task string, r ports.TaskRecord) error {
	if err := s.own(task); err != nil {
		return err
	}
	setRecord(s.status, r)
	if p := r.Progress.Phase; p == model.TaskMerged || p == model.TaskFailed {
		s.r.end(s.task, s.status, model.TaskEnd{Phase: p, Reason: r.Reason, Message: r.Message})
	}
	return nil
}

// recordOf is the TaskRecord a Task's status holds.
func recordOf(status *c4v1beta1.TaskStatus) ports.TaskRecord {
	rec := ports.TaskRecord{Progress: progressOf(status), PullRequest: int(status.PullRequest),
		PullRequestURL: status.PullRequestURL, Summary: status.Summary}
	if rec.Progress.Phase == "" {
		rec.Progress.Phase = model.TaskPending
	}
	if status.PhaseChangedAt != nil {
		rec.PhaseSince = status.PhaseChangedAt.Time
	}
	return rec
}

// setRecord copies r into a Task's status, but when its phase changed.
func setRecord(status *c4v1beta1.TaskStatus, r ports.TaskRecord) {
	setProgress(status, r.Progress)
	status.PullRequest, status.PullRequestURL = int32(r.PullRequest), r.PullRequestURL
	status.Summary = r.Summary
}
