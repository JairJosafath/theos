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
