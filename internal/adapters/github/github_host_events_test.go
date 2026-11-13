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
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/JairJosafath/theos/internal/adapters/github/githubtest"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// listener receives what webhooks deliver.
type listener struct {
	srv *httptest.Server
	mu  sync.Mutex
	got []contract.Delivery
}

func listen(t *testing.T) *listener {
	t.Helper()
	l := &listener{}
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		header := r.Header.Clone()
		l.mu.Lock()
		l.got = append(l.got, contract.Delivery{URL: "http://" + r.Host + r.URL.RequestURI(), Header: header.Get,
			Body: body})
		l.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(l.srv.Close)
	return l
}

// bases are two addresses that both reach the listener.
func (l *listener) bases() [2]string {
	return [2]string{l.srv.URL, strings.Replace(l.srv.URL, "127.0.0.1", "localhost", 1)}
}

func (l *listener) received() []contract.Delivery {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.got)
}

// TestEventsContract covers #79: the GitHub adapter passes the Events
// contract against the fake GitHub, each subtest in an organization of its
// own, whose webhook delivers to a listener of the subtest's own.
func TestEventsContract(t *testing.T) {
	gh := githubtest.New(t, "ada", hostToken)
	orgs := 0
	contract.Events(t, contract.EventsEnv{
		New: func(t *testing.T) ports.GitHost {
			t.Helper()
			orgs++
			org := fmt.Sprintf("events-%d", orgs)
			gh.AddOrg(org)
			h, err := (Client{}).GitHost(ports.Config{URL: gh.URL(), Owner: org, Credentials: ports.Credentials{Token: hostToken}})
			if err != nil {
				t.Fatal(err)
			}
			return h
		},
		Receiver: func(t *testing.T, _ ports.GitHost) contract.Receiver {
			l := listen(t)
			return contract.Receiver{Bases: l.bases(), Received: l.received}
		},
		Hooks: func(_ *testing.T, h ports.GitHost) []string {
			hooks := gh.OrgWebhooks(h.Repository("any").Owner)
			urls := make([]string, 0, len(hooks))
			for _, hook := range hooks {
				urls = append(urls, hook.URL)
			}
			return urls
		},
		// GitHub can do all of it: nothing is skipped.
		Cannot: map[string]string{},
	})
	if calls := gh.Unserved(); len(calls) > 0 {
		t.Errorf("the adapter made calls the fake GitHub doesn't serve: %q", calls)
	}
}

// hooked is a shop whose organization's webhook delivers to a listener.
type hooked struct {
	shop
	rcv  *listener
	hook ports.EventHook
}

const eventsPath = "/git-events/factory/github"

func newHooked(t *testing.T) hooked {
	t.Helper()
	x := hooked{shop: newShop(t), rcv: listen(t)}
	x.hook = ports.EventHook{URL: x.rcv.srv.URL + eventsPath, Secret: rand.Text()}
	x.ensure(t, x.hook, true)
	return x
}

// ensure ensures hook and fails t unless it reports changed.
func (x hooked) ensure(t *testing.T, hook ports.EventHook, changed bool) {
	t.Helper()
	got, err := x.h.EnsureEventHook(x.ctx, hook)
	if err != nil {
		t.Fatal(err)
	}
	if got != changed {
		t.Errorf("EnsureEventHook() reported changed %v, want %v", got, changed)
	}
}

// api makes one call on the fake GitHub as its administrator, as somebody
// with the organization's settings open would.
func (x hooked) api(t *testing.T, method, path string, body any) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(x.ctx, method, x.gh.URL()+"/api/v3"+path, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+hostToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusMultipleChoices {
		answer, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s %s answered %d: %s", method, path, resp.StatusCode, answer)
	}
}

// since parses the deliveries that arrived after the first from with the
// hook's secret, and returns those of kind.
func (x hooked) since(t *testing.T, from int, kind string) []ports.Event {
	t.Helper()
	var out []ports.Event
	for _, d := range x.rcv.received()[from:] {
		e, err := x.h.ParseEvent(d.Header, d.Body, []byte(x.hook.Secret))
		if err != nil {
			t.Fatalf("a delivery to %s: %v", d.URL, err)
		}
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// wantOne fails t unless one delivery of kind arrived after the first from,
// and it concerns want.
func (x hooked) wantOne(t *testing.T, from int, kind string, want ports.Event) {
	t.Helper()
	want.Kind = kind
	if got := x.since(t, from, kind); len(got) != 1 || got[0] != want {
		t.Errorf("the %s deliveries are %+v, want one, %+v", kind, got, want)
	}
}

// TestEventsOnGitHub covers what the Events contract leaves to each host
// (#79): what the webhook asks GitHub for, what somebody changed on it put
// back, the others' webhooks left alone, GitHub's ping, the deliveries the
// contract doesn't cause, and an owner that is a user's account or neither.
func TestEventsOnGitHub(t *testing.T) {
	t.Run("the webhook asks for the events the operator reconciles on, as JSON", webhookAsks)
	t.Run("it puts back what somebody changed on the webhook", webhookHeals)
	t.Run("it removes its duplicates and leaves the others' webhooks alone", webhookDuplicates)
	t.Run("GitHub's ping of the new webhook arrives and concerns no repository", webhookPing)
	t.Run("a push, a tag, a comment, a review, a merge and a run each deliver what they concern", webhookDelivers)
	t.Run("a user's account has a webhook on each repository the operator names", userHooks)
	t.Run("an owner that is neither, and webhooks the token may not manage, are errors that say what to grant",
		noHooks)
}

func webhookAsks(t *testing.T) {
	x := newHooked(t)
	hooks := x.gh.OrgWebhooks("acme")
	if len(hooks) != 1 {
		t.Fatalf("the organization's webhooks are %+v, want one", hooks)
	}
	got := hooks[0]
	want := []string{"issues", "issue_comment", "pull_request", "pull_request_review", "status", "check_run",
		"workflow_run", "push", "release"}
	if !slices.Equal(got.Events, want) {
		t.Errorf("the webhook asks for %q, want %q", got.Events, want)
	}
	if got.ContentType != "json" || !got.Active || got.Secret != x.hook.Secret {
		t.Errorf("the webhook is %+v, want active, JSON, and signed with the hook's secret", got)
	}
	u, err := url.Parse(got.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != eventsPath || u.Query().Get(fingerprintQuery) != secretFingerprint(x.hook.Secret) {
		t.Errorf("the webhook delivers to %s, want %s with the secret's fingerprint", got.URL, eventsPath)
	}
	if strings.Contains(got.URL, x.hook.Secret) {
		t.Errorf("the webhook's URL %s holds its secret", got.URL)
	}
	if hooks := x.gh.RepoWebhooks("acme", "shop"); len(hooks) != 0 {
		t.Errorf("the repository has webhooks of its own, %+v, want the organization's alone", hooks)
	}
}

func webhookHeals(t *testing.T) {
	x := newHooked(t)
	made := x.gh.OrgWebhooks("acme")[0]
	// Somebody turns it off, narrows it and gives it a secret of their own.
	x.api(t, http.MethodPatch, fmt.Sprintf("/orgs/acme/hooks/%d", made.ID), map[string]any{
		"active": false, "events": []string{"push"},
		"config": map[string]string{"url": made.URL, "content_type": "form", "secret": rand.Text()},
	})
	// The URL is the one wanted, so nothing the operator set changed.
	x.ensure(t, x.hook, false)
	hooks := x.gh.OrgWebhooks("acme")
	if len(hooks) != 1 {
		t.Fatalf("the organization's webhooks are %+v, want one", hooks)
	}
	got := hooks[0]
	if got.ID != made.ID || !got.Active || got.ContentType != "json" || got.Secret != x.hook.Secret ||
		!slices.Contains(got.Events, "issues") {
		t.Errorf("the webhook is %+v, want the one made, active, JSON, signed with the hook's secret, for every event",
			got)
	}
	from := len(x.rcv.received())
	if _, err := x.h.CreateIssue(x.ctx, x.repo, "An issue", "", nil, 0); err != nil {
		t.Fatal(err)
	}
	x.wantOne(t, from, "issues", ports.Event{Repository: "shop", Number: 1})
}

func webhookDuplicates(t *testing.T) {
	x := newHooked(t)
	const builds = "https://ci.example.com/hooks/build"
	for _, at := range []string{"https://old.example.com" + eventsPath, builds,
		"https://older.example.com" + eventsPath + "?fingerprint=0"} {
		// Not active: the fake pings a webhook that is, and these go nowhere.
		x.api(t, http.MethodPost, "/orgs/acme/hooks", map[string]any{"name": "web", "active": false,
			"config": map[string]string{"url": at, "content_type": "json"}, "events": []string{"push"}})
	}
	x.ensure(t, x.hook, false)
	hooks := x.gh.OrgWebhooks("acme")
	urls := make([]string, 0, len(hooks))
	for _, hook := range hooks {
		urls = append(urls, hook.URL)
	}
	if len(urls) != 2 || !strings.HasPrefix(urls[0], x.hook.URL+"?") || urls[1] != builds {
		t.Errorf("the organization's webhooks are %q, want the operator's and %s", urls, builds)
	}
}

func webhookPing(t *testing.T) {
	x := newHooked(t)
	got := x.rcv.received()
	if len(got) != 1 || got[0].Header("X-GitHub-Event") != "ping" {
		t.Fatalf("%d deliveries arrived on making the webhook, want GitHub's ping", len(got))
	}
	e, err := x.h.ParseEvent(got[0].Header, got[0].Body, []byte(x.hook.Secret))
	if want := (ports.Event{Kind: "ping"}); err != nil || e != want {
		t.Errorf("the ping reads as %+v, %v, want %+v", e, err, want)
	}
}

func webhookDelivers(t *testing.T) {
	x := newHooked(t)
	const branch, tag = "feature", "v1.0.0"

	from := len(x.rcv.received())
	head, err := x.h.PushBranch(x.ctx, x.repo, x.head, branch, scaffold.Tree{"new.txt": {Data: []byte("new\n")}}, nil,
		"Add new.txt", shopAuthor)
	if err != nil {
		t.Fatal(err)
	}
	x.wantOne(t, from, "push", ports.Event{Repository: "shop", Branch: branch})

	from = len(x.rcv.received())
	if err := x.h.CreateTag(x.ctx, x.repo, tag, x.head, "The first release"); err != nil {
		t.Fatal(err)
	}
	x.wantOne(t, from, "push", ports.Event{Repository: "shop"})

	pr, err := x.h.OpenPullRequest(x.ctx, x.repo, branch, ports.DefaultBranch, "A change", "")
	if err != nil {
		t.Fatal(err)
	}
	from = len(x.rcv.received())
	if err := x.h.CommentIssue(x.ctx, x.repo, pr.Number, "A comment"); err != nil {
		t.Fatal(err)
	}
	x.wantOne(t, from, "issue_comment", ports.Event{Repository: "shop", Number: pr.Number})

	from = len(x.rcv.received())
	if err := x.as(t, "linus").ReviewPullRequest(x.ctx, x.repo, pr.Number, head, ports.ReviewApprove, ""); err != nil {
		t.Fatal(err)
	}
	x.wantOne(t, from, "pull_request_review", ports.Event{Repository: "shop", Number: pr.Number, Branch: branch})

	from = len(x.rcv.received())
	run := x.gh.AddRun("acme", "shop", "check.yaml", "pull_request", branch, head, pr.Number)
	x.wantOne(t, from, "workflow_run", ports.Event{Repository: "shop", Branch: branch})
	from = len(x.rcv.received())
	x.gh.AddJob("acme", "shop", run, "check", "completed", "success", "")
	x.wantOne(t, from, "check_run", ports.Event{Repository: "shop", Branch: branch})
	x.wantOne(t, from, "workflow_run", ports.Event{Repository: "shop", Branch: branch})

	from = len(x.rcv.received())
	if err := x.h.MergePullRequest(x.ctx, x.repo, pr.Number, head, "A change", ""); err != nil {
		t.Fatal(err)
	}
	// The merge moves main, and the merged branch goes with it.
	pushes := x.since(t, from, "push")
	if len(pushes) != 2 || pushes[0].Branch != ports.DefaultBranch || pushes[1].Branch != branch {
		t.Errorf("the merge's push deliveries are %+v, want main's and then the merged branch's", pushes)
	}
	x.wantOne(t, from, "pull_request", ports.Event{Repository: "shop", Number: pr.Number, Branch: branch})
}

// userHooks covers the owner GitHub gives no webhook of its own: the
// token's own account. The webhook is each named repository's, of those
// that exist.
func userHooks(t *testing.T) {
	ctx := context.Background()
	gh := githubtest.New(t, "ada", hostToken)
	h, err := (Client{}).Host(ports.Config{URL: gh.URL(), Owner: "ada", Credentials: ports.Credentials{Token: hostToken}})
	if err != nil {
		t.Fatal(err)
	}
	shop, err := h.EnsureRepository(ctx, "shop", "")
	if err != nil {
		t.Fatal(err)
	}
	blog, err := h.EnsureRepository(ctx, "blog", "")
	if err != nil {
		t.Fatal(err)
	}
	rcv := listen(t)
	hook := ports.EventHook{URL: rcv.srv.URL + eventsPath, Secret: rand.Text()}

	if changed, err := h.EnsureEventHook(ctx, hook); err != nil || changed {
		t.Errorf("with no repository named EnsureEventHook() = %v, %v, want nothing to do", changed, err)
	}
	hook.Repositories = []string{"shop", "not-made-yet"}
	for _, want := range []bool{true, false} {
		if changed, err := h.EnsureEventHook(ctx, hook); err != nil || changed != want {
			t.Fatalf("EnsureEventHook() = %v, %v, want changed %v", changed, err, want)
		}
	}
	if hooks := gh.RepoWebhooks("ada", "shop"); len(hooks) != 1 || hooks[0].Secret != hook.Secret {
		t.Errorf("shop's webhooks are %+v, want one, signed with the hook's secret", hooks)
	}
	if hooks := gh.RepoWebhooks("ada", "blog"); len(hooks) != 0 {
		t.Errorf("blog, which the operator didn't name, has webhooks: %+v", hooks)
	}

	from := len(rcv.received())
	for _, repo := range []ports.Repository{shop, blog} {
		if _, err := h.CreateIssue(ctx, repo, "An issue", "", nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	got := rcv.received()[from:]
	if len(got) != 1 {
		t.Fatalf("%d deliveries arrived for an issue in each repository, want shop's alone", len(got))
	}
	e, err := h.ParseEvent(got[0].Header, got[0].Body, []byte(hook.Secret))
	if want := (ports.Event{Repository: "shop", Number: 1, Kind: "issues"}); err != nil || e != want {
		t.Errorf("the delivery reads as %+v, %v, want %+v", e, err, want)
	}

	hook.Secret = rand.Text()
	hook.Repositories = []string{"shop", "blog"}
	if changed, err := h.EnsureEventHook(ctx, hook); err != nil || !changed {
		t.Errorf("with a new secret and a second repository EnsureEventHook() = %v, %v, want changed", changed, err)
	}
	for _, name := range hook.Repositories {
		if hooks := gh.RepoWebhooks("ada", name); len(hooks) != 1 || hooks[0].Secret != hook.Secret {
			t.Errorf("%s's webhooks are %+v, want one, signed with the new secret", name, hooks)
		}
	}
	if calls := gh.Unserved(); len(calls) > 0 {
		t.Errorf("the adapter made calls the fake GitHub doesn't serve: %q", calls)
	}
}

func noHooks(t *testing.T) {
	ctx := context.Background()
	gh := githubtest.New(t, "ada", hostToken)
	hook := ports.EventHook{URL: "https://factory.example.com" + eventsPath, Secret: rand.Text(),
		Repositories: []string{"shop"}}

	h, err := (Client{}).Host(ports.Config{URL: gh.URL(), Owner: "nobody", Credentials: ports.Credentials{Token: hostToken}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.EnsureEventHook(ctx, hook); err == nil || !strings.Contains(err.Error(), "admin:org_hook") ||
		!strings.Contains(err.Error(), "nobody") {
		t.Errorf("for an owner that is no organization and not the token's account EnsureEventHook() gave %v, "+
			"want an error naming the owner and admin:org_hook", err)
	}

	// grace owns a repository and may not manage its webhooks, as a token
	// without admin:repo_hook.
	token := rand.Text()
	gh.AddUser("grace", token)
	h, err = (Client{}).Host(ports.Config{URL: gh.URL(), Owner: "grace", Credentials: ports.Credentials{Token: token}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.EnsureRepository(ctx, "shop", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.EnsureEventHook(ctx, hook); err == nil || !strings.Contains(err.Error(), "admin:repo_hook") ||
		!strings.Contains(err.Error(), "grace/shop") {
		t.Errorf("for a repository whose webhooks the token may not manage EnsureEventHook() gave %v, "+
			"want an error naming the repository and admin:repo_hook", err)
	}
}

// What GitHub's documentation gives to check a receiver against
// (docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries,
// "Testing the webhook payload validation"): a secret, a payload and the
// signature header the two must give. The secret is the page's own example
// and signs nothing else.
const (
	documentedSecret    = "It's a Secret to Everybody"
	documentedPayload   = "Hello, World!"
	documentedSignature = "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
)

// signedBy returns the headers GitHub sends with body for an event of kind,
// from a webhook whose secret is secret.
func signedBy(secret, kind string, body []byte) func(string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	header := http.Header{}
	header.Set("X-GitHub-Event", kind)
	header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return header.Get
}

// TestParseEventSignature covers #79: the signature ParseEvent checks is
// the one GitHub documents, and no other header or form of it passes.
func TestParseEventSignature(t *testing.T) {
	h, err := (Client{}).Host(ports.Config{Owner: "acme", Credentials: ports.Credentials{Token: hostToken}})
	if err != nil {
		t.Fatal(err)
	}
	only := func(name, value string) func(string) string {
		header := http.Header{}
		header.Set(name, value)
		return header.Get
	}
	// The documented payload is no JSON, so it reads to an error, which is
	// not the signature's: the signature passed.
	_, err = h.ParseEvent(only("X-Hub-Signature-256", documentedSignature), []byte(documentedPayload),
		[]byte(documentedSecret))
	if err == nil || errors.Is(err, ports.ErrBadSignature) {
		t.Errorf("GitHub's documented payload, secret and signature gave %v, want a signature that passes", err)
	}
	digest := strings.TrimPrefix(documentedSignature, "sha256=")
	for what, header := range map[string]func(string) string{
		"the digest without sha256=":        only("X-Hub-Signature-256", digest),
		"the digest under the SHA-1 header": only("X-Hub-Signature", documentedSignature),
		"a digest that is no hex":           only("X-Hub-Signature-256", "sha256=not-hex"),
		"an empty digest":                   only("X-Hub-Signature-256", "sha256="),
		"another payload's digest":          signedBy(documentedSecret, "issues", []byte("Hello, World?")),
	} {
		if _, err := h.ParseEvent(header, []byte(documentedPayload), []byte(documentedSecret)); !errors.Is(err, ports.ErrBadSignature) {
			t.Errorf("%s gave %v, want ErrBadSignature", what, err)
		}
	}
}

// TestParseEvent covers #79: each event the webhook asks for maps to the
// repository, the issue or pull request and the branch the receiver
// reconciles. The payloads are the examples of GitHub's deliveries that
// github.com/octokit/webhooks records (payload-examples/api.github.com),
// which GitHub's documentation showed for each event, cut down to the
// fields that say what a delivery concerns and those beside them a reader
// could take for them; where a case changes an example, its name says so.
func TestParseEvent(t *testing.T) {
	h, err := (Client{}).Host(ports.Config{Owner: "Codertocat", Credentials: ports.Credentials{Token: hostToken}})
	if err != nil {
		t.Fatal(err)
	}
	const (
		helloWorld = `"repository":{"id":186853002,"name":"Hello-World","full_name":"Codertocat/Hello-World",` +
			`"owner":{"login":"Codertocat"},"default_branch":"master"},"sender":{"login":"Codertocat"}`
		pullRequest = `"pull_request":{"number":2,"state":"open","title":"Update the README with new information.",` +
			`"head":{"label":"Codertocat:changes","ref":"changes","sha":"ec26c3e57ca3a959ca5aad62de7213c562f8c821",` +
			`"repo":{"name":"Hello-World","full_name":"%s"}},` +
			`"base":{"label":"Codertocat:master","ref":"master","sha":"f95f852bd8fca8fcc58a9a2d6c842781e32a215e",` +
			`"repo":{"name":"Hello-World","full_name":"Codertocat/Hello-World"}}}`
		status = `{"id":6805126730,"sha":"%s","name":"Codertocat/Hello-World","context":"default","state":"success",` +
			`"branches":[{"name":"master","commit":{"sha":"f95f852bd8fca8fcc58a9a2d6c842781e32a215e"},"protected":false},` +
			`{"name":"changes","commit":{"sha":"ec26c3e57ca3a959ca5aad62de7213c562f8c821"},"protected":false},` +
			`{"name":"gh-pages","commit":{"sha":"507fc9acd0d04ac4a9db87d12cb228c052cd813a"},"protected":false}],` + helloWorld + `}`
		hello = "Hello-World"
	)
	here := fmt.Sprintf(pullRequest, "Codertocat/Hello-World")
	for _, c := range []struct {
		name, kind, body string
		want             ports.Event
	}{
		{"an issue opened", "issues",
			`{"action":"opened","issue":{"number":1,"title":"Spelling error in the README file","state":"open"},` + helloWorld + `}`,
			ports.Event{Repository: hello, Number: 1}},
		{"a comment on an issue", "issue_comment",
			`{"action":"created","issue":{"number":1,"state":"open"},"comment":{"id":492700400,` +
				`"body":"You are totally right! I'll get this fixed right away."},` + helloWorld + `}`,
			ports.Event{Repository: hello, Number: 1}},
		{"a pull request opened", "pull_request",
			`{"action":"opened","number":2,` + here + `,` + helloWorld + `}`,
			ports.Event{Repository: hello, Number: 2, Branch: "changes"}},
		{"a pull request opened, changed to come from a fork: its branch is none of the repository's", "pull_request",
			`{"action":"opened","number":2,` + fmt.Sprintf(pullRequest, "Octocoders/Hello-World") + `,` + helloWorld + `}`,
			ports.Event{Repository: hello, Number: 2}},
		{"a review submitted", "pull_request_review",
			`{"action":"submitted","review":{"id":237895671,"state":"commented",` +
				`"commit_id":"ec26c3e57ca3a959ca5aad62de7213c562f8c821"},` + here + `,` + helloWorld + `}`,
			ports.Event{Repository: hello, Number: 2, Branch: "changes"}},
		{"a status on a commit that is the newest of none of its branches", "status",
			fmt.Sprintf(status, "6113728f27ae82c7b1a177c8d03f9e96e0adf246"),
			ports.Event{Repository: hello}},
		{"a status, changed to be on the newest commit of one branch", "status",
			fmt.Sprintf(status, "ec26c3e57ca3a959ca5aad62de7213c562f8c821"),
			ports.Event{Repository: hello, Branch: "changes"}},
		{"a check run completed", "check_run",
			`{"action":"completed","check_run":{"id":128620228,"name":"Octocoders-linter",` +
				`"head_sha":"ec26c3e57ca3a959ca5aad62de7213c562f8c821","status":"completed","conclusion":"success",` +
				`"check_suite":{"id":118578147,"head_branch":"changes","head_sha":"ec26c3e57ca3a959ca5aad62de7213c562f8c821"},` +
				`"pull_requests":[{"number":2,"head":{"ref":"changes"},"base":{"ref":"master"}}]},` + helloWorld + `}`,
			ports.Event{Repository: hello, Branch: "changes"}},
		{"a workflow run completed", "workflow_run",
			`{"action":"completed","workflow_run":{"id":289782451,"head_branch":"master",` +
				`"head_sha":"3484a3fb816e0859fd6e1cea078d76385ff50625","event":"repository_dispatch","status":"completed",` +
				`"conclusion":"success","pull_requests":[]},"workflow":{"path":".github/workflows/test.yml"},` +
				`"repository":{"name":"octo-repo","full_name":"octo-org/octo-repo"},"organization":{"login":"octo-org"}}`,
			ports.Event{Repository: "octo-repo", Branch: "master"}},
		{"a push that made a branch", "push",
			`{"ref":"refs/heads/master","before":"0000000000000000000000000000000000000000",` +
				`"after":"6113728f27ae82c7b1a177c8d03f9e96e0adf246","created":true,"deleted":false,"forced":false,` +
				`"base_ref":null,` + helloWorld + `}`,
			ports.Event{Repository: hello, Branch: "master"}},
		{"a push that deleted a tag", "push",
			`{"ref":"refs/tags/simple-tag","before":"6113728f27ae82c7b1a177c8d03f9e96e0adf246",` +
				`"after":"0000000000000000000000000000000000000000","created":false,"deleted":true,"forced":false,` +
				`"base_ref":null,` + helloWorld + `}`,
			ports.Event{Repository: hello}},
		{"a release published", "release",
			`{"action":"published","release":{"id":17372790,"tag_name":"0.0.1","target_commitish":"master",` +
				`"draft":false,"prerelease":false},` + helloWorld + `}`,
			ports.Event{Repository: hello}},
		{"the ping of a repository's webhook", "ping",
			`{"zen":"Anything added dilutes everything else.","hook_id":109948940,"hook":{"type":"Repository",` +
				`"id":109948940,"name":"web","active":true,"events":["*"],"config":{"content_type":"json",` +
				`"url":"https://smee.io/YEbF2t8kjvPAn7Ck","insecure_ssl":"0"}},` + helloWorld + `}`,
			ports.Event{Repository: hello}},
		{"the ping of an organization's webhook, which names no repository", "ping",
			`{"zen":"Anything added dilutes everything else.","hook_id":109948940,"hook":{"type":"Organization",` +
				`"id":266738161,"name":"web","active":true,"events":["member","repository"],"config":{"content_type":"json",` +
				`"insecure_ssl":"0","secret":"********","url":"https://smee.io/YEbF2t8kjvPAn7Ck"}},` +
				`"organization":{"login":"Octocoders","id":38302899},"sender":{"login":"Codertocat"}}`,
			ports.Event{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !json.Valid([]byte(c.body)) {
				t.Fatalf("the payload is no JSON: %s", c.body)
			}
			secret := rand.Text()
			c.want.Kind = c.kind
			got, err := h.ParseEvent(signedBy(secret, c.kind, []byte(c.body)), []byte(c.body), []byte(secret))
			if err != nil || got != c.want {
				t.Errorf("ParseEvent() = %+v, %v, want %+v", got, err, c.want)
			}
		})
	}
}
