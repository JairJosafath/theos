package gitea

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/JairJosafath/theos/internal/ports"
)

var _ ports.Events = (*gitea)(nil)

// hookEvents are the events the operator's hook asks Gitea for: those the
// reconcilers read (docs/plans/done/git-events.md, decision 2). Gitea 1.27 takes
// the reviews as one, pull_request_review, which delivers as
// pull_request_review_approved, _rejected and _comment.
var hookEvents = []string{
	"issues", "issue_comment", "issue_label", "issue_assign",
	"pull_request", "pull_request_sync", "pull_request_label", "pull_request_comment",
	"pull_request_review",
	"push", "create", "release", "status", "workflow_run",
}

// hookPages bounds the pages of the owner's hooks EnsureEventHook reads; an
// organization has a handful.
const hookPages = 20

// giteaHook is an organization hook as Gitea's API lists it.
type giteaHook struct {
	ID     int64  `json:"id"`
	Type   string `json:"type"`
	Config struct {
		URL string `json:"url"`
	} `json:"config"`
}

// hooksPath is the owner organization's hooks API.
func (g *gitea) hooksPath() string {
	return "/api/v1/orgs/" + url.PathEscape(g.owner) + "/hooks"
}

// EnsureEventHook makes the owner organization's hook deliver to hook.URL,
// signed with hook.Secret. Gitea 1.27 never shows a hook's secret and its
// edit ignores a new one, so the URL Gitea holds carries a fingerprint of
// the secret in its query (secretFingerprint). The hook is the gitea-type
// one whose URL has hook.URL's path: when its URL, fingerprint included, is
// the one wanted it is edited, else a hook with that URL is created; the
// others with that path are removed. It reports whether it created a hook,
// which is when the URL or the secret changed.
func (g *gitea) EnsureEventHook(ctx context.Context, hook ports.EventHook) (bool, error) {
	want, err := url.Parse(hook.URL)
	if err != nil || want.Scheme == "" || want.Host == "" {
		return false, fmt.Errorf("gitea: invalid event hook URL %q", hook.URL)
	}
	if hook.Secret == "" {
		return false, fmt.Errorf("gitea: the event hook to %s has no secret", hook.URL)
	}
	query := want.Query()
	query.Set(secretQuery, secretFingerprint(hook.Secret))
	want.RawQuery = query.Encode()
	target := want.String()
	hooks, err := g.listHooks(ctx)
	if err != nil {
		return false, err
	}
	var keep *giteaHook
	var stale []giteaHook
	for _, h := range hooks {
		if u, err := url.Parse(h.Config.URL); err != nil || h.Type != "gitea" || u.Path != want.Path {
			continue
		}
		if keep == nil && h.Config.URL == target {
			keep = &h
			continue
		}
		stale = append(stale, h)
	}
	options := map[string]any{
		"config": map[string]string{"url": target, "content_type": "json", "secret": hook.Secret},
		"events": hookEvents,
		"active": true,
	}
	if keep != nil {
		err = g.hookCall(ctx, http.MethodPatch, fmt.Sprintf("%s/%d", g.hooksPath(), keep.ID), options,
			http.StatusOK, "editing")
	} else {
		options["type"] = "gitea"
		err = g.hookCall(ctx, http.MethodPost, g.hooksPath(), options, http.StatusCreated, "creating")
	}
	if err != nil {
		return false, err
	}
	for _, h := range stale {
		if err := g.hookCall(ctx, http.MethodDelete, fmt.Sprintf("%s/%d", g.hooksPath(), h.ID), nil,
			http.StatusNoContent, "removing a stale or duplicate"); err != nil {
			return false, err
		}
	}
	return keep == nil, nil
}

// secretQuery is the query parameter of a hook's URL that holds
// secretFingerprint.
const secretQuery = "secret"

// secretFingerprint names secret without giving it away: the first 12 hex
// digits of its SHA-256. A secret is rand.Text(), 128 bits, so the
// fingerprint tells whoever reads the hook nothing they can use.
func secretFingerprint(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:6])
}

// listHooks reads the owner organization's hooks, oldest first.
func (g *gitea) listHooks(ctx context.Context) ([]giteaHook, error) {
	const limit = 50
	var out []giteaHook
	for page := 1; page <= hookPages; page++ {
		status, data, err := g.do(ctx, http.MethodGet, fmt.Sprintf("%s?limit=%d&page=%d", g.hooksPath(), limit, page), nil)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("gitea: listing the hooks of %q failed (HTTP %d): %s", g.owner, status, apiMessage(data))
		}
		var hooks []giteaHook
		if err := json.Unmarshal(data, &hooks); err != nil {
			return nil, fmt.Errorf("gitea: listing the hooks of %q: %w", g.owner, err)
		}
		// An empty page ends the list: Gitea may cap a page below limit.
		if len(hooks) == 0 {
			break
		}
		out = append(out, hooks...)
	}
	return out, nil
}

// hookCall sends one call on the owner's hooks and wants status back.
func (g *gitea) hookCall(ctx context.Context, method, path string, body any, want int, doing string) error {
	var data []byte
	if body != nil {
		var err error
		if data, err = json.Marshal(body); err != nil {
			return err
		}
	}
	status, answer, err := g.do(ctx, method, path, data)
	if err != nil {
		return err
	}
	if status != want {
		return fmt.Errorf("gitea: %s the event hook of %q failed (HTTP %d): %s", doing, g.owner, status, apiMessage(answer))
	}
	return nil
}

// giteaDelivery is the part of a hook's payload that says what it concerns.
type giteaDelivery struct {
	Number  int    `json:"number"`
	Ref     string `json:"ref"`
	RefType string `json:"ref_type"`
	Issue   *struct {
		Number int `json:"number"`
	} `json:"issue"`
	PullRequest *struct {
		Number int `json:"number"`
	} `json:"pull_request"`
	WorkflowRun *struct {
		HeadBranch string `json:"head_branch"`
	} `json:"workflow_run"`
	Repository *struct {
		Name string `json:"name"`
	} `json:"repository"`
}

// ParseEvent checks X-Gitea-Signature, the hex HMAC-SHA256 of body under
// secret, and reads what the delivery concerns: its repository, its issue or
// pull request, and the branch a push, a branch's creation or a workflow run is
// on. Gitea 1.27's status names only its commit, so a status delivery names
// only its repository. It makes no call to Gitea.
func (g *gitea) ParseEvent(header func(string) string, body, secret []byte) (ports.Event, error) {
	signature, err := hex.DecodeString(header("X-Gitea-Signature"))
	if err != nil || len(signature) == 0 || len(secret) == 0 {
		return ports.Event{}, ports.ErrBadSignature
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return ports.Event{}, ports.ErrBadSignature
	}
	event := ports.Event{Kind: header("X-Gitea-Event")}
	var d giteaDelivery
	if err := json.Unmarshal(body, &d); err != nil {
		return event, fmt.Errorf("gitea: reading a %q delivery: %w", event.Kind, err)
	}
	if d.Repository == nil || d.Repository.Name == "" {
		return event, fmt.Errorf("gitea: a %q delivery names no repository", event.Kind)
	}
	event.Repository = d.Repository.Name
	switch {
	case d.Issue != nil:
		event.Number = d.Issue.Number
	case d.PullRequest != nil:
		event.Number = d.PullRequest.Number
	default:
		event.Number = d.Number
	}
	switch {
	case d.WorkflowRun != nil:
		event.Branch = d.WorkflowRun.HeadBranch
	case strings.HasPrefix(d.Ref, "refs/heads/"):
		event.Branch = strings.TrimPrefix(d.Ref, "refs/heads/")
	case d.RefType == "branch":
		event.Branch = d.Ref
	}
	return event, nil
}
