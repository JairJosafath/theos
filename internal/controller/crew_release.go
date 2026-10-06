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
)

// tagRelease tags the Crew's first open release once it is done, at the merge
// commit of its most recently merged issue, and closes its milestone. A release
// that is done but can't go out is a Warning Event, once per release and reason.
func (r *CrewReconciler) tagRelease(ctx context.Context, crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus,
	provider gitprovider.Provider, repo gitprovider.Repository, tasks []c4v1alpha1.Task) {
	log := logf.FromContext(ctx)
	gitCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	facts, err := releaseFacts(gitCtx, provider, repo, tasks)
	var step model.ReleaseStep
	if err == nil {
		step = model.NextRelease(facts)
		if step.CreateTag {
			err = provider.CreateTag(gitCtx, repo, step.Tag, step.Commit, "Release "+step.Tag)
		}
		if err == nil && step.Commit != "" {
			err = provider.CloseMilestone(gitCtx, repo, step.Milestone)
		}
	}
	switch {
	case err != nil:
		log.Error(err, "Failed to tag a release", "container", crew.Spec.ContainerRef.Name, "tag", step.Tag)
		r.setReleased(crew, status, metav1.ConditionFalse, reasonReleaseGitFailure,
			"Could not tag the next release: "+err.Error())
	case step.Commit != "":
		message := fmt.Sprintf("Released %s at %s", step.Tag, step.Commit)
		log.Info("Released milestone", "tag", step.Tag, "commit", step.Commit, "created", step.CreateTag)
		r.event(crew, corev1.EventTypeNormal, reasonReleased, message)
		status.LastRelease = &c4v1alpha1.CrewRelease{Tag: step.Tag, Commit: step.Commit, Time: metav1.NewTime(r.now())}
		r.setReleased(crew, status, metav1.ConditionTrue, reasonReleased, message)
	case step.Held != "":
		// The last poll's status write can fail after its Event, so the
		// Reconciler remembers what it said too.
		said := step.Reason + "\n" + step.Held
		prev := apimeta.FindStatusCondition(status.Conditions, ConditionReleased)
		last, _ := r.heldSaid.Load(crew.UID)
		if (prev == nil || prev.Reason != step.Reason || prev.Message != step.Held) && last != said {
			log.Info("Left a release to a person", "tag", step.Tag, "reason", step.Reason)
			r.event(crew, corev1.EventTypeWarning, step.Reason, step.Held)
		}
		r.heldSaid.Store(crew.UID, said)
		r.setReleased(crew, status, metav1.ConditionFalse, step.Reason, step.Held)
	default:
		if prev := apimeta.FindStatusCondition(status.Conditions, ConditionReleased); prev == nil ||
			prev.Reason != reasonReleased {
			r.setReleased(crew, status, metav1.ConditionTrue, reasonNoReleaseDone, "No open release is done")
		}
	}
}

// releaseFacts reads the open releases, their issues and tags, and the Tasks'
// merges.
func releaseFacts(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	tasks []c4v1alpha1.Task) (model.ReleaseFacts, error) {
	var facts model.ReleaseFacts
	milestones, err := provider.ListMilestones(ctx, repo)
	if err != nil {
		return facts, fmt.Errorf("milestones: %w", err)
	}
	for _, m := range milestones {
		if !model.IsReleaseTitle(m.Title) || m.Due.IsZero() {
			continue
		}
		open, closed, err := provider.MilestoneIssues(ctx, repo, m.ID)
		if err != nil {
			return facts, fmt.Errorf("issues of milestone %s: %w", m.Title, err)
		}
		facts.Milestones = append(facts.Milestones, model.ReleaseMilestone{
			Release: model.Release{Title: m.Title, Due: m.Due}, ID: m.ID, Open: open, Closed: closed})
		commit, err := provider.GetTag(ctx, repo, m.Title)
		if err != nil {
			return facts, fmt.Errorf("tag %s: %w", m.Title, err)
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
	return facts, nil
}

func (r *CrewReconciler) setReleased(crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus,
	value metav1.ConditionStatus, reason, message string) {
	setCondition(&status.Conditions, crew.Generation, ConditionReleased, value, reason, message)
}

func (r *CrewReconciler) event(crew *c4v1alpha1.Crew, eventType, reason, note string) {
	if r.Recorder != nil {
		r.Recorder.Eventf(crew, nil, eventType, reason, "TagRelease", "%s", note)
	}
}
