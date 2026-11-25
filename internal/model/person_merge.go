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
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// A person's merge in the lead runtime (docs/plans/lead-runtime.md,
// decision 8; docs/plans/no-cluster.md, decision 10): theos merge merges an
// approved pull request from a person's terminal, and holds what no host
// is trusted to hold for it there: a counted approving verdict on the
// pull request's head from a session other than its author's, the checks
// main requires, and the protected paths.

// The merge settings of model/settings.yaml.
const (
	// MergePerson is the default: a person merges.
	MergePerson = "person"
	// MergeRules lets the merge rules merge with no person (#298), not built.
	MergeRules = "rules"
)

// The reasons a person's merge is refused.
const (
	ReasonRulesMergeNotBuilt = "RulesMergeNotBuilt"
	ReasonPullNotOpen        = "PullRequestNotOpen"
	ReasonNoVerdict          = "NoApprovingVerdict"
	ReasonAuthorsVerdict     = "AuthorsOwnVerdict"
	ReasonVerdictOlderHead   = "VerdictOnAnOlderHead"
	ReasonChecksNotGreen     = "ChecksNotGreen"
	ReasonProtectedPaths     = "ProtectedPathsChanged"
)

// PersonMergeFacts are what a person's merge of a Task's pull request is
// decided on.
type PersonMergeFacts struct {
	// Setting is model/settings.yaml's merge, "" for person.
	Setting string
	// Open means the pull request is open and not merged.
	Open bool
	// Head is the pull request's head.
	Head string
	// Comments are its issue's comments, and Rules the rules the markers
	// count by.
	Comments []HostComment
	Rules    ClaimRules
	// Checks is the state of the checks main requires on Head (BaseState).
	Checks string
	// Files are the paths the pull request changes, and Protected the
	// paths main protects.
	Files, Protected []string
}

// PersonMergeDecision is whether the merge goes, and why not.
type PersonMergeDecision struct {
	Merge  bool
	Reason string
	Says   string
}

// DecidePersonMerge decides a person's merge: the settings take it, the
// pull request is open, a counted approving verdict of a review run that
// came after the implement run whose done named the head is there from a
// session that isn't that run's holder, the checks main requires passed
// on the head, and no protected path changed.
func DecidePersonMerge(f PersonMergeFacts) PersonMergeDecision {
	refuse := func(reason, says string) PersonMergeDecision {
		return PersonMergeDecision{Reason: reason, Says: says}
	}
	switch {
	case f.Setting == MergeRules:
		return refuse(ReasonRulesMergeNotBuilt, "merge: rules in model/settings.yaml merges with no person, which "+
			"is not built yet (#298)")
	case f.Setting != "" && f.Setting != MergePerson:
		return refuse(ReasonRulesMergeNotBuilt, fmt.Sprintf("merge: %q in model/settings.yaml is not %s",
			f.Setting, MergePerson))
	case !f.Open:
		return refuse(ReasonPullNotOpen, "the pull request is not open")
	}
	if d, ok := verdictRefusal(f); !ok {
		return d
	}
	switch {
	case f.Checks != checkSuccess:
		return refuse(ReasonChecksNotGreen, fmt.Sprintf("the checks main requires are %s on %s, not passed",
			cmp.Or(f.Checks, "not reported"), short(f.Head)))
	}
	if touched := ProtectedTouched(f.Files, f.Protected); len(touched) > 0 {
		return refuse(ReasonProtectedPaths, "it changes protected paths: "+strings.Join(touched, ", ")+
			"; a Standard change goes through its own review")
	}
	return PersonMergeDecision{Merge: true, Says: "approved on its head with green checks"}
}

// verdictRefusal is the refusal of f's verdict, ok when an approving one
// counts.
func verdictRefusal(f PersonMergeFacts) (PersonMergeDecision, bool) {
	runs := runsOf(f.Comments, f.Rules)
	author := -1
	var holder Claim
	for i, r := range runs {
		if r.claim.Offer.Attrs[markerKeyRole] == RoleImplementer && r.done != nil && f.Head != "" &&
			doneCounts(*r.done, f.Head) && r.done.Attrs[markerKeyCommit] != "" {
			author, holder = i, r.claim
		}
	}
	approved := func(r runReplay) bool {
		return r.claim.Offer.Attrs[markerKeyRole] == RoleReviewer && r.done != nil &&
			r.done.Attrs[markerKeyVerdict] == VerdictApprove
	}
	own := false
	for _, r := range runs[author+1:] {
		if !approved(r) {
			continue
		}
		if author >= 0 && r.claim.Names(holder.Account, holder.Session) {
			own = true
			continue
		}
		if author >= 0 {
			return PersonMergeDecision{}, true
		}
	}
	switch {
	case own:
		return PersonMergeDecision{Reason: ReasonAuthorsVerdict, Says: fmt.Sprintf("the only approval is of "+
			"session %s, which wrote %s: a verdict is another session's", holder.Session, short(f.Head))}, false
	case author < 0 && slices.ContainsFunc(runs, approved):
		return PersonMergeDecision{Reason: ReasonVerdictOlderHead, Says: fmt.Sprintf("the approval is of an "+
			"earlier round, not of the head %s", short(f.Head))}, false
	}
	return PersonMergeDecision{Reason: ReasonNoVerdict, Says: fmt.Sprintf("no counted approving verdict on %s",
		short(f.Head))}, false
}

// runsOf is each counted offer's run among comments, in the host's order:
// the replay of its offer up to the next counted one.
func runsOf(comments []HostComment, rules ClaimRules) []runReplay {
	ordered := slices.Clone(comments)
	slices.SortStableFunc(ordered, func(a, b HostComment) int {
		return cmp.Or(a.Created.Compare(b.Created), cmp.Compare(a.ID, b.ID))
	})
	var starts []int
	for i, c := range ordered {
		if m, ok := ParseMarker(c); ok && m.Kind == MarkerOffer && rules.engineCounts(c.Author) {
			starts = append(starts, i)
		}
	}
	runs := make([]runReplay, 0, len(starts))
	for j, s := range starts {
		end := len(ordered)
		if j+1 < len(starts) {
			end = starts[j+1]
		}
		runs = append(runs, replayMarkers(ordered[s:end], rules))
	}
	return runs
}

// ProtectedTouched are the files that protected names: a path itself, a
// folder's (ending in "/") files, or a glob's matches (*, any run of
// characters).
func ProtectedTouched(files, protected []string) []string {
	var out []string
	for _, f := range files {
		if slices.ContainsFunc(protected, func(p string) bool {
			return f == p || strings.HasSuffix(p, "/") && strings.HasPrefix(f, p) || matchGlob(p, f)
		}) {
			out = append(out, f)
		}
	}
	return out
}
