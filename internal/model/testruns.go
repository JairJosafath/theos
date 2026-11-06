package model

import (
	"cmp"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"path"
	"slices"
	"strconv"
	"strings"
)

// Test runs (docs/plans/test-environments.md, T12, step 10): an environment
// whose provider runs test packages rather than commands, such as AWS Device
// Farm's real phones. A run that leased one can't reach the operator, which
// holds the provider's credential, nor may the credential reach the run: the
// run asks over its Git host instead, which the sandbox fence lets it reach.
// `theos-env test` pushes a request commit to the lease's branch,
// TestRunBranch, holding the request, the app and the test package; the
// operator reads it there, checks it against the lease's token, starts the
// run on the provider, and pushes the report and its artifacts to the same
// branch, where theos-env reads them back into the working tree. The
// operator comments each finished run on the Task's pull request, where the
// reviewer and people see it.

// TestEnvMemberDeviceFarm is the deviceFarm member of a TestEnvironment: AWS
// Device Farm's real phones, which run test packages (T12).
const TestEnvMemberDeviceFarm = "deviceFarm"

// DeviceFarmRegion is the one AWS region Device Farm serves its devices
// from.
const DeviceFarmRegion = "us-west-2"

// DeviceFarmMilliUSDPerMinute is what AWS charges for a metered device
// minute, in thousandths of a US dollar: its list price, 0.17 USD. Device
// Farm isn't in the AWS Price List the EC2 member reads.
const DeviceFarmMilliUSDPerMinute = 170

// DeviceFarmCostMilliUSD is what metered device minutes cost, rounded up,
// so a budget never counts less than AWS bills.
func DeviceFarmCostMilliUSD(meteredMinutes float64) int64 {
	if meteredMinutes <= 0 {
		return 0
	}
	return int64(math.Ceil(meteredMinutes * DeviceFarmMilliUSDPerMinute))
}

// TestRunBranchPrefix starts the branch of each lease of a test-run
// environment.
const TestRunBranchPrefix = "theos-env/"

// testRunScheme starts a test-run lease's endpoint: its branch on the Git
// host, not an address.
const testRunScheme = "git:"

// TestRunBranch is the branch of lease id on the Container's Git host, where
// its run pushes test-run requests and the operator their results.
func TestRunBranch(id string) string { return TestRunBranchPrefix + id }

// TestRunEndpoint is a test-run lease's endpoint, THEOS_ENV_<NAME>_URL: its
// branch, git:theos-env/<lease>.
func TestRunEndpoint(id string) string { return testRunScheme + TestRunBranch(id) }

// TestRunBranchOf is the branch a test-run lease's endpoint names, and
// whether endpoint is one: an endpoint of an environment that runs commands
// is an address.
func TestRunBranchOf(endpoint string) (string, bool) {
	branch, ok := strings.CutPrefix(endpoint, testRunScheme)
	return branch, ok && strings.HasPrefix(branch, TestRunBranchPrefix) && len(branch) > len(TestRunBranchPrefix)
}

// The files of a test-run request commit, and of the operator's result.
const (
	TestRunRequestFile = ".theos-env/request.json"
	TestRunAppDir      = ".theos-env/app/"
	TestRunTestsDir    = ".theos-env/tests/"
	// TestRunResultDir holds the result: TestRunAnswerFile, TestRunReportFile
	// and the artifacts under TestRunArtifactsDir.
	TestRunResultDir = ".theos-env/result/"
	// TestRunAnswerFile holds the request commit the result answers.
	TestRunAnswerFile    = TestRunResultDir + "request"
	TestRunReportFile    = TestRunResultDir + "report.json"
	TestRunArtifactsDir  = TestRunResultDir + "artifacts/"
	testRunRequestFormat = 1
)

// Caps on a test run's files: the app and the test package a request sends,
// together, and the artifacts the operator brings back, each and together.
const (
	TestRunMaxUpload    = 256 << 20
	TestRunMaxArtifact  = 16 << 20
	TestRunMaxArtifacts = 64 << 20
)

// TestRunType is a kind of test package: which app and test package files
// it takes.
type TestRunType struct {
	// AppExt and TestsExt are the files' extensions.
	AppExt, TestsExt string
	// Platform is the devices it runs on: Android or iOS.
	Platform string
}

// TestRunTypes are the test packages a request may run, by the name
// `theos-env test --type` takes: Android instrumentation (Espresso,
// UI Automator) and iOS XCTest and XCUITest.
var TestRunTypes = map[string]TestRunType{
	"instrumentation": {AppExt: extAPK, TestsExt: extAPK, Platform: "Android"},
	"xctest":          {AppExt: extIPA, TestsExt: ".zip", Platform: "iOS"},
	"xctest-ui":       {AppExt: extIPA, TestsExt: extIPA, Platform: "iOS"},
}

// An Android app's file extension, and an iOS app's.
const (
	extAPK = ".apk"
	extIPA = ".ipa"
)

// TestRunTypeNames are TestRunTypes' names, sorted.
func TestRunTypeNames() []string {
	names := make([]string, 0, len(TestRunTypes))
	for n := range TestRunTypes {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// TestRunRequest is a test-run request commit's request.json: what to run,
// on which commit of the work, and the lease's MAC over it, which only the
// run that holds the lease's token can make.
type TestRunRequest struct {
	Format int `json:"format"`
	// Lease is the lease the request runs under, the branch's.
	Lease string `json:"lease"`
	// Commit is the sandbox's commit the app and tests were built from, the
	// request commit's parent.
	Commit string `json:"commit"`
	// Type is one of TestRunTypes.
	Type string `json:"type"`
	// App and Tests are the files' names under TestRunAppDir and
	// TestRunTestsDir, and their SHA-256s, hex.
	App         string `json:"app"`
	AppSHA256   string `json:"appSHA256"`
	Tests       string `json:"tests"`
	TestsSHA256 string `json:"testsSHA256"`
	// MAC is TestRunMAC's, base64url.
	MAC string `json:"mac,omitempty"`
}

// NewTestRunRequest is the request to run app and tests, as type, on commit
// under lease, signed with the lease's token.
func NewTestRunRequest(token, lease, commit, typ, appName string, app []byte, testsName string,
	tests []byte) TestRunRequest {
	r := TestRunRequest{Format: testRunRequestFormat, Lease: lease, Commit: commit, Type: typ,
		App: appName, AppSHA256: sha256Hex(app), Tests: testsName, TestsSHA256: sha256Hex(tests)}
	r.MAC = TestRunMAC(token, r)
	return r
}

// TestRunMAC is the HMAC-SHA256 under the lease's token of everything r
// asks for, base64url: the operator runs a request only with it, so a run
// can't spend another lease's devices.
func TestRunMAC(token string, r TestRunRequest) string {
	mac := hmac.New(sha256.New, []byte(token))
	for _, field := range []string{"theos-env test v1", strconv.Itoa(r.Format), r.Lease, r.Commit, r.Type, r.App,
		r.AppSHA256, r.Tests, r.TestsSHA256} {
		mac.Write([]byte(field))
		mac.Write([]byte{0})
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// CheckTestRunFiles says what is wrong with a request to run app and tests
// as typ, nil when nothing is: a known type, plain file names with its
// extensions, and both within TestRunMaxUpload together.
func CheckTestRunFiles(typ, appName string, appSize int, testsName string, testsSize int) error {
	t, ok := TestRunTypes[typ]
	if !ok {
		return fmt.Errorf("test type %q is none of %s", typ, joinAnd(TestRunTypeNames()))
	}
	for _, f := range []struct{ what, name, ext string }{{"app", appName, t.AppExt}, {"test package", testsName, t.TestsExt}} {
		if f.name == "" || f.name != path.Base(f.name) || strings.HasPrefix(f.name, ".") ||
			strings.ContainsAny(f.name, "/\\\x00\n") {
			return fmt.Errorf("the %s's name %q is no plain file name", f.what, f.name)
		}
		if !strings.EqualFold(path.Ext(f.name), f.ext) {
			return fmt.Errorf("a %s test's %s is a %s file, not %q", typ, f.what, f.ext, f.name)
		}
	}
	if appSize+testsSize > TestRunMaxUpload {
		return fmt.Errorf("the app and the test package are %d bytes together, more than the %d a test run takes",
			appSize+testsSize, TestRunMaxUpload)
	}
	return nil
}

// CheckTestRun says why the operator doesn't run r, read from lease's
// branch with app and tests, nil when it does: its format, its lease, its
// files and their digests, and its MAC under the lease's token.
func CheckTestRun(token, lease string, r TestRunRequest, app, tests []byte) error {
	switch {
	case r.Format != testRunRequestFormat:
		return fmt.Errorf("the request's format is %d; this operator reads format %d", r.Format, testRunRequestFormat)
	case r.Lease != lease:
		return fmt.Errorf("the request names lease %q, and is on the branch of lease %q", r.Lease, lease)
	case len(r.Commit) < 7 || strings.Trim(r.Commit, "0123456789abcdef") != "":
		return fmt.Errorf("the request's commit %q is no commit", r.Commit)
	}
	if err := CheckTestRunFiles(r.Type, r.App, len(app), r.Tests, len(tests)); err != nil {
		return err
	}
	if sha256Hex(app) != r.AppSHA256 || sha256Hex(tests) != r.TestsSHA256 {
		return errors.New("the app or the test package isn't the one the request names")
	}
	if token == "" || !hmac.Equal([]byte(TestRunMAC(token, r)), []byte(r.MAC)) {
		return errors.New("the request isn't signed with the lease's token: only the run that holds the lease asks for its test runs")
	}
	return nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestRunID is the provider's name of the test run request commit asks
// for under lease: the lease and the commit, so a second look at the same
// request starts nothing new.
func TestRunID(lease, request string) string {
	return lease + "-" + request[:min(len(request), 12)]
}

// The states of a test run on its provider (ports.TestRun.State).
const (
	TestRunPending = "Pending"
	TestRunRunning = "Running"
	TestRunDone    = "Done"
)

// The states of a test run in a TestEnvironment's status: the provider's,
// and Reported once its result is on the lease's branch, Rejected when the
// operator ran nothing.
const (
	TestRunReported = "Reported"
	TestRunRejected = "Rejected"
)

// The results of a test run, as Device Farm's.
const (
	TestRunPassed   = "Passed"
	TestRunWarned   = "Warned"
	TestRunFailed   = "Failed"
	TestRunSkipped  = "Skipped"
	TestRunErrored  = "Errored"
	TestRunStopped  = "Stopped"
	TestRunRefused  = "Rejected"
	testRunNoResult = ""
)

// TestRunResultOf is a provider's result word as a TestRun result: PASSED
// is Passed; one it doesn't know is Errored.
func TestRunResultOf(word string) string {
	for _, r := range []string{TestRunPassed, TestRunWarned, TestRunFailed, TestRunSkipped, TestRunErrored,
		TestRunStopped} {
		if strings.EqualFold(word, r) {
			return r
		}
	}
	if word == "" || strings.EqualFold(word, "PENDING") {
		return testRunNoResult
	}
	return TestRunErrored
}

// TestRunCounters are a run's or a device's tests by result.
type TestRunCounters struct {
	Total   int `json:"total"`
	Passed  int `json:"passed,omitempty"`
	Failed  int `json:"failed,omitempty"`
	Errored int `json:"errored,omitempty"`
	Warned  int `json:"warned,omitempty"`
	Skipped int `json:"skipped,omitempty"`
	Stopped int `json:"stopped,omitempty"`
}

// TestRunDevice is one device's part of a run.
type TestRunDevice struct {
	Name     string          `json:"name"`
	OS       string          `json:"os,omitempty"`
	Result   string          `json:"result"`
	Message  string          `json:"message,omitempty"`
	Counters TestRunCounters `json:"counters"`
}

// TestRunProblem is a test that didn't pass on a device.
type TestRunProblem struct {
	Device  string `json:"device"`
	Suite   string `json:"suite,omitempty"`
	Test    string `json:"test,omitempty"`
	Result  string `json:"result"`
	Message string `json:"message,omitempty"`
}

// TestRunArtifact is a file a run made, such as a screenshot or a log: its
// path under the report's artifacts folder, or why it was left out.
type TestRunArtifact struct {
	Path    string `json:"path"`
	Device  string `json:"device,omitempty"`
	Type    string `json:"type,omitempty"`
	Bytes   int    `json:"bytes"`
	LeftOut string `json:"leftOut,omitempty"`
}

// TestRunReport is a test run's report, report.json on the lease's branch
// and in the folder `theos-env test` writes.
type TestRunReport struct {
	// Environment is the TestEnvironment's name, Run the test run's ID.
	Environment string `json:"environment"`
	Run         string `json:"run,omitempty"`
	// Commit is the commit the request was built from, and Type its test
	// type.
	Commit string `json:"commit"`
	Type   string `json:"type,omitempty"`
	// Result is a TestRun result, or TestRunRefused when the operator ran
	// nothing, and Message why.
	Result  string `json:"result"`
	Message string `json:"message,omitempty"`
	// URL is the run on the provider's console, for people with access.
	URL       string            `json:"url,omitempty"`
	Counters  TestRunCounters   `json:"counters"`
	Devices   []TestRunDevice   `json:"devices,omitempty"`
	Problems  []TestRunProblem  `json:"problems,omitempty"`
	Artifacts []TestRunArtifact `json:"artifacts,omitempty"`
	// CostMilliUSD is what the run cost, by the provider's count.
	CostMilliUSD int64 `json:"costMilliUSD,omitempty"`
}

// TestRunPassedResult reports whether result is a run every test of which
// passed: Passed, or Warned, which Device Farm gives a pass with warnings.
func TestRunPassedResult(result string) bool {
	return result == TestRunPassed || result == TestRunWarned
}

// TestRunOutcome is a report's one line: its result and its counts.
func TestRunOutcome(r TestRunReport) string {
	if r.Result == TestRunRefused {
		return "rejected: " + r.Message
	}
	c := r.Counters
	var parts []string
	for _, n := range []struct {
		n      int
		result string
	}{{c.Passed, TestRunPassed}, {c.Failed, TestRunFailed}, {c.Errored, TestRunErrored}, {c.Warned, TestRunWarned},
		{c.Skipped, TestRunSkipped}, {c.Stopped, TestRunStopped}} {
		if n.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n.n, strings.ToLower(n.result)))
		}
	}
	counts := "no tests ran"
	if len(parts) > 0 {
		counts = strings.Join(parts, ", ") + fmt.Sprintf(" of %d tests", c.Total)
	}
	out := fmt.Sprintf("%s: %s on %d %s", strings.ToLower(cmp.Or(r.Result, TestRunErrored)), counts, len(r.Devices),
		plural(len(r.Devices), "device", "devices"))
	if r.Message != "" && !TestRunPassedResult(r.Result) {
		out += " (" + r.Message + ")"
	}
	return out
}

// maxListedProblems caps the problems a summary lists.
const maxListedProblems = 20

// TestRunSummary is a report as `theos-env test` prints it and the operator
// comments it on the pull request: the outcome, each device's, and the
// tests that didn't pass.
func TestRunSummary(r TestRunReport) string {
	var b strings.Builder
	commit := r.Commit[:min(len(r.Commit), 12)]
	fmt.Fprintf(&b, "Test run %s of %s on %s: %s.\n", cmp.Or(r.Type, "request"), commit, r.Environment,
		TestRunOutcome(r))
	for _, d := range r.Devices {
		fmt.Fprintf(&b, "- %s", d.Name)
		if d.OS != "" {
			fmt.Fprintf(&b, " (%s)", d.OS)
		}
		fmt.Fprintf(&b, ": %s, %d of %d passed\n", strings.ToLower(d.Result), d.Counters.Passed, d.Counters.Total)
	}
	for i, p := range r.Problems {
		if i == maxListedProblems {
			fmt.Fprintf(&b, "and %d more tests that didn't pass, in the report.\n", len(r.Problems)-i)
			break
		}
		name := strings.Trim(p.Suite+"."+p.Test, ".")
		fmt.Fprintf(&b, "- %s on %s: %s", cmp.Or(name, "the run"), p.Device, strings.ToLower(p.Result))
		if p.Message != "" {
			fmt.Fprintf(&b, ": %s", firstLine(p.Message))
		}
		b.WriteString("\n")
	}
	if r.URL != "" {
		fmt.Fprintf(&b, "The run on the provider's console: %s\n", r.URL)
	}
	return b.String()
}

// TestRunComment is the operator's comment on a Task's pull request for a
// finished test run, where the reviewer and people see it.
func TestRunComment(r TestRunReport) string {
	return "The operator ran a test package of this change on test environment `" + r.Environment +
		"`, which a run of it asked for:\n\n" + TestRunSummary(r)
}

// TestRunExitCode is `theos-env test`'s exit code for a report: 0 when every
// test passed, 1 when one didn't or the run errored.
func TestRunExitCode(r TestRunReport) int {
	if TestRunPassedResult(r.Result) {
		return 0
	}
	return 1
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
