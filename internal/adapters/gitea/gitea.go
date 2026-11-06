package gitea

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

// gitea implements every Git host port, backed by Gitea's REST API for repositories and plain Git
// over HTTP for commits.
type gitea struct {
	base  *url.URL
	owner string
	creds ports.Credentials
	http  *http.Client
	// basic, when set, signs requests with a username and password instead of the token.
	basic *ports.Credentials
	// packages is where the owner's npm and PyPI packages go; "" fields take
	// the host's own registries.
	packages ports.Packages
}

var (
	_ ports.GitHost     = (*gitea)(nil)
	_ ports.Issues      = (*gitea)(nil)
	_ ports.CI          = (*gitea)(nil)
	_ ports.AgentAccess = (*gitea)(nil)
	_ ports.Registry    = (*gitea)(nil)
)

func newGitea(cfg ports.Config, client *http.Client) (*gitea, error) {
	base, err := url.Parse(strings.TrimSuffix(cfg.URL, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("invalid Gitea URL %q", cfg.URL)
	}
	if cfg.Owner == "" {
		return nil, errors.New("gitea: owner is required")
	}
	return &gitea{base: base, owner: cfg.Owner, creds: cfg.Credentials, http: client, packages: cfg.Packages}, nil
}

type giteaRepo struct {
	Name  string `json:"name"`
	Empty bool   `json:"empty"`
}

// EnsureRepository creates the repository in the owner organization, or returns
// the existing one.
func (g *gitea) EnsureRepository(ctx context.Context, name, description string) (ports.Repository, error) {
	body, _ := json.Marshal(map[string]any{
		"name":           name,
		"description":    description,
		"private":        false,
		"auto_init":      false,
		"default_branch": ports.DefaultBranch,
	})
	status, data, err := g.do(ctx, http.MethodPost, "/api/v1/orgs/"+url.PathEscape(g.owner)+"/repos", body)
	if err != nil {
		return ports.Repository{}, err
	}
	switch status {
	case http.StatusCreated, http.StatusConflict:
		// Created now, or it already exists. Either way, read it back.
	case http.StatusNotFound:
		return ports.Repository{}, fmt.Errorf("gitea: organization %q not found at %s", g.owner, g.base)
	case http.StatusUnauthorized, http.StatusForbidden:
		return ports.Repository{}, fmt.Errorf("gitea: not allowed to create repositories in %q (HTTP %d): %s",
			g.owner, status, apiMessage(data))
	default:
		return ports.Repository{}, fmt.Errorf("gitea: creating %s/%s failed (HTTP %d): %s", g.owner, name, status, apiMessage(data))
	}

	status, data, err = g.do(ctx, http.MethodGet, "/api/v1/repos/"+url.PathEscape(g.owner)+"/"+url.PathEscape(name), nil)
	if err != nil {
		return ports.Repository{}, err
	}
	if status != http.StatusOK {
		return ports.Repository{}, fmt.Errorf("gitea: reading %s/%s failed (HTTP %d): %s", g.owner, name, status, apiMessage(data))
	}
	var repo giteaRepo
	if err := json.Unmarshal(data, &repo); err != nil {
		return ports.Repository{}, fmt.Errorf("gitea: reading %s/%s: %w", g.owner, name, err)
	}
	return g.repository(name), nil
}

// Repository returns the addresses of a repository in the owner organization,
// without calling the host.
func (g *gitea) Repository(name string) ports.Repository { return g.repository(name) }

// repository builds addresses from the configured base URL rather than trusting
// the API's, which follow the Host the request came in on.
func (g *gitea) repository(name string) ports.Repository {
	web := g.base.JoinPath(g.owner, name).String()
	registry := g.base.JoinPath("api/packages", g.owner)
	return ports.Repository{Owner: g.owner, Name: name, WebURL: web, CloneURL: web + ".git",
		GoProxy:       registry.JoinPath("go").String(),
		ImageRegistry: g.base.Host + "/" + strings.ToLower(g.owner),
		NpmScope:      orDefault(g.packages.NpmScope, ports.DefaultNpmScope),
		// npm joins a registry URL with a trailing slash and a package name.
		NpmRegistry: strings.TrimSuffix(orDefault(g.packages.NpmRegistry, registry.JoinPath("npm").String()), "/") + "/",
		PyPIIndex:   orDefault(g.packages.PyPIIndex, registry.JoinPath("pypi/simple").String()),
		PyPIUpload:  orDefault(g.packages.PyPIUpload, registry.JoinPath("pypi").String()),
	}
}

// orDefault is value, or def when value is "".
func orDefault(value, def string) string {
	if value == "" {
		return def
	}
	return value
}

// History reads the default branch over Git, which is up to date at once; the
// API's "empty" flag lags behind a push.
func (g *gitea) History(ctx context.Context, repo ports.Repository, limit int) ([]ports.Commit, error) {
	return git.History(ctx, repo.CloneURL, &g.creds, limit)
}

// PushInitialCommit pushes files as the single commit on the default branch,
// and returns once Gitea's API sees the branch: just after the push its branch
// API still takes the repository for empty (HTTP 404 "Git Repository is
// empty", or HTTP 500), so a caller's next HasBranch or CreateBranch would fail.
func (g *gitea) PushInitialCommit(ctx context.Context, repo ports.Repository, files scaffold.Tree, message string, author ports.Signature) (string, error) {
	commit, err := git.PushInitialCommit(ctx, repo.CloneURL, &g.creds, files, message, author)
	if err != nil {
		return "", err
	}
	return commit, g.awaitBranch(ctx, repo, ports.DefaultBranch)
}

// awaitBranch polls branch's API until Gitea serves it, for up to
// branchSettleTimeout.
func (g *gitea) awaitBranch(ctx context.Context, repo ports.Repository, branch string) error {
	deadline := time.Now().Add(branchSettleTimeout)
	for {
		status, data, err := g.do(ctx, http.MethodGet, branchPath(repo, branch), nil)
		if err != nil {
			return err
		}
		if status == http.StatusOK {
			return nil
		}
		if status != http.StatusNotFound && status != http.StatusInternalServerError || time.Now().After(deadline) {
			return fmt.Errorf("gitea: reading branch %s of %s after the push failed (HTTP %d): %s",
				branch, repoPath(repo), status, apiMessage(data))
		}
		if err := pause(ctx); err != nil {
			return err
		}
	}
}

// branchSettleTimeout is how long the provider waits for Gitea to catch up
// with a push or a pull request's check.
const branchSettleTimeout = 30 * time.Second

// pause waits a moment before the next try, or until ctx ends.
func pause(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(200 * time.Millisecond):
		return nil
	}
}

// ReadFiles reads paths from branch over Git, at the commit it is at now.
func (g *gitea) ReadFiles(ctx context.Context, repo ports.Repository, branch string, paths []string) (scaffold.Tree, string, error) {
	return git.ReadFiles(ctx, repo.CloneURL, &g.creds, branch, paths)
}

// PushDefaultBranch builds one commit on base over Git and pushes it to the
// default branch without force.
func (g *gitea) PushDefaultBranch(ctx context.Context, repo ports.Repository, base string, files scaffold.Tree,
	removes []string, message string, author ports.Signature) (string, error) {
	return git.PushDefaultBranch(ctx, repo.CloneURL, &g.creds, base, files, removes, message, author)
}

// PushBranch builds one commit on base over Git and force-pushes it as branch.
func (g *gitea) PushBranch(ctx context.Context, repo ports.Repository, base, branch string, files scaffold.Tree,
	removes []string, message string, author ports.Signature) (string, error) {
	return git.PushBranch(ctx, repo.CloneURL, &g.creds, base, branch, files, removes, message, author)
}

func (g *gitea) do(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := g.request(ctx, method, path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("gitea: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("gitea: %s %s: %w", method, path, err)
	}
	return resp.StatusCode, data, nil
}

// request builds a request for path on the host, signed with the provider's
// credentials.
func (g *gitea) request(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, g.base.String()+path, body)
	if err != nil {
		return nil, err
	}
	switch {
	case g.basic != nil:
		req.SetBasicAuth(g.basic.Username, g.basic.Token)
	case g.creds.Token != "":
		req.Header.Set("Authorization", "token "+g.creds.Token)
	}
	return req, nil
}

// apiMessage pulls the message out of a Gitea error body.
func apiMessage(data []byte) string {
	var body struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &body) == nil && body.Message != "" {
		return body.Message
	}
	return strings.TrimSpace(string(data))
}
