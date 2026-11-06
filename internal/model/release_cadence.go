package model

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// A Crew's releases go out on a cadence (docs/plans/done/release-cadence.md): each
// once it is done (Release), or at a cut once a day (Day) or a week (Week),
// which ships what is merged and carries the rest into the next release.

// Cadences, a releaseCadence's every.
const (
	CadenceRelease = "Release"
	CadenceDay     = "Day"
	CadenceWeek    = "Week"
)

// The defaults of a cadence's fields (C1, C3).
const (
	DefaultCutAt       = "18:00"
	DefaultCutTimeZone = "UTC"
	DefaultCutWeekday  = "Friday"
)

// Reasons of a cadence.
const (
	// ReleaseWaitingForCut holds the first open release until its cut (C4).
	// It is no warning: the release goes out on its own at the cut.
	ReleaseWaitingForCut = "WaitingForCut"
	// ReasonInvalidTimeZone is a cadence whose time zone the operator can't
	// load (C1).
	ReasonInvalidTimeZone = "InvalidTimeZone"
)

// ReleaseCadence is how often a Crew's releases go out. Every is Release, Day
// or Week, and empty means Release; At (HH:MM, 24-hour), TimeZone (an IANA
// name) and Weekday (with Week) say when a cut is, and empty means their
// defaults.
type ReleaseCadence struct {
	Every    string
	At       string
	TimeZone string
	Weekday  string
}

// Cuts reports whether the cadence holds releases for a cut: Day or Week.
func (c ReleaseCadence) Cuts() bool {
	return c.Every == CadenceDay || c.Every == CadenceWeek
}

// Zone is the cadence's time zone name, UTC when unset.
func (c ReleaseCadence) Zone() string {
	if c.TimeZone == "" {
		return DefaultCutTimeZone
	}
	return c.TimeZone
}

// CheckCadence says why a cadence can't be used: a time zone the operator
// can't load, an at that isn't HH:MM, or a weekday that isn't one. The CRD
// checks the last two; the time zone only the operator can (C1).
func CheckCadence(c ReleaseCadence) error {
	if !c.Cuts() {
		return nil
	}
	_, _, _, _, err := c.parts()
	return err
}

var cutAt = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])$`)

// parts reads the cadence's location, hour, minute and weekday.
func (c ReleaseCadence) parts() (*time.Location, int, int, time.Weekday, error) {
	loc, err := time.LoadLocation(c.Zone())
	if err != nil || strings.TrimSpace(c.Zone()) != c.Zone() || strings.EqualFold(c.Zone(), "Local") {
		return nil, 0, 0, 0, fmt.Errorf("time zone %q is not one the operator can load; name an IANA time zone, "+
			"such as UTC or Europe/Amsterdam", c.Zone())
	}
	at := c.At
	if at == "" {
		at = DefaultCutAt
	}
	m := cutAt.FindStringSubmatch(at)
	if m == nil {
		return nil, 0, 0, 0, fmt.Errorf("at %q is not a time of day as HH:MM, such as 18:00", at)
	}
	hour, _ := strconv.Atoi(m[1])
	minute, _ := strconv.Atoi(m[2])
	name := c.Weekday
	if name == "" {
		name = DefaultCutWeekday
	}
	day := slices.IndexFunc(weekdays, func(d time.Weekday) bool { return d.String() == name })
	if day < 0 {
		return nil, 0, 0, 0, fmt.Errorf("weekday %q is not a day of the week, such as Friday", name)
	}
	return loc, hour, minute, weekdays[day], nil
}

var weekdays = []time.Weekday{time.Sunday, time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday,
	time.Saturday}

// NextCut is the cadence's first cut after lastCut, or after now when the
// Crew has cut none yet: at At in TimeZone, every day for Day, on Weekday for
// Week, with daylight saving as the time zone has it (C4). A cut on the very
// moment of lastCut is no later one, so a cut is never due twice. It fails
// for a time zone the operator can't load.
func NextCut(c ReleaseCadence, lastCut, now time.Time) (time.Time, error) {
	loc, hour, minute, weekday, err := c.parts()
	if err != nil {
		return time.Time{}, err
	}
	from := lastCut
	if from.IsZero() {
		from = now
	}
	y, m, d := from.In(loc).Date()
	for i := 0; ; i++ {
		cut := time.Date(y, m, d+i, hour, minute, 0, 0, loc)
		if !cut.After(from) || c.Every == CadenceWeek && cut.Weekday() != weekday {
			continue
		}
		return cut, nil
	}
}

// CutTime says when a cut is, for a message: "18:00 UTC" on now's day in the
// cadence's time zone, else with the day, "18:00 UTC on Friday".
func CutTime(c ReleaseCadence, cut, now time.Time) string {
	loc, err := time.LoadLocation(c.Zone())
	if err != nil {
		loc = time.UTC
	}
	local := cut.In(loc)
	at := local.Format("15:04") + " " + c.Zone()
	if y, m, d := now.In(loc).Date(); y == local.Year() && m == local.Month() && d == local.Day() {
		return at
	}
	return at + " on " + local.Weekday().String()
}

// LabelReleaseNow marks a merged issue that cuts its release at once, without
// waiting for the cut (C8).
const LabelReleaseNow = "release/now"

// ReleaseNowValue is the value of a plan issue's release: front matter that
// marks it bigger (C9).
const ReleaseNowValue = "now"

// ReleaseNowLabel is the label, for the Git host.
func ReleaseNowLabel() IssueLabel {
	return IssueLabel{LabelReleaseNow, "#e11d21",
		"Bigger: once merged, its release goes out at once instead of at the cadence's cut", false}
}

// IsReleaseNow reports whether an issue's labels mark it bigger (C8).
func IsReleaseNow(labels []string) bool {
	return slices.ContainsFunc(labels, func(l string) bool { return strings.TrimSpace(l) == LabelReleaseNow })
}

// cutMarkPrefix starts the line that marks a milestone cut, with the Crew's
// name, "/cut" and the cut's time after it.
const cutMarkPrefix = "<!-- theos-crew: "

// CutMarker is the line that marks a release milestone cut by Crew crew at at
// (C4): it takes no more work, and the time is the Crew's last cut.
func CutMarker(crew string, at time.Time) string {
	return fmt.Sprintf("%s%s/cut %s -->", cutMarkPrefix, crew, at.UTC().Format(time.RFC3339))
}

// CutMarkTime is the time of a cut mark on a line of description, by any
// Crew, and whether it has one.
func CutMarkTime(description string) (time.Time, bool) {
	for line := range strings.SplitSeq(description, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), cutMarkPrefix)
		if !ok {
			continue
		}
		rest, ok = strings.CutSuffix(rest, " -->")
		_, at, cut := strings.Cut(rest, "/cut ")
		if !ok || !cut {
			continue
		}
		if t, err := time.Parse(time.RFC3339, at); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// LastCut is a Crew's last cut: the one it recorded, or the newest cut mark
// among its milestones' descriptions when that is later, so a status write
// lost after a cut doesn't make the cut due again.
func LastCut(recorded time.Time, descriptions []string) time.Time {
	last := recorded
	for _, d := range descriptions {
		if t, ok := CutMarkTime(d); ok && t.After(last) {
			last = t
		}
	}
	return last
}

// CarryMarker is the line that ties a release milestone Crew crew made at a
// cut to the release it carries work on from (C6).
func CarryMarker(crew, from string) string {
	return fmt.Sprintf("%s%s/carry %s -->", cutMarkPrefix, crew, from)
}

// CarryMilestoneDescription is the description of the release milestone a
// Crew makes at the cut of release from; it ends with the carry marker.
func CarryMilestoneDescription(crew, from string) string {
	return fmt.Sprintf("The work the cut of %s carried on, with the work filed after it; released at the next "+
		"cut.\n\n%s\n", from, CarryMarker(crew, from))
}

// HasCarryMarker reports whether a milestone's description carries the carry
// marker of Crew crew's cut of release from.
func HasCarryMarker(description, crew, from string) bool {
	return hasMarkerLine(description, CarryMarker(crew, from))
}

// CarriedComment is the comment on an open issue a cut carries from release
// from into release into.
func CarriedComment(from, into string, cut ReleaseCadence) string {
	return fmt.Sprintf("Carried from %s to %s: %s was cut with the work merged by then (releases go out every %s), "+
		"and this issue goes out with %s.", from, into, from, strings.ToLower(cut.Every), into)
}

// ReleaseCut is the cut of a release (C4, C6, C7): mark its milestone cut,
// unless Marked says it is already, retitle it Retitle, when set, and carry
// its open issues Carry into milestone Into, or into a new one titled
// IntoTitle and due IntoDue when Into is 0. Untagged says the release has no
// merged work of its own while a later one has: once its issues moved, it
// closes untagged, unmarked, so it holds the later one no more.
type ReleaseCut struct {
	Marked    bool
	Retitle   string
	Carry     []int
	Into      int64
	IntoTitle string
	IntoDue   time.Time
	Untagged  bool
	// ReleaseNow says a merged release/now issue cuts it, not the time.
	ReleaseNow bool
}

// UpcomingCut is a Crew's next cut: when, and the release it cuts with how
// many merged issues; Release is empty while no release holds work.
type UpcomingCut struct {
	At      time.Time
	Release string
	Merged  int
}

// NextCutOf is the facts' next cut (C12): the release is the first open one
// with issues that isn't cut yet.
func NextCutOf(f ReleaseFacts) (UpcomingCut, error) {
	at, err := NextCut(f.Cadence, f.LastCut, f.Now)
	if err != nil {
		return UpcomingCut{}, err
	}
	next := UpcomingCut{At: at}
	releases, _ := sortedReleases(f.Milestones)
	merged := doneByChanges(f.Merged)
	for _, m := range releases {
		if !m.Cut {
			next.Release, next.Merged = m.Title, len(mergedWork(m, merged))
			break
		}
	}
	return next, nil
}

// cadenceStep is NextRelease under a cadence that cuts, on the releases with
// issues, in order, and the empty ones (C4 to C8). The first release that
// isn't cut waits for its cut, or for a merged release/now issue; at the cut
// it is cut when it, or a release after it, holds merged work. A cut release
// is carried out and retitled first, and then goes out as with every
// Release.
func cadenceStep(f ReleaseFacts, releases, empty []ReleaseMilestone) ReleaseStep {
	cutAt, err := NextCut(f.Cadence, f.LastCut, f.Now)
	if err != nil {
		title := ""
		if len(releases) > 0 {
			title = releases[0].Title
		}
		return ReleaseStep{Tag: title, Reason: ReasonInvalidTimeZone, Held: err.Error()}
	}
	f.Merged = doneByChanges(f.Merged)
	if len(releases) == 0 {
		return nothingToCut(f, cutAt)
	}
	first := releases[0]
	if first.Cut {
		if cut := cutLeft(f, first, releases, empty); cut != nil {
			return ReleaseStep{Tag: first.Title, Milestone: first.ID, Cut: cut}
		}
		return nextRelease(f, releases)
	}
	now := releaseNowMerged(f, releases)
	if f.Now.Before(cutAt) && !now {
		work := len(mergedWork(first, f.Merged))
		when := CutTime(f.Cadence, cutAt, f.Now)
		if work == 0 {
			return held(first.Title, ReleaseWaitingForCut, "%s holds no merged issue yet; its cut is at %s",
				first.Title, when)
		}
		return held(first.Title, ReleaseWaitingForCut, "%s holds %d merged %s and goes out at %s", first.Title,
			work, plural(work, "issue", "issues"), when)
	}
	withWork := slices.IndexFunc(releases, func(m ReleaseMilestone) bool { return len(mergedWork(m, f.Merged)) > 0 })
	switch {
	case withWork < 0:
		return nothingToCut(f, cutAt)
	case withWork > 0:
		// The release has nothing to ship: its issues go on, and the one
		// with work is cut as it would have been.
		cut := &ReleaseCut{Carry: carried(first), Untagged: true, ReleaseNow: now}
		carryInto(f, cut, first, first.Title, releases, empty)
		return ReleaseStep{Tag: first.Title, Milestone: first.ID, Cut: cut}
	}
	cut := &ReleaseCut{Carry: carried(first), Retitle: cutTitle(f, first), ReleaseNow: now}
	carryInto(f, cut, first, cmp.Or(cut.Retitle, first.Title), releases, empty)
	return ReleaseStep{Tag: first.Title, Milestone: first.ID, Cut: cut, CutAt: f.Now}
}

// nothingToCut is the step when no release holds merged work: once the cut's
// time passed, the cut counts as made, so work merged later waits for the
// next one rather than going out at once.
func nothingToCut(f ReleaseFacts, cutAt time.Time) ReleaseStep {
	if f.Now.Before(cutAt) {
		return ReleaseStep{}
	}
	return ReleaseStep{CutAt: f.Now}
}

// cutLeft is what is left of the cut of release first, marked cut already:
// its open issues to carry on, and its retitle until it is prepared; nil
// when nothing is.
func cutLeft(f ReleaseFacts, first ReleaseMilestone, releases, empty []ReleaseMilestone) *ReleaseCut {
	cut := &ReleaseCut{Marked: true, Carry: carried(first), Retitle: cutTitle(f, first)}
	if len(cut.Carry) == 0 && cut.Retitle == "" {
		return nil
	}
	carryInto(f, cut, first, cmp.Or(cut.Retitle, first.Title), releases, empty)
	return cut
}

// carried are the open issues a cut carries out of release m: all but its
// own, a prepare issue and an issue of its full check, which hold it as
// with every Release.
func carried(m ReleaseMilestone) []int {
	var out []int
	for _, n := range m.Open {
		if !slices.Contains(m.Prepared, n) && !m.fullCheckFix(n) {
			out = append(out, n)
		}
	}
	return out
}

// carryInto sets where the cut of release first carries its open issues: the
// next open release after it that can still take work, else a new one, the
// next minor after the newest version once first is titled title (C6).
func carryInto(f ReleaseFacts, cut *ReleaseCut, first ReleaseMilestone, title string,
	releases, empty []ReleaseMilestone) {
	if len(cut.Carry) == 0 {
		return
	}
	var into *ReleaseMilestone
	for _, list := range [][]ReleaseMilestone{releases, empty} {
		for i, m := range list {
			if m.ID == first.ID || m.Cut || len(m.Prepared) > 0 || !ReleaseBefore(first.Release, m.Release) {
				continue
			}
			if into == nil || ReleaseBefore(m.Release, into.Release) {
				into = &list[i]
			}
		}
	}
	if into != nil {
		cut.Into = into.ID
		return
	}
	titles := slices.DeleteFunc(slices.Clone(f.Titles), func(t string) bool { return t == first.Title })
	cut.IntoTitle = ReleaseMilestoneTitle(append(titles, title), false)
	open := make([]Release, 0, len(f.Milestones))
	for _, m := range f.Milestones {
		open = append(open, m.Release)
	}
	// The cut after this one: the facts' last cut is the one being made.
	next, err := NextCut(f.Cadence, f.Now, f.Now)
	if err != nil {
		next = f.Now.Add(ReleaseMilestoneDue)
	}
	cut.IntoDue = CadenceDue(open, next)
}

// cutTitle is the title release first takes at its cut, "" to keep its own
// (C7): when every issue a Task merged in it is a fix, the next patch after
// the previous release, skipping a title another milestone has. A release
// with no previous one, one with a new major, one tagged or prepared already,
// and one whose title is that patch or before it keep theirs.
func cutTitle(f ReleaseFacts, first ReleaseMilestone) string {
	work := mergedWork(first, f.Merged)
	if len(work) == 0 || f.Previous == nil || len(first.Prepared) > 0 ||
		slices.ContainsFunc(work, func(n int) bool { return !slices.Contains(f.Fixes, n) }) {
		return ""
	}
	if _, tagged := f.Tags[first.Title]; tagged {
		return ""
	}
	previous, current := parseVersion(f.Previous.Title), parseVersion(first.Title)
	if previous == nil || current == nil || previous[0] != current[0] {
		return ""
	}
	others := slices.DeleteFunc(slices.Clone(f.Titles), func(t string) bool { return t == first.Title })
	patch := slices.Clone(previous)
	for {
		patch[2]++
		if !slices.Contains(others, versionTitle(patch)) {
			break
		}
	}
	if compareVersions(patch, current) >= 0 {
		return ""
	}
	return versionTitle(patch)
}

func versionTitle(v []int) string { return fmt.Sprintf("v%d.%d.%d", v[0], v[1], v[2]) }

// releaseNowMerged reports whether an open release that isn't cut holds an
// issue labelled release/now that a Task merged (C8).
func releaseNowMerged(f ReleaseFacts, releases []ReleaseMilestone) bool {
	for _, m := range releases {
		if m.Cut {
			continue
		}
		if slices.ContainsFunc(mergedWork(m, f.Merged), func(n int) bool { return slices.Contains(f.ReleaseNow, n) }) {
			return true
		}
	}
	return false
}

// CadenceDue is when a release milestone made under a cadence is due: at
// next, the next cut, or an hour after the latest due date among the open
// releases, whichever is later, as its version is the newest (C6, P4).
func CadenceDue(open []Release, next time.Time) time.Time {
	due := next
	for _, r := range open {
		if parseVersion(r.Title) != nil && !r.Due.Before(due) {
			due = r.Due.Add(time.Hour)
		}
	}
	return due
}

// CadenceSlot is where a Brief's round puts every issue of a released
// Container under cadence c, at now (C5): the release in progress, the first
// open, dated version milestone that can still take work (no prepare issue,
// not cut), joined (its ID); or, when none is open, a new one titled the next
// minor after the newest version milestone, v0.1.0 when there is none, due
// at the next cut (CadenceDue). It fails for a time zone the operator can't
// load.
func CadenceSlot(milestones []ReleaseMilestone, c ReleaseCadence, now time.Time) (Slot, error) {
	var open []Release
	var titles []string
	var join *ReleaseMilestone
	for i, m := range milestones {
		titles = append(titles, m.Title)
		if parseVersion(m.Title) == nil || m.MilestoneClosed {
			continue
		}
		open = append(open, m.Release)
		if m.Due.IsZero() || m.Cut || len(m.Prepared) > 0 {
			continue
		}
		if join == nil || compareReleases(&m.Release, &join.Release) < 0 {
			join = &milestones[i]
		}
	}
	if join != nil {
		return Slot{ID: join.ID, Title: join.Title, Due: join.Due}, nil
	}
	next, err := NextCut(c, time.Time{}, now)
	if err != nil {
		return Slot{}, err
	}
	return Slot{Title: ReleaseMilestoneTitle(titles, false), Due: CadenceDue(open, next)}, nil
}

// CutReleaseOf is the release a Crew that prepares releases cut and hasn't
// tagged: the first open one with issues, once it is marked cut; "" for
// none, and with no prepare issue to wait for. Until it is tagged, a Task of
// an issue outside it waits at its merge (C10).
func CutReleaseOf(f ReleaseFacts) string {
	if !f.Cadence.Cuts() || !f.Prepare {
		return ""
	}
	releases, _ := sortedReleases(f.Milestones)
	if len(releases) == 0 || !releases[0].Cut {
		return ""
	}
	return releases[0].Title
}

// ReasonWaitingForRelease is a Task's, ready to merge while its Crew's cut
// release waits for its tag (C10). Such a Task runs no agent, so it takes
// no place from the Crew's parallel.
const ReasonWaitingForRelease = "WaitingForRelease"

// MergeWaitsForRelease reports whether a Task whose issue is in release
// issueRelease ("" for none) waits at its merge while its Crew's release
// cutRelease waits for its tag: unless it is of that release, a prepare
// issue or a fix of its full check, its merge would land under the tag and
// miss the changelog.
func MergeWaitsForRelease(cutRelease, issueRelease string) bool {
	return cutRelease != "" && issueRelease != cutRelease
}

// WaitingForReleaseMessage says why a Task ready to merge waits.
func WaitingForReleaseMessage(cutRelease string) string {
	return fmt.Sprintf("Approved; merging once %s, cut and being prepared, is tagged, so its changelog holds all "+
		"the tag carries", cutRelease)
}

// CutMessage says what the cut of step.Tag did, its open issues carried
// into release into.
func CutMessage(step ReleaseStep, into string) string {
	cut := step.Cut
	var b strings.Builder
	switch {
	case cut.Untagged:
		fmt.Fprintf(&b, "%s had no merged work: carried %s into %s and closed it untagged", step.Tag,
			issueList(cut.Carry), into)
		return b.String()
	case cut.Marked:
		fmt.Fprintf(&b, "Went on with the cut of %s", step.Tag)
	case cut.ReleaseNow:
		fmt.Fprintf(&b, "Cut %s at once for its merged %s work", step.Tag, LabelReleaseNow)
	default:
		fmt.Fprintf(&b, "Cut %s", step.Tag)
	}
	if cut.Retitle != "" {
		fmt.Fprintf(&b, ", retitled %s as its merged work is all fixes", cut.Retitle)
	}
	if len(cut.Carry) > 0 {
		fmt.Fprintf(&b, "; carried %s into %s", issueList(cut.Carry), into)
	}
	return b.String()
}

// issueList is "#3, #5".
func issueList(numbers []int) string {
	out := make([]string, 0, len(numbers))
	for _, n := range numbers {
		out = append(out, fmt.Sprintf("#%d", n))
	}
	return strings.Join(out, ", ")
}
