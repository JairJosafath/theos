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

package awsenv

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/devicefarm"
	dftypes "github.com/aws/aws-sdk-go-v2/service/devicefarm/types"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
)

// farmWorld is a fake AWS with Device Farm, a deviceFarm provider on it and
// the test's clock.
type farmWorld struct {
	aws   *fakeAWS
	farm  *fakeFarm
	p     *DeviceFarm
	clock *clock
	envs  int
}

func newFarmWorld(t *testing.T) *farmWorld {
	c := &clock{t: time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)}
	f := newFakeAWS(c.now)
	d := newFakeFarm(f)
	t.Cleanup(d.server.Close)
	p := NewDeviceFarm(d.clients)
	p.now = c.now
	return &farmWorld{aws: f, farm: d, p: p, clock: c}
}

// devices is a new deviceFarm environment with a project and a pool of its
// own in account, and a key of its own.
func (w *farmWorld) devices() ports.Environment {
	w.envs++
	key := rand.Text()
	w.aws.account(key, account, model.DeviceFarmRegion, subnet, gpuType, gpuPrice)
	project, pool := w.farm.project(account)
	return ports.Environment{Namespace: "apps", Name: "devices", UID: fmt.Sprintf("5d1e7c2a-%04d", w.envs),
		Member:     model.TestEnvMemberDeviceFarm,
		DeviceFarm: &ports.DeviceFarmEnvironment{ProjectARN: project, DevicePoolARN: pool, Account: account},
		Secret:     map[string][]byte{"accessKeyId": []byte(key), "secretAccessKey": []byte(rand.Text())}}
}

func (w *farmWorld) lease(t *testing.T, req ports.LeaseRequest) ports.Lease {
	t.Helper()
	l, err := w.p.Lease(context.Background(), req)
	if err != nil {
		t.Fatalf("Lease = %v", err)
	}
	return l
}

// The contract every provider meets (T6), on a fake Device Farm: a lease is
// one tag on the project however often it is asked for, Ready at once at
// its branch, Failed once its device pool is gone; Return takes the tag
// off, and Leases lists the environment's.
func TestDeviceFarmContract(t *testing.T) {
	w := newFarmWorld(t)
	contract.TestEnvironments(t, contract.TestEnvironmentsEnv{
		Provider:    w.p,
		Environment: func(*testing.T) ports.Environment { return w.devices() },
		Places: func(_ *testing.T, env ports.Environment) int {
			return w.farm.leaseTags(env.DeviceFarm.ProjectARN, env.UID)
		},
		Fail: func(_ *testing.T, env ports.Environment, _ string) { w.farm.dropPool(env.DeviceFarm.DevicePoolARN) },
	})
}

// A lease is Ready at once at its branch, where its run asks for test runs
// (T12), and passes its deadline.
func TestDeviceFarmLeaseIsItsBranch(t *testing.T) {
	w := newFarmWorld(t)
	env := w.devices()
	req := request(env, "add-share-sheet-r1-implement")
	l := w.lease(t, req)
	if l.State != model.LeaseReady || l.Endpoint != "git:theos-env/"+req.ID || l.CA != "" {
		t.Errorf("Lease = %+v, want Ready at its branch, with no certificate", l)
	}
	branch, ok := model.TestRunBranchOf(l.Endpoint)
	if !ok || branch != model.TestRunBranch(req.ID) {
		t.Errorf("the endpoint names branch %q", branch)
	}
	w.clock.add(45 * time.Minute)
	if l, err := w.p.Get(context.Background(), env, req.ID); err != nil || l.State != model.LeaseFailed ||
		!strings.Contains(l.Message, "deadline") {
		t.Errorf("Get past the deadline = %+v, %v; want Failed", l, err)
	}
}

// testRunRequest is a request to run an Android app's instrumentation
// tests under lease.
func testRunRequest(lease string) ports.TestRunRequest {
	return ports.TestRunRequest{ID: model.TestRunID(lease, "0a1b2c3d4e5f60718293a4b5c6d7e8f901234567"), Lease: lease,
		Type: "instrumentation", App: ports.TestRunFile{Name: "app-debug.apk", Data: []byte("the app")},
		Tests:   ports.TestRunFile{Name: "app-debug-androidTest.apk", Data: []byte("its tests")},
		Timeout: 45 * time.Minute}
}

// A test run's app and test package go up to the addresses Device Farm
// gives, once each, and the run is scheduled on the pool once Device Farm
// processed both; it ends with its devices, the tests that didn't pass and
// its screenshots and logs, its video left on the console, and costs its
// metered minutes (T12, step 10).
func TestDeviceFarmRunsATestPackage(t *testing.T) {
	ctx := context.Background()
	w := newFarmWorld(t)
	env := w.devices()
	lease := request(env, "add-share-sheet-r1-implement")
	w.lease(t, lease)
	req := testRunRequest(lease.ID)

	run, err := w.p.StartTestRun(ctx, env, req)
	if err != nil || run.State != model.TestRunPending || !strings.Contains(run.Message, "processing") {
		t.Fatalf("StartTestRun = %+v, %v; want Pending while Device Farm processes the files", run, err)
	}
	for name, want := range map[string][]byte{req.ID + "-app-debug.apk": []byte("the app"),
		req.ID + "-app-debug-androidTest.apk": []byte("its tests")} {
		if got, ok := w.farm.uploaded(name); !ok || !bytes.Equal(got, want) {
			t.Errorf("upload %s holds %q, want %q", name, got, want)
		}
	}
	if _, err := w.p.GetTestRun(ctx, env, req.ID); !errors.Is(err, ports.ErrTestRunNotFound) {
		t.Errorf("GetTestRun before it is scheduled = %v, want ErrTestRunNotFound", err)
	}
	if run, err = w.p.StartTestRun(ctx, env, req); err != nil || run.State != model.TestRunPending {
		t.Fatalf("StartTestRun once processed = %+v, %v; want the run scheduled", run, err)
	}
	if run, err = w.p.StartTestRun(ctx, env, req); err != nil || len(w.farm.schedule) != 1 {
		t.Fatalf("StartTestRun again = %+v, %v; scheduled %d times, want once", run, err, len(w.farm.schedule))
	}
	checkScheduled(t, w.farm.schedule[0], req.ID, env.DeviceFarm.DevicePoolARN)
	w.farm.running(req.ID)
	if run, _ = w.p.GetTestRun(ctx, env, req.ID); run.State != model.TestRunRunning ||
		!strings.HasPrefix(run.URL, "https://us-west-2.console.aws.amazon.com/devicefarm/") {
		t.Errorf("GetTestRun = %+v, want Running, at the console", run)
	}

	finishShareSheetRun(w, req.ID)
	run, err = w.p.GetTestRun(ctx, env, req.ID)
	if err != nil || run.State != model.TestRunDone || run.Result != model.TestRunFailed || run.Counters.Failed != 1 ||
		run.CostMilliUSD != 2125 || run.Devices != 2 {
		t.Errorf("GetTestRun = %+v, %v; want Done, Failed, 1 failed, 12.5 metered minutes' 2125 on 2 devices", run, err)
	}
	report, err := w.p.TestRunReport(ctx, env, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	checkShareSheetReport(t, report)
	if len(w.farm.deleted) != 2 {
		t.Errorf("uploads deleted %v, want the run's two", w.farm.deleted)
	}
	leases, err := w.p.Leases(ctx, env)
	if err != nil || len(leases) != 1 || leases[0].CostMilliUSD != 2125 {
		t.Errorf("Leases = %+v, %v; want the lease at its run's 2125", leases, err)
	}
}

// Artifacts come back within the caps; the rest are listed, left out.
func TestDeviceFarmArtifactsAreCapped(t *testing.T) {
	ctx := context.Background()
	w := newFarmWorld(t)
	env := w.devices()
	lease := request(env, "add-share-sheet-r1-implement")
	w.lease(t, lease)
	req := testRunRequest(lease.ID)
	for range 3 {
		if _, err := w.p.StartTestRun(ctx, env, req); err != nil {
			t.Fatal(err)
		}
	}
	big := bytes.Repeat([]byte("x"), model.TestRunMaxArtifact+1)
	artifacts := map[string][]byte{"0/huge.log": big}
	types := map[string]dftypes.ArtifactType{"0/huge.log": dftypes.ArtifactTypeDeviceLog}
	for i := range 5 {
		name := fmt.Sprintf("0/screen-%d.png", i)
		artifacts[name], types[name] = bytes.Repeat([]byte("s"), model.TestRunMaxArtifact), dftypes.ArtifactTypeScreenshot
	}
	w.farm.finish(req.ID, dftypes.ExecutionResultPassed, dftypes.Counters{Total: aws.Int32(1), Passed: aws.Int32(1)}, 1,
		[]dftypes.Job{{Name: aws.String("Pixel"), Result: dftypes.ExecutionResultPassed}}, nil, artifacts, types)
	report, err := w.p.TestRunReport(ctx, env, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, a := range report.Artifacts {
		total += len(a.Data)
	}
	if total > model.TestRunMaxArtifacts || len(report.Artifacts) != 4 {
		t.Errorf("%d artifacts of %d bytes came back, want 4 within %d", len(report.Artifacts), total,
			model.TestRunMaxArtifacts)
	}
	left := 0
	for _, a := range report.Report.Artifacts {
		if a.LeftOut != "" {
			left++
		}
	}
	if left != 2 {
		t.Errorf("%d artifacts left out, want the one too big and the one past the total", left)
	}
}

// An app Device Farm can't read ends the run, Errored, with its message.
func TestDeviceFarmUploadRefused(t *testing.T) {
	ctx := context.Background()
	w := newFarmWorld(t)
	env := w.devices()
	lease := request(env, "add-share-sheet-r1-implement")
	w.lease(t, lease)
	req := testRunRequest(lease.ID)
	if _, err := w.p.StartTestRun(ctx, env, req); err != nil {
		t.Fatal(err)
	}
	w.farm.refuseUploads("The APK is invalid: no AndroidManifest.xml")
	run, err := w.p.StartTestRun(ctx, env, req)
	if err != nil || run.State != model.TestRunDone || run.Result != model.TestRunErrored ||
		!strings.Contains(run.Message, "no AndroidManifest.xml") {
		t.Errorf("StartTestRun = %+v, %v; want Done, Errored, with Device Farm's message", run, err)
	}
	if len(w.farm.schedule) != 0 {
		t.Error("a run was scheduled on a refused upload")
	}
}

// Return stops the runs its lease left going and takes the lease's tag
// off.
func TestDeviceFarmReturnStopsTheLeasesRuns(t *testing.T) {
	ctx := context.Background()
	w := newFarmWorld(t)
	env := w.devices()
	lease := request(env, "add-share-sheet-r1-implement")
	w.lease(t, lease)
	req := testRunRequest(lease.ID)
	for range 3 {
		if _, err := w.p.StartTestRun(ctx, env, req); err != nil {
			t.Fatal(err)
		}
	}
	w.farm.running(req.ID)
	if err := w.p.Return(ctx, env, lease.ID); err != nil {
		t.Fatal(err)
	}
	if len(w.farm.stops) != 1 || w.farm.stops[0] != req.ID {
		t.Errorf("runs stopped %v, want the lease's", w.farm.stops)
	}
	if n := w.farm.leaseTags(env.DeviceFarm.ProjectARN, env.UID); n != 0 {
		t.Errorf("lease tags %d after Return, want none", n)
	}
	if err := w.p.Return(ctx, env, lease.ID); err != nil {
		t.Errorf("a second Return = %v", err)
	}
}

// A credential of another account, or one AWS doesn't know, is refused
// before any lease; the environment's pool is read with it.
func TestDeviceFarmCredentials(t *testing.T) {
	ctx := context.Background()
	w := newFarmWorld(t)
	good := w.devices()
	if err := w.p.CheckCredentials(ctx, good); err != nil {
		t.Fatalf("CheckCredentials = %v", err)
	}
	other := w.devices()
	other.DeviceFarm.Account = "210987654321"
	if err := w.p.CheckCredentials(ctx, other); !errors.Is(err, ports.ErrCredentialRefused) ||
		!strings.Contains(err.Error(), "names account 210987654321") {
		t.Errorf("CheckCredentials of another account = %v, want refused", err)
	}
	unknown := w.devices()
	unknown.Secret["accessKeyId"] = []byte(rand.Text())
	if _, err := w.p.Lease(ctx, request(unknown, "add-share-sheet-r1-implement")); !errors.Is(err, ports.ErrCredentialRefused) {
		t.Errorf("Lease with an unknown key = %v, want refused", err)
	}
	gone := w.devices()
	w.farm.dropPool(gone.DeviceFarm.DevicePoolARN)
	if err := w.p.CheckCredentials(ctx, gone); err == nil || !strings.Contains(err.Error(), "is gone") {
		t.Errorf("CheckCredentials with the pool gone = %v, want it to say so", err)
	}
	if l := w.lease(t, request(gone, "add-share-sheet-r1-implement")); l.State != model.LeaseFailed {
		t.Errorf("Lease with the pool gone = %+v, want Failed", l)
	}
}

// Device Farm answers in us-west-2 alone: the SDK's clients ask it there.
func TestAWSDeviceFarmClientsAreInUSWest2(t *testing.T) {
	cfg, err := config(ports.Environment{AWS: &ports.AWSEnvironment{Region: model.DeviceFarmRegion},
		Member: model.TestEnvMemberDeviceFarm, Secret: map[string][]byte{"accessKeyId": []byte("k"),
			"secretAccessKey": []byte("s")}})
	if err != nil {
		t.Fatal(err)
	}
	c := AWSDeviceFarmClients(cfg)
	if got := c.DeviceFarm.(*devicefarm.Client).Options().Region; got != "us-west-2" {
		t.Errorf("Device Farm's region %q, want us-west-2", got)
	}
	if c.HTTP == nil || c.HTTP.Timeout == 0 {
		t.Error("the uploads go up with no client, or one that never times out")
	}
}

// finishShareSheetRun ends run id failed on a Pixel and passed on a
// Galaxy, after 12.5 metered minutes, with a screenshot and a log of the
// Pixel's and a video of the Galaxy's.
func finishShareSheetRun(w *farmWorld, id string) {
	w.farm.finish(id, dftypes.ExecutionResultFailed,
		dftypes.Counters{Total: aws.Int32(13), Passed: aws.Int32(12), Failed: aws.Int32(1)}, 12.5,
		[]dftypes.Job{
			{Name: aws.String("Google Pixel 8"), Device: &dftypes.Device{Name: aws.String("Google Pixel 8"), Os: aws.String("14")},
				Result: dftypes.ExecutionResultFailed, Counters: &dftypes.Counters{Total: aws.Int32(7), Passed: aws.Int32(6),
					Failed: aws.Int32(1)}},
			{Name: aws.String("Samsung Galaxy S24"), Device: &dftypes.Device{Name: aws.String("Samsung Galaxy S24"),
				Os: aws.String("14")}, Result: dftypes.ExecutionResultPassed,
				Counters: &dftypes.Counters{Total: aws.Int32(6), Passed: aws.Int32(6)}},
		},
		map[string][]dftypes.UniqueProblem{"FAILED": {{Message: aws.String("expected: <3> but was: <2>"),
			Problems: []dftypes.Problem{{Device: &dftypes.Device{Name: aws.String("Google Pixel 8")},
				Suite: &dftypes.ProblemDetail{Name: aws.String("ShareSheetTest")},
				Test:  &dftypes.ProblemDetail{Name: aws.String("sharesTheCart")}, Result: dftypes.ExecutionResultFailed}}}},
			"PASSED": {{Problems: []dftypes.Problem{{Result: dftypes.ExecutionResultPassed}}}}},
		map[string][]byte{"0/share sheet.png": []byte("a screenshot"), "0/logcat.txt": []byte("a log"),
			"1/video.mp4": []byte("a video")},
		map[string]dftypes.ArtifactType{"0/share sheet.png": dftypes.ArtifactTypeScreenshot,
			"0/logcat.txt": dftypes.ArtifactTypeDeviceLog, "1/video.mp4": dftypes.ArtifactTypeVideo})
}

// checkShareSheetReport checks finishShareSheetRun's report: the devices,
// the failed test alone, the Pixel's screenshot and log, the video left on
// the console.
func checkShareSheetReport(t *testing.T, report ports.TestRunReport) {
	t.Helper()
	r := report.Report
	if r.Result != model.TestRunFailed || len(r.Devices) != 2 || r.Devices[0].Name != "Google Pixel 8" ||
		r.Devices[0].Counters.Failed != 1 || r.Devices[1].Result != model.TestRunPassed {
		t.Errorf("devices %+v, want the Pixel failed and the Galaxy passed", r.Devices)
	}
	if len(r.Problems) != 1 || r.Problems[0] != (model.TestRunProblem{Device: "Google Pixel 8", Suite: "ShareSheetTest",
		Test: "sharesTheCart", Result: model.TestRunFailed, Message: "expected: <3> but was: <2>"}) {
		t.Errorf("problems %+v, want the failed test alone", r.Problems)
	}
	files := map[string]string{}
	for _, a := range report.Artifacts {
		files[a.Path] = string(a.Data)
	}
	if files["Google-Pixel-8/share-sheet.png"] != "a screenshot" || files["Google-Pixel-8/logcat.txt"] != "a log" ||
		len(files) != 2 {
		t.Errorf("artifacts %v, want the Pixel's screenshot and log", files)
	}
	var video *model.TestRunArtifact
	for i := range r.Artifacts {
		if r.Artifacts[i].Type == string(dftypes.ArtifactTypeVideo) {
			video = &r.Artifacts[i]
		}
	}
	if video == nil || video.LeftOut == "" || video.Device != "Samsung Galaxy S24" {
		t.Errorf("the video is listed as %+v, want left out on the console", video)
	}
}

// checkScheduled checks a run's ScheduleRun: named id, on pool, of its two
// uploads, 45 minutes a device.
func checkScheduled(t *testing.T, in devicefarm.ScheduleRunInput, id, pool string) {
	t.Helper()
	if aws.ToString(in.Name) != id || aws.ToString(in.DevicePoolArn) != pool ||
		in.Test.Type != dftypes.TestTypeInstrumentation || aws.ToInt32(in.ExecutionConfiguration.JobTimeoutMinutes) != 45 ||
		!strings.Contains(aws.ToString(in.AppArn), ":upload:") ||
		!strings.Contains(aws.ToString(in.Test.TestPackageArn), ":upload:") {
		t.Errorf("ScheduleRun %+v, want the request's run on the pool, its uploads, 45 minutes a device", in)
	}
}
