/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
// merges. A repeated step reuses the issue it filed before. A Task without an
// issue or a Crew, or whose issue a person closed during the round, fails
// instead.
func (r *TaskReconciler) propose(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, step model.TaskStep) (ctrl.Result, error) {
	run, err := runState(ctx, r.Client, task.Namespace, status.Sandbox, status.SandboxPod)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reading the proposal of %s: %w", status.Sandbox, err)
	}
	if run.Result == nil || run.Result.Proposal == nil {
		return ctrl.Result{}, fmt.Errorf("sandbox %s reported no proposal", status.Sandbox)
	}
	proposal := *run.Result.Proposal
	n := int(task.Spec.Issue)
	if refusal := model.ProposeRefusal(n, task.Labels[LabelCrew] != "", proposal.Paths); refusal != "" {
		return ctrl.Result{}, r.fail(ctx, task, status, container, "Failed", refusal)
	}
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return ctrl.Result{}, err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	waiting, err := provider.GetIssue(gitCtx, repo, n)
	if err != nil {
		return ctrl.Result{}, err
	}
	if waiting.State == stateClosed {
		// Nobody waits for a change any more.
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		r.end(task, status, model.IssueClosed(n))
		return ctrl.Result{}, nil
	}
	skip := 0
	if status.StandardChange != nil {
		skip = int(status.StandardChange.Issue)
	}
	filed, isNew, err := fileStandardChange(gitCtx, provider, repo, task, proposal, waiting, skip)
	if err != nil {
		return ctrl.Result{}, err
	}
	notes := []error{provider.AddIssueDependency(gitCtx, repo, n, repo, filed.Number)}
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
	var earlier int
	if status.StandardChange != nil {
		earlier = int(status.StandardChange.Count)
	}
	count := model.ProposalCount(earlier)
	setProgress(status, model.Proposed(progressOf(status), run.Result.Commit))
	status.Summary = run.Result.Summary
	status.StandardChange = &c4v1alpha1.TaskStandardChange{Issue: int32(filed.Number), IssueURL: filed.URL,
		Paths: proposal.Paths, Branch: proposal.Branch, Count: int32(count)}
	r.progress(task, status, metav1.ConditionTrue, "WaitingForStandardChange", step.Message)
	logf.FromContext(ctx).Info("Filed a Standard change", "issue", filed.Number, "paths", proposal.Paths)
	return ctrl.Result{}, nil // the status write triggers the next step
}

// fileStandardChange returns the open Standard change issue filed for the
// proposal's branch, other than the one numbered skip, or files it: labelled
// kind/standard-change and status/ready, in the waiting issue's milestone, and
// urgent when the waiting issue is. isNew says it was filed now.
func fileStandardChange(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	task *c4v1alpha1.Task, proposal model.Proposal, waiting gitprovider.Issue, skip int) (gitprovider.Issue, bool, error) {
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
	labels := []string{model.LabelStandardChange, model.LabelReady}
	if slices.Contains(waiting.Labels, model.LabelUrgent) {
		labels = append(labels, model.LabelUrgent)
	}
	filed, err := provider.CreateIssue(ctx, repo, title, body, labels, waiting.Milestone)
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

// standardChangeCount is how many Standard changes the Task filed.
func standardChangeCount(status *c4v1alpha1.TaskStatus) int {
	if status.StandardChange == nil {
		return 0
	}
	return int(status.StandardChange.Count)
}

// changeFacts is the Standard change the Task filed last, as its status says.
// While the Task is Blocked on it, the Tasks on the change's issue say whether it
// merged or will not, as does its issue closed with none.
func (r *TaskReconciler) changeFacts(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container) (model.ChangeFacts, error) {
	change := status.StandardChange
	if change == nil {
		return model.ChangeFacts{}, nil
	}
	facts := model.ChangeFacts{Issue: int(change.Issue), Paths: change.Paths}
	if status.Phase != c4v1alpha1.TaskBlocked {
		return facts, nil
	}
	var tasks c4v1alpha1.TaskList
	if err := r.List(ctx, &tasks, client.InNamespace(task.Namespace),
		client.MatchingFields{indexTaskContainer: container.Name}); err != nil {
		return facts, err
	}
	// Oldest first: the last one that failed says why the change will not merge.
	slices.SortFunc(tasks.Items, func(a, b c4v1alpha1.Task) int {
		return cmp.Or(a.CreationTimestamp.Compare(b.CreationTimestamp.Time), cmp.Compare(a.Name, b.Name))
	})
	var changeTasks []model.ChangeTask
	for _, t := range tasks.Items {
		if t.Spec.Issue != change.Issue {
			continue
		}
		ct := model.ChangeTask{Name: t.Name, Final: isFinal(t.Status.Phase), Failed: t.Status.Phase == c4v1alpha1.TaskFailed}
		if cond := meta.FindStatusCondition(t.Status.Conditions, ConditionSucceeded); cond != nil {
			ct.Message = cond.Message
			ct.Blocked = ct.Failed && cond.Reason == model.ReasonBlocked
		}
		changeTasks = append(changeTasks, ct)
	}
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return facts, err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	issue, err := provider.GetIssue(gitCtx, repo, facts.Issue)
	if err != nil {
		return facts, err
	}
	facts.Merged, facts.Failed, facts.Blocked = model.StandardChangeOutcome(facts.Issue, changeTasks, issue.State == stateClosed)
	return facts, nil
}

// resumedRun reports whether the Task's implement run is one of the round it
// resumed in after its Standard change merged.
func resumedRun(status *c4v1alpha1.TaskStatus) bool {
	change := status.StandardChange
	return status.Phase == c4v1alpha1.TaskImplementing && change != nil && change.ResumedRound == status.Round
}

// resume puts a Task whose Standard change merged back to work, in the round it
// was in, telling the implementer the change is in main.
func (r *TaskReconciler) resume(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, step model.TaskStep) {
	change := status.StandardChange
	provider, repo, err := r.gitRepo(ctx, container)
	notes := []error{err}
	if err == nil {
		gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
		notes = []error{provider.CommentIssue(gitCtx, repo, int(task.Spec.Issue), fmt.Sprintf(
			"Standard change #%d merged; Task `%s` goes on with round %d.", change.Issue, task.Name, status.Round)),
			deleteProposal(gitCtx, provider, repo, change)}
		cancel()
	}
	logNotes(ctx, task, notes)
	setProgress(status, model.ResumeRound(progressOf(status), step.Feedback))
	r.progress(task, status, metav1.ConditionTrue, "Resumed", step.Message)
	logf.FromContext(ctx).Info("Resumed Task after its Standard change merged", "issue", change.Issue,
		"round", status.Round)
}

// reasonDoneByStandardChange ends a Task its Standard change finished: it is
// Merged with no merge of its own, and a release counts the change's merge.
const reasonDoneByStandardChange = "DoneByStandardChange"

// doneByStandardChange ends a resumed Task whose branch has nothing beyond main:
// its Standard change did the whole issue. It closes the Task's pull request from
// an earlier round, if any, and the issue, with a comment naming the change.
func (r *TaskReconciler) doneByStandardChange(ctx context.Context, task *c4v1alpha1.Task,
	status *c4v1alpha1.TaskStatus, container *c4v1alpha1.Container, step model.TaskStep) error {
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return err
	}
	if err := r.endRun(ctx, task, status); err != nil {
		return err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	var notes []error
	if status.PullRequest > 0 {
		notes = append(notes, provider.ClosePullRequest(gitCtx, repo, int(status.PullRequest)))
	}
	change := status.StandardChange
	notes = append(notes, deleteProposal(gitCtx, provider, repo, change))
	notes = append(notes, r.endIssue(ctx, provider, repo, int(task.Spec.Issue), fmt.Sprintf(
		"Done by Standard change #%d (%s): with it merged, this issue needed nothing more.",
		change.Issue, change.IssueURL))...)
	logNotes(ctx, task, notes)
	r.finish(task, status, c4v1alpha1.TaskMerged, metav1.ConditionTrue, reasonDoneByStandardChange, step.Message)
	logf.FromContext(ctx).Info("Merged Task done by its Standard change", "issue", change.Issue)
	return nil
}

// deleteProposal deletes the proposal branch of the Task's Standard change:
// nothing reads it once the change merged or will not.
func deleteProposal(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	change *c4v1alpha1.TaskStandardChange) error {
	if change == nil || change.Branch == "" {
		return nil
	}
	return provider.DeleteBranch(ctx, repo, change.Branch)
}

// dropProposal deletes the proposal branch of a Task that fails while Blocked on
// its Standard change, once the change's issue is closed. While it is open the
// change can still be retried, and its retry merges the branch, so it stays;
// the change's own merge, or its close by a failed issue's answer, deletes it
// then.
func (r *TaskReconciler) dropProposal(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container) {
	change := status.StandardChange
	if status.Phase != c4v1alpha1.TaskBlocked || change == nil {
		return
	}
	provider, repo, err := r.gitRepo(ctx, container)
	if err == nil {
		gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
		var issue gitprovider.Issue
		issue, err = provider.GetIssue(gitCtx, repo, int(change.Issue))
		switch {
		case errors.Is(err, gitprovider.ErrIssueNotFound):
			err = deleteProposal(gitCtx, provider, repo, change)
		case err == nil && issue.State == stateClosed:
			err = deleteProposal(gitCtx, provider, repo, change)
		}
		cancel()
	}
	logNotes(ctx, task, []error{err})
}

// deleteChangeProposal deletes the proposal branch a Standard change issue
// names, if it names one: its change merged or was closed.
func deleteChangeProposal(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	issue gitprovider.Issue) error {
	branch := model.ProposalBranchOf(issue.Body)
	if !slices.Contains(issue.Labels, model.LabelStandardChange) || branch == "" {
		return nil
	}
	return provider.DeleteBranch(ctx, repo, branch)
}

// goneProposal returns the proposal branch of Standard change issue when the
// Git host no longer has it, so a retry would have nothing to merge; "" for
// another issue, or a branch still there.
func goneProposal(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	issue gitprovider.Issue) (string, error) {
	branch := model.ProposalBranchOf(issue.Body)
	if !slices.Contains(issue.Labels, model.LabelStandardChange) || branch == "" {
		return "", nil
	}
	ok, err := provider.HasBranch(ctx, repo, branch)
	if err != nil || ok {
		return "", err
	}
	return branch, nil
}

func quotePaths(paths []string) string { return model.QuotePaths(paths) }

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
// rule, as the Git host's admin, once CI passed on the reviewed commit (every
// check main requires among it), the reviewer approved and every path it changes
// is allowed. Otherwise it says why
// not and tries no merge. It records CI's state in status.checks. CI still
// running is errChecksPending; a list of changed paths too long to read in full
// is a refusal, and one it can't read for now an error. The reviewer's approval
// is posted with the merge only, so waiting for CI posts it once.
func (r *TaskReconciler) gatedMerge(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus, container *c4v1alpha1.Container,
	reviewer *c4v1alpha1.Agent) (refusal string, err error) {
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
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
	protected, err := protectedPaths(ctx, r.Client, container)
	if err != nil {
		return "", err // never merge a path nobody checked
	}
	if why := model.GatedMergeRefusal(checks.State, checks.Contexts, verdict, changed,
		task.Spec.AllowedProtectedPaths, protected); why != "" {
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
