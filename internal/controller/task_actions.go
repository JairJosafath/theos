/*
Copyright 2026.

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
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// apply does one step and records it in status.
func (r *TaskReconciler) apply(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, agent *c4v1alpha1.Agent, step model.TaskStep) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	switch step.Action {
	case model.ActionWait:
		r.progress(task, status, metav1.ConditionTrue, "Waiting", step.Message)
		if status.Phase == c4v1alpha1.TaskChecking {
			return ctrl.Result{RequeueAfter: r.checksRecheck()}, nil
		}
		return ctrl.Result{}, nil

	case model.ActionStartImplement, model.ActionStartReview:
		role := model.RoleImplementer
		if step.Action == model.ActionStartReview {
			role = model.RoleReviewer
		}
		return r.startRun(ctx, task, status, container, agent, role, step.Message)

	case model.ActionOpenPR:
		provider, repo, err := r.gitRepo(ctx, container)
		if err != nil {
			return ctrl.Result{}, err
		}
		run, err := runState(ctx, r.Client, task.Namespace, status.Sandbox)
		if err != nil || run.Result == nil {
			return ctrl.Result{}, fmt.Errorf("reading the implement result of %s: %v", status.Sandbox, err)
		}
		gitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		pr, err := provider.OpenPullRequest(gitCtx, repo, status.Branch, gitprovider.DefaultBranch,
			pullTitle(task), pullBody(task))
		if err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Opened pull request", "url", pr.URL, "commit", run.Result.Commit, "round", status.Round)
		status.Commit, status.Summary = run.Result.Commit, run.Result.Summary
		status.PullRequest, status.PullRequestURL = int32(pr.Number), pr.URL
		status.Checks = nil
		status.Phase = c4v1alpha1.TaskChecking
		r.progress(task, status, metav1.ConditionTrue, "Checking", step.Message+"; waiting for checks")
		return ctrl.Result{RequeueAfter: r.checksRecheck()}, r.endRun(ctx, task, status)

	case model.ActionRetryReview:
		r.progress(task, status, metav1.ConditionTrue, "ReviewerFailed", step.Message)
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		status.ReviewAttempts++
		return ctrl.Result{}, nil // the status write triggers the next step

	case model.ActionMerge:
		return r.merge(ctx, task, status, container, step)

	case model.ActionRetry:
		review := r.reviewFrom(ctx, task, status)
		r.comment(ctx, task, status, container, fmt.Sprintf("Round %d was not accepted.\n\n%s", status.Round, step.Feedback))
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		if review != nil {
			status.Review = review
		}
		status.Round++
		status.Feedback = step.Feedback
		status.ReviewAttempts = 0
		status.Checks = nil
		status.Phase = c4v1alpha1.TaskImplementing
		r.progress(task, status, metav1.ConditionTrue, "NextRound", step.Message)
		log.Info("Started next round of Task", "round", status.Round, "feedback", firstLine(step.Feedback))
		return ctrl.Result{}, nil // the status write triggers the next step

	case model.ActionFail:
		if review := r.reviewFrom(ctx, task, status); review != nil {
			status.Review = review
		}
		r.comment(ctx, task, status, container, "The Task failed. "+step.Message)
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		status.Feedback = step.Feedback
		r.finish(task, status, c4v1alpha1.TaskFailed, metav1.ConditionFalse, "Failed", step.Message)
		log.Info("Failed Task", "message", step.Message)
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, nil
}

// startRun creates the sandbox for this round's role, after making sure the
// Container's agent Git access and the Agent's credentials exist.
func (r *TaskReconciler) startRun(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, agent *c4v1alpha1.Agent, role, message string) (ctrl.Result, error) {
	if ref := agent.Spec.CredentialsSecretRef; ref != nil {
		var secret corev1.Secret
		err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: ref.Name}, &secret)
		if apierrors.IsNotFound(err) {
			r.progress(task, status, metav1.ConditionFalse, "CredentialsNotFound",
				fmt.Sprintf("Secret %s of Agent %s not found", ref.Name, agent.Name))
			return ctrl.Result{RequeueAfter: taskSecretRecheck}, nil
		}
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return ctrl.Result{}, err
	}
	gitSecret, err := r.ensureAgentGit(ctx, container, provider, repo)
	if err != nil {
		return ctrl.Result{}, err
	}

	if status.Round == 0 {
		status.Round = 1
	}
	status.Branch = model.TaskBranch(task.Name)
	if role == model.RoleImplementer {
		status.Phase = c4v1alpha1.TaskImplementing
	} else {
		status.Phase = c4v1alpha1.TaskReviewing
	}
	name := sandboxName(task, status, role)
	sandbox, err := buildSandbox(sandboxRun{
		task: task, container: container, agent: agent, name: name, role: role,
		repoURL: repo.CloneURL, gitSecret: gitSecret, context: r.taskContext(ctx, container),
	}, r.Scheme, r.now())
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, sandbox); err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, err
	}
	status.Sandbox = name
	r.progress(task, status, metav1.ConditionTrue, "Running", fmt.Sprintf("%s in sandbox %s", message, name))
	logf.FromContext(ctx).Info("Started agent sandbox", "sandbox", name, "role", role, "round", status.Round)
	return ctrl.Result{}, nil
}

// ensureAgentGit returns the Secret with the Container's agent Git credentials,
// creating the bot user and the Secret the first time. The Secret is owned by the
// Container, so it goes when the Container goes.
func (r *TaskReconciler) ensureAgentGit(ctx context.Context, container *c4v1alpha1.Container,
	provider gitprovider.Provider, repo gitprovider.Repository) (string, error) {
	name := container.Name + "-agent-git"
	var secret corev1.Secret
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: container.Namespace, Name: name}, &secret)
	if err == nil && len(secret.Data["token"]) > 0 {
		return name, nil
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return "", err
	}
	found := err == nil
	gitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	creds, err := provider.EnsureAgentAccess(gitCtx, repo, model.AgentUsername(container.Namespace, container.Name))
	if err != nil {
		return "", err
	}
	// Written without reading through the cache: the operator doesn't watch Secrets.
	desired := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: container.Namespace, Name: name, Labels: map[string]string{
			c4v1alpha1.LabelProject: container.Namespace, c4v1alpha1.LabelContainer: container.Name}},
		Data: map[string][]byte{"username": []byte(creds.Username), "token": []byte(creds.Token)},
	}
	if err := controllerutil.SetControllerReference(container, &desired, r.Scheme); err != nil {
		return "", err
	}
	if found {
		orig := secret.DeepCopy()
		secret.Labels, secret.Data, secret.OwnerReferences = desired.Labels, desired.Data, desired.OwnerReferences
		err = r.Patch(ctx, &secret, client.MergeFrom(orig))
	} else {
		err = r.Create(ctx, &desired)
	}
	if err != nil {
		return "", err
	}
	logf.FromContext(ctx).Info("Created agent Git access", "user", creds.Username, "secret", name)
	return name, nil
}

// merge squashes the approved pull request into main.
func (r *TaskReconciler) merge(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, step model.TaskStep) (ctrl.Result, error) {
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return ctrl.Result{}, err
	}
	status.Review = r.reviewFrom(ctx, task, status)
	gitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pr, err := provider.GetPullRequest(gitCtx, repo, int(status.PullRequest))
	if err != nil {
		return ctrl.Result{}, err
	}
	if !pr.Merged {
		if pr.HeadSHA != "" && pr.HeadSHA != status.Commit {
			// Someone pushed after the review: check and review that commit instead.
			status.Commit, status.Checks, status.Phase = pr.HeadSHA, nil, c4v1alpha1.TaskChecking
			r.progress(task, status, metav1.ConditionTrue, "BranchMoved",
				"The branch moved after the review; checking "+pr.HeadSHA)
			return ctrl.Result{RequeueAfter: r.checksRecheck()}, r.endRun(ctx, task, status)
		}
		if status.Review != nil && status.Review.Summary != "" {
			_ = provider.CommentPullRequest(gitCtx, repo, pr.Number, "Review: approved.\n\n"+status.Review.Summary)
		}
		if err := provider.MergePullRequest(gitCtx, repo, pr.Number, pullTitle(task),
			fmt.Sprintf("Task %s, round %d.", task.Name, status.Round)); err != nil {
			return ctrl.Result{}, err
		}
	}
	if err := r.endRun(ctx, task, status); err != nil {
		return ctrl.Result{}, err
	}
	r.finish(task, status, c4v1alpha1.TaskMerged, metav1.ConditionTrue, "Merged", step.Message)
	logf.FromContext(ctx).Info("Merged Task", "url", status.PullRequestURL, "round", status.Round)
	return ctrl.Result{}, nil
}

// reviewFrom reads the verdict of the current review sandbox, if any.
func (r *TaskReconciler) reviewFrom(ctx context.Context, task *c4v1alpha1.Task,
	status *c4v1alpha1.TaskStatus) *c4v1alpha1.TaskReview {
	if status.Phase != c4v1alpha1.TaskReviewing || status.Sandbox == "" {
		return status.Review
	}
	run, err := runState(ctx, r.Client, task.Namespace, status.Sandbox)
	if err != nil || run.Result == nil || run.Result.Verdict == "" {
		return status.Review
	}
	return &c4v1alpha1.TaskReview{Verdict: run.Result.Verdict, Summary: run.Result.Summary}
}

// endRun deletes the current sandbox, once its result is in status.
func (r *TaskReconciler) endRun(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus) error {
	if status.Sandbox == "" {
		return nil
	}
	sandbox := newSandbox()
	sandbox.SetNamespace(task.Namespace)
	sandbox.SetName(status.Sandbox)
	if err := r.Delete(ctx, sandbox, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil &&
		!apierrors.IsNotFound(err) {
		return err
	}
	status.Sandbox = ""
	return nil
}

func (r *TaskReconciler) finish(task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus, phase c4v1alpha1.TaskPhase,
	succeeded metav1.ConditionStatus, reason, message string) {
	now := metav1.NewTime(r.now())
	status.Phase, status.CompletedAt = phase, &now
	r.progress(task, status, metav1.ConditionFalse, reason, message)
	setCondition(&status.Conditions, task.Generation, ConditionSucceeded, succeeded, reason, message)
}

// comment adds a note to the Task's pull request, if it has one. The pull request
// is a report; failing to comment doesn't stop the Task.
func (r *TaskReconciler) comment(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, body string) {
	if status.PullRequest == 0 {
		return
	}
	provider, repo, err := r.gitRepo(ctx, container)
	if err == nil {
		gitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = provider.CommentPullRequest(gitCtx, repo, int(status.PullRequest), body)
		cancel()
	}
	if err != nil {
		logf.FromContext(ctx).Info("Could not comment on pull request", "task", task.Name, "error", err.Error())
	}
}

func pullTitle(task *c4v1alpha1.Task) string {
	return firstLine(task.Spec.Goal)
}

func pullBody(task *c4v1alpha1.Task) string {
	return fmt.Sprintf("Task `%s`, done by Agent `%s`.\n\n## Goal\n\n%s\n", task.Name, task.Spec.AgentRef.Name, task.Spec.Goal)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if len(line) > 72 {
		line = line[:69] + "..."
	}
	return line
}
