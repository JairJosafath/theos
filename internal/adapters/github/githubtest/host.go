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
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The fake's side of step 2 of docs/plans/github-host.md (#75) that is no
// pull request: what GitHub keeps beside a repository's Git. Each call
// answers as GitHub documents it:
//
//	GET   /repos/{o}/{r}/commits/{ref}/status      the combined status: the latest status of each context; its state is
//	                                               pending with no status at all, and failure with an error among them
//	GET   /repos/{o}/{r}/commits/{ref}/statuses    every status of the commit, newest first
//	POST  /repos/{o}/{r}/statuses/{sha}            report a status; 422 for a state it has none of, or a description past 140
//	GET   /repos/{o}/{r}/commits/{ref}/check-runs  the commit's check runs, as Actions reports its jobs: the latest of
//	                                               each name unless filter=all
//	GET   /repos/{o}/{r}/rulesets                  the rulesets, without their rules
//	GET   /repos/{o}/{r}/rulesets/{id}             a ruleset whole
//	POST  /repos/{o}/{r}/rulesets                  make one; 422 for a rule or an actor GitHub has none of
//	PUT   /repos/{o}/{r}/rulesets/{id}             replace one
//	GET   /repos/{o}/{r}/actions/variables/{name}  a variable, 404 without it; its name is kept in upper case
//	POST  /repos/{o}/{r}/actions/variables         make one; 409 when it exists
//	PATCH /repos/{o}/{r}/actions/variables/{name}  set one's value
//	DELETE /repos/{o}/{r}/actions/variables/{name} remove one
//	GET   /repos/{o}/{r}/releases/tags/{tag}       the published release of a tag; a draft is not found there
//	GET   /repos/{o}/{r}/releases                  every release, drafts too, newest first
//	PATCH /repos/{o}/{r}/releases/{id}             set a release's body
//	GET   /repos/{o}/{r}/releases/assets/{id}      an asset's content, asked for as application/octet-stream
//	GET   /repos/{o}/{r}/zipball/{ref}             git archive's zip of a ref, its files below <owner>-<repo>-<sha>/
//
// https://docs.github.com/en/rest/commits/statuses, /rest/checks/runs,
// /rest/repos/rules, /rest/actions/variables, /rest/releases/releases,
// /rest/releases/assets and /rest/repos/contents#download-a-repository-archive-zip.
// A ruleset is written by a repository's administrator only: another user's
// token gets 403, as GitHub answers a token without the administration
// permission. A variable is not: "to create secrets or variables ... through
// the REST API, you must have collaborator access"
// (https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/use-variables),
// so any of the fake's users writes one. The fake keeps the rulesets and
// enforces none: New's user bypasses them all, and what another user's push
// meets is step 4's.

type commitStatus struct {
	Context     string `json:"context"`
	State       string `json:"state"`
	TargetURL   string `json:"target_url"`
	Description string `json:"description"`
}

type checkRun struct {
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	Conclusion *string `json:"conclusion"`
	HTMLURL    string  `json:"html_url"`
}

// Ruleset is a repository ruleset as the fake keeps it: what the adapter sent.
type Ruleset struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Target      string `json:"target"`
	Enforcement string `json:"enforcement"`
	Conditions  struct {
		RefName struct {
			Include []string `json:"include"`
			Exclude []string `json:"exclude"`
		} `json:"ref_name"`
	} `json:"conditions"`
	BypassActors []struct {
		ActorID    int64  `json:"actor_id"`
		ActorType  string `json:"actor_type"`
		BypassMode string `json:"bypass_mode"`
	} `json:"bypass_actors"`
	Rules []struct {
		Type string `json:"type"`
	} `json:"rules"`
}

type release struct {
	id     int64
	tag    string
	name   string
	body   string
	draft  bool
	assets []*asset
}

type asset struct {
	id      int64
	name    string
	content []byte
}

var (
	combinedPath  = regexp.MustCompile(`^/commits/(.+)/status$`)
	statusesPath  = regexp.MustCompile(`^/commits/(.+)/statuses$`)
	checkRunsPath = regexp.MustCompile(`^/commits/(.+)/check-runs$`)
	setStatusPath = regexp.MustCompile(`^/statuses/([0-9a-f]{40})$`)
	rulesetPath   = regexp.MustCompile(`^/rulesets/(\d+)$`)
	variablePath  = regexp.MustCompile(`^/actions/variables/([^/]+)$`)
	variableName  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	releaseTag    = regexp.MustCompile(`^/releases/tags/(.+)$`)
	releasePath   = regexp.MustCompile(`^/releases/(\d+)$`)
	assetPath     = regexp.MustCompile(`^/releases/assets/(\d+)$`)
	zipballPath   = regexp.MustCompile(`^/zipball/(.+)$`)
)

const (
	rulesetsRest  = "/rulesets"
	variablesRest = "/actions/variables"
	releasesRest  = "/releases"
)

// The states of a commit status.
const (
	stateError   = "error"
	stateFailure = "failure"
	statePending = "pending"
	stateSuccess = "success"
)

// AddCheckRun reports a check run on a commit, as an Actions job does: its
// status is queued, in_progress or completed, and a completed one has a
// conclusion such as success or failure.
func (s *Server) AddCheckRun(owner, name, sha, run, status, conclusion string) {
	s.mu.Lock()
	defer s.done()
	r := s.repos[owner+"/"+name]
	c := checkRun{Name: run, Status: status, HTMLURL: s.srv.URL + "/" + owner + "/" + name + "/runs/" + run}
	if conclusion != "" {
		c.Conclusion = &conclusion
	}
	r.checkRuns[sha] = append(r.checkRuns[sha], c)
	if s.watched(r) {
		s.checkRunEvent(r, c, sha, s.headOf(r, sha), []pullRef{})
	}
}

// Rulesets returns a repository's rulesets, in the order they were made.
func (s *Server) Rulesets(owner, name string) []Ruleset {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.repos[owner+"/"+name].rulesets
	out := make([]Ruleset, 0, len(kept))
	for _, rs := range kept {
		out = append(out, *rs)
	}
	return out
}

// Variable returns an Actions variable of a repository, and whether it has one.
func (s *Server) Variable(owner, name, variable string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.repos[owner+"/"+name].variables[strings.ToUpper(variable)]
	return v, ok
}

// AddRelease makes the release of tag with body and one asset holding
// content, as a release run does. A published one makes the tag at commit
// when the repository has none; a draft has no tag yet, as on GitHub.
func (s *Server) AddRelease(owner, name, tag, commit, body, assetName, content string, draft bool) error {
	s.mu.Lock()
	defer s.done()
	r := s.repos[owner+"/"+name]
	if !draft {
		if _, err := s.gitIn(owner, name, "rev-parse", "--verify", "--quiet", "refs/tags/"+tag); err != nil {
			if _, err := s.gitIn(owner, name, "update-ref", "refs/tags/"+tag, commit); err != nil {
				return err
			}
		}
	}
	s.nextID++
	rel := &release{id: s.nextID, tag: tag, name: tag, body: body, draft: draft}
	s.nextID++
	rel.assets = append(rel.assets, &asset{id: s.nextID, name: assetName, content: []byte(content)})
	r.releases = append(r.releases, rel)
	if !draft {
		s.emit(r, actionsBot, eventRelease, func() eventBody {
			listed := s.releaseJSON(r, rel)
			return eventBody{Action: "published", Release: &listed}
		})
	}
	return nil
}

// hostAPI serves the calls of this file, and says whether rest was one.
func (s *Server) hostAPI(w http.ResponseWriter, r *http.Request, kept *repo, rest string) bool {
	get, post := r.Method == http.MethodGet, r.Method == http.MethodPost
	switch {
	case get && combinedPath.MatchString(rest):
		s.combinedStatus(w, r, kept, combinedPath.FindStringSubmatch(rest)[1])
	case get && statusesPath.MatchString(rest):
		s.listStatuses(w, r, kept, statusesPath.FindStringSubmatch(rest)[1])
	case get && checkRunsPath.MatchString(rest):
		s.listCheckRuns(w, r, kept, checkRunsPath.FindStringSubmatch(rest)[1])
	case post && setStatusPath.MatchString(rest):
		s.setStatus(w, r, kept, setStatusPath.FindStringSubmatch(rest)[1])
	case rest == rulesetsRest || rulesetPath.MatchString(rest):
		s.rulesetsAPI(w, r, kept, rest)
	case rest == variablesRest || variablePath.MatchString(rest):
		s.variablesAPI(w, r, kept, rest)
	case get && releaseTag.MatchString(rest):
		tag, _ := url.PathUnescape(releaseTag.FindStringSubmatch(rest)[1])
		i := slices.IndexFunc(kept.releases, func(rel *release) bool { return rel.tag == tag && !rel.draft })
		if i < 0 {
			reply(w, http.StatusNotFound, message("Not Found"))
			break
		}
		reply(w, http.StatusOK, s.releaseJSON(kept, kept.releases[i]))
	case get && rest == releasesRest:
		newest := make([]releaseJSON, 0, len(kept.releases))
		for _, rel := range slices.Backward(kept.releases) {
			newest = append(newest, s.releaseJSON(kept, rel))
		}
		reply(w, http.StatusOK, page(r.URL.Query(), newest))
	case r.Method == http.MethodPatch && releasePath.MatchString(rest):
		s.patchRelease(w, r, kept, releasePath.FindStringSubmatch(rest)[1])
	case get && assetPath.MatchString(rest):
		s.getAsset(w, r, kept, assetPath.FindStringSubmatch(rest)[1])
	case get && zipballPath.MatchString(rest):
		s.zipball(w, kept, zipballPath.FindStringSubmatch(rest)[1])
	default:
		return false
	}
	return true
}

// resolve returns the commit a ref of a path names: a SHA, a branch, a tag,
// heads/<branch> or tags/<tag>.
func (s *Server) resolve(kept *repo, ref string) (string, bool) {
	ref, _ = url.PathUnescape(ref)
	for _, full := range []string{"refs/" + ref, "refs/heads/" + ref, "refs/tags/" + ref, ref} {
		if sha, _, ok := s.commit(kept.owner, kept.name, full); ok {
			return sha, true
		}
	}
	return "", false
}

// latest returns the newest status of each context, in the order the
// contexts first reported.
func latest(statuses []commitStatus) []commitStatus {
	var out []commitStatus
	for _, st := range statuses {
		if i := slices.IndexFunc(out, func(o commitStatus) bool { return o.Context == st.Context }); i >= 0 {
			out[i] = st
		} else {
			out = append(out, st)
		}
	}
	return out
}

func (s *Server) combinedStatus(w http.ResponseWriter, r *http.Request, kept *repo, ref string) {
	sha, ok := s.resolve(kept, ref)
	if !ok {
		reply(w, http.StatusUnprocessableEntity, message("No commit found for SHA: "+ref))
		return
	}
	statuses := latest(kept.statuses[sha])
	// GitHub: failure if any context is error or failure, pending if any is
	// pending or there are none, success otherwise.
	state := stateSuccess
	if len(statuses) == 0 {
		state = statePending
	}
	for _, st := range statuses {
		if st.State == stateFailure || st.State == stateError {
			state = stateFailure
			break
		}
		if st.State == statePending {
			state = statePending
		}
	}
	// The state is of every context; the statuses come a page at a time.
	reply(w, http.StatusOK, struct {
		State      string         `json:"state"`
		SHA        string         `json:"sha"`
		TotalCount int            `json:"total_count"`
		Statuses   []commitStatus `json:"statuses"`
	}{state, sha, len(statuses), append([]commitStatus{}, page(r.URL.Query(), statuses)...)})
}

func (s *Server) listStatuses(w http.ResponseWriter, r *http.Request, kept *repo, ref string) {
	sha, ok := s.resolve(kept, ref)
	if !ok {
		reply(w, http.StatusUnprocessableEntity, message("No commit found for SHA: "+ref))
		return
	}
	newest := slices.Clone(kept.statuses[sha])
	slices.Reverse(newest)
	reply(w, http.StatusOK, page(r.URL.Query(), newest))
}

func (s *Server) listCheckRuns(w http.ResponseWriter, r *http.Request, kept *repo, ref string) {
	sha, ok := s.resolve(kept, ref)
	if !ok {
		reply(w, http.StatusUnprocessableEntity, message("No commit found for SHA: "+ref))
		return
	}
	// filter is latest unless it says all: the newest run of each name.
	runs := kept.checkRuns[sha]
	if r.URL.Query().Get("filter") != listAll {
		var newest []checkRun
		for _, run := range runs {
			if i := slices.IndexFunc(newest, func(o checkRun) bool { return o.Name == run.Name }); i >= 0 {
				newest[i] = run
			} else {
				newest = append(newest, run)
			}
		}
		runs = newest
	}
	reply(w, http.StatusOK, struct {
		TotalCount int        `json:"total_count"`
		CheckRuns  []checkRun `json:"check_runs"`
	}{len(runs), page(r.URL.Query(), append([]checkRun{}, runs...))})
}

func (s *Server) setStatus(w http.ResponseWriter, r *http.Request, kept *repo, sha string) {
	var body commitStatus
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		!slices.Contains([]string{stateError, stateFailure, statePending, stateSuccess}, body.State) {
		reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
		return
	}
	if utf8.RuneCountInString(body.Description) > 140 {
		reply(w, http.StatusUnprocessableEntity,
			message("Validation Failed: description is too long (maximum is 140 characters)"))
		return
	}
	if _, err := s.gitIn(kept.owner, kept.name, "cat-file", "-e", sha+"^{commit}"); err != nil {
		reply(w, http.StatusUnprocessableEntity, message("No commit found for SHA: "+sha))
		return
	}
	if body.Context == "" {
		body.Context = "default"
	}
	kept.statuses[sha] = append(kept.statuses[sha], body)
	s.emit(kept, s.acting, eventStatus, func() eventBody {
		return eventBody{statusBody: &statusBody{SHA: sha, Name: kept.owner + "/" + kept.name, commitStatus: body,
			Branches: s.holding(kept, sha)}}
	})
	reply(w, http.StatusCreated, body)
}

// administers answers 403 unless the call is New's user's, and says whether
// it is: a ruleset is an administrator's to write.
func (s *Server) administers(w http.ResponseWriter) bool {
	if s.acting != s.admin {
		reply(w, http.StatusForbidden, message("Resource not accessible by personal access token"))
		return false
	}
	return true
}

// The rules and the bypass actors of a ruleset GitHub knows, of those the
// adapter may send.
var (
	ruleTypes  = []string{ruleCreation, ruleUpdate, ruleDeletion, ruleNonFastForward, "pull_request", "required_status_checks"}
	actorTypes = []string{"RepositoryRole", "OrganizationAdmin", "Integration", "Team", "DeployKey"}
)

func (s *Server) rulesetsAPI(w http.ResponseWriter, r *http.Request, kept *repo, rest string) {
	find := func() *Ruleset {
		id, _ := strconv.ParseInt(rulesetPath.FindStringSubmatch(rest)[1], 10, 64)
		if i := slices.IndexFunc(kept.rulesets, func(rs *Ruleset) bool { return rs.ID == id }); i >= 0 {
			return kept.rulesets[i]
		}
		reply(w, http.StatusNotFound, message("Not Found"))
		return nil
	}
	read := func() (*Ruleset, bool) {
		var rs Ruleset
		if err := json.NewDecoder(r.Body).Decode(&rs); err != nil || rs.Name == "" ||
			!slices.Contains([]string{"branch", "tag"}, rs.Target) ||
			!slices.Contains([]string{enforced, "disabled", "evaluate"}, rs.Enforcement) {
			reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
			return nil, false
		}
		for _, rule := range rs.Rules {
			if !slices.Contains(ruleTypes, rule.Type) {
				reply(w, http.StatusUnprocessableEntity, message("Invalid rule '"+rule.Type+"'"))
				return nil, false
			}
		}
		for _, a := range rs.BypassActors {
			if !slices.Contains(actorTypes, a.ActorType) || !slices.Contains([]string{bypassAlways, bypassByPull}, a.BypassMode) {
				reply(w, http.StatusUnprocessableEntity, message("Invalid bypass actor"))
				return nil, false
			}
		}
		return &rs, true
	}
	switch {
	case rest == rulesetsRest && r.Method == http.MethodGet:
		// The list has no rules, conditions or actors: a ruleset is read
		// whole one at a time.
		type listed struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			Target      string `json:"target"`
			Enforcement string `json:"enforcement"`
		}
		all := make([]listed, 0, len(kept.rulesets))
		for _, rs := range kept.rulesets {
			all = append(all, listed{rs.ID, rs.Name, rs.Target, rs.Enforcement})
		}
		reply(w, http.StatusOK, page(r.URL.Query(), all))
	case rest == rulesetsRest && r.Method == http.MethodPost:
		if !s.administers(w) {
			return
		}
		rs, ok := read()
		if !ok {
			return
		}
		if slices.ContainsFunc(kept.rulesets, func(o *Ruleset) bool { return o.Name == rs.Name }) {
			reply(w, http.StatusUnprocessableEntity, message("Validation Failed: Name must be unique"))
			return
		}
		s.nextID++
		rs.ID = s.nextID
		kept.rulesets = append(kept.rulesets, rs)
		reply(w, http.StatusCreated, rs)
	case rulesetPath.MatchString(rest) && r.Method == http.MethodGet:
		if rs := find(); rs != nil {
			reply(w, http.StatusOK, rs)
		}
	case rulesetPath.MatchString(rest) && r.Method == http.MethodPut:
		if !s.administers(w) {
			return
		}
		old := find()
		if old == nil {
			return
		}
		rs, ok := read()
		if !ok {
			return
		}
		rs.ID = old.ID
		*old = *rs
		reply(w, http.StatusOK, old)
	default:
		s.notServed(w, r)
	}
}

func (s *Server) variablesAPI(w http.ResponseWriter, r *http.Request, kept *repo, rest string) {
	type variable struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	read := func() (variable, bool) {
		var v variable
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
			return v, false
		}
		v.Name = strings.ToUpper(v.Name)
		return v, true
	}
	switch {
	case rest == variablesRest && r.Method == http.MethodPost:
		v, ok := read()
		if !ok {
			return
		}
		if !variableName.MatchString(v.Name) || strings.HasPrefix(v.Name, "GITHUB_") {
			reply(w, http.StatusUnprocessableEntity, message("Invalid variable name"))
			return
		}
		if _, exists := kept.variables[v.Name]; exists {
			reply(w, http.StatusConflict, message("Already exists - Variable already exists"))
			return
		}
		kept.variables[v.Name] = v.Value
		reply(w, http.StatusCreated, nil)
	case variablePath.MatchString(rest):
		name := strings.ToUpper(variablePath.FindStringSubmatch(rest)[1])
		value, exists := kept.variables[name]
		switch {
		case r.Method == http.MethodGet && exists:
			reply(w, http.StatusOK, variable{name, value})
		case r.Method == http.MethodGet:
			reply(w, http.StatusNotFound, message("Not Found"))
		case r.Method == http.MethodPatch:
			v, ok := read()
			if !ok {
				return
			}
			if !exists {
				reply(w, http.StatusNotFound, message("Not Found"))
				return
			}
			kept.variables[name] = v.Value
			reply(w, http.StatusNoContent, nil)
		case r.Method == http.MethodDelete && exists:
			delete(kept.variables, name)
			reply(w, http.StatusNoContent, nil)
		case r.Method == http.MethodDelete:
			reply(w, http.StatusNotFound, message("Not Found"))
		default:
			s.notServed(w, r)
		}
	default:
		s.notServed(w, r)
	}
}

// releaseJSON and assetJSON are a release and its asset as GitHub answers
// them.
type releaseJSON struct {
	ID      int64       `json:"id"`
	TagName string      `json:"tag_name"`
	Name    string      `json:"name"`
	Body    string      `json:"body"`
	Draft   bool        `json:"draft"`
	HTMLURL string      `json:"html_url"`
	Assets  []assetJSON `json:"assets"`
}

type assetJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Size int    `json:"size"`
}

func (a *asset) json() assetJSON { return assetJSON{a.id, a.name, len(a.content)} }

func (s *Server) releaseJSON(kept *repo, rel *release) releaseJSON {
	assets := make([]assetJSON, 0, len(rel.assets))
	for _, a := range rel.assets {
		assets = append(assets, a.json())
	}
	return releaseJSON{rel.id, rel.tag, rel.name, rel.body, rel.draft,
		s.srv.URL + "/" + kept.owner + "/" + kept.name + "/releases/tag/" + rel.tag, assets}
}

func (s *Server) patchRelease(w http.ResponseWriter, r *http.Request, kept *repo, id string) {
	n, _ := strconv.ParseInt(id, 10, 64)
	i := slices.IndexFunc(kept.releases, func(rel *release) bool { return rel.id == n })
	if i < 0 {
		reply(w, http.StatusNotFound, message("Not Found"))
		return
	}
	var body struct {
		Body *string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
		return
	}
	if body.Body != nil {
		kept.releases[i].body = *body.Body
	}
	reply(w, http.StatusOK, s.releaseJSON(kept, kept.releases[i]))
}

func (s *Server) getAsset(w http.ResponseWriter, r *http.Request, kept *repo, id string) {
	n, _ := strconv.ParseInt(id, 10, 64)
	for _, rel := range kept.releases {
		for _, a := range rel.assets {
			if a.id != n {
				continue
			}
			// The content comes only when asked for as a binary; else the
			// asset's description does.
			if r.Header.Get("Accept") != "application/octet-stream" {
				reply(w, http.StatusOK, a.json())
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", strconv.Itoa(len(a.content)))
			_, _ = w.Write(a.content)
			return
		}
	}
	reply(w, http.StatusNotFound, message("Not Found"))
}

// zipball answers git archive's zip of ref, its files below one top
// directory named after the repository and the commit, as GitHub's is.
func (s *Server) zipball(w http.ResponseWriter, kept *repo, ref string) {
	sha, ok := s.resolve(kept, ref)
	if !ok {
		reply(w, http.StatusNotFound, message("Not Found"))
		return
	}
	prefix := fmt.Sprintf("%s-%s-%s/", kept.owner, kept.name, sha[:7])
	//nolint:gosec // a test fake running git on its own temporary repositories
	cmd := exec.Command("git", "-C", s.dir(kept.owner, kept.name), "archive", "--format=zip", "--prefix="+prefix, sha)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		reply(w, http.StatusInternalServerError, message("git archive: "+err.Error()+": "+stderr.String()))
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	_, _ = w.Write(out.Bytes())
}
