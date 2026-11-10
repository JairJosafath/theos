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
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/JairJosafath/theos/internal/ports"
)

// The Events port on GitHub, step 6 of docs/plans/github-host.md (#79): the
// webhook that calls the operator back, and the reading of what it delivers.
// An organization holds one webhook for all its repositories. A user's
// account holds none, so there the webhook is each repository's, of the
// repositories the operator names.
//
// https://docs.github.com/en/rest/orgs/webhooks, /rest/repos/webhooks,
// /webhooks/webhook-events-and-payloads and
// /webhooks/using-webhooks/validating-webhook-deliveries.

var _ ports.Events = (*host)(nil)

// hookEvents are the events the operator's webhook asks GitHub for: those
// the reconcilers read. A branch or a tag made or deleted arrives as a push.
var hookEvents = []string{
	"issues", "issue_comment",
	"pull_request", "pull_request_review",
	"status", "check_run", "workflow_run",
	eventPush, "release",
}

// eventPush is the event of a branch or a tag that moved, and what started
// the workflow run of one.
const eventPush = "push"

// hookName is the name of every webhook: GitHub takes no other.
const hookName = "web"

// hookPages bounds the pages of webhooks EnsureEventHook reads of one
// organization or repository; GitHub lets either hold a few dozen.
const hookPages = 10

// ghHook is a webhook as GitHub lists it. It never shows the secret.
type ghHook struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Config struct {
		URL string `json:"url"`
	} `json:"config"`
}

// hookConfig is where a webhook delivers and how. InsecureSSL is "0":
// GitHub checks the receiver's certificate.
type hookConfig struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Secret      string `json:"secret"`
	InsecureSSL string `json:"insecure_ssl"`
}

// hookSettings is the body that makes or edits a webhook.
type hookSettings struct {
	Name   string     `json:"name,omitempty"`
	Config hookConfig `json:"config"`
	Events []string   `json:"events"`
	Active bool       `json:"active"`
}

// EnsureEventHook makes the owner's webhook deliver to hook.URL as JSON,
// signed with hook.Secret, for hookEvents. GitHub never shows a webhook's
// secret, so the URL it holds carries a fingerprint of the secret in its
// query (secretFingerprint): a webhook whose URL is the one wanted,
// fingerprint included, has the secret wanted. The webhook is the one whose
// URL has hook.URL's path. It is made when there is none and edited when
// there is one, its secret, events and content type set again each time,
// since GitHub shows no secret to compare; the others with that path are
// removed. It reports whether it made a webhook or changed one's URL or
// secret.
//
// The owner's webhook is the organization's. For an owner that is the
// token's own account it is the webhook of each of hook.Repositories that
// exists, and the report is of any of them.
func (h *host) EnsureEventHook(ctx context.Context, hook ports.EventHook) (bool, error) {
	want, err := url.Parse(hook.URL)
	if err != nil || want.Scheme == "" || want.Host == "" {
		return false, fmt.Errorf("github: invalid event hook URL %q", hook.URL)
	}
	if hook.Secret == "" {
		return false, fmt.Errorf("github: the event hook to %s has no secret", hook.URL)
	}
	query := want.Query()
	query.Set(fingerprintQuery, secretFingerprint(hook.Secret))
	want.RawQuery = query.Encode()

	org := "/orgs/" + url.PathEscape(h.owner) + "/hooks"
	hooks, found, err := h.listHooks(ctx, org)
	if err != nil {
		return false, err
	}
	if found {
		return h.ensureHook(ctx, org, hooks, want, hook.Secret)
	}
	// GitHub answers 404 for an owner that is no organization, and for an
	// organization whose webhooks the token may not manage.
	login, err := h.tokenUser(ctx)
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(login, h.owner) {
		return false, fmt.Errorf("github: %s has no webhooks the token may manage: it is no organization the "+
			"token's account, %s, owns with the admin:org_hook scope (or, for a fine-grained token, the "+
			"organization's Webhooks permission), and it is not that account itself", h.owner, login)
	}
	changed := false
	for _, name := range hook.Repositories {
		repo := h.Repository(name)
		at := h.repoPath(repo) + "/hooks"
		hooks, found, err := h.listHooks(ctx, at)
		if err != nil {
			return false, err
		}
		if !found {
			// Not made yet, or the token may not manage its webhooks.
			if err := h.hooksDenied(ctx, repo); err != nil {
				return false, err
			}
			continue
		}
		one, err := h.ensureHook(ctx, at, hooks, want, hook.Secret)
		if err != nil {
			return false, err
		}
		changed = changed || one
	}
	return changed, nil
}

// hooksDenied is the error of a repository that exists and whose webhooks
// answered 404: the token may read it and not manage its webhooks. A
// repository that isn't there gives nil.
func (h *host) hooksDenied(ctx context.Context, repo ports.Repository) error {
	status, data, err := h.do(ctx, http.MethodGet, h.repoPath(repo), nil)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusNotFound:
		return nil
	case http.StatusOK:
		return fmt.Errorf("github: the token may not manage the webhooks of %s/%s: it needs the admin:repo_hook "+
			"scope, or, for a fine-grained token, the repository's Webhooks permission", repo.Owner, repo.Name)
	}
	return failed("reading repository "+repo.Owner+"/"+repo.Name, status, data)
}

// ensureHook leaves one webhook with want's path among hooks, the webhooks
// at the API path at, delivering to want signed with secret, and reports
// whether it made it or changed its URL.
func (h *host) ensureHook(ctx context.Context, at string, hooks []ghHook, want *url.URL, secret string) (bool, error) {
	target := want.String()
	var keep *ghHook
	var stale []ghHook
	for _, k := range hooks {
		if u, err := url.Parse(k.Config.URL); err != nil || k.Name != hookName || u.Path != want.Path {
			continue
		}
		switch {
		case keep == nil:
			keep = &k
		case keep.Config.URL != target && k.Config.URL == target:
			// The one that is right already stays, so nothing is reported changed.
			stale = append(stale, *keep)
			keep = &k
		default:
			stale = append(stale, k)
		}
	}
	settings := hookSettings{Events: hookEvents, Active: true,
		Config: hookConfig{URL: target, ContentType: "json", Secret: secret, InsecureSSL: "0"}}
	changed := true
	var err error
	if keep == nil {
		settings.Name = hookName
		err = h.hookCall(ctx, http.MethodPost, at, settings, "making the webhook", http.StatusCreated)
	} else {
		changed = keep.Config.URL != target
		err = h.hookCall(ctx, http.MethodPatch, fmt.Sprintf("%s/%d", at, keep.ID), settings, "editing the webhook",
			http.StatusOK)
	}
	if err != nil {
		return false, err
	}
	for _, k := range stale {
		// One that is gone already is as wanted.
		if err := h.hookCall(ctx, http.MethodDelete, fmt.Sprintf("%s/%d", at, k.ID), nil,
			"removing a stale or duplicate webhook", http.StatusNoContent, http.StatusNotFound); err != nil {
			return false, err
		}
	}
	return changed, nil
}

// fingerprintQuery is the query parameter of a webhook's URL that holds
// secretFingerprint.
const fingerprintQuery = "fingerprint"

// secretFingerprint names secret without giving it away: the first 12 hex
// digits of its SHA-256. A secret is rand.Text(), 128 bits, so the
// fingerprint tells whoever reads the webhook nothing they can use.
func secretFingerprint(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:6])
}

// listHooks reads the webhooks at the API path at, oldest first; found is
// false when GitHub answers 404 there.
func (h *host) listHooks(ctx context.Context, at string) (hooks []ghHook, found bool, err error) {
	const perPage = 100
	for page := 1; page <= hookPages; page++ {
		status, data, err := h.do(ctx, http.MethodGet, fmt.Sprintf("%s?per_page=%d&page=%d", at, perPage, page), nil)
		if err != nil {
			return nil, false, err
		}
		if status == http.StatusNotFound {
			return nil, false, nil
		}
		if status != http.StatusOK {
			return nil, false, failed("listing the webhooks at "+at, status, data)
		}
		var some []ghHook
		if err := json.Unmarshal(data, &some); err != nil {
			return nil, false, fmt.Errorf("github: listing the webhooks at %s: %w", at, err)
		}
		hooks = append(hooks, some...)
		if len(some) < perPage {
			break
		}
	}
	return hooks, true, nil
}

// hookCall sends one call on a webhook and wants one of the statuses back.
func (h *host) hookCall(ctx context.Context, method, path string, body any, doing string, want ...int) error {
	status, data, err := h.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	if slices.Contains(want, status) {
		return nil
	}
	return failed(doing+" at "+path, status, data)
}

// ghDelivery is the part of a webhook's payload that says what it concerns.
type ghDelivery struct {
	// Number is a pull request's, beside pull_request.
	Number int `json:"number"`
	// Ref is the full ref of a push; SHA and Branches are a status's commit
	// and the branches that hold it.
	Ref      string `json:"ref"`
	SHA      string `json:"sha"`
	Branches []struct {
		Name   string `json:"name"`
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	} `json:"branches"`
	Issue *struct {
		Number int `json:"number"`
	} `json:"issue"`
	PullRequest *struct {
		Number int `json:"number"`
		Head   struct {
			Ref  string `json:"ref"`
			Repo *struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
	} `json:"pull_request"`
	CheckRun *struct {
		CheckSuite struct {
			HeadBranch string `json:"head_branch"`
		} `json:"check_suite"`
	} `json:"check_run"`
	WorkflowRun *struct {
		HeadBranch string `json:"head_branch"`
	} `json:"workflow_run"`
	Repository *struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// signaturePrefix starts the value of X-Hub-Signature-256, before the hex
// HMAC-SHA256 of the body under the webhook's secret.
const signaturePrefix = "sha256="

// ParseEvent checks X-Hub-Signature-256 and reads what the delivery
// concerns: its repository; its issue or pull request; and its branch,
// which is the head of a pull request in the same repository, the branch a
// push moved, the one a check run or a workflow run is on, and, for a
// status, the one branch whose newest commit it is on (GitHub lists up to
// ten branches that merely hold the commit; a status on a commit that is
// the newest of none, or of several, names no branch). The push of a tag
// names none either.
//
// A signed delivery that names no repository concerns none: GitHub pings a
// new webhook, and an organization's ping has no repository. Its Event has
// its Kind alone, so the receiver finds nothing to reconcile and answers
// that it arrived. ParseEvent makes no call to GitHub.
func (h *host) ParseEvent(header func(string) string, body, secret []byte) (ports.Event, error) {
	sent, ok := strings.CutPrefix(header("X-Hub-Signature-256"), signaturePrefix)
	signature, err := hex.DecodeString(sent)
	if !ok || err != nil || len(signature) == 0 || len(secret) == 0 {
		return ports.Event{}, ports.ErrBadSignature
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return ports.Event{}, ports.ErrBadSignature
	}
	event := ports.Event{Kind: header("X-GitHub-Event")}
	var d ghDelivery
	if err := json.Unmarshal(body, &d); err != nil {
		return event, fmt.Errorf("github: reading a %q delivery: %w", event.Kind, err)
	}
	if d.Repository == nil || d.Repository.Name == "" {
		return event, nil
	}
	event.Repository = d.Repository.Name
	switch {
	case d.Issue != nil:
		event.Number = d.Issue.Number
	case d.PullRequest != nil:
		event.Number = d.PullRequest.Number
		// A fork's branch is none of this repository's.
		if from := d.PullRequest.Head.Repo; from == nil || from.FullName == d.Repository.FullName {
			event.Branch = d.PullRequest.Head.Ref
		}
	default:
		event.Number = d.Number
	}
	switch {
	case d.CheckRun != nil:
		event.Branch = d.CheckRun.CheckSuite.HeadBranch
	case d.WorkflowRun != nil:
		event.Branch = d.WorkflowRun.HeadBranch
	case strings.HasPrefix(d.Ref, "refs/heads/"):
		event.Branch = strings.TrimPrefix(d.Ref, "refs/heads/")
	case d.SHA != "":
		heads := 0
		for _, b := range d.Branches {
			if b.Commit.SHA == d.SHA {
				event.Branch = b.Name
				heads++
			}
		}
		if heads != 1 {
			event.Branch = ""
		}
	}
	return event, nil
}
