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
