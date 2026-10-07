package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ReleaseMilestoneDue is how long after it is made a Brief's release
// milestone is due; the date only orders releases (plans/done/brief-releases.md, R6).
const ReleaseMilestoneDue = 7 * 24 * time.Hour

// ReleaseMilestoneTitle is the title of the release milestone a Brief's round
// makes in a repository whose milestones, open or closed, have titles: the
// next minor version after the newest vMAJOR.MINOR.PATCH among them, or
// v0.1.0 when there is none. Titles that aren't versions are ignored (R6).
func ReleaseMilestoneTitle(titles []string) string {
	var newest []int
	for _, title := range titles {
		m := releaseTitle.FindStringSubmatch(title)
		if m == nil {
			continue
		}
		version := make([]int, 3)
		for i := range version {
			n, err := strconv.Atoi(m[i+1])
			if err != nil {
				// Too large to be a version a Crew tags.
				version = nil
				break
			}
			version[i] = n
		}
		if version != nil && (newest == nil || compareVersions(version, newest) > 0) {
			newest = version
		}
	}
	if newest == nil {
		return "v0.1.0"
	}
	return fmt.Sprintf("v%d.%d.0", newest[0], newest[1]+1)
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
