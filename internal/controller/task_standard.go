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
	"errors"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// A Standard change: the implementer needed protected files changed, pushed that
// change to a proposal branch, and the operator files it as an issue of its own,
// which a gated Task runs. The proposing Task is Blocked until it merges.

// defaultStandardChangeRecheck: the Git host doesn't call back when the change's
// issue closes.
const defaultStandardChangeRecheck = time.Minute

// propose files the implementer's proposal as a Standard change issue, authored
// by the operator, which blocks the Task's issue; the Task is Blocked until it
// merges. A repeated step reuses the issue it filed before.
func (r *TaskReconciler) propose(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, step model.TaskStep) (ctrl.Result, error) {
	run, err := runState(ctx, r.Client, task.Namespace, status.Sandbox)
	if err != nil || run.Result == nil || run.Result.Proposal == nil {
		return ctrl.Result{}, fmt.Errorf("reading the proposal of %s: %v", status.Sandbox, err)
	}
	proposal := *run.Result.Proposal
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return ctrl.Result{}, err
	}
	gitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	n := int(task.Spec.Issue)
	waiting, err := provider.GetIssue(gitCtx, repo, n)
	if err != nil {
		return ctrl.Result{}, err
	}
	skip := 0
	if status.StandardChange != nil {
		skip = int(status.StandardChange.Issue)
	}
	filed, isNew, err := fileStandardChange(gitCtx, provider, repo, task, proposal, waiting.Milestone, skip)
	if err != nil {
		return ctrl.Result{}, err
	}
	notes := []error{provider.AddIssueDependency(gitCtx, repo, n, filed.Number)}
	if isNew {
		// Said once: a repeated step finds the issue, and the comment, already there.
		notes = append(notes, provider.CommentIssue(gitCtx, repo, n, fmt.Sprintf(
			"Task `%s` needs a Standard change to %s, filed as #%d, which blocks this issue.",
			task.Name, quotePaths(proposal.Paths), filed.Number)))
	}
	logNotes(ctx, task, notes)

	if err := r.endRun(ctx, task, status); err != nil {
		return ctrl.Result{}, err
	}
	count := int32(1)
	if status.StandardChange != nil {
		count = status.StandardChange.Count + 1
	}
	status.Commit, status.Summary = run.Result.Commit, run.Result.Summary
	status.StandardChange = &c4v1alpha1.TaskStandardChange{Issue: int32(filed.Number), IssueURL: filed.URL,
		Paths: proposal.Paths, Branch: proposal.Branch, Count: count}
	status.Phase = c4v1alpha1.TaskBlocked
	r.progress(task, status, metav1.ConditionTrue, "WaitingForStandardChange", step.Message)
	logf.FromContext(ctx).Info("Filed a Standard change", "issue", filed.Number, "paths", proposal.Paths)
	return ctrl.Result{}, nil // the status write triggers the next step
}

// fileStandardChange returns the open Standard change issue filed for the
// proposal's branch, other than the one numbered skip, or files it: labelled
// kind/standard-change and status/ready, in the waiting issue's milestone. isNew
// says it was filed now.
func fileStandardChange(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	task *c4v1alpha1.Task, proposal model.Proposal, milestone int64, skip int) (gitprovider.Issue, bool, error) {
	if err := provider.EnsureLabels(ctx, repo, standardChangeLabels()); err != nil {
		return gitprovider.Issue{}, false, err
	}
	open, err := provider.ListIssues(ctx, repo, model.LabelStandardChange)
	if err != nil {
		return gitprovider.Issue{}, false, err
	}
	if found, ok := standardChangeToReuse(open, proposal.Branch, skip); ok {
		return found, false, nil
	}
	title, body := model.StandardChangeIssue(int(task.Spec.Issue), task.Name, proposal)
	filed, err := provider.CreateIssue(ctx, repo, title, body, []string{model.LabelStandardChange, model.LabelReady},
		milestone)
	return filed, err == nil, err
}

// standardChangeToReuse finds the issue filed for branch among issues: the first
// with the branch's marker on a line of its own, other than the one numbered skip,
// the change the Task waited on before.
func standardChangeToReuse(issues []gitprovider.Issue, branch string, skip int) (gitprovider.Issue, bool) {
	for _, issue := range issues {
		if issue.Number != skip && model.HasProposalMarker(issue.Body, branch) {
			return issue, true
		}
	}
	return gitprovider.Issue{}, false
}

// standardChangeLabels are the factory's issue labels and the Standard change's,
// for the Git host.
func standardChangeLabels() []gitprovider.Label {
	l := model.StandardChangeLabel()
	return append(issueLabels(),
		gitprovider.Label{Name: l.Name, Color: l.Color, Description: l.Description, Exclusive: l.Exclusive})
}

// standardChangeFacts are what the Task's status says of its Standard changes:
// how many it filed, and the one it waits for.
func standardChangeFacts(status *c4v1alpha1.TaskStatus) (int, model.ChangeFacts) {
	change := status.StandardChange
	if change == nil {
		return 0, model.ChangeFacts{}
	}
	return int(change.Count), model.ChangeFacts{Issue: int(change.Issue), Paths: change.Paths}
}

func quotePaths(paths []string) string {
	return "`" + strings.Join(paths, "`, `") + "`"
}

func (r *TaskReconciler) standardChangeRecheck() time.Duration {
	if r.StandardChangeRecheck > 0 {
		return r.StandardChangeRecheck
	}
	return defaultStandardChangeRecheck
}

// errChecksPending: CI runs again on the reviewed commit, and the gated merge
// waits for its result.
var errChecksPending = errors.New("CI runs again on the reviewed commit")

// mergeWaitsForChecks reports whether the Task's gated merge waits for CI on the
// reviewed commit: gatedMerge found it pending there. The wait lasts only as long
// as the approving review run, whose result the merge step reads.
func mergeWaitsForChecks(status *c4v1alpha1.TaskStatus) bool {
	checks := status.Checks
	return status.Phase == c4v1alpha1.TaskReviewing && checks != nil && checks.State == gitprovider.CheckPending &&
		checks.Commit == status.Commit
}

// gatedMerge merges a Standard change's pull request past the protected-file
// rule, as the Git host's admin, once CI passed on the reviewed commit, the
// reviewer approved and every path it changes is allowed. Otherwise it says why
// not and tries no merge. It records CI's state in status.checks. CI still
// running is errChecksPending; a list of changed paths too long to read in full
// is a refusal, and one it can't read for now an error. The reviewer's approval
// is posted with the merge only, so waiting for CI posts it once.
func (r *TaskReconciler) gatedMerge(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus, container *c4v1alpha1.Container,
	reviewer *c4v1alpha1.Agent) (refusal string, err error) {
	gitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	checks, err := provider.CombinedStatus(gitCtx, repo, status.Commit)
	if err != nil {
		return "", err
	}
	// Recorded for mergeWaitsForChecks too: a wait is bounded by the review run.
	status.Checks = &c4v1alpha1.CheckStatus{Commit: status.Commit, State: checks.State, URL: checks.URL}
	if checks.State == gitprovider.CheckPending {
		return "", errChecksPending
	}
	n := int(status.PullRequest)
	changed, err := provider.PullRequestFiles(gitCtx, repo, n)
	if errors.Is(err, gitprovider.ErrTooManyFiles) {
		return "not every changed path can be checked: " + err.Error(), nil
	}
	if err != nil {
		return "", err // never merge a path nobody checked
	}
	verdict := ""
	if status.Review != nil {
		verdict = status.Review.Verdict
	}
	if why := model.GatedMergeRefusal(checks.State, verdict, changed, task.Spec.AllowedProtectedPaths); why != "" {
		return why, nil
	}
	r.approve(ctx, task, status, container, reviewer)
	if err := provider.ForceMergePullRequest(gitCtx, repo, n, status.Commit, pullTitle(task, status),
		mergeMessage(task, status, container)); err != nil {
		return "", err
	}
	logf.FromContext(ctx).Info("Merged a Standard change with the admin override", "url", status.PullRequestURL,
		"paths", changed)
	return "", nil
}
