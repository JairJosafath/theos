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

// LabelBug marks a fix, which goes into the patch release after the release in
// progress (plans/done/fixes-patch-next.md, F5).
const LabelBug = "kind/bug"

// BugLabel is the fix's label, for the Git host.
func BugLabel() IssueLabel {
	return IssueLabel{LabelBug, "#d73a4a", "A fix: goes into the patch release after the release in progress", false}
}

// LabelTest marks work that adds or fixes a test; like kind/bug, it is a fix
// (plans/done/fixes-patch-next.md, F5).
const LabelTest = "kind/test"

// IsFix reports whether an issue the Brief files is a fix, so it goes into the
// patch release after the release in progress: labelled kind/bug or kind/test,
// and not kind/by-hand, which no Task does (F5).
func IsFix(labels []string) bool {
	if IsByHand(labels) {
		return false
	}
	return slices.ContainsFunc(labels, func(l string) bool {
		l = strings.TrimSpace(l)
		return l == LabelBug || l == LabelTest
	})
}

// Slot is where a round's fixes go: an existing milestone to join (ID), or,
// when ID is 0, a new one to make with Title, due at Due.
type Slot struct {
	ID    int64
	Title string
	Due   time.Time
}

// PatchSlot is where a Brief's round puts its fixes for a Container whose
// milestones are milestones, at now (plans/done/fixes-patch-next.md, F1 to F4).
// From the release in progress, the open, dated version milestone due first,
// or the newest version milestone when none is open, it takes the next patch
// title. An open milestone with that title that can still take work, with no
// prepare issue, is joined; a title that is closed or prepared is skipped for
// the patch after it. A new milestone is due halfway between the later of the
// release in progress and the patches skipped, and the next open version
// milestone after that, or as ReleaseMilestoneDueAt has it when nothing is
// open after it. With no version milestone at all, the first release is v0.1.0.
func PatchSlot(milestones []ReleaseMilestone, now time.Time) Slot {
	var versions, open []ReleaseMilestone
	var openReleases []Release
	for _, m := range milestones {
		if parseVersion(m.Title) == nil {
			continue
		}
		versions = append(versions, m)
		if !m.MilestoneClosed {
			openReleases = append(openReleases, m.Release)
			if !m.Due.IsZero() {
				open = append(open, m)
			}
		}
	}
	if len(versions) == 0 {
		return Slot{Title: ReleaseMilestoneTitle(nil, false), Due: ReleaseMilestoneDueAt(openReleases, now)}
	}
	follows := releaseInProgress(versions, open)
	after := follows.Release
	for {
		version := parseVersion(follows.Title)
		title := fmt.Sprintf("v%d.%d.%d", version[0], version[1], version[2]+1)
		same := slices.DeleteFunc(slices.Clone(versions), func(m ReleaseMilestone) bool { return m.Title != title })
		if len(same) == 0 {
			return Slot{Title: title, Due: patchDue(after, open, openReleases, now)}
		}
		for _, m := range same {
			if !m.MilestoneClosed && len(m.Prepared) == 0 {
				return Slot{ID: m.ID, Title: m.Title, Due: m.Due}
			}
		}
		follows = same[0]
		if compareReleases(&follows.Release, &after) > 0 {
			after = follows.Release
		}
	}
}

// releaseInProgress is the open, dated version milestone due first, or the
// newest version milestone when none is open (F1).
func releaseInProgress(versions, open []ReleaseMilestone) ReleaseMilestone {
	if len(open) > 0 {
		return slices.MinFunc(open, func(a, b ReleaseMilestone) int { return compareReleases(&a.Release, &b.Release) })
	}
	return slices.MaxFunc(versions, func(a, b ReleaseMilestone) int {
		return compareVersions(parseVersion(a.Title), parseVersion(b.Title))
	})
}

// patchDue is when a new patch milestone is due that comes after after, the
// later of the release in progress and the patches skipped: halfway to the
// next open, dated version milestone after it, else as ReleaseMilestoneDueAt
// has it (F4). An after with no due date counts from now.
func patchDue(after Release, open []ReleaseMilestone, openReleases []Release, now time.Time) time.Time {
	var next *Release
	for i := range open {
		if compareReleases(&open[i].Release, &after) > 0 &&
			(next == nil || compareReleases(&open[i].Release, next) < 0) {
			next = &open[i].Release
		}
	}
	if next == nil {
		return ReleaseMilestoneDueAt(openReleases, now)
	}
	from := after.Due
	if from.IsZero() {
		from = now
	}
	return from.Add(next.Due.Sub(from) / 2)
}

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

// EmptyReleaseToJoin is the open, dated release milestone with no issue at all
// that a Brief's round takes for its release milestone instead of making one,
// or nil (#2025). An earlier round of the Brief made it (its ID is among
// made) and nothing went in, so it is the next minor release the planner
// names: the one ReleaseMilestoneTitle gives with those empty milestones left
// out. A milestone a person made is theirs, empty or not. Open and Closed say
// which issues each milestone holds.
func EmptyReleaseToJoin(milestones []ReleaseMilestone, made []int64) *ReleaseMilestone {
	empty := func(m ReleaseMilestone) bool {
		return !m.MilestoneClosed && !m.Due.IsZero() && len(m.Open) == 0 && len(m.Closed) == 0 &&
			slices.Contains(made, m.ID)
	}
	var titles []string
	for _, m := range milestones {
		if !empty(m) {
			titles = append(titles, m.Title)
		}
	}
	next := ReleaseMilestoneTitle(titles, false)
	for i, m := range milestones {
		if m.Title == next && empty(m) {
			return &milestones[i]
		}
	}
	return nil
}

// AddMarker is a milestone's description with marker on a line of its own
// at its end, once.
func AddMarker(description, marker string) string {
	if hasMarkerLine(description, marker) {
		return description
	}
	if description = strings.TrimRight(description, "\n"); description != "" {
		description += "\n\n"
	}
	return description + marker + "\n"
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

// HasAnyReleaseMilestoneMarker reports whether a milestone's description
// carries the release marker of any round of the Brief on a line of its own.
func HasAnyReleaseMilestoneMarker(description, brief string) bool {
	prefix, suffix := "<!-- c4-brief: "+brief+"/", " -->"
	for line := range strings.SplitSeq(description, "\n") {
		line = strings.TrimSpace(line)
		round, ok := strings.CutPrefix(line, prefix)
		if !ok {
			continue
		}
		if round, ok = strings.CutSuffix(round, suffix); ok {
			if _, err := strconv.Atoi(round); err == nil {
				return true
			}
		}
	}
	return false
}

// HasReleaseMilestoneMarker reports whether a milestone's description carries
// the marker of the Brief's round on a line of its own.
func HasReleaseMilestoneMarker(description, brief string, round int) bool {
	return hasMarkerLine(description, ReleaseMilestoneMarker(brief, round))
}

// PatchMilestoneMarker is the line that ties a patch milestone a Brief's round
// made for its fixes to the round, next to the round's release milestone, so
// a restart finds each (F7).
func PatchMilestoneMarker(brief string, round int) string {
	return fmt.Sprintf("<!-- c4-brief: %s/%d fixes -->", brief, round)
}

// PatchMilestoneDescription is the description the operator gives the patch
// milestone the Brief's round makes; it ends with the patch marker.
func PatchMilestoneDescription(brief string, round int) string {
	return fmt.Sprintf("The fixes Brief %s's round %d filed; released right after the release in progress.\n\n%s\n",
		brief, round, PatchMilestoneMarker(brief, round))
}

// HasPatchMilestoneMarker reports whether a milestone's description carries
// the patch marker of the Brief's round on a line of its own.
func HasPatchMilestoneMarker(description, brief string, round int) bool {
	return hasMarkerLine(description, PatchMilestoneMarker(brief, round))
}

// hasMarkerLine reports whether marker is a line of description.
func hasMarkerLine(description, marker string) bool {
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
// every release. A released Container's are put in its first release that
// takes work (ReleaseTakingWork), so work it already has never waits behind
// its own later releases (#599). A suggestion still proposed, a person's
// kind/by-hand work and an issue handed back to a person stay where they are:
// none of them may hold a release.
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
