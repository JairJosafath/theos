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
	// MilestoneClosed says the milestone itself is closed.
	MilestoneClosed bool
	// FullChecks are the issues, open or closed, a Crew filed when the
	// release's full check failed, and the commit each failed at.
	FullChecks []FullCheckFix
	// Cut says its description carries a cut mark: under a cadence it was
	// cut, and takes no more work (docs/plans/release-cadence.md, C4).
	Cut bool
}

// MergedTask is a Merged Task on the Crew's Container.
type MergedTask struct {
	Name  string
	Issue int
	// Commit is the merge commit on the default branch; empty until recorded.
	Commit string
	// At is when the Task completed.
	At time.Time
	// DoneBy is the issue of the Standard change that did the Task's whole
	// issue, so the Task never had a merge of its own; 0 for any other Task.
	DoneBy int
}

// ReleaseFacts is everything tagging a release depends on.
type ReleaseFacts struct {
	// Milestones are the repository's open milestones with a due date.
	Milestones []ReleaseMilestone
	// Active are the issues of the Tasks not yet Merged or Failed.
	Active []int
	// Working are the Tasks not yet Merged or Failed, Blocked ones left out:
	// their work merges before a prepare issue filed now.
	Working []string
	// Merged are the Merged Tasks with an issue. A deleted Task's merge is lost:
	// a release whose newest merge it was would be tagged at an older one, or
	// clash with the tag already made.
	Merged []MergedTask
	// Tags maps the existing tags among the releases' titles to their commits.
	Tags map[string]string
	// Previous is the newest closed release and the commit its tag names; nil
	// when there is none or it has no tag. The next tag must come after it.
	Previous *ReleasedTag
	// Prepare says the Crew files each release's prepare issue: a done release
	// with merged work and no prepare issue gets one before it is tagged, and
	// work merged after its prepare issue holds it.
	Prepare bool
	// FullCheckRerun is the release and commit whose failed full check a
	// person runs again (rerun-release, #2028); nil for none. Its issues of
	// that check, open or at work, hold the release no more meanwhile.
	FullCheckRerun *FullCheckAt
	// Cadence is the Crew's release cadence; one that doesn't cut (Release,
	// or unset) tags each release once it is done
	// (docs/plans/release-cadence.md).
	Cadence ReleaseCadence
	// Now is the time of the poll, and LastCut the Crew's last cut, zero
	// when it has cut none (see the function LastCut).
	Now, LastCut time.Time
	// ReleaseNow are the issues labelled release/now, and Fixes those that
	// are fixes (IsFix), of the open releases.
	ReleaseNow, Fixes []int
	// Titles are the titles of every milestone of the repository, open or
	// closed, which a new release's title and a retitle must not take.
	Titles []string
}

// FullCheckAt is release Tag's full check at Commit.
type FullCheckAt struct {
	Tag    string
	Commit string
}

// ReleasedTag is a release that went out, and the commit its tag names.
type ReleasedTag struct {
	Release
	Commit string
}

// ReleaseBefore reports whether release a goes out before release b.
func ReleaseBefore(a, b Release) bool { return compareReleases(&a, &b) < 0 }

// PreviousReleases are the closed releases that went out before every open
// one, newest first, at most limit: where the previous release's tag is
// looked for.
func PreviousReleases(closed []Release, open []ReleaseMilestone, limit int) []Release {
	var out []Release
	for _, r := range closed {
		if !slices.ContainsFunc(open, func(m ReleaseMilestone) bool { return !ReleaseBefore(r, m.Release) }) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b Release) int { return compareReleases(&b, &a) })
	return out[:min(len(out), limit)]
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
	ReleaseTagClash      = "TagClash"
	// ReleaseMergedAfterPrepare holds a release whose changelog misses work
	// merged after its prepare issue.
	ReleaseMergedAfterPrepare = "MergedAfterPrepare"
	// ReleaseBehindPrevious holds a release whose tag would not come after
	// the previous release's: it would ship older code under a newer version.
	ReleaseBehindPrevious = "BehindPreviousRelease"
	// ReleasePrepareWaits holds a done release's prepare issue until the
	// Tasks at work have ended, so it is built on the main their work merges
	// into and lists that work (#956). It is no warning: the Crew starts no
	// other issue meanwhile (CrewFacts.Preparing).
	ReleasePrepareWaits = "PrepareWaitsForTasks"
)

// ReleaseStep is what a Crew does about its releases. With File set, it files
// release Tag's prepare issue in milestone Milestone, listing the issues Merged:
// its own in the order they merged, then the later releases' merged ones its
// tag will carry. With Commit set, it moves the issues Moved into
// milestone Milestone, tags Commit as Tag when CreateTag says so, then closes
// milestone Milestone, the milestones Emptied and the milestones Skipped. With
// Cut set, it cuts release Tag, milestone Milestone, under a cadence. With
// Held set, release Tag is
// done but can't go out: Reason says why in a word, Held in a sentence. With
// none, there is nothing to do. CutAt, when set, is the Crew's last cut from
// now on.
type ReleaseStep struct {
	Tag       string
	Commit    string
	Milestone int64
	CreateTag bool
	File      bool
	Merged    []int
	Moved     []ReleaseMove
	Emptied   []EmptiedRelease
	// Skipped are the open releases before Tag with no issue at all, which
	// close untagged with it (#2025).
	Skipped []EmptiedRelease
	// Closes are the open issues of Tag's full check that a re-run a person
	// asked for passed over (FullCheckRerun), closed once Tag is tagged.
	Closes []int
	Reason string
	Held   string
	Cut    *ReleaseCut
	CutAt  time.Time
}

// ReleaseMove is a later release's closed issue whose merge the tag carries, to
// move into the tagged release: issue Issue of release From.
type ReleaseMove struct {
	Issue int
	From  string
}

// EmptiedRelease is a later release whose issues all moved into the tagged one,
// so its milestone closes untagged.
type EmptiedRelease struct {
	ID    int64
	Title string
}

// held is a ReleaseStep that leaves release title alone.
func held(title, reason, format string, args ...any) ReleaseStep {
	return ReleaseStep{Tag: title, Reason: reason, Held: fmt.Sprintf(format, args...)}
}

// NextRelease decides whether the first open release is done and where its tag
// goes. A release is done when it has no open issue and no Task works on one of
// its issues. Releases go out in order, so only the first can be tagged; its tag
// goes at the merge commit of its most recently merged issue, and is never moved.
// A release with no issue at all, open or closed, is no release yet: it is
// neither done nor in the way, so it holds no later release, and one before the
// release tagged closes untagged with it (#2025).
//
// Under a cadence that cuts (Day or Week), the first release waits for its cut
// instead, and is cut first: docs/plans/release-cadence.md, cadenceStep.
func NextRelease(f ReleaseFacts) ReleaseStep {
	rerunFixes := withoutRerunFixes(&f)
	releases, empty := sortedReleases(f.Milestones)
	var step ReleaseStep
	switch {
	case f.Cadence.Cuts():
		step = cadenceStep(f, releases, empty)
	case len(releases) == 0:
		return ReleaseStep{}
	default:
		step = nextRelease(f, releases)
	}
	if step.Commit != "" {
		step.Skipped = skippedBefore(empty, releases[0].Release)
		if f.FullCheckRerun != nil && step.Tag == f.FullCheckRerun.Tag {
			step.Closes = rerunFixes
		}
	}
	return step
}

// sortedReleases are the milestones that are releases and have issues, in
// order, and those with no issue at all.
func sortedReleases(milestones []ReleaseMilestone) (releases, empty []ReleaseMilestone) {
	for _, m := range milestones {
		switch {
		case !IsReleaseTitle(m.Title) || m.Due.IsZero():
		case len(m.Open) == 0 && len(m.Closed) == 0:
			empty = append(empty, m)
		default:
			releases = append(releases, m)
		}
	}
	slices.SortStableFunc(releases, func(a, b ReleaseMilestone) int {
		return compareReleases(&a.Release, &b.Release)
	})
	return releases, empty
}

// withoutRerunFixes drops from the facts' open and active issues those of the
// full check a person runs again (FullCheckRerun), which hold its release no
// more, and returns them. The facts' slices are left as they were.
func withoutRerunFixes(f *ReleaseFacts) []int {
	rerun := f.FullCheckRerun
	if rerun == nil {
		return nil
	}
	var dropped []int
	f.Milestones = slices.Clone(f.Milestones)
	for i, m := range f.Milestones {
		if m.Title != rerun.Tag {
			continue
		}
		var open []int
		for _, n := range m.Open {
			if slices.Contains(m.FullChecks, FullCheckFix{Issue: n, Commit: rerun.Commit}) {
				dropped = append(dropped, n)
				continue
			}
			open = append(open, n)
		}
		f.Milestones[i].Open = open
	}
	f.Active = slices.DeleteFunc(slices.Clone(f.Active), func(n int) bool { return slices.Contains(dropped, n) })
	return dropped
}

// skippedBefore are the empty releases that go out before release, in order.
func skippedBefore(empty []ReleaseMilestone, release Release) []EmptiedRelease {
	slices.SortStableFunc(empty, func(a, b ReleaseMilestone) int {
		return compareReleases(&a.Release, &b.Release)
	})
	var out []EmptiedRelease
	for _, m := range empty {
		if ReleaseBefore(m.Release, release) {
			out = append(out, EmptiedRelease{ID: m.ID, Title: m.Title})
		}
	}
	return out
}

// nextRelease is NextRelease on the releases that have issues, in order.
func nextRelease(f ReleaseFacts, releases []ReleaseMilestone) ReleaseStep {
	f.Merged = doneByChanges(f.Merged)
	first := releases[0]
	if !releaseDone(first, f.Active) {
		// Under a cadence a later release waits for its own cut anyway.
		for _, later := range releases[1:] {
			if f.Cadence.Cuts() {
				break
			}
			if releaseDone(later, f.Active) {
				return held(later.Title, ReleaseWaiting, "%s is done and waits for %s to be released",
					later.Title, first.Title)
			}
		}
		return ReleaseStep{}
	}

	if step, ok := prepareStep(f, releases); ok {
		return step
	}

	// A merge without its commit or time holds the release, unless another
	// merged Task of the same issue (a later try) has them, or a Standard change
	// did its issue and that change's merge is lost: it is not among the Crew's
	// Tasks, and never will be. A change's merge still waiting for its commit
	// holds the release like the issue's own would.
	var newest *MergedTask
	for i, t := range f.Merged {
		if !slices.Contains(first.Closed, t.Issue) {
			continue
		}
		if !recorded(t) {
			if slices.ContainsFunc(f.Merged, func(o MergedTask) bool { return o.Issue == t.Issue && recorded(o) }) {
				continue
			}
			if t.DoneBy != 0 {
				change := slices.IndexFunc(f.Merged, func(o MergedTask) bool { return o.Issue == t.DoneBy })
				if change < 0 {
					continue
				}
				return held(first.Title, ReleaseNoMergeCommit, "%s waits for the merge commit of Task %s (#%d), "+
					"the Standard change that did #%d; a person moves #%d out of %s to release it without",
					first.Title, f.Merged[change].Name, t.DoneBy, t.Issue, t.Issue, first.Title)
			}
			return held(first.Title, ReleaseNoMergeCommit, "%s waits for the merge commit of Task %s (#%d); "+
				"a person moves #%d out of %s to release it without", first.Title, t.Name, t.Issue, t.Issue,
				first.Title)
		}
		if newest == nil || mergedAfter(t, *newest) {
			newest = &f.Merged[i]
		}
	}
	if newest == nil {
		return held(first.Title, ReleaseNoMergedIssue,
			"%s is done but none of its issues was merged by a Task; a person tags it", first.Title)
	}
	// A fix of the release's full check is the release's own: the changelog
	// stays its prepare issue's.
	if latest := newestOf(f.Merged, slices.DeleteFunc(slices.Clone(first.Closed), first.fullCheckFix)); f.Prepare &&
		latest != nil && !slices.Contains(first.Prepared, latest.Issue) {
		if prepared := newestOf(f.Merged, first.Prepared); prepared != nil {
			return held(first.Title, ReleaseMergedAfterPrepare, "%s's #%d merged after its prepare issue #%d, so its "+
				"changelog misses it; a person files another %s issue in %s or tags it",
				first.Title, latest.Issue, prepared.Issue, LabelRelease, first.Title)
		}
	}
	// An existing tag is never moved; at the right commit, only the milestones
	// are left to settle, whoever made the tag.
	existing, tagged := f.Tags[first.Title]
	if tagged && existing != newest.Commit {
		return held(first.Title, ReleaseTagClash, "%s is done at %s but the tag %s names %s; a person resolves it",
			first.Title, newest.Commit, first.Title, existing)
	}
	if behind := behindPrevious(f.Previous, first.Release, f.Merged, *newest); behind != "" && !tagged {
		return held(first.Title, ReleaseBehindPrevious, "%s would be tagged at %s, the merge of #%d, but %s; a "+
			"release's tag must come after the one before it, so a person tags it", first.Title, newest.Commit,
			newest.Issue, behind)
	}
	moved, emptied := carriedWork(releases[1:], f.Merged, *newest)
	return ReleaseStep{Tag: first.Title, Commit: newest.Commit, Milestone: first.ID, CreateTag: !tagged,
		Moved: moved, Emptied: emptied}
}

// prepareStep is the step about the done first release's prepare issue when
// the Crew files one and the release has merged work but no prepare issue yet:
// file it, or wait for the Tasks at work, whose merges move main under it.
func prepareStep(f ReleaseFacts, releases []ReleaseMilestone) (ReleaseStep, bool) {
	first := releases[0]
	if !f.Prepare || len(first.Prepared) > 0 {
		return ReleaseStep{}, false
	}
	work := mergedWork(first, f.Merged)
	if len(work) == 0 {
		return ReleaseStep{}, false
	}
	// Under a cadence the release was cut: no Task works on its issues, and
	// the other Tasks' merges wait for its tag instead (C10).
	if len(f.Working) > 0 && !f.Cadence.Cuts() {
		return held(first.Title, ReleasePrepareWaits, "%s is done; its prepare issue waits for %s %s to "+
			"end, so it is built on the main their work merges into", first.Title,
			plural(len(f.Working), "Task", "Tasks"), joinAnd(f.Working)), true
	}
	// The tag will carry the later releases' work merged so far.
	for _, later := range releases[1:] {
		work = append(work, mergedWork(later, f.Merged)...)
	}
	return ReleaseStep{Tag: first.Title, Milestone: first.ID, File: true, Merged: work}, true
}

// recorded reports whether a merge has its commit and time.
func recorded(t MergedTask) bool { return t.Commit != "" && !t.At.IsZero() }

// doneByChanges returns merged with each Task its Standard change finished
// merged at that change's newest recorded merge: the change's commit is where
// the issue's work landed. The slice given is left as it is.
func doneByChanges(merged []MergedTask) []MergedTask {
	out := slices.Clone(merged)
	for i, t := range out {
		if t.DoneBy == 0 || recorded(t) {
			continue
		}
		var change *MergedTask
		for j, c := range merged {
			if c.Issue == t.DoneBy && recorded(c) && (change == nil || mergedAfter(c, *change)) {
				change = &merged[j]
			}
		}
		if change != nil {
			out[i].Commit, out[i].At = change.Commit, change.At
		}
	}
	return out
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

// MovedComment is the comment on an issue a release's tag carries, moved into it
// from a later release.
func MovedComment(step ReleaseStep, m ReleaseMove) string {
	return fmt.Sprintf("Moved from %s to %s: it merged before %s's newest merge, so the tag %s at %s carries it.",
		m.From, step.Tag, step.Tag, step.Tag, step.Commit)
}

// ReleasedMessage says a release went out, with the later releases' issues
// moved into it and the later releases that left empty.
func ReleasedMessage(step ReleaseStep) string {
	message := fmt.Sprintf("Released %s at %s", step.Tag, step.Commit)
	if len(step.Moved) > 0 {
		moved := make([]string, 0, len(step.Moved))
		for _, m := range step.Moved {
			moved = append(moved, fmt.Sprintf("#%d of %s", m.Issue, m.From))
		}
		message += ", carrying " + strings.Join(moved, ", ")
	}
	if len(step.Emptied) > 0 {
		titles := make([]string, 0, len(step.Emptied))
		for _, e := range step.Emptied {
			titles = append(titles, e.Title)
		}
		message += "; " + strings.Join(titles, ", ") + " had nothing left and closed untagged"
	}
	if len(step.Skipped) > 0 {
		titles := make([]string, 0, len(step.Skipped))
		for _, e := range step.Skipped {
			titles = append(titles, e.Title)
		}
		message += "; " + strings.Join(titles, ", ") + " had no issues and closed untagged"
	}
	return message
}

// mergedAfter orders merges by completion time, then by Task name.
func mergedAfter(a, b MergedTask) bool {
	if c := a.At.Compare(b.At); c != 0 {
		return c > 0
	}
	return strings.Compare(a.Name, b.Name) > 0
}

// carriedWork returns the later releases' closed issues, prepare issues left
// out, whose newest recorded merge came before newest or is its commit, so a
// tag at newest carries them, and the later releases that moving them leaves
// with no issue.
// An open issue stays in its release, whose tag carries the rest of its work.
func carriedWork(later []ReleaseMilestone, merged []MergedTask, newest MergedTask) ([]ReleaseMove, []EmptiedRelease) {
	var moved []ReleaseMove
	var emptied []EmptiedRelease
	for _, m := range later {
		left := len(m.Open)
		for _, n := range m.Closed {
			last := newestOf(merged, []int{n})
			// A Task its Standard change finished shares the change's merge, so
			// the tag at that commit carries both.
			if slices.Contains(m.Prepared, n) || last == nil ||
				(!mergedAfter(newest, *last) && last.Commit != newest.Commit) {
				left++
				continue
			}
			moved = append(moved, ReleaseMove{Issue: n, From: m.Title})
		}
		if left == 0 && len(m.Closed) > 0 {
			emptied = append(emptied, EmptiedRelease{ID: m.ID, Title: m.Title})
		}
	}
	return moved, emptied
}

// behindPrevious says why tagging newest would not come after the previous
// release's tag, or "" when it does or can't be told. The default branch's
// merges follow one another, so a merge that ended before the one the previous
// tag names is behind it, and the same commit is no newer.
func behindPrevious(previous *ReleasedTag, release Release, merged []MergedTask, newest MergedTask) string {
	if previous == nil || !ReleaseBefore(previous.Release, release) {
		return ""
	}
	if previous.Commit == newest.Commit {
		return fmt.Sprintf("%s's tag names that commit already", previous.Title)
	}
	for _, t := range merged {
		if t.Commit == previous.Commit && !t.At.IsZero() && mergedAfter(t, newest) {
			return fmt.Sprintf("%s's tag names %s, the merge of #%d, which came after it", previous.Title,
				previous.Commit, t.Issue)
		}
	}
	return ""
}

// ReleaseRun is one CI workflow run a release's tag started.
type ReleaseRun struct {
	// ID is the run's ID on the Git host.
	ID int64
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

// FailedReleaseRuns returns the runs a re-run of a release starts again, newest
// first: of each workflow's newest run, the ones that ended other than success
// or skipped, as ReleaseRunResult counts them.
func FailedReleaseRuns(runs []ReleaseRun) []ReleaseRun {
	var failed []ReleaseRun
	seen := map[string]bool{}
	for _, run := range runs {
		if !seen[run.Workflow] && run.Status == "completed" && run.Conclusion != "success" &&
			run.Conclusion != "skipped" {
			failed = append(failed, run)
		}
		seen[run.Workflow] = true
	}
	return failed
}

// What a Crew does with its rerun-release annotation's value.
const (
	// RerunNone: the value is empty or answered already.
	RerunNone = ""
	// RerunRuns: re-run the last release's failed runs.
	RerunRuns = "Rerun"
	// RerunFullCheck: re-run the failed full check that holds the next release.
	RerunFullCheck = "RerunFullCheck"
	// RerunNothingFailed: say there is no failed run to re-run.
	RerunNothingFailed = "NothingFailed"
)

// ReleaseRerun says what a Crew does with value, its rerun-release
// annotation, given the value it answered last, its last release's run
// result, "" when it tagged none, and the result of the full check that holds
// its next release, "" when none runs. A failed full check goes first, as the
// next tag waits on it (#2028); a new value then re-runs the last release's.
// Each value is answered once: a value set while the run hasn't failed doesn't
// wait for it to fail.
func ReleaseRerun(value, answered, run, fullCheck string) string {
	switch {
	case value == "" || value == answered:
		return RerunNone
	case fullCheck == RunFailed:
		return RerunFullCheck
	case run == RunFailed:
		return RerunRuns
	default:
		return RerunNothingFailed
	}
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
// release whose runs failed or haven't ended waits, however long. A release its
// Crew publishes waits until it is published, since a bundler pins the published
// files (#864); publishing needs its runs to succeed, so the bound doesn't apply.
func ReleaseBumpable(result string, publishes, published bool, tagged, now time.Time) bool {
	if publishes {
		return published
	}
	switch result {
	case RunSucceeded:
		return true
	case RunWaiting, "":
		return now.Sub(tagged) >= ReleaseRunBound
	}
	return false
}
