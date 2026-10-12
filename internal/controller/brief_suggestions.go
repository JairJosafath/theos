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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// The reviewers' suggestions for a Brief's apps (#375): the Brief lists the
// ones waiting in its Containers' repositories, a prompt answers them, and the
// operator takes or closes each as the plan says. Labels and milestones stay
// the operator's.

// recordSuggestions sets the Brief's waiting suggestions to the ones found,
// by Container and number; the status holds the first model.MaxSuggestions.
// When a repository couldn't be read (partial), found has only the ones the
// status listed for it, so the count doesn't go down.
func recordSuggestions(ctx context.Context, brief *c4v1alpha1.Brief, found []model.Suggestion, partial bool) {
	found = slices.Clone(found)
	slices.SortFunc(found, func(a, b model.Suggestion) int {
		if c := strings.Compare(a.Container, b.Container); c != 0 {
			return c
		}
		return a.Number - b.Number
	})
	listed := make([]c4v1alpha1.BriefSuggestion, 0, min(len(found), model.MaxSuggestions))
	for _, s := range found[:min(len(found), model.MaxSuggestions)] {
		listed = append(listed, c4v1alpha1.BriefSuggestion{Container: s.Container, Number: int32(s.Number),
			Title: model.Clip(s.Title, 255), URL: s.URL, Criteria: s.Criteria})
	}
	status := &brief.Status
	count := int32(len(found))
	if partial {
		count = max(count, status.SuggestionsWaiting)
	}
	if status.SuggestionsWaiting != count {
		logf.FromContext(ctx).Info("Reviewers' suggestions wait for the person", "count", count)
	}
	status.SuggestionsWaiting = count
	status.Suggestions = nil
	if len(listed) > 0 {
		status.Suggestions = listed
	}
}

// waitingSuggestions are the Brief's waiting suggestions, as its status lists them.
func waitingSuggestions(brief *c4v1alpha1.Brief) []model.Suggestion {
	out := make([]model.Suggestion, 0, len(brief.Status.Suggestions))
	for _, s := range brief.Status.Suggestions {
		out = append(out, model.Suggestion{Container: s.Container, Number: int(s.Number), Title: s.Title, URL: s.URL,
			Criteria: s.Criteria})
	}
	return out
}

// lastSuggestions are the suggestions the Brief last saw in Container name.
func lastSuggestions(brief *c4v1alpha1.Brief, name string) []model.Suggestion {
	return slices.DeleteFunc(waitingSuggestions(brief), func(s model.Suggestion) bool { return s.Container != name })
}

// answerSuggestions does what the plan's answers say: a suggestion to take
// leaves the Later milestone, when its Container is released, for the patch
// release right after the release in progress when its answer says fix: true,
// else for the round's release milestone (F6), and else for none, and is
// labelled ready, for the Crew; one to skip is closed. Each gets a comment
// with the planner's why and the round's prompts. A suggestion that no longer waits (answered by an earlier try or
// by a person on the Git host, or gone with its issue or Container) is left
// as it is. It returns how many it took and skipped now.
func (r *BriefReconciler) answerSuggestions(ctx context.Context, run *roundApply,
	answers []model.SuggestionAnswer) (took, skipped int, err error) {
	if len(answers) == 0 {
		return 0, 0, nil
	}
	brief := run.brief
	status := &brief.Status
	prompts := takenPrompts(brief)
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	for _, a := range answers {
		forgetSuggestion(brief, a.Container, a.Issue)
		var c c4v1alpha1.Container
		err := r.Get(ctx, client.ObjectKey{Namespace: brief.Namespace, Name: a.Container}, &c)
		if apierrors.IsNotFound(err) {
			continue // gone, and its suggestions with it
		}
		if err != nil {
			return took, skipped, fmt.Errorf("reading Container %s: %w", a.Container, err)
		}
		provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, c.Namespace, c.Spec.GitProviderRef.Name)
		if err != nil {
			return took, skipped, err
		}
		repo := provider.Repository(c.Name)
		issue, err := provider.GetIssue(gitCtx, repo, a.Issue)
		if errors.Is(err, gitprovider.ErrIssueNotFound) {
			continue // deleted on the Git host: nothing waits
		}
		if err != nil {
			return took, skipped, fmt.Errorf("reading %s#%d: %w", a.Container, a.Issue, err)
		}
		if issue.State != stateOpen || !model.IsSuggestion(issue.Labels, issue.Body) {
			continue // answered by an earlier try, or by a person on the Git host
		}
		var milestone int64
		if a.Answer == model.AnswerTake && run.released[a.Container] {
			if milestone, err = releaseMilestone(gitCtx, provider, repo, brief.Name, int(status.Round),
				a.Fix); err != nil {
				return took, skipped, fmt.Errorf("making the release milestone of %s: %w", a.Container, err)
			}
		}
		if err := answerSuggestion(gitCtx, provider, repo, issue, a, milestone,
			model.SuggestionComment(a, brief.Name, int(status.Round), prompts)); err != nil {
			return took, skipped, fmt.Errorf("answering %s#%d: %w", a.Container, a.Issue, err)
		}
		logf.FromContext(ctx).Info("Answered a reviewer's suggestion", "container", a.Container, "number", a.Issue,
			"answer", a.Answer)
		if a.Answer == model.AnswerTake {
			took++
		} else {
			skipped++
		}
	}
	return took, skipped, nil
}

// answerSuggestion takes one suggestion into milestone, 0 for none, or closes
// it. What marks it answered, the ready label or the closing, comes last, so a
// try that stops part way is done again whole.
func answerSuggestion(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	issue gitprovider.Issue, a model.SuggestionAnswer, milestone int64, comment string) error {
	if a.Answer == model.AnswerTake {
		if issue.Milestone != milestone {
			if err := provider.SetIssueMilestone(ctx, repo, issue.Number, milestone); err != nil {
				return err
			}
		}
		if err := provider.EnsureLabels(ctx, repo, issueLabels()); err != nil {
			return err
		}
		if err := provider.CommentIssue(ctx, repo, issue.Number, comment); err != nil {
			return err
		}
		return provider.AddIssueLabel(ctx, repo, issue.Number, model.LabelReady)
	}
	if err := provider.CommentIssue(ctx, repo, issue.Number, comment); err != nil {
		return err
	}
	return provider.CloseIssue(ctx, repo, issue.Number)
}

// forgetSuggestion takes an answered suggestion off the Brief's list.
func forgetSuggestion(brief *c4v1alpha1.Brief, container string, number int) {
	status := &brief.Status
	before := len(status.Suggestions)
	status.Suggestions = slices.DeleteFunc(status.Suggestions, func(s c4v1alpha1.BriefSuggestion) bool {
		return s.Container == container && int(s.Number) == number
	})
	if len(status.Suggestions) < before && status.SuggestionsWaiting > 0 {
		status.SuggestionsWaiting--
	}
	if len(status.Suggestions) == 0 {
		status.Suggestions = nil
	}
}

// briefOfTaskContainer maps a Container's Task to the Brief that made the
// Container: a Task's merge files reviewers' suggestions the Brief lists. A
// Plan Task is the Brief's own and left to Owns.
func briefOfTaskContainer(c client.Reader) func(context.Context, client.Object) []reconcile.Request {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		task, ok := obj.(*c4v1alpha1.Task)
		if !ok || task.Spec.ContainerRef.Name == "" {
			return nil
		}
		var container c4v1alpha1.Container
		if err := c.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: task.Spec.ContainerRef.Name},
			&container); err != nil {
			return nil
		}
		if name := container.Labels[c4v1alpha1.LabelBrief]; name != "" {
			return []reconcile.Request{requestFor(task.Namespace, name)}
		}
		return nil
	}
}
