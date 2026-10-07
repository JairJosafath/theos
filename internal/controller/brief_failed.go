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

// issueTasks are the namespace's Tasks on an issue, by Container, as
// model.FailedReason sees them.
func issueTasks(tasks []c4v1alpha1.Task) map[string][]model.IssueTask {
	out := map[string][]model.IssueTask{}
	for _, t := range tasks {
		if t.Spec.Issue == 0 || t.Spec.ContainerRef.Name == "" {
			continue
		}
		it := model.IssueTask{Name: t.Name, Issue: int(t.Spec.Issue), Merged: t.Status.Phase == c4v1alpha1.TaskMerged,
			Failed: t.Status.Phase == c4v1alpha1.TaskFailed, Created: t.CreationTimestamp.Time}
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
// host: its clarification must make the whole body pass the Crew's check. One
// that is gone is checked without its body.
func (r *BriefReconciler) failedToCheck(ctx context.Context, brief *c4v1alpha1.Brief,
	answers []model.FailedAnswer) ([]model.FailedIssue, error) {
	out := make([]model.FailedIssue, 0, len(brief.Status.Failed))
	for _, f := range brief.Status.Failed {
		out = append(out, model.FailedIssue{Container: f.Container, Number: int(f.Number), Title: f.Title, URL: f.URL,
			Kind: f.Kind, Reason: f.Reason, Tries: int(f.Tries)})
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	for i, f := range out {
		if f.Kind != model.FailedUnclear || !slices.ContainsFunc(answers, func(a model.FailedAnswer) bool {
			return a.Container == f.Container && a.Issue == f.Number && a.Retries()
		}) {
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
		issue, err := provider.GetIssue(gitCtx, provider.Repository(c.Name), f.Number)
		if errors.Is(err, gitprovider.ErrIssueNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s#%d: %w", f.Container, f.Number, err)
		}
		out[i].Body = issue.Body
	}
	return out, nil
}

// answerFailed does what the plan's answers to failed issues say: one to
// retry gets its clarification appended to its body and is labelled ready
// again, for the Crew; one to close is closed. Each gets a comment with the
// planner's why and the round's prompts. An issue no longer handed back
// (answered by an earlier try or by a person on the Git host, or gone with
// its Container) is left as it is. It returns how many it retried and closed
// now.
func (r *BriefReconciler) answerFailed(ctx context.Context, brief *c4v1alpha1.Brief,
	answers []model.FailedAnswer) (retried, closed int, err error) {
	if len(answers) == 0 {
		return 0, 0, nil
	}
	status := &brief.Status
	round := int(status.Round)
	prompts := model.RoundPrompts(brief.Spec.Prompts[:min(max(int(status.RoundPrompts), 0), len(brief.Spec.Prompts))],
		int(status.PromptsPlanned))
	var filed []string
	for _, i := range status.Issues {
		if i.Round == status.Round {
			filed = append(filed, strings.TrimSpace(fmt.Sprintf("%s#%d %s %s", i.Repository, i.Number, i.Title, i.URL)))
		}
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	for _, a := range answers {
		if !a.Applies() {
			continue // a wait or an ask leaves it handed back
		}
		forgetFailed(brief, a.Container, a.Issue)
		var c c4v1alpha1.Container
		err := r.Get(ctx, client.ObjectKey{Namespace: brief.Namespace, Name: a.Container}, &c)
		if apierrors.IsNotFound(err) {
			continue // gone, and its issues with it
		}
		if err != nil {
			return retried, closed, fmt.Errorf("reading Container %s: %w", a.Container, err)
		}
		provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, c.Namespace, c.Spec.GitProviderRef.Name)
		if err != nil {
			return retried, closed, err
		}
		repo := provider.Repository(c.Name)
		issue, err := provider.GetIssue(gitCtx, repo, a.Issue)
		if errors.Is(err, gitprovider.ErrIssueNotFound) {
			continue // deleted on the Git host: nothing waits
		}
		if err != nil {
			return retried, closed, fmt.Errorf("reading %s#%d: %w", a.Container, a.Issue, err)
		}
		if issue.State != stateOpen || !model.IsFailedIssue(issue.Labels) {
			continue // answered by an earlier try, or by a person on the Git host
		}
		if err := answerFailedIssue(gitCtx, provider, repo, issue, a, round,
			model.FailedComment(a, brief.Name, round, tending(status), prompts, filed)); err != nil {
			return retried, closed, fmt.Errorf("answering %s#%d: %w", a.Container, a.Issue, err)
		}
		logf.FromContext(ctx).Info("Answered a failed issue", "container", a.Container, "number", a.Issue,
			"answer", a.Answer)
		if a.Answer == model.AnswerRetry {
			retried++
		} else {
			closed++
		}
	}
	return retried, closed, nil
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
