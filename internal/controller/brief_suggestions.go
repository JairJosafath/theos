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
	"path"
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

// waitingSuggestion is open issue i of Container c, a reviewer's suggestion
// that waits for an answer.
func waitingSuggestion(c *c4v1alpha1.Container, i gitprovider.Issue) model.Suggestion {
	return model.Suggestion{Container: c.Name, Repository: repositoryName(c), Number: i.Number, Title: i.Title,
		URL: i.URL, Criteria: model.MissingAcceptanceCriteria(i.Body) == ""}
}

// repositoryName is the name of Container c's repository on the Git host, the
// last part of its address.
func repositoryName(c *c4v1alpha1.Container) string {
	if name := path.Base(strings.TrimSuffix(c.Status.RepositoryURL, "/")); name != "." && name != "/" {
		return name
	}
	return c.Name
}

// suggestionsToCheck are the suggestions a plan's answers are checked
// against: the ones the Brief's status lists, and, for an answer to one it
// doesn't list, that number as the Git host has it in each Container the
// Brief made, when it waits there, so an answer is never refused for the
// list's length (#1019) and a refusal can say where the number does wait.
// Past model.MaxSuggestionAnswers answers it reads nothing: the check refuses
// the plan.
func (r *BriefReconciler) suggestionsToCheck(ctx context.Context, brief *c4v1alpha1.Brief,
	answers []model.SuggestionAnswer) ([]model.Suggestion, error) {
	out := waitingSuggestions(brief)
	listed := func(container string, number int) bool {
		return slices.ContainsFunc(out, func(s model.Suggestion) bool {
			return s.Container == container && s.Number == number
		})
	}
	var numbers []int
	for _, a := range answers {
		if !listed(a.Container, a.Issue) && !slices.Contains(numbers, a.Issue) {
			numbers = append(numbers, a.Issue)
		}
	}
	if len(numbers) == 0 || len(answers) > model.MaxSuggestionAnswers {
		return out, nil
	}
	var containers c4v1alpha1.ContainerList
	if err := r.List(ctx, &containers, client.InNamespace(brief.Namespace),
		client.MatchingLabels{c4v1alpha1.LabelBrief: brief.Name}); err != nil {
		return nil, fmt.Errorf("listing Containers: %w", err)
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	for _, c := range containers.Items {
		if c.Status.RepositoryURL == "" || !slices.ContainsFunc(numbers, func(n int) bool { return !listed(c.Name, n) }) {
			continue
		}
		provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, c.Namespace, c.Spec.GitProviderRef.Name)
		if err != nil {
			return nil, err
		}
		repo := provider.Repository(c.Name)
		for _, n := range numbers {
			if listed(c.Name, n) {
				continue
			}
			issue, err := provider.GetIssue(gitCtx, repo, n)
			if errors.Is(err, gitprovider.ErrIssueNotFound) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("reading %s#%d: %w", c.Name, n, err)
			}
			if issue.State == stateOpen && model.IsSuggestion(issue.Labels, issue.Body) {
				out = append(out, waitingSuggestion(&c, issue))
			}
		}
	}
	return out, nil
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
// with the planner's why and the round's prompts. One answered missing is
// left as it is, and what the operator found of it goes to run.unanswered. A
// suggestion that no longer waits (answered by an earlier try or
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
	run.unanswered = nil
	for _, a := range answers {
		if a.Answer == model.AnswerMissing {
			found, err := r.foundMissing(gitCtx, brief, a)
			if err != nil {
				return took, skipped, err
			}
			run.unanswered = append(run.unanswered, model.UnansweredLine(a, found))
			continue
		}
		listed := forgetSuggestion(brief, a.Container, a.Issue)
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
		if !listed && status.SuggestionsWaiting > 0 {
			status.SuggestionsWaiting-- // past the status's list, counted all the same
		}
		if a.Answer == model.AnswerTake {
			took++
		} else {
			skipped++
		}
	}
	return took, skipped, nil
}

// foundMissing says what the operator finds of the suggestion answer a names
// missing: a Container the Brief has no more, or an issue its repository
// doesn't have, doesn't exist.
func (r *BriefReconciler) foundMissing(ctx context.Context, brief *c4v1alpha1.Brief,
	a model.SuggestionAnswer) (string, error) {
	var c c4v1alpha1.Container
	err := r.Get(ctx, client.ObjectKey{Namespace: brief.Namespace, Name: a.Container}, &c)
	if apierrors.IsNotFound(err) {
		return model.FoundGone, nil
	}
	if err != nil {
		return "", fmt.Errorf("reading Container %s: %w", a.Container, err)
	}
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, c.Namespace, c.Spec.GitProviderRef.Name)
	if err != nil {
		return "", err
	}
	issue, err := provider.GetIssue(ctx, provider.Repository(c.Name), a.Issue)
	if errors.Is(err, gitprovider.ErrIssueNotFound) {
		return model.FoundGone, nil
	}
	if err != nil {
		return "", fmt.Errorf("reading %s#%d: %w", a.Container, a.Issue, err)
	}
	return model.FoundIssue(true, issue.State == stateOpen, issue.Labels, issue.Body), nil
}

// answerSuggestion takes one suggestion into milestone, 0 for none, or closes
// it. A take's ready label, what marks it answered, comes last, so a try
// that stops part way is done again whole. A skip closes first, so a try that
// stops after it leaves the issue closed without its comment.
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
	// The close comes first, so one the host refuses (a dependency added on the
	// host) leaves the issue uncommented, and the next try comments it once.
	if err := provider.CloseIssue(ctx, repo, issue.Number); err != nil {
		return err
	}
	return provider.CommentIssue(ctx, repo, issue.Number, comment)
}

// forgetSuggestion takes an answered suggestion off the Brief's list, and
// reports whether the list had it.
func forgetSuggestion(brief *c4v1alpha1.Brief, container string, number int) bool {
	status := &brief.Status
	before := len(status.Suggestions)
	status.Suggestions = slices.DeleteFunc(status.Suggestions, func(s c4v1alpha1.BriefSuggestion) bool {
		return s.Container == container && int(s.Number) == number
	})
	if len(status.Suggestions) < before && status.SuggestionsWaiting > 0 {
		status.SuggestionsWaiting--
	}
	listed := len(status.Suggestions) < before
	if len(status.Suggestions) == 0 {
		status.Suggestions = nil
	}
	return listed
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
