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
