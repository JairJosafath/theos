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
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The fake's webhooks, step 6 of docs/plans/github-host.md (#79): those of
// an organization and of a repository, and what they deliver.
//
//	GET    /orgs/{org}/hooks, /repos/{o}/{r}/hooks  the webhooks, oldest first, 100 a page at most; a secret shows as ********
//	POST   .../hooks                                make one: its name must be "web" and its config hold a url, 422 otherwise,
//	                                                and for a second one with the same url; the new one is pinged
//	GET    .../hooks/{id}                           one webhook, 404 without it
//	PATCH  .../hooks/{id}                           only the fields sent change; a config sent replaces the config whole,
//	                                                so a secret it leaves out is removed
//	DELETE .../hooks/{id}                           remove one, 404 without it
//
// https://docs.github.com/en/rest/orgs/webhooks and /rest/repos/webhooks.
// Webhooks are their owner's to manage: New's user alone reads and writes
// them, another account gets 404, as GitHub answers a token without
// admin:org_hook or admin:repo_hook, and so does an owner that is no
// organization. An installation token takes the repository_hooks
// permission.
//
// A delivery is a POST of the event's payload to the webhook's URL, as
// /webhooks/webhook-events-and-payloads documents it, with X-GitHub-Event,
// X-GitHub-Delivery, X-GitHub-Hook-ID and, for a webhook with a secret,
// X-Hub-Signature-256: "sha256=" and the hex HMAC-SHA256 of the body under
// the secret. An event of a repository goes to the repository's webhooks
// and its organization's, those that are active and ask for the event or
// for "*". GitHub delivers soon after the event; the fake delivers before
// the call that caused it returns, in order, so a test reads what arrived
// without waiting. It delivers:
//
//	ping                 a webhook was made
//	issues               opened, closed, reopened, edited, labeled, unlabeled
//	issue_comment        created
//	pull_request         opened, closed, labeled, unlabeled
//	pull_request_review  submitted
//	status               a commit status was reported
//	push                 a branch or a tag moved, was made or was deleted: over Git, by a ref call, a merge or an update-branch
//	check_run            created or completed, by AddCheckRun and AddJob
//	workflow_run         requested, in_progress or completed, by AddRun and AddJob
//	release              published, by AddRelease

// hook is a webhook of an organization or a repository.
type hook struct {
	id     int64
	config hookConfig
	events []string
	active bool
}

// hookConfig is a webhook's config as the API takes and answers it.
// insecure_ssl comes as a string or a number.
type hookConfig struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type,omitempty"`
	Secret      string `json:"secret,omitempty"`
	InsecureSSL any    `json:"insecure_ssl,omitempty"`
}

// hookJSON is a webhook as the API answers it.
type hookJSON struct {
	Type   string     `json:"type"`
	ID     int64      `json:"id"`
	Name   string     `json:"name"`
	Active bool       `json:"active"`
	Events []string   `json:"events"`
	Config hookConfig `json:"config"`
}

// Webhook is a webhook as the fake keeps it, its secret included, for a
// test to read.
type Webhook struct {
	ID                       int64
	URL, ContentType, Secret string
	Events                   []string
	Active                   bool
}

// The kinds of owner a webhook has, as a delivery's
// X-GitHub-Hook-Installation-Target-Type and a webhook's type name them.
const (
	ofOrganization = "Organization"
	ofRepository   = "Repository"
)

// The events the fake delivers, and the actor of those Actions causes.
const (
	eventPing        = "ping"
	eventIssues      = "issues"
	eventComment     = "issue_comment"
	eventPull        = "pull_request"
	eventReview      = "pull_request_review"
	eventStatus      = "status"
	eventPush        = "push"
	eventCheckRun    = "check_run"
	eventWorkflowRun = "workflow_run"
	eventRelease     = "release"

	actionsBot = "github-actions[bot]"
)

// hookName is the name of every webhook; secretMask is how the API shows a
// secret; zeroSHA is the commit of a ref that isn't there, in a push.
const (
	hookName   = "web"
	secretMask = "********"
	// actionCreated is the action of a new comment and a new check run.
	actionCreated = "created"
	zeroSHA       = "0000000000000000000000000000000000000000"
)

var (
	orgHooksPath  = regexp.MustCompile(`^/api/v3/orgs/([^/]+)/hooks(?:/(\d+))?$`)
	repoHooksPath = regexp.MustCompile(`^/hooks(?:/(\d+))?$`)
	hooksAt       = regexp.MustCompile(`^/hooks(/|$)`)
)

// OrgWebhooks returns an organization's webhooks, oldest first.
func (s *Server) OrgWebhooks(org string) []Webhook {
	s.mu.Lock()
	defer s.mu.Unlock()
	return webhooks(s.orgHooks[org])
}

// RepoWebhooks returns a repository's own webhooks, oldest first.
func (s *Server) RepoWebhooks(owner, name string) []Webhook {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kept := s.repos[owner+"/"+name]; kept != nil {
		return webhooks(kept.hooks)
	}
	return nil
}

func webhooks(hooks []*hook) []Webhook {
	out := make([]Webhook, 0, len(hooks))
	for _, h := range hooks {
		out = append(out, Webhook{ID: h.id, URL: h.config.URL, ContentType: h.config.ContentType,
			Secret: h.config.Secret, Events: slices.Clone(h.events), Active: h.active})
	}
	return out
}

func (h *hook) json(kind string) hookJSON {
	config := h.config
	if config.Secret != "" {
		config.Secret = secretMask
	}
	return hookJSON{Type: kind, ID: h.id, Name: hookName, Active: h.active, Events: h.events, Config: config}
}

// hooksAPI serves the webhooks of one owner, hooks, of kind ofOrganization
// or ofRepository, kept being the repository of the latter; id is the
// webhook the path names, "" for the list. It returns the webhooks as the
// call left them.
func (s *Server) hooksAPI(w http.ResponseWriter, r *http.Request, hooks []*hook, id, kind string, kept *repo) []*hook {
	if s.acting != s.admin {
		reply(w, http.StatusNotFound, message("Not Found"))
		return hooks
	}
	if id == "" {
		switch r.Method {
		case http.MethodGet:
			listed := make([]hookJSON, 0, len(hooks))
			for _, h := range hooks {
				listed = append(listed, h.json(kind))
			}
			reply(w, http.StatusOK, page(r.URL.Query(), listed))
		case http.MethodPost:
			return s.createHook(w, r, hooks, kind, kept)
		default:
			s.notServed(w, r)
		}
		return hooks
	}
	n, _ := strconv.ParseInt(id, 10, 64)
	i := slices.IndexFunc(hooks, func(h *hook) bool { return h.id == n })
	if i < 0 {
		reply(w, http.StatusNotFound, message("Not Found"))
		return hooks
	}
	switch r.Method {
	case http.MethodGet:
		reply(w, http.StatusOK, hooks[i].json(kind))
	case http.MethodPatch:
		s.patchHook(w, r, hooks[i], kind)
	case http.MethodDelete:
		hooks = slices.Delete(hooks, i, i+1)
		reply(w, http.StatusNoContent, nil)
	default:
		s.notServed(w, r)
	}
	return hooks
}

// hookBody is what makes or edits a webhook.
type hookBody struct {
	Name   string      `json:"name"`
	Config *hookConfig `json:"config"`
	Events *[]string   `json:"events"`
	Active *bool       `json:"active"`
}

func (s *Server) createHook(w http.ResponseWriter, r *http.Request, hooks []*hook, kind string, kept *repo) []*hook {
	invalid := func(text string) {
		reply(w, http.StatusUnprocessableEntity, struct {
			apiError
			Errors []apiError `json:"errors"`
		}{message("Validation Failed"), []apiError{message(text)}})
	}
	var body hookBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name != hookName {
		invalid(`name must be passed as "web"`)
		return hooks
	}
	if body.Config == nil || body.Config.URL == "" {
		invalid("url cannot be blank")
		return hooks
	}
	if slices.ContainsFunc(hooks, func(h *hook) bool { return h.config.URL == body.Config.URL }) {
		invalid("Hook already exists on this " + strings.ToLower(kind))
		return hooks
	}
	s.nextID++
	h := &hook{id: s.nextID, config: *body.Config, events: []string{eventPush}, active: true}
	if body.Events != nil {
		h.events = *body.Events
	}
	if body.Active != nil {
		h.active = *body.Active
	}
	if h.active {
		made := h.json(kind)
		ping := eventBody{Zen: "Anything added dilutes everything else.", HookID: h.id, Hook: &made,
			Sender: account{s.acting}}
		if kept != nil {
			ping.Repository = s.eventRepository(kept)
		}
		s.queue(h, kind, eventPing, ping)
	}
	reply(w, http.StatusCreated, h.json(kind))
	return append(hooks, h)
}

func (s *Server) patchHook(w http.ResponseWriter, r *http.Request, h *hook, kind string) {
	var body hookBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Config != nil && body.Config.URL == "" {
		reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
		return
	}
	if body.Config != nil {
		h.config = *body.Config
	}
	if body.Events != nil {
		h.events = *body.Events
	}
	if body.Active != nil {
		h.active = *body.Active
	}
	reply(w, http.StatusOK, h.json(kind))
}

// delivery is one request a webhook is to send.
type delivery struct {
	url, secret, event, kind string
	form                     bool
	hook                     int64
	body                     []byte
}

// account is a user or an organization in a payload.
type account struct {
	Login string `json:"login"`
}

// eventBody is a delivery's payload: the fields of the event it is of, then
// where it happened and who caused it.
type eventBody struct {
	Action      string         `json:"action,omitempty"`
	Number      int            `json:"number,omitempty"`
	Issue       any            `json:"issue,omitempty"`
	Comment     *eventNote     `json:"comment,omitempty"`
	Label       *label         `json:"label,omitempty"`
	PullRequest any            `json:"pull_request,omitempty"`
	Review      *pullReview    `json:"review,omitempty"`
	CheckRun    *checkRunBody  `json:"check_run,omitempty"`
	WorkflowRun *runJSON       `json:"workflow_run,omitempty"`
	Workflow    *eventWorkflow `json:"workflow,omitempty"`
	Release     *releaseJSON   `json:"release,omitempty"`
	// A ping's.
	Zen    string    `json:"zen,omitempty"`
	HookID int64     `json:"hook_id,omitempty"`
	Hook   *hookJSON `json:"hook,omitempty"`
	// A status's and a push's are the payload's own top fields.
	*statusBody
	*pushBody

	Repository   any      `json:"repository,omitempty"`
	Organization *account `json:"organization,omitempty"`
	Sender       account  `json:"sender"`
}

// eventNote is a comment in a payload.
type eventNote struct {
	Body string  `json:"body"`
	User account `json:"user"`
}

// statusBody is a status's payload: the status, its commit and the
// branches that hold the commit.
type statusBody struct {
	SHA  string `json:"sha"`
	Name string `json:"name"`
	commitStatus
	Branches []eventBranch `json:"branches"`
}

// eventBranch is a branch in a status's payload.
type eventBranch struct {
	Name      string `json:"name"`
	Commit    object `json:"commit"`
	Protected bool   `json:"protected"`
}

// pushBody is a push's payload; a ref that isn't there has zeroSHA.
type pushBody struct {
	Ref        string  `json:"ref"`
	Before     string  `json:"before"`
	After      string  `json:"after"`
	Created    bool    `json:"created"`
	Deleted    bool    `json:"deleted"`
	Forced     bool    `json:"forced"`
	Pusher     pusher  `json:"pusher"`
	HeadCommit *pushed `json:"head_commit"`
}

type pusher struct {
	Name string `json:"name"`
}

type pushed struct {
	ID string `json:"id"`
}

// checkRunBody is a check run in a payload: the run, its commit, the
// suite it is of, whose branch is null for a commit no branch ends at, and
// the pull requests of that branch.
type checkRunBody struct {
	checkRun
	HeadSHA      string     `json:"head_sha"`
	CheckSuite   eventSuite `json:"check_suite"`
	PullRequests []pullRef  `json:"pull_requests"`
}

type eventSuite struct {
	HeadBranch *string `json:"head_branch"`
	HeadSHA    string  `json:"head_sha"`
}

// eventWorkflow is the workflow of a workflow run's payload.
type eventWorkflow struct {
	Path string `json:"path"`
}

// eventRepository is the repository a payload names.
func (s *Server) eventRepository(kept *repo) any {
	return struct {
		repository
		Owner account `json:"owner"`
	}{s.repoJSON(kept.owner, kept.name), account{kept.owner}}
}

// watched says whether an event of kept has a webhook to go to.
func (s *Server) watched(kept *repo) bool {
	return kept != nil && len(s.orgHooks[kept.owner])+len(kept.hooks) > 0
}

// emit queues the delivery of event, which sender caused in kept, to every
// webhook that asks for it. payload builds the event's own part of the
// payload and runs only when a webhook does; the repository, its
// organization and the sender are added to it.
func (s *Server) emit(kept *repo, sender, event string, payload func() eventBody) {
	var body *eventBody
	deliver := func(hooks []*hook, kind string) {
		for _, h := range hooks {
			if !h.active || !slices.Contains(h.events, event) && !slices.Contains(h.events, "*") {
				continue
			}
			if body == nil {
				built := payload()
				built.Repository, built.Sender = s.eventRepository(kept), account{sender}
				if s.orgs[kept.owner] {
					built.Organization = &account{kept.owner}
				}
				body = &built
			}
			s.queue(h, kind, event, *body)
		}
	}
	deliver(s.orgHooks[kept.owner], ofOrganization)
	deliver(kept.hooks, ofRepository)
}

// queue keeps the delivery of payload to h until the call is done.
func (s *Server) queue(h *hook, kind, event string, payload eventBody) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	s.outbox = append(s.outbox, delivery{url: h.config.URL, secret: h.config.Secret, event: event, kind: kind,
		form: h.config.ContentType == "form", hook: h.id, body: body})
}

// done ends a call that holds the lock: it lets the lock go and sends what
// the call queued, oldest first, before the call returns.
func (s *Server) done() {
	queued := s.outbox
	s.outbox = nil
	s.mu.Unlock()
	for _, d := range queued {
		d.send()
	}
}

// deliveries sends the fake's deliveries. A receiver that doesn't answer
// fails the delivery and holds the call no longer than this.
var deliveries = &http.Client{Timeout: 10 * time.Second}

// send posts the delivery; what the receiver answers is nobody's to read.
func (d delivery) send() {
	body, contentType := d.body, "application/json"
	if d.form {
		body, contentType = []byte(url.Values{"payload": {string(d.body)}}.Encode()), "application/x-www-form-urlencoded"
	}
	req, err := http.NewRequest(http.MethodPost, d.url, bytes.NewReader(body))
	if err != nil {
		return
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "GitHub-Hookshot/githubtest")
	req.Header.Set("X-GitHub-Event", d.event)
	req.Header.Set("X-GitHub-Delivery", fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]))
	req.Header.Set("X-GitHub-Hook-ID", strconv.FormatInt(d.hook, 10))
	req.Header.Set("X-GitHub-Hook-Installation-Target-Type", strings.ToLower(d.kind))
	if d.secret != "" {
		mac := hmac.New(sha256.New, []byte(d.secret))
		mac.Write(body)
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := deliveries.Do(req)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// issueEvent queues what happened to an issue: an issues event, or, for one
// that is a pull request, a pull_request event. l is the label of labeled
// and unlabeled.
func (s *Server) issueEvent(kept *repo, i *issue, action string, l *label) {
	p := kept.pulls[i.number]
	if slices.Contains(i.labels, pullRequestMark) || p != nil {
		if p != nil {
			s.pullEvent(kept, p, action, l)
		}
		return
	}
	s.emit(kept, s.acting, eventIssues, func() eventBody {
		return eventBody{Action: action, Issue: s.issueJSON(kept, i), Label: l}
	})
}

// pullEvent queues what happened to a pull request.
func (s *Server) pullEvent(kept *repo, p *pull, action string, l *label) {
	s.emit(kept, s.acting, eventPull, func() eventBody {
		return eventBody{Action: action, Number: p.number, PullRequest: s.pullJSON(kept, p), Label: l}
	})
}

// labelEvents queues labeled and unlabeled for what changed between an
// issue's labels before and now.
func (s *Server) labelEvents(kept *repo, i *issue, before []string) {
	for _, name := range before {
		if !slices.Contains(i.labels, name) {
			s.issueEvent(kept, i, "unlabeled", kept.label(name))
		}
	}
	for _, name := range i.labels {
		if !slices.Contains(before, name) {
			s.issueEvent(kept, i, "labeled", kept.label(name))
		}
	}
}

// refs are a repository's branches and tags with their commits.
func (s *Server) refs(owner, name string) map[string]string {
	out := map[string]string{}
	listed, err := s.gitIn(owner, name, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/tags")
	if err != nil || listed == "" {
		return out
	}
	for line := range strings.SplitSeq(listed, "\n") {
		if ref, sha, ok := strings.Cut(line, " "); ok {
			out[ref] = sha
		}
	}
	return out
}

// pushed queues a push for every branch and tag that differs between
// before and the repository's refs now, as a push over Git leaves them.
func (s *Server) pushed(kept *repo, sender string, before map[string]string) {
	after := s.refs(kept.owner, kept.name)
	all := slices.Sorted(maps.Keys(before))
	for _, ref := range slices.Sorted(maps.Keys(after)) {
		if _, was := before[ref]; !was {
			all = append(all, ref)
		}
	}
	for _, ref := range all {
		if before[ref] != after[ref] {
			s.push(kept, sender, ref, before[ref], after[ref])
		}
	}
}

// push queues the push that moved ref from one commit to another; "" is a
// ref that wasn't, or is no longer, there. Only branches and tags push.
func (s *Server) push(kept *repo, sender, ref, from, to string) {
	if !strings.HasPrefix(ref, "refs/heads/") && !strings.HasPrefix(ref, "refs/tags/") {
		return
	}
	s.emit(kept, sender, eventPush, func() eventBody {
		push := pushBody{Ref: ref, Before: from, After: to, Created: from == "", Deleted: to == "",
			Pusher: pusher{sender}}
		if from == "" {
			push.Before = zeroSHA
		}
		if to == "" {
			push.After = zeroSHA
		} else {
			push.HeadCommit = &pushed{to}
		}
		return eventBody{pushBody: &push}
	})
}

// holding are the branches that hold a commit, ten at most, as a status's
// payload lists them: the commit may be the newest of none of them.
func (s *Server) holding(kept *repo, sha string) []eventBranch {
	out := []eventBranch{}
	listed, err := s.gitIn(kept.owner, kept.name, "branch", "--contains", sha, "--format=%(refname:short) %(objectname)")
	if err != nil || listed == "" {
		return out
	}
	for line := range strings.SplitSeq(listed, "\n") {
		if name, head, ok := strings.Cut(line, " "); ok && len(out) < 10 {
			out = append(out, eventBranch{Name: name, Commit: object{SHA: head}})
		}
	}
	return out
}

// headOf is the branch whose newest commit is sha, nil when there is none:
// what a check run reported for a commit alone names as its branch.
func (s *Server) headOf(kept *repo, sha string) *string {
	for _, b := range s.holding(kept, sha) {
		if b.Commit.SHA == sha {
			return &b.Name
		}
	}
	return nil
}

// checkRunEvent queues a check run's event: created, or completed for one
// that is.
func (s *Server) checkRunEvent(kept *repo, c checkRun, sha string, branch *string, pulls []pullRef) {
	action := actionCreated
	if c.Status == statusCompleted {
		action = statusCompleted
	}
	s.emit(kept, actionsBot, eventCheckRun, func() eventBody {
		return eventBody{Action: action, CheckRun: &checkRunBody{checkRun: c, HeadSHA: sha,
			CheckSuite: eventSuite{HeadBranch: branch, HeadSHA: sha}, PullRequests: pulls}}
	})
}

// runEvent queues a workflow run's event: requested for a new one, then
// in_progress or completed as its jobs say.
func (s *Server) runEvent(kept *repo, r *run, action string) {
	s.emit(kept, actionsBot, eventWorkflowRun, func() eventBody {
		listed := s.runJSON(kept, r)
		return eventBody{Action: action, WorkflowRun: &listed,
			Workflow: &eventWorkflow{Path: ".github/workflows/" + r.workflow}}
	})
}
