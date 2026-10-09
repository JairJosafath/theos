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
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// ConditionReleased says whether a Crew's next release went out, and why not.
const ConditionReleased = "Released"

// Reasons of the Released condition besides the model's.
const (
	reasonReleased          = "Released"
	reasonNoReleaseDone     = "NoReleaseDone"
	reasonReleaseGitFailure = "GitHostUnavailable"
	reasonPreparingRelease  = "PreparingRelease"
	reasonReleaseRunFailed  = "ReleaseRunFailed"
	reasonReleaseRunPassed  = "ReleaseRunSucceeded"
	reasonReleaseRunOverdue = "ReleaseRunOverdue"
)

// Reasons of the Events answering a Crew's rerun-release annotation.
const (
	reasonReleaseRerun        = "ReleaseRunRerun"
	reasonReleaseRerunFailed  = "ReleaseRunRerunFailed"
	reasonReleaseNothingRerun = "NoFailedReleaseRun"
)

// runOverdueNote ends the Released condition's message while the last
// release's runs are past model.ReleaseRunBound.
const runOverdueNote = "; its runs have not ended an hour after tagging"

// tagRelease looks after the Crew's first open release, as its switches say:
// with prepareReleases, once the release is done with merged work and has no
// prepare issue, it files one; with tagReleases, once it is done, it tags the
// merge commit of its most recently merged issue and closes its milestone. A
// release that is done but can't go out is a Warning Event, once per release and
// reason. It returns the prepare issue it filed, as a ready issue, or nil.
func (r *CrewReconciler) tagRelease(ctx context.Context, crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus,
	provider gitprovider.Provider, repo gitprovider.Repository, tasks []c4v1alpha1.Task) *model.Issue {
	log := logf.FromContext(ctx)
	gitCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	facts, titles, err := releaseFacts(gitCtx, provider, repo, tasks)
	facts.Prepare = crew.Spec.PrepareReleases
	var step model.ReleaseStep
	var filed gitprovider.Issue
	if err == nil {
		step = model.NextRelease(facts)
		switch {
		case step.File:
			filed, err = filePrepareIssue(gitCtx, provider, repo, step, titles)
		case !crew.Spec.TagReleases:
		case step.CreateTag:
			err = provider.CreateTag(gitCtx, repo, step.Tag, step.Commit, "Release "+step.Tag)
		}
		if err == nil && step.Commit != "" && crew.Spec.TagReleases {
			err = settleMilestones(gitCtx, provider, repo, step)
		}
	}
	if step.File && err == nil {
		message := fmt.Sprintf("Filed #%d to prepare %s", filed.Number, step.Tag)
		log.Info("Filed release preparation", "tag", step.Tag, "issue", filed.Number)
		r.event(crew, corev1.EventTypeNormal, reasonPreparingRelease, "PrepareRelease", message)
		if crew.Spec.TagReleases {
			r.setReleased(crew, status, metav1.ConditionTrue, reasonPreparingRelease, message)
		}
		return &model.Issue{Number: filed.Number, Title: filed.Title, Body: filed.Body,
			Release: releaseOf(facts, step.Milestone)}
	}
	if !crew.Spec.TagReleases {
		if err != nil {
			log.Error(err, "Failed to prepare a release", "container", crew.Spec.ContainerRef.Name, "tag", step.Tag)
		}
		return nil
	}
	switch {
	case err != nil:
		log.Error(err, "Failed to tag a release", "container", crew.Spec.ContainerRef.Name, "tag", step.Tag)
		r.setReleased(crew, status, metav1.ConditionFalse, reasonReleaseGitFailure,
			"Could not tag the next release: "+err.Error())
	case step.Commit != "":
		message := model.ReleasedMessage(step)
		for _, m := range step.Moved {
			log.Info("Moved an issue into the release", "tag", step.Tag, "issue", m.Issue, "from", m.From)
		}
		log.Info("Released milestone", "tag", step.Tag, "commit", step.Commit, "created", step.CreateTag)
		r.event(crew, corev1.EventTypeNormal, reasonReleased, "TagRelease", message)
		r.notePublishSkipped(ctx, crew, status.LastRelease)
		status.LastRelease = &c4v1alpha1.CrewRelease{Tag: step.Tag, Commit: step.Commit, Time: metav1.NewTime(r.now()),
			Run: model.RunWaiting}
		r.releaseSaid.Store(crew.UID, reasonReleased+"\n"+message)
		r.tagged.Store(crew.UID, step.Tag)
		r.setReleased(crew, status, metav1.ConditionTrue, reasonReleased, message)
	case step.Held != "":
		// The last poll's status write can fail after its Event, so the
		// Reconciler remembers what it said too.
		said := step.Reason + "\n" + step.Held
		prev := apimeta.FindStatusCondition(status.Conditions, ConditionReleased)
		last, _ := r.releaseSaid.Load(crew.UID)
		if (prev == nil || prev.Reason != step.Reason || prev.Message != step.Held) && last != said {
			log.Info("Left a release to a person", "tag", step.Tag, "reason", step.Reason)
			r.event(crew, corev1.EventTypeWarning, step.Reason, "TagRelease", step.Held)
		}
		r.releaseSaid.Store(crew.UID, said)
		r.setReleased(crew, status, metav1.ConditionFalse, step.Reason, step.Held)
	default:
		// The cached Crew can miss the release the last poll wrote.
		last, _ := r.releaseSaid.Load(crew.UID)
		if said, ok := last.(string); ok && strings.HasPrefix(said, reasonReleased+"\n") {
			r.setReleased(crew, status, metav1.ConditionTrue, reasonReleased,
				strings.TrimPrefix(said, reasonReleased+"\n"))
		} else if prev := apimeta.FindStatusCondition(status.Conditions, ConditionReleased); prev == nil ||
			prev.Reason != reasonReleased {
			r.setReleased(crew, status, metav1.ConditionTrue, reasonNoReleaseDone, "No open release is done")
		}
	}
	return nil
}

// settleMilestones moves the later releases' issues the tag carries into its
// milestone, each with a comment, closes each later milestone left empty right
// after its last move, and last the tagged release's own. The tag is made
// first, so a poll that fails half way finds it and moves the rest. A comment
// goes before its move, so a failed one is made again; a failure between a
// release's last move and closing it leaves that release empty, to a person.
func settleMilestones(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	step model.ReleaseStep) error {
	for i, m := range step.Moved {
		if err := provider.CommentIssue(ctx, repo, m.Issue, model.MovedComment(step, m)); err != nil {
			return fmt.Errorf("commenting on #%d: %w", m.Issue, err)
		}
		if err := provider.SetIssueMilestone(ctx, repo, m.Issue, step.Milestone); err != nil {
			return fmt.Errorf("moving #%d into %s: %w", m.Issue, step.Tag, err)
		}
		if i+1 < len(step.Moved) && step.Moved[i+1].From == m.From {
			continue
		}
		for _, e := range step.Emptied {
			if e.Title != m.From {
				continue
			}
			if err := provider.CloseMilestone(ctx, repo, e.ID); err != nil {
				return fmt.Errorf("closing %s: %w", e.Title, err)
			}
		}
	}
	return provider.CloseMilestone(ctx, repo, step.Milestone)
}

// releaseOf is the release of milestone id among the facts' releases.
func releaseOf(facts model.ReleaseFacts, id int64) *model.Release {
	for _, m := range facts.Milestones {
		if m.ID == id {
			return &m.Release
		}
	}
	return nil
}

// filePrepareIssue files release step.Tag's prepare issue in its milestone,
// labelled kind/release and ready.
func filePrepareIssue(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	step model.ReleaseStep, titles map[int]string) (gitprovider.Issue, error) {
	l := model.ReleaseLabel()
	labels := append(issueLabels(),
		gitprovider.Label{Name: l.Name, Color: l.Color, Description: l.Description, Exclusive: l.Exclusive})
	if err := provider.EnsureLabels(ctx, repo, labels); err != nil {
		return gitprovider.Issue{}, err
	}
	title, body := model.PrepareIssue(step.Tag, step.Merged, titles)
	return provider.CreateIssue(ctx, repo, title, body, []string{model.LabelRelease, model.LabelReady}, step.Milestone)
}

// maxPreviousTags bounds the closed releases a poll reads the tags of to find
// the previous release: closed without a tag, a release was dropped.
const maxPreviousTags = 5

// releaseFacts reads the open releases, their issues and tags, the newest
// tagged closed release before them, and the Tasks' merges, and the titles of
// the releases' issues.
func releaseFacts(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	tasks []c4v1alpha1.Task) (model.ReleaseFacts, map[int]string, error) {
	var facts model.ReleaseFacts
	titles := map[int]string{}
	milestones, err := provider.ListAllMilestones(ctx, repo)
	if err != nil {
		return facts, nil, fmt.Errorf("milestones: %w", err)
	}
	var closed []model.Release
	for _, m := range milestones {
		if !model.IsReleaseTitle(m.Title) || m.Due.IsZero() {
			continue
		}
		if m.Closed {
			closed = append(closed, model.Release{Title: m.Title, Due: m.Due})
			continue
		}
		issues, err := provider.MilestoneIssues(ctx, repo, m.ID)
		if err != nil {
			return facts, nil, fmt.Errorf("issues of milestone %s: %w", m.Title, err)
		}
		release := model.ReleaseMilestone{Release: model.Release{Title: m.Title, Due: m.Due}, ID: m.ID}
		for _, i := range issues {
			if i.State == "closed" {
				release.Closed = append(release.Closed, i.Number)
			} else {
				release.Open = append(release.Open, i.Number)
			}
			if slices.Contains(i.Labels, model.LabelRelease) {
				release.Prepared = append(release.Prepared, i.Number)
			}
			titles[i.Number] = i.Title
		}
		facts.Milestones = append(facts.Milestones, release)
		commit, err := provider.GetTag(ctx, repo, m.Title)
		if err != nil {
			return facts, nil, fmt.Errorf("tag %s: %w", m.Title, err)
		}
		if commit != "" {
			if facts.Tags == nil {
				facts.Tags = map[string]string{}
			}
			facts.Tags[m.Title] = commit
		}
	}
	for _, r := range model.PreviousReleases(closed, facts.Milestones, maxPreviousTags) {
		commit, err := provider.GetTag(ctx, repo, r.Title)
		if err != nil {
			return facts, nil, fmt.Errorf("tag %s: %w", r.Title, err)
		}
		if commit != "" {
			facts.Previous = &model.ReleasedTag{Release: r, Commit: commit}
			break
		}
	}
	for _, t := range tasks {
		switch {
		case t.Spec.Issue == 0:
		case !isFinal(t.Status.Phase):
			facts.Active = append(facts.Active, int(t.Spec.Issue))
		case t.Status.Phase == c4v1alpha1.TaskMerged:
			merged := model.MergedTask{Name: t.Name, Issue: int(t.Spec.Issue), Commit: t.Status.MergeCommit}
			if t.Status.CompletedAt != nil {
				merged.At = t.Status.CompletedAt.Time
			}
			done := apimeta.FindStatusCondition(t.Status.Conditions, ConditionSucceeded)
			if done != nil && done.Reason == reasonDoneByStandardChange && t.Status.StandardChange != nil {
				merged.DoneBy = int(t.Status.StandardChange.Issue)
			}
			facts.Merged = append(facts.Merged, merged)
		}
	}
	return facts, titles, nil
}

func (r *CrewReconciler) setReleased(crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus,
	value metav1.ConditionStatus, reason, message string) {
	setCondition(&status.Conditions, crew.Generation, ConditionReleased, value, reason, message)
}

func (r *CrewReconciler) event(crew *c4v1alpha1.Crew, eventType, reason, action, note string) {
	if r.Recorder != nil {
		r.Recorder.Eventf(crew, nil, eventType, reason, action, "%s", note)
	}
}

// rerunRelease answers the Crew's c4.factory.io/rerun-release annotation, once
// per value: while its last release's run Failed, it re-runs the failed jobs of
// that release's failed runs, and the watch follows their new attempts;
// otherwise it says with a Normal Event that there is nothing to re-run. It
// runs with spec.tagReleases, as the watch does. A
// re-run the Git host refuses is a Warning Event, and a new value tries again:
// when it refused only some runs, the release stays Failed, and a new value
// re-runs those.
func (r *CrewReconciler) rerunRelease(ctx context.Context, crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus,
	provider gitprovider.Provider, repo gitprovider.Repository) {
	value := crew.Annotations[c4v1alpha1.AnnotationRerunRelease]
	release := status.LastRelease
	run := ""
	if release != nil {
		run = release.Run
	}
	if said, _ := r.rerunSaid.Load(crew.UID); value != "" && said == value {
		status.RerunRelease = value
	}
	step := model.ReleaseRerun(value, status.RerunRelease, run)
	if step == model.RerunNone {
		return
	}
	status.RerunRelease = value
	r.rerunSaid.Store(crew.UID, value)
	log := logf.FromContext(ctx)
	if step == model.RerunNothingFailed {
		note := "No release run failed, so there is nothing to re-run"
		if release != nil {
			note = fmt.Sprintf("Release runs of %s are %s, not Failed, so there is nothing to re-run", release.Tag, run)
		}
		log.Info("Skipped the release re-run", "value", value, "run", run)
		r.event(crew, corev1.EventTypeNormal, reasonReleaseNothingRerun, "RerunRelease", note)
		return
	}
	gitCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	found, err := provider.TagRuns(gitCtx, repo, release.Tag, release.Commit)
	var rerun, refused []string
	if err == nil {
		runs := make([]model.ReleaseRun, 0, len(found))
		for _, f := range found {
			runs = append(runs, model.ReleaseRun{ID: f.ID, Workflow: f.Workflow, Status: f.Status,
				Conclusion: f.Conclusion, URL: f.URL})
		}
		for _, failed := range model.FailedReleaseRuns(runs) {
			if rerr := provider.RerunFailedJobs(gitCtx, repo, failed.ID); rerr != nil {
				err = cmp.Or(err, rerr)
				refused = append(refused, failed.Workflow)
				continue
			}
			rerun = append(rerun, failed.Workflow)
		}
	}
	if len(rerun) > 0 {
		// The watch reads the runs' new attempts from here. A run the host
		// refused still failed, so the release stays Failed and a new value
		// re-runs only that one: the others aren't failed any more.
		now := metav1.NewTime(r.now())
		release.RerunTime = &now
		if err == nil {
			release.Run = model.RunRunning
		}
	}
	if err != nil {
		log.Error(err, "Failed to re-run the release runs", "tag", release.Tag, "rerun", rerun, "refused", refused)
		note := fmt.Sprintf("Could not re-run the failed runs of %s: %v; set a new %s value to try again",
			release.Tag, err, c4v1alpha1.AnnotationRerunRelease)
		if len(rerun) > 0 {
			note = fmt.Sprintf("Re-ran %s of %s, but could not re-run %s: %v; set a new %s value to try those again",
				strings.Join(rerun, ", "), release.Tag, strings.Join(refused, ", "), err,
				c4v1alpha1.AnnotationRerunRelease)
		}
		r.event(crew, corev1.EventTypeWarning, reasonReleaseRerunFailed, "RerunRelease", note)
		return
	}
	if len(rerun) == 0 {
		// Someone re-ran them already, in Gitea: the watch follows those.
		log.Info("Skipped the release re-run", "value", value, "tag", release.Tag)
		r.event(crew, corev1.EventTypeNormal, reasonReleaseNothingRerun, "RerunRelease",
			fmt.Sprintf("No run of %s failed any more, so there is nothing to re-run", release.Tag))
		return
	}
	log.Info("Re-ran the release runs", "tag", release.Tag, "workflows", rerun)
	r.event(crew, corev1.EventTypeNormal, reasonReleaseRerun, "RerunRelease",
		fmt.Sprintf("Re-ran the failed runs of %s: %s", release.Tag, strings.Join(rerun, ", ")))
}

// watchReleaseRun reads how the CI workflow runs on the Crew's last release
// went, until they all ended well, and reports a run that failed with a Warning
// Event and one that ended well with a Normal Event, once per change.
func (r *CrewReconciler) watchReleaseRun(ctx context.Context, crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus,
	provider gitprovider.Provider, repo gitprovider.Repository) {
	release := status.LastRelease
	if release == nil || release.Run == model.RunSucceeded {
		return
	}
	log := logf.FromContext(ctx)
	gitCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	found, err := provider.TagRuns(gitCtx, repo, release.Tag, release.Commit)
	if err != nil {
		// Each poll would fail alike while the host's Actions can't be read, so
		// the Crew logs each error once.
		if last, _ := r.runErrSaid.Load(crew.UID); last != err.Error() {
			log.Error(err, "Failed to read the release run", "tag", release.Tag)
		}
		r.runErrSaid.Store(crew.UID, err.Error())
		return
	}
	r.runErrSaid.Delete(crew.UID)
	runs := make([]model.ReleaseRun, 0, len(found))
	for _, run := range found {
		runs = append(runs, model.ReleaseRun{ID: run.ID, Workflow: run.Workflow, Status: run.Status,
			Conclusion: run.Conclusion, URL: run.URL})
	}
	result, run := model.ReleaseRunResult(runs)
	// The last poll's status write can fail after its Event, so the Reconciler
	// remembers what it said too.
	said := release.Tag + "\n" + result
	last, _ := r.runSaid.Load(crew.UID)
	if result != release.Run && last != said {
		switch result {
		case model.RunFailed:
			log.Info("Release run failed", "tag", release.Tag, "workflow", run.Workflow,
				"conclusion", run.Conclusion, "url", run.URL)
			r.event(crew, corev1.EventTypeWarning, reasonReleaseRunFailed, "WatchReleaseRun",
				fmt.Sprintf("Release run %s of %s ended %s: %s", run.Workflow, release.Tag, run.Conclusion, run.URL))
		case model.RunSucceeded:
			log.Info("Release run succeeded", "tag", release.Tag, "url", run.URL)
			r.event(crew, corev1.EventTypeNormal, reasonReleaseRunPassed, "WatchReleaseRun",
				fmt.Sprintf("Release runs of %s succeeded: %s", release.Tag, run.URL))
		}
	}
	r.runSaid.Store(crew.UID, said)
	release.Run, release.RunURL = result, run.URL
}

// noteOverdueRun reports a last release whose runs haven't ended
// model.ReleaseRunBound after tagging, such as one no runner picks up: one
// Warning Event per release, and while the Released condition names that
// release, a note on its message until the runs end. The watch carries on, so
// a later end is reported as usual.
func (r *CrewReconciler) noteOverdueRun(ctx context.Context, crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus) {
	release := status.LastRelease
	if release == nil {
		return
	}
	started := release.Time.Time
	if release.RerunTime != nil {
		started = release.RerunTime.Time
	}
	late := model.ReleaseRunLate(release.Run, started, r.now())
	// A re-run's runs get their own hour, and their own report.
	lateKey := release.Tag + "\n" + started.UTC().Format(time.RFC3339)
	// The Crew as read says whether an earlier process reported it already.
	read := apimeta.FindStatusCondition(crew.Status.Conditions, ConditionReleased)
	noted := read != nil && read.Reason == reasonReleased && strings.HasSuffix(read.Message, runOverdueNote)
	if last, _ := r.runLateSaid.Load(crew.UID); late && !noted && last != lateKey {
		note := fmt.Sprintf("Release runs of %s have not ended an hour after tagging, %s", release.Tag, release.Run)
		if release.RunURL != "" {
			note += ": " + release.RunURL
		}
		logf.FromContext(ctx).Info("Release run overdue", "tag", release.Tag, "run", release.Run,
			"url", release.RunURL)
		r.event(crew, corev1.EventTypeWarning, reasonReleaseRunOverdue, "WatchReleaseRun", note)
	}
	if late {
		r.runLateSaid.Store(crew.UID, lateKey)
	}
	cond := apimeta.FindStatusCondition(status.Conditions, ConditionReleased)
	if cond == nil || cond.Reason != reasonReleased {
		return
	}
	message := strings.TrimSuffix(cond.Message, runOverdueNote)
	if late {
		message += runOverdueNote
	}
	r.setReleased(crew, status, metav1.ConditionTrue, reasonReleased, message)
}
