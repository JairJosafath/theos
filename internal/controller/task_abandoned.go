/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"errors"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// defaultPullRequestRecheck: a Failed Task's pull request waits on its issue
// and on newer tries, and the Git host doesn't call back when an issue closes.
const defaultPullRequestRecheck = 10 * time.Minute

// tendAbandonedPull closes a Failed Task's pull request once no later try will
// continue it, saying why on it, and records how it ended so the Task stops
// looking. The branch stays. A Git host that can't be reached is tried again
// at the next look.
func (r *TaskReconciler) tendAbandonedPull(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container) ctrl.Result {
	if status.PullRequest == 0 || status.PullRequestEnded != "" {
		return ctrl.Result{}
	}
	log := logf.FromContext(ctx)
	again := ctrl.Result{RequeueAfter: r.pullRequestRecheck()}
	facts, err := r.abandonedPullFacts(ctx, task, status, container)
	if err != nil {
		log.Error(err, "Failed to read whether a Failed Task's pull request is still wanted",
			"pullRequest", status.PullRequest)
		return again
	}
	end := model.AbandonedPull(facts)
	if end.Close {
		provider, repo, err := r.gitRepo(ctx, container)
		if err == nil {
			n := int(status.PullRequest)
			if err = r.gitCall(ctx, func(ctx context.Context) error {
				return provider.CommentPullRequest(ctx, repo, n, end.Comment)
			}); err == nil {
				err = r.gitCall(ctx, func(ctx context.Context) error { return provider.ClosePullRequest(ctx, repo, n) })
			}
		}
		if err != nil {
			log.Error(err, "Failed to close a Failed Task's pull request", "pullRequest", status.PullRequest)
			return again
		}
		log.Info("Closed a Failed Task's pull request", "pullRequest", status.PullRequest, "why", end.Ended)
	}
	if end.Ended == "" {
		return again
	}
	status.PullRequestEnded = end.Ended
	return ctrl.Result{}
}

// abandonedPullFacts reads the pull request and its issue on the Git host, and
// the Container's Tasks at the same issue.
func (r *TaskReconciler) abandonedPullFacts(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container) (model.AbandonedPullFacts, error) {
	facts := model.AbandonedPullFacts{Task: task.Name, Issue: int(task.Spec.Issue), PullRequest: int(status.PullRequest)}
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return facts, err
	}
	var pr ports.PullRequest
	if err := r.gitCall(ctx, func(ctx context.Context) (err error) {
		pr, err = provider.GetPullRequest(ctx, repo, facts.PullRequest)
		return err
	}); err != nil {
		return facts, err
	}
	facts.PullOpen, facts.PullMerged = pr.State == stateOpen && !pr.Merged, pr.Merged
	if !facts.PullOpen || facts.Issue == 0 {
		return facts, nil
	}
	var issue ports.Issue
	err = r.gitCall(ctx, func(ctx context.Context) (err error) {
		issue, err = provider.GetIssue(ctx, repo, facts.Issue)
		return err
	})
	switch {
	case errors.Is(err, ports.ErrIssueNotFound):
		facts.IssueClosed = true // deleted: no try comes back to it
	case err != nil:
		return facts, err
	default:
		facts.IssueClosed = issue.State == stateClosed
	}
	// Read live: a newer try that took this pull request up a moment ago must
	// keep it, and the cache may not have its status yet.
	var tasks c4v1beta1.TaskList
	if err := r.APIReader.List(ctx, &tasks, client.InNamespace(task.Namespace)); err != nil {
		return facts, err
	}
	for _, t := range tasks.Items {
		if t.Spec.ContainerRef.Name != container.Name || t.Spec.Issue != task.Spec.Issue ||
			t.Spec.Kind == c4v1beta1.TaskPlan || t.Spec.Kind == c4v1beta1.TaskTriage || !sameKind(&t, task) {
			continue
		}
		if t.Name == task.Name {
			t.Status = *status
		}
		facts.Tries = append(facts.Tries, model.IssueTry{Name: t.Name, Created: t.CreationTimestamp.Unix(),
			Final: isFinal(t.Status.Phase), Failed: t.Status.Phase == c4v1beta1.TaskFailed, NoCI: failedNoCI(&t.Status),
			PullRequest: int(t.Status.PullRequest), PullRequestURL: t.Status.PullRequestURL})
	}
	return facts, nil
}

func (r *TaskReconciler) pullRequestRecheck() time.Duration {
	if r.PullRequestRecheck > 0 {
		return r.PullRequestRecheck
	}
	return defaultPullRequestRecheck
}
