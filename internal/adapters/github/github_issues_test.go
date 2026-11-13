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

package github

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// fakeGitHubIssues is GitHub's issues API for fakeGitHubRepo and its teams'
// memberships. A token it doesn't know is a 401, another repository a 404,
// and it fails the test on any other request.
type fakeGitHubIssues struct {
	errorf func(format string, args ...any)
	// url is where it listens, and token the token that may do all of it.
	url, token string
	// lacks holds the tokens that may not use the issues (lacksIssues) or
	// read a team's members (lacksMembers).
	lacks map[string]string

	mu sync.Mutex
	// issues holds issues and pull requests, oldest first, open and closed.
	issues   []githubIssueState
	comments map[int][]githubComment
	// members is team -> login -> state.
	members map[string]map[string]string
	nextID  int64
	// calls records each request as "METHOD path?query If-None-Match".
	calls []string
	// fail, when set, answers every request instead.
	fail func(w http.ResponseWriter, r *http.Request)
}

// What a token in fakeGitHubIssues.lacks may not do.
const (
	lacksIssues  = "issues"
	lacksMembers = "members"
)

type githubIssueState struct {
	githubIssue
	State       string
	StateReason string
}

func newFakeGitHubIssues(t *testing.T) (*fakeGitHubIssues, ports.IssueTracker) {
	t.Helper()
	f := &fakeGitHubIssues{errorf: t.Errorf, token: rand.Text(), lacks: map[string]string{},
		comments: map[int][]githubComment{}, members: map[string]map[string]string{}, nextID: 1000}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	f.url = srv.URL
	tracker, err := Client{}.GitHubIssues(ports.GitHubConfig{Repository: fakeGitHubRepo, Token: f.token, APIURL: srv.URL})
	if err != nil {
		t.Fatalf("GitHubIssues() error = %v", err)
	}
	return f, tracker
}

// open adds an open issue, or a pull request, by author.
func (f *fakeGitHubIssues) open(title, author string, pull bool) int {
	return f.file(title, "body of "+title, author, pull)
}

// file adds an open issue, or a pull request, by author with body.
func (f *fakeGitHubIssues) file(title, body, author string, pull bool) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(f.issues) + 1
	i := githubIssue{Number: n, Title: title, Body: body, User: githubUser{Login: author},
		HTMLURL: fmt.Sprintf("https://github.com/%s/issues/%d", fakeGitHubRepo, n),
		Created: time.Date(2026, 10, 9, 0, 0, n, 0, time.UTC)}
	if pull {
		i.PullRequest = json.RawMessage(`{"url":"x"}`)
	}
	f.issues = append(f.issues, githubIssueState{githubIssue: i, State: "open"})
	return n
}

func (f *fakeGitHubIssues) issue(n int) *githubIssueState {
	if n < 1 || n > len(f.issues) {
		return nil
	}
	return &f.issues[n-1]
}

// etag is a hash of the open list's first page, as GitHub's is of its body.
func etagOf(items []githubIssue) string {
	data, _ := json.Marshal(items)
	h := fnv.New64a()
	_, _ = h.Write(data)
	return fmt.Sprintf(`W/"%x"`, h.Sum64())
}

func (f *fakeGitHubIssues) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.TrimSpace(r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("If-None-Match")))
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if _, limited := f.lacks[token]; token != f.token && !limited {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})
		return
	}
	if f.fail != nil {
		f.fail(w, r)
		return
	}
	issues := "/repos/" + fakeGitHubRepo + "/issues"
	path := r.URL.Path
	switch {
	case f.lacks[token] == lacksIssues && strings.HasPrefix(path, "/repos/"),
		f.lacks[token] == lacksMembers && strings.HasPrefix(path, "/orgs/"):
		writeJSON(w, http.StatusForbidden,
			map[string]string{"message": "Resource not accessible by personal access token"})
	case strings.HasPrefix(path, "/repos/") && !strings.HasPrefix(path, "/repos/"+fakeGitHubRepo+"/"):
		// Another repository: none the token can see.
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	case path == "/user" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, githubUser{Login: "Theos-Bot"})
	case path == issues && r.Method == http.MethodGet:
		f.list(w, r)
	case strings.HasPrefix(path, issues+"/"):
		rest := strings.Split(strings.TrimPrefix(path, issues+"/"), "/")
		n, _ := strconv.Atoi(rest[0])
		issue := f.issue(n)
		if issue == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
			return
		}
		switch {
		case len(rest) == 1 && r.Method == http.MethodPatch:
			issue.patch(r)
			writeJSON(w, http.StatusOK, issue.githubIssue)
		case len(rest) == 2 && rest[1] == "comments" && r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, page(r, f.comments[n]))
		case len(rest) == 2 && rest[1] == "comments" && r.Method == http.MethodPost:
			var in struct{ Body string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.nextID++
			c := githubComment{ID: f.nextID, Body: in.Body, User: githubUser{Login: "theos-bot"},
				HTMLURL: fmt.Sprintf("https://github.com/%s/issues/%d#issuecomment-%d", fakeGitHubRepo, n, f.nextID)}
			f.comments[n] = append(f.comments[n], c)
			writeJSON(w, http.StatusCreated, c)
		default:
			f.errorf("unexpected request %s %s", r.Method, path)
			http.NotFound(w, r)
		}
	case strings.HasPrefix(path, "/orgs/") && r.Method == http.MethodGet:
		// /orgs/{org}/teams/{slug}/memberships/{login}
		parts := strings.Split(strings.TrimPrefix(path, "/orgs/"), "/")
		if len(parts) != 5 || parts[1] != "teams" || parts[3] != "memberships" {
			f.errorf("unexpected request %s %s", r.Method, path)
			http.NotFound(w, r)
			return
		}
		state, ok := f.members[parts[0]+"/"+parts[2]][parts[4]]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"state": state, "role": "member"})
	default:
		f.errorf("unexpected request %s %s", r.Method, path)
		http.NotFound(w, r)
	}
}

// listQuery is what the client lists the issues with, on every page. The
// fake answers a list without state=open or sort=created as GitHub does, the
// same, so the tests of the list hold the client to asking for both.
const listQuery = "state=open&sort=created&direction=asc"

// list answers a page of the issues as GitHub lists them: the open ones, the
// newest first, unless the query says otherwise.
func (f *fakeGitHubIssues) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if sort := q.Get("sort"); sort != "" && sort != "created" {
		f.errorf("listing issues with %s", r.URL.RawQuery)
	}
	state := cmp.Or(q.Get("state"), "open")
	var listed []githubIssue
	for _, i := range f.issues {
		if state == "all" || i.State == state {
			listed = append(listed, i.githubIssue)
		}
	}
	if q.Get("direction") != "asc" {
		slices.Reverse(listed)
	}
	items := page(r, listed)
	etag := etagOf(items)
	w.Header().Set("ETag", etag)
	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// patch changes the fields r sends and no other, an issue's text among them.
func (i *githubIssueState) patch(r *http.Request) {
	var in struct {
		State       *string `json:"state"`
		StateReason string  `json:"state_reason"`
		Title, Body *string
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.State != nil {
		i.State, i.StateReason = *in.State, in.StateReason
	}
	if in.Title != nil {
		i.Title = *in.Title
	}
	if in.Body != nil {
		i.Body = *in.Body
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeGitHubIssues) takeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := f.calls
	f.calls = nil
	return calls
}

func numbers(issues []ports.TrackerIssue) []int {
	out := make([]int, 0, len(issues))
	for _, i := range issues {
		out = append(out, i.Number)
	}
	return out
}

// TestGitHubIssuesList lists issues with pull requests left out, asks again
// with the ETag and gets a 304, then sees a new issue and a closed one. Each
// issue's Key is GitHub's "#number" (#151).
func TestGitHubIssuesList(t *testing.T) {
	f, g := newFakeGitHubIssues(t)
	ctx := context.Background()

	empty, err := g.ListOpenIssues(ctx, "")
	if err != nil || len(empty.Issues) != 0 || empty.NotModified || empty.ETag == "" {
		t.Fatalf("ListOpenIssues() on no issues = %+v, %v", empty, err)
	}

	f.open("dark mode", "ada", false)
	f.open("a pull request", "ada", true)
	f.open("export to CSV", "stranger", false)
	list, err := g.ListOpenIssues(ctx, empty.ETag)
	if err != nil {
		t.Fatalf("ListOpenIssues() error = %v", err)
	}
	if list.NotModified || !reflect.DeepEqual(numbers(list.Issues), []int{1, 3}) {
		t.Fatalf("ListOpenIssues() = %+v; want issues 1 and 3", list)
	}
	want := ports.TrackerIssue{Number: 1, Key: "#1", Title: "dark mode", Body: "body of dark mode", Author: "ada",
		URL: "https://github.com/" + fakeGitHubRepo + "/issues/1", Created: time.Date(2026, 10, 9, 0, 0, 1, 0, time.UTC)}
	if !reflect.DeepEqual(list.Issues[0], want) {
		t.Errorf("issue 1 = %+v; want %+v", list.Issues[0], want)
	}
	if list.ETag == "" || list.ETag == empty.ETag {
		t.Errorf("ETag = %q after %q; want a new one", list.ETag, empty.ETag)
	}

	f.takeCalls()
	same, err := g.ListOpenIssues(ctx, list.ETag)
	if err != nil || !same.NotModified || same.ETag != list.ETag || len(same.Issues) != 0 {
		t.Errorf("ListOpenIssues(unchanged) = %+v, %v; want NotModified with the same ETag", same, err)
	}
	if calls := f.takeCalls(); len(calls) != 1 || !strings.Contains(calls[0], listQuery) ||
		!strings.HasSuffix(calls[0], " "+list.ETag) {
		t.Errorf("unchanged list calls = %q; want one, with %s and If-None-Match", calls, listQuery)
	}

	f.mu.Lock()
	f.issue(1).State = "closed"
	f.mu.Unlock()
	f.open("print it", "ada", false)
	changed, err := g.ListOpenIssues(ctx, list.ETag)
	if err != nil || changed.NotModified || !reflect.DeepEqual(numbers(changed.Issues), []int{3, 4}) {
		t.Errorf("ListOpenIssues(changed) = %+v, %v; want issues 3 and 4", changed, err)
	}
}

// TestGitHubIssuesListPages pages through more issues than one page holds. Such
// a list has no ETag, since page 1's doesn't cover the rest.
func TestGitHubIssuesListPages(t *testing.T) {
	f, g := newFakeGitHubIssues(t)
	ctx := context.Background()
	var want []int
	for n := 1; n <= 2*githubPageSize+10; n++ {
		pull := n%7 == 0
		f.open("issue "+strconv.Itoa(n), "ada", pull)
		if !pull {
			want = append(want, n)
		}
	}
	list, err := g.ListOpenIssues(ctx, "")
	if err != nil {
		t.Fatalf("ListOpenIssues() error = %v", err)
	}
	if !reflect.DeepEqual(numbers(list.Issues), want) {
		t.Errorf("ListOpenIssues() = %v; want %v", numbers(list.Issues), want)
	}
	if list.ETag != "" || list.NotModified {
		t.Errorf("ListOpenIssues() of three pages has ETag %q, NotModified %v; want neither", list.ETag, list.NotModified)
	}
	calls := f.takeCalls()
	if len(calls) != 3 {
		t.Errorf("ListOpenIssues() made %d calls; want 3: %q", len(calls), calls)
	}
	for _, call := range calls {
		if !strings.Contains(call, listQuery) {
			t.Errorf("ListOpenIssues() called %q; want every page asked with %s", call, listQuery)
		}
	}

	// An ETag of page 1 is never trusted for the pages after it.
	if _, err := g.ListOpenIssues(ctx, `W/"stale"`); err != nil {
		t.Fatal(err)
	}
	calls = f.takeCalls()
	if len(calls) != 3 || !strings.HasSuffix(calls[0], `W/"stale"`) || strings.Contains(calls[1]+calls[2], "stale") {
		t.Errorf("calls with an ETag = %q; want If-None-Match on page 1 only", calls)
	}

	for range githubMaxPages*githubPageSize - len(f.issues) + 1 {
		f.open("more", "ada", false)
	}
	if _, err := g.ListOpenIssues(ctx, ""); ports.IssuesReason(err) != ports.ReasonTrackerError {
		t.Errorf("ListOpenIssues() past %d pages error = %v; want reason %s", githubMaxPages, err, ports.ReasonTrackerError)
	}
}

// teamMembers is tracker's ports.TeamMembers: the GitHub client has teams.
func teamMembers(t *testing.T, tracker ports.IssueTracker) ports.TeamMembers {
	t.Helper()
	members, ok := tracker.(ports.TeamMembers)
	if !ok {
		t.Fatalf("GitHubIssues() = %T, which is no ports.TeamMembers", tracker)
	}
	return members
}

func TestGitHubIssuesTeamMember(t *testing.T) {
	f, tracker := newFakeGitHubIssues(t)
	g := teamMembers(t, tracker)
	ctx := context.Background()
	f.members["acme/maintainers"] = map[string]string{"a-colleague": "active", "invited": "pending"}
	for _, tt := range []struct {
		team, login string
		want        bool
	}{
		{"acme/maintainers", "a-colleague", true},
		{"acme/maintainers", "invited", false},
		{"acme/maintainers", "stranger", false},
		{"acme/other", "a-colleague", false},
	} {
		got, err := g.TeamMember(ctx, tt.team, tt.login)
		if err != nil || got != tt.want {
			t.Errorf("TeamMember(%s, %s) = %v, %v; want %v", tt.team, tt.login, got, err, tt.want)
		}
	}
	if calls := f.takeCalls(); calls[0] != "GET /orgs/acme/teams/maintainers/memberships/a-colleague" {
		t.Errorf("TeamMember() called %q", calls[0])
	}
	for _, bad := range [][2]string{
		{"maintainers", "a-colleague"},
		{"acme/maintainers/x", "a-colleague"},
		{"-org/maintainers", "a-colleague"},
		{"acme/..", "a-colleague"},
		{"acme/.", "a-colleague"},
		{"acme/maintainers", "../../../repos/acme/cel-lens"},
		{"acme/maintainers", strings.Repeat("a", 40)},
		{"acme/maintainers", ""},
	} {
		if _, err := g.TeamMember(ctx, bad[0], bad[1]); err == nil {
			t.Errorf("TeamMember(%q, %q) error = nil; want one", bad[0], bad[1])
		}
	}
	if calls := f.takeCalls(); len(calls) != 0 {
		t.Errorf("TeamMember() of a bad team or login called %q", calls)
	}
}

// TestGitHubIssuesCommentAndClose finds no marked comment, comments, finds it
// on the second page of comments, and closes the issue with each reason. The
// comment's ID is a string, the number its page's URL ends in, in decimal
// (#151).
func TestGitHubIssuesCommentAndClose(t *testing.T) {
	f, g := newFakeGitHubIssues(t)
	ctx := context.Background()
	n := f.open("dark mode", "ada", false)
	const marker = "<!-- theos:request brief=shop stage=planned -->"

	found, err := g.FindComment(ctx, n, marker)
	if err != nil || found != nil {
		t.Fatalf("FindComment() on no comments = %+v, %v; want nil", found, err)
	}
	for i := range githubPageSize + 5 {
		if _, err := g.Comment(ctx, n, "chatter "+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	posted, err := g.Comment(ctx, n, "Planned as: dark mode\n\n"+marker)
	if err != nil || !strings.Contains(posted.Body, marker) || posted.URL == "" {
		t.Fatalf("Comment() = %+v, %v", posted, err)
	}
	// The ID is GitHub's number in decimal, the one its page's address ends in.
	if id, ok := strings.CutPrefix(posted.URL, fmt.Sprintf("https://github.com/%s/issues/%d#issuecomment-", fakeGitHubRepo, n)); !ok || id != posted.ID || posted.ID == "" {
		t.Errorf("Comment() has the ID %q and the page %s; want the ID the page ends in", posted.ID, posted.URL)
	}
	found, err = g.FindComment(ctx, n, marker)
	if err != nil || found == nil || found.ID != posted.ID || found.Author != "theos-bot" {
		t.Errorf("FindComment() = %+v, %v; want comment %s", found, err, posted.ID)
	}
	if calls := f.takeCalls(); !slices.Contains(calls, "GET /user") {
		t.Errorf("FindComment() called %q; want GET /user", calls)
	}
	if _, err := g.FindComment(ctx, n, marker); err != nil {
		t.Fatal(err)
	}
	if calls := f.takeCalls(); slices.Contains(calls, "GET /user") {
		t.Errorf("a second FindComment() read the token's account again: %q", calls)
	}
	if found, err := g.FindComment(ctx, n, "<!-- theos:request brief=shop stage=released -->"); err != nil || found != nil {
		t.Errorf("FindComment(another stage) = %+v, %v; want nil", found, err)
	}

	for _, reason := range []ports.CloseReason{ports.CloseNotPlanned, ports.CloseCompleted} {
		if err := g.CloseIssue(ctx, n, reason); err != nil {
			t.Fatalf("CloseIssue(%s) error = %v", reason, err)
		}
		f.mu.Lock()
		state, why := f.issue(n).State, f.issue(n).StateReason
		f.mu.Unlock()
		if state != "closed" || why != string(reason) {
			t.Errorf("after CloseIssue(%s) the issue is %s, %s", reason, state, why)
		}
	}
	f.takeCalls()
	if err := g.CloseIssue(ctx, n, "duplicate"); err == nil {
		t.Error("CloseIssue(duplicate) error = nil; want one")
	}
	if calls := f.takeCalls(); len(calls) != 0 {
		t.Errorf("CloseIssue() with a bad reason called %q", calls)
	}

	if _, err := g.Comment(ctx, 99, "hello"); ports.IssuesReason(err) != ports.ReasonIssueNotFound {
		t.Errorf("Comment() on no issue error = %v; want reason %s", err, ports.ReasonIssueNotFound)
	}
}

// TestGitHubIssuesFindCommentStranger ignores a marker in a comment by anyone
// but the token's own account, and finds the factory's after it.
func TestGitHubIssuesFindCommentStranger(t *testing.T) {
	f, g := newFakeGitHubIssues(t)
	ctx := context.Background()
	n := f.open("dark mode", "ada", false)
	const marker = "<!-- theos:request brief=shop stage=declined -->"
	f.mu.Lock()
	f.comments[n] = []githubComment{{ID: 1, Body: "Declined.\n\n" + marker, User: githubUser{Login: "a-stranger"}}}
	f.mu.Unlock()

	found, err := g.FindComment(ctx, n, marker)
	if err != nil || found != nil {
		t.Fatalf("FindComment() with a stranger's marker = %+v, %v; want nil", found, err)
	}
	posted, err := g.Comment(ctx, n, "Declined.\n\n"+marker)
	if err != nil {
		t.Fatal(err)
	}
	found, err = g.FindComment(ctx, n, marker)
	if err != nil || found == nil || found.ID != posted.ID {
		t.Errorf("FindComment() = %+v, %v; want the factory's comment %s", found, err, posted.ID)
	}
}

// TestGitHubIssuesFailures answers every call with one response and checks the
// reason each call fails with.
func TestGitHubIssuesFailures(t *testing.T) {
	type response struct {
		status  int
		headers map[string]string
		body    string
	}
	const noPermission = `{"message":"Resource not accessible by personal access token"}`
	tests := []struct {
		name string
		resp response
		// want is the reason of every call; team, when set, TeamMember's.
		want, team string
	}{
		{"bad token", response{401, nil, `{"message":"Bad credentials"}`}, ports.ReasonUnauthorized, ""},
		{"no permission", response{403, nil, noPermission}, ports.ReasonTokenLacksIssues, ports.ReasonTokenLacksMembers},
		{"rate limited", response{403, map[string]string{"X-RateLimit-Remaining": "0"}, `{"message":"API rate limit exceeded"}`}, ports.ReasonTrackerUnavailable, ""},
		{"secondary rate limit", response{403, map[string]string{"Retry-After": "60"}, `{"message":"You have exceeded a secondary rate limit"}`}, ports.ReasonTrackerUnavailable, ""},
		{"too many requests", response{429, nil, `{"message":"slow down"}`}, ports.ReasonTrackerUnavailable, ""},
		{"server error", response{502, nil, "<html>" + strings.Repeat("Bad Gateway ", 1000) + "</html>"}, ports.ReasonTrackerUnavailable, ""},
		{"not found", response{404, nil, `{"message":"Not Found"}`}, ports.ReasonIssueNotFound, "-"},
		{"gone", response{410, nil, `{"message":"Issues are disabled for this repo"}`}, ports.ReasonIssueNotFound, ports.ReasonTrackerError},
		{"moved", response{301, map[string]string{"Location": "/repositories/1/issues/1"}, `{"message":"Moved Permanently"}`}, ports.ReasonIssueNotFound, ""},
		{"locked", response{403, nil, `{"message":"Unable to create comment because issue is locked."}`}, ports.ReasonTrackerError, ""},
		{"validation", response{422, nil, `{"message":"Validation Failed"}`}, ports.ReasonTrackerError, ""},
		{"unreadable success", response{200, nil, `not json`}, ports.ReasonTrackerError, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, g := newFakeGitHubIssues(t)
			f.fail = func(w http.ResponseWriter, _ *http.Request) {
				for k, v := range tt.resp.headers {
					w.Header().Set(k, v)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.resp.status)
				_, _ = w.Write([]byte(tt.resp.body))
			}
			ctx := context.Background()
			calls := map[string]func() error{
				"ListOpenIssues": func() error { _, err := g.ListOpenIssues(ctx, `W/"x"`); return err },
				"TeamMember": func() error {
					_, err := teamMembers(t, g).TeamMember(ctx, "acme/maintainers", "a-colleague")
					return err
				},
				"FindComment": func() error { _, err := g.FindComment(ctx, 1, "marker"); return err },
				"Comment":     func() error { _, err := g.Comment(ctx, 1, "hello"); return err },
				"CloseIssue":  func() error { return g.CloseIssue(ctx, 1, ports.CloseCompleted) },
			}
			for name, call := range calls {
				err := call()
				want := tt.want
				switch {
				case tt.resp.status == http.StatusMovedPermanently && name != "Comment" && name != "CloseIssue":
					// A read follows it, here round and round.
					want = ports.ReasonTrackerUnavailable
				case name == "TeamMember" && tt.team == "-":
					want = "" // not a member
				case name == "TeamMember" && tt.team != "":
					want = tt.team
				case name == "ListOpenIssues" && tt.resp.status == http.StatusNotFound:
					want = ports.ReasonTrackerNotFound
				case name == "ListOpenIssues" && tt.resp.status == http.StatusGone:
					want = ports.ReasonIssuesDisabled
				case name == "CloseIssue" && tt.resp.status == http.StatusOK:
					want = "" // its response isn't read
				}
				if want == "" {
					if err != nil {
						t.Errorf("%s() error = %v; want nil", name, err)
					}
					continue
				}
				var ie *ports.IssuesError
				if !errors.As(err, &ie) || ie.Reason != want {
					t.Errorf("%s() error = %v; want reason %s", name, err, want)
				}
				if err != nil && strings.Contains(err.Error(), f.token) {
					t.Errorf("%s() error %q holds the token", name, err)
				}
				if err != nil && len([]rune(err.Error())) > 450 {
					t.Errorf("%s() error is %d characters long", name, len([]rune(err.Error())))
				}
			}
		})
	}
}

func TestGitHubIssuesNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	g, err := Client{}.GitHubIssues(ports.GitHubConfig{Repository: fakeGitHubRepo, Token: rand.Text(), APIURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := g.ListOpenIssues(ctx, ""); ports.IssuesReason(err) != ports.ReasonTrackerUnavailable {
		t.Errorf("ListOpenIssues() on a closed server error = %v; want reason %s", err, ports.ReasonTrackerUnavailable)
	}
	if _, err := teamMembers(t, g).TeamMember(ctx, "acme/maintainers", "a-colleague"); ports.IssuesReason(err) != ports.ReasonTrackerUnavailable {
		t.Errorf("TeamMember() on a closed server error = %v; want reason %s", err, ports.ReasonTrackerUnavailable)
	}
	if err := g.CloseIssue(ctx, 1, ports.CloseCompleted); ports.IssuesReason(err) != ports.ReasonTrackerUnavailable {
		t.Errorf("CloseIssue() on a closed server error = %v; want reason %s", err, ports.ReasonTrackerUnavailable)
	}
	if ports.IssuesReason(context.DeadlineExceeded) != ports.ReasonTrackerUnavailable {
		t.Error("IssuesReason() of a plain error is not TrackerUnavailable")
	}
}

func TestGitHubIssuesConfig(t *testing.T) {
	for _, cfg := range []ports.GitHubConfig{
		{Repository: "cel-lens", Token: "t"},
		{Repository: "acme/cel-lens"},
		{Repository: "acme/cel-lens", Token: "t", APIURL: "not a url"},
	} {
		if _, err := (Client{}).GitHubIssues(cfg); err == nil {
			t.Errorf("GitHubIssues(%+v) error = nil, want one", cfg)
		}
	}
	g, err := Client{}.GitHubIssues(ports.GitHubConfig{Repository: "Acme-1/cel.lens_2", Token: "t"})
	if err != nil {
		t.Fatalf("GitHubIssues() error = %v", err)
	}
	if gh := g.(*githubIssues); gh.api.String() != GitHubAPIURL || gh.issuePath(7) != "/repos/Acme-1/cel.lens_2/issues/7" {
		t.Errorf("GitHubIssues() = %s %s", gh.api, gh.issuePath(7))
	}
}
