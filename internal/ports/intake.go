package ports

import (
	"context"
	"errors"
	"fmt"
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
	// ReasonIssueNotFound: no such issue, or it was deleted or moved (404, 410
	// or 301 on one issue's call). A 404 on the list is ReasonRepositoryNotFound.
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
	// FindComment returns the first comment on issue number written by the
	// token's own account whose body contains marker, nil when there is none.
	// Anyone can copy a marker into a comment of their own; theirs is ignored.
	FindComment(ctx context.Context, number int, marker string) (*GitHubComment, error)
	// Comment posts body on issue number.
	Comment(ctx context.Context, number int, body string) (GitHubComment, error)
	// CloseIssue closes issue number with reason. Closing a closed issue
	// changes nothing but its reason.
	CloseIssue(ctx context.Context, number int, reason CloseReason) error
}
