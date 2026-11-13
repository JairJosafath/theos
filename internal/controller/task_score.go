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
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// reasonScored is the Succeeded reason of a Score Task whose judge gave its
// verdicts.
const reasonScored = "Scored"

// reconcileScore takes the next step of a Score Task
// (docs/plans/done/measure-and-improve.md, step 4): one judge run on the judged
// change, on a clone it pushes nothing from, whose verdicts the Task keeps.
// Its Scorer made and owns it, so its Container doesn't adopt it; a failed run
// fails it with every criterion Unclear, and no second try.
func (r *TaskReconciler) reconcileScore(ctx context.Context, task *c4v1beta1.Task) (ctrl.Result, error) {
	status := task.Status.DeepCopy()
	if status.Phase == "" {
		status.Phase = c4v1beta1.TaskPending
	}
	if isFinal(status.Phase) {
		return ctrl.Result{}, r.writeStatus(ctx, task, status)
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
	var agent c4v1beta1.Agent
	err = r.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: task.Spec.AgentRef.Name}, &agent)
	if apierrors.IsNotFound(err) {
		r.progress(task, status, metav1.ConditionFalse, "AgentNotFound", "Agent "+task.Spec.AgentRef.Name+" not found")
		return ctrl.Result{}, r.writeStatus(ctx, task, status)
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	facts, err := r.scoreFacts(ctx, task, status)
	if err != nil {
		return ctrl.Result{}, err
	}
	step := model.NextTaskStep(facts)
	logf.FromContext(ctx).V(1).Info("Took Task step", "phase", status.Phase, "action", step.Action,
		"message", step.Message)
	result, err := r.applyScore(ctx, task, status, &container, &agent, step)
	if err != nil {
		return result, err
	}
	r.queueForSlot(status, step, &result)
	return result, r.writeStatus(ctx, task, status)
}

// scoreFacts gathers what a Score Task's next step depends on: its one run
// and the slots.
func (r *TaskReconciler) scoreFacts(ctx context.Context, task *c4v1beta1.Task,
	status *c4v1beta1.TaskStatus) (model.TaskFacts, error) {
	facts := model.TaskFacts{
		Phase:     string(status.Phase),
		Container: task.Spec.ContainerRef.Name,
		Round:     int(status.Round),
		MaxRounds: 1,
		Reruns:    int(status.Reruns),
		Resumes:   int(status.Resumes),
		Feedback:  status.Feedback,
		Score:     true,
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
		name := sandboxName(task, status, model.RoleScorer)
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

// applyScore does one step of a Score Task and records it in status.
func (r *TaskReconciler) applyScore(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agent *c4v1beta1.Agent, step model.TaskStep) (ctrl.Result, error) {
	agents := taskAgents{implementer: agent, reviewer: agent}
	switch step.Action {
	case model.ActionWait:
		return r.wait(ctx, task, status, step), nil

	case model.ActionStartImplement:
		return r.startScore(ctx, task, status, container, agents, step.Message)

	case model.ActionRerun, model.ActionRerunPaused, model.ActionAgentUnavailable, model.ActionSuspendRun,
		model.ActionPauseRun, model.ActionSuspendForAgent:
		// None reads the role, which a judge run's sandbox name holds.
		return r.apply(ctx, task, status, container, agents, step)

	case model.ActionResumeRun:
		in, result, err := r.runInputs(ctx, task, status, container, agents, model.RoleScorer)
		if in == nil {
			return result, err
		}
		fresh, err := r.runSandbox(ctx, task, status, container, agents, model.RoleScorer, status.Sandbox, in, nil)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, r.resumeWith(ctx, task, status, agent, fresh, step)

	case model.ActionScored:
		return ctrl.Result{}, r.recordScore(ctx, task, status)

	case model.ActionFail:
		return ctrl.Result{}, r.failScore(ctx, task, status, step)
	}
	return ctrl.Result{}, nil
}

// startScore creates the judge's sandbox on the judged Container's
// repository. The run clones the base and pushes nothing.
func (r *TaskReconciler) startScore(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents, message string) (ctrl.Result, error) {
	in, result, err := r.runInputs(ctx, task, status, container, agents, model.RoleScorer)
	if in == nil {
		return result, err
	}
	setProgress(status, model.StartRun(progressOf(status), model.RoleScorer))
	name := sandboxName(task, status, model.RoleScorer)
	sandbox, err := r.runSandbox(ctx, task, status, container, agents, model.RoleScorer, name, in, nil)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, sandbox); err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, err
	}
	status.Sandbox, status.SandboxPod, status.AgentBackAt = name, "", nil
	r.progress(task, status, metav1.ConditionTrue, "Running", fmt.Sprintf("%s in sandbox %s", message, name))
	logf.FromContext(ctx).Info("Started agent sandbox", "sandbox", name, "role", model.RoleScorer,
		"judged", task.Spec.Score.TaskRef.Name)
	return ctrl.Result{}, nil
}

// recordScore keeps the judge's verdicts and ends the Task Merged.
func (r *TaskReconciler) recordScore(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus) error {
	run, err := runState(ctx, r.Client, task.Namespace, status.Sandbox, status.SandboxPod)
	if err != nil || run.Result == nil || run.Result.Score == nil {
		return fmt.Errorf("reading the verdicts of %s: %v", status.Sandbox, err)
	}
	status.Score = scoreStatus(task.Spec.Score.Criteria, run.Result.Score)
	if err := r.endRun(ctx, task, status); err != nil {
		return err
	}
	pass, fail, unclear := 0, 0, 0
	for _, c := range status.Score.Criteria {
		switch c.Verdict {
		case c4v1beta1.ScorePass:
			pass++
		case c4v1beta1.ScoreFail:
			fail++
		default:
			unclear++
		}
	}
	message := fmt.Sprintf("Judged %s: %d passed, %d failed, %d unclear", task.Spec.Score.TaskRef.Name, pass, fail, unclear)
	status.Summary = message
	r.finish(task, status, c4v1beta1.TaskMerged, metav1.ConditionTrue, reasonScored, message)
	logf.FromContext(ctx).Info("Scored Task", "judged", task.Spec.Score.TaskRef.Name, "pass", pass, "fail", fail,
		"unclear", unclear)
	return nil
}

// failScore ends the run and fails the Score Task, with every criterion
// Unclear and why.
func (r *TaskReconciler) failScore(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	step model.TaskStep) error {
	if err := r.endRun(ctx, task, status); err != nil {
		return err
	}
	why := step.Feedback
	if why == "" {
		why = step.Message
	}
	status.Score = scoreStatus(task.Spec.Score.Criteria, model.UnclearScore(task.Spec.Score.Criteria,
		"The judge run failed: "+why))
	status.Feedback = step.Feedback
	r.finish(task, status, c4v1beta1.TaskFailed, metav1.ConditionFalse, model.ReasonFailed, step.Message)
	logf.FromContext(ctx).Info("Failed Task", "reason", model.ReasonFailed, "message", step.Message)
	return nil
}

// scoreStatus is the verdicts as a Score Task keeps them, under the criteria's
// own texts.
func scoreStatus(criteria []string, verdicts []model.ScoreCriterion) c4v1beta1.TaskScoreStatus {
	out := c4v1beta1.TaskScoreStatus{}
	for i, v := range verdicts {
		if i >= len(criteria) {
			break
		}
		out.Criteria = append(out.Criteria, c4v1beta1.TaskScoreCriterion{Criterion: criteria[i],
			Verdict: c4v1beta1.ScoreVerdict(v.Verdict), Reason: v.Reason})
	}
	return out
}
