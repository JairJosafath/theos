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

package gitea

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// delivery is one request Gitea sent the test's listener.
type delivery struct {
	header http.Header
	body   []byte
}

// TestGiteaEventsIntegration registers a new organization's event hook with a
// listener of the test's own, ensures it again, with a duplicate and with a
// changed URL, and then makes in a repository of the organization one event of
// each kind the hook asks for, which must each arrive signed and parse to the
// repository, issue or pull request and branch it concerns.
// THEOS_GITEA_EVENTS_CAPTURE=1 writes one delivery of each kind to
// testdata/events, which TestParseEvent reads.
func TestGiteaEventsIntegration(t *testing.T) {
	base := os.Getenv("THEOS_GITEA_URL")
	if base == "" {
		t.Skip("THEOS_GITEA_URL not set")
	}
	creds := ports.Credentials{Username: os.Getenv("THEOS_GITEA_USERNAME"), Token: os.Getenv("THEOS_GITEA_TOKEN")}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var mu sync.Mutex
	var got []delivery
	listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, delivery{header: r.Header.Clone(), body: body})
		mu.Unlock()
	}))
	defer listener.Close()

	stamp := time.Now().UnixNano()
	org := fmt.Sprintf("it-events-%d", stamp)
	createOrg(t, base, org, creds.Token)
	p, err := Client{}.Provider(ports.Config{URL: base, Owner: org, Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	events, ok := p.(ports.Events)
	if !ok {
		t.Fatalf("%T has no Events", p)
	}
	secret := checkEventHook(ctx, t, events, base, creds.Token, org, listener.URL)

	// One event of each kind.
	name := "events"
	t.Cleanup(func() { deleteRepo(t, base, org, name, creds.Token) })
	repo, err := p.EnsureRepository(ctx, name, "events integration test")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := p.PushInitialCommit(ctx, repo, scaffold.Tree{
		"README.md": {Data: []byte("hi")},
		".gitea/workflows/ci.yaml": {Data: []byte(
			"on: push\njobs:\n  check:\n    runs-on: no-such-runner\n    steps:\n      - run: \"true\"\n")},
	}, "Scaffold\n", testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	api := "/api/v1/repos/" + org + "/" + name
	reviewer := fmt.Sprintf("it-reviewer-%d", stamp)
	giteaJSON(t, base, creds.Token, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"username": reviewer, "email": reviewer + "@theos.factory.io", "password": rand.Text(), "must_change_password": false,
	}, nil)
	t.Cleanup(func() {
		giteaJSON(t, base, creds.Token, http.MethodDelete, "/api/v1/admin/users/"+reviewer+"?purge=true", nil, nil)
	})
	giteaJSON(t, base, creds.Token, http.MethodPut, api+"/collaborators/"+reviewer, map[string]string{"permission": "write"}, nil)

	var label struct {
		ID int64 `json:"id"`
	}
	giteaJSON(t, base, creds.Token, http.MethodPost, api+"/labels", map[string]string{"name": "ready", "color": "#00aa00"}, &label)
	giteaJSON(t, base, creds.Token, http.MethodPost, api+"/issues", map[string]string{"title": "An issue"}, nil)
	giteaJSON(t, base, creds.Token, http.MethodPost, api+"/issues/1/comments", map[string]string{"body": "a comment"}, nil)
	giteaJSON(t, base, creds.Token, http.MethodPost, api+"/issues/1/labels", map[string]any{"labels": []int64{label.ID}}, nil)
	giteaJSON(t, base, creds.Token, http.MethodPatch, api+"/issues/1", map[string]any{"assignees": []string{creds.Username}}, nil)

	if _, err := p.PushBranch(ctx, repo, initial, "feature", scaffold.Tree{"a.txt": {Data: []byte("a")}}, nil, "Add a\n", testAuthor); err != nil {
		t.Fatal(err)
	}
	giteaJSON(t, base, creds.Token, http.MethodPost, api+"/pulls", map[string]string{
		"title": "A change", "head": "feature", "base": ports.DefaultBranch,
	}, nil)
	// PushBranch builds on a commit of main, so the sync is a force-push of
	// a second commit in place of the first.
	head, err := p.PushBranch(ctx, repo, initial, "feature", scaffold.Tree{
		"a.txt": {Data: []byte("a")}, "b.txt": {Data: []byte("b")},
	}, nil, "Add a and b\n", testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	giteaJSON(t, base, creds.Token, http.MethodPost, api+"/issues/2/labels", map[string]any{"labels": []int64{label.ID}}, nil)
	giteaJSON(t, base, creds.Token, http.MethodPost, api+"/issues/2/comments", map[string]string{"body": "a comment"}, nil)
	for _, review := range []string{"APPROVED", "REQUEST_CHANGES", "COMMENT"} {
		sudo(t, base, creds.Token, reviewer, api+"/pulls/2/reviews", map[string]string{"event": review, "body": "a review"})
	}
	giteaJSON(t, base, creds.Token, http.MethodPost, api+"/statuses/"+head, map[string]string{"state": "success", "context": "ci"}, nil)
	giteaJSON(t, base, creds.Token, http.MethodPost, api+"/releases", map[string]string{"tag_name": "v1.0.0"}, nil)

	want := map[string]ports.Event{
		"issues":                       {Repository: name, Number: 1},
		"issue_comment":                {Repository: name, Number: 1},
		"issue_label":                  {Repository: name, Number: 1},
		"issue_assign":                 {Repository: name, Number: 1},
		"create":                       {Repository: name, Branch: "feature"},
		"push":                         {Repository: name, Branch: "feature"},
		"pull_request":                 {Repository: name, Number: 2},
		"pull_request_sync":            {Repository: name, Number: 2},
		"pull_request_label":           {Repository: name, Number: 2},
		"pull_request_comment":         {Repository: name, Number: 2},
		"pull_request_review_approved": {Repository: name, Number: 2},
		"pull_request_review_rejected": {Repository: name, Number: 2},
		"pull_request_review_comment":  {Repository: name, Number: 2},
		"status":                       {Repository: name},
		"release":                      {Repository: name},
		"workflow_run":                 {Repository: name, Branch: ports.DefaultBranch},
	}
	// found is a delivery of each kind that parsed to what it should.
	found := map[string]delivery{}
	deadline := time.Now().Add(time.Minute)
	for len(found) < len(want) && time.Now().Before(deadline) {
		mu.Lock()
		seen := append([]delivery(nil), got...)
		mu.Unlock()
		for _, d := range seen {
			kind := d.header.Get("X-Gitea-Event-Type")
			event, err := events.ParseEvent(d.header.Get, d.body, []byte(secret))
			if err != nil {
				t.Fatalf("a %s delivery: %v", kind, err)
			}
			if w, ok := want[kind]; ok && event.Repository == w.Repository && event.Number == w.Number && event.Branch == w.Branch {
				found[kind] = d
			}
		}
		time.Sleep(time.Second)
	}
	for kind, w := range want {
		if _, ok := found[kind]; !ok {
			t.Errorf("no %s delivery parsed to %+v", kind, w)
		}
	}
	if d, ok := found["issues"]; ok {
		if _, err := events.ParseEvent(d.header.Get, d.body, []byte(rand.Text())); !errors.Is(err, ports.ErrBadSignature) {
			t.Errorf("a delivery checked with another secret gave %v, want ErrBadSignature", err)
		}
	}
	if os.Getenv("THEOS_GITEA_EVENTS_CAPTURE") != "" {
		captureEvents(t, found)
	}
}

// checkEventHook ensures org's event hook to listener: twice, with a
// duplicate and a hook to another path, with a changed URL, with a new
// secret and with none. It returns the secret the hook has at the end.
func checkEventHook(ctx context.Context, t *testing.T, events ports.Events, base, token, org, listener string) string {
	t.Helper()
	hooks := func() []giteaHook {
		t.Helper()
		var out []giteaHook
		giteaJSON(t, base, token, http.MethodGet, "/api/v1/orgs/"+org+"/hooks", nil, &out)
		return out
	}

	secret := rand.Text()
	path := "/git-events/c4-system/" + org
	hook := ports.EventHook{URL: listener + path, Secret: secret}
	if changed, err := events.EnsureEventHook(ctx, hook); err != nil || !changed {
		t.Fatalf("the first EnsureEventHook = %v, %v; want it created", changed, err)
	}
	if changed, err := events.EnsureEventHook(ctx, hook); err != nil || changed {
		t.Fatalf("ensuring the hook again = %v, %v; want it unchanged", changed, err)
	}
	if n := len(hooks()); n != 1 {
		t.Fatalf("ensuring the hook twice left %d hooks, want 1", n)
	}

	// A duplicate with the same path goes; a hook to another path stays.
	for _, u := range []string{listener + path, listener + "/elsewhere"} {
		giteaJSON(t, base, token, http.MethodPost, "/api/v1/orgs/"+org+"/hooks", map[string]any{
			"type": "gitea", "active": true, "events": []string{"push"},
			"config": map[string]string{"url": u, "content_type": "json"},
		}, nil)
	}
	if changed, err := events.EnsureEventHook(ctx, hook); err != nil || changed {
		t.Fatalf("ensuring the hook with a duplicate = %v, %v; want it unchanged", changed, err)
	}
	if n := len(hooks()); n != 2 {
		t.Fatalf("the duplicate was not removed: %d hooks, want the hook and the one to another path", n)
	}
	giteaJSON(t, base, token, http.MethodDelete, fmt.Sprintf("/api/v1/orgs/%s/hooks/%d", org, otherHook(t, hooks(), path).ID), nil, nil)

	// localhost is the same listener under another URL.
	hook.URL = strings.Replace(listener, "127.0.0.1", "localhost", 1) + path
	if changed, err := events.EnsureEventHook(ctx, hook); err != nil || !changed {
		t.Fatalf("ensuring the hook with a changed URL = %v, %v; want it changed", changed, err)
	}
	// Rotating is ensuring with a new secret; the deliveries below are
	// checked with it.
	secret = rand.Text()
	hook.Secret = secret
	if changed, err := events.EnsureEventHook(ctx, hook); err != nil || !changed {
		t.Fatalf("ensuring the hook with a new secret = %v, %v; want it made again", changed, err)
	}
	if _, err := events.EnsureEventHook(ctx, ports.EventHook{URL: hook.URL}); err == nil {
		t.Error("ensuring the hook with no secret succeeded, want an error")
	}
	all := hooks()
	if len(all) != 1 || !strings.HasPrefix(all[0].Config.URL, hook.URL+"?") {
		t.Fatalf("after the URL changed the hooks are %+v, want one to %s", all, hook.URL)
	}
	var full struct {
		Events []string `json:"events"`
	}
	giteaJSON(t, base, token, http.MethodGet, fmt.Sprintf("/api/v1/orgs/%s/hooks/%d", org, all[0].ID), nil, &full)
	if missing := without(hookEvents, full.Events); len(missing) > 0 {
		t.Errorf("Gitea dropped events from the hook: %v (it has %v)", missing, full.Events)
	}
	return secret
}

// otherHook is the one hook whose URL's path is not path.
func otherHook(t *testing.T, hooks []giteaHook, path string) giteaHook {
	t.Helper()
	for _, h := range hooks {
		if u, err := url.Parse(h.Config.URL); err == nil && u.Path != path {
			return h
		}
	}
	t.Fatalf("no hook to another path than %s in %+v", path, hooks)
	return giteaHook{}
}

// without is what of want is not in have.
func without(want, have []string) []string {
	var out []string
	for _, w := range want {
		found := false
		for _, h := range have {
			found = found || h == w
		}
		if !found {
			out = append(out, w)
		}
	}
	return out
}

// sudo posts body to path as user, through the admin's token.
func sudo(t *testing.T, base, token, user, path string, body map[string]string) {
	t.Helper()
	data := []byte(fmt.Sprintf(`{"event":%q,"body":%q}`, body["event"], body["body"]))
	req, _ := http.NewRequest(http.MethodPost, strings.TrimSuffix(base, "/")+path, bytes.NewReader(data))
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Sudo", user)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		answer, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s as %s: HTTP %d: %s", path, user, resp.StatusCode, answer)
	}
}

// captureEvents writes the body, compacted, and event header of a delivery of
// each kind to testdata/events/<kind>.json and .event.
func captureEvents(t *testing.T, found map[string]delivery) {
	t.Helper()
	dir := filepath.Join("testdata", "events")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for kind, d := range found {
		var compact bytes.Buffer
		if err := json.Compact(&compact, d.body); err != nil {
			t.Fatal(err)
		}
		compact.WriteByte('\n')
		if err := os.WriteFile(filepath.Join(dir, kind+".json"), compact.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, kind+".event"), []byte(d.header.Get("X-Gitea-Event")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
