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
	"errors"
	"sync"
	"time"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// TestRuns is a deviceFarm provider in memory: TestEnvironments' leases,
// Ready at once at their branch (model.TestRunEndpoint), and test runs that
// run until the test finishes them with Finish. It records each request,
// so a test can read the app and the test package a run took.
type TestRuns struct {
	TestEnvironments

	runsMu sync.Mutex
	runs   map[envKey]map[string]*fakeRun
}

var (
	_ ports.TestEnvironments = (*TestRuns)(nil)
	_ ports.TestRuns         = (*TestRuns)(nil)
)

type fakeRun struct {
	req    ports.TestRunRequest
	run    ports.TestRun
	report ports.TestRunReport
}

// Lease takes a lease, Ready at its branch.
func (f *TestRuns) Lease(ctx context.Context, req ports.LeaseRequest) (ports.Lease, error) {
	l, err := f.TestEnvironments.Lease(ctx, req)
	if err != nil || l.State != model.LeaseReady {
		return l, err
	}
	f.set(req.Env, req.ID, func(l *ports.Lease) { l.Endpoint = model.TestRunEndpoint(req.ID) })
	l.Endpoint = model.TestRunEndpoint(req.ID)
	return l, nil
}

// StartTestRun starts req's run on a lease held, Running at once on two
// devices, or returns the one started for its ID.
func (f *TestRuns) StartTestRun(_ context.Context, env ports.Environment, req ports.TestRunRequest) (ports.TestRun, error) {
	if _, ok := f.Request(env, req.Lease); !ok {
		return ports.TestRun{}, errors.New("no lease " + req.Lease)
	}
	f.runsMu.Lock()
	defer f.runsMu.Unlock()
	if f.runs == nil {
		f.runs = map[envKey]map[string]*fakeRun{}
	}
	k := keyOf(env)
	if f.runs[k] == nil {
		f.runs[k] = map[string]*fakeRun{}
	}
	if r, ok := f.runs[k][req.ID]; ok {
		return r.run, nil
	}
	r := &fakeRun{req: req, run: ports.TestRun{ID: req.ID, State: model.TestRunRunning, Devices: 2,
		URL: "https://devicefarm.example/runs/" + req.ID, Started: time.Now().Truncate(time.Second)}}
	f.runs[k][req.ID] = r
	return r.run, nil
}

// GetTestRun reads a run back.
func (f *TestRuns) GetTestRun(_ context.Context, env ports.Environment, id string) (ports.TestRun, error) {
	f.runsMu.Lock()
	defer f.runsMu.Unlock()
	if r, ok := f.runs[keyOf(env)][id]; ok {
		return r.run, nil
	}
	return ports.TestRun{}, ports.ErrTestRunNotFound
}

// TestRunReport is the report Finish gave a run.
func (f *TestRuns) TestRunReport(_ context.Context, env ports.Environment, id string) (ports.TestRunReport, error) {
	f.runsMu.Lock()
	defer f.runsMu.Unlock()
	r, ok := f.runs[keyOf(env)][id]
	switch {
	case !ok:
		return ports.TestRunReport{}, ports.ErrTestRunNotFound
	case r.run.State != model.TestRunDone:
		return ports.TestRunReport{}, errors.New("the run isn't done")
	}
	return r.report, nil
}

// Finish ends run id of env with report, at its result, counts and cost.
func (f *TestRuns) Finish(env ports.Environment, id string, report ports.TestRunReport) {
	f.runsMu.Lock()
	defer f.runsMu.Unlock()
	if r, ok := f.runs[keyOf(env)][id]; ok {
		r.run.State, r.run.Result, r.run.Message = model.TestRunDone, report.Report.Result, report.Report.Message
		r.run.Counters, r.run.CostMilliUSD = report.Report.Counters, report.Report.CostMilliUSD
		r.run.Devices = len(report.Report.Devices)
		r.report = report
	}
}

// Started is the request run id of env was started with, and whether it
// was.
func (f *TestRuns) Started(env ports.Environment, id string) (ports.TestRunRequest, bool) {
	f.runsMu.Lock()
	defer f.runsMu.Unlock()
	r, ok := f.runs[keyOf(env)][id]
	if !ok {
		return ports.TestRunRequest{}, false
	}
	return r.req, true
}

// Runs counts the runs started for env.
func (f *TestRuns) Runs(env ports.Environment) int {
	f.runsMu.Lock()
	defer f.runsMu.Unlock()
	return len(f.runs[keyOf(env)])
}
