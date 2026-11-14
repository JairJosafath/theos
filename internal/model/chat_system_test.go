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

// TestChatSystems covers #152 and #159: Matrix is the default, and its row
// says what the Chat controller and theos-chat assumed of it before the
// row: a Space, the token under accessToken and nothing more, HTML. Slack's
// has no Space, the bot token and the app-level token, and mrkdwn
// (docs/plans/requests-and-chat.md, decisions 5 and 9).
func TestChatSystems(t *testing.T) {
	if got := ChatSystemNames(); !slices.Equal(got, []string{ChatMatrix, ChatSlack}) {
		t.Errorf("ChatSystemNames() = %q, want matrix and slack", got)
	}
	for _, tt := range []struct {
		name string
		want ChatSystem
		keys []string
	}{
		{ChatSystemDefault, ChatSystem{Name: "matrix", Spaces: true, TokenKey: "accessToken", Markup: "html",
			People: "spec.people", AddressFlag: "--chat-homeserver"}, []string{"accessToken"}},
		{ChatSlack, ChatSystem{Name: "slack", Spaces: false, TokenKey: "botToken", AppTokenKey: "appToken",
			Markup: "mrkdwn", People: "spec.slack.people", AddressFlag: "--chat-slack-api-url"},
			[]string{"botToken", "appToken"}},
	} {
		s, err := ChatSystemByName(tt.name)
		if err != nil {
			t.Fatal(err)
		}
		if s != tt.want {
			t.Errorf("ChatSystemByName(%q) = %+v, want %+v", tt.name, s, tt.want)
		}
		if got := s.Keys(); !slices.Equal(got, tt.keys) {
			t.Errorf("ChatSystemByName(%q).Keys() = %q, want %q", tt.name, got, tt.keys)
		}
	}
	if _, err := ChatSystemByName("teams"); err == nil {
		t.Error("ChatSystemByName(teams) = nil error, want the host unknown")
	}
}

// TestChatHostName covers #158: a Chat that names no host is on the default,
// Matrix, as one written before spec.host is; one that names a host is on
// it.
func TestChatHostName(t *testing.T) {
	for host, want := range map[string]string{"": ChatMatrix, ChatMatrix: ChatMatrix, ChatSlack: ChatSlack} {
		if got := ChatHostName(host); got != want {
			t.Errorf("ChatHostName(%q) = %q, want %q", host, got, want)
		}
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
		// #155: Slack's markup.
		{ChatMarkupMrkdwn, p.Mrkdwn()},
		{"", ""},
		{"unknown", ""},
	} {
		if got := (ChatSystem{Markup: tt.markup}).Format(p); got != tt.want {
			t.Errorf("Format() with markup %q = %q, want %q", tt.markup, got, tt.want)
		}
	}
	// #159: Slack's row writes a post in mrkdwn, the three characters
	// escaped, and Matrix's in HTML.
	q := ChatPost{Title: "Spec: a < b & c", Status: "Waiting"}
	for name, want := range map[string]string{ChatSlack: "*Spec: a &lt; b &amp; c*\nWaiting",
		ChatMatrix: "<p><b>Spec: a &lt; b &amp; c</b><br>Waiting</p>"} {
		s, err := ChatSystemByName(name)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Format(q); got != want {
			t.Errorf("the %s row's Format() = %q, want %q", name, got, want)
		}
	}
}
