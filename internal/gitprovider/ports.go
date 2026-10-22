package gitprovider

import "github.com/JairJosafath/c4factory/internal/ports"

// The Git host's shared types, constants and errors are the ports' (internal/
// ports, one file per port). This file aliases them while the controllers
// still import this package; the aliases go with the adapter move (#1169).

// DefaultBranch is the branch every Container repository starts with and merges into.
const DefaultBranch = ports.DefaultBranch

// DefaultNpmScope is the npm scope the factory's packages share when the
// GitProvider names none.
const DefaultNpmScope = ports.DefaultNpmScope

// The GitHost port's types.
type (
	Credentials  = ports.Credentials
	Config       = ports.Config
	Packages     = ports.Packages
	Repository   = ports.Repository
	Commit       = ports.Commit
	Signature    = ports.Signature
	BranchRule   = ports.BranchRule
	CheckStatus  = ports.CheckStatus
	PullRequest  = ports.PullRequest
	SourceMode   = ports.SourceMode
	SourceFile   = ports.SourceFile
	Release      = ports.Release
	ReleaseAsset = ports.ReleaseAsset
)

// Check states, as the host's combined commit status reports them.
const (
	CheckPending = ports.CheckPending
	CheckSuccess = ports.CheckSuccess
	CheckFailure = ports.CheckFailure
	CheckError   = ports.CheckError
)

// Review events of ReviewPullRequest.
const (
	ReviewApprove        = ports.ReviewApprove
	ReviewRequestChanges = ports.ReviewRequestChanges
	ReviewComment        = ports.ReviewComment
)

// Source modes, as git stores files.
const (
	SourcePlain      = ports.SourcePlain
	SourceExecutable = ports.SourceExecutable
	SourceSymlink    = ports.SourceSymlink
)

// The Issues port's types.
type (
	Issue     = ports.Issue
	Label     = ports.Label
	Milestone = ports.Milestone
)

// The CI port's types.
type (
	WorkflowRun = ports.WorkflowRun
	WorkflowJob = ports.WorkflowJob
)

// The Registry port's types.
type (
	GoModuleOutcome = ports.GoModuleOutcome
	GoModule        = ports.GoModule
	PackageOutcome  = ports.PackageOutcome
	Package         = ports.Package
	ReleasePackages = ports.ReleasePackages
	ImageOutcome    = ports.ImageOutcome
	Image           = ports.Image
)

// Outcomes of PublishGoModule, PublishReleasePackages and PublishImage, and
// the ecosystems of a release's packages.
const (
	GoModulePublished = ports.GoModulePublished
	GoModuleExists    = ports.GoModuleExists
	GoModuleNoGoMod   = ports.GoModuleNoGoMod
	GoModuleInvalid   = ports.GoModuleInvalid

	PackagePublished = ports.PackagePublished
	PackageExists    = ports.PackageExists
	PackageUnscoped  = ports.PackageUnscoped
	PackageInvalid   = ports.PackageInvalid

	EcosystemNpm  = ports.EcosystemNpm
	EcosystemPyPI = ports.EcosystemPyPI

	ImagePublished = ports.ImagePublished
	ImageNone      = ports.ImageNone
	ImageInvalid   = ports.ImageInvalid
	ImageTagTaken  = ports.ImageTagTaken
)

// The ports' errors.
var (
	ErrTooManyFiles        = ports.ErrTooManyFiles
	ErrEmptyChange         = ports.ErrEmptyChange
	ErrConflict            = ports.ErrConflict
	ErrPullRequestNotFound = ports.ErrPullRequestNotFound
	ErrIssueNotFound       = ports.ErrIssueNotFound
	ErrOpenDependencies    = ports.ErrOpenDependencies
	ErrNoCILog             = ports.ErrNoCILog
	ErrNoRelease           = ports.ErrNoRelease
)
