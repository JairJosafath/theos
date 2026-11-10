/*
Copyright 2026 The theos Authors.

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
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// A Crew's releases go out on its cadence (docs/plans/done/release-cadence.md): at
// each cut the Crew marks the release in progress cut, carries its open
// issues on into the next release and tags it as with every Release.

// reasonReleaseCut is the Released condition's reason, and the Normal
// Event's, once the Crew cut its release.
const reasonReleaseCut = "ReleaseCut"

// crewCadence is the cadence the Crew's releases go out on: its
// spec.releaseCadence while it tags releases, as cutting is part of tagging;
// every Release otherwise.
func crewCadence(crew *c4v1beta1.Crew) model.ReleaseCadence {
	c := crew.Spec.ReleaseCadence
	if c == nil || !crew.Spec.TagReleases {
		return model.ReleaseCadence{}
	}
	return model.ReleaseCadence{Every: c.Every, At: c.At, TimeZone: c.TimeZone, Weekday: c.Weekday}
}

// crewIssueLabels are the labels the Crew keeps in its repository: the
// factory's, and release/now while it releases at cuts (C8).
func crewIssueLabels(cadence model.ReleaseCadence) []ports.Label {
	labels := issueLabels()
	if cadence.Cuts() {
		l := model.ReleaseNowLabel()
		labels = append(labels, ports.Label{Name: l.Name, Color: l.Color, Description: l.Description,
			Exclusive: l.Exclusive})
	}
	return labels
}

// cutFacts adds the cadence to the release facts: the time, and the last
// cut, the one the status recorded or, when later, the newest cut mark among
// the milestones, so a status write lost after a cut cuts nothing again. A
// Crew that just went on its cadence records now as its last cut: its first
// cut is the first after it.
func cutFacts(facts *model.ReleaseFacts, cadence model.ReleaseCadence, status *c4v1beta1.CrewStatus,
	milestones []ports.Milestone, now time.Time) {
	facts.Cadence, facts.Now = cadence, now
	if !cadence.Cuts() {
		return
	}
	if status.LastCut == nil {
		at := metav1.NewTime(now)
		status.LastCut = &at
	}
	descriptions := make([]string, 0, len(milestones))
	for _, m := range milestones {
		descriptions = append(descriptions, m.Description)
	}
	facts.LastCut = model.LastCut(status.LastCut.Time, descriptions)
}

// cutRelease makes the cut of release step.Tag, milestone step.Milestone,
// that step.Cut says (C4, C6, C7), and returns the title of the release its
// open issues went into. In order: the cut mark, so no more work goes in and
// a restart finds the cut; the retitle; the release the issues go into, the
// one step.Cut names, else one the Crew made for this cut before (its carry
// marker), else a new one; each issue, a comment before its move, so a
// failed move is commented again; and, for a release with no merged work,
// closing it untagged. Each step done is found done again.
func cutRelease(ctx context.Context, crew *c4v1beta1.Crew, provider ports.Issues, repo ports.Repository,
	step model.ReleaseStep, milestones []ports.Milestone) (string, error) {
	log := logf.FromContext(ctx)
	cut := step.Cut
	i := slices.IndexFunc(milestones, func(m ports.Milestone) bool { return m.ID == step.Milestone })
	if i < 0 {
		return "", fmt.Errorf("milestone %s is gone", step.Tag)
	}
	release := milestones[i]
	if !cut.Marked && !cut.Untagged {
		description := model.AddMarker(release.Description, model.CutMarker(crew.Name, step.CutAt))
		if err := provider.DescribeMilestone(ctx, repo, release.ID, description); err != nil {
			return "", fmt.Errorf("marking %s cut: %w", step.Tag, err)
		}
	}
	title := step.Tag
	if cut.Retitle != "" {
		if err := provider.EditMilestoneTitle(ctx, repo, release.ID, cut.Retitle); err != nil {
			return "", fmt.Errorf("retitling %s %s: %w", step.Tag, cut.Retitle, err)
		}
		log.Info("Retitled a cut release", "from", step.Tag, "tag", cut.Retitle)
		title = cut.Retitle
	}
	into := ""
	if len(cut.Carry) > 0 {
		id, intoTitle, err := carryTarget(ctx, crew, provider, repo, step, title, milestones)
		if err != nil {
			return "", err
		}
		into = intoTitle
		for _, n := range cut.Carry {
			if err := provider.CommentIssue(ctx, repo, n, model.CarriedComment(title, into, crewCadence(crew))); err != nil {
				return into, fmt.Errorf("commenting on #%d: %w", n, err)
			}
			if err := provider.SetIssueMilestone(ctx, repo, n, id); err != nil {
				return into, fmt.Errorf("carrying #%d into %s: %w", n, into, err)
			}
			log.Info("Carried an issue on at the cut", "tag", title, "issue", n, "into", into)
		}
	}
	if cut.Untagged {
		if err := provider.CloseMilestone(ctx, repo, release.ID); err != nil {
			return into, fmt.Errorf("closing %s: %w", title, err)
		}
	}
	return into, nil
}

// carryTarget is the release a cut of release title carries its open issues
// into: the one step.Cut names, else an open one this Crew made at an
// earlier try of the cut, else a new one, titled and due as step.Cut says.
func carryTarget(ctx context.Context, crew *c4v1beta1.Crew, provider ports.Issues, repo ports.Repository,
	step model.ReleaseStep, title string, milestones []ports.Milestone) (int64, string, error) {
	cut := step.Cut
	for _, m := range milestones {
		if cut.Into != 0 && m.ID == cut.Into || cut.Into == 0 && !m.Closed &&
			model.HasCarryMarker(m.Description, crew.Name, title) {
			return m.ID, m.Title, nil
		}
	}
	if cut.Into != 0 {
		return 0, "", fmt.Errorf("milestone %d to carry %s's open issues into is gone", cut.Into, title)
	}
	made, err := provider.CreateMilestone(ctx, repo, cut.IntoTitle, model.CarryMilestoneDescription(crew.Name, title),
		cut.IntoDue)
	if err != nil {
		return 0, "", fmt.Errorf("making %s: %w", cut.IntoTitle, err)
	}
	logf.FromContext(ctx).Info("Made the release a cut carries work into", "tag", title, "into", made.Title,
		"due", cut.IntoDue)
	return made.ID, made.Title, nil
}

// cadenceStatus writes the Crew's next cut and the release it cut and
// hasn't tagged (C10, C12), from the facts as the step left them: a cut
// made now marks its release, a tag made now ends it. Off a cadence, it
// clears them.
func cadenceStatus(status *c4v1beta1.CrewStatus, facts model.ReleaseFacts, step model.ReleaseStep, done bool) {
	if !facts.Cadence.Cuts() {
		status.NextCut, status.LastCut, status.CutRelease = nil, nil, ""
		return
	}
	if done && !step.CutAt.IsZero() {
		at := metav1.NewTime(step.CutAt)
		status.LastCut = &at
		facts.LastCut = step.CutAt
	}
	facts.Milestones = slices.Clone(facts.Milestones)
	i := slices.IndexFunc(facts.Milestones, func(m model.ReleaseMilestone) bool { return m.ID == step.Milestone })
	switch {
	case !done || i < 0:
	case step.Commit != "" || step.Cut != nil && step.Cut.Untagged:
		facts.Milestones = slices.Delete(facts.Milestones, i, i+1)
	case step.Cut != nil:
		facts.Milestones[i].Cut = true
		facts.Milestones[i].Title = cmp.Or(step.Cut.Retitle, facts.Milestones[i].Title)
	}
	status.CutRelease = model.CutReleaseOf(facts)
	next, err := model.NextCutOf(facts)
	if err != nil {
		status.NextCut = nil
		return
	}
	status.NextCut = &c4v1beta1.CrewNextCut{Time: metav1.NewTime(next.At), Release: next.Release,
		Merged: int32(next.Merged)}
}
