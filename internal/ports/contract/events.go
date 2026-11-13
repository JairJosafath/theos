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

package contract

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// EventsEnv builds the GitHosts with Events under test and receives what
// their hooks deliver.
type EventsEnv struct {
	// New returns a GitHost of a fresh owner that holds no repository and
	// no hook. It has Events and Issues.
	New func(t *testing.T) ports.GitHost
	// Receiver returns a receiver of t's own for h's deliveries: an
	// httptest listener on a real host, an outbox on an in-memory one.
	Receiver func(t *testing.T, h ports.GitHost) Receiver
	// Hooks are the URLs of h's owner's hooks, as the host holds them.
	Hooks func(t *testing.T, h ports.GitHost) []string
	// Cannot names the subtests the host can't run, with why; they are
	// skipped.
	Cannot map[string]string
}

// Receiver is where a host's deliveries arrive.
type Receiver struct {
	// Bases are two base addresses (scheme, host and port) that both
	// reach the receiver, so a hook can move from one to the other.
	Bases [2]string
	// Received returns the deliveries that arrived so far, oldest first.
	Received func() []Delivery
}

// Delivery is one request a hook sent.
type Delivery struct {
	// URL is where it was sent, query included.
	URL string
	// Header looks up one of its headers.
	Header func(string) string
	// Body is what it carried.
	Body []byte
}

// eventsBranch is the branch the delivery subtest pushes and opens a pull
// request from.
const eventsBranch = "events"

// Events states what the receiver relies on of every GitHost with Events:
// ensuring a hook twice leaves one; a changed URL or secret changes it and
// says so; a hook without a secret is refused; a change to an issue, a pull
// request and a commit status delivers a request, signed with the hook's
// secret, that ParseEvent reads to the right repository, number and branch;
// and a wrong secret, a changed body or a missing signature gives
// ErrBadSignature.
func Events(t *testing.T, env EventsEnv) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	s := eventsSuite{ctx: ctx, env: env}
	for _, test := range []struct {
		name string
		run  func(eventsSuite, *testing.T)
	}{
		{"ensures one hook", eventsSuite.ensuresOneHook},
		{"edits a hook whose URL changed", eventsSuite.changesURL},
		{"takes a new secret", eventsSuite.changesSecret},
		{"delivers changes to issues, pull requests and statuses", eventsSuite.delivers},
		{"refuses a wrong signature", eventsSuite.refusesSignature},
	} {
		t.Run(test.name, func(t *testing.T) {
			if why, ok := env.Cannot[test.name]; ok {
				t.Skip(why)
			}
			test.run(s, t)
		})
	}
}

// eventsSuite holds what every subtest of Events shares.
type eventsSuite struct {
	ctx context.Context
	env EventsEnv
}

// hooked is a fresh host whose hook delivers to its receiver's first base.
type hooked struct {
	host   ports.GitHost
	events ports.Events
	issues ports.Issues
	rcv    Receiver
	hook   ports.EventHook
}

// hooked makes a fresh host and ensures its hook, which it must report as
// made.
func (s eventsSuite) hooked(t *testing.T) hooked {
	t.Helper()
	h := s.env.New(t)
	events, ok := h.(ports.Events)
	if !ok {
		t.Fatalf("%T has no Events", h)
	}
	issues, ok := h.(ports.Issues)
	if !ok {
		t.Fatalf("%T has no Issues", h)
	}
	rcv := s.env.Receiver(t, h)
	path := "/git-events/contract/" + h.Repository(contractRepo).Owner
	hook := ports.EventHook{URL: rcv.Bases[0] + path, Secret: rand.Text()}
	if changed := must(events.EnsureEventHook(s.ctx, hook))(t); !changed {
		t.Fatal("the first EnsureEventHook() reported no change, want the hook made")
	}
	return hooked{host: h, events: events, issues: issues, rcv: rcv, hook: hook}
}

// wantOneHook fails t unless the owner has one hook, and it delivers to to.
func (s eventsSuite) wantOneHook(t *testing.T, x hooked, to string) {
	t.Helper()
	hooks := s.env.Hooks(t, x.host)
	if len(hooks) != 1 || !sameAddress(hooks[0], to) {
		t.Fatalf("the owner's hooks are %q, want one to %s", hooks, to)
	}
}

func (s eventsSuite) ensuresOneHook(t *testing.T) {
	x := s.hooked(t)
	if changed := must(x.events.EnsureEventHook(s.ctx, x.hook))(t); changed {
		t.Error("ensuring the same hook again reported a change")
	}
	s.wantOneHook(t, x, x.hook.URL)
	if _, err := x.events.EnsureEventHook(s.ctx, ports.EventHook{URL: x.hook.URL}); err == nil {
		t.Error("ensuring a hook with no secret succeeded, want an error")
	}
	s.wantOneHook(t, x, x.hook.URL)
}

func (s eventsSuite) changesURL(t *testing.T) {
	x := s.hooked(t)
	moved := x.hook
	u := must(url.Parse(x.hook.URL))(t)
	moved.URL = x.rcv.Bases[1] + u.Path
	if changed := must(x.events.EnsureEventHook(s.ctx, moved))(t); !changed {
		t.Error("ensuring the hook with a changed URL reported no change")
	}
	if changed := must(x.events.EnsureEventHook(s.ctx, moved))(t); changed {
		t.Error("ensuring the moved hook again reported a change")
	}
	s.wantOneHook(t, x, moved.URL)
	repo := must(x.host.EnsureRepository(s.ctx, contractRepo, "the Events contract suite"))(t)
	from := len(x.rcv.Received())
	issue := must(x.issues.CreateIssue(s.ctx, repo, "An issue", "", nil, 0))(t)
	s.await(t, x, from, moved, "the new issue's, to the moved hook", func(e ports.Event) bool {
		return e.Repository == repo.Name && e.Number == issue.Number && e.Branch == ""
	})
}

func (s eventsSuite) changesSecret(t *testing.T) {
	x := s.hooked(t)
	rotated := x.hook
	rotated.Secret = rand.Text()
	if changed := must(x.events.EnsureEventHook(s.ctx, rotated))(t); !changed {
		t.Error("ensuring the hook with a new secret reported no change")
	}
	s.wantOneHook(t, x, rotated.URL)
	repo := must(x.host.EnsureRepository(s.ctx, contractRepo, "the Events contract suite"))(t)
	from := len(x.rcv.Received())
	issue := must(x.issues.CreateIssue(s.ctx, repo, "An issue", "", nil, 0))(t)
	d := s.await(t, x, from, rotated, "the new issue's, signed with the new secret", func(e ports.Event) bool {
		return e.Repository == repo.Name && e.Number == issue.Number
	})
	if _, err := x.events.ParseEvent(d.Header, d.Body, []byte(x.hook.Secret)); !errors.Is(err, ports.ErrBadSignature) {
		t.Errorf("a delivery checked with the old secret gave %v, want ErrBadSignature", err)
	}
}

// delivers changes an issue, a pull request and a commit status, in turn,
// and waits after each for a delivery that names it. A status may name no
// branch (a host's status may name only its commit), and a pull request's
// delivery may name its head or no branch. Each change is looked for only
// among what arrived after it began.
func (s eventsSuite) delivers(t *testing.T) {
	x := s.hooked(t)
	repo := must(x.host.EnsureRepository(s.ctx, contractRepo, "the Events contract suite"))(t)
	head := must(x.host.PushInitialCommit(s.ctx, repo, seedFiles, "Scaffold\n", contractAuthor))(t)
	sha := must(x.host.PushBranch(s.ctx, repo, head, eventsBranch, scaffold.Tree{newTxt: {Data: []byte("new\n")}},
		nil, "Add new.txt", contractAuthor))(t)

	from := len(x.rcv.Received())
	issue := must(x.issues.CreateIssue(s.ctx, repo, "An issue", "", nil, 0))(t)
	s.await(t, x, from, x.hook, "the new issue's", func(e ports.Event) bool {
		return e.Repository == repo.Name && e.Number == issue.Number && e.Branch == ""
	})

	from = len(x.rcv.Received())
	pr := must(x.host.OpenPullRequest(s.ctx, repo, eventsBranch, ports.DefaultBranch, "A change", ""))(t)
	s.await(t, x, from, x.hook, "the new pull request's", func(e ports.Event) bool {
		return e.Repository == repo.Name && e.Number == pr.Number && (e.Branch == "" || e.Branch == eventsBranch)
	})

	from = len(x.rcv.Received())
	if err := x.host.SetCommitStatus(s.ctx, repo, sha, ports.CommitStatus{Context: "contract",
		State: ports.CheckSuccess}); err != nil {
		t.Fatal(err)
	}
	s.await(t, x, from, x.hook, "the commit status's", func(e ports.Event) bool {
		return e.Repository == repo.Name && e.Number == 0 && (e.Branch == "" || e.Branch == eventsBranch)
	})
}

func (s eventsSuite) refusesSignature(t *testing.T) {
	x := s.hooked(t)
	repo := must(x.host.EnsureRepository(s.ctx, contractRepo, "the Events contract suite"))(t)
	from := len(x.rcv.Received())
	issue := must(x.issues.CreateIssue(s.ctx, repo, "An issue", "", nil, 0))(t)
	d := s.await(t, x, from, x.hook, "the new issue's", func(e ports.Event) bool {
		return e.Repository == repo.Name && e.Number == issue.Number
	})
	changed := append(append([]byte(nil), d.Body...), ' ')
	for _, bad := range []struct {
		what   string
		header func(string) string
		body   []byte
		secret []byte
	}{
		{"another secret", d.Header, d.Body, []byte(rand.Text())},
		{"no secret", d.Header, d.Body, nil},
		{"a changed body", d.Header, changed, []byte(x.hook.Secret)},
		{"no headers", func(string) string { return "" }, d.Body, []byte(x.hook.Secret)},
	} {
		if _, err := x.events.ParseEvent(bad.header, bad.body, bad.secret); !errors.Is(err, ports.ErrBadSignature) {
			t.Errorf("a delivery checked with %s gave %v, want ErrBadSignature", bad.what, err)
		}
	}
}

// await waits for a delivery among those x's receiver got after the first
// from that was sent to hook's address and parses, with hook's secret, to an
// event ok accepts, and returns it. Every delivery after from must verify:
// the hook signs all it sends.
func (s eventsSuite) await(t *testing.T, x hooked, from int, hook ports.EventHook, what string,
	ok func(ports.Event) bool) Delivery {
	t.Helper()
	ctx, cancel := context.WithTimeout(s.ctx, waitFor)
	defer cancel()
	for {
		got := x.rcv.Received()
		var seen []string
		for _, d := range got[min(from, len(got)):] {
			e, err := x.events.ParseEvent(d.Header, d.Body, []byte(hook.Secret))
			if err != nil {
				t.Fatalf("a delivery to %s: %v", d.URL, err)
			}
			if sameAddress(d.URL, hook.URL) && ok(e) {
				return d
			}
			seen = append(seen, fmt.Sprintf("%+v to %s", e, d.URL))
		}
		select {
		case <-ctx.Done():
			t.Fatalf("no delivery like %s to %s came; the deliveries were %q", what, hook.URL, seen)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// sameAddress reports whether a and b have the same scheme, host and path:
// a host may add a query to the URL it holds.
func sameAddress(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	return errA == nil && errB == nil && ua.Scheme == ub.Scheme && ua.Host == ub.Host && ua.Path == ub.Path
}
