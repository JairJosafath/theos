package model

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// A Container whose checks are BeforeRelease runs its full check only on the
// commit a release will be tagged at, before the tag
// (plans/checks-before-release.md): its Crew pushes the branch
// FullCheckBranch there and tags once the runs that push started end well.

// FullCheckBranch is the branch whose push runs release tag's full check.
func FullCheckBranch(tag string) string { return "release-check/" + tag }

// Reasons of a release its full check holds.
const (
	// FullCheckRunning: the full check runs, or waits for a runner.
	FullCheckRunning = "FullCheckRunning"
	// FullCheckFailed: the full check failed, so the release isn't tagged and
	// an issue to fix it is filed in it.
	FullCheckFailed = "FullCheckFailed"
	// NoFullCheck: no run started within ReleaseRunBound, so a person tags it.
	NoFullCheck = "NoFullCheck"
)

// What a Crew does about a release's full check.
const (
	FullCheckTag   = "Tag"
	FullCheckWait  = "Wait"
	FullCheckFile  = "File"
	FullCheckLeave = "Leave"
)

// FullCheckGate decides what a Crew does about a release whose full check it
// started at started (it pushed the branch, or found it there), from runs, the
// runs that push started, newest first: tag once they all ended well, file an
// issue as soon as one failed, wait while one hasn't ended, and leave the
// release to a person when none started within ReleaseRunBound, as with no
// runner or a workflow that has no full check. A run that started keeps it
// waiting however long it takes. The run returned is the failed one, else the
// newest.
func FullCheckGate(runs []ReleaseRun, started, now time.Time) (string, ReleaseRun) {
	result, run := ReleaseRunResult(runs)
	switch result {
	case RunSucceeded:
		return FullCheckTag, run
	case RunFailed:
		return FullCheckFile, run
	case RunWaiting:
		if now.Sub(started) >= ReleaseRunBound {
			return FullCheckLeave, run
		}
	}
	return FullCheckWait, run
}

// FullCheckFix is an issue a Crew filed when a release's full check failed
// at Commit.
type FullCheckFix struct {
	Issue  int
	Commit string
}

// fullCheckFix reports whether issue n is one of the release's full check fixes.
func (m ReleaseMilestone) fullCheckFix(n int) bool {
	return slices.ContainsFunc(m.FullChecks, func(f FullCheckFix) bool { return f.Issue == n })
}

// FullCheckFiled reports whether the release has the issue of its full check
// failing at commit already, open or closed: it is filed once per commit.
func FullCheckFiled(m ReleaseMilestone, commit string) bool {
	return slices.ContainsFunc(m.FullChecks, func(f FullCheckFix) bool { return f.Commit == commit })
}

// fullCheckMarker marks the issue a Crew files when a release's full check
// fails, with the release and the commit it failed at.
var fullCheckMarker = regexp.MustCompile(`<!-- c4-full-check (\S+) (\S+) -->`)

// FullCheckCommit returns the commit the issue with body says release tag's
// full check failed at, and whether it is such an issue.
func FullCheckCommit(tag, body string) (string, bool) {
	for _, m := range fullCheckMarker.FindAllStringSubmatch(body, -1) {
		if m[1] == tag {
			return m[2], true
		}
	}
	return "", false
}

// FullCheckIssue renders the issue a Crew files in release tag when its full
// check fails at commit; run is the failed run.
func FullCheckIssue(tag, commit string, run ReleaseRun) (title, body string) {
	var b strings.Builder
	fmt.Fprintf(&b, "The full check of release %s failed at %s, so %s is not tagged: %s ended %s, %s\n\n",
		tag, commit, tag, run.Workflow, run.Conclusion, run.URL)
	fmt.Fprintf(&b, "The full check is `make check`, run on the branch `%s` at the commit the tag will name. "+
		"Once this issue merges, %s is done again and its full check runs at the new merge.\n\n",
		FullCheckBranch(tag), tag)
	b.WriteString("## Acceptance criteria\n\n")
	b.WriteString("- [ ] `make check` passes: what made the run above fail is fixed\n")
	b.WriteString("- [ ] The changelog doesn't change: it is the release's prepare issue's\n")
	fmt.Fprintf(&b, "\n<!-- c4-full-check %s %s -->\n", tag, commit)
	return "Full check fails for " + tag, b.String()
}

// FullCheckMessage says what release tag's full check at commit is doing, for
// the Crew's Released condition: reason is one of the FullCheck reasons.
func FullCheckMessage(reason, tag, commit string, run ReleaseRun, issue int) string {
	switch reason {
	case FullCheckFailed:
		message := fmt.Sprintf("The full check of %s failed at %s: %s", tag, commit, run.URL)
		if issue != 0 {
			message += fmt.Sprintf("; #%d fixes it, and %s is tagged once it merges and the check passes", issue, tag)
		}
		return message
	case NoFullCheck:
		return fmt.Sprintf("No full check of %s started at %s within an hour of pushing %s; a person checks and "+
			"tags it", tag, commit, FullCheckBranch(tag))
	}
	message := fmt.Sprintf("%s waits for its full check at %s on %s", tag, commit, FullCheckBranch(tag))
	if run.URL != "" {
		message += ": " + run.URL
	}
	return message
}
