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

// #97: a Standard whose files differ arrives as a pull request a person
// merges or closes.
func TestNextAdoptionStep(t *testing.T) {
	const url = "http://git.example/webshop/orders-api/pulls/1"
	v1 := StandardVersion{Ref: "v1", Checks: ChecksEveryChange}
	v2 := StandardVersion{Ref: "v2", Checks: ChecksEveryChange}
	open := func() *StandardAdoptionInFlight {
		return &StandardAdoptionInFlight{Version: StandardVersion{Ref: "v1"}, PullRequestURL: url, Base: "m1"}
	}
	closed := func() *StandardAdoptionInFlight {
		a := open()
		a.Stopped = true
		return a
	}
	differs := func() *StandardAdoptionInFlight {
		a := closed()
		a.Merged = true
		return a
	}
	none := &AdoptionDiff{}
	guideOnly := &AdoptionDiff{Paths: []string{"AGENTS.md"}}
	workflow := &AdoptionDiff{Paths: []string{"ci/check.yaml", "AGENTS.md", "Makefile"},
		Protected: []string{"ci/check.yaml", "Makefile"}}

	tests := []struct {
		name    string
		facts   StandardAdoptionFacts
		want    AdoptionAction
		version StandardVersion
		message string
	}{
		{name: "no adoption yet: compare main with the Standard at its ref",
			facts: StandardAdoptionFacts{}, want: AdoptionCompare, version: v1},
		{name: "main holds every file: record the Standard, no pull request",
			facts: StandardAdoptionFacts{Diff: none}, want: AdoptionRecord, version: v1},
		{name: "a file differs: open the pull request",
			facts: StandardAdoptionFacts{Diff: workflow}, want: AdoptionOpen, version: v1},
		{name: "only a file the Standard doesn't protect differs: still a pull request, at adoption",
			facts: StandardAdoptionFacts{Diff: guideOnly}, want: AdoptionOpen, version: v1},

		{name: "a pull request in flight is read",
			facts: StandardAdoptionFacts{Adoption: open()}, want: AdoptionReadPR},
		{name: "an open pull request waits on a person",
			facts: StandardAdoptionFacts{Adoption: open(), PR: &UpdatePullRequest{}}, want: AdoptionWait,
			message: "Pull request " + url + " brings the files of python-strict at v1 into the repository; a person " +
				"merges it, or closes it to leave the repository without them. No agent works on the repository " +
				"until it merges"},
		{name: "an open pull request stays at its version when the Standard's ref moves",
			facts: StandardAdoptionFacts{Target: v2, Adoption: open(), PR: &UpdatePullRequest{}}, want: AdoptionWait},
		{name: "a merged pull request is compared with main at its own version",
			facts: StandardAdoptionFacts{Target: v2, Adoption: open(), PR: &UpdatePullRequest{Merged: true, Closed: true}},
			want:  AdoptionCompare, version: v1},
		{name: "merged, and main holds the files: record at the pull request's version",
			facts: StandardAdoptionFacts{Target: v2, Adoption: open(), PR: &UpdatePullRequest{Merged: true, Closed: true},
				Diff: none},
			want: AdoptionRecord, version: v1},
		{name: "merged with a file the Standard doesn't protect edited: record",
			facts: StandardAdoptionFacts{Adoption: open(), PR: &UpdatePullRequest{Merged: true, Closed: true},
				Diff: guideOnly},
			want: AdoptionRecord, version: v1},
		{name: "merged with a protected file differing: StandardFilesDiffer",
			facts: StandardAdoptionFacts{Adoption: open(), PR: &UpdatePullRequest{Merged: true, Closed: true},
				Diff: workflow},
			want: AdoptionStopDiffer,
			message: "Pull request " + url + " merged, but main holds ci/check.yaml, Makefile with other " +
				"content than python-strict renders at v1, and python-strict protects them. No agent works on the " +
				"repository until main holds them as rendered; a change of the Standard's ref or of the Container's " +
				"checks opens a new pull request"},
		{name: "a closed pull request is compared with main",
			facts: StandardAdoptionFacts{Adoption: open(), PR: &UpdatePullRequest{Closed: true}},
			want:  AdoptionCompare, version: v1},
		{name: "closed, and main is without its files: the repository stays without the Standard",
			facts: StandardAdoptionFacts{Adoption: open(), PR: &UpdatePullRequest{Closed: true}, Diff: workflow},
			want:  AdoptionStopClosed,
			message: "Pull request " + url + " was closed without merging, so the repository is without " +
				"ci/check.yaml, AGENTS.md, Makefile as python-strict renders them at v1. No agent works on the " +
				"repository until main holds them; a change of the Standard's ref or of the Container's checks " +
				"opens a new pull request"},
		{name: "closed, and only a file the Standard doesn't protect differs: still closed, a person took none",
			facts: StandardAdoptionFacts{Adoption: open(), PR: &UpdatePullRequest{Closed: true}, Diff: guideOnly},
			want:  AdoptionStopClosed},

		{name: "a stopped adoption reads main's head",
			facts: StandardAdoptionFacts{Adoption: closed()}, want: AdoptionReadMain},
		{name: "a stopped adoption waits while main is as it was compared",
			facts: StandardAdoptionFacts{Adoption: closed(), Main: "m1"}, want: AdoptionNone},
		{name: "a stopped adoption is compared again once main moved",
			facts: StandardAdoptionFacts{Adoption: closed(), Main: "m2"}, want: AdoptionCompare, version: v1},
		{name: "closed, then a person brought every file by hand: record",
			facts: StandardAdoptionFacts{Adoption: closed(), Main: "m2", Diff: none},
			want:  AdoptionRecord, version: v1},
		{name: "closed, and a person brought only the protected files: still closed",
			facts: StandardAdoptionFacts{Adoption: closed(), Main: "m2", Diff: guideOnly}, want: AdoptionStopClosed},
		{name: "merged and differing, then a person fixed the protected files: record",
			facts: StandardAdoptionFacts{Adoption: differs(), Main: "m2", Diff: guideOnly},
			want:  AdoptionRecord, version: v1},
		{name: "closed, and main moved without them: still closed",
			facts: StandardAdoptionFacts{Adoption: closed(), Main: "m2", Diff: workflow}, want: AdoptionStopClosed},
		{name: "merged and differing, then main fixed: record",
			facts: StandardAdoptionFacts{Adoption: differs(), Main: "m2", Diff: none}, want: AdoptionRecord, version: v1},
		{name: "merged and differing, and main moved without a fix: still StandardFilesDiffer",
			facts: StandardAdoptionFacts{Adoption: differs(), Main: "m2", Diff: workflow}, want: AdoptionStopDiffer},
		{name: "a stopped adoption is forgotten when the Standard's ref moves",
			facts: StandardAdoptionFacts{Target: v2, Adoption: closed(), Main: "m1"}, want: AdoptionForget},
		{name: "a stopped adoption is forgotten when the Container's checks change",
			facts: StandardAdoptionFacts{Target: StandardVersion{Ref: "v1", Checks: ChecksBeforeRelease},
				Adoption: differs()},
			want: AdoptionForget},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.facts
			f.Name = "python-strict"
			if f.Target.Ref == "" {
				f.Target = StandardVersion{Ref: "v1"}
			}
			got := NextAdoptionStep(f)
			if got.Action != tt.want {
				t.Fatalf("NextAdoptionStep() = %s (%q), want %s", got.Action, got.Message, tt.want)
			}
			if tt.version != (StandardVersion{}) && got.Version != tt.version {
				t.Errorf("version = %+v, want %+v", got.Version, tt.version)
			}
			if tt.message != "" && got.Message != tt.message {
				t.Errorf("message =\n%s\nwant\n%s", got.Message, tt.message)
			}
		})
	}
}

func TestClosestRender(t *testing.T) {
	tests := []struct {
		name   string
		differ []int
		want   int
	}{
		{name: "only the render with no Domains", differ: []int{4}, want: 0},
		{name: "a repository with none of the files gets them without Domains", differ: []int{4, 4, 4}, want: 2},
		{name: "the recorded Domains render closer", differ: []int{1, 2, 2}, want: 0},
		{name: "the system's Domains render closer", differ: []int{2, 1, 2}, want: 1},
		{name: "two sets as close as each other: the earlier", differ: []int{1, 1, 2}, want: 0},
		{name: "no Domains renders closest", differ: []int{3, 2, 1}, want: 2},
		{name: "a set as close as none loses to none", differ: []int{2, 1, 1}, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClosestRender(tt.differ); got != tt.want {
				t.Errorf("ClosestRender(%v) = %d, want %d", tt.differ, got, tt.want)
			}
		})
	}
}

func TestStandardAdoptionPullRequest(t *testing.T) {
	v := StandardVersion{Ref: "release/1.0", Checks: ChecksBeforeRelease}
	if got, want := StandardAdoptionBranch("python-strict", v), "standard-adopt/python-strict-release-1.0-before-release"; got != want {
		t.Errorf("StandardAdoptionBranch() = %q, want %q", got, want)
	}
	if got, want := StandardAdoptionBranch("python-strict", StandardVersion{Ref: "v1.0.0"}), "standard-adopt/python-strict-v1.0.0"; got != want {
		t.Errorf("StandardAdoptionBranch() = %q, want %q", got, want)
	}
	if got, want := StandardAdoptionTitle("python-strict", StandardVersion{Ref: "v1.0.0"}), "Bring in the Standard python-strict at v1.0.0"; got != want {
		t.Errorf("StandardAdoptionTitle() = %q, want %q", got, want)
	}
	body := StandardAdoptionBody("python-strict", StandardVersion{Ref: "v1.0.0"}, AdoptionDiff{
		Paths: []string{"ci/check.yaml", "AGENTS.md"}, Protected: []string{"ci/check.yaml"}})
	for _, want := range []string{
		"adopted with the Standard `python-strict`",
		"renders them at `v1.0.0`",
		"- `ci/check.yaml` (protected)\n",
		"- `AGENTS.md`\n",
		"The operator never merges this pull request.",
		"**Merge it**", "**Close it**",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("StandardAdoptionBody() lacks %q:\n%s", want, body)
		}
	}
}
