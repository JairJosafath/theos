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

package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/JairJosafath/theos/internal/adapters/git"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// The GitHub side of the GitHost port (docs/plans/github-host.md). Step 1
// (#74) is here: repositories, files, branches and tags. Step 2 (#75) is the
// rest of the port: pull requests, reviews and merges in
// github_host_pulls.go, branch and tag rules in github_host_rules.go,
// statuses in github_host_status.go, releases and the source at a tag in
// github_host_releases.go. The Issues port, step 3 (#76), is in
// github_host_issues.go, AgentAccess, step 4 (#77), in
// github_host_agents.go, CI, step 5 (#78), in github_host_ci.go, Events,
// step 6 (#79), in github_host_events.go, and the Registry, step 7 (#80),
// in github_host_registry.go. No GitProvider reaches this before step 8.

// GitHubWebURL is github.com, where a GitProvider's spec.url means the public
// GitHub; any other URL is a GitHub Enterprise Server.
const GitHubWebURL = "https://github.com"

// gitUsername is the user name GitHub takes with a token as the password
// over Git: a personal token's and an installation token's alike.
const gitUsername = "x-access-token"

// host is the GitHost of one owner, an organization or the token's user, on
// github.com or a GitHub Enterprise Server.
type host struct {
	// api is the REST API's root; web is where repositories are browsed and
	// cloned from.
	api, web *url.URL
	owner    string
	creds    ports.Credentials
	http     *http.Client
	// agents is the App that mints the agents' credentials, nil without
	// one; noAgents says why the provider has none it can use.
	agents   *agentApp
	noAgents *ports.AgentAccessUnavailable
	now      func() time.Time
	// What github_host_registry.go keeps: where the owner's packages are,
	// what the GitProvider set of them, and the user name the registries
	// take with the token.
	places   packagePlaces
	packages ports.Packages
	login    string
}

// Host is the part of ports.Provider the GitHub adapter has so far.
type Host interface {
	ports.GitHost
	ports.Issues
	ports.AgentAccess
	ports.AgentAccessChecker
	ports.CI
	ports.Events
	ports.Registry
}

// GitHost returns the GitHost of cfg's owner, as Host does.
func (c Client) GitHost(cfg ports.Config) (ports.GitHost, error) { return c.Host(cfg) }

// Host returns the GitHost, the Issues and the AgentAccess of cfg's owner.
// cfg.URL is https://github.com, whose API is api.github.com, or a GitHub
// Enterprise Server's address, whose API is under /api/v3. cfg.Credentials
// holds the operator's token, of an account that administers the owner's
// repositories; its user name is the one GitHub takes over Git whatever it
// says. cfg.Secret names the GitHub App that mints the agents' credentials
// (appId, privateKey, and installationId where it is known); without one
// the host has no agent access, and CheckAgentAccess says so.
func (c Client) Host(cfg ports.Config) (Host, error) {
	if cfg.Owner == "" {
		return nil, errors.New("github: owner is required")
	}
	if cfg.Credentials.Token == "" {
		return nil, errors.New("github: token is required")
	}
	web, err := githubURL(cfg.URL, GitHubWebURL)
	if err != nil {
		return nil, err
	}
	api, err := githubURL(GitHubAPIURL, GitHubAPIURL)
	if err != nil {
		return nil, err
	}
	if web.String() != GitHubWebURL {
		api = web.JoinPath("api", "v3")
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{}
	}
	places, err := placesOf(web, cfg.Owner, cfg.Packages)
	if err != nil {
		return nil, err
	}
	agents, noAgents := agentsOf(cfg.Secret)
	return &host{api: api, web: web, owner: cfg.Owner, http: client, agents: agents, noAgents: noAgents, now: c.now,
		creds:  ports.Credentials{Username: gitUsername, Token: cfg.Credentials.Token},
		places: places, packages: cfg.Packages, login: orDefault(cfg.Credentials.Username, gitUsername)}, nil
}

// Repository returns the repository's addresses, and where the owner's
// packages are, without asking GitHub. GitHub has no Go module proxy: it
// serves a module from its repository, so the owner's modules are the
// paths below GoPrivate. It has no Python index but the one the
// GitProvider names.
func (h *host) Repository(name string) ports.Repository {
	at := h.web.JoinPath(h.owner, name).String()
	return ports.Repository{Owner: h.owner, Name: name, CloneURL: at + ".git", WebURL: at,
		GoPrivate:     h.web.Hostname() + "/" + h.owner,
		ImageRegistry: h.places.images + "/" + strings.ToLower(h.owner),
		NpmScope:      h.places.npmScope, NpmRegistry: h.places.npm,
		PyPIIndex: h.packages.PyPIIndex, PyPIUpload: h.packages.PyPIUpload}
}

func (h *host) repoPath(repo ports.Repository) string {
	return "/repos/" + url.PathEscape(repo.Owner) + "/" + url.PathEscape(repo.Name)
}

// do sends one API call as the operator and returns its status and body.
func (h *host) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	return h.doAs(ctx, h.creds.Token, method, path, body)
}

// doAs sends one API call with token as its bearer: the operator's own, an
// App's JSON Web Token, or an installation token.
func (h *host) doAs(ctx context.Context, token, method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.api.String()+path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("github: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("github: %s %s: %w", method, path, err)
	}
	return resp.StatusCode, data, nil
}

// failed is the error of an API call that answered status.
func failed(what string, status int, data []byte) error {
	var body struct {
		Message string `json:"message"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	text := strings.TrimSpace(string(data))
	if json.Unmarshal(data, &body) == nil && body.Message != "" {
		text = body.Message
		for _, e := range body.Errors {
			if e.Message != "" {
				text += ": " + e.Message
			}
		}
	}
	return fmt.Errorf("github: %s failed (HTTP %d): %s", what, status, text)
}

// EnsureRepository returns the owner's repository name, making it, private
// and empty, when the owner has none: in the organization, or for an owner
// that is no organization, as the token's user's own.
func (h *host) EnsureRepository(ctx context.Context, name, description string) (ports.Repository, error) {
	repo := h.Repository(name)
	status, data, err := h.do(ctx, http.MethodGet, h.repoPath(repo), nil)
	if err != nil {
		return ports.Repository{}, err
	}
	switch status {
	case http.StatusOK:
		return repo, nil
	case http.StatusNotFound:
	default:
		return ports.Repository{}, failed("reading repository "+h.owner+"/"+name, status, data)
	}
	// No auto_init: the operator's first push is the repository's first
	// commit. Merges are squashes, and a merged branch goes with its merge.
	create := map[string]any{"name": name, "description": description, "private": true, "auto_init": false,
		"allow_squash_merge": true, "delete_branch_on_merge": true}
	status, data, err = h.do(ctx, http.MethodPost, "/orgs/"+url.PathEscape(h.owner)+"/repos", create)
	if err == nil && status == http.StatusNotFound {
		// The owner is no organization the token sees. It may be the token's
		// own account, and only then: a repository made for another owner
		// would land in the wrong account.
		login, err := h.tokenUser(ctx)
		if err != nil {
			return ports.Repository{}, err
		}
		if !strings.EqualFold(login, h.owner) {
			return ports.Repository{}, fmt.Errorf("github: %s is neither an organization the token may create "+
				"repositories in nor the token's own account, which is %s", h.owner, login)
		}
		status, data, err = h.do(ctx, http.MethodPost, "/user/repos", create)
		if err != nil {
			return ports.Repository{}, err
		}
	}
	if err != nil {
		return ports.Repository{}, err
	}
	if status != http.StatusCreated {
		return ports.Repository{}, failed("creating repository "+h.owner+"/"+name, status, data)
	}
	return repo, nil
}

// tokenUser returns the login of the account the token is of.
func (h *host) tokenUser(ctx context.Context) (string, error) {
	status, data, err := h.do(ctx, http.MethodGet, "/user", nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", failed("reading the token's account", status, data)
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(data, &user); err != nil || user.Login == "" {
		return "", errors.New("github: reading the token's account: no login in the answer")
	}
	return user.Login, nil
}

// History returns the default branch's newest commits over Git.
func (h *host) History(ctx context.Context, repo ports.Repository, limit int) ([]ports.Commit, error) {
	return git.History(ctx, repo.CloneURL, &h.creds, limit)
}

// PushInitialCommit pushes files as the repository's first commit, on main.
func (h *host) PushInitialCommit(ctx context.Context, repo ports.Repository, files scaffold.Tree, message string,
	author ports.Signature) (string, error) {
	return git.PushInitialCommit(ctx, repo.CloneURL, &h.creds, files, message, author)
}

// ReadFiles reads paths from branch over Git, at the commit it is at now.
func (h *host) ReadFiles(ctx context.Context, repo ports.Repository, branch string, paths []string) (scaffold.Tree,
	string, error) {
	return git.ReadFiles(ctx, repo.CloneURL, &h.creds, branch, paths)
}

// PushBranch builds one commit on base over Git and force-pushes it as branch.
func (h *host) PushBranch(ctx context.Context, repo ports.Repository, base, branch string, files scaffold.Tree,
	removes []string, message string, author ports.Signature) (string, error) {
	return git.PushBranch(ctx, repo.CloneURL, &h.creds, base, branch, files, removes, message, author)
}

// PushDefaultBranch builds one commit on base over Git and pushes it to the
// default branch without force.
func (h *host) PushDefaultBranch(ctx context.Context, repo ports.Repository, base string, files scaffold.Tree,
	removes []string, message string, author ports.Signature) (string, error) {
	return git.PushDefaultBranch(ctx, repo.CloneURL, &h.creds, base, files, removes, message, author)
}

// commitOf returns the commit ref names (a SHA, heads/<branch> or
// tags/<tag>, an annotated tag peeled) and its tree, or "" for a ref the
// repository doesn't have or a repository with no commit.
func (h *host) commitOf(ctx context.Context, repo ports.Repository, ref string) (sha, tree string, err error) {
	status, data, err := h.do(ctx, http.MethodGet, h.repoPath(repo)+"/commits/"+pathEscapeRef(ref), nil)
	if err != nil {
		return "", "", err
	}
	switch status {
	case http.StatusOK:
	// 404 and 422: no such ref ("No commit found for SHA"); 409: the
	// repository is empty.
	case http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusConflict:
		return "", "", nil
	default:
		return "", "", failed("reading "+ref+" of "+repo.Owner+"/"+repo.Name, status, data)
	}
	var body struct {
		SHA    string `json:"sha"`
		Commit struct {
			Tree struct {
				SHA string `json:"sha"`
			} `json:"tree"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(data, &body); err != nil || body.SHA == "" {
		return "", "", fmt.Errorf("github: reading %s of %s/%s: no commit in the answer", ref, repo.Owner, repo.Name)
	}
	return body.SHA, body.Commit.Tree.SHA, nil
}

// pathEscapeRef escapes a ref for a path, keeping the slashes of a branch
// such as task/a, which GitHub reads as part of the name.
func pathEscapeRef(ref string) string {
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// HasBranch reports whether the repository has branch.
func (h *host) HasBranch(ctx context.Context, repo ports.Repository, branch string) (bool, error) {
	sha, err := h.BranchCommit(ctx, repo, branch)
	return sha != "", err
}

// BranchCommit returns the commit branch is at, or "" without the branch.
func (h *host) BranchCommit(ctx context.Context, repo ports.Repository, branch string) (string, error) {
	sha, _, err := h.commitOf(ctx, repo, "heads/"+branch)
	return sha, err
}

// CommitTree returns the tree of commit sha.
func (h *host) CommitTree(ctx context.Context, repo ports.Repository, sha string) (string, error) {
	got, tree, err := h.commitOf(ctx, repo, sha)
	if err != nil {
		return "", err
	}
	if got == "" {
		return "", fmt.Errorf("github: %s/%s has no commit %s", repo.Owner, repo.Name, sha)
	}
	return tree, nil
}

// CreateBranch makes branch at commit; a branch that exists is an error.
func (h *host) CreateBranch(ctx context.Context, repo ports.Repository, branch, commit string) error {
	return h.createRef(ctx, repo, "refs/heads/"+branch, commit)
}

func (h *host) createRef(ctx context.Context, repo ports.Repository, ref, sha string) error {
	status, data, err := h.do(ctx, http.MethodPost, h.repoPath(repo)+"/git/refs", newRef{Ref: ref, SHA: sha})
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return failed("creating "+ref+" in "+repo.Owner+"/"+repo.Name, status, data)
	}
	return nil
}

// newRef is the body of POST /git/refs.
type newRef struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

// DeleteBranch deletes branch; one that is gone already is no error.
func (h *host) DeleteBranch(ctx context.Context, repo ports.Repository, branch string) error {
	status, data, err := h.do(ctx, http.MethodDelete, h.repoPath(repo)+"/git/refs/heads/"+pathEscapeRef(branch), nil)
	if err != nil {
		return err
	}
	switch {
	// 404 is a repository that is gone. 422 is "Reference does not exist",
	// and also a ruleset's refusal, which is no branch gone (#77).
	case status == http.StatusNoContent, status == http.StatusNotFound,
		status == http.StatusUnprocessableEntity && strings.Contains(string(data), "Reference does not exist"):
		return nil
	}
	return failed("deleting branch "+branch+" of "+repo.Owner+"/"+repo.Name, status, data)
}

// GetTag returns the commit tag names, or "" without the tag.
func (h *host) GetTag(ctx context.Context, repo ports.Repository, tag string) (string, error) {
	sha, _, err := h.commitOf(ctx, repo, "tags/"+tag)
	return sha, err
}

// CreateTag makes the annotated tag at commit, with message; a tag that
// exists is an error, and is never moved.
func (h *host) CreateTag(ctx context.Context, repo ports.Repository, tag, commit, message string) error {
	if existing, err := h.GetTag(ctx, repo, tag); err != nil {
		return err
	} else if existing != "" {
		return fmt.Errorf("github: %s/%s already has tag %s, at %s", repo.Owner, repo.Name, tag, existing)
	}
	status, data, err := h.do(ctx, http.MethodPost, h.repoPath(repo)+"/git/tags",
		map[string]string{"tag": tag, "message": message, "object": commit, "type": "commit"})
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return failed("creating the tag object "+tag+" in "+repo.Owner+"/"+repo.Name, status, data)
	}
	var object struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(data, &object); err != nil || object.SHA == "" {
		return fmt.Errorf("github: creating the tag object %s: no sha in the answer", tag)
	}
	return h.createRef(ctx, repo, "refs/tags/"+tag, object.SHA)
}
