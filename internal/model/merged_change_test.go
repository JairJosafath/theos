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
)

// #204: one rule says what a merged change is, and the Prometheus series and
// theos-session report both read it. A Task that ended DoneByStandardChange is
// the Standard change's merge, which its record in status names, and not its
// own pull request, which an earlier round closed.
func TestMergedChange(t *testing.T) {
	for name, c := range map[string]struct {
		facts TaskMetricFacts
		want  bool
	}{
		"its own pull request merged": {
			TaskMetricFacts{Phase: TaskMerged, PullRequest: 7}, true},
		"merged in a later round, after a send-back": {
			TaskMetricFacts{Phase: TaskMerged, PullRequest: 9, Round: 3}, true},
		"done by its Standard change, whose record names the change (#204)": {
			TaskMetricFacts{Phase: TaskMerged, DoneByStandardChange: true, StandardChange: 41}, true},
		"done by its Standard change, whose only pull request an earlier round closed (#204)": {
			TaskMetricFacts{Phase: TaskMerged, PullRequest: 7, DoneByStandardChange: true, StandardChange: 41}, true},
		"done by its Standard change, with no record of it: nothing merged": {
			TaskMetricFacts{Phase: TaskMerged, PullRequest: 7, DoneByStandardChange: true}, false},
		"a Merged Task with no pull request, a Plan accepted (#204)": {
			TaskMetricFacts{Kind: "Plan", Phase: TaskMerged}, false},
		"a Merged Task with no pull request, a Triage (#204)": {
			TaskMetricFacts{Kind: "Triage", Phase: TaskMerged}, false},
		"a Merged Task with no pull request, a Score": {
			TaskMetricFacts{Kind: "Score", Phase: TaskMerged}, false},
		"a Standard change's record is no merge of the Task's own, which has none": {
			TaskMetricFacts{Phase: TaskMerged, StandardChange: 41}, false},
		"nothing to do, with the pull request its agent never opened": {
			TaskMetricFacts{Phase: TaskMerged, NoChange: true}, false},
		"nothing to do, whatever else it says": {
			TaskMetricFacts{Phase: TaskMerged, NoChange: true, PullRequest: 7, DoneByStandardChange: true, StandardChange: 41}, false},
		"a Benchmark's Task ends Approved and never merges": {
			TaskMetricFacts{Phase: TaskApproved, Benchmark: true, PullRequest: 7}, false},
		"failed, with a pull request": {
			TaskMetricFacts{Phase: TaskFailed, PullRequest: 7}, false},
		"not ended yet": {
			TaskMetricFacts{Phase: TaskReviewing, PullRequest: 7}, false},
		"no phase yet": {
			TaskMetricFacts{}, false},
	} {
		if got := MergedChange(c.facts); got != c.want {
			t.Errorf("%s: merged change = %v, want %v", name, got, c.want)
		}
	}
}

// #204: the series and the report count the same Tasks as merged changes
// because both call MergedChange; this holds that to the Tasks that disagreed
// before, a Task done by its Standard change and one whose only pull request
// an earlier round closed. The report lists them in the order they merged, then by name.
func TestSeriesAndReportCountTheSameMergedChanges(t *testing.T) {
	merged := func(name string, mutate func(*ReportTask)) ReportTask {
		task := mergedTask(10)
		task.Name = name
		mutate(&task)
		return task
	}
	tasks := []ReportTask{
		merged("its-own", func(*ReportTask) {}),
		merged("by-standard-change", func(t *ReportTask) {
			t.PullRequest, t.DoneByStandardChange, t.StandardChange = 0, true, 41
		}),
		merged("by-standard-change-after-a-closed-pull", func(t *ReportTask) {
			t.PullRequest, t.DoneByStandardChange, t.StandardChange = 7, true, 42
		}),
		merged("closed-and-no-record", func(t *ReportTask) { t.DoneByStandardChange = true }),
		merged("a-plan", func(t *ReportTask) { t.Kind, t.PullRequest = "Plan", 0 }),
		merged("nothing", func(t *ReportTask) { t.NoChange = true }),
	}
	facts := make([]TaskMetricFacts, 0, len(tasks))
	for _, task := range tasks {
		facts = append(facts, task.TaskMetricFacts)
	}
	counted := 0.0
	for _, s := range family(t, FactoryMetrics(facts), MetricTasksMerged).Samples {
		counted += s.Value
	}
	report := FactoryReport(period(), tasks)
	if counted != 3 || report.Merged != 3 {
		t.Errorf("the series count %v merged changes and the report %d, want 3 and 3", counted, report.Merged)
	}
	want := []ReportMerged{
		{Namespace: "shop", Task: "by-standard-change", StandardChange: 41},
		{Namespace: "shop", Task: "by-standard-change-after-a-closed-pull", StandardChange: 42},
		{Namespace: "shop", Task: "its-own", PullRequest: 1},
	}
	for i := range want {
		want[i].Completed = minutesAgo(10).UTC().Format("2006-01-02T15:04:05Z")
	}
	if !reflect.DeepEqual(report.MergedTasks, want) {
		t.Errorf("merged Tasks = %+v, want %+v: a Task done by its Standard change names that change, never its closed pull request",
			report.MergedTasks, want)
	}
}
