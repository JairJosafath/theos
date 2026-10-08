package model

import "strings"

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

// notBlocked are the reasons, in lower case, that say nothing blocks the issue.
var notBlocked = map[string]bool{"none": true, "n/a": true, "na": true, "nothing": true, "no": true, "-": true}

// saysNotBlocked reports whether a blocked line's reason says nothing blocks
// the issue, in any case, with or without a full stop or Markdown emphasis.
func saysNotBlocked(reason string) bool {
	r := strings.Trim(strings.TrimSpace(reason), "*_`")
	r = strings.Trim(strings.TrimRight(r, "."), "*_`")
	return notBlocked[strings.ToLower(strings.TrimSpace(r))]
}

// NoChangeNoReason is a round's error when the agent changed nothing and wrote
// no blocked note.
const NoChangeNoReason = "the agent made no changes and gave no reason; if the issue can't be done yet, " +
	"say why in a line \"" + BlockedPrefix + " ...\""

// BlockedMessage is a blocked Task's message: the agent's own words.
func BlockedMessage(note string) string {
	return "The agent can't start the issue: " + note
}
