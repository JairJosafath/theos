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

// Package lineartest is a fake Linear over httptest: its GraphQL endpoint
// with the teams, workflow states, items, labels, comments and API keys the
// linear adapter's calls touch (docs/plans/requests-and-chat.md, decision
// 21). It is built from Linear's published schema and reference, read on
// 2026-11-13: the state types, the issue filter, the cursor paging, the
// raw API key in the Authorization header, and an item that still answers
// to the key it had before it moved to another team.
//
// The fake answers by the operation's name and variables: it parses no
// GraphQL, so it can't refuse a query Linear's validator would. What it
// holds the adapter to is the filter, which it reads as the IssueFilter
// Linear takes and refuses a field of it that Linear's schema lacks, the page
// size Linear allows, the key sent as it is, and the order Linear lists in
// (the newest first). The error codes it answers with are from memory of
// Linear's answers, the reference naming none.
package lineartest

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Types of a workflow state, as Linear's schema lists them.
const (
	Triage    = "triage"
	Backlog   = "backlog"
	Unstarted = "unstarted"
	Started   = "started"
	Completed = "completed"
	Canceled  = "canceled"
	Duplicate = "duplicate"
)

// State is a status of a team's workflow.
type State struct {
	Name string
	// Type is one of the types above.
	Type string
	// Position orders the states of one type, the lowest first.
	Position float64
}

// DefaultStates are what a team has unless it is given others.
func DefaultStates() []State {
	return []State{
		{"Triage", Triage, 0}, {"Backlog", Backlog, 1}, {"Todo", Unstarted, 2},
		{"In Progress", Started, 3}, {"Done", Completed, 4}, {"Canceled", Canceled, 5},
		{"Duplicate", Duplicate, 6},
	}
}

// Maker is who or what made an item or a comment: the display name of a
// user, else the name of a bot, else the display name of an external user
// (made by an integration for someone who has no Linear account). All empty
// is an item with no creator at all, as one whose maker's account is gone.
type Maker struct {
	User, Bot, External string
}

// Item is an item to file.
type Item struct {
	Team, Title, Description string
	Labels                   []string
	By                       Maker
}

// Said is a comment as the fake shows it.
type Said struct {
	Body string
	// By is the display name of the user who wrote it.
	By string
}

// Shown is an item as the fake shows it.
type Shown struct {
	Team                string
	Number              int
	Title, Description  string
	Labels              []string
	Created             time.Time
	State, StateType    string
	Comments            []Said // oldest first
	Identifier          string
	PreviousIdentifiers []string
	Updated             time.Time
	CreatedBy           Maker
}

// Request is a call the fake received.
type Request struct {
	Operation     string
	Variables     map[string]any
	Authorization string
}

// maxPage is the most nodes a page may ask for, as Linear's schema says.
const maxPage = 250

// The keys of Linear's answers the fake writes more than once.
const (
	keyCode      = "code"
	keyName      = "name"
	keyNodes     = "nodes"
	keyType      = "type"
	keyUserError = "userError"
)

// epoch is when the fake's clock starts; it ticks a second at each change.
var epoch = time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)

// Server is a fake Linear.
type Server struct {
	srv *httptest.Server

	mu       sync.Mutex
	users    map[string]*user // by ID
	keys     map[string]key   // by API key
	teams    map[string]*team // by key
	issues   []*issue
	ticks    int
	ids      int
	down     bool
	requests []Request
}

type user struct{ id, display string }

type key struct {
	user   string
	issues bool
}

type team struct {
	id, key string
	states  []workflow
	last    int // the last number given
}

type workflow struct {
	id string
	State
}

type issue struct {
	id, team           string
	number             int
	title, description string
	labels             []string
	by                 Maker
	created, updated   time.Time
	state              string
	previous           []string
	comments           []*comment
}

type comment struct {
	id, body string
	created  time.Time
	user     string // ID
}

// New starts a fake Linear, closed with t.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{users: map[string]*user{}, keys: map[string]key{}, teams: map[string]*team{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the GraphQL address.
func (s *Server) URL() string { return s.srv.URL + "/graphql" }

// tick is the time of the next change.
func (s *Server) tick() time.Time {
	s.ticks++
	return epoch.Add(time.Duration(s.ticks) * time.Second)
}

func (s *Server) id() string {
	s.ids++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", s.ids)
}

// userID returns the ID of the user whose display name is display, made
// when there is none.
func (s *Server) userID(display string) string {
	for _, u := range s.users {
		if u.display == display {
			return u.id
		}
	}
	u := &user{id: s.id(), display: display}
	s.users[u.id] = u
	return u.id
}

// Team makes team key with states, DefaultStates when there are none.
func (s *Server) Team(key string, states ...State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(states) == 0 {
		states = DefaultStates()
	}
	t := &team{id: s.id(), key: key}
	for _, st := range states {
		t.states = append(t.states, workflow{id: s.id(), State: st})
	}
	s.teams[key] = t
}

// Skip makes number the last one team has given, so that its next item has
// the number after it: Linear's numbers are not consecutive.
func (s *Server) Skip(team string, number int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.teams[team].last = number
}

// Account makes a user with that display name, and returns an API key of
// theirs that may read and write the issues.
func (s *Server) Account(display string) string {
	return s.account(display, true)
}

// Restricted is Account's for a key that may not use the issues, as one
// limited to teams that aren't this one.
func (s *Server) Restricted(display string) string {
	return s.account(display, false)
}

func (s *Server) account(display string, issues bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	token := rand.Text()
	s.keys[token] = key{user: s.userID(display), issues: issues}
	return token
}

// File files an item and returns its number in its team.
func (s *Server) File(it Item) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.teams[it.Team]
	if t == nil {
		panic("lineartest: no team " + it.Team)
	}
	return s.fileIn(t, it).number
}

func (s *Server) fileIn(t *team, it Item) *issue {
	t.last++
	now := s.tick()
	if it.By.User != "" {
		s.userID(it.By.User)
	}
	i := &issue{id: s.id(), team: t.key, number: t.last, title: it.Title, description: it.Description,
		labels: slices.Clone(it.Labels), by: it.By, created: now, updated: now, state: t.open().id}
	s.issues = append(s.issues, i)
	return i
}

// open is the state a new item starts in: the first that isn't closed.
func (t *team) open() workflow {
	var first *workflow
	for i := range t.states {
		st := &t.states[i]
		if closed(st.Type) {
			continue
		}
		if first == nil || st.Position < first.Position {
			first = st
		}
	}
	return *first
}

func closed(kind string) bool {
	return kind == Completed || kind == Canceled || kind == Duplicate
}

// find is item number of team.
func (s *Server) find(team string, number int) *issue {
	for _, i := range s.issues {
		if i.team == team && i.number == number {
			return i
		}
	}
	panic(fmt.Sprintf("lineartest: no item %s-%d", team, number))
}

// change does what a person does to an item on Linear's pages.
func (s *Server) change(team string, number int, do func(*issue)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.find(team, number)
	do(i)
	i.updated = s.tick()
}

// Label puts label on the item.
func (s *Server) Label(team string, number int, label string) {
	s.change(team, number, func(i *issue) {
		if !slices.Contains(i.labels, label) {
			i.labels = append(i.labels, label)
		}
	})
}

// Unlabel takes label off the item.
func (s *Server) Unlabel(team string, number int, label string) {
	s.change(team, number, func(i *issue) { i.labels = slices.DeleteFunc(i.labels, func(l string) bool { return l == label }) })
}

// Edit replaces the item's description, as its author does.
func (s *Server) Edit(team string, number int, description string) {
	s.change(team, number, func(i *issue) { i.description = description })
}

// Close moves the item to the first state of its team of type completed, as
// a person does.
func (s *Server) Close(team string, number int) {
	s.change(team, number, func(i *issue) { i.state = s.teams[i.team].first(Completed).id })
}

// MoveTo moves the item to the state named name, as a person does.
func (s *Server) MoveTo(team string, number int, name string) {
	s.change(team, number, func(i *issue) {
		for _, st := range s.teams[i.team].states {
			if st.Name == name {
				i.state = st.id
				return
			}
		}
		panic("lineartest: no state " + name)
	})
}

func (t *team) first(kind string) workflow {
	var first *workflow
	for i := range t.states {
		if st := &t.states[i]; st.Type == kind && (first == nil || st.Position < first.Position) {
			first = st
		}
	}
	if first == nil {
		panic("lineartest: no state of type " + kind + " in team " + t.key)
	}
	return *first
}

// Move moves the item to team to, where it has a number of its own and
// another key. Linear keeps the key it had and goes on answering to it. It
// returns the new number.
func (s *Server) Move(team string, number int, to string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, t := s.find(team, number), s.teams[to]
	i.previous = append(i.previous, fmt.Sprintf("%s-%d", i.team, i.number))
	t.last++
	i.team, i.number, i.state = to, t.last, t.open().id
	i.updated = s.tick()
	return i.number
}

// Say comments body on the item as the user with that display name.
func (s *Server) Say(team string, number int, author, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.say(s.find(team, number), s.userID(author), body)
}

func (s *Server) say(i *issue, userID, body string) *comment {
	c := &comment{id: s.id(), body: body, created: s.tick(), user: userID}
	i.comments = append(i.comments, c)
	return c
}

// Down makes Linear answer nothing but an error of its own from now on, as
// an outage does; Down(false) brings it back.
func (s *Server) Down(down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down = down
}

// Show is the item as Linear shows it.
func (s *Server) Show(team string, number int) Shown {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.find(team, number)
	st := s.state(i)
	shown := Shown{Team: i.team, Number: i.number, Title: i.title, Description: i.description,
		Labels: slices.Clone(i.labels), Created: i.created, Updated: i.updated, State: st.Name, StateType: st.Type,
		Identifier: fmt.Sprintf("%s-%d", i.team, i.number), PreviousIdentifiers: slices.Clone(i.previous),
		CreatedBy: i.by}
	for _, c := range i.comments {
		shown.Comments = append(shown.Comments, Said{Body: c.body, By: s.users[c.user].display})
	}
	return shown
}

func (s *Server) state(i *issue) workflow {
	for _, st := range s.teams[i.team].states {
		if st.id == i.state {
			return st
		}
	}
	panic("lineartest: an item in no state")
}

// Requests are the calls received, oldest first.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// Calls counts the calls of the operation named op.
func (s *Server) Calls(op string) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Operation == op {
			n++
		}
	}
	return n
}

type gqlError struct {
	Message    string         `json:"message"`
	Extensions map[string]any `json:"extensions"`
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OperationName string         `json:"operationName"`
		Variables     map[string]any `json:"variables"`
	}
	if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, "POST a GraphQL request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{Operation: req.OperationName, Variables: req.Variables,
		Authorization: r.Header.Get("Authorization")})
	if s.down {
		http.Error(w, "<html>Service Unavailable</html>", http.StatusServiceUnavailable)
		return
	}
	// A key is sent as it is: "Bearer <key>" is an OAuth token's form.
	k, ok := s.keys[r.Header.Get("Authorization")]
	if !ok {
		reply(w, http.StatusBadRequest, nil, gqlError{"Authentication required, not authenticated",
			map[string]any{keyType: "authentication error", keyCode: "AUTHENTICATION_ERROR", keyUserError: true}})
		return
	}
	if !k.issues {
		reply(w, http.StatusOK, nil, gqlError{"You do not have access to this resource",
			map[string]any{keyType: "forbidden", keyCode: "FORBIDDEN", keyUserError: true}})
		return
	}
	var data map[string]any
	var err *gqlError
	switch req.OperationName {
	case "TheosOpenIssues":
		data, err = s.openIssues(req.Variables, false)
	case "TheosOpenIssueStamps":
		data, err = s.openIssues(req.Variables, true)
	case "TheosIssue", "TheosIssueOpen":
		data, err = s.issueOf(req.Variables, false)
	case "TheosIssueComments":
		data, err = s.comments(req.Variables, k)
	case "TheosCloseTarget":
		data, err = s.issueOf(req.Variables, true)
	case "TheosComment":
		data, err = s.commentCreate(req.Variables, k)
	case "TheosMoveIssue":
		data, err = s.issueUpdate(req.Variables)
	default:
		err = &gqlError{Message: "unknown operation " + req.OperationName,
			Extensions: map[string]any{keyCode: "GRAPHQL_VALIDATION_FAILED"}}
	}
	if err != nil {
		status := http.StatusOK
		if err.Extensions[keyCode] == "GRAPHQL_VALIDATION_FAILED" {
			status = http.StatusBadRequest
		}
		reply(w, status, nil, *err)
		return
	}
	reply(w, http.StatusOK, data)
}

func reply(w http.ResponseWriter, status int, data map[string]any, errs ...gqlError) {
	body := map[string]any{"data": data}
	if len(errs) > 0 {
		body["errors"] = errs
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func invalid(format string, args ...any) *gqlError {
	return &gqlError{Message: fmt.Sprintf(format, args...),
		Extensions: map[string]any{keyType: "invalid input", keyCode: "INVALID_INPUT", keyUserError: true}}
}

func notFound() *gqlError {
	const entity = "Issue"
	return &gqlError{Message: "Entity not found: " + entity + " - Could not find referenced " + entity + ".",
		Extensions: map[string]any{keyType: "invalid input", keyCode: "INVALID_INPUT", keyUserError: true,
			"userPresentableMessage": "Could not find referenced " + entity + "."}}
}

// lookup is the item the key or ID id names: by its ID, by its key, or by
// a key it had before it moved to another team.
func (s *Server) lookup(id string) *issue {
	for _, i := range s.issues {
		if i.id == id || id == fmt.Sprintf("%s-%d", i.team, i.number) || slices.Contains(i.previous, id) {
			return i
		}
	}
	return nil
}

// stringVar is variable name, which Linear types String!.
func stringVar(vars map[string]any, name string) (string, *gqlError) {
	v, ok := vars[name].(string)
	if !ok {
		return "", invalid("Variable \"$%s\" of required type \"String!\" was not provided.", name)
	}
	return v, nil
}

// page returns the window of n items a connection's arguments ask for.
func page(vars map[string]any, n int) (from, to int, err *gqlError) {
	first, ok := vars["first"].(float64)
	if !ok || first < 1 || first > maxPage {
		return 0, 0, invalid("Variable \"$first\" must be between 1 and %d.", maxPage)
	}
	if after, ok := vars["after"].(string); ok {
		i, e := strconv.Atoi(strings.TrimPrefix(after, "cursor:"))
		if e != nil || !strings.HasPrefix(after, "cursor:") || i < 0 {
			return 0, 0, invalid("Invalid cursor %q.", after)
		}
		from = i
	}
	return min(from, n), min(from+int(first), n), nil
}

func pageInfo(to, n int) map[string]any {
	return map[string]any{"hasNextPage": to < n, "endCursor": "cursor:" + strconv.Itoa(to)}
}

// stamp is a time as Linear writes it.
func stamp(t time.Time) string { return t.Format("2006-01-02T15:04:05.000Z") }

func (s *Server) userJSON(id string) any {
	u := s.users[id]
	if u == nil {
		return nil
	}
	return map[string]any{"id": u.id, "displayName": u.display, keyName: "Full name of " + u.display}
}

func (s *Server) teamJSON(key string) map[string]any {
	t := s.teams[key]
	nodes := make([]map[string]any, 0, len(t.states))
	for _, st := range t.states {
		nodes = append(nodes, map[string]any{"id": st.id, keyName: st.Name, keyType: st.Type, "position": st.Position})
	}
	return map[string]any{"id": t.id, "key": t.key, "states": map[string]any{keyNodes: nodes}}
}

// issueJSON is every field of the item that the adapter may ask for.
func (s *Server) issueJSON(i *issue) map[string]any {
	st := s.state(i)
	labels := make([]map[string]any, 0, len(i.labels))
	for _, l := range i.labels {
		labels = append(labels, map[string]any{keyName: l})
	}
	var creator, bot, external any
	switch {
	case i.by.User != "":
		creator = s.userJSON(s.userID(i.by.User))
	case i.by.Bot != "":
		bot = map[string]any{keyName: i.by.Bot}
	case i.by.External != "":
		external = map[string]any{keyName: "Full name of " + i.by.External, "displayName": i.by.External}
	}
	return map[string]any{
		"id": i.id, "number": i.number, "identifier": fmt.Sprintf("%s-%d", i.team, i.number),
		"title": i.title, "description": nilIfEmpty(i.description),
		"url":       fmt.Sprintf("https://linear.test/acme/issue/%s-%d", i.team, i.number),
		"createdAt": stamp(i.created), "updatedAt": stamp(i.updated),
		"creator": creator, "botActor": bot, "externalUserCreator": external,
		"team":                s.teamJSON(i.team),
		"state":               map[string]any{"id": st.id, keyName: st.Name, keyType: st.Type},
		"previousIdentifiers": i.previous,
		"labels":              map[string]any{keyNodes: labels},
	}
}

// nilIfEmpty is the null Linear gives for a description that isn't stored.
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// openIssues is the issues of the team the filter names, newest first, with
// only their IDs and update times when stamps.
func (s *Server) openIssues(vars map[string]any, stamps bool) (map[string]any, *gqlError) {
	teamKey, err := stringVar(vars, "team")
	if err != nil {
		return nil, err
	}
	match, err := filterOf(vars["filter"])
	if err != nil {
		return nil, err
	}
	teams := []map[string]any{}
	if t := s.teams[teamKey]; t != nil {
		teams = append(teams, map[string]any{"id": t.id})
	}
	var found []*issue
	for _, i := range s.issues {
		if match(i, s.state(i)) {
			found = append(found, i)
		}
	}
	slices.SortFunc(found, func(a, b *issue) int {
		if c := b.created.Compare(a.created); c != 0 {
			return c
		}
		return b.number - a.number
	})
	from, to, err := page(vars, len(found))
	if err != nil {
		return nil, err
	}
	nodes := []map[string]any{}
	for _, i := range found[from:to] {
		node := s.issueJSON(i)
		if stamps {
			node = map[string]any{"id": i.id, "updatedAt": node["updatedAt"]}
		}
		nodes = append(nodes, node)
	}
	return map[string]any{"teams": map[string]any{keyNodes: teams},
		"issues": map[string]any{keyNodes: nodes, "pageInfo": pageInfo(to, len(found))}}, nil
}

// filterOf reads an IssueFilter of the three fields the adapter uses, as
// Linear does, and refuses what Linear's schema doesn't have.
func filterOf(raw any) (func(*issue, workflow) bool, *gqlError) {
	f, ok := raw.(map[string]any)
	if !ok {
		return nil, invalid("Variable \"$filter\" of required type \"IssueFilter!\" was not provided.")
	}
	var matches []func(*issue, workflow) bool
	for name, v := range f {
		switch name {
		case "team":
			eq, err := comparator(v, "key", "eq")
			if err != nil {
				return nil, err
			}
			matches = append(matches, func(i *issue, _ workflow) bool { return i.team == eq })
		case "labels":
			some, err := comparator(v, "some", "name", "eq")
			if err != nil {
				return nil, err
			}
			matches = append(matches, func(i *issue, _ workflow) bool { return slices.Contains(i.labels, some) })
		case "state":
			nin, err := path(v, "type", "nin")
			if err != nil {
				return nil, err
			}
			var types []string
			list, _ := nin.([]any)
			for _, t := range list {
				types = append(types, fmt.Sprint(t))
			}
			matches = append(matches, func(_ *issue, st workflow) bool { return !slices.Contains(types, st.Type) })
		default:
			return nil, invalid("Field \"%s\" is not defined by type \"IssueFilter\".", name)
		}
	}
	return func(i *issue, st workflow) bool {
		for _, m := range matches {
			if !m(i, st) {
				return false
			}
		}
		return true
	}, nil
}

// comparator is the string at keys of v, which names a comparator.
func comparator(v any, keys ...string) (string, *gqlError) {
	got, err := path(v, keys...)
	if err != nil {
		return "", err
	}
	str, ok := got.(string)
	if !ok {
		return "", invalid("Expected a string at %s.", strings.Join(keys, "."))
	}
	return str, nil
}

// path walks the objects of v by keys, and refuses any key but the one
// asked for at each step: the fake knows one shape of each filter.
func path(v any, keys ...string) (any, *gqlError) {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok || len(m) != 1 {
			return nil, invalid("Expected an object with the field %q.", k)
		}
		if v, ok = m[k]; !ok {
			return nil, invalid("Field %q is not defined here.", firstKey(m))
		}
	}
	return v, nil
}

func firstKey(m map[string]any) string {
	for k := range m {
		return k
	}
	return ""
}

// issueOf is the item the key in "id" names, with its team and its states.
// What the adapter reads of the state, it reads here.
func (s *Server) issueOf(vars map[string]any, _ bool) (map[string]any, *gqlError) {
	id, err := stringVar(vars, "id")
	if err != nil {
		return nil, err
	}
	i := s.lookup(id)
	if i == nil {
		return nil, notFound()
	}
	return map[string]any{"issue": s.issueJSON(i)}, nil
}

func (s *Server) viewer(k key) map[string]any {
	return s.userJSON(k.user).(map[string]any)
}

func (s *Server) commentJSON(c *comment) map[string]any {
	return map[string]any{"id": c.id, "body": c.body, "createdAt": stamp(c.created),
		"url": "https://linear.test/acme/issue/comment/" + c.id, "user": s.userJSON(c.user), "botActor": nil}
}

// comments is the item's comments, newest first, and the key's own user.
func (s *Server) comments(vars map[string]any, k key) (map[string]any, *gqlError) {
	id, err := stringVar(vars, "id")
	if err != nil {
		return nil, err
	}
	i := s.lookup(id)
	if i == nil {
		return nil, notFound()
	}
	newest := slices.Clone(i.comments)
	slices.Reverse(newest)
	from, to, err := page(vars, len(newest))
	if err != nil {
		return nil, err
	}
	nodes := []map[string]any{}
	for _, c := range newest[from:to] {
		nodes = append(nodes, s.commentJSON(c))
	}
	issue := s.issueJSON(i)
	issue["comments"] = map[string]any{keyNodes: nodes, "pageInfo": pageInfo(to, len(newest))}
	return map[string]any{"viewer": s.viewer(k), "issue": issue}, nil
}

func (s *Server) commentCreate(vars map[string]any, k key) (map[string]any, *gqlError) {
	input, _ := vars["input"].(map[string]any)
	issueID, _ := input["issueId"].(string)
	body, ok := input["body"].(string)
	i := s.lookup(issueID)
	if issueID == "" || !ok {
		return nil, invalid("Variable \"$input\" of type \"CommentCreateInput!\" needs issueId and body.")
	}
	if i == nil {
		return nil, notFound()
	}
	c := s.say(i, k.user, body)
	return map[string]any{"commentCreate": map[string]any{"success": true, "comment": s.commentJSON(c)}}, nil
}

func (s *Server) issueUpdate(vars map[string]any) (map[string]any, *gqlError) {
	id, err := stringVar(vars, "id")
	if err != nil {
		return nil, err
	}
	input, _ := vars["input"].(map[string]any)
	stateID, _ := input["stateId"].(string)
	i := s.lookup(id)
	if i == nil {
		return nil, notFound()
	}
	for k := range input {
		if k != "stateId" {
			return nil, invalid("Field %q is not used by the fake's IssueUpdateInput.", k)
		}
	}
	for _, st := range s.teams[i.team].states {
		if st.id != stateID {
			continue
		}
		if st.Type == Duplicate {
			// Linear moves an issue to the reserved status only with a
			// duplicate relation.
			return nil, invalid("Moving to the duplicate status needs a duplicate relation.")
		}
		i.state, i.updated = st.id, s.tick()
		return map[string]any{"issueUpdate": map[string]any{"success": true}}, nil
	}
	return nil, invalid("The status %q is not a status of team %s.", stateID, i.team)
}
