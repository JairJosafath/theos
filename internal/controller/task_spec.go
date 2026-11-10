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
	"fmt"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// A Spec Task (docs/design.md, "Spec") runs the implement flow on an issue
// labelled status/needs-spec with the spec writer's prompt: its agent writes
// docs/specs/<issue>.md and nothing else. Once CI and the reviewer passed the
// spec, it waits for its review point, phase AwaitingApproval, until a person
// approves its pull request (or the reviewer's approval is enough, the Crew's
// specApproval Reviewer). Then the operator merges it, copies its acceptance
// criteria into the issue and labels the issue status/ready; the issue stays
// open. A pull request a person closes unmerged declines the spec.

// isSpec reports whether t is a Spec Task.
func isSpec(t *c4v1beta1.Task) bool { return t.Spec.Kind == c4v1beta1.TaskKindSpec }

// sameKind reports whether two Tasks on an issue are tries at the same work:
// a spec's tries continue and replace only a spec's, a build's only a build's.
func sameKind(a, b *c4v1beta1.Task) bool { return isSpec(a) == isSpec(b) }

// specApproval is who approves the spec of a Task whose Crew says
// approval: Person unless the Crew says Reviewer.
func specApproval(approval c4v1beta1.SpecApproval) c4v1beta1.SpecApproval {
	if approval == c4v1beta1.SpecApprovalReviewer {
		return approval
	}
	return c4v1beta1.SpecApprovalPerson
}

// specFacts adds what a Spec Task's next step depends on: who approves its
// spec; once its round's checks passed, why its pull request can't go to
// review; and, while it waits for its approval, its pull request and what
// people said of it. It records the spec's file and approval in status.
func (r *TaskReconciler) specFacts(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, facts *model.TaskFacts) error {
	if !isSpec(task) {
		return nil
	}
	approval := specApproval(c4v1beta1.SpecApproval(facts.SpecApproval))
	facts.Spec, facts.SpecApproval = true, string(approval)
	if status.Spec == nil {
		status.Spec = &c4v1beta1.TaskSpecResult{}
	}
	status.Spec.Path, status.Spec.Approval = model.SpecPath(int(task.Spec.Issue)), approval
	switch {
	case status.Phase == c4v1beta1.TaskChecking && facts.Checks == ports.CheckSuccess && !status.MergeWhenChecksPass &&
		status.PullRequest != 0:
		refusal, err := r.specRefusal(ctx, status, container)
		facts.SpecRefusal = refusal
		return err
	case status.Phase == c4v1beta1.TaskAwaitingApproval:
		return r.specPull(ctx, task, status, container, facts)
	}
	return nil
}

// specRefusal reads the round's pull request and its spec file on the branch,
// and says why it can't go to review (model.SpecRefusal).
func (r *TaskReconciler) specRefusal(ctx context.Context, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container) (string, error) {
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return "", err
	}
	path := status.Spec.Path
	var changed []string
	err = r.gitCall(ctx, func(ctx context.Context) (err error) {
		changed, err = provider.PullRequestFiles(ctx, repo, int(status.PullRequest))
		return err
	})
	if errors.Is(err, ports.ErrTooManyFiles) {
		return model.SpecScopeRefusal(path, []string{"more files than the Git host lists"}), nil
	}
	if err != nil {
		return "", err
	}
	var spec []byte
	found := false
	err = r.gitCall(ctx, func(ctx context.Context) error {
		files, _, err := provider.ReadFiles(ctx, repo, status.Branch, []string{path})
		if f, ok := files[path]; ok {
			spec, found = f.Data, true
		}
		return err
	})
	if err != nil {
		return "", err
	}
	return model.SpecRefusal(path, changed, string(spec), found), nil
}

// specPull reads a waiting spec's pull request: merged or closed on the Git
// host, moved past the reviewed commit, behind main or in conflict with it,
// and what people other than the factory's own accounts said of the reviewed
// commit, there and from chat: the Task's approve-spec and spec-changes
// annotations, each counted as the person its -by companion names, which
// the admission policy holds to whoever wrote it (model.ChatSpecVerdict). A
// person's approval is recorded in status.
func (r *TaskReconciler) specPull(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, facts *model.TaskFacts) error {
	git, err := gitAccessFor(ctx, r.Client, r.APIReader, r.Git, container.Namespace, container.Spec.GitProviderRef.Name)
	if err != nil {
		return err
	}
	provider, repo := git.provider, git.provider.Repository(container.Name)
	var pr ports.PullRequest
	if err := r.gitCall(ctx, func(ctx context.Context) (err error) {
		pr, err = provider.GetPullRequest(ctx, repo, int(status.PullRequest))
		return err
	}); err != nil {
		return err
	}
	pull := &facts.SpecPull
	pull.Merged, pull.Closed = pr.Merged, pr.State == stateClosed && !pr.Merged
	pull.HeadMoved = pr.HeadSHA != "" && pr.HeadSHA != status.Commit
	if pull.Merged || pull.Closed || pull.HeadMoved {
		return nil
	}
	facts.Unmergeable, facts.Behind = !pr.Mergeable, pr.Behind
	var reviews []ports.PullReview
	if err := r.gitCall(ctx, func(ctx context.Context) (err error) {
		reviews, err = provider.PullRequestReviews(ctx, repo, int(status.PullRequest))
		return err
	}); err != nil {
		return err
	}
	seen := make([]model.PullReview, 0, len(reviews))
	for _, rv := range reviews {
		seen = append(seen, model.PullReview{User: rv.User, State: rv.State, Commit: rv.Commit, Body: rv.Body,
			Stale: rv.Stale, Dismissed: rv.Dismissed})
	}
	factory := func(user string) bool { return model.FactoryAccount(user, git.creds.Username, container.Namespace) }
	chat, ignored := model.ChatSpecVerdict(model.ChatWordOf(task.Annotations, model.ChatAnnotationApproveSpec),
		model.ChatWordOf(task.Annotations, model.ChatAnnotationSpecChanges), status.Commit)
	pull.People = model.CombineVerdicts(model.PersonReviews(seen, factory, status.Commit), chat)
	pull.Ignored = ignored
	if pull.People.ApprovedBy != "" && facts.SpecApproval == string(c4v1beta1.SpecApprovalPerson) {
		status.Spec.ApprovedBy, status.Spec.ApprovedInChat = pull.People.ApprovedBy, pull.People.ApprovedInChat
	}
	return nil
}

// applySpec does a step only a Spec Task takes: wait for its approval, or
// fail as declined.
func (r *TaskReconciler) applySpec(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents, step model.TaskStep) (ctrl.Result, error) {
	if step.Action == model.ActionSpecDeclined {
		return ctrl.Result{}, r.failWith(ctx, task, status, container, model.SpecDeclined(status.PullRequestURL))
	}
	return r.awaitApproval(ctx, task, status, container, agents, step)
}

// awaitApproval posts the reviewer's approval of the spec on its pull request
// and has the Task wait for a person's: its review point.
func (r *TaskReconciler) awaitApproval(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents, step model.TaskStep) (ctrl.Result, error) {
	status.Review = r.reviewFrom(ctx, task, status)
	r.approve(ctx, task, status, container, agents.reviewer)
	if err := r.endRun(ctx, task, status); err != nil {
		return ctrl.Result{}, err
	}
	setProgress(status, model.AwaitApproval(progressOf(status)))
	if status.Spec != nil {
		status.Spec.ApprovedBy, status.Spec.ApprovedInChat = "", false
	}
	r.progress(task, status, metav1.ConditionTrue, model.ReasonAwaitingApproval,
		model.SpecWaitMessage(int(task.Spec.Issue), status.PullRequestURL))
	logf.FromContext(ctx).Info("Put the spec up for a person's approval", "url", status.PullRequestURL,
		"round", status.Round, "message", step.Message)
	return ctrl.Result{RequeueAfter: hostRecheck(ctx, r.checksRecheck())}, nil
}

// syncNeedsYou sets the Task's NeedsYou condition while its spec waits for a
// person to approve its pull request, and takes it away once it doesn't.
func syncNeedsYou(task *c4v1beta1.Task, status *c4v1beta1.TaskStatus) {
	waiting := status.Phase == c4v1beta1.TaskAwaitingApproval &&
		(status.Spec == nil || status.Spec.Approval != c4v1beta1.SpecApprovalReviewer)
	if !waiting {
		apimeta.RemoveStatusCondition(&status.Conditions, c4v1beta1.ConditionNeedsYou)
		return
	}
	setCondition(&status.Conditions, task.Generation, c4v1beta1.ConditionNeedsYou, metav1.ConditionTrue,
		model.ReasonAwaitingApproval, model.SpecWaitMessage(int(task.Spec.Issue), status.PullRequestURL))
}

// labelSpecReview labels a spec's issue status/spec-review once its pull
// request is open.
func (r *TaskReconciler) labelSpecReview(ctx context.Context, task *c4v1beta1.Task, container *c4v1beta1.Container) {
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		logNotes(ctx, task, []error{err})
		return
	}
	n := int(task.Spec.Issue)
	logNotes(ctx, task, []error{
		r.gitCall(ctx, func(ctx context.Context) error { return provider.EnsureLabels(ctx, repo, issueLabels()) }),
		r.gitCall(ctx, func(ctx context.Context) error {
			return provider.AddIssueLabel(ctx, repo, n, model.LabelSpecReview)
		}),
		// The exclusive scope drops it too, but not where the labels came over
		// non-exclusive from a migration (#2039).
		r.gitCall(ctx, func(ctx context.Context) error {
			return provider.RemoveIssueLabel(ctx, repo, n, model.LabelInProgress)
		}),
	})
}

// specMerged copies the merged spec's acceptance criteria into its issue,
// with a link to the spec, and labels the issue status/ready, so the Crew
// builds it; the issue stays open (model.PlanSpecMerged). What it did goes in
// status.spec. A failed write is an error: the Task, still unmerged in
// status, finds its pull request merged on the next pass and comes back here.
func (r *TaskReconciler) specMerged(ctx context.Context, provider interface {
	ports.GitHost
	ports.Issues
}, repo ports.Repository, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus) error {
	n := int(task.Spec.Issue)
	path := model.SpecPath(n)
	var spec string
	if err := r.gitCall(ctx, func(ctx context.Context) error {
		files, _, err := provider.ReadFiles(ctx, repo, ports.DefaultBranch, []string{path})
		spec = string(files[path].Data)
		return err
	}); err != nil {
		return fmt.Errorf("reading the merged spec %s: %w", path, err)
	}
	var issue ports.Issue
	if err := r.gitCall(ctx, func(ctx context.Context) (err error) {
		issue, err = provider.GetIssue(ctx, repo, n)
		return err
	}); err != nil {
		return err
	}
	plan := model.PlanSpecMerged(task.Name, model.SpecIssue{Number: n, Body: issue.Body,
		Closed: issue.State == stateClosed}, path, repo.FileURL(path), status.PullRequestURL, spec)
	applied := plan.Skip
	if applied == "" {
		calls := []func(context.Context) error{
			func(ctx context.Context) error { return provider.EnsureLabels(ctx, repo, issueLabels()) },
		}
		if plan.Body != "" && plan.Body != issue.Body {
			calls = append(calls, func(ctx context.Context) error { return provider.EditIssueBody(ctx, repo, n, plan.Body) })
		}
		calls = append(calls, func(ctx context.Context) error { return provider.AddIssueLabel(ctx, repo, n, plan.Label) })
		// The exclusive scope drops them too, but not where the labels came
		// over non-exclusive from a migration (#2039).
		for _, l := range []string{model.LabelSpecReview, model.LabelInProgress, model.LabelNeedsSpec} {
			calls = append(calls, func(ctx context.Context) error { return provider.RemoveIssueLabel(ctx, repo, n, l) })
		}
		calls = append(calls, func(ctx context.Context) error { return provider.CommentIssue(ctx, repo, n, plan.Comment) })
		for _, call := range calls {
			if err := r.gitCall(ctx, call); err != nil {
				return err
			}
		}
		applied = plan.Applied
	}
	if status.Spec == nil {
		status.Spec = &c4v1beta1.TaskSpecResult{Path: path}
	}
	status.Spec.Criteria, status.Spec.Applied = plan.Criteria, applied
	logf.FromContext(ctx).Info("Copied the merged spec into its issue", "issue", n, "criteria", len(plan.Criteria),
		"applied", applied)
	return nil
}
