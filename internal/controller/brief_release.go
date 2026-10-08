/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// The Brief's releases (docs/plans/done/brief-releases.md): a released Container's
// Crew that the Brief made tags and prepares releases, and publishes them where
// spec.releases says; its rounds' issues go into a release milestone.

// briefRelease returns the Brief's spec.releases entry for container, or nil.
func briefRelease(brief *c4v1alpha1.Brief, container string) *c4v1alpha1.BriefRelease {
	for i := range brief.Spec.Releases {
		if brief.Spec.Releases[i].Container == container {
			return &brief.Spec.Releases[i]
		}
	}
	return nil
}

// releaseSpec turns releases on in a Crew's spec (R4), publishing them as
// entry says when there is one (R5). It never turns anything off (R2).
func releaseSpec(spec *c4v1alpha1.CrewSpec, entry *c4v1alpha1.BriefRelease) {
	spec.TagReleases = true
	spec.PrepareReleases = true
	if entry != nil {
		publish := *entry.CrewPublishReleases.DeepCopy()
		spec.PublishReleases = &publish
	}
}

// releaseCrew turns releases on for the Crew the Brief made for container. A
// Crew it didn't make, or none yet, is left alone: releases on the first are
// its owner's, and the second gets them when the Brief makes it.
func (r *BriefReconciler) releaseCrew(ctx context.Context, brief *c4v1alpha1.Brief, container string) error {
	var crew c4v1alpha1.Crew
	err := r.Get(ctx, client.ObjectKey{Namespace: brief.Namespace, Name: container}, &crew)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if crew.Labels[c4v1alpha1.LabelBrief] != brief.Name {
		return nil
	}
	orig := crew.DeepCopy()
	releaseSpec(&crew.Spec, briefRelease(brief, container))
	if equality.Semantic.DeepEqual(crew.Spec, orig.Spec) {
		return nil
	}
	if err := r.Patch(ctx, &crew, client.MergeFrom(orig)); err != nil {
		return err
	}
	publishTo := ""
	if crew.Spec.PublishReleases != nil {
		publishTo = crew.Spec.PublishReleases.GitHub.Repository
	}
	logf.FromContext(ctx).Info("Turned on the Crew's releases", "crew", crew.Name, "publishTo", publishTo)
	return nil
}

// syncReleases brings the Crews the Brief made for its spec.releases entries
// up to date, whatever its round is doing: an entry added or changed later
// reaches the Crew without waiting for a round.
func (r *BriefReconciler) syncReleases(ctx context.Context, brief *c4v1alpha1.Brief) error {
	for _, entry := range brief.Spec.Releases {
		if err := r.releaseCrew(ctx, brief, entry.Container); err != nil {
			return fmt.Errorf("turning on the releases of Crew %s: %w", entry.Container, err)
		}
	}
	return nil
}

// releasedContainers are the Containers a round releases: those its plan
// names, those spec.releases names, and those whose Crew the Brief made
// already tags releases, as one released once stays released (R2).
func (r *BriefReconciler) releasedContainers(ctx context.Context, brief *c4v1alpha1.Brief, plan model.Plan) (
	map[string]bool, error) {
	released := map[string]bool{}
	for _, name := range plan.Releases {
		released[name] = true
	}
	for _, entry := range brief.Spec.Releases {
		released[entry.Container] = true
	}
	var crews c4v1alpha1.CrewList
	if err := r.List(ctx, &crews, client.InNamespace(brief.Namespace),
		client.MatchingLabels{c4v1alpha1.LabelBrief: brief.Name}); err != nil {
		return nil, err
	}
	for _, crew := range crews.Items {
		if crew.Spec.TagReleases {
			released[crew.Spec.ContainerRef.Name] = true
		}
	}
	return released, nil
}

// releaseMilestone returns the ID of the release milestone of the Brief's
// round in repo (R6): the one whose description carries the round's marker,
// open or closed, so a restart makes no second one, or a new one titled the
// next patch version for a round of fixes, else the next minor one, due after
// every open release (plans/done/brief-patch-releases.md).
func releaseMilestone(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository, brief string,
	round int, fixes bool) (int64, error) {
	milestones, err := provider.ListAllMilestones(ctx, repo)
	if err != nil {
		return 0, err
	}
	titles := make([]string, 0, len(milestones))
	var open []model.Release
	for _, m := range milestones {
		if model.HasReleaseMilestoneMarker(m.Description, brief, round) {
			return m.ID, nil
		}
		titles = append(titles, m.Title)
		if !m.Closed {
			open = append(open, model.Release{Title: m.Title, Due: m.Due})
		}
	}
	title := model.ReleaseMilestoneTitle(titles, fixes)
	m, err := provider.CreateMilestone(ctx, repo, title, model.ReleaseMilestoneDescription(brief, round),
		model.ReleaseMilestoneDueAt(open, time.Now()))
	if err != nil {
		return 0, err
	}
	logf.FromContext(ctx).Info("Made the round's release milestone", "repository", repo.Owner+"/"+repo.Name,
		"milestone", title, "round", round, "fixes", fixes)
	return m.ID, nil
}

// adoptStrayWork puts the work each released Container the Brief made
// already has, in no milestone, into its first open release (#599): issues
// filed before releases were turned on would otherwise come after every
// release, the next round's included. A Container that isn't Ready, or has
// no open release, is left as it is.
func (r *BriefReconciler) adoptStrayWork(ctx context.Context, run *roundApply) error {
	for _, name := range slices.Sorted(maps.Keys(run.released)) {
		var c c4v1alpha1.Container
		err := r.Get(ctx, client.ObjectKey{Namespace: run.brief.Namespace, Name: name}, &c)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		if c.Labels[c4v1alpha1.LabelBrief] != run.brief.Name || c.Status.Phase != c4v1alpha1.ContainerReady {
			continue
		}
		if err := r.adoptContainerWork(ctx, &c); err != nil {
			return fmt.Errorf("putting the work of %s in its first release: %w", name, err)
		}
	}
	return nil
}

func (r *BriefReconciler) adoptContainerWork(ctx context.Context, c *c4v1alpha1.Container) error {
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, c.Namespace, c.Spec.GitProviderRef.Name)
	if err != nil {
		return err
	}
	repo := provider.Repository(c.Name)
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	milestones, err := provider.ListMilestones(gitCtx, repo)
	if err != nil {
		return err
	}
	releases := make([]model.ReleaseMilestone, 0, len(milestones))
	for _, m := range milestones {
		releases = append(releases, model.ReleaseMilestone{Release: model.Release{Title: m.Title, Due: m.Due}, ID: m.ID})
	}
	first := model.FirstOpenRelease(releases)
	if first == 0 {
		return nil
	}
	open, err := provider.ListIssues(gitCtx, repo, "")
	if err != nil {
		return err
	}
	issues := make([]model.OpenIssue, 0, len(open))
	for _, i := range open {
		issues = append(issues, model.OpenIssue{Number: i.Number, Labels: i.Labels, Milestone: i.Milestone})
	}
	for _, n := range model.StrayWork(issues) {
		if err := provider.SetIssueMilestone(gitCtx, repo, n, first); err != nil {
			return err
		}
		logf.FromContext(ctx).Info("Put an issue in no milestone in the first release", "container", c.Name,
			"number", n, "milestone", first)
	}
	return nil
}
