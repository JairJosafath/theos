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
	"strings"
	"testing"
)

// The rules of docs/plans/approval-rules.md's example (#62): two folders and
// a label wait for a person, as does a big change, and docs merge on CI.
var approvalExample = []ApprovalRule{
	{Paths: []string{"migrations/", "internal/auth/"}, Require: ApprovalPerson},
	{Labels: []string{"risk/high"}, Require: ApprovalPerson},
	{ChangedLinesOver: 400, Require: ApprovalPerson},
	{Labels: []string{"kind/docs"}, Require: ApprovalNone},
}

func TestMatchesPath(t *testing.T) {
	tests := []struct {
		pattern, file string
		want          bool
	}{
		{"Makefile", "Makefile", true},
		{"Makefile", "docs/Makefile", false},
		{"migrations/", "migrations/001.sql", true},
		{"migrations/", "migrations/2026/001.sql", true},
		{"migrations/", "db/migrations/001.sql", false},
		{"migrations", "migrations/001.sql", false},
		{"*.md", "README.md", true},
		{"*.md", "docs/guide/install.md", true},
		{"*.md", "docs/guide/install.mdx", false},
		{"docs/*", "docs/guide/install.md", true},
		{"docs/*", "src/docs/x", false},
		{"internal/*/auth.go", "internal/api/auth.go", true},
		{"internal/*/auth.go", "internal/api/auth_test.go", false},
	}
	for _, tt := range tests {
		if got := MatchesPath(tt.pattern, tt.file); got != tt.want {
			t.Errorf("MatchesPath(%q, %q) = %v, want %v", tt.pattern, tt.file, got, tt.want)
		}
	}
}

func TestApprovalFor(t *testing.T) {
	startSmall := []ApprovalRule{
		{Paths: []string{"docs/", "*.md"}, Require: ApprovalNone},
		{Require: ApprovalPerson},
	}
	tests := []struct {
		name    string
		rules   []ApprovalRule
		change  ApprovalChange
		require string
		rule    int
		why     string
	}{
		{"no rules ask for nothing", nil, ApprovalChange{Files: []string{"app.go"}}, "", 0, ""},
		{"a file no rule matches needs the reviewer", approvalExample,
			ApprovalChange{Files: []string{"app.go"}, Lines: 20}, ApprovalReviewer, 0, "no approval rule matches app.go"},
		{"a file under a Person path waits for a person", approvalExample,
			ApprovalChange{Files: []string{"app.go", "migrations/001.sql"}, Lines: 20}, ApprovalPerson, 1,
			"approval rule 1 (paths migrations/, internal/auth/) asks for a person for migrations/001.sql"},
		{"a label waits for a person, whatever the files", approvalExample,
			ApprovalChange{Files: []string{"app.go"}, Lines: 20, Labels: []string{"kind/feature", "risk/high"}},
			ApprovalPerson, 2, "approval rule 2 (labels risk/high) asks for a person"},
		{"a change over the size waits for a person", approvalExample,
			ApprovalChange{Files: []string{"app.go"}, Lines: 401}, ApprovalPerson, 3,
			"approval rule 3 (more than 400 changed lines) asks for a person"},
		{"a change at the size doesn't", approvalExample,
			ApprovalChange{Files: []string{"app.go"}, Lines: 400}, ApprovalReviewer, 0, ""},
		{"a labelled docs change merges on CI", approvalExample,
			ApprovalChange{Files: []string{"docs/a.md", "README.md"}, Lines: 20, Labels: []string{"kind/docs"}},
			ApprovalNone, 4, "approval rule 4 (labels kind/docs) asks for no review"},
		{"a docs label doesn't loosen a Person path", approvalExample,
			ApprovalChange{Files: []string{"docs/a.md", "internal/auth/token.go"}, Lines: 20,
				Labels: []string{"kind/docs"}}, ApprovalPerson, 1, "for internal/auth/token.go"},
		{"a docs label doesn't loosen a big change", approvalExample,
			ApprovalChange{Files: []string{"docs/a.md"}, Lines: 900, Labels: []string{"kind/docs"}},
			ApprovalPerson, 3, ""},
		{"start small: docs alone merge on CI", startSmall,
			ApprovalChange{Files: []string{"docs/a.md", "CHANGELOG.md"}}, ApprovalNone, 1,
			"approval rule 1 (paths docs/, *.md) asks for no review for docs/a.md"},
		{"start small: docs with code wait for a person", startSmall,
			ApprovalChange{Files: []string{"docs/a.md", "app.go"}}, ApprovalPerson, 2,
			"approval rule 2 (every change) asks for a person"},
		{"the first matching rule decides a file", []ApprovalRule{
			{Paths: []string{"docs/"}, Require: ApprovalReviewer},
			{Paths: []string{"docs/"}, Require: ApprovalPerson},
		}, ApprovalChange{Files: []string{"docs/a.md"}}, ApprovalReviewer, 1,
			"approval rule 1 (paths docs/) asks for the reviewer for docs/a.md"},
		{"the strictest file decides, not the first", []ApprovalRule{
			{Paths: []string{"docs/"}, Require: ApprovalNone},
			{Paths: []string{"db/"}, Require: ApprovalPerson},
		}, ApprovalChange{Files: []string{"docs/a.md", "app.go", "db/schema.sql"}}, ApprovalPerson, 2, "db/schema.sql"},
		{"None with a file no rule matches is the reviewer's", []ApprovalRule{
			{Paths: []string{"docs/"}, Require: ApprovalNone},
		}, ApprovalChange{Files: []string{"docs/a.md", "app.go"}}, ApprovalReviewer, 0, "no approval rule matches app.go"},
		{"more files than the host lists is never None", startSmall[:1],
			ApprovalChange{Files: []string{"docs/a.md"}, TooManyFiles: true}, ApprovalReviewer, 0,
			"more files than the Git host lists"},
		{"more files than the host lists waits when a rule asks a person", startSmall,
			ApprovalChange{Files: []string{"docs/a.md"}, TooManyFiles: true}, ApprovalPerson, 2,
			"more files than the Git host lists"},
		{"a Person rule that isn't for the change doesn't hold unlisted files", approvalExample[1:2],
			ApprovalChange{TooManyFiles: true, Labels: []string{"kind/bug"}}, ApprovalReviewer, 0, ""},
		{"no file listed is never None", startSmall[:1], ApprovalChange{}, ApprovalReviewer, 0,
			"lists no changed file"},
		{"a long rule is told short", []ApprovalRule{
			{Paths: []string{"a/", "b/", "c/", "d/", "e/"}, Require: ApprovalPerson},
		}, ApprovalChange{Files: []string{"e/x"}}, ApprovalPerson, 1, "(paths a/, b/, c/ and 2 more)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApprovalFor(tt.rules, tt.change)
			if got.Require != tt.require || got.Rule != tt.rule || !strings.Contains(got.Why, tt.why) {
				t.Errorf("ApprovalFor() = %+v, want %s by rule %d with %q", got, tt.require, tt.rule, tt.why)
			}
		})
	}
}

func TestApprovalRulesKey(t *testing.T) {
	if key := ApprovalRulesKey(nil); key != "" {
		t.Errorf("ApprovalRulesKey(nil) = %q, want none", key)
	}
	key := ApprovalRulesKey(approvalExample)
	if len(key) != 16 || key != ApprovalRulesKey(append([]ApprovalRule{}, approvalExample...)) {
		t.Errorf("ApprovalRulesKey() = %q, want 16 characters, the same for the same rules", key)
	}
	changed := append([]ApprovalRule{}, approvalExample...)
	changed[2].ChangedLinesOver = 200
	reordered := []ApprovalRule{approvalExample[1], approvalExample[0], approvalExample[2], approvalExample[3]}
	// A path that would join another's text must not read as the same rules.
	joined := [][]ApprovalRule{
		{{Paths: []string{"a", "b"}, Require: ApprovalPerson}},
		{{Paths: []string{"a b"}, Require: ApprovalPerson}},
	}
	for name, other := range map[string]string{
		"a changed bound": ApprovalRulesKey(changed),
		"another order":   ApprovalRulesKey(reordered),
	} {
		if other == key {
			t.Errorf("%s has the key of the rules it came from", name)
		}
	}
	if ApprovalRulesKey(joined[0]) == ApprovalRulesKey(joined[1]) {
		t.Error("two paths and one with a space in it have the same key")
	}
}

func TestApprovalNote(t *testing.T) {
	person := ApprovalNeed{Require: ApprovalPerson, Why: "approval rule 1 (paths db/) asks for a person for db/x"}
	none := ApprovalNeed{Require: ApprovalNone, Why: "approval rule 2 (labels kind/docs) asks for no review"}
	tests := []struct {
		name       string
		need       ApprovalNeed
		by         string
		inChat     bool
		reviewed   bool
		want       string
		wantNoNote bool
	}{
		{"a person's approval is named", person, "ada", false, true,
			"ada approved it, as the Crew's approval rules ask: approval rule 1 (paths db/) asks for a person for db/x.",
			false},
		{"from chat, said so", person, "ada@example.com", true, true, "ada@example.com approved it in chat, as", false},
		{"a person merged it by hand: nobody to name", person, "", false, true, "", true},
		{"no review is said", none, "", false, false,
			"No agent reviewed it, as the Crew's approval rules ask: approval rule 2 (labels kind/docs) asks for no " +
				"review.", false},
		{"a review that was made anyway is not denied", none, "", false, true, "", true},
		{"the reviewer's approval needs no note", ApprovalNeed{Require: ApprovalReviewer}, "", false, true, "", true},
		{"nor a Crew without rules", ApprovalNeed{}, "", false, true, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApprovalNote(tt.need, tt.by, tt.inChat, tt.reviewed)
			if tt.wantNoNote != (got == "") || !strings.Contains(got, tt.want) {
				t.Errorf("ApprovalNote() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A change's steps under its Crew's approval rules (#62): the wait comes
// after the agents, and None skips them.
func TestApprovalSteps(t *testing.T) {
	done := func(result *RunResult) RunState { return RunState{Started: true, Done: true, Result: result} }
	approved := done(&RunResult{Verdict: VerdictApprove, Summary: "Good."})
	person := ApprovalNeed{Require: ApprovalPerson, Rule: 1, Why: "approval rule 1 (paths db/) asks for a person for db/x"}
	none := ApprovalNeed{Require: ApprovalNone, Rule: 2, Why: "approval rule 2 (labels kind/docs) asks for no review"}
	change := func(f TaskFacts) TaskFacts {
		f.Protected, f.ContainerReady, f.Container = true, true, "orders-api"
		if f.MaxRounds == 0 {
			f.MaxRounds = 3
		}
		if f.Round == 0 {
			f.Round = 1
		}
		return f
	}
	awaiting := func(need ApprovalNeed, pull WaitingPullFacts) TaskFacts {
		return change(TaskFacts{Phase: TaskAwaitingApproval, Approval: need, WaitingPull: pull})
	}
	tests := []struct {
		name       string
		facts      TaskFacts
		want       TaskAction
		message    string
		reason     string
		unreviewed bool
	}{
		{"no rules: the reviewer's approval merges", change(TaskFacts{Phase: TaskReviewing, Run: approved}),
			ActionMerge, "Approved; merging", "", false},
		{"Reviewer: the reviewer's approval merges", change(TaskFacts{Phase: TaskReviewing, Run: approved,
			Approval: ApprovalNeed{Require: ApprovalReviewer}}), ActionMerge, "Approved; merging", "", false},
		{"Person: an approved change waits, with why", change(TaskFacts{Phase: TaskReviewing, Run: approved,
			Approval: person}), ActionAwaitApproval, "The reviewer approved; approval rule 1 (paths db/) asks for a " +
			"person for db/x, so the change waits for a person to approve its pull request", "", false},
		{"Person: the reviewer's changes still start the next round", change(TaskFacts{Phase: TaskReviewing,
			Run: done(&RunResult{Verdict: VerdictChanges, Summary: "No."}), Approval: person}), ActionRetry, "", "",
			false},
		{"Person, with a verifier: the verifier runs first", change(TaskFacts{Phase: TaskReviewing, Run: approved,
			Approval: person, Verify: true}), ActionVerify, "", "", false},
		{"Person: a verified change waits", change(TaskFacts{Phase: TaskVerifying, Run: approved, Approval: person,
			Verify: true}), ActionAwaitApproval, "The verifier approved; approval rule 1", "", false},
		{"no rules: a verified change merges", change(TaskFacts{Phase: TaskVerifying, Run: approved, Verify: true}),
			ActionMerge, "Verified; merging", "", false},
		{"Person: a Standard change is never held", change(TaskFacts{Phase: TaskReviewing, Run: approved,
			Approval: person, AllowedPaths: []string{"Makefile"}}), ActionMerge, "Approved; merging", "", false},
		{"None: passed checks merge it, no review", change(TaskFacts{Phase: TaskChecking, Checks: checkSuccess,
			Approval: none}), ActionMerge, "Checks passed; approval rule 2 (labels kind/docs) asks for no review, so " +
			"it merges with no review", "", true},
		{"None: main moved, so it takes main in first", change(TaskFacts{Phase: TaskChecking, Checks: checkSuccess,
			Approval: none, Behind: true}), ActionUpdateBranch,
			"Checks passed and no review is asked for, but main moved", "", true},
		{"None: a Standard change is still reviewed", change(TaskFacts{Phase: TaskChecking, Checks: checkSuccess,
			Approval: none, AllowedPaths: []string{"Makefile"}}), ActionStartReview, "", "", false},
		{"None: failed checks still start the next round", change(TaskFacts{Phase: TaskChecking,
			Checks: checkFailure, Approval: none}), ActionRetry, "", "", false},
		{"Reviewer: passed checks start the review", change(TaskFacts{Phase: TaskChecking, Checks: checkSuccess,
			Approval: ApprovalNeed{Require: ApprovalReviewer}}), ActionStartReview, "", "", false},
		{"no merge before a person's approval", awaiting(person, WaitingPullFacts{}), ActionWait,
			"The change waits for a person to approve its pull request: approval rule 1", ReasonAwaitingApproval,
			false},
		{"a person's approval merges it", awaiting(person, WaitingPullFacts{People: PersonVerdict{ApprovedBy: "ada"}}),
			ActionMerge, "ada approved the change; merging", "", false},
		{"an approval from chat merges it, said so", awaiting(person, WaitingPullFacts{
			People: PersonVerdict{ApprovedBy: "ada@example.com", ApprovedInChat: true}}), ActionMerge,
			"ada@example.com approved the change in chat; merging", "", false},
		{"approved, but main moved", func() TaskFacts {
			f := awaiting(person, WaitingPullFacts{People: PersonVerdict{ApprovedBy: "ada"}})
			f.Behind = true
			return f
		}(), ActionUpdateBranch, "Approved, but main moved", "", false},
		{"a person's changes start the next round", awaiting(person, WaitingPullFacts{
			People: PersonVerdict{ChangesBy: "ada", Changes: "Keep the old column."}}), ActionRetry, "", "", false},
		{"a person merged it", awaiting(person, WaitingPullFacts{Merged: true}), ActionMerge,
			"A person merged the pull request", "", false},
		{"a person closed it: the merge step ends the Task", awaiting(person, WaitingPullFacts{Closed: true}),
			ActionMerge, "A person closed the pull request", "", false},
		{"its branch moved: checked again", awaiting(person, WaitingPullFacts{HeadMoved: true,
			People: PersonVerdict{ApprovedBy: "ada"}}), ActionMerge, "The change's branch moved after the review", "",
			false},
		{"rules that no longer ask a person let it go", awaiting(ApprovalNeed{Require: ApprovalReviewer},
			WaitingPullFacts{}), ActionMerge, "no longer ask for a person", "", false},
		{"rules taken away let it go", awaiting(ApprovalNeed{}, WaitingPullFacts{}), ActionMerge,
			"no longer ask for a person", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextTaskStep(tt.facts)
			if got.Action != tt.want || !strings.Contains(got.Message, tt.message) || got.Reason != tt.reason ||
				got.Unreviewed != tt.unreviewed {
				t.Errorf("NextTaskStep() = %+v, want %s with %q, reason %q, unreviewed %v", got, tt.want, tt.message,
					tt.reason, tt.unreviewed)
			}
		})
	}
	changes := NextTaskStep(awaiting(person, WaitingPullFacts{People: PersonVerdict{ChangesBy: "ada",
		Changes: "Keep the old column."}}))
	if !strings.Contains(changes.Feedback, "ada asked for changes on its pull request:\nKeep the old column.") {
		t.Errorf("feedback = %q, want the person's review", changes.Feedback)
	}
	wait := ChangeWaitMessage(7, "http://git/pulls/9", person.Why)
	for _, want := range []string{"The change for issue #7", "because approval rule 1 (paths db/)", "http://git/pulls/9"} {
		if !strings.Contains(wait, want) {
			t.Errorf("ChangeWaitMessage() = %q, want %q in it", wait, want)
		}
	}
}

// #92: the reviewer may raise what a change needs, never lower it.
func TestRaisedByReviewer(t *testing.T) {
	reviewer := ApprovalNeed{Require: ApprovalReviewer, Why: "no approval rule matches app.py"}
	person := ApprovalNeed{Require: ApprovalPerson, Rule: 1, Why: "approval rule 1 (paths migrations/) asks for a person"}
	none := ApprovalNeed{Require: ApprovalNone, Rule: 2, Why: "approval rule 2 (paths docs/) asks for no review"}
	tests := []struct {
		name string
		need ApprovalNeed
		risk string
		want ApprovalNeed
	}{
		{"a change left to the reviewer waits for a person", reviewer, "It drops a column.", ApprovalNeed{
			Require: ApprovalPerson, Raised: true, Why: `the reviewer called the change high-risk ("It drops a column")`}},
		{"also by a rule that asks for the reviewer", ApprovalNeed{Require: ApprovalReviewer, Rule: 3, Why: "rule 3"},
			" It moves money ", ApprovalNeed{Require: ApprovalPerson, Raised: true,
				Why: `the reviewer called the change high-risk ("It moves money")`}},
		{"a reviewer that says nothing raises nothing", reviewer, "", reviewer},
		{"nor one that says only spaces", reviewer, "  ", reviewer},
		{"a change that needs a person keeps its rule", person, "It is a migration.", person},
		{"a change the rules ask no review of has no reviewer to raise it", none, "It rewrites the docs.", none},
		{"a Crew without rules has no person in the loop", ApprovalNeed{}, "It drops a column.", ApprovalNeed{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, raised := RaisedByReviewer(tt.need, tt.risk)
			if got != tt.want || raised != got.Raised {
				t.Errorf("RaisedByReviewer() = %+v, %v; want %+v", got, raised, tt.want)
			}
		})
	}
	// What the reviewer raised waits for a person as a rule's answer does,
	// and the merge's note says whose word it was.
	raised, _ := RaisedByReviewer(reviewer, "It drops a column.")
	if !WaitsForPerson(TaskFacts{Approval: raised}) {
		t.Error("a raised change does not wait for a person")
	}
	if got, want := ApprovalNote(raised, "ada", true, true),
		`ada approved it in chat, because the reviewer called the change high-risk ("It drops a column").`; got != want {
		t.Errorf("ApprovalNote() = %q, want %q", got, want)
	}
}
