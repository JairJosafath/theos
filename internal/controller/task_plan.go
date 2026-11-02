/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// reconcilePlan takes the next step of a Plan Task, one round of a Brief
// (docs/plans/done/brief.md, step 5): the planner pushes a plan to the brief
// repository's plan/<round>, and the Brief checks it. The Brief made the Task,
// owned and labelled; its verdict comes back as annotations on the Task.
func (r *TaskReconciler) reconcilePlan(ctx context.Context, task *c4v1beta1.Task) (ctrl.Result, error) {
	status := task.Status.DeepCopy()
	if status.Phase == "" {
		status.Phase = c4v1beta1.TaskPending
	}
	if isFinal(status.Phase) {
		return ctrl.Result{}, r.writeStatus(ctx, task, status)
	}

	var brief c4v1beta1.Brief
	err := r.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: task.Spec.BriefRef.Name}, &brief)
	if apierrors.IsNotFound(err) {
		r.progress(task, status, metav1.ConditionFalse, "BriefNotFound", "Brief "+task.Spec.BriefRef.Name+" not found")
		return ctrl.Result{}, r.writeStatus(ctx, task, status)
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	round, err := strconv.Atoi(task.Labels[c4v1beta1.LabelBriefRound])
	if err != nil || round < 1 {
		message := fmt.Sprintf("A Plan Task needs label %s with the Brief's round, a number from 1; it has %q",
			c4v1beta1.LabelBriefRound, task.Labels[c4v1beta1.LabelBriefRound])
		if err := r.failPlan(ctx, task, status, model.TaskStep{Action: model.ActionFail, Message: message}); err != nil {
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

	facts, err := r.planFacts(ctx, task, status, &agent)
	if err != nil {
		return ctrl.Result{}, err
	}
	step := model.NextTaskStep(facts)
	logf.FromContext(ctx).V(1).Info("Took Task step", "phase", status.Phase, "round", status.Round,
		"action", step.Action, "message", step.Message)
	result, err := r.applyPlan(ctx, task, status, &brief, &agent, round, step)
	if err != nil {
		return result, err
	}
	r.queueForSlot(status, step, &result)
	return result, r.writeStatus(ctx, task, status)
}

// planFacts gathers what a Plan Task's next step depends on.
func (r *TaskReconciler) planFacts(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	agent *c4v1beta1.Agent) (model.TaskFacts, error) {
	facts := model.TaskFacts{
		Phase:     string(status.Phase),
		Round:     int(status.Round),
		MaxRounds: int(agent.Spec.MaxRounds),
		Reruns:    int(status.Reruns),
		Resumes:   int(status.Resumes),
		Feedback:  status.Feedback,
		Plan:      true,
	}
	if facts.MaxRounds == 0 {
		facts.MaxRounds = 3
	}
	// A verdict counts only for the plan this try pushed.
	if checked := task.Annotations[c4v1beta1.AnnotationPlanChecked]; checked != "" && checked == status.Commit {
		facts.PlanChecked = true
		facts.PlanRefusals = task.Annotations[c4v1beta1.AnnotationPlanRefusals]
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
	if status.Phase == c4v1beta1.TaskImplementing {
		name := sandboxName(task, status, model.RolePlanner)
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

// applyPlan does one step of a Plan Task and records it in status.
func (r *TaskReconciler) applyPlan(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	brief *c4v1beta1.Brief, agent *c4v1beta1.Agent, round int, step model.TaskStep) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	switch step.Action {
	case model.ActionWait:
		if status.Phase == c4v1beta1.TaskChecking {
			// The Brief's verdict is an annotation on the Task, which wakes it.
			r.progress(task, status, metav1.ConditionTrue, "WaitingForBrief", step.Message)
			return ctrl.Result{}, nil
		}
		return r.wait(task, status, step), nil

	case model.ActionStartImplement:
		return r.startPlan(ctx, task, status, brief, agent, round, step.Message)

	case model.ActionRerun, model.ActionAgentUnavailable, model.ActionSuspendRun:
		// None touches the Container, which a Plan Task doesn't have.
		return r.apply(ctx, task, status, nil, taskAgents{implementer: agent, reviewer: agent}, step)

	case model.ActionResumeRun:
		fresh, result, err := r.planSandbox(ctx, task, status, brief, agent, round, status.Sandbox)
		if fresh == nil {
			return result, err
		}
		return ctrl.Result{}, r.resumeWith(ctx, task, status, agent, fresh, step)

	case model.ActionAwaitPlanCheck:
		run, err := runState(ctx, r.Client, task.Namespace, status.Sandbox, status.SandboxPod)
		if err != nil || run.Result == nil {
			return ctrl.Result{}, fmt.Errorf("reading the plan result of %s: %v", status.Sandbox, err)
		}
		setProgress(status, model.PlanPushed(progressOf(status), run.Result.Commit))
		status.Summary = run.Result.Summary
		r.progress(task, status, metav1.ConditionTrue, "WaitingForBrief", step.Message)
		log.Info("Planner pushed a plan", "commit", run.Result.Commit, "round", status.Round, "briefRound", round)
		return ctrl.Result{}, r.endRun(ctx, task, status)

	case model.ActionPlanAccepted:
		r.finish(task, status, c4v1beta1.TaskMerged, metav1.ConditionTrue, "PlanAccepted", step.Message)
		log.Info("Brief accepted the plan of Task", "commit", status.Commit, "round", status.Round)
		return ctrl.Result{}, nil

	case model.ActionRetry:
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		setProgress(status, model.NextRound(progressOf(status), step.Feedback))
		r.progress(task, status, metav1.ConditionTrue, "NextRound", step.Message)
		log.Info("Started next round of Task", "round", status.Round, "feedback", firstLine(step.Feedback))
		return ctrl.Result{}, nil // the status write triggers the next step

	case model.ActionFail:
		return ctrl.Result{}, r.failPlan(ctx, task, status, step)
	}
	return ctrl.Result{}, nil
}

// failPlan ends the run and fails the Plan Task; its Brief reads why from it.
func (r *TaskReconciler) failPlan(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	step model.TaskStep) error {
	if err := r.endRun(ctx, task, status); err != nil {
		return err
	}
	status.Feedback = step.Feedback
	r.finish(task, status, c4v1beta1.TaskFailed, metav1.ConditionFalse, "Failed", step.Message)
	logf.FromContext(ctx).Info("Failed Task", "reason", "Failed", "message", step.Message)
	return nil
}

// startPlan creates the planner's sandbox for this try, on the brief
// repository with the Brief's bot. The Brief makes the bot's Secret before it
// starts a round; until it is there, the Task waits.
func (r *TaskReconciler) startPlan(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	brief *c4v1beta1.Brief, agent *c4v1beta1.Agent, round int, message string) (ctrl.Result, error) {
	progress := model.StartRun(progressOf(status), model.RolePlanner)
	next := status.DeepCopy()
	setProgress(next, progress)
	name := sandboxName(task, next, model.RolePlanner)
	sandbox, result, err := r.planSandbox(ctx, task, status, brief, agent, round, name)
	if sandbox == nil {
		return result, err
	}
	setProgress(status, progress)
	status.Branch = model.PlanBranch(round)
	if err := r.Create(ctx, sandbox); err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, err
	}
	status.Sandbox, status.SandboxPod, status.AgentBackAt = name, "", nil
	r.progress(task, status, metav1.ConditionTrue, "Running", fmt.Sprintf("%s in sandbox %s", message, name))
	logf.FromContext(ctx).Info("Started agent sandbox", "sandbox", name, "role", model.RolePlanner,
		"round", status.Round, "briefRound", round)
	return ctrl.Result{}, nil
}

// planSandbox builds the planner's Sandbox named name, once the Agent's
// credentials and the Brief bot's Secret are there; until then it is nil,
// and result says when to look again.
func (r *TaskReconciler) planSandbox(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	brief *c4v1beta1.Brief, agent *c4v1beta1.Agent, round int, name string) (*unstructured.Unstructured, ctrl.Result, error) {
	if found, err := r.agentCredentials(ctx, task, status, agent); err != nil {
		return nil, ctrl.Result{}, err
	} else if !found {
		return nil, ctrl.Result{RequeueAfter: r.secretRecheck()}, nil
	}
	gitSecret := briefGitSecret(brief.Name)
	var secret corev1.Secret
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: gitSecret}, &secret)
	if apierrors.IsNotFound(err) {
		r.progress(task, status, metav1.ConditionFalse, "CredentialsNotFound",
			fmt.Sprintf("Secret %s of Brief %s not found; the Brief makes it with its repository", gitSecret, brief.Name))
		return nil, ctrl.Result{RequeueAfter: r.secretRecheck()}, nil
	}
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, task.Namespace, brief.Spec.GitProviderRef.Name)
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	repo := provider.Repository(model.BriefRepository(brief.Name))
	run := status.DeepCopy()
	run.Branch = model.PlanBranch(round)
	sandbox, err := buildSandbox(sandboxRun{
		task: task, status: run, agent: agent, name: name, role: model.RolePlanner,
		repoURL: repo.CloneURL, gitSecret: gitSecret, goal: task.Spec.Goal, maxRounds: agent.Spec.MaxRounds,
		brief: brief.Name, briefRound: round, workspaceMemory: r.WorkspaceMemory, checkpointClass: r.CheckpointClass,
		pullSecrets: r.SharedPullSecrets, modelProxyURL: r.sandboxModelProxy(agent),
	}, r.Scheme, r.now())
	return sandbox, ctrl.Result{}, err
}
