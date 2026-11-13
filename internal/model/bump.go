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
	"time"
)

// LabelBump marks an issue the operator filed to move a Container to a new
// release of a Container it bundles.
const LabelBump = "kind/bump"

// BumpLabel is the label the operator keeps for bump issues.
func BumpLabel() IssueLabel {
	return IssueLabel{LabelBump, "#0e8a16", "Moves to a new release of a Container this one bundles; filed by the operator", false}
}

// Bundler is a Container that bundles the released one, through a Relationship
// with bundles set.
type Bundler struct {
	Container    string
	Relationship string
	Description  string
	// Created is when the Relationship was created.
	Created time.Time
	// Ready says the Container has its repository.
	Ready bool
}

// BundlersToBump returns, by Container name, the bundlers that still need a bump
// issue for a release tagged at released: Ready, linked by then, and not in
// filed. A Container linked twice is bumped once, through its first link by name.
func BundlersToBump(released time.Time, bundlers []Bundler, filed []string) []Bundler {
	sorted := slices.Clone(bundlers)
	slices.SortFunc(sorted, func(a, b Bundler) int {
		if c := strings.Compare(a.Container, b.Container); c != 0 {
			return c
		}
		return strings.Compare(a.Relationship, b.Relationship)
	})
	var out []Bundler
	for _, b := range sorted {
		switch {
		case !b.Ready, b.Created.After(released), slices.Contains(filed, b.Container):
		case len(out) > 0 && out[len(out)-1].Container == b.Container:
		default:
			out = append(out, b)
		}
	}
	return out
}

// ReleaseTakingWork returns the ID of the release due first among milestones,
// the dated vMAJOR.MINOR.PATCH ones, that can still take work: one with no
// prepare issue, open or closed, and not cut under a cadence. Work put in a
// release whose prepare issue is filed would merge after it and hold the
// release as MergedAfterPrepare (#834); a cut release ships what it holds
// (docs/plans/done/release-cadence.md, C4). It returns 0 when there is none.
func ReleaseTakingWork(milestones []ReleaseMilestone) int64 {
	var first *ReleaseMilestone
	for i, m := range milestones {
		if !IsReleaseTitle(m.Title) || m.Due.IsZero() || len(m.Prepared) > 0 || m.Cut {
			continue
		}
		if first == nil || compareReleases(&m.Release, &first.Release) < 0 {
			first = &milestones[i]
		}
	}
	if first == nil {
		return 0
	}
	return first.ID
}

// Bump is a release to bring to a Container that bundles the released one.
type Bump struct {
	// Bundled is the released Container; Tag and Commit its release, TagURL where
	// the tag can be seen.
	Bundled string
	Tag     string
	Commit  string
	TagURL  string
	Bundler Bundler
}

// BumpMarker is the line that ties a bump issue to its Container and release.
func BumpMarker(bundled, tag string) string {
	return "<!-- theos:bump " + bundled + " " + tag + " -->"
}

// HasBumpMarker reports whether body carries the marker of bundled's tag on a
// line of its own, as BumpIssue writes it.
func HasBumpMarker(body, bundled, tag string) bool {
	marker := BumpMarker(bundled, tag)
	for line := range strings.SplitSeq(body, "\n") {
		if strings.TrimSpace(line) == marker {
			return true
		}
	}
	return false
}

// BumpIssue renders the issue that asks the bundler to move to the release.
func BumpIssue(b Bump) (title, body string) {
	title = fmt.Sprintf("Bump %s to %s", b.Bundled, b.Tag)
	var s strings.Builder
	fmt.Fprintf(&s, "%s released %s at `%s`: %s\n\n", b.Bundled, b.Tag, b.Commit, b.TagURL)
	fmt.Fprintf(&s, "%s bundles %s (Relationship `%s`", b.Bundler.Container, b.Bundled, b.Bundler.Relationship)
	if b.Bundler.Description != "" {
		fmt.Fprintf(&s, ": %s", b.Bundler.Description)
	}
	s.WriteString("), so it ships a pinned release of it.\n\n")
	fmt.Fprintf(&s, "## Goal\n\n%s ships %s %s.\n\n", b.Bundler.Container, b.Bundled, b.Tag)
	s.WriteString("## Acceptance criteria\n\n")
	fmt.Fprintf(&s, "- [ ] Every place this repository pins a release of %s names %s\n", b.Bundled, b.Tag)
	fmt.Fprintf(&s, "- [ ] What %s's changes up to %s ask of the Containers that use it is done here, or filed as an issue\n",
		b.Bundled, b.Tag)
	s.WriteString("- [ ] `make check` passes\n\n")
	// A person wrote the description: no comment of theirs may pass for a marker.
	body = strings.ReplaceAll(s.String(), "<!--", "&lt;!--") + BumpMarker(b.Bundled, b.Tag) + "\n"
	return title, body
}
