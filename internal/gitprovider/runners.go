package gitprovider

import "context"

// OwnerRunners is an optional capability of the CI port: the CI runners
// registered to the provider's owner organization, which the operator keeps
// as a pool of single-job runners per project (docs/plans/ci-runner-isolation.md).
// A Provider that has it implements it too; find it with a type assertion.
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
