/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// The Brief's releases (docs/plans/done/brief-releases.md): a released Container's
// Crew that the Brief made tags and prepares releases, and publishes them where
// spec.releases says; its rounds' issues go into a release milestone.

// briefRelease returns the Brief's spec.releases entry for container, or nil.
func briefRelease(brief *c4v1beta1.Brief, container string) *c4v1beta1.BriefRelease {
	for i := range brief.Spec.Releases {
		if brief.Spec.Releases[i].Container == container {
			return &brief.Spec.Releases[i]
		}
	}
	return nil
}

// releaseSpec turns releases on in a Crew's spec (R4). It never turns
// anything off (R2); briefTarget sets where they are published (R5).
func releaseSpec(spec *c4v1beta1.CrewSpec) {
	spec.TagReleases = true
	spec.PrepareReleases = true
}

// publishJSON is a release target in the one form the Crew's
// AnnotationBriefPublishReleases holds and the Brief compares; empty for none.
func publishJSON(p *c4v1beta1.CrewPublishReleases) string {
	if p == nil {
		return ""
	}
	out, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(out)
}

// briefMark is the release target the Brief last wrote on the Crew, as
// publishJSON says it, and whether it wrote one. A mark that isn't JSON is
// taken as it is, so it matches no entry and the Brief writes it again.
func briefMark(crew *c4v1beta1.Crew) (*c4v1beta1.CrewPublishReleases, string, bool) {
	raw, ok := crew.Annotations[c4v1beta1.AnnotationBriefPublishReleases]
	if !ok {
		return nil, "", false
	}
	var p c4v1beta1.CrewPublishReleases
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, raw, true
	}
	return &p, publishJSON(&p), true
}

// briefTarget sets the release target of a Crew the Brief made from its
// spec.releases entry, as model.ReleaseTarget decides: written with the mark,
// or left as it is when someone changed it on the Crew, which holds it. With
// no entry the Brief sets no target: the Crew keeps its own (R5), unmarked.
// It returns whether the Crew is held.
func briefTarget(crew *c4v1beta1.Crew, entry *c4v1beta1.BriefRelease) bool {
	if entry == nil {
		delete(crew.Annotations, c4v1beta1.AnnotationBriefPublishReleases)
		return false
	}
	want := publishJSON(&entry.CrewPublishReleases)
	_, mark, marked := briefMark(crew)
	action := model.ReleaseTarget(want, mark, marked, publishJSON(crew.Spec.PublishReleases))
	if action == model.TargetInSync {
		return false
	}
	if crew.Annotations == nil {
		crew.Annotations = map[string]string{}
	}
	crew.Annotations[c4v1beta1.AnnotationBriefPublishReleases] = want
	if action == model.TargetHold {
		return true
	}
	publish := *entry.CrewPublishReleases.DeepCopy()
	crew.Spec.PublishReleases = &publish
	return false
}

// targetDiffers is the Crew's release target as it differs from the one its
// Brief last set; nil when the Brief sets none or the two agree.
func targetDiffers(crew *c4v1beta1.Crew) *model.TargetDiffers {
	mark, want, marked := briefMark(crew)
	if !marked || want == publishJSON(crew.Spec.PublishReleases) {
		return nil
	}
	d := &model.TargetDiffers{Brief: crew.Labels[c4v1beta1.LabelBrief], Crew: crew.Name}
	if mark != nil {
		d.BriefRepo = mark.GitHub.Repository
	}
	if p := crew.Spec.PublishReleases; p != nil {
		d.CrewRepo = p.GitHub.Repository
		d.SameRepository = d.CrewRepo == d.BriefRepo
	} else {
		d.PublishesNowhere = true
	}
	return d
}

// releaseCrew turns releases on for the Crew the Brief made for container,
// and sets where they are published (briefTarget). A Crew it didn't make, or
// none yet, is left alone: releases on the first are its owner's, and the
// second gets them when the Brief makes it.
func (r *BriefReconciler) releaseCrew(ctx context.Context, brief *c4v1beta1.Brief, container string) error {
	var crew c4v1beta1.Crew
	err := r.Get(ctx, client.ObjectKey{Namespace: brief.Namespace, Name: container}, &crew)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if crew.Labels[c4v1beta1.LabelBrief] != brief.Name {
		return nil
	}
	orig := crew.DeepCopy()
	releaseSpec(&crew.Spec)
	held := briefTarget(&crew, briefRelease(brief, container))
	if equality.Semantic.DeepEqual(crew.Spec, orig.Spec) &&
		maps.Equal(crew.Annotations, orig.Annotations) {
		return nil
	}
	if err := r.Patch(ctx, &crew, client.MergeFrom(orig)); err != nil {
		return err
	}
	if held {
		logf.FromContext(ctx).Info("Left a Crew's release target that differs from spec.releases",
			"crew", crew.Name)
	}
	if equality.Semantic.DeepEqual(crew.Spec, orig.Spec) {
		return nil
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
// reaches the Crew without waiting for a round, and a removed one drops the
// Brief's mark from it.
func (r *BriefReconciler) syncReleases(ctx context.Context, brief *c4v1beta1.Brief) error {
	names := map[string]bool{}
	for _, entry := range brief.Spec.Releases {
		names[entry.Container] = true
	}
	var crews c4v1beta1.CrewList
	if err := r.List(ctx, &crews, client.InNamespace(brief.Namespace),
		client.MatchingLabels{c4v1beta1.LabelBrief: brief.Name}); err != nil {
		return err
	}
	for _, crew := range crews.Items {
		if _, marked := crew.Annotations[c4v1beta1.AnnotationBriefPublishReleases]; marked {
			names[crew.Name] = true
		}
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		if err := r.releaseCrew(ctx, brief, name); err != nil {
			return fmt.Errorf("turning on the releases of Crew %s: %w", name, err)
		}
	}
	return nil
}

// releasedContainers are the Containers a round releases: those its plan
// names, those spec.releases names, and those whose Crew the Brief made
// already tags releases, as one released once stays released (R2).
func (r *BriefReconciler) releasedContainers(ctx context.Context, brief *c4v1beta1.Brief, plan model.Plan) (
	map[string]bool, error) {
	released := map[string]bool{}
	for _, name := range plan.Releases {
		released[name] = true
	}
	for _, entry := range brief.Spec.Releases {
		released[entry.Container] = true
	}
	var crews c4v1beta1.CrewList
	if err := r.List(ctx, &crews, client.InNamespace(brief.Namespace),
		client.MatchingLabels{c4v1beta1.LabelBrief: brief.Name}); err != nil {
		return nil, err
	}
	for _, crew := range crews.Items {
		if crew.Spec.TagReleases {
			released[crew.Spec.ContainerRef.Name] = true
		}
	}
	return released, nil
}

// releaseMilestone returns the ID of the milestone the Brief's round puts an
// issue of a released Container in, in repo (plans/done/fixes-patch-next.md, F6,
// F7). Any other work goes into the round's release milestone: the one whose
// description carries the round's marker, open or closed, so a restart makes
// no second one, or else an open release milestone an earlier round left with
// no issue, titled the next minor version (joinEmptyRelease, #2025), or else a
// new one titled the next minor version, due after every open release (R6). A fix goes into the patch slot right after the release in
// progress (patchMilestone), and into the round's release milestone while the
// repository has no release yet. Under a cadence that cuts, every issue goes
// into the release in progress instead (cadenceMilestone).
func releaseMilestone(ctx context.Context, provider ports.Issues, repo ports.Repository, brief string,
	round int, fix bool, cadence model.ReleaseCadence) (int64, error) {
	milestones, err := provider.ListAllMilestones(ctx, repo)
	if err != nil {
		return 0, err
	}
	if cadence.Cuts() {
		return cadenceMilestone(ctx, provider, repo, brief, round, cadence, milestones)
	}
	var minor *ports.Milestone
	var others []ports.Milestone
	for i, m := range milestones {
		switch {
		case model.HasReleaseMilestoneMarker(m.Description, brief, round):
			minor = &milestones[i]
		case model.HasPatchMilestoneMarker(m.Description, brief, round):
			if fix {
				return m.ID, nil
			}
		default:
			others = append(others, m)
		}
	}
	if fix {
		id, err := patchMilestone(ctx, provider, repo, brief, round, others, minor)
		if err != nil || id != 0 {
			return id, err
		}
	}
	if minor != nil {
		return minor.ID, nil
	}
	if joined, err := joinEmptyRelease(ctx, provider, repo, brief, round, milestones); err != nil || joined != 0 {
		return joined, err
	}
	titles := make([]string, 0, len(milestones))
	var open []model.Release
	for _, m := range milestones {
		titles = append(titles, m.Title)
		if !m.Closed {
			open = append(open, model.Release{Title: m.Title, Due: m.Due})
		}
	}
	title := model.ReleaseMilestoneTitle(titles, false)
	m, err := provider.CreateMilestone(ctx, repo, title, model.ReleaseMilestoneDescription(brief, round),
		model.ReleaseMilestoneDueAt(open, time.Now()))
	if err != nil {
		return 0, err
	}
	logf.FromContext(ctx).Info("Made the round's release milestone", "repository", repo.Owner+"/"+repo.Name,
		"milestone", title, "round", round)
	return m.ID, nil
}

// joinEmptyRelease makes the open release milestone an earlier round of the
// Brief made and left with no issue the round's release milestone, by adding the round's
// marker to its description, and returns its ID; 0 when there is none to join
// (model.EmptyReleaseToJoin, #2025). The round's work then goes into the next
// minor release the planner names, not one after it.
func joinEmptyRelease(ctx context.Context, provider ports.Issues, repo ports.Repository, brief string,
	round int, milestones []ports.Milestone) (int64, error) {
	var releases []model.ReleaseMilestone
	var made []int64
	for _, m := range milestones {
		release := model.ReleaseMilestone{Release: model.Release{Title: m.Title, Due: m.Due}, ID: m.ID,
			MilestoneClosed: m.Closed}
		if !m.Closed && !m.Due.IsZero() && model.IsReleaseTitle(m.Title) &&
			model.HasAnyReleaseMilestoneMarker(m.Description, brief) {
			made = append(made, m.ID)
			issues, err := provider.MilestoneIssues(ctx, repo, m.ID)
			if err != nil {
				return 0, err
			}
			for _, i := range issues {
				release.Open = append(release.Open, i.Number)
			}
		}
		releases = append(releases, release)
	}
	join := model.EmptyReleaseToJoin(releases, made)
	if join == nil {
		return 0, nil
	}
	i := slices.IndexFunc(milestones, func(m ports.Milestone) bool { return m.ID == join.ID })
	description := model.AddMarker(milestones[i].Description, model.ReleaseMilestoneMarker(brief, round))
	if err := provider.DescribeMilestone(ctx, repo, join.ID, description); err != nil {
		return 0, err
	}
	logf.FromContext(ctx).Info("Took an empty release milestone for the round's", "repository",
		repo.Owner+"/"+repo.Name, "milestone", join.Title, "round", round)
	return join.ID, nil
}

// patchMilestone returns the ID of the patch slot of the Brief's round among
// others, the repository's milestones but the round's own (F1 to F4): an open
// patch that can still take work, joined as it is, or a new one made with the
// round's patch marker. It returns 0 when others hold no release, so the fix
// goes into the round's release milestone. The round's own release milestone,
// minor once made, is left out of where the patch goes, as it holds the
// round's other work; a new patch is due before it.
func patchMilestone(ctx context.Context, provider ports.Issues, repo ports.Repository, brief string,
	round int, others []ports.Milestone, minor *ports.Milestone) (int64, error) {
	var releases []model.ReleaseMilestone
	for _, m := range others {
		if !model.IsReleaseTitle(m.Title) {
			continue
		}
		release := model.ReleaseMilestone{Release: model.Release{Title: m.Title, Due: m.Due}, ID: m.ID,
			MilestoneClosed: m.Closed}
		if !m.Closed {
			issues, err := provider.MilestoneIssues(ctx, repo, m.ID)
			if err != nil {
				return 0, fmt.Errorf("issues of milestone %s: %w", m.Title, err)
			}
			for _, i := range issues {
				if slices.Contains(i.Labels, model.LabelRelease) {
					release.Prepared = append(release.Prepared, i.Number)
				}
			}
		}
		releases = append(releases, release)
	}
	if len(releases) == 0 {
		return 0, nil
	}
	slot := model.PatchSlot(releases, time.Now())
	if slot.ID != 0 {
		return slot.ID, nil
	}
	if minor != nil && !minor.Due.IsZero() && !slot.Due.Before(minor.Due) {
		// Made an hour or more after every other open release (P4), so
		// half an hour before it is still after the release in progress.
		slot.Due = minor.Due.Add(-30 * time.Minute)
	}
	m, err := provider.CreateMilestone(ctx, repo, slot.Title, model.PatchMilestoneDescription(brief, round), slot.Due)
	if err != nil {
		return 0, err
	}
	logf.FromContext(ctx).Info("Made the round's patch milestone", "repository", repo.Owner+"/"+repo.Name,
		"milestone", slot.Title, "round", round)
	return m.ID, nil
}

// adoptStrayWork puts the work each released Container the Brief made
// already has, in no milestone, into its first release that can still take
// work (#599, #834): issues filed before releases were turned on would
// otherwise come after every release, the next round's included. A Container
// that isn't Ready, or has no release without a prepare issue, is left as it is.
func (r *BriefReconciler) adoptStrayWork(ctx context.Context, run *roundApply) error {
	for _, name := range slices.Sorted(maps.Keys(run.released)) {
		var c c4v1beta1.Container
		err := r.Get(ctx, client.ObjectKey{Namespace: run.brief.Namespace, Name: name}, &c)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		if c.Labels[c4v1beta1.LabelBrief] != run.brief.Name || c.Status.Phase != c4v1beta1.ContainerReady {
			continue
		}
		if err := r.adoptContainerWork(ctx, &c); err != nil {
			return fmt.Errorf("putting the work of %s in its first release: %w", name, err)
		}
	}
	return nil
}

func (r *BriefReconciler) adoptContainerWork(ctx context.Context, c *c4v1beta1.Container) error {
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, c.Namespace, c.Spec.GitProviderRef.Name)
	if err != nil {
		return err
	}
	repo := provider.Repository(c.Name)
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	first, err := releaseTakingWork(gitCtx, provider, repo)
	if err != nil {
		return err
	}
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
