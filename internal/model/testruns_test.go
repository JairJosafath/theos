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

package model

import (
	"strings"
	"testing"
)

func TestTestRunBranch(t *testing.T) {
	id := LeaseID("devices", "add-share-sheet-r1-implement")
	if got := TestRunEndpoint(id); got != "git:theos-env/"+id {
		t.Errorf("TestRunEndpoint = %q", got)
	}
	if branch, ok := TestRunBranchOf(TestRunEndpoint(id)); !ok || branch != TestRunBranch(id) {
		t.Errorf("TestRunBranchOf = %q, %v; want the lease's branch", branch, ok)
	}
	for _, endpoint := range []string{"http://browser-0123456789.apps:7070", "https://203.0.113.7:443", "git:main",
		"git:theos-env/", ""} {
		if _, ok := TestRunBranchOf(endpoint); ok {
			t.Errorf("TestRunBranchOf(%q) names a test-run branch", endpoint)
		}
	}
	if got := TestRunID(id, "0a1b2c3d4e5f60718293a4b5c6d7e8f901234567"); got != id+"-0a1b2c3d4e5f" {
		t.Errorf("TestRunID = %q", got)
	}
}

// Only the run that holds a lease asks for its test runs: the operator runs a
// request whose MAC the lease's token makes over everything it asks for,
// and whose files are the ones it names (T12, T5).
func TestCheckTestRun(t *testing.T) {
	const token, lease, commit = "K4GK5QSQHY34QRMZKSABRKPR27", "devices-0123456789", "0a1b2c3d4e5f60718293a4b5c6d7e8f901234567"
	app, tests := []byte("an app"), []byte("its tests")
	good := NewTestRunRequest(token, lease, commit, "instrumentation", "app-debug.apk", app, "app-debug-androidTest.apk", tests)
	if err := CheckTestRun(token, lease, good, app, tests); err != nil {
		t.Fatalf("CheckTestRun of a signed request = %v", err)
	}
	for _, tt := range []struct {
		name   string
		mutate func(r *TestRunRequest, app, tests *[]byte, token *string)
		says   string
	}{
		{"another lease's token", func(_ *TestRunRequest, _, _ *[]byte, tok *string) { *tok = "ANOTHERTOKEN" }, "isn't signed"},
		{"no token", func(_ *TestRunRequest, _, _ *[]byte, tok *string) { *tok = "" }, "isn't signed"},
		{"another type than it signed", func(r *TestRunRequest, _, _ *[]byte, _ *string) { r.Type = "xctest" }, ".ipa file"},
		{"another commit than it signed", func(r *TestRunRequest, _, _ *[]byte, _ *string) {
			r.Commit = "ffffffffffff60718293a4b5c6d7e8f901234567"
		}, "isn't signed"},
		{"another app than it names", func(_ *TestRunRequest, a, _ *[]byte, _ *string) { *a = []byte("another app") },
			"isn't the one the request names"},
		{"another lease's branch", func(r *TestRunRequest, _, _ *[]byte, _ *string) { r.Lease = "devices-9876543210" },
			"is on the branch of lease"},
		{"a format it doesn't read", func(r *TestRunRequest, _, _ *[]byte, _ *string) { r.Format = 2 }, "format"},
		{"no commit", func(r *TestRunRequest, _, _ *[]byte, _ *string) { r.Commit = "HEAD" }, "is no commit"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, a, b, tok := good, app, tests, token
			tt.mutate(&r, &a, &b, &tok)
			if err := CheckTestRun(tok, lease, r, a, b); err == nil || !strings.Contains(err.Error(), tt.says) {
				t.Errorf("CheckTestRun = %v, want it refused, saying %q", err, tt.says)
			}
		})
	}
}

func TestCheckTestRunFiles(t *testing.T) {
	if err := CheckTestRunFiles("xctest", "Shop.ipa", 10, "ShopTests.zip", 10); err != nil {
		t.Errorf("an XCTest run = %v", err)
	}
	if err := CheckTestRunFiles("xctest-ui", "Shop.IPA", 10, "ShopUITests-Runner.ipa", 10); err != nil {
		t.Errorf("an XCUITest run = %v", err)
	}
	for _, tt := range []struct {
		typ, app, tests string
		size            int
		says            string
	}{
		{"espresso", "app.apk", "tests.apk", 1, "is none of instrumentation, xctest and xctest-ui"},
		{"instrumentation", "app.ipa", "tests.apk", 1, "is a .apk file"},
		{"instrumentation", "../app.apk", "tests.apk", 1, "no plain file name"},
		{"instrumentation", ".apk", "tests.apk", 1, "no plain file name"},
		{"instrumentation", "app.apk", "", 1, "no plain file name"},
		{"instrumentation", "app.apk", "tests.apk", TestRunMaxUpload, "more than"},
	} {
		if err := CheckTestRunFiles(tt.typ, tt.app, tt.size, tt.tests, 1); err == nil || !strings.Contains(err.Error(), tt.says) {
			t.Errorf("CheckTestRunFiles(%q, %q, %q) = %v, want %q", tt.typ, tt.app, tt.tests, err, tt.says)
		}
	}
}

func TestDeviceFarmCostMilliUSD(t *testing.T) {
	for minutes, want := range map[float64]int64{0: 0, -1: 0, 1: 170, 12.5: 2125, 0.001: 1} {
		if got := DeviceFarmCostMilliUSD(minutes); got != want {
			t.Errorf("DeviceFarmCostMilliUSD(%v) = %d, want %d", minutes, got, want)
		}
	}
}

func TestTestRunResultOf(t *testing.T) {
	for word, want := range map[string]string{"PASSED": TestRunPassed, "FAILED": TestRunFailed, "WARNED": TestRunWarned,
		"ERRORED": TestRunErrored, "STOPPED": TestRunStopped, "SKIPPED": TestRunSkipped, "PENDING": "", "": "",
		"SOMETHING_NEW": TestRunErrored} {
		if got := TestRunResultOf(word); got != want {
			t.Errorf("TestRunResultOf(%q) = %q, want %q", word, got, want)
		}
	}
}

// A report reads the same where the agent and the reviewer see it: the
// outcome, each device and each test that didn't pass.
func TestTestRunSummary(t *testing.T) {
	r := TestRunReport{Environment: "devices", Commit: "0a1b2c3d4e5f60718293", Type: "instrumentation",
		Result: TestRunFailed, URL: "https://us-west-2.console.aws.amazon.com/devicefarm/home#/mobile/projects/p/runs/r",
		Counters: TestRunCounters{Total: 13, Passed: 12, Failed: 1},
		Devices: []TestRunDevice{{Name: "Google Pixel 8", OS: "14", Result: TestRunFailed,
			Counters: TestRunCounters{Total: 7, Passed: 6, Failed: 1}},
			{Name: "Samsung Galaxy S24", OS: "14", Result: TestRunPassed, Counters: TestRunCounters{Total: 6, Passed: 6}}},
		Problems: []TestRunProblem{{Device: "Google Pixel 8", Suite: "ShareSheetTest", Test: "sharesTheCart",
			Result: TestRunFailed, Message: "expected: <3> but was: <2>\n\tat ShareSheetTest.java:41"}}}
	got := TestRunSummary(r)
	for _, want := range []string{
		"Test run instrumentation of 0a1b2c3d4e5f on devices: failed: 12 passed, 1 failed of 13 tests on 2 devices.\n",
		"- Google Pixel 8 (14): failed, 6 of 7 passed\n", "- Samsung Galaxy S24 (14): passed, 6 of 6 passed\n",
		"- ShareSheetTest.sharesTheCart on Google Pixel 8: failed: expected: <3> but was: <2>\n",
		"console: https://us-west-2.console.aws.amazon.com/",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lacks %q:\n%s", want, got)
		}
	}
	if !strings.Contains(TestRunComment(r), "test environment `devices`") {
		t.Errorf("comment %q names no environment", TestRunComment(r))
	}
	if TestRunExitCode(r) != 1 {
		t.Error("a failed run exits 0")
	}
	r.Result = TestRunWarned
	if TestRunExitCode(r) != 0 {
		t.Error("a run that passed with warnings doesn't exit 0")
	}
	refused := TestRunReport{Environment: "devices", Commit: "0a1b2c3", Result: TestRunRefused,
		Message: "the request isn't signed with the lease's token"}
	if got := TestRunOutcome(refused); got != "rejected: the request isn't signed with the lease's token" {
		t.Errorf("TestRunOutcome of a refused request = %q", got)
	}
	if TestRunExitCode(refused) != 1 {
		t.Error("a refused request exits 0")
	}
}

func TestTestEnvRemoteMembers(t *testing.T) {
	for member, want := range map[string]bool{TestEnvMemberAWS: true, TestEnvMemberDeviceFarm: true,
		TestEnvMemberLocal: false} {
		if TestEnvRemote(member) != want {
			t.Errorf("TestEnvRemote(%q) = %v", member, !want)
		}
	}
	if got := TestEnvMissingKeys(TestEnvMemberDeviceFarm, map[string][]byte{"accessKeyId": []byte("k")}); len(got) != 1 ||
		got[0] != "secretAccessKey" {
		t.Errorf("TestEnvMissingKeys = %v, want secretAccessKey", got)
	}
}
