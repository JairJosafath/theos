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
	"testing"
)

// TestChatSystems covers #152: Matrix is the one chat host, the default, and
// its row says what the Chat controller and theos-chat assumed of it before
// the row: a Space, the token under accessToken and nothing more, HTML.
func TestChatSystems(t *testing.T) {
	if got := ChatSystemNames(); !slices.Equal(got, []string{ChatMatrix}) {
		t.Errorf("ChatSystemNames() = %q, want matrix alone", got)
	}
	s, err := ChatSystemByName(ChatSystemDefault)
	if err != nil {
		t.Fatal(err)
	}
	want := ChatSystem{Name: "matrix", Spaces: true, TokenKey: "accessToken", Markup: "html"}
	if s != want {
		t.Errorf("ChatSystemByName(%q) = %+v, want %+v", ChatSystemDefault, s, want)
	}
	if _, err := ChatSystemByName("teams"); err == nil {
		t.Error("ChatSystemByName(teams) = nil error, want the host unknown")
	}
}

// TestChatSystemFormat covers #152: a post is formatted in the host's markup,
// and a host that reads plain text alone gets none.
func TestChatSystemFormat(t *testing.T) {
	p := ChatPost{Title: "Spec: dark mode", Status: "Waiting for approval"}
	for _, tt := range []struct {
		markup, want string
	}{
		{ChatMarkupHTML, p.HTML()},
		// #155: Slack's markup, which no row names yet.
		{ChatMarkupMrkdwn, p.Mrkdwn()},
		{"", ""},
		{"unknown", ""},
	} {
		if got := (ChatSystem{Markup: tt.markup}).Format(p); got != tt.want {
			t.Errorf("Format() with markup %q = %q, want %q", tt.markup, got, tt.want)
		}
	}
}
