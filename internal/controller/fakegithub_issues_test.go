package controller

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

// The issues side of fakeGitHub: a repository's issues and their comments,
// organizations' teams, and the failures a token or GitHub can have.

type fakeGitHubUser struct {
	Login string `json:"login"`
}

type fakeGitHubIssue struct {
	Number      int            `json:"number"`
	Title       string         `json:"title"`
	Body        string         `json:"body"`
	User        fakeGitHubUser `json:"user"`
	HTMLURL     string         `json:"html_url"`
	State       string         `json:"state"`
	StateReason string         `json:"state_reason,omitempty"`
	Created     time.Time      `json:"created_at"`
	PullRequest *struct{}      `json:"pull_request,omitempty"`
	comments    []fakeGitHubComment
}

type fakeGitHubComment struct {
	ID      int64          `json:"id"`
	Body    string         `json:"body"`
	User    fakeGitHubUser `json:"user"`
	HTMLURL string         `json:"html_url"`
}

// fakeGitHubIssues is the issues state of the fake, under its mutex.
type fakeGitHubIssues struct {
	issues map[string][]*fakeGitHubIssue
	// teams holds each team's members by org/slug.
	teams map[string][]string
	// lacksIssues are the repositories whose token may not touch issues,
	// lacksMembers the tokens that may not read teams, and down the
	// repositories GitHub answers with a 503.
	lacksIssues  map[string]bool
	lacksMembers map[string]bool
	down         map[string]bool
	// lists counts the issue lists asked of each repository, and notModified
	// those answered with a 304.
	lists       map[string]int
	notModified map[string]int
}

// GitHubIssues is the real client, pointed at the fake.
func (f *fakeGitHub) GitHubIssues(cfg gitprovider.GitHubConfig) (gitprovider.IssueTracker, error) {
	cfg.APIURL, cfg.UploadURL = f.server.URL, f.server.URL
	return gitprovider.Client{}.GitHubIssues(cfg)
}

func (f *fakeGitHub) issueState() *fakeGitHubIssues {
	if f.is == nil {
		f.is = &fakeGitHubIssues{issues: map[string][]*fakeGitHubIssue{}, teams: map[string][]string{},
			lacksIssues: map[string]bool{}, lacksMembers: map[string]bool{}, down: map[string]bool{},
			lists: map[string]int{}, notModified: map[string]int{}}
	}
	return f.is
}

// openIssue opens an issue of repository by author and returns its number.
func (f *fakeGitHub) openIssue(repository, author, title, body string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.issueState()
	n := len(s.issues[repository]) + 1
	s.issues[repository] = append(s.issues[repository], &fakeGitHubIssue{Number: n, Title: title, Body: body,
		User: fakeGitHubUser{author}, HTMLURL: fmt.Sprintf("https://github.com/%s/issues/%d", repository, n),
		State: "open", Created: time.Now().UTC().Truncate(time.Second)})
	return n
}

// openPullRequest opens a pull request, which GitHub lists with the issues.
func (f *fakeGitHub) openPullRequest(repository, author, title string) int {
	n := f.openIssue(repository, author, title, "")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issueState().issues[repository][n-1].PullRequest = &struct{}{}
	return n
}

// closeIssue closes issue n as a person would.
func (f *fakeGitHub) closeIssue(repository string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issueState().issues[repository][n-1].State = "closed"
}

// editIssue changes the body of issue n as its author would.
func (f *fakeGitHub) editIssue(repository string, n int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issueState().issues[repository][n-1].Body = body
}

// gitHubIssue returns a copy of issue n of repository and its comments.
func (f *fakeGitHub) gitHubIssue(repository string, n int) (fakeGitHubIssue, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.issueState().issues[repository][n-1]
	comments := make([]string, 0, len(i.comments))
	for _, c := range i.comments {
		comments = append(comments, c.Body)
	}
	return *i, comments
}

// addTeamMember makes login an active member of team, org/slug.
func (f *fakeGitHub) addTeamMember(team, login string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.issueState()
	s.teams[team] = append(s.teams[team], login)
}

// setLacksIssues says whether repository's token lacks Issues read and write.
func (f *fakeGitHub) setLacksIssues(repository string, lacks bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issueState().lacksIssues[repository] = lacks
}

// setLacksMembers says whether token lacks Organization members read.
func (f *fakeGitHub) setLacksMembers(token string, lacks bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issueState().lacksMembers[token] = lacks
}

// setDown says whether GitHub answers repository's issue calls with a 503.
func (f *fakeGitHub) setDown(repository string, down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issueState().down[repository] = down
}

// issueLists returns how many issue lists of repository were asked, and how
// many of them were answered with a 304.
func (f *fakeGitHub) issueLists(repository string) (asked, notModified int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.issueState()
	return s.lists[repository], s.notModified[repository]
}

// serveTeam answers /orgs/{org}/teams/{slug}/memberships/{login}, under f.mu.
func (f *fakeGitHub) serveTeam(w http.ResponseWriter, req *http.Request, parts []string) {
	s := f.issueState()
	token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	known := false
	for _, t := range f.tokens {
		known = known || t == token
	}
	switch {
	case !known:
		reply(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})
	case s.lacksMembers[token]:
		reply(w, http.StatusForbidden, map[string]string{"message": "Resource not accessible by personal access token"})
	case len(parts) != 5 || parts[1] != "teams" || parts[3] != "memberships" || req.Method != http.MethodGet:
		reply(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	default:
		for _, login := range s.teams[parts[0]+"/"+parts[2]] {
			if strings.EqualFold(login, parts[4]) {
				reply(w, http.StatusOK, map[string]string{"state": "active"})
				return
			}
		}
		reply(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	}
}

// serveIssues answers /repos/{owner}/{name}/issues[/...], under f.mu.
func (f *fakeGitHub) serveIssues(w http.ResponseWriter, req *http.Request, repository string, rest []string) {
	s := f.issueState()
	switch {
	case s.down[repository]:
		reply(w, http.StatusServiceUnavailable, map[string]string{"message": "Service Unavailable"})
		return
	case s.lacksIssues[repository]:
		reply(w, http.StatusForbidden, map[string]string{"message": "Resource not accessible by personal access token"})
		return
	}
	issues := s.issues[repository]
	if len(rest) == 0 && req.Method == http.MethodGet {
		s.lists[repository]++
		var open []*fakeGitHubIssue
		for _, i := range issues {
			if i.State == "open" {
				open = append(open, i)
			}
		}
		if req.URL.Query().Get("page") != "1" {
			reply(w, http.StatusOK, []any{})
			return
		}
		data, _ := json.Marshal(open)
		etag := fmt.Sprintf(`"%x"`, sha256.Sum256(data))
		if req.Header.Get("If-None-Match") == etag {
			s.notModified[repository]++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		reply(w, http.StatusOK, open)
		return
	}
	n, _ := strconv.Atoi(rest[0])
	if n < 1 || n > len(issues) {
		reply(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	issue := issues[n-1]
	switch {
	case len(rest) == 1 && req.Method == http.MethodPatch:
		var patch struct {
			State       string `json:"state"`
			StateReason string `json:"state_reason"`
		}
		_ = json.NewDecoder(req.Body).Decode(&patch)
		issue.State, issue.StateReason = patch.State, patch.StateReason
		reply(w, http.StatusOK, issue)
	case len(rest) == 2 && rest[1] == "comments" && req.Method == http.MethodGet:
		if req.URL.Query().Get("page") != "1" {
			reply(w, http.StatusOK, []any{})
			return
		}
		reply(w, http.StatusOK, issue.comments)
	case len(rest) == 2 && rest[1] == "comments" && req.Method == http.MethodPost:
		var c fakeGitHubComment
		_ = json.NewDecoder(req.Body).Decode(&c)
		f.nextID++
		c.ID, c.User = f.nextID, fakeGitHubUser{"c4factory-bot"}
		issue.comments = append(issue.comments, c)
		reply(w, http.StatusCreated, c)
	default:
		reply(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	}
}
