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

// Package githubtest is a fake GitHub over httptest, for the GitHub adapter's
// side of the Git host ports (docs/plans/github-host.md, #74). It stands in
// for a GitHub Enterprise Server: the REST API is under URL() + "/api/v3" and
// each repository's Git is at URL() + "/<owner>/<name>.git", served by Git's
// own git-http-backend from bare repositories on disk, so a push and a clone
// are real ones. The REST calls it serves, each as GitHub documents it:
//
//	GET    /repos/{owner}/{repo}                 a repository, 404 without it
//	POST   /orgs/{org}/repos                     make one in an organization; 404 for an owner that is none, 422 when it exists
//	GET    /user                                 the account the token is of
//	POST   /user/repos                           make one of the token's user
//	GET    /repos/{owner}/{repo}/branches/{b}    a branch and its commit, 404 without it
//	GET    /repos/{owner}/{repo}/commits/{ref}   the commit a SHA, heads/<b> or tags/<t> names, an annotated tag's too;
//	                                             409 for an empty repository, 422 for a ref it doesn't have
//	POST   /repos/{owner}/{repo}/git/refs        make a ref at a commit; 422 when it exists
//	DELETE /repos/{owner}/{repo}/git/refs/{ref}  delete a ref; 422 when it doesn't exist
//	POST   /repos/{owner}/{repo}/git/tags        make an annotated tag object
//
// in pulls.go, pull requests with their files, commits, reviews, merges and
// update-branch, and the comparison of two refs; in host.go, commit statuses
// and check runs, repository rulesets, Actions variables, releases with
// their assets and the zip of a ref (#75), each file naming its calls and
// what they answer;
//
// and, in issues.go, labels, issues with their comments, assignees and
// labels, milestones and the dependencies of an issue (blocked_by), as
// /rest/issues documents them: a list answers 100 a page at most, pull
// requests are none of its business, a label's name matches without case,
// and an issue closes whatever it depends on.
//
// https://docs.github.com/en/rest/repos/repos, /rest/users/users, /rest/branches/branches,
// /rest/commits/commits, /rest/git/refs and /rest/git/tags. Every call needs
// the token New was given, or one AddUser made, as a Bearer token for the API
// and as the password for Git. New's user administers every repository;
// AddUser's may write to them and no more, as an agent's token does. What it doesn't serve answers 404, and Unserved lists those calls,
// so a test sees an endpoint the adapter grew that the fake lacks.
package githubtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cgi" //nolint:gosec // a test fake on Go 1.26, long past the 1.6.3 Httpoxy fix
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// Server is a fake GitHub instance.
type Server struct {
	srv *httptest.Server
	// tokens are the tokens the instance takes, each with its user's login;
	// admin is New's user, and acting the user of the call being served.
	tokens map[string]string
	admin  string
	acting string
	// root holds the bare repositories, as <owner>/<name>.git.
	root string
	git  http.Handler

	mu sync.Mutex
	// orgs are the organizations.
	orgs map[string]bool
	// repos holds each repository by owner/name; nextID numbers the issues
	// across them, as GitHub's ids do.
	repos    map[string]*repo
	nextID   int64
	unserved []string
}

// repo is what the fake keeps of a repository beside its Git.
type repo struct {
	owner, name string
	description string
	labels      []*label
	issues      []*issue
	milestones  []*milestone
	// What host.go and pulls.go keep: the pull requests by their number, the
	// statuses and check runs of each commit, oldest first, the rulesets, the
	// Actions variables and the releases.
	pulls     map[int]*pull
	statuses  map[string][]commitStatus
	checkRuns map[string][]checkRun
	rulesets  []*Ruleset
	variables map[string]string
	releases  []*release
}

// New starts a fake GitHub that takes token, whose user is user. It skips the
// test where Git has no git-http-backend.
func New(t *testing.T, user, token string) *Server {
	t.Helper()
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skipf("no git on PATH: %v", err)
	}
	backend := filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skipf("git has no git-http-backend: %v", err)
	}
	s := &Server{tokens: map[string]string{token: user}, admin: user, root: t.TempDir(), orgs: map[string]bool{},
		repos: map[string]*repo{}}
	s.git = &cgi.Handler{Path: backend, Env: []string{"GIT_PROJECT_ROOT=" + s.root, "GIT_HTTP_EXPORT_ALL=1"}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the instance's address: the adapter's spec.url.
func (s *Server) URL() string { return s.srv.URL }

// AddUser makes another account, with a token of its own: it may read and
// write every repository, as a collaborator with write access or an agent's
// token does, and administers none.
func (s *Server) AddUser(login, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token] = login
}

// AddOrg makes an organization the token's user may make repositories in.
func (s *Server) AddOrg(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orgs[name] = true
}

// Unserved lists the calls the fake answered 404 because it has no such
// endpoint, as "METHOD /path".
func (s *Server) Unserved() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.unserved...)
}

var (
	gitPath    = regexp.MustCompile(`^/([^/]+)/([^/]+)\.git/`)
	repoPath   = regexp.MustCompile(`^/api/v3/repos/([^/]+)/([^/]+)(/.*)?$`)
	orgRepos   = regexp.MustCompile(`^/api/v3/orgs/([^/]+)/repos$`)
	branchPath = regexp.MustCompile(`^/branches/(.+)$`)
	commitPath = regexp.MustCompile(`^/commits/(.+)$`)
	refPath    = regexp.MustCompile(`^/git/refs/(.+)$`)
)

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if m := gitPath.FindStringSubmatch(r.URL.Path); m != nil {
		_, password, ok := r.BasicAuth()
		s.mu.Lock()
		_, known := s.tokens[password]
		s.mu.Unlock()
		if !ok || !known {
			w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
			http.Error(w, "Bad credentials", http.StatusUnauthorized)
			return
		}
		s.git.ServeHTTP(w, r)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user, known := s.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	if !known || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		reply(w, http.StatusUnauthorized, message("Bad credentials"))
		return
	}
	s.acting = user
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/user":
		reply(w, http.StatusOK, struct {
			Login string `json:"login"`
		}{s.acting})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v3/user/repos":
		s.createRepo(w, r, s.acting)
	case r.Method == http.MethodPost && orgRepos.MatchString(r.URL.Path):
		org := orgRepos.FindStringSubmatch(r.URL.Path)[1]
		if !s.orgs[org] {
			reply(w, http.StatusNotFound, message("Not Found"))
			return
		}
		s.createRepo(w, r, org)
	case repoPath.MatchString(r.URL.Path):
		m := repoPath.FindStringSubmatch(r.URL.Path)
		s.repo(w, r, m[1], m[2], m[3])
	default:
		s.notServed(w, r)
	}
}

func (s *Server) notServed(w http.ResponseWriter, r *http.Request) {
	s.unserved = append(s.unserved, r.Method+" "+r.URL.Path)
	reply(w, http.StatusNotFound, message("Not Found"))
}

// apiError is GitHub's error body.
type apiError struct {
	Message string `json:"message"`
}

func message(text string) apiError { return apiError{Message: text} }

// repository, commitRef and gitRef are the parts of GitHub's answers the
// adapter reads.
type repository struct {
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	Description   string `json:"description"`
	CloneURL      string `json:"clone_url"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

type object struct {
	SHA string `json:"sha"`
}

type commitRef struct {
	SHA    string `json:"sha"`
	Commit struct {
		Tree object `json:"tree"`
	} `json:"commit"`
}

type gitRef struct {
	Ref    string `json:"ref"`
	Object object `json:"object"`
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func (s *Server) repoJSON(owner, name string) repository {
	at := s.srv.URL + "/" + owner + "/" + name
	return repository{Name: name, FullName: owner + "/" + name, Description: s.repos[owner+"/"+name].description,
		CloneURL: at + ".git", HTMLURL: at, DefaultBranch: "main", Private: true}
}

func (s *Server) dir(owner, name string) string { return filepath.Join(s.root, owner, name+".git") }

// git runs git in a repository's directory and returns its trimmed output.
func (s *Server) gitIn(owner, name string, args ...string) (string, error) {
	//nolint:gosec // a test fake running git on its own temporary repositories
	cmd := exec.Command("git", append([]string{"-C", s.dir(owner, name)}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_NAME=github", "GIT_COMMITTER_EMAIL=noreply@github.test",
		"GIT_AUTHOR_NAME=github", "GIT_AUTHOR_EMAIL=noreply@github.test",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func (s *Server) createRepo(w http.ResponseWriter, r *http.Request, owner string) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		AutoInit    bool   `json:"auto_init"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
		return
	}
	key := owner + "/" + body.Name
	if _, ok := s.repos[key]; ok {
		reply(w, http.StatusUnprocessableEntity, struct {
			apiError
			Errors []apiError `json:"errors"`
		}{message("Repository creation failed."), []apiError{message("name already exists on this account")}})
		return
	}
	dir := s.dir(owner, body.Name)
	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		reply(w, http.StatusInternalServerError, message(err.Error()))
		return
	}
	// A push over HTTP needs http.receivepack; the fake checks the token itself.
	for _, args := range [][]string{{"init", "--quiet", "--bare", "--initial-branch=main", dir},
		{"-C", dir, "config", "http.receivepack", "true"}} {
		//nolint:gosec // a test fake running git on its own temporary repositories
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			reply(w, http.StatusInternalServerError, message(fmt.Sprintf("git %v: %v: %s", args, err, out)))
			return
		}
	}
	s.repos[key] = &repo{owner: owner, name: body.Name, description: body.Description, pulls: map[int]*pull{},
		statuses: map[string][]commitStatus{}, checkRuns: map[string][]checkRun{}, variables: map[string]string{}}
	reply(w, http.StatusCreated, s.repoJSON(owner, body.Name))
}

// empty says whether a repository has no commit on any ref.
func (s *Server) empty(owner, name string) bool {
	out, err := s.gitIn(owner, name, "for-each-ref", "--count=1")
	return err == nil && out == ""
}

// commit resolves ref to the commit it names, peeling an annotated tag.
func (s *Server) commit(owner, name, ref string) (sha, tree string, ok bool) {
	sha, err := s.gitIn(owner, name, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", "", false
	}
	tree, err = s.gitIn(owner, name, "rev-parse", sha+"^{tree}")
	return sha, tree, err == nil
}

func (s *Server) repo(w http.ResponseWriter, r *http.Request, owner, name, rest string) {
	kept, ok := s.repos[owner+"/"+name]
	if !ok {
		reply(w, http.StatusNotFound, message("Not Found"))
		return
	}
	if s.hostAPI(w, r, kept, rest) || s.pullsAPI(w, r, kept, rest) || s.issuesAPI(w, r, kept, rest) {
		return
	}
	switch {
	case rest == "" && r.Method == http.MethodGet:
		reply(w, http.StatusOK, s.repoJSON(owner, name))
	case r.Method == http.MethodGet && branchPath.MatchString(rest):
		s.getBranch(w, owner, name, branchPath.FindStringSubmatch(rest)[1])
	case r.Method == http.MethodGet && commitPath.MatchString(rest):
		s.getCommit(w, owner, name, commitPath.FindStringSubmatch(rest)[1])
	case r.Method == http.MethodPost && rest == "/git/refs":
		s.createRef(w, r, owner, name)
	case r.Method == http.MethodDelete && refPath.MatchString(rest):
		s.deleteRef(w, owner, name, "refs/"+refPath.FindStringSubmatch(rest)[1])
	case r.Method == http.MethodPost && rest == "/git/tags":
		s.createTag(w, r, owner, name)
	default:
		s.notServed(w, r)
	}
}

func (s *Server) getBranch(w http.ResponseWriter, owner, name, branch string) {
	sha, _, ok := s.commit(owner, name, "refs/heads/"+branch)
	if !ok {
		reply(w, http.StatusNotFound, message("Branch not found"))
		return
	}
	reply(w, http.StatusOK, struct {
		Name   string `json:"name"`
		Commit object `json:"commit"`
	}{branch, object{SHA: sha}})
}

func (s *Server) getCommit(w http.ResponseWriter, owner, name, ref string) {
	if s.empty(owner, name) {
		reply(w, http.StatusConflict, message("Git Repository is empty."))
		return
	}
	full := ref
	if strings.HasPrefix(ref, "heads/") || strings.HasPrefix(ref, "tags/") {
		full = "refs/" + ref
	}
	sha, tree, ok := s.commit(owner, name, full)
	if !ok {
		reply(w, http.StatusUnprocessableEntity, message("No commit found for SHA: "+ref))
		return
	}
	answer := commitRef{SHA: sha}
	answer.Commit.Tree.SHA = tree
	reply(w, http.StatusOK, answer)
}

func (s *Server) createRef(w http.ResponseWriter, r *http.Request, owner, name string) {
	var body struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !strings.HasPrefix(body.Ref, "refs/") {
		reply(w, http.StatusUnprocessableEntity, message("Reference name must start with 'refs/'"))
		return
	}
	if _, err := s.gitIn(owner, name, "rev-parse", "--verify", "--quiet", body.Ref); err == nil {
		reply(w, http.StatusUnprocessableEntity, message("Reference already exists"))
		return
	}
	if _, err := s.gitIn(owner, name, "cat-file", "-e", body.SHA); err != nil {
		reply(w, http.StatusUnprocessableEntity, message("Object does not exist"))
		return
	}
	if _, err := s.gitIn(owner, name, "update-ref", body.Ref, body.SHA); err != nil {
		reply(w, http.StatusUnprocessableEntity, message(err.Error()))
		return
	}
	reply(w, http.StatusCreated, gitRef{Ref: body.Ref, Object: object{SHA: body.SHA}})
}

func (s *Server) deleteRef(w http.ResponseWriter, owner, name, ref string) {
	if _, err := s.gitIn(owner, name, "rev-parse", "--verify", "--quiet", ref); err != nil {
		reply(w, http.StatusUnprocessableEntity, message("Reference does not exist"))
		return
	}
	if _, err := s.gitIn(owner, name, "update-ref", "-d", ref); err != nil {
		reply(w, http.StatusInternalServerError, message(err.Error()))
		return
	}
	reply(w, http.StatusNoContent, nil)
}

// createTag makes a tag object without its ref, as the API does: git mktag
// reads one.
func (s *Server) createTag(w http.ResponseWriter, r *http.Request, owner, name string) {
	var body struct {
		Tag     string `json:"tag"`
		Message string `json:"message"`
		Object  string `json:"object"`
		Type    string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Tag == "" || body.Type != "commit" {
		reply(w, http.StatusUnprocessableEntity, message("Validation Failed"))
		return
	}
	if _, err := s.gitIn(owner, name, "cat-file", "-e", body.Object+"^{commit}"); err != nil {
		reply(w, http.StatusUnprocessableEntity, message("Object does not exist"))
		return
	}
	tag := fmt.Sprintf("object %s\ntype commit\ntag %s\ntagger github <noreply@github.test> 0 +0000\n\n%s\n",
		body.Object, body.Tag, strings.TrimRight(body.Message, "\n"))
	//nolint:gosec // a test fake running git on its own temporary repositories
	cmd := exec.Command("git", "-C", s.dir(owner, name), "mktag")
	cmd.Stdin = strings.NewReader(tag)
	out, err := cmd.Output()
	if err != nil {
		reply(w, http.StatusUnprocessableEntity, message("mktag: "+err.Error()))
		return
	}
	reply(w, http.StatusCreated, object{SHA: strings.TrimSpace(string(out))})
}
