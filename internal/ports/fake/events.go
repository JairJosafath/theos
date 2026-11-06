package fake

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/JairJosafath/theos/internal/ports"
)

// The fake host's event hooks: the owner's hook, held as EnsureEventHook
// leaves it, and the outbox of the deliveries it signs and queues when an
// issue, a pull request or a commit status changes. Nothing is sent: a test
// reads the outbox with Deliveries.

var _ ports.Events = (*Git)(nil)

// The headers of a fake delivery: its kind, and the hex HMAC-SHA256 of its
// body under the hook's secret, after "sha256=".
const (
	EventHeader     = "X-Fake-Event"
	SignatureHeader = "X-Fake-Signature"
	signaturePrefix = "sha256="
)

// The kinds of fake deliveries.
const (
	EventIssue       = "issue"
	EventPullRequest = "pull_request"
	EventStatus      = "status"
)

// Delivery is one request a fake hook sent.
type Delivery struct {
	// URL is the hook's URL.
	URL string
	// Header holds EventHeader and SignatureHeader.
	Header map[string]string
	// Body is the JSON of what the delivery concerns.
	Body []byte
}

// deliveryBody is what a fake delivery's body holds.
type deliveryBody struct {
	Repository string `json:"repository"`
	Number     int    `json:"number,omitempty"`
	Branch     string `json:"branch,omitempty"`
}

// eventStore is every owner's hooks and what they delivered.
type eventStore struct {
	hooks  map[string][]ports.EventHook // owner, oldest first
	outbox map[string][]Delivery        // owner, oldest first
}

func (s *state) eventState() *eventStore {
	if s.events == nil {
		s.events = &eventStore{hooks: map[string][]ports.EventHook{}, outbox: map[string][]Delivery{}}
	}
	return s.events
}

// EnsureEventHook makes the owner's hook deliver to hook.URL, signed with
// hook.Secret: the hook whose URL has the same path is kept, or made, with
// that URL and secret, and the others with that path removed. It reports
// whether the URL or the secret changed.
func (g *Git) EnsureEventHook(_ context.Context, hook ports.EventHook) (bool, error) {
	want, err := url.Parse(hook.URL)
	if err != nil || want.Host == "" {
		return false, fmt.Errorf("the event hook's URL %q is not an absolute URL", hook.URL)
	}
	if hook.Secret == "" {
		return false, fmt.Errorf("the event hook to %s has no secret", hook.URL)
	}
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.eventState()
	owner := g.cfg.Owner
	changed := true
	kept := []ports.EventHook{}
	for _, h := range s.hooks[owner] {
		// Every hook held parsed when it was ensured.
		u, _ := url.Parse(h.URL)
		switch {
		case u.Path != want.Path:
			kept = append(kept, h)
		case h == hook:
			changed = false
		}
	}
	s.hooks[owner] = append(kept, hook)
	return changed, nil
}

// ParseEvent checks SignatureHeader against secret and reads what the
// delivery concerns.
func (g *Git) ParseEvent(header func(string) string, body, secret []byte) (ports.Event, error) {
	hexSum, ok := strings.CutPrefix(header(SignatureHeader), signaturePrefix)
	signature, err := hex.DecodeString(hexSum)
	if !ok || err != nil || len(signature) == 0 || len(secret) == 0 ||
		!hmac.Equal(signature, sign(secret, body)) {
		return ports.Event{}, ports.ErrBadSignature
	}
	event := ports.Event{Kind: header(EventHeader)}
	var d deliveryBody
	if err := json.Unmarshal(body, &d); err != nil {
		return event, fmt.Errorf("reading a %q delivery: %w", event.Kind, err)
	}
	if d.Repository == "" {
		return event, fmt.Errorf("a %q delivery names no repository", event.Kind)
	}
	event.Repository, event.Number, event.Branch = d.Repository, d.Number, d.Branch
	return event, nil
}

// EventHooks are owner's hooks, oldest first.
func (g *Git) EventHooks(owner string) []ports.EventHook {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return slices.Clone(g.s.eventState().hooks[owner])
}

// Deliveries are what owner's hooks delivered, oldest first.
func (g *Git) Deliveries(owner string) []Delivery {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	out := slices.Clone(g.s.eventState().outbox[owner])
	for i := range out {
		out[i].Header = maps.Clone(out[i].Header)
		out[i].Body = slices.Clone(out[i].Body)
	}
	return out
}

// deliver signs and queues, to each hook of repo's owner, a delivery of kind
// that concerns repo and number. It names no branch: the fake delivers no
// push or run, and its pull requests and statuses name none, as a status on
// a real host may not. The caller holds the lock.
func (s *state) deliver(repo ports.Repository, kind string, number int) {
	if s.events == nil || len(s.events.hooks[repo.Owner]) == 0 {
		return
	}
	body, _ := json.Marshal(deliveryBody{Repository: repo.Name, Number: number})
	for _, h := range s.events.hooks[repo.Owner] {
		s.events.outbox[repo.Owner] = append(s.events.outbox[repo.Owner], Delivery{URL: h.URL, Body: body,
			Header: map[string]string{
				EventHeader:     kind,
				SignatureHeader: signaturePrefix + hex.EncodeToString(sign([]byte(h.Secret), body)),
			}})
	}
}

// sign is the HMAC-SHA256 of body under secret.
func sign(secret, body []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return mac.Sum(nil)
}
