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
	"strings"
	"testing"
)

// The UI's address for theos-chat's links: none, or an http(s) base the
// page's query can follow.
func TestChatUIURL(t *testing.T) {
	tests := []struct {
		raw, want string
		ok        bool
	}{
		{"", "", true},
		{"  ", "", true},
		{"http://localhost:8080", "http://localhost:8080", true},
		{"https://theos.example.com/", "https://theos.example.com", true},
		{"https://example.com/theos//", "https://example.com/theos", true},
		{"ftp://theos.example.com", "", false},
		{"javascript:alert(1)", "", false},
		{"theos.example.com", "", false},
		{"https://", "", false},
		{"https://ada:secret@theos.example.com", "", false},
		{"https://theos.example.com/?area=work", "", false},
		{"https://theos.example.com/?", "", false},
		{"https://theos.example.com/#top", "", false},
		{"https://theos example.com", "", false},
	}
	for _, tt := range tests {
		got, err := ChatUIURL(tt.raw)
		if got != tt.want || (err == nil) != tt.ok {
			t.Errorf("ChatUIURL(%q) = %q, %v; want %q, ok %t", tt.raw, got, err, tt.want, tt.ok)
		}
	}
}

// An object's page is the UI's ?ns=&kind=&name=, escaped; none without the
// UI's address.
func TestChatPageURL(t *testing.T) {
	if got := ChatPageURL("https://theos.example.com", "shop", "Task", "api-4"); got !=
		"https://theos.example.com/?kind=Task&name=api-4&ns=shop" {
		t.Errorf("page = %q", got)
	}
	if got := ChatPageURL("http://localhost:8080", "a&b", "Container", "x y"); got !=
		"http://localhost:8080/?kind=Container&name=x+y&ns=a%26b" {
		t.Errorf("an escaped page = %q", got)
	}
	for _, args := range [][4]string{{"", "shop", "Task", "api-4"}, {"https://t", "shop", "Task", ""},
		{"https://t", "shop", "", "api-4"}} {
		if got := ChatPageURL(args[0], args[1], args[2], args[3]); got != "" {
			t.Errorf("ChatPageURL%q = %q, want none", args, got)
		}
	}
}

// A Task's and a Container's posts link to their pages in the UI, after the
// Git host's links; without the UI's address they link as before.
func TestChatNewsLinksToTheUI(t *testing.T) {
	page := "https://theos.example.com/?kind=Task&name=api-6&ns=shop"
	failed := ChatTaskView{Name: "api-6", Kind: "Implement", Phase: "Failed", Issue: 6, IssueURL: "http://git/api/issues/6",
		Message: "Out of rounds", PageURL: page}
	n := newsByEvent(ChatTaskNews(failed))[ChatEventTaskFailed]
	if want := "Issue #6: http://git/api/issues/6\nTask in theos: " + page; !strings.HasSuffix(n.Post.Text(), want) {
		t.Errorf("a failed Task's post:\n%s\nwant it to end in\n%s", n.Post.Text(), want)
	}
	if html := n.Post.HTML(); !strings.Contains(html, `<a href="https://theos.example.com/?kind=Task&amp;name=api-6&amp;ns=shop">Task in theos</a>`) {
		t.Errorf("a failed Task's HTML: %s", html)
	}
	spec := ChatTaskView{Name: "api-4-spec", Kind: "Spec", Phase: "AwaitingApproval", Round: 1, Issue: 4,
		SpecApproval: "Person", PageURL: page}
	if n := newsByEvent(ChatTaskNews(spec))[ChatEventSpecWaiting]; !strings.Contains(n.Post.Text(), "Task in theos: "+page) {
		t.Errorf("a spec's thread:\n%s", n.Post.Text())
	}
	failed.PageURL = ""
	if text := newsByEvent(ChatTaskNews(failed))[ChatEventTaskFailed].Post.Text(); strings.Contains(text, "in theos") {
		t.Errorf("a post links to the UI without its address:\n%s", text)
	}

	cpage := "https://theos.example.com/?kind=Container&name=api&ns=shop"
	c := ChatContainerView{Name: "api", Phase: "Failed", RepositoryURL: "http://git/shop/api", PageURL: cpage,
		Deploys: []ChatDeployState{{Target: "prod", WantedVersion: "v1.2.0", Phase: "Failed", Message: "no image",
			URL: "https://api.example.com"}}}
	byEvent := newsByEvent(ChatContainerNews(c))
	if text := byEvent[ChatEventContainerFailed].Post.Text(); !strings.HasSuffix(text,
		"Repository: http://git/shop/api\nContainer in theos: "+cpage) {
		t.Errorf("a failed scaffold's thread:\n%s", text)
	}
	if text := byEvent[ChatEventDeployFailed].Post.Text(); !strings.HasSuffix(text,
		"prod: https://api.example.com\nContainer in theos: "+cpage) {
		t.Errorf("a failed deploy's post:\n%s", text)
	}
}
