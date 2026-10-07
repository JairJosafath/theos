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
	"strings"

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// The Git host is where people follow the work: issues say who works on them and
// how it went, pull requests carry the reviews. None of it decides what the Task
// does next, so a failed note is logged and the Task goes on.

// stateClosed is the state of a closed issue or pull request on the Git host.
const stateClosed = "closed"

// agentName is how comments name an Agent: its name, and its model when set.
func agentName(agent *c4v1alpha1.Agent) string {
	if agent.Spec.Model == "" {
		return "Agent `" + agent.Name + "`"
	}
	return fmt.Sprintf("Agent `%s` (%s)", agent.Name, agent.Spec.Model)
}

// announce marks an issue as taken: in progress, assigned to the Container's
// agent bot, with a comment naming the Task and its Agents.
func (r *TaskReconciler) announce(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	task *c4v1alpha1.Task, container *c4v1alpha1.Container, agents taskAgents) {
	n := int(task.Spec.Issue)
	notes := []error{
		provider.EnsureLabels(ctx, repo, issueLabels()),
		provider.AddIssueLabel(ctx, repo, n, model.LabelInProgress),
		provider.AssignIssue(ctx, repo, n, []string{model.AgentUsername(container.Namespace, container.Name)}),
		provider.CommentIssue(ctx, repo, n, fmt.Sprintf(
			"Task `%s` picked this up: %s implements, %s reviews, in up to %d rounds.",
			task.Name, agentName(agents.implementer), agentName(agents.reviewer), agents.implementer.Spec.MaxRounds)),
	}
	logNotes(ctx, task, notes)
}

// review posts the reviewer's verdict as a review of the round's commit.
func (r *TaskReconciler) review(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, reviewer *c4v1alpha1.Agent, event, summary string) {
	if status.PullRequest == 0 {
		return
	}
	provider, repo, err := r.gitRepo(ctx, container)
	if err == nil {
		gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
		body := fmt.Sprintf("Review by %s, round %d.\n\n%s", agentName(reviewer), status.Round, summary)
		err = provider.ReviewPullRequest(gitCtx, repo, int(status.PullRequest), status.Commit, event, body)
		cancel()
	}
	logNotes(ctx, task, []error{err})
}

// closeIssue reports the merge on the issue, files the reviewer's follow-ups as
// proposed issues in the Later milestone, outside any release, and closes it. It
// returns the follow-ups' addresses.
func (r *TaskReconciler) closeIssue(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus, reviewer *c4v1alpha1.Agent, followUps []model.FollowUp) []string {
	n := int(task.Spec.Issue)
	var filed []string
	var notes []error
	var milestone int64
	if len(followUps) > 0 {
		err := r.gitCall(ctx, func(ctx context.Context) (err error) {
			milestone, err = provider.EnsureMilestone(ctx, repo, model.FollowUpMilestone, model.FollowUpMilestoneDescription)
			return err
		})
		if err != nil {
			milestone = 0 // file them without it
		}
		notes = append(notes, r.gitCall(ctx, func(ctx context.Context) error {
			return provider.EnsureLabels(ctx, repo, issueLabels())
		}), err)
	}
	for _, f := range followUps {
		body := model.FollowUpIssue(f, agentName(reviewer), status.PullRequestURL, n, task.Name)
		var issue gitprovider.Issue
		err := r.gitCall(ctx, func(ctx context.Context) (err error) {
			issue, err = provider.CreateIssue(ctx, repo, f.Title, body, []string{model.LabelProposed}, milestone)
			return err
		})
		notes = append(notes, err)
		if err == nil {
			filed = append(filed, issue.URL)
		}
	}
	comment := fmt.Sprintf("Merged in %s after %d %s.", status.PullRequestURL, status.Round, plural(int(status.Round), "round", "rounds"))
	if len(filed) > 0 {
		comment += "\n\nThe reviewer suggested follow-ups, filed as " + model.LabelProposed + ":\n- " + strings.Join(filed, "\n- ")
	}
	notes = append(notes, r.endIssue(ctx, provider, repo, n, comment)...)
	logNotes(ctx, task, notes)
	return filed
}

// endIssue says on an issue how its Task ended, takes it out of progress and
// closes it. It returns the notes' errors.
func (r *TaskReconciler) endIssue(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository, n int,
	comment string) []error {
	return []error{
		r.gitCall(ctx, func(ctx context.Context) error { return provider.CommentIssue(ctx, repo, n, comment) }),
		r.gitCall(ctx, func(ctx context.Context) error {
			return provider.RemoveIssueLabel(ctx, repo, n, model.LabelInProgress)
		}),
		r.gitCall(ctx, func(ctx context.Context) error { return provider.CloseIssue(ctx, repo, n) }),
	}
}

// handBack gives a failed Task's issue back to a person, saying why.
func (r *TaskReconciler) handBack(ctx context.Context, task *c4v1alpha1.Task, container *c4v1alpha1.Container, why string) {
	if task.Spec.Issue == 0 {
		return
	}
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		logNotes(ctx, task, []error{err})
		return
	}
	n := int(task.Spec.Issue)
	again := fmt.Sprintf("clarify or split the issue and label it %s", model.LabelReady)
	if brief := container.Labels[c4v1alpha1.LabelBrief]; brief != "" {
		again += fmt.Sprintf(", or say in a prompt on Brief `%s`", brief)
	}
	comment := fmt.Sprintf("Task `%s` failed: %s\n\nTo try again, %s; a Crew tries an issue up to %d times.",
		task.Name, why, again, model.MaxIssueAttempts)
	logNotes(ctx, task, []error{
		r.gitCall(ctx, func(ctx context.Context) error { return provider.EnsureLabels(ctx, repo, issueLabels()) }),
		r.gitCall(ctx, func(ctx context.Context) error { return provider.AddIssueLabel(ctx, repo, n, model.LabelNeedsHuman) }),
		r.gitCall(ctx, func(ctx context.Context) error { return provider.CommentIssue(ctx, repo, n, comment) }),
	})
}

// gitCall makes one call to the Git host in its own time, so that a slow call
// doesn't use up the time of the calls after it.
func (r *TaskReconciler) gitCall(ctx context.Context, call func(context.Context) error) error {
	callCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	return call(callCtx)
}

// issueLabels are the factory's issue labels, for the Git host.
func issueLabels() []gitprovider.Label {
	labels := append(model.StatusLabels(), model.UrgentLabel())
	out := make([]gitprovider.Label, 0, len(labels))
	for _, l := range labels {
		out = append(out, gitprovider.Label{Name: l.Name, Color: l.Color, Description: l.Description, Exclusive: l.Exclusive})
	}
	return out
}

func logNotes(ctx context.Context, task *c4v1alpha1.Task, notes []error) {
	for _, err := range notes {
		if err != nil {
			logf.FromContext(ctx).Error(err, "Failed to update the Git host's notes for Task", "task", task.Name)
		}
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
