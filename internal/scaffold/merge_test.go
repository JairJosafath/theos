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
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestMerge3(t *testing.T) {
	// lines joins its arguments, each ending a line.
	lines := func(l ...string) string { return strings.Join(l, "\n") + "\n" }
	base := lines("one", "two", "three", "four", "five")
	tests := []struct {
		name               string
		base, ours, theirs string
		want               string
		wantHunks          []Hunk
		wantErr            error
	}{
		{name: "edits in different places",
			base: base, ours: lines("one", "TWO", "three", "four", "five"),
			theirs: lines("one", "two", "three", "FOUR", "five"),
			want:   lines("one", "TWO", "three", "FOUR", "five")},
		{name: "a line added on one side, another changed on the other",
			base: base, ours: lines("zero", "one", "two", "three", "four", "five"),
			theirs: lines("one", "two", "three", "four", "FIVE"),
			want:   lines("zero", "one", "two", "three", "four", "FIVE")},
		{name: "lines removed on one side, changed on the other",
			base: base, ours: lines("three", "four", "five"),
			theirs: lines("one", "two", "three", "four", "FIVE", "six"),
			want:   lines("three", "four", "FIVE", "six")},
		{name: "only theirs changed",
			base: base, ours: base, theirs: lines("one", "two", "THREE", "four", "five"),
			want: lines("one", "two", "THREE", "four", "five")},
		{name: "only ours changed",
			base: base, ours: lines("one", "two", "THREE", "four", "five"), theirs: base,
			want: lines("one", "two", "THREE", "four", "five")},
		{name: "edits at the start and the end of the file",
			base: base, ours: lines("ONE", "two", "three", "four", "five"),
			theirs: lines("one", "two", "three", "four", "FIVE"),
			want:   lines("ONE", "two", "three", "four", "FIVE")},
		{name: "the same edit on both sides",
			base: base, ours: lines("one", "two", "THREE", "four", "five"),
			theirs: lines("one", "two", "THREE", "four", "five"),
			want:   lines("one", "two", "THREE", "four", "five")},
		{name: "the same edit on both sides and one more on ours",
			base: base, ours: lines("ONE", "two", "THREE", "four", "five"),
			theirs: lines("one", "two", "THREE", "four", "five"),
			want:   lines("ONE", "two", "THREE", "four", "five")},
		{name: "overlapping edits",
			base: base, ours: lines("one", "two", "ours", "four", "five"),
			theirs:    lines("one", "two", "theirs", "four", "five"),
			wantHunks: []Hunk{{Start: 2, End: 3, Ours: []string{"ours\n"}, Theirs: []string{"theirs\n"}}}},
		{name: "edits of neighbouring lines touch, as in git",
			base: base, ours: lines("one", "TWO", "three", "four", "five"),
			theirs: lines("one", "two", "THREE", "four", "five"),
			wantHunks: []Hunk{{Start: 1, End: 3,
				Ours: []string{"TWO\n", "three\n"}, Theirs: []string{"two\n", "THREE\n"}}}},
		{name: "both add different lines at the same place",
			base: base, ours: lines("one", "two", "ours", "three", "four", "five"),
			theirs:    lines("one", "two", "theirs", "three", "four", "five"),
			wantHunks: []Hunk{{Start: 2, End: 2, Ours: []string{"ours\n"}, Theirs: []string{"theirs\n"}}}},
		{name: "one overlap among clean edits",
			base: base, ours: lines("ONE", "two", "ours", "four", "five"),
			theirs:    lines("one", "two", "theirs", "four", "FIVE"),
			wantHunks: []Hunk{{Start: 2, End: 3, Ours: []string{"ours\n"}, Theirs: []string{"theirs\n"}}}},
		{name: "a missing final newline kept",
			base: "one\ntwo\nthree", ours: "ONE\ntwo\nthree", theirs: "one\ntwo\nTHREE",
			want: "ONE\ntwo\nTHREE"},
		{name: "a final newline added on one side, a line changed on the other",
			base: "one\ntwo\nthree", ours: "ONE\ntwo\nthree", theirs: "one\ntwo\nthree\n",
			want: "ONE\ntwo\nthree\n"},
		{name: "CRLF line endings kept",
			base: "one\r\ntwo\r\nthree\r\nfour\r\n", ours: "ONE\r\ntwo\r\nthree\r\nfour\r\n",
			theirs: "one\r\ntwo\r\nthree\r\nFOUR\r\n",
			want:   "ONE\r\ntwo\r\nthree\r\nFOUR\r\n"},
		{name: "an empty base",
			base: "", ours: "", theirs: lines("new"),
			want: lines("new")},
		{name: "a binary file refused",
			base: "one\x00", ours: "ONE\x00", theirs: "one\x00two", wantErr: ErrBinary},
		{name: "a binary side refused",
			base: base, ours: base, theirs: "\x89PNG\x00", wantErr: ErrBinary},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, hunks, err := Merge3([]byte(tt.base), []byte(tt.ours), []byte(tt.theirs))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Merge3() error = %v, want %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(hunks, tt.wantHunks) {
				t.Errorf("Merge3() hunks = %+v, want %+v", hunks, tt.wantHunks)
			}
			if tt.wantErr == nil && tt.wantHunks == nil && string(got) != tt.want {
				t.Errorf("Merge3() = %q, want %q", got, tt.want)
			}
			if tt.wantHunks != nil && got != nil {
				t.Errorf("Merge3() = %q with hunks, want no content", got)
			}
		})
	}
}

func TestMerge3TooLarge(t *testing.T) {
	var base, theirs strings.Builder
	for i := range 3000 {
		base.WriteString("base " + strings.Repeat("x", i%7) + "\n")
		theirs.WriteString("theirs\n")
	}
	if _, _, err := Merge3([]byte(base.String()), []byte(base.String()), []byte(theirs.String())); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Merge3() error = %v, want %v", err, ErrTooLarge)
	}
}

func TestMergeTheirs(t *testing.T) {
	lines := func(l ...string) string { return strings.Join(l, "\n") + "\n" }
	base := lines("one", "two", "three", "four", "five")
	tests := []struct {
		name               string
		base, ours, theirs string
		want               string
		wantHunks          []Hunk
		wantErr            error
	}{
		{name: "clean edits merge as in Merge3",
			base: base, ours: lines("one", "TWO", "three", "four", "five"),
			theirs: lines("one", "two", "three", "FOUR", "five"),
			want:   lines("one", "TWO", "three", "FOUR", "five")},
		{name: "an overlap takes theirs, the other edits of ours kept",
			base: base, ours: lines("ONE", "two", "ours", "four", "five"),
			theirs:    lines("one", "two", "theirs", "four", "FIVE"),
			want:      lines("ONE", "two", "theirs", "four", "FIVE"),
			wantHunks: []Hunk{{Start: 2, End: 3, Ours: []string{"ours\n"}, Theirs: []string{"theirs\n"}}}},
		{name: "lines ours removed where theirs changed come back as theirs",
			base: base, ours: lines("one", "two", "four", "five"),
			theirs:    lines("one", "two", "THREE", "four", "five"),
			want:      lines("one", "two", "THREE", "four", "five"),
			wantHunks: []Hunk{{Start: 2, End: 3, Ours: []string{}, Theirs: []string{"THREE\n"}}}},
		{name: "a binary file refused",
			base: "one\x00", ours: "ONE\x00", theirs: "one\x00two", wantErr: ErrBinary},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, hunks, err := MergeTheirs([]byte(tt.base), []byte(tt.ours), []byte(tt.theirs))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("MergeTheirs() error = %v, want %v", err, tt.wantErr)
			}
			if len(hunks) != len(tt.wantHunks) || len(hunks) > 0 && !reflect.DeepEqual(hunks, tt.wantHunks) {
				t.Errorf("MergeTheirs() hunks = %#v, want %#v", hunks, tt.wantHunks)
			}
			if tt.wantErr == nil && string(got) != tt.want {
				t.Errorf("MergeTheirs() = %q, want %q", got, tt.want)
			}
		})
	}
}
