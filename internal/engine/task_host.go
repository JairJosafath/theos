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

package engine

import (
	"context"
	"errors"
	"time"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// The host side of a Task (docs/plans/lead-runtime.md, decision 11; #334):
// what a Task's facts read of the Git host, its pull request, its checks
// and main's, and the host's half of its actions. Both runtimes call these:
// the operator's Task reconciler and the engine's TaskTick.

// Logger is where the engine says what it could not read and goes on
// without; a logr.Logger is one.
type Logger interface {
	Info(msg string, keysAndValues ...any)
}

// noLog says nothing.
type noLog struct{}

func (noLog) Info(string, ...any) {}

// logOr is log, or one that says nothing.
func logOr(log Logger) Logger {
	if log == nil {
		return noLog{}
	}
	return log
}

// ChecksInput is what reading a round's checks takes besides the host.
type ChecksInput struct {
	// Commit is the round's commit, whose checks are read.
	Commit string
	// Reruns is how often the round's failed CI jobs ran again; RerunAt when
	// that was last asked for, zero for never; RerunDue that the round
	// pushed again a commit whose checks failed.
	Reruns   int
	RerunAt  time.Time
	RerunDue bool
	// CheckingSince is when the Task began to wait for the checks.
	CheckingSince time.Time
	Now           time.Time
	// PodFate is the fate of the CI runner pool's pod named runner, empty
	// when no pool pod of that name is kept; nil keeps none.
	PodFate func(runner string) string
	Log     Logger
}

// CI is what reading a round's checks reaches: the commit's statuses on the
// Git host and its CI runs.
type CI interface {
	CombinedStatus(ctx context.Context, repo ports.Repository, ref string) (ports.CheckStatus, error)
	ports.CI
}

// ReadChecks reads the CI result on the round's commit into facts while the
// Task is Checking: its state and failed log, and the runs at the commit
// while it waits, or while a failure may still be the one from before a
// rerun. It returns the commit's status as read.
func ReadChecks(ctx context.Context, ci CI, repo ports.Repository, in ChecksInput,
	facts *model.TaskFacts) (ports.CheckStatus, error) {
	in.Log = logOr(in.Log)
	checks, err := ci.CombinedStatus(ctx, repo, in.Commit)
	if err != nil {
		return checks, err
	}
	facts.Checks, facts.ChecksURL = checks.State, checks.URL
	failed := checks.State == ports.CheckFailure || checks.State == ports.CheckError
	facts.ChecksReruns, facts.ChecksRerunDue = in.Reruns, in.RerunDue
	if !in.RerunAt.IsZero() {
		facts.ChecksRerunAt = in.RerunAt
		facts.SinceChecksRerun = in.Now.Sub(in.RerunAt)
		if failed {
			// Until the rerun ends, the failure is the killed run's.
			facts.CIRun, facts.ChecksRerunEnded = rerunState(ctx, ci, repo, in)
		}
	}
	read := failed && (facts.ChecksRerunAt.IsZero() || facts.ChecksRerunEnded)
	if read {
		// Best effort: without the log the agent still gets the link.
		log, err := ci.CheckLog(ctx, repo, checks.URL)
		facts.ChecksLog, facts.ChecksNoLog = log, errors.Is(err, ports.ErrNoCILog)
	}
	// A failure whose runs still run waits for them before its jobs run again.
	if checks.State != ports.CheckSuccess && facts.CIRun == (model.CIRun{}) {
		facts.CIRun = ciRun(ctx, ci, repo, in, read)
	}
	facts.CheckingSince = in.CheckingSince
	return checks, nil
}

// rerunState reads whether the runs at the commit whose failed jobs ran
// again have given their result, and the state of its runs while they
// haven't. Runs that can't be read are unknown, which waits.
func rerunState(ctx context.Context, ci ports.CI, repo ports.Repository, in ChecksInput) (model.CIRun, bool) {
	runs, err := ci.CommitRuns(ctx, repo, in.Commit)
	if err != nil {
		in.Log.Info("Could not read the CI runs of the Task's commit", "commit", in.Commit, "error", err.Error())
		return model.CIRun{Commit: in.Commit, State: model.CIRunUnknown}, false
	}
	seen := WorkflowRuns(runs)
	if model.RerunEnded(seen, in.RerunAt) {
		return model.CIRun{}, true
	}
	return runState(ctx, ci, repo, in, seen, false), false
}

// WorkflowRuns is the model's view of the Git host's runs.
func WorkflowRuns(runs []ports.WorkflowRun) []model.WorkflowRun {
	seen := make([]model.WorkflowRun, 0, len(runs))
	for _, run := range runs {
		seen = append(seen, model.WorkflowRun{ID: run.ID, Workflow: run.Workflow, Event: run.Event,
			Status: run.Status, Conclusion: run.Conclusion, Started: run.Started, URL: run.URL})
	}
	return seen
}

// ciRun reads the state of the CI runs at the commit, whose checks have no
// result yet, whether a runner is online while one is queued, and, when
// canceller, who cancelled a failed one. Runs that can't be read are
// unknown, which waits up to model.QueuedChecksLimit.
func ciRun(ctx context.Context, ci ports.CI, repo ports.Repository, in ChecksInput, canceller bool) model.CIRun {
	runs, err := ci.CommitRuns(ctx, repo, in.Commit)
	if err != nil {
		in.Log.Info("Could not read the CI runs of the Task's commit", "commit", in.Commit, "error", err.Error())
		return model.CIRun{Commit: in.Commit, State: model.CIRunUnknown}
	}
	return runState(ctx, ci, repo, in, WorkflowRuns(runs), canceller)
}

// runState sums up the runs at the commit, whether a runner is online while
// one is queued, and, when canceller, who cancelled a failed one that ended
// cancelled: asked only when the failure is read, as it costs a call a job.
func runState(ctx context.Context, ci ports.CI, repo ports.Repository, in ChecksInput,
	seen []model.WorkflowRun, canceller bool) model.CIRun {
	out := model.CIRun{Commit: in.Commit}
	state, run := model.CommitRunState(seen)
	out.State, out.URL, out.Started = state, run.URL, run.Started
	if cancelled := model.CancelledRuns(seen); canceller && len(cancelled) > 0 {
		out.CancelledBy = cancellerOf(ctx, ci, repo, in, cancelled)
	}
	switch state {
	case model.CIRunRunning:
		if !run.Started.IsZero() {
			out.Running = in.Now.Sub(run.Started)
		}
	case model.CIRunQueued:
		online, known := ci.RunnersOnline(ctx)
		out.NoRunnerOnline = known && online == 0
	}
	return out
}

// cancellerOf tells who cancelled the cancelled runs at the commit: by the
// fate of the pool pod a cancelled job ran on (#1140), else whether the
// job's runner stayed online (model.Canceller).
func cancellerOf(ctx context.Context, ci ports.CI, repo ports.Repository, in ChecksInput,
	cancelled []model.WorkflowRun) string {
	var jobs []model.CIJob
	online, known := map[int64]bool{}, map[int64]bool{}
	for _, run := range cancelled {
		found, err := ci.RunJobs(ctx, repo, run.ID)
		if err != nil {
			in.Log.Info("Could not read the jobs of a cancelled CI run", "run", run.URL, "error", err.Error())
			return model.Canceller(nil, false, in.Now)
		}
		for _, j := range found {
			job := model.CIJob{Conclusion: j.Conclusion, Runner: j.RunnerID, RunnerName: j.RunnerName,
				Ended: j.Completed}
			if job.Cancelled() && job.Runner != 0 {
				if in.PodFate != nil && job.RunnerName != "" {
					job.PodFate = in.PodFate(job.RunnerName)
				}
				if _, asked := known[job.Runner]; !asked && job.PodFate == "" {
					online[job.Runner], known[job.Runner] = ci.RunnerOnline(ctx, job.Runner)
				}
				job.RunnerOnline, job.RunnerUnknown = online[job.Runner], !known[job.Runner]
			}
			jobs = append(jobs, job)
		}
	}
	return model.Canceller(jobs, true, in.Now)
}

// Pulls is what reading and updating a Task's pull request reaches.
type Pulls interface {
	GetPullRequest(ctx context.Context, repo ports.Repository, number int) (ports.PullRequest, error)
	UpdatePullRequest(ctx context.Context, repo ports.Repository, number int) error
}

// ReadPull reads, for an approved change, whether its pull request misses
// what merged into main since its checks ran and whether the Git host says
// it merges: an approved change merges only on checks that ran against the
// main it merges into. facts holds the Task's phase, run and checks;
// mergeWhenChecksPass that its branch took in main and merges once its
// checks pass. pull 0 reads nothing.
func ReadPull(ctx context.Context, pulls Pulls, repo ports.Repository, pull int, mergeWhenChecksPass bool,
	facts *model.TaskFacts) error {
	if !PullWanted(pull, mergeWhenChecksPass, *facts) {
		return nil
	}
	pr, err := pulls.GetPullRequest(ctx, repo, pull)
	if err != nil {
		return err
	}
	open := pr.State == stateOpen && !pr.Merged
	facts.Unmergeable, facts.Behind = open && !pr.Mergeable, open && pr.Behind
	return nil
}

// PullWanted reports whether ReadPull reads the pull request: for a change
// approved, or one that merges on its checks once they passed.
func PullWanted(pull int, mergeWhenChecksPass bool, facts model.TaskFacts) bool {
	run := facts.Run
	judging := facts.Phase == model.TaskReviewing || facts.Phase == model.TaskVerifying
	approved := judging && run.Done && run.Result != nil && run.Result.Verdict == model.VerdictApprove
	// A change its Crew's approval rules ask no review of merges on its checks.
	updated := facts.Phase == model.TaskChecking && facts.Checks == ports.CheckSuccess &&
		(mergeWhenChecksPass || model.MergesUnreviewed(facts))
	return (approved || updated) && pull != 0
}

// stateOpen is an open pull request's State.
const stateOpen = "open"

// ReadBase reads the CI state of main's head for the checks main requires
// (contexts), before round 1: round 1 starts only on a green base.
func ReadBase(ctx context.Context, ci CI, repo ports.Repository, contexts []string, facts *model.TaskFacts) error {
	checks, err := ci.CombinedStatus(ctx, repo, ports.DefaultBranch)
	if err != nil {
		return err
	}
	facts.Base = model.BaseState(contexts, checks.State, checks.Contexts)
	facts.BaseURL = checks.URL
	return nil
}

// BranchUpdate is what merging main into an approved change's branch did.
type BranchUpdate struct {
	// Conflict means the branch conflicts with main: it goes back to its
	// implementer (model.ConflictStep).
	Conflict bool
	// Head is the branch's head after the merge; the round's commit when
	// nothing merged, as the branch had main already.
	Head string
}

// UpdateBranch merges main into pull's branch. The merge, not the Git
// host's mergeable flag, tells a conflict.
func UpdateBranch(ctx context.Context, pulls Pulls, repo ports.Repository, pull int) (BranchUpdate, error) {
	err := pulls.UpdatePullRequest(ctx, repo, pull)
	if errors.Is(err, ports.ErrConflict) {
		return BranchUpdate{Conflict: true}, nil
	}
	if err != nil {
		return BranchUpdate{}, err
	}
	pr, err := pulls.GetPullRequest(ctx, repo, pull)
	if err != nil {
		return BranchUpdate{}, err
	}
	return BranchUpdate{Head: pr.HeadSHA}, nil
}
