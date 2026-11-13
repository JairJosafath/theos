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
// them. They are the port's own, whatever tracker is behind it: a credential
// the tracker refuses is ReasonUnauthorized, and anything no other reason
// covers ReasonTrackerError.
const (
	// ReasonTokenLacksIssues: the credential may not read or write the
	// tracker's issues.
	ReasonTokenLacksIssues = "TokenLacksIssues"
	// ReasonTokenLacksMembers: the credential may not read a team's members
	// (TeamMembers).
	ReasonTokenLacksMembers = "TokenLacksMembers"
	// ReasonTrackerUnavailable: a rate limit, a failure of the tracker's own
	// or the network; the next try may work.
	ReasonTrackerUnavailable = "TrackerUnavailable"
	// ReasonTrackerError: anything else the tracker answers, an answer that
	// can't be read, or a call the tracker cannot make (a team's members on
	// a tracker with no teams).
	ReasonTrackerError = "TrackerError"
	// ReasonTrackerNotFound: no such repository, team or project to read
	// issues from, or one the credential can't see.
	ReasonTrackerNotFound = "TrackerNotFound"
	// ReasonIssuesDisabled: the tracker has its issues turned off.
	ReasonIssuesDisabled = "IssuesDisabled"
	// ReasonIssueNotFound: no such issue, or it was deleted or moved. A list
	// that finds no tracker is ReasonTrackerNotFound.
	ReasonIssueNotFound = "IssueNotFound"
	// ReasonCannotClose: the tracker's workflow offers no way to close the
	// issue the way it was asked to, or the settings name a status or a
	// resolution it doesn't have; a person closes it. The message names what
	// the tracker does offer.
	ReasonCannotClose = "CannotClose"
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
// ReasonTrackerUnavailable, since only the network or ctx fails otherwise.
func IssuesReason(err error) string {
	if ie, ok := errors.AsType[*IssuesError](err); ok {
		return ie.Reason
	}
	return ReasonTrackerUnavailable
}

// TrackerIssue is an open issue of a tracker.
type TrackerIssue struct {
	// Number is the issue's own in its tracker, which the calls below take.
	Number int
	// Key is the issue as people name it, to read and never to look up by:
	// `#12` where an issue has a number alone, `ENG-412` where a team's or a
	// project's prefix comes first. Never empty, and never another issue's.
	Key   string
	Title string
	Body  string
	// Author is the login of the person who opened it.
	Author string
	// URL is the issue's page.
	URL string
	// Created is when it was filed.
	Created time.Time
}

// TrackerComment is a comment on a tracker's issue.
type TrackerComment struct {
	// ID is the comment's own, as its tracker writes it (a number, a UUID):
	// never empty and never another comment's.
	ID   string
	Body string
	// Author is the login of the account that wrote it.
	Author string
	// URL is the comment's page.
	URL string
}

// IssueList is a tracker's open issues, or that they didn't change.
type IssueList struct {
	// Issues are the open issues, oldest first, pull requests left out. Empty
	// when NotModified.
	Issues []TrackerIssue
	// NotModified: the list is the one ETag was given for.
	NotModified bool
	// ETag is passed to the next ListOpenIssues. A NotModified list carries
	// one too, to ask with again. It is empty when the adapter has none that
	// covers the whole list.
	ETag string
}

// CloseReason is why an issue is closed, as its tracker shows it.
type CloseReason string

const (
	CloseCompleted  CloseReason = "completed"
	CloseNotPlanned CloseReason = "not_planned"
)

// IssueTracker is where a Brief reads requests: the issues of one repository,
// team or project on a tracker. It never reads a pull request, and never
// edits an issue's text. What a Brief relies on of every adapter is the suite
// contract.IssueTracker (internal/ports/contract/intake.go), which each
// adapter's tests run. A tracker that has teams is a TeamMembers too.
type IssueTracker interface {
	// ListOpenIssues returns the open issues. Given the ETag of the last
	// list, a list that didn't change is NotModified, which an adapter
	// answers as cheaply as its tracker lets it. A list that failed is empty.
	ListOpenIssues(ctx context.Context, etag string) (IssueList, error)
	// FindComment returns the first comment on issue number written by the
	// credential's own account whose body contains marker, nil when there is
	// none. Anyone can copy a marker into a comment of their own; theirs is
	// ignored. An empty marker is in every comment and says nothing: it finds
	// none.
	FindComment(ctx context.Context, number int, marker string) (*TrackerComment, error)
	// Comment posts body on issue number as the credential's account, and
	// returns the comment with its ID, Author and URL.
	Comment(ctx context.Context, number int, body string) (TrackerComment, error)
	// CloseIssue closes issue number with reason. Closing a closed issue
	// changes nothing but its reason. A reason that is neither CloseCompleted
	// nor CloseNotPlanned is refused with an error, the issue left as it was.
	CloseIssue(ctx context.Context, number int, reason CloseReason) error
}

// TeamMembers is an optional capability of an IssueTracker: who is in the
// tracker's teams, which a Brief asks when an entry lets a team's members
// file requests. A tracker that has teams implements it too; find it with a
// type assertion. What a Brief relies on of it is the suite
// contract.TeamMembers (internal/ports/contract/teammembers.go), which the
// adapter's tests run beside contract.IssueTracker.
type TeamMembers interface {
	// TeamMember reports whether login is an active member of team, named as
	// the tracker names its teams. A pending invitation is not; neither is a
	// team the credential can't see. It is false when the call failed.
	TeamMember(ctx context.Context, team, login string) (bool, error)
}

// IssueReader is an optional capability of an IssueTracker whose list is a
// search, which may lag behind the issues themselves the way a search index
// does: an issue closed a minute ago can still be listed (#161). It reads one
// issue on its own, which never lags, so a Brief can tell an issue it closed
// from a new one before it makes a request of it. Find it with a type
// assertion; a tracker whose list doesn't lag needn't have it.
type IssueReader interface {
	// IssueOpen reports whether issue number is one ListOpenIssues lists
	// now, read from the issue itself: open and, where the source has a
	// Label, carrying it. An issue deleted, moved to another project or never
	// filed is false, with no error.
	IssueOpen(ctx context.Context, number int) (bool, error)
}

// TrackerSource is where a Brief reads requests: a project of a tracker.
type TrackerSource struct {
	// Tracker names the kind of tracker, as IssueTrackers key their
	// adapters.
	Tracker string
	// Address is the tracker's API, "" for its public service's.
	Address string
	// Project is the repository, team or project whose issues are read, as
	// the tracker names it.
	Project string
	// Label, where set, is the label that makes an issue of Project a
	// request: the tracker lists only the open issues that carry it.
	Label string
	// Site is the address people open the tracker's pages at, where it is
	// not Address; the issues' links point to it.
	Site string
	// Instance is the tracker's own ID of the site, where its API's address
	// names one.
	Instance string
	// Close says how an issue is closed for each reason, where the
	// tracker's workflow takes names: an absent reason takes the adapter's
	// default.
	Close map[CloseReason]CloseOutcome
}

// CloseOutcome is how an issue is closed for one reason, as the tracker
// names its states: the status it is moved to and the resolution it is
// given, each optional.
type CloseOutcome struct {
	Status     string
	Resolution string
}

// CredentialMissingError is a Secret whose data holds no whole credential
// for a tracker, or no Secret at all.
type CredentialMissingError struct {
	// Keys says which keys the tracker takes, as a person reads it: "the
	// key token", or a choice of keys.
	Keys string
}

func (e *CredentialMissingError) Error() string {
	return "the Secret lacks " + e.Keys
}

// TrackerAdapter builds the IssueTracker of a source of its tracker. It
// reads the credential out of the data of the source's Secret, nil when
// there is none, and turns it into what the tracker takes, minting a token
// where the tracker does: which keys a Secret holds is the adapter's
// business, never its caller's. A Secret without a whole credential is a
// CredentialMissingError; a credential the tracker refuses while it is
// built is an IssuesError with the reason.
type TrackerAdapter interface {
	Tracker(ctx context.Context, source TrackerSource, secret map[string][]byte) (IssueTracker, error)
}

// IssueTrackers are the operator's tracker adapters, by the name of their
// tracker: they build a tracker for a source of any of them.
type IssueTrackers map[string]TrackerAdapter

var _ TrackerAdapter = IssueTrackers(nil)

// Tracker builds the tracker of source with the adapter of source.Tracker.
// A tracker no adapter is kept for is ReasonTrackerError.
func (t IssueTrackers) Tracker(ctx context.Context, source TrackerSource,
	secret map[string][]byte) (IssueTracker, error) {
	adapter, ok := t[source.Tracker]
	if !ok || adapter == nil {
		return nil, &IssuesError{Reason: ReasonTrackerError,
			Message: fmt.Sprintf("the operator has no adapter for tracker %q", source.Tracker)}
	}
	return adapter.Tracker(ctx, source, secret)
}
