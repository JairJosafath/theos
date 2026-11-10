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

package controller

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// FactoryMetrics serves the factory's numbers on the manager's /metrics
// (#59, docs/design.md, "Measuring the factory"): at each scrape it lists the
// Tasks from the manager's cache and hands model.FactoryMetrics' families to
// Prometheus. It keeps nothing between scrapes, so a restart counts nothing
// twice.
type FactoryMetrics struct {
	// Reader lists the Tasks; the manager's cached client, so a scrape costs
	// the API server nothing.
	Reader client.Reader
	// Timeout bounds one scrape's list; zero means 10 seconds.
	Timeout time.Duration
}

// Describe sends nothing: the label values come from the Tasks, so the
// collector is unchecked, as client_golang calls it.
func (FactoryMetrics) Describe(chan<- *prometheus.Desc) {}

// Collect lists the Tasks and sends their metrics. A failed list sends
// nothing and logs why; the scrape still answers with the other collectors.
func (m FactoryMetrics) Collect(ch chan<- prometheus.Metric) {
	timeout := m.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var tasks c4v1beta1.TaskList
	if err := m.Reader.List(ctx, &tasks); err != nil {
		logf.Log.WithName("factory-metrics").Error(err, "Listing the Tasks failed; the scrape has no factory metrics")
		return
	}
	facts := make([]model.TaskMetricFacts, 0, len(tasks.Items))
	for i := range tasks.Items {
		facts = append(facts, taskMetricFacts(&tasks.Items[i]))
	}
	for _, f := range model.FactoryMetrics(facts) {
		desc := prometheus.NewDesc(f.Name, f.Help, f.Labels, nil)
		for _, s := range f.Samples {
			ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, s.Value, s.Values...)
		}
		for _, h := range f.Histograms {
			buckets := make(map[float64]uint64, len(f.Buckets))
			for i, bound := range f.Buckets {
				buckets[bound] = h.Counts[i]
			}
			ch <- prometheus.MustNewConstHistogram(desc, h.Count, h.Sum, buckets, h.Values...)
		}
	}
}

// taskMetricFacts is what the metrics read of task.
func taskMetricFacts(task *c4v1beta1.Task) model.TaskMetricFacts {
	f := model.TaskMetricFacts{
		Namespace:    task.Namespace,
		Container:    task.Spec.ContainerRef.Name,
		Kind:         string(task.Spec.Kind),
		Benchmark:    task.Spec.Benchmark != nil,
		Phase:        string(task.Status.Phase),
		Round:        int(task.Status.Round),
		CostMilliUSD: task.Status.CostMilliUSD,
	}
	if f.Kind == "" {
		f.Kind = string(c4v1beta1.TaskImplement)
	}
	if n := task.Status.PersonCommits; n != nil {
		commits := int(*n)
		f.PersonCommits = &commits
	}
	// Who let the change in (#92): a person, at a spec's review point or
	// where the Crew's approval rules held it, or nobody, where they asked
	// for no review and none was made.
	spec, approval := task.Status.Spec, task.Status.Approval
	f.ApprovedByPerson = spec != nil && spec.ApprovedBy != "" || approval != nil && approval.ApprovedBy != ""
	f.Unreviewed = approval != nil && approval.Require == c4v1beta1.ApprovalNone && task.Status.Review == nil
	for _, step := range task.Status.History {
		f.History = append(f.History, model.TaskMetricStep{At: step.At.Time, Phase: string(step.Phase)})
	}
	for _, run := range task.Status.Runs {
		r := model.TaskMetricRun{Agent: run.Agent, Harness: run.Harness, Model: run.Model, Role: run.Role,
			CostMilliUSD: run.CostMilliUSD}
		if t := run.Tokens; t != nil {
			r.Input, r.Output, r.CacheRead, r.CacheWrite = t.Input, t.Output, t.CacheRead, t.CacheWrite
		}
		f.Runs = append(f.Runs, r)
	}
	return f
}
