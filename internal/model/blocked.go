package model

import (
	"regexp"
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
// the summary has no such line, or the line gives no reason. A line whose
// reason says nothing blocks the issue ("Blocked: none") is not a blocked
// note: it is skipped as if it weren't there.
func BlockedNote(summary string) string {
	lines := strings.Split(summary, "\n")
	for i, l := range lines {
		rest, ok := strings.CutPrefix(strings.TrimLeft(l, " \t*_>"), BlockedPrefix)
		if !ok {
			continue
		}
		rest = strings.TrimLeft(rest, "*_")
		if saysNotBlocked(rest) {
			continue
		}
		note := strings.TrimSpace(strings.Join(append([]string{rest}, lines[i+1:]...), "\n"))
		return ClipSentences(note, MaxBlockedNote)
	}
	return ""
}

// notBlocked are the first words, in lower case, of a reason that says
// nothing blocks the issue.
var notBlocked = map[string]bool{"none": true, "n/a": true, "na": true, "nothing": true, "no": true, "-": true}

// notBlockedFiller are the words, in lower case, that may follow a notBlocked
// first word without saying what blocks the issue ("No blockers.", "nothing
// blocks this issue").
var notBlockedFiller = map[string]bool{
	"block": true, "blocks": true, "blocked": true, "blocking": true, "blocker": true, "blockers": true,
	"this": true, "the": true, "issue": true, "task": true, "it": true, "me": true,
	"here": true, "at": true, "all": true, "for": true, "now": true, "currently": true,
}

// saysNotBlocked reports whether a blocked line's reason says nothing blocks
// the issue, in any case, with or without punctuation or Markdown emphasis:
// its first word is one of notBlocked and every other word is filler
// ("None!", "No blockers."). A reason that goes on to say something ("none
// of the engine's releases has the fix") is a blocked note.
func saysNotBlocked(reason string) bool {
	var words []string
	for w := range strings.FieldsSeq(strings.ToLower(reason)) {
		if w = strings.Trim(w, ".,!?;:*_`'\"()"); w != "" {
			words = append(words, w)
		}
	}
	if len(words) == 0 || !notBlocked[words[0]] {
		return false
	}
	for _, w := range words[1:] {
		if !notBlockedFiller[w] {
			return false
		}
	}
	return true
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

// An agent that says, in its blocked note, that it can't see its issue was
// not given what the factory owes it: the issue's text is in its prompt and
// in THEOS_GOAL (#2037). That is the runner's fault, not the issue's, so
// the Task fails reason ReasonIssueUnseen, not ReasonBlocked, and the first
// MaxFreeUnseen such Tasks on an issue cost it no try (TryCounter).

// ReasonIssueUnseen is the reason of a Task whose agent's blocked note says
// it can't see the issue.
const ReasonIssueUnseen = "IssueUnseen"

// MaxFreeUnseen is how many Tasks on one issue whose agent couldn't see the
// issue cost it no try. More is no passing fault, so they count: a runner
// that never shows the issue can't start Tasks on it without end.
const MaxFreeUnseen = 1

// issueUnseenSigns are what a blocked note says when the agent can't see its
// issue, in lower case with straight apostrophes. They are narrow on purpose:
// the subject is the issue (or task, ticket, goal) itself or its text, and the
// words say the agent can't see, read or have it, or that it isn't given or
// available to the agent. A note on something the issue waits on ("the issue
// needs the engine's release, which isn't available yet") is no such sign.
var issueUnseenSigns = func() []*regexp.Regexp {
	const (
		thing = `(?:issue|task|ticket|goal)`
		text  = `(?:text|body|description|contents?|details|statement)`
		subj  = `(?:the |this |its |your |my )?` + thing + `(?:'s)?(?: ` + text + `)?`
		// what may follow the subject: the end of a clause, or what it is of
		after   = `(?:\s+(?:for|of|itself|here)\b|\s+in (?:the|this|my) (?:prompt|sandbox|environment|context|workspace)|\s*[#.,;:)!]|$)`
		cannot  = `(?:can't|cannot|can not|couldn't|could not|don't|do not|didn't|did not|am unable to|was unable to|wasn't able to|have no way to)`
		see     = `(?:see|access|read|view|have|get|receive|find)`
		isnt    = `(?:is|was|are|were)(?:n't| not)`
		given   = `(?:available|provided|included|given|visible|shown|present|accessible|passed|attached)`
		between = `[^.;]{0,80}?`
	)
	return []*regexp.Regexp{
		// "I can't see the issue", "I don't have access to the issue's text"
		regexp.MustCompile(`\b` + cannot + ` ` + see + `(?: access to| any)? ` + subj + after),
		// "the issue text for cel-lens-core-106 isn't available"
		regexp.MustCompile(`\b` + thing + `(?:'s)? ` + text + `\b` + between + `\b` + isnt + ` ` + given + `\b`),
		// "issue #106 isn't available to me"
		regexp.MustCompile(`\b` + thing + `\b` + between + `\b` + isnt + ` ` + given + ` to (?:me|the agent|this session)\b`),
		// "no issue text was provided"
		regexp.MustCompile(`\bno ` + thing + `(?: ` + text + `)? (?:was |is |has been )?` + given + `\b`),
	}
}()

// IssueUnseen reports whether a blocked note says the agent can't see its
// issue (issueUnseenSigns).
func IssueUnseen(note string) bool {
	s := strings.Join(strings.Fields(strings.ToLower(strings.ReplaceAll(note, "’", "'"))), " ")
	for _, sign := range issueUnseenSigns {
		if sign.MatchString(s) {
			return true
		}
	}
	return false
}

// IssueUnseenMessage is the message of a Task whose agent couldn't see the
// issue: the agent's own words.
func IssueUnseenMessage(note string) string {
	return "The agent says it can't see the issue, which the factory gave it: " + note
}

// TryCounter decides which of an issue's failed Tasks, oldest first, cost the
// issue a try: not one that failed through its CI runner's fault
// (RunnerFault), nor the first MaxFreeUnseen whose agent couldn't see the
// issue (ReasonIssueUnseen). Its zero value is ready for one issue.
type TryCounter struct{ unseen int }

// Costs reports whether a failed Task costs the issue a try: noCI when it
// failed through its CI runner's fault, unseen when its agent couldn't see
// the issue.
func (c *TryCounter) Costs(noCI, unseen bool) bool {
	switch {
	case noCI:
		return false
	case unseen && c.unseen < MaxFreeUnseen:
		c.unseen++
		return false
	}
	return true
}
