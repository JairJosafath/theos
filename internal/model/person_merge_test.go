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

// #292: theos merge's table: a person merges an open pull request with a
// counted approving verdict on its head from another session than its
// author's, green checks and no protected path; merge: rules is not built.
func TestDecidePersonMerge292(t *testing.T) {
	m := runMarkers{t}
	const head, older = "4f2a9c1d8e7b6a5f4e3d2c1b0a9f8e7d6c5b4a39", "0123456789abcdef0123456789abcdef01234567"
	owner, stranger := "shop-owner", "drive-by"
	rules := DefaultClaimRules(owner)
	at := func(id int64, author, body string) HostComment { return hostAt(id, author, int(id), body) }
	implement := []HostComment{
		at(1, owner, m.offer(RoleImplementer)),
		at(2, owner, m.session(MarkerClaim, "s1", "")),
		at(3, owner, m.done("s1", head, "", "Added the cart.")),
		at(4, owner, m.offer(RoleReviewer)),
	}
	review := func(author, session, verdict string) []HostComment {
		return []HostComment{at(5, author, m.session(MarkerClaim, session, "")),
			at(6, author, m.done(session, "", verdict, "Reviewed."))}
	}
	with := func(more ...HostComment) []HostComment { return append(append([]HostComment{}, implement...), more...) }
	approved := with(review(owner, "s2", VerdictApprove)...)
	base := PersonMergeFacts{Open: true, Head: head, Rules: rules, Checks: checkSuccess,
		Files: []string{"cart.go"}, Protected: []string{"AGENTS.md", ".github/"}}
	for _, tt := range []struct {
		name   string
		change func(*PersonMergeFacts)
		want   string
	}{
		{"#292 approved by another session on the head, green, no protected path: merges", func(f *PersonMergeFacts) {
			f.Comments = approved
		}, ""},
		{"#292 no verdict", func(f *PersonMergeFacts) { f.Comments = with() }, ReasonNoVerdict},
		{"#292 a verdict of changes is none", func(f *PersonMergeFacts) {
			f.Comments = with(review(owner, "s2", VerdictChanges)...)
		}, ReasonNoVerdict},
		{"#292 the author's own session's verdict", func(f *PersonMergeFacts) {
			f.Comments = with(review(owner, "s1", VerdictApprove)...)
		}, ReasonAuthorsVerdict},
		{"#292 a verdict on an older head", func(f *PersonMergeFacts) {
			f.Comments, f.Head = approved, older
		}, ReasonVerdictOlderHead},
		{"#292 a verdict from outside claimants counts for nothing", func(f *PersonMergeFacts) {
			f.Comments = with(review(stranger, "s9", VerdictApprove)...)
		}, ReasonNoVerdict},
		{"#292 merge: rules is not built", func(f *PersonMergeFacts) {
			f.Comments, f.Setting = approved, MergeRules
		}, ReasonRulesMergeNotBuilt},
		{"#292 merge: person is the default", func(f *PersonMergeFacts) {
			f.Comments, f.Setting = approved, MergePerson
		}, ""},
		{"#292 a closed pull request", func(f *PersonMergeFacts) { f.Comments, f.Open = approved, false },
			ReasonPullNotOpen},
		{"#292 a red check", func(f *PersonMergeFacts) { f.Comments, f.Checks = approved, checkFailure },
			ReasonChecksNotGreen},
		{"#292 checks that haven't reported", func(f *PersonMergeFacts) { f.Comments, f.Checks = approved, "" },
			ReasonChecksNotGreen},
		{"#292 a protected path, a file", func(f *PersonMergeFacts) {
			f.Comments, f.Files = approved, []string{"cart.go", "AGENTS.md"}
		}, ReasonProtectedPaths},
		{"#292 a protected path, in a folder", func(f *PersonMergeFacts) {
			f.Comments, f.Files = approved, []string{".github/workflows/check.yml"}
		}, ReasonProtectedPaths},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := base
			tt.change(&f)
			got := DecidePersonMerge(f)
			if got.Merge != (tt.want == "") || got.Reason != tt.want || got.Says == "" {
				t.Errorf("decision %+v, want reason %q", got, tt.want)
			}
		})
	}
}

// #292: what a refusal names of the protected paths.
func TestProtectedTouched292(t *testing.T) {
	got := ProtectedTouched([]string{"a.go", "AGENTS.md", "docs/x.md", ".claude/agents/r.md"},
		[]string{"AGENTS.md", ".claude/", "*.lock"})
	if strings.Join(got, " ") != "AGENTS.md .claude/agents/r.md" {
		t.Errorf("touched %v", got)
	}
}
