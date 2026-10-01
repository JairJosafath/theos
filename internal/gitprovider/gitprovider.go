// Package gitprovider reaches Git hosts: it creates repositories, reads their
// history, fetches templates at pinned refs and pushes the initial commit.
//
// It is an adapter: it may use net/http and go-git, and the business rules, but
// must not import Kubernetes or the api package; see docs/architecture.md.
package gitprovider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// Kinds of Git hosting. Gitea is the only one in v1.
const KindGitea = "gitea"

// DefaultBranch is the branch every Container repository starts with and merges into.
const DefaultBranch = defaultBranch

// Credentials authenticate against a Git host.
type Credentials struct {
	Username string
	Token    string
}

// Config is how to reach one Git host and where repositories go.
type Config struct {
	Kind        string
	URL         string
	Owner       string
	Credentials Credentials
}

// Repository is a repository on a Git host.
type Repository struct {
	Owner    string
	Name     string
	CloneURL string
	WebURL   string
}

// Commit is one commit on the default branch.
type Commit struct {
	Hash    string
	Message string
}

// Signature is who a commit is by.
type Signature struct {
	Name  string
	Email string
	When  time.Time
}

// Provider creates and fills repositories on one Git host.
type Provider interface {
	// EnsureRepository creates owner/name if it doesn't exist, and returns it.
	EnsureRepository(ctx context.Context, name, description string) (Repository, error)
	// History returns up to limit commits on the default branch, newest first, or
	// on another branch when the default one is missing. An empty repository has
	// none.
	History(ctx context.Context, repo Repository, limit int) ([]Commit, error)
	// PushInitialCommit pushes files as the first commit on the default branch.
	// It fails, without force, if the branch already exists.
	PushInitialCommit(ctx context.Context, repo Repository, files scaffold.Tree, message string, author Signature) (string, error)
	// Repository returns the addresses of name in the owner organization, without
	// calling the host.
	Repository(name string) Repository
	// ProtectBranch makes branch accept changes only through pull requests whose
	// status checks matching statusContexts pass. It is safe to repeat.
	ProtectBranch(ctx context.Context, repo Repository, branch string, statusContexts []string) error
	// CombinedStatus returns the combined state of the checks on ref.
	CombinedStatus(ctx context.Context, repo Repository, ref string) (CheckStatus, error)
	// CheckLog returns the log of the CI job a check status links to.
	CheckLog(ctx context.Context, repo Repository, checkURL string) (string, error)
	// EnsureAgentAccess makes user a bot that can write to repo only, and returns
	// a fresh token for it.
	EnsureAgentAccess(ctx context.Context, repo Repository, user string) (Credentials, error)
	// OpenPullRequest opens a pull request from head into base, or returns the
	// open one for head.
	OpenPullRequest(ctx context.Context, repo Repository, head, base, title, body string) (PullRequest, error)
	// GetPullRequest reads a pull request.
	GetPullRequest(ctx context.Context, repo Repository, number int) (PullRequest, error)
	// CommentPullRequest adds a comment to a pull request.
	CommentPullRequest(ctx context.Context, repo Repository, number int, body string) error
	// MergePullRequest squashes a pull request into its base and deletes its branch.
	MergePullRequest(ctx context.Context, repo Repository, number int, title, message string) error
}

// Client builds Providers and fetches templates. It is what the operator uses;
// tests substitute their own.
type Client struct {
	// HTTP is used for API calls. Defaults to a client with a 30 second timeout.
	HTTP *http.Client
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
