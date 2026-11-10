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
	"reflect"
	"testing"
	"time"
)

// family finds name among the families FactoryMetrics returned.
func family(t *testing.T, families []MetricFamily, name string) MetricFamily {
	t.Helper()
	for _, f := range families {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no family %s", name)
	return MetricFamily{}
}

// TestFactoryMetrics covers #59: the factory's numbers as metric families,
// computed from the Tasks that exist.
func TestFactoryMetrics(t *testing.T) {
	t0 := time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return t0.Add(time.Duration(minutes) * time.Minute) }
	zero, two := 0, 2
	cost := func(milli int64) *int64 { return &milli }

	merged := TaskMetricFacts{Namespace: "webshop", Container: "orders-api", Kind: "Implement", Phase: TaskMerged, Round: 2,
		PersonCommits: &zero, CostMilliUSD: 1500,
		History: []TaskMetricStep{{At: at(0), Phase: TaskPending}, {At: at(1), Phase: TaskImplementing},
			{At: at(11), Phase: TaskChecking}, {At: at(31), Phase: TaskReviewing}, {At: at(36), Phase: TaskMerged}},
		Runs: []TaskMetricRun{
			{Agent: "implementer", Harness: "claude-code", Model: "claude-sonnet-5-5", Role: RoleImplementer,
				CostMilliUSD: cost(1000), Input: 100, Output: 20, CacheRead: 5000, CacheWrite: 300},
			{Agent: "reviewer", Harness: "claude-code", Model: "claude-opus-5-5", Role: RoleReviewer, CostMilliUSD: cost(500), Input: 50},
		}}
	byPerson := TaskMetricFacts{Namespace: "webshop", Container: "orders-api", Kind: "Implement", Phase: TaskMerged, Round: 1,
		PersonCommits: &two, CostMilliUSD: 250,
		History: []TaskMetricStep{{At: at(0), Phase: TaskImplementing}, {At: at(120), Phase: TaskMerged}}}
	unread := TaskMetricFacts{Namespace: "webshop", Container: "storefront", Kind: "Implement", Phase: TaskMerged, Round: 1}
	failed := TaskMetricFacts{Namespace: "webshop", Container: "storefront", Kind: "Implement", Phase: TaskFailed, Round: 5,
		History: []TaskMetricStep{{At: at(0), Phase: TaskImplementing}, {At: at(600), Phase: TaskFailed}},
		Runs:    []TaskMetricRun{{Role: RoleImplementer, Input: 7}}}
	running := TaskMetricFacts{Namespace: "webshop", Container: "storefront", Kind: "Implement", Phase: TaskReviewing, Round: 1,
		History: []TaskMetricStep{{At: at(0), Phase: TaskImplementing}, {At: at(4), Phase: TaskReviewing}}}
	waiting := TaskMetricFacts{Namespace: "webshop", Container: "storefront", Kind: "Spec", Phase: TaskAwaitingApproval}
	benchmark := TaskMetricFacts{Namespace: "lab", Kind: "Implement", Benchmark: true, Phase: "Approved", Round: 1,
		History: []TaskMetricStep{{At: at(0), Phase: TaskImplementing}, {At: at(30), Phase: "Approved"}}}
	fresh := TaskMetricFacts{Namespace: "lab", Container: "cli", Kind: "Triage"}

	got := FactoryMetrics([]TaskMetricFacts{merged, byPerson, unread, failed, running, waiting, benchmark, fresh})

	names := make([]string, 0, len(got))
	for _, f := range got {
		names = append(names, f.Name)
	}
	wantNames := []string{MetricTasks, MetricTasksMerged, MetricTasksCostUSD, MetricTaskSeconds, MetricTaskPhaseSeconds,
		MetricTaskRounds, MetricAgentRuns, MetricAgentRunsCostUSD, MetricAgentRunsWithoutCost, MetricAgentRunsTokens}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("families:\n got %v\nwant %v", names, wantNames)
	}

	samples := []struct {
		name   string
		family string
		want   []MetricSample
	}{
		{name: "Tasks by phase; a Benchmark's Task is its own kind, a Task with no phase is Pending", family: MetricTasks,
			want: []MetricSample{
				{Values: []string{"lab", "cli", "Triage", TaskPending}, Value: 1},
				{Values: []string{"lab", MetricNone, KindBenchmark, "Approved"}, Value: 1},
				{Values: []string{"webshop", "orders-api", "Implement", TaskMerged}, Value: 2},
				{Values: []string{"webshop", "storefront", "Implement", TaskFailed}, Value: 1},
				{Values: []string{"webshop", "storefront", "Implement", TaskMerged}, Value: 1},
				{Values: []string{"webshop", "storefront", "Implement", TaskReviewing}, Value: 1},
				{Values: []string{"webshop", "storefront", "Spec", TaskAwaitingApproval}, Value: 1},
			}},
		{name: "merged Tasks by who wrote the code: no person's commit, a person's, not read", family: MetricTasksMerged,
			want: []MetricSample{
				{Values: []string{"webshop", "orders-api", "Implement", CodeFactory}, Value: 1},
				{Values: []string{"webshop", "orders-api", "Implement", CodePerson}, Value: 1},
				{Values: []string{"webshop", "storefront", "Implement", CodeUnknown}, Value: 1},
			}},
		{name: "cost in dollars, summed per Container", family: MetricTasksCostUSD,
			want: []MetricSample{{Values: []string{"webshop", "orders-api", "Implement"}, Value: 1.75}}},
		{name: "runs by Agent, harness, model and role; a run with none recorded says none", family: MetricAgentRuns,
			want: []MetricSample{
				{Values: []string{"webshop", "implementer", "claude-code", "claude-sonnet-5-5", RoleImplementer}, Value: 1},
				{Values: []string{"webshop", MetricNone, MetricNone, MetricNone, RoleImplementer}, Value: 1},
				{Values: []string{"webshop", "reviewer", "claude-code", "claude-opus-5-5", RoleReviewer}, Value: 1},
			}},
		{name: "what the runs cost", family: MetricAgentRunsCostUSD,
			want: []MetricSample{
				{Values: []string{"webshop", "implementer", "claude-code", "claude-sonnet-5-5", RoleImplementer}, Value: 1},
				{Values: []string{"webshop", "reviewer", "claude-code", "claude-opus-5-5", RoleReviewer}, Value: 0.5},
			}},
		{name: "a run with no cost is counted apart, not as free", family: MetricAgentRunsWithoutCost,
			want: []MetricSample{{Values: []string{"webshop", MetricNone, MetricNone, MetricNone, RoleImplementer}, Value: 1}}},
		{name: "tokens by type, zero ones left out", family: MetricAgentRunsTokens,
			want: []MetricSample{
				{Values: []string{"webshop", "implementer", "claude-code", "claude-sonnet-5-5", RoleImplementer, "cache_read"}, Value: 5000},
				{Values: []string{"webshop", "implementer", "claude-code", "claude-sonnet-5-5", RoleImplementer, "cache_write"}, Value: 300},
				{Values: []string{"webshop", "implementer", "claude-code", "claude-sonnet-5-5", RoleImplementer, "input"}, Value: 100},
				{Values: []string{"webshop", "implementer", "claude-code", "claude-sonnet-5-5", RoleImplementer, "output"}, Value: 20},
				{Values: []string{"webshop", MetricNone, MetricNone, MetricNone, RoleImplementer, "input"}, Value: 7},
				{Values: []string{"webshop", "reviewer", "claude-code", "claude-opus-5-5", RoleReviewer, "input"}, Value: 50},
			}},
	}
	for _, tt := range samples {
		t.Run(tt.name, func(t *testing.T) {
			f := family(t, got, tt.family)
			if !reflect.DeepEqual(f.Samples, tt.want) {
				t.Errorf("%s:\n got %v\nwant %v", tt.family, f.Samples, tt.want)
			}
			if len(f.Samples) > 0 && len(f.Samples[0].Values) != len(f.Labels) {
				t.Errorf("%s: %d values for labels %v", tt.family, len(f.Samples[0].Values), f.Labels)
			}
		})
	}

	// hist finds the histogram with these label values.
	hist := func(t *testing.T, name string, values ...string) MetricHistogram {
		t.Helper()
		f := family(t, got, name)
		for _, h := range f.Histograms {
			if reflect.DeepEqual(h.Values, values) {
				if len(h.Counts) != len(f.Buckets) {
					t.Fatalf("%s: %d counts for %d buckets", name, len(h.Counts), len(f.Buckets))
				}
				return h
			}
		}
		t.Fatalf("%s has no histogram %v: %v", name, values, f.Histograms)
		return MetricHistogram{}
	}
	histograms := []struct {
		name      string
		got       MetricHistogram
		wantCount uint64
		wantSum   float64
		// wantUnder is the count at or under the bucket bound.
		bound     float64
		wantUnder uint64
	}{
		{name: "finished Tasks' whole time: 36 minutes and 2 hours",
			got:       hist(t, MetricTaskSeconds, "webshop", "Implement", TaskMerged),
			wantCount: 2, wantSum: 36*60 + 120*60, bound: 3600, wantUnder: 1},
		{name: "a failed Task's time is its own outcome",
			got:       hist(t, MetricTaskSeconds, "webshop", "Implement", TaskFailed),
			wantCount: 1, wantSum: 600 * 60, bound: 28800, wantUnder: 0},
		{name: "a Benchmark's Task ends Approved",
			got:       hist(t, MetricTaskSeconds, "lab", KindBenchmark, "Approved"),
			wantCount: 1, wantSum: 30 * 60, bound: 1800, wantUnder: 1},
		{name: "time implementing: every stay that ended, the running Task's too",
			got:       hist(t, MetricTaskPhaseSeconds, "webshop", "Implement", TaskImplementing),
			wantCount: 4, wantSum: (10 + 120 + 600 + 4) * 60, bound: 900, wantUnder: 2},
		{name: "time reviewing leaves out the stay that hasn't ended",
			got:       hist(t, MetricTaskPhaseSeconds, "webshop", "Implement", TaskReviewing),
			wantCount: 1, wantSum: 5 * 60, bound: 300, wantUnder: 1},
		{name: "rounds of merged Tasks: 2, 1 and 1",
			got:       hist(t, MetricTaskRounds, "webshop", "Implement", TaskMerged),
			wantCount: 3, wantSum: 4, bound: 1, wantUnder: 2},
		{name: "rounds of a failed Task",
			got:       hist(t, MetricTaskRounds, "webshop", "Implement", TaskFailed),
			wantCount: 1, wantSum: 5, bound: 4, wantUnder: 0},
	}
	for _, tt := range histograms {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got.Count != tt.wantCount || tt.got.Sum != tt.wantSum {
				t.Errorf("got count %d sum %v, want count %d sum %v", tt.got.Count, tt.got.Sum, tt.wantCount, tt.wantSum)
			}
			buckets := TaskSecondsBuckets
			if tt.bound < 60 {
				buckets = TaskRoundsBuckets
			}
			for i, b := range buckets {
				if b == tt.bound && tt.got.Counts[i] != tt.wantUnder {
					t.Errorf("at or under %v: got %d, want %d", b, tt.got.Counts[i], tt.wantUnder)
				}
			}
		})
	}

	t.Run("a running Task has no whole time and no rounds yet", func(t *testing.T) {
		for _, name := range []string{MetricTaskSeconds, MetricTaskRounds} {
			for _, h := range family(t, got, name).Histograms {
				if h.Values[2] == TaskReviewing || h.Values[2] == TaskAwaitingApproval || h.Values[2] == TaskPending {
					t.Errorf("%s has %v", name, h.Values)
				}
			}
		}
	})
	t.Run("the same Tasks in another order give the same families", func(t *testing.T) {
		again := FactoryMetrics([]TaskMetricFacts{fresh, benchmark, waiting, running, failed, unread, byPerson, merged})
		if !reflect.DeepEqual(got, again) {
			t.Error("the families depend on the Tasks' order")
		}
	})
	t.Run("no Tasks give every family, empty", func(t *testing.T) {
		for _, f := range FactoryMetrics(nil) {
			if len(f.Samples)+len(f.Histograms) != 0 {
				t.Errorf("%s has values with no Tasks", f.Name)
			}
		}
	})
}
