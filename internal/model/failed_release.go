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

// A failed release step is a failed issue labelled kind/release: a release's
// prepare issue. Why its last try stopped can be stale by the time a round
// answers it: work can merge into its milestone since, so a step judged empty
// then has something to release now. The planner sees the milestone as it is,
// and a plan can't close the step while work merged after the try stopped.

// FailedRelease is a failed release step's milestone as it is now.
type FailedRelease struct {
	// Milestone is the release's title, such as v0.3.0.
	Milestone string
	// Stopped is when the step's newest failed Task ended; zero when none did.
	Stopped time.Time
	// Merged are the milestone's closed issues a Task merged, prepare issues
	// left out, each at its newest merge, in the order they merged.
	Merged []ReleaseMerge
}

// ReleaseMerge is an issue merged into a release.
type ReleaseMerge struct {
	Issue int
	Title string
	At    time.Time
}

// NewFailedRelease is release step number's milestone: titles are the
// milestone's closed issues by number, prepared its prepare issues, and tasks
// the Container's Tasks.
func NewFailedRelease(milestone string, number int, titles map[int]string, prepared []int,
	tasks []IssueTask) *FailedRelease {
	out := &FailedRelease{Milestone: milestone}
	newest := map[int]time.Time{}
	for _, t := range tasks {
		switch {
		case t.Issue == number && t.Failed && t.Completed.After(out.Stopped):
			out.Stopped = t.Completed
		case !t.Merged || t.Issue == number || slices.Contains(prepared, t.Issue):
		default:
			if _, closed := titles[t.Issue]; !closed {
				continue
			}
			if at, seen := newest[t.Issue]; !seen || t.Completed.After(at) {
				newest[t.Issue] = t.Completed
			}
		}
	}
	for n, at := range newest {
		out.Merged = append(out.Merged, ReleaseMerge{Issue: n, Title: titles[n], At: at})
	}
	slices.SortFunc(out.Merged, func(a, b ReleaseMerge) int {
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		return a.Issue - b.Issue
	})
	return out
}

// MergedSince are the merges the step's last try didn't see: those after it
// stopped, or all of them when no try did. A merge without a time counts, as
// it can't be shown to be older.
func (r *FailedRelease) MergedSince() []ReleaseMerge {
	if r == nil {
		return nil
	}
	var out []ReleaseMerge
	for _, m := range r.Merged {
		if r.Stopped.IsZero() || m.At.IsZero() || m.At.After(r.Stopped) {
			out = append(out, m)
		}
	}
	return out
}

// checkReleaseClose refuses closing release step f while work merged into its
// milestone after its last try stopped: that try judged an older milestone.
func checkReleaseClose(answer string, f FailedIssue, subject string) []string {
	since := f.Release.MergedSince()
	if answer != AnswerClose || len(since) == 0 {
		return nil
	}
	issues := make([]string, 0, len(since))
	for _, m := range since {
		issues = append(issues, fmt.Sprintf("#%d", m.Issue))
	}
	stopped := "it was first tried"
	if !f.Release.Stopped.IsZero() {
		stopped = "its last try stopped at " + f.Release.Stopped.UTC().Format(time.RFC3339)
	}
	return []string{fmt.Sprintf("closes %s, the release step of %s, but %s merged into %s after %s, so it has "+
		"work to release: retry it, or ask the person", subject, f.Release.Milestone, strings.Join(issues, ", "), f.Release.Milestone,
		stopped)}
}

type releaseOut struct {
	Milestone string `yaml:"milestone"`
	// Stopped is when the last try ended; empty when none did.
	Stopped string     `yaml:"stopped,omitempty"`
	Merged  []mergeOut `yaml:"merged"`
}

type mergeOut struct {
	Issue    int    `yaml:"issue"`
	Title    string `yaml:"title"`
	MergedAt string `yaml:"mergedAt,omitempty"`
	// AfterStop marks a merge the last try didn't see.
	AfterStop bool `yaml:"afterStop,omitempty"`
}

// workspaceRelease renders a failed release step's milestone for
// model/failed.yaml, or nil for another failed issue.
func workspaceRelease(r *FailedRelease) *releaseOut {
	if r == nil {
		return nil
	}
	out := &releaseOut{Milestone: r.Milestone, Merged: []mergeOut{}}
	if !r.Stopped.IsZero() {
		out.Stopped = r.Stopped.UTC().Format(time.RFC3339)
	}
	since := r.MergedSince()
	for _, m := range r.Merged {
		entry := mergeOut{Issue: m.Issue, Title: oneParagraph(m.Title), AfterStop: slices.Contains(since, m)}
		if !m.At.IsZero() {
			entry.MergedAt = m.At.UTC().Format(time.RFC3339)
		}
		out.Merged = append(out.Merged, entry)
	}
	return out
}
