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
	"github.com/JairJosafath/theos/internal/taskfacts"
)

// FactoryMetrics serves the factory's numbers on the manager's /metrics
// (#59, docs/design.md, "Measuring the factory"): at each scrape it lists the
// Tasks from the manager's cache, reads each as theos-session report does
// (taskfacts.Metric, #142) and hands model.FactoryMetrics' families to
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
		facts = append(facts, taskfacts.Metric(&tasks.Items[i]))
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
