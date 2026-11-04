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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// Client builds the GitHub clients: a ReleaseTarget, an IssueTracker, and a
// GitHub App's installation tokens. It is what the operator uses; tests
// substitute their own.
type Client struct {
	// HTTP is used for API calls. Defaults to a client with a 30 second
	// timeout for a GitHub App's token, and to one with no overall timeout
	// for releases and issues, where ctx bounds each call.
	HTTP *http.Client
	// Now is the clock a GitHub App's token is minted and kept by; defaults to
	// time.Now.
	Now func() time.Time
}

func (c Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// GitHub's hosts on github.com.
const (
	GitHubAPIURL    = "https://api.github.com"
	GitHubUploadURL = "https://uploads.github.com"
)

var githubRepository = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)

// GitHubReleases returns a ReleaseTarget on a GitHub repository. Its HTTP client
// has no overall timeout, since an upload takes as long as the file is big; ctx
// bounds each call.
func (c Client) GitHubReleases(cfg ports.GitHubConfig) (ports.ReleaseTarget, error) {
	api, err := c.githubAPI(cfg)
	if err != nil {
		return nil, err
	}
	upload, err := githubURL(cfg.UploadURL, GitHubUploadURL)
	if err != nil {
		return nil, err
	}
	return &githubReleases{githubAPI: api, upload: upload, releases: api.repo + "/releases"}, nil
}

// githubAPI checks cfg and returns the API host's client for its repository.
func (c Client) githubAPI(cfg ports.GitHubConfig) (githubAPI, error) {
	if !githubRepository.MatchString(cfg.Repository) {
		return githubAPI{}, fmt.Errorf("github: repository %q is not owner/name", cfg.Repository)
	}
	if cfg.Token == "" {
		return githubAPI{}, errors.New("github: token is required")
	}
	api, err := githubURL(cfg.APIURL, GitHubAPIURL)
	if err != nil {
		return githubAPI{}, err
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{}
	}
	owner, name, _ := strings.Cut(cfg.Repository, "/")
	return githubAPI{api: api, token: cfg.Token, http: client,
		repo: "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)}, nil
}

func githubURL(raw, def string) (*url.URL, error) {
	if raw == "" {
		raw = def
	}
	u, err := url.Parse(strings.TrimSuffix(raw, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("github: invalid URL %q", raw)
	}
	return u, nil
}

// githubAPI is GitHub's REST API for one repository, with a token.
type githubAPI struct {
	api   *url.URL
	token string
	http  *http.Client
	// repo is /repos/{owner}/{repo}.
	repo string
}

type githubReleases struct {
	githubAPI
	upload *url.URL
	// releases is /repos/{owner}/{repo}/releases, the prefix of every path it calls.
	releases string
}

// githubPageSize and githubMaxPages bound the listings: a repository of
// published releases has its newest first, and drafts with them.
const (
	githubPageSize = 100
	githubMaxPages = 10
)

// githubMaxBody bounds a response read: a page of releases holds their bodies.
const githubMaxBody = 32 << 20

type githubRelease struct {
	ID      int64  `json:"id,omitempty"`
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Body    string `json:"body"`
	Draft   bool   `json:"draft"`
	// TargetCommitish is GitHub's default branch's name when none was given.
	TargetCommitish string `json:"target_commitish,omitempty"`
	HTMLURL         string `json:"html_url,omitempty"`
}

func (r githubRelease) release() ports.TargetRelease {
	return ports.TargetRelease{ID: r.ID, TagName: r.TagName, Name: r.Name, Body: r.Body, Draft: r.Draft,
		Commit: r.TargetCommitish, URL: r.HTMLURL}
}

type githubAsset struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	State string `json:"state"`
}

func (a githubAsset) asset() ports.TargetAsset {
	return ports.TargetAsset{ID: a.ID, Name: a.Name, Size: a.Size, Uploaded: a.State == "uploaded"}
}

// FindRelease lists the releases, since a draft can't be read by its tag. A
// published release with the marker comes before a draft with it, so a second
// draft left behind never holds back a release already out.
func (g *githubReleases) FindRelease(ctx context.Context, tag, marker string) (*ports.TargetRelease, error) {
	var tagged, draft *ports.TargetRelease
	for page := 1; page <= githubMaxPages; page++ {
		var batch []githubRelease
		if err := g.list(ctx, g.releases, page, "listing releases", &batch); err != nil {
			return nil, err
		}
		for _, r := range batch {
			rel := r.release()
			if marker != "" && strings.Contains(r.Body, marker) {
				if !r.Draft {
					return &rel, nil
				}
				if draft == nil {
					draft = &rel
				}
				continue
			}
			// Several drafts can share a tag; only a published one takes it.
			if tagged == nil && r.TagName == tag && !r.Draft {
				tagged = &rel
			}
		}
		if len(batch) < githubPageSize {
			if draft != nil {
				return draft, nil
			}
			return tagged, nil
		}
	}
	return nil, &ports.ReleaseError{Reason: ports.ReasonGitHubError,
		Message: fmt.Sprintf("github: more than %d releases to look through", githubMaxPages*githubPageSize)}
}

func (g *githubReleases) ListReleases(ctx context.Context) ([]ports.TargetRelease, error) {
	var out []ports.TargetRelease
	for page := 1; page <= githubMaxPages; page++ {
		var batch []githubRelease
		if err := g.list(ctx, g.releases, page, "listing releases", &batch); err != nil {
			return nil, err
		}
		for _, r := range batch {
			out = append(out, r.release())
		}
		if len(batch) < githubPageSize {
			return out, nil
		}
	}
	return nil, &ports.ReleaseError{Reason: ports.ReasonGitHubError,
		Message: fmt.Sprintf("github: more than %d releases to look through", githubMaxPages*githubPageSize)}
}

func (g *githubReleases) CreateDraft(ctx context.Context, tag, name, body, commit string) (ports.TargetRelease, error) {
	payload, _ := json.Marshal(githubRelease{TagName: tag, Name: name, Body: body, Draft: true, TargetCommitish: commit})
	var r githubRelease
	err := g.call(ctx, http.MethodPost, g.api, g.releases, payload, http.StatusCreated, "creating release "+tag, &r)
	return r.release(), err
}

func (g *githubReleases) RetargetDraft(ctx context.Context, id int64, commit string) (ports.TargetRelease, error) {
	if commit == "" {
		return ports.TargetRelease{}, &ports.ReleaseError{Reason: ports.ReasonGitHubError,
			Message: fmt.Sprintf("github: retargeting release %d: no commit", id)}
	}
	payload, _ := json.Marshal(map[string]string{"target_commitish": commit})
	var r githubRelease
	err := g.call(ctx, http.MethodPatch, g.api, g.releasePath(id), payload, http.StatusOK,
		"retargeting release "+strconv.FormatInt(id, 10), &r)
	return r.release(), err
}

func (g *githubReleases) ListAssets(ctx context.Context, id int64) ([]ports.TargetAsset, error) {
	var out []ports.TargetAsset
	for page := 1; page <= githubMaxPages; page++ {
		var batch []githubAsset
		if err := g.list(ctx, g.releasePath(id)+"/assets", page, "listing assets", &batch); err != nil {
			return nil, err
		}
		for _, a := range batch {
			out = append(out, a.asset())
		}
		if len(batch) < githubPageSize {
			return out, nil
		}
	}
	return nil, &ports.ReleaseError{Reason: ports.ReasonGitHubError,
		Message: fmt.Sprintf("github: release %d has more than %d assets", id, githubMaxPages*githubPageSize)}
}

// UploadAsset streams content as the request body with its Content-Length,
// never holding it whole.
func (g *githubReleases) UploadAsset(ctx context.Context, id int64, name string, size int64, content io.Reader) (ports.TargetAsset, error) {
	path := g.releasePath(id) + "/assets?name=" + url.QueryEscape(name)
	if size < 0 {
		return ports.TargetAsset{}, &ports.ReleaseError{Reason: ports.ReasonGitHubError,
			Message: fmt.Sprintf("github: uploading %s: its size is unknown", name)}
	}
	if size == 0 {
		if c, ok := content.(io.Closer); ok {
			_ = c.Close()
		}
		content = http.NoBody
	}
	req, err := g.request(ctx, http.MethodPost, g.upload, path, content)
	if err != nil {
		return ports.TargetAsset{}, err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	var a githubAsset
	err = g.send(req, http.StatusCreated, "uploading "+name, &a)
	return a.asset(), err
}

func (g *githubReleases) DeleteAsset(ctx context.Context, assetID int64) error {
	err := g.call(ctx, http.MethodDelete, g.api, g.releases+"/assets/"+strconv.FormatInt(assetID, 10), nil,
		http.StatusNoContent, "deleting asset "+strconv.FormatInt(assetID, 10), nil)
	if re, ok := errors.AsType[*ports.ReleaseError](err); ok && re.Status == http.StatusNotFound {
		// Gone already. A repository that is gone fails the next call instead.
		return nil
	}
	return err
}

func (g *githubReleases) PublishRelease(ctx context.Context, id int64, latest bool) (ports.TargetRelease, error) {
	// make_latest is a string: "true", "false" or "legacy", GitHub's default
	// that picks by date and version.
	payload, _ := json.Marshal(map[string]any{"draft": false, "make_latest": strconv.FormatBool(latest)})
	var r githubRelease
	err := g.call(ctx, http.MethodPatch, g.api, g.releasePath(id), payload, http.StatusOK,
		"publishing release "+strconv.FormatInt(id, 10), &r)
	return r.release(), err
}

func (g *githubReleases) LatestRelease(ctx context.Context) (*ports.TargetRelease, error) {
	var r githubRelease
	err := g.call(ctx, http.MethodGet, g.api, g.releases+"/latest", nil, http.StatusOK, "reading the Latest release", &r)
	if re, ok := errors.AsType[*ports.ReleaseError](err); ok && re.Status == http.StatusNotFound {
		// No published release yet. A repository that is gone fails the next
		// call instead.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rel := r.release()
	return &rel, nil
}

func (g *githubReleases) releasePath(id int64) string {
	return g.releases + "/" + strconv.FormatInt(id, 10)
}

func (g *githubAPI) list(ctx context.Context, path string, page int, what string, out any) error {
	query := fmt.Sprintf("?per_page=%d&page=%d", githubPageSize, page)
	return g.call(ctx, http.MethodGet, g.api, path+query, nil, http.StatusOK, what, out)
}

func (g *githubAPI) call(ctx context.Context, method string, host *url.URL, path string, body []byte,
	want int, what string, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := g.request(ctx, method, host, path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return g.send(req, want, what, out)
}

func (g *githubAPI) request(ctx context.Context, method string, host *url.URL, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, host.String()+path, body)
	if err != nil {
		return nil, &ports.ReleaseError{Reason: ports.ReasonGitHubError, Message: "github: " + err.Error()}
	}
	setGitHubHeaders(req, g.token)
	return req, nil
}

func setGitHubHeaders(req *http.Request, token string) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "theos")
	req.Header.Set("Authorization", "Bearer "+token)
}

// send makes req and decodes a want response into out; any other response is a
// ReleaseError with its reason.
func (g *githubAPI) send(req *http.Request, want int, what string, out any) error {
	resp, data, err := g.do(req)
	if err != nil {
		return &ports.ReleaseError{Reason: ports.ReasonGitHubError, Message: fmt.Sprintf("github: %s: %v", what, err)}
	}
	if resp.StatusCode != want {
		return githubFailure(resp, data, what)
	}
	if out != nil && json.Unmarshal(data, out) != nil {
		return &ports.ReleaseError{Reason: ports.ReasonGitHubError, Status: resp.StatusCode,
			Message: fmt.Sprintf("github: %s: unreadable response", what)}
	}
	return nil
}

// do makes req and reads its response, at most githubMaxBody of it. An error
// is the network's: no response came, or it broke off.
func (g *githubAPI) do(req *http.Request) (*http.Response, []byte, error) {
	resp, err := g.http.Do(req) //nolint:gosec // to the configured GitHub API
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, githubMaxBody))
	if err != nil {
		return nil, nil, err
	}
	return resp, data, nil
}

type githubErrorBody struct {
	Message string `json:"message"`
	Errors  []struct {
		Code    string `json:"code"`
		Field   string `json:"field"`
		Message string `json:"message"`
	} `json:"errors"`
}

// maxFailureMessage bounds GitHub's message in a ReleaseError.
const maxFailureMessage = 300

// githubFailure maps a response GitHub refused to the reason it failed.
func githubFailure(resp *http.Response, data []byte, what string) *ports.ReleaseError {
	body, message := githubMessage(resp, data)
	e := &ports.ReleaseError{Reason: ports.ReasonGitHubError, Status: resp.StatusCode,
		Message: fmt.Sprintf("github: %s failed: %s", what, message)}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		e.Reason = ports.ReasonUnauthorized
	case http.StatusForbidden:
		if !rateLimited(resp, message) {
			e.Reason = ports.ReasonUnauthorized
		}
	case http.StatusNotFound:
		e.Reason = ports.ReasonRepositoryNotFound
	case http.StatusRequestEntityTooLarge:
		e.Reason = ports.ReasonSourceTooLarge
	case http.StatusConflict:
		switch {
		case body.protectedBranch():
			e.Reason = ports.ReasonBranchProtected
		case body.says("repository is empty"):
			e.Reason = ports.ReasonEmptyRepository
		}
	case http.StatusUnprocessableEntity:
		switch {
		case body.protectedBranch():
			e.Reason = ports.ReasonBranchProtected
		case body.says("repository is empty"):
			e.Reason = ports.ReasonEmptyRepository
		case body.alreadyExists("tag_name"):
			e.Reason = ports.ReasonTagTaken
		}
	}
	return e
}

// githubMessage reads GitHub's error body and its message, cut to
// maxFailureMessage.
func githubMessage(resp *http.Response, data []byte) (githubErrorBody, string) {
	var body githubErrorBody
	_ = json.Unmarshal(data, &body)
	message := body.Message
	if message == "" {
		// Not GitHub's JSON: an HTML error page, say. Keep the message short.
		message = http.StatusText(resp.StatusCode)
	}
	if r := []rune(message); len(r) > maxFailureMessage {
		message = string(r[:maxFailureMessage]) + "..."
	}
	return body, message
}

// says reports whether the message, or one of the errors', contains text.
func (b githubErrorBody) says(text string) bool {
	if strings.Contains(strings.ToLower(b.Message), text) {
		return true
	}
	for _, e := range b.Errors {
		if strings.Contains(strings.ToLower(e.Message), text) {
			return true
		}
	}
	return false
}

// protectedBranch reports whether GitHub refused a ref update for the
// branch's protection or a ruleset.
func (b githubErrorBody) protectedBranch() bool {
	return b.says("protected branch") || b.says("repository rule violation")
}

func (b githubErrorBody) alreadyExists(field string) bool {
	for _, e := range b.Errors {
		if e.Code == "already_exists" && e.Field == field {
			return true
		}
	}
	return false
}

// rateLimited reports whether a 403 is GitHub's primary or secondary rate limit
// rather than a refusal.
func rateLimited(resp *http.Response, message string) bool {
	return resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "" ||
		strings.Contains(strings.ToLower(message), "rate limit")
}
