package gitprovider

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHubIssues is GitHub's issues API for fakeGitHubRepo and its teams'
// memberships. It fails the test on any other request or a missing token.
type fakeGitHubIssues struct {
	errorf func(format string, args ...any)
	token  string

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

type githubIssueState struct {
	githubIssue
	State       string
	StateReason string
}

func newFakeGitHubIssues(t *testing.T) (*fakeGitHubIssues, IssueTracker) {
	t.Helper()
	f := &fakeGitHubIssues{errorf: t.Errorf, token: rand.Text(), comments: map[int][]githubComment{},
		members: map[string]map[string]string{}, nextID: 1000}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	tracker, err := Client{}.GitHubIssues(GitHubConfig{Repository: fakeGitHubRepo, Token: f.token, APIURL: srv.URL})
	if err != nil {
		t.Fatalf("GitHubIssues() error = %v", err)
	}
	return f, tracker
}

// open adds an open issue, or a pull request, by author.
func (f *fakeGitHubIssues) open(title, author string, pull bool) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(f.issues) + 1
	i := githubIssue{Number: n, Title: title, Body: "body of " + title, User: githubUser{Login: author},
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
	if got := r.Header.Get("Authorization"); got != "Bearer "+f.token {
		f.errorf("request %s %s has Authorization %q", r.Method, r.URL.Path, got)
	}
	if f.fail != nil {
		f.fail(w, r)
		return
	}
	issues := "/repos/" + fakeGitHubRepo + "/issues"
	path := r.URL.Path
	switch {
	case path == issues && r.Method == http.MethodGet:
		q := r.URL.Query()
		if q.Get("state") != "open" || q.Get("sort") != "created" || q.Get("direction") != "asc" {
			f.errorf("listing issues with %s", r.URL.RawQuery)
		}
		var open []githubIssue
		for _, i := range f.issues {
			if i.State == "open" {
				open = append(open, i.githubIssue)
			}
		}
		items := page(r, open)
		etag := etagOf(items)
		if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
			w.Header().Set("ETag", etag)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		writeJSON(w, http.StatusOK, items)
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
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			issue.State, issue.StateReason = in["state"], in["state_reason"]
			writeJSON(w, http.StatusOK, issue.githubIssue)
		case len(rest) == 2 && rest[1] == "comments" && r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, page(r, f.comments[n]))
		case len(rest) == 2 && rest[1] == "comments" && r.Method == http.MethodPost:
			var in struct{ Body string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.nextID++
			c := githubComment{ID: f.nextID, Body: in.Body, User: githubUser{Login: "c4factory-bot"},
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

func numbers(issues []GitHubIssue) []int {
	out := make([]int, 0, len(issues))
	for _, i := range issues {
		out = append(out, i.Number)
	}
	return out
}

// TestGitHubIssuesList lists issues with pull requests left out, asks again
// with the ETag and gets a 304, then sees a new issue and a closed one.
func TestGitHubIssuesList(t *testing.T) {
	f, g := newFakeGitHubIssues(t)
	ctx := context.Background()

	empty, err := g.ListOpenIssues(ctx, "")
	if err != nil || len(empty.Issues) != 0 || empty.NotModified || empty.ETag == "" {
		t.Fatalf("ListOpenIssues() on no issues = %+v, %v", empty, err)
	}

	f.open("dark mode", "gray-lenovo", false)
	f.open("a pull request", "gray-lenovo", true)
	f.open("export to CSV", "stranger", false)
	list, err := g.ListOpenIssues(ctx, empty.ETag)
	if err != nil {
		t.Fatalf("ListOpenIssues() error = %v", err)
	}
	if list.NotModified || !reflect.DeepEqual(numbers(list.Issues), []int{1, 3}) {
		t.Fatalf("ListOpenIssues() = %+v; want issues 1 and 3", list)
	}
	want := GitHubIssue{Number: 1, Title: "dark mode", Body: "body of dark mode", Author: "gray-lenovo",
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
	if calls := f.takeCalls(); len(calls) != 1 || !strings.HasSuffix(calls[0], " "+list.ETag) {
		t.Errorf("unchanged list calls = %q; want one, with If-None-Match", calls)
	}

	f.mu.Lock()
	f.issue(1).State = "closed"
	f.mu.Unlock()
	f.open("print it", "gray-lenovo", false)
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
		f.open("issue "+strconv.Itoa(n), "gray-lenovo", pull)
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
	if calls := f.takeCalls(); len(calls) != 3 {
		t.Errorf("ListOpenIssues() made %d calls; want 3: %q", len(calls), calls)
	}

	// An ETag of page 1 is never trusted for the pages after it.
	if _, err := g.ListOpenIssues(ctx, `W/"stale"`); err != nil {
		t.Fatal(err)
	}
	calls := f.takeCalls()
	if len(calls) != 3 || !strings.HasSuffix(calls[0], `W/"stale"`) || strings.Contains(calls[1]+calls[2], "stale") {
		t.Errorf("calls with an ETag = %q; want If-None-Match on page 1 only", calls)
	}

	for range githubMaxPages*githubPageSize - len(f.issues) + 1 {
		f.open("more", "gray-lenovo", false)
	}
	if _, err := g.ListOpenIssues(ctx, ""); IssuesReason(err) != ReasonGitHubError {
		t.Errorf("ListOpenIssues() past %d pages error = %v; want reason %s", githubMaxPages, err, ReasonGitHubError)
	}
}

func TestGitHubIssuesTeamMember(t *testing.T) {
	f, g := newFakeGitHubIssues(t)
	ctx := context.Background()
	f.members["gray-org/maintainers"] = map[string]string{"a-colleague": "active", "invited": "pending"}
	for _, tt := range []struct {
		team, login string
		want        bool
	}{
		{"gray-org/maintainers", "a-colleague", true},
		{"gray-org/maintainers", "invited", false},
		{"gray-org/maintainers", "stranger", false},
		{"gray-org/other", "a-colleague", false},
	} {
		got, err := g.TeamMember(ctx, tt.team, tt.login)
		if err != nil || got != tt.want {
			t.Errorf("TeamMember(%s, %s) = %v, %v; want %v", tt.team, tt.login, got, err, tt.want)
		}
	}
	if calls := f.takeCalls(); calls[0] != "GET /orgs/gray-org/teams/maintainers/memberships/a-colleague" {
		t.Errorf("TeamMember() called %q", calls[0])
	}
	for _, bad := range [][2]string{
		{"maintainers", "a-colleague"},
		{"gray-org/maintainers/x", "a-colleague"},
		{"-org/maintainers", "a-colleague"},
		{"gray-org/maintainers", "../../../repos/acme/cel-lens"},
		{"gray-org/maintainers", strings.Repeat("a", 40)},
		{"gray-org/maintainers", ""},
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
// on the second page of comments, and closes the issue with each reason.
func TestGitHubIssuesCommentAndClose(t *testing.T) {
	f, g := newFakeGitHubIssues(t)
	ctx := context.Background()
	n := f.open("dark mode", "gray-lenovo", false)
	const marker = "<!-- c4factory:request brief=shop stage=planned -->"

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
	if err != nil || posted.ID == 0 || !strings.Contains(posted.Body, marker) || posted.URL == "" {
		t.Fatalf("Comment() = %+v, %v", posted, err)
	}
	found, err = g.FindComment(ctx, n, marker)
	if err != nil || found == nil || found.ID != posted.ID || found.Author != "c4factory-bot" {
		t.Errorf("FindComment() = %+v, %v; want comment %d", found, err, posted.ID)
	}
	if found, err := g.FindComment(ctx, n, "<!-- c4factory:request brief=shop stage=released -->"); err != nil || found != nil {
		t.Errorf("FindComment(another stage) = %+v, %v; want nil", found, err)
	}

	for _, reason := range []CloseReason{CloseNotPlanned, CloseCompleted} {
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

	if _, err := g.Comment(ctx, 99, "hello"); IssuesReason(err) != ReasonIssueNotFound {
		t.Errorf("Comment() on no issue error = %v; want reason %s", err, ReasonIssueNotFound)
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
		{"bad token", response{401, nil, `{"message":"Bad credentials"}`}, ReasonUnauthorized, ""},
		{"no permission", response{403, nil, noPermission}, ReasonTokenLacksIssues, ReasonTokenLacksMembers},
		{"rate limited", response{403, map[string]string{"X-RateLimit-Remaining": "0"}, `{"message":"API rate limit exceeded"}`}, ReasonGitHubUnavailable, ""},
		{"secondary rate limit", response{403, map[string]string{"Retry-After": "60"}, `{"message":"You have exceeded a secondary rate limit"}`}, ReasonGitHubUnavailable, ""},
		{"too many requests", response{429, nil, `{"message":"slow down"}`}, ReasonGitHubUnavailable, ""},
		{"server error", response{502, nil, "<html>" + strings.Repeat("Bad Gateway ", 1000) + "</html>"}, ReasonGitHubUnavailable, ""},
		{"not found", response{404, nil, `{"message":"Not Found"}`}, ReasonIssueNotFound, "-"},
		{"issues disabled", response{410, nil, `{"message":"Issues are disabled for this repo"}`}, ReasonIssuesDisabled, ""},
		{"validation", response{422, nil, `{"message":"Validation Failed"}`}, ReasonGitHubError, ""},
		{"unreadable success", response{200, nil, `not json`}, ReasonGitHubError, ""},
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
				"TeamMember":     func() error { _, err := g.TeamMember(ctx, "gray-org/maintainers", "a-colleague"); return err },
				"FindComment":    func() error { _, err := g.FindComment(ctx, 1, "marker"); return err },
				"Comment":        func() error { _, err := g.Comment(ctx, 1, "hello"); return err },
				"CloseIssue":     func() error { return g.CloseIssue(ctx, 1, CloseCompleted) },
			}
			for name, call := range calls {
				err := call()
				want := tt.want
				switch {
				case name == "TeamMember" && tt.team == "-":
					want = "" // not a member
				case name == "TeamMember" && tt.team != "":
					want = tt.team
				case name == "ListOpenIssues" && tt.resp.status == http.StatusNotFound:
					want = ReasonRepositoryNotFound
				case name == "CloseIssue" && tt.resp.status == http.StatusOK:
					want = "" // its response isn't read
				}
				if want == "" {
					if err != nil {
						t.Errorf("%s() error = %v; want nil", name, err)
					}
					continue
				}
				var ie *IssuesError
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
	g, err := Client{}.GitHubIssues(GitHubConfig{Repository: fakeGitHubRepo, Token: rand.Text(), APIURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := g.ListOpenIssues(ctx, ""); IssuesReason(err) != ReasonGitHubUnavailable {
		t.Errorf("ListOpenIssues() on a closed server error = %v; want reason %s", err, ReasonGitHubUnavailable)
	}
	if _, err := g.TeamMember(ctx, "gray-org/maintainers", "a-colleague"); IssuesReason(err) != ReasonGitHubUnavailable {
		t.Errorf("TeamMember() on a closed server error = %v; want reason %s", err, ReasonGitHubUnavailable)
	}
	if err := g.CloseIssue(ctx, 1, CloseCompleted); IssuesReason(err) != ReasonGitHubUnavailable {
		t.Errorf("CloseIssue() on a closed server error = %v; want reason %s", err, ReasonGitHubUnavailable)
	}
	if IssuesReason(context.DeadlineExceeded) != ReasonGitHubUnavailable {
		t.Error("IssuesReason() of a plain error is not GitHubUnavailable")
	}
}

func TestGitHubIssuesConfig(t *testing.T) {
	for _, cfg := range []GitHubConfig{
		{Repository: "cel-lens", Token: "t"},
		{Repository: "acme/cel-lens"},
		{Repository: "acme/cel-lens", Token: "t", APIURL: "not a url"},
	} {
		if _, err := (Client{}).GitHubIssues(cfg); err == nil {
			t.Errorf("GitHubIssues(%+v) error = nil, want one", cfg)
		}
	}
	g, err := Client{}.GitHubIssues(GitHubConfig{Repository: "Acme-1/cel.lens_2", Token: "t"})
	if err != nil {
		t.Fatalf("GitHubIssues() error = %v", err)
	}
	if gh := g.(*githubIssues); gh.api.String() != GitHubAPIURL || gh.issuePath(7) != "/repos/Acme-1/cel.lens_2/issues/7" {
		t.Errorf("GitHubIssues() = %s %s", gh.api, gh.issuePath(7))
	}
}
