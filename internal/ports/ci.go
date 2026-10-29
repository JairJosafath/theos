package ports

import (
	"context"
	"errors"
	"time"
)

// WorkflowRun is a CI workflow run.
type WorkflowRun struct {
	ID int64
	// Workflow is the workflow file it runs, such as release.yaml.
	Workflow string
	// Event is what started it: push, pull_request and the like.
	Event string
	// Ref is the ref it ran at, such as refs/heads/main or refs/pull/3/head.
	Ref string
	// Status is queued, waiting, in_progress or completed.
	Status string
	// Conclusion is how a completed run ended: success, failure, cancelled or
	// skipped.
	Conclusion string
	// Started is when a runner started it, zero while it waits for one.
	Started time.Time
	// URL is the run's page.
	URL string
}

// WorkflowJob is a job of a CI workflow run.
type WorkflowJob struct {
	ID int64
	// Status is queued, waiting, in_progress or completed, and Conclusion how a
	// completed job ended.
	Status     string
	Conclusion string
	// RunnerID is the runner that ran it, zero when none started it, and
	// RunnerName that runner's name (a pool pod's, when the factory's pool
	// registered it).
	RunnerID   int64
	RunnerName string
	// Completed is when it ended, zero while it runs.
	Completed time.Time
}

// ErrNoCILog is CheckLog's error for a CI job the CI keeps no log of: one
// whose runner went away before it sent any.
var ErrNoCILog = errors.New("the CI job has no log")

// CI is the continuous integration port (docs/architecture.md, "Ports"): the
// runs of a commit or tag, their jobs, a job's log, failed jobs run again,
// and the runners online. The per-CI table (its name, check contexts and
// protected paths) is model.CI, so the business rules read it without an
// adapter.
type CI interface {
	// CheckLog returns the log of the CI job a check status links to;
	// ErrNoCILog when the CI keeps none.
	CheckLog(ctx context.Context, repo Repository, checkURL string) (string, error)
	// TagRuns returns the CI workflow runs a push of tag at commit started,
	// newest first.
	TagRuns(ctx context.Context, repo Repository, tag, commit string) ([]WorkflowRun, error)
	// CommitRuns returns the CI workflow runs at commit, of any event, newest
	// first.
	CommitRuns(ctx context.Context, repo Repository, commit string) ([]WorkflowRun, error)
	// RunnersOnline returns how many CI runners the instance has online, idle
	// or busy; known is false when the CI doesn't say.
	RunnersOnline(ctx context.Context) (online int, known bool)
	// RunnerOnline reports whether CI runner id has called the host lately:
	// false for one that is offline, still busy with the job it stopped in or
	// not, or no longer registered. known is false when the CI doesn't say.
	RunnerOnline(ctx context.Context, id int64) (online, known bool)
	// RunJobs returns the jobs of workflow run id, of its newest attempt.
	RunJobs(ctx context.Context, repo Repository, id int64) ([]WorkflowJob, error)
	// RerunFailedJobs runs the failed jobs of workflow run id again, as a new
	// attempt of the same run.
	RerunFailedJobs(ctx context.Context, repo Repository, id int64) error
}

// OwnerRunners is an optional capability of the CI port: the CI runners
// registered to the provider's owner organization, which the operator keeps
// as a pool of single-job runners per project (docs/plans/ci-runner-isolation.md).
// A CI that has it implements it too; find it with a type assertion.
type OwnerRunners interface {
	// RunnerRegistrationToken returns the token that registers a runner to the
	// owner organization. It is the same token on every call until someone
	// resets it, so asking again doesn't stop runners registering with it.
	RunnerRegistrationToken(ctx context.Context) (string, error)
	// Runners returns the runners registered to the owner organization, the
	// instance's and other organizations' left out.
	Runners(ctx context.Context) ([]Runner, error)
	// DeleteRunner removes the owner organization's runner id. It is safe to
	// repeat: a runner that is gone already is no error.
	DeleteRunner(ctx context.Context, id int64) error
}

// Runner is a CI runner registered to an organization.
type Runner struct {
	ID   int64
	Name string
	// Labels are the labels it takes jobs for.
	Labels []string
	// Ephemeral is whether it takes one job and is removed when that job ends.
	Ephemeral bool
	// Online is whether it has called the host lately; a registration whose
	// runner stopped, or never started, isn't.
	Online bool
	// Busy is whether the host has given it a job that hasn't ended, which a
	// runner that stopped mid-job still has.
	Busy bool
}

// OwnerActions is an optional capability of the CI port: the Actions
// variables and secrets of the provider's owner organization, which every
// workflow of the owner's repositories reads. The operator sets the package
// registries there when a GitProvider's spec.packages departs from the
// host's own (docs/plans/done/app-packages.md, decision 5). A CI that has
// it implements it too; find it with a type assertion.
type OwnerActions interface {
	// SetActionsVariable creates or updates the owner organization's
	// variable name.
	SetActionsVariable(ctx context.Context, name, value string) error
	// DeleteActionsVariable removes the owner organization's variable name.
	// It is safe to repeat: one that is gone already is no error.
	DeleteActionsVariable(ctx context.Context, name string) error
	// SetActionsSecret creates or updates the owner organization's secret
	// name.
	SetActionsSecret(ctx context.Context, name, value string) error
	// DeleteActionsSecret removes the owner organization's secret name. It
	// is safe to repeat: one that is gone already is no error.
	DeleteActionsSecret(ctx context.Context, name string) error
}
