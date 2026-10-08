package model

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// An implementer that finds its issue can't be done yet (it waits on work
// that isn't merged or released) or can't be done in its sandbox stops on
// purpose: it ends its summary with a blocked note, a line starting
// BlockedPrefix that says what it waits on and why. The Task then fails at
// once, reason ReasonBlocked, with the agent's words.

// BlockedPrefix starts the line of an agent's summary that says it is blocked.
const BlockedPrefix = "Blocked:"

// ReasonFailed is a failed Task's reason when nothing more specific says why.
const ReasonFailed = "Failed"

// ReasonBlocked is the reason of a Task whose agent said it can't start its issue.
const ReasonBlocked = "Blocked"

// MaxBlockedNote is the longest blocked note, in bytes, a Task carries; a
// longer one is cut after its last whole sentence or word.
const MaxBlockedNote = 1000

// BlockedNote takes the blocked note from an agent's summary: everything
// after the first line starting "Blocked:" (Markdown emphasis around the
// prefix allowed), trimmed and at most MaxBlockedNote bytes. It is empty when
// the summary has no such line, or the line gives no reason.
func BlockedNote(summary string) string {
	lines := strings.Split(summary, "\n")
	for i, l := range lines {
		rest, ok := strings.CutPrefix(strings.TrimLeft(l, " \t*_>"), BlockedPrefix)
		if !ok {
			continue
		}
		rest = strings.TrimLeft(rest, "*_")
		note := strings.TrimSpace(strings.Join(append([]string{rest}, lines[i+1:]...), "\n"))
		return ClipSentences(note, MaxBlockedNote)
	}
	return ""
}

// NoChangeNoReason is a round's error when the agent changed nothing and wrote
// no blocked note.
const NoChangeNoReason = "the agent made no changes and gave no reason; " + blockedHint

// blockedHint tells the next round how to stop on purpose.
const blockedHint = "if the issue can't be done yet, say why in a line \"" + BlockedPrefix + " ...\""

// MaxSummaryEnd is the longest end of an agent's summary, in bytes, a
// no-change round's error carries.
const MaxSummaryEnd = 400

// NoChangeError is a round's error when the agent changed nothing and wrote no
// blocked note. It carries the end of the agent's summary, where an agent that
// stopped usually says why ("the test needs xvfb-run or a display"), and is
// NoChangeNoReason when the summary is empty.
func NoChangeError(summary string) string {
	end := ClipWordsFromEnd(strings.Join(strings.Fields(summary), " "), MaxSummaryEnd)
	if end == "" {
		return NoChangeNoReason
	}
	return "the agent made no changes and wrote no \"" + BlockedPrefix + "\" line; its summary ends: " +
		strconv.Quote(end) + "; " + blockedHint
}

// ClipWordsFromEnd keeps the end of s, at most n bytes: from the first whole
// word that fits, after "…".
func ClipWordsFromEnd(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	const ellipsis = "…"
	if n <= len(ellipsis) {
		return ""
	}
	start := len(s) - (n - len(ellipsis))
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	if s[start-1] != ' ' && s[start-1] != '\n' {
		if i := strings.IndexAny(s[start:], " \n"); i >= 0 {
			start += i
		} else {
			start = len(s)
		}
	}
	tail := strings.TrimLeft(s[start:], " \n")
	if tail == "" {
		return ""
	}
	return ellipsis + tail
}

// BlockedMessage is a blocked Task's message: the agent's own words.
func BlockedMessage(note string) string {
	return "The agent can't start the issue: " + note
}
