package model

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ReleaseMilestoneDue is how long after it is made a Brief's release
// milestone is due; the date only orders releases (plans/done/brief-releases.md, R6).
const ReleaseMilestoneDue = 7 * 24 * time.Hour

// LabelBug marks a fix; a round whose work for a Container is all fixes gets a
// patch release (plans/brief-patch-releases.md, P1).
const LabelBug = "kind/bug"

// ReleaseMilestoneTitle is the title of the release milestone a Brief's round
// makes in a repository whose milestones, open or closed, have titles: after
// the newest vMAJOR.MINOR.PATCH among them, the next patch version for a round
// of fixes, else the next minor version; v0.1.0 when there is none. Titles that
// aren't versions are ignored (R6, P1).
func ReleaseMilestoneTitle(titles []string, fixes bool) string {
	newest := newestVersion(titles)
	switch {
	case newest == nil:
		return "v0.1.0"
	case fixes:
		return fmt.Sprintf("v%d.%d.%d", newest[0], newest[1], newest[2]+1)
	default:
		return fmt.Sprintf("v%d.%d.0", newest[0], newest[1]+1)
	}
}

// newestVersion is the newest version among titles, nil when none is one.
func newestVersion(titles []string) []int {
	var newest []int
	for _, title := range titles {
		if version := parseVersion(title); version != nil &&
			(newest == nil || compareVersions(version, newest) > 0) {
			newest = version
		}
	}
	return newest
}

// parseVersion reads a vMAJOR.MINOR.PATCH title, nil when it isn't one or is
// too large to be a version a Crew tags.
func parseVersion(title string) []int {
	m := releaseTitle.FindStringSubmatch(title)
	if m == nil {
		return nil
	}
	version := make([]int, 3)
	for i := range version {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return nil
		}
		version[i] = n
	}
	return version
}

// ReleaseMilestoneDueAt is when a release milestone made at now is due:
// ReleaseMilestoneDue later, or an hour after the latest due date among the
// open releases, whichever is later, as its version is the newest (P4).
func ReleaseMilestoneDueAt(open []Release, now time.Time) time.Time {
	due := now.Add(ReleaseMilestoneDue)
	for _, r := range open {
		if parseVersion(r.Title) != nil && !r.Due.Before(due) {
			due = r.Due.Add(time.Hour)
		}
	}
	return due
}

// RoundFixes reports, by Container, whether the plan's work for it is all
// fixes: at least one issue that isn't by hand, each labelled kind/bug, and no
// reviewer's suggestion taken, which is new work (P2).
func RoundFixes(plan Plan) map[string]bool {
	fixes := map[string]bool{}
	for _, issue := range plan.Issues {
		if IsByHand(issue.Labels) {
			continue
		}
		bug := slices.ContainsFunc(issue.Labels, func(l string) bool { return strings.TrimSpace(l) == LabelBug })
		if all, seen := fixes[issue.Container]; !seen || all {
			fixes[issue.Container] = bug
		}
	}
	for _, a := range plan.Answers {
		if a.Answer == AnswerTake {
			fixes[a.Container] = false
		}
	}
	return fixes
}

func compareVersions(a, b []int) int {
	for i := range a {
		if a[i] != b[i] {
			return a[i] - b[i]
		}
	}
	return 0
}

// ReleaseMilestoneMarker is the line that ties a release milestone to the
// Brief's round, so a restart finds the milestone again (R6).
func ReleaseMilestoneMarker(brief string, round int) string {
	return fmt.Sprintf("<!-- c4-brief: %s/%d -->", brief, round)
}

// ReleaseMilestoneDescription is the description the operator gives the
// release milestone of the Brief's round; it ends with the marker.
func ReleaseMilestoneDescription(brief string, round int) string {
	return fmt.Sprintf("The work Brief %s's round %d filed; released once it is done.\n\n%s\n",
		brief, round, ReleaseMilestoneMarker(brief, round))
}

// HasReleaseMilestoneMarker reports whether a milestone's description carries
// the marker of the Brief's round on a line of its own.
func HasReleaseMilestoneMarker(description, brief string, round int) bool {
	marker := ReleaseMilestoneMarker(brief, round)
	for line := range strings.SplitSeq(description, "\n") {
		if strings.TrimSpace(line) == marker {
			return true
		}
	}
	return false
}

// OpenIssue is an open issue of a repository: its number, labels and
// milestone's ID, 0 when it is in none.
type OpenIssue struct {
	Number    int
	Labels    []string
	Milestone int64
}

// StrayWork returns the numbers of the issues a Crew works, status/ready or
// status/in-progress, that sit in no milestone, so they would come after
// every release. A released Container's are put in its first open release,
// so work it already has never waits behind its own later releases (#599).
// A suggestion still proposed, a person's kind/by-hand work and an issue
// handed back to a person stay where they are: none of them may hold a
// release.
func StrayWork(issues []OpenIssue) []int {
	var out []int
	for _, i := range issues {
		works := slices.Contains(i.Labels, LabelReady) || slices.Contains(i.Labels, LabelInProgress)
		if i.Milestone == 0 && works && !IsByHand(i.Labels) {
			out = append(out, i.Number)
		}
	}
	return out
}
