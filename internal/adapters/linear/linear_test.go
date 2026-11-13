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

package linear

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/JairJosafath/theos/internal/adapters/linear/lineartest"
	"github.com/JairJosafath/theos/internal/ports"
)

const marker = "<!-- test:request stage=planned -->"

// world is a fake Linear with the team ENG, an account of the factory's
// and its tracker of ENG's requests.
type world struct {
	*lineartest.Server
	token   string
	tracker ports.IssueTracker
}

func newWorld(t *testing.T) world {
	t.Helper()
	s := lineartest.New(t)
	s.Team(testTeam)
	token := s.Account(testAccount)
	return world{Server: s, token: token, tracker: tracker(t, s, testTeam, token)}
}

// request files an item of ENG that carries the label.
func (w world) request(title string) int {
	return w.File(lineartest.Item{Team: testTeam, Title: title, Labels: []string{testLabel},
		By: lineartest.Maker{User: "ada"}})
}

// listed is the numbers of the open requests, oldest first.
func (w world) listed(t *testing.T) []int {
	t.Helper()
	list, err := w.tracker.ListOpenIssues(context.Background(), "")
	if err != nil {
		t.Fatalf("ListOpenIssues() = %v", err)
	}
	numbers := make([]int, 0, len(list.Issues))
	for _, i := range list.Issues {
		numbers = append(numbers, i.Number)
	}
	return numbers
}

func wantReason(t *testing.T, what string, err error, reason string) {
	t.Helper()
	if err == nil || ports.IssuesReason(err) != reason {
		t.Errorf("%s = %v, want a failure with the reason %s", what, err, reason)
	}
}

// TestETag covers #156 (decision 18): Linear has no conditional list, so the
// adapter's ETag is a hash of the open items' IDs and update times.
func TestETag(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	first := w.request("Dark mode")
	list, err := w.tracker.ListOpenIssues(ctx, "")
	if err != nil || len(list.Issues) != 1 || len(list.ETag) != 64 {
		t.Fatalf("ListOpenIssues() = %+v, %v; want one issue and an ETag that is a SHA-256", list, err)
	}
	etag := list.ETag

	// What isn't an open request of the team with the label changes nothing.
	w.File(lineartest.Item{Team: testTeam, Title: "No label"})
	w.File(lineartest.Item{Team: testTeam, Title: "Another label", Labels: []string{"other"}})
	w.Team("OPS")
	w.File(lineartest.Item{Team: "OPS", Title: "Another team", Labels: []string{testLabel}})
	w.Say(testTeam, first, "grace", "I'd like that too.")
	before := w.Calls(opOpenIssues)
	for range 2 {
		same, err := w.tracker.ListOpenIssues(ctx, etag)
		if err != nil || !same.NotModified || same.ETag != etag || len(same.Issues) != 0 {
			t.Fatalf("ListOpenIssues(%q) of an unchanged list = %+v, %v; want NotModified with the same ETag", etag, same, err)
		}
	}
	// An unchanged list costs the IDs and times alone, never the items' text.
	if got := w.Calls(opOpenIssues); got != before {
		t.Errorf("an unchanged list read the items' text %d more times, want none", got-before)
	}
	if got := w.Calls(opOpenStamps); got != 2 {
		t.Errorf("an unchanged list was asked 2 times, and read its stamps %d times", got)
	}

	// Each change of what is listed changes the ETag.
	for _, step := range []struct {
		name   string
		change func()
	}{
		{"a request filed", func() { w.request("Export") }},
		{"a request edited", func() { w.Edit(testTeam, first, "More.") }},
		{"a label taken off", func() { w.Unlabel(testTeam, first, testLabel) }},
		{"the label put back", func() { w.Label(testTeam, first, testLabel) }},
		{"a request closed", func() { w.Close(testTeam, first) }},
	} {
		step.change()
		next, err := w.tracker.ListOpenIssues(ctx, etag)
		if err != nil || next.NotModified || next.ETag == etag {
			t.Fatalf("ListOpenIssues(%q) after %s = %+v, %v; want the list read again, with another ETag", etag, step.name,
				next, err)
		}
		etag = next.ETag
	}
	// An ETag the adapter didn't make is a list that changed.
	if got, err := w.tracker.ListOpenIssues(ctx, "W/\"abc\""); err != nil || got.NotModified {
		t.Errorf("ListOpenIssues() with another's ETag = %+v, %v; want the list read", got, err)
	}
}

// TestClosesIntoTheFirstStateOfItsType covers #156 (decision 17): the team's
// first state of type completed, or of type canceled for not planned, and a
// closed item closed again with the other reason moves to the other state.
func TestClosesIntoTheFirstStateOfItsType(t *testing.T) {
	ctx := context.Background()
	s := lineartest.New(t)
	// Their order in the workflow is their position, not their ID or name.
	s.Team("OPS", lineartest.State{Name: "Todo", Type: lineartest.Unstarted, Position: 1},
		lineartest.State{Name: "Shipped", Type: lineartest.Completed, Position: 9},
		lineartest.State{Name: "Released", Type: lineartest.Completed, Position: 3},
		lineartest.State{Name: "Won't Fix", Type: lineartest.Canceled, Position: 2},
		lineartest.State{Name: "Abandoned", Type: lineartest.Canceled, Position: 1})
	tr := tracker(t, s, "OPS", s.Account(testAccount))
	n := s.File(lineartest.Item{Team: "OPS", Title: "Dark mode", Labels: []string{testLabel}})
	in := func() string { return s.Show("OPS", n).State }
	close := func(reason ports.CloseReason, want string, moves int) {
		t.Helper()
		before := s.Calls(opMove)
		if err := tr.CloseIssue(ctx, n, reason); err != nil {
			t.Fatalf("CloseIssue(%s) = %v", reason, err)
		}
		if got := in(); got != want || s.Calls(opMove)-before != moves {
			t.Errorf("after CloseIssue(%s) the item is in %q after %d moves, want %q after %d", reason, got,
				s.Calls(opMove)-before, want, moves)
		}
	}
	close(ports.CloseCompleted, "Released", 1)
	close(ports.CloseCompleted, "Released", 0)
	close(ports.CloseNotPlanned, "Abandoned", 1)
	close(ports.CloseNotPlanned, "Abandoned", 0)
	close(ports.CloseCompleted, "Released", 1)
	// A person closed it into another state of the type: it is closed so.
	s.MoveTo("OPS", n, "Shipped")
	close(ports.CloseCompleted, "Shipped", 0)
	close(ports.CloseNotPlanned, "Abandoned", 1)
}

// TestClosesWhereLinearHasNoStateOfTheType: a workflow without the state of
// the type to move to fails with TrackerError, the item left where it is.
func TestClosesWhereLinearHasNoStateOfTheType(t *testing.T) {
	s := lineartest.New(t)
	s.Team("BAR", lineartest.State{Name: "Todo", Type: lineartest.Unstarted, Position: 1},
		lineartest.State{Name: "Done", Type: lineartest.Completed, Position: 2})
	tr := tracker(t, s, "BAR", s.Account(testAccount))
	n := s.File(lineartest.Item{Team: "BAR", Title: "Dark mode", Labels: []string{testLabel}})
	err := tr.CloseIssue(context.Background(), n, ports.CloseNotPlanned)
	wantReason(t, "CloseIssue(not planned) in a team with no canceled state", err, ports.ReasonTrackerError)
	if err != nil && !strings.Contains(err.Error(), "canceled") {
		t.Errorf("the error is %q, want it to name the state type that is missing", err)
	}
	if got := s.Show("BAR", n); got.State != "Todo" {
		t.Errorf("the item is in %q after a close that failed, want Todo", got.State)
	}
	if err := tr.CloseIssue(context.Background(), n, ports.CloseCompleted); err != nil {
		t.Errorf("CloseIssue(completed) = %v", err)
	}
}

// TestDuplicateIsNotPlanned: an item a person marked a duplicate is closed,
// and closed as not planned; closing it as completed moves it.
func TestDuplicateIsNotPlanned(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	n := w.request("Dark mode")
	w.MoveTo(testTeam, n, "Duplicate")
	if got := w.listed(t); len(got) != 0 {
		t.Errorf("a duplicate is listed: %v", got)
	}
	if err := w.tracker.CloseIssue(ctx, n, ports.CloseNotPlanned); err != nil || w.Calls(opMove) != 0 {
		t.Errorf("CloseIssue(not planned) of a duplicate = %v after %d moves, want none", err, w.Calls(opMove))
	}
	if err := w.tracker.CloseIssue(ctx, n, ports.CloseCompleted); err != nil || w.Show(testTeam, n).State != "Done" {
		t.Errorf("CloseIssue(completed) of a duplicate = %v, item in %q, want Done", err, w.Show(testTeam, n).State)
	}
}

// TestBadReasonCallsNothing: a reason that isn't one of the two is refused
// before Linear is asked anything.
func TestBadReasonCallsNothing(t *testing.T) {
	w := newWorld(t)
	n := w.request("Dark mode")
	if err := w.tracker.CloseIssue(context.Background(), n, "duplicate"); err == nil {
		t.Error("CloseIssue() with a reason of Linear's own = nil, want an error")
	}
	if got := len(w.Requests()); got != 0 {
		t.Errorf("a refused reason made %d calls, want none", got)
	}
}

// TestLabelTakenOff covers #156 (decision 13): an item whose label is taken
// off is no longer listed, one whose state is closed isn't either, and the
// label put back lists it again.
func TestLabelTakenOff(t *testing.T) {
	w := newWorld(t)
	w.Team("OPS")
	a, b := w.request("A"), w.request("B")
	w.File(lineartest.Item{Team: testTeam, Title: "No label"})
	w.File(lineartest.Item{Team: testTeam, Title: "Another label", Labels: []string{"other"}})
	w.File(lineartest.Item{Team: "OPS", Title: "Another team", Labels: []string{testLabel}})
	if got := w.listed(t); !slices.Equal(got, []int{a, b}) {
		t.Fatalf("ListOpenIssues() lists %v, want the two labelled items of the team %v", got, []int{a, b})
	}
	w.Unlabel(testTeam, a, testLabel)
	if got := w.listed(t); !slices.Equal(got, []int{b}) {
		t.Errorf("ListOpenIssues() after the label was taken off %d lists %v, want %v", a, got, []int{b})
	}
	w.Label(testTeam, a, testLabel)
	if got := w.listed(t); !slices.Equal(got, []int{a, b}) {
		t.Errorf("ListOpenIssues() after the label was put back lists %v, want %v", got, []int{a, b})
	}

	// What is open is a state of any type but the three that close.
	for state, open := range map[string]bool{"Triage": true, "Backlog": true, "Todo": true, "In Progress": true,
		"Done": false, "Canceled": false, "Duplicate": false} {
		w.MoveTo(testTeam, a, state)
		if got := slices.Contains(w.listed(t), a); got != open {
			t.Errorf("an item in %q is listed: %v, want %v", state, got, open)
		}
	}
}

// TestIssueOpen covers #161: an item read on its own is one the list shows
// while it is in an open state and carries the label; one closed, unlabelled,
// moved to another team or never filed isn't, with no error.
func TestIssueOpen(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.Team("OPS")
	reader, ok := w.tracker.(ports.IssueReader)
	if !ok {
		t.Fatal("the Linear tracker is no ports.IssueReader")
	}
	open, unlabelled, done, canceled, moved := w.request("Open"), w.request("Unlabelled"), w.request("Done"),
		w.request("Canceled"), w.request("Moved")
	w.Unlabel(testTeam, unlabelled, testLabel)
	w.MoveTo(testTeam, done, "Done")
	if err := w.tracker.CloseIssue(ctx, canceled, ports.CloseNotPlanned); err != nil {
		t.Fatal(err)
	}
	w.Move(testTeam, moved, "OPS")
	for n, want := range map[int]bool{open: true, unlabelled: false, done: false, canceled: false, moved: false,
		0: false, 5000: false} {
		if got, err := reader.IssueOpen(ctx, n); err != nil || got != want {
			t.Errorf("IssueOpen(%d) = %v, %v; want %v", n, got, err, want)
		}
	}
	w.Down(true)
	_, err := reader.IssueOpen(ctx, open)
	wantReason(t, "IssueOpen() while Linear is down", err, ports.ReasonTrackerUnavailable)
}

// TestMovedItem covers #156: Linear goes on answering to the key an item
// had before it moved to another team, with the item, which is that team's
// now. The factory must neither comment on it nor close it.
func TestMovedItem(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.Team("OPS")
	for range 3 {
		w.File(lineartest.Item{Team: "OPS", Title: "Theirs"})
	}
	moved, kept := w.request("Dark mode"), w.request("Export")
	if _, err := w.tracker.Comment(ctx, moved, "Planned.\n\n"+marker); err != nil {
		t.Fatal(err)
	}
	now := w.Move(testTeam, moved, "OPS")
	if got := w.Show("OPS", now); got.Identifier != fmt.Sprintf("OPS-%d", now) || !slices.Equal(got.PreviousIdentifiers,
		[]string{fmt.Sprintf("%s-%d", testTeam, moved)}) {
		t.Fatalf("the moved item is %+v, want it OPS's with the key it had before", got)
	}

	if got := w.listed(t); !slices.Equal(got, []int{kept}) {
		t.Errorf("ListOpenIssues() lists %v, want the moved item gone: %v", got, []int{kept})
	}
	found, err := w.tracker.FindComment(ctx, moved, marker)
	wantReason(t, "FindComment() of a moved item", err, ports.ReasonIssueNotFound)
	if found != nil {
		t.Errorf("FindComment() of a moved item = %+v, want none", found)
	}
	_, err = w.tracker.Comment(ctx, moved, "Released.")
	wantReason(t, "Comment() on a moved item", err, ports.ReasonIssueNotFound)
	for _, reason := range []ports.CloseReason{ports.CloseCompleted, ports.CloseNotPlanned} {
		wantReason(t, fmt.Sprintf("CloseIssue(%s) of a moved item", reason), w.tracker.CloseIssue(ctx, moved, reason),
			ports.ReasonIssueNotFound)
	}
	got := w.Show("OPS", now)
	if got.StateType == lineartest.Completed || got.StateType == lineartest.Canceled || len(got.Comments) != 1 {
		t.Errorf("the item that moved is %q with %d comments after the factory's calls, want it open, with the one it had",
			got.State, len(got.Comments))
	}
	// An item moved into the team has a number here, and is a request.
	w.Team("SUP")
	in := w.Move("SUP", w.File(lineartest.Item{Team: "SUP", Title: "From elsewhere", Labels: []string{testLabel}}), testTeam)
	list, err := w.tracker.ListOpenIssues(ctx, "")
	if err != nil || len(list.Issues) != 2 || list.Issues[1].Number != in || list.Issues[1].Key != fmt.Sprintf("ENG-%d", in) {
		t.Errorf("ListOpenIssues() after an item moved in = %+v, %v; want it listed as ENG-%d", list, err, in)
	}
}

// TestAuthor covers #156 (decision 15): the display name of whoever made the
// item, on one line of at most 100 characters; for an item with no person as
// its creator, what Linear gives for who or what made it, else nothing.
func TestAuthor(t *testing.T) {
	w := newWorld(t)
	file := func(by lineartest.Maker) int {
		return w.File(lineartest.Item{Team: testTeam, Title: "Dark mode", Labels: []string{testLabel}, By: by})
	}
	long := strings.Repeat("é", 150)
	for _, tt := range []struct {
		name string
		by   lineartest.Maker
		want string
	}{
		{"a user", lineartest.Maker{User: "ada"}, "ada"},
		{"no creator at all", lineartest.Maker{}, ""},
		{"an integration's bot", lineartest.Maker{Bot: "Zapier"}, "Zapier"},
		{"an external user", lineartest.Maker{External: "Pat the customer"}, "Pat the customer"},
		{"a name on two lines", lineartest.Maker{User: "Ada\nLovelace\r\n\t<b>"}, "Ada Lovelace <b>"},
		{"a name that is too long", lineartest.Maker{User: long}, strings.Repeat("é", 100)},
	} {
		number := file(tt.by)
		list, err := w.tracker.ListOpenIssues(context.Background(), "")
		if err != nil {
			t.Fatalf("%s: ListOpenIssues() = %v", tt.name, err)
		}
		var got *ports.TrackerIssue
		for _, i := range list.Issues {
			if i.Number == number {
				got = &i
			}
		}
		if got == nil || got.Author != tt.want || !utf8.ValidString(got.Author) {
			t.Errorf("%s: the author is %+v, want %q", tt.name, got, tt.want)
		}
	}
}

// TestKeys: numbers stay numbers (decision 14). The port's int is Linear's
// number, which isn't consecutive, the adapter writes the key itself, and a
// call names the item by that key, a comment going to it by its ID.
func TestKeys(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.Skip(testTeam, 411)
	n := w.request("Dark mode")
	list, err := w.tracker.ListOpenIssues(ctx, "")
	if err != nil || len(list.Issues) != 1 || list.Issues[0].Number != 412 || list.Issues[0].Key != "ENG-412" || n != 412 {
		t.Fatalf("ListOpenIssues() = %+v, %v; want the issue 412 with the key ENG-412", list, err)
	}
	if want := "https://linear.test/acme/issue/ENG-412"; list.Issues[0].URL != want {
		t.Errorf("the issue's page is %q, want %q", list.Issues[0].URL, want)
	}
	if _, err := w.tracker.FindComment(ctx, n, marker); err != nil {
		t.Fatal(err)
	}
	if _, err := w.tracker.Comment(ctx, n, "Planned.\n\n"+marker); err != nil {
		t.Fatal(err)
	}
	if err := w.tracker.CloseIssue(ctx, n, ports.CloseCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := w.tracker.(ports.IssueReader).IssueOpen(ctx, n); err != nil {
		t.Fatal(err)
	}
	for _, r := range w.Requests() {
		switch r.Operation {
		case opIssue, opIssueOpen, opComments, opCloseTarget:
			if r.Variables["id"] != "ENG-412" {
				t.Errorf("%s asked for the item %v, want its key ENG-412", r.Operation, r.Variables["id"])
			}
		case opComment:
			input, _ := r.Variables["input"].(map[string]any)
			if id, _ := input["issueId"].(string); id == "" || id == "ENG-412" {
				t.Errorf("the comment went to %q, want the item's ID", id)
			}
		case opMove:
			if id, _ := r.Variables["id"].(string); id == "" || id == "ENG-412" {
				t.Errorf("the move was of %q, want the item's ID", id)
			}
		}
	}
	for _, number := range []int{0, -3, 5000} {
		if _, err := w.tracker.Comment(ctx, number, "x"); ports.IssuesReason(err) != ports.ReasonIssueNotFound {
			t.Errorf("Comment() on issue %d = %v, want IssueNotFound", number, err)
		}
	}
}

// TestKeyIsSentAsItIs: an API key goes in the Authorization header with no
// scheme before it, which is how Linear's GraphQL page has a personal key
// sent; "Bearer" is an OAuth token's.
func TestKeyIsSentAsItIs(t *testing.T) {
	w := newWorld(t)
	w.request("Dark mode")
	w.listed(t)
	requests := w.Requests()
	if len(requests) == 0 || requests[0].Authorization != w.token {
		t.Errorf("the key was sent as %+v, want %q alone", requests, w.token)
	}
}

// TestPages: Linear lists at most 250 nodes a page, newest first. The list
// comes back whole and oldest first, and a comment is found past the first
// page, the first of the account's own with the marker.
func TestPages(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	const total = 2*pageSize + 30
	want := make([]int, 0, total)
	for i := range total {
		want = append(want, w.request(fmt.Sprintf("Request %d", i)))
	}
	list, err := w.tracker.ListOpenIssues(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, i := range list.Issues {
		got = append(got, i.Number)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ListOpenIssues() listed %d issues, %v...; want %d, oldest first", len(got), got[:min(5, len(got))], total)
	}
	if again, err := w.tracker.ListOpenIssues(ctx, list.ETag); err != nil || !again.NotModified {
		t.Errorf("ListOpenIssues() of %d unchanged issues = %+v, %v; want NotModified", total, again, err)
	}

	n := want[0]
	for i := range total {
		switch i {
		case 120, 121:
			w.Say(testTeam, n, testAccount, fmt.Sprintf("Answer %d\n\n%s", i, marker))
		case 125:
			w.Say(testTeam, n, "mallory", "Planned, I say.\n\n"+marker)
		default:
			w.Say(testTeam, n, "grace", fmt.Sprintf("Comment %d", i))
		}
	}
	before := w.Calls(opComments)
	found, err := w.tracker.FindComment(ctx, n, marker)
	if err != nil || found == nil || !strings.HasPrefix(found.Body, "Answer 120") {
		t.Errorf("FindComment() among %d comments = %+v, %v; want the first of its own with the marker, Answer 120", total,
			found, err)
	}
	if got := w.Calls(opComments) - before; got != 3 {
		t.Errorf("FindComment() read %d pages of comments, want 3", got)
	}
}

// TestComment: what is returned is the comment Linear made, by its ID, with
// the display name of the token's account and its page.
func TestComment(t *testing.T) {
	w := newWorld(t)
	n := w.request("Dark mode")
	got, err := w.tracker.Comment(context.Background(), n, "Planned.\n\n"+marker)
	if err != nil || got.ID == "" || got.Author != testAccount || got.Body != "Planned.\n\n"+marker ||
		!strings.HasPrefix(got.URL, "https://linear.test/") {
		t.Errorf("Comment() = %+v, %v; want the comment by %s with its ID and page", got, err, testAccount)
	}
}

// TestFailures reads Linear's failures by what its answer says and, where it
// says nothing, by the status. The codes are from memory of Linear's own
// answers: its reference lists none.
func TestFailures(t *testing.T) {
	json := func(status int, body string) func(http.ResponseWriter) {
		return func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = fmt.Fprint(w, body)
		}
	}
	oneError := func(message, kind, code string) string {
		return fmt.Sprintf(`{"errors":[{"message":%q,"extensions":{"type":%q,"code":%q}}],"data":null}`, message, kind, code)
	}
	for _, tt := range []struct {
		name  string
		reply func(http.ResponseWriter)
		// list is the reason of ListOpenIssues, issue that of a call on an
		// item.
		list, issue string
	}{
		{"401 with no body", func(w http.ResponseWriter) { w.WriteHeader(http.StatusUnauthorized) },
			ports.ReasonUnauthorized, ports.ReasonUnauthorized},
		{"400 that says authentication error", json(400, oneError("Authentication required", "authentication error", "AUTHENTICATION_ERROR")),
			ports.ReasonUnauthorized, ports.ReasonUnauthorized},
		{"403 with no body", func(w http.ResponseWriter) { w.WriteHeader(http.StatusForbidden) },
			ports.ReasonTokenLacksIssues, ports.ReasonTokenLacksIssues},
		{"200 that says forbidden", json(200, oneError("No access", "forbidden", "FORBIDDEN")),
			ports.ReasonTokenLacksIssues, ports.ReasonTokenLacksIssues},
		{"429 that says rate limited", json(429, oneError("Rate limit exceeded", "ratelimited", "RATELIMITED")),
			ports.ReasonTrackerUnavailable, ports.ReasonTrackerUnavailable},
		{"502 from a proxy", func(w http.ResponseWriter) { http.Error(w, "<html>Bad gateway</html>", http.StatusBadGateway) },
			ports.ReasonTrackerUnavailable, ports.ReasonTrackerUnavailable},
		{"an entity that isn't found", json(200, oneError("Entity not found: Issue - Could not find referenced Issue.", "invalid input", "INVALID_INPUT")),
			ports.ReasonTrackerNotFound, ports.ReasonIssueNotFound},
		{"an error and its data on a 200", json(200, `{"data":{"issue":{"id":"x","number":1,"team":{"key":"ENG"}}},"errors":[{"message":"boom"}]}`),
			ports.ReasonTrackerError, ports.ReasonTrackerError},
		{"a 400 of Linear's own words", json(400, oneError("Variable \"$first\" got invalid value", "invalid input", "INVALID_INPUT")),
			ports.ReasonTrackerError, ports.ReasonTrackerError},
		{"data that is null", json(200, `{"data":null}`), ports.ReasonTrackerError, ports.ReasonTrackerError},
		{"an answer that isn't JSON", func(w http.ResponseWriter) { _, _ = fmt.Fprint(w, "hello") },
			ports.ReasonTrackerError, ports.ReasonTrackerError},
		{"a redirect", func(w http.ResponseWriter) { w.Header().Set("Location", "/elsewhere"); w.WriteHeader(http.StatusFound) },
			ports.ReasonTrackerError, ports.ReasonTrackerError},
		{"a message of many lines", json(400, oneError(strings.Repeat("a very long line\n", 100), "x", "X")),
			ports.ReasonTrackerError, ports.ReasonTrackerError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { tt.reply(w) }))
			defer srv.Close()
			tr, err := Client{}.Issues(Config{Team: testTeam, Label: testLabel, Token: rand.Text(), APIURL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			list, err := tr.ListOpenIssues(context.Background(), "")
			wantReason(t, "ListOpenIssues()", err, tt.list)
			if len(list.Issues) != 0 || list.NotModified || list.ETag != "" {
				t.Errorf("ListOpenIssues() that failed returned %+v", list)
			}
			closeErr := tr.CloseIssue(context.Background(), 1, ports.CloseCompleted)
			wantReason(t, "CloseIssue()", closeErr, tt.issue)
			for _, e := range []error{err, closeErr} {
				if e != nil && (strings.Contains(e.Error(), "\n") || len(e.Error()) > 400) {
					t.Errorf("the error is %d characters on %d lines, want one short line: %q", len(e.Error()),
						strings.Count(e.Error(), "\n")+1, e)
				}
			}
		})
	}

	t.Run("no answer", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		tr, err := Client{}.Issues(Config{Team: testTeam, Label: testLabel, Token: rand.Text(), APIURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		srv.Close()
		_, err = tr.ListOpenIssues(context.Background(), "")
		wantReason(t, "ListOpenIssues() of Linear that doesn't answer", err, ports.ReasonTrackerUnavailable)
	})
}

// TestConfig: what Issues refuses, and Linear's own address as its default.
func TestConfig(t *testing.T) {
	token := rand.Text()
	for name, cfg := range map[string]Config{
		"no team":                    {Label: testLabel, Token: token},
		"a team with a space":        {Team: "E NG", Label: testLabel, Token: token},
		"no label":                   {Team: testTeam, Token: token},
		"no token":                   {Team: testTeam, Label: testLabel},
		"a token on two lines":       {Team: testTeam, Label: testLabel, Token: token + "\nX-Other: 1"},
		"an address that isn't one":  {Team: testTeam, Label: testLabel, Token: token, APIURL: "api.linear.app"},
		"an address that isn't http": {Team: testTeam, Label: testLabel, Token: token, APIURL: "ftp://api.linear.app/graphql"},
	} {
		if tr, err := (Client{}).Issues(cfg); err == nil {
			t.Errorf("Issues() with %s = %v, want an error", name, tr)
		}
	}
	tr, err := Client{}.Issues(Config{Team: testTeam, Label: testLabel, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.(*linear).api; got != "https://api.linear.app/graphql" {
		t.Errorf("the default address is %q, want https://api.linear.app/graphql (decision 20)", got)
	}
}

// TestOperations: each query is the operation its name says, which the fake
// Linear answers by.
func TestOperations(t *testing.T) {
	for op, text := range map[string]string{opOpenIssues: queryOpenIssues, opOpenStamps: queryOpenStamps,
		opIssue: queryIssue, opIssueOpen: queryIssueOpen, opComments: queryComments, opComment: mutationComment,
		opCloseTarget: queryCloseTarget, opMove: mutationMove} {
		if !strings.HasPrefix(text, "query "+op+"(") && !strings.HasPrefix(text, "mutation "+op+"(") {
			t.Errorf("the text of %s starts %q", op, text[:min(40, len(text))])
		}
		if strings.Count(text, "{") != strings.Count(text, "}") || strings.Count(text, "(") != strings.Count(text, ")") {
			t.Errorf("the text of %s has unbalanced brackets", op)
		}
	}
}

func TestOneLine(t *testing.T) {
	for _, tt := range []struct {
		in    string
		limit int
		want  string
	}{
		{"ada", 100, "ada"},
		{"  ada \n\t lovelace\r\n", 100, "ada lovelace"},
		{"a\u2028b\u0007c", 100, "a b c"},
		{"abcdef", 3, "abc"},
		{"ab cd", 3, "ab"},
		{"ab cd", 4, "ab c"},
		{"ééé", 2, "éé"},
		{"", 5, ""},
		{" \n ", 5, ""},
	} {
		if got := oneLine(tt.in, tt.limit); got != tt.want {
			t.Errorf("oneLine(%q, %d) = %q, want %q", tt.in, tt.limit, got, tt.want)
		}
	}
}

// TestLowerCaseTeam covers #156: a team configured as "eng" lists and reads
// the items of ENG, as Linear's filter compares the key case-sensitively.
func TestLowerCaseTeam(t *testing.T) {
	w := newWorld(t)
	n := w.request("Dark mode")
	tr := tracker(t, w.Server, strings.ToLower(testTeam), w.token)
	list, err := tr.ListOpenIssues(context.Background(), "")
	if err != nil || len(list.Issues) != 1 || list.Issues[0].Number != n {
		t.Fatalf("ListOpenIssues() = %v, %v, want item %d", list.Issues, err, n)
	}
	if c, err := tr.FindComment(context.Background(), n, marker); err != nil || c != nil {
		t.Errorf("FindComment(%d) = %v, %v, want none and no error", n, c, err)
	}
}

// TestTrackerFromASource covers #161: the operator builds the adapter from a
// Brief's source, the Secret's key token and its own address for Linear.
func TestTrackerFromASource(t *testing.T) {
	s := lineartest.New(t)
	s.Team(testTeam)
	token := s.Account(testAccount)
	source := ports.TrackerSource{Tracker: "linear", Project: testTeam, Label: testLabel}
	tr, err := Client{APIURL: s.URL()}.Tracker(context.Background(), source,
		map[string][]byte{SecretToken: []byte(token + "\n")})
	if err != nil {
		t.Fatalf("Tracker() = %v", err)
	}
	w := world{Server: s, token: token, tracker: tr}
	n := w.request("Dark mode")
	if got := w.listed(t); !slices.Equal(got, []int{n}) {
		t.Errorf("listed %v, want [%d]", got, n)
	}
	_, err = Client{APIURL: s.URL()}.Tracker(context.Background(), source, nil)
	if missing, ok := errors.AsType[*ports.CredentialMissingError](err); !ok || missing.Keys != "the key token" {
		t.Errorf("Tracker() with no Secret = %v, want the key token missing", err)
	}
	source.Project = "E NG"
	if _, err := (Client{APIURL: s.URL()}).Tracker(context.Background(), source,
		map[string][]byte{SecretToken: []byte(token)}); ports.IssuesReason(err) != ports.ReasonTrackerError {
		t.Errorf("Tracker() with a team that isn't a key = %v, want %s", err, ports.ReasonTrackerError)
	}
}
