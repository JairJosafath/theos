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

package model

import (
	"slices"
	"strings"
	"testing"
)

// #2037: a blocked note that says the agent can't see its issue is the
// runner's fault, not a blocker; a note on what the issue waits on is not one.
func TestIssueUnseen(t *testing.T) {
	tests := []struct {
		note string
		want bool
	}{
		{"the issue text for cel-lens-core-106 isn't available to me, so I can't tell what to build.", true},
		{"The issue text for cel-lens-core-106 isn’t available to me.", true},
		{"I can't see the issue: the prompt names cel-lens-core-106 but not what it asks.", true},
		{"I don't have access to the issue's description.", true},
		{"The task description was not provided in this session.", true},
		{"Issue #106 isn't available to me in this sandbox.", true},
		{"No issue text was provided, only that I stopped with a Blocked: line.", true},
		{"I cannot read the issue itself; only the send-back message reached me.", true},
		{"cel-lens#10 must be merged and released first.", false},
		{"the issue needs the engine's release, which isn't available yet.", false},
		{"The engine's 2.0 release isn't available to me: it is not published.", false},
		{"I can't find the issue it depends on, cel-lens#10, in the tracker.", false},
		{"I can't access the issue tracker of the engine to check its release.", false},
		{"needs network access to npm, which isn't provided in the sandbox.", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IssueUnseen(tt.note); got != tt.want {
			t.Errorf("IssueUnseen(%q) = %v, want %v", tt.note, got, tt.want)
		}
	}
}

// #2037: the first Task on an issue whose agent couldn't see it costs no try;
// any more do, so a runner that never shows the issue can't loop.
func TestTryCounter(t *testing.T) {
	var c TryCounter
	got := []bool{c.Costs(true, false), c.Costs(false, true), c.Costs(false, true), c.Costs(false, false)}
	if want := []bool{false, false, true, true}; !slices.Equal(got, want) {
		t.Errorf("Costs = %v, want %v", got, want)
	}
}

func TestBlockedNote(t *testing.T) {
	long := strings.Repeat("waiting on engine ", 100)
	tests := []struct {
		name, summary, want string
	}{
		{"absent", "Added the parser and its tests.", ""},
		{"empty summary", "", ""},
		{"present", "I changed nothing.\nBlocked: cel-lens#10 must be merged and released first.",
			"cel-lens#10 must be merged and released first."},
		{"runs to the end", "Nothing changed.\nBlocked: waits on the engine.\nThe release is not out yet.\n",
			"waits on the engine.\nThe release is not out yet."},
		{"first line wins", "Blocked: one\nBlocked: two", "one\nBlocked: two"},
		{"markdown emphasis", "**Blocked:** needs network access to npm", "needs network access to npm"},
		{"indented", "  Blocked: the sandbox has no GPU", "the sandbox has no GPU"},
		{"no reason", "Blocked:   \n", ""},
		{"only at line start", "This is not Blocked: by anything.", ""},
		{"lower case is prose", "blocked: maybe", ""},
		{"none", "Fixed the description.\nBlocked: none", ""},
		{"none in emphasis with a full stop", "**Blocked:** None.", ""},
		{"n/a", "Blocked: n/a\n", ""},
		{"nothing", "Blocked: nothing", ""},
		{"no", "Blocked: No", ""},
		{"dash", "Blocked: -", ""},
		{"emphasised none", "Blocked: *none*", ""},
		{"none then a real one", "Blocked: none\nBlocked: waits on #12", "waits on #12"},
		{"no blockers", "Blocked: No blockers.", ""},
		{"none with an exclamation mark", "Blocked: None!", ""},
		{"nothing blocks this issue", "Blocked: nothing blocks this issue", ""},
		{"none at all in emphasis", "**Blocked:** _none at all_.", ""},
		{"no then a reason", "Blocked: no release of the engine has the fix", "no release of the engine has the fix"},
		{"nothing then a reason", "Blocked: nothing until #12 is merged", "nothing until #12 is merged"},
		{"filler alone", "Blocked: this issue", "this issue"},
		{"none as a word of a reason", "Blocked: none of the engine's releases has the fix", "none of the engine's releases has the fix"},
		{"real reason", "Blocked: waits on #12", "waits on #12"},
		{"oversized", "Blocked: " + long, ClipSentences(long, MaxBlockedNote)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BlockedNote(tt.summary)
			if got != tt.want {
				t.Errorf("BlockedNote(%q) = %q, want %q", tt.summary, got, tt.want)
			}
			if len(got) > MaxBlockedNote {
				t.Errorf("note is %d bytes, more than %d", len(got), MaxBlockedNote)
			}
		})
	}
	if got := BlockedNote("Blocked: " + long); !strings.HasSuffix(got, "waiting…") {
		t.Errorf("oversized note is not cut after a whole word: %q", got[len(got)-20:])
	}
}

func TestClipWordsFromEnd(t *testing.T) {
	tests := []struct {
		name, s string
		n       int
		want    string
	}{
		{"fits", "  the whole summary  ", 40, "the whole summary"},
		{"at a word start", "one two three", len("…") + len("three"), "…three"},
		{"inside a word", "one two three", len("…") + len("wo three"), "…three"},
		{"after a space", "one two three", len("…") + len(" three"), "…three"},
		{"one long word", "abcdefghij", len("…") + 4, ""},
		{"no room", "one two three", 2, ""},
		{"multibyte", "naïve café ünïcode", len("…") + len("ïcode") + 1, ""},
		{"multibyte word", "naïve café ünïcode", len("…") + len("ünïcode"), "…ünïcode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClipWordsFromEnd(tt.s, tt.n)
			if got != tt.want {
				t.Errorf("ClipWordsFromEnd(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
			if len(got) > tt.n {
				t.Errorf("%q is %d bytes, more than %d", got, len(got), tt.n)
			}
		})
	}
}

func TestNoChangeError(t *testing.T) {
	if got := NoChangeError(" \n "); got != NoChangeNoReason {
		t.Errorf("empty summary: %q, want NoChangeNoReason", got)
	}
	got := NoChangeError("Wrote the integration test.\n\nIt can't run here:\nthe test needs xvfb-run or a display.")
	want := `the agent made no changes and wrote no "Blocked:" line; its summary ends: ` +
		`"Wrote the integration test. It can't run here: the test needs xvfb-run or a display."; ` +
		`if the issue can't be done yet, say why in a line "Blocked: ..."`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	long := strings.Repeat("filler words ", 100) + "so the test needs xvfb-run or a display."
	got = NoChangeError(long)
	if !strings.Contains(got, `its summary ends: "…`) || !strings.Contains(got, "needs xvfb-run or a display.\"") ||
		strings.Contains(got, `"…ords`) || strings.Contains(got, `"…iller`) {
		t.Errorf("long summary: %q", got)
	}
	if n := len(ClipWordsFromEnd(long, MaxSummaryEnd)); n > MaxSummaryEnd {
		t.Errorf("end is %d bytes, more than %d", n, MaxSummaryEnd)
	}
}
