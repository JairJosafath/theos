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
	"bytes"
	"cmp"
	"encoding/json"
	"slices"
	"time"
)

// The factory's numbers as a report (#66, docs/plans/results-report.md): the
// Tasks that finished in a period, as one JSON document and one Markdown
// page a release publishes. The rules are the UI's "How well it works"
// (ui/src/model/metrics.ts, scores.ts and factory.ts), written again here
// because a command can't call TypeScript. One fixture holds the two
// together: ui/src/model/testdata/report has Tasks as the API serves them
// and the report they give, the UI's test computes its numbers from the same
// Tasks and compares them with that report, and a rule changed on one side
// alone fails the other's test, for the rules the fixture has a Task for
// (the plan lists them).

// ReportTask is what the report reads of one Task: the metrics' facts, and
// what only a report needs.
type ReportTask struct {
	TaskMetricFacts
	// Name is the Task's; the report names each merged Task by it.
	Name string
	// Repository is the web address of the Task's Container's repository,
	// which its pull request is in; empty when it wasn't read.
	Repository string
	// Completed is when the Task finished; zero while it hasn't.
	Completed time.Time
	// Size is the merged pull request's changed lines; nil when the Git host
	// wasn't read.
	Size *ReportSize
	// Score is a Score Task's: whose criteria it judged, and the verdicts.
	Score *ReportScore
}

// ReportSize is a pull request's changed lines.
type ReportSize struct {
	Additions, Deletions int
}

// ReportScore is what a Score Task records: its Scorer and the generation it
// copied the criteria from, the configuration of the Task it judged, and the
// judge's verdicts, in the criteria's order.
type ReportScore struct {
	Scorer     string
	Generation int64
	Criteria   []string
	// Harness and Model are the judged Task's implementer's; Harness is empty
	// when its runs recorded none.
	Harness, Model string
	// Verdicts is empty until the judge reported.
	Verdicts []ScoreCriterion
}

// ReportPeriod is the time a report covers: the Tasks that finished after
// From, up to and including To. A release's report runs from the release
// before it, so two reports in a row count no Task twice. A zero From means
// from the first Task.
type ReportPeriod struct {
	// Name is what the period is called, a release's tag.
	Name     string
	From, To time.Time
}

// holds reports whether a Task that finished at t is the period's.
func (p ReportPeriod) holds(t time.Time) bool {
	return !t.IsZero() && t.After(p.From) && !t.After(p.To)
}

// Report is the factory's numbers over a period. Its JSON is the document a
// release publishes beside the page; a field that is absent has no value (no
// Task to take a median of), which is not zero.
type Report struct {
	Name string `json:"name"`
	// From and To are the period, RFC 3339 in UTC; From is empty for a report
	// from the first Task.
	From string `json:"from,omitempty"`
	To   string `json:"to"`

	// Merged counts the Tasks that merged a pull request, Failed the ones
	// that failed, and NoChange the ones whose agent found the issue needs no
	// change. A Scorer's judge runs and a Benchmark's runs measure the work
	// and are none of them.
	Merged         int           `json:"merged"`
	Failed         int           `json:"failed"`
	NoChange       int           `json:"noChange"`
	FailedByReason []ReportCount `json:"failedByReason"`

	// NoPersonCommit is the merged Tasks none of whose commits a person
	// wrote, of those whose commits were read.
	NoPersonCommit ReportShare `json:"noPersonCommit"`
	// PersonApproved is the merged Tasks a person approved (a spec, or a
	// change the Crew's approval rules held), and FirstRound the ones that
	// merged in their first round; both of all merged.
	PersonApproved ReportShare `json:"personApproved"`
	FirstRound     ReportShare `json:"firstRound"`

	// LeadTimes run from a merged Task's first Implementing step: to its pull
	// request, its first review and its merge. StageTimes are the time a
	// merged Task spent in each stage, over the Tasks that were in it.
	LeadTimes  []ReportTime `json:"leadTimes"`
	StageTimes []ReportTime `json:"stageTimes"`

	// Cost is a merged Task's, over those whose agents reported one.
	Cost        ReportCost       `json:"cost"`
	CostBySize  []ReportSizeCost `json:"costBySize"`
	WithoutSize int              `json:"withoutSize"`
	// ByConfig groups the merged Tasks by the harness and model that
	// implemented them, ByAgent by the Agent; the most merged first and the
	// ones that recorded none last.
	ByConfig []ReportGroup `json:"byConfig"`
	ByAgent  []ReportGroup `json:"byAgent"`

	Scorers []ReportScorer `json:"scorers"`
	// MergedTasks names every merged Task the numbers are over, with its pull
	// request, in the order they merged, so a reader can hold a number against
	// the pull requests it counts.
	MergedTasks []ReportMerged `json:"mergedTasks"`
	// NotShown is what the numbers leave out, said on every report.
	NotShown []string `json:"notShown"`
}

// ReportMerged is one merged Task: its Container, whose repository the pull
// request is in, and when it merged, RFC 3339 in UTC.
type ReportMerged struct {
	Namespace string `json:"namespace"`
	Container string `json:"container"`
	// Repository is the web address of the Container's repository, so a
	// reader has the pull request's address and not only its number. It is
	// absent when the Container's wasn't read (a Container deleted since).
	Repository string `json:"repository,omitempty"`
	Task       string `json:"task"`
	// PullRequest is the Task's own pull request that merged; absent for a
	// Task that ended DoneByStandardChange, which merged none (#204).
	PullRequest int `json:"pullRequest,omitempty"`
	// StandardChange is the issue of the Standard change that merged in its
	// place, for such a Task, and absent otherwise.
	StandardChange int    `json:"standardChange,omitempty"`
	Completed      string `json:"completed"`
}

// ReportCount is how many Tasks share a key.
type ReportCount struct {
	Key   string `json:"key"`
	Tasks int    `json:"tasks"`
}

// ReportShare is Tasks out of Of; Share is absent when Of is zero.
type ReportShare struct {
	Tasks int      `json:"tasks"`
	Of    int      `json:"of"`
	Share *float64 `json:"share,omitempty"`
}

// ReportTime is how long something took over Tasks Tasks: the median and the
// 90th percentile, in seconds.
type ReportTime struct {
	Key           string   `json:"key"`
	Label         string   `json:"label"`
	Tasks         int      `json:"tasks"`
	MedianSeconds *float64 `json:"medianSeconds,omitempty"`
	P90Seconds    *float64 `json:"p90Seconds,omitempty"`
}

// ReportCost is the median cost of the Tasks that reported one, in
// thousandths of a US dollar.
type ReportCost struct {
	// Tasks is how many reported a cost: the median's whole.
	Tasks          int      `json:"tasks"`
	MedianMilliUSD *float64 `json:"medianMilliUSD,omitempty"`
}

// ReportSizeCost is the merged Tasks of one size and what they cost.
type ReportSizeCost struct {
	Size   string     `json:"size"`
	Merged int        `json:"merged"`
	Cost   ReportCost `json:"cost"`
}

// ReportGroup is the merged Tasks of one configuration or one Agent.
type ReportGroup struct {
	Key            string      `json:"key"`
	Merged         int         `json:"merged"`
	NoPersonCommit ReportShare `json:"noPersonCommit"`
	Cost           ReportCost  `json:"cost"`
	// ToMergeSeconds is the median from the first Implementing step to the merge.
	ToMergeSeconds *float64 `json:"toMergeSeconds,omitempty"`
}

// ReportScorer is one Scorer's verdicts in the period, per criterion.
type ReportScorer struct {
	Namespace string `json:"namespace"`
	Scorer    string `json:"scorer"`
	// Generation is the Scorer's newest one judged in the period; the Scores
	// of an older one answered other criteria and are left out.
	Generation int64 `json:"generation"`
	// Scores is how many Score Tasks of that generation finished with verdicts.
	Scores int `json:"scores"`
	// Configs are the judged Tasks' configurations, the most judged first.
	Configs  []string          `json:"configs"`
	Criteria []ReportCriterion `json:"criteria"`
}

// ReportCriterion is how one criterion fared: over every Score, and per
// configuration in the Scorer's Configs' order.
type ReportCriterion struct {
	Criterion string           `json:"criterion"`
	All       ReportVerdicts   `json:"all"`
	ByConfig  []ReportVerdicts `json:"byConfig"`
}

// ReportVerdicts counts a criterion's verdicts. Rate is Pass out of Pass and
// Fail; Unclear is said apart and not counted against. It is absent when
// nothing was judged either way.
type ReportVerdicts struct {
	Config  string   `json:"config,omitempty"`
	Pass    int      `json:"pass"`
	Fail    int      `json:"fail"`
	Unclear int      `json:"unclear"`
	Rate    *float64 `json:"rate,omitempty"`
}

// taskKindScore is the kind of a Score Task, a Scorer's judge run.
const taskKindScore = "Score"

// ReportNotRecorded keys the merged Tasks whose runs recorded no harness or
// no Agent, as the UI's "not recorded" does.
const ReportNotRecorded = "not recorded"

// ReportConfigKey names a harness and model as the UI does: "claude-code ·
// opus", "claude-code · default model" for the harness's own, and
// ReportNotRecorded without a harness.
func ReportConfigKey(harness, model string) string {
	switch {
	case harness == "":
		return ReportNotRecorded
	case model == "":
		return harness + " · default model"
	}
	return harness + " · " + model
}

// The lead times' keys.
const (
	LeadToPullRequest = "toPullRequest"
	LeadToFirstReview = "toFirstReview"
	LeadToMerge       = "toMerge"
)

// reportStage is a stage of a Task's timeline, under the UI's key and name.
type reportStage struct{ key, label string }

var (
	reportLeads = []reportStage{
		{LeadToPullRequest, "The pull request"},
		{LeadToFirstReview, "The first review"},
		{LeadToMerge, "The merge"},
	}
	// reportStages are in the order a Task passes them.
	reportStages = []reportStage{
		{StageQueued, "Queued"},
		{StageSlot, "Waiting for a slot"},
		{StageImplement, "Implementing"},
		{StageChecks, "CI checks"},
		{StageReview, "Reviewing"},
		{StageVerify, "Verifying"},
		{StageApproval, "Awaiting approval"},
		{StageBlocked, "Blocked"},
	}
	reportSizes = []string{SizeS, SizeM, SizeL, SizeXL}
)

// reportStageOf is the stage of one timeline step; empty for the steps that
// end a timeline. Waiting for an agent slot is a stage of its own, whatever
// phase the Task waits in.
func reportStageOf(step TaskMetricStep) string {
	if step.Reason == reasonWaitingForSlot {
		return StageSlot
	}
	switch step.Phase {
	case TaskPending:
		return StageQueued
	case TaskImplementing:
		return StageImplement
	case TaskChecking:
		return StageChecks
	case TaskReviewing:
		return StageReview
	case TaskVerifying:
		return StageVerify
	case TaskAwaitingApproval:
		return StageApproval
	case TaskBlocked:
		return StageBlocked
	}
	return ""
}

// reportNotShown is what no report's numbers show (#66). The page prints it
// and the JSON carries it, so a number quoted from either travels with it.
var reportNotShown = []string{
	"What people decided. Which issues were filed, what a spec says and when a release is deployed are people's " +
		"choices. A Task a person approved still counts as merged with no person's commit when the person wrote " +
		"none of its code: the share is of code, not of decisions.",
	"Why a Task failed. Failed Tasks are counted by the reason the Task recorded; what went wrong is on each " +
		"Task's issue and pull request.",
	"What the failures cost. Cost is the median of the merged Tasks whose agents reported one. A failed Task's " +
		"runs cost money too, and are in no number here.",
	"What is no longer recorded. The numbers come from the Tasks the cluster held when the report was made: a " +
		"Task deleted before that is not counted, and a Task keeps the newest 64 steps of its timeline, so the " +
		"times of a longer one are cut short.",
	"Other projects. This is one factory building one codebase, with its own Standards, Agents and issues. " +
		"It says how that went, not how another project would go.",
}

// FactoryReport is the report of the Tasks that finished in period.
func FactoryReport(period ReportPeriod, tasks []ReportTask) Report {
	r := Report{
		Name:           period.Name,
		To:             period.To.UTC().Format(time.RFC3339),
		FailedByReason: []ReportCount{},
		NotShown:       reportNotShown,
	}
	if !period.From.IsZero() {
		r.From = period.From.UTC().Format(time.RFC3339)
	}

	var merged []ReportTask
	failed := map[string]int{}
	for _, t := range tasks {
		if !period.holds(t.Completed) {
			continue
		}
		measures := t.Kind == taskKindScore || t.Benchmark
		switch {
		case t.Phase == TaskMerged && t.NoChange:
			if !measures {
				r.NoChange++
			}
		case MergedChange(t.TaskMetricFacts):
			merged = append(merged, t)
		case t.Phase == TaskFailed && !measures:
			r.Failed++
			failed[reportFailedReason(t)]++
		}
	}
	r.Merged = len(merged)
	r.MergedTasks = make([]ReportMerged, 0, len(merged))
	for _, t := range merged {
		entry := ReportMerged{Namespace: t.Namespace, Container: t.Container, Repository: t.Repository,
			Task: t.Name, PullRequest: t.PullRequest, Completed: t.Completed.UTC().Format(time.RFC3339)}
		if t.DoneByStandardChange {
			// Its own pull request was closed; the change that merged is the Standard's.
			entry.PullRequest, entry.StandardChange = 0, t.StandardChange
		}
		r.MergedTasks = append(r.MergedTasks, entry)
	}
	slices.SortFunc(r.MergedTasks, func(a, b ReportMerged) int {
		return cmp.Or(cmp.Compare(a.Completed, b.Completed), cmp.Compare(a.Namespace, b.Namespace),
			cmp.Compare(a.Task, b.Task))
	})
	for reason, n := range failed {
		r.FailedByReason = append(r.FailedByReason, ReportCount{Key: reason, Tasks: n})
	}
	slices.SortFunc(r.FailedByReason, func(a, b ReportCount) int {
		return cmp.Or(cmp.Compare(b.Tasks, a.Tasks), cmp.Compare(a.Key, b.Key))
	})

	all := reportGroup("all", merged)
	r.NoPersonCommit, r.Cost = all.NoPersonCommit, all.Cost
	r.PersonApproved = reportShare(merged, len(merged), func(t ReportTask) bool { return t.ApprovedByPerson })
	r.FirstRound = reportShare(merged, len(merged), func(t ReportTask) bool { return t.Round <= 1 })
	r.LeadTimes, r.StageTimes = reportTimes(merged)

	for _, size := range reportSizes {
		var sized []ReportTask
		for _, t := range merged {
			if t.Size != nil && ChangeSize(t.Size.Additions, t.Size.Deletions) == size {
				sized = append(sized, t)
			}
		}
		r.CostBySize = append(r.CostBySize, ReportSizeCost{Size: size, Merged: len(sized), Cost: reportCost(sized)})
	}
	for _, t := range merged {
		if t.Size == nil {
			r.WithoutSize++
		}
	}
	r.ByConfig = reportGroups(merged, func(t ReportTask) string {
		run := reportImplementer(t)
		return ReportConfigKey(run.Harness, run.Model)
	})
	r.ByAgent = reportGroups(merged, func(t ReportTask) string {
		return cmp.Or(reportImplementer(t).Agent, ReportNotRecorded)
	})
	r.Scorers = reportScorers(period, tasks)
	return r
}

// reportFailedReason is why a failed Task says it failed: the reason of its
// timeline's last Failed step, or ReasonFailed when it gave none.
func reportFailedReason(t ReportTask) string {
	for _, step := range slices.Backward(t.History) {
		if step.Phase == TaskFailed {
			return cmp.Or(step.Reason, ReasonFailed)
		}
	}
	return ReasonFailed
}

// reportImplementer is the run a merged Task counts under: its newest
// implement run that recorded a harness. Zero when none did.
func reportImplementer(t ReportTask) TaskMetricRun {
	for _, run := range slices.Backward(t.Runs) {
		if run.Role == RoleImplementer && run.Harness != "" {
			return run
		}
	}
	return TaskMetricRun{}
}

// reportShare counts the tasks that are in, out of of.
func reportShare(tasks []ReportTask, of int, in func(ReportTask) bool) ReportShare {
	s := ReportShare{Of: of}
	for _, t := range tasks {
		if in(t) {
			s.Tasks++
		}
	}
	if of > 0 {
		share := float64(s.Tasks) / float64(of)
		s.Share = &share
	}
	return s
}

// reportCost is the median cost of the tasks that reported one.
func reportCost(tasks []ReportTask) ReportCost {
	var costs []float64
	for _, t := range tasks {
		if t.CostMilliUSD > 0 {
			costs = append(costs, float64(t.CostMilliUSD))
		}
	}
	return ReportCost{Tasks: len(costs), MedianMilliUSD: Median(costs)}
}

// reportGroup is the row of one group of merged Tasks.
func reportGroup(key string, tasks []ReportTask) ReportGroup {
	var read []ReportTask
	var toMerge []float64
	for _, t := range tasks {
		if t.PersonCommits != nil {
			read = append(read, t)
		}
		if lead := reportLead(t); lead[LeadToMerge] != nil {
			toMerge = append(toMerge, *lead[LeadToMerge])
		}
	}
	return ReportGroup{
		Key:            key,
		Merged:         len(tasks),
		NoPersonCommit: reportShare(read, len(read), func(t ReportTask) bool { return *t.PersonCommits == 0 }),
		Cost:           reportCost(tasks),
		ToMergeSeconds: Median(toMerge),
	}
}

// reportGroups groups the merged Tasks by key: the most merged first, then by
// key (reportKeyOrder), and the ones that recorded none last.
func reportGroups(merged []ReportTask, key func(ReportTask) string) []ReportGroup {
	by := map[string][]ReportTask{}
	for _, t := range merged {
		k := key(t)
		by[k] = append(by[k], t)
	}
	groups := make([]ReportGroup, 0, len(by))
	for k, ts := range by {
		groups = append(groups, reportGroup(k, ts))
	}
	slices.SortFunc(groups, func(a, b ReportGroup) int {
		return reportKeyOrder(a.Key, b.Key, a.Merged, b.Merged)
	})
	return groups
}

// reportKeyOrder sorts keys by their counts, the larger first, then by name,
// and ReportNotRecorded last. Names are in the order of their code points,
// which Go's string comparison gives and the UI's compareText: "Opus" before
// "haiku", the same on every machine. A locale's order differs from one
// reader's browser to the next.
func reportKeyOrder(a, b string, na, nb int) int {
	last := func(k string) int {
		if k == ReportNotRecorded {
			return 1
		}
		return 0
	}
	return cmp.Or(cmp.Compare(last(a), last(b)), cmp.Compare(nb, na), cmp.Compare(a, b))
}

// reportLead is a merged Task's lead times in seconds, from its first
// Implementing step: to the first Checking step from then on (its pull
// request), to the first Reviewing step, and to when it finished. A time the
// Task's timeline doesn't have is nil; so are all of them for a Task that
// never reached Implementing.
func reportLead(t ReportTask) map[string]*float64 {
	out := map[string]*float64{}
	first := func(phase string, from time.Time) (time.Time, bool) {
		for _, step := range t.History {
			if step.Phase == phase && !step.At.IsZero() && !step.At.Before(from) {
				return step.At, true
			}
		}
		return time.Time{}, false
	}
	start, ok := first(TaskImplementing, time.Time{})
	if !ok {
		return out
	}
	since := func(at time.Time) *float64 {
		s := at.Sub(start).Seconds()
		return &s
	}
	if at, ok := first(TaskChecking, start); ok {
		out[LeadToPullRequest] = since(at)
	}
	if at, ok := first(TaskReviewing, start); ok {
		out[LeadToFirstReview] = since(at)
	}
	if !t.Completed.IsZero() {
		out[LeadToMerge] = since(t.Completed)
	}
	return out
}

// reportStageSeconds is how long a finished Task spent in each stage it was
// in: each step lasts until the next one, and the last until the Task
// finished. A stage none of its steps is in has no entry, which is not zero
// seconds.
func reportStageSeconds(t ReportTask) map[string]float64 {
	out := map[string]float64{}
	for i, step := range t.History {
		stage := reportStageOf(step)
		if stage == "" || step.At.IsZero() {
			continue
		}
		end := step.At
		switch {
		case i+1 < len(t.History):
			if next := t.History[i+1].At; !next.IsZero() {
				end = next
			}
		case !t.Completed.IsZero():
			end = t.Completed
		}
		out[stage] += max(0, end.Sub(step.At).Seconds())
	}
	return out
}

// reportTimes is the merged Tasks' lead times and their time per stage.
func reportTimes(merged []ReportTask) (leads, stages []ReportTime) {
	lead, stage := map[string][]float64{}, map[string][]float64{}
	for _, t := range merged {
		for k, v := range reportLead(t) {
			lead[k] = append(lead[k], *v)
		}
		// A stage a Task never was in is no observation of it, and
		// reportStageSeconds has no entry for it: most Tasks are never
		// blocked, and their zeros would make every median zero. A Task that
		// left a stage in the second it entered was in it, for zero seconds.
		for k, v := range reportStageSeconds(t) {
			stage[k] = append(stage[k], v)
		}
	}
	row := func(s reportStage, values []float64) ReportTime {
		return ReportTime{Key: s.key, Label: s.label, Tasks: len(values),
			MedianSeconds: Median(values), P90Seconds: Percentile(values, 90)}
	}
	for _, s := range reportLeads {
		leads = append(leads, row(s, lead[s.key]))
	}
	for _, s := range reportStages {
		stages = append(stages, row(s, stage[s.key]))
	}
	return leads, stages
}

// reportScorers is each Scorer's pass rates from its Score Tasks that
// finished in period with verdicts: only its newest generation's, whose
// criteria the Score Tasks copied, since one with other criteria would mix
// questions. Scorers are in the order of their namespace and name.
func reportScorers(period ReportPeriod, tasks []ReportTask) []ReportScorer {
	type key struct{ namespace, scorer string }
	by := map[key][]ReportTask{}
	var keys []key
	for _, t := range tasks {
		if t.Kind != taskKindScore || t.Score == nil || !taskFinal(t.Phase) || len(t.Score.Verdicts) == 0 ||
			!period.holds(t.Completed) {
			continue
		}
		k := key{t.Namespace, t.Score.Scorer}
		if by[k] == nil {
			keys = append(keys, k)
		}
		by[k] = append(by[k], t)
	}
	slices.SortFunc(keys, func(a, b key) int {
		return cmp.Compare(a.namespace+"/"+a.scorer, b.namespace+"/"+b.scorer)
	})

	out := make([]ReportScorer, 0, len(keys))
	for _, k := range keys {
		var newest int64
		for _, t := range by[k] {
			newest = max(newest, t.Score.Generation)
		}
		var criteria []string
		scores := map[string][][]ScoreCriterion{}
		var every [][]ScoreCriterion
		for _, t := range by[k] {
			if t.Score.Generation != newest {
				continue
			}
			if every == nil {
				criteria = t.Score.Criteria
			}
			config := ReportConfigKey(t.Score.Harness, t.Score.Model)
			scores[config] = append(scores[config], t.Score.Verdicts)
			every = append(every, t.Score.Verdicts)
		}
		configs := make([]string, 0, len(scores))
		for c := range scores {
			configs = append(configs, c)
		}
		slices.SortFunc(configs, func(a, b string) int {
			return reportKeyOrder(a, b, len(scores[a]), len(scores[b]))
		})

		s := ReportScorer{Namespace: k.namespace, Scorer: k.scorer, Generation: newest, Scores: len(every),
			Configs: configs, Criteria: []ReportCriterion{}}
		for _, c := range TallyScores(criteria, every) {
			s.Criteria = append(s.Criteria, ReportCriterion{Criterion: c.Criterion, All: reportVerdicts("", c)})
		}
		for _, config := range configs {
			for i, c := range TallyScores(criteria, scores[config]) {
				s.Criteria[i].ByConfig = append(s.Criteria[i].ByConfig, reportVerdicts(config, c))
			}
		}
		out = append(out, s)
	}
	return out
}

func reportVerdicts(config string, c ScoreCount) ReportVerdicts {
	v := ReportVerdicts{Config: config, Pass: c.Pass, Fail: c.Fail, Unclear: c.Unclear}
	if judged := c.Pass + c.Fail; judged > 0 {
		rate := float64(c.Pass) / float64(judged)
		v.Rate = &rate
	}
	return v
}

// Median is the middle of values, the mean of the middle two when their
// count is even; nil without values.
func Median(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sorted := slices.Sorted(slices.Values(values))
	mid := len(sorted) / 2
	m := sorted[mid]
	if len(sorted)%2 == 0 {
		m = (sorted[mid-1] + sorted[mid]) / 2
	}
	return &m
}

// Percentile is the p-th percentile of values by nearest rank: the smallest
// value that at least p in a hundred of them are at or under. It is always
// one of the values, so the 90th percentile of ten Tasks is the second
// slowest of them (the ninth from the fastest), a Task a reader can go and
// look at. Nil without values.
func Percentile(values []float64, p int) *float64 {
	if len(values) == 0 {
		return nil
	}
	sorted := slices.Sorted(slices.Values(values))
	rank := (p*len(sorted) + 99) / 100
	v := sorted[min(max(rank, 1), len(sorted))-1]
	return &v
}

// JSON is the report as the document a release publishes: indented, its
// fields in a fixed order, a newline at its end.
func (r Report) JSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
