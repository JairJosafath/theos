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
	URL string
	// Created is when it was filed.
	Created time.Time
}

// GitHubComment is a comment on a GitHub issue.
type GitHubComment struct {
	// ID is the comment's own, never 0 and never another comment's.
	ID   int64
	Body string
	// Author is the login of the account that wrote it.
	Author string
	// URL is the comment's page.
	URL string
}

// IssueList is a repository's open issues, or that they didn't change.
type IssueList struct {
	// Issues are the open issues, oldest first, pull requests left out. Empty
	// when NotModified.
	Issues []GitHubIssue
	// NotModified: the list is the one ETag was given for.
	NotModified bool
	// ETag is passed to the next ListOpenIssues. A NotModified list carries
	// one too, to ask with again. It is empty when the list took more than one
	// page, since a page's ETag doesn't cover the others.
	ETag string
}

// CloseReason is why an issue is closed, as GitHub shows it.
type CloseReason string

const (
	CloseCompleted  CloseReason = "completed"
	CloseNotPlanned CloseReason = "not_planned"
)

// IssueTracker is the issues of a repository on GitHub. It never reads a
// pull request, and never edits an issue's text. What a Brief relies on of
// every adapter is the suite contract.IssueTracker
// (internal/ports/contract/intake.go), which each adapter's tests run.
type IssueTracker interface {
	// ListOpenIssues returns the open issues. Given the ETag of the last list,
	// it asks with If-None-Match, which an unchanged list answers with a 304
	// that costs no rate limit. A list that failed is empty.
	ListOpenIssues(ctx context.Context, etag string) (IssueList, error)
	// TeamMember reports whether login is an active member of team, org/slug.
	// A pending invitation is not; neither is a team the token can't see. It
	// is false when the call failed.
	TeamMember(ctx context.Context, team, login string) (bool, error)
	// FindComment returns the first comment on issue number written by the
	// token's own account whose body contains marker, nil when there is none.
	// Anyone can copy a marker into a comment of their own; theirs is ignored.
	// An empty marker is in every comment and says nothing: it finds none.
	FindComment(ctx context.Context, number int, marker string) (*GitHubComment, error)
	// Comment posts body on issue number as the token's account, and returns
	// the comment with its ID, Author and URL.
	Comment(ctx context.Context, number int, body string) (GitHubComment, error)
	// CloseIssue closes issue number with reason. Closing a closed issue
	// changes nothing but its reason. A reason that is neither CloseCompleted
	// nor CloseNotPlanned is refused with an error, the issue left as it was.
	CloseIssue(ctx context.Context, number int, reason CloseReason) error
}
