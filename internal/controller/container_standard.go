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
	// answered says the pass acted on or ignored the person's answer: the
	// standard-take and standard-keep annotations are removed once the status
	// is written.
	answered bool
}

// updateStandard keeps the Container's Standard files at its Standard's ref, one
// step per reconcile, and reports it in status. The Container names a Standard.
// It reports whether the pass answered the person's annotation, which the
// caller removes once the status is written.
func (r *ContainerReconciler) updateStandard(ctx context.Context, container *c4v1alpha1.Container,
	status *c4v1alpha1.ContainerStatus) bool {
	name := container.Spec.StandardRef.Name
	if status.Standard == nil || status.Standard.Name != name {
		ref := model.ScaffoldedRef(status.ScaffoldedFrom, name)
		if ref == "" {
			r.condition(container, status, c4v1alpha1.ConditionStandardUpdated, metav1.ConditionFalse,
				c4v1alpha1.ReasonAddedAfterScaffold, fmt.Sprintf(
					"The Standard %s was not part of the scaffold; its updates don't reach this Container", name))
			return false
		}
		status.Standard = &c4v1alpha1.ContainerStandardStatus{Name: name, Ref: ref}
	}

	var standard c4v1alpha1.Standard
	found, err := r.lookup(ctx, container.Namespace, name, &standard)
	if err != nil {
		logf.FromContext(ctx).Info("Could not read Standard", "standard", name, "error", err.Error())
		return false
	}
	if !found {
		r.condition(container, status, c4v1alpha1.ConditionStandardUpdated, metav1.ConditionFalse,
			c4v1alpha1.ReasonNotFound, notFoundMessage([]string{"Standard " + name}))
		return false
	}

	// The cache can lag behind our own status writes, and the steps act in Git
	// from the update in status. Take them only from the live Container, so a
	// reconcile queued during the last pass doesn't open an update a second time
	// or leave the one it replaced open; the watch brings the newer one.
	var live c4v1alpha1.Container
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(container), &live); err != nil {
		logf.FromContext(ctx).Info("Could not read Container", "error", err.Error())
		return false
	}
	if live.ResourceVersion != container.ResourceVersion {
		return false
	}

	// Git calls get a deadline of their own, so the status write after them still lands.
	gitCtx, cancel := context.WithTimeout(ctx, r.scaffoldTimeout())
	defer cancel()
	u := &standardUpdate{r: r, container: container, status: status}
	u.run(ctx, gitCtx, &standard)
	return u.answered
}

// run takes the steps towards standard's ref this pass can: it gathers the facts
// model.NextStandardStep asks for and does what it decides.
func (u *standardUpdate) run(ctx, gitCtx context.Context, standard *c4v1alpha1.Standard) {
	log := logf.FromContext(ctx)
	s := u.status.Standard
	if u.answer(ctx, gitCtx, standard) {
		return
	}
	target := u.target(standard)
	f := model.StandardUpdateFacts{Name: s.Name, Target: target.Ref, TargetMode: target.Checks}
	for {
		// What was read stays in f when the update is gone: the model looks at it
		// only while an update is in flight, and only Plan, which ends the pass,
		// starts one.
		f.Ref, f.Mode, f.Update = s.Ref, recordedChecks(u.status), inFlight(s.Update)
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
			f.ChecksURL, f.Contexts, f.CI = checks.URL, checks.Contexts, git.ci
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
			take := s.Update.Take
			if !u.drop(ctx, gitCtx, step.Message) {
				return
			}
			// A take that conflicts with main is the person's answer still: take
			// again from the new main.
			if take && u.version().Normal() == u.target(standard).Normal() {
				u.take(ctx, gitCtx, standard)
				return
			}
		case model.StandardTakeClosed:
			u.takeClosed(ctx, gitCtx)
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
		Ref: update.Ref, Mode: string(update.Checks), Stopped: update.Stopped, PullRequest: int(update.PullRequest),
		PullRequestURL: update.PullRequestURL, Branch: update.Branch, Commit: update.Commit, Paths: update.Paths,
		Take: update.Take, Held: update.Stopped == c4v1alpha1.ReasonNoLightCheck,
	}
}

// plan renders the Standard at the ref and checks its files are at and at
// target, and opens a pull request with what main still holds as the old render
// made it. An update that changes the checks and would leave out a file the
// change of checks writes isn't opened: the checks stay as they are.
func (u *standardUpdate) plan(ctx, gitCtx context.Context, standard *c4v1alpha1.Standard) {
	log := logf.FromContext(ctx)
	s := u.status.Standard
	name := s.Name
	from := model.StandardVersion{Ref: s.Ref, Checks: recordedChecks(u.status)}
	to := model.StandardVersion{Ref: standard.Spec.Template.Ref,
		Checks: model.EffectiveChecks(string(u.container.Spec.Checks), standard.Spec.ChecksBeforeRelease)}
	git, ok := u.access(ctx)
	if !ok {
		return
	}
	var creds *gitprovider.Credentials
	if gitprovider.SameHost(git.gitProvider.Spec.URL, standard.Spec.Template.URL) {
		creds = &git.creds
	}
	oldFiles, err := u.r.Git.FetchTemplate(gitCtx, standard.Spec.Template.URL, from.Ref, creds)
	var nextFiles scaffold.Tree
	if err == nil {
		nextFiles, err = u.r.Git.FetchTemplate(gitCtx, standard.Spec.Template.URL, to.Ref, creds)
	}
	stopped := func(reason string) *c4v1alpha1.StandardUpdateStatus {
		return &c4v1alpha1.StandardUpdateStatus{Ref: to.Ref, Checks: c4v1alpha1.ChecksMode(to.Checks), Stopped: reason}
	}
	if errors.Is(err, gitprovider.ErrUnpinnedRef) {
		s.Update = stopped(c4v1alpha1.ReasonUnpinnedRef)
		u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonUnpinnedRef, fmt.Sprintf("Standard %s: %v", name, err))
		return
	}
	if err != nil {
		u.gitError(ctx, err)
		return
	}

	in := scaffoldInput(u.container, git.ci, scaffoldedConnectors(u.status), to.Checks,
		scaffoldedOperations(u.container, u.status))
	render := func(v model.StandardVersion, files scaffold.Tree) (scaffold.Tree, bool) {
		in := in
		in.Checks = v.Checks
		tree, err := scaffold.RenderLayer(in, scaffold.Layer{Side: model.SideStandard, Name: name, Ref: v.Ref, Files: files})
		if err != nil {
			s.Update = stopped(c4v1alpha1.ReasonFailed)
			u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonFailed,
				fmt.Sprintf("Could not render the Standard %s at %s: %v", name, v, err))
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
	// The files the change of checks writes: the new ref rendered with the old
	// checks against the new render.
	var checksPaths []string
	if from.Checks != to.Checks {
		keep, ok := render(model.StandardVersion{Ref: to.Ref, Checks: from.Checks}, nextFiles)
		if !ok {
			return
		}
		checksPaths = scaffold.ChangedPaths(keep, next)
	}

	// Each file a past update left out is merged against the render its copy
	// was last brought to.
	behind := behindFiles(s)
	// They render with the checks the files are at.
	renders := map[string]scaffold.Tree{from.Ref: old}
	bases := map[string]scaffold.Tree{}
	for _, b := range behind {
		if _, ok := renders[b.Ref]; !ok {
			files, err := u.r.Git.FetchTemplate(gitCtx, standard.Spec.Template.URL, b.Ref, creds)
			if err != nil {
				u.gitError(ctx, err)
				return
			}
			if renders[b.Ref], ok = render(model.StandardVersion{Ref: b.Ref, Checks: from.Checks}, files); !ok {
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
	if blocked := intersect(plan.LeftOut, checksPaths); len(blocked) > 0 {
		s.Update = stopped(c4v1alpha1.ReasonChecksLeftOut)
		u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonChecksLeftOut, fmt.Sprintf(
			"CI can't change to run the full check %s: %s changed in the repository since %s and doesn't merge "+
				"with the change; CI keeps running it %s until the Container's checks or the Standard's ref "+
				"change again",
			checksWhen(to.Checks), strings.Join(blocked, ", "), from, checksWhen(from.Checks)))
		log.Info("Did not change the checks of a Container whose repository changed their files",
			"standard", name, "from", from.String(), "to", to.String(), "leftOut", blocked)
		return
	}
	if to.Checks == model.ChecksBeforeRelease && from.Checks != to.Checks &&
		!model.HasLightCheck(plannedFile(plan, current, model.LightCheckFile)) {
		s.Update = stopped(c4v1alpha1.ReasonNoLightCheck)
		u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonNoLightCheck, noLightCheck(s, plan))
		log.Info("Held the change of a Container's checks until its Makefile has a light check",
			"standard", name, "from", from.String(), "to", to.String())
		return
	}
	leftOut := u.leftOut(to.Ref, behind, from.Ref, old, bases, next, current, plan.LeftOut)
	// The files behind once main holds the update, each with the Standard's
	// change to it, as this plan sees it.
	behindAfter := nextBehind(behind, from.Ref, plan.LeftOut, "", model.BehindChanges(to.Ref, leftOut.Files))
	if plan.Empty() {
		u.alreadyUpToDate(ctx, to, behindAfter)
		return
	}

	branch := model.StandardUpdateBranch(name, to)
	author := gitprovider.Signature{Name: scaffold.AuthorName, Email: scaffold.AuthorEmail, When: time.Now()}
	commit, err := git.provider.PushBranch(gitCtx, repo, base, branch, plan.Files, plan.Removes,
		scaffold.UpdateMessage(in, name, from, to, plan), author)
	if errors.Is(err, gitprovider.ErrEmptyChange) {
		// main got there between the read and the push.
		u.alreadyUpToDate(ctx, to, behindAfter)
		return
	}
	if err != nil {
		u.gitError(ctx, err)
		return
	}
	pr, err := git.provider.OpenPullRequest(gitCtx, repo, branch, gitprovider.DefaultBranch,
		model.StandardUpdateTitle(name, from, to), model.StandardUpdateBody(name, from, to, plan.Paths(), plan.Merged,
			leftOut))
	if err != nil {
		u.gitError(ctx, err)
		return
	}
	s.Update = &c4v1alpha1.StandardUpdateStatus{
		Ref: to.Ref, Checks: c4v1alpha1.ChecksMode(to.Checks), Branch: branch, Commit: commit, PullRequest: int32(pr.Number), PullRequestURL: pr.URL,
		Paths: plan.Paths(), Merged: plan.Merged, LeftOut: plan.LeftOut, Behind: behindAfter,
	}
	u.updating()
	log.Info("Opened a Standard update", "url", pr.URL, "standard", name, "from", from.String(), "to", to.String(),
		"paths", plan.Paths(), "merged", plan.Merged, "leftOut", plan.LeftOut)
}

// leftOut is what the update's pull request says of the files it leaves out:
// each one's change between the Standard's render at the ref its copy is at
// (old at from, or its base in bases) and next, and what taking next's version
// of it would drop from current.
func (u *standardUpdate) leftOut(to string, behind []c4v1alpha1.StandardFile, from string, old scaffold.Tree,
	bases map[string]scaffold.Tree, next, current scaffold.Tree, paths []string) model.LeftOut {
	refs := map[string]string{}
	for _, b := range behind {
		refs[b.Path] = b.Ref
	}
	files := map[string]scaffold.Tree{}
	for _, p := range paths {
		files[p] = old
		if base, ok := bases[p]; ok {
			files[p] = base
		}
	}
	take := scaffold.PlanTake(files, next, current)
	out := model.LeftOut{Take: u.annotate(c4v1alpha1.AnnotationStandardTake, to),
		Keep: u.annotate(c4v1alpha1.AnnotationStandardKeep, to)}
	for _, p := range paths {
		change := scaffold.StandardChange(files[p], next, p)
		f := model.LeftOutFile{Path: p, Ref: cmp.Or(refs[p], from), Diff: change.Diff, Note: change.Note}
		if i := slices.IndexFunc(take.Taken, func(t model.TakenFile) bool { return t.Path == p }); i >= 0 {
			f.Take = take.Taken[i]
		}
		out.Files = append(out.Files, f)
	}
	return out
}

// annotate is the command that annotates the Container with key=ref.
func (u *standardUpdate) annotate(key, ref string) string {
	return fmt.Sprintf("kubectl -n %s annotate container %s %s=%s", u.container.Namespace, u.container.Name, key, ref)
}

// plannedFile is path's content once plan merges into current, or nil when it
// has no such path.
func plannedFile(plan scaffold.Update, current scaffold.Tree, path string) []byte {
	if f, ok := plan.Files[path]; ok {
		return f.Data
	}
	if slices.Contains(plan.Removes, path) {
		return nil
	}
	return current[path].Data
}

// noLightCheck says why CI can't change to run the full check only before a
// release: the Makefile the update would leave has no check-light target,
// and, when updates left the Makefile out, since which ref.
func noLightCheck(s *c4v1alpha1.ContainerStandardStatus, plan scaffold.Update) string {
	why := "has no check-light target"
	for _, b := range s.Behind {
		if b.Path == model.LightCheckFile {
			why = fmt.Sprintf("was left out of the %s updates (at %s) and has no check-light target", s.Name, b.Ref)
		}
	}
	if slices.Contains(plan.LeftOut, model.LightCheckFile) {
		why = fmt.Sprintf("is left out of this %s update and has no check-light target", s.Name)
	}
	return fmt.Sprintf("CI can't change to run the full check only before a release: the repository's %s %s, "+
		"which %s on each change needs; CI keeps running the full check on every change until the %s has one",
		model.LightCheckFile, why, model.CheckLightCommand, model.LightCheckFile)
}

// standardRenders renders a Standard's files for the Container at the refs a
// pass asks for, each ref once.
type standardRenders struct {
	fetch    func(ctx context.Context, ref string) (scaffold.Tree, error)
	in       scaffold.Input
	standard string
	trees    map[string]scaffold.Tree
}

// renderError is a Standard that fetched but didn't render at ref.
type renderError struct {
	ref string
	err error
}

func (e *renderError) Error() string { return fmt.Sprintf("at %s: %v", e.ref, e.err) }

// renders renders standard for the Container, fetching its template with the
// Git host's credentials when it lives on the same host.
func (u *standardUpdate) renders(git *gitAccess, standard *c4v1alpha1.Standard) *standardRenders {
	var creds *gitprovider.Credentials
	if gitprovider.SameHost(git.gitProvider.Spec.URL, standard.Spec.Template.URL) {
		creds = &git.creds
	}
	return &standardRenders{
		fetch: func(ctx context.Context, ref string) (scaffold.Tree, error) {
			return u.r.Git.FetchTemplate(ctx, standard.Spec.Template.URL, ref, creds)
		},
		in: scaffoldInput(u.container, git.ci, scaffoldedConnectors(u.status), recordedChecks(u.status),
			scaffoldedOperations(u.container, u.status)),
		standard: standard.Name,
		trees:    map[string]scaffold.Tree{},
	}
}

// at is the Standard's render at ref.
func (r *standardRenders) at(ctx context.Context, ref string) (scaffold.Tree, error) {
	if tree, ok := r.trees[ref]; ok {
		return tree, nil
	}
	files, err := r.fetch(ctx, ref)
	if err != nil {
		return nil, err
	}
	tree, err := scaffold.RenderLayer(r.in, scaffold.Layer{Side: model.SideStandard, Name: r.standard, Ref: ref,
		Files: files})
	if err != nil {
		return nil, &renderError{ref: ref, err: err}
	}
	r.trees[ref] = tree
	return tree, nil
}

// bases are, by path, the renders the files behind were last brought to.
func (r *standardRenders) bases(ctx context.Context, behind []c4v1alpha1.StandardFile) (map[string]scaffold.Tree,
	error) {
	bases := make(map[string]scaffold.Tree, len(behind))
	for _, b := range behind {
		tree, err := r.at(ctx, b.Ref)
		if err != nil {
			return nil, err
		}
		bases[b.Path] = tree
	}
	return bases, nil
}

// renderFailed reports a Standard that could not be fetched or rendered for a
// take: an unpinned ref or a render that fails, or a Git error, retried at the
// next pass.
func (u *standardUpdate) renderFailed(ctx context.Context, err error) {
	s := u.status.Standard
	switch {
	case errors.Is(err, gitprovider.ErrUnpinnedRef):
		u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonUnpinnedRef, fmt.Sprintf("Standard %s: %v", s.Name, err))
	case errors.As(err, new(*renderError)):
		u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonFailed,
			fmt.Sprintf("Could not render the Standard %s %v", s.Name, err))
	default:
		u.gitError(ctx, err)
	}
}

// answer acts on the person's answer to the files behind, the standard-take or
// standard-keep annotation naming the ref the files are at. It reports true
// when the pass ends: it opened a take, or could not. An answer it acts on or
// ignores is answered, so the caller removes it; one given while a take is in
// flight waits for the take to end.
func (u *standardUpdate) answer(ctx, gitCtx context.Context, standard *c4v1alpha1.Standard) bool {
	log := logf.FromContext(ctx)
	annotations := u.container.GetAnnotations()
	take, asksTake := annotations[c4v1alpha1.AnnotationStandardTake]
	keep, asksKeep := annotations[c4v1alpha1.AnnotationStandardKeep]
	s := u.status.Standard
	if !asksTake && !asksKeep || s.Update != nil && s.Update.Take {
		return false
	}
	u.answered = true
	behind := behindFiles(s)
	takes, keeps := asksTake && take == s.Ref, asksKeep && keep == s.Ref
	var ignored string
	switch {
	case len(behind) == 0:
		ignored = "no Standard files are behind"
	case u.version().Normal() != u.target(standard).Normal():
		ignored = fmt.Sprintf("the Standard moved on to %s, whose update plans the files again", u.target(standard))
	case takes && keeps:
		ignored = "it both takes and keeps the files"
	case !takes && !keeps:
		ignored = fmt.Sprintf("it names another ref than %s, the one the files are at", s.Ref)
	}
	if ignored != "" {
		log.Info("Ignored an answer to the Standard files behind", "standard", s.Name, "ref", s.Ref,
			"take", take, "keep", keep, "why", ignored)
		return false
	}
	if takes {
		u.take(ctx, gitCtx, standard)
		return true
	}
	for _, b := range behind {
		kept := c4v1alpha1.StandardFile{Path: b.Path, Ref: s.Ref}
		if i := slices.IndexFunc(s.Kept, func(k c4v1alpha1.StandardFile) bool { return k.Path == b.Path }); i >= 0 {
			s.Kept[i] = kept
		} else {
			s.Kept = append(s.Kept, kept)
		}
	}
	setBehind(s, nil)
	log.Info("Kept the repository's version of Standard files behind", "standard", s.Name, "ref", s.Ref,
		"paths", pathsOf(behind))
	return false
}

// take opens the pull request that takes the Standard's version at the ref the
// files are at of every file behind, merged with the repository's changes and
// the Standard's lines kept where both changed the same ones. On a Git error
// the answer waits for the next pass.
func (u *standardUpdate) take(ctx, gitCtx context.Context, standard *c4v1alpha1.Standard) {
	log := logf.FromContext(ctx)
	s := u.status.Standard
	git, ok := u.access(ctx)
	if !ok {
		u.answered = false
		return
	}
	behind := behindFiles(s)
	renders := u.renders(git, standard)
	next, err := renders.at(gitCtx, s.Ref)
	var bases map[string]scaffold.Tree
	if err == nil {
		bases, err = renders.bases(gitCtx, behind)
	}
	if err != nil {
		u.answered = false
		u.renderFailed(ctx, err)
		return
	}
	repo := git.provider.Repository(u.container.Name)
	current, base, err := git.provider.ReadFiles(gitCtx, repo, gitprovider.DefaultBranch, pathsOf(behind))
	if err != nil {
		u.answered = false
		u.gitError(ctx, err)
		return
	}
	take := scaffold.PlanTake(bases, next, current)
	upToDate := func() {
		setBehind(s, nil)
		u.upToDate(ctx)
		log.Info("Found the Standard files behind already as the Standard has them", "standard", s.Name,
			"ref", s.Ref)
	}
	if take.Empty() {
		upToDate()
		return
	}
	branch := model.StandardTakeBranch(s.Name, s.Ref)
	author := gitprovider.Signature{Name: scaffold.AuthorName, Email: scaffold.AuthorEmail, When: time.Now()}
	commit, err := git.provider.PushBranch(gitCtx, repo, base, branch, take.Files, take.Removes,
		scaffold.TakeMessage(renders.in, s.Name, s.Ref, take), author)
	if errors.Is(err, gitprovider.ErrEmptyChange) {
		upToDate()
		return
	}
	if err == nil {
		var pr gitprovider.PullRequest
		pr, err = git.provider.OpenPullRequest(gitCtx, repo, branch, gitprovider.DefaultBranch,
			model.StandardTakeTitle(s.Name, s.Ref, take.Paths()), model.StandardTakeBody(s.Name, s.Ref, take.Taken))
		if err == nil {
			s.Update = &c4v1alpha1.StandardUpdateStatus{
				Ref: s.Ref, Checks: c4v1alpha1.ChecksMode(recordedChecks(u.status)), Take: true, Branch: branch, Commit: commit, PullRequest: int32(pr.Number),
				PullRequestURL: pr.URL, Paths: take.Paths(),
			}
			u.updating()
			log.Info("Opened a take of a Standard's files", "url", pr.URL, "standard", s.Name, "ref", s.Ref,
				"paths", take.Paths())
			return
		}
	}
	u.answered = false
	u.gitError(ctx, err)
}

// takeClosed forgets a take a person closed without merging and deletes its
// branch: the files stay behind. The pass goes on and reports them.
func (u *standardUpdate) takeClosed(ctx, gitCtx context.Context) {
	log := logf.FromContext(ctx)
	s := u.status.Standard
	update := s.Update
	if git, ok := u.access(ctx); ok {
		if err := git.provider.DeleteBranch(gitCtx, git.provider.Repository(u.container.Name),
			update.Branch); err != nil {
			log.Info("Could not delete the branch of a closed take", "branch", update.Branch, "error", err.Error())
		}
	}
	s.Update = nil
	log.Info("Forgot a take of Standard files closed without merging", "url", update.PullRequestURL,
		"paths", update.Paths)
}

// pathsOf are the paths of files.
func pathsOf(files []c4v1alpha1.StandardFile) []string {
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	return paths
}

// alreadyUpToDate records to when main already holds what it renders, apart
// from the files behind.
func (u *standardUpdate) alreadyUpToDate(ctx context.Context, to model.StandardVersion,
	behind []c4v1alpha1.StandardFile) {
	s := u.status.Standard
	from := u.version()
	s.Ref, s.Checks, s.Update = to.Ref, c4v1alpha1.ChecksMode(to.Checks), nil
	setBehind(s, behind)
	u.upToDate(ctx)
	logf.FromContext(ctx).Info("Found the Standard's files up to date", "standard", s.Name, "from", from.String(),
		"to", to.String(), "leftOut", s.LeftOut)
}

// target is the version the Container's Standard files are to be at: the
// Standard's ref, with the Container's effective checks.
func (u *standardUpdate) target(standard *c4v1alpha1.Standard) model.StandardVersion {
	return model.StandardVersion{Ref: standard.Spec.Template.Ref,
		Checks: model.EffectiveChecks(string(u.container.Spec.Checks), standard.Spec.ChecksBeforeRelease)}
}

// version is the ref and checks the Standard's files are at.
func (u *standardUpdate) version() model.StandardVersion {
	return model.StandardVersion{Ref: u.status.Standard.Ref, Checks: recordedChecks(u.status)}
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
// at from, and a file the update brought up to date is no longer behind. Each
// carries its change in changes, the Standard's change to it up to the
// update's ref.
func nextBehind(behind []c4v1alpha1.StandardFile, from string, leftOut []string,
	url string, changes map[string]string) []c4v1alpha1.StandardFile {
	next := make([]c4v1alpha1.StandardFile, 0, len(leftOut))
	for _, p := range leftOut {
		f := c4v1alpha1.StandardFile{Path: p, Ref: from}
		if i := slices.IndexFunc(behind, func(b c4v1alpha1.StandardFile) bool { return b.Path == p }); i >= 0 {
			f = behind[i]
		}
		f.PullRequestURL = cmp.Or(url, f.PullRequestURL)
		f.Change = changes[p]
		next = append(next, f)
	}
	return next
}

// setBehind records the files behind, and their paths in leftOut. A kept file
// left out again is behind, no longer kept.
func setBehind(s *c4v1alpha1.ContainerStandardStatus, behind []c4v1alpha1.StandardFile) {
	s.Behind, s.LeftOut = nil, nil
	for _, b := range behind {
		s.Behind = append(s.Behind, b)
		s.LeftOut = append(s.LeftOut, b.Path)
	}
	s.Kept = slices.DeleteFunc(s.Kept, func(k c4v1alpha1.StandardFile) bool { return slices.Contains(s.LeftOut, k.Path) })
	if len(s.Kept) == 0 {
		s.Kept = nil
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
	changes := map[string]string{}
	for _, b := range update.Behind {
		changes[b.Path] = b.Change
	}
	behind := nextBehind(behindFiles(s), s.Ref, update.LeftOut, update.PullRequestURL, changes)
	s.Ref, s.Checks, s.Update = update.Ref, update.Checks, nil
	setBehind(s, behind)
	// A kept file the update wrote is the Standard's again.
	s.Kept = slices.DeleteFunc(s.Kept, func(k c4v1alpha1.StandardFile) bool {
		return slices.Contains(update.Paths, k.Path)
	})
	logf.FromContext(ctx).Info("Recorded a merged Standard update", "url", update.PullRequestURL,
		"standard", s.Name, "ref", s.Ref, "checks", recordedChecks(u.status), "take", update.Take)
}

// mergeMessage is the title and message of the update's squash commit on main:
// the update's own commit message.
func (u *standardUpdate) mergeMessage() (title, message string) {
	s := u.status.Standard
	update := s.Update
	in := scaffoldInput(u.container, model.CI{}, scaffoldedConnectors(u.status), recordedChecks(u.status),
		scaffoldedOperations(u.container, u.status))
	if update.Take {
		taken := make([]model.TakenFile, 0, len(update.Paths))
		for _, p := range update.Paths {
			taken = append(taken, model.TakenFile{Path: p})
		}
		title = model.StandardTakeTitle(s.Name, update.Ref, update.Paths)
		message, _ = strings.CutPrefix(scaffold.TakeMessage(in, s.Name, update.Ref, scaffold.Take{Taken: taken}),
			title+"\n\n")
		return title, message
	}
	from, to := u.version(), model.StandardVersion{Ref: update.Ref, Checks: string(update.Checks)}
	title = model.StandardUpdateTitle(s.Name, from, to)
	full := scaffold.UpdateMessage(scaffoldInput(u.container, model.CI{}, scaffoldedConnectors(u.status), to.Checks,
		scaffoldedOperations(u.container, u.status)), s.Name,
		from, to, scaffold.Update{Merged: update.Merged, LeftOut: update.LeftOut})
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
		u.condition(metav1.ConditionTrue, c4v1alpha1.ReasonUpToDate, fmt.Sprintf("%s is at %s", s.Name, u.version()))
		return
	}
	files := make([]string, 0, len(behind))
	for _, b := range behind {
		files = append(files, fmt.Sprintf("%s (at %s)", b.Path, b.Ref))
	}
	message := fmt.Sprintf("%s is at %s, but its updates left out %s: the repository changed them in lines "+
		"the Standard changed too. What the Standard changed in each is in status.standard.behind and on the "+
		"Container's page. Take the Standard's version: `%s`; or keep the repository's: `%s`",
		s.Name, u.version(), strings.Join(files, ", "), u.annotate(c4v1alpha1.AnnotationStandardTake, s.Ref),
		u.annotate(c4v1alpha1.AnnotationStandardKeep, s.Ref))
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
	if s.Update.Take {
		u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonUpdating, fmt.Sprintf(
			"Pull request %s takes the version of %s at %s of %s; it merges once CI passes",
			s.Update.PullRequestURL, s.Name, s.Ref, strings.Join(s.Update.Paths, ", ")))
		return
	}
	u.condition(metav1.ConditionFalse, c4v1alpha1.ReasonUpdating, fmt.Sprintf(
		"Pull request %s brings %s from %s to %s; it merges once CI passes",
		s.Update.PullRequestURL, s.Name, u.version(),
		model.StandardVersion{Ref: s.Update.Ref, Checks: string(s.Update.Checks)}))
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

// intersect returns the items of a that b holds too, in a's order.
func intersect(a, b []string) []string {
	var both []string
	for _, item := range a {
		if slices.Contains(b, item) {
			both = append(both, item)
		}
	}
	return both
}

// checksWhen says when CI runs the full check with checks.
func checksWhen(checks string) string {
	if checks == model.ChecksBeforeRelease {
		return "only before a release"
	}
	return "on every change"
}
