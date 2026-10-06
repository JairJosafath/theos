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
)

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
			err = provider.CloseMilestone(gitCtx, repo, step.Milestone)
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
		message := fmt.Sprintf("Released %s at %s", step.Tag, step.Commit)
		log.Info("Released milestone", "tag", step.Tag, "commit", step.Commit, "created", step.CreateTag)
		r.event(crew, corev1.EventTypeNormal, reasonReleased, "TagRelease", message)
		status.LastRelease = &c4v1alpha1.CrewRelease{Tag: step.Tag, Commit: step.Commit, Time: metav1.NewTime(r.now()),
			Run: model.RunWaiting}
		r.releaseSaid.Store(crew.UID, reasonReleased+"\n"+message)
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

// releaseFacts reads the open releases, their issues and tags, and the Tasks'
// merges, and the titles of the releases' issues.
func releaseFacts(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	tasks []c4v1alpha1.Task) (model.ReleaseFacts, map[int]string, error) {
	var facts model.ReleaseFacts
	titles := map[int]string{}
	milestones, err := provider.ListMilestones(ctx, repo)
	if err != nil {
		return facts, nil, fmt.Errorf("milestones: %w", err)
	}
	for _, m := range milestones {
		if !model.IsReleaseTitle(m.Title) || m.Due.IsZero() {
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
		runs = append(runs, model.ReleaseRun{Workflow: run.Workflow, Status: run.Status, Conclusion: run.Conclusion,
			URL: run.URL})
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
