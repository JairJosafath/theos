/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"fmt"
	"slices"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// A Brief's apps release on its cadence (docs/plans/release-cadence.md, C2,
// C5, C9): the Brief writes it onto the Crews it made, and under Day or Week
// its rounds file a released app's work into the release in progress.

// briefCadence is the Brief's release cadence: spec.releaseCadence, which the
// CRD defaults to every: Day, else that default, for a Brief read some other
// way.
func briefCadence(brief *c4v1beta1.Brief) *c4v1beta1.ReleaseCadence {
	if c := brief.Spec.ReleaseCadence; c != nil {
		return c.DeepCopy()
	}
	return &c4v1beta1.ReleaseCadence{Every: model.CadenceDay, At: model.DefaultCutAt,
		TimeZone: model.DefaultCutTimeZone}
}

// briefModelCadence is the Brief's cadence for the model.
func briefModelCadence(brief *c4v1beta1.Brief) model.ReleaseCadence {
	c := briefCadence(brief)
	return model.ReleaseCadence{Every: c.Every, At: c.At, TimeZone: c.TimeZone, Weekday: c.Weekday}
}

// cadenceMilestone returns the ID of the milestone the Brief's round puts a
// released Container's issue in under a cadence that cuts (C5): the release
// in progress, the first open release that can still take work, or a new one
// made with the round's marker (R6), titled and due as model.CadenceSlot
// says. A restart finds the one it made: open and taking work, it is the
// release in progress.
func cadenceMilestone(ctx context.Context, provider ports.Issues, repo ports.Repository, brief string,
	round int, cadence model.ReleaseCadence, milestones []ports.Milestone) (int64, error) {
	releases := make([]model.ReleaseMilestone, 0, len(milestones))
	for _, m := range milestones {
		_, cut := model.CutMarkTime(m.Description)
		release := model.ReleaseMilestone{Release: model.Release{Title: m.Title, Due: m.Due}, ID: m.ID,
			MilestoneClosed: m.Closed, Cut: cut}
		if !m.Closed && !cut && model.IsReleaseTitle(m.Title) && !m.Due.IsZero() {
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
	slot, err := model.CadenceSlot(releases, cadence, time.Now())
	if err != nil {
		return 0, fmt.Errorf("spec.releaseCadence: %w", err)
	}
	if slot.ID != 0 {
		return slot.ID, nil
	}
	m, err := provider.CreateMilestone(ctx, repo, slot.Title, model.ReleaseMilestoneDescription(brief, round), slot.Due)
	if err != nil {
		return 0, err
	}
	logf.FromContext(ctx).Info("Made the release in progress", "repository", repo.Owner+"/"+repo.Name,
		"milestone", slot.Title, "round", round, "due", slot.Due)
	return m.ID, nil
}

// cadenceLabels are the labels the Brief adds to an issue of a released
// Container it files or takes under a cadence that cuts: release/now for
// one the plan marks bigger, but never in a tending round, which has no
// person's word that it is (C9); and kind/bug for a suggestion taken as a
// fix that has no fix label, so the Crew sees it as one at the cut (C7).
func cadenceLabels(cadence model.ReleaseCadence, tendingRound, now, fix bool, labels []string) []string {
	if !cadence.Cuts() {
		return nil
	}
	var out []string
	if fix && !model.IsFix(labels) {
		out = append(out, model.LabelBug)
	}
	if now && !tendingRound && !model.IsReleaseNow(labels) {
		out = append(out, model.LabelReleaseNow)
	}
	return out
}

// cadenceIssueLabels are the labels cadenceLabels may add, for the Git host.
func cadenceIssueLabels() []ports.Label {
	out := make([]ports.Label, 0, 2)
	for _, l := range []model.IssueLabel{model.BugLabel(), model.ReleaseNowLabel()} {
		out = append(out, ports.Label{Name: l.Name, Color: l.Color, Description: l.Description, Exclusive: l.Exclusive})
	}
	return out
}

// appNextRelease is an app's next release, from its Crew's next cut; nil
// while no release holds work or the Crew has no cadence.
func appNextRelease(crew *c4v1beta1.Crew) *c4v1beta1.BriefAppNextRelease {
	next := crew.Status.NextCut
	if next == nil || next.Release == "" {
		return nil
	}
	return &c4v1beta1.BriefAppNextRelease{Tag: next.Release, Time: next.Time, Merged: next.Merged,
		TimeZone: crewCadence(crew).Zone()}
}
