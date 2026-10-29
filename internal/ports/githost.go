package ports

import (
	"context"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// DefaultBranch is the branch every Container repository starts with and
// merges into.
const DefaultBranch = "main"

// Credentials authenticate against a Git host.
type Credentials struct {
	Username string
	Token    string
}

// Kinds of Git hosting. Gitea is the only one in v1.
const KindGitea = "gitea"

// Config is how to reach one Git host and where repositories go.
type Config struct {
	Kind        string
	URL         string
	Owner       string
	Credentials Credentials
	// Packages is where the owner's npm and PyPI packages are published and
	// installed from, the GitProvider's spec.packages. A field left "" takes
	// its default: the scope @c4factory, and the host's own registries.
	Packages Packages
}

// DefaultNpmScope is the npm scope the factory's packages share when the
// GitProvider names none.
const DefaultNpmScope = "@c4factory"

// Packages names the registries of the owner's npm and PyPI packages. Any
// registry that takes npm publish's PUT document, and any index that takes
// twine's upload form, works.
type Packages struct {
	// NpmScope is the scope the owner's npm packages are named under, such as
	// "@acme"; a tarball under another scope, or none, is never published.
	NpmScope string
	// NpmRegistry is the npm registry's URL.
	NpmRegistry string
	// NpmToken publishes to NpmRegistry, as a bearer token; "" publishes with
	// the host's own credentials, which fits the host's own registry.
	NpmToken string
	// PyPIIndex is the simple index packages are installed from.
	PyPIIndex string
	// PyPIUpload is the URL twine's upload form goes to.
	PyPIUpload string
	// PyPIToken publishes to PyPIUpload, as the password of the user
	// __token__; "" publishes with the host's own credentials.
	PyPIToken string
}

// Repository is a repository on a Git host.
type Repository struct {
	Owner    string
	Name     string
	CloneURL string
	WebURL   string
	// GoProxy is the Go module proxy of the owner's package registry, where the
	// owner's released Go modules are fetched from; empty when the host has none.
	GoProxy string
	// ImageRegistry is the owner's container registry, <host>/<owner>, where
	// the images of the owner's releases are pushed; empty when the host has
	// none.
	ImageRegistry string
	// NpmScope is the scope the owner's npm packages are named under, and
	// NpmRegistry the registry they are published to and installed from, with
	// a trailing slash: the GitProvider's settings, else "@c4factory" and the
	// host's own registry.
	NpmScope    string
	NpmRegistry string
	// PyPIIndex is the simple index the owner's Python packages are installed
	// from, and PyPIUpload where they are published: the GitProvider's
	// settings, else the host's own.
	PyPIIndex  string
	PyPIUpload string
}

// ImageRepository is the repository of the images of r's releases in the
// owner's container registry, <ImageRegistry>/<repository>, the repository
// name lower-case as an OCI reference wants; "" when the host has no
// registry.
func (r Repository) ImageRepository() string {
	if r.ImageRegistry == "" {
		return ""
	}
	return r.ImageRegistry + "/" + strings.ToLower(r.Name)
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

// BranchRule is what a protected branch requires of a pull request.
type BranchRule struct {
	// StatusContexts are glob patterns of the status checks that must pass.
	StatusContexts []string
	// ProtectedPaths may not change: a file path, or a directory ending in "/".
	ProtectedPaths []string
	// OperatorPushes lets the provider's own user push to the branch directly,
	// and nobody else.
	OperatorPushes bool
}

// Check states, as a host's combined commit status reports them. An empty
// state means no check has reported on the commit yet.
const (
	CheckPending = "pending"
	CheckSuccess = "success"
	CheckFailure = "failure"
	CheckError   = "error"
)

// CheckStatus is the combined state of the checks on one commit.
type CheckStatus struct {
	Commit string
	State  string
	URL    string
	// Contexts is the state of each check by its context, such as
	// "check / check (pull_request)".
	Contexts map[string]string
}

// PullRequest is a pull request on a repository.
type PullRequest struct {
	Number  int
	URL     string
	Head    string
	HeadSHA string
	State   string
	Merged  bool
	// Mergeable is false while the pull request conflicts with its base.
	Mergeable bool
	// Behind means the base moved since the branch last took it in: checks on
	// the head ran without what merged into the base since.
	Behind bool
	// MergeCommit is the commit the merge made on the base; empty until merged.
	MergeCommit string
}

// Review events of ReviewPullRequest, with the wire values of the first host
// (APPROVED, not APPROVE); an adapter whose host names them otherwise
// translates.
const (
	ReviewApprove        = "APPROVED"
	ReviewRequestChanges = "REQUEST_CHANGES"
	ReviewComment        = "COMMENT"
)

// SourceMode is how git stores a file, as its tree entry's mode.
type SourceMode string

const (
	// SourcePlain is a plain file.
	SourcePlain SourceMode = "100644"
	// SourceExecutable is a file with its execute bit set.
	SourceExecutable SourceMode = "100755"
	// SourceSymlink is a symbolic link; its content is the path it points to.
	SourceSymlink SourceMode = "120000"
)

// SourceFile is a file of a repository at a tag, as ReadSource hands it on.
type SourceFile struct {
	// Path is the file's slash-separated path in the repository.
	Path string
	Mode SourceMode
	// Size is the content's length in bytes.
	Size int64
}

// Release is a release on a repository, with the files attached to it.
type Release struct {
	ID int64
	// Tag is the tag it releases.
	Tag  string
	Name string
	// Body is its notes, "" when nobody wrote any.
	Body  string
	Draft bool
	// URL is the release's page.
	URL    string
	Assets []ReleaseAsset
}

// ReleaseAsset is a file attached to a release.
type ReleaseAsset struct {
	ID   int64
	Name string
	Size int64
	// UUID names the file's download, independent of the release's tag.
	UUID string
}

// ErrBranchNotFound means ReadFiles asked for a branch the repository doesn't
// have. A repository with commits but no default branch is one: no retry
// makes the branch appear.
var ErrBranchNotFound = errors.New("branch not found")

// ErrTooManyFiles means a pull request changes more files than the host can
// list in full; no retry reads them all.
var ErrTooManyFiles = errors.New("too many changed files to list in full")

// ErrEmptyChange means a commit would leave its base as it is: every file it
// writes is already there as it is, and no file it removes is. Nothing is pushed.
var ErrEmptyChange = errors.New("the change leaves its base as it is")

// ErrConflict is UpdatePullRequest's error for a branch that conflicts with its base.
var ErrConflict = errors.New("the branch conflicts with its base")

// ErrPullRequestNotFound is GetPullRequest's error for a pull request the
// repository doesn't have, say one deleted or of a repository made again.
var ErrPullRequestNotFound = errors.New("no such pull request")

// ErrUnpinnedRef means a template ref is a branch or doesn't exist. Templates
// must be pinned to a tag or a full commit SHA. Fetching templates is plain
// Git and no port (docs/plans/git-host-ports.md, decision 6); the error is
// here so the fetcher and the controllers' fake share it.
var ErrUnpinnedRef = errors.New("template ref is not a tag or commit")

// GitHost is the Git host port (docs/architecture.md, "Ports"): repositories,
// files, pushes, branches, tags, branch rules, pull requests, merges, commit
// statuses, a release beside its tag, and the source at a tag. One GitHost
// serves one owner on one host.
type GitHost interface {
	// EnsureRepository creates owner/name if it doesn't exist, and returns it.
	EnsureRepository(ctx context.Context, name, description string) (Repository, error)
	// History returns up to limit commits on the default branch, newest first, or
	// on another branch when the default one is missing. An empty repository has
	// none.
	History(ctx context.Context, repo Repository, limit int) ([]Commit, error)
	// PushInitialCommit pushes files as the first commit on the default branch.
	// It fails, without force, if the branch already exists.
	PushInitialCommit(ctx context.Context, repo Repository, files scaffold.Tree, message string, author Signature) (string, error)
	// ReadFiles returns branch's copy of those of paths it has, and the commit it
	// read them at. A path it doesn't have is left out, not an error. A path
	// ending in "/" reads every regular file below that directory, keyed by its
	// full path; a missing directory gives nothing. A branch the repository
	// doesn't have is ErrBranchNotFound.
	ReadFiles(ctx context.Context, repo Repository, branch string, paths []string) (scaffold.Tree, string, error)
	// PushBranch commits files, and the removal of removes, on top of commit base
	// of the default branch, and force-pushes the commit as branch, creating or
	// replacing it. author is the commit's author and committer. It returns the
	// commit's SHA.
	PushBranch(ctx context.Context, repo Repository, base, branch string, files scaffold.Tree, removes []string,
		message string, author Signature) (string, error)
	// PushDefaultBranch commits files, and the removal of removes, on top of
	// commit base, the default branch's head, and pushes the commit to the
	// default branch without force: it fails when the branch moved on past base.
	// It returns the commit's SHA, or ErrEmptyChange when the commit would
	// change nothing.
	PushDefaultBranch(ctx context.Context, repo Repository, base string, files scaffold.Tree, removes []string,
		message string, author Signature) (string, error)
	// Repository returns the addresses of name in the owner organization, without
	// calling the host.
	Repository(name string) Repository
	// ProtectBranch makes branch accept changes only through pull requests that
	// pass rule, merged by the provider's own user, and, when rule.OperatorPushes,
	// that user's own pushes. It is safe to repeat.
	ProtectBranch(ctx context.Context, repo Repository, branch string, rule BranchRule) error
	// ProtectTags lets only the provider's own user push tags matching pattern.
	// It is safe to repeat.
	ProtectTags(ctx context.Context, repo Repository, pattern string) error
	// CombinedStatus returns the combined state of the checks on ref, and the
	// latest state of each check by its context.
	CombinedStatus(ctx context.Context, repo Repository, ref string) (CheckStatus, error)
	// OpenPullRequest opens a pull request from head into base, or returns the
	// open one for head.
	OpenPullRequest(ctx context.Context, repo Repository, head, base, title, body string) (PullRequest, error)
	// GetPullRequest reads a pull request; ErrPullRequestNotFound when there is none.
	GetPullRequest(ctx context.Context, repo Repository, number int) (PullRequest, error)
	// CommentPullRequest adds a comment to a pull request.
	CommentPullRequest(ctx context.Context, repo Repository, number int, body string) error
	// MergePullRequest squashes a pull request into its base and deletes its
	// branch, provided its head is still headSHA.
	MergePullRequest(ctx context.Context, repo Repository, number int, headSHA, title, message string) error
	// ForceMergePullRequest is MergePullRequest as the provider's admin user,
	// overriding the branch protection's merge restrictions: the design's
	// "Merge takes whether to bypass", as a second method.
	ForceMergePullRequest(ctx context.Context, repo Repository, number int, headSHA, title, message string) error
	// UpdatePullRequest merges the pull request's base into its branch, so its
	// checks run on what it would merge into. ErrConflict when they conflict.
	UpdatePullRequest(ctx context.Context, repo Repository, number int) error
	// PullRequestFiles returns every path a pull request changes, a renamed
	// file's old path too, or an error when it can't read them all:
	// ErrTooManyFiles when there are too many.
	PullRequestFiles(ctx context.Context, repo Repository, number int) ([]string, error)
	// ClosePullRequest closes a pull request without merging it.
	ClosePullRequest(ctx context.Context, repo Repository, number int) error
	// ReviewPullRequest posts a review of commit: ReviewApprove,
	// ReviewRequestChanges or ReviewComment, with body.
	ReviewPullRequest(ctx context.Context, repo Repository, number int, commit, event, body string) error
	// DeleteBranch deletes branch; a branch already gone is no error.
	DeleteBranch(ctx context.Context, repo Repository, branch string) error
	// HasBranch reports whether repo has branch.
	HasBranch(ctx context.Context, repo Repository, branch string) (bool, error)
	// BranchCommit returns the commit branch points at, or "" when repo has no
	// such branch.
	BranchCommit(ctx context.Context, repo Repository, branch string) (string, error)
	// CreateBranch creates branch at commit. It fails if the branch exists,
	// wherever it points.
	CreateBranch(ctx context.Context, repo Repository, branch, commit string) error
	// GetTag returns the commit tag points at, or "" when repo has no such tag.
	GetTag(ctx context.Context, repo Repository, tag string) (string, error)
	// CreateTag tags commit as tag, annotated with message. It fails if the tag
	// exists, wherever it points.
	CreateTag(ctx context.Context, repo Repository, tag, commit, message string) error
	// GetRelease returns the release of tag with its assets, a draft too, or nil
	// when repo has none. A release lives on the host beside its tag; the CI
	// only fills it.
	GetRelease(ctx context.Context, repo Repository, tag string) (*Release, error)
	// OpenReleaseAsset starts the download of asset and returns its body, which
	// the caller closes, and its size in bytes. The body is streamed, never held
	// whole in memory, and fails as it is read if it is not that size. ctx
	// bounds the whole download.
	OpenReleaseAsset(ctx context.Context, repo Repository, asset ReleaseAsset) (io.ReadCloser, int64, error)
	// SetReleaseBody replaces the notes of release id with body; a body of ""
	// leaves them as they are.
	SetReleaseBody(ctx context.Context, repo Repository, id int64, body string) error
	// ReadSource calls fn with each file of repo as of tag, as git archive makes
	// it, its path, mode and size, and its content, which is streamed, never
	// held whole in memory, and read only during the call. A submodule fails the
	// read. An error from fn stops the read and is returned.
	ReadSource(ctx context.Context, repo Repository, tag string, fn func(SourceFile, io.Reader) error) error
}

// Provider is every Git host port in one, the union of GitHost, Issues, CI,
// AgentAccess and Registry, for GitClient and cmd/main.go, and the adapter
// and the fake that implement them all; a helper takes the narrowest port it
// calls.
type Provider interface {
	GitHost
	Issues
	CI
	AgentAccess
	Registry
}

// SameHost reports whether two URLs point at the same host, so a Git provider's
// credentials can be reused for a template on it.
func SameHost(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	return errA == nil && errB == nil && ua.Host != "" && ua.Scheme == ub.Scheme && ua.Host == ub.Host
}
