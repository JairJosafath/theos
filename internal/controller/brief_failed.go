/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// A Brief's failed issues (plans/brief-failed-issues.md): the issues of the
// Containers it made that were handed back to a person, each with why, for
// the person to answer in a prompt.

const (
	// eventIssueFailed says an issue of the Brief's Containers was handed back.
	eventIssueFailed = "IssueFailed"
	// reasonIssuesWaiting is IssuesFailed's reason while failed issues wait.
	reasonIssuesWaiting = "IssuesWaiting"
)

// failedIssue is an open issue of Container name that was handed back to a
// person, with why, decided from the Container's Tasks.
func failedIssue(name string, number int, title, url string, labels []string, body string,
	tasks []model.IssueTask) model.FailedIssue {
	kind, reason, tries := model.FailedReason(number, labels, body, tasks)
	return model.FailedIssue{Container: name, Number: number, Title: title, URL: url, Body: body, Kind: kind,
		Reason: reason, Tries: tries}
}

// failedRelease is failed issue's milestone as it is now when the issue is a
// release step (labelled kind/release, in a milestone) of a release not yet
// tagged, else nil: the
// milestone's closed issues a Task merged, and when the step's last try
// stopped, from the Container's tasks.
func failedRelease(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	issue gitprovider.Issue, tasks []model.IssueTask) (*model.FailedRelease, error) {
	if !slices.Contains(issue.Labels, model.LabelRelease) || issue.Milestone == 0 {
		return nil, nil
	}
	// A release that went out (a person tagged it) has nothing left to
	// prepare, so closing its step is right whatever merged.
	if tag, err := provider.GetTag(ctx, repo, issue.MilestoneTitle); err != nil || tag != "" {
		return nil, err
	}
	issues, err := provider.MilestoneIssues(ctx, repo, issue.Milestone)
	if err != nil {
		return nil, fmt.Errorf("issues of milestone %s: %w", issue.MilestoneTitle, err)
	}
	titles := map[int]string{}
	var prepared []int
	for _, i := range issues {
		if slices.Contains(i.Labels, model.LabelRelease) {
			prepared = append(prepared, i.Number)
		}
		if i.State != stateOpen {
			titles[i.Number] = i.Title
		}
	}
	return model.NewFailedRelease(issue.MilestoneTitle, issue.Number, titles, prepared, tasks), nil
}

// issueTasks are the namespace's Tasks on an issue, by Container, as
// model.FailedReason sees them.
func issueTasks(tasks []c4v1alpha1.Task) map[string][]model.IssueTask {
	out := map[string][]model.IssueTask{}
	for _, t := range tasks {
		if t.Spec.Issue == 0 || t.Spec.ContainerRef.Name == "" {
			continue
		}
		it := model.IssueTask{Name: t.Name, Issue: int(t.Spec.Issue), Merged: t.Status.Phase == c4v1alpha1.TaskMerged,
			Failed: t.Status.Phase == c4v1alpha1.TaskFailed, NoCI: failedNoCI(&t.Status), Created: t.CreationTimestamp.Time}
		if t.Status.CompletedAt != nil {
			it.Completed = t.Status.CompletedAt.Time
		}
		if cond := meta.FindStatusCondition(t.Status.Conditions, ConditionSucceeded); cond != nil {
			it.Message = cond.Message
			it.Blocked = it.Failed && cond.Reason == model.ReasonBlocked
		}
		out[t.Spec.ContainerRef.Name] = append(out[t.Spec.ContainerRef.Name], it)
	}
	return out
}

// recordFailed sets the Brief's failed issues to the ones found, by Container
// and number; the status holds the first model.MaxFailedIssues. When a
// repository couldn't be read (partial), found has only the ones the status
// listed for it, so the count doesn't go down. Each listed issue the status
// didn't list before gets an IssueFailed Event, and IssuesFailed says the
// first and how many more wait.
func (r *BriefReconciler) recordFailed(ctx context.Context, brief *c4v1alpha1.Brief, found []model.FailedIssue,
	partial bool) {
	found = slices.Clone(found)
	slices.SortFunc(found, func(a, b model.FailedIssue) int {
		if c := strings.Compare(a.Container, b.Container); c != 0 {
			return c
		}
		return a.Number - b.Number
	})
	count := int32(len(found))
	found = found[:min(len(found), model.MaxFailedIssues)]
	status := &brief.Status
	listed := make([]c4v1alpha1.BriefFailedIssue, 0, len(found))
	for _, f := range found {
		listed = append(listed, c4v1alpha1.BriefFailedIssue{Container: f.Container, Number: int32(f.Number),
			Title: model.Clip(f.Title, 255), URL: f.URL, Kind: f.Kind, Reason: model.Clip(f.Reason, 1000),
			Tries: int32(f.Tries)})
		if !slices.ContainsFunc(status.Failed, func(s c4v1alpha1.BriefFailedIssue) bool {
			return s.Container == f.Container && int(s.Number) == f.Number
		}) {
			logf.FromContext(ctx).Info("An issue was handed back to the person", "container", f.Container,
				"number", f.Number, "kind", f.Kind)
			r.event(brief, corev1.EventTypeWarning, eventIssueFailed, "ListFailedIssue",
				model.FailedMessage([]model.FailedIssue{f}, 1))
		}
	}
	if partial {
		count = max(count, status.FailedWaiting)
	}
	if status.FailedWaiting != count {
		logf.FromContext(ctx).Info("Failed issues wait for the person", "count", count)
	}
	status.FailedWaiting = count
	status.Failed = nil
	if len(listed) > 0 {
		status.Failed = listed
	}
	switch {
	case count > 0:
		setCondition(&status.Conditions, brief.Generation, c4v1alpha1.ConditionIssuesFailed, metav1.ConditionTrue,
			reasonIssuesWaiting, model.FailedMessage(found, int(count)))
	case meta.FindStatusCondition(status.Conditions, c4v1alpha1.ConditionIssuesFailed) != nil:
		setCondition(&status.Conditions, brief.Generation, c4v1alpha1.ConditionIssuesFailed, metav1.ConditionFalse,
			c4v1alpha1.ReasonResolved, "No failed issue waits for the person")
	}
}

// lastFailed are the failed issues the Brief last saw in Container name,
// without their bodies, which the status doesn't keep.
func lastFailed(brief *c4v1alpha1.Brief, name string) []model.FailedIssue {
	var out []model.FailedIssue
	for _, f := range brief.Status.Failed {
		if f.Container == name {
			out = append(out, model.FailedIssue{Container: f.Container, Number: int(f.Number), Title: f.Title,
				URL: f.URL, Kind: f.Kind, Reason: f.Reason, Tries: int(f.Tries)})
		}
	}
	return out
}

// failedToCheck are the Brief's waiting failed issues, as its status lists
// them, for model.CheckFailedAnswers. The status keeps no bodies, so an
// unclear one the answers retry, or recommend retrying, is read from the Git
// host: its clarification must make the whole body pass the Crew's check. So
// is a release step the answers close, or recommend closing, with its
// milestone as it is now: work merged since its last try stopped keeps it
// open. A Standard change the answers retry, or recommend retrying, is
// checked for its proposal branch: a retry of one whose branch is gone has
// nothing to merge. One that is gone is checked without its body.
func (r *BriefReconciler) failedToCheck(ctx context.Context, brief *c4v1alpha1.Brief,
	answers []model.FailedAnswer) ([]model.FailedIssue, error) {
	out := make([]model.FailedIssue, 0, len(brief.Status.Failed))
	for _, f := range brief.Status.Failed {
		out = append(out, model.FailedIssue{Container: f.Container, Number: int(f.Number), Title: f.Title, URL: f.URL,
			Kind: f.Kind, Reason: f.Reason, Tries: int(f.Tries)})
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	var tasks map[string][]model.IssueTask
	for i, f := range out {
		answered := func(match func(model.FailedAnswer) bool) bool {
			return slices.ContainsFunc(answers, func(a model.FailedAnswer) bool {
				return a.Container == f.Container && a.Issue == f.Number && match(a)
			})
		}
		retries := answered(model.FailedAnswer.Retries)
		closes := answered(model.FailedAnswer.Closes)
		if !retries && !closes {
			continue
		}
		var c c4v1alpha1.Container
		err := r.Get(ctx, client.ObjectKey{Namespace: brief.Namespace, Name: f.Container}, &c)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading Container %s: %w", f.Container, err)
		}
		provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, c.Namespace, c.Spec.GitProviderRef.Name)
		if err != nil {
			return nil, err
		}
		repo := provider.Repository(c.Name)
		issue, err := provider.GetIssue(gitCtx, repo, f.Number)
		if errors.Is(err, gitprovider.ErrIssueNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s#%d: %w", f.Container, f.Number, err)
		}
		out[i].Body = issue.Body
		if retries {
			if out[i].ProposalGone, err = goneProposal(gitCtx, provider, repo, issue); err != nil {
				return nil, fmt.Errorf("reading %s#%d's proposal branch: %w", f.Container, f.Number, err)
			}
		}
		if !closes {
			continue
		}
		if tasks == nil {
			var list c4v1alpha1.TaskList
			if err := r.List(ctx, &list, client.InNamespace(brief.Namespace)); err != nil {
				return nil, fmt.Errorf("listing Tasks: %w", err)
			}
			tasks = issueTasks(list.Items)
		}
		if out[i].Release, err = failedRelease(gitCtx, provider, repo, issue, tasks[f.Container]); err != nil {
			return nil, fmt.Errorf("reading %s#%d's release: %w", f.Container, f.Number, err)
		}
	}
	return out, nil
}

// answerFailed does what the plan's answers to failed issues say: one to
// retry gets its clarification appended to its body and is labelled ready
// again, for the Crew; one to close is closed. Each gets a comment with the
// planner's why and the round's prompts. An issue no longer handed back
// (answered by an earlier try or by a person on the Git host, or gone with
// its Container) is left as it is. A close of an issue that waits on an open
// one waits for the round's other closes, so a round closes an issue and the
// Standard change it waits on in either order (#976); one still waiting
// after them is refused: it gets a comment saying why and stays handed back,
// and the rest of the round goes on. It returns how many it retried and
// closed now, and the refused closes as "container#3 (waits on #4)".
func (r *BriefReconciler) answerFailed(ctx context.Context, brief *c4v1alpha1.Brief,
	answers []model.FailedAnswer) (retried, closed int, refused []string, err error) {
	if len(answers) == 0 {
		return 0, 0, nil, nil
	}
	status := &brief.Status
	round := int(status.Round)
	prompts := takenPrompts(brief)
	var filed []string
	for _, i := range status.Issues {
		if i.Round == status.Round {
			filed = append(filed, strings.TrimSpace(fmt.Sprintf("%s#%d %s %s", i.Repository, i.Number, i.Title, i.URL)))
		}
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	// answer answers one issue; a close that waits on an open issue is left
	// for later, with what it waits on.
	answer := func(c answering) ([]string, error) {
		if c.answer.Answer != model.AnswerRetry {
			blockers, err := c.provider.OpenBlockers(gitCtx, c.repo, c.issue.Number)
			if err != nil {
				return nil, fmt.Errorf("reading %s#%d's dependencies: %w", c.answer.Container, c.issue.Number, err)
			}
			if len(blockers) > 0 {
				return blockers, nil
			}
		}
		err := answerFailedIssue(gitCtx, c.provider, c.repo, c.issue, c.answer, round,
			model.FailedComment(c.answer, brief.Name, round, tending(status), prompts, filed))
		if errors.Is(err, gitprovider.ErrOpenDependencies) {
			return []string{"an open issue"}, nil // one opened since it was read
		}
		if err != nil {
			return nil, fmt.Errorf("answering %s#%d: %w", c.answer.Container, c.issue.Number, err)
		}
		logf.FromContext(ctx).Info("Answered a failed issue", "container", c.answer.Container,
			"number", c.issue.Number, "answer", c.answer.Answer)
		if c.answer.Answer == model.AnswerRetry {
			retried++
		} else {
			closed++
		}
		return nil, nil
	}
	var waiting []answering
	for _, a := range answers {
		if !a.Applies() {
			continue // a wait or an ask leaves it handed back
		}
		forgetFailed(brief, a.Container, a.Issue)
		c, ok, err := r.openFailed(ctx, gitCtx, brief, a)
		if err != nil {
			return retried, closed, nil, err
		}
		if !ok {
			continue
		}
		if c.blockers, err = answer(c); err != nil {
			return retried, closed, nil, err
		}
		if len(c.blockers) > 0 {
			waiting = append(waiting, c)
		}
	}
	// Each pass closes the waiting issues whose blockers the round closed.
	for progress := true; progress && len(waiting) > 0; {
		progress = false
		var still []answering
		for _, c := range waiting {
			if c.blockers, err = answer(c); err != nil {
				return retried, closed, nil, err
			}
			if len(c.blockers) > 0 {
				still = append(still, c)
			} else {
				progress = true
			}
		}
		waiting = still
	}
	for _, c := range waiting {
		if err := c.provider.CommentIssue(gitCtx, c.repo, c.issue.Number,
			model.CloseRefusedComment(c.answer, brief.Name, round, tending(status), c.blockers)); err != nil {
			return retried, closed, nil, fmt.Errorf("commenting on %s#%d: %w", c.answer.Container, c.issue.Number, err)
		}
		logf.FromContext(ctx).Info("Could not close a failed issue: it waits on open issues",
			"container", c.answer.Container, "number", c.issue.Number, "blockers", c.blockers)
		refused = append(refused, fmt.Sprintf("%s#%d (waits on %s)", c.answer.Container, c.issue.Number,
			strings.Join(c.blockers, ", ")))
	}
	return retried, closed, refused, nil
}

// answering is a failed issue a round answers, on its Git host, and while
// its close waits, the open issues it waits on.
type answering struct {
	answer   model.FailedAnswer
	provider gitprovider.Provider
	repo     gitprovider.Repository
	issue    gitprovider.Issue
	blockers []string
}

// openFailed reads the failed issue an answer is to. It is not ok when the
// issue or its Container is gone, or it is no longer handed back.
func (r *BriefReconciler) openFailed(ctx, gitCtx context.Context, brief *c4v1alpha1.Brief,
	a model.FailedAnswer) (answering, bool, error) {
	var c c4v1alpha1.Container
	err := r.Get(ctx, client.ObjectKey{Namespace: brief.Namespace, Name: a.Container}, &c)
	if apierrors.IsNotFound(err) {
		return answering{}, false, nil // gone, and its issues with it
	}
	if err != nil {
		return answering{}, false, fmt.Errorf("reading Container %s: %w", a.Container, err)
	}
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, c.Namespace, c.Spec.GitProviderRef.Name)
	if err != nil {
		return answering{}, false, err
	}
	repo := provider.Repository(c.Name)
	issue, err := provider.GetIssue(gitCtx, repo, a.Issue)
	if errors.Is(err, gitprovider.ErrIssueNotFound) {
		return answering{}, false, nil // deleted on the Git host: nothing waits
	}
	if err != nil {
		return answering{}, false, fmt.Errorf("reading %s#%d: %w", a.Container, a.Issue, err)
	}
	if issue.State != stateOpen || !model.IsFailedIssue(issue.Labels) {
		return answering{}, false, nil // answered by an earlier try, or by a person on the Git host
	}
	return answering{answer: a, provider: provider, repo: repo, issue: issue}, true, nil
}

// answerFailedIssue retries or closes one failed issue. What marks it
// answered, the ready label in place of needs-human or the closing, comes
// last, so a try that stops part way is done again, its comment maybe twice;
// the clarification is appended once a round.
func answerFailedIssue(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	issue gitprovider.Issue, a model.FailedAnswer, round int, comment string) error {
	if a.Answer == model.AnswerRetry {
		if body := model.ClarifiedBody(issue.Body, round, a.Clarify); body != issue.Body {
			if err := provider.EditIssueBody(ctx, repo, issue.Number, body); err != nil {
				return err
			}
		}
		if err := provider.EnsureLabels(ctx, repo, issueLabels()); err != nil {
			return err
		}
		if err := provider.CommentIssue(ctx, repo, issue.Number, comment); err != nil {
			return err
		}
		// status/ready is exclusive and takes needs-human's place; removing
		// needs-human too keeps a host without exclusive labels right.
		if err := provider.AddIssueLabel(ctx, repo, issue.Number, model.LabelReady); err != nil {
			return err
		}
		return provider.RemoveIssueLabel(ctx, repo, issue.Number, model.LabelNeedsHuman)
	}
	if err := provider.CommentIssue(ctx, repo, issue.Number, comment); err != nil {
		return err
	}
	// A closed Standard change is never retried: nothing reads its proposal.
	if err := deleteChangeProposal(ctx, provider, repo, issue); err != nil {
		return err
	}
	return provider.CloseIssue(ctx, repo, issue.Number)
}

// forgetFailed takes an answered failed issue off the Brief's list.
func forgetFailed(brief *c4v1alpha1.Brief, container string, number int) {
	status := &brief.Status
	before := len(status.Failed)
	status.Failed = slices.DeleteFunc(status.Failed, func(f c4v1alpha1.BriefFailedIssue) bool {
		return f.Container == container && int(f.Number) == number
	})
	if len(status.Failed) < before && status.FailedWaiting > 0 {
		status.FailedWaiting--
	}
	if len(status.Failed) == 0 {
		status.Failed = nil
	}
}
