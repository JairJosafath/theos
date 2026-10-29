// Package gitprovider reaches Git hosts: it creates repositories, reads their
// history, fetches templates at pinned refs and pushes the initial commit.
//
// It is an adapter: it may use net/http and go-git, and the business rules, but
// must not import Kubernetes or the api package; see docs/architecture.md.
package gitprovider

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/JairJosafath/c4factory/internal/ports"
)

// Kinds of Git hosting. Gitea is the only one in v1.
const KindGitea = "gitea"

// Provider is every Git host port in one, the union of GitHost, Issues, CI,
// AgentAccess and Registry (internal/ports), for the operator and the fake
// that implement them all; a helper takes the narrowest port it calls.
type Provider interface {
	ports.GitHost
	ports.Issues
	ports.CI
	ports.AgentAccess
	ports.Registry
}

// Client builds Providers and fetches templates. It is what the operator uses;
// tests substitute their own.
type Client struct {
	// HTTP is used for API calls. Defaults to a client with a 30 second timeout.
	HTTP *http.Client
	// Now is the clock a GitHub App's token is minted and kept by; defaults to
	// time.Now.
	Now func() time.Time
}

// Provider returns the Provider for a GitProvider's settings.
func (c Client) Provider(cfg Config) (Provider, error) {
	switch cfg.Kind {
	case KindGitea, "":
		return newGitea(cfg, c.httpClient())
	default:
		return nil, fmt.Errorf("git provider %q is not supported; v1 supports %q", cfg.Kind, KindGitea)
	}
}

func (c Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// ErrUnpinnedRef means a template ref is a branch or doesn't exist. Templates
// must be pinned to a tag or a full commit SHA.
var ErrUnpinnedRef = errors.New("template ref is not a tag or commit")

// SameHost reports whether two URLs point at the same host, so a Git provider's
// credentials can be reused for a template on it.
func SameHost(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	return errA == nil && errB == nil && ua.Host != "" && ua.Scheme == ub.Scheme && ua.Host == ub.Host
}
