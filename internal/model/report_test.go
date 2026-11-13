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
	"encoding/json"
	"flag"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The report's fixture (#66): Tasks as the API serves them, the JSON report
// they give and the page. It lives in the UI's tree because the UI's test
// reads tasks.json and report.json too (ui/src/model/report.test.ts) and the
// UI's check runs only when a file under ui/ changed; under a testdata
// directory, so make test tests every Go package when one of them changes.
// A changed rule fails here until the files are written again:
//
//	go test ./internal/model -run TestFactoryReportFixture -update
const reportFixture = "../../ui/src/model/testdata/report"

var update = flag.Bool("update", false, "write the report fixture's report.json and report.md again")

var reportTo = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

// reportFixturePeriod is the fixture's period: the 30 days the UI's test asks
// its own rules for. It is called "report", so the fixture's directory holds
// it as a repository holds a release's: report.md and the report.json its
// page links, which the check of the Markdown's links finds there.
var reportFixturePeriod = ReportPeriod{Name: "report", From: reportTo.Add(-30 * 24 * time.Hour), To: reportTo}

// fixtureTask is a Task as the API serves it, as far as the report reads it.
type fixtureTask struct {
	Metadata struct{ Name, Namespace string }
	Spec     struct {
		Kind         string
		ContainerRef struct{ Name string }
		Benchmark    *struct{}
		Score        *struct {
			ScorerRef        struct{ Name string }
			ScorerGeneration int64
			Criteria         []string
			Config           struct{ Harness, Model string }
		}
	}
	Status struct {
		Phase           string
		Round           int
		PullRequest     int
		PullRequestSize *struct{ Additions, Deletions int }
		PersonCommits   *int
		CostMilliUSD    int64
		CompletedAt     time.Time
		Spec, Approval  *struct{ ApprovedBy, Require string }
		Review          *struct{}
		StandardChange  *struct{ Issue int }
		Score           struct{ Criteria []ScoreCriterion }
		Conditions      []struct{ Type, Reason string }
		History         []struct {
			At            time.Time
			Phase, Reason string
		}
		Runs []struct {
			Agent, Harness, Model, Role string
			CostMilliUSD                *int64
		}
	}
}

// reportTask reads a fixture Task as taskfacts.Metric reads the API's Task
// (internal/taskfacts, which this package can't import), and the report's
// own fields. The fixture's merged Tasks say how they ended done (#142), so
// reading that otherwise here gives another report.
func (f fixtureTask) reportTask() ReportTask {
	t := ReportTask{
		TaskMetricFacts: TaskMetricFacts{
			Namespace: f.Metadata.Namespace, Container: f.Spec.ContainerRef.Name, Kind: f.Spec.Kind,
			Benchmark: f.Spec.Benchmark != nil, Phase: f.Status.Phase, Round: f.Status.Round,
			PersonCommits: f.Status.PersonCommits, CostMilliUSD: f.Status.CostMilliUSD, PullRequest: f.Status.PullRequest,
		},
		Name: f.Metadata.Name, Completed: f.Status.CompletedAt,
	}
	if t.Kind == "" {
		t.Kind = chatKindImplement
	}
	spec, approval := f.Status.Spec, f.Status.Approval
	t.ApprovedByPerson = spec != nil && spec.ApprovedBy != "" || approval != nil && approval.ApprovedBy != ""
	t.Unreviewed = approval != nil && approval.Require == "None" && f.Status.Review == nil
	for _, c := range f.Status.Conditions {
		t.NoChange = t.NoChange || c.Type == "Succeeded" && c.Reason == ReasonNothingToDo && t.Phase == TaskMerged
		t.DoneByStandardChange = t.DoneByStandardChange ||
			c.Type == "Succeeded" && c.Reason == ReasonDoneByStandardChange && t.Phase == TaskMerged
	}
	if c := f.Status.StandardChange; c != nil {
		t.StandardChange = c.Issue
	}
	for _, s := range f.Status.History {
		t.History = append(t.History, TaskMetricStep{At: s.At, Phase: s.Phase, Reason: s.Reason})
	}
	for _, r := range f.Status.Runs {
		t.Runs = append(t.Runs, TaskMetricRun{Agent: r.Agent, Harness: r.Harness, Model: r.Model, Role: r.Role,
			CostMilliUSD: r.CostMilliUSD})
	}
	if s := f.Status.PullRequestSize; s != nil {
		t.Size = &ReportSize{Additions: s.Additions, Deletions: s.Deletions}
	}
	if s := f.Spec.Score; s != nil {
		t.Score = &ReportScore{Scorer: s.ScorerRef.Name, Generation: s.ScorerGeneration, Criteria: s.Criteria,
			Harness: s.Config.Harness, Model: s.Config.Model, Verdicts: f.Status.Score.Criteria}
	}
	return t
}

func readReportFixture(t *testing.T) []ReportTask {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(reportFixture, "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var list struct{ Items []fixtureTask }
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	tasks := make([]ReportTask, 0, len(list.Items))
	for _, item := range list.Items {
		tasks = append(tasks, item.reportTask())
	}
	return tasks
}

// The fixture's Tasks give its report and its page, byte for byte (#66).
func TestFactoryReportFixture(t *testing.T) {
	r := FactoryReport(reportFixturePeriod, readReportFixture(t))
	document, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.Markdown()
	if err != nil {
		t.Fatal(err)
	}
	pagePath, documentPath, err := ReportPaths(reportFixture, r.Name)
	if err != nil {
		t.Fatal(err)
	}
	for path, got := range map[string][]byte{documentPath: document, pagePath: []byte(page)} {
		name := filepath.Base(path)
		if *update {
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("%s is not what the fixture's Tasks give; if the rule changed on purpose, run this test with "+
				"-update and make ui-test, which holds the UI's rules to the same file.\n--- got ---\n%s", name, got)
		}
	}

	// What the fixture is there to show, so a fixture written again by
	// mistake doesn't pass for a report of nothing.
	if r.PersonApproved.Tasks != 2 {
		t.Errorf("approved by a person = %d, want 2: a held change and a spec", r.PersonApproved.Tasks)
	}
	if r.Merged != 14 || r.Failed != 4 || r.NoChange != 1 {
		t.Errorf("merged, failed, no change = %d, %d, %d, want 14, 4, 1", r.Merged, r.Failed, r.NoChange)
	}
	if got := r.NoPersonCommit; got.Tasks != 10 || got.Of != 12 {
		t.Errorf("no person's commit = %d of %d, want 10 of 12", got.Tasks, got.Of)
	}
	// The rules the fixture pins on both sides (docs/plans/results-report.md,
	// "What the fixture holds"): each needs its Task to still be there.
	merged := map[string]bool{}
	for _, m := range r.MergedTasks {
		merged[m.Task] = true
	}
	for _, name := range []string{"hotfix-22", "pricing-23", "pricing-24", "adopted-25", "catalog-17", "checkout-18"} {
		if !merged[name] {
			t.Errorf("the fixture's report has no merged Task %s", name)
		}
	}
	checkMergedByTheStandardChange(t, r)
	if merged["edge-26"] {
		t.Error("edge-26 finished on the period's first instant and is the report before's, yet it is counted")
	}
	if queued := r.StageTimes[0]; queued.Key != StageQueued || queued.Tasks != 12 {
		t.Errorf("queued = %+v, want 12 Tasks: hotfix-22 was queued for zero seconds, and counts", queued)
	}
	keys := make([]string, 0, len(r.ByConfig))
	for _, g := range r.ByConfig {
		keys = append(keys, g.Key)
	}
	if want := []string{"claude-code · sonnet", "claude-code · opus", "claude-code · Opus",
		"claude-code · default model", "claude-code · haiku", ReportNotRecorded}; !reflect.DeepEqual(keys, want) {
		t.Errorf("by harness and model = %q, want %q: catalog-17 under its newest run's opus, and the three "+
			"with one Task each by their code points", keys, want)
	}
	if got := r.Cost.Tasks; got != 12 {
		t.Errorf("%d merged Tasks reported a cost, want 12: checkout-18's cost of 0 is none", got)
	}
	for _, lead := range r.LeadTimes {
		if lead.Tasks != 14 || *lead.P90Seconds == slowest(t, lead.Key) {
			t.Errorf("%s: over %d Tasks, 90th percentile %v: want 14 Tasks and not the slowest of them",
				lead.Key, lead.Tasks, *lead.P90Seconds)
		}
	}
	var back Report
	if err := json.Unmarshal(document, &back); err != nil || !reflect.DeepEqual(back, r) {
		t.Errorf("the JSON document doesn't read back as the report (%v)", err)
	}
}

// checkMergedByTheStandardChange holds #204 to the fixture's report: a Task
// done by its Standard change is a merged change, the Standard change's, and
// its pull request that an earlier round closed is none; a Plan accepted and
// a Triage have no pull request, and merged nothing.
func checkMergedByTheStandardChange(t *testing.T, r Report) {
	t.Helper()
	merged := map[string]ReportMerged{}
	for _, m := range r.MergedTasks {
		merged[m.Task] = m
	}
	if _, ok := merged["plan-51"]; ok {
		t.Error("plan-51 has no pull request and is counted as a merged change")
	}
	if _, ok := merged["triage-52"]; ok {
		t.Error("triage-52 has no pull request and is counted as a merged change")
	}
	if m, ok := merged["std-53"]; !ok || m.PullRequest != 0 || m.StandardChange != 253 {
		t.Errorf("std-53 is %+v (listed: %v), want listed, with no pull request and Standard change 253", m, ok)
	}
}

// slowest is the longest lead time of the fixture's merged Tasks under key,
// which the 90th percentile of fourteen Tasks is not.
func slowest(t *testing.T, key string) float64 {
	t.Helper()
	longest := 0.0
	for _, task := range readReportFixture(t) {
		if lead := reportLead(task)[key]; lead != nil && MergedChange(task.TaskMetricFacts) &&
			reportFixturePeriod.holds(task.Completed) {
			longest = max(longest, *lead)
		}
	}
	return longest
}

// minutesAgo is a time so many minutes before the period's end.
func minutesAgo(m int) time.Time { return reportTo.Add(-time.Duration(m) * time.Minute) }

// reportStep is a step of a timeline that lasted so many minutes.
type reportStep struct {
	phase   string
	minutes int
	reason  string
}

// mergedTask is a Task that merged pull request 1 so many minutes before the
// period's end, after steps.
func mergedTask(ago int, steps ...reportStep) ReportTask {
	t := ReportTask{TaskMetricFacts: TaskMetricFacts{Namespace: "shop", Kind: chatKindImplement, Phase: TaskMerged,
		Round: 1, PullRequest: 1}, Completed: minutesAgo(ago)}
	total := 0
	for _, s := range steps {
		total += s.minutes
	}
	at := ago + total
	for _, s := range steps {
		t.History = append(t.History, TaskMetricStep{At: minutesAgo(at), Phase: s.phase, Reason: s.reason})
		at -= s.minutes
	}
	t.History = append(t.History, TaskMetricStep{At: minutesAgo(ago), Phase: TaskMerged})
	return t
}

// period is the day that ends at reportTo.
func period() ReportPeriod {
	return ReportPeriod{Name: "v1", From: reportTo.Add(-24 * time.Hour), To: reportTo}
}

func TestFactoryReportCountsWhatFinishedInThePeriod(t *testing.T) {
	failed := func(ago int, reason string) ReportTask {
		return ReportTask{TaskMetricFacts: TaskMetricFacts{Kind: chatKindImplement, Phase: TaskFailed,
			History: []TaskMetricStep{{At: minutesAgo(ago + 5), Phase: TaskImplementing, Reason: "Started"},
				{At: minutesAgo(ago), Phase: TaskFailed, Reason: reason}}}, Completed: minutesAgo(ago)}
	}
	with := func(t ReportTask, change func(*ReportTask)) ReportTask {
		change(&t)
		return t
	}
	day := 24 * 60
	tasks := []ReportTask{
		mergedTask(10),
		// The period's last instant is in it, its first is the report's before.
		mergedTask(0),
		mergedTask(day),
		mergedTask(day + 1),
		mergedTask(-1),
		// Not finished, and done with nothing merged: no merged Task.
		with(mergedTask(10), func(t *ReportTask) { t.Completed = time.Time{} }),
		with(mergedTask(10), func(t *ReportTask) { t.PullRequest = 0 }),
		with(mergedTask(10), func(t *ReportTask) { t.PullRequest, t.NoChange = 0, true }),
		with(mergedTask(10), func(t *ReportTask) { t.NoChange = true }),
		failed(20, ReasonNoCIResult),
		failed(30, ReasonNoCIResult),
		failed(40, ReasonBaseRed),
		failed(50, ""),
		failed(day+5, ReasonBaseRed),
		// A Scorer's judge run and a Benchmark's run measure the work.
		with(failed(20, ""), func(t *ReportTask) { t.Kind = taskKindScore }),
		with(failed(20, ""), func(t *ReportTask) { t.Benchmark = true }),
		with(mergedTask(10), func(t *ReportTask) { t.Kind, t.NoChange, t.PullRequest = taskKindScore, true, 0 }),
	}
	r := FactoryReport(period(), tasks)
	if r.Merged != 2 || r.Failed != 4 || r.NoChange != 2 {
		t.Errorf("merged, failed, no change = %d, %d, %d, want 2, 4, 2", r.Merged, r.Failed, r.NoChange)
	}
	want := []ReportCount{{ReasonNoCIResult, 2}, {ReasonBaseRed, 1}, {ReasonFailed, 1}}
	if !reflect.DeepEqual(r.FailedByReason, want) {
		t.Errorf("failed by reason = %v, want %v", r.FailedByReason, want)
	}
	if r.From != "2026-11-04T12:00:00Z" || r.To != "2026-11-05T12:00:00Z" {
		t.Errorf("period = %q to %q", r.From, r.To)
	}

	// From the first Task: no beginning, and everything up to the end.
	open := FactoryReport(ReportPeriod{To: reportTo}, tasks)
	if open.Merged != 4 || open.Failed != 5 || open.From != "" {
		t.Errorf("with no beginning: merged, failed, from = %d, %d, %q, want 4, 5 and none",
			open.Merged, open.Failed, open.From)
	}
}

func TestFactoryReportShares(t *testing.T) {
	commits := func(n int) *int { return &n }
	task := func(personCommits *int, approved bool, round int) ReportTask {
		t := mergedTask(10)
		t.PersonCommits, t.ApprovedByPerson, t.Round = personCommits, approved, round
		return t
	}
	r := FactoryReport(period(), []ReportTask{
		task(commits(0), false, 1), task(commits(0), true, 2), task(commits(3), false, 0), task(nil, false, 1),
	})
	for name, c := range map[string]struct {
		got       ReportShare
		tasks, of int
		share     float64
	}{
		"no person's commit, of those read": {r.NoPersonCommit, 2, 3, 2.0 / 3},
		"approved by a person":              {r.PersonApproved, 1, 4, 0.25},
		"merged in the first round":         {r.FirstRound, 3, 4, 0.75},
	} {
		if c.got.Tasks != c.tasks || c.got.Of != c.of || c.got.Share == nil || *c.got.Share != c.share {
			t.Errorf("%s = %+v, want %d of %d", name, c.got, c.tasks, c.of)
		}
	}
	// No merged Task whose commits were read: no share, which is not 0%.
	if got := FactoryReport(period(), []ReportTask{task(nil, false, 1)}).NoPersonCommit; got.Share != nil || got.Of != 0 {
		t.Errorf("with no commits read = %+v, want no share", got)
	}
}

func TestMedianAndPercentile(t *testing.T) {
	for _, c := range []struct {
		values      []float64
		median, p90 float64
	}{
		{[]float64{5}, 5, 5},
		{[]float64{3, 1}, 2, 3},
		{[]float64{9, 1, 5}, 5, 9},
		{[]float64{10, 1, 2, 3, 4, 5, 6, 7, 8, 9}, 5.5, 9},
		{[]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, 6, 10},
		{[]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}, 10.5, 18},
	} {
		if m, p := Median(c.values), Percentile(c.values, 90); *m != c.median || *p != c.p90 {
			t.Errorf("of %v: median %v and 90th percentile %v, want %v and %v", c.values, *m, *p, c.median, c.p90)
		}
	}
	if Median(nil) != nil || Percentile(nil, 90) != nil {
		t.Error("no values have a median or a percentile")
	}
	if p := Percentile([]float64{1, 2, 3}, 0); *p != 1 {
		t.Errorf("the lowest percentile is the smallest value, got %v", *p)
	}
}

func TestFactoryReportTimes(t *testing.T) {
	slot := reportStep{TaskPending, 4, reasonWaitingForSlot}
	tasks := []ReportTask{
		// 10 minutes to the pull request, 15 to the review, 20 to the merge.
		mergedTask(10, reportStep{TaskPending, 2, ""}, slot, reportStep{TaskImplementing, 10, ""},
			reportStep{TaskChecking, 5, ""}, reportStep{TaskReviewing, 5, ""}),
		// Two rounds: the lead times are to the first of each, the stages sum.
		mergedTask(10, reportStep{TaskImplementing, 30, ""}, reportStep{TaskChecking, 10, ""},
			reportStep{TaskReviewing, 20, ""}, reportStep{TaskImplementing, 10, ""},
			reportStep{TaskChecking, 10, ""}, reportStep{TaskReviewing, 10, ""}),
		mergedTask(10, reportStep{TaskImplementing, 20, ""}, reportStep{TaskChecking, 5, ""},
			reportStep{TaskReviewing, 5, ""}, reportStep{TaskAwaitingApproval, 60, ""}),
		// A Task that never reached Implementing has stages and no lead time.
		mergedTask(10, reportStep{TaskPending, 7, ""}),
	}
	r := FactoryReport(period(), tasks)
	type row struct {
		tasks       int
		median, p90 float64
	}
	want := map[string]row{
		LeadToPullRequest: {3, 20 * 60, 30 * 60},
		LeadToFirstReview: {3, 25 * 60, 40 * 60},
		LeadToMerge:       {3, 90 * 60, 90 * 60},
		StageQueued:       {2, 4.5 * 60, 7 * 60},
		StageSlot:         {1, 4 * 60, 4 * 60},
		StageImplement:    {3, 20 * 60, 40 * 60},
		StageChecks:       {3, 5 * 60, 20 * 60},
		StageReview:       {3, 5 * 60, 30 * 60},
		StageApproval:     {1, 60 * 60, 60 * 60},
		StageVerify:       {},
		StageBlocked:      {},
	}
	rows := append(append([]ReportTime{}, r.LeadTimes...), r.StageTimes...)
	if len(rows) != len(want) {
		t.Fatalf("%d rows, want %d", len(rows), len(want))
	}
	for _, got := range rows {
		w := want[got.Key]
		if got.Tasks != w.tasks {
			t.Errorf("%s: %d Tasks, want %d", got.Key, got.Tasks, w.tasks)
			continue
		}
		if w.tasks == 0 {
			// A stage no Task was in has no time, which is not zero seconds.
			if got.MedianSeconds != nil || got.P90Seconds != nil {
				t.Errorf("%s: a time over no Tasks", got.Key)
			}
			continue
		}
		if *got.MedianSeconds != w.median || *got.P90Seconds != w.p90 {
			t.Errorf("%s: median %v, 90th percentile %v, want %v and %v", got.Key, *got.MedianSeconds,
				*got.P90Seconds, w.median, w.p90)
		}
	}
	if keys := []string{r.StageTimes[0].Key, r.StageTimes[1].Key, r.StageTimes[7].Key}; !reflect.DeepEqual(keys,
		[]string{StageQueued, StageSlot, StageBlocked}) {
		t.Errorf("the stages are not in a Task's order: %v", keys)
	}
}

// #66: a Task that left a stage in the second it entered it was in the stage.
// The report dropped it, so three Tasks queued for no time and one for ten
// minutes read "Queued | 1 | 10m".
func TestFactoryReportCountsAZeroSecondStay(t *testing.T) {
	at := func(queued int) ReportTask {
		return mergedTask(10, reportStep{TaskPending, queued, ""}, reportStep{TaskImplementing, 5, ""})
	}
	r := FactoryReport(period(), []ReportTask{at(0), at(0), at(10), at(0)})
	queued := r.StageTimes[0]
	if queued.Key != StageQueued || queued.Tasks != 4 || queued.MedianSeconds == nil || *queued.MedianSeconds != 0 ||
		*queued.P90Seconds != 600 {
		t.Fatalf("queued = %+v, want 4 Tasks, a median of 0 seconds and a 90th percentile of 600", queued)
	}
	page, err := r.Markdown()
	if err != nil {
		t.Fatal(err)
	}
	if want := "| Queued | 4 | 0s | 10m |\n"; !strings.Contains(page, want) {
		t.Errorf("the page lacks %q:\n%s", want, page)
	}
	// A stage none of them was in is still no row and no time.
	if blocked := r.StageTimes[7]; blocked.Tasks != 0 || blocked.MedianSeconds != nil || strings.Contains(page, "| Blocked |") {
		t.Errorf("blocked = %+v, want no Task and no row", blocked)
	}
}

// #66: the lead times run from the first Implementing step to the first
// Checking and Reviewing steps from then on. A Task that took up a pull
// request already in CI has both before it, and they are no lead time.
func TestFactoryReportLeadTimesStartAtTheFirstImplementingStep(t *testing.T) {
	task := mergedTask(10, reportStep{TaskChecking, 30, ""}, reportStep{TaskReviewing, 10, ""},
		reportStep{TaskImplementing, 50, ""}, reportStep{TaskChecking, 40, ""}, reportStep{TaskReviewing, 20, ""})
	lead := reportLead(task)
	for key, want := range map[string]float64{LeadToPullRequest: 50 * 60, LeadToFirstReview: 90 * 60, LeadToMerge: 110 * 60} {
		if lead[key] == nil || *lead[key] != want {
			t.Errorf("%s = %v, want %v seconds from the first Implementing step", key, lead[key], want)
		}
	}
	// A step at the very second Implementing began is from then on.
	same := mergedTask(10, reportStep{TaskImplementing, 0, ""}, reportStep{TaskChecking, 5, ""})
	if got := reportLead(same)[LeadToPullRequest]; got == nil || *got != 0 {
		t.Errorf("a pull request in the second Implementing began = %v, want 0 seconds", got)
	}
}

// #66: the JSON names each merged Task and its pull request, so a reader can
// hold a number against the pull requests it counts.
func TestFactoryReportNamesTheMergedTasks(t *testing.T) {
	named := func(ago int, namespace, container, name string, pr int) ReportTask {
		t := mergedTask(ago)
		t.Namespace, t.Container, t.Name, t.PullRequest = namespace, container, name, pr
		return t
	}
	failed := named(5, "shop", "api", "failed", 9)
	failed.Phase = TaskFailed
	// The repository is its Container's, where it was read (decision 15).
	late := named(30, "shop", "web", "late", 3)
	late.Repository = "https://git.example.com/shop/web"
	r := FactoryReport(period(), []ReportTask{
		named(10, "shop", "api", "b", 7), late, named(10, "shop", "api", "a", 8),
		named(10, "docs", "site", "z", 1), named(48*60, "shop", "api", "before", 2), failed,
	})
	want := []ReportMerged{
		{"shop", "web", "https://git.example.com/shop/web", "late", 3, 0, "2026-11-05T11:30:00Z"},
		{"docs", "site", "", "z", 1, 0, "2026-11-05T11:50:00Z"},
		{"shop", "api", "", "a", 8, 0, "2026-11-05T11:50:00Z"},
		{"shop", "api", "", "b", 7, 0, "2026-11-05T11:50:00Z"},
	}
	if !reflect.DeepEqual(r.MergedTasks, want) {
		t.Errorf("merged Tasks = %+v, want %+v: the period's, the oldest merge first, then by namespace and name",
			r.MergedTasks, want)
	}
	document, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(document), `"repository"`); got != 1 ||
		!strings.Contains(string(document), `"repository": "https://git.example.com/shop/web"`) {
		t.Errorf("the JSON has %d repositories, want the one that was read and no empty one:\n%s", got, document)
	}
	if none := FactoryReport(period(), nil); none.MergedTasks == nil || len(none.MergedTasks) != 0 {
		t.Errorf("with no merged Task the list is %v, want an empty one, which the JSON writes as []", none.MergedTasks)
	}
}

// #66: rows with as many Tasks are in the order of their names' code points,
// which the UI's compareText gives too; a locale's order is another on
// another machine.
func TestReportKeyOrder(t *testing.T) {
	for _, c := range [][]string{
		{"claude-code · Opus", "claude-code · haiku"},
		{"claude-code · m-1", "claude-code · m_1"},
		{"claude-code · Z", "claude-code · a"},
		{"claude-code · z", "claude-code · ä"},
		{"a", "a b"},
		{"zebra", ReportNotRecorded},
	} {
		if reportKeyOrder(c[0], c[1], 1, 1) >= 0 || reportKeyOrder(c[1], c[0], 1, 1) <= 0 {
			t.Errorf("%q is not before %q", c[0], c[1])
		}
	}
	if reportKeyOrder("b", "a", 2, 1) >= 0 || reportKeyOrder(ReportNotRecorded, "a", 9, 1) <= 0 {
		t.Error("the larger count is not first, or the unrecorded not last")
	}
}

func TestFactoryReportCost(t *testing.T) {
	implement := func(agent, harness, model string) TaskMetricRun {
		return TaskMetricRun{Agent: agent, Harness: harness, Model: model, Role: RoleImplementer}
	}
	task := func(cost int64, size *ReportSize, runs ...TaskMetricRun) ReportTask {
		t := mergedTask(10, reportStep{TaskImplementing, int(cost / 100), ""})
		t.CostMilliUSD, t.Size, t.Runs = cost, size, runs
		return t
	}
	review := TaskMetricRun{Agent: "reviewer", Harness: "claude-code", Model: "opus", Role: RoleReviewer}
	r := FactoryReport(period(), []ReportTask{
		task(800, &ReportSize{40, 10}, implement("junior", "claude-code", "sonnet"), review),
		task(1200, &ReportSize{20, 0}, implement("junior", "claude-code", "sonnet")),
		task(0, &ReportSize{60, 39}, implement("junior", "claude-code", "sonnet")),
		task(9000, &ReportSize{600, 100}, implement("senior", "claude-code", "opus"), review),
		// Its newest implement run is the one it counts under.
		task(4000, &ReportSize{60, 40}, implement("junior", "claude-code", "sonnet"), implement("senior", "claude-code", "")),
		// No size read, and no run that recorded a harness.
		task(100, nil, TaskMetricRun{Agent: "junior", Role: RoleImplementer}, review),
	})
	if r.Cost.Tasks != 5 || *r.Cost.MedianMilliUSD != 1200 {
		t.Errorf("cost = %d Tasks, median %v, want 5 and 1200", r.Cost.Tasks, *r.Cost.MedianMilliUSD)
	}
	type size struct {
		size          string
		merged, costs int
		median        float64
	}
	sizes := make([]size, 0, len(r.CostBySize))
	for _, s := range r.CostBySize {
		got := size{size: s.Size, merged: s.Merged, costs: s.Cost.Tasks}
		if s.Cost.MedianMilliUSD != nil {
			got.median = *s.Cost.MedianMilliUSD
		}
		sizes = append(sizes, got)
	}
	if want := []size{{SizeS, 3, 2, 1000}, {SizeM, 1, 1, 4000}, {SizeL, 1, 1, 9000}, {SizeXL, 0, 0, 0}}; !reflect.DeepEqual(sizes, want) {
		t.Errorf("by size = %v, want %v", sizes, want)
	}
	if r.CostBySize[3].Cost.MedianMilliUSD != nil {
		t.Error("a size with no Task has a median cost")
	}
	if r.WithoutSize != 1 {
		t.Errorf("without a size = %d, want 1", r.WithoutSize)
	}

	type group struct {
		key           string
		merged, costs int
		median        float64
	}
	groups := func(rows []ReportGroup) []group {
		out := make([]group, 0, len(rows))
		for _, g := range rows {
			out = append(out, group{g.Key, g.Merged, g.Cost.Tasks, *g.Cost.MedianMilliUSD})
		}
		return out
	}
	// The most merged first, then by name, and the unrecorded last. A cost of
	// zero is none reported: sonnet's median is over two of its three.
	if got, want := groups(r.ByConfig), []group{{"claude-code · sonnet", 3, 2, 1000}, {"claude-code · default model", 1, 1, 4000},
		{"claude-code · opus", 1, 1, 9000}, {ReportNotRecorded, 1, 1, 100}}; !reflect.DeepEqual(got, want) {
		t.Errorf("by harness and model = %v, want %v", got, want)
	}
	if got, want := groups(r.ByAgent), []group{{"junior", 3, 2, 1000}, {"senior", 2, 2, 6500},
		{ReportNotRecorded, 1, 1, 100}}; !reflect.DeepEqual(got, want) {
		t.Errorf("by Agent = %v, want %v", got, want)
	}
	if got := *r.ByAgent[1].ToMergeSeconds; got != 65*60 {
		t.Errorf("senior's median time to merge = %v, want 65 minutes", got)
	}
}

func TestFactoryReportScorers(t *testing.T) {
	criteria := []string{"does what the issue asks", "has a test"}
	score := func(scorer string, generation int64, harness, model string, verdicts ...string) ReportTask {
		t := ReportTask{TaskMetricFacts: TaskMetricFacts{Namespace: "shop", Kind: taskKindScore, Phase: TaskMerged},
			Completed: minutesAgo(10),
			Score:     &ReportScore{Scorer: scorer, Generation: generation, Criteria: criteria, Harness: harness, Model: model}}
		for i, v := range verdicts {
			t.Score.Verdicts = append(t.Score.Verdicts, ScoreCriterion{Criterion: criteria[i], Verdict: v})
		}
		return t
	}
	with := func(t ReportTask, change func(*ReportTask)) ReportTask {
		change(&t)
		return t
	}
	r := FactoryReport(period(), []ReportTask{
		score("quality", 2, "claude-code", "sonnet", ScorePass, ScoreFail),
		score("quality", 2, "claude-code", "sonnet", ScorePass, ScorePass),
		score("quality", 2, "claude-code", "opus", ScoreFail, ScoreUnclear),
		score("quality", 2, "", "", ScorePass, ScorePass),
		// An older generation asked other questions; a Score with no verdicts,
		// one still running and one from before the period say nothing.
		score("quality", 1, "claude-code", "sonnet", ScoreFail, ScoreFail),
		score("quality", 2, "claude-code", "sonnet"),
		with(score("quality", 2, "claude-code", "sonnet", ScoreFail, ScoreFail), func(t *ReportTask) { t.Phase = TaskImplementing }),
		with(score("quality", 2, "claude-code", "sonnet", ScoreFail, ScoreFail), func(t *ReportTask) {
			t.Completed = minutesAgo(48 * 60)
		}),
		score("a-first", 7, "claude-code", "opus", ScoreUnclear),
	})
	if len(r.Scorers) != 2 || r.Scorers[0].Scorer != "a-first" || r.Scorers[1].Scorer != "quality" {
		t.Fatalf("Scorers = %+v, want a-first then quality", r.Scorers)
	}
	if v := r.Scorers[0].Criteria[0].All; v.Unclear != 1 || v.Rate != nil {
		t.Errorf("only unclear verdicts = %+v, want no rate", v)
	}
	q := r.Scorers[1]
	if q.Generation != 2 || q.Scores != 4 {
		t.Errorf("generation %d, %d Scores, want 2 and 4", q.Generation, q.Scores)
	}
	if want := []string{"claude-code · sonnet", "claude-code · opus", ReportNotRecorded}; !reflect.DeepEqual(q.Configs, want) {
		t.Errorf("configurations = %v, want %v", q.Configs, want)
	}
	rate := func(v ReportVerdicts) [4]float64 {
		r := -1.0
		if v.Rate != nil {
			r = *v.Rate
		}
		return [4]float64{float64(v.Pass), float64(v.Fail), float64(v.Unclear), r}
	}
	if got := rate(q.Criteria[0].All); got != [4]float64{3, 1, 0, 0.75} {
		t.Errorf("the first criterion over all = %v", got)
	}
	// Unclear is said apart and not counted against: 2 of 3, not 2 of 4.
	if got := rate(q.Criteria[1].All); got != [4]float64{2, 1, 1, 2.0 / 3} {
		t.Errorf("the second criterion over all = %v", got)
	}
	if got := rate(q.Criteria[1].ByConfig[1]); got != [4]float64{0, 0, 1, -1} || q.Criteria[1].ByConfig[1].Config != "claude-code · opus" {
		t.Errorf("the second criterion under opus = %v", got)
	}
}

func TestReportPage(t *testing.T) {
	for name, c := range map[string]struct{ got, want string }{
		"seconds":           {reportDuration(new(44.6)), "45s"},
		"minutes":           {reportDuration(new(750.0)), "12m 30s"},
		"whole minutes":     {reportDuration(new(1200.0)), "20m"},
		"hours":             {reportDuration(new(11100.0)), "3h 5m"},
		"days":              {reportDuration(new(187200.0)), "2d 4h"},
		"no time":           {reportDuration(nil), "–"},
		"cost":              {reportMoney(new(1500.0)), "$1.50"},
		"half a cent, up":   {reportMoney(new(125.0)), "$0.13"},
		"and over a dollar": {reportMoney(new(1125.0)), "$1.13"},
		"under a cent":      {reportMoney(new(9.5)), "<$0.01"},
		"no cost":           {reportMoney(nil), "–"},
		"a cost under zero": {reportMoney(new(-1500.0)), "–"},
		"and a hair under":  {reportMoney(new(-0.001)), "–"},
		"a cost of NaN":     {reportMoney(new(math.NaN())), "–"},
		"an endless cost":   {reportMoney(new(math.Inf(1))), "–"},
		"and its opposite":  {reportMoney(new(math.Inf(-1))), "–"},
		"mathematics":       {reportCell(`reads $HOME and $PATH, costs $5`), `reads <span>$</span>HOME and <span>$</span>PATH, costs <span>$</span>5`},
		"its own span":      {reportCell(`<span>$</span> \$`), `&lt;span><span>$</span>&lt;/span> \\<span>$</span>`},
		"share":             {reportPercent(new(2.0 / 3)), "67%"},
		"rate":              {reportRate(ReportVerdicts{Pass: 3, Fail: 1, Unclear: 2, Rate: new(0.75)}), "75% (3 of 4), 2 unclear"},
		"nothing judged":    {reportRate(ReportVerdicts{}), "–"},
		"a cell stays one":  {reportCell("reads plainly | no\njargon"), `reads plainly \| no jargon`},
		"its own backslash": {reportCell(`a\|b`), `a\\\|b`},
		"a tag":             {reportCell(`<img src=x>`), `&lt;img src=x>`},
		"an entity":         {reportCell(`a &lt; b && c`), `a &amp;lt; b &amp;&amp; c`},
		"a code span":       {reportCell("uses `a|b` not `c\\d`"), "uses \\`a\\|b\\` not \\`c\\\\d\\`"},
		"a link":            {reportCell(`[see here](https://example.com/x_y)`), `\[see here\](https://example.com/x\_y)`},
		"an image":          {reportCell(`![x](https://example.com/p.png)`), `!\[x\](https://example.com/p.png)`},
		"emphasis":          {reportCell(`*all* of __it__ ~~now~~`), `\*all\* of \_\_it\_\_ \~\~now\~\~`},
		"plain text":        {reportCell("claude-code · opus (4.5), 100% + more: yes/no #1"), "claude-code · opus (4.5), 100% + more: yes/no #1"},
		"default model":     {ReportConfigKey("claude-code", ""), "claude-code · default model"},
		"no harness":        {ReportConfigKey("", "opus"), ReportNotRecorded},
		"a time, in UTC":    {reportTime("2026-11-05T14:00:00+02:00"), "2026-11-05 12:00 UTC"},
		"the period's line": {strings.Split(reportPage(t, FactoryReport(ReportPeriod{Name: "v1", To: reportTo}, nil)), "\n")[2], "The Tasks that finished up to 2026-11-05 12:00 UTC, counted from what each Task records. The same numbers as JSON: [v1.json](v1.json)."},
		"a name's heading":  {strings.Split(reportPage(t, FactoryReport(ReportPeriod{Name: "v1._rc_", To: reportTo}, nil)), "\n")[0], `# The factory's numbers: v1.\_rc\_`},
	} {
		if c.got != c.want {
			t.Errorf("%s: %q, want %q", name, c.got, c.want)
		}
	}

	// A period in which nothing finished still says so, and what it can't show.
	page := reportPage(t, FactoryReport(period(), nil))
	for _, want := range []string{
		"# The factory's numbers: v1\n",
		"| Merged a pull request | 0 |",
		"| With no commit by a person | 0 of 0 | – |",
		"No merged Task has a timeline in this period.",
		"The median is –, over the merged Tasks whose agents reported a cost: 0 of 0.",
		"No Scorer judged a Task in this period.",
		"## What these numbers don't show\n\n- What people decided.",
		"- Other projects.",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the empty period's page lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "by the reason each recorded") || strings.Contains(page, "| Agent |") ||
		strings.Contains(page, "mergedTasks") {
		t.Errorf("the empty period's page has a table of nothing, or points at merged Tasks it has none of:\n%s", page)
	}
}

// reportPage is r's page.
func reportPage(t *testing.T, r Report) string {
	t.Helper()
	page, err := r.Markdown()
	if err != nil {
		t.Fatal(err)
	}
	return page
}

// #66: a report's name is two files' and goes into its page's heading and
// link, so one that is a path, or empty, is refused before anything is
// written.
func TestReportPathsRefuseANameThatIsNoName(t *testing.T) {
	for _, c := range []struct{ dir, name, page, document string }{
		{"", "v0.5.0", "docs/results/v0.5.0.md", "docs/results/v0.5.0.json"},
		{"", "2026-11", "docs/results/2026-11.md", "docs/results/2026-11.json"},
		{"", "v1.0.0-rc.1+build_7", "docs/results/v1.0.0-rc.1+build_7.md", "docs/results/v1.0.0-rc.1+build_7.json"},
		// The directory is the repository's own to choose.
		{"site/numbers", "v0.5.0", "site/numbers/v0.5.0.md", "site/numbers/v0.5.0.json"},
		{"../pages/results/", "v0.5.0", "../pages/results/v0.5.0.md", "../pages/results/v0.5.0.json"},
	} {
		page, document, err := ReportPaths(c.dir, c.name)
		if err != nil || page != c.page || document != c.document {
			t.Errorf("ReportPaths(%q, %q) = %q, %q, %v, want %q and %q", c.dir, c.name, page, document, err, c.page, c.document)
		}
	}
	for _, name := range []string{"", "../../README", "a/b", `a\b`, "..", ".hidden", "-rf", "v1 final", "v1\n", "v1|x",
		"<b>", "[x](y)", "v1*", "é"} {
		page, document, err := ReportPaths("", name)
		if err == nil || page != "" || document != "" || !strings.Contains(err.Error(), strconv.Quote(name)) {
			t.Errorf("ReportPaths(%q) = %q, %q, %v, want an error that quotes the name and no path", name, page, document, err)
		}
		if page, err := (Report{Name: name, To: "2026-11-05T12:00:00Z"}).Markdown(); err == nil || page != "" {
			t.Errorf("a report called %q has a page:\n%s", name, page)
		}
	}
}

// #66: the page writes money as the UI's formatCost does. money.json has the
// costs both must print alike; ui/src/model/report.test.ts reads it too.
func TestReportMoneyIsTheUIs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(reportFixture, "money.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		MilliUSD float64
		Shown    string
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 10 {
		t.Fatalf("money.json has %d cases", len(cases))
	}
	for _, c := range cases {
		if got := reportMoney(&c.MilliUSD); got != c.Shown {
			t.Errorf("%v thousandths of a dollar: %q, want %q", c.MilliUSD, got, c.Shown)
		}
	}
}
