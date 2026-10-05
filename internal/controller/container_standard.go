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
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// A Standard's fixes reach the Containers that use it: when the Standard's
// template ref moves, the operator opens a pull request that brings the
// Standard's files in the repository to the new ref, and merges it as admin
// once CI passes. It is a report on a Ready Container, never a failure: every
// outcome is the condition StandardUpdated and status.standard, and a Git
// error is retried at the next recheck. See docs/plans/done/standard-update.md.

// standardUpdate is one pass over a Container's Standard update.
type standardUpdate struct {
	r         *ContainerReconciler
	container *c4v1alpha1.Container
	status    *c4v1alpha1.ContainerStatus
	// git reaches the Container's Git host; it is built on first use.
	git *gitAccess
}

// updateStandard keeps the Container's Standard files at its Standard's ref, one
// step per reconcile, and reports it in status. The Container names a Standard.
func (r *ContainerReconciler) updateStandard(ctx context.Context, container *c4v1alpha1.Container,
	status *c4v1alpha1.ContainerStatus) {
	name := container.Spec.StandardRef.Name
	if status.Standard == nil || status.Standard.Name != name {
		ref := model.ScaffoldedRef(status.ScaffoldedFrom, name)
		if ref == "" {
			r.condition(container, status, c4v1alpha1.ConditionStandardUpdated, metav1.ConditionFalse,
				c4v1alpha1.ReasonAddedAfterScaffold, fmt.Sprintf(
					"The Standard %s was not part of the scaffold; its updates don't reach this Container", name))
			return
		}
		status.Standard = &c4v1alpha1.ContainerStandardStatus{Name: name, Ref: ref}
	}

	var standard c4v1alpha1.Standard
	found, err := r.lookup(ctx, container.Namespace, name, &standard)
	if err != nil {
		logf.FromContext(ctx).Info("Could not read Standard", "standard", name, "error", err.Error())
		return
	}
	if !found {
		r.condition(container, status, c4v1alpha1.ConditionStandardUpdated, metav1.ConditionFalse,
			c4v1alpha1.ReasonNotFound, notFoundMessage([]string{"Standard " + name}))
		return
	}

	// Git calls get a deadline of their own, so the status write after them still lands.
	gitCtx, cancel := context.WithTimeout(ctx, r.scaffoldTimeout())
	defer cancel()
	u := &standardUpdate{r: r, container: container, status: status}
	u.run(ctx, gitCtx, &standard)
}

// run takes the next step towards standard's ref.
func (u *standardUpdate) run(ctx, gitCtx context.Context, standard *c4v1alpha1.Standard) {
	s := u.status.Standard
	target := standard.Spec.Template.Ref
	if update := s.Update; update != nil {
		switch {
		case update.Ref != target:
			// The Standard moved on: end the update and plan towards its new ref.
			if update.Stopped == "" && !u.movePast(ctx, gitCtx) {
				return
			}
			s.Update = nil
		case update.Stopped != "":
			u.followStopped(ctx, gitCtx)
			return
		default:
			if !u.follow(ctx, gitCtx) {
				return
			}
		}
	}
	if s.Ref == target {
		u.upToDate()
		return
	}
	u.plan(ctx, gitCtx, standard)
}

// plan renders the Standard at the ref its files are at and at target, and
// opens a pull request with what main still holds as the old render made it.
func (u *standardUpdate) plan(ctx, gitCtx context.Context, standard *c4v1alpha1.Standard) {
	log := logf.FromContext(ctx)
	s := u.status.Standard
	name, from, to := s.Name, s.Ref, standard.Spec.Template.Ref
	git, ok := u.access(ctx)
	if !ok {
		return
	}
	var creds *gitprovider.Credentials
	if gitprovider.SameHost(git.gitProvider.Spec.URL, standard.Spec.Template.URL) {
		creds = &git.creds
	}
	oldFiles, err := u.r.Git.FetchTemplate(gitCtx, standard.Spec.Template.URL, from, creds)
	var nextFiles scaffold.Tree
	if err == nil {
		nextFiles, err = u.r.Git.FetchTemplate(gitCtx, standard.Spec.Template.URL, to, creds)
	}
	if errors.Is(err, gitprovider.ErrUnpinnedRef) {
		s.Update = &c4v1alpha1.StandardUpdateStatus{Ref: to, Stopped: c4v1alpha1.ReasonUnpinnedRef}
		u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonUnpinnedRef, fmt.Sprintf("Standard %s: %v", name, err))
		return
	}
	if err != nil {
		u.gitError(ctx, err)
		return
	}

	in := scaffoldInput(u.container, scaffoldedConnectors(u.status))
	render := func(ref string, files scaffold.Tree) (scaffold.Tree, bool) {
		tree, err := scaffold.RenderLayer(in, scaffold.Layer{Side: model.SideStandard, Name: name, Ref: ref, Files: files})
		if err != nil {
			s.Update = &c4v1alpha1.StandardUpdateStatus{Ref: to, Stopped: c4v1alpha1.ReasonFailed}
			u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonFailed,
				fmt.Sprintf("Could not render the Standard %s at %s: %v", name, ref, err))
			return nil, false
		}
		return tree, true
	}
	old, ok := render(from, oldFiles)
	if !ok {
		return
	}
	next, ok := render(to, nextFiles)
	if !ok {
		return
	}

	repo := git.provider.Repository(u.container.Name)
	paths := append(old.Paths(), next.Paths()...)
	slices.Sort(paths)
	current, base, err := git.provider.ReadFiles(gitCtx, repo, gitprovider.DefaultBranch, slices.Compact(paths))
	if err != nil {
		u.gitError(ctx, err)
		return
	}
	plan := scaffold.PlanUpdate(old, next, current)
	if plan.Empty() {
		u.alreadyUpToDate(ctx, to, plan.LeftOut)
		return
	}

	branch := model.StandardUpdateBranch(name, to)
	author := gitprovider.Signature{Name: scaffold.AuthorName, Email: scaffold.AuthorEmail, When: time.Now()}
	commit, err := git.provider.PushBranch(gitCtx, repo, base, branch, plan.Files, plan.Removes,
		scaffold.UpdateMessage(in, name, from, to, plan), author)
	if errors.Is(err, gitprovider.ErrEmptyChange) {
		// main got there between the read and the push.
		u.alreadyUpToDate(ctx, to, plan.LeftOut)
		return
	}
	if err != nil {
		u.gitError(ctx, err)
		return
	}
	pr, err := git.provider.OpenPullRequest(gitCtx, repo, branch, gitprovider.DefaultBranch,
		model.StandardUpdateTitle(name, to), model.StandardUpdateBody(name, from, to, plan.Paths(), plan.LeftOut))
	if err != nil {
		u.gitError(ctx, err)
		return
	}
	s.Update = &c4v1alpha1.StandardUpdateStatus{
		Ref: to, Branch: branch, Commit: commit, PullRequest: int32(pr.Number), PullRequestURL: pr.URL,
		Paths: plan.Paths(), LeftOut: plan.LeftOut,
	}
	u.updating()
	log.Info("Opened a Standard update", "url", pr.URL, "standard", name, "from", from, "to", to,
		"paths", plan.Paths(), "leftOut", plan.LeftOut)
}

// alreadyUpToDate records ref when main already holds what it renders, apart
// from leftOut.
func (u *standardUpdate) alreadyUpToDate(ctx context.Context, ref string, leftOut []string) {
	s := u.status.Standard
	from := s.Ref
	s.Ref, s.LeftOut, s.Update = ref, leftOut, nil
	u.upToDate()
	logf.FromContext(ctx).Info("Found the Standard's files up to date", "standard", s.Name, "from", from, "to", ref,
		"leftOut", leftOut)
}

// follow takes the update in flight one step: record its merge, stop it when a
// person closed it or pushed to it, wait for CI, or merge it. It reports true
// when the update is gone and the Standard is to be planned again.
func (u *standardUpdate) follow(ctx, gitCtx context.Context) bool {
	log := logf.FromContext(ctx)
	s := u.status.Standard
	update := s.Update
	git, ok := u.access(ctx)
	if !ok {
		return false
	}
	repo := git.provider.Repository(u.container.Name)
	n := int(update.PullRequest)
	pr, err := git.provider.GetPullRequest(gitCtx, repo, n)
	if err != nil {
		u.gitError(ctx, err)
		return false
	}
	switch {
	case pr.Merged:
		u.merged(ctx)
		return false
	case pr.State == stateClosed:
		update.Stopped = c4v1alpha1.ReasonUpdateClosed
		u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonUpdateClosed, fmt.Sprintf(
			"Pull request %s was closed without merging; %s stays at %s until its ref changes again",
			update.PullRequestURL, s.Name, s.Ref))
		if err := git.provider.DeleteBranch(gitCtx, repo, update.Branch); err != nil {
			log.Info("Could not delete the branch of a closed Standard update", "branch", update.Branch,
				"error", err.Error())
		}
		log.Info("Stopped a Standard update closed without merging", "url", update.PullRequestURL)
		return false
	case !pr.Mergeable:
		// main moved with conflicting changes: plan again from the new main.
		return u.drop(ctx, gitCtx, "Closed a Standard update that conflicts with main")
	case pr.HeadSHA != update.Commit:
		update.Stopped = c4v1alpha1.ReasonUpdateRefused
		u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonUpdateRefused, fmt.Sprintf(
			"Someone else pushed to %s; a person looks at %s", update.Branch, update.PullRequestURL))
		log.Info("Stopped a Standard update someone else pushed to", "url", update.PullRequestURL,
			"head", pr.HeadSHA)
		return false
	}

	checks, err := git.provider.CombinedStatus(gitCtx, repo, update.Commit)
	if err != nil {
		u.gitError(ctx, err)
		return false
	}
	switch checks.State {
	case gitprovider.CheckSuccess:
	case gitprovider.CheckFailure, gitprovider.CheckError:
		u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonChecksFailed, fmt.Sprintf(
			"CI is %s on %s; the update merges once a re-run passes, or close %s to drop it",
			checks.State, checks.URL, update.PullRequestURL))
		return false
	default:
		u.updating()
		return false
	}

	changed, err := git.provider.PullRequestFiles(gitCtx, repo, n)
	if errors.Is(err, gitprovider.ErrTooManyFiles) {
		u.refuse(ctx, "not every changed path can be checked: "+err.Error())
		return false
	}
	if err != nil {
		u.gitError(ctx, err) // never merge a path nobody checked
		return false
	}
	if why := model.StandardUpdateRefusal(checks.State, checks.Contexts, changed, update.Paths); why != "" {
		u.refuse(ctx, why)
		return false
	}
	title, message := u.mergeMessage()
	if err := git.provider.ForceMergePullRequest(gitCtx, repo, n, update.Commit, title, message); err != nil {
		u.gitError(ctx, err)
		return false
	}
	log.Info("Merged a Standard update with the admin override", "url", update.PullRequestURL, "paths", changed)
	u.merged(ctx)
	return false
}

// movePast ends the update in flight when the Standard moved past its ref: it
// records the pull request if it merged meanwhile, and otherwise closes it and
// deletes its branch. It reports false on a Git error.
func (u *standardUpdate) movePast(ctx, gitCtx context.Context) bool {
	git, ok := u.access(ctx)
	if !ok {
		return false
	}
	pr, err := git.provider.GetPullRequest(gitCtx, git.provider.Repository(u.container.Name),
		int(u.status.Standard.Update.PullRequest))
	if err != nil {
		u.gitError(ctx, err)
		return false
	}
	if pr.Merged {
		u.merged(ctx)
		return true
	}
	return u.drop(ctx, gitCtx, "Closed a Standard update its Standard moved past")
}

// followStopped records a stopped update's pull request if a person merged it
// after all. Otherwise the update waits for the Standard's ref to change, and
// the condition keeps saying why it stopped.
func (u *standardUpdate) followStopped(ctx, gitCtx context.Context) {
	update := u.status.Standard.Update
	if update.PullRequest == 0 {
		return
	}
	git, err := gitAccessFor(ctx, u.r.Client, u.r.APIReader, u.r.Git, u.container.Namespace,
		u.container.Spec.GitProviderRef.Name)
	var pr gitprovider.PullRequest
	if err == nil {
		pr, err = git.provider.GetPullRequest(gitCtx, git.provider.Repository(u.container.Name),
			int(update.PullRequest))
	}
	if err != nil {
		logf.FromContext(ctx).Info("Could not read a stopped Standard update", "url", update.PullRequestURL,
			"error", err.Error())
		return
	}
	if pr.Merged {
		u.merged(ctx)
	}
}

// drop closes the update's pull request and deletes its branch, then forgets
// the update. It reports false on a Git error.
func (u *standardUpdate) drop(ctx, gitCtx context.Context, why string) bool {
	update := u.status.Standard.Update
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
	logf.FromContext(ctx).Info(why, "url", update.PullRequestURL, "ref", update.Ref)
	u.status.Standard.Update = nil
	return true
}

// merged records the update's pull request as merged: the Standard's files are
// at its ref.
func (u *standardUpdate) merged(ctx context.Context) {
	s := u.status.Standard
	update := s.Update
	s.Ref, s.LeftOut, s.Update = update.Ref, update.LeftOut, nil
	u.upToDate()
	logf.FromContext(ctx).Info("Recorded a merged Standard update", "url", update.PullRequestURL,
		"standard", s.Name, "ref", s.Ref)
}

// refuse stops the update: its pull request may not merge, and a person looks at it.
func (u *standardUpdate) refuse(ctx context.Context, why string) {
	update := u.status.Standard.Update
	update.Stopped = c4v1alpha1.ReasonUpdateRefused
	u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonUpdateRefused, fmt.Sprintf(
		"The update may not merge: %s; a person looks at %s", why, update.PullRequestURL))
	logf.FromContext(ctx).Info("Refused to merge a Standard update", "url", update.PullRequestURL, "reason", why)
}

// mergeMessage is the title and message of the update's squash commit on main:
// the update's own commit message.
func (u *standardUpdate) mergeMessage() (title, message string) {
	s := u.status.Standard
	update := s.Update
	title = model.StandardUpdateTitle(s.Name, update.Ref)
	full := scaffold.UpdateMessage(scaffoldInput(u.container, scaffoldedConnectors(u.status)), s.Name, s.Ref,
		update.Ref, scaffold.Update{LeftOut: update.LeftOut})
	message, _ = strings.CutPrefix(full, title+"\n\n")
	return title, message
}

// access returns the Container's Git host, building it on first use. A failure
// is reported like a Git error.
func (u *standardUpdate) access(ctx context.Context) (*gitAccess, bool) {
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

// upToDate reports the Standard's files at its ref, naming the files the last
// update left out.
func (u *standardUpdate) upToDate() {
	s := u.status.Standard
	message := fmt.Sprintf("%s is at %s", s.Name, s.Ref)
	if len(s.LeftOut) > 0 {
		message += fmt.Sprintf("; its last update left out %s, changed in the repository since: "+
			"a person brings them up to date", strings.Join(s.LeftOut, ", "))
	}
	u.condition(metav1.ConditionTrue, c4v1alpha1.ReasonUpToDate, message)
}

// updating reports the update's pull request waiting for CI.
func (u *standardUpdate) updating() {
	s := u.status.Standard
	u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonUpdating, fmt.Sprintf(
		"Pull request %s brings %s from %s to %s; it merges once CI passes",
		s.Update.PullRequestURL, s.Name, s.Ref, s.Update.Ref))
}

// gitError reports a Git error. The update's progress stays, and the next
// recheck tries again.
func (u *standardUpdate) gitError(ctx context.Context, err error) {
	u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonGitError, err.Error()+"; retrying")
	logf.FromContext(ctx).Info("Could not update the Standard's files", "standard", u.status.Standard.Name,
		"error", err.Error())
}

func (u *standardUpdate) condition(s metav1.ConditionStatus, reason, message string) {
	u.r.condition(u.container, u.status, c4v1alpha1.ConditionStandardUpdated, s, reason, message)
}

// scaffoldedConnectors are the Connectors the scaffold applied, as the scaffold
// passed them to the templates: unique and sorted. A Container scaffolded
// before appliedConnectors was kept has none.
func scaffoldedConnectors(status *c4v1alpha1.ContainerStatus) []string {
	names := make([]string, 0, len(status.AppliedConnectors))
	for _, a := range status.AppliedConnectors {
		names = append(names, a.Connector)
	}
	slices.Sort(names)
	return slices.Compact(names)
}
