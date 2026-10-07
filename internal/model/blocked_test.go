package model

import (
	"strings"
	"testing"
)

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
