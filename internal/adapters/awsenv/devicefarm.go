package awsenv

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/devicefarm"
	dftypes "github.com/aws/aws-sdk-go-v2/service/devicefarm/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// The deviceFarm member of a TestEnvironment (docs/plans/done/test-environments.md,
// T12, step 10): AWS Device Farm's real phones in the user's account, which
// run an app's test packages rather than commands. A lease holds no device:
// it is the run's right to ask for test runs, within the environment's
// limits, recorded as a tag on the environment's Device Farm project so a
// restarted operator finds it. The operator starts each test run the
// lease's run asks for (StartTestRun), named by the lease, so the lease's
// cost is its runs' metered device minutes and Return stops the runs it
// left. The app and the test package go up to the pre-signed addresses
// Device Farm gives, and the artifacts come down from them, with the
// operator's own HTTP client: nothing of it reaches the run but the report
// and the files.

// DeviceFarmAPI is what the member calls of AWS Device Farm.
type DeviceFarmAPI interface {
	CreateUpload(ctx context.Context, in *devicefarm.CreateUploadInput,
		opts ...func(*devicefarm.Options)) (*devicefarm.CreateUploadOutput, error)
	GetUpload(ctx context.Context, in *devicefarm.GetUploadInput,
		opts ...func(*devicefarm.Options)) (*devicefarm.GetUploadOutput, error)
	ListUploads(ctx context.Context, in *devicefarm.ListUploadsInput,
		opts ...func(*devicefarm.Options)) (*devicefarm.ListUploadsOutput, error)
	DeleteUpload(ctx context.Context, in *devicefarm.DeleteUploadInput,
		opts ...func(*devicefarm.Options)) (*devicefarm.DeleteUploadOutput, error)
	ScheduleRun(ctx context.Context, in *devicefarm.ScheduleRunInput,
		opts ...func(*devicefarm.Options)) (*devicefarm.ScheduleRunOutput, error)
	ListRuns(ctx context.Context, in *devicefarm.ListRunsInput,
		opts ...func(*devicefarm.Options)) (*devicefarm.ListRunsOutput, error)
	StopRun(ctx context.Context, in *devicefarm.StopRunInput,
		opts ...func(*devicefarm.Options)) (*devicefarm.StopRunOutput, error)
	ListJobs(ctx context.Context, in *devicefarm.ListJobsInput,
		opts ...func(*devicefarm.Options)) (*devicefarm.ListJobsOutput, error)
	ListUniqueProblems(ctx context.Context, in *devicefarm.ListUniqueProblemsInput,
		opts ...func(*devicefarm.Options)) (*devicefarm.ListUniqueProblemsOutput, error)
	ListArtifacts(ctx context.Context, in *devicefarm.ListArtifactsInput,
		opts ...func(*devicefarm.Options)) (*devicefarm.ListArtifactsOutput, error)
	GetDevicePool(ctx context.Context, in *devicefarm.GetDevicePoolInput,
		opts ...func(*devicefarm.Options)) (*devicefarm.GetDevicePoolOutput, error)
	TagResource(ctx context.Context, in *devicefarm.TagResourceInput,
		opts ...func(*devicefarm.Options)) (*devicefarm.TagResourceOutput, error)
	UntagResource(ctx context.Context, in *devicefarm.UntagResourceInput,
		opts ...func(*devicefarm.Options)) (*devicefarm.UntagResourceOutput, error)
	ListTagsForResource(ctx context.Context, in *devicefarm.ListTagsForResourceInput,
		opts ...func(*devicefarm.Options)) (*devicefarm.ListTagsForResourceOutput, error)
}

// DeviceFarmClients are the clients of one credential: Device Farm and STS
// in model.DeviceFarmRegion, and the HTTP client that sends the uploads to,
// and reads the artifacts from, the pre-signed addresses Device Farm gives.
type DeviceFarmClients struct {
	DeviceFarm DeviceFarmAPI
	STS        STSAPI
	HTTP       *http.Client
}

// NewDeviceFarmClients builds the clients of cfg: AWSDeviceFarmClients on
// AWS, fakes in tests.
type NewDeviceFarmClients func(cfg aws.Config) DeviceFarmClients

// transferTimeout bounds one upload or download of a run's files.
const transferTimeout = 10 * time.Minute

// AWSDeviceFarmClients are the SDK's clients of cfg.
func AWSDeviceFarmClients(cfg aws.Config) DeviceFarmClients {
	return DeviceFarmClients{DeviceFarm: devicefarm.NewFromConfig(cfg), STS: sts.NewFromConfig(cfg),
		HTTP: &http.Client{Timeout: transferTimeout}}
}

// DeviceFarm leases Device Farm's devices with each environment's own
// credential and runs the test packages its leases' runs ask for.
type DeviceFarm struct {
	clients NewDeviceFarmClients
	now     func() time.Time
}

var (
	_ ports.TestEnvironments           = (*DeviceFarm)(nil)
	_ ports.TestEnvironmentCredentials = (*DeviceFarm)(nil)
	_ ports.TestRuns                   = (*DeviceFarm)(nil)
)

// NewDeviceFarm returns the deviceFarm member's provider, on AWS when
// clients is nil.
func NewDeviceFarm(clients NewDeviceFarmClients) *DeviceFarm {
	if clients == nil {
		clients = AWSDeviceFarmClients
	}
	return &DeviceFarm{clients: clients, now: time.Now}
}

// farm is an environment's credential at work on Device Farm.
type farm struct {
	env ports.Environment
	c   DeviceFarmClients
}

// session opens env's credential in Device Farm's region, without calling
// AWS.
func (p *DeviceFarm) session(env ports.Environment) (*farm, error) {
	if env.DeviceFarm == nil {
		return nil, fmt.Errorf("test environment %s/%s has no deviceFarm member", env.Namespace, env.Name)
	}
	regional := env
	regional.AWS = &ports.AWSEnvironment{Region: model.DeviceFarmRegion, Account: env.DeviceFarm.Account}
	cfg, err := config(regional)
	if err != nil {
		return nil, err
	}
	return &farm{env: env, c: p.clients(cfg)}, nil
}

// whoAmI refuses a credential AWS doesn't take, and one of another account
// than the environment names.
func (f *farm) whoAmI(ctx context.Context) error {
	env := f.env
	env.AWS = &ports.AWSEnvironment{Region: model.DeviceFarmRegion, Account: env.DeviceFarm.Account}
	return (&session{env: env, clients: Clients{STS: f.c.STS}}).whoAmI(ctx)
}

// CheckCredentials reads the credential's identity, refuses an account
// other than the environment's, and reads its device pool, which every
// test run runs on.
func (p *DeviceFarm) CheckCredentials(ctx context.Context, env ports.Environment) error {
	f, err := p.session(env)
	if err != nil {
		return err
	}
	if err := f.whoAmI(ctx); err != nil {
		return err
	}
	return f.pool(ctx)
}

// pool reads the environment's device pool.
func (f *farm) pool(ctx context.Context) error {
	_, err := f.c.DeviceFarm.GetDevicePool(ctx, &devicefarm.GetDevicePoolInput{Arn: aws.String(f.env.DeviceFarm.DevicePoolARN)})
	if err != nil && apiCode(err) == "NotFoundException" {
		return fmt.Errorf("device pool %s is gone from Device Farm", f.env.DeviceFarm.DevicePoolARN)
	}
	return portError(err)
}

// leaseTagPrefix starts the project tag of each lease of an environment,
// which its UID follows, so two factories on one project never read each
// other's.
const leaseTagPrefix = "theos.factory.io/lease:"

// leaseRecord is a lease as its tag on the project keeps it.
type leaseRecord struct {
	id, holder        string
	started, deadline time.Time
}

func (f *farm) tagKey(id string) string { return leaseTagPrefix + f.env.UID + ":" + id }

// leases are the environment's leases its project's tags record, by ID.
func (f *farm) leases(ctx context.Context) (map[string]leaseRecord, error) {
	out, err := f.c.DeviceFarm.ListTagsForResource(ctx, &devicefarm.ListTagsForResourceInput{
		ResourceARN: aws.String(f.env.DeviceFarm.ProjectARN)})
	if err != nil {
		return nil, portError(err)
	}
	prefix := f.tagKey("")
	records := map[string]leaseRecord{}
	for _, t := range out.Tags {
		id, ok := strings.CutPrefix(aws.ToString(t.Key), prefix)
		if !ok || id == "" {
			continue
		}
		holder, times, _ := strings.Cut(aws.ToString(t.Value), " ")
		startedText, deadlineText, _ := strings.Cut(times, " ")
		started, _ := time.Parse(time.RFC3339, startedText)
		deadline, _ := time.Parse(time.RFC3339, deadlineText)
		records[id] = leaseRecord{id: id, holder: holder, started: started, deadline: deadline}
	}
	return records, nil
}

// Lease records req's lease on the project, Ready at once at its branch,
// or returns the one recorded for its ID. A pool that is gone, or a project
// that takes no more tags, is a Failed lease with Device Farm's message.
func (p *DeviceFarm) Lease(ctx context.Context, req ports.LeaseRequest) (ports.Lease, error) {
	f, err := p.session(req.Env)
	if err != nil {
		return ports.Lease{}, err
	}
	records, err := f.leases(ctx)
	if err != nil {
		return ports.Lease{}, err
	}
	failed := func(err error) (ports.Lease, error) {
		if portKind(err) {
			return ports.Lease{}, err
		}
		return ports.Lease{ID: req.ID, Holder: req.Holder, State: model.LeaseFailed, Message: err.Error()}, nil
	}
	if err := f.pool(ctx); err != nil {
		return failed(err)
	}
	rec, ok := records[req.ID]
	if !ok {
		now := p.now().UTC().Truncate(time.Second)
		deadline := req.Deadline
		if deadline <= 0 {
			deadline = model.LeaseDeadline(0)
		}
		rec = leaseRecord{id: req.ID, holder: req.Holder, started: now, deadline: now.Add(deadline)}
		if _, err := f.c.DeviceFarm.TagResource(ctx, &devicefarm.TagResourceInput{
			ResourceARN: aws.String(f.env.DeviceFarm.ProjectARN),
			Tags: []dftypes.Tag{{Key: aws.String(f.tagKey(req.ID)), Value: aws.String(req.Holder + " " +
				rec.started.Format(time.RFC3339) + " " + rec.deadline.Format(time.RFC3339))}}}); err != nil {
			return failed(fmt.Errorf("recording the lease on project %s: %w", f.env.DeviceFarm.ProjectARN, portError(err)))
		}
	}
	runs, err := f.runs(ctx, req.ID)
	if err != nil {
		return ports.Lease{}, err
	}
	return p.leaseOf(rec, runs), nil
}

// leaseOf is a lease from its record and its runs: Ready at its branch,
// Failed past its deadline, costing its runs' metered minutes.
func (p *DeviceFarm) leaseOf(rec leaseRecord, runs []dftypes.Run) ports.Lease {
	l := ports.Lease{ID: rec.id, Holder: rec.holder, Started: rec.started, State: model.LeaseReady,
		Endpoint: model.TestRunEndpoint(rec.id)}
	for _, r := range runs {
		l.CostMilliUSD += runCost(r)
	}
	if !rec.deadline.IsZero() && !p.now().Before(rec.deadline) {
		l.State, l.Endpoint, l.Message = model.LeaseFailed, "", "the lease passed its deadline"
	}
	return l
}

// Get reads lease id back; a lease whose device pool is gone is Failed.
func (p *DeviceFarm) Get(ctx context.Context, env ports.Environment, id string) (ports.Lease, error) {
	f, err := p.session(env)
	if err != nil {
		return ports.Lease{}, err
	}
	records, err := f.leases(ctx)
	if err != nil {
		return ports.Lease{}, err
	}
	rec, ok := records[id]
	if !ok {
		return ports.Lease{}, ports.ErrLeaseNotFound
	}
	if err := f.pool(ctx); err != nil {
		if portKind(err) {
			return ports.Lease{}, err
		}
		return ports.Lease{ID: id, Holder: rec.holder, Started: rec.started, State: model.LeaseFailed,
			Message: err.Error()}, nil
	}
	runs, err := f.runs(ctx, id)
	if err != nil {
		return ports.Lease{}, err
	}
	return p.leaseOf(rec, runs), nil
}

// Return stops lease id's runs still going and takes its tag off the
// project.
func (p *DeviceFarm) Return(ctx context.Context, env ports.Environment, id string) error {
	f, err := p.session(env)
	if err != nil {
		return err
	}
	runs, err := f.runs(ctx, id)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if r.Status == dftypes.ExecutionStatusCompleted || r.Status == dftypes.ExecutionStatusStopping {
			continue
		}
		if _, err := f.c.DeviceFarm.StopRun(ctx, &devicefarm.StopRunInput{Arn: r.Arn}); err != nil &&
			apiCode(err) != "NotFoundException" {
			return portError(err)
		}
	}
	_, err = f.c.DeviceFarm.UntagResource(ctx, &devicefarm.UntagResourceInput{
		ResourceARN: aws.String(f.env.DeviceFarm.ProjectARN), TagKeys: []string{f.tagKey(id)}})
	return portError(err)
}

// Leases lists the environment's leases its project's tags record, each
// costing its runs.
func (p *DeviceFarm) Leases(ctx context.Context, env ports.Environment) ([]ports.Lease, error) {
	f, err := p.session(env)
	if err != nil {
		return nil, err
	}
	records, err := f.leases(ctx)
	if err != nil || len(records) == 0 {
		return nil, err
	}
	runs, err := f.runs(ctx, "")
	if err != nil {
		return nil, err
	}
	out := make([]ports.Lease, 0, len(records))
	for _, rec := range records {
		out = append(out, p.leaseOf(rec, slices.DeleteFunc(slices.Clone(runs), func(r dftypes.Run) bool {
			return !strings.HasPrefix(aws.ToString(r.Name), rec.id+"-")
		})))
	}
	slices.SortFunc(out, func(a, b ports.Lease) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// runs are the project's runs of lease, every lease's when lease is "";
// a run of a lease is named by it (model.TestRunID).
func (f *farm) runs(ctx context.Context, lease string) ([]dftypes.Run, error) {
	var out []dftypes.Run
	in := &devicefarm.ListRunsInput{Arn: aws.String(f.env.DeviceFarm.ProjectARN)}
	for {
		page, err := f.c.DeviceFarm.ListRuns(ctx, in)
		if err != nil {
			return nil, portError(err)
		}
		for _, r := range page.Runs {
			if lease == "" || strings.HasPrefix(aws.ToString(r.Name), lease+"-") {
				out = append(out, r)
			}
		}
		if aws.ToString(page.NextToken) == "" {
			return out, nil
		}
		in.NextToken = page.NextToken
	}
}

// run is the project's run named id, nil when there is none.
func (f *farm) run(ctx context.Context, id string) (*dftypes.Run, error) {
	lease, _, _ := cutLast(id, "-")
	runs, err := f.runs(ctx, lease)
	if err != nil {
		return nil, err
	}
	for i := range runs {
		if aws.ToString(runs[i].Name) == id {
			return &runs[i], nil
		}
	}
	return nil, nil
}

func cutLast(s, sep string) (string, string, bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// deviceFarmType is how Device Farm names a test type and its uploads.
type deviceFarmType struct {
	app, tests dftypes.UploadType
	test       dftypes.TestType
}

// deviceFarmTypes are model.TestRunTypes as Device Farm names them.
var deviceFarmTypes = map[string]deviceFarmType{
	"instrumentation": {dftypes.UploadTypeAndroidApp, dftypes.UploadTypeInstrumentationTestPackage,
		dftypes.TestTypeInstrumentation},
	"xctest":    {dftypes.UploadTypeIosApp, dftypes.UploadTypeXctestTestPackage, dftypes.TestTypeXctest},
	"xctest-ui": {dftypes.UploadTypeIosApp, dftypes.UploadTypeXctestUiTestPackage, dftypes.TestTypeXctestUi},
}

// maxJobMinutes is the longest a metered Device Farm run may take per
// device.
const maxJobMinutes = 150

// StartTestRun returns the run named req.ID, or moves its uploads on: each
// file is uploaded once to the address Device Farm gives, and once Device
// Farm processed both, the run is scheduled on the environment's device
// pool. An upload Device Farm refuses ends the run, Errored, with its
// message.
func (p *DeviceFarm) StartTestRun(ctx context.Context, env ports.Environment, req ports.TestRunRequest) (ports.TestRun,
	error) {
	t, ok := deviceFarmTypes[req.Type]
	if !ok {
		return ports.TestRun{}, fmt.Errorf("test type %q is none Device Farm runs", req.Type)
	}
	f, err := p.session(env)
	if err != nil {
		return ports.TestRun{}, err
	}
	if run, err := f.run(ctx, req.ID); err != nil || run != nil {
		if run == nil {
			return ports.TestRun{}, err
		}
		return f.testRun(req.ID, run), nil
	}
	app, appDone, err := f.upload(ctx, req.ID+"-"+req.App.Name, t.app, req.App.Data)
	if err != nil {
		return refused(req.ID, err)
	}
	tests, testsDone, err := f.upload(ctx, req.ID+"-"+req.Tests.Name, t.tests, req.Tests.Data)
	if err != nil {
		return refused(req.ID, err)
	}
	if !appDone || !testsDone {
		return ports.TestRun{ID: req.ID, State: model.TestRunPending,
			Message: "Device Farm is processing the app and the test package"}, nil
	}
	minutes := int32(maxJobMinutes)
	if m := int32(req.Timeout / time.Minute); m > 0 && m < minutes {
		minutes = m
	}
	out, err := f.c.DeviceFarm.ScheduleRun(ctx, &devicefarm.ScheduleRunInput{
		Name: aws.String(req.ID), ProjectArn: aws.String(env.DeviceFarm.ProjectARN), AppArn: app.Arn,
		DevicePoolArn: aws.String(env.DeviceFarm.DevicePoolARN),
		Test:          &dftypes.ScheduleRunTest{Type: t.test, TestPackageArn: tests.Arn},
		ExecutionConfiguration: &dftypes.ExecutionConfiguration{JobTimeoutMinutes: aws.Int32(minutes),
			AccountsCleanup: aws.Bool(true), AppPackagesCleanup: aws.Bool(true)},
	})
	if err != nil {
		return refused(req.ID, portError(err))
	}
	return f.testRun(req.ID, out.Run), nil
}

// refused is a run Device Farm refused on its own grounds, Done and
// Errored; a refused credential or an AWS that can't be reached is the
// error.
func refused(id string, err error) (ports.TestRun, error) {
	if portKind(err) {
		return ports.TestRun{}, err
	}
	return ports.TestRun{ID: id, State: model.TestRunDone, Result: model.TestRunErrored, Message: err.Error()}, nil
}

// upload is the project's upload named name, made and sent data when it
// has none, and whether Device Farm processed it; Device Farm's refusal of
// it is the error.
func (f *farm) upload(ctx context.Context, name string, typ dftypes.UploadType, data []byte) (*dftypes.Upload, bool,
	error) {
	u, err := f.findUpload(ctx, name, typ)
	if err != nil {
		return nil, false, err
	}
	if u == nil {
		out, err := f.c.DeviceFarm.CreateUpload(ctx, &devicefarm.CreateUploadInput{Name: aws.String(name), Type: typ,
			ProjectArn: aws.String(f.env.DeviceFarm.ProjectARN), ContentType: aws.String("application/octet-stream")})
		if err != nil {
			return nil, false, portError(err)
		}
		return out.Upload, false, f.put(ctx, out.Upload, data)
	}
	switch u.Status {
	case dftypes.UploadStatusSucceeded:
		return u, true, nil
	case dftypes.UploadStatusFailed:
		return nil, false, fmt.Errorf("the upload %s was refused: %s", name, cmp.Or(aws.ToString(u.Message),
			"Device Farm can't read it"))
	case dftypes.UploadStatusInitialized:
		// Made, but never sent: send it again, to a fresh address.
		read, err := f.c.DeviceFarm.GetUpload(ctx, &devicefarm.GetUploadInput{Arn: u.Arn})
		if err != nil {
			return nil, false, portError(err)
		}
		return read.Upload, false, f.put(ctx, read.Upload, data)
	}
	return u, false, nil
}

// findUpload is the project's upload of typ named name, nil when there is
// none.
func (f *farm) findUpload(ctx context.Context, name string, typ dftypes.UploadType) (*dftypes.Upload, error) {
	in := &devicefarm.ListUploadsInput{Arn: aws.String(f.env.DeviceFarm.ProjectARN), Type: typ}
	for {
		page, err := f.c.DeviceFarm.ListUploads(ctx, in)
		if err != nil {
			return nil, portError(err)
		}
		for i := range page.Uploads {
			if aws.ToString(page.Uploads[i].Name) == name {
				return &page.Uploads[i], nil
			}
		}
		if aws.ToString(page.NextToken) == "" {
			return nil, nil
		}
		in.NextToken = page.NextToken
	}
}

// put sends data to u's pre-signed address.
func (f *farm) put(ctx context.Context, u *dftypes.Upload, data []byte) error {
	ctx, cancel := context.WithTimeout(ctx, transferTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, aws.ToString(u.Url), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := f.c.HTTP.Do(req)
	if err != nil {
		return &ports.Error{Kind: ports.ErrUnreachable, Cause: fmt.Errorf("uploading %s: %w", aws.ToString(u.Name), err)}
	}
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return &ports.Error{Kind: ports.ErrUnreachable,
			Cause: fmt.Errorf("uploading %s: Device Farm's storage answered %s", aws.ToString(u.Name), resp.Status)}
	}
	return nil
}

// GetTestRun reads run id back.
func (p *DeviceFarm) GetTestRun(ctx context.Context, env ports.Environment, id string) (ports.TestRun, error) {
	f, err := p.session(env)
	if err != nil {
		return ports.TestRun{}, err
	}
	run, err := f.run(ctx, id)
	switch {
	case err != nil:
		return ports.TestRun{}, err
	case run == nil:
		return ports.TestRun{}, ports.ErrTestRunNotFound
	}
	return f.testRun(id, run), nil
}

// testRun is what Device Farm says of run.
func (f *farm) testRun(id string, run *dftypes.Run) ports.TestRun {
	out := ports.TestRun{ID: id, State: model.TestRunPending, Message: aws.ToString(run.Message),
		URL: consoleURL(aws.ToString(run.Arn)), Counters: counters(run.Counters), Devices: int(aws.ToInt32(run.TotalJobs)),
		CostMilliUSD: runCost(*run), Started: aws.ToTime(run.Created)}
	switch run.Status {
	case dftypes.ExecutionStatusCompleted:
		out.State, out.Result = model.TestRunDone, cmp.Or(model.TestRunResultOf(string(run.Result)), model.TestRunErrored)
	case dftypes.ExecutionStatusRunning, dftypes.ExecutionStatusStopping:
		out.State = model.TestRunRunning
	}
	return out
}

// runCost is what a run's metered device minutes cost.
func runCost(r dftypes.Run) int64 {
	if r.DeviceMinutes == nil {
		return 0
	}
	return model.DeviceFarmCostMilliUSD(aws.ToFloat64(r.DeviceMinutes.Metered))
}

func counters(c *dftypes.Counters) model.TestRunCounters {
	if c == nil {
		return model.TestRunCounters{}
	}
	return model.TestRunCounters{Total: int(aws.ToInt32(c.Total)), Passed: int(aws.ToInt32(c.Passed)),
		Failed: int(aws.ToInt32(c.Failed)), Errored: int(aws.ToInt32(c.Errored)), Warned: int(aws.ToInt32(c.Warned)),
		Skipped: int(aws.ToInt32(c.Skipped)), Stopped: int(aws.ToInt32(c.Stopped))}
}

// arnPath is the resource path of a Device Farm ARN, such as
// [project run job] for a job's.
func arnPath(arn string) []string {
	parts := strings.SplitN(arn, ":", 7)
	if len(parts) < 7 {
		return nil
	}
	return strings.Split(parts[6], "/")
}

// consoleURL is run arn on Device Farm's console.
func consoleURL(arn string) string {
	p := arnPath(arn)
	if len(p) != 2 {
		return ""
	}
	return "https://" + model.DeviceFarmRegion + ".console.aws.amazon.com/devicefarm/home?region=" +
		model.DeviceFarmRegion + "#/mobile/projects/" + p[0] + "/runs/" + p[1]
}

// unsafePath are the characters an artifact's path is cleaned of.
var unsafePath = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeName(s string) string { return strings.Trim(unsafePath.ReplaceAllString(s, "-"), "-.") }

// TestRunReport is a finished run's report: its devices, the tests that
// didn't pass and its screenshots and files, videos left on the console,
// each at most model.TestRunMaxArtifact and all at most
// model.TestRunMaxArtifacts. The run's uploads are deleted then: Device
// Farm keeps the run without them.
func (p *DeviceFarm) TestRunReport(ctx context.Context, env ports.Environment, id string) (ports.TestRunReport, error) {
	f, err := p.session(env)
	if err != nil {
		return ports.TestRunReport{}, err
	}
	run, err := f.run(ctx, id)
	switch {
	case err != nil:
		return ports.TestRunReport{}, err
	case run == nil:
		return ports.TestRunReport{}, ports.ErrTestRunNotFound
	case run.Status != dftypes.ExecutionStatusCompleted:
		return ports.TestRunReport{}, fmt.Errorf("test run %s isn't done", id)
	}
	tr := f.testRun(id, run)
	report := model.TestRunReport{Environment: env.Name, Run: id, Result: tr.Result, Message: tr.Message, URL: tr.URL,
		Counters: tr.Counters, CostMilliUSD: tr.CostMilliUSD}
	devices, err := f.devices(ctx, run.Arn, &report)
	if err != nil {
		return ports.TestRunReport{}, err
	}
	if err := f.problems(ctx, run.Arn, &report); err != nil {
		return ports.TestRunReport{}, err
	}
	artifacts, err := f.artifacts(ctx, run.Arn, devices, &report)
	if err != nil {
		return ports.TestRunReport{}, err
	}
	f.deleteUploads(ctx, id)
	return ports.TestRunReport{Report: report, Artifacts: artifacts}, nil
}

// devices adds the run's jobs, one per device, to report, and returns the
// devices' names by job.
func (f *farm) devices(ctx context.Context, runARN *string, report *model.TestRunReport) (map[string]string, error) {
	names := map[string]string{}
	in := &devicefarm.ListJobsInput{Arn: runARN}
	for {
		page, err := f.c.DeviceFarm.ListJobs(ctx, in)
		if err != nil {
			return nil, portError(err)
		}
		for _, j := range page.Jobs {
			d := model.TestRunDevice{Name: aws.ToString(j.Name), Result: cmp.Or(model.TestRunResultOf(string(j.Result)),
				model.TestRunErrored), Message: aws.ToString(j.Message), Counters: counters(j.Counters)}
			if j.Device != nil {
				d.Name, d.OS = cmp.Or(aws.ToString(j.Device.Name), d.Name), aws.ToString(j.Device.Os)
			}
			report.Devices = append(report.Devices, d)
			if p := arnPath(aws.ToString(j.Arn)); len(p) >= 3 {
				names[p[2]] = d.Name
			}
		}
		if aws.ToString(page.NextToken) == "" {
			break
		}
		in.NextToken = page.NextToken
	}
	slices.SortFunc(report.Devices, func(a, b model.TestRunDevice) int { return strings.Compare(a.Name, b.Name) })
	return names, nil
}

// problems adds the tests that didn't pass to report.
func (f *farm) problems(ctx context.Context, runARN *string, report *model.TestRunReport) error {
	in := &devicefarm.ListUniqueProblemsInput{Arn: runARN}
	for {
		page, err := f.c.DeviceFarm.ListUniqueProblems(ctx, in)
		if err != nil {
			return portError(err)
		}
		for result, problems := range page.UniqueProblems {
			if strings.EqualFold(result, string(dftypes.ExecutionResultPassed)) {
				continue
			}
			for _, up := range problems {
				for _, pr := range up.Problems {
					p := model.TestRunProblem{Result: cmp.Or(model.TestRunResultOf(string(pr.Result)),
						model.TestRunResultOf(result)), Message: cmp.Or(aws.ToString(pr.Message), aws.ToString(up.Message))}
					if pr.Device != nil {
						p.Device = aws.ToString(pr.Device.Name)
					}
					if pr.Suite != nil {
						p.Suite = aws.ToString(pr.Suite.Name)
					}
					if pr.Test != nil {
						p.Test = aws.ToString(pr.Test.Name)
					}
					report.Problems = append(report.Problems, p)
				}
			}
		}
		if aws.ToString(page.NextToken) == "" {
			break
		}
		in.NextToken = page.NextToken
	}
	slices.SortFunc(report.Problems, func(a, b model.TestRunProblem) int {
		return cmp.Or(strings.Compare(a.Device, b.Device), strings.Compare(a.Suite, b.Suite),
			strings.Compare(a.Test, b.Test))
	})
	return nil
}

// videoTypes are the artifacts left on Device Farm's console: too big to
// bring back.
var videoTypes = []dftypes.ArtifactType{dftypes.ArtifactTypeVideo, dftypes.ArtifactTypeVideoLog}

// artifacts downloads the run's screenshots and files, within the caps,
// and lists each in report, by device; one left out says why.
func (f *farm) artifacts(ctx context.Context, runARN *string, devices map[string]string,
	report *model.TestRunReport) ([]ports.TestRunArtifact, error) {
	var listed []dftypes.Artifact
	for _, category := range []dftypes.ArtifactCategory{dftypes.ArtifactCategoryScreenshot, dftypes.ArtifactCategoryFile} {
		in := &devicefarm.ListArtifactsInput{Arn: runARN, Type: category}
		for {
			page, err := f.c.DeviceFarm.ListArtifacts(ctx, in)
			if err != nil {
				return nil, portError(err)
			}
			listed = append(listed, page.Artifacts...)
			if aws.ToString(page.NextToken) == "" {
				break
			}
			in.NextToken = page.NextToken
		}
	}
	var out []ports.TestRunArtifact
	total := 0
	seen := map[string]int{}
	for _, a := range listed {
		device := ""
		if p := arnPath(aws.ToString(a.Arn)); len(p) >= 3 {
			device = devices[p[2]]
		}
		name := safeName(aws.ToString(a.Name))
		if ext := safeName(aws.ToString(a.Extension)); ext != "" {
			name += "." + ext
		}
		file := cmp.Or(safeName(device), "run") + "/" + cmp.Or(name, "artifact")
		if n := seen[file]; n > 0 {
			file = fmt.Sprintf("%s-%d", file, n+1)
		}
		seen[file]++
		entry := model.TestRunArtifact{Path: file, Device: device, Type: string(a.Type)}
		switch {
		case slices.Contains(videoTypes, a.Type):
			entry.LeftOut = "videos stay on the provider's console"
		case total >= model.TestRunMaxArtifacts:
			entry.LeftOut = fmt.Sprintf("the run's artifacts passed the %d bytes brought back", model.TestRunMaxArtifacts)
		default:
			data, err := f.download(ctx, aws.ToString(a.Url), min(model.TestRunMaxArtifact, model.TestRunMaxArtifacts-total))
			switch {
			case errors.Is(err, errTooBig):
				entry.LeftOut = fmt.Sprintf("bigger than the %d bytes an artifact may be",
					min(model.TestRunMaxArtifact, model.TestRunMaxArtifacts-total))
			case err != nil:
				entry.LeftOut = "it couldn't be downloaded: " + err.Error()
			default:
				entry.Bytes = len(data)
				total += len(data)
				out = append(out, ports.TestRunArtifact{Path: file, Data: data})
			}
		}
		report.Artifacts = append(report.Artifacts, entry)
	}
	return out, nil
}

var errTooBig = errors.New("too big")

// download reads url's body, refusing one longer than limit.
func (f *farm) download(ctx context.Context, url string, limit int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, transferTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the artifact's address answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, errTooBig
	}
	return data, nil
}

// deleteUploads deletes run id's uploads, best effort: a run keeps its
// results without them, and the project needn't keep every app.
func (f *farm) deleteUploads(ctx context.Context, id string) {
	for _, t := range []dftypes.UploadType{dftypes.UploadTypeAndroidApp, dftypes.UploadTypeIosApp,
		dftypes.UploadTypeInstrumentationTestPackage, dftypes.UploadTypeXctestTestPackage,
		dftypes.UploadTypeXctestUiTestPackage} {
		in := &devicefarm.ListUploadsInput{Arn: aws.String(f.env.DeviceFarm.ProjectARN), Type: t}
		page, err := f.c.DeviceFarm.ListUploads(ctx, in)
		if err != nil {
			return
		}
		for _, u := range page.Uploads {
			if strings.HasPrefix(aws.ToString(u.Name), id+"-") {
				_, _ = f.c.DeviceFarm.DeleteUpload(ctx, &devicefarm.DeleteUploadInput{Arn: u.Arn})
			}
		}
	}
}
