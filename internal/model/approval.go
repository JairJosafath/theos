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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

// A Crew's approval rules (docs/design.md, "Approval rules"; #62) say which
// of its changes wait for a person and which merge on CI alone. Each changed
// file takes the first rule that matches it, and the change needs the
// strictest of what its files need: a rule can only loosen the files it
// names.

// What a change needs before it merges, as Crew.spec.approval names it.
const (
	// ApprovalNone merges on CI alone: no reviewer and no verifier runs.
	ApprovalNone = "None"
	// ApprovalReviewer merges on the reviewer's approval, and the verifier's
	// where the Task has one: what a Crew without rules does.
	ApprovalReviewer = "Reviewer"
	// ApprovalPerson waits, after those, for a person to approve the pull
	// request.
	ApprovalPerson = "Person"
)

// ApprovalRule is one rule of a Crew's spec.approval: the changes it is for
// (an issue with one of Labels, a pull request that adds and deletes more
// than ChangedLinesOver lines; either unset is any), the files of those it
// matches (under one of Paths; none is every file) and what it asks of them.
type ApprovalRule struct {
	Paths            []string
	Labels           []string
	ChangedLinesOver int
	Require          string
}

// ApprovalChange is the change a Crew's rules are asked about: its pull
// request's changed files (TooManyFiles when the Git host lists only some),
// the lines it adds and deletes, and its issue's labels.
type ApprovalChange struct {
	Files        []string
	TooManyFiles bool
	Lines        int
	Labels       []string
}

// ApprovalNeed is what a Crew's rules ask of a change: Require, the rule
// that decided (from 1; 0 when no rule matched the deciding file) and Why,
// which reads after "because" or before ", so".
type ApprovalNeed struct {
	Require string
	Rule    int
	Why     string
}

// approvalRank orders what a change may need, loosest first; an unknown word
// ranks as the reviewer, which CEL keeps out anyway.
func approvalRank(require string) int {
	switch require {
	case ApprovalNone:
		return 0
	case ApprovalPerson:
		return 2
	}
	return 1
}

// MatchesPath reports whether file is under pattern: the file itself, a
// folder ending in "/" with everything in it, as a Standard's protected
// paths are, or a pattern in which * stands for any run of characters,
// slashes included ("*.md", "docs/*").
func MatchesPath(pattern, file string) bool {
	return covers(pattern, file) || matchGlob(pattern, file)
}

// isFor reports whether the rule is for the change at all: its issue has
// one of the rule's labels, and it changes more lines than the rule's bound.
func (r ApprovalRule) isFor(c ApprovalChange) bool {
	if len(r.Labels) > 0 && !slices.ContainsFunc(r.Labels, func(l string) bool { return slices.Contains(c.Labels, l) }) {
		return false
	}
	return r.ChangedLinesOver == 0 || c.Lines > r.ChangedLinesOver
}

// matches reports whether the rule matches file: under one of its paths, or
// any file when it names none.
func (r ApprovalRule) matches(file string) bool {
	return len(r.Paths) == 0 || slices.ContainsFunc(r.Paths, func(p string) bool { return MatchesPath(p, file) })
}

// describe says what the rule is for, for a message: "(paths migrations/,
// internal/auth/)", "(labels risk/high; more than 400 changed lines)",
// "(every change)".
func (r ApprovalRule) describe() string {
	var parts []string
	if len(r.Paths) > 0 {
		parts = append(parts, "paths "+someOf(r.Paths))
	}
	if len(r.Labels) > 0 {
		parts = append(parts, "labels "+someOf(r.Labels))
	}
	if r.ChangedLinesOver > 0 {
		parts = append(parts, fmt.Sprintf("more than %d changed lines", r.ChangedLinesOver))
	}
	if len(parts) == 0 {
		return "(every change)"
	}
	return "(" + strings.Join(parts, "; ") + ")"
}

// someOf lists up to three of items, and says how many more there are.
func someOf(items []string) string {
	const shown = 3
	if len(items) <= shown {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(items[:shown], ", "), len(items)-shown)
}

// asks is the rule's requirement in a sentence.
func asks(require string) string {
	switch require {
	case ApprovalNone:
		return "asks for no review"
	case ApprovalPerson:
		return "asks for a person"
	}
	return "asks for the reviewer"
}

// ApprovalFor is what rules ask of change c. The rules that are for the
// change (its labels, its size) are tried in order on each changed file: the
// first that matches the file says what the file needs, and a file none
// matches needs the reviewer. The change needs the strictest of that. With
// no rules it needs nothing said: the zero ApprovalNeed. A pull request with
// more files than the Git host lists, or none listed, is never ApprovalNone:
// it needs a person when a rule for the change asks for one, else the
// reviewer.
func ApprovalFor(rules []ApprovalRule, c ApprovalChange) ApprovalNeed {
	if len(rules) == 0 {
		return ApprovalNeed{}
	}
	if c.TooManyFiles || len(c.Files) == 0 {
		return approvalUnlisted(rules, c)
	}
	need := ApprovalNeed{Require: ApprovalNone, Rule: -1}
	for _, file := range c.Files {
		got := ApprovalNeed{Require: ApprovalReviewer, Why: "no approval rule matches " + file}
		for i, rule := range rules {
			if !rule.isFor(c) || !rule.matches(file) {
				continue
			}
			got = ApprovalNeed{Require: rule.Require, Rule: i + 1,
				Why: fmt.Sprintf("approval rule %d %s %s", i+1, rule.describe(), asks(rule.Require))}
			if len(rule.Paths) > 0 {
				got.Why += " for " + file
			}
			break
		}
		if need.Rule < 0 || approvalRank(got.Require) > approvalRank(need.Require) {
			need = got
		}
	}
	return need
}

// approvalUnlisted is what rules ask of a change whose files can't all be
// read: any of them may be under any rule's paths.
func approvalUnlisted(rules []ApprovalRule, c ApprovalChange) ApprovalNeed {
	unlisted := "the pull request lists no changed file"
	if c.TooManyFiles {
		unlisted = "the pull request changes more files than the Git host lists"
	}
	for i, rule := range rules {
		if rule.isFor(c) && rule.Require == ApprovalPerson {
			return ApprovalNeed{Require: ApprovalPerson, Rule: i + 1, Why: fmt.Sprintf(
				"approval rule %d %s %s, and %s", i+1, rule.describe(), asks(rule.Require), unlisted)}
		}
	}
	return ApprovalNeed{Require: ApprovalReviewer, Why: unlisted + ", so no approval rule spares it the review"}
}

// ApprovalRulesKey names a list of rules by what they say, so a Task can
// tell that its Crew's rules changed since it asked them; "" for none.
func ApprovalRulesKey(rules []ApprovalRule) string {
	if len(rules) == 0 {
		return ""
	}
	h := sha256.New()
	for _, r := range rules {
		_, _ = fmt.Fprintf(h, "%q %q %d %q\n", r.Paths, r.Labels, r.ChangedLinesOver, r.Require)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// WaitsForPerson reports whether a change its agents approved waits for a
// person: its Crew's rules ask for one, and it is an Implement Task's, not a
// Standard change, whose gated merge needs a review run that still lasts.
func WaitsForPerson(f TaskFacts) bool {
	return f.Approval.Require == ApprovalPerson && approvalRulesApply(f)
}

// MergesUnreviewed reports whether a change merges on its checks alone, with
// no reviewer and no verifier run: its Crew's rules ask for no review.
func MergesUnreviewed(f TaskFacts) bool {
	return f.Approval.Require == ApprovalNone && approvalRulesApply(f)
}

// approvalRulesApply reports whether a Crew's approval rules are for the
// Task: an Implement Task that is no Standard change.
func approvalRulesApply(f TaskFacts) bool {
	return !f.Spec && !f.Plan && !f.Triage && !f.Score && len(f.AllowedPaths) == 0
}

// ApprovalNote says on a merged change's issue who let it in, where its
// Crew's approval rules decided that: the person who approved it (inChat
// when from chat), or nobody when the rules asked for no review and none
// was made (reviewed is whether an agent reviewed the merged round). Empty
// when the reviewer's approval merged it, as without rules.
func ApprovalNote(need ApprovalNeed, approvedBy string, inChat, reviewed bool) string {
	switch {
	case need.Require == ApprovalPerson && approvedBy != "":
		where := ""
		if inChat {
			where = " in chat"
		}
		return fmt.Sprintf("%s approved it%s, as the Crew's approval rules ask: %s.", approvedBy, where,
			orUnknown(need.Why))
	case need.Require == ApprovalNone && !reviewed:
		return fmt.Sprintf("No agent reviewed it, as the Crew's approval rules ask: %s.", orUnknown(need.Why))
	}
	return ""
}

// ChangeWaitMessage is the status of a change waiting for a person to
// approve its pull request: why, what to do, and where.
func ChangeWaitMessage(issue int, pullURL, why string) string {
	of := "The change"
	if issue > 0 {
		of = fmt.Sprintf("The change for issue #%d", issue)
	}
	return fmt.Sprintf("%s passed CI and the factory's review, and waits for a person because %s; approve pull "+
		"request %s on the Git host to merge it, ask for changes there for another round, or close it to drop the "+
		"change", of, orUnknown(why), orUnknown(pullURL))
}
