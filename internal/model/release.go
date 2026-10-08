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
	// Prepared are the numbers of its prepare issues, open or closed: those
	// labelled kind/release.
	Prepared []int
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
	// Prepare says the Crew files each release's prepare issue: a done release
	// with merged work and no prepare issue gets one before it is tagged, and
	// work merged after its prepare issue holds it.
	Prepare bool
}

// LabelRelease marks a release's prepare issue, which sets the version and
// writes the changelog; the release is tagged at its merge.
const LabelRelease = "kind/release"

// ReleaseLabel is the prepare issue's label, for the Git host.
func ReleaseLabel() IssueLabel {
	return IssueLabel{LabelRelease, "#1d76db", "Prepares a release: sets its version and writes its changelog", false}
}

// Reasons a done release doesn't go out.
const (
	ReleaseWaiting       = "WaitsForEarlierRelease"
	ReleaseNoMergedIssue = "NoMergedIssue"
	ReleaseNoMergeCommit = "NoMergeCommit"
	ReleaseLaterWork     = "HoldsLaterWork"
	ReleaseTagClash      = "TagClash"
	// ReleaseMergedAfterPrepare holds a release whose changelog misses work
	// merged after its prepare issue.
	ReleaseMergedAfterPrepare = "MergedAfterPrepare"
)

// ReleaseStep is what a Crew does about its releases. With File set, it files
// release Tag's prepare issue in milestone Milestone, listing the issues Merged
// in the order they merged. With Commit set, it tags Commit as Tag when
// CreateTag says so, then closes milestone Milestone. With Held set, release Tag
// is done but can't go out: Reason says why in a word, Held in a sentence. With
// none, there is nothing to do.
type ReleaseStep struct {
	Tag       string
	Commit    string
	Milestone int64
	CreateTag bool
	File      bool
	Merged    []int
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

	if f.Prepare && len(first.Prepared) == 0 {
		if work := mergedWork(first, f.Merged); len(work) > 0 {
			return ReleaseStep{Tag: first.Title, Milestone: first.ID, File: true, Merged: work}
		}
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
	if f.Prepare && !slices.Contains(first.Prepared, newest.Issue) {
		if prepared := newestOf(f.Merged, first.Prepared); prepared != nil {
			return held(first.Title, ReleaseMergedAfterPrepare, "%s's #%d merged after its prepare issue #%d, so its "+
				"changelog misses it; a person files another %s issue in %s or tags it",
				first.Title, newest.Issue, prepared.Issue, LabelRelease, first.Title)
		}
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

// mergedWork returns the release's closed issues, prepare issues left out, that
// a Task merged, each once, in the order they first merged.
func mergedWork(m ReleaseMilestone, merged []MergedTask) []int {
	var work []MergedTask
	for _, t := range merged {
		if slices.Contains(m.Closed, t.Issue) && !slices.Contains(m.Prepared, t.Issue) {
			work = append(work, t)
		}
	}
	slices.SortStableFunc(work, func(a, b MergedTask) int {
		if mergedAfter(a, b) {
			return 1
		}
		return -1
	})
	var out []int
	for _, t := range work {
		if !slices.Contains(out, t.Issue) {
			out = append(out, t.Issue)
		}
	}
	return out
}

// newestOf returns the newest recorded merge of one of the issues, or nil.
func newestOf(merged []MergedTask, issues []int) *MergedTask {
	var newest *MergedTask
	for i, t := range merged {
		if slices.Contains(issues, t.Issue) && !t.At.IsZero() && (newest == nil || mergedAfter(t, *newest)) {
			newest = &merged[i]
		}
	}
	return newest
}

// PrepareIssue renders release tag's prepare issue, which lists the issues
// merged in it, by number and title, in the order they merged.
func PrepareIssue(tag string, merged []int, titles map[int]string) (title, body string) {
	version := strings.TrimPrefix(tag, "v")
	var b strings.Builder
	fmt.Fprintf(&b, "The work of release %s is merged. This issue prepares it: once it merges, %s is tagged "+
		"at its merge commit, so the tag carries the version and the changelog.\n\n", tag, tag)
	fmt.Fprintf(&b, "## Merged in %s\n\n", tag)
	for _, n := range merged {
		fmt.Fprintf(&b, "- #%d %s\n", n, titles[n])
	}
	b.WriteString("\n## Acceptance criteria\n\n")
	fmt.Fprintf(&b, "- [ ] Every file that holds the version says %s, such as `version` in `package.json` and its "+
		"lockfile; `make release-version VERSION=%s` passes when the Makefile has that target\n", version, tag)
	fmt.Fprintf(&b, "- [ ] `CHANGELOG.md` has a %s section that covers the issues above, with the \"Unreleased\" "+
		"entries moved under it; the file is created in Keep a Changelog form when it is missing\n", version)
	b.WriteString("- [ ] Nothing else changes: no code, test or check\n")
	return "Prepare " + tag, b.String()
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

// ReleaseRun is one CI workflow run a release's tag started.
type ReleaseRun struct {
	// Workflow is the workflow file it runs, such as release.yaml.
	Workflow string
	// Status is queued, waiting, in_progress or completed.
	Status string
	// Conclusion is how a completed run ended: success, failure, cancelled or
	// skipped.
	Conclusion string
	URL        string
}

// Results of a release's runs.
const (
	RunWaiting   = "Waiting"
	RunRunning   = "Running"
	RunSucceeded = "Succeeded"
	RunFailed    = "Failed"
)

// ReleaseRunResult sums up the runs on a release's tag, newest first, counting
// only each workflow's newest run (a tag pushed again runs it again): Failed as
// soon as one ended other than success or skipped, with that run, Running while
// one hasn't ended, Succeeded once all ended well, Waiting while none started.
// The run returned is the failed one, else the newest.
func ReleaseRunResult(runs []ReleaseRun) (string, ReleaseRun) {
	if len(runs) == 0 {
		return RunWaiting, ReleaseRun{}
	}
	result := RunSucceeded
	seen := map[string]bool{}
	for _, run := range runs {
		switch {
		case seen[run.Workflow]:
		case run.Status != "completed":
			result = RunRunning
		case run.Conclusion != "success" && run.Conclusion != "skipped":
			return RunFailed, run
		}
		seen[run.Workflow] = true
	}
	return result, runs[0]
}

// ReleaseRunBound is how long after tagging a release's runs may take to end
// before the Crew reports them: a run no runner picks up never ends.
const ReleaseRunBound = time.Hour

// ReleaseRunLate says whether a release tagged at tagged, whose runs are at
// result, has runs that haven't ended ReleaseRunBound after tagging at now.
func ReleaseRunLate(result string, tagged, now time.Time) bool {
	return (result == RunWaiting || result == RunRunning) && now.Sub(tagged) >= ReleaseRunBound
}

// ReleaseBumpable says whether the bumps of a release tagged at tagged, whose
// runs are at result, may be filed at now: once its runs all ended well, since
// they build the files a bump reads, or once ReleaseRunBound has passed with no
// run started (or none read), for a repository whose tags start no workflow. A
// release whose runs failed or haven't ended waits, however long.
func ReleaseBumpable(result string, tagged, now time.Time) bool {
	switch result {
	case RunSucceeded:
		return true
	case RunWaiting, "":
		return now.Sub(tagged) >= ReleaseRunBound
	}
	return false
}
