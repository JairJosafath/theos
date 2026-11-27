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
	"fmt"
	"strings"
	"time"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// TaskHost is what a Task tick reaches on the Git host: the repository, its
// pull requests and checks, the issue and its CI.
type TaskHost interface {
	ports.GitHost
	ports.Issues
	ports.CI
}

// TaskInput is what a Task tick is told rather than reads: the Task, its
// Container's repository, its issue and its Agents, and the Container's
// state.
type TaskInput struct {
	Task      string
	Container string
	Repo      ports.Repository
	// Issue is the Task's issue, Title its title and Goal its title and
	// body, as model.IssueGoal reads them.
	Issue int
	Title string
	Goal  string
	// OrderText renders an offer's text from its work order (the lead
	// runtime's method.WorkOrder); nil offers none, and the Workers say
	// what the turn is.
	OrderText func(model.WorkOrder) string
	// Implementer and Reviewer are the Agents that play the roles: for a
	// Spec Task the implementer is the Crew's spec writer, and for a Triage
	// Task its triager. Verifier verifies an approved change; empty for
	// none (a spec is never verified).
	Implementer, Reviewer, Verifier string
	// Spec means a Spec Task (#337): its round writes the issue's spec
	// (model.SpecPath) and nothing else, and an approved spec waits for its
	// review point; SpecApproval is who approves it, a person when empty.
	Spec         bool
	SpecApproval string
	// Triage means a Triage Task: one triager run decides the issue
	// (model.PlanTriage). TriageFailed counts its issue's Triage Tasks that
	// failed before this one, and Environments are the project's test
	// environments by name, which a ready decision may name.
	Triage       bool
	TriageFailed int
	Environments []string
	// Ours reports whether a pull request review's user is one of theos's
	// own accounts (model.ClaimRules.Ours): a person's review is anyone
	// else's. nil counts every reviewer a person.
	Ours func(user string) bool
	// MaxRounds is the implementer's maxRounds; 0 means 3.
	MaxRounds int
	// ContainerReady means the repository is scaffolded; Protected that its
	// main is protected; CheckContexts are the checks main requires.
	ContainerReady bool
	Protected      bool
	CheckContexts  []string
	// Lease is how long a claim of an offered turn holds (model.ClaimRules).
	Lease time.Duration
	Now   time.Time
	Log   Logger
	// Facts, when set, reads the Task's facts in place of the engine's
	// reads of the host, for a runtime that reads more than the engine does
	// yet (the operator, #336: a Standard change, slots, budgets, pauses, a
	// person's approval and rules). It gets the facts from the record and
	// the run the Workers read back, and returns what is decided on.
	Facts func(ctx context.Context, facts model.TaskFacts) (model.TaskFacts, error)
	// Carry, when set, is asked first to carry out each step decided: it
	// returns true for a step it carried out itself, through the runtime,
	// and the engine then does nothing more; false leaves the step to the
	// engine. The operator carries every step but a turn's offer itself
	// until #336 moves the rest (docs/plans/lead-runtime.md, step 12). The
	// engine reaches the host only for a step Carry leaves it, so a runtime
	// whose Carry leaves it only the offers may tick with no host.
	Carry func(ctx context.Context, facts model.TaskFacts, step model.TaskStep) (bool, error)
	// Ending, when set, is the runtime's half of a step that ends a turn
	// (TurnEnd), called before the Workers end the run: what the step says
	// on the pull request, in place of the engine's own comment or review,
	// and what the runtime keeps of the run before it goes. The operator
	// posts as each Agent's own account, and keeps the review on the Task's
	// status (#336). With Ending set, the engine reaches the host for none
	// of these steps.
	Ending func(ctx context.Context, facts model.TaskFacts, step model.TaskStep) error
}

// TurnEnd reports whether action ends a turn and moves the Task on without
// the host but for what the step says on the pull request (TaskInput.Ending):
// a run to verify, a round not accepted, a review or verification to try
// again, an interrupted run to run again and a conflict to resolve.
func TurnEnd(action model.TaskAction) bool {
	switch action {
	case model.ActionVerify, model.ActionRetryVerify, model.ActionRetry, model.ActionRetryReview,
		model.ActionRerun, model.ActionResolveConflicts:
		return true
	}
	return false
}

// TaskResult is what a Task tick did: the step it carried out, which a
// capability the runtime lacks may have turned into a wait
// (model.ReasonPersonMerges), and the Task's record after it.
type TaskResult struct {
	Step   model.TaskStep
	Record ports.TaskRecord
}

// defaultMaxRounds is an Agent's maxRounds when it sets none.
const defaultMaxRounds = 3

// whyDone is the word a run ends with once its result is taken.
const whyDone = "done"

// TaskTick moves a Task one step: it reads its record from the runtime's
// store, its run from the runtime's Workers and its pull request and
// checks from the host, decides with model.NextTaskStep and carries out the
// step: a turn through Workers, a pull request, a merge and the issue
// through the host, its progress through the store. A merge the runtime
// can't do waits for a person (model.ReasonPersonMerges), and the tick after
// the person's merge ends the Task Merged. The operator's Task reconcile
// runs on it too (#336): it gives its own facts (TaskInput.Facts), carries
// out every step but a turn's offer and end itself (TaskInput.Carry), says
// what a turn's end says itself (TaskInput.Ending), and its Workers ready a
// turn before it is offered (ports.Preparer), which also offers a restarted
// turn on a later tick. The rest of the operator's run side (pauses,
// resumes, budgets) is still its own.
func TaskTick(ctx context.Context, rt ports.Runtime, host TaskHost, in TaskInput) (TaskResult, error) {
	store, workers := rt.Store(), rt.Workers()
	if workers == nil {
		return TaskResult{}, errors.New("the runtime has no Workers to run a Task's turns")
	}
	in.Log = logOr(in.Log)
	switch {
	case in.Triage:
		in.MaxRounds = 1 // a failed triage fails, and its Crew tries again
	case in.MaxRounds == 0:
		in.MaxRounds = defaultMaxRounds
	}
	rec, err := store.Progress(ctx, in.Task)
	if err != nil {
		return TaskResult{}, err
	}
	t := &taskTick{rt: rt, host: host, in: in, rec: rec}
	if final(rec.Progress.Phase) {
		return TaskResult{Step: model.TaskStep{Action: model.ActionNone}, Record: rec}, nil
	}
	facts, err := t.facts(ctx)
	if err != nil {
		return TaskResult{}, err
	}
	step := model.NextTaskStep(facts)
	if in.Carry != nil {
		carried, err := in.Carry(ctx, facts, step)
		if err != nil {
			return TaskResult{Step: step, Record: t.rec}, err
		}
		if carried {
			// The runtime carried it out, through its own store: its record
			// is what the store keeps now.
			return t.result(ctx, step)
		}
	}
	if host == nil && !t.hostFree(step.Action) {
		return TaskResult{Step: step, Record: t.rec}, fmt.Errorf(
			"the Task tick has no host to carry out %s", step.Action)
	}
	step, err = t.carry(ctx, facts, step)
	if err == nil && step.Reason == ReasonOfferHeld {
		return t.result(ctx, step)
	}
	return TaskResult{Step: step, Record: t.rec}, err
}

// result is the tick's result after a step the runtime carried out, or an
// offer its Workers held: the record is read back from the store, which the
// runtime may have changed.
func (t *taskTick) result(ctx context.Context, step model.TaskStep) (TaskResult, error) {
	rec, err := t.rt.Store().Progress(ctx, t.in.Task)
	if err != nil {
		return TaskResult{Step: step, Record: t.rec}, err
	}
	t.rec = rec
	return TaskResult{Step: step, Record: rec}, nil
}

// hostFree reports whether the engine carries out action without the host:
// a wait, a turn's offer, or a turn's end whose words on the pull request
// are the runtime's (TaskInput.Ending) or none.
func (t *taskTick) hostFree(action model.TaskAction) bool {
	switch action {
	case model.ActionNone, model.ActionWait, model.ActionStartImplement, model.ActionStartReview,
		model.ActionStartVerify, model.ActionVerify, model.ActionRetryVerify, model.ActionRetryReview,
		model.ActionRerun:
		return true
	case model.ActionRetry, model.ActionResolveConflicts:
		return t.in.Ending != nil
	}
	return false
}

// taskTick is one Task tick's state.
type taskTick struct {
	rt   ports.Runtime
	host TaskHost
	in   TaskInput
	rec  ports.TaskRecord
}

func final(phase string) bool { return phase == model.TaskMerged || phase == model.TaskFailed }

// RunRole is the role whose run the Task waits on in phase: the triager's
// in a Triage Task's Implementing, else RunRole's.
func (in TaskInput) RunRole(phase string) string {
	if in.Triage && phase == model.TaskImplementing {
		return model.RoleTriager
	}
	return RunRole(phase)
}

// RunRole is the role whose run a phase waits on; empty for none.
func RunRole(phase string) string {
	switch phase {
	case model.TaskImplementing:
		return model.RoleImplementer
	case model.TaskReviewing:
		return model.RoleReviewer
	case model.TaskVerifying:
		return model.RoleVerifier
	}
	return ""
}

// facts reads what the Task's next step depends on.
func (t *taskTick) facts(ctx context.Context) (model.TaskFacts, error) {
	p, in := t.rec.Progress, t.in
	facts := model.TaskFacts{Phase: p.Phase, Container: in.Container, Round: p.Round, MaxRounds: in.MaxRounds,
		ContainerReady: in.ContainerReady, Protected: in.Protected, ReviewAttempts: p.ReviewAttempts,
		ReviewAgains: p.ReviewAgains, Reruns: p.Reruns, Resumes: p.Resumes, Feedback: p.Feedback,
		MergeWhenChecksPass: p.MergeWhenChecksPass, Triage: in.Triage, Spec: in.Spec, Verify: in.Verifier != ""}
	if in.Spec {
		facts.SpecApproval = cmpOr(in.SpecApproval, model.SpecApprovalPerson)
	}
	if facts.Phase == "" {
		facts.Phase = model.TaskPending
	}
	if !t.rec.PhaseSince.IsZero() {
		facts.InPhase = in.Now.Sub(t.rec.PhaseSince)
	}
	if p.ChecksFailed {
		facts.FailedCommit = p.Commit
	}
	if role := in.RunRole(facts.Phase); role != "" {
		run, err := t.rt.Workers().Run(ctx, in.Task, p.Round, role)
		if err != nil {
			return facts, err
		}
		if m := run.Markers; m != nil && m.EndDue != "" {
			// A claim past its lease and grace, or released: the engine ends
			// the run, and reads it back interrupted, to offer it again.
			if err := t.rt.Workers().End(ctx, in.Task, p.Round, role, m.EndDue); err != nil {
				return facts, err
			}
			if run, err = t.rt.Workers().Run(ctx, in.Task, p.Round, role); err != nil {
				return facts, err
			}
		}
		facts.Run = run
	}
	if in.Facts != nil {
		facts, err := in.Facts(ctx, facts)
		if err != nil {
			return facts, err
		}
		// Reading them may have changed the record through the runtime's
		// store: a step is carried out on the record as it is now.
		t.rec, err = t.rt.Store().Progress(ctx, in.Task)
		return facts, err
	}
	if t.host == nil {
		return facts, errors.New("the Task tick has no host to read the Task's facts from")
	}
	hostCtx, cancel := context.WithTimeout(ctx, HostTimeout)
	defer cancel()
	if facts.Phase == model.TaskPending && in.Protected && in.ContainerReady && !in.Triage {
		if err := ReadBase(hostCtx, t.host, in.Repo, in.CheckContexts, &facts); err != nil {
			return facts, &HostError{Err: err}
		}
	}
	if facts.Phase == model.TaskChecking && p.Commit != "" {
		if _, err := ReadChecks(hostCtx, t.host, in.Repo, ChecksInput{Commit: p.Commit, Reruns: p.ChecksReruns,
			RerunAt: p.ChecksRerunAt, RerunDue: p.ChecksRerunDue, CheckingSince: t.rec.PhaseSince, Now: in.Now,
			Log: in.Log}, &facts); err != nil {
			return facts, &HostError{Err: err}
		}
	}
	if err := ReadPull(hostCtx, t.host, in.Repo, t.rec.PullRequest, p.MergeWhenChecksPass, &facts); err != nil {
		return facts, &HostError{Err: err}
	}
	if err := t.specFacts(hostCtx, &facts); err != nil {
		return facts, &HostError{Err: err}
	}
	return facts, nil
}

// carry carries out step, and returns it as carried out.
func (t *taskTick) carry(ctx context.Context, facts model.TaskFacts, step model.TaskStep) (model.TaskStep, error) {
	p := t.rec.Progress
	role := t.in.RunRole(facts.Phase)
	if facts.Run.Done {
		// What an approving review or verification leaves for the merge is
		// kept with the next progress, as its run ends before the merge.
		t.rec.Findings = model.KeepFindings(t.rec.Findings, role, facts.Run.Result)
	}
	switch step.Action {
	case model.ActionNone, model.ActionWait:
		return step, nil
	case model.ActionStartImplement:
		return t.start(ctx, step, t.in.RunRole(model.TaskImplementing))
	case model.ActionStartReview:
		return t.start(ctx, step, model.RoleReviewer)
	case model.ActionStartVerify:
		return t.start(ctx, step, model.RoleVerifier)
	case model.ActionVerify:
		// The review's run ends; its approval stays, and the verifier runs.
		if err := t.ending(ctx, facts, step, nil); err != nil {
			return step, err
		}
		return step, t.next(ctx, role, model.VerifyChange(p))
	case model.ActionRetryVerify:
		if err := t.ending(ctx, facts, step, nil); err != nil {
			return step, err
		}
		return step, t.restart(ctx, role, model.RetryReview(p), model.RoleVerifier)
	case model.ActionAwaitApproval:
		return step, t.awaitApproval(ctx, role)
	case model.ActionSpecDeclined:
		return step, t.fail(ctx, role, model.TaskStep{Action: model.ActionFail, Reason: model.ReasonSpecDeclined,
			Message: model.SpecDeclinedMessage(t.rec.PullRequestURL)})
	case model.ActionTriaged:
		return t.triaged(ctx, facts, step)
	case model.ActionOpenPR:
		return step, t.openPR(ctx, facts, false)
	case model.ActionRerunChecks:
		if facts.Phase == model.TaskImplementing {
			return step, t.openPR(ctx, facts, true)
		}
		return step, t.rerunChecks(ctx, step)
	case model.ActionRetry:
		if err := t.ending(ctx, facts, step, func() { t.commentRound(ctx, facts, step) }); err != nil {
			return step, err
		}
		t.rec.Findings = nil // the next round is reviewed afresh
		return step, t.next(ctx, role, model.NextRound(p, step.Feedback))
	case model.ActionRetryReview:
		if err := t.ending(ctx, facts, step, nil); err != nil {
			return step, err
		}
		return step, t.restart(ctx, role, model.RetryReview(p), model.RoleReviewer)
	case model.ActionRerun:
		if err := t.ending(ctx, facts, step, nil); err != nil {
			return step, err
		}
		return step, t.restart(ctx, role, model.Rerun(p), role)
	case model.ActionResolveConflicts:
		if err := t.ending(ctx, facts, step, func() { t.comment(ctx, step.Message+".") }); err != nil {
			return step, err
		}
		return step, t.restart(ctx, role, model.ResolveConflicts(p, step.Feedback), model.RoleImplementer)
	case model.ActionUpdateBranch:
		return t.updateBranch(ctx, facts, step)
	case model.ActionMerge:
		return t.merge(ctx, role, step)
	case model.ActionFail:
		if t.in.Triage {
			return step, t.failTriage(ctx, step)
		}
		return step, t.fail(ctx, role, step)
	}
	// What the operator alone does (a sandbox suspended, paused or resumed in
	// place, a Standard change) waits here, saying so.
	return model.TaskStep{Action: model.ActionWait, Reason: ReasonNotBuilt, Message: fmt.Sprintf(
		"%s: this runtime does not %s yet", step.Message, step.Action)}, nil
}

// ReasonNotBuilt is the reason of a step a runtime has no way to carry out.
const ReasonNotBuilt = "NotBuilt"

// ReasonOfferHeld is the reason of a turn's start its Workers were not ready
// to offer (ports.Preparer): why is the runtime's, and a later tick offers
// it.
const ReasonOfferHeld = "OfferHeld"

// start offers role's turn, the step decided; a turn its Workers held waits.
func (t *taskTick) start(ctx context.Context, step model.TaskStep, role string) (model.TaskStep, error) {
	offered, err := t.offer(ctx, role)
	if err != nil || offered {
		return step, err
	}
	return model.TaskStep{Action: model.ActionWait, Reason: ReasonOfferHeld,
		Message: step.Message + ": its Workers did not offer the turn yet"}, nil
}

// save stores p as the Task's progress, with when its phase changed.
func (t *taskTick) save(ctx context.Context, p model.TaskProgress) error {
	if p.Phase != t.rec.Progress.Phase {
		t.rec.PhaseSince = t.in.Now
	}
	t.rec.Progress = p
	return t.rt.Store().SaveProgress(ctx, t.in.Task, t.rec)
}

// next ends the run of role, if any, and saves p.
func (t *taskTick) next(ctx context.Context, role string, p model.TaskProgress) error {
	if role != "" {
		if err := t.rt.Workers().End(ctx, t.in.Task, t.rec.Progress.Round, role, whyDone); err != nil {
			return err
		}
	}
	return t.save(ctx, p)
}

// ending is what a step that ends a turn says before the run ends: the
// runtime's half when it has one (TaskInput.Ending), else the engine's own
// words on the pull request, say, which may be none.
func (t *taskTick) ending(ctx context.Context, facts model.TaskFacts, step model.TaskStep, say func()) error {
	if t.in.Ending != nil {
		return t.in.Ending(ctx, facts, step)
	}
	if say != nil {
		say()
	}
	return nil
}

// restart ends the run of role, saves p and offers its round to again at
// once: a turn of the same round offered again starts afresh, where the
// round's earlier answers would count until it is. Workers that ready a
// turn (ports.Preparer) have it offered by a later tick instead, which
// decides its start again and readies it then: the restart is a step of its
// own in the runtime's record, and the readying, which may hold the turn and
// say why, is the new turn's. Such Workers name each turn apart, so the
// ended run reads back not started.
func (t *taskTick) restart(ctx context.Context, role string, p model.TaskProgress, again string) error {
	if err := t.next(ctx, role, p); err != nil {
		return err
	}
	if _, readies := t.rt.Workers().(ports.Preparer); readies {
		return nil
	}
	_, err := t.offer(ctx, again)
	return err
}

// offer offers role's turn: the progress first, so a tick that stops after
// it offers the same turn again, which Offer takes as one. Workers that
// ready a turn first (ports.Preparer) are asked before the progress is
// saved; not ready, nothing is saved or offered, and offer reports false.
// Ready, the start is saved on the record as the store keeps it then: what
// readying the turn changed in it (an earlier try's pull request and
// feedback the operator took over) stays.
func (t *taskTick) offer(ctx context.Context, role string) (bool, error) {
	p, order := t.order(role)
	if prep, ok := t.rt.Workers().(ports.Preparer); ok {
		ready, err := prep.Prepare(ctx, order)
		if err != nil || !ready {
			return false, err
		}
		if t.rec, err = t.rt.Store().Progress(ctx, t.in.Task); err != nil {
			return false, err
		}
		readied := order
		if p, order = t.order(role); order.Round != readied.Round {
			return false, fmt.Errorf("readying the %s turn of round %d moved the Task to round %d", role,
				readied.Round, order.Round)
		}
	}
	if err := t.save(ctx, p); err != nil {
		return false, err
	}
	return true, t.rt.Workers().Offer(ctx, order)
}

// order is the start of role's run on the record, and the turn it offers.
func (t *taskTick) order(role string) (model.TaskProgress, model.WorkOrder) {
	agent := t.in.Implementer
	switch role {
	case model.RoleReviewer:
		agent = t.in.Reviewer
	case model.RoleVerifier:
		agent = t.in.Verifier
	}
	p := model.StartRun(t.rec.Progress, role)
	branch := model.TaskBranch(t.in.Task)
	if t.in.Triage {
		branch = ports.DefaultBranch // a triager reads main, and pushes nothing
	}
	order := model.WorkOrder{Task: t.in.Task, Issue: t.in.Issue, Branch: branch, Role: role,
		Agent: agent, Round: p.Round, Lease: t.in.Lease, Goal: t.in.Goal, Spec: t.in.Spec}
	if role == model.RoleImplementer {
		order.Feedback = p.Feedback
	}
	if t.in.OrderText != nil {
		order.Text = t.in.OrderText(order)
	}
	return p, order
}

// openPR opens the round's pull request from the implementer's branch, or
// finds the one open, and waits for the checks of the commit the run
// pushed; again means the round pushed again a commit whose checks failed.
func (t *taskTick) openPR(ctx context.Context, facts model.TaskFacts, again bool) error {
	res := facts.Run.Result
	if res == nil || res.Commit == "" {
		return errors.New("the implementer's run ended with no commit to open a pull request of")
	}
	hostCtx, cancel := context.WithTimeout(ctx, HostTimeout)
	defer cancel()
	pr, err := t.host.OpenPullRequest(hostCtx, t.in.Repo, model.TaskBranch(t.in.Task), ports.DefaultBranch,
		t.pullTitle(), PullBody(PullBodyInput{Task: t.in.Task, Issue: t.in.Issue, Implementer: t.in.Implementer,
			Reviewer: t.in.Reviewer, Verifier: t.in.Verifier, Round: facts.Round, Summary: res.Summary}))
	if err != nil {
		return &HostError{Err: err}
	}
	t.in.Log.Info("Opened pull request", "url", pr.URL, "commit", res.Commit, "round", facts.Round)
	if err := t.rt.Workers().End(ctx, t.in.Task, facts.Round, model.RoleImplementer, whyDone); err != nil {
		return err
	}
	t.rec.PullRequest, t.rec.PullRequestURL, t.rec.Summary = pr.Number, pr.URL, res.Summary
	if again {
		return t.save(ctx, model.PushedAgain(t.rec.Progress, res.Commit))
	}
	return t.save(ctx, model.OpenedPR(t.rec.Progress, res.Commit))
}

// rerunChecks runs again the failed jobs of the round's commit.
func (t *taskTick) rerunChecks(ctx context.Context, step model.TaskStep) error {
	p := t.rec.Progress
	hostCtx, cancel := context.WithTimeout(ctx, HostTimeout)
	defer cancel()
	found, err := t.host.CommitRuns(hostCtx, t.in.Repo, p.Commit)
	if err != nil {
		return &HostError{Err: err}
	}
	runs := WorkflowRuns(found)
	if !model.RunsEnded(runs) {
		return nil // the next tick looks again
	}
	for _, run := range model.FailedCommitRuns(runs) {
		if err := t.host.RerunFailedJobs(hostCtx, t.in.Repo, run.ID); err != nil {
			return t.fail(ctx, "", model.TaskStep{Action: model.ActionFail, Reason: model.ReasonNoCIResult,
				Message: fmt.Sprintf("%s, and the Git host refused to run its failed jobs again: %v", step.Message,
					err)})
		}
	}
	if p.ChecksRerunDue {
		return t.save(ctx, model.RerunPushedChecks(p, t.in.Now))
	}
	return t.save(ctx, model.RerunChecks(p, t.in.Now))
}

// updateBranch merges main into an approved change's branch, and waits for
// the checks on the new head; a conflict sends it back to its implementer.
func (t *taskTick) updateBranch(ctx context.Context, facts model.TaskFacts,
	step model.TaskStep) (model.TaskStep, error) {
	hostCtx, cancel := context.WithTimeout(ctx, HostTimeout)
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
	t.comment(ctx, step.Message+".")
	if role := RunRole(facts.Phase); role != "" {
		if err := t.rt.Workers().End(ctx, t.in.Task, facts.Round, role, whyDone); err != nil {
			return step, err
		}
	}
	return step, t.save(ctx, model.UpdatedBranch(t.rec.Progress, up.Head))
}

// merge merges an approved change, or, in a runtime that merges nothing,
// waits for a person's merge and ends the Task Merged once it came.
func (t *taskTick) merge(ctx context.Context, role string, step model.TaskStep) (model.TaskStep, error) {
	p := t.rec.Progress
	hostCtx, cancel := context.WithTimeout(ctx, HostTimeout)
	defer cancel()
	pr, err := t.host.GetPullRequest(hostCtx, t.in.Repo, t.rec.PullRequest)
	if err != nil {
		return step, &HostError{Err: err}
	}
	switch model.DecideMerge(model.PullFacts{Merged: pr.Merged, Closed: pr.State == stateClosed,
		HeadSHA: pr.HeadSHA}, p.Commit) {
	case model.MergeFailClosed:
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
		if err := t.host.MergePullRequest(hostCtx, t.in.Repo, t.rec.PullRequest, p.Commit,
			t.pullTitle(), fmt.Sprintf("Task %s, round %d.", t.in.Task, p.Round)); err != nil {
			return step, &HostError{Err: err}
		}
	}
	if t.in.Spec {
		// A spec's issue stays open: its criteria go into it, and it is ready.
		if err := t.specMerged(hostCtx); err != nil {
			return step, &HostError{Err: err}
		}
		return step, t.end(ctx, role, model.TaskEnd{Phase: model.TaskMerged, Reason: "Merged", Message: step.Message})
	}
	// Merged, by theos or by a person: what the review and the verification
	// left is filed, and the issue closes with it.
	if t.in.Issue > 0 {
		t.fileFindings(hostCtx)
		if err := t.host.CloseIssue(hostCtx, t.in.Repo, t.in.Issue); err != nil {
			t.in.Log.Info("Could not close the merged Task's issue", "issue", t.in.Issue, "error", err.Error())
		}
	}
	return step, t.end(ctx, role, model.TaskEnd{Phase: model.TaskMerged, Reason: "Merged", Message: step.Message})
}

// stateClosed is a closed pull request's State.
const stateClosed = "closed"

// fail ends the Task Failed, saying why on its pull request and handing its
// issue back to a person.
func (t *taskTick) fail(ctx context.Context, role string, step model.TaskStep) error {
	reason := cmpOr(step.Reason, model.ReasonFailed)
	t.comment(ctx, step.Message+".")
	if t.in.Issue > 0 {
		hostCtx, cancel := context.WithTimeout(ctx, HostTimeout)
		err := errors.Join(t.host.AddIssueLabel(hostCtx, t.in.Repo, t.in.Issue, model.LabelNeedsHuman),
			t.host.CommentIssue(hostCtx, t.in.Repo, t.in.Issue, fmt.Sprintf("Task %s failed: %s", t.in.Task,
				step.Message)))
		cancel()
		if err != nil {
			t.in.Log.Info("Could not hand the failed Task's issue back", "issue", t.in.Issue, "error", err.Error())
		}
	}
	return t.end(ctx, role, model.TaskEnd{Phase: model.TaskFailed, Reason: reason, Message: step.Message})
}

// end ends the run of role, if any, and the Task as e says.
func (t *taskTick) end(ctx context.Context, role string, e model.TaskEnd) error {
	p := t.rec.Progress
	p.Phase = e.Phase
	t.rec.Reason, t.rec.Message = e.Reason, e.Message
	return t.next(ctx, role, p)
}

// commentRound says on the pull request why the round was not accepted: a
// reviewer's request for changes as a review, any other reason as a
// comment. A host that refuses the review, as one does of an account's own
// pull request where theos and the sessions share one account
// (docs/plans/lead-runtime.md, decision 10), gets the verdict as a comment.
func (t *taskTick) commentRound(ctx context.Context, facts model.TaskFacts, step model.TaskStep) {
	if t.rec.PullRequest == 0 {
		return
	}
	hostCtx, cancel := context.WithTimeout(ctx, HostTimeout)
	defer cancel()
	var err error
	if res := facts.Run.Result; facts.Phase == model.TaskReviewing && res != nil &&
		res.Verdict == model.VerdictChanges {
		err = t.host.ReviewPullRequest(hostCtx, t.in.Repo, t.rec.PullRequest, t.rec.Progress.Commit,
			ports.ReviewRequestChanges, res.Summary)
		if err != nil {
			t.in.Log.Info("The host refused the review; posting it as a comment", "error", err.Error())
			err = t.host.CommentPullRequest(hostCtx, t.in.Repo, t.rec.PullRequest, fmt.Sprintf(
				"%s asks for changes in round %d.\n\n%s", t.in.Reviewer, facts.Round, res.Summary))
		}
	} else {
		err = t.host.CommentPullRequest(hostCtx, t.in.Repo, t.rec.PullRequest,
			fmt.Sprintf("Round %d was not accepted.\n\n%s", facts.Round, step.Feedback))
	}
	if err != nil {
		t.in.Log.Info("Could not say on the pull request why the round was not accepted", "error", err.Error())
	}
}

// comment adds body to the Task's pull request, if it has one.
func (t *taskTick) comment(ctx context.Context, body string) {
	if t.rec.PullRequest == 0 {
		return
	}
	hostCtx, cancel := context.WithTimeout(ctx, HostTimeout)
	defer cancel()
	if err := t.host.CommentPullRequest(hostCtx, t.in.Repo, t.rec.PullRequest, body); err != nil {
		t.in.Log.Info("Could not comment on pull request", "task", t.in.Task, "error", err.Error())
	}
}

func cmpOr(s, or string) string {
	if s != "" {
		return s
	}
	return or
}

// PullTitle is the title of a Task's pull request: its issue's title, else
// its name, on one line of at most 72 characters.
func PullTitle(title, task string) string {
	if title == "" {
		title = task
	}
	return FirstLine(title)
}

// FirstLine is s's first line, cut to 72 characters.
func FirstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if len(line) > 72 {
		line = line[:69] + "..."
	}
	return line
}

// PullBodyInput is what a Task's pull request's description says.
type PullBodyInput struct {
	Task  string
	Issue int
	// Implementer, Reviewer and Verifier are how the description names the
	// Agents; Verifier empty for none.
	Implementer, Reviewer, Verifier string
	Round                           int
	Summary                         string
}

// PullBody is a Task's pull request's description: the issue it closes,
// who worked it, and the round's summary.
func PullBody(in PullBodyInput) string {
	var b strings.Builder
	if in.Issue > 0 {
		fmt.Fprintf(&b, "Closes #%d.\n\n", in.Issue)
	}
	fmt.Fprintf(&b, "Task `%s`: implemented by %s, reviewed by %s", in.Task, in.Implementer, in.Reviewer)
	if in.Verifier != "" {
		fmt.Fprintf(&b, ", verified by %s", in.Verifier)
	}
	b.WriteString(".\n")
	if summary := strings.TrimSpace(in.Summary); summary != "" {
		fmt.Fprintf(&b, "\n## Summary of round %d\n\n%s\n", in.Round, summary)
	}
	return b.String()
}
