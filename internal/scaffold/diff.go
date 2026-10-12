package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// diffContext is how many unchanged lines a unified diff shows around each change.
const diffContext = 3

// FileChange is what one path's content and mode did between two renders.
type FileChange struct {
	// Diff is a unified diff of the two contents, without its --- and +++
	// header: @@ hunk headers, then lines marked " ", "-" or "+", each without
	// its line ending; "\ No newline at end of file" follows a line that has
	// none. Nil when no diff is shown.
	Diff []string
	// Note says what the change did to the file as a whole, or why no diff is
	// shown, or "".
	Note string
}

// StandardChange is what the Standard changed in path between its render base
// and its render next, as a unified diff with three lines of context. A path
// only one render holds shows as all added or all removed; a binary file, one
// too large to diff, or one that only changed its executable bit gets a note
// and no diff.
func StandardChange(base, next Tree, path string) FileChange {
	old, inBase := base[path]
	nu, inNext := next[path]
	var notes []string
	switch {
	case !inBase && !inNext, same(base, next, path):
		return FileChange{Note: "The Standard didn't change it."}
	case !inBase:
		notes = append(notes, "The Standard adds this file.")
	case !inNext:
		notes = append(notes, "The Standard removes this file.")
	case old.Executable != nu.Executable:
		if nu.Executable {
			notes = append(notes, "The Standard makes it executable.")
		} else {
			notes = append(notes, "The Standard makes it no longer executable.")
		}
	}
	if bytes.Equal(old.Data, nu.Data) {
		return FileChange{Note: strings.Join(notes, " ")}
	}
	diff, err := unifiedDiff(old.Data, nu.Data)
	switch {
	case errors.Is(err, ErrBinary):
		notes = append(notes, "It is a binary file, so no diff is shown.")
	case errors.Is(err, ErrTooLarge):
		notes = append(notes, "It changed too many lines to show a diff.")
	case endingsOnly(diff):
		notes = append(notes, "Some lines changed only their line endings (CRLF and LF).")
	}
	return FileChange{Diff: diff.lines(), Note: strings.Join(notes, " ")}
}

// diffLine is one line of a unified diff: its mark and the line with its ending.
type diffLine struct {
	mark byte
	line string
}

type unified []diffLine

// unifiedDiff is the unified diff that makes side of base, hunk headers marked '@'.
func unifiedDiff(base, side []byte) (unified, error) {
	for _, data := range [][]byte{base, side} {
		if bytes.IndexByte(data[:min(len(data), binaryPeek)], 0) >= 0 {
			return nil, ErrBinary
		}
	}
	b, s := splitLines(base), splitLines(side)
	edits, err := diffLines(b, s)
	if err != nil {
		return nil, err
	}
	var out unified
	for i := 0; i < len(edits); {
		// A hunk holds every edit whose context touches the one before.
		j := i + 1
		for j < len(edits) && edits[j].start-edits[j-1].end <= 2*diffContext {
			j++
		}
		first, last := edits[i], edits[j-1]
		lo, hi := max(0, first.start-diffContext), min(len(b), last.end+diffContext)
		sideLo, sideHi := first.sideStart-(first.start-lo), last.sideEnd+(hi-last.end)
		out = append(out, diffLine{'@', fmt.Sprintf("@@ -%s +%s @@", hunkRange(lo, hi-lo), hunkRange(sideLo, sideHi-sideLo))})
		pos := lo
		for _, e := range edits[i:j] {
			for _, line := range b[pos:e.start] {
				out = append(out, diffLine{' ', line})
			}
			for _, line := range b[e.start:e.end] {
				out = append(out, diffLine{'-', line})
			}
			for _, line := range s[e.sideStart:e.sideEnd] {
				out = append(out, diffLine{'+', line})
			}
			pos = e.end
		}
		for _, line := range b[pos:hi] {
			out = append(out, diffLine{' ', line})
		}
		i = j
	}
	return out, nil
}

// hunkRange is one side of a hunk header, from its zero-based start and its
// length, as diff -u writes it: an empty side names the line before it.
func hunkRange(start, n int) string {
	if n == 0 {
		return fmt.Sprintf("%d,0", start)
	}
	if n == 1 {
		return fmt.Sprintf("%d", start+1)
	}
	return fmt.Sprintf("%d,%d", start+1, n)
}

// lines writes the diff as text lines, without endings.
func (d unified) lines() []string {
	var out []string
	for _, l := range d {
		if l.mark == '@' {
			out = append(out, l.line)
			continue
		}
		out = append(out, string(l.mark)+strings.TrimSuffix(strings.TrimSuffix(l.line, "\n"), "\r"))
		if !strings.HasSuffix(l.line, "\n") {
			out = append(out, `\ No newline at end of file`)
		}
	}
	return out
}

// endingsOnly reports whether a line the diff removes comes back added with
// only CRLF for LF or LF for CRLF, which the written diff doesn't show.
func endingsOnly(d unified) bool {
	for i, l := range d {
		if l.mark != '+' || !strings.HasSuffix(l.line, "\n") {
			continue
		}
		bare := strings.TrimSuffix(strings.TrimSuffix(l.line, "\n"), "\r")
		// The lines the stretch before l removes.
		for k := i - 1; k >= 0 && d[k].mark != '@' && d[k].mark != ' '; k-- {
			if d[k].mark == '-' && d[k].line != l.line && (d[k].line == bare+"\n" || d[k].line == bare+"\r\n") {
				return true
			}
		}
	}
	return false
}
