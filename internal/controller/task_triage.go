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
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// reasonTriaged is the Succeeded reason of a Triage Task whose decision the
// operator applied, or found a person had made already.
const reasonTriaged = "Triaged"

// reconcileTriage takes the next step of a Triage Task (docs/design.md,
// "Triage"): one triager run on the issue, on a clone it pushes nothing from,
// then the operator applies its decision to the issue. Its Crew made the
// Task; like an Implement Task, it is adopted by its Container.
func (r *TaskReconciler) reconcileTriage(ctx context.Context, task *c4v1beta1.Task) (ctrl.Result, error) {
	status := task.Status.DeepCopy()
	if status.Phase == "" {
		status.Phase = c4v1beta1.TaskPending
	}
	var container c4v1beta1.Container
	err := r.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: task.Spec.ContainerRef.Name}, &container)
	if apierrors.IsNotFound(err) {
		r.progress(task, status, metav1.ConditionFalse, "ContainerNotFound",
			"Container "+task.Spec.ContainerRef.Name+" not found")
		return ctrl.Result{}, r.writeStatus(ctx, task, status)
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	ctx = withHostEvents(ctx, r.Client, task.Namespace, container.Spec.GitProviderRef.Name)
	labels := projectLabels(task.Namespace)
	labels[c4v1beta1.LabelContainer] = container.Name
	if err := adopt(ctx, r.Client, r.Scheme, &container, task, labels); err != nil {
		return ctrl.Result{}, err
	}
	// Adopting reads the Task back: its status is the newer one (#591).
	status = task.Status.DeepCopy()
	if status.Phase == "" {
		status.Phase = c4v1beta1.TaskPending
	}
	if isFinal(status.Phase) {
		if err := r.answerTriage(ctx, task, status, &container); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, r.writeStatus(ctx, task, status)
	}
	var agent c4v1beta1.Agent
	err = r.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: task.Spec.AgentRef.Name}, &agent)
	if apierrors.IsNotFound(err) {
		r.progress(task, status, metav1.ConditionFalse, "AgentNotFound", "Agent "+task.Spec.AgentRef.Name+" not found")
		return ctrl.Result{}, r.writeStatus(ctx, task, status)
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	facts, err := r.triageFacts(ctx, task, status)
	if err != nil {
		return ctrl.Result{}, err
	}
	step := model.NextTaskStep(facts)
	logf.FromContext(ctx).V(1).Info("Took Task step", "phase", status.Phase, "action", step.Action,
		"message", step.Message)
	if status.Phase == c4v1beta1.TaskImplementing {
		if err := r.syncPause(ctx, task, status, sandboxName(task, status, model.RoleTriager), facts); err != nil {
			return ctrl.Result{}, err
		}
	}
	result, err := r.applyTriage(ctx, task, status, &container, &agent, step)
	if err != nil {
		return result, err
	}
	r.setPaused(task, status, step, facts.Pause)
	r.queueForSlot(status, step, &result)
	return result, r.writeStatus(ctx, task, status)
}

// triageFacts gathers what a Triage Task's next step depends on: its one run,
// the slots, and its Crew's cost cap and pause.
func (r *TaskReconciler) triageFacts(ctx context.Context, task *c4v1beta1.Task,
	status *c4v1beta1.TaskStatus) (model.TaskFacts, error) {
	facts := model.TaskFacts{
		Phase:     string(status.Phase),
		Container: task.Spec.ContainerRef.Name,
		Round:     int(status.Round),
		MaxRounds: 1,
		Reruns:    int(status.Reruns),
		Resumes:   int(status.Resumes),
		Feedback:  status.Feedback,
		Triage:    true,
	}
	if status.PhaseChangedAt != nil {
		facts.InPhase = r.now().Sub(status.PhaseChangedAt.Time)
	}
	if status.AgentBackAt != nil {
		facts.AgentBackIn = status.AgentBackAt.Sub(r.now())
	}
	if r.MaxSandboxes > 0 {
		noSlot, err := r.noFreeSlot(ctx, task)
		if err != nil {
			return facts, err
		}
		facts.NoFreeSlot = noSlot
	}
	if err := r.crewFacts(ctx, task, status, &facts); err != nil {
		return facts, err
	}
	if status.Phase == c4v1beta1.TaskImplementing {
		name := sandboxName(task, status, model.RoleTriager)
		startedOn := ""
		if name == status.Sandbox {
			startedOn = status.SandboxPod
		}
		run, err := runState(ctx, r.Client, task.Namespace, name, startedOn)
		if err != nil {
			return facts, err
		}
		if name == status.Sandbox && startedOn == "" {
			status.SandboxPod = run.Pod // the pod the run started on
		}
		facts.Run = r.withSuspendedFor(run)
	}
	return facts, nil
}

// applyTriage does one step of a Triage Task and records it in status.
func (r *TaskReconciler) applyTriage(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agent *c4v1beta1.Agent, step model.TaskStep) (ctrl.Result, error) {
	agents := taskAgents{implementer: agent, reviewer: agent}
	switch step.Action {
	case model.ActionWait:
		return r.wait(ctx, task, status, step), nil

	case model.ActionStartImplement:
		return r.startTriage(ctx, task, status, container, agents, step.Message)

	case model.ActionRerun, model.ActionRerunPaused, model.ActionAgentUnavailable, model.ActionSuspendRun,
		model.ActionPauseRun, model.ActionSuspendForAgent:
		// None reads the role, which a triage run's sandbox name holds.
		return r.apply(ctx, task, status, container, agents, step)

	case model.ActionResumeRun:
		in, result, err := r.runInputs(ctx, task, status, container, agents, model.RoleTriager)
		if in == nil {
			return result, err
		}
		listed, err := listedEnvironments(ctx, r.Client, task.Namespace)
		if err != nil {
			return ctrl.Result{}, err
		}
		fresh, err := r.runSandbox(ctx, task, status, container, agents, model.RoleTriager, status.Sandbox, in, listed)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, r.resumeWith(ctx, task, status, agent, fresh, step)

	case model.ActionTriaged:
		return ctrl.Result{}, r.applyDecision(ctx, task, status, container)

	case model.ActionFail:
		return ctrl.Result{}, r.failTriage(ctx, task, status, container, step)
	}
	return ctrl.Result{}, nil
}

// startTriage creates the triager's sandbox on the Container's repository,
// with the issue as it reads now. The run clones the base and pushes nothing.
func (r *TaskReconciler) startTriage(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents, message string) (ctrl.Result, error) {
	in, result, err := r.runInputs(ctx, task, status, container, agents, model.RoleTriager)
	if in == nil {
		return result, err
	}
	// The triager reads the project's test environments, which it may
	// name for the issue (docs/plans/done/test-environments.md, T13).
	listed, err := listedEnvironments(ctx, r.Client, task.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}
	setProgress(status, model.StartRun(progressOf(status), model.RoleTriager))
	name := sandboxName(task, status, model.RoleTriager)
	sandbox, err := r.runSandbox(ctx, task, status, container, agents, model.RoleTriager, name, in, listed)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, sandbox); err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, err
	}
	status.Sandbox, status.SandboxPod, status.AgentBackAt = name, "", nil
	r.progress(task, status, metav1.ConditionTrue, "Running", fmt.Sprintf("%s in sandbox %s", message, name))
	logf.FromContext(ctx).Info("Started agent sandbox", "sandbox", name, "role", model.RoleTriager,
		"issue", task.Spec.Issue)
	return ctrl.Result{}, nil
}

// applyDecision applies the triager's decision to the issue as it reads now
// (model.PlanTriage), records it in status and ends the Task Merged. Its
// status label goes on last: a write that fails before it is tried again on
// an issue still status/proposed.
func (r *TaskReconciler) applyDecision(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container) error {
	log := logf.FromContext(ctx)
	run, err := runState(ctx, r.Client, task.Namespace, status.Sandbox, status.SandboxPod)
	if err != nil || run.Result == nil || run.Result.Triage == nil {
		return fmt.Errorf("reading the triage of %s: %v", status.Sandbox, err)
	}
	decision := model.NormalTriage(*run.Result.Triage)
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	issue, err := provider.GetIssue(gitCtx, repo, int(task.Spec.Issue))
	if err != nil {
		return err
	}
	envs, err := projectEnvironments(ctx, r.Client, task.Namespace)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(envs))
	for _, e := range envs {
		names = append(names, e.Name)
	}
	actions := model.PlanTriage(task.Name, model.TriageIssue{Number: issue.Number, Body: issue.Body,
		Labels: issue.Labels, Closed: issue.State == stateClosed, Environments: names}, decision)
	if actions.Problem != "" {
		return r.failTriage(ctx, task, status, container, model.TaskStep{Action: model.ActionFail,
			Feedback: actions.Problem, Message: "The triage failed: " + actions.Problem})
	}
	applied := actions.Skip
	if applied == "" {
		// A Crew keeps the labels; a Triage Task made by hand may come first.
		labels := issueLabels()
		for _, l := range actions.EnvLabels {
			env := model.EnvLabel(strings.TrimPrefix(l, model.LabelEnvPrefix))
			labels = append(labels, ports.Label{Name: env.Name, Color: env.Color, Description: env.Description})
		}
		if err := provider.EnsureLabels(gitCtx, repo, labels); err != nil {
			return err
		}
		if applied, err = r.applyTriageActions(gitCtx, provider, repo, issue.Number, actions); err != nil {
			return err
		}
	}
	status.Triage = &c4v1beta1.TriageDecision{Decision: decision.Decision, Reason: decision.Reason,
		Criteria: decision.Criteria, Question: decision.Question, Duplicate: int32(decision.Duplicate), Applied: applied,
		Environments: decision.Environments}
	status.Summary = decision.Reason
	if err := r.endRun(ctx, task, status); err != nil {
		return err
	}
	message := fmt.Sprintf("The triager decided %s: %s", decision.Decision, applied)
	r.finish(task, status, c4v1beta1.TaskMerged, metav1.ConditionTrue, reasonTriaged, message)
	log.Info("Triaged issue", "issue", issue.Number, "decision", decision.Decision, "applied", applied)
	return nil
}

// applyTriageActions writes a decision's actions to the issue and says what
// it did: the body, the comment, the close, then the status label. A host
// that won't close an issue with open dependencies leaves it open, said.
func (r *TaskReconciler) applyTriageActions(ctx context.Context, provider ports.Issues, repo ports.Repository,
	number int, actions model.TriageActions) (string, error) {
	applied := actions.Applied
	if actions.Body != "" {
		if err := provider.EditIssueBody(ctx, repo, number, actions.Body); err != nil {
			return "", err
		}
	}
	if err := provider.CommentIssue(ctx, repo, number, actions.Comment); err != nil {
		return "", err
	}
	if actions.Close {
		switch err := provider.CloseIssue(ctx, repo, number); {
		case errors.Is(err, ports.ErrOpenDependencies):
			applied = fmt.Sprintf("Left issue #%d open: the Git host won't close it while an issue it depends on is open",
				number)
		case err != nil:
			return "", err
		}
	}
	for _, l := range actions.EnvLabels {
		if err := provider.AddIssueLabel(ctx, repo, number, l); err != nil {
			return "", err
		}
	}
	if actions.Label != "" {
		if err := provider.AddIssueLabel(ctx, repo, number, actions.Label); err != nil {
			return "", err
		}
		// The exclusive scope drops it too, but not where the labels came over
		// non-exclusive from a migration (#2039).
		if err := provider.RemoveIssueLabel(ctx, repo, number, model.LabelProposed); err != nil {
			return "", err
		}
	}
	return applied, nil
}

// failTriage ends the run and fails the Triage Task, and says so on the
// issue, which stays status/proposed: its Crew starts another Triage Task
// until MaxIssueAttempts of them failed.
func (r *TaskReconciler) failTriage(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, step model.TaskStep) error {
	log := logf.FromContext(ctx)
	if err := r.endRun(ctx, task, status); err != nil {
		return err
	}
	why := step.Feedback
	if why == "" {
		why = step.Message
	}
	tries, err := r.failedTriages(ctx, task)
	if err != nil {
		return err
	}
	provider, repo, err := r.gitRepo(ctx, container)
	if err == nil {
		gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
		err = provider.CommentIssue(gitCtx, repo, int(task.Spec.Issue), model.TriageFailedComment(task.Name, why, tries))
		cancel()
	}
	if err != nil {
		log.Info("Could not comment on the issue", "issue", task.Spec.Issue, "error", err.Error())
	}
	status.Feedback = step.Feedback
	r.finish(task, status, c4v1beta1.TaskFailed, metav1.ConditionFalse, model.ReasonFailed, step.Message)
	log.Info("Failed Task", "reason", model.ReasonFailed, "message", step.Message)
	return nil
}

// failedTriages counts the failed Triage Tasks of task's issue, task with
// them.
func (r *TaskReconciler) failedTriages(ctx context.Context, task *c4v1beta1.Task) (int, error) {
	var tasks c4v1beta1.TaskList
	if err := r.List(ctx, &tasks, client.InNamespace(task.Namespace),
		client.MatchingFields{indexTaskContainer: task.Spec.ContainerRef.Name}); err != nil {
		return 0, err
	}
	failed := 1
	for _, t := range tasks.Items {
		if t.Name != task.Name && t.Spec.Kind == c4v1beta1.TaskTriage && t.Spec.Issue == task.Spec.Issue &&
			t.Status.Phase == c4v1beta1.TaskFailed {
			failed++
		}
	}
	return failed, nil
}

// answerTriage takes a person's answer from chat to the question of the
// Task's parked issue, its theos.factory.io/answer annotation, to the issue
// (model.PlanChatAnswer): a comment naming who answered, the person the
// annotation's -by companion names, which the admission policy holds to
// whoever wrote it, and the issue back to status/proposed, which the Crew
// then triages again, as the Task recorded the answer in status. An answer
// to no question, or from nobody, is left as it is; one answer is taken.
func (r *TaskReconciler) answerTriage(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container) error {
	if status.Triage == nil {
		return nil
	}
	answer := model.ChatWordOf(task.Annotations, model.ChatAnnotationAnswer)
	// Planned for an issue unread first: only an answer to take reads it.
	plan := model.PlanChatAnswer(status.Triage.Decision, status.Triage.AnsweredBy, answer, model.TriageIssue{})
	if plan.Ignore != "" {
		logf.FromContext(ctx).Info("Left an answer from chat as it is", "reason", plan.Ignore)
		return nil
	}
	if plan.Answered == "" {
		return nil
	}
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return err
	}
	n := int(task.Spec.Issue)
	var issue ports.Issue
	if err := r.gitCall(ctx, func(ctx context.Context) (err error) {
		issue, err = provider.GetIssue(ctx, repo, n)
		return err
	}); err != nil {
		return err
	}
	plan = model.PlanChatAnswer(status.Triage.Decision, status.Triage.AnsweredBy, answer,
		model.TriageIssue{Number: n, Body: issue.Body, Labels: issue.Labels, Closed: issue.State == stateClosed})
	var calls []func(context.Context) error
	if plan.Comment != "" {
		calls = append(calls, func(ctx context.Context) error { return provider.CommentIssue(ctx, repo, n, plan.Comment) })
	}
	if plan.Relabel {
		calls = append(calls,
			func(ctx context.Context) error { return provider.EnsureLabels(ctx, repo, issueLabels()) },
			func(ctx context.Context) error { return provider.AddIssueLabel(ctx, repo, n, model.LabelProposed) },
			// The exclusive scope drops it too, but not where the labels came
			// over non-exclusive from a migration (#2039).
			func(ctx context.Context) error { return provider.RemoveIssueLabel(ctx, repo, n, model.LabelNeedsHuman) })
	}
	for _, call := range calls {
		if err := r.gitCall(ctx, call); err != nil {
			return err
		}
	}
	status.Triage.AnsweredBy, status.Triage.Answered = answer.By, plan.Answered
	logf.FromContext(ctx).Info("Took an answer from chat to the issue", "issue", n, "by", answer.By,
		"answered", plan.Answered)
	return nil
}

// isTriage reports whether t is a Triage Task.
func isTriage(t *c4v1beta1.Task) bool { return t.Spec.Kind == c4v1beta1.TaskTriage }

// triageTasks are a Crew's Container's Triage Tasks as model.NextTriage
// reads them, and the rest.
func triageTasks(tasks []c4v1beta1.Task) (triage []model.TriageTask, rest []c4v1beta1.Task) {
	for i := range tasks {
		t := &tasks[i]
		if !isTriage(t) {
			rest = append(rest, *t)
			continue
		}
		triage = append(triage, model.TriageTask{Name: t.Name, Issue: int(t.Spec.Issue),
			Final: isFinal(t.Status.Phase), Failed: t.Status.Phase == c4v1beta1.TaskFailed,
			Answered: t.Status.Triage != nil && t.Status.Triage.AnsweredBy != ""})
	}
	slices.SortFunc(triage, func(a, b model.TriageTask) int { return a.Issue - b.Issue })
	return triage, rest
}
