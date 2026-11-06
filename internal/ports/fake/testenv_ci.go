package fake

import (
	"context"

	"github.com/JairJosafath/theos/internal/ports"
)

// The fake provider's leases that run a CI runner
// (docs/plans/done/test-environments.md, step 8).

var _ ports.TestEnvironmentRunners = (*TestEnvironments)(nil)

// SetRunnerMissing makes RunnerMissing say why, "" for a provider that
// starts runners.
func (f *TestEnvironments) SetRunnerMissing(why string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runnerMissing = why
}

// RunnerMissing is what SetRunnerMissing set.
func (f *TestEnvironments) RunnerMissing() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runnerMissing
}

// LeaseRunner is Lease, recording runner.
func (f *TestEnvironments) LeaseRunner(ctx context.Context, req ports.LeaseRequest, runner ports.JobRunner) (
	ports.Lease, error) {
	l, err := f.Lease(ctx, req)
	if err != nil {
		return l, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if held := f.leases[keyOf(req.Env)][req.ID]; held.runner == nil {
		held.runner = &runner
	}
	return l, nil
}

// Runner is the CI runner lease id of env runs, and whether it runs one.
func (f *TestEnvironments) Runner(env ports.Environment, id string) (ports.JobRunner, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.leases[keyOf(env)][id]
	if !ok || l.runner == nil {
		return ports.JobRunner{}, false
	}
	return *l.runner, true
}
