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

package fake

import (
	"context"
	"crypto/rand"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// IssueTracker is an issue tracker in memory, for the controllers' tests
// and the IssueTracker and TeamMembers suites: its projects and their
// issues, its accounts and its teams. For gives the ports.IssueTracker an
// account reads a project through; the other methods are the tracker's
// people and the tracker itself, as a test drives them.
//
// A project's issues are numbered from 1, its pull requests among them, and
// keyed REQ-1, REQ-2: a key is never the number alone, so a caller that
// looks an issue up by its key fails on it. WithoutTeams makes a tracker
// without teams, whose port is no ports.TeamMembers.
type IssueTracker struct {
	s *trackerState
}

// TrackerOption shapes an IssueTracker.
type TrackerOption func(*trackerState)

// WithoutTeams makes a tracker without teams: the ports For returns are no
// ports.TeamMembers, as a tracker's without them.
func WithoutTeams() TrackerOption {
	return func(s *trackerState) { s.noTeams = true }
}

// TrackerAccess is what an account's credential may do.
type TrackerAccess struct {
	// Issues: read, comment on and close the issues.
	Issues bool
	// Members: read who is in a team.
	Members bool
}

// TrackerURL is the address of the fake tracker's pages.
const TrackerURL = "https://tracker.test"

// trackerKeyPrefix comes before an issue's number in its key.
const trackerKeyPrefix = "REQ-"

// TrackedIssue is an issue as the fake tracker keeps it.
type TrackedIssue struct {
	ports.TrackerIssue
	// PullRequest says it is a pull request, which no list shows.
	PullRequest bool
	// Unlabelled says the label that makes it a request was taken off: it
	// is open, and no list shows it.
	Unlabelled bool
	// Closed says it is closed, and Reason why.
	Closed bool
	Reason ports.CloseReason
	// Comments are what was said on it, oldest first.
	Comments []ports.TrackerComment
}

// trackerState is what every view of one IssueTracker shares.
type trackerState struct {
	mu       sync.Mutex
	noTeams  bool
	accounts map[string]trackerAccount // by token
	projects map[string]*trackerProject
	// teams are each team's people, true for an active member and false
	// for an invitation not accepted.
	teams    map[string]map[string]bool
	down     bool
	filed    int // issues filed on the tracker, which dates each
	comments int // comments made on the tracker, which number each
	// calls counts the port's calls, by method.
	calls map[string]int
}

type trackerAccount struct {
	login  string
	access TrackerAccess
}

type trackerProject struct {
	issues   []*TrackedIssue // by number - 1
	disabled bool
	// cannotClose, when set, is why its workflow can't close an open issue.
	cannotClose string
	// version counts the changes the ETag names: filing an issue, and an edit,
	// a comment or a close of one, bumps it.
	version int
}

// trackerEpoch is when the fake tracker's first issue is filed; each next
// one a second later.
var trackerEpoch = time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)

// NewIssueTracker returns an empty tracker, shaped by opts.
func NewIssueTracker(opts ...TrackerOption) *IssueTracker {
	s := &trackerState{accounts: map[string]trackerAccount{}, projects: map[string]*trackerProject{},
		teams: map[string]map[string]bool{}, calls: map[string]int{}}
	for _, opt := range opts {
		opt(s)
	}
	return &IssueTracker{s: s}
}

// Account makes an account whose credential may do what access says, and
// returns the credential.
func (tr *IssueTracker) Account(login string, access TrackerAccess) string {
	s := tr.s
	s.mu.Lock()
	defer s.mu.Unlock()
	token := rand.Text()
	s.accounts[token] = trackerAccount{login: login, access: access}
	return token
}

// Project makes a project, a repository or a team's board, that holds no
// issue; making one that exists changes nothing.
func (tr *IssueTracker) Project(name string) {
	s := tr.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.projects[name] == nil {
		s.projects[name] = &trackerProject{}
	}
}

// For returns the port of project as the account whose credential token
// is; a ports.TeamMembers too unless the tracker is WithoutTeams.
func (tr *IssueTracker) For(project, token string) ports.IssueTracker {
	v := &trackerView{s: tr.s, project: project, token: token}
	if tr.s.noTeams {
		return v
	}
	return &teamsView{v}
}

// CredentialKey is the key of a Secret's data the fake tracker reads an
// account's credential from.
const CredentialKey = "token"

var _ ports.TrackerAdapter = (*IssueTracker)(nil)

// Tracker is the fake as a ports.TrackerAdapter: For on source.Project as
// the account whose credential the Secret's CredentialKey holds. A Secret
// without it, or none, is a ports.CredentialMissingError; the address is
// not read.
func (tr *IssueTracker) Tracker(_ context.Context, source ports.TrackerSource,
	secret map[string][]byte) (ports.IssueTracker, error) {
	token := strings.TrimSpace(string(secret[CredentialKey]))
	if token == "" {
		return nil, &ports.CredentialMissingError{Keys: "the key " + CredentialKey}
	}
	return tr.For(source.Project, token), nil
}

// File files an issue on project as author and returns its number.
func (tr *IssueTracker) File(project, author, title, body string) (int, error) {
	return tr.file(project, author, title, body, false)
}

// PullRequest opens a pull request on project as author, among its issues,
// and returns its number.
func (tr *IssueTracker) PullRequest(project, author, title string) (int, error) {
	return tr.file(project, author, title, "", true)
}

func (tr *IssueTracker) file(project, author, title, body string, pull bool) (int, error) {
	s := tr.s
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[project]
	if !ok {
		return 0, fmt.Errorf("no project %s", project)
	}
	n := len(p.issues) + 1
	created := trackerEpoch.Add(time.Duration(s.filed) * time.Second)
	s.filed++
	p.issues = append(p.issues, &TrackedIssue{PullRequest: pull, TrackerIssue: ports.TrackerIssue{Number: n,
		Key: trackerKeyPrefix + strconv.Itoa(n), Title: title, Body: body, Author: author, Created: created,
		URL: fmt.Sprintf("%s/%s/issues/%d", TrackerURL, project, n)}})
	p.version++
	return n, nil
}

// Edit changes the body of issue number of project, as its author does.
func (tr *IssueTracker) Edit(project string, number int, body string) error {
	return tr.change(project, number, func(i *TrackedIssue) { i.Body = body })
}

// Close closes issue number of project, as a person does: completed.
func (tr *IssueTracker) Close(project string, number int) error {
	return tr.change(project, number, func(i *TrackedIssue) { i.Closed, i.Reason = true, ports.CloseCompleted })
}

// Say comments body on issue number of project as author.
func (tr *IssueTracker) Say(project string, number int, author, body string) error {
	s := tr.s
	return tr.change(project, number, func(i *TrackedIssue) { s.comment(project, i, author, body) })
}

func (tr *IssueTracker) change(project string, number int, change func(*TrackedIssue)) error {
	s := tr.s
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[project]
	if !ok || number < 1 || number > len(p.issues) {
		return fmt.Errorf("no issue %d in project %s", number, project)
	}
	change(p.issues[number-1])
	p.version++
	return nil
}

// Unlabel takes the label that makes issue number of project a request off,
// as a person does on a tracker that reads requests by a label: it stays
// open, and no list shows it from then on. Label puts it back.
func (tr *IssueTracker) Unlabel(project string, number int) error {
	return tr.change(project, number, func(i *TrackedIssue) { i.Unlabelled = true })
}

// Label puts back the label Unlabel took off issue number of project.
func (tr *IssueTracker) Label(project string, number int) error {
	return tr.change(project, number, func(i *TrackedIssue) { i.Unlabelled = false })
}

// CannotClose makes project's workflow offer no way to close an open issue,
// saying why, as a tracker whose settings name a state it doesn't have does:
// CloseIssue fails with ports.ReasonCannotClose and changes nothing. An empty
// why closes again.
func (tr *IssueTracker) CannotClose(project, why string) {
	s := tr.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.projects[project]; p != nil {
		p.cannotClose = why
	}
}

// Disable turns project's issues off.
func (tr *IssueTracker) Disable(project string) {
	s := tr.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.projects[project]; p != nil {
		p.disabled = true
	}
}

// Down makes the tracker answer nothing from now on, as an outage or a rate
// limit does; Down(false) brings it back.
func (tr *IssueTracker) Down(down bool) {
	s := tr.s
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down = down
}

// Team makes a team nobody is in; making one that exists changes nothing.
func (tr *IssueTracker) Team(team string) {
	s := tr.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.teams[team] == nil {
		s.teams[team] = map[string]bool{}
	}
}

// Join makes login an active member of team, accepting an invitation.
func (tr *IssueTracker) Join(team, login string) error {
	return tr.member(team, login, true)
}

// Invite invites login to team; until they join, they are not a member.
func (tr *IssueTracker) Invite(team, login string) error {
	return tr.member(team, login, false)
}

func (tr *IssueTracker) member(team, login string, active bool) error {
	s := tr.s
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.teams[team]
	if !ok {
		return fmt.Errorf("no team %s", team)
	}
	t[login] = active
	return nil
}

// Issue is issue number of project as the tracker shows it.
func (tr *IssueTracker) Issue(project string, number int) (TrackedIssue, bool) {
	s := tr.s
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[project]
	if !ok || number < 1 || number > len(p.issues) {
		return TrackedIssue{}, false
	}
	i := *p.issues[number-1]
	i.Comments = slices.Clone(i.Comments)
	return i, true
}

// Calls counts the port's calls of method, such as "ListOpenIssues", on
// every project.
func (tr *IssueTracker) Calls(method string) int {
	s := tr.s
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[method]
}

// comment adds body to issue i of project as author.
func (s *trackerState) comment(project string, i *TrackedIssue, author, body string) ports.TrackerComment {
	s.comments++
	id := strconv.Itoa(s.comments)
	c := ports.TrackerComment{ID: id, Body: body, Author: author,
		URL: fmt.Sprintf("%s/%s/issues/%d#comment-%s", TrackerURL, project, i.Number, id)}
	i.Comments = append(i.Comments, c)
	return c
}

// trackerError is a failed call with reason; the fake speaks no HTTP, so it
// has no status.
func trackerError(reason, format string, args ...any) error {
	return &ports.IssuesError{Reason: reason, Message: fmt.Sprintf(format, args...)}
}

// trackerView is an IssueTracker's port: one project as one account.
type trackerView struct {
	s       *trackerState
	project string
	token   string
}

var (
	_ ports.IssueTracker = (*trackerView)(nil)
	_ ports.IssueTracker = (*teamsView)(nil)
	_ ports.TeamMembers  = (*teamsView)(nil)
)

// open locks the tracker, counts a call of method, and returns the account
// and, unless the call fails, the project; it fails when the tracker is
// down, the credential unknown or without issues, or the project missing
// or with its issues off. The caller unlocks the tracker.
func (v *trackerView) open(method string) (trackerAccount, *trackerProject, error) {
	s := v.s
	s.mu.Lock()
	s.calls[method]++
	if s.down {
		return trackerAccount{}, nil, trackerError(ports.ReasonTrackerUnavailable,
			"the tracker is down")
	}
	account, ok := s.accounts[v.token]
	if !ok {
		return trackerAccount{}, nil, trackerError(ports.ReasonUnauthorized,
			"the tracker doesn't know the credential")
	}
	if !account.access.Issues {
		return account, nil, trackerError(ports.ReasonTokenLacksIssues,
			"%s may not use the issues", account.login)
	}
	p, ok := s.projects[v.project]
	if !ok {
		return account, nil, trackerError(ports.ReasonTrackerNotFound, "no project %s", v.project)
	}
	if p.disabled {
		return account, nil, trackerError(ports.ReasonIssuesDisabled,
			"project %s has its issues turned off", v.project)
	}
	return account, p, nil
}

// issue is the project's open or closed issue number, never a pull request.
func (v *trackerView) issue(p *trackerProject, number int) (*TrackedIssue, error) {
	if number < 1 || number > len(p.issues) || p.issues[number-1].PullRequest {
		return nil, trackerError(ports.ReasonIssueNotFound, "no issue %d in project %s", number,
			v.project)
	}
	return p.issues[number-1], nil
}

// etag names a version of p's open issues.
func (v *trackerView) etag(p *trackerProject) string {
	return fmt.Sprintf("%q", v.project+"@"+strconv.Itoa(p.version))
}

func (v *trackerView) ListOpenIssues(_ context.Context, etag string) (ports.IssueList, error) {
	_, p, err := v.open("ListOpenIssues")
	defer v.s.mu.Unlock()
	if err != nil {
		return ports.IssueList{}, err
	}
	now := v.etag(p)
	if etag != "" && etag == now {
		return ports.IssueList{NotModified: true, ETag: now}, nil
	}
	list := ports.IssueList{ETag: now}
	for _, i := range p.issues {
		if !i.Closed && !i.PullRequest && !i.Unlabelled {
			list.Issues = append(list.Issues, i.TrackerIssue)
		}
	}
	return list, nil
}

func (v *trackerView) FindComment(_ context.Context, number int, marker string) (*ports.TrackerComment, error) {
	account, p, err := v.open("FindComment")
	defer v.s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	i, err := v.issue(p, number)
	if err != nil || marker == "" {
		return nil, err
	}
	for _, c := range i.Comments {
		if c.Author == account.login && strings.Contains(c.Body, marker) {
			return &c, nil
		}
	}
	return nil, nil
}

func (v *trackerView) Comment(_ context.Context, number int, body string) (ports.TrackerComment, error) {
	account, p, err := v.open("Comment")
	defer v.s.mu.Unlock()
	if err != nil {
		return ports.TrackerComment{}, err
	}
	i, err := v.issue(p, number)
	if err != nil {
		return ports.TrackerComment{}, err
	}
	return v.s.comment(v.project, i, account.login, body), nil
}

func (v *trackerView) CloseIssue(_ context.Context, number int, reason ports.CloseReason) error {
	_, p, err := v.open("CloseIssue")
	defer v.s.mu.Unlock()
	if err != nil {
		return err
	}
	i, err := v.issue(p, number)
	if err != nil {
		return err
	}
	if reason != ports.CloseCompleted && reason != ports.CloseNotPlanned {
		return trackerError(ports.ReasonTrackerError, "no reason to close %q",
			reason)
	}
	if !i.Closed && p.cannotClose != "" {
		return trackerError(ports.ReasonCannotClose, "%s", p.cannotClose)
	}
	if !i.Closed {
		p.version++
	}
	i.Closed, i.Reason = true, reason
	return nil
}

// teamsView is the port of a tracker that has teams.
type teamsView struct {
	*trackerView
}

func (v *teamsView) TeamMember(_ context.Context, team, login string) (bool, error) {
	s := v.s
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls["TeamMember"]++
	if s.down {
		return false, trackerError(ports.ReasonTrackerUnavailable,
			"the tracker is down")
	}
	account, ok := s.accounts[v.token]
	if !ok {
		return false, trackerError(ports.ReasonUnauthorized,
			"the tracker doesn't know the credential")
	}
	if !account.access.Members {
		return false, trackerError(ports.ReasonTokenLacksMembers,
			"%s may not read a team's members", account.login)
	}
	return s.teams[team][login], nil
}
