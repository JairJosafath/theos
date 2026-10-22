/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// A change to a Container's spec.operations after the scaffold reaches its
// repository the way a Standard bump does: the operator renders the Framework
// at the ref it was scaffolded at with the new block, takes only
// infra/operations.ts, opens a pull request with it, and merges it as admin
// once CI passes. Every outcome is the condition OperationsApplied and
// status.operationsUpdate, and a Git error is retried at the next recheck.
// See docs/plans/aws-production.md, step 12.

// operationsUpdate is one pass over a Container's operations update.
type operationsUpdate struct {
	r         *ContainerReconciler
	container *c4v1alpha1.Container
	status    *c4v1alpha1.ContainerStatus
	// git reaches the Container's Git host; it is built on first use.
	git *gitAccess
	// failed says the pass ended on a Git error, which the next recheck retries.
	failed bool
}

// updateOperations keeps the repository's infra/operations.ts rendered from
// spec.operations, one step per reconcile, and reports it in OperationsApplied.
// live says whether the cached Container is the one on the API server: the
// steps act in Git from the update in status, so they are taken only then. It
// reports whether the pass ended on a Git error, for the caller to retry.
func (r *ContainerReconciler) updateOperations(ctx context.Context, container *c4v1alpha1.Container,
	status *c4v1alpha1.ContainerStatus, live func() bool) bool {
	if r.reportOperationsApplied(container, status) || !live() {
		return false
	}
	// Git calls get a deadline of their own, so the status write after them still lands.
	gitCtx, cancel := context.WithTimeout(ctx, r.scaffoldTimeout())
	defer cancel()
	u := &operationsUpdate{r: r, container: container, status: status}
	u.run(ctx, gitCtx)
	return u.failed
}

// operationsInFlight says the Container's operations update has a pull request
// that hasn't merged or stopped, which the operator reads again at each
// recheck. A stopped one is read again only when the Container is reconciled
// for another reason, as a stopped Standard update is.
func operationsInFlight(status *c4v1alpha1.ContainerStatus) bool {
	u := status.OperationsUpdate
	return status.Phase == c4v1alpha1.ContainerReady && u != nil && u.PullRequest != 0 && u.Stopped == "" &&
		!operationsMerged(status)
}

// operationsMerged says the update in status merged: it names the commit on
// main that holds its block, or its block is the one status records main
// rendered with. Only a merge records an update's block, and a new update is
// planned only for a block other than the recorded one, so the second holds
// for a merge the host named no commit for.
func operationsMerged(status *c4v1alpha1.ContainerStatus) bool {
	u := status.OperationsUpdate
	if u == nil {
		return false
	}
	return u.MergeCommit != "" || (status.Operations != nil &&
		model.ResolveOperations(operationsChoices(u.Operations)) ==
			model.ResolveOperations(operationsChoices(status.Operations)))
}

// operationsFacts are the facts of the Container's operations update before
// anything is read. status records the block the repository was rendered with.
func operationsFacts(container *c4v1alpha1.Container, status *c4v1alpha1.ContainerStatus) model.OperationsUpdateFacts {
	return model.OperationsUpdateFacts{Target: specOperations(container),
		Recorded: scaffoldedOperations(container, status), Update: operationsInStatus(status)}
}

// operationsInStatus is the update in status as the model sees it, or nil.
func operationsInStatus(status *c4v1alpha1.ContainerStatus) *model.OperationsUpdateInFlight {
	update := status.OperationsUpdate
	if update == nil {
		return nil
	}
	return &model.OperationsUpdateInFlight{
		Operations: model.ResolveOperations(operationsChoices(update.Operations)), Stopped: update.Stopped,
		PullRequest: int(update.PullRequest), PullRequestURL: update.PullRequestURL, Branch: update.Branch,
		Commit: update.Commit, Merged: operationsMerged(status),
	}
}

// run takes the steps towards spec.operations this pass can: it gathers the
// facts model.NextOperationsStep asks for and does what it decides.
func (u *operationsUpdate) run(ctx, gitCtx context.Context) {
	log := logf.FromContext(ctx)
	u.keep()
	f := operationsFacts(u.container, u.status)
	// mergeCommit is the commit the pull request's merge made, as last read.
	var mergeCommit string
	for {
		// What was read stays in f when the update is gone: the model looks at it
		// only while an update is in flight, and only Plan, which ends the pass,
		// starts one.
		f.Recorded, f.Update = scaffoldedOperations(u.container, u.status), operationsInStatus(u.status)
		step := model.NextOperationsStep(f)
		switch step.Action {
		case model.OperationsReadPR:
			pr, ok := u.readPullRequest(ctx, gitCtx, step.Quiet)
			if !ok {
				return
			}
			f.PR = &model.UpdatePullRequest{Merged: pr.Merged, Closed: pr.State == stateClosed,
				Mergeable: pr.Mergeable, HeadSHA: pr.HeadSHA}
			mergeCommit = pr.MergeCommit
		case model.OperationsReadChecks:
			git, ok := u.access(ctx)
			if !ok {
				return
			}
			checks, err := git.provider.CombinedStatus(gitCtx, git.provider.Repository(u.container.Name),
				u.status.OperationsUpdate.Commit)
			if err != nil {
				u.gitError(ctx, err)
				return
			}
			// A state the host leaves empty is pending, so the model doesn't ask again.
			f.Checks = cmp.Or(checks.State, gitprovider.CheckPending)
			f.ChecksURL, f.Contexts, f.CI = checks.URL, checks.Contexts, git.ci
		case model.OperationsReadFiles:
			git, ok := u.access(ctx)
			if !ok {
				return
			}
			changed, err := git.provider.PullRequestFiles(gitCtx, git.provider.Repository(u.container.Name),
				int(u.status.OperationsUpdate.PullRequest))
			switch {
			case errors.Is(err, gitprovider.ErrTooManyFiles):
				f.Files = &model.UpdateFiles{Unchecked: err.Error()}
			case err != nil:
				u.gitError(ctx, err) // never merge a path nobody checked
				return
			default:
				f.Files = &model.UpdateFiles{Changed: changed}
			}
		case model.OperationsUpToDate:
			u.r.operationsUpToDate(u.container, u.status)
			return
		case model.OperationsPlan:
			u.plan(ctx, gitCtx)
			return
		case model.OperationsForget:
			u.status.OperationsUpdate = nil
		case model.OperationsRecordMerge:
			u.merged(ctx, gitCtx, mergeCommit)
		case model.OperationsDrop:
			if !u.drop(ctx, gitCtx, step.Message) {
				return
			}
		case model.OperationsStopClosed:
			u.stopClosed(ctx, gitCtx, step.Message)
			return
		case model.OperationsStopPushed:
			u.status.OperationsUpdate.Stopped = c4v1alpha1.ReasonUpdateRefused
			u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonUpdateRefused, step.Message)
			log.Info("Stopped an operations update someone else pushed to",
				"url", u.status.OperationsUpdate.PullRequestURL, "head", f.PR.HeadSHA)
			return
		case model.OperationsRefuseMerge:
			u.status.OperationsUpdate.Stopped = c4v1alpha1.ReasonUpdateRefused
			u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonUpdateRefused, step.Message)
			log.Info("Refused to merge an operations update", "url", u.status.OperationsUpdate.PullRequestURL,
				"reason", step.Why)
			return
		case model.OperationsChecksFailed:
			u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonChecksFailed, step.Message)
			return
		case model.OperationsWaitForChecks:
			u.updating()
			return
		case model.OperationsForceMerge:
			// The merge is recorded at once: a host slow to show it merged must
			// not merge it again, or drop it.
			if !u.forceMerge(ctx, gitCtx) {
				return
			}
		default: // model.OperationsNone
			return
		}
	}
}

// plan renders the Framework at the ref it was scaffolded at with the block
// spec.operations resolves to, and opens a pull request that writes its
// infra/operations.ts, and nothing else, when main's differs.
func (u *operationsUpdate) plan(ctx, gitCtx context.Context) {
	log := logf.FromContext(ctx)
	name := u.container.Spec.FrameworkRef.Name
	from, to := scaffoldedOperations(u.container, u.status), specOperations(u.container)
	stopped := func(reason, message string) {
		u.status.OperationsUpdate = &c4v1alpha1.OperationsUpdateStatus{Operations: operationsStatus(to),
			Stopped: reason}
		u.condition(metav1.ConditionFalse, reason, message)
	}
	ref := model.ScaffoldedRef(u.status.ScaffoldedFrom, name)
	if ref == "" {
		stopped(c4v1alpha1.ReasonChangedAfterScaffold, fmt.Sprintf("spec.operations changed after the scaffold, "+
			"which doesn't record the Framework %s's ref, so the factory can't render %s again; change it by a Task",
			name, model.OperationsFile))
		return
	}
	var framework c4v1alpha1.Framework
	found, err := u.r.lookup(ctx, u.container.Namespace, name, &framework)
	if err != nil {
		log.Info("Could not read Framework", "framework", name, "error", err.Error())
		return
	}
	if !found {
		u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonNotFound, notFoundMessage([]string{"Framework " + name}))
		return
	}
	git, ok := u.access(ctx)
	if !ok {
		return
	}
	var creds *gitprovider.Credentials
	if gitprovider.SameHost(git.gitProvider.Spec.URL, framework.Spec.Template.URL) {
		creds = &git.creds
	}
	files, err := u.r.Git.FetchTemplate(gitCtx, framework.Spec.Template.URL, ref, creds)
	if err != nil {
		u.gitError(ctx, err)
		return
	}
	in := scaffoldInput(u.container, git.ci, scaffoldedConnectors(u.status), recordedChecks(u.status), to,
		git.provider.Repository(u.container.Name).NpmScope)
	tree, err := scaffold.RenderLayer(in, scaffold.Layer{Side: model.SideFramework, Name: name, Ref: ref, Files: files})
	if err != nil {
		stopped(c4v1alpha1.ReasonFailed, fmt.Sprintf("Could not render the Framework %s at %s: %v", name, ref, err))
		return
	}
	file, ok := tree[model.OperationsFile]
	if !ok {
		stopped(c4v1alpha1.ReasonChangedAfterScaffold, fmt.Sprintf("spec.operations changed after the scaffold, "+
			"and the Framework %s at %s renders no %s for the factory to render again; change the repository "+
			"by a Task", name, ref, model.OperationsFile))
		log.Info("Did not carry a changed spec.operations to a Framework that renders no operations file",
			"framework", name, "ref", ref)
		return
	}

	repo := git.provider.Repository(u.container.Name)
	current, base, err := git.provider.ReadFiles(gitCtx, repo, gitprovider.DefaultBranch,
		[]string{model.OperationsFile})
	if err != nil {
		u.gitError(ctx, err)
		return
	}
	if c, ok := current[model.OperationsFile]; ok && bytes.Equal(c.Data, file.Data) && c.Executable == file.Executable {
		u.alreadyUpToDate(ctx, to, base)
		return
	}
	branch := model.OperationsUpdateBranch(to)
	author := gitprovider.Signature{Name: scaffold.AuthorName, Email: scaffold.AuthorEmail, When: time.Now()}
	commit, err := git.provider.PushBranch(gitCtx, repo, base, branch, scaffold.Tree{model.OperationsFile: file},
		nil, scaffold.OperationsMessage(in, from), author)
	if errors.Is(err, gitprovider.ErrEmptyChange) {
		// main got there between the read and the push.
		u.alreadyUpToDate(ctx, to, base)
		return
	}
	if err != nil {
		u.gitError(ctx, err)
		return
	}
	pr, err := git.provider.OpenPullRequest(gitCtx, repo, branch, gitprovider.DefaultBranch,
		model.OperationsUpdateTitle(), model.OperationsUpdateBody(from, to))
	if err != nil {
		u.gitError(ctx, err)
		return
	}
	u.status.OperationsUpdate = &c4v1alpha1.OperationsUpdateStatus{Operations: operationsStatus(to), Branch: branch,
		Commit: commit, PullRequest: int32(pr.Number), PullRequestURL: pr.URL}
	u.updating()
	log.Info("Opened an operations update", "url", pr.URL, "operations", model.OperationsSummary(to))
}

// alreadyUpToDate records the block to when main's infra/operations.ts, at
// commit base, already renders it.
func (u *operationsUpdate) alreadyUpToDate(ctx context.Context, to model.Operations, base string) {
	u.status.Operations = operationsStatus(to)
	u.status.OperationsUpdate = &c4v1alpha1.OperationsUpdateStatus{Operations: operationsStatus(to),
		MergeCommit: base}
	u.r.operationsUpToDate(u.container, u.status)
	logf.FromContext(ctx).Info("Found infra/operations.ts already rendered from spec.operations", "commit", base)
}

// readPullRequest reads the update's pull request. When quiet, the update
// stopped and a Git error is only logged: the condition keeps saying why it
// stopped.
func (u *operationsUpdate) readPullRequest(ctx, gitCtx context.Context, quiet bool) (gitprovider.PullRequest, bool) {
	update := u.status.OperationsUpdate
	if quiet {
		git, err := gitAccessFor(ctx, u.r.Client, u.r.APIReader, u.r.Git, u.container.Namespace,
			u.container.Spec.GitProviderRef.Name)
		var pr gitprovider.PullRequest
		if err == nil {
			pr, err = git.provider.GetPullRequest(gitCtx, git.provider.Repository(u.container.Name),
				int(update.PullRequest))
		}
		if err != nil {
			logf.FromContext(ctx).Info("Could not read a stopped operations update", "url", update.PullRequestURL,
				"error", err.Error())
			return pr, false
		}
		return pr, true
	}
	git, ok := u.access(ctx)
	if !ok {
		return gitprovider.PullRequest{}, false
	}
	pr, err := git.provider.GetPullRequest(gitCtx, git.provider.Repository(u.container.Name), int(update.PullRequest))
	if err != nil {
		u.gitError(ctx, err)
		return pr, false
	}
	return pr, true
}

// stopClosed stops an update a person closed without merging, and deletes its
// branch.
func (u *operationsUpdate) stopClosed(ctx, gitCtx context.Context, message string) {
	log := logf.FromContext(ctx)
	update := u.status.OperationsUpdate
	update.Stopped = c4v1alpha1.ReasonUpdateClosed
	u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonUpdateClosed, message)
	if git, ok := u.access(ctx); ok {
		if err := git.provider.DeleteBranch(gitCtx, git.provider.Repository(u.container.Name),
			update.Branch); err != nil {
			log.Info("Could not delete the branch of a closed operations update", "branch", update.Branch,
				"error", err.Error())
		}
	}
	log.Info("Stopped an operations update closed without merging", "url", update.PullRequestURL)
}

// forceMerge merges the update's pull request with the admin override and
// records it. It reports false on a Git error.
func (u *operationsUpdate) forceMerge(ctx, gitCtx context.Context) bool {
	update := u.status.OperationsUpdate
	git, ok := u.access(ctx)
	if !ok {
		return false
	}
	in := scaffoldInput(u.container, model.CI{}, scaffoldedConnectors(u.status), recordedChecks(u.status),
		model.ResolveOperations(operationsChoices(update.Operations)), "")
	title := model.OperationsUpdateTitle()
	message, _ := strings.CutPrefix(scaffold.OperationsMessage(in, scaffoldedOperations(u.container, u.status)),
		title+"\n\n")
	if err := git.provider.ForceMergePullRequest(gitCtx, git.provider.Repository(u.container.Name),
		int(update.PullRequest), update.Commit, title, message); err != nil {
		u.gitError(ctx, err)
		return false
	}
	logf.FromContext(ctx).Info("Merged an operations update with the admin override", "url", update.PullRequestURL)
	// The pull request is read again only to name the commit the merge made; a
	// failed read leaves it to main's head.
	pr, err := git.provider.GetPullRequest(gitCtx, git.provider.Repository(u.container.Name), int(update.PullRequest))
	if err != nil {
		logf.FromContext(ctx).Info("Could not read a merged operations update", "url", update.PullRequestURL,
			"error", err.Error())
	}
	u.merged(ctx, gitCtx, pr.MergeCommit)
	return true
}

// drop closes the update's pull request and deletes its branch, then forgets
// the update. It reports false on a Git error.
func (u *operationsUpdate) drop(ctx, gitCtx context.Context, why string) bool {
	update := u.status.OperationsUpdate
	git, ok := u.access(ctx)
	if !ok {
		return false
	}
	repo := git.provider.Repository(u.container.Name)
	if err := git.provider.ClosePullRequest(gitCtx, repo, int(update.PullRequest)); err != nil {
		u.gitError(ctx, err)
		return false
	}
	if err := git.provider.DeleteBranch(gitCtx, repo, update.Branch); err != nil {
		u.gitError(ctx, err)
		return false
	}
	logf.FromContext(ctx).Info(why, "url", update.PullRequestURL)
	u.status.OperationsUpdate = nil
	return true
}

// merged records the update's pull request as merged at mergeCommit: the
// repository's infra/operations.ts renders its block. The pass goes on, and
// its next step reports it: up to date, or the next update.
func (u *operationsUpdate) merged(ctx, gitCtx context.Context, mergeCommit string) {
	update := u.status.OperationsUpdate
	// A host that doesn't say which commit the merge made leaves main's head,
	// which holds the block; the update's own commit is on no branch now. When
	// that can't be read either, the merge is recorded without a commit:
	// status.operations holds the block, which operationsMerged sees.
	if mergeCommit == "" {
		mergeCommit = u.mainHead(ctx, gitCtx)
	}
	update.MergeCommit, update.Stopped = mergeCommit, ""
	u.status.Operations = update.Operations.DeepCopy()
	logf.FromContext(ctx).Info("Recorded a merged operations update", "url", update.PullRequestURL,
		"commit", update.MergeCommit)
}

// mainHead is the commit main points at, or "" when it can't be read: a
// failure is only logged, as the merge it names is recorded either way.
func (u *operationsUpdate) mainHead(ctx, gitCtx context.Context) string {
	git := u.git
	if git == nil {
		access, err := gitAccessFor(ctx, u.r.Client, u.r.APIReader, u.r.Git, u.container.Namespace,
			u.container.Spec.GitProviderRef.Name)
		if err != nil {
			logf.FromContext(ctx).Info("Could not read main for a merged operations update", "error", err.Error())
			return ""
		}
		git = &access
	}
	head, err := git.provider.BranchCommit(gitCtx, git.provider.Repository(u.container.Name),
		gitprovider.DefaultBranch)
	if err != nil {
		logf.FromContext(ctx).Info("Could not read main for a merged operations update", "error", err.Error())
	}
	return head
}

// access returns the Container's Git host, building it on first use. A failure
// is reported like a Git error.
func (u *operationsUpdate) access(ctx context.Context) (*gitAccess, bool) {
	if u.git == nil {
		git, err := gitAccessFor(ctx, u.r.Client, u.r.APIReader, u.r.Git, u.container.Namespace,
			u.container.Spec.GitProviderRef.Name)
		if err != nil {
			u.gitError(ctx, err)
			return nil, false
		}
		u.git = &git
	}
	return u.git, true
}

// updating reports the update's pull request waiting for CI.
func (u *operationsUpdate) updating() {
	u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonUpdating, fmt.Sprintf(
		"Pull request %s renders %s from spec.operations; it merges once CI passes",
		u.status.OperationsUpdate.PullRequestURL, model.OperationsFile))
}

// keep sets the condition as it stands for the Container's generation, for a
// pass that ends without changing it.
func (u *operationsUpdate) keep() {
	if c := meta.FindStatusCondition(u.status.Conditions, c4v1alpha1.ConditionOperationsApplied); c != nil {
		u.condition(c.Status, c.Reason, c.Message)
	}
}

// gitError reports a Git error. The update's progress stays, and the next
// recheck tries again.
func (u *operationsUpdate) gitError(ctx context.Context, err error) {
	u.failed = true
	u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonGitError, err.Error()+"; retrying")
	logf.FromContext(ctx).Info("Could not update infra/operations.ts", "error", err.Error())
}

func (u *operationsUpdate) condition(s metav1.ConditionStatus, reason, message string) {
	u.r.condition(u.container, u.status, c4v1alpha1.ConditionOperationsApplied, s, reason, message)
}
