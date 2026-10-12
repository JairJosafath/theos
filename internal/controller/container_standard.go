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
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// eventStandardFilesBehind is the Warning Event that says a Container's
// Standard files were left out of an update and wait on a person.
const eventStandardFilesBehind = "StandardFilesBehind"

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

	// The cache can lag behind our own status writes, and the steps act in Git
	// from the update in status. Take them only from the live Container, so a
	// reconcile queued during the last pass doesn't open an update a second time
	// or leave the one it replaced open; the watch brings the newer one.
	var live c4v1alpha1.Container
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(container), &live); err != nil {
		logf.FromContext(ctx).Info("Could not read Container", "error", err.Error())
		return
	}
	if live.ResourceVersion != container.ResourceVersion {
		return
	}

	// Git calls get a deadline of their own, so the status write after them still lands.
	gitCtx, cancel := context.WithTimeout(ctx, r.scaffoldTimeout())
	defer cancel()
	u := &standardUpdate{r: r, container: container, status: status}
	u.run(ctx, gitCtx, &standard)
}

// run takes the steps towards standard's ref this pass can: it gathers the facts
// model.NextStandardStep asks for and does what it decides.
func (u *standardUpdate) run(ctx, gitCtx context.Context, standard *c4v1alpha1.Standard) {
	log := logf.FromContext(ctx)
	s := u.status.Standard
	f := model.StandardUpdateFacts{Name: s.Name, Target: standard.Spec.Template.Ref}
	for {
		// What was read stays in f when the update is gone: the model looks at it
		// only while an update is in flight, and only Plan, which ends the pass,
		// starts one.
		f.Ref, f.Update = s.Ref, inFlight(s.Update)
		step := model.NextStandardStep(f)
		switch step.Action {
		case model.StandardReadPR:
			pr, ok := u.readPullRequest(ctx, gitCtx, step.Quiet)
			if !ok {
				return
			}
			f.PR = &model.UpdatePullRequest{Merged: pr.Merged, Closed: pr.State == stateClosed,
				Mergeable: pr.Mergeable, HeadSHA: pr.HeadSHA}
		case model.StandardReadChecks:
			git, ok := u.access(ctx)
			if !ok {
				return
			}
			checks, err := git.provider.CombinedStatus(gitCtx, git.provider.Repository(u.container.Name),
				s.Update.Commit)
			if err != nil {
				u.gitError(ctx, err)
				return
			}
			// A state the host leaves empty is pending, so the model doesn't ask again.
			f.Checks = cmp.Or(checks.State, gitprovider.CheckPending)
			f.ChecksURL, f.Contexts = checks.URL, checks.Contexts
		case model.StandardReadFiles:
			git, ok := u.access(ctx)
			if !ok {
				return
			}
			changed, err := git.provider.PullRequestFiles(gitCtx, git.provider.Repository(u.container.Name),
				int(s.Update.PullRequest))
			switch {
			case errors.Is(err, gitprovider.ErrTooManyFiles):
				f.Files = &model.UpdateFiles{Unchecked: err.Error()}
			case err != nil:
				u.gitError(ctx, err) // never merge a path nobody checked
				return
			default:
				f.Files = &model.UpdateFiles{Changed: changed}
			}
		case model.StandardUpToDate:
			u.upToDate(ctx)
			return
		case model.StandardPlan:
			u.plan(ctx, gitCtx, standard)
			return
		case model.StandardForget:
			s.Update = nil
		case model.StandardRecordMerge:
			u.merged(ctx)
		case model.StandardDrop:
			if !u.drop(ctx, gitCtx, step.Message) {
				return
			}
		case model.StandardStopClosed:
			u.stopClosed(ctx, gitCtx, step.Message)
			return
		case model.StandardStopPushed:
			s.Update.Stopped = c4v1alpha1.ReasonUpdateRefused
			u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonUpdateRefused, step.Message)
			log.Info("Stopped a Standard update someone else pushed to", "url", s.Update.PullRequestURL,
				"head", f.PR.HeadSHA)
			return
		case model.StandardRefuseMerge:
			s.Update.Stopped = c4v1alpha1.ReasonUpdateRefused
			u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonUpdateRefused, step.Message)
			log.Info("Refused to merge a Standard update", "url", s.Update.PullRequestURL, "reason", step.Why)
			return
		case model.StandardChecksFailed:
			u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonChecksFailed, step.Message)
			return
		case model.StandardWaitForChecks:
			u.updating()
			return
		case model.StandardForceMerge:
			if !u.forceMerge(ctx, gitCtx, f.Files.Changed) {
				return
			}
		default: // model.StandardNone
			return
		}
	}
}

// inFlight is the update in status as the model sees it, or nil.
func inFlight(update *c4v1alpha1.StandardUpdateStatus) *model.StandardUpdateInFlight {
	if update == nil {
		return nil
	}
	return &model.StandardUpdateInFlight{
		Ref: update.Ref, Stopped: update.Stopped, PullRequest: int(update.PullRequest),
		PullRequestURL: update.PullRequestURL, Branch: update.Branch, Commit: update.Commit, Paths: update.Paths,
	}
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

	in := scaffoldInput(u.container, scaffoldedConnectors(u.status), recordedChecks(u.status))
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

	// Each file a past update left out is merged against the render its copy
	// was last brought to.
	behind := behindFiles(s)
	renders := map[string]scaffold.Tree{from: old}
	bases := map[string]scaffold.Tree{}
	for _, b := range behind {
		if _, ok := renders[b.Ref]; !ok {
			files, err := u.r.Git.FetchTemplate(gitCtx, standard.Spec.Template.URL, b.Ref, creds)
			if err != nil {
				u.gitError(ctx, err)
				return
			}
			if renders[b.Ref], ok = render(b.Ref, files); !ok {
				return
			}
		}
		bases[b.Path] = renders[b.Ref]
	}

	repo := git.provider.Repository(u.container.Name)
	paths := append(old.Paths(), next.Paths()...)
	for p := range bases {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	current, base, err := git.provider.ReadFiles(gitCtx, repo, gitprovider.DefaultBranch, slices.Compact(paths))
	if err != nil {
		u.gitError(ctx, err)
		return
	}
	plan := scaffold.PlanUpdate(old, next, current, bases)
	if plan.Empty() {
		u.alreadyUpToDate(ctx, to, nextBehind(behind, from, plan.LeftOut, ""))
		return
	}

	branch := model.StandardUpdateBranch(name, to)
	author := gitprovider.Signature{Name: scaffold.AuthorName, Email: scaffold.AuthorEmail, When: time.Now()}
	commit, err := git.provider.PushBranch(gitCtx, repo, base, branch, plan.Files, plan.Removes,
		scaffold.UpdateMessage(in, name, from, to, plan), author)
	if errors.Is(err, gitprovider.ErrEmptyChange) {
		// main got there between the read and the push.
		u.alreadyUpToDate(ctx, to, nextBehind(behind, from, plan.LeftOut, ""))
		return
	}
	if err != nil {
		u.gitError(ctx, err)
		return
	}
	pr, err := git.provider.OpenPullRequest(gitCtx, repo, branch, gitprovider.DefaultBranch,
		model.StandardUpdateTitle(name, to), model.StandardUpdateBody(name, from, to, plan.Paths(), plan.Merged, plan.LeftOut))
	if err != nil {
		u.gitError(ctx, err)
		return
	}
	s.Update = &c4v1alpha1.StandardUpdateStatus{
		Ref: to, Branch: branch, Commit: commit, PullRequest: int32(pr.Number), PullRequestURL: pr.URL,
		Paths: plan.Paths(), Merged: plan.Merged, LeftOut: plan.LeftOut,
	}
	u.updating()
	log.Info("Opened a Standard update", "url", pr.URL, "standard", name, "from", from, "to", to,
		"paths", plan.Paths(), "merged", plan.Merged, "leftOut", plan.LeftOut)
}

// alreadyUpToDate records ref when main already holds what it renders, apart
// from the files behind.
func (u *standardUpdate) alreadyUpToDate(ctx context.Context, ref string, behind []c4v1alpha1.StandardFile) {
	s := u.status.Standard
	from := s.Ref
	s.Ref, s.Update = ref, nil
	setBehind(s, behind)
	u.upToDate(ctx)
	logf.FromContext(ctx).Info("Found the Standard's files up to date", "standard", s.Name, "from", from, "to", ref,
		"leftOut", s.LeftOut)
}

// behindFiles are the files s says are behind. A path of leftOut that behind
// doesn't list, recorded before behind was kept, is taken as at s.Ref.
func behindFiles(s *c4v1alpha1.ContainerStandardStatus) []c4v1alpha1.StandardFile {
	behind := slices.Clone(s.Behind)
	for _, p := range s.LeftOut {
		if !slices.ContainsFunc(behind, func(b c4v1alpha1.StandardFile) bool { return b.Path == p }) {
			behind = append(behind, c4v1alpha1.StandardFile{Path: p, Ref: s.Ref})
		}
	}
	return behind
}

// nextBehind are the files behind once an update from ref from leaves out
// leftOut, through the pull request at url when it opened one: a file behind
// before keeps the ref its copy was last brought to, a newly left-out one is
// at from, and a file the update brought up to date is no longer behind.
func nextBehind(behind []c4v1alpha1.StandardFile, from string, leftOut []string,
	url string) []c4v1alpha1.StandardFile {
	next := make([]c4v1alpha1.StandardFile, 0, len(leftOut))
	for _, p := range leftOut {
		f := c4v1alpha1.StandardFile{Path: p, Ref: from}
		if i := slices.IndexFunc(behind, func(b c4v1alpha1.StandardFile) bool { return b.Path == p }); i >= 0 {
			f = behind[i]
		}
		f.PullRequestURL = cmp.Or(url, f.PullRequestURL)
		next = append(next, f)
	}
	return next
}

// setBehind records the files behind, and their paths in leftOut.
func setBehind(s *c4v1alpha1.ContainerStandardStatus, behind []c4v1alpha1.StandardFile) {
	s.Behind, s.LeftOut = nil, nil
	for _, b := range behind {
		s.Behind = append(s.Behind, b)
		s.LeftOut = append(s.LeftOut, b.Path)
	}
}

// readPullRequest reads the update's pull request. When quiet, the update
// stopped and a Git error is only logged: the condition keeps saying why it
// stopped.
func (u *standardUpdate) readPullRequest(ctx, gitCtx context.Context, quiet bool) (gitprovider.PullRequest, bool) {
	update := u.status.Standard.Update
	if quiet {
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
func (u *standardUpdate) stopClosed(ctx, gitCtx context.Context, message string) {
	log := logf.FromContext(ctx)
	update := u.status.Standard.Update
	update.Stopped = c4v1alpha1.ReasonUpdateClosed
	u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonUpdateClosed, message)
	if git, ok := u.access(ctx); ok {
		if err := git.provider.DeleteBranch(gitCtx, git.provider.Repository(u.container.Name),
			update.Branch); err != nil {
			log.Info("Could not delete the branch of a closed Standard update", "branch", update.Branch,
				"error", err.Error())
		}
	}
	log.Info("Stopped a Standard update closed without merging", "url", update.PullRequestURL)
}

// forceMerge merges the update's pull request with the admin override and
// records it. It reports false on a Git error.
func (u *standardUpdate) forceMerge(ctx, gitCtx context.Context, changed []string) bool {
	update := u.status.Standard.Update
	git, ok := u.access(ctx)
	if !ok {
		return false
	}
	title, message := u.mergeMessage()
	if err := git.provider.ForceMergePullRequest(gitCtx, git.provider.Repository(u.container.Name),
		int(update.PullRequest), update.Commit, title, message); err != nil {
		u.gitError(ctx, err)
		return false
	}
	logf.FromContext(ctx).Info("Merged a Standard update with the admin override", "url", update.PullRequestURL,
		"paths", changed)
	u.merged(ctx)
	return true
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
// at its ref. The pass goes on, and its next step reports it: up to date, or
// the next update.
func (u *standardUpdate) merged(ctx context.Context) {
	s := u.status.Standard
	update := s.Update
	behind := nextBehind(behindFiles(s), s.Ref, update.LeftOut, update.PullRequestURL)
	s.Ref, s.Update = update.Ref, nil
	setBehind(s, behind)
	logf.FromContext(ctx).Info("Recorded a merged Standard update", "url", update.PullRequestURL,
		"standard", s.Name, "ref", s.Ref)
}

// mergeMessage is the title and message of the update's squash commit on main:
// the update's own commit message.
func (u *standardUpdate) mergeMessage() (title, message string) {
	s := u.status.Standard
	update := s.Update
	title = model.StandardUpdateTitle(s.Name, update.Ref)
	full := scaffold.UpdateMessage(scaffoldInput(u.container, scaffoldedConnectors(u.status), recordedChecks(u.status)), s.Name, s.Ref,
		update.Ref, scaffold.Update{Merged: update.Merged, LeftOut: update.LeftOut})
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

// upToDate reports the Standard's files at its ref, or the files its updates
// left out waiting on a person, with a Warning Event once per ref.
func (u *standardUpdate) upToDate(ctx context.Context) {
	s := u.status.Standard
	behind := behindFiles(s)
	setBehind(s, behind)
	if len(behind) == 0 {
		u.condition(metav1.ConditionTrue, c4v1alpha1.ReasonUpToDate, fmt.Sprintf("%s is at %s", s.Name, s.Ref))
		return
	}
	files := make([]string, 0, len(behind))
	for _, b := range behind {
		files = append(files, fmt.Sprintf("%s (at %s)", b.Path, b.Ref))
	}
	annotate := fmt.Sprintf("kubectl -n %s annotate container %s", u.container.Namespace, u.container.Name)
	message := fmt.Sprintf("%s is at %s, but its updates left out %s: the repository changed them in lines "+
		"the Standard changed too. Take the Standard's version: `%s %s=%s`; or keep the repository's: `%s %s=%s`",
		s.Name, s.Ref, strings.Join(files, ", "), annotate, c4v1alpha1.AnnotationStandardTake, s.Ref,
		annotate, c4v1alpha1.AnnotationStandardKeep, s.Ref)
	was := meta.FindStatusCondition(u.container.Status.Conditions, c4v1alpha1.ConditionStandardUpdated)
	u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonFilesBehind, message)
	if was != nil && was.Reason == c4v1alpha1.ReasonFilesBehind && was.Message == message {
		return
	}
	logf.FromContext(ctx).Info("Standard files left out of an update wait on a person", "standard", s.Name,
		"ref", s.Ref, "behind", files)
	if u.r.Recorder != nil {
		u.r.Recorder.Eventf(u.container, nil, corev1.EventTypeWarning, eventStandardFilesBehind,
			"UpdateStandard", "%s", message)
	}
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
