/*
Copyright 2026 The theos Authors.

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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// A Crew's approval rules (docs/design.md, "Approval rules"; #62) say which
// of its changes wait for a person and which merge on CI alone. The Task
// asks them once its round's checks passed, of the pull request's files, its
// size and its issue's labels, and keeps the answer in status.approval. A
// change that needs None merges then, with no reviewer run; one that needs
// Person waits, after its agents approved, in phase AwaitingApproval, as a
// spec does at its review point.

// approvalApplies reports whether a Crew's approval rules are for the Task:
// an Implement Task that is no Standard change, whose gated merge needs a
// review run that still lasts, and no Benchmark's, which never merges.
func approvalApplies(task *c4v1beta1.Task) bool {
	kind := task.Spec.Kind
	return (kind == "" || kind == c4v1beta1.TaskImplement) && len(task.Spec.AllowedProtectedPaths) == 0 &&
		task.Spec.Benchmark == nil
}

// approvalRules are a Crew's approval rules as the model reads them.
func approvalRules(crew *c4v1beta1.Crew) []model.ApprovalRule {
	rules := make([]model.ApprovalRule, 0, len(crew.Spec.Approval))
	for _, r := range crew.Spec.Approval {
		rules = append(rules, model.ApprovalRule{Paths: r.Paths, Labels: r.Labels,
			ChangedLinesOver: int(r.ChangedLinesOver), Require: string(r.Require)})
	}
	return rules
}

// ruleFacts adds what the Crew's approval rules ask of the Task's change,
// and, while the change waits for a person, its pull request and what people
// said of it. The rules are asked once the round's checks passed and while
// the change is judged or waits, when status.approval is of another head or
// of other rules; a branch that only took main in keeps its answer. A Crew
// without rules, or gone, asks nothing: status.approval goes, and a Task
// that waited for a person merges on its agents' approval.
func (r *TaskReconciler) ruleFacts(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, rules []model.ApprovalRule, facts *model.TaskFacts) error {
	if !approvalApplies(task) || status.PullRequest == 0 {
		return nil
	}
	waiting := status.Phase == c4v1beta1.TaskAwaitingApproval
	if len(rules) == 0 {
		status.Approval = nil
	} else {
		passed := status.Phase == c4v1beta1.TaskChecking && facts.Checks == ports.CheckSuccess &&
			!status.MergeWhenChecksPass
		judged := status.Phase == c4v1beta1.TaskReviewing || status.Phase == c4v1beta1.TaskVerifying
		key := model.ApprovalRulesKey(rules)
		asked := status.Approval != nil && status.Approval.Commit == status.Commit && status.Approval.Rules == key
		if (passed || judged || waiting) && !asked {
			if err := r.askRules(ctx, task, status, container, rules, key); err != nil {
				return err
			}
		}
	}
	if a := status.Approval; a != nil {
		facts.Approval = model.ApprovalNeed{Require: string(a.Require), Rule: int(a.Rule), Why: a.Reason}
	}
	if !waiting {
		return nil
	}
	if err := r.waitingPull(ctx, task, status, container, facts); err != nil {
		return err
	}
	if a := status.Approval; a != nil && a.Require == c4v1beta1.ApprovalPerson {
		a.ApprovedBy, a.ApprovedInChat = facts.WaitingPull.People.ApprovedBy, facts.WaitingPull.People.ApprovedInChat
	}
	return nil
}

// askRules reads the Task's pull request, its changed files and its issue's
// labels, and records in status.approval what rules ask of them.
func (r *TaskReconciler) askRules(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, rules []model.ApprovalRule, key string) error {
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return err
	}
	var change model.ApprovalChange
	err = r.gitCall(ctx, func(ctx context.Context) (err error) {
		change.Files, err = provider.PullRequestFiles(ctx, repo, int(status.PullRequest))
		return err
	})
	if errors.Is(err, ports.ErrTooManyFiles) {
		change.TooManyFiles, err = true, nil
	}
	if err != nil {
		return err
	}
	if err := r.gitCall(ctx, func(ctx context.Context) error {
		pr, err := provider.GetPullRequest(ctx, repo, int(status.PullRequest))
		change.Lines = pr.Additions + pr.Deletions
		return err
	}); err != nil {
		return err
	}
	if task.Spec.Issue > 0 {
		err := r.gitCall(ctx, func(ctx context.Context) error {
			issue, err := provider.GetIssue(ctx, repo, int(task.Spec.Issue))
			change.Labels = issue.Labels
			return err
		})
		if err != nil && !errors.Is(err, ports.ErrIssueNotFound) {
			return err
		}
	}
	need := model.ApprovalFor(rules, change)
	before := status.Approval
	status.Approval = &c4v1beta1.TaskApproval{Require: c4v1beta1.ApprovalRequirement(need.Require),
		Rule: int32(need.Rule), Reason: model.Clip(need.Why, 1024), Commit: status.Commit, Rules: key}
	if before == nil || before.Require != status.Approval.Require || before.Rule != status.Approval.Rule {
		logf.FromContext(ctx).Info("Asked the Crew's approval rules", "require", need.Require, "rule", need.Rule,
			"why", need.Why, "files", len(change.Files), "lines", change.Lines, "commit", status.Commit)
	}
	return nil
}

// awaitPerson puts a change its agents approved up for a person's approval,
// as its Crew's approval rules ask: the reviewer's approval, or after it the
// verifier's, goes on the pull request, and what the review left for the
// merge stays in status.review.
func (r *TaskReconciler) awaitPerson(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents, step model.TaskStep) (ctrl.Result, error) {
	if status.Phase == c4v1beta1.TaskVerifying {
		// The reviewer's approval went on the pull request as the verification began.
		r.postVerification(ctx, task, status, container, agents.verifier, ports.ReviewApprove)
	} else {
		status.Review = r.reviewFindings(ctx, task, status)
		r.approve(ctx, task, status, container, agents.reviewer)
	}
	if err := r.endRun(ctx, task, status); err != nil {
		return ctrl.Result{}, err
	}
	setProgress(status, model.AwaitApproval(progressOf(status)))
	if status.Approval != nil {
		status.Approval.ApprovedBy, status.Approval.ApprovedInChat = "", false
	}
	r.progress(task, status, metav1.ConditionTrue, model.ReasonAwaitingApproval, changeWaitMessage(task, status))
	logf.FromContext(ctx).Info("Put the change up for a person's approval", "url", status.PullRequestURL,
		"round", status.Round, "message", step.Message)
	return ctrl.Result{RequeueAfter: hostRecheck(ctx, r.checksRecheck())}, nil
}

// changeWaitMessage is the status of a change waiting for a person.
func changeWaitMessage(task *c4v1beta1.Task, status *c4v1beta1.TaskStatus) string {
	why := ""
	if status.Approval != nil {
		why = status.Approval.Reason
	}
	return model.ChangeWaitMessage(int(task.Spec.Issue), status.PullRequestURL, why)
}
