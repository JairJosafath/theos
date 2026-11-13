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
	"cmp"
	"slices"
	"strings"
	"time"
)

// The factory's numbers as metric families a Prometheus scrapes (#59). They
// are computed from the Tasks that exist, at the scrape, as the UI's "How
// well it works" is: nothing is counted at a transition, so a restart counts
// nothing twice, and a deleted Task leaves the sums. They are gauges for
// that reason, not counters.
const (
	// MetricTasks counts Tasks by project, Container, kind and phase.
	MetricTasks = "theos_tasks"
	// MetricTasksMerged counts merged Tasks by who wrote the change's code.
	MetricTasksMerged = "theos_tasks_merged"
	// MetricTasksCostUSD sums what the Tasks' agent runs cost.
	MetricTasksCostUSD = "theos_tasks_cost_usd"
	// MetricTaskSeconds is how long finished Tasks took, first step to last.
	MetricTaskSeconds = "theos_task_seconds"
	// MetricTaskPhaseSeconds is how long Tasks stayed in each phase.
	MetricTaskPhaseSeconds = "theos_task_phase_seconds"
	// MetricTaskRounds is how many implement rounds finished Tasks took.
	MetricTaskRounds = "theos_task_rounds"
	// MetricAgentRuns counts ended agent runs by Agent, harness, model and role.
	MetricAgentRuns = "theos_agent_runs"
	// MetricAgentRunsCostUSD sums what those runs cost.
	MetricAgentRunsCostUSD = "theos_agent_runs_cost_usd"
	// MetricAgentRunsWithoutCost counts the runs whose agent reported no cost.
	MetricAgentRunsWithoutCost = "theos_agent_runs_without_cost"
	// MetricAgentRunsTokens sums the tokens those runs used, by type.
	MetricAgentRunsTokens = "theos_agent_runs_tokens"
)

// Who wrote a merged Task's code, the "code" label of MetricTasksMerged.
const (
	// CodeFactory: no commit of the pull request was a person's.
	CodeFactory = "factory"
	// CodePerson: at least one commit was a person's.
	CodePerson = "person"
	// CodeUnknown: the Git host wasn't read when the Task merged.
	CodeUnknown = "unknown"
)

// Who let a merged Task's change in, the "approved" label of
// MetricTasksMerged (#92): a team that starts small, with its Crew's
// approval rules holding changes for a person, sees there what it could hand
// over next.
const (
	// ApprovedPerson: a person approved the pull request, a spec's at its
	// review point or a change's its Crew's approval rules held.
	ApprovedPerson = "person"
	// ApprovedReviewer: the reviewer Agent's approval, and the verifier's
	// where the Task has one, merged it.
	ApprovedReviewer = "reviewer"
	// ApprovedNone: nobody reviewed it. Its Crew's approval rules asked for
	// no review, or the Task is of a kind that has none (a plan, a triage).
	ApprovedNone = MetricNone
)

// KindBenchmark is the metricLabelKind label of a Benchmark's Tasks: they are
// Implement Tasks that never merge, and counting them as work would bend
// every rate.
const KindBenchmark = "Benchmark"

// metricLabelKind is the label that carries a Task's kind.
const metricLabelKind = "kind"

// metricLabelNamespace is the label that carries a Task's project.
const metricLabelNamespace = "namespace"

// MetricNone stands for a label value the Task doesn't have: a run from
// before its Agent was recorded, a Task of no Container.
const MetricNone = "none"

// Histogram buckets. Seconds run from a minute to a week: a triage takes
// minutes, a spec may wait days for a person.
var (
	TaskSecondsBuckets = []float64{60, 300, 900, 1800, 3600, 7200, 14400, 28800, 86400, 259200, 604800}
	TaskRoundsBuckets  = []float64{1, 2, 3, 4, 5, 8}
)

// TaskMetricFacts is what the metrics read of one Task.
type TaskMetricFacts struct {
	Namespace string
	Container string
	Kind      string
	// Benchmark is true for a Benchmark's Task.
	Benchmark bool
	Phase     string
	Round     int
	// PersonCommits is the merged pull request's commits a person wrote; nil
	// when it wasn't read.
	PersonCommits *int
	// ApprovedByPerson means a person approved the merged pull request, and
	// Unreviewed that no agent reviewed it, as its Crew's approval rules
	// asked.
	ApprovedByPerson bool
	Unreviewed       bool
	// NoChange means the Task ended done with nothing merged: its agent found
	// its issue needs no change (#72). It is no merged change.
	NoChange bool
	// PullRequest is the Task's own pull request, the newest it opened: the
	// one that merged, or, for a Task DoneByStandardChange, one closed in an
	// earlier round. 0 when it has none.
	PullRequest int
	// DoneByStandardChange means the Task ended because its Standard change
	// merged and left its issue nothing to do (ReasonDoneByStandardChange),
	// and StandardChange is that change's issue number, from the Task's
	// record of it; 0 when it has none. MergedChange reads them (#204).
	DoneByStandardChange bool
	StandardChange       int
	CostMilliUSD         int64
	// History is the Task's timeline, oldest first.
	History []TaskMetricStep
	Runs    []TaskMetricRun
}

// TaskMetricStep is one step of a Task's timeline: from At on it was in Phase.
type TaskMetricStep struct {
	At    time.Time
	Phase string
	// Reason is the step's reason, which the metric families don't read and
	// the report does (#66): WaitingForSlot is a stage of its own there, and
	// a Failed step's reason is why the Task failed.
	Reason string
}

// TaskMetricRun is one ended agent run.
type TaskMetricRun struct {
	Agent, Harness, Model, Role string
	// CostMilliUSD is nil when the agent reported no cost.
	CostMilliUSD *int64
	Input, Output,
	CacheRead, CacheWrite int64
}

// MetricFamily is one metric: its samples (gauges) or its histograms.
type MetricFamily struct {
	Name   string
	Help   string
	Labels []string
	// Samples are a gauge family's values, sorted by their label values.
	Samples []MetricSample
	// Histograms are a histogram family's, sorted the same; Buckets are their
	// upper bounds.
	Buckets    []float64
	Histograms []MetricHistogram
}

// MetricSample is one gauge value; Values are in the family's Labels' order.
type MetricSample struct {
	Values []string
	Value  float64
}

// MetricHistogram is one histogram; Counts holds, per bucket, how many
// observations were at or under its bound.
type MetricHistogram struct {
	Values []string
	Count  uint64
	Sum    float64
	Counts []uint64
}

// taskFinal says whether phase ends a Task. Approved ends a Benchmark's.
func taskFinal(phase string) bool {
	return phase == TaskMerged || phase == TaskFailed || phase == "Approved"
}

// FactoryMetrics turns the Tasks into the factory's metric families, in a
// fixed order. Label values are bounded by what a cluster holds (projects,
// Containers, Agents, models): no Task name and no issue number is a label.
func FactoryMetrics(tasks []TaskMetricFacts) []MetricFamily {
	gauges := map[string]map[string]float64{}
	add := func(name string, v float64, values ...string) {
		if gauges[name] == nil {
			gauges[name] = map[string]float64{}
		}
		gauges[name][strings.Join(values, "\x00")] += v
	}
	type hist struct {
		buckets []float64
		obs     map[string][]float64
	}
	hists := map[string]*hist{
		MetricTaskSeconds:      {buckets: TaskSecondsBuckets, obs: map[string][]float64{}},
		MetricTaskPhaseSeconds: {buckets: TaskSecondsBuckets, obs: map[string][]float64{}},
		MetricTaskRounds:       {buckets: TaskRoundsBuckets, obs: map[string][]float64{}},
	}
	observe := func(name string, v float64, values ...string) {
		k := strings.Join(values, "\x00")
		hists[name].obs[k] = append(hists[name].obs[k], v)
	}
	orNone := func(s string) string {
		if s == "" {
			return MetricNone
		}
		return s
	}

	for _, t := range tasks {
		kind := orNone(t.Kind)
		if t.Benchmark {
			kind = KindBenchmark
		}
		container := orNone(t.Container)
		phase := t.Phase
		if phase == "" {
			phase = TaskPending
		}
		add(MetricTasks, 1, t.Namespace, container, kind, phase)
		if t.CostMilliUSD != 0 {
			add(MetricTasksCostUSD, float64(t.CostMilliUSD)/1000, t.Namespace, container, kind)
		}
		if MergedChange(t) {
			code := CodeUnknown
			if t.PersonCommits != nil {
				code = CodeFactory
				if *t.PersonCommits > 0 {
					code = CodePerson
				}
			}
			add(MetricTasksMerged, 1, t.Namespace, container, kind, code, mergedApproval(t))
		}

		// Each step lasts until the next one; the last step is where the Task
		// is now, so only a finished Task's whole time is known.
		for i := 0; i+1 < len(t.History); i++ {
			step, next := t.History[i], t.History[i+1]
			if d := next.At.Sub(step.At).Seconds(); d >= 0 && step.Phase != "" && !taskFinal(step.Phase) {
				observe(MetricTaskPhaseSeconds, d, t.Namespace, kind, step.Phase)
			}
		}
		if taskFinal(phase) {
			if n := len(t.History); n > 0 {
				if d := t.History[n-1].At.Sub(t.History[0].At).Seconds(); d >= 0 {
					observe(MetricTaskSeconds, d, t.Namespace, kind, phase)
				}
			}
			if t.Round > 0 {
				observe(MetricTaskRounds, float64(t.Round), t.Namespace, kind, phase)
			}
		}

		for _, r := range t.Runs {
			labels := []string{t.Namespace, orNone(r.Agent), orNone(r.Harness), orNone(r.Model), orNone(r.Role)}
			add(MetricAgentRuns, 1, labels...)
			if r.CostMilliUSD == nil {
				add(MetricAgentRunsWithoutCost, 1, labels...)
			} else {
				add(MetricAgentRunsCostUSD, float64(*r.CostMilliUSD)/1000, labels...)
			}
			for _, tok := range []struct {
				kind string
				n    int64
			}{{"input", r.Input}, {"output", r.Output}, {"cache_read", r.CacheRead}, {"cache_write", r.CacheWrite}} {
				if tok.n != 0 {
					add(MetricAgentRunsTokens, float64(tok.n), append(slices.Clone(labels), tok.kind)...)
				}
			}
		}
	}

	task := []string{metricLabelNamespace, "container", metricLabelKind}
	run := []string{metricLabelNamespace, "agent", "harness", "model", "role"}
	families := []MetricFamily{
		{Name: MetricTasks, Labels: append(slices.Clone(task), "phase"),
			Help: "Tasks that exist, by project, Container, kind and phase."},
		{Name: MetricTasksMerged, Labels: append(slices.Clone(task), "code", "approved"),
			Help: "Merged Tasks that exist, by who wrote the change's code (factory: no person's commit, person or " +
				"unknown) and who approved it (person, reviewer or none)."},
		{Name: MetricTasksCostUSD, Labels: task,
			Help: "What the agent runs of the Tasks that exist cost, in US dollars, as the agents reported it."},
		{Name: MetricTaskSeconds, Labels: []string{metricLabelNamespace, metricLabelKind, "outcome"},
			Help: "How long each finished Task that exists took, from its first step to its last, in seconds."},
		{Name: MetricTaskPhaseSeconds, Labels: []string{metricLabelNamespace, metricLabelKind, "phase"},
			Help: "How long the Tasks that exist stayed in a phase, per stay that ended, in seconds, from the newest 64 steps of each."},
		{Name: MetricTaskRounds, Labels: []string{metricLabelNamespace, metricLabelKind, "outcome"},
			Help: "The implement rounds each finished Task that exists took."},
		{Name: MetricAgentRuns, Labels: run,
			Help: "Ended agent runs on the Tasks that exist (the newest 64 of each), by Agent, harness, model and role."},
		{Name: MetricAgentRunsCostUSD, Labels: run,
			Help: "What those agent runs cost, in US dollars."},
		{Name: MetricAgentRunsWithoutCost, Labels: run,
			Help: "Those agent runs whose agent reported no cost; theos_agent_runs_cost_usd leaves them out."},
		{Name: MetricAgentRunsTokens, Labels: append(slices.Clone(run), "type"),
			Help: "Tokens those agent runs used, by type: input, output, cache_read or cache_write."},
	}
	for i := range families {
		f := &families[i]
		if h, ok := hists[f.Name]; ok {
			f.Buckets = h.buckets
			for k, obs := range h.obs {
				mh := MetricHistogram{Values: strings.Split(k, "\x00"), Count: uint64(len(obs)), Counts: make([]uint64, len(h.buckets))}
				for _, v := range obs {
					mh.Sum += v
					for b, bound := range h.buckets {
						if v <= bound {
							mh.Counts[b]++
						}
					}
				}
				f.Histograms = append(f.Histograms, mh)
			}
			slices.SortFunc(f.Histograms, func(a, b MetricHistogram) int { return slices.Compare(a.Values, b.Values) })
			continue
		}
		for k, v := range gauges[f.Name] {
			f.Samples = append(f.Samples, MetricSample{Values: strings.Split(k, "\x00"), Value: v})
		}
		slices.SortFunc(f.Samples, func(a, b MetricSample) int {
			return cmp.Or(slices.Compare(a.Values, b.Values), cmp.Compare(a.Value, b.Value))
		})
	}
	return families
}

// mergedApproval is who let a merged Task's change in, the "approved" label:
// a person, the reviewer, or nobody.
func mergedApproval(t TaskMetricFacts) string {
	reviewed := t.Kind == "" || t.Kind == chatKindImplement || t.Kind == chatKindSpec
	switch {
	case t.ApprovedByPerson:
		return ApprovedPerson
	case t.Unreviewed || !reviewed || t.Benchmark:
		return ApprovedNone
	}
	return ApprovedReviewer
}
