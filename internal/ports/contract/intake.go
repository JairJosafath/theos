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

package contract

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// IssueTrackerEnv builds the IssueTracker under test.
type IssueTrackerEnv struct {
	// New returns a fresh tracker (a repository's issues, a team's board)
	// that holds no issue, with the IssueTracker that speaks as the
	// factory's account.
	New func(t *testing.T) TrackerHost
	// Cannot names the subtests the tracker can't run, with why; they are
	// skipped.
	Cannot map[string]string
}

// TrackerHost is an issue tracker as the suite reaches it: through the port
// as the factory's account, and beside the port as its people and as the
// tracker itself.
type TrackerHost struct {
	// Tracker speaks as the factory's account, which may read and answer
	// the issues and read the teams' members.
	Tracker ports.IssueTracker
	// Account is the login of the factory's account, as a comment names its
	// author.
	Account string
	// Refused is an IssueTracker of the same issues with a credential the
	// tracker doesn't know.
	Refused ports.IssueTracker
	// LacksIssues has a credential that may not read or answer the issues,
	// LacksMembers one that may not read a team's members.
	LacksIssues, LacksMembers ports.IssueTracker
	// Elsewhere is an IssueTracker of a repository the tracker doesn't
	// have, or doesn't show the credential.
	Elsewhere ports.IssueTracker
	// Team is a team of the tracker without members, as TeamMember takes
	// it, and NoTeam the name of one the tracker doesn't have.
	Team, NoTeam string
	// Person makes a person's account and returns their login, as an issue
	// names its author and TeamMember takes it.
	Person func(t *testing.T, name string) string
	// File files an issue as author and returns its number.
	File func(t *testing.T, author, title, body string) int
	// PullRequest opens a pull request as author, where the tracker keeps
	// them among the issues, and returns its number.
	PullRequest func(t *testing.T, author, title string) int
	// Edit changes the body of issue number, as its author does.
	Edit func(t *testing.T, number int, body string)
	// Close closes issue number, as a person does.
	Close func(t *testing.T, number int)
	// Say comments body on issue number as author.
	Say func(t *testing.T, number int, author, body string)
	// Join makes login an active member of team; Invite leaves login with
	// an invitation to team that isn't accepted.
	Join, Invite func(t *testing.T, team, login string)
	// Disable turns the issues off.
	Disable func(t *testing.T)
	// Down makes the tracker answer nothing from now on, as an outage or a
	// rate limit does.
	Down func(t *testing.T)
	// Issue is issue number as the tracker shows it.
	Issue func(t *testing.T, number int) TrackedIssue
}

// TrackedIssue is an issue as its tracker shows it.
type TrackedIssue struct {
	// Title and Body are its text, and Author the login of who filed it.
	Title, Body, Author string
	// Created is when it was filed.
	Created time.Time
	// Closed says it is closed, and Reason why.
	Closed bool
	Reason ports.CloseReason
	// Comments are what was said on it, oldest first.
	Comments []TrackedComment
}

// TrackedComment is a comment as its tracker shows it.
type TrackedComment struct {
	Body string
	// Own says the factory's account wrote it.
	Own bool
}

// What the IssueTracker suite files, says and looks for more than once.
const (
	authorAda       = "ada"
	authorGrace     = "grace"
	requestDarkMode = "Dark mode"
	requestExport   = "Export to CSV"
	requestWhy      = "The night shift reads it in the dark."
	// The markers of two of a Brief's answers, and an answer with the first.
	markerPlanned  = "<!-- contract:request stage=planned -->"
	markerReleased = "<!-- contract:request stage=released -->"
	answerPlanned  = "Planned as: dark mode\n\n" + markerPlanned
)

// IssueTracker states what a Brief relies on of every IssueTracker: the
// list is the open issues, oldest first, each with its number, text, author,
// page and when it was filed, with no closed issue and no pull request; a
// list asked with the ETag of the last is NotModified while nothing changed,
// with the ETag to ask with again, and is read again once an issue is filed,
// edited or closed; TeamMember is true for a team's active member alone;
// Comment posts as the factory's account and returns the comment with its
// ID, author and page, and FindComment finds that comment by its marker on
// that issue, the first when two have it, nobody else's, and none by an
// empty marker; CloseIssue closes with one of the two reasons, a closed
// issue again with the new reason, and never with another reason; no call
// changes an issue's text or what people said on it; and seven failures
// have their reason, as ports.IssuesReason reads it: an issue that isn't
// there, a credential the tracker doesn't know, one that may not use the
// issues, one that may not read the members, a repository that isn't there,
// issues turned off, and a tracker that doesn't answer. A failed list is
// empty and a failed TeamMember false. ReasonGitHubError, the reason of
// anything else, is not in the suite.
//
// What a tracker does its own way stays in its adapter's tests: how lists
// and comments are paged and which page an ETag covers, the names a team
// and a login may have, what a moved or locked issue answers, and what an
// error's message holds.
func IssueTracker(t *testing.T, env IssueTrackerEnv) {
	t.Helper()
	ctx, cancel := suiteContext()
	defer cancel()
	runSubtests(t, trackerSuite{ctx: ctx, env: env}, env.Cannot, []subtest[trackerSuite]{
		{"lists the open issues, oldest first, with who wrote each", trackerSuite.lists},
		{"lists each issue with when it was filed", trackerSuite.listsCreated},
		{"lists no closed issue", trackerSuite.listsNoClosed},
		{"never lists a pull request", trackerSuite.listsNoPulls},
		{"says an unchanged list is not modified, with the ETag to ask with again", trackerSuite.notModified},
		{"reads the list again once an issue is filed, edited or closed", trackerSuite.modified},
		{"tells a team's active member from a stranger, and from a team it can't see", trackerSuite.members},
		{"does not count an invitation that isn't accepted", trackerSuite.invited},
		{"comments as the factory's account, and finds its comment by its marker", trackerSuite.comments},
		{"returns the comment it posted, with its ID, author and page", trackerSuite.posted},
		{"finds the first of its comments with a marker", trackerSuite.firstComment},
		{"finds no comment by an empty marker", trackerSuite.emptyMarker},
		{"ignores a marker in a comment someone else wrote", trackerSuite.strangers},
		{"closes an issue with the reason given", trackerSuite.closes},
		{"closes a closed issue again, with the new reason, even one a person closed", trackerSuite.closesAgain},
		{"refuses a reason to close it doesn't know, and leaves the issue open", trackerSuite.badReason},
		{"never changes an issue's text, nor what people said on it", trackerSuite.keepsText},
		{"says an issue that isn't there is not found", trackerSuite.issueNotFound},
		{"says a credential the tracker doesn't know is unauthorized", trackerSuite.refused},
		{"says a credential that may not use the issues lacks them", trackerSuite.lacksIssues},
		{"says a credential that may not read a team's members lacks them", trackerSuite.lacksMembers},
		{"says a repository that isn't there is not found", trackerSuite.repositoryNotFound},
		{"says issues that are turned off are disabled", trackerSuite.disabled},
		{"says a tracker that doesn't answer is unavailable", trackerSuite.unavailable},
	})
}

// trackerSuite holds what every subtest of IssueTracker shares.
type trackerSuite struct {
	ctx context.Context
	env IssueTrackerEnv
}

// tracked is a fresh tracker with ada, and her request on it.
type tracked struct {
	TrackerHost
	ada     string
	request int
}

// filed makes a fresh tracker with ada's request for dark mode.
func (s trackerSuite) filed(t *testing.T) tracked {
	t.Helper()
	x := tracked{TrackerHost: s.env.New(t)}
	x.ada = x.Person(t, authorAda)
	x.request = x.File(t, x.ada, requestDarkMode, requestWhy)
	return x
}

// list is the tracker's open issues, asked with etag.
func (s trackerSuite) list(t *testing.T, tracker ports.IssueTracker, etag string) ports.IssueList {
	t.Helper()
	list, err := tracker.ListOpenIssues(s.ctx, etag)
	if err != nil {
		t.Fatalf("ListOpenIssues(%q) = %v", etag, err)
	}
	return list
}

// wantListed fails t unless list was read and holds the issues numbers, in
// that order.
func wantListed(t *testing.T, list ports.IssueList, numbers ...int) {
	t.Helper()
	got := make([]int, 0, len(list.Issues))
	for _, i := range list.Issues {
		got = append(got, i.Number)
	}
	if list.NotModified || !slices.Equal(got, numbers) {
		t.Fatalf("ListOpenIssues() listed issues %v, NotModified %v; want issues %v, read", got, list.NotModified,
			numbers)
	}
}

// wantReason fails t unless err is a failure with reason, as a Brief reads
// it.
func wantReason(t *testing.T, call string, err error, reason string) {
	t.Helper()
	if err == nil || ports.IssuesReason(err) != reason {
		t.Errorf("%s = %v, want a failure with the reason %s", call, err, reason)
	}
}

// trackerCall is one call of the port, and the error it returned.
type trackerCall struct {
	name string
	err  error
}

// answers reads and answers issue number through tracker, as a Brief does
// a request it declines, and returns each call's error. The list is asked
// with etag.
func (s trackerSuite) answers(tracker ports.IssueTracker, etag string, number int) []trackerCall {
	_, listed := tracker.ListOpenIssues(s.ctx, etag)
	_, found := tracker.FindComment(s.ctx, number, markerPlanned)
	_, said := tracker.Comment(s.ctx, number, answerPlanned)
	return []trackerCall{
		{fmt.Sprintf("ListOpenIssues(%q)", etag), listed},
		{"FindComment()", found},
		{"Comment()", said},
		{"CloseIssue()", tracker.CloseIssue(s.ctx, number, ports.CloseNotPlanned)},
	}
}

// wantUntouched fails t unless x's request is open with nothing said on it.
func wantUntouched(t *testing.T, x tracked) {
	t.Helper()
	if got := x.Issue(t, x.request); got.Closed || len(got.Comments) != 0 {
		t.Errorf("the issue is now %+v, want it open with no comment", got)
	}
}

func (s trackerSuite) lists(t *testing.T) {
	h := s.env.New(t)
	if list := s.list(t, h.Tracker, ""); len(list.Issues) != 0 || list.NotModified {
		t.Fatalf("ListOpenIssues() of a tracker without issues = %+v, want no issue, read", list)
	}
	ada, grace := h.Person(t, authorAda), h.Person(t, authorGrace)
	want := []ports.GitHubIssue{
		{Title: requestDarkMode, Body: requestWhy, Author: ada},
		{Title: requestExport, Body: "With the totals.", Author: grace},
		{Title: "Print it", Body: "", Author: ada},
	}
	numbers := make([]int, 0, len(want))
	for i := range want {
		want[i].Number = h.File(t, want[i].Author, want[i].Title, want[i].Body)
		numbers = append(numbers, want[i].Number)
	}
	list := s.list(t, h.Tracker, "")
	wantListed(t, list, numbers...)
	pages := map[string]bool{}
	for i, got := range list.Issues {
		if got.Title != want[i].Title || got.Body != want[i].Body || !strings.EqualFold(got.Author, want[i].Author) {
			t.Errorf("issue %d is listed as %q by %q with the body %q; want %q by %q with %q", got.Number, got.Title,
				got.Author, got.Body, want[i].Title, want[i].Author, want[i].Body)
		}
		pages[got.URL] = true
	}
	if len(pages) != len(want) || pages[""] {
		t.Errorf("the issues' pages are %v, want one for each issue", pages)
	}
}

func (s trackerSuite) listsCreated(t *testing.T) {
	x := s.filed(t)
	second := x.File(t, x.ada, requestExport, "")
	list := s.list(t, x.Tracker, "")
	wantListed(t, list, x.request, second)
	for _, got := range list.Issues {
		want := x.Issue(t, got.Number).Created
		if got.Created.IsZero() || !got.Created.Equal(want) {
			t.Errorf("issue %d is listed as filed at %s, want when it was filed, %s", got.Number, got.Created, want)
		}
	}
	if list.Issues[1].Created.Before(list.Issues[0].Created) {
		t.Errorf("the list has an issue filed at %s before one filed at %s, want the oldest first",
			list.Issues[1].Created, list.Issues[0].Created)
	}
}

func (s trackerSuite) listsNoClosed(t *testing.T) {
	x := s.filed(t)
	closed := x.File(t, x.ada, requestExport, "")
	last := x.File(t, x.ada, "Print it", "")
	x.Close(t, closed)
	wantListed(t, s.list(t, x.Tracker, ""), x.request, last)
}

func (s trackerSuite) listsNoPulls(t *testing.T) {
	x := s.filed(t)
	x.PullRequest(t, x.ada, "Add dark mode")
	last := x.File(t, x.ada, requestExport, "")
	wantListed(t, s.list(t, x.Tracker, ""), x.request, last)
}

func (s trackerSuite) notModified(t *testing.T) {
	x := s.filed(t)
	first := s.list(t, x.Tracker, "")
	wantListed(t, first, x.request)
	if first.ETag == "" {
		t.Fatal("ListOpenIssues() gave no ETag to ask with again")
	}
	etag := first.ETag
	for range 2 {
		same := s.list(t, x.Tracker, etag)
		if !same.NotModified || len(same.Issues) != 0 || same.ETag == "" {
			t.Fatalf("ListOpenIssues(%q) of an unchanged list = %+v; want NotModified, no issue, and an ETag", etag,
				same)
		}
		etag = same.ETag
	}
	// Asked without an ETag, the list is read whatever it was before.
	wantListed(t, s.list(t, x.Tracker, ""), x.request)
}

func (s trackerSuite) modified(t *testing.T) {
	x := s.filed(t)
	list := s.list(t, x.Tracker, "")
	wantListed(t, list, x.request)

	second := x.File(t, x.ada, requestExport, "")
	list = s.list(t, x.Tracker, list.ETag)
	wantListed(t, list, x.request, second)

	const edited = "The night shift reads it in the dark, and so does the day shift."
	x.Edit(t, x.request, edited)
	list = s.list(t, x.Tracker, list.ETag)
	wantListed(t, list, x.request, second)
	if got := list.Issues[0].Body; got != edited {
		t.Errorf("an edited issue is listed with the body %q, want %q", got, edited)
	}

	x.Close(t, second)
	wantListed(t, s.list(t, x.Tracker, list.ETag), x.request)
}

func (s trackerSuite) members(t *testing.T) {
	h := s.env.New(t)
	ada, grace := h.Person(t, authorAda), h.Person(t, authorGrace)
	h.Join(t, h.Team, ada)
	for _, tt := range []struct {
		who, team, login string
		want             bool
	}{
		{"the team's member", h.Team, ada, true},
		{"a stranger to the team", h.Team, grace, false},
		{"a team the tracker doesn't have", h.NoTeam, ada, false},
	} {
		if got, err := h.Tracker.TeamMember(s.ctx, tt.team, tt.login); err != nil || got != tt.want {
			t.Errorf("TeamMember(%s, %s), %s = %v, %v; want %v", tt.team, tt.login, tt.who, got, err, tt.want)
		}
	}
}

func (s trackerSuite) invited(t *testing.T) {
	h := s.env.New(t)
	grace := h.Person(t, authorGrace)
	h.Invite(t, h.Team, grace)
	if got, err := h.Tracker.TeamMember(s.ctx, h.Team, grace); err != nil || got {
		t.Errorf("TeamMember() of someone invited = %v, %v; want false", got, err)
	}
	h.Join(t, h.Team, grace)
	if got, err := h.Tracker.TeamMember(s.ctx, h.Team, grace); err != nil || !got {
		t.Errorf("TeamMember() once the invitation is accepted = %v, %v; want true", got, err)
	}
}

func (s trackerSuite) comments(t *testing.T) {
	x := s.filed(t)
	other := x.File(t, x.ada, requestExport, "")
	if found, err := x.Tracker.FindComment(s.ctx, x.request, markerPlanned); err != nil || found != nil {
		t.Fatalf("FindComment() on an issue without comments = %+v, %v; want none", found, err)
	}
	if _, err := x.Tracker.Comment(s.ctx, x.request, answerPlanned); err != nil {
		t.Fatalf("Comment() = %v", err)
	}
	want := []TrackedComment{{Body: answerPlanned, Own: true}}
	if got := x.Issue(t, x.request).Comments; !slices.Equal(got, want) {
		t.Errorf("the issue shows the comments %+v, want %+v", got, want)
	}
	found, err := x.Tracker.FindComment(s.ctx, x.request, markerPlanned)
	if err != nil || found == nil || found.Body != answerPlanned {
		t.Errorf("FindComment() of the marker it commented = %+v, %v; want that comment", found, err)
	}
	if found, err := x.Tracker.FindComment(s.ctx, x.request, markerReleased); err != nil || found != nil {
		t.Errorf("FindComment() of another marker = %+v, %v; want none", found, err)
	}
	if found, err := x.Tracker.FindComment(s.ctx, other, markerPlanned); err != nil || found != nil {
		t.Errorf("FindComment() on another issue = %+v, %v; want none", found, err)
	}
}

func (s trackerSuite) posted(t *testing.T) {
	x := s.filed(t)
	first, err := x.Tracker.Comment(s.ctx, x.request, answerPlanned)
	if err != nil {
		t.Fatalf("Comment() = %v", err)
	}
	if first.ID == 0 || first.Body != answerPlanned || !strings.EqualFold(first.Author, x.Account) || first.URL == "" {
		t.Errorf("Comment() = %+v; want the comment with an ID, the body %q, the author %q and its page", first,
			answerPlanned, x.Account)
	}
	second, err := x.Tracker.Comment(s.ctx, x.request, "Released.\n\n"+markerReleased)
	if err != nil {
		t.Fatalf("Comment() = %v", err)
	}
	if second.ID == first.ID || second.URL == first.URL {
		t.Errorf("a second Comment() = %+v after %+v; want an ID and a page of its own", second, first)
	}
	found, err := x.Tracker.FindComment(s.ctx, x.request, markerReleased)
	if err != nil || found == nil || *found != second {
		t.Errorf("FindComment() = %+v, %v; want the comment as Comment() returned it, %+v", found, err, second)
	}
}

func (s trackerSuite) firstComment(t *testing.T) {
	x := s.filed(t)
	first, err := x.Tracker.Comment(s.ctx, x.request, answerPlanned)
	if err != nil {
		t.Fatalf("Comment() = %v", err)
	}
	// A second answer with the marker, as two rounds that raced leave.
	if _, err := x.Tracker.Comment(s.ctx, x.request, "Planned as: a dark theme\n\n"+markerPlanned); err != nil {
		t.Fatalf("Comment() = %v", err)
	}
	found, err := x.Tracker.FindComment(s.ctx, x.request, markerPlanned)
	if err != nil || found == nil || found.ID != first.ID || found.Body != answerPlanned {
		t.Errorf("FindComment() of a marker two comments have = %+v, %v; want the first, %+v", found, err, first)
	}
}

func (s trackerSuite) emptyMarker(t *testing.T) {
	x := s.filed(t)
	if _, err := x.Tracker.Comment(s.ctx, x.request, answerPlanned); err != nil {
		t.Fatalf("Comment() = %v", err)
	}
	if found, err := x.Tracker.FindComment(s.ctx, x.request, ""); err != nil || found != nil {
		t.Errorf("FindComment() of an empty marker = %+v, %v; want none", found, err)
	}
}

func (s trackerSuite) strangers(t *testing.T) {
	x := s.filed(t)
	x.Say(t, x.request, x.Person(t, "mallory"), "Planned, I say.\n\n"+markerPlanned)
	if found, err := x.Tracker.FindComment(s.ctx, x.request, markerPlanned); err != nil || found != nil {
		t.Fatalf("FindComment() of a marker only someone else wrote = %+v, %v; want none", found, err)
	}
	if _, err := x.Tracker.Comment(s.ctx, x.request, answerPlanned); err != nil {
		t.Fatalf("Comment() = %v", err)
	}
	found, err := x.Tracker.FindComment(s.ctx, x.request, markerPlanned)
	if err != nil || found == nil || found.Body != answerPlanned {
		t.Errorf("FindComment() after someone else's marker = %+v, %v; want the factory's own comment", found, err)
	}
}

// wantClosed closes issue number of x with reason through the port, and
// fails t unless the tracker then shows it closed with that reason.
func (s trackerSuite) wantClosed(t *testing.T, x tracked, number int, reason ports.CloseReason) {
	t.Helper()
	if err := x.Tracker.CloseIssue(s.ctx, number, reason); err != nil {
		t.Fatalf("CloseIssue(%s) = %v", reason, err)
	}
	if got := x.Issue(t, number); !got.Closed || got.Reason != reason {
		t.Errorf("after CloseIssue(%s) the issue is closed %v, with the reason %q", reason, got.Closed, got.Reason)
	}
}

func (s trackerSuite) closes(t *testing.T) {
	x := s.filed(t)
	other := x.File(t, x.ada, requestExport, "")
	s.wantClosed(t, x, x.request, ports.CloseNotPlanned)
	wantListed(t, s.list(t, x.Tracker, ""), other)
	s.wantClosed(t, x, other, ports.CloseCompleted)
	wantListed(t, s.list(t, x.Tracker, ""))
}

func (s trackerSuite) closesAgain(t *testing.T) {
	x := s.filed(t)
	other := x.File(t, x.ada, requestExport, "")
	// A closed issue, as one whose close was answered by an outage.
	s.wantClosed(t, x, x.request, ports.CloseNotPlanned)
	s.wantClosed(t, x, x.request, ports.CloseCompleted)
	// And one its author closed meanwhile.
	x.Close(t, other)
	s.wantClosed(t, x, other, ports.CloseNotPlanned)
	wantListed(t, s.list(t, x.Tracker, ""))
}

func (s trackerSuite) badReason(t *testing.T) {
	x := s.filed(t)
	if err := x.Tracker.CloseIssue(s.ctx, x.request, "duplicate"); err == nil {
		t.Error("CloseIssue() with a reason that is not one of the two = nil, want an error")
	}
	wantUntouched(t, x)
}

func (s trackerSuite) keepsText(t *testing.T) {
	x := s.filed(t)
	grace := x.Person(t, authorGrace)
	const said = "I'd like that too."
	x.Say(t, x.request, grace, said)
	for _, call := range s.answers(x.Tracker, "", x.request) {
		if call.err != nil {
			t.Fatalf("%s = %v", call.name, call.err)
		}
	}
	got := x.Issue(t, x.request)
	if got.Title != requestDarkMode || got.Body != requestWhy || !strings.EqualFold(got.Author, x.ada) {
		t.Errorf("an answered issue is %q by %q with the body %q; want it as filed: %q by %q with %q", got.Title,
			got.Author, got.Body, requestDarkMode, x.ada, requestWhy)
	}
	want := []TrackedComment{{Body: said}, {Body: answerPlanned, Own: true}}
	if !slices.Equal(got.Comments, want) {
		t.Errorf("an answered issue shows the comments %+v, want %+v", got.Comments, want)
	}
}

func (s trackerSuite) issueNotFound(t *testing.T) {
	x := s.filed(t)
	for _, call := range s.answers(x.Tracker, "", x.request+41)[1:] {
		wantReason(t, call.name+" on an issue that isn't there", call.err, ports.ReasonIssueNotFound)
	}
	wantUntouched(t, x)
}

func (s trackerSuite) refused(t *testing.T) {
	x := s.filed(t)
	x.Join(t, x.Team, x.ada)
	const with = " with a credential the tracker doesn't know"
	for _, call := range s.answers(x.Refused, "", x.request) {
		wantReason(t, call.name+with, call.err, ports.ReasonUnauthorized)
	}
	_, err := x.Refused.TeamMember(s.ctx, x.Team, x.ada)
	wantReason(t, "TeamMember()"+with, err, ports.ReasonUnauthorized)
	wantUntouched(t, x)
}

func (s trackerSuite) lacksIssues(t *testing.T) {
	x := s.filed(t)
	for _, call := range s.answers(x.LacksIssues, "", x.request) {
		wantReason(t, call.name+" with a credential that may not use the issues", call.err,
			ports.ReasonTokenLacksIssues)
	}
	wantUntouched(t, x)
}

func (s trackerSuite) lacksMembers(t *testing.T) {
	x := s.filed(t)
	x.Join(t, x.Team, x.ada)
	member, err := x.LacksMembers.TeamMember(s.ctx, x.Team, x.ada)
	wantReason(t, "TeamMember() with a credential that may not read the members", err, ports.ReasonTokenLacksMembers)
	if member {
		t.Error("TeamMember() that failed said the login is a member")
	}
}

func (s trackerSuite) repositoryNotFound(t *testing.T) {
	x := s.filed(t)
	list, err := x.Elsewhere.ListOpenIssues(s.ctx, "")
	wantReason(t, "ListOpenIssues() of a repository that isn't there", err, ports.ReasonRepositoryNotFound)
	if len(list.Issues) != 0 || list.NotModified {
		t.Errorf("ListOpenIssues() that failed returned %+v", list)
	}
}

func (s trackerSuite) disabled(t *testing.T) {
	x := s.filed(t)
	etag := s.list(t, x.Tracker, "").ETag
	x.Disable(t)
	for _, etag := range []string{"", etag} {
		_, err := x.Tracker.ListOpenIssues(s.ctx, etag)
		wantReason(t, fmt.Sprintf("ListOpenIssues(%q) with the issues turned off", etag), err,
			ports.ReasonIssuesDisabled)
	}
}

func (s trackerSuite) unavailable(t *testing.T) {
	x := s.filed(t)
	x.Join(t, x.Team, x.ada)
	etag := s.list(t, x.Tracker, "").ETag
	x.Down(t)
	const down = " of a tracker that doesn't answer"
	for _, call := range s.answers(x.Tracker, etag, x.request) {
		wantReason(t, call.name+down, call.err, ports.ReasonGitHubUnavailable)
	}
	member, err := x.Tracker.TeamMember(s.ctx, x.Team, x.ada)
	wantReason(t, "TeamMember()"+down, err, ports.ReasonGitHubUnavailable)
	if member {
		t.Error("TeamMember() that failed said the login is a member")
	}
}
