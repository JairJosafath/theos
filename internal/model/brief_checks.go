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

package model

import (
	"fmt"
	"slices"
	"strings"
)

// A Brief sets when its apps' CI runs the full check (#977): a plan's
// Container may say checks, EveryChange or BeforeRelease, and the round sets
// it on the Container. Left out, the Container's setting stays as it is, so
// one a person made isn't undone.

// checksOrDefault is a Container's checks, where empty reads as EveryChange.
func checksOrDefault(checks string) string {
	if checks == "" {
		return ChecksEveryChange
	}
	return checks
}

// checksChange reports whether the plan's Container o sets checks other than
// the existing Container before has.
func checksChange(before, o PlanObject) bool {
	return o.Kind == KindContainer && o.Checks != "" && o.Checks != checksOrDefault(before.Checks)
}

// checkChecks checks the plan's Containers' checks: EveryChange or
// BeforeRelease, set only when a new prompt asks, and changed only on a
// Container the Brief made.
func checkChecks(plan Plan, current CurrentModel, refuse func(string, string, ...any)) {
	for _, o := range plan.Objects {
		if o.Kind != KindContainer || o.Checks == "" {
			continue
		}
		subject := o.Ref().String()
		if o.Checks != ChecksEveryChange && o.Checks != ChecksBeforeRelease {
			refuse(subject, "checks is %q; write %s or %s, or leave it out to keep the Container's setting",
				o.Checks, ChecksEveryChange, ChecksBeforeRelease)
			continue
		}
		before, exists := findObject(current.Objects, o.Ref())
		if exists && !checksChange(before, o) || !exists && o.Checks == ChecksEveryChange {
			continue
		}
		if exists && !slices.Contains(current.BriefContainers, o.Name) {
			refuse(subject, "checks can't change (from %s to %s): the Brief didn't make this Container, so its "+
				"checks are the person's to set; leave checks out", checksOrDefault(before.Checks), o.Checks)
			continue
		}
		if !current.NewPrompt {
			refuse(subject, "sets checks: %s in a round with no new prompt; when CI runs the full check is the "+
				"person's say, so set it only when a new prompt asks, and leave it out otherwise", o.Checks)
		}
	}
}

// ChecksNotes are the sentences a round's summary adds for each of the plan's
// Containers that asks for checks: BeforeRelease the Standard can't give it,
// so the person reads that it doesn't take effect (ChecksApplied says so on
// the Container). A Container on a Bundle has the Bundle's Standard (#1622).
func ChecksNotes(plan Plan, catalog []CatalogEntry, bundles []Bundle) []string {
	var notes []string
	for _, o := range plan.Objects {
		if o.Kind != KindContainer || o.Checks != ChecksBeforeRelease {
			continue
		}
		standard := o.Standard
		if o.Bundle != "" {
			b, _ := findBundle(bundles, o.Bundle)
			standard = b.Standard
		}
		if standard == "" {
			notes = append(notes, fmt.Sprintf("%s asks for checks %s, but has no Standard, so its CI runs no "+
				"full check to move.", o.Name, ChecksBeforeRelease))
			continue
		}
		i := slices.IndexFunc(catalog, func(e CatalogEntry) bool {
			return e.Side == SideStandard && e.Name == standard
		})
		if i >= 0 && !catalog[i].ChecksBeforeRelease {
			notes = append(notes, fmt.Sprintf("%s asks for checks %s, but its Standard %s can't run the full "+
				"check only before a release, so its CI runs it on every change.", o.Name, ChecksBeforeRelease,
				standard))
		}
	}
	return notes
}

// AppliedRoundSummary is RoundSummary for a round the operator applied, with
// ChecksNotes after the planner's words, still within the history's limit.
func AppliedRoundSummary(plan Plan, catalog []CatalogEntry, bundles []Bundle) string {
	notes := strings.Join(ChecksNotes(plan, catalog, bundles), " ")
	if notes == "" {
		return RoundSummary(plan)
	}
	notes = ClipSentences(notes, maxRoundSummary/2)
	return roundSummary(plan, maxRoundSummary-len(notes)-1) + " " + notes
}
