/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package model

import "fmt"

// A Brief's release target (docs/plans/done/brief-release-target.md): the Brief
// remembers on the Crew the target it last set, so it tells a change of its
// spec.releases entry from a change made on the Crew.

// TargetAction is what a Brief does with the release target of a Crew it made.
type TargetAction int

const (
	// TargetInSync leaves the Crew: it publishes where the entry says.
	TargetInSync TargetAction = iota
	// TargetWrite writes the entry to the Crew's spec and its mark.
	TargetWrite
	// TargetHold leaves the Crew's spec, which someone else changed, and
	// marks it with the entry if it has no mark yet: the Crew is held.
	TargetHold
)

// ReleaseTarget decides what a Brief does with the release target of a Crew
// it made, from its spec.releases entry, the target it last wrote on the Crew
// (mark, when marked) and the Crew's own, each in one canonical form; crew is
// empty when the Crew publishes nowhere. The newest act on the Brief wins: an
// entry other than the mark is written. An entry equal to the mark and a Crew
// other than both was changed on the Crew, and is held. A Crew from before
// the mark is adopted when it publishes nowhere or where the entry says, and
// held otherwise, as nobody can tell who set it.
func ReleaseTarget(entry, mark string, marked bool, crew string) TargetAction {
	switch {
	case marked && entry != mark:
		return TargetWrite
	case crew == entry && marked:
		return TargetInSync
	case !marked && (crew == "" || crew == entry):
		return TargetWrite
	default:
		return TargetHold
	}
}

// TargetDiffers is a Crew held because its release target differs from the
// one its Brief's spec.releases entry sets: the repositories each names, the
// Crew's empty when it publishes nowhere.
type TargetDiffers struct {
	Brief, Crew         string
	BriefRepo, CrewRepo string
	SameRepository      bool
	PublishesNowhere    bool
}

// crewTarget says where the held Crew's spec.publishReleases points.
func (d TargetDiffers) crewTarget() string {
	switch {
	case d.PublishesNowhere:
		return "nowhere"
	case d.SameRepository:
		return d.CrewRepo + " with other settings"
	default:
		return d.CrewRepo
	}
}

// ReleaseTargetSetByBrief is the message of a Crew's ReleaseTarget condition
// while its Brief sets the target.
func ReleaseTargetSetByBrief(brief string) string {
	return fmt.Sprintf("Brief %s sets this in spec.releases; change it there", brief)
}

// ReleaseTargetDiffersMessage is the message of a held Crew's ReleaseTarget
// condition and Event: both targets and the two ways to settle it.
func ReleaseTargetDiffersMessage(d TargetDiffers) string {
	return fmt.Sprintf("The Crew's spec.publishReleases points to %s, but Brief %s's spec.releases entry for %s "+
		"says %s; nothing is published until they agree. Change the Brief's entry to the target wanted, or "+
		"put the Crew's spec.publishReleases back", d.crewTarget(), d.Brief, d.Crew, d.BriefRepo)
}

// ReleaseTargetPublishMessage is the message of a held Crew's Published
// condition.
func ReleaseTargetPublishMessage(d TargetDiffers) string {
	return fmt.Sprintf("Not published: the Crew's target (%s) differs from Brief %s's (%s); see the "+
		"ReleaseTarget condition", d.crewTarget(), d.Brief, d.BriefRepo)
}

// ReleaseTargetNeedsYou is the Brief's status.needsYou item of a held Crew.
// It recommends the Crew's target, the person's latest act; only a person
// knows which repository is right, so no autopilot answers it.
func ReleaseTargetNeedsYou(d TargetDiffers) NeedsYou {
	recommend := fmt.Sprintf("Change the Brief's spec.releases entry for %s to the Crew's target.", d.Crew)
	if d.PublishesNowhere {
		recommend = fmt.Sprintf("Remove the Brief's spec.releases entry for %s, or put the Crew's "+
			"spec.publishReleases back.", d.Crew)
	}
	return NeedsYou{Kind: AttentionCrew, Name: d.Crew,
		Question: fmt.Sprintf("Crew `%s` publishes its releases to %s, but the Brief says %s: where should "+
			"they go?", d.Crew, d.crewTarget(), d.BriefRepo),
		Recommend: recommend,
		Why: fmt.Sprintf("The Crew was changed to %s but the Brief still says %s; nothing is published until "+
			"they agree.", d.crewTarget(), d.BriefRepo)}
}
