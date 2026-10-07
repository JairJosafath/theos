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
	"io"
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
	// ReadFiles returns branch's copy of those of paths it has, and the commit it
	// read them at. A path it doesn't have is left out, not an error. A path
	// ending in "/" reads every regular file below that directory, keyed by its
	// full path; a missing directory gives nothing.
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
	// CheckLog returns the log of the CI job a check status links to.
	CheckLog(ctx context.Context, repo Repository, checkURL string) (string, error)
	// EnsureAgentAccess makes user a bot that can write to repo only, and returns
	// a fresh token for it.
	EnsureAgentAccess(ctx context.Context, repo Repository, user string) (Credentials, error)
	// RemoveAgentAccess takes away what EnsureAgentAccess gave user on repo: on
	// Gitea it deletes the bot user, and its tokens with it. A user already gone
	// is no error.
	RemoveAgentAccess(ctx context.Context, repo Repository, user string) error
	// OpenPullRequest opens a pull request from head into base, or returns the
	// open one for head.
	OpenPullRequest(ctx context.Context, repo Repository, head, base, title, body string) (PullRequest, error)
	// GetPullRequest reads a pull request.
	GetPullRequest(ctx context.Context, repo Repository, number int) (PullRequest, error)
	// CommentPullRequest adds a comment to a pull request.
	CommentPullRequest(ctx context.Context, repo Repository, number int, body string) error
	// MergePullRequest squashes a pull request into its base and deletes its
	// branch, provided its head is still headSHA.
	MergePullRequest(ctx context.Context, repo Repository, number int, headSHA, title, message string) error
	// ForceMergePullRequest is MergePullRequest as the provider's admin user,
	// overriding the branch protection's merge restrictions.
	ForceMergePullRequest(ctx context.Context, repo Repository, number int, headSHA, title, message string) error
	// PullRequestFiles returns every path a pull request changes, a renamed
	// file's old path too, or an error when it can't read them all:
	// ErrTooManyFiles when there are too many.
	PullRequestFiles(ctx context.Context, repo Repository, number int) ([]string, error)
	// ClosePullRequest closes a pull request without merging it.
	ClosePullRequest(ctx context.Context, repo Repository, number int) error
	// DeleteBranch deletes branch; a branch already gone is no error.
	DeleteBranch(ctx context.Context, repo Repository, branch string) error
	// ReviewPullRequest posts a review of commit: ReviewApprove,
	// ReviewRequestChanges or ReviewComment, with body.
	ReviewPullRequest(ctx context.Context, repo Repository, number int, commit, event, body string) error

	// EnsureLabels creates the labels repo doesn't have yet. It is safe to repeat.
	EnsureLabels(ctx context.Context, repo Repository, labels []Label) error
	// EnsureMilestone returns the ID of repo's milestone titled title, open or
	// closed, and creates it, open and with description, if there is none. It
	// leaves an existing milestone as it is, and is safe to repeat.
	EnsureMilestone(ctx context.Context, repo Repository, title, description string) (int64, error)
	// ListIssues returns the open issues of repo labelled label, or all of them
	// when label is "", without pull requests.
	ListIssues(ctx context.Context, repo Repository, label string) ([]Issue, error)
	// GetIssue reads one issue; ErrIssueNotFound when there is none.
	GetIssue(ctx context.Context, repo Repository, number int) (Issue, error)
	// OpenBlockers returns the open issues an issue depends on: "#3" in the same
	// repository, "owner/name#3" in another.
	OpenBlockers(ctx context.Context, repo Repository, number int) ([]string, error)
	// AddIssueDependency makes issue number of repo depend on issue blocker of
	// blockerRepo, which may be repo itself or another repository. It is safe
	// to repeat.
	AddIssueDependency(ctx context.Context, repo Repository, number int, blockerRepo Repository, blocker int) error
	// AddIssueLabel adds a label to an issue by name. An exclusive scoped label
	// replaces the issue's other labels of its scope.
	AddIssueLabel(ctx context.Context, repo Repository, number int, label string) error
	// RemoveIssueLabel removes a label from an issue, if it has it.
	RemoveIssueLabel(ctx context.Context, repo Repository, number int, label string) error
	// CommentIssue adds a comment to an issue or a pull request.
	CommentIssue(ctx context.Context, repo Repository, number int, body string) error
	// CloseIssue closes an issue.
	CloseIssue(ctx context.Context, repo Repository, number int) error
	// SetIssueMilestone moves an issue into the milestone with ID milestone, or
	// out of every milestone when it is 0.
	SetIssueMilestone(ctx context.Context, repo Repository, number int, milestone int64) error
	// AssignIssue makes users the issue's assignees.
	AssignIssue(ctx context.Context, repo Repository, number int, users []string) error
	// CreateIssue files an issue with labels, which must exist in the repository,
	// in the milestone with ID milestone, or in none when it is 0.
	CreateIssue(ctx context.Context, repo Repository, title, body string, labels []string, milestone int64) (Issue, error)

	// ListMilestones returns repo's open milestones.
	ListMilestones(ctx context.Context, repo Repository) ([]Milestone, error)
	// ListAllMilestones returns repo's milestones, open and closed, with their
	// descriptions.
	ListAllMilestones(ctx context.Context, repo Repository) ([]Milestone, error)
	// CreateMilestone creates an open milestone in repo with description, due at
	// due, or with no due date when due is zero, and returns it. It doesn't look
	// for one of the same title first: a repeat makes a second milestone.
	CreateMilestone(ctx context.Context, repo Repository, title, description string, due time.Time) (Milestone, error)
	// MilestoneIssues returns the open and the closed issues in milestone id,
	// without pull requests.
	MilestoneIssues(ctx context.Context, repo Repository, id int64) ([]Issue, error)
	// CloseMilestone closes milestone id, keeping the day it is due. It is safe
	// to repeat.
	CloseMilestone(ctx context.Context, repo Repository, id int64) error
	// GetTag returns the commit tag points at, or "" when repo has no such tag.
	GetTag(ctx context.Context, repo Repository, tag string) (string, error)
	// CreateTag tags commit as tag, annotated with message. It fails if the tag
	// exists, wherever it points.
	CreateTag(ctx context.Context, repo Repository, tag, commit, message string) error
	// TagRuns returns the CI workflow runs a push of tag at commit started,
	// newest first.
	TagRuns(ctx context.Context, repo Repository, tag, commit string) ([]WorkflowRun, error)
	// GetRelease returns the release of tag with its assets, a draft too, or nil
	// when repo has none.
	GetRelease(ctx context.Context, repo Repository, tag string) (*Release, error)
	// OpenReleaseAsset starts the download of asset and returns its body, which
	// the caller closes, and its size in bytes. The body is streamed, never held
	// whole in memory, and fails as it is read if it is not that size. ctx
	// bounds the whole download.
	OpenReleaseAsset(ctx context.Context, repo Repository, asset ReleaseAsset) (io.ReadCloser, int64, error)
	// SetReleaseBody replaces the notes of release id with body; a body of ""
	// leaves them as they are.
	SetReleaseBody(ctx context.Context, repo Repository, id int64, body string) error
	// PublishGoModule publishes the Go module at the root of repo, as of tag, to
	// the owner's Go package registry, at tag's version. No go.mod, a version
	// published before and a module that can't be published at tag are
	// outcomes, not errors; a repeat publishes nothing.
	PublishGoModule(ctx context.Context, repo Repository, tag string) (GoModule, error)
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

// ErrTooManyFiles means a pull request changes more files than the provider
// can list in full; no retry reads them all.
var ErrTooManyFiles = errors.New("too many changed files to list in full")

// ErrEmptyChange means a commit would leave its base as it is: every file it
// writes is already there as it is, and no file it removes is. Nothing is pushed.
var ErrEmptyChange = errors.New("the change leaves its base as it is")

// ErrIssueNotFound is GetIssue's error for an issue the repository doesn't have.
var ErrIssueNotFound = errors.New("no such issue")

// SameHost reports whether two URLs point at the same host, so a Git provider's
// credentials can be reused for a template on it.
func SameHost(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	return errA == nil && errB == nil && ua.Host != "" && ua.Scheme == ub.Scheme && ua.Host == ub.Host
}
