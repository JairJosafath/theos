package ports

import (
	"context"
	"errors"
	"time"

	"github.com/JairJosafath/theos/internal/model"
)

// TestRuns is the optional capability of a TestEnvironments provider whose
// environment runs test packages rather than commands, such as AWS Device
// Farm's phones (docs/plans/done/test-environments.md, T12, step 10). A lease of
// it is Ready at model.TestRunEndpoint, its branch on the Git host: the run
// that holds it can't reach the operator, which holds the provider's
// credential, so it pushes its requests there, and the operator starts
// each on the provider with these calls and pushes the report back.
type TestRuns interface {
	// StartTestRun starts req's run, or moves on the one started for its
	// ID as far as it goes: the app and the test package uploaded, then the
	// run scheduled. Repeating it starts nothing new. A run the provider
	// refuses on its own grounds, such as an app it can't read, is Done
	// with that as its message; a refused credential or a provider that
	// can't be reached is the error.
	StartTestRun(ctx context.Context, env Environment, req TestRunRequest) (TestRun, error)
	// GetTestRun reads run id back; ErrTestRunNotFound for one the provider
	// doesn't have, such as one whose uploads it still processes.
	GetTestRun(ctx context.Context, env Environment, id string) (TestRun, error)
	// TestRunReport is a Done run's report, its devices and the tests that
	// didn't pass, and its artifacts, each at most model.TestRunMaxArtifact
	// and model.TestRunMaxArtifacts together; the report lists the ones
	// left out.
	TestRunReport(ctx context.Context, env Environment, id string) (TestRunReport, error)
}

// ErrTestRunNotFound is GetTestRun's error for a run the provider doesn't
// have.
var ErrTestRunNotFound = errors.New("no such test run")

// DeviceFarmEnvironment is the deviceFarm member: AWS Device Farm's devices,
// in model.DeviceFarmRegion.
type DeviceFarmEnvironment struct {
	// ProjectARN is the project the runs go in, and DevicePoolARN the
	// devices they run on.
	ProjectARN, DevicePoolARN string
	// Account, when set, is the AWS account the credential must be in.
	Account string
}

// TestRunRequest asks for one test run.
type TestRunRequest struct {
	// ID names the run (model.TestRunID): a second request with it gets the
	// same run.
	ID string
	// Lease is the lease the run is under; Return stops its runs.
	Lease string
	// Type is one of model.TestRunTypes.
	Type string
	// App and Tests are the app and the test package.
	App, Tests TestRunFile
	// Timeout is how long each device may run the tests at most.
	Timeout time.Duration
}

// TestRunFile is a file a test run takes, by its name.
type TestRunFile struct {
	Name string
	Data []byte
}

// TestRun is one test run on a provider.
type TestRun struct {
	ID string
	// State is model.TestRunPending, TestRunRunning or TestRunDone.
	State string
	// Result is a model.TestRun result once Done.
	Result string
	// Message says why a run is Pending, or how it ended.
	Message string
	// URL is the run on the provider's console.
	URL      string
	Counters model.TestRunCounters
	// Devices are how many devices the run runs on.
	Devices int
	// CostMilliUSD is what the run cost so far.
	CostMilliUSD int64
	Started      time.Time
}

// TestRunReport is a Done run's report and the artifacts it lists.
type TestRunReport struct {
	Report model.TestRunReport
	// Artifacts are the files, by their path in the report.
	Artifacts []TestRunArtifact
}

// TestRunArtifact is a file a run made.
type TestRunArtifact struct {
	Path string
	Data []byte
}
