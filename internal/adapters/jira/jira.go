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

// Package jira is the request intake port (ports.IssueTracker) over Jira
// Cloud's REST API version 3: the open items of one project that carry one
// label are the requests (docs/plans/requests-and-chat.md, decisions 13, 14
// and 17 to 20, and "Step 6 as built, Jira"). Jira Cloud has no teams to ask
// about members of, so it is no ports.TeamMembers. Data Center has other
// auth and other search calls and is not claimed. A Brief's
// spec.requestSources entry builds one through Client.Tracker (step 10).
package jira

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// APIURL is where Atlassian's API gateway is, which a scoped API token
// calls instead of the site's own address, unless Config says another.
const APIURL = "https://api.atlassian.com"

// The scopes of the API token the adapter is built for. read:jira-user is
// for one call, the account's own (GET /myself), which is how FindComment
// knows its own comments from a stranger's.
const (
	ScopeRead  = "read:jira-work"
	ScopeWrite = "write:jira-work"
	ScopeUser  = "read:jira-user"
)

// The names Jira's own answers are made of.
const (
	// categoryDone is the key of the status category of an item that is
	// closed.
	categoryDone = "done"
	// resolutionDone and resolutionWontDo are the resolutions a standard
	// Jira Cloud site has for completed and not planned, sent when Closing
	// names none.
	resolutionDone    = "Done"
	resolutionWontDo  = "Won't Do"
	fieldResolution   = "resolution"
	issueFields       = "summary,description,reporter,creator,created,updated,project"
	issueStampsFields = "updated"
)

// maxAuthor is the longest author an issue is listed with, the most a
// request's status keeps.
const maxAuthor = 100

// pageSize is how many issues or comments one call asks for.
const pageSize = 100

// maxPages bounds a list or a comment thread, so that no project ties the
// adapter up for ever and none is returned cut short.
const maxPages = 100

var (
	// projectKey is what a Jira project's key is made of.
	projectKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	// cloudIDPattern is what a site's ID at Atlassian is made of: a UUID
	// today, and nothing that could change the path it goes in.
	cloudIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
)

// Outcome is how an item is closed for one of the two reasons: into the
// status named Status and with the resolution named Resolution. Each is
// optional.
type Outcome struct {
	// Status is the name of a status of category Done, such as "Done" or
	// "Won't Do". Empty: the first status of category Done the item offers
	// a transition into.
	Status string
	// Resolution is the name of a resolution the site has, such as "Fixed".
	// Empty: Done for completed and Won't Do for not planned. It is sent
	// only when the transition's screen takes a resolution, since a
	// team-managed project has none to send.
	Resolution string
}

// Closing is how an item is closed, for each reason. The CRD's
// `close.completed` and `close.notPlanned` are step 9's.
type Closing struct {
	Completed  Outcome
	NotPlanned Outcome
}

// Config is where the requests are: one label of one project of one site.
type Config struct {
	// Site is the site people open and the factory's links point to, such
	// as https://acme.atlassian.net. It is never called.
	Site string
	// CloudID is the site's ID at Atlassian, which a scoped token's address
	// holds.
	CloudID string
	// Project is the project's key, the prefix of its items' keys (SHOP in
	// SHOP-412).
	Project string
	// Label is the label that makes an item a request.
	Label string
	// Email and Token are the Atlassian account's address and an API token
	// with scopes (ScopeRead, ScopeWrite and ScopeUser).
	Email, Token string
	// Address is the API gateway, APIURL when empty. The operator's flag
	// for it is the plan's step 10.
	Address string
	// Close is how items are closed.
	Close Closing
}

// Client builds the issue tracker. HTTP is the client it calls Jira with, a
// default one when nil. Address is the API gateway for every source that
// names no address of its own, the package's APIURL when empty: the
// operator's --jira-api-url.
type Client struct {
	HTTP    *http.Client
	Address string
}

// The keys of a source's Secret: the Atlassian account's e-mail address and
// its API token.
const (
	SecretEmail = "email"
	SecretToken = "token"
)

var _ ports.TrackerAdapter = Client{}

// Tracker is the adapter as a ports.TrackerAdapter: the tracker of
// source.Project, the project's key, and source.Label on the site
// source.Site, whose cloud ID is source.Instance, with the account the
// Secret's SecretEmail and SecretToken hold. source.Close names the status
// and the resolution of each reason. source.Address, else the Client's
// Address, is the gateway. A Secret without both keys is a
// ports.CredentialMissingError; a source the adapter can't read
// ReasonTrackerError.
func (c Client) Tracker(_ context.Context, source ports.TrackerSource,
	secret map[string][]byte) (ports.IssueTracker, error) {
	email := strings.TrimSpace(string(secret[SecretEmail]))
	token := strings.TrimSpace(string(secret[SecretToken]))
	if email == "" || token == "" {
		return nil, &ports.CredentialMissingError{Keys: "the keys " + SecretEmail + " and " + SecretToken}
	}
	outcome := func(reason ports.CloseReason) Outcome {
		o := source.Close[reason]
		return Outcome{Status: o.Status, Resolution: o.Resolution}
	}
	tr, err := c.Issues(Config{Site: source.Site, CloudID: source.Instance, Project: source.Project,
		Label: source.Label, Email: email, Token: token, Address: cmp.Or(source.Address, c.Address),
		Close: Closing{Completed: outcome(ports.CloseCompleted), NotPlanned: outcome(ports.CloseNotPlanned)}})
	if err != nil {
		return nil, &ports.IssuesError{Reason: ports.ReasonTrackerError, Message: err.Error()}
	}
	return tr, nil
}

// Issues returns an IssueTracker on the items of cfg's project with cfg's
// label.
func (c Client) Issues(cfg Config) (ports.IssueTracker, error) {
	site, err := url.Parse(cfg.Site)
	if err != nil || site.Scheme != "https" || site.Host == "" {
		return nil, fmt.Errorf("jira: the site is the https address people open, such as https://acme.atlassian.net, not %q", cfg.Site)
	}
	if !cloudIDPattern.MatchString(cfg.CloudID) {
		return nil, errors.New("jira: the cloud ID is the site's ID at Atlassian")
	}
	if !projectKey.MatchString(cfg.Project) {
		return nil, fmt.Errorf("jira: the project is the project's key, such as SHOP, not %q", cfg.Project)
	}
	if cfg.Label == "" || strings.ContainsAny(cfg.Label, " \t\r\n") {
		return nil, errors.New("jira: the label is one word: a Jira label has no space")
	}
	if cfg.Email == "" || strings.ContainsAny(cfg.Email, ":\r\n") {
		return nil, errors.New("jira: the email is the account's address")
	}
	if cfg.Token == "" || strings.ContainsAny(cfg.Token, "\r\n") {
		return nil, errors.New("jira: the token is one line")
	}
	address := cfg.Address
	if address == "" {
		address = APIURL
	}
	u, err := url.Parse(address)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("jira: invalid address %q", address)
	}
	// Basic auth over http would send the token in clear text: only a
	// loopback address, which never leaves the machine, may use it.
	if u.Scheme == "http" && !loopback(u.Hostname()) {
		return nil, fmt.Errorf("jira: the address %q is http; the credential is sent only over https or to a loopback address", address)
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{}
	}
	// A redirect would take the credential to another address: the call
	// fails with the redirect's status instead.
	redirectless := *client
	redirectless.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &jira{
		site: strings.TrimRight(cfg.Site, "/"), project: strings.ToUpper(cfg.Project), label: cfg.Label,
		email: cfg.Email, token: cfg.Token, closing: cfg.Close, http: &redirectless,
		base: strings.TrimRight(address, "/") + "/ex/jira/" + cfg.CloudID + "/rest/api/3",
	}, nil
}

type jira struct {
	site, project, label, email, token string
	// base is every call's address up to the resource: the gateway, the
	// site's cloud ID and the REST API's version.
	base    string
	closing Closing
	http    *http.Client

	mu sync.Mutex
	// account is the ID of the credential's own account, once it is read.
	account string
}

var (
	_ ports.IssueTracker = (*jira)(nil)
	_ ports.IssueReader  = (*jira)(nil)
)

// key is item number as people name it: SHOP-412.
func (j *jira) key(number int) string {
	return j.project + "-" + strconv.Itoa(number)
}

// page is the address of an item or a comment, on the site.
func (j *jira) page(key string) string {
	return j.site + "/browse/" + key
}

// The pieces of an answer, with the fields of Jira's schema they read.
type (
	person struct {
		AccountID   string `json:"accountId"`
		DisplayName string `json:"displayName"`
	}
	issueNode struct {
		ID     string `json:"id"`
		Key    string `json:"key"`
		Fields struct {
			Summary     string                `json:"summary"`
			Description json.RawMessage       `json:"description"`
			Reporter    *person               `json:"reporter"`
			Creator     *person               `json:"creator"`
			Created     string                `json:"created"`
			Updated     string                `json:"updated"`
			Project     *struct{ Key string } `json:"project"`
			Labels      []string              `json:"labels"`
			Status      *struct {
				Name           string `json:"name"`
				StatusCategory struct {
					Key string `json:"key"`
				} `json:"statusCategory"`
			} `json:"status"`
		} `json:"fields"`
	}
	searchPage struct {
		Issues        []issueNode `json:"issues"`
		NextPageToken string      `json:"nextPageToken"`
		IsLast        bool        `json:"isLast"`
	}
	commentNode struct {
		ID     string          `json:"id"`
		Author *person         `json:"author"`
		Body   json.RawMessage `json:"body"`
	}
	commentPage struct {
		Comments   []commentNode `json:"comments"`
		StartAt    int           `json:"startAt"`
		MaxResults int           `json:"maxResults"`
		Total      int           `json:"total"`
	}
)

// number is the digits of key, the number the port's calls take. The API
// stores a request's number as an int32 (decision 14), so a number that does
// not fit one, like one that is not digits, is no number (CodeQL #228,
// go/incorrect-integer-conversion).
func number(key string) (int, bool) {
	i := strings.LastIndex(key, "-")
	if i < 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(key[i+1:], 10, 32)
	return int(n), err == nil && n > 0
}

// jql is the search for the project's open items that carry the label:
// open is a status whose category is not Done (decision 13).
func (j *jira) jql() string {
	return `project = ` + quote(j.project) + ` AND labels = ` + quote(j.label) +
		` AND statusCategory != Done ORDER BY created ASC`
}

// quote is s as a JQL string.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// search reads every page of the open requests, asking for fields.
func (j *jira) search(ctx context.Context, fields string) ([]issueNode, error) {
	var all []issueNode
	token := ""
	for range maxPages {
		q := url.Values{"jql": {j.jql()}, "fields": {fields}, "maxResults": {strconv.Itoa(pageSize)}}
		if token != "" {
			q.Set("nextPageToken", token)
		}
		var p searchPage
		if err := j.get(ctx, "list the open items", "/search/jql", q, ports.ReasonTrackerNotFound, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Issues...)
		if p.IsLast || p.NextPageToken == "" || len(p.Issues) == 0 {
			return all, nil
		}
		token = p.NextPageToken
	}
	return nil, &ports.IssuesError{Reason: ports.ReasonTrackerError,
		Message: fmt.Sprintf("jira: the project has more than %d open requests", maxPages*pageSize)}
}

// etag names the open items by their IDs and when each was updated.
func etag(issues []issueNode) string {
	stamps := make([]string, 0, len(issues))
	for _, i := range issues {
		stamps = append(stamps, i.ID+"@"+i.Fields.Updated)
	}
	slices.Sort(stamps)
	sum := sha256.Sum256([]byte(strings.Join(stamps, "\n")))
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// ListOpenIssues lists the open requests. Jira has no conditional list, so
// the ETag is the adapter's own (decision 18): a list that costs only the
// IDs and update times is read first, and the items with their text only
// when it differs from etag's.
func (j *jira) ListOpenIssues(ctx context.Context, since string) (ports.IssueList, error) {
	if since != "" {
		stamps, err := j.search(ctx, issueStampsFields)
		if err != nil {
			return ports.IssueList{}, err
		}
		if now := etag(stamps); now == since {
			return ports.IssueList{NotModified: true, ETag: now}, nil
		}
	}
	nodes, err := j.search(ctx, issueFields)
	if err != nil {
		return ports.IssueList{}, err
	}
	list := ports.IssueList{ETag: etag(nodes)}
	for _, n := range nodes {
		num, ok := number(n.Key)
		if !ok {
			return ports.IssueList{}, &ports.IssuesError{Reason: ports.ReasonTrackerError,
				Message: fmt.Sprintf("jira: an item has the key %q, which is no project's key and number", n.Key)}
		}
		list.Issues = append(list.Issues, ports.TrackerIssue{Number: num, Key: j.key(num), Title: n.Fields.Summary,
			Body: FlattenADF(n.Fields.Description), Author: j.author(n), URL: j.page(j.key(num)),
			Created: parseTime(n.Fields.Created)})
	}
	// The search orders by when an item was filed; items filed in the same
	// second go by number.
	slices.SortStableFunc(list.Issues, func(a, b ports.TrackerIssue) int {
		if c := a.Created.Compare(b.Created); c != 0 {
			return c
		}
		return a.Number - b.Number
	})
	return list, nil
}

// author is the display name Jira gives for who made the item: its
// reporter, else its creator. An item that came in by e-mail or a service
// desk, or that an automation made, may have neither, or a name Jira
// withholds: it is then empty, and no name is made up.
func (j *jira) author(n issueNode) string {
	for _, p := range []*person{n.Fields.Reporter, n.Fields.Creator} {
		if p != nil {
			if name := oneLine(p.DisplayName, maxAuthor); name != "" {
				return name
			}
		}
	}
	return ""
}

// loopback is whether host is localhost or a loopback IP address.
func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// parseTime reads a time as Jira writes it: RFC 3339, or with an offset
// that has no colon (2026-01-02T09:00:00.000+0000), which is what its
// reference shows. It is the zero time when it is neither.
func parseTime(s string) time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05.000-0700", time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// item reads item number of the project. Jira keeps the old key of an item
// that moved to another project and answers a call for it with the item as
// it is now (Atlassian's reference for "Get issue"): an item of another
// project is IssueNotFound, so that the factory never comments on or closes
// what is not its project's.
func (j *jira) item(ctx context.Context, number int, fields string) (issueNode, error) {
	if number < 1 {
		return issueNode{}, notFound(j.key(number))
	}
	var n issueNode
	err := j.get(ctx, "read "+j.key(number), "/issue/"+url.PathEscape(j.key(number)),
		url.Values{"fields": {fields}}, ports.ReasonIssueNotFound, &n)
	if err != nil {
		return issueNode{}, err
	}
	p := n.Fields.Project
	if p == nil || p.Key == "" {
		return issueNode{}, &ports.IssuesError{Reason: ports.ReasonTrackerError,
			Message: "jira: read " + j.key(number) + ": the answer names no project"}
	}
	if !strings.EqualFold(p.Key, j.project) {
		return issueNode{}, notFound(j.key(number))
	}
	return n, nil
}

// IssueOpen reads item number on its own, never through the search
// ListOpenIssues makes, whose index can lag behind a change for minutes and
// sometimes longer (#161), and reports whether that search lists it: in a
// status whose category is not Done, and carrying the label. An item that
// isn't there, or is another project's now, is false.
func (j *jira) IssueOpen(ctx context.Context, number int) (bool, error) {
	n, err := j.item(ctx, number, "project,status,labels")
	if ports.IssuesReason(err) == ports.ReasonIssueNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	status := n.Fields.Status
	if status == nil {
		return false, &ports.IssuesError{Reason: ports.ReasonTrackerError,
			Message: "jira: read " + j.key(number) + ": the answer names no status"}
	}
	if status.StatusCategory.Key == categoryDone {
		return false, nil
	}
	// A label is matched ignoring case, so that no item the search lists is
	// left out.
	return slices.ContainsFunc(n.Fields.Labels, func(l string) bool { return strings.EqualFold(l, j.label) }), nil
}

func notFound(key string) *ports.IssuesError {
	return &ports.IssuesError{Reason: ports.ReasonIssueNotFound, Message: "jira: no item " + key + " in the project"}
}

// me is the ID of the credential's own account.
func (j *jira) me(ctx context.Context) (string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.account != "" {
		return j.account, nil
	}
	var who person
	if err := j.get(ctx, "read the account (the token needs the scope "+ScopeUser+")", "/myself", nil,
		ports.ReasonTrackerError, &who); err != nil {
		return "", err
	}
	if who.AccountID == "" {
		return "", &ports.IssuesError{Reason: ports.ReasonTrackerError, Message: "jira: the account has no ID"}
	}
	j.account = who.AccountID
	return j.account, nil
}

// comments reads every comment on item id, oldest first.
func (j *jira) comments(ctx context.Context, id string) ([]commentNode, error) {
	var all []commentNode
	for range maxPages {
		q := url.Values{"startAt": {strconv.Itoa(len(all))}, "maxResults": {strconv.Itoa(pageSize)},
			"orderBy": {"created"}}
		var p commentPage
		if err := j.get(ctx, "read the comments", "/issue/"+url.PathEscape(id)+"/comment", q,
			ports.ReasonIssueNotFound, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Comments...)
		if len(p.Comments) == 0 || len(all) >= p.Total {
			return all, nil
		}
	}
	return nil, &ports.IssuesError{Reason: ports.ReasonTrackerError,
		Message: fmt.Sprintf("jira: the item has more than %d comments", maxPages*pageSize)}
}

// FindComment finds the first comment the credential's account wrote whose
// text holds marker. The marker is the last line of a comment, shown: Jira
// has no hidden text.
func (j *jira) FindComment(ctx context.Context, number int, marker string) (*ports.TrackerComment, error) {
	n, err := j.item(ctx, number, "project")
	if err != nil || marker == "" {
		return nil, err
	}
	own, err := j.me(ctx)
	if err != nil {
		return nil, err
	}
	all, err := j.comments(ctx, n.ID)
	if err != nil {
		return nil, err
	}
	for _, c := range all {
		if c.Author != nil && c.Author.AccountID == own && strings.Contains(FlattenADF(c.Body), marker) {
			found := j.comment(j.key(number), c)
			return &found, nil
		}
	}
	return nil, nil
}

// comment is c, a comment on item key, as the port has it.
func (j *jira) comment(key string, c commentNode) ports.TrackerComment {
	out := ports.TrackerComment{ID: c.ID, Body: FlattenADF(c.Body),
		URL: j.page(key) + "?focusedCommentId=" + url.QueryEscape(c.ID)}
	if c.Author != nil {
		out.Author = oneLine(c.Author.DisplayName, maxAuthor)
	}
	return out
}

// Comment posts body as a comment, in Atlassian's document format: a
// paragraph to a line.
func (j *jira) Comment(ctx context.Context, number int, body string) (ports.TrackerComment, error) {
	n, err := j.item(ctx, number, "project")
	if err != nil {
		return ports.TrackerComment{}, err
	}
	var c commentNode
	if err := j.send(ctx, "comment on "+j.key(number), http.MethodPost, "/issue/"+url.PathEscape(n.ID)+"/comment",
		map[string]any{"body": adfDocument(body)}, ports.ReasonIssueNotFound, &c); err != nil {
		return ports.TrackerComment{}, err
	}
	if c.ID == "" {
		return ports.TrackerComment{}, &ports.IssuesError{Reason: ports.ReasonTrackerError,
			Message: "jira: comment on " + j.key(number) + ": unreadable response"}
	}
	return j.comment(j.key(number), c), nil
}
