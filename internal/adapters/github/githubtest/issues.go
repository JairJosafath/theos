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

package githubtest

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The fake's issues, labels, milestones and dependencies
// (https://docs.github.com/en/rest/issues):
//
//	GET, POST   /repos/{o}/{r}/labels                     a label's name is unique without case
//	GET, PATCH  /repos/{o}/{r}/labels/{name}
//	GET, POST   /repos/{o}/{r}/issues                     ?state=open|closed|all, labels=a,b, milestone=N, page, per_page
//	GET, PATCH  /repos/{o}/{r}/issues/{n}                 state, body, milestone (null takes it out), assignees
//	POST        /repos/{o}/{r}/issues/{n}/comments
//	POST, PUT   /repos/{o}/{r}/issues/{n}/labels          add to the issue's labels; replace them
//	DELETE      /repos/{o}/{r}/issues/{n}/labels/{name}   404 when the issue doesn't have it
//	GET, POST   /repos/{o}/{r}/issues/{n}/dependencies/blocked_by   {issue_id}; 422 for one that is there
//	GET, POST   /repos/{o}/{r}/milestones                 ?state=open|closed|all
//	PATCH       /repos/{o}/{r}/milestones/{number}        only the fields sent change

type label struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

type milestone struct {
	Number      int64      `json:"number"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"`
	DueOn       *time.Time `json:"due_on"`
}

type issue struct {
	id        int64
	number    int
	title     string
	body      string
	state     string
	labels    []string
	assignees []string
	comments  []string
	milestone int64
	// blockedBy are the ids of the issues it depends on, in any repository.
	blockedBy []int64
}

const (
	open   = "open"
	closed = "closed"

	labelsRest     = "/labels"
	milestonesRest = "/milestones"
)

var (
	labelPath          = regexp.MustCompile(`^/labels/(.+)$`)
	issuePath          = regexp.MustCompile(`^/issues/(\d+)$`)
	issueCommentsPath  = regexp.MustCompile(`^/issues/(\d+)/comments$`)
	issueLabelsPath    = regexp.MustCompile(`^/issues/(\d+)/labels$`)
	issueLabelPath     = regexp.MustCompile(`^/issues/(\d+)/labels/(.+)$`)
	issueBlockedByPath = regexp.MustCompile(`^/issues/(\d+)/dependencies/blocked_by$`)
	milestonePath      = regexp.MustCompile(`^/milestones/(\d+)$`)
)

// Comments returns an issue's comments, oldest first, for a test to read.
func (s *Server) Comments(owner, name string, number int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.repos[owner+"/"+name]; r != nil {
		if i := r.issue(number); i != nil {
			return append([]string(nil), i.comments...)
		}
	}
	return nil
}

// Assignees returns an issue's assignees, for a test to read.
func (s *Server) Assignees(owner, name string, number int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r := s.repos[owner+"/"+name]; r != nil {
		if i := r.issue(number); i != nil {
			return append([]string(nil), i.assignees...)
		}
	}
	return nil
}

// AddPullRequest files a pull request among a repository's issues, as GitHub
// keeps them: the issues' lists must leave it out.
func (s *Server) AddPullRequest(owner, name, title string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.repos[owner+"/"+name]
	s.nextID++
	i := &issue{id: s.nextID, number: len(r.issues) + 1, title: title, state: open, labels: []string{pullRequestMark}}
	r.issues = append(r.issues, i)
	return i.number
}

// pullRequestMark is the label the fake keeps on a pull request, and never
// shows: it is how an issue is known to be one.
const pullRequestMark = "\x00pull-request"

func (r *repo) issue(number int) *issue {
	if number < 1 || number > len(r.issues) {
		return nil
	}
	return r.issues[number-1]
}

func (r *repo) label(name string) *label {
	for _, l := range r.labels {
		if strings.EqualFold(l.Name, name) {
			return l
		}
	}
	return nil
}

func (r *repo) milestone(number int64) *milestone {
	for _, m := range r.milestones {
		if m.Number == number {
			return m
		}
	}
	return nil
}

// issueJSON is an issue as the API answers it.
func (s *Server) issueJSON(r *repo, i *issue) map[string]any {
	at := s.srv.URL + "/" + r.owner + "/" + r.name
	labels := []*label{}
	for _, name := range i.labels {
		if l := r.label(name); l != nil {
			labels = append(labels, l)
		}
	}
	out := map[string]any{
		"id": i.id, "number": i.number, "title": i.title, "body": i.body, "state": i.state, "labels": labels,
		"html_url":       at + "/issues/" + strconv.Itoa(i.number),
		"repository_url": s.srv.URL + "/api/v3/repos/" + r.owner + "/" + r.name,
		"milestone":      r.milestone(i.milestone),
	}
	if slices.Contains(i.labels, pullRequestMark) {
		out["pull_request"] = map[string]string{"url": at + "/pull/" + strconv.Itoa(i.number)}
	}
	return out
}

// page cuts items to the page the query asks for, 30 a page unless per_page
// says otherwise, 100 at most.
func page[T any](q url.Values, items []T) []T {
	per, _ := strconv.Atoi(q.Get("per_page"))
	if per < 1 {
		per = 30
	}
	per = min(per, 100)
	n, _ := strconv.Atoi(q.Get("page"))
	n = max(n, 1)
	from := min((n-1)*per, len(items))
	return items[from:min(from+per, len(items))]
}

// issuesAPI serves rest when it is one of this file's endpoints.
func (s *Server) issuesAPI(w http.ResponseWriter, r *http.Request, kept *repo, rest string) bool {
	switch {
	case rest == labelsRest || labelPath.MatchString(rest):
		s.labelsAPI(w, r, kept, rest)
	case rest == milestonesRest || milestonePath.MatchString(rest):
		s.milestonesAPI(w, r, kept, rest)
	case rest == "/issues":
		s.issuesList(w, r, kept)
	case strings.HasPrefix(rest, "/issues/"):
		s.issueAPI(w, r, kept, rest)
	default:
		return false
	}
	return true
}

func (s *Server) labelsAPI(w http.ResponseWriter, r *http.Request, kept *repo, rest string) {
	var body label
	if r.Method == http.MethodPost || r.Method == http.MethodPatch {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
			return
		}
	}
	switch {
	case rest == labelsRest && r.Method == http.MethodGet:
		reply(w, http.StatusOK, page(r.URL.Query(), kept.labels))
	case rest == labelsRest && r.Method == http.MethodPost:
		if body.Name == "" || strings.HasPrefix(body.Color, "#") || kept.label(body.Name) != nil {
			reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
			return
		}
		kept.labels = append(kept.labels, &body)
		reply(w, http.StatusCreated, body)
	case rest != labelsRest && (r.Method == http.MethodGet || r.Method == http.MethodPatch):
		name, _ := url.PathUnescape(labelPath.FindStringSubmatch(rest)[1])
		l := kept.label(name)
		if l == nil {
			reply(w, http.StatusNotFound, message("Not Found"))
			return
		}
		if r.Method == http.MethodPatch {
			l.Description = body.Description
		}
		reply(w, http.StatusOK, l)
	default:
		s.notServed(w, r)
	}
}

func (s *Server) milestonesAPI(w http.ResponseWriter, r *http.Request, kept *repo, rest string) {
	switch {
	case rest == milestonesRest && r.Method == http.MethodGet:
		state := r.URL.Query().Get("state")
		if state == "" {
			state = open
		}
		listed := []*milestone{}
		for _, m := range kept.milestones {
			if state == "all" || m.State == state {
				listed = append(listed, m)
			}
		}
		reply(w, http.StatusOK, page(r.URL.Query(), listed))
	case rest == milestonesRest && r.Method == http.MethodPost:
		var body struct {
			Title       string     `json:"title"`
			Description string     `json:"description"`
			DueOn       *time.Time `json:"due_on"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Title == "" {
			reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
			return
		}
		// GitHub refuses a second milestone of one title.
		if slices.ContainsFunc(kept.milestones, func(m *milestone) bool { return m.Title == body.Title }) {
			reply(w, http.StatusUnprocessableEntity, message("Validation Failed: title already_exists"))
			return
		}
		m := &milestone{Number: int64(len(kept.milestones) + 1), Title: body.Title, Description: body.Description,
			State: open, DueOn: body.DueOn}
		kept.milestones = append(kept.milestones, m)
		reply(w, http.StatusCreated, m)
	case rest != milestonesRest && r.Method == http.MethodPatch:
		number, _ := strconv.ParseInt(milestonePath.FindStringSubmatch(rest)[1], 10, 64)
		m := kept.milestone(number)
		if m == nil {
			reply(w, http.StatusNotFound, message("Not Found"))
			return
		}
		// Only the fields sent change: the due date stays.
		var body struct {
			Title       *string `json:"title"`
			Description *string `json:"description"`
			State       *string `json:"state"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
			return
		}
		if body.Title != nil {
			m.Title = *body.Title
		}
		if body.Description != nil {
			m.Description = *body.Description
		}
		if body.State != nil {
			m.State = *body.State
		}
		reply(w, http.StatusOK, m)
	default:
		s.notServed(w, r)
	}
}

func (s *Server) issuesList(w http.ResponseWriter, r *http.Request, kept *repo) {
	q := r.URL.Query()
	switch r.Method {
	case http.MethodGet:
		state := q.Get("state")
		if state == "" {
			state = open
		}
		var want []string
		if l := q.Get("labels"); l != "" {
			want = strings.Split(l, ",")
		}
		inMilestone, _ := strconv.ParseInt(q.Get("milestone"), 10, 64)
		listed := []map[string]any{}
		for _, i := range kept.issues {
			if state != "all" && i.state != state || inMilestone != 0 && i.milestone != inMilestone {
				continue
			}
			if slices.ContainsFunc(want, func(l string) bool { return !hasLabel(i, l) }) {
				continue
			}
			listed = append(listed, s.issueJSON(kept, i))
		}
		reply(w, http.StatusOK, page(q, listed))
	case http.MethodPost:
		var body struct {
			Title     string   `json:"title"`
			Body      string   `json:"body"`
			Labels    []string `json:"labels"`
			Milestone int64    `json:"milestone"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Title == "" ||
			body.Milestone != 0 && kept.milestone(body.Milestone) == nil {
			reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
			return
		}
		s.nextID++
		i := &issue{id: s.nextID, number: len(kept.issues) + 1, title: body.Title, body: body.Body, state: open,
			milestone: body.Milestone}
		for _, l := range body.Labels {
			// GitHub makes a label an issue is filed with when it is new.
			if kept.label(l) == nil {
				kept.labels = append(kept.labels, &label{Name: l, Color: "ededed"})
			}
			i.labels = append(i.labels, kept.label(l).Name)
		}
		kept.issues = append(kept.issues, i)
		reply(w, http.StatusCreated, s.issueJSON(kept, i))
	default:
		s.notServed(w, r)
	}
}

func hasLabel(i *issue, name string) bool {
	return slices.ContainsFunc(i.labels, func(l string) bool { return strings.EqualFold(l, name) })
}

// issueAPI serves the endpoints under one issue.
func (s *Server) issueAPI(w http.ResponseWriter, r *http.Request, kept *repo, rest string) {
	number := 0
	for _, re := range []*regexp.Regexp{issuePath, issueCommentsPath, issueLabelsPath, issueLabelPath, issueBlockedByPath} {
		if m := re.FindStringSubmatch(rest); m != nil {
			number, _ = strconv.Atoi(m[1])
			break
		}
	}
	i := kept.issue(number)
	if i == nil {
		reply(w, http.StatusNotFound, message("Not Found"))
		return
	}
	switch {
	case issuePath.MatchString(rest) && r.Method == http.MethodGet:
		reply(w, http.StatusOK, s.issueJSON(kept, i))
	case issuePath.MatchString(rest) && r.Method == http.MethodPatch:
		s.patchIssue(w, r, kept, i)
	case issueCommentsPath.MatchString(rest) && r.Method == http.MethodPost:
		var body struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Body == "" {
			reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
			return
		}
		i.comments = append(i.comments, body.Body)
		reply(w, http.StatusCreated, body)
	case issueLabelsPath.MatchString(rest) && (r.Method == http.MethodPost || r.Method == http.MethodPut):
		s.setLabels(w, r, kept, i)
	case issueLabelPath.MatchString(rest) && r.Method == http.MethodDelete:
		name, _ := url.PathUnescape(issueLabelPath.FindStringSubmatch(rest)[2])
		if !hasLabel(i, name) {
			reply(w, http.StatusNotFound, message("Label does not exist"))
			return
		}
		i.labels = slices.DeleteFunc(i.labels, func(l string) bool { return strings.EqualFold(l, name) })
		reply(w, http.StatusOK, s.issueJSON(kept, i)["labels"])
	case issueBlockedByPath.MatchString(rest):
		s.blockedBy(w, r, i)
	default:
		s.notServed(w, r)
	}
}

func (s *Server) patchIssue(w http.ResponseWriter, r *http.Request, kept *repo, i *issue) {
	var body struct {
		State     *string         `json:"state"`
		Body      *string         `json:"body"`
		Assignees *[]string       `json:"assignees"`
		Milestone json.RawMessage `json:"milestone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
		return
	}
	if len(body.Milestone) > 0 {
		var number int64
		if string(body.Milestone) != "null" {
			if err := json.Unmarshal(body.Milestone, &number); err != nil || kept.milestone(number) == nil {
				reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
				return
			}
		}
		i.milestone = number
	}
	// GitHub closes an issue whatever it depends on.
	if body.State != nil {
		i.state = *body.State
	}
	if body.Body != nil {
		i.body = *body.Body
	}
	if body.Assignees != nil {
		i.assignees = *body.Assignees
	}
	reply(w, http.StatusOK, s.issueJSON(kept, i))
}

func (s *Server) setLabels(w http.ResponseWriter, r *http.Request, kept *repo, i *issue) {
	var body struct {
		Labels []string `json:"labels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
		return
	}
	if r.Method == http.MethodPut {
		i.labels = slices.DeleteFunc(i.labels, func(l string) bool { return l != pullRequestMark })
	}
	for _, name := range body.Labels {
		l := kept.label(name)
		if l == nil {
			l = &label{Name: name, Color: "ededed"}
			kept.labels = append(kept.labels, l)
		}
		if !hasLabel(i, l.Name) {
			i.labels = append(i.labels, l.Name)
		}
	}
	reply(w, http.StatusOK, s.issueJSON(kept, i)["labels"])
}

// find returns the issue with id, and its repository, across repositories.
func (s *Server) find(id int64) (*repo, *issue) {
	for _, r := range s.repos {
		for _, i := range r.issues {
			if i.id == id {
				return r, i
			}
		}
	}
	return nil, nil
}

func (s *Server) blockedBy(w http.ResponseWriter, r *http.Request, i *issue) {
	switch r.Method {
	case http.MethodGet:
		listed := []map[string]any{}
		for _, id := range i.blockedBy {
			if in, blocker := s.find(id); blocker != nil {
				listed = append(listed, s.issueJSON(in, blocker))
			}
		}
		reply(w, http.StatusOK, page(r.URL.Query(), listed))
	case http.MethodPost:
		var body struct {
			IssueID int64 `json:"issue_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
			return
		}
		in, blocker := s.find(body.IssueID)
		if blocker == nil || slices.Contains(i.blockedBy, body.IssueID) || blocker == i {
			reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
			return
		}
		i.blockedBy = append(i.blockedBy, body.IssueID)
		reply(w, http.StatusCreated, s.issueJSON(in, blocker))
	default:
		s.notServed(w, r)
	}
}
