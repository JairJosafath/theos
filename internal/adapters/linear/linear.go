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

// Package linear is the request intake port (ports.IssueTracker) over
// Linear's GraphQL API: the open items of one team that carry one label are
// the requests (docs/plans/requests-and-chat.md, decisions 13, 14 and 17 to
// 20). Linear has no teams to ask about members of, so it is no
// ports.TeamMembers. Nothing builds one yet: no Brief can name Linear until
// the plan's steps 9 and 10.
package linear

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// APIURL is where Linear's GraphQL API is, unless Config says another.
const APIURL = "https://api.linear.app/graphql"

// maxAuthor is the longest author an issue is listed with, the most a
// request's status keeps.
const maxAuthor = 100

// pageSize is how many nodes one call asks for. Linear takes at most 250,
// and refuses a query that costs over 10,000 points, which a page of 250
// issues with their text could.
const pageSize = 100

// maxPages bounds a list or a comment thread, so that no item ties the
// adapter up for ever.
const maxPages = 40

// closedTypes are the types of a workflow state that make an item closed.
// Linear's reference names seven: triage, backlog, unstarted and started are
// open; completed, canceled and duplicate are not (the plan's decision 13
// had the first two of the three alone).
var closedTypes = []string{typeCompleted, typeCanceled, typeDuplicate}

// The types of a workflow state that closing moves an item into, and the
// reserved one that an item marked a duplicate is in.
const (
	typeCompleted = "completed"
	typeCanceled  = "canceled"
	typeDuplicate = "duplicate"
)

// Config is where the requests are: one label of one team.
type Config struct {
	// Team is the team's key, the prefix of its items' keys (ENG in ENG-412).
	Team string
	// Label is the name of the label that makes an item a request.
	Label string
	// Token is an API key of the factory's account.
	Token string
	// APIURL is Linear's GraphQL address, APIURL when empty. Linear's own
	// is the default; the operator's flag for it is the plan's step 10.
	APIURL string
}

// Client builds the issue tracker. HTTP is the client it calls Linear with,
// a default one when nil.
type Client struct {
	HTTP *http.Client
}

// Issues returns an IssueTracker on the items of cfg's team with cfg's
// label. ctx bounds each call.
func (c Client) Issues(cfg Config) (ports.IssueTracker, error) {
	if strings.TrimSpace(cfg.Team) == "" || strings.ContainsAny(cfg.Team, " \t\r\n") {
		return nil, errors.New("linear: the team is the team's key, such as ENG")
	}
	if strings.TrimSpace(cfg.Label) == "" {
		return nil, errors.New("linear: the label is required")
	}
	if cfg.Token == "" {
		return nil, errors.New("linear: the token is required")
	}
	if strings.ContainsAny(cfg.Token, "\r\n") {
		return nil, errors.New("linear: the token is not one line")
	}
	api := cfg.APIURL
	if api == "" {
		api = APIURL
	}
	if u, err := url.Parse(api); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("linear: invalid URL %q", api)
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{}
	}
	// A redirect turns a POST into a GET, which would answer with a page:
	// the call fails with the redirect's status instead.
	redirectless := *client
	redirectless.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	// Linear's team keys are upper case and its filter compares them
	// case-sensitively, while own() ignores case: one form for both.
	return &linear{team: strings.ToUpper(strings.TrimSpace(cfg.Team)), label: cfg.Label, token: cfg.Token, api: api, http: &redirectless}, nil
}

type linear struct {
	team, label, token, api string
	http                    *http.Client
}

var _ ports.IssueTracker = (*linear)(nil)

// key is item number as people name it: ENG-412.
func (l *linear) key(number int) string {
	return l.team + "-" + strconv.Itoa(number)
}

// The pieces of an answer, with the fields of Linear's schema they read.
type (
	user struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
	}
	bot struct {
		Name string `json:"name"`
	}
	team struct {
		ID     string `json:"id"`
		Key    string `json:"key"`
		States struct {
			Nodes []state `json:"nodes"`
		} `json:"states"`
	}
	state struct {
		ID       string  `json:"id"`
		Type     string  `json:"type"`
		Position float64 `json:"position"`
	}
	pageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	}
	issueNode struct {
		ID          string    `json:"id"`
		Number      float64   `json:"number"`
		Title       string    `json:"title"`
		Description *string   `json:"description"`
		URL         string    `json:"url"`
		CreatedAt   time.Time `json:"createdAt"`
		UpdatedAt   string    `json:"updatedAt"`
		Creator     *user     `json:"creator"`
		BotActor    *bot      `json:"botActor"`
		External    *user     `json:"externalUserCreator"`
		Team        *team     `json:"team"`
		State       *state    `json:"state"`
	}
	commentNode struct {
		ID        string    `json:"id"`
		Body      string    `json:"body"`
		URL       string    `json:"url"`
		CreatedAt time.Time `json:"createdAt"`
		User      *user     `json:"user"`
		BotActor  *bot      `json:"botActor"`
	}
)

// The operations, by the name Linear's logs show for them and the fake
// Linear dispatches on.
const (
	opOpenIssues  = "TheosOpenIssues"
	opOpenStamps  = "TheosOpenIssueStamps"
	opIssue       = "TheosIssue"
	opComments    = "TheosIssueComments"
	opComment     = "TheosComment"
	opCloseTarget = "TheosCloseTarget"
	opMove        = "TheosMoveIssue"
)

// The first fields of a list, which say whether the team is there: a filter
// on a team key that no team has lists nothing, as a team without requests
// does.
const teamField = `teams(filter: {key: {eq: $team}}, first: 1) { nodes { id } }`

const queryOpenIssues = `query ` + opOpenIssues + `($team: String!, $filter: IssueFilter!, $first: Int!, $after: String) {
  ` + teamField + `
  issues(filter: $filter, first: $first, after: $after) {
    nodes {
      id number title description url createdAt updatedAt
      creator { name displayName }
      botActor { name }
      externalUserCreator { name displayName }
    }
    pageInfo { hasNextPage endCursor }
  }
}`

// queryOpenStamps asks for what the ETag is made of and nothing more.
const queryOpenStamps = `query ` + opOpenStamps + `($team: String!, $filter: IssueFilter!, $first: Int!, $after: String) {
  ` + teamField + `
  issues(filter: $filter, first: $first, after: $after) {
    nodes { id updatedAt }
    pageInfo { hasNextPage endCursor }
  }
}`

const queryIssue = `query ` + opIssue + `($id: String!) {
  issue(id: $id) { id number team { key } }
}`

const queryComments = `query ` + opComments + `($id: String!, $first: Int!, $after: String) {
  viewer { id }
  issue(id: $id) {
    id number team { key }
    comments(first: $first, after: $after) {
      nodes { id body url createdAt user { id name displayName } botActor { name } }
      pageInfo { hasNextPage endCursor }
    }
  }
}`

const mutationComment = `mutation ` + opComment + `($input: CommentCreateInput!) {
  commentCreate(input: $input) {
    success
    comment { id body url user { name displayName } botActor { name } }
  }
}`

const queryCloseTarget = `query ` + opCloseTarget + `($id: String!) {
  issue(id: $id) {
    id number
    team { key states(first: 250) { nodes { id type position } } }
    state { id type }
  }
}`

const mutationMove = `mutation ` + opMove + `($id: String!, $input: IssueUpdateInput!) {
  issueUpdate(id: $id, input: $input) { success }
}`

// filter is the IssueFilter of the open requests: the team's items that
// carry the label and are in a state that is not closed.
func (l *linear) filter() map[string]any {
	return map[string]any{
		"team":   map[string]any{"key": map[string]any{"eq": l.team}},
		"labels": map[string]any{"some": map[string]any{"name": map[string]any{"eq": l.label}}},
		"state":  map[string]any{"type": map[string]any{"nin": closedTypes}},
	}
}

// openPages reads every page of the open requests with the operation op.
func (l *linear) openPages(ctx context.Context, op, query string) ([]issueNode, error) {
	var nodes []issueNode
	after := ""
	for range maxPages {
		vars := map[string]any{"team": l.team, "filter": l.filter(), "first": pageSize}
		if after != "" {
			vars["after"] = after
		}
		var data struct {
			Teams struct {
				Nodes []struct{} `json:"nodes"`
			} `json:"teams"`
			Issues struct {
				Nodes    []issueNode `json:"nodes"`
				PageInfo pageInfo    `json:"pageInfo"`
			} `json:"issues"`
		}
		if err := l.call(ctx, "listing the open issues of team "+l.team, op, query, vars,
			ports.ReasonTrackerNotFound, &data); err != nil {
			return nil, err
		}
		if len(data.Teams.Nodes) == 0 {
			return nil, &ports.IssuesError{Reason: ports.ReasonTrackerNotFound,
				Message: fmt.Sprintf("linear: no team %s, or one the credential can't see", l.team)}
		}
		nodes = append(nodes, data.Issues.Nodes...)
		if !data.Issues.PageInfo.HasNextPage {
			return nodes, nil
		}
		after = data.Issues.PageInfo.EndCursor
		if after == "" {
			break
		}
	}
	return nil, &ports.IssuesError{Reason: ports.ReasonTrackerError,
		Message: fmt.Sprintf("linear: more than %d open requests of team %s", maxPages*pageSize, l.team)}
}

// etag names the open items by their IDs and update times: Linear has no
// conditional list, so the list is read and this says whether it changed.
func etag(nodes []issueNode) string {
	stamps := make([]string, 0, len(nodes))
	for _, n := range nodes {
		stamps = append(stamps, n.ID+" "+n.UpdatedAt)
	}
	slices.Sort(stamps)
	sum := sha256.Sum256([]byte(strings.Join(stamps, "\n")))
	return hex.EncodeToString(sum[:])
}

// ListOpenIssues reads the open requests of the team. Given an ETag it reads
// the IDs and update times alone first, and the items' text only when they
// changed.
func (l *linear) ListOpenIssues(ctx context.Context, tag string) (ports.IssueList, error) {
	if tag != "" {
		stamps, err := l.openPages(ctx, opOpenStamps, queryOpenStamps)
		if err != nil {
			return ports.IssueList{}, err
		}
		if etag(stamps) == tag {
			return ports.IssueList{NotModified: true, ETag: tag}, nil
		}
	}
	nodes, err := l.openPages(ctx, opOpenIssues, queryOpenIssues)
	if err != nil {
		return ports.IssueList{}, err
	}
	list := ports.IssueList{ETag: etag(nodes)}
	for _, n := range nodes {
		number, ok := integer(n.Number)
		if !ok {
			return ports.IssueList{}, &ports.IssuesError{Reason: ports.ReasonTrackerError,
				Message: fmt.Sprintf("linear: issue %s has the number %v", n.ID, n.Number)}
		}
		issue := ports.TrackerIssue{Number: number, Key: l.key(number), Title: n.Title, URL: n.URL,
			Created: n.CreatedAt, Author: author(n.Creator, n.BotActor, n.External)}
		if n.Description != nil {
			issue.Body = *n.Description
		}
		list.Issues = append(list.Issues, issue)
	}
	// Linear lists the newest first with `first`: the port wants the oldest.
	slices.SortStableFunc(list.Issues, func(a, b ports.TrackerIssue) int {
		if c := a.Created.Compare(b.Created); c != 0 {
			return c
		}
		return a.Number - b.Number
	})
	return list, nil
}

// integer is f as a number of an item, which Linear types as a float.
func integer(f float64) (int, bool) {
	if f < 1 || f > math.MaxInt32 || f != math.Trunc(f) {
		return 0, false
	}
	return int(f), true
}

// person is the display name of a user, or their name where they have none.
func person(u *user) string {
	if u == nil {
		return ""
	}
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Name
}

// author is who made an item, as one line of at most maxAuthor characters:
// the display name of its creator, else what Linear gives for a bot or an
// external user who made it, else nothing. It invents no name.
func author(creator *user, b *bot, external *user) string {
	switch {
	case person(creator) != "":
		return oneLine(person(creator), maxAuthor)
	case b != nil && b.Name != "":
		return oneLine(b.Name, maxAuthor)
	}
	return oneLine(person(external), maxAuthor)
}

// own is an issue of this team and with this number, as Linear returned it
// for the key of number. Linear may answer a key the item had before it
// moved to another team with the item, which now has another key and is
// another team's: the factory would answer and close it. Such an item isn't
// there.
func (l *linear) own(n *issueNode, number int) error {
	got, ok := integer(n.Number)
	if n.ID == "" || n.Team == nil || !ok || got != number || !strings.EqualFold(n.Team.Key, l.team) {
		return &ports.IssuesError{Reason: ports.ReasonIssueNotFound,
			Message: fmt.Sprintf("linear: no issue %s of team %s", l.key(number), l.team)}
	}
	return nil
}

// missing is the error of a number no item can have.
func (l *linear) missing(number int) error {
	return &ports.IssuesError{Reason: ports.ReasonIssueNotFound,
		Message: fmt.Sprintf("linear: no issue %d of team %s", number, l.team)}
}

// issue reads item number of the team, which is ReasonIssueNotFound when
// there is none, or when it is another team's now.
func (l *linear) issue(ctx context.Context, number int) (issueNode, error) {
	if number < 1 {
		return issueNode{}, l.missing(number)
	}
	var data struct {
		Issue *issueNode `json:"issue"`
	}
	if err := l.call(ctx, "reading issue "+l.key(number), opIssue, queryIssue,
		map[string]any{"id": l.key(number)}, ports.ReasonIssueNotFound, &data); err != nil {
		return issueNode{}, err
	}
	if data.Issue == nil {
		return issueNode{}, l.missing(number)
	}
	if err := l.own(data.Issue, number); err != nil {
		return issueNode{}, err
	}
	return *data.Issue, nil
}

// asComment is c as the port names it.
func asComment(c commentNode) ports.TrackerComment {
	name := person(c.User)
	if name == "" && c.BotActor != nil {
		name = c.BotActor.Name
	}
	return ports.TrackerComment{ID: c.ID, Body: c.Body, Author: oneLine(name, maxAuthor), URL: c.URL}
}

// FindComment reads every comment of the item, since Linear lists the newest
// first and the port wants the first of the credential's own to have the
// marker.
func (l *linear) FindComment(ctx context.Context, number int, marker string) (*ports.TrackerComment, error) {
	if marker == "" {
		return nil, nil
	}
	if number < 1 {
		return nil, l.missing(number)
	}
	var found *commentNode
	after := ""
	for range maxPages {
		vars := map[string]any{"id": l.key(number), "first": pageSize}
		if after != "" {
			vars["after"] = after
		}
		var page struct {
			Issue *struct {
				issueNode
				Comments struct {
					Nodes    []commentNode `json:"nodes"`
					PageInfo pageInfo      `json:"pageInfo"`
				} `json:"comments"`
			} `json:"issue"`
			Viewer user `json:"viewer"`
		}
		if err := l.call(ctx, "listing comments of issue "+l.key(number), opComments, queryComments, vars,
			ports.ReasonIssueNotFound, &page); err != nil {
			return nil, err
		}
		if page.Issue == nil {
			return nil, l.missing(number)
		}
		if err := l.own(&page.Issue.issueNode, number); err != nil {
			return nil, err
		}
		if page.Viewer.ID == "" {
			return nil, &ports.IssuesError{Reason: ports.ReasonTrackerError,
				Message: "linear: Linear didn't say whose the token is"}
		}
		for _, c := range page.Issue.Comments.Nodes {
			if c.User == nil || c.User.ID != page.Viewer.ID || !strings.Contains(c.Body, marker) {
				continue
			}
			if found == nil || c.CreatedAt.Before(found.CreatedAt) {
				found = &c
			}
		}
		after = page.Issue.Comments.PageInfo.EndCursor
		if !page.Issue.Comments.PageInfo.HasNextPage || after == "" {
			if found == nil {
				return nil, nil
			}
			comment := asComment(*found)
			return &comment, nil
		}
	}
	return nil, &ports.IssuesError{Reason: ports.ReasonTrackerError,
		Message: fmt.Sprintf("linear: issue %s has more than %d comments", l.key(number), maxPages*pageSize)}
}

// Comment posts body as the token's account, on the item's ID: the key was
// checked a call before, so a post can't land on a moved item.
func (l *linear) Comment(ctx context.Context, number int, body string) (ports.TrackerComment, error) {
	item, err := l.issue(ctx, number)
	if err != nil {
		return ports.TrackerComment{}, err
	}
	var data struct {
		CommentCreate struct {
			Success bool        `json:"success"`
			Comment commentNode `json:"comment"`
		} `json:"commentCreate"`
	}
	input := map[string]any{"issueId": item.ID, "body": body}
	if err := l.call(ctx, "commenting on issue "+l.key(number), opComment, mutationComment,
		map[string]any{"input": input}, ports.ReasonIssueNotFound, &data); err != nil {
		return ports.TrackerComment{}, err
	}
	c := data.CommentCreate
	if !c.Success || c.Comment.ID == "" {
		return ports.TrackerComment{}, &ports.IssuesError{Reason: ports.ReasonTrackerError,
			Message: fmt.Sprintf("linear: commenting on issue %s was not done", l.key(number))}
	}
	return asComment(c.Comment), nil
}

// CloseIssue moves the item to the team's first state of type completed, or
// of type canceled for not planned. An item that is in a state of the type
// is left where it is, so closing twice is one move, and one a person closed
// meanwhile is moved only if its type is the other. A duplicate is not
// planned already.
func (l *linear) CloseIssue(ctx context.Context, number int, reason ports.CloseReason) error {
	var want string
	switch reason {
	case ports.CloseCompleted:
		want = typeCompleted
	case ports.CloseNotPlanned:
		want = typeCanceled
	default:
		return &ports.IssuesError{Reason: ports.ReasonTrackerError,
			Message: fmt.Sprintf("linear: %q is not a reason to close", reason)}
	}
	if number < 1 {
		return l.missing(number)
	}
	var data struct {
		Issue *issueNode `json:"issue"`
	}
	what := "closing issue " + l.key(number)
	if err := l.call(ctx, what, opCloseTarget, queryCloseTarget, map[string]any{"id": l.key(number)},
		ports.ReasonIssueNotFound, &data); err != nil {
		return err
	}
	item := data.Issue
	if item == nil {
		return l.missing(number)
	}
	if err := l.own(item, number); err != nil {
		return err
	}
	if item.State != nil && (item.State.Type == want || want == typeCanceled && item.State.Type == typeDuplicate) {
		return nil
	}
	target := firstState(item.Team.States.Nodes, want)
	if target == nil {
		return &ports.IssuesError{Reason: ports.ReasonTrackerError,
			Message: fmt.Sprintf("linear: team %s has no workflow state of type %s", l.team, want)}
	}
	var moved struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	if err := l.call(ctx, what, opMove, mutationMove,
		map[string]any{"id": item.ID, "input": map[string]any{"stateId": target.ID}},
		ports.ReasonIssueNotFound, &moved); err != nil {
		return err
	}
	if !moved.IssueUpdate.Success {
		return &ports.IssuesError{Reason: ports.ReasonTrackerError, Message: "linear: " + what + " was not done"}
	}
	return nil
}

// firstState is the state of type kind that comes first in its workflow,
// nil when there is none.
func firstState(states []state, kind string) *state {
	var first *state
	for i := range states {
		s := &states[i]
		if s.Type == kind && (first == nil || s.Position < first.Position ||
			s.Position == first.Position && s.ID < first.ID) {
			first = s
		}
	}
	return first
}
