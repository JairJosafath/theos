/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package scaffold

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestStandardChange(t *testing.T) {
	text := func(s string) File { return File{Data: []byte(s)} }
	// numbered is lines first to last, "1\n" to "<last>\n".
	numbered := func(first, last int) string {
		var b strings.Builder
		for n := first; n <= last; n++ {
			fmt.Fprintf(&b, "%d\n", n)
		}
		return b.String()
	}
	tests := []struct {
		name       string
		base, next Tree
		wantDiff   []string
		wantNote   string
	}{
		{
			name:     "a changed line, with its context",
			base:     Tree{"Makefile": text("check:\n\ttrue\n")},
			next:     Tree{"Makefile": text("check:\n\tmake lint test\n")},
			wantDiff: []string{"@@ -1,2 +1,2 @@", " check:", "-\ttrue", "+\tmake lint test"},
		},
		{
			name: "changes far apart are two hunks, three lines of context each",
			base: Tree{"f": text(numbered(1, 20))},
			next: Tree{"f": text(strings.Replace(strings.Replace(numbered(1, 20), "2\n", "two\n", 1), "18\n", "eighteen\n", 1))},
			wantDiff: []string{"@@ -1,5 +1,5 @@", " 1", "-2", "+two", " 3", " 4", " 5",
				"@@ -15,6 +15,6 @@", " 15", " 16", " 17", "-18", "+eighteen", " 19", " 20"},
		},
		{
			name:     "changes six lines apart share a hunk",
			base:     Tree{"f": text(numbered(1, 9))},
			next:     Tree{"f": text("one\n" + numbered(2, 7) + "eight\n9\n")},
			wantDiff: []string{"@@ -1,9 +1,9 @@", "-1", "+one", " 2", " 3", " 4", " 5", " 6", " 7", "-8", "+eight", " 9"},
		},
		{
			name:     "lines added at the end",
			base:     Tree{"f": text("a\n")},
			next:     Tree{"f": text("a\nb\nc\n")},
			wantDiff: []string{"@@ -1 +1,3 @@", " a", "+b", "+c"},
		},
		{
			name:     "an added file",
			base:     Tree{},
			next:     Tree{"f": text("a\nb\n")},
			wantDiff: []string{"@@ -0,0 +1,2 @@", "+a", "+b"},
			wantNote: "The Standard adds this file.",
		},
		{
			name:     "a removed file",
			base:     Tree{"f": text("a\n")},
			next:     Tree{},
			wantDiff: []string{"@@ -1 +0,0 @@", "-a"},
			wantNote: "The Standard removes this file.",
		},
		{
			name:     "a binary file",
			base:     Tree{"logo.png": text("\x89PNG\x00a")},
			next:     Tree{"logo.png": text("\x89PNG\x00b")},
			wantNote: "It is a binary file, so no diff is shown.",
		},
		{
			name:     "only the executable bit",
			base:     Tree{"run.sh": text("echo\n")},
			next:     Tree{"run.sh": {Data: []byte("echo\n"), Executable: true}},
			wantNote: "The Standard makes it executable.",
		},
		{
			name:     "the executable bit and the content",
			base:     Tree{"run.sh": {Data: []byte("echo\n"), Executable: true}},
			next:     Tree{"run.sh": text("echo hi\n")},
			wantDiff: []string{"@@ -1 +1 @@", "-echo", "+echo hi"},
			wantNote: "The Standard makes it no longer executable.",
		},
		{
			name:     "CRLF lines show without their CR",
			base:     Tree{"f": text("a\r\nb\r\n")},
			next:     Tree{"f": text("a\r\nc\r\n")},
			wantDiff: []string{"@@ -1,2 +1,2 @@", " a", "-b", "+c"},
		},
		{
			name:     "a change of line endings only is noted",
			base:     Tree{"f": text("a\r\nb\r\n")},
			next:     Tree{"f": text("a\nb\n")},
			wantDiff: []string{"@@ -1,2 +1,2 @@", "-a", "-b", "+a", "+b"},
			wantNote: "Some lines changed only their line endings (CRLF and LF).",
		},
		{
			name:     "no final newline",
			base:     Tree{"f": text("a\nb")},
			next:     Tree{"f": text("a\nb\n")},
			wantDiff: []string{"@@ -1,2 +1,2 @@", " a", "-b", `\ No newline at end of file`, "+b"},
		},
		{
			name:     "unchanged",
			base:     Tree{"f": text("a\n")},
			next:     Tree{"f": text("a\n")},
			wantNote: "The Standard didn't change it.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.base.Paths()
			if len(p) == 0 {
				p = tt.next.Paths()
			}
			got := StandardChange(tt.base, tt.next, p[0])
			if !slices.Equal(got.Diff, tt.wantDiff) {
				t.Errorf("Diff =\n%s\nwant\n%s", strings.Join(got.Diff, "\n"), strings.Join(tt.wantDiff, "\n"))
			}
			if got.Note != tt.wantNote {
				t.Errorf("Note = %q, want %q", got.Note, tt.wantNote)
			}
		})
	}
}

func TestStandardChangeTooLarge(t *testing.T) {
	var a, b strings.Builder
	for n := range 3000 {
		fmt.Fprintf(&a, "a%d\n", n)
		fmt.Fprintf(&b, "b%d\n", n)
	}
	got := StandardChange(Tree{"f": {Data: []byte(a.String())}}, Tree{"f": {Data: []byte(b.String())}}, "f")
	if got.Diff != nil || got.Note != "It changed too many lines to show a diff." {
		t.Errorf("StandardChange of a file too large to diff = %d lines, note %q", len(got.Diff), got.Note)
	}
}
