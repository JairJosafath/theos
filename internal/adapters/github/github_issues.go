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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// The issues client is a ports.TeamMembers too: GitHub has teams.
var _ ports.TeamMembers = (*githubIssues)(nil)

// Client is the adapter of GitHub's issues in ports.IssueTrackers.
var _ ports.TrackerAdapter = Client{}

// Tracker returns GitHubIssues on source's repository (owner/name), at
// source.Address when set, with the credential secret holds: its token, or
// its GitHub App's installation token for the repository alone, minted and
// kept as GitHubToken does. A Secret with neither whole is a
// ports.CredentialMissingError; a mint that fails is a ports.IssuesError
// with the mint's reason, status and message.
func (c Client) Tracker(ctx context.Context, source ports.TrackerSource,
	secret map[string][]byte) (ports.IssueTracker, error) {
	if _, _, ok := model.GitHubCredential(secret); !ok {
		return nil, &ports.CredentialMissingError{Keys: fmt.Sprintf("the key token (or a GitHub App's %s)",
			strings.Join(model.GitHubAppKeys, ", "))}
	}
	token, err := c.GitHubToken(ctx, source.Address, source.Project, secret)
	if mint, ok := errors.AsType[*ports.ReleaseError](err); ok {
		return nil, &ports.IssuesError{Reason: mint.Reason, Status: mint.Status, Message: mint.Message}
	}
	if err != nil {
		return nil, err
	}
	return c.GitHubIssues(ports.GitHubConfig{Repository: source.Project, Token: token, APIURL: source.Address})
}

// GitHubIssues returns an IssueTracker on cfg's repository, which is a
// ports.TeamMembers too. ctx bounds each call; UploadURL is not used.
func (c Client) GitHubIssues(cfg ports.GitHubConfig) (ports.IssueTracker, error) {
	api, err := c.githubAPI(cfg)
	if err != nil {
		return nil, err
	}
	// A redirect turns a PATCH or POST into a GET that answers 200: a moved
	// issue would look closed. Only reads follow one.
	client := *api.http
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if via[0].Method != http.MethodGet {
			return http.ErrUseLastResponse
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	api.http = &client
	return &githubIssues{githubAPI: api}, nil
}

type githubIssues struct {
	githubAPI

	// login is the token's own account, read once by self.
	mu    sync.Mutex
	login string
}

type githubUser struct {
	Login string `json:"login"`
}

type githubIssue struct {
	Number  int        `json:"number"`
	Title   string     `json:"title"`
	Body    string     `json:"body"`
	User    githubUser `json:"user"`
	HTMLURL string     `json:"html_url"`
	Created time.Time  `json:"created_at"`
	// PullRequest is set on a pull request, which GitHub lists with the issues.
	PullRequest json.RawMessage `json:"pull_request,omitempty"`
}

type githubComment struct {
	ID      int64      `json:"id"`
	Body    string     `json:"body"`
	User    githubUser `json:"user"`
	HTMLURL string     `json:"html_url"`
}

// comment is c as the port names it, its ID in decimal.
func (c githubComment) comment() ports.TrackerComment {
	return ports.TrackerComment{ID: strconv.FormatInt(c.ID, 10), Body: c.Body, Author: c.User.Login, URL: c.HTMLURL}
}

// issueKey is issue number as GitHub writes it for people: #12.
func issueKey(number int) string {
	return "#" + strconv.Itoa(number)
}

// stateClosed is a closed issue's state.
const stateClosed = "closed"

var (
	// githubLogin is GitHub's rule for a user or organization name.
	githubLogin = regexp.MustCompile(`^[A-Za-z0-9](-?[A-Za-z0-9])*$`)
	githubSlug  = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// githubMaxLogin is the longest login GitHub allows.
const githubMaxLogin = 39

func validLogin(login string) bool {
	return len(login) <= githubMaxLogin && githubLogin.MatchString(login)
}

// ListOpenIssues pages through the open issues oldest first, so a new issue
// lands on the last page. Only the first page is asked with If-None-Match:
// a list that fits one page has its whole state in that page's ETag.
func (g *githubIssues) ListOpenIssues(ctx context.Context, etag string) (ports.IssueList, error) {
	var list ports.IssueList
	for page := 1; page <= githubMaxPages; page++ {
		query := fmt.Sprintf("?state=open&sort=created&direction=asc&per_page=%d&page=%d", githubPageSize, page)
		req, err := g.issuesRequest(ctx, http.MethodGet, g.repo+"/issues"+query, nil)
		if err != nil {
			return ports.IssueList{}, err
		}
		if page == 1 && etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		resp, data, err := g.do(req)
		if err != nil {
			return ports.IssueList{}, unavailable("listing issues", err)
		}
		if page == 1 && etag != "" && resp.StatusCode == http.StatusNotModified {
			return ports.IssueList{NotModified: true, ETag: etag}, nil
		}
		var batch []githubIssue
		if err := issuesResult(resp, data, http.StatusOK, "listing issues", ports.ReasonTokenLacksIssues, ports.ReasonTrackerNotFound, &batch); err != nil {
			return ports.IssueList{}, err
		}
		if page == 1 {
			list.ETag = resp.Header.Get("ETag")
		}
		for _, i := range batch {
			if i.PullRequest != nil {
				continue
			}
			list.Issues = append(list.Issues, ports.TrackerIssue{Number: i.Number, Key: issueKey(i.Number), Title: i.Title,
				Body: i.Body, Author: i.User.Login, URL: i.HTMLURL, Created: i.Created})
		}
		if len(batch) < githubPageSize {
			if page > 1 {
				list.ETag = ""
			}
			return list, nil
		}
	}
	return ports.IssueList{}, &ports.IssuesError{Reason: ports.ReasonTrackerError,
		Message: fmt.Sprintf("github: more than %d open issues and pull requests", githubMaxPages*githubPageSize)}
}

func (g *githubIssues) TeamMember(ctx context.Context, team, login string) (bool, error) {
	org, slug, ok := strings.Cut(team, "/")
	if !ok || !validLogin(org) || !githubSlug.MatchString(slug) {
		return false, &ports.IssuesError{Reason: ports.ReasonTrackerError, Message: fmt.Sprintf("github: team %q is not org/slug", team)}
	}
	if !validLogin(login) {
		return false, &ports.IssuesError{Reason: ports.ReasonTrackerError, Message: fmt.Sprintf("github: %q is not a GitHub login", login)}
	}
	what := "reading " + login + "'s membership of " + team
	req, err := g.issuesRequest(ctx, http.MethodGet,
		"/orgs/"+url.PathEscape(org)+"/teams/"+url.PathEscape(slug)+"/memberships/"+url.PathEscape(login), nil)
	if err != nil {
		return false, err
	}
	resp, data, err := g.do(req)
	if err != nil {
		return false, unavailable(what, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		// Not a member, or a team or organization the token can't see.
		return false, nil
	}
	var membership struct {
		State string `json:"state"`
	}
	if err := issuesResult(resp, data, http.StatusOK, what, ports.ReasonTokenLacksMembers, ports.ReasonTrackerError, &membership); err != nil {
		return false, err
	}
	return membership.State == "active", nil
}

func (g *githubIssues) FindComment(ctx context.Context, number int, marker string) (*ports.TrackerComment, error) {
	what := "listing comments of issue " + strconv.Itoa(number)
	for page := 1; page <= githubMaxPages; page++ {
		query := fmt.Sprintf("?per_page=%d&page=%d", githubPageSize, page)
		var batch []githubComment
		if err := g.call(ctx, http.MethodGet, g.issuePath(number)+"/comments"+query, nil, http.StatusOK, what, &batch); err != nil {
			return nil, err
		}
		for _, c := range batch {
			if marker == "" || !strings.Contains(c.Body, marker) {
				continue
			}
			self, err := g.self(ctx)
			if err != nil {
				return nil, err
			}
			if strings.EqualFold(c.User.Login, self) {
				comment := c.comment()
				return &comment, nil
			}
		}
		if len(batch) < githubPageSize {
			return nil, nil
		}
	}
	return nil, &ports.IssuesError{Reason: ports.ReasonTrackerError,
		Message: fmt.Sprintf("github: issue %d has more than %d comments", number, githubMaxPages*githubPageSize)}
}

// self returns the login of the token's account, asked of GitHub the first
// time a marker is found. GitHub compares logins without case.
func (g *githubIssues) self(ctx context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.login != "" {
		return g.login, nil
	}
	var user githubUser
	if err := g.call(ctx, http.MethodGet, "/user", nil, http.StatusOK, "reading the token's account", &user); err != nil {
		return "", err
	}
	if user.Login == "" {
		return "", &ports.IssuesError{Reason: ports.ReasonTrackerError, Message: "github: the token's account has no login"}
	}
	g.login = user.Login
	return g.login, nil
}

func (g *githubIssues) Comment(ctx context.Context, number int, body string) (ports.TrackerComment, error) {
	payload, _ := json.Marshal(map[string]string{"body": body})
	var c githubComment
	err := g.call(ctx, http.MethodPost, g.issuePath(number)+"/comments", payload, http.StatusCreated,
		"commenting on issue "+strconv.Itoa(number), &c)
	if err != nil {
		return ports.TrackerComment{}, err
	}
	return c.comment(), nil
}

func (g *githubIssues) CloseIssue(ctx context.Context, number int, reason ports.CloseReason) error {
	if reason != ports.CloseCompleted && reason != ports.CloseNotPlanned {
		return &ports.IssuesError{Reason: ports.ReasonTrackerError, Message: fmt.Sprintf("github: %q is not a reason to close", reason)}
	}
	payload, _ := json.Marshal(struct {
		State       string `json:"state"`
		StateReason string `json:"state_reason"`
	}{stateClosed, string(reason)})
	return g.call(ctx, http.MethodPatch, g.issuePath(number), payload, http.StatusOK,
		"closing issue "+strconv.Itoa(number), nil)
}

func (g *githubIssues) issuePath(number int) string {
	return g.repo + "/issues/" + strconv.Itoa(number)
}

// call makes a call on one issue, whose 404 is ReasonIssueNotFound.
func (g *githubIssues) call(ctx context.Context, method, path string, body []byte, want int, what string, out any) error {
	req, err := g.issuesRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	resp, data, err := g.do(req)
	if err != nil {
		return unavailable(what, err)
	}
	return issuesResult(resp, data, want, what, ports.ReasonTokenLacksIssues, ports.ReasonIssueNotFound, out)
}

func (g *githubIssues) issuesRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, g.api.String()+path, bytes.NewReader(body))
	if err != nil {
		return nil, &ports.IssuesError{Reason: ports.ReasonTrackerError, Message: "github: " + err.Error()}
	}
	if body == nil {
		req.Body, req.ContentLength = http.NoBody, 0
	} else {
		req.Header.Set("Content-Type", "application/json")
	}
	setGitHubHeaders(req, g.token)
	return req, nil
}

func unavailable(what string, err error) *ports.IssuesError {
	return &ports.IssuesError{Reason: ports.ReasonTrackerUnavailable, Message: fmt.Sprintf("github: %s: %v", what, err)}
}

// issuesResult decodes a want response into out; any other response is an
// IssuesError with its reason: lacks that of a 403 that isn't a rate limit,
// notFound that of a 404.
func issuesResult(resp *http.Response, data []byte, want int, what, lacks, notFound string, out any) error {
	if resp.StatusCode == want {
		if out != nil && json.Unmarshal(data, out) != nil {
			return &ports.IssuesError{Reason: ports.ReasonTrackerError, Status: resp.StatusCode,
				Message: fmt.Sprintf("github: %s: unreadable response", what)}
		}
		return nil
	}
	_, message := githubMessage(resp, data)
	e := &ports.IssuesError{Reason: ports.ReasonTrackerError, Status: resp.StatusCode,
		Message: fmt.Sprintf("github: %s failed: %s", what, message)}
	switch status := resp.StatusCode; {
	case status == http.StatusUnauthorized:
		e.Reason = ports.ReasonUnauthorized
	case status == http.StatusForbidden && strings.Contains(strings.ToLower(message), "locked"):
		// A locked issue refuses comments from anyone but collaborators.
	case status == http.StatusForbidden && !rateLimited(resp, message):
		e.Reason = lacks
	case status == http.StatusForbidden, status == http.StatusTooManyRequests, status >= http.StatusInternalServerError:
		e.Reason = ports.ReasonTrackerUnavailable
	case status == http.StatusGone && notFound == ports.ReasonTrackerNotFound:
		e.Reason = ports.ReasonIssuesDisabled
	case status == http.StatusNotFound, status == http.StatusGone, status == http.StatusMovedPermanently:
		// On one issue, a 410 is a deleted issue and a 301 a moved one.
		e.Reason = notFound
	}
	return e
}
