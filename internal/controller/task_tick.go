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
	"cmp"
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
// verification tried again, an interrupted or paused run run again, a
// conflict to resolve, an approval that ran out reviewed again), what the
// end says on the pull request the reconciler's (taskTurn.ending). It
// suspends and resumes a run in place and brings its pause up to date
// through the same Workers (engine.InPlace; ports.Suspender, ports.Resumer,
// ports.Pauser). It opens the round's pull request, runs failed checks
// again and fails the Task through the operator's host (taskHost, the
// pull request as the Container's agent bot), and ends a turn whose agent is
// unavailable, keeping when it is back in the record (hostStep); what these
// say is the reconciler's, before the run ends (ending), after the tick
// (carried) and after the status write (afterWrite). It updates an approved
// change's branch with main, the judging run kept through it
// (ports.Keeper), and merges it, the merge's operator-only parts the
// reconciler's (engine.Merger: HoldMerge, Merge, Merged), what it says after
// the tick (mergeCarried). Every other step the reconciler carries out
// itself (apply), as before, until later slices of #336 move them. The
// status is written once, after the tick.

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
	// Progressing reason the reconcile writes after the tick (ended);
	// inPlace that it carries out step, a run suspended or resumed in place,
	// which the reconcile says after the tick (keptInPlace); byHost that it
	// carries out step through the Task's host (hostStep), which the
	// reconcile says after the tick (carried); merges that it carries out
	// step, a merge or a branch's update (mergeStep), which the reconcile
	// says after the tick (mergeCarried).
	endsTurn, inPlace, byHost, merges bool
	// reviewRun is the approving review run's result HoldMerge read, for the
	// follow-ups Merged files, and skip what Merge planned for main's push
	// run (planSkip), which Merged carries onto the merge commit.
	reviewRun *model.RunResult
	skip      *pushSkip
	// reopened says the Task had a pull request before the step: a later
	// round's, whose description stays the first round's. failed is the
	// failure the engine carried out, said by ending, whose issue goes back
	// to a person once the Task failed (carried).
	reopened bool
	failed   *model.TaskStep
	// notes are what the step says once the status that records it is
	// written (afterWrite), as a retry after a failed write must not say it
	// twice (#906).
	notes func(context.Context)
	// in and envs are the run's inputs and test environments, read by
	// prepare for the Sandbox the offer makes; firstIssueRun says the run
	// is the Task's first on its issue, which announces the Task.
	in            *runInput
	envs          []runEnvironment
	firstIssueRun bool
}

var (
	_ ports.Runtime = (*taskTurn)(nil)
	_ engine.Merger = (*taskTurn)(nil)
)

// newTaskTurn is the reconcile of task on the tick, on status as the
// reconcile leaves it.
func (r *TaskReconciler) newTaskTurn(task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents) *taskTurn {
	t := &taskTurn{r: r, task: task, status: status, container: container, agents: agents}
	t.workers = r.workers(task, status, t.build)
	t.workers.prepare = t.prepare
	t.workers.resume = t.readyResume
	return t
}

func (t *taskTurn) Store() ports.TaskStore {
	return &statusStore{r: t.r, task: t.task, status: t.status}
}

func (t *taskTurn) Workers() ports.Workers { return t.workers }

func (t *taskTurn) Capabilities() ports.Capabilities { return operatorCapabilities }

// input is the tick's input: the reconciler reads the facts, carries out
// every step but a turn's offer, end and restart, a run kept in place and the
// steps of hostStep and mergeStep, says what a turn's end, a failure and a
// branch's update say (ending), writes the round's pull request (pull) and
// merges as the operator merges (the Merger); the host is resolved when a
// step needs it (host).
func (t *taskTurn) input(ctx context.Context) engine.TaskInput {
	in := engine.TaskInput{Task: t.task.Name, Container: t.container.Name, Issue: int(t.task.Spec.Issue),
		Title: t.status.IssueTitle, Goal: t.task.Spec.Goal, Spec: isSpec(t.task),
		Implementer: t.agents.implementer.Name, Reviewer: t.agents.reviewer.Name,
		MaxRounds: int(t.agents.implementer.Spec.MaxRounds), Now: t.r.now(), Log: logf.FromContext(ctx),
		Facts: t.readFacts, Carry: t.carry, Ending: t.ending, Host: t.host, HostTimeout: t.r.gitTimeout(),
		Pull: t.pull, Benchmark: t.task.Spec.Benchmark != nil, Merger: t}
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

// hostStep reports whether the engine carries out action through the Task's
// host (taskHost), where it acts on the run the reconciler would
// (endsAsTheEngine): the round's pull request opened (also by a rerun of the
// checks of a commit pushed again), the failed jobs of its checks run again,
// and a failure, which hands the issue back.
func hostStep(action model.TaskAction) bool {
	switch action {
	case model.ActionOpenPR, model.ActionRerunChecks, model.ActionFail:
		return true
	}
	return false
}

// mergeStep reports whether the engine carries out action, an approved
// change's way into main, where it acts on the run the reconciler would
// (endsAsTheEngine): its merge, and its branch updated with main, the
// judging run kept through it for the merge (ports.Keeper).
func mergeStep(action model.TaskAction) bool {
	return action == model.ActionMerge || action == model.ActionUpdateBranch
}

// carry carries out step, unless it is a turn's start, or a turn's end, a
// run kept in place or a step of hostStep or mergeStep the engine carries
// out on the run the reconciler would (endsAsTheEngine), which it leaves to
// the engine. The engine brought the run's pause up to date before
// (ports.Pauser), as the step may wait on it.
func (t *taskTurn) carry(ctx context.Context, _ model.TaskFacts, step model.TaskStep) (bool, error) {
	t.step = step
	status := t.status
	logf.FromContext(ctx).V(1).Info("Took Task step", "phase", status.Phase, "round", status.Round,
		"action", step.Action, "message", step.Message)
	if offers(step.Action) {
		return false, nil
	}
	if engine.TurnEnd(step.Action) && t.endsAsTheEngine() {
		t.endsTurn = true
		return false, nil
	}
	if engine.InPlace(step.Action) && t.endsAsTheEngine() {
		t.inPlace = true
		return false, nil
	}
	if hostStep(step.Action) && t.endsAsTheEngine() {
		if step.Unreviewed {
			// As apply drops them: no agent judged this round's change.
			status.Review, status.Verification = nil, nil
		}
		t.byHost, t.reopened = true, status.PullRequest != 0
		return false, nil
	}
	if mergeStep(step.Action) && t.endsAsTheEngine() {
		if step.Unreviewed {
			// As apply drops them: no agent judged this round's change.
			status.Review, status.Verification = nil, nil
		}
		t.merges = true
		return false, nil
	}
	result, err := t.r.apply(ctx, t.task, status, t.container, t.agents, step)
	t.result = result
	return true, err
}

// endsAsTheEngine reports whether the engine ends, suspends or resumes the
// phase's run as the reconciler's endRun, suspendRun and resumeRun would:
// the turn the engine acts on (the phase's role, else the judging run kept
// past its turn for the merge, in the Sandbox the status's counters name) is
// the status's current Sandbox, or the phase has no run and the status names
// no Sandbox. A status that forgot its Sandbox, or names another, is left to
// apply, whose endRun ends whatever the status names, as it always has.
func (t *taskTurn) endsAsTheEngine() bool {
	role := runRole(t.status.Phase)
	if role == "" {
		role = keptRole(t.task, t.status, int(t.status.Round)) // sandboxWorkers.Kept
	}
	if role == "" {
		return t.status.Sandbox == ""
	}
	return t.status.Sandbox != "" && t.status.Sandbox == sandboxName(t.task, t.status, role)
}

// ending is the reconciler's half of a turn's end or a failure the engine
// carries out (engine.TaskInput.Ending), before the Workers end the run: what
// the step says on the pull request, as each Agent's own account, and the
// verdict the status keeps once the review's run is gone. A round no agent
// judged drops an earlier round's verdict first, as apply does. A failure
// says so on the pull request and drops a Blocked Task's proposal, as fail
// does; its issue goes back to a person after the tick (carried).
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
	case model.ActionReviewAgain:
		r.comment(ctx, task, status, container, step.Message+".")
	case model.ActionUpdateBranch:
		// The judging run is kept for the merge; its verdict goes into the
		// status as the branch takes in main, as updateBranch has it.
		status.Review = r.reviewFrom(ctx, task, status)
		r.comment(ctx, task, status, container, step.Message+".")
	case model.ActionFail:
		if t.step.Action == model.ActionFail {
			// The step decided, as apply's: the review's verdict stays, and
			// the feedback says why, which the engine reads back.
			if review := r.reviewFrom(ctx, task, status); review != nil {
				status.Review = review
			}
			status.Feedback = step.Feedback
		}
		r.comment(ctx, task, status, container, "The Task failed. "+step.Message)
		// Only a Blocked Task has a proposal to drop, and no run to end.
		r.dropProposal(ctx, task, status, container)
		t.failed = &step
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
	if step.Action == model.ActionAgentUnavailable {
		// The record kept when the agent is back (agentBackAt): the Task
		// waits, and looks again then.
		t.result = t.r.waitForAgent(ctx, t.task, t.status, step)
		return
	}
	t.r.turnEnded(ctx, t.task, t.status, step)
}

// carried says in the status what a step of hostStep the tick carried out
// did (step is the tick's result), as apply's openPR, rerunChecks and fail
// say it: a pull request opened waits for its checks, and its notes follow
// the status write (afterWrite); checks run again, or a wait for their runs
// to end first, or a failure read as it stands; a failed Task's issue goes
// back to a person.
func (t *taskTurn) carried(ctx context.Context, step model.TaskStep) {
	if !t.byHost || t.handBack(ctx) {
		return
	}
	r, task, status := t.r, t.task, t.status
	switch {
	case step.Action != t.step.Action && step.Action != model.ActionWait:
		return // another step, said by its own
	case t.facts.Phase == model.TaskImplementing:
		if step.Action == t.step.Action {
			t.opened(ctx)
		}
	case t.step.Action != model.ActionRerunChecks:
	case step.Action == model.ActionWait && step.Recheck:
		r.progress(task, status, metav1.ConditionTrue, "Waiting", step.Message)
		t.result = ctrl.Result{RequeueAfter: hostRecheck(ctx, r.checksRecheck())}
	case step.Action == model.ActionWait:
		r.progress(task, status, metav1.ConditionTrue, "Checking", step.Message)
	default:
		r.progress(task, status, metav1.ConditionTrue, "RerunningChecks", step.Message)
	}
}

// handBack hands a Task the engine failed back to a person, said by ending
// (failed), once it failed, as fail hands it back after endRun; it reports
// whether it did.
func (t *taskTurn) handBack(ctx context.Context) bool {
	f := t.failed
	if f == nil || t.status.Phase != c4v1beta1.TaskFailed {
		return false
	}
	t.r.failed(ctx, t.task, t.container, cmp.Or(f.Reason, model.ReasonFailed), f.Message)
	return true
}

// opened says in the status that the tick opened the round's pull request:
// the Task waits for its checks, and looks again; once the status is written,
// a spec's pull request is labelled for its review and a later round's notes
// go on it (pullOpened).
func (t *taskTurn) opened(ctx context.Context) {
	r, task, status, container := t.r, t.task, t.status, t.container
	r.progress(task, status, metav1.ConditionTrue, "Checking", t.step.Message+"; waiting for checks")
	t.result = ctrl.Result{RequeueAfter: hostRecheck(ctx, r.checksRecheck())}
	res, reopened := t.facts.Run.Result, t.reopened
	t.notes = func(ctx context.Context) { r.pullOpened(ctx, task, status, container, res, reopened) }
}

// afterWrite says what the step says once the status that records it is
// written.
func (t *taskTurn) afterWrite(ctx context.Context) {
	if t.notes != nil {
		t.notes(ctx)
	}
}

// pull is the round's pull request as the operator writes it
// (engine.TaskInput.Pull): from the Task's branch, which may be an earlier
// try's it continues, titled and described as openPR's.
func (t *taskTurn) pull(facts model.TaskFacts) engine.PullDraft {
	var summary string
	var extra []string
	if res := facts.Run.Result; res != nil {
		summary, extra = res.Summary, res.Extra
	}
	return engine.PullDraft{Head: t.status.Branch, Title: pullTitle(t.task, t.status),
		Body: pullBody(t.task, t.agents, t.status.Round, summary, extra)}
}

// keptInPlace says in the status that the tick suspended or resumed the
// phase's run in place (step is the tick's result): the Progressing reason a
// fresh suspend writes (runSuspended), or that the resumed run runs
// (runResumed), as the Plan, Triage and Score reconciles say it. A resume
// its Workers held says why itself (readyResume), and a run suspended
// already says nothing more.
func (t *taskTurn) keptInPlace(step model.TaskStep) {
	if !t.inPlace || step.Action != t.step.Action {
		return
	}
	if step.Action == model.ActionResumeRun {
		if t.workers.resumed != "" {
			t.r.runResumed(t.task, t.status, step)
		}
		return
	}
	if t.workers.suspended != "" {
		t.r.runSuspended(t.task, t.status, step)
	}
}

// readyResume readies a suspended run's resume, as the reconciler always
// has (resumeRun): the Agent's credentials and the Container's agent Git
// access, the Standard on main and the issue open (runInputs), and the test
// environments the run needs leased again under its Sandbox's name. Not
// ready, the status says why and result when to look again.
func (t *taskTurn) readyResume(ctx context.Context, order ports.WorkOrder, name string) (bool, error) {
	r, task, status, role := t.r, t.task, t.status, order.Role
	in, result, err := r.runInputs(ctx, task, status, t.container, t.agents, role)
	if in == nil {
		t.result = result
		return false, err
	}
	envs, wait, err := r.leaseEnvironments(ctx, task, status, t.container, role, name, in.labels)
	if err != nil {
		return false, err
	}
	if wait != nil {
		t.result = r.waitForEnvironment(task, status, wait)
		return false, nil
	}
	t.in, t.envs = in, envs
	return true, nil
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
// reconciler ends one (end): merged, failed, or a Benchmark's approved.
func (s *statusStore) SaveProgress(_ context.Context, task string, r ports.TaskRecord) error {
	if err := s.own(task); err != nil {
		return err
	}
	setRecord(s.status, r)
	if p := r.Progress.Phase; p == model.TaskMerged || p == model.TaskFailed || p == model.TaskApproved {
		s.r.end(s.task, s.status, model.TaskEnd{Phase: p, Reason: r.Reason, Message: r.Message})
	}
	return nil
}

// recordOf is the TaskRecord a Task's status holds.
func recordOf(status *c4v1beta1.TaskStatus) ports.TaskRecord {
	rec := ports.TaskRecord{Progress: progressOf(status), PullRequest: int(status.PullRequest),
		PullRequestURL: status.PullRequestURL, Summary: status.Summary}
	if status.AgentBackAt != nil {
		rec.AgentBack = status.AgentBackAt.Time
	}
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
	status.AgentBackAt = nil
	if !r.AgentBack.IsZero() {
		status.AgentBackAt = &metav1.Time{Time: r.AgentBack}
	}
}
