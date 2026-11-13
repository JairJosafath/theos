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

import "context"

// TestEnvironmentRunners is the optional capability of a remote provider to
// lease a machine that runs one CI job instead of serving an agent's runs
// (docs/plans/done/test-environments.md, step 8): its machine starts runner, a
// runner the CI registered for one job (JobRunners), fenced as any lease's
// machine is, and ends when that job does. The lease is an ordinary one:
// Get, Return and Leases take it, and its Holder names the runner
// (model.CIHolder).
type TestEnvironmentRunners interface {
	// RunnerMissing says why the provider can start no runner, such as the
	// operator having no address to download one from; "" when it can.
	RunnerMissing() string
	// LeaseRunner is Lease for a machine that runs runner: repeating it
	// starts nothing new.
	LeaseRunner(ctx context.Context, req LeaseRequest, runner JobRunner) (Lease, error)
}
