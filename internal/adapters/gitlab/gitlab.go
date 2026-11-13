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

package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// Client builds the client of a GitLab project. It is what the operator uses;
// tests substitute their own.
type Client struct {
	// HTTP is used for API calls. Defaults to a client with no overall
	// timeout, since an upload takes as long as the file is big; ctx bounds
	// each call.
	HTTP *http.Client
}

// gitlabMaxBody bounds a response read: a page of releases holds their bodies.
const gitlabMaxBody = 32 << 20

// maxFailureMessage bounds GitLab's message in a ReleaseError.
const maxFailureMessage = 300

// Reasons a GitLab call fails, besides ReasonUnauthorized and
// ReasonRepositoryNotFound, which GitLab's 401 and 403 and its 404 on the
// project are.
const (
	// ReasonGitLabError: the network, a 5xx, a rate limit or an unreadable
	// answer; trying again may work.
	ReasonGitLabError = "GitLabError"
	// ReasonGitLabRejected: any other refusal (a 4xx), such as the package
	// registry being off or a bad ref; the message is GitLab's.
	ReasonGitLabRejected = "GitLabRejected"
)

// GitLabConfig is the GitLab project a deploy target publishes to, and how.
type GitLabConfig struct {
	// URL is the instance, https://host[:port] with an optional path for an
	// instance under a relative URL root; "" is gitlab.com. The client calls
	// its API at URL + "/api/v4", the rule model's GitLab hosts follow.
	URL string
	// Project is the project's full path, group/project or deeper.
	Project string
	// Token is any GitLab access token, sent as PRIVATE-TOKEN.
	Token string
}

// GitLabProjectInfo is what GET /projects/:id says of a project.
type GitLabProjectInfo struct {
	ID            int64
	Path          string
	WebURL        string
	DefaultBranch string
	// RegistryPrefix is the project's container_registry_image_prefix: the
	// image path, registry host included, its images go under.
	RegistryPrefix string
	// RegistryEnabled is false when container_registry_access_level is
	// "disabled".
	RegistryEnabled bool
}

// GitLabPackageFile is a file of a generic package.
type GitLabPackageFile struct {
	PackageID int64
	ID        int64
	Name      string
	Size      int64
}

// GitLabReleaseLink is a link a release is created with.
type GitLabReleaseLink struct {
	Name            string
	URL             string
	DirectAssetPath string
	LinkType        string
}

// GitLabRelease is a release to create: the tag, made at Ref when it is not
// there yet, its name, description and links.
type GitLabRelease struct {
	Tag         string
	Name        string
	Description string
	Ref         string
	Links       []GitLabReleaseLink
}

// GitLabTreeEntry is one entry of a repository tree: a "blob" (a file, ID
// its content's sha) or a "tree" (a directory).
type GitLabTreeEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Path string `json:"path"`
	Mode string `json:"mode"`
}

// GitLabCommitAction is one change of a commit: Action is "create", "update",
// "delete" or "chmod", Content the file's bytes, base64 when Encoding is
// "base64". ExecuteFilemode is read on chmod alone (a create is 100644, an
// update keeps the mode). LastCommitID, on update and delete, is the last
// commit that touched that file, not the branch's head: GitLab compares it
// with the file's own last commit on the branch and refuses a 400 when they
// differ, so a head that left the file alone is refused too (#1950). The
// deployer reads it with FileLastCommit (#1979).
type GitLabCommitAction struct {
	Action          string `json:"action"`
	FilePath        string `json:"file_path"`
	Content         string `json:"content,omitempty"`
	Encoding        string `json:"encoding,omitempty"`
	ExecuteFilemode *bool  `json:"execute_filemode,omitempty"`
	LastCommitID    string `json:"last_commit_id,omitempty"`
}

// GitLabCommit is a commit to make on Branch, through the Commits API, on
// Branch's head. StartSHA starts a new Branch from that commit: GitLab
// refuses it on a Branch that exists, "A branch called '<branch>' already
// exists" (#1979).
type GitLabCommit struct {
	Branch   string               `json:"branch"`
	StartSHA string               `json:"start_sha,omitempty"`
	Message  string               `json:"commit_message"`
	Actions  []GitLabCommitAction `json:"actions"`
}

// GitLabProject is one GitLab project's REST API, for a deploy target that
// publishes releases there. A failed call is a *ReleaseError.
type GitLabProject interface {
	// Project reads the project.
	Project(ctx context.Context) (GitLabProjectInfo, error)
	// TokenScopes returns the scopes of the token the client calls with.
	TokenScopes(ctx context.Context) ([]string, error)
	// User returns the username of the token's user: the login at the
	// project's container registry (docs/plans/gitlab-deploy.md, Decision 5).
	User(ctx context.Context) (string, error)
	// Release returns the release of tag, nil when there is none. Its Target
	// is the commit the tag names, Body the description and URL its page.
	Release(ctx context.Context, tag string) (*model.TargetRelease, error)
	// ListReleases returns every release, newest first, as GitLab lists them.
	ListReleases(ctx context.Context) ([]model.TargetRelease, error)
	// CreateRelease creates the release r, and its tag at r.Ref when the tag
	// is not there yet. A release of the tag already there is a 409,
	// ReasonGitLabRejected.
	CreateRelease(ctx context.Context, r GitLabRelease) (model.TargetRelease, error)
	// Tag returns the commit tag name names, "" when there is no such tag.
	Tag(ctx context.Context, name string) (string, error)
	// Branch returns the head commit of branch name, "" when there is no
	// such branch.
	Branch(ctx context.Context, name string) (string, error)
	// Tree lists the tree at ref under path (recursive); nil, nil when path
	// is not there.
	Tree(ctx context.Context, ref, path string) ([]GitLabTreeEntry, error)
	// FileLastCommit returns the last commit that touched the file at path
	// as of ref, its last_commit_id; "" when there is no such file.
	FileLastCommit(ctx context.Context, ref, path string) (string, error)
	// CreateCommit commits c.Actions on c.Branch and returns the new
	// commit's sha.
	CreateCommit(ctx context.Context, c GitLabCommit) (string, error)
	// CreateTag creates tag name at ref. A tag already there is
	// ReasonGitLabRejected.
	CreateTag(ctx context.Context, name, ref string) error
	// PackageFiles returns the files of the generic package pkg at version,
	// nil when there is no such package.
	PackageFiles(ctx context.Context, pkg, version string) ([]GitLabPackageFile, error)
	// UploadPackageFile streams size bytes of content to the generic package
	// pkg at version as the file name.
	UploadPackageFile(ctx context.Context, pkg, version, name string, size int64, content io.Reader) error
	// DeletePackageFile removes a package's file. It is safe to repeat.
	DeletePackageFile(ctx context.Context, packageID, fileID int64) error
}

// gitlabProjectPath is a GitLab project's full path: two or more segments,
// each a GitLab path.
var gitlabProjectPath = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*(/[A-Za-z0-9_][A-Za-z0-9_.-]*)+$`)

// gitlabMaxProjectPath is the longest full path GitLab takes.
const gitlabMaxProjectPath = 255

// GitLab returns a GitLabProject on cfg's project. Its HTTP client has no
// overall timeout, since an upload takes as long as the file is big; ctx
// bounds each call.
func (c Client) GitLab(cfg GitLabConfig) (GitLabProject, error) {
	if len(cfg.Project) > gitlabMaxProjectPath || !gitlabProjectPath.MatchString(cfg.Project) {
		return nil, fmt.Errorf("gitlab: project %q is not a project's full path, group/project", cfg.Project)
	}
	if cfg.Token == "" {
		return nil, errors.New("gitlab: token is required")
	}
	// The hosts are the model's rule (model.GitLabHostsOf), so the API
	// base and the links Ship writes come from one place.
	h := model.GitLabHostsOf(cfg.URL)
	if u, err := url.Parse(h.Web); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("gitlab: invalid URL %q", cfg.URL)
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{}
	}
	return &gitlabProject{api: h.API, token: cfg.Token, http: client, path: cfg.Project,
		project: "/projects/" + url.PathEscape(cfg.Project)}, nil
}

type gitlabProject struct {
	// api is the instance's API, .../api/v4.
	api   string
	token string
	http  *http.Client
	// path is the project's full path, project the escaped /projects/:id.
	path    string
	project string
}

// gitlabPageSize and gitlabMaxPages bound the listings; a tree is listed
// recursively, directories counted, so gitlabTreeMaxPages gives it room for
// a repository of 10,000 entries.
const (
	gitlabPageSize     = 100
	gitlabMaxPages     = 10
	gitlabTreeMaxPages = 100
)

type gitlabProjectBody struct {
	ID                  int64  `json:"id"`
	PathWithNamespace   string `json:"path_with_namespace"`
	WebURL              string `json:"web_url"`
	DefaultBranch       string `json:"default_branch"`
	RegistryImagePrefix string `json:"container_registry_image_prefix"`
	RegistryAccessLevel string `json:"container_registry_access_level"`
}

type gitlabReleaseBody struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Commit      struct {
		ID string `json:"id"`
	} `json:"commit"`
	Links struct {
		Self string `json:"self"`
	} `json:"_links"`
}

func (r gitlabReleaseBody) release() model.TargetRelease {
	return model.TargetRelease{TagName: r.TagName, Target: r.Commit.ID, Body: r.Description, URL: r.Links.Self}
}

func (g *gitlabProject) Project(ctx context.Context) (GitLabProjectInfo, error) {
	var p gitlabProjectBody
	if err := g.call(ctx, http.MethodGet, g.project, nil, http.StatusOK, "reading project "+g.path, &p); err != nil {
		return GitLabProjectInfo{}, err
	}
	return GitLabProjectInfo{ID: p.ID, Path: p.PathWithNamespace, WebURL: p.WebURL, DefaultBranch: p.DefaultBranch,
		RegistryPrefix: p.RegistryImagePrefix, RegistryEnabled: p.RegistryAccessLevel != "disabled"}, nil
}

// TokenScopes reads the token's own record. A 404 there is the token's: GitLab
// has no record of it (docs/plans/gitlab-deploy.md, Decision 2).
func (g *gitlabProject) TokenScopes(ctx context.Context) ([]string, error) {
	var t struct {
		Scopes []string `json:"scopes"`
	}
	err := g.call(ctx, http.MethodGet, "/personal_access_tokens/self", nil, http.StatusOK, "reading the token", &t)
	if re, ok := errors.AsType[*ports.ReleaseError](err); ok && re.Status == http.StatusNotFound {
		re.Reason = ports.ReasonUnauthorized
		re.Message = "gitlab: the instance has no record of the token: " + re.Message
	}
	return t.Scopes, err
}

// User reads the token's user, GET /user.
func (g *gitlabProject) User(ctx context.Context) (string, error) {
	var u struct {
		Username string `json:"username"`
	}
	err := g.call(ctx, http.MethodGet, "/user", nil, http.StatusOK, "reading the token's user", &u)
	return u.Username, err
}

func (g *gitlabProject) Release(ctx context.Context, tag string) (*model.TargetRelease, error) {
	var r gitlabReleaseBody
	err := g.call(ctx, http.MethodGet, g.project+"/releases/"+url.PathEscape(tag), nil, http.StatusOK,
		"reading release "+tag, &r)
	if notFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rel := r.release()
	return &rel, nil
}

func (g *gitlabProject) ListReleases(ctx context.Context) ([]model.TargetRelease, error) {
	var out []model.TargetRelease
	for page := 1; page <= gitlabMaxPages; page++ {
		var batch []gitlabReleaseBody
		if err := g.list(ctx, g.project+"/releases", "", page, "listing releases", &batch); err != nil {
			return nil, err
		}
		for _, r := range batch {
			out = append(out, r.release())
		}
		if len(batch) < gitlabPageSize {
			return out, nil
		}
	}
	return nil, &ports.ReleaseError{Reason: ReasonGitLabError,
		Message: fmt.Sprintf("gitlab: more than %d releases to look through", gitlabMaxPages*gitlabPageSize)}
}

type gitlabLinkBody struct {
	Name            string `json:"name"`
	URL             string `json:"url"`
	DirectAssetPath string `json:"direct_asset_path,omitempty"`
	LinkType        string `json:"link_type,omitempty"`
}

type gitlabCreateRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Ref         string `json:"ref,omitempty"`
	Assets      struct {
		Links []gitlabLinkBody `json:"links"`
	} `json:"assets"`
}

func (g *gitlabProject) CreateRelease(ctx context.Context, r GitLabRelease) (model.TargetRelease, error) {
	body := gitlabCreateRelease{TagName: r.Tag, Name: r.Name, Description: r.Description, Ref: r.Ref}
	body.Assets.Links = []gitlabLinkBody{}
	for _, l := range r.Links {
		body.Assets.Links = append(body.Assets.Links, gitlabLinkBody(l))
	}
	payload, _ := json.Marshal(body)
	var out gitlabReleaseBody
	err := g.call(ctx, http.MethodPost, g.project+"/releases", payload, http.StatusCreated,
		"creating release "+r.Tag, &out)
	return out.release(), err
}

func (g *gitlabProject) Tag(ctx context.Context, name string) (string, error) {
	var t struct {
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	err := g.call(ctx, http.MethodGet, g.project+"/repository/tags/"+url.PathEscape(name), nil, http.StatusOK,
		"reading tag "+name, &t)
	if notFound(err) {
		return "", nil
	}
	return t.Commit.ID, err
}

func (g *gitlabProject) Branch(ctx context.Context, name string) (string, error) {
	var b struct {
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	err := g.call(ctx, http.MethodGet, g.project+"/repository/branches/"+url.PathEscape(name), nil, http.StatusOK,
		"reading branch "+name, &b)
	if notFound(err) {
		return "", nil
	}
	return b.Commit.ID, err
}

func (g *gitlabProject) Tree(ctx context.Context, ref, path string) ([]GitLabTreeEntry, error) {
	query := "recursive=true&ref=" + url.QueryEscape(ref)
	if path != "" {
		query += "&path=" + url.QueryEscape(path)
	}
	var out []GitLabTreeEntry
	for page := 1; page <= gitlabTreeMaxPages; page++ {
		var batch []GitLabTreeEntry
		err := g.list(ctx, g.project+"/repository/tree", query, page, "reading tree "+ref, &batch)
		if notFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
		if len(batch) < gitlabPageSize {
			return out, nil
		}
	}
	return nil, &ports.ReleaseError{Reason: ReasonGitLabError,
		Message: fmt.Sprintf("gitlab: more than %d tree entries to look through", gitlabTreeMaxPages*gitlabPageSize)}
}

func (g *gitlabProject) FileLastCommit(ctx context.Context, ref, path string) (string, error) {
	var f struct {
		LastCommitID string `json:"last_commit_id"`
	}
	err := g.call(ctx, http.MethodGet, g.project+"/repository/files/"+url.PathEscape(path)+"?ref="+url.QueryEscape(ref),
		nil, http.StatusOK, "reading file "+path+" at "+ref, &f)
	if notFound(err) {
		return "", nil
	}
	return f.LastCommitID, err
}

func (g *gitlabProject) CreateCommit(ctx context.Context, c GitLabCommit) (string, error) {
	if c.Actions == nil {
		c.Actions = []GitLabCommitAction{}
	}
	payload, _ := json.Marshal(c)
	var out struct {
		ID string `json:"id"`
	}
	err := g.call(ctx, http.MethodPost, g.project+"/repository/commits", payload, http.StatusCreated,
		"committing to "+c.Branch, &out)
	return out.ID, err
}

func (g *gitlabProject) CreateTag(ctx context.Context, name, ref string) error {
	payload, _ := json.Marshal(map[string]string{"tag_name": name, "ref": ref})
	return g.call(ctx, http.MethodPost, g.project+"/repository/tags", payload, http.StatusCreated,
		"creating tag "+name, nil)
}

type gitlabPackageBody struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

type gitlabPackageFileBody struct {
	ID        int64  `json:"id"`
	PackageID int64  `json:"package_id"`
	FileName  string `json:"file_name"`
	Size      int64  `json:"size"`
}

// PackageFiles finds the package by its exact name and version, since
// GitLab's package_name filter matches any name containing it, then lists the
// files of each such package.
func (g *gitlabProject) PackageFiles(ctx context.Context, pkg, version string) ([]GitLabPackageFile, error) {
	query := url.Values{"package_type": {"generic"}, "package_name": {pkg}, "package_version": {version}}.Encode()
	var ids []int64
	for page := 1; ; page++ {
		if page > gitlabMaxPages {
			return nil, &ports.ReleaseError{Reason: ReasonGitLabError,
				Message: fmt.Sprintf("gitlab: more than %d packages named like %s", gitlabMaxPages*gitlabPageSize, pkg)}
		}
		var batch []gitlabPackageBody
		if err := g.list(ctx, g.project+"/packages", query, page, "listing packages", &batch); err != nil {
			return nil, err
		}
		for _, p := range batch {
			if p.Name == pkg && p.Version == version && p.Status != "pending_destruction" {
				ids = append(ids, p.ID)
			}
		}
		if len(batch) < gitlabPageSize {
			break
		}
	}
	var out []GitLabPackageFile
	for _, id := range ids {
		files, err := g.packageFiles(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, files...)
	}
	return out, nil
}

func (g *gitlabProject) packageFiles(ctx context.Context, id int64) ([]GitLabPackageFile, error) {
	var out []GitLabPackageFile
	path := g.project + "/packages/" + strconv.FormatInt(id, 10) + "/package_files"
	for page := 1; page <= gitlabMaxPages; page++ {
		var batch []gitlabPackageFileBody
		if err := g.list(ctx, path, "", page, "listing package files", &batch); err != nil {
			return nil, err
		}
		for _, f := range batch {
			out = append(out, GitLabPackageFile{PackageID: id, ID: f.ID, Name: f.FileName, Size: f.Size})
		}
		if len(batch) < gitlabPageSize {
			return out, nil
		}
	}
	return nil, &ports.ReleaseError{Reason: ReasonGitLabError,
		Message: fmt.Sprintf("gitlab: package %d has more than %d files", id, gitlabMaxPages*gitlabPageSize)}
}

// UploadPackageFile streams content as the request body with its
// Content-Length, never holding it whole.
func (g *gitlabProject) UploadPackageFile(ctx context.Context, pkg, version, name string, size int64,
	content io.Reader) error {
	if size < 0 {
		return &ports.ReleaseError{Reason: ReasonGitLabError,
			Message: fmt.Sprintf("gitlab: uploading %s: its size is unknown", name)}
	}
	if size == 0 {
		// The caller owns content and closes it.
		content = http.NoBody
	}
	path := g.project + "/packages/generic/" + url.PathEscape(pkg) + "/" + url.PathEscape(version) + "/" +
		url.PathEscape(name)
	req, err := g.request(ctx, http.MethodPut, path, content)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	return g.send(req, http.StatusCreated, "uploading "+name, nil)
}

func (g *gitlabProject) DeletePackageFile(ctx context.Context, packageID, fileID int64) error {
	what := fmt.Sprintf("deleting file %d of package %d", fileID, packageID)
	err := g.call(ctx, http.MethodDelete,
		g.project+"/packages/"+strconv.FormatInt(packageID, 10)+"/package_files/"+strconv.FormatInt(fileID, 10),
		nil, http.StatusNoContent, what, nil)
	if notFound(err) {
		// Gone already. A project that is gone fails the next call instead.
		return nil
	}
	return err
}

// notFound reports whether err is GitLab's 404 on something in the project,
// not on the project itself.
func notFound(err error) bool {
	re, ok := errors.AsType[*ports.ReleaseError](err)
	return ok && re.Status == http.StatusNotFound && re.Reason != ports.ReasonRepositoryNotFound
}

func (g *gitlabProject) list(ctx context.Context, path, query string, page int, what string, out any) error {
	q := fmt.Sprintf("per_page=%d&page=%d", gitlabPageSize, page)
	if query != "" {
		q = query + "&" + q
	}
	return g.call(ctx, http.MethodGet, path+"?"+q, nil, http.StatusOK, what, out)
}

func (g *gitlabProject) call(ctx context.Context, method, path string, body []byte, want int, what string,
	out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := g.request(ctx, method, path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return g.send(req, want, what, out)
}

func (g *gitlabProject) request(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, g.api+path, body)
	if err != nil {
		return nil, &ports.ReleaseError{Reason: ReasonGitLabError, Message: "gitlab: " + err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "theos")
	req.Header.Set("PRIVATE-TOKEN", g.token)
	return req, nil
}

// send makes req and decodes a want response into out; any other response is
// a ReleaseError with its reason.
func (g *gitlabProject) send(req *http.Request, want int, what string, out any) error {
	resp, data, err := g.do(req)
	if err != nil {
		return &ports.ReleaseError{Reason: ReasonGitLabError, Message: fmt.Sprintf("gitlab: %s: %v", what, err)}
	}
	if resp.StatusCode != want {
		return g.failure(resp, data, what)
	}
	if out != nil && json.Unmarshal(data, out) != nil {
		return &ports.ReleaseError{Reason: ReasonGitLabError, Status: resp.StatusCode,
			Message: fmt.Sprintf("gitlab: %s: unreadable response", what)}
	}
	return nil
}

// do makes req and reads its response, at most gitlabMaxBody of it. An error
// is the network's: no response came, or it broke off.
func (g *gitlabProject) do(req *http.Request) (*http.Response, []byte, error) {
	resp, err := g.http.Do(req) //nolint:gosec // to the configured GitLab API
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, gitlabMaxBody))
	if err != nil {
		return nil, nil, err
	}
	return resp, data, nil
}

// failure maps a response GitLab refused to the reason it failed: a 401 or a
// 403 is the token's, unless GitLab says a feature is disabled; a 404 on the
// project, or GitLab's "Project Not Found", is the project's; a 5xx or 429
// may pass; any other 4xx is GitLab's refusal.
func (g *gitlabProject) failure(resp *http.Response, data []byte, what string) *ports.ReleaseError {
	message := gitlabMessage(resp, data)
	e := &ports.ReleaseError{Reason: ReasonGitLabRejected, Status: resp.StatusCode,
		Message: fmt.Sprintf("gitlab: %s failed: %s", what, message)}
	lower := strings.ToLower(message)
	switch code := resp.StatusCode; {
	case code == http.StatusUnauthorized:
		e.Reason = ports.ReasonUnauthorized
	case code == http.StatusForbidden:
		if !strings.Contains(lower, "disabled") {
			e.Reason = ports.ReasonUnauthorized
		}
	case code == http.StatusNotFound:
		if resp.Request != nil && resp.Request.URL.EscapedPath() == g.apiPath()+g.project ||
			strings.Contains(lower, "project not found") {
			e.Reason = ports.ReasonRepositoryNotFound
			e.Message = fmt.Sprintf("gitlab: can't see project %s: %s", g.path, message)
		}
	case code == http.StatusTooManyRequests, code >= http.StatusInternalServerError, code < http.StatusBadRequest:
		e.Reason = ReasonGitLabError
	}
	return e
}

// apiPath is the path part of the API URL.
func (g *gitlabProject) apiPath() string {
	u, err := url.Parse(g.api)
	if err != nil {
		return ""
	}
	return u.EscapedPath()
}

// gitlabMessage reads GitLab's error body, {"message": ...} or
// {"error": ...}, its message a string or an object of field errors, cut to
// maxFailureMessage.
func gitlabMessage(resp *http.Response, data []byte) string {
	var body struct {
		Message json.RawMessage `json:"message"`
		Error   string          `json:"error"`
	}
	_ = json.Unmarshal(data, &body)
	message := gitlabMessageText(body.Message)
	if message == "" {
		message = body.Error
	}
	if message == "" {
		// Not GitLab's JSON: an HTML error page, say. Keep the message short.
		message = http.StatusText(resp.StatusCode)
	}
	if r := []rune(message); len(r) > maxFailureMessage {
		message = string(r[:maxFailureMessage]) + "..."
	}
	return message
}

// gitlabMessageText is a message as GitLab sends it: a string, or an object
// of each field's errors, "field error, field error".
func gitlabMessageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return string(raw)
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var parts []string
	for _, k := range keys {
		var list []string
		if json.Unmarshal(fields[k], &list) == nil {
			for _, m := range list {
				parts = append(parts, k+" "+m)
			}
			continue
		}
		parts = append(parts, k+" "+gitlabMessageText(fields[k]))
	}
	return strings.Join(parts, ", ")
}
