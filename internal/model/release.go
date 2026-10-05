package model

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// releaseTitle is a release's version: vMAJOR.MINOR.PATCH, no leading zeros.
var releaseTitle = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// IsReleaseTitle reports whether a milestone's title names a version a Crew may tag.
func IsReleaseTitle(title string) bool {
	return releaseTitle.MatchString(title)
}

// ReleaseMilestone is an open milestone with a due date and its issues.
type ReleaseMilestone struct {
	Release
	ID int64
	// Open and Closed are the numbers of its open and closed issues, without pull
	// requests.
	Open   []int
	Closed []int
}

// MergedTask is a Merged Task on the Crew's Container.
type MergedTask struct {
	Name  string
	Issue int
	// Commit is the merge commit on the default branch; empty until recorded.
	Commit string
	// At is when the Task completed.
	At time.Time
}

// ReleaseFacts is everything tagging a release depends on.
type ReleaseFacts struct {
	// Milestones are the repository's open milestones with a due date.
	Milestones []ReleaseMilestone
	// Active are the issues of the Tasks not yet Merged or Failed.
	Active []int
	// Merged are the Merged Tasks with an issue. A deleted Task's merge is lost:
	// a release whose newest merge it was would be tagged at an older one, or
	// clash with the tag already made.
	Merged []MergedTask
	// Tags maps the existing tags among the releases' titles to their commits.
	Tags map[string]string
}

// Reasons a done release doesn't go out.
const (
	ReleaseWaiting       = "WaitsForEarlierRelease"
	ReleaseNoMergedIssue = "NoMergedIssue"
	ReleaseNoMergeCommit = "NoMergeCommit"
	ReleaseLaterWork     = "HoldsLaterWork"
	ReleaseTagClash      = "TagClash"
)

// ReleaseStep is what a Crew does about its releases. With Commit set, it tags
// Commit as Tag when CreateTag says so, then closes milestone Milestone. With
// Held set, release Tag is done but can't go out: Reason says why in a word,
// Held in a sentence. With neither, there is nothing to do.
type ReleaseStep struct {
	Tag       string
	Commit    string
	Milestone int64
	CreateTag bool
	Reason    string
	Held      string
}

// held is a ReleaseStep that leaves release title alone.
func held(title, reason, format string, args ...any) ReleaseStep {
	return ReleaseStep{Tag: title, Reason: reason, Held: fmt.Sprintf(format, args...)}
}

// NextRelease decides whether the first open release is done and where its tag
// goes. A release is done when it has no open issue and no Task works on one of
// its issues. Releases go out in order, so only the first can be tagged; its tag
// goes at the merge commit of its most recently merged issue, and is never moved.
func NextRelease(f ReleaseFacts) ReleaseStep {
	var releases []ReleaseMilestone
	for _, m := range f.Milestones {
		if IsReleaseTitle(m.Title) && !m.Due.IsZero() {
			releases = append(releases, m)
		}
	}
	if len(releases) == 0 {
		return ReleaseStep{}
	}
	slices.SortStableFunc(releases, func(a, b ReleaseMilestone) int {
		return compareReleases(&a.Release, &b.Release)
	})
	first := releases[0]
	if !releaseDone(first, f.Active) {
		for _, later := range releases[1:] {
			if releaseDone(later, f.Active) {
				return held(later.Title, ReleaseWaiting, "%s is done and waits for %s to be released",
					later.Title, first.Title)
			}
		}
		return ReleaseStep{}
	}

	// A merge without its commit or time holds the release, unless another
	// merged Task of the same issue (a later try) has them.
	recorded := func(t MergedTask) bool { return t.Commit != "" && !t.At.IsZero() }
	var newest *MergedTask
	for i, t := range f.Merged {
		if !slices.Contains(first.Closed, t.Issue) {
			continue
		}
		if !recorded(t) {
			if slices.ContainsFunc(f.Merged, func(o MergedTask) bool { return o.Issue == t.Issue && recorded(o) }) {
				continue
			}
			return held(first.Title, ReleaseNoMergeCommit, "%s waits for the merge commit of Task %s (#%d)",
				first.Title, t.Name, t.Issue)
		}
		if newest == nil || mergedAfter(t, *newest) {
			newest = &f.Merged[i]
		}
	}
	if newest == nil {
		return held(first.Title, ReleaseNoMergedIssue,
			"%s is done but none of its issues was merged by a Task; a person tags it", first.Title)
	}
	// An existing tag is never moved; at the right commit, only the milestone is
	// left to close, whoever made the tag.
	if existing, ok := f.Tags[first.Title]; ok {
		if existing != newest.Commit {
			return held(first.Title, ReleaseTagClash, "%s is done at %s but the tag %s names %s; a person resolves it",
				first.Title, newest.Commit, first.Title, existing)
		}
		return ReleaseStep{Tag: first.Title, Commit: newest.Commit, Milestone: first.ID}
	}
	if early := laterWorkBefore(releases[1:], f.Merged, *newest); early != "" {
		return held(first.Title, ReleaseLaterWork, "%s's newest merge (#%d) came after %s merged, so its tag would "+
			"hold later work; a person tags it", first.Title, newest.Issue, early)
	}
	return ReleaseStep{Tag: first.Title, Commit: newest.Commit, Milestone: first.ID, CreateTag: true}
}

// releaseDone reports whether a release has no open issue and no Task at work on
// one of its issues.
func releaseDone(m ReleaseMilestone, active []int) bool {
	if len(m.Open) > 0 {
		return false
	}
	return !slices.ContainsFunc(active, func(n int) bool { return slices.Contains(m.Closed, n) })
}

// mergedAfter orders merges by completion time, then by Task name.
func mergedAfter(a, b MergedTask) bool {
	if c := a.At.Compare(b.At); c != 0 {
		return c > 0
	}
	return strings.Compare(a.Name, b.Name) > 0
}

// laterWorkBefore names an issue of a later release that merged before newest,
// as "#120 of v0.3.0"; empty when there is none.
func laterWorkBefore(later []ReleaseMilestone, merged []MergedTask, newest MergedTask) string {
	for _, m := range later {
		for _, t := range merged {
			inRelease := slices.Contains(m.Closed, t.Issue) || slices.Contains(m.Open, t.Issue)
			if inRelease && mergedAfter(newest, t) {
				return fmt.Sprintf("#%d of %s", t.Issue, m.Title)
			}
		}
	}
	return ""
}
