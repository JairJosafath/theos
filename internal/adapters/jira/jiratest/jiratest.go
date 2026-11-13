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

// Package jiratest is a fake Jira Cloud over httptest: the REST API version
// 3 calls the jira adapter makes, behind the address a scoped API token
// calls (/ex/jira/<cloudID>/rest/api/3), which is the only one it serves:
// the site's own address is never called. It has projects with workflows,
// items with labels, reporters, ADF descriptions and comments, an item that
// moved to another project and is still answered by its old key, the
// enhanced JQL search with its page tokens, transitions with their screens,
// basic auth with scopes, and an outage and a rate limit on demand. The
// adapter's tests and, later, the controllers' envtest share it.
//
// It is shaped by Atlassian's reference for what is in it (paths, query
// parameters, request and answer fields). Three things are from memory,
// since the reference holds none of them: the words of an error, the 401 a
// token that lacks a scope gets, and the status categories' keys (new,
// indeterminate, done). The JQL it understands is the one query the adapter
// sends and nothing else, so a change of the adapter's query is a change
// here.
package jiratest

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// SiteURL is the fake site's address: where people open an item. The fake
// never serves it.
const SiteURL = "https://acme.jira.test"

// The three scopes a call needs, as an API token names them.
const (
	ScopeRead  = "read:jira-work"
	ScopeWrite = "write:jira-work"
	ScopeUser  = "read:jira-user"
)

// Credentials are an account's e-mail address and API token.
type Credentials struct {
	Email, Token string
}

// Maker is who made an item, or a comment's author: the display name Jira
// shows, and the type of the account ("atlassian", the default, or "app").
// The zero Maker is nobody.
type Maker struct {
	Name string
	Type string
}

// Item is an item to file.
type Item struct {
	Project     string
	Title       string
	Description string
	// ADF is the description as Atlassian's document format, when it has
	// more than Description's paragraphs; it takes Description's place.
	ADF    json.RawMessage
	Labels []string
	// Reporter and Creator are who made it, as Jira's two fields say; an
	// item that came in by e-mail or an automation may have neither.
	Reporter, Creator Maker
}

// Shown is an item as the site's page shows it.
type Shown struct {
	Key         string
	Title       string
	Description string
	Labels      []string
	Status      string
	// Category is the key of the status's category.
	Category   string
	Resolution string
	Reporter   string
	Created    time.Time
	Comments   []ShownComment
}

// ShownComment is a comment as the page shows it, its text one line to a
// paragraph.
type ShownComment struct {
	Body string
	// By is the display name of its author.
	By string
	// Account is the ID of its author's account.
	Account string
}

// Request is one call the fake received.
type Request struct {
	Method string
	Path   string
	Query  url.Values
	// Email is who the call said it was, by basic auth.
	Email string
}

// Server is a fake Jira Cloud.
type Server struct {
	srv     *httptest.Server
	cloudID string

	mu       sync.Mutex
	accounts map[string]*account // by e-mail address
	projects map[string]*project
	items    []*item // by id - 10000
	comments int
	tick     int
	down     bool
	throttle int
	pageCap  int
	log      []Request
}

type account struct {
	id, name, token string
	scopes          []string
	forbidden       bool
}

type project struct {
	key      string
	next     int
	workflow Workflow
}

type item struct {
	id          int
	key         string
	oldKeys     []string
	project     string
	title       string
	description json.RawMessage
	labels      []string
	status      string
	resolution  string
	reporter    Maker
	creator     Maker
	created     time.Time
	updated     time.Time
	comments    []*comment
}

type comment struct {
	id      int
	author  string // account ID
	by      string // display name
	body    json.RawMessage
	created time.Time
}

// epoch is when the fake's first change happens; each next one a second
// later.
var epoch = time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)

// New starts a fake Jira Cloud, closed when t ends.
func New(t testing.TB) *Server {
	s := Start()
	t.Cleanup(s.Close)
	return s
}

// Start starts a fake Jira Cloud the caller closes with Close.
func Start() *Server {
	s := &Server{accounts: map[string]*account{}, projects: map[string]*project{},
		cloudID: "11111111-2222-4333-8444-" + strings.ToLower(rand.Text()[:12])}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Close stops the server.
func (s *Server) Close() { s.srv.Close() }

// URL is the gateway's address: what the adapter is given as its address.
func (s *Server) URL() string { return s.srv.URL }

// CloudID is the site's ID at the fake Atlassian.
func (s *Server) CloudID() string { return s.cloudID }

// Account makes an account with a token that has the three scopes the
// adapter needs, and returns its credentials.
func (s *Server) Account(name string) Credentials {
	return s.Restricted(name, ScopeRead, ScopeWrite, ScopeUser)
}

// Restricted makes an account with a token that has only scopes.
func (s *Server) Restricted(name string, scopes ...string) Credentials {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := &account{id: accountID(name), name: name, token: rand.Text(), scopes: scopes}
	email := strings.ToLower(strings.ReplaceAll(name, " ", ".")) + "@jira.test"
	s.accounts[email] = a
	return Credentials{Email: email, Token: a.token}
}

// Forbid makes every call of an account answer 403.
func (s *Server) Forbid(c Credentials) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts[c.Email].forbidden = true
}

func accountID(name string) string {
	return "acct-" + strings.ToLower(strings.ReplaceAll(name, " ", "-"))
}

// Project makes a project with the classic workflow; making one that exists
// changes nothing.
func (s *Server) Project(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.projects[key] == nil {
		s.projects[key] = &project{key: key, next: 1, workflow: Classic()}
	}
}

// SetWorkflow gives a project a workflow of its own.
func (s *Server) SetWorkflow(key string, w Workflow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projects[key].workflow = w
}

// Down makes the site answer 503 from now on; Down(false) brings it back.
func (s *Server) Down(down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down = down
}

// Throttle makes the next n calls answer 429.
func (s *Server) Throttle(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.throttle = n
}

// PageSize caps how many issues or comments a page holds, whatever the
// call asks for; 0 lifts the cap.
func (s *Server) PageSize(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pageCap = n
}

// Requests are the calls the fake received, in order, the refused included.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.log)
}

// File files an item and returns its number in its project.
func (s *Server) File(it Item) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projects[it.Project]
	if p == nil {
		panic("jiratest: no project " + it.Project)
	}
	n := p.next
	p.next++
	now := s.now()
	desc := it.ADF
	if desc == nil && it.Description != "" {
		desc = document(it.Description)
	}
	s.items = append(s.items, &item{id: 10000 + len(s.items), key: fmt.Sprintf("%s-%d", p.key, n), project: p.key,
		title: it.Title, description: desc, labels: slices.Clone(it.Labels), status: StatusToDo,
		reporter: it.Reporter, creator: it.Creator, created: now, updated: now})
	return n
}

// now is the time of the next change.
func (s *Server) now() time.Time {
	s.tick++
	return epoch.Add(time.Duration(s.tick) * time.Second)
}

// find is the item of project's number n.
func (s *Server) find(project string, n int) *item {
	key := fmt.Sprintf("%s-%d", project, n)
	for _, it := range s.items {
		if it.key == key {
			return it
		}
	}
	panic("jiratest: no item " + key)
}

// change applies change to item n of project, and dates it.
func (s *Server) change(project string, n int, change func(*item)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it := s.find(project, n)
	change(it)
	it.updated = s.now()
}

// Edit changes the description of item n of project, as its author does.
func (s *Server) Edit(project string, n int, body string) {
	s.change(project, n, func(it *item) { it.description = document(body) })
}

// Finish closes item n of project as a person does, with the resolution Done.
func (s *Server) Finish(project string, n int) {
	s.change(project, n, func(it *item) { it.status, it.resolution = StatusDone, ResolutionDone })
}

// Unlabel takes label off item n of project.
func (s *Server) Unlabel(project string, n int, label string) {
	s.change(project, n, func(it *item) { it.labels = slices.DeleteFunc(it.labels, func(l string) bool { return l == label }) })
}

// Say comments body on item n of project as the person named author, a
// paragraph to a line.
func (s *Server) Say(project string, n int, author, body string) {
	s.change(project, n, func(it *item) {
		s.comments++
		it.comments = append(it.comments, &comment{id: 10000 + s.comments, author: accountID(author), by: author,
			body: document(body), created: s.now()})
	})
}

// Move moves item n of project to another project, which gives it a key and
// a number of its own there, and returns them. The old key goes on
// answering with the item, as Jira's does.
func (s *Server) Move(project string, n int, to string) (string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, target := s.find(project, n), s.projects[to]
	number := target.next
	target.next++
	it.oldKeys = append(it.oldKeys, it.key)
	it.project, it.key = to, fmt.Sprintf("%s-%d", to, number)
	it.updated = s.now()
	return it.key, number
}

// Show is item n of project as its page shows it.
func (s *Server) Show(project string, n int) Shown {
	s.mu.Lock()
	defer s.mu.Unlock()
	it := s.find(project, n)
	out := Shown{Key: it.key, Title: it.title, Description: plain(it.description), Labels: slices.Clone(it.labels),
		Status: it.status, Category: s.projects[it.project].workflow.category(it.status), Resolution: it.resolution,
		Reporter: it.reporter.Name, Created: it.created}
	for _, c := range it.comments {
		out.Comments = append(out.Comments, ShownComment{Body: plain(c.body), By: c.by, Account: c.author})
	}
	return out
}

// document is text as Atlassian's document format: a paragraph to a line.
func document(text string) json.RawMessage {
	var content []map[string]any
	for line := range strings.SplitSeq(text, "\n") {
		p := map[string]any{keyType: "paragraph"}
		if line != "" {
			p["content"] = []map[string]any{{keyType: "text", "text": line}}
		}
		content = append(content, p)
	}
	b, _ := json.Marshal(map[string]any{"version": 1, keyType: "doc", "content": content})
	return b
}

// plain is a document of paragraphs as text, one paragraph to a line. It is
// the fake's own reading, which the adapter's flattening is not tested with.
func plain(doc json.RawMessage) string {
	var d struct {
		Content []struct {
			Content []struct{ Text string }
		}
	}
	if json.Unmarshal(doc, &d) != nil {
		return ""
	}
	lines := make([]string, 0, len(d.Content))
	for _, p := range d.Content {
		var b strings.Builder
		for _, t := range p.Content {
			b.WriteString(t.Text)
		}
		lines = append(lines, b.String())
	}
	return strings.Join(lines, "\n")
}

// stamp is a time as Jira writes it, with an offset that has no colon.
func stamp(t time.Time) string { return t.Format("2006-01-02T15:04:05.000-0700") }

// reply writes a JSON answer.
func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

// refuse writes an error as the REST API words it.
func refuse(w http.ResponseWriter, status int, message string) {
	reply(w, status, map[string]any{keyErrorMessages: []string{message}, keyErrors: map[string]string{}})
}

// scopeFor is the scope a call needs.
func scopeFor(method, path string) string {
	switch {
	case path == "/myself":
		return ScopeUser
	case method == http.MethodGet:
		return ScopeRead
	}
	return ScopeWrite
}

const restPath = "/rest/api/3"

// The names of the answers' fields that more than one place spells.
const (
	keyType          = "type"
	keyErrorMessages = "errorMessages"
	keyCode          = "code"
	keyCreated       = "created"
	keyKey           = "key"
	keyResolution    = "resolution"
	keyErrors        = "errors"
	keyMessage       = "message"
	keyName          = "name"
)

// serve is the fake: the gateway's prefix, basic auth, the account's scopes,
// and then the REST API.
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	email, token, _ := r.BasicAuth()
	s.log = append(s.log, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Email: email})
	prefix := "/ex/jira/" + s.cloudID + restPath
	path, ok := strings.CutPrefix(r.URL.Path, prefix)
	if !ok {
		// The site's own address, or another site's: a scoped token has no
		// business there.
		reply(w, http.StatusNotFound, map[string]any{keyCode: 404, keyMessage: "no such resource"})
		return
	}
	if s.throttle > 0 {
		s.throttle--
		w.Header().Set("Retry-After", "1")
		refuse(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}
	if s.down {
		refuse(w, http.StatusServiceUnavailable, "No server is currently available")
		return
	}
	a := s.accounts[email]
	if a == nil || a.token != token {
		reply(w, http.StatusUnauthorized, map[string]any{keyCode: 401, keyMessage: "Unauthorized"})
		return
	}
	if !slices.Contains(a.scopes, scopeFor(r.Method, path)) {
		// From memory of the gateway's words; the reference says only that
		// the credentials are incorrect.
		reply(w, http.StatusUnauthorized, map[string]any{keyCode: 401, keyMessage: "Unauthorized; scope does not match"})
		return
	}
	if a.forbidden {
		refuse(w, http.StatusForbidden, "You do not have permission to do that.")
		return
	}
	s.route(w, r, a, path)
}

var (
	issuePath = regexp.MustCompile(`^/issue/([^/]+)(/comment|/transitions)?$`)
	// jqlPattern is the one search the adapter sends.
	jqlPattern = regexp.MustCompile(`^project = "((?:[^"\\]|\\.)*)" AND labels = "((?:[^"\\]|\\.)*)" ` +
		`AND statusCategory != Done ORDER BY created ASC$`)
	unescape = strings.NewReplacer(`\"`, `"`, `\\`, `\`)
)

func (s *Server) route(w http.ResponseWriter, r *http.Request, a *account, path string) {
	switch m := issuePath.FindStringSubmatch(path); {
	case path == "/myself" && r.Method == http.MethodGet:
		reply(w, http.StatusOK, person(a.id, a.name, ""))
	case path == "/search/jql" && r.Method == http.MethodGet:
		s.search(w, r)
	case m != nil:
		it := s.lookup(m[1])
		if it == nil {
			refuse(w, http.StatusNotFound, "Issue does not exist or you do not have permission to see it.")
			return
		}
		switch {
		case m[2] == "" && r.Method == http.MethodGet:
			reply(w, http.StatusOK, s.issueJSON(it, strings.Split(r.URL.Query().Get("fields"), ",")))
		case m[2] == "/comment" && r.Method == http.MethodGet:
			s.listComments(w, r, it)
		case m[2] == "/comment" && r.Method == http.MethodPost:
			s.addComment(w, r, a, it)
		case m[2] == "/transitions" && r.Method == http.MethodGet:
			s.transitions(w, r, it)
		case m[2] == "/transitions" && r.Method == http.MethodPost:
			s.transit(w, r, it)
		default:
			refuse(w, http.StatusMethodNotAllowed, "no such method")
		}
	default:
		reply(w, http.StatusNotFound, map[string]any{keyCode: 404, keyMessage: "no such resource"})
	}
}

// lookup is the item an ID or a key names, a key that was the item's once
// included: Jira answers a call for the old key of a moved item with the
// item as it is now.
func (s *Server) lookup(idOrKey string) *item {
	for _, it := range s.items {
		if strconv.Itoa(it.id) == idOrKey || strings.EqualFold(it.key, idOrKey) ||
			slices.ContainsFunc(it.oldKeys, func(k string) bool { return strings.EqualFold(k, idOrKey) }) {
			return it
		}
	}
	return nil
}

// person is an account as an answer shows it.
func person(id, name, kind string) map[string]any {
	if kind == "" {
		kind = "atlassian"
	}
	return map[string]any{"accountId": id, "accountType": kind, "active": true, "displayName": name}
}

func maker(m Maker) any {
	if m.Name == "" {
		return nil
	}
	return person(accountID(m.Name), m.Name, m.Type)
}

// issueJSON is the item with the fields asked for, and its ID and key.
func (s *Server) issueJSON(it *item, fields []string) map[string]any {
	p := s.projects[it.project]
	all := map[string]func() any{
		"summary": func() any { return it.title },
		"description": func() any {
			if it.description == nil {
				return nil
			}
			return it.description
		},
		"reporter": func() any { return maker(it.reporter) },
		"creator":  func() any { return maker(it.creator) },
		keyCreated: func() any { return stamp(it.created) },
		"updated":  func() any { return stamp(it.updated) },
		"labels":   func() any { return it.labels },
		"project":  func() any { return map[string]any{keyKey: p.key, keyName: p.key + " project"} },
		"status": func() any {
			return map[string]any{keyName: it.status, "statusCategory": map[string]any{keyKey: p.workflow.category(it.status)}}
		},
		keyResolution: func() any {
			if it.resolution == "" {
				return nil
			}
			return map[string]any{keyName: it.resolution}
		},
	}
	out := map[string]any{}
	for _, f := range fields {
		if get, ok := all[strings.TrimSpace(f)]; ok {
			out[strings.TrimSpace(f)] = get()
		}
	}
	return map[string]any{"id": strconv.Itoa(it.id), "key": it.key, "fields": out}
}

// capped is how many items a page holds, asked for ask.
func (s *Server) capped(ask string) int {
	n, err := strconv.Atoi(ask)
	if err != nil || n < 1 {
		n = 50
	}
	if s.pageCap > 0 && n > s.pageCap {
		n = s.pageCap
	}
	return n
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	m := jqlPattern.FindStringSubmatch(q.Get("jql"))
	if m == nil {
		refuse(w, http.StatusBadRequest, "Error in the JQL Query: the fake understands one search only.")
		return
	}
	key, label := unescape.Replace(m[1]), unescape.Replace(m[2])
	p := s.projects[key]
	if p == nil {
		refuse(w, http.StatusBadRequest, fmt.Sprintf("The value '%s' does not exist for the field 'project'.", key))
		return
	}
	var found []*item
	for _, it := range s.items {
		if it.project == key && slices.Contains(it.labels, label) && p.workflow.category(it.status) != CategoryDone {
			found = append(found, it)
		}
	}
	slices.SortStableFunc(found, func(a, b *item) int {
		if c := a.created.Compare(b.created); c != 0 {
			return c
		}
		return a.id - b.id
	})
	start := 0
	if tok := q.Get("nextPageToken"); tok != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(tok, "page-"))
		if err != nil || n < 0 || n > len(found) {
			refuse(w, http.StatusBadRequest, "The page token is not valid.")
			return
		}
		start = n
	}
	end := min(start+s.capped(q.Get("maxResults")), len(found))
	fields := strings.Split(q.Get("fields"), ",")
	issues := make([]any, 0, end-start)
	for _, it := range found[start:end] {
		issues = append(issues, s.issueJSON(it, fields))
	}
	out := map[string]any{"issues": issues, "isLast": end == len(found)}
	if end < len(found) {
		out["nextPageToken"] = fmt.Sprintf("page-%d", end)
	}
	reply(w, http.StatusOK, out)
}

func (s *Server) commentJSON(c *comment) map[string]any {
	return map[string]any{"id": strconv.Itoa(c.id), "author": person(c.author, c.by, ""), "body": c.body,
		keyCreated: stamp(c.created)}
}

func (s *Server) listComments(w http.ResponseWriter, r *http.Request, it *item) {
	q := r.URL.Query()
	if o := q.Get("orderBy"); o != "" && o != "created" && o != "+created" && o != "-created" {
		refuse(w, http.StatusBadRequest, "orderBy must be created.")
		return
	}
	// Jira's order is oldest first, or newest first with a minus sign.
	all := slices.Clone(it.comments)
	if q.Get("orderBy") == "-created" {
		slices.Reverse(all)
	}
	start, _ := strconv.Atoi(q.Get("startAt"))
	start = max(0, min(start, len(all)))
	end := min(start+s.capped(q.Get("maxResults")), len(all))
	page := make([]any, 0, end-start)
	for _, c := range all[start:end] {
		page = append(page, s.commentJSON(c))
	}
	reply(w, http.StatusOK, map[string]any{"startAt": start, "maxResults": s.capped(q.Get("maxResults")),
		"total": len(it.comments), "comments": page})
}

func (s *Server) addComment(w http.ResponseWriter, r *http.Request, a *account, it *item) {
	var in struct {
		Body json.RawMessage `json:"body"`
	}
	data, _ := io.ReadAll(r.Body)
	var doc struct{ Type string }
	if json.Unmarshal(data, &in) != nil || json.Unmarshal(in.Body, &doc) != nil || doc.Type != "doc" {
		// Version 3 takes Atlassian's document format and nothing else.
		refuse(w, http.StatusBadRequest, "Comment body is not valid!")
		return
	}
	s.comments++
	c := &comment{id: 10000 + s.comments, author: a.id, by: a.name, body: in.Body, created: s.now()}
	it.comments = append(it.comments, c)
	it.updated = c.created
	reply(w, http.StatusCreated, s.commentJSON(c))
}

func (s *Server) transitions(w http.ResponseWriter, r *http.Request, it *item) {
	// Jira leaves a transition's fields out unless the call expands them.
	expanded := slices.Contains(strings.Split(r.URL.Query().Get("expand"), ","), "transitions.fields")
	wf := s.projects[it.project].workflow
	offered := wf.offered(it.status)
	out := make([]any, 0, len(offered))
	for _, t := range offered {
		to := map[string]any{keyName: t.To, "statusCategory": map[string]any{"key": wf.category(t.To)}}
		fields := map[string]any{}
		if t.Resolution != NoScreen {
			values := make([]any, 0, len(wf.Resolutions))
			for _, name := range wf.Resolutions {
				values = append(values, map[string]any{keyName: name})
			}
			fields[keyResolution] = map[string]any{keyName: "Resolution", keyKey: keyResolution,
				"required": t.Resolution == RequiresResolution, "hasDefaultValue": false, "allowedValues": values}
		}
		for _, name := range t.Asks {
			fields[strings.ToLower(name)] = map[string]any{keyName: name, "required": true, "hasDefaultValue": false}
		}
		entry := map[string]any{"id": t.ID, keyName: t.Name, "to": to, "hasScreen": len(fields) > 0,
			"isAvailable": true}
		if expanded {
			entry["fields"] = fields
		}
		out = append(out, entry)
	}
	reply(w, http.StatusOK, map[string]any{"transitions": out})
}

func (s *Server) transit(w http.ResponseWriter, r *http.Request, it *item) {
	var in struct {
		Transition struct{ ID string } `json:"transition"`
		Fields     map[string]struct {
			Name string `json:"name"`
		} `json:"fields"`
	}
	if data, _ := io.ReadAll(r.Body); json.Unmarshal(data, &in) != nil || in.Transition.ID == "" {
		refuse(w, http.StatusBadRequest, "No transition specified.")
		return
	}
	wf := s.projects[it.project].workflow
	i := slices.IndexFunc(wf.offered(it.status), func(t Transition) bool { return t.ID == in.Transition.ID })
	if i < 0 {
		refuse(w, http.StatusBadRequest, "Transition id '"+in.Transition.ID+"' is not valid for this issue.")
		return
	}
	t := wf.offered(it.status)[i]
	if t.Refuses != "" {
		refuse(w, http.StatusBadRequest, t.Refuses)
		return
	}
	for name := range in.Fields {
		if name != keyResolution || t.Resolution == NoScreen {
			reply(w, http.StatusBadRequest, map[string]any{keyErrorMessages: []string{},
				keyErrors: map[string]string{name: "Field '" + name + "' cannot be set. It is not on the appropriate screen, or unknown."}})
			return
		}
	}
	res, sent := in.Fields[keyResolution]
	switch {
	case sent && !slices.Contains(wf.Resolutions, res.Name):
		reply(w, http.StatusBadRequest, map[string]any{keyErrorMessages: []string{},
			keyErrors: map[string]string{keyResolution: "Resolution name '" + res.Name + "' is not valid"}})
		return
	case !sent && t.Resolution == RequiresResolution:
		reply(w, http.StatusBadRequest, map[string]any{keyErrorMessages: []string{},
			keyErrors: map[string]string{keyResolution: "Resolution is required."}})
		return
	case len(t.Asks) > 0:
		refuse(w, http.StatusBadRequest, "The transition needs "+strings.Join(t.Asks, ", ")+".")
		return
	}
	it.status, it.resolution = t.To, res.Name
	it.updated = s.now()
	w.WriteHeader(http.StatusNoContent)
}
