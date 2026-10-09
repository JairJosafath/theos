package gitprovider

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
	"time"
)

// Reasons an IssueTracker call fails, as a Brief's Requests condition names
// them. A 401 is ReasonUnauthorized, and anything else ReasonGitHubError.
const (
	// ReasonTokenLacksIssues: the token may not read or write the repository's
	// issues (403 without rate limiting).
	ReasonTokenLacksIssues = "TokenLacksIssues"
	// ReasonTokenLacksMembers: the token may not read the organization's team
	// members (403 without rate limiting).
	ReasonTokenLacksMembers = "TokenLacksMembers"
	// ReasonGitHubUnavailable: a rate limit, a 5xx or the network; the next try
	// may work.
	ReasonGitHubUnavailable = "GitHubUnavailable"
	// ReasonIssuesDisabled: the repository has its issues turned off (410).
	ReasonIssuesDisabled = "IssuesDisabled"
	// ReasonIssueNotFound: no such issue, or it was deleted or moved (404 on one
	// issue's call). A 404 on the list is ReasonRepositoryNotFound.
	ReasonIssueNotFound = "IssueNotFound"
)

// IssuesError is a failed IssueTracker call, with the reason it failed.
type IssuesError struct {
	Reason string
	// Status is the HTTP status, 0 when no response came.
	Status  int
	Message string
}

func (e *IssuesError) Error() string {
	if e.Status == 0 {
		return e.Message
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
}

// IssuesReason returns the reason err failed with: an IssuesError's, else
// ReasonGitHubUnavailable, since only the network or ctx fails otherwise.
func IssuesReason(err error) string {
	if ie, ok := errors.AsType[*IssuesError](err); ok {
		return ie.Reason
	}
	return ReasonGitHubUnavailable
}

// GitHubIssue is an open issue of a repository on GitHub.
type GitHubIssue struct {
	Number int
	Title  string
	Body   string
	// Author is the login of the person who opened it.
	Author string
	// URL is the issue's page.
	URL     string
	Created time.Time
}

// GitHubComment is a comment on a GitHub issue.
type GitHubComment struct {
	ID     int64
	Body   string
	Author string
	URL    string
}

// IssueList is a repository's open issues, or that they didn't change.
type IssueList struct {
	// Issues are the open issues, oldest first, pull requests left out. Empty
	// when NotModified.
	Issues []GitHubIssue
	// NotModified: the list is the one ETag was given for.
	NotModified bool
	// ETag is passed to the next ListOpenIssues. It is empty when the list took
	// more than one page, since a page's ETag doesn't cover the others.
	ETag string
}

// CloseReason is why an issue is closed, as GitHub shows it.
type CloseReason string

const (
	CloseCompleted  CloseReason = "completed"
	CloseNotPlanned CloseReason = "not_planned"
)

// IssueTracker is the issues of a repository on GitHub. It never reads a
// pull request, and never edits an issue's text.
type IssueTracker interface {
	// ListOpenIssues returns the open issues. Given the ETag of the last list,
	// it asks with If-None-Match, which an unchanged list answers with a 304
	// that costs no rate limit.
	ListOpenIssues(ctx context.Context, etag string) (IssueList, error)
	// TeamMember reports whether login is an active member of team, org/slug.
	// A pending invitation is not; neither is a team the token can't see.
	TeamMember(ctx context.Context, team, login string) (bool, error)
	// FindComment returns the first comment on issue number whose body
	// contains marker, nil when there is none.
	FindComment(ctx context.Context, number int, marker string) (*GitHubComment, error)
	// Comment posts body on issue number.
	Comment(ctx context.Context, number int, body string) (GitHubComment, error)
	// CloseIssue closes issue number with reason. Closing a closed issue
	// changes nothing but its reason.
	CloseIssue(ctx context.Context, number int, reason CloseReason) error
}

// GitHubIssues returns an IssueTracker on cfg's repository. ctx bounds each
// call; UploadURL is not used.
func (c Client) GitHubIssues(cfg GitHubConfig) (IssueTracker, error) {
	api, err := c.githubAPI(cfg)
	if err != nil {
		return nil, err
	}
	return &githubIssues{githubAPI: api}, nil
}

type githubIssues struct {
	githubAPI
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

func (c githubComment) comment() GitHubComment {
	return GitHubComment{ID: c.ID, Body: c.Body, Author: c.User.Login, URL: c.HTMLURL}
}

var (
	// githubLogin is GitHub's rule for a user or organization name.
	githubLogin = regexp.MustCompile(`^[A-Za-z0-9](-?[A-Za-z0-9])*$`)
	githubSlug  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// githubMaxLogin is the longest login GitHub allows.
const githubMaxLogin = 39

func validLogin(login string) bool {
	return len(login) <= githubMaxLogin && githubLogin.MatchString(login)
}

// ListOpenIssues pages through the open issues oldest first, so a new issue
// lands on the last page. Only the first page is asked with If-None-Match:
// a list that fits one page has its whole state in that page's ETag.
func (g *githubIssues) ListOpenIssues(ctx context.Context, etag string) (IssueList, error) {
	var list IssueList
	for page := 1; page <= githubMaxPages; page++ {
		query := fmt.Sprintf("?state=open&sort=created&direction=asc&per_page=%d&page=%d", githubPageSize, page)
		req, err := g.issuesRequest(ctx, http.MethodGet, g.repo+"/issues"+query, nil)
		if err != nil {
			return IssueList{}, err
		}
		if page == 1 && etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		resp, data, err := g.do(req)
		if err != nil {
			return IssueList{}, unavailable("listing issues", err)
		}
		if page == 1 && etag != "" && resp.StatusCode == http.StatusNotModified {
			return IssueList{NotModified: true, ETag: etag}, nil
		}
		var batch []githubIssue
		if err := issuesResult(resp, data, http.StatusOK, "listing issues", ReasonTokenLacksIssues, ReasonRepositoryNotFound, &batch); err != nil {
			return IssueList{}, err
		}
		if page == 1 {
			list.ETag = resp.Header.Get("ETag")
		}
		for _, i := range batch {
			if i.PullRequest != nil {
				continue
			}
			list.Issues = append(list.Issues, GitHubIssue{Number: i.Number, Title: i.Title, Body: i.Body,
				Author: i.User.Login, URL: i.HTMLURL, Created: i.Created})
		}
		if len(batch) < githubPageSize {
			if page > 1 {
				list.ETag = ""
			}
			return list, nil
		}
	}
	return IssueList{}, &IssuesError{Reason: ReasonGitHubError,
		Message: fmt.Sprintf("github: more than %d open issues and pull requests", githubMaxPages*githubPageSize)}
}

func (g *githubIssues) TeamMember(ctx context.Context, team, login string) (bool, error) {
	org, slug, ok := strings.Cut(team, "/")
	if !ok || !validLogin(org) || !githubSlug.MatchString(slug) {
		return false, &IssuesError{Reason: ReasonGitHubError, Message: fmt.Sprintf("github: team %q is not org/slug", team)}
	}
	if !validLogin(login) {
		return false, &IssuesError{Reason: ReasonGitHubError, Message: fmt.Sprintf("github: %q is not a GitHub login", login)}
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
	if err := issuesResult(resp, data, http.StatusOK, what, ReasonTokenLacksMembers, ReasonGitHubError, &membership); err != nil {
		return false, err
	}
	return membership.State == "active", nil
}

func (g *githubIssues) FindComment(ctx context.Context, number int, marker string) (*GitHubComment, error) {
	what := "listing comments of issue " + strconv.Itoa(number)
	for page := 1; page <= githubMaxPages; page++ {
		query := fmt.Sprintf("?per_page=%d&page=%d", githubPageSize, page)
		var batch []githubComment
		if err := g.call(ctx, http.MethodGet, g.issuePath(number)+"/comments"+query, nil, http.StatusOK, what, &batch); err != nil {
			return nil, err
		}
		for _, c := range batch {
			if marker != "" && strings.Contains(c.Body, marker) {
				comment := c.comment()
				return &comment, nil
			}
		}
		if len(batch) < githubPageSize {
			return nil, nil
		}
	}
	return nil, &IssuesError{Reason: ReasonGitHubError,
		Message: fmt.Sprintf("github: issue %d has more than %d comments", number, githubMaxPages*githubPageSize)}
}

func (g *githubIssues) Comment(ctx context.Context, number int, body string) (GitHubComment, error) {
	payload, _ := json.Marshal(map[string]string{"body": body})
	var c githubComment
	err := g.call(ctx, http.MethodPost, g.issuePath(number)+"/comments", payload, http.StatusCreated,
		"commenting on issue "+strconv.Itoa(number), &c)
	return c.comment(), err
}

func (g *githubIssues) CloseIssue(ctx context.Context, number int, reason CloseReason) error {
	if reason != CloseCompleted && reason != CloseNotPlanned {
		return &IssuesError{Reason: ReasonGitHubError, Message: fmt.Sprintf("github: %q is not a reason to close", reason)}
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
	return issuesResult(resp, data, want, what, ReasonTokenLacksIssues, ReasonIssueNotFound, out)
}

func (g *githubIssues) issuesRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, g.api.String()+path, bytes.NewReader(body))
	if err != nil {
		return nil, &IssuesError{Reason: ReasonGitHubError, Message: "github: " + err.Error()}
	}
	if body == nil {
		req.Body, req.ContentLength = http.NoBody, 0
	} else {
		req.Header.Set("Content-Type", "application/json")
	}
	setGitHubHeaders(req, g.token)
	return req, nil
}

func unavailable(what string, err error) *IssuesError {
	return &IssuesError{Reason: ReasonGitHubUnavailable, Message: fmt.Sprintf("github: %s: %v", what, err)}
}

// issuesResult decodes a want response into out; any other response is an
// IssuesError with its reason: lacks that of a 403 that isn't a rate limit,
// notFound that of a 404.
func issuesResult(resp *http.Response, data []byte, want int, what, lacks, notFound string, out any) error {
	if resp.StatusCode == want {
		if out != nil && json.Unmarshal(data, out) != nil {
			return &IssuesError{Reason: ReasonGitHubError, Status: resp.StatusCode,
				Message: fmt.Sprintf("github: %s: unreadable response", what)}
		}
		return nil
	}
	_, message := githubMessage(resp, data)
	e := &IssuesError{Reason: ReasonGitHubError, Status: resp.StatusCode,
		Message: fmt.Sprintf("github: %s failed: %s", what, message)}
	switch status := resp.StatusCode; {
	case status == http.StatusUnauthorized:
		e.Reason = ReasonUnauthorized
	case status == http.StatusForbidden && !rateLimited(resp, message):
		e.Reason = lacks
	case status == http.StatusForbidden, status == http.StatusTooManyRequests, status >= http.StatusInternalServerError:
		e.Reason = ReasonGitHubUnavailable
	case status == http.StatusNotFound:
		e.Reason = notFound
	case status == http.StatusGone:
		e.Reason = ReasonIssuesDisabled
	}
	return e
}
