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
	"fmt"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// The Git host is where people follow the work: issues say who works on them and
// how it went, pull requests carry the reviews. None of it decides what the Task
// does next, so a failed note is logged and the Task goes on.

// stateOpen and stateClosed are the states of an issue or pull request on the
// Git host.
const (
	stateOpen   = "open"
	stateClosed = "closed"
)

// agentName is how comments name an Agent: its name, and its model when set.
func agentName(agent *c4v1beta1.Agent) string {
	if agent.Spec.Model == "" {
		return "Agent `" + agent.Name + "`"
	}
	return fmt.Sprintf("Agent `%s` (%s)", agent.Name, agent.Spec.Model)
}

// announce marks an issue as taken: in progress, assigned to the Container's
// agent bot, with a comment naming the Task and its Agents.
func (r *TaskReconciler) announce(ctx context.Context, provider ports.Issues, repo ports.Repository,
	task *c4v1beta1.Task, container *c4v1beta1.Container, agents taskAgents) {
	n := int(task.Spec.Issue)
	notes := []error{
		provider.EnsureLabels(ctx, repo, issueLabels()),
		provider.AddIssueLabel(ctx, repo, n, model.LabelInProgress),
		provider.AssignIssue(ctx, repo, n, []string{model.AgentUsername(container.Namespace, container.Name)}),
		provider.CommentIssue(ctx, repo, n, announcement(task, agents)),
	}
	logNotes(ctx, task, notes)
}

// announcement is the comment that marks an issue as taken by task.
func announcement(task *c4v1beta1.Task, agents taskAgents) string {
	if isSpec(task) {
		return fmt.Sprintf("Task `%s` picked this up to write its spec, `%s`: %s writes it, %s reviews it, in up to "+
			"%d rounds. The spec's pull request then waits for its approval before it merges and its acceptance "+
			"criteria come into this description.", task.Name, model.SpecPath(int(task.Spec.Issue)),
			agentName(agents.implementer), agentName(agents.reviewer), agents.implementer.Spec.MaxRounds)
	}
	if agents.verifier != nil {
		return fmt.Sprintf("Task `%s` picked this up: %s implements, %s reviews, %s builds and runs the approved "+
			"change to check each acceptance criterion, in up to %d rounds.", task.Name, agentName(agents.implementer),
			agentName(agents.reviewer), agentName(agents.verifier), agents.implementer.Spec.MaxRounds)
	}
	return fmt.Sprintf("Task `%s` picked this up: %s implements, %s reviews, in up to %d rounds.",
		task.Name, agentName(agents.implementer), agentName(agents.reviewer), agents.implementer.Spec.MaxRounds)
}

// continueEarlierTry takes over the pull request of the issue's earlier try
// when it is still open: its branch, number and feedback become the Task's, so
// the try goes on from where the last one stopped (#607). It returns the
// commit the try starts at instead of implementing, when the earlier try's
// unreviewed commit is still the head and its checks now pass
// (model.StartsAtReview); empty otherwise.
func (r *TaskReconciler) continueEarlierTry(ctx context.Context, provider ports.GitHost,
	repo ports.Repository, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus) (string, error) {
	var list c4v1beta1.TaskList
	if err := r.List(ctx, &list, client.InNamespace(task.Namespace),
		client.MatchingFields{indexTaskContainer: task.Spec.ContainerRef.Name}); err != nil {
		return "", err
	}
	tries := make([]model.Try, 0, len(list.Items))
	byName := make(map[string]*c4v1beta1.Task, len(list.Items))
	for i := range list.Items {
		t := &list.Items[i]
		if t.Spec.Kind == c4v1beta1.TaskTriage || !sameKind(t, task) {
			// A triage is no try at the issue: it has no pull request; a
			// spec's try continues only a spec's, a build's only a build's.
			continue
		}
		byName[t.Name] = t
		tries = append(tries, model.Try{Name: t.Name, Issue: int(t.Spec.Issue), Created: t.CreationTimestamp.Unix(),
			Failed: t.Status.Phase == c4v1beta1.TaskFailed, PullRequest: int(t.Status.PullRequest)})
	}
	earlier, ok := model.EarlierTry(task.Name, task.CreationTimestamp.Unix(), int(task.Spec.Issue), tries)
	if !ok {
		return "", nil
	}
	pr, err := provider.GetPullRequest(ctx, repo, earlier.PullRequest)
	if errors.Is(err, ports.ErrPullRequestNotFound) {
		// Gone for good, deleted or with its repository: like a closed one,
		// the Task starts on its own branch.
		logf.FromContext(ctx).Info("Earlier pull request not found", "number", earlier.PullRequest,
			"earlierTask", earlier.Name)
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if pr.State != stateOpen || pr.Merged || pr.Head == "" {
		return "", nil
	}
	prior := byName[earlier.Name].Status
	status.Continues, status.Branch = earlier.Name, pr.Head
	status.PullRequest, status.PullRequestURL = int32(pr.Number), pr.URL
	status.Feedback = prior.Feedback
	logf.FromContext(ctx).Info("Continuing pull request", "url", pr.URL, "branch", pr.Head, "earlierTask", earlier.Name)

	work := model.InheritedWork{Pushed: prior.Commit, Head: pr.HeadSHA,
		Unreviewed: model.EndedUnreviewed(tryHistory(prior.History))}
	if work.Unreviewed && work.Pushed != "" && work.Head == work.Pushed {
		checks, err := provider.CombinedStatus(ctx, repo, work.Head)
		if err != nil {
			return "", err
		}
		work.Checks = checks.State
	}
	if !model.StartsAtReview(work) {
		return "", nil
	}
	status.Summary = prior.Summary
	return work.Head, nil
}

// tryHistory is a Task's timeline as model.EndedUnreviewed reads it.
func tryHistory(history []c4v1beta1.TaskTransition) []model.TryStep {
	steps := make([]model.TryStep, len(history))
	for i, h := range history {
		steps[i] = model.TryStep{Phase: string(h.Phase), Round: int(h.Round)}
	}
	return steps
}

// noteContinued says on the pull request a Task took over that it goes on with
// it. The caller posts it once, with the run's new sandbox, so a retry after a
// failed status write doesn't post it again.
func (r *TaskReconciler) noteContinued(ctx context.Context, provider ports.GitHost,
	repo ports.Repository, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus) {
	if err := provider.CommentPullRequest(ctx, repo, int(status.PullRequest), fmt.Sprintf(
		"Task `%s` continues this pull request, which Task `%s` opened.", task.Name, status.Continues)); err != nil {
		logf.FromContext(ctx).Info("Could not comment on pull request", "task", task.Name, "error", err.Error())
	}
}

// review posts the reviewer's verdict as a review of the round's commit.
func (r *TaskReconciler) review(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, reviewer *c4v1beta1.Agent, event, summary string) {
	r.postReview(ctx, task, status, container, event,
		fmt.Sprintf("Review by %s, round %d.\n\n%s", agentName(reviewer), status.Round, summary))
}

// postReview posts a review of the round's commit on the Task's pull request,
// if it has one: event, with body.
func (r *TaskReconciler) postReview(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, event, body string) {
	if status.PullRequest == 0 {
		return
	}
	provider, repo, err := r.gitRepo(ctx, container)
	if err == nil {
		gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
		err = provider.ReviewPullRequest(gitCtx, repo, int(status.PullRequest), status.Commit, event, body)
		cancel()
	}
	logNotes(ctx, task, []error{err})
}

// closeIssue reports the merge on the issue, files the reviewer's follow-ups as
// proposed issues and the criteria the review (and the verification) left open
// as one kind/by-hand issue for a person (status.byHand), both in the Later
// milestone, outside any release, and closes it. openBy names the Agents that
// left them open. It returns the follow-ups' addresses.
func (r *TaskReconciler) closeIssue(ctx context.Context, provider interface {
	ports.GitHost
	ports.Issues
}, repo ports.Repository,
	task *c4v1beta1.Task, status *c4v1beta1.TaskStatus, reviewer *c4v1beta1.Agent, openBy string,
	followUps []model.FollowUp, open []model.OpenCriterion) []string {
	n := int(task.Spec.Issue)
	var filed []string
	var notes []error
	var milestone int64
	byHand := len(open) > 0 && status.ByHand == ""
	if len(followUps) > 0 || byHand {
		err := r.gitCall(ctx, func(ctx context.Context) (err error) {
			milestone, err = provider.EnsureMilestone(ctx, repo, model.FollowUpMilestone, model.FollowUpMilestoneDescription)
			return err
		})
		if err != nil {
			milestone = 0 // file them without it
		}
		labels := issueLabels()
		if byHand {
			l := model.ByHandLabel()
			labels = append(labels,
				ports.Label{Name: l.Name, Color: l.Color, Description: l.Description, Exclusive: l.Exclusive})
		}
		notes = append(notes, r.gitCall(ctx, func(ctx context.Context) error {
			return provider.EnsureLabels(ctx, repo, labels)
		}), err)
	}
	for _, f := range followUps {
		body := model.FollowUpIssue(f, agentName(reviewer), status.PullRequestURL, n, task.Name)
		var issue ports.Issue
		err := r.gitCall(ctx, func(ctx context.Context) (err error) {
			issue, err = provider.CreateIssue(ctx, repo, f.Title, body, []string{model.LabelProposed}, milestone)
			return err
		})
		notes = append(notes, err)
		if err == nil {
			filed = append(filed, issue.URL)
		}
	}
	if byHand {
		url, err := r.fileByHand(ctx, provider, repo, task, status, openBy, open, milestone)
		notes = append(notes, err)
		if err == nil {
			status.ByHand = url
			logf.FromContext(ctx).Info("Filed the open criteria for a person", "url", url, "criteria", len(open))
		}
	}
	comment := fmt.Sprintf("Merged in %s after %d %s.", status.PullRequestURL, status.Round, plural(int(status.Round), "round", "rounds"))
	if status.ByHand != "" {
		comment += "\n\n" + openBy + " accepted criteria that need a person as open; they wait in " + status.ByHand +
			", " + model.LabelByHand + "."
	}
	if len(filed) > 0 {
		comment += "\n\nThe reviewer suggested follow-ups, filed as " + model.LabelProposed + ":\n- " + strings.Join(filed, "\n- ")
	}
	if len(task.Spec.AllowedProtectedPaths) > 0 {
		// A Standard change merged: nothing reads its proposal branch any more.
		notes = append(notes, r.gitCall(ctx, func(ctx context.Context) error {
			issue, err := provider.GetIssue(ctx, repo, n)
			if err != nil {
				return err
			}
			return deleteChangeProposal(ctx, provider, repo, issue)
		}))
	}
	notes = append(notes, r.endIssue(ctx, provider, repo, task, comment,
		fmt.Sprintf("its pull request %s merged", status.PullRequestURL))...)
	logNotes(ctx, task, notes)
	return filed
}

// fileByHand files the open criteria as the Task's kind/by-hand issue,
// status/needs-human so no Crew picks it, and returns its address. When the
// Task's status lost the address, the issue is found again by its marker.
func (r *TaskReconciler) fileByHand(ctx context.Context, provider ports.Issues, repo ports.Repository,
	task *c4v1beta1.Task, status *c4v1beta1.TaskStatus, by string, open []model.OpenCriterion,
	milestone int64) (string, error) {
	var existing []ports.Issue
	if err := r.gitCall(ctx, func(ctx context.Context) (err error) {
		existing, err = provider.ListIssues(ctx, repo, model.LabelByHand)
		return err
	}); err != nil {
		return "", err
	}
	for _, issue := range existing {
		if model.HasByHandMarker(issue.Body, task.Name) {
			return issue.URL, nil
		}
	}
	title, body := model.ByHandIssue(status.IssueTitle, int(task.Spec.Issue), status.PullRequestURL, task.Name,
		by, open)
	var issue ports.Issue
	err := r.gitCall(ctx, func(ctx context.Context) (err error) {
		issue, err = provider.CreateIssue(ctx, repo, title, body, []string{model.LabelByHand, model.LabelNeedsHuman},
			milestone)
		return err
	})
	return issue.URL, err
}

// endIssue takes the Task's issue out of progress, closes it and says how the
// Task ended. The comment comes only once the host took the close, so a close
// it refuses (an open dependency) leaves no comment saying the open issue is
// done: it says instead that the Task ended (ended, how) and why the host
// refused, and labels the issue status/needs-human so a person sees it. It
// returns the notes' errors.
func (r *TaskReconciler) endIssue(ctx context.Context, provider ports.Issues, repo ports.Repository,
	task *c4v1beta1.Task, comment, ended string) []error {
	n := int(task.Spec.Issue)
	notes := []error{
		r.gitCall(ctx, func(ctx context.Context) error {
			return provider.RemoveIssueLabel(ctx, repo, n, model.LabelInProgress)
		}),
	}
	closeErr := r.gitCall(ctx, func(ctx context.Context) error { return provider.CloseIssue(ctx, repo, n) })
	notes = append(notes, closeErr)
	if closeErr != nil {
		refused := fmt.Sprintf("Task `%s` ended: %s. The host refused to close this issue: %v\n\n"+
			"Close it once what keeps it open is done, or label it %s to work it again.",
			task.Name, ended, closeErr, model.LabelReady)
		return append(notes,
			r.gitCall(ctx, func(ctx context.Context) error { return provider.EnsureLabels(ctx, repo, issueLabels()) }),
			r.gitCall(ctx, func(ctx context.Context) error {
				return provider.AddIssueLabel(ctx, repo, n, model.LabelNeedsHuman)
			}),
			r.gitCall(ctx, func(ctx context.Context) error { return provider.CommentIssue(ctx, repo, n, refused) }))
	}
	return append(notes,
		r.gitCall(ctx, func(ctx context.Context) error { return provider.CommentIssue(ctx, repo, n, comment) }))
}

// handBack gives a failed Task's issue back to a person, saying why.
func (r *TaskReconciler) handBack(ctx context.Context, task *c4v1beta1.Task, container *c4v1beta1.Container, why string) {
	if task.Spec.Issue == 0 {
		return
	}
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		logNotes(ctx, task, []error{err})
		return
	}
	n := int(task.Spec.Issue)
	label := model.LabelReady
	if isSpec(task) {
		label = model.LabelNeedsSpec
	}
	again := fmt.Sprintf("clarify or split the issue and label it %s", label)
	if brief := container.Labels[c4v1beta1.LabelBrief]; brief != "" {
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
func issueLabels() []ports.Label {
	labels := append(model.StatusLabels(), model.UrgentLabel())
	out := make([]ports.Label, 0, len(labels))
	for _, l := range labels {
		out = append(out, ports.Label{Name: l.Name, Color: l.Color, Description: l.Description, Exclusive: l.Exclusive})
	}
	return out
}

func logNotes(ctx context.Context, task *c4v1beta1.Task, notes []error) {
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
