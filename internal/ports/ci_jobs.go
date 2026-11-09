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

package ports

import (
	"context"
	"time"
)

// JobRunners is an optional capability of the CI port: the owner
// organization's jobs that wait for a runner, and a runner registered for
// one of them that a machine outside the cluster runs, such as a remote
// test environment's (docs/plans/done/test-environments.md, step 8). A CI that
// has it implements it too; find it with a type assertion.
type JobRunners interface {
	// WaitingJobs returns the owner organization's jobs that wait for a
	// runner and that a runner with labels could take: every label a job
	// runs on is one of labels. Oldest first. A job that waits for another
	// job (needs) isn't listed until that one ends.
	WaitingJobs(ctx context.Context, labels []string) ([]WaitingJob, error)
	// RegisterJobRunner registers an ephemeral runner to the owner
	// organization with the organization's registration token, which never
	// leaves the operator, and returns the runner's own registration: the
	// runner takes one job and the CI removes it, and its credential with
	// it, when that job ends. DeleteRunner removes it before.
	RegisterJobRunner(ctx context.Context, reg RunnerRegistration) (JobRunner, error)
}

// WaitingJob is a CI job that waits for a runner.
type WaitingJob struct {
	ID int64
	// RunID is the workflow run it is a job of, and Name its name there.
	RunID int64
	Name  string
	// Labels are the labels it runs on, its runs-on.
	Labels []string
	// Created is when it was queued.
	Created time.Time
	// URL is its page.
	URL string
}

// RunnerRegistration asks for a runner registered for one job.
type RunnerRegistration struct {
	// Name is the runner's name, such as the lease it runs in.
	Name string
	// Labels are the labels it takes jobs for, as the runner takes them,
	// with their schema: env-gpu:host runs a job on the machine itself.
	Labels []string
	// Address is where the runner reaches the CI host, which may not be
	// where the operator does: from outside the cluster.
	Address string
	// Timeout is how long its job may run.
	Timeout time.Duration
}

// JobRunner is a runner registered for one job, as a machine runs it: the
// CI's runner binary, run in model.RemoteRunnerDir with Args, reading Files
// there.
type JobRunner struct {
	ID int64
	// Name and Labels are the registration's, Labels as the CI lists them.
	Name   string
	Labels []string
	// Files are what the runner reads, by name in model.RemoteRunnerDir:
	// its registration, with its address, labels and its own credential,
	// good for its one job alone, and its settings. They never hold the
	// registration token.
	Files map[string][]byte
	// Args are the runner binary's arguments: it takes its one job, runs it
	// and exits.
	Args []string
}
