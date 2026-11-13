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
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// TestFactoryMetricsServesTheTasksNumbers covers #59: a scrape of the
// manager's registry gives the factory's numbers from the Tasks that exist,
// and a second scrape (or a restarted manager's first) gives the same ones.
func TestFactoryMetricsServesTheTasksNumbers(t *testing.T) {
	t0 := metav1.NewTime(time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC))
	later := func(minutes int) metav1.Time { return metav1.NewTime(t0.Add(time.Duration(minutes) * time.Minute)) }
	none := int32(0)
	cost := int64(1250)
	merged := &c4v1beta1.Task{
		ObjectMeta: metav1.ObjectMeta{Namespace: "webshop", Name: "orders-api-7"},
		Spec:       c4v1beta1.TaskSpec{ContainerRef: c4v1beta1.LocalRef{Name: "orders-api"}},
		Status: c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskMerged, Round: 2, PersonCommits: &none, CostMilliUSD: 1250,
			// #92: its Crew's approval rules held it, and ada approved it.
			Approval: &c4v1beta1.TaskApproval{Require: c4v1beta1.ApprovalPerson, Rule: 1, ApprovedBy: "ada"},
			History: []c4v1beta1.TaskTransition{{At: t0, Phase: c4v1beta1.TaskImplementing},
				{At: later(10), Phase: c4v1beta1.TaskReviewing}, {At: later(15), Phase: c4v1beta1.TaskMerged}},
			Runs: []c4v1beta1.TaskAgentRun{{Round: 1, Role: "implement", Agent: "implementer", Harness: "claude-code",
				Model: "claude-sonnet-5-5", EndedAt: later(10), CostMilliUSD: &cost,
				Tokens: &c4v1beta1.TokenUsage{Input: 100, Output: 20}}}},
	}
	failed := &c4v1beta1.Task{
		ObjectMeta: metav1.ObjectMeta{Namespace: "webshop", Name: "orders-api-8"},
		Spec:       c4v1beta1.TaskSpec{Kind: c4v1beta1.TaskImplement, ContainerRef: c4v1beta1.LocalRef{Name: "orders-api"}},
		Status: c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskFailed, Round: 5,
			History: []c4v1beta1.TaskTransition{{At: t0, Phase: c4v1beta1.TaskImplementing}, {At: later(600), Phase: c4v1beta1.TaskFailed}}},
	}
	benchmark := &c4v1beta1.Task{
		ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "bench-a-1"},
		Spec:       c4v1beta1.TaskSpec{Benchmark: &c4v1beta1.TaskBenchmarkSpec{}},
		Status:     c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskApproved},
	}
	c := fake.NewClientBuilder().WithScheme(briefScheme(t)).WithObjects(merged, failed, benchmark).Build()

	reg := prometheus.NewPedanticRegistry()
	if err := reg.Register(FactoryMetrics{Reader: c}); err != nil {
		t.Fatal(err)
	}
	want := `
# HELP theos_tasks Tasks that exist, by project, Container, kind and phase.
# TYPE theos_tasks gauge
theos_tasks{container="none",kind="Benchmark",namespace="lab",phase="Approved"} 1
theos_tasks{container="orders-api",kind="Implement",namespace="webshop",phase="Failed"} 1
theos_tasks{container="orders-api",kind="Implement",namespace="webshop",phase="Merged"} 1
# HELP theos_tasks_merged Merged Tasks that exist, by who wrote the change's code (factory: no person's commit, person or unknown) and who approved it (person, reviewer or none).
# TYPE theos_tasks_merged gauge
theos_tasks_merged{approved="person",code="factory",container="orders-api",kind="Implement",namespace="webshop"} 1
# HELP theos_tasks_cost_usd What the agent runs of the Tasks that exist cost, in US dollars, as the agents reported it.
# TYPE theos_tasks_cost_usd gauge
theos_tasks_cost_usd{container="orders-api",kind="Implement",namespace="webshop"} 1.25
# HELP theos_agent_runs Ended agent runs on the Tasks that exist (the newest 64 of each), by Agent, harness, model and role.
# TYPE theos_agent_runs gauge
theos_agent_runs{agent="implementer",harness="claude-code",model="claude-sonnet-5-5",namespace="webshop",role="implement"} 1
# HELP theos_agent_runs_cost_usd What those agent runs cost, in US dollars.
# TYPE theos_agent_runs_cost_usd gauge
theos_agent_runs_cost_usd{agent="implementer",harness="claude-code",model="claude-sonnet-5-5",namespace="webshop",role="implement"} 1.25
# HELP theos_agent_runs_tokens Tokens those agent runs used, by type: input, output, cache_read or cache_write.
# TYPE theos_agent_runs_tokens gauge
theos_agent_runs_tokens{agent="implementer",harness="claude-code",model="claude-sonnet-5-5",namespace="webshop",role="implement",type="input"} 100
theos_agent_runs_tokens{agent="implementer",harness="claude-code",model="claude-sonnet-5-5",namespace="webshop",role="implement",type="output"} 20
`
	gauges := []string{"theos_tasks", "theos_tasks_merged", "theos_tasks_cost_usd", "theos_agent_runs",
		"theos_agent_runs_cost_usd", "theos_agent_runs_tokens"}
	for scrape := 1; scrape <= 2; scrape++ {
		if err := testutil.GatherAndCompare(reg, strings.NewReader(want), gauges...); err != nil {
			t.Errorf("scrape %d: %v", scrape, err)
		}
	}

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	histograms := map[string]uint64{}
	for _, f := range families {
		for _, m := range f.GetMetric() {
			if h := m.GetHistogram(); h != nil {
				key := f.GetName()
				for _, l := range m.GetLabel() {
					if l.GetName() == "outcome" || l.GetName() == "phase" {
						key += "/" + l.GetValue()
					}
				}
				histograms[key] = h.GetSampleCount()
			}
		}
	}
	for key, want := range map[string]uint64{
		"theos_task_seconds/Merged": 1, "theos_task_seconds/Failed": 1,
		"theos_task_rounds/Merged": 1, "theos_task_rounds/Failed": 1,
		"theos_task_phase_seconds/Implementing": 2, "theos_task_phase_seconds/Reviewing": 1,
	} {
		if histograms[key] != want {
			t.Errorf("%s: got %d observations, want %d (all: %v)", key, histograms[key], want, histograms)
		}
	}
}

// TestFactoryMetricsCountAChangeByHowTheTaskEnded covers #142: the series
// read a Task through taskfacts.Metric, as theos-session report does, so a
// Task that ended done counts as a merged change for every reason the
// operator writes on its Succeeded condition but NothingToDo (#72).
//
// The facts are the same for both; the count is not yet. The
// DoneByStandardChange case pins today's count of the series, a merged
// change: theos-session report counts such a Task only when its
// status.pullRequest is set. Both rules are internal/model's.
func TestFactoryMetricsCountAChangeByHowTheTaskEnded(t *testing.T) {
	ended := func(name, reason string) *c4v1beta1.Task {
		return &c4v1beta1.Task{
			ObjectMeta: metav1.ObjectMeta{Namespace: "webshop", Name: name},
			Spec:       c4v1beta1.TaskSpec{ContainerRef: c4v1beta1.LocalRef{Name: "orders-api"}},
			Status: c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskMerged, Conditions: []metav1.Condition{
				{Type: ConditionSucceeded, Status: metav1.ConditionTrue, Reason: reason}}},
		}
	}
	// orders-api-2 has no pull request: a merged change of the series today,
	// and in no number of the report.
	c := fake.NewClientBuilder().WithScheme(briefScheme(t)).WithObjects(ended("orders-api-1", "Merged"),
		ended("orders-api-2", reasonDoneByStandardChange), ended("orders-api-3", model.ReasonNothingToDo)).Build()
	g := gomega.NewWithT(t)
	tasks := map[string]string{"namespace": "webshop", "container": "orders-api", "kind": "Implement"}
	merged := maps.Clone(tasks)
	merged["code"], merged["approved"] = model.CodeUnknown, model.ApprovedReviewer
	g.Expect(factoryMetricValue(g, c, model.MetricTasksMerged, merged)).To(gomega.Equal(2.0),
		"the Task that ended with nothing to do is no merged change, and the two others are")
	tasks["phase"] = "Merged"
	g.Expect(factoryMetricValue(g, c, model.MetricTasks, tasks)).To(gomega.Equal(3.0))
}

// factoryMetricValue scrapes a fresh registry with FactoryMetrics on reader and
// returns the gauge name with exactly these labels; 0 when no such sample.
func factoryMetricValue(g gomega.Gomega, reader client.Reader, name string, labels map[string]string) float64 {
	reg := prometheus.NewPedanticRegistry()
	g.Expect(reg.Register(FactoryMetrics{Reader: reader})).To(gomega.Succeed())
	families, err := reg.Gather()
	g.Expect(err).NotTo(gomega.HaveOccurred())
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			got := map[string]string{}
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			if maps.Equal(got, labels) {
				return m.GetGauge().GetValue()
			}
		}
	}
	return 0
}
