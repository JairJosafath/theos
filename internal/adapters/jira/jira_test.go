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

package jira

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/adapters/jira/jiratest"
	"github.com/JairJosafath/theos/internal/ports"
)

// The text of the items and comments these tests file and look for.
const (
	titleDarkMode = "Dark mode"
	titleExport   = "Export to CSV"
	marker        = "<!-- theos:request stage=planned -->"
	answer        = "Planned as: dark mode\n\n" + marker
	ada           = "Ada Lovelace"
)

// site is a fake Jira Cloud with the project, the account the factory speaks
// as and its tracker.
type site struct {
	*jiratest.Server
	creds jiratest.Credentials
	tr    ports.IssueTracker
}

func newSite(t *testing.T, change ...func(*Config)) site {
	t.Helper()
	s := jiratest.New(t)
	s.Project(testProject)
	creds := s.Account(testAccount)
	return site{Server: s, creds: creds, tr: tracker(t, s, testProject, creds, change...)}
}

// file files an item that carries the label, made by ada, and returns its
// number.
func (s site) file(title, body string) int {
	who := jiratest.Maker{Name: ada}
	return s.File(jiratest.Item{Project: testProject, Title: title, Description: body, Labels: []string{testLabel},
		Reporter: who, Creator: who})
}

// listed is the numbers of the open requests.
func listed(t *testing.T, tr ports.IssueTracker) []int {
	t.Helper()
	list, err := tr.ListOpenIssues(context.Background(), "")
	if err != nil {
		t.Fatalf("ListOpenIssues() = %v", err)
	}
	numbers := make([]int, 0, len(list.Issues))
	for _, i := range list.Issues {
		numbers = append(numbers, i.Number)
	}
	return numbers
}

// wantReason fails t unless err is a failure with reason whose message holds
// every one of parts.
func wantReason(t *testing.T, err error, reason string, parts ...string) {
	t.Helper()
	if err == nil || ports.IssuesReason(err) != reason {
		t.Fatalf("got %v, want a failure with the reason %s", err, reason)
	}
	for _, part := range parts {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("the failure %q does not say %q", err, part)
		}
	}
}

// posts counts the calls of site that used method on a path that ends in
// suffix.
func posts(s site, method, suffix string) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Method == method && strings.HasSuffix(r.Path, suffix) {
			n++
		}
	}
	return n
}

// TestAddressHoldsTheCloudID covers #157: a scoped API token does not call
// the site's own address. Every call goes to <address>/ex/jira/<cloudID>
// /rest/api/3/..., as the account of the e-mail address and the token
// (Atlassian's page on API tokens), and none to the site.
func TestAddressHoldsTheCloudID(t *testing.T) {
	s := newSite(t)
	n := s.file(titleDarkMode, "")
	if _, err := s.tr.Comment(context.Background(), n, answer); err != nil {
		t.Fatal(err)
	}
	if _, err := s.tr.FindComment(context.Background(), n, marker); err != nil {
		t.Fatal(err)
	}
	if err := s.tr.CloseIssue(context.Background(), n, ports.CloseCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := s.tr.ListOpenIssues(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	prefix := "/ex/jira/" + s.CloudID() + "/rest/api/3/"
	reqs := s.Requests()
	if len(reqs) < 6 {
		t.Fatalf("the adapter made %d calls, want at least a list, a read, a comment, a close and the account's own", len(reqs))
	}
	for _, r := range reqs {
		if !strings.HasPrefix(r.Path, prefix) || r.Email != s.creds.Email {
			t.Errorf("%s %s as %q, want a path under %s as %s", r.Method, r.Path, r.Email, prefix, s.creds.Email)
		}
	}
}

// TestDefaultAddress: the address is Atlassian's gateway unless it is given,
// and the cloud ID is in it either way.
func TestDefaultAddress(t *testing.T) {
	const cloud = "00000000-0000-4000-8000-000000000000"
	for address, want := range map[string]string{
		"":                           "https://api.atlassian.com/ex/jira/" + cloud + "/rest/api/3",
		"https://gateway.example/":   "https://gateway.example/ex/jira/" + cloud + "/rest/api/3",
		"http://127.0.0.1:8080/jira": "http://127.0.0.1:8080/jira/ex/jira/" + cloud + "/rest/api/3",
		"http://localhost:8080":      "http://localhost:8080/ex/jira/" + cloud + "/rest/api/3",
		"http://127.1.2.3":           "http://127.1.2.3/ex/jira/" + cloud + "/rest/api/3",
		"http://[::1]:8080":          "http://[::1]:8080/ex/jira/" + cloud + "/rest/api/3",
	} {
		tr, err := Client{}.Issues(Config{Site: jiratest.SiteURL, CloudID: cloud, Project: "shop", Label: testLabel,
			Email: "a@b.test", Token: rand.Text(), Address: address})
		if err != nil {
			t.Fatalf("Issues(Address %q) = %v", address, err)
		}
		j := tr.(*jira)
		if j.base != want || j.project != testProject {
			t.Errorf("Issues(Address %q) calls %s for the project %s, want %s for %s", address, j.base, j.project,
				want, testProject)
		}
	}
}

// TestConfigRefused: what the adapter is not built for is refused before a
// call is made.
func TestConfigRefused(t *testing.T) {
	good := Config{Site: jiratest.SiteURL, CloudID: "abc-123", Project: testProject, Label: testLabel,
		Email: "a@b.test", Token: rand.Text()}
	for name, change := range map[string]func(*Config){
		"a site that is not https":        func(c *Config) { c.Site = "http://acme.atlassian.net" },
		"no site":                         func(c *Config) { c.Site = "" },
		"no cloud ID":                     func(c *Config) { c.CloudID = "" },
		"a cloud ID with a slash":         func(c *Config) { c.CloudID = "a/b" },
		"no project":                      func(c *Config) { c.Project = "" },
		"a project that is no key":        func(c *Config) { c.Project = "SHOP 1" },
		"no label":                        func(c *Config) { c.Label = "" },
		"a label with a space":            func(c *Config) { c.Label = "two words" },
		"no e-mail address":               func(c *Config) { c.Email = "" },
		"an e-mail address with a :":      func(c *Config) { c.Email = "a:b@c.test" },
		"no token":                        func(c *Config) { c.Token = "" },
		"a token on two lines":            func(c *Config) { c.Token = "a\nb" },
		"an address with no host":         func(c *Config) { c.Address = "https://" },
		"an address that is no URL":       func(c *Config) { c.Address = "ftp://x.test" },
		"an http address off the machine": func(c *Config) { c.Address = "http://jira-gateway.example" },
		"an http address that only starts like localhost": func(c *Config) { c.Address = "http://localhost.example" },
	} {
		cfg := good
		change(&cfg)
		if _, err := (Client{}).Issues(cfg); err == nil {
			t.Errorf("Issues() with %s = nil, want an error", name)
		}
	}
	if _, err := (Client{}).Issues(good); err != nil {
		t.Errorf("Issues() of a good config = %v", err)
	}
}

// TestNumbersAreTheKeysDigits covers #157 (decision 14): the port's number
// is the digits of the key, the adapter writes the key itself, and the
// item's ID, which is another number, is never taken for it.
func TestNumbersAreTheKeysDigits(t *testing.T) {
	s := newSite(t)
	for i := range 12 {
		s.file(fmt.Sprintf("Request %d", i+1), "")
	}
	list, err := s.tr.ListOpenIssues(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Issues) != 12 {
		t.Fatalf("listed %d items, want 12", len(list.Issues))
	}
	for i, got := range list.Issues {
		n := i + 1
		if got.Number != n || got.Key != fmt.Sprintf("SHOP-%d", n) ||
			got.URL != fmt.Sprintf("%s/browse/SHOP-%d", jiratest.SiteURL, n) {
			t.Errorf("item %d is listed as number %d, key %s, page %s", n, got.Number, got.Key, got.URL)
		}
	}
	// A call names the number: SHOP-10 is read, not the item whose ID is 10.
	if _, err := s.tr.Comment(context.Background(), 10, answer); err != nil {
		t.Fatal(err)
	}
	if got := s.Show(testProject, 10).Comments; len(got) != 1 {
		t.Errorf("SHOP-10 shows the comments %+v, want the one that was posted", got)
	}
}

// TestKeyNumberMustFitInt32 covers CodeQL #228
// (go/incorrect-integer-conversion): the API stores a request's number as an
// int32 (decision 14), so a key whose digits do not fit one is no number. Like
// a key that does not parse, it fails the list with the key in the message,
// and never reaches the API as a number that wrapped.
func TestKeyNumberMustFitInt32(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want int
		ok   bool
	}{
		{"SHOP-1", 1, true},
		{"SHOP-2147483647", 2147483647, true},
		{"SHOP-2147483648", 0, false},
		{"SHOP-4294967297", 0, false},
		{"SHOP-99999999999999999999", 0, false},
		{"SHOP-0", 0, false},
		{"SHOP-x", 0, false},
		{"SHOP", 0, false},
	} {
		got, ok := number(tc.key)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("number(%q) = %d, %t, want %d, %t", tc.key, got, ok, tc.want, tc.ok)
		}
	}

	// In a list: the last number an int32 holds is listed, the next is refused.
	s := newSite(t)
	s.NumberFrom(testProject, 2147483647)
	s.file(titleDarkMode, "")
	if got := listed(t, s.tr); len(got) != 1 || got[0] != 2147483647 {
		t.Fatalf("listed %v, want [2147483647]", got)
	}
	s.file(titleExport, "")
	_, err := s.tr.ListOpenIssues(context.Background(), "")
	wantReason(t, err, ports.ReasonTrackerError, "SHOP-2147483648")
}

// TestETag covers #157 (decision 18): Jira has no conditional list, so the
// ETag is the adapter's own, a hash of the open items' IDs and update times.
func TestETag(t *testing.T) {
	ctx := context.Background()
	s := newSite(t)
	a, b := s.file(titleDarkMode, "Why."), s.file(titleExport, "")
	first, err := s.tr.ListOpenIssues(ctx, "")
	if err != nil || first.ETag == "" || len(first.Issues) != 2 {
		t.Fatalf("ListOpenIssues() = %+v, %v; want two items and an ETag", first, err)
	}

	// An unchanged list costs the IDs and update times alone, no text.
	before := len(s.Requests())
	same, err := s.tr.ListOpenIssues(ctx, first.ETag)
	if err != nil || !same.NotModified || same.ETag != first.ETag || len(same.Issues) != 0 {
		t.Fatalf("ListOpenIssues(etag) of an unchanged list = %+v, %v; want NotModified with the same ETag", same, err)
	}
	asked := s.Requests()[before:]
	if len(asked) != 1 || asked[0].Query.Get("fields") != "updated" {
		t.Errorf("an unchanged list cost the calls %+v, want one that asks for the update times alone", asked)
	}

	// Another adapter of the same project reads the same ETag.
	other := tracker(t, s.Server, testProject, s.creds)
	if again, err := other.ListOpenIssues(ctx, ""); err != nil || again.ETag != first.ETag {
		t.Errorf("a second adapter lists the ETag %q, %v; want %q", again.ETag, err, first.ETag)
	}

	// Each change is one the list must show, in the order that leaves the item
	// it names there.
	for _, step := range []struct {
		name   string
		change func()
	}{
		{"an item edited", func() { s.Edit(testProject, a, "Why, again.") }},
		{"an item filed", func() { s.file("Print it", "") }},
		{"an item closed", func() { s.Finish(testProject, b) }},
		{"an item's label removed", func() { s.Unlabel(testProject, a, testLabel) }},
	} {
		before, err := s.tr.ListOpenIssues(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		step.change()
		after, err := s.tr.ListOpenIssues(ctx, before.ETag)
		if err != nil || after.NotModified || after.ETag == before.ETag {
			t.Errorf("after %s, ListOpenIssues(etag) = %+v, %v; want the list read again with another ETag", step.name,
				after, err)
		}
	}
}

// TestETagOfAnEmptyList: a project without requests has an ETag as well, and
// the first request changes it.
func TestETagOfAnEmptyList(t *testing.T) {
	ctx := context.Background()
	s := newSite(t)
	empty, err := s.tr.ListOpenIssues(ctx, "")
	if err != nil || empty.ETag == "" {
		t.Fatalf("ListOpenIssues() = %+v, %v; want an ETag", empty, err)
	}
	if same, err := s.tr.ListOpenIssues(ctx, empty.ETag); err != nil || !same.NotModified {
		t.Errorf("ListOpenIssues(etag) of an empty list that didn't change = %+v, %v; want NotModified", same, err)
	}
	s.file(titleDarkMode, "")
	if got, err := s.tr.ListOpenIssues(ctx, empty.ETag); err != nil || got.NotModified || len(got.Issues) != 1 {
		t.Errorf("ListOpenIssues(etag) after the first request = %+v, %v; want it read", got, err)
	}
}

// TestLabelTakenOff covers #157 (decision 13): an item whose label is taken
// off is no longer listed, an item with another label, or in another
// project, never is, and the label put back is listed again.
func TestLabelTakenOff(t *testing.T) {
	s := newSite(t)
	s.Project("OPS")
	a, b := s.file(titleDarkMode, ""), s.file(titleExport, "")
	s.File(jiratest.Item{Project: testProject, Title: "No label"})
	s.File(jiratest.Item{Project: testProject, Title: "Another label", Labels: []string{"backlog"}})
	s.File(jiratest.Item{Project: "OPS", Title: "Elsewhere", Labels: []string{testLabel}})
	if got := listed(t, s.tr); !slices.Equal(got, []int{a, b}) {
		t.Fatalf("the requests are %v, want %v", got, []int{a, b})
	}
	s.Unlabel(testProject, a, testLabel)
	if got := listed(t, s.tr); !slices.Equal(got, []int{b}) {
		t.Errorf("after the label is taken off the requests are %v, want %v", got, []int{b})
	}
}

// TestMovedItem covers #157: Jira keeps the old key of an item that moved to
// another project and answers a call for it with the item as it is now
// (Atlassian's reference for "Get issue"), so the factory would comment on
// and close an item of another project. The adapter compares the project of
// what Jira returned with its own: IssueNotFound for every call that names
// an item, and nothing written to the item.
func TestMovedItem(t *testing.T) {
	ctx := context.Background()
	s := newSite(t)
	s.Project("OPS")
	n, kept := s.file(titleDarkMode, ""), s.file(titleExport, "")
	newKey, _ := s.Move(testProject, n, "OPS")
	if newKey == fmt.Sprintf("%s-%d", testProject, n) {
		t.Fatalf("the item moved and has the key %s still", newKey)
	}

	_, found := s.tr.FindComment(ctx, n, marker)
	wantReason(t, found, ports.ReasonIssueNotFound)
	_, said := s.tr.Comment(ctx, n, answer)
	wantReason(t, said, ports.ReasonIssueNotFound)
	wantReason(t, s.tr.CloseIssue(ctx, n, ports.CloseNotPlanned), ports.ReasonIssueNotFound)

	moved := s.Show("OPS", 1)
	if len(moved.Comments) != 0 || moved.Category == jiratest.CategoryDone {
		t.Errorf("the moved item is now %+v, want it left as it was: open, with no comment", moved)
	}
	if posts(s, http.MethodPost, "/comment")+posts(s, http.MethodPost, "/transitions") != 0 {
		t.Error("the adapter wrote to the item of another project")
	}
	// It is no longer listed either; the project's other item is.
	if got := listed(t, s.tr); !slices.Equal(got, []int{kept}) {
		t.Errorf("the requests are %v, want %v", got, []int{kept})
	}
}

// TestNoAuthor covers #157 (decision 15): the author is the display name Jira
// gives for who made the item, and nothing when it gives none: an item that
// came in by e-mail or a service desk, or that an automation made, may have
// no reporter, or one that is not a person.
func TestNoAuthor(t *testing.T) {
	s := newSite(t)
	file := func(reporter, creator jiratest.Maker) int {
		return s.File(jiratest.Item{Project: testProject, Title: "T", Labels: []string{testLabel}, Reporter: reporter,
			Creator: creator})
	}
	person := jiratest.Maker{Name: ada}
	automation := jiratest.Maker{Name: "Automation for Jira", Type: "app"}
	long := jiratest.Maker{Name: "Ada\n" + strings.Repeat("Lovelace ", 40)}
	nobody := file(jiratest.Maker{}, jiratest.Maker{})
	reporter := file(person, jiratest.Maker{})
	creator := file(jiratest.Maker{}, automation)
	both := file(person, automation)
	app := file(automation, automation)
	cut := file(long, long)
	list, err := s.tr.ListOpenIssues(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]string{}
	for _, i := range list.Issues {
		got[i.Number] = i.Author
	}
	want := map[int]string{nobody: "", reporter: ada, creator: automation.Name, both: ada, app: automation.Name}
	for n, name := range want {
		if got[n] != name {
			t.Errorf("item %d is listed with the author %q, want %q", n, got[n], name)
		}
	}
	if a := got[cut]; len([]rune(a)) > maxAuthor || strings.ContainsAny(a, "\n\r\t") || !strings.HasPrefix(a, "Ada Lovelace ") {
		t.Errorf("a long name is listed as %q, want one line of at most %d characters", a, maxAuthor)
	}
}

// TestDescriptionIsFlattened covers #157 (decision 19): a description in
// Atlassian's document format is listed as text, and an item without one has
// an empty body.
func TestDescriptionIsFlattened(t *testing.T) {
	s := newSite(t)
	doc := json.RawMessage(`{"version":1,"type":"doc","content":[
		{"type":"heading","attrs":{"level":3},"content":[{"type":"text","text":"Why"}]},
		{"type":"paragraph","content":[{"type":"text","text":"Night shift, "},
			{"type":"text","text":"see","marks":[{"type":"link","attrs":{"href":"https://x.test/y"}}]}]},
		{"type":"bulletList","content":[{"type":"listItem","content":[
			{"type":"paragraph","content":[{"type":"text","text":"dark"}]}]}]}]}`)
	s.File(jiratest.Item{Project: testProject, Title: "Rich", ADF: doc, Labels: []string{testLabel}})
	s.File(jiratest.Item{Project: testProject, Title: "Bare", Labels: []string{testLabel}})
	list, err := s.tr.ListOpenIssues(context.Background(), "")
	if err != nil || len(list.Issues) != 2 {
		t.Fatalf("ListOpenIssues() = %+v, %v", list, err)
	}
	if want := "Why\nNight shift, see (https://x.test/y)\n- dark"; list.Issues[0].Body != want {
		t.Errorf("the description is listed as %q, want %q", list.Issues[0].Body, want)
	}
	if list.Issues[1].Body != "" {
		t.Errorf("an item without a description is listed with the body %q", list.Issues[1].Body)
	}
}

// TestCreatedIsRead: Jira writes a time with an offset that has no colon.
func TestCreatedIsRead(t *testing.T) {
	s := newSite(t)
	s.file(titleDarkMode, "")
	list, err := s.tr.ListOpenIssues(context.Background(), "")
	if err != nil || len(list.Issues) != 1 {
		t.Fatalf("ListOpenIssues() = %+v, %v", list, err)
	}
	if want := s.Show(testProject, 1).Created; !list.Issues[0].Created.Equal(want) || want.IsZero() {
		t.Errorf("the item is listed as filed at %s, want %s", list.Issues[0].Created, want)
	}
	for in, want := range map[string]time.Time{
		"2026-01-02T09:00:00.000+0000": time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC),
		"2026-01-02T10:00:00.000+0100": time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC),
		"2026-01-02T09:00:00Z":         time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC),
		"yesterday":                    {},
	} {
		if got := parseTime(in); !got.Equal(want) {
			t.Errorf("parseTime(%q) = %s, want %s", in, got, want)
		}
	}
}

// TestPaging: a project with more requests than a page holds is listed in
// full, in order, and a thread with more comments than a page holds is read
// to its last comment.
func TestPaging(t *testing.T) {
	ctx := context.Background()
	s := newSite(t)
	s.PageSize(2)
	want := make([]int, 0, 5)
	for i := range 5 {
		want = append(want, s.file(fmt.Sprintf("Request %d", i), ""))
	}
	if got := listed(t, s.tr); !slices.Equal(got, want) {
		t.Errorf("the requests are %v, want %v", got, want)
	}
	if pages := posts(s, http.MethodGet, "/search/jql"); pages != 3 {
		t.Errorf("five requests in pages of two took %d calls, want 3", pages)
	}
	for i := range 5 {
		s.Say(testProject, want[0], "Grace Hopper", fmt.Sprintf("Remark %d", i))
	}
	posted, err := s.tr.Comment(ctx, want[0], answer)
	if err != nil {
		t.Fatal(err)
	}
	found, err := s.tr.FindComment(ctx, want[0], marker)
	if err != nil || found == nil || found.ID != posted.ID {
		t.Errorf("FindComment() under five other comments = %+v, %v; want %+v", found, err, posted)
	}
}

// TestComments covers #157 (decision 17): a comment is written as Atlassian's
// document format, a paragraph to a line, and read back as it was written.
// Its page is the item's on the site. The marker is its last line, which
// shows, since Jira has no hidden text.
func TestComments(t *testing.T) {
	ctx := context.Background()
	s := newSite(t)
	n := s.file(titleDarkMode, "")
	posted, err := s.tr.Comment(ctx, n, answer)
	if err != nil {
		t.Fatal(err)
	}
	if posted.Body != answer || posted.Author != testAccount ||
		!strings.HasPrefix(posted.URL, jiratest.SiteURL+"/browse/SHOP-1?focusedCommentId="+posted.ID) {
		t.Errorf("Comment() = %+v, want the body %q by %s on the item's page", posted, answer, testAccount)
	}
	shown := s.Show(testProject, n).Comments
	if len(shown) != 1 || shown[0].Body != answer || shown[0].By != testAccount {
		t.Errorf("the item shows the comments %+v, want the one that was posted", shown)
	}
	found, err := s.tr.FindComment(ctx, n, marker)
	if err != nil || found == nil || *found != posted {
		t.Errorf("FindComment() = %+v, %v; want %+v", found, err, posted)
	}
	// The marker is found on a line of its own, wherever the comment's
	// text is, and a part of it is not the marker.
	if found, err := s.tr.FindComment(ctx, n, "<!-- theos:request stage=released -->"); err != nil || found != nil {
		t.Errorf("FindComment() of another marker = %+v, %v; want none", found, err)
	}
}

// TestFindCommentTakesTheEarliest covers #157: with two own comments that
// hold the marker, the earlier one is the one found, since the comments are
// read oldest first.
func TestFindCommentTakesTheEarliest(t *testing.T) {
	ctx := context.Background()
	s := newSite(t)
	n := s.file(titleDarkMode, "")
	first, err := s.tr.Comment(ctx, n, answer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.tr.Comment(ctx, n, "Planned again\n\n"+marker); err != nil {
		t.Fatal(err)
	}
	found, err := s.tr.FindComment(ctx, n, marker)
	if err != nil || found == nil || found.ID != first.ID {
		t.Errorf("FindComment() of two comments with the marker = %+v, %v; want the first, %+v", found, err, first)
	}
}

// TestAccountIsReadOnce: the credential's own account is asked for once and
// kept, since it is what an own comment is told from a stranger's by.
func TestAccountIsReadOnce(t *testing.T) {
	ctx := context.Background()
	s := newSite(t)
	n := s.file(titleDarkMode, "")
	for range 3 {
		if _, err := s.tr.FindComment(ctx, n, marker); err != nil {
			t.Fatal(err)
		}
	}
	if got := posts(s, http.MethodGet, "/myself"); got != 1 {
		t.Errorf("three reads of comments asked for the account %d times, want once", got)
	}
}

// TestScopes covers #157 (decision 20): the token's scopes are read:jira-work
// and write:jira-work, and read:jira-user for the account's own ID, which
// Atlassian's reference asks of that one call. A token without a scope is
// named as one that lacks it, with the scope in the message where it is the
// account's.
func TestScopes(t *testing.T) {
	ctx := context.Background()
	s := newSite(t)
	n := s.file(titleDarkMode, "")

	readOnly := tracker(t, s.Server, testProject, s.Restricted("reader", jiratest.ScopeRead, jiratest.ScopeUser))
	if got := listed(t, readOnly); !slices.Equal(got, []int{n}) {
		t.Errorf("a token that may read lists %v, want %v", got, []int{n})
	}
	_, err := readOnly.Comment(ctx, n, answer)
	wantReason(t, err, ports.ReasonTokenLacksIssues, "scope")
	wantReason(t, readOnly.CloseIssue(ctx, n, ports.CloseCompleted), ports.ReasonTokenLacksIssues)

	noUser := tracker(t, s.Server, testProject, s.Restricted("no-user", jiratest.ScopeRead, jiratest.ScopeWrite))
	_, err = noUser.FindComment(ctx, n, marker)
	wantReason(t, err, ports.ReasonTokenLacksIssues, ScopeUser)
	if _, err := noUser.Comment(ctx, n, answer); err != nil {
		t.Errorf("Comment() with the scopes to read and write = %v, want it posted", err)
	}
}

// TestFailures: how Jira's refusals are named, past the suite's six.
func TestFailures(t *testing.T) {
	ctx := context.Background()
	s := newSite(t)
	n := s.file(titleDarkMode, "")

	s.Throttle(1)
	_, err := s.tr.ListOpenIssues(ctx, "")
	wantReason(t, err, ports.ReasonTrackerUnavailable, "rate limit")

	forbidden := tracker(t, s.Server, testProject, func() jiratest.Credentials {
		c := s.Account("forbidden")
		s.Forbid(c)
		return c
	}())
	_, err = forbidden.ListOpenIssues(ctx, "")
	wantReason(t, err, ports.ReasonTokenLacksIssues, "permission")

	if _, err := s.tr.Comment(ctx, 0, answer); ports.IssuesReason(err) != ports.ReasonIssueNotFound {
		t.Errorf("Comment() on number 0 = %v, want IssueNotFound", err)
	}
	if err := s.tr.CloseIssue(ctx, n, "duplicate"); err == nil {
		t.Error("CloseIssue() with a reason that is not one of the two = nil, want an error")
	}
	if got := s.Show(testProject, n); got.Category == jiratest.CategoryDone {
		t.Error("a reason that is not one closed the item")
	}
}

// closes closes item n of project with reason through tr, and returns the
// item as its page then shows it.
func closes(t *testing.T, s site, tr ports.IssueTracker, n int, reason ports.CloseReason) jiratest.Shown {
	t.Helper()
	if err := tr.CloseIssue(context.Background(), n, reason); err != nil {
		t.Fatalf("CloseIssue(%s) = %v", reason, err)
	}
	return s.Show(testProject, n)
}

// TestCloseCompletedAndNotPlanned covers #157 (decision 17): closing is a
// transition into a status of category Done, with the resolution Done for
// completed and Won't Do for not planned, which a standard site has.
func TestCloseCompletedAndNotPlanned(t *testing.T) {
	s := newSite(t)
	a, b := s.file(titleDarkMode, ""), s.file(titleExport, "")
	got := closes(t, s, s.tr, a, ports.CloseCompleted)
	if got.Status != jiratest.StatusDone || got.Category != jiratest.CategoryDone || got.Resolution != jiratest.ResolutionDone {
		t.Errorf("a completed item is %+v, want Done with the resolution Done", got)
	}
	got = closes(t, s, s.tr, b, ports.CloseNotPlanned)
	if got.Status != jiratest.StatusDone || got.Resolution != jiratest.ResolutionWontDo {
		t.Errorf("an item not planned is %+v, want Done with the resolution Won't Do", got)
	}
	if got := listed(t, s.tr); len(got) != 0 {
		t.Errorf("the closed items are still listed: %v", got)
	}
}

// withClosed gives the project a workflow with a second status of category
// Done, which the first one offered is not.
func withClosed() jiratest.Workflow {
	w := jiratest.Classic()
	w.Statuses = append(w.Statuses, jiratest.Status{Name: "Closed", Category: jiratest.CategoryDone})
	w.Transitions = append(w.Transitions, jiratest.Transition{ID: "22", Name: "Close", To: "Closed",
		Resolution: jiratest.TakesResolution})
	return w
}

// TestCloseStatusAndResolutionNamed covers #157 (decision 17): the status
// and the resolution each reason closes with are the entry's settings. The
// default status is the first of category Done the item offers a transition
// into.
func TestCloseStatusAndResolutionNamed(t *testing.T) {
	s := newSite(t)
	s.SetWorkflow(testProject, withClosed())
	for _, tt := range []struct {
		name       string
		closing    Closing
		reason     ports.CloseReason
		wantStatus string
		wantRes    string
	}{
		{"the first status of category Done offered", Closing{}, ports.CloseCompleted, "Done", "Done"},
		{"a status named for completed", Closing{Completed: Outcome{Status: "closed"}}, ports.CloseCompleted, "Closed", "Done"},
		{"a status named for not planned", Closing{NotPlanned: Outcome{Status: "Closed"}}, ports.CloseNotPlanned, "Closed", "Won't Do"},
		{"a resolution named", Closing{Completed: Outcome{Resolution: "Duplicate"}}, ports.CloseCompleted, "Done", "Duplicate"},
		{"both named", Closing{NotPlanned: Outcome{Status: "Closed", Resolution: "Cannot Reproduce"}}, ports.CloseNotPlanned,
			"Closed", "Cannot Reproduce"},
		{"the other reason's setting is not used", Closing{NotPlanned: Outcome{Status: "Closed"}}, ports.CloseCompleted,
			"Done", "Done"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tr := tracker(t, s.Server, testProject, s.creds, func(c *Config) { c.Close = tt.closing })
			n := s.file(titleDarkMode, "")
			got := closes(t, s, tr, n, tt.reason)
			if got.Status != tt.wantStatus || got.Resolution != tt.wantRes {
				t.Errorf("the item is %s with the resolution %s, want %s with %s", got.Status, got.Resolution,
					tt.wantStatus, tt.wantRes)
			}
		})
	}
}

// TestResolutionSentOnlyWhereTheScreenTakesOne covers #157 (decision 17): a
// team-managed project has no resolution to send, and one whose screen
// requires it gets the default.
func TestResolutionSentOnlyWhereTheScreenTakesOne(t *testing.T) {
	for name, tt := range map[string]struct {
		workflow jiratest.Workflow
		closing  Closing
		want     string
	}{
		"no resolution on the screen":              {jiratest.TeamManaged(), Closing{}, ""},
		"one named, and the screen has none":       {jiratest.TeamManaged(), Closing{Completed: Outcome{Resolution: "Fixed"}}, ""},
		"a required resolution gets the default":   {requiring(), Closing{}, "Done"},
		"a required resolution gets the one named": {requiring(), Closing{Completed: Outcome{Resolution: "Duplicate"}}, "Duplicate"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSite(t, func(c *Config) { c.Close = tt.closing })
			s.SetWorkflow(testProject, tt.workflow)
			n := s.file(titleDarkMode, "")
			got := closes(t, s, s.tr, n, ports.CloseCompleted)
			if got.Category != jiratest.CategoryDone || got.Resolution != tt.want {
				t.Errorf("the item is %+v, want closed with the resolution %q", got, tt.want)
			}
		})
	}
}

func requiring() jiratest.Workflow {
	w := jiratest.Classic()
	w.Transitions = slices.Clone(w.Transitions)
	w.Transitions[1].Resolution = jiratest.RequiresResolution
	return w
}

// TestCannotClose covers #157 (decision 17): a workflow that offers no way
// to close, a status the item can't reach, a resolution the site doesn't
// have, a screen that asks for more and a workflow that refuses are
// CannotClose, with the names the site does offer in the message. The item
// is left as it was, and the factory can still comment on it.
func TestCannotClose(t *testing.T) {
	older := jiratest.Classic()
	older.Resolutions = []string{"Fixed", "Won't Fix"}
	noDone := jiratest.Classic()
	noDone.Transitions = noDone.Transitions[:1]
	asks := jiratest.Classic()
	asks.Transitions = slices.Clone(asks.Transitions)
	asks.Transitions[1].Asks = []string{"Reviewer"}
	refuses := jiratest.Classic()
	refuses.Transitions = slices.Clone(refuses.Transitions)
	refuses.Transitions[1].Refuses = "Add a fix version first."
	for name, tt := range map[string]struct {
		workflow jiratest.Workflow
		closing  Closing
		reason   ports.CloseReason
		says     []string
	}{
		"no transition into a status of category Done": {noDone, Closing{}, ports.CloseCompleted,
			[]string{"no transition into a status of category Done", `"In Progress"`}},
		"a status the item can't reach": {jiratest.Classic(), Closing{Completed: Outcome{Status: "Closed"}}, ports.CloseCompleted,
			[]string{`"Closed"`, `"Done"`}},
		"a resolution the site doesn't have": {jiratest.Classic(), Closing{Completed: Outcome{Resolution: "Fixed"}},
			ports.CloseCompleted, []string{`"Fixed"`, `"Done"`, `"Won't Do"`, `"Duplicate"`}},
		"the default resolution on a site that says Fixed": {older, Closing{}, ports.CloseCompleted,
			[]string{`"Done"`, `"Fixed"`, `"Won't Fix"`}},
		"the default resolution for not planned on a site that says Won't Fix": {older, Closing{}, ports.CloseNotPlanned,
			[]string{`"Won't Do"`, `"Won't Fix"`}},
		"a screen that asks for a field": {asks, Closing{}, ports.CloseCompleted, []string{`"Reviewer"`}},
		"a workflow that refuses":        {refuses, Closing{}, ports.CloseCompleted, []string{"Add a fix version first."}},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := newSite(t, func(c *Config) { c.Close = tt.closing })
			s.SetWorkflow(testProject, tt.workflow)
			n := s.file(titleDarkMode, "")
			wantReason(t, s.tr.CloseIssue(ctx, n, tt.reason), ports.ReasonCannotClose, append(tt.says, "SHOP-1")...)
			if got := s.Show(testProject, n); got.Category == jiratest.CategoryDone || got.Resolution != "" {
				t.Errorf("the item is %+v, want it left as it was", got)
			}
			if _, err := s.tr.Comment(ctx, n, answer); err != nil {
				t.Errorf("Comment() after a close that could not be = %v, want the comment posted", err)
			}
		})
	}

	// The older site's way out: the settings name what it has.
	s := newSite(t, func(c *Config) {
		c.Close = Closing{Completed: Outcome{Resolution: "Fixed"}, NotPlanned: Outcome{Resolution: "Won't Fix"}}
	})
	s.SetWorkflow(testProject, older)
	if got := closes(t, s, s.tr, s.file(titleDarkMode, ""), ports.CloseNotPlanned); got.Resolution != "Won't Fix" {
		t.Errorf("an item not planned on the older site has the resolution %q, want Won't Fix", got.Resolution)
	}
}

// TestClosedItemStaysClosed covers #157 (decision 17): Jira is not asked to
// close an item that is closed, or to reopen one to close it again, since
// closed is what the Brief asked for. CloseIssue returns no error, the item
// keeps its resolution, and no transition is made.
func TestClosedItemStaysClosed(t *testing.T) {
	ctx := context.Background()
	s := newSite(t)
	byPerson, byFactory := s.file(titleDarkMode, ""), s.file(titleExport, "")
	s.Finish(testProject, byPerson)
	closes(t, s, s.tr, byFactory, ports.CloseCompleted)
	if posts(s, http.MethodPost, "/transitions") != 1 {
		t.Fatalf("closing one item made %d transitions", posts(s, http.MethodPost, "/transitions"))
	}
	for _, n := range []int{byPerson, byFactory} {
		for _, reason := range []ports.CloseReason{ports.CloseNotPlanned, ports.CloseCompleted} {
			if err := s.tr.CloseIssue(ctx, n, reason); err != nil {
				t.Errorf("CloseIssue(%s) of a closed item = %v, want none", reason, err)
			}
		}
		got := s.Show(testProject, n)
		if got.Status != jiratest.StatusDone || got.Resolution != jiratest.ResolutionDone {
			t.Errorf("a closed item is now %+v, want it as it was: Done with the resolution Done", got)
		}
	}
	if got := posts(s, http.MethodPost, "/transitions"); got != 1 {
		t.Errorf("closing closed items made %d transitions in all, want the first one's alone", got)
	}
	if got := posts(s, http.MethodGet, "/transitions"); got != 1 {
		t.Errorf("closing closed items read the transitions %d times in all, want the first one's alone", got)
	}
}

// TestBadReasonCallsNothing: a reason that is not
// one of the two is refused before any call to Jira.
func TestBadReasonCallsNothing(t *testing.T) {
	s := newSite(t)
	n := s.file(titleDarkMode, "")
	before := len(s.Requests())
	if err := s.tr.CloseIssue(context.Background(), n, "duplicate"); err == nil {
		t.Fatal("CloseIssue() with a reason that is not one of the two = nil")
	}
	if got := len(s.Requests()) - before; got != 0 {
		t.Errorf("a refused reason made %d calls, want none", got)
	}
}

// TestTrackerFromASource covers #161: the operator builds the adapter from a
// Brief's source, the Secret's two keys and its own gateway address, and
// the source's close settings reach the close.
func TestTrackerFromASource(t *testing.T) {
	s := newSite(t)
	s.SetWorkflow(testProject, withClosed())
	source := ports.TrackerSource{Tracker: "jira", Project: testProject, Label: testLabel, Site: jiratest.SiteURL,
		Instance: s.CloudID(), Close: map[ports.CloseReason]ports.CloseOutcome{
			ports.CloseNotPlanned: {Status: "Closed"}}}
	secret := map[string][]byte{SecretEmail: []byte(s.creds.Email), SecretToken: []byte(s.creds.Token + "\n")}
	tr, err := Client{Address: s.URL()}.Tracker(context.Background(), source, secret)
	if err != nil {
		t.Fatalf("Tracker() = %v", err)
	}
	n := s.file(titleDarkMode, "")
	if got := closes(t, s, tr, n, ports.CloseNotPlanned); got.Status != "Closed" || got.Resolution != "Won't Do" {
		t.Errorf("closed as not planned into %s with %s, want Closed with Won't Do", got.Status, got.Resolution)
	}
	n = s.file(titleExport, "")
	if got := closes(t, s, tr, n, ports.CloseCompleted); got.Status != "Done" {
		t.Errorf("closed as completed into %s, want Done: only notPlanned names a status", got.Status)
	}

	for name, data := range map[string]map[string][]byte{
		"no Secret": nil, "no email": {SecretToken: []byte(s.creds.Token)},
		"no token": {SecretEmail: []byte(s.creds.Email)}} {
		_, err := Client{Address: s.URL()}.Tracker(context.Background(), source, data)
		if missing, ok := errors.AsType[*ports.CredentialMissingError](err); !ok ||
			missing.Keys != "the keys email and token" {
			t.Errorf("Tracker() with %s = %v, want the keys email and token missing", name, err)
		}
	}
	bad := source
	bad.Site = "http://acme.atlassian.net"
	if _, err := (Client{Address: s.URL()}).Tracker(context.Background(), bad, secret); ports.IssuesReason(err) !=
		ports.ReasonTrackerError {
		t.Errorf("Tracker() with an http site = %v, want %s", err, ports.ReasonTrackerError)
	}
}
