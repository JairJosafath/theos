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

// Package ports holds the factory's swappable parts: one file per port with
// its interface and shared types, in plain Go with no Kubernetes
// (docs/architecture.md, "Ports"). Fakes are in ports/fake, contract suites in
// ports/contract, and the adapters in internal/adapters.
package ports

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/JairJosafath/theos/internal/model"
)

// Deployer ships a built release to a place a DeployTarget names and reads
// back what runs there. One Deployer serves one member of DeployTarget.spec;
// cmd/main.go maps each member's name to its adapter, and the controllers see
// only this port. It never builds and never runs code from the repository: CI
// built what it ships.
type Deployer interface {
	// Check says whether target accepts its credential: nil, or an error that
	// is ErrCredentialRefused or ErrUnreachable.
	Check(ctx context.Context, target Target) error
	// Ship takes the next step of shipping s to target and returns, without
	// waiting for the target's tool to finish. Repeating it for the same app
	// and version is safe and starts nothing new: it goes on with the one
	// deploy. An adapter whose tool runs the deploy (CloudFormation, Argo CD)
	// starts it in one call; one that is the tool itself (GitHub releases)
	// takes one write per call, so the caller ships again on each poll while
	// State says PhaseDeploying.
	Ship(ctx context.Context, target Target, s Shipment) error
	// State reads back the deploy of app at target: version is the version
	// the entry wants, its pin, else the newest release. An adapter whose
	// target runs one version at a time (CloudFormation, Argo CD) reports
	// what runs, and CloudFormation reads version only to report Deployed a
	// stack a failed update rolled back to it; one whose versions stand side by side (GitHub
	// releases) reports version's, else the newest. The zero DeployState when
	// there is none.
	State(ctx context.Context, target Target, app, version string) (DeployState, error)
}

// Deployers are the Deployers by member name, as cmd/main.go builds them.
type Deployers map[string]Deployer

// For returns the Deployer of member.
func (d Deployers) For(member string) (Deployer, error) {
	if dep, ok := d[member]; ok && dep != nil {
		return dep, nil
	}
	return nil, fmt.Errorf("no deployer for deploy target member %q", member)
}

// Target is a DeployTarget as an adapter reads it.
type Target struct {
	// Name is the DeployTarget's namespace/name, for messages.
	Name string
	// Member is the member's name, such as github, and Settings its fields
	// (repository for github).
	Member   string
	Settings map[string]string
	// Tier is development or production.
	Tier string
	// Parameters are the target's deploy-time values, mapped by the adapter.
	Parameters map[string]string
	// Secret is the data of the target's Secret, read by the operator only.
	Secret map[string][]byte
}

// Shipment is one version of an app, as CI built it.
type Shipment struct {
	// App names the app uniquely at any target, namespace/name of its
	// Container; DisplayName is the name a person reads.
	App, DisplayName string
	// Type, Deployment and Framework are the Container's type, Deployment
	// and Framework, which with ScaffoldedFrom, its status.scaffoldedFrom
	// (name@ref per template), say whether a release builds an image.
	Type, Deployment, Framework string
	ScaffoldedFrom              []string
	// Version is the release's tag.
	Version string
	// Commit is the commit the version is tagged at where the target holds the
	// app's source, "" where it doesn't.
	Commit string
	// Repository is the clone URL of the app's repository where the target
	// holds the app's source, "" where it doesn't; a tool that pulls the
	// source itself (Argo CD) clones it at Version.
	Repository string
	// Notes are the release's notes.
	Notes string
	// Files are the release's files, streamed from the Git host.
	Files []File
	// Image is the app's image by digest, "" until CI builds one (#1069).
	Image string
	// CopyImage copies Image, with its digest, to repository at a registry
	// a target's nodes pull from, tagged tag; nil when there is no Image.
	// The owner's registry's credential stays with the caller.
	CopyImage func(ctx context.Context, to ImageRegistry, repository, tag string) error
	// Source is the release's source as the Git host holds it at Version,
	// read and hashed by the caller with the factory's own files left out;
	// nil where the target holds no source.
	Source *model.ReleaseSource
	// ReadSource streams Source's files again, each with its path and size,
	// for an adapter that sends the ones the target lacks; nil with no Source.
	ReadSource func(ctx context.Context, each func(path string, size int64, content io.Reader) error) error
}

// File is a release file, opened only when the adapter sends it.
type File struct {
	Name string
	Size int64
	// Open streams the file, with its size. The caller closes the reader.
	Open func(ctx context.Context) (io.ReadCloser, int64, error)
}

// Phases of a deploy.
const (
	PhaseDeploying = "Deploying"
	PhaseDeployed  = "Deployed"
	PhaseFailed    = "Failed"
)

// Health of what runs at a target, as its tool reports it.
const (
	HealthHealthy     = "Healthy"
	HealthProgressing = "Progressing"
	HealthDegraded    = "Degraded"
	HealthUnknown     = "Unknown"
)

// DeployState is the newest deploy of an app at a target.
type DeployState struct {
	// Version is that deploy's version, "" when the app was never shipped.
	Version string
	// Phase is PhaseDeploying, PhaseDeployed or PhaseFailed, "" with no deploy.
	Phase string
	// Health is one of Health*, as the target's tool reports it.
	Health string
	// URL is where the app, or its release, answers, when the tool knows it.
	URL string
	// Image is the app's image as the target holds it, host/repository@digest,
	// when the target copies it; "" otherwise.
	Image string
	// Reason and Message say why a deploy failed, or what it waits on.
	Reason, Message string
	// Source is the release's source as the target published it, nil until
	// its tag is there or where the target holds no source.
	Source *model.PublishedSource
}

// Errors a Deployer's calls fail with that the operator tells apart; test with
// errors.Is. An adapter wraps them with what the tool said.
var (
	// ErrCredentialRefused: the target refuses the credential, or it can't see
	// the place.
	ErrCredentialRefused = errors.New("the target refuses the credential")
	// ErrUnreachable: the call failed before the target's tool had it (the
	// network, a timeout, a 5xx); tried again on the next poll.
	ErrUnreachable = errors.New("the target can't be reached")
	// ErrArtifactMissing: the shipment lacks a file the member needs.
	ErrArtifactMissing = errors.New("the release lacks a file the target needs")
	// ErrPrerequisiteMissing: something a person sets up once at the target
	// is missing (the CDK bootstrap, a template parameter with no default
	// the target's parameters don't give); shipped again on the next poll.
	ErrPrerequisiteMissing = errors.New("the target lacks something a person sets up there")
)

// Error is an adapter's error that is one of the port's errors and keeps the
// tool's own error, whose text it has: errors.Is finds Kind, errors.As the
// cause.
type Error struct {
	Kind  error
	Cause error
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return e.Kind.Error()
	}
	return e.Cause.Error()
}

// Unwrap returns Kind and Cause.
func (e *Error) Unwrap() []error {
	if e.Cause == nil {
		return []error{e.Kind}
	}
	return []error{e.Kind, e.Cause}
}

// The GitHub Deployer's ports: a GitHub repository its releases and their source
// are published to (internal/adapters/github).

// Reasons a ReleaseTarget call fails, as the Crew's Published condition names them.
const (
	// ReasonUnauthorized: the token is refused (401), or not allowed (403 without
	// rate limiting).
	ReasonUnauthorized = "Unauthorized"
	// ReasonRepositoryNotFound: no such repository, or one the token can't see (404).
	ReasonRepositoryNotFound = "RepositoryNotFound"
	// ReasonEmptyRepository: the repository has no default branch to tag.
	ReasonEmptyRepository = "EmptyRepository"
	// ReasonTagTaken: a release of that tag already exists.
	ReasonTagTaken = "TagTaken"
	// ReasonGitHubError: anything else: 5xx, rate limits, the network.
	ReasonGitHubError = "GitHubError"
	// ReasonBranchProtected: GitHub refuses to move a protected branch.
	ReasonBranchProtected = "BranchProtected"
	// ReasonSourceTooLarge: a file is over GitHub's limit for a blob.
	ReasonSourceTooLarge = "SourceTooLarge"
)

// ReleaseError is a failed ReleaseTarget call, with the reason it failed.
type ReleaseError struct {
	Reason string
	// Status is the HTTP status, 0 when no response came.
	Status  int
	Message string
}

func (e *ReleaseError) Error() string {
	if e.Status == 0 {
		return e.Message
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
}

// ReleaseReason returns the reason err failed with: a ReleaseError's, else
// ReasonGitHubError.
func ReleaseReason(err error) string {
	if re, ok := errors.AsType[*ReleaseError](err); ok {
		return re.Reason
	}
	return ReasonGitHubError
}

// TargetRelease is a release on a ReleaseTarget.
type TargetRelease struct {
	ID      int64
	TagName string
	Name    string
	Body    string
	Draft   bool
	// Commit is what the tag is created at when a draft is published: a commit
	// SHA, a branch, or "" for the default branch.
	Commit string
	// URL is the release's page.
	URL string
}

// TargetAsset is a file attached to a release on a ReleaseTarget.
type TargetAsset struct {
	ID   int64
	Name string
	Size int64
	// Uploaded is false for an upload GitHub never finished; it is deleted and
	// sent again, whatever its size.
	Uploaded bool
}

// ReleaseTarget is a repository a Container's releases are published to, with
// their source: release calls, and the Git Data calls of a SourceTarget.
type ReleaseTarget interface {
	SourceTarget
	// FindRelease returns the release, drafts included, whose body contains
	// marker; failing that, the published release tagged tag; nil when there is
	// neither.
	FindRelease(ctx context.Context, tag, marker string) (*TargetRelease, error)
	// ListReleases returns every release, drafts included.
	ListReleases(ctx context.Context) ([]TargetRelease, error)
	// CreateDraft creates a draft release of tag, named name, with body. The tag
	// is created when the draft is published, at commit, or at the default
	// branch's head when commit is "".
	CreateDraft(ctx context.Context, tag, name, body, commit string) (TargetRelease, error)
	// RetargetDraft sets the commit draft id's tag is created at.
	RetargetDraft(ctx context.Context, id int64, commit string) (TargetRelease, error)
	// ListAssets returns the files attached to release id.
	ListAssets(ctx context.Context, id int64) ([]TargetAsset, error)
	// UploadAsset streams size bytes of content to release id as the file name.
	// GitHub may rename the file (a space becomes a dot); the asset it returns
	// has the name GitHub gave it.
	UploadAsset(ctx context.Context, id int64, name string, size int64, content io.Reader) (TargetAsset, error)
	// DeleteAsset removes a file from a release. It is safe to repeat.
	DeleteAsset(ctx context.Context, assetID int64) error
	// PublishRelease turns draft id into a published release, the repository's
	// Latest when latest is set, else leaving Latest where it is.
	PublishRelease(ctx context.Context, id int64, latest bool) (TargetRelease, error)
	// LatestRelease returns the repository's Latest release, nil when it has
	// none.
	LatestRelease(ctx context.Context) (*TargetRelease, error)
}

// GitHubConfig is where a ReleaseTarget on GitHub publishes, and how.
type GitHubConfig struct {
	// Repository is owner/name.
	Repository string
	Token      string
	// APIURL and UploadURL default to github.com's; a GitHub Enterprise
	// Server's are its own (model.GitHubHostsOf), and tests point them at a
	// fake.
	APIURL    string
	UploadURL string
}

// MaxBlobSize is GitHub's limit for one blob: a file over it can't be published.
const MaxBlobSize = 100 << 20

// ErrNotFastForward is a branch update GitHub refused because the branch moved
// on: another commit landed first. It is no failure; the next try builds on
// the new head.
var ErrNotFastForward = errors.New("github: the update is not a fast-forward")

// ErrRefExists is a ref created after someone else created it first.
var ErrRefExists = errors.New("github: the ref already exists")

// GitTreeEntry is one entry of a git tree on GitHub.
type GitTreeEntry struct {
	// Path is the entry's name; in a recursive listing, or when creating a
	// tree, its path from the tree's root.
	Path string
	// Mode is git's: 100644, 100755, 120000 (a symlink), 040000 (a tree) or
	// 160000 (a submodule).
	Mode string
	// Type is blob, tree or commit.
	Type string
	// SHA is the object's; "" when creating a tree removes the path.
	SHA string
}

// GitTree is a git tree on GitHub.
type GitTree struct {
	SHA     string
	Entries []GitTreeEntry
	// Truncated is set when a recursive listing was too big for GitHub to send
	// whole.
	Truncated bool
}

// GitCommit is a git commit on GitHub.
type GitCommit struct {
	SHA     string
	Tree    string
	Parents []string
	Message string
}

// TargetRepository is a GitHub repository as GitHub answers for it.
type TargetRepository struct {
	// ID is GitHub's id of the repository, which a rename keeps and a new
	// repository under the old name doesn't.
	ID int64
	// FullName is owner/name as GitHub has it now: another than asked for when
	// GitHub followed a rename.
	FullName string
	// DefaultBranch is the default branch's name, "" in an empty repository.
	DefaultBranch string
	// Private is set when GitHub reports the repository private: the public
	// Go proxy can't read it.
	Private bool
}

// SourceTarget is a repository a release's source is written to, through
// GitHub's Git Data API: it reads and creates git objects and refs, and only
// ever moves a branch by a fast-forward.
type SourceTarget interface {
	// Repository reads the repository, following GitHub's redirect from a
	// renamed one's old name.
	Repository(ctx context.Context) (TargetRepository, error)
	// DefaultBranch returns the repository's default branch's name.
	DefaultBranch(ctx context.Context) (string, error)
	// Ref returns the commit ref names (heads/main, tags/engine/v0.6.1), an
	// annotated tag peeled; "" when there is no such ref. A repository that
	// is gone reads as no ref: DefaultBranch tells the two apart.
	Ref(ctx context.Context, ref string) (string, error)
	// Commit reads commit sha.
	Commit(ctx context.Context, sha string) (GitCommit, error)
	// Tree reads tree sha, with every path below it when recursive.
	Tree(ctx context.Context, sha string, recursive bool) (GitTree, error)
	// CreateBlob streams size bytes of content into a blob and returns its
	// SHA. A size over MaxBlobSize fails with ReasonSourceTooLarge before any
	// request. The caller closes content.
	CreateBlob(ctx context.Context, size int64, content io.Reader) (string, error)
	// CreateTree creates a tree of entries on base (none when ""): base's
	// paths stay unless an entry replaces or, with no SHA, removes them.
	CreateTree(ctx context.Context, base string, entries []GitTreeEntry) (string, error)
	// CreateCommit creates a commit of tree on parents. It moves no branch.
	CreateCommit(ctx context.Context, message, tree string, parents []string) (string, error)
	// CreateRef creates ref (heads/..., tags/...) at sha; ErrRefExists when it
	// is there already.
	CreateRef(ctx context.Context, ref, sha string) error
	// UpdateRef moves ref to sha, never by force: ErrNotFastForward when sha
	// doesn't descend from where ref is, ReasonBranchProtected when GitHub's
	// protection or rules refuse it.
	UpdateRef(ctx context.Context, ref, sha string) error
}

// SourceFolder is what was read of a repository for a release's source: the
// facts the source rule decides on, and the branch and head's root tree a
// source commit builds on.
type SourceFolder struct {
	Facts    *model.GitHubSource
	Branch   string
	HeadRoot string
}

// ReadSourceFolder reads the default branch's head and path's tree at it,
// with the blobs in it, and the commit sourceTag names with path's tree there.
func ReadSourceFolder(ctx context.Context, target SourceTarget, path, sourceTag string) (SourceFolder, error) {
	branch, err := target.DefaultBranch(ctx)
	if err != nil {
		return SourceFolder{}, err
	}
	out := SourceFolder{Facts: &model.GitHubSource{}, Branch: branch}
	if out.Facts.Head, err = target.Ref(ctx, "heads/"+branch); err != nil {
		return SourceFolder{}, err
	}
	if out.Facts.Head != "" {
		head, err := target.Commit(ctx, out.Facts.Head)
		if err != nil {
			return SourceFolder{}, err
		}
		out.HeadRoot = head.Tree
		if out.Facts.HeadTree, err = folderTree(ctx, target, head.Tree, path); err != nil {
			return SourceFolder{}, err
		}
		if out.Facts.HeadTree != "" {
			// Only to skip uploads: a listing cut short uploads a few blobs again.
			tree, err := target.Tree(ctx, out.Facts.HeadTree, true)
			if err != nil {
				return SourceFolder{}, err
			}
			for _, e := range tree.Entries {
				if e.Type == "blob" {
					out.Facts.HeadBlobs = append(out.Facts.HeadBlobs, e.SHA)
				}
			}
		}
	}
	if out.Facts.TagCommit, err = target.Ref(ctx, "tags/"+sourceTag); err != nil || out.Facts.TagCommit == "" {
		return out, err
	}
	tagged, err := target.Commit(ctx, out.Facts.TagCommit)
	if err != nil {
		return SourceFolder{}, err
	}
	if out.Facts.TagTree, err = folderTree(ctx, target, tagged.Tree, path); err != nil {
		return SourceFolder{}, err
	}
	return out, nil
}

// folderTree is the tree of path under root, one folder at a time, or "" when
// there is no folder there.
func folderTree(ctx context.Context, target SourceTarget, root, path string) (string, error) {
	if path == "." {
		return root, nil
	}
	sha := root
	for name := range strings.SplitSeq(path, "/") {
		tree, err := target.Tree(ctx, sha, false)
		if err != nil {
			return "", err
		}
		sha = ""
		for _, e := range tree.Entries {
			if e.Path == name && e.Type == "tree" {
				sha = e.SHA
			}
		}
		if sha == "" {
			return "", nil
		}
	}
	return sha, nil
}
