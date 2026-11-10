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

package chart

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// renderOne renders one template of the chart with the values sets.
func renderOne(t *testing.T, template string, sets ...string) (string, error) {
	t.Helper()
	args := []string{"template", release, chartDir, "--namespace", releaseNS, "-s", template}
	if len(sets) > 0 {
		args = append(args, "--set", strings.Join(sets, ","))
	}
	out, err := exec.Command(helmBinary(t), args...).CombinedOutput()
	return string(out), err
}

// TestChartInstallsTheFactoryDashboard covers #59: with
// prometheus.dashboard.enabled the chart renders a ConfigMap that Grafana's
// sidecar loads, holding the dashboard as JSON, in the namespace the values
// name; without it, nothing.
func TestChartInstallsTheFactoryDashboard(t *testing.T) {
	const template = "templates/prometheus/factory-dashboard.yaml"
	if out, err := renderOne(t, template); err == nil {
		t.Fatalf("the dashboard renders with its value off:\n%s", out)
	}
	out, err := renderOne(t, template, "prometheus.dashboard.enabled=true", "prometheus.dashboard.namespace=monitoring")
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	var cm corev1.ConfigMap
	if err := yaml.Unmarshal([]byte(out), &cm); err != nil {
		t.Fatalf("the dashboard's ConfigMap: %v\n%s", err, out)
	}
	if cm.Namespace != "monitoring" {
		t.Errorf("namespace %q, want the one the values name", cm.Namespace)
	}
	if cm.Labels["grafana_dashboard"] != "1" {
		t.Errorf("labels %v lack grafana_dashboard, which Grafana's sidecar selects", cm.Labels)
	}
	var dashboard struct {
		UID    string `json:"uid"`
		Panels []struct {
			Title   string `json:"title"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal([]byte(cm.Data["theos-factory.json"]), &dashboard); err != nil {
		t.Fatalf("theos-factory.json is not JSON: %v", err)
	}
	if dashboard.UID != "theos-factory" || len(dashboard.Panels) < 8 {
		t.Errorf("dashboard %q with %d panels", dashboard.UID, len(dashboard.Panels))
	}
	for _, p := range dashboard.Panels {
		if len(p.Targets) == 0 || !strings.Contains(p.Targets[0].Expr, "theos_") {
			t.Errorf("panel %q queries no theos_ series", p.Title)
		}
	}
}

// TestChartInstallsTheFactoryAlerts covers #59: with prometheus.rules.enabled
// the chart renders a PrometheusRule whose annotations reach Prometheus with
// their own {{ }} templates intact, the cost alert at the values' budget, and
// no cost alert when the budget is 0.
func TestChartInstallsTheFactoryAlerts(t *testing.T) {
	const template = "templates/prometheus/factory-rules.yaml"
	if out, err := renderOne(t, template); err == nil {
		t.Fatalf("the rules render with their value off:\n%s", out)
	}
	alerts := func(sets ...string) map[string]map[string]any {
		t.Helper()
		out, err := renderOne(t, template, append([]string{"prometheus.rules.enabled=true"}, sets...)...)
		if err != nil {
			t.Fatalf("helm template: %v\n%s", err, out)
		}
		var rule struct {
			Kind string `json:"kind"`
			Spec struct {
				Groups []struct {
					Rules []map[string]any `json:"rules"`
				} `json:"groups"`
			} `json:"spec"`
		}
		if err := yaml.Unmarshal([]byte(out), &rule); err != nil {
			t.Fatalf("the PrometheusRule: %v\n%s", err, out)
		}
		if rule.Kind != "PrometheusRule" || len(rule.Spec.Groups) != 1 {
			t.Fatalf("kind %q with %d groups", rule.Kind, len(rule.Spec.Groups))
		}
		byName := map[string]map[string]any{}
		for _, r := range rule.Spec.Groups[0].Rules {
			byName[r["alert"].(string)] = r
		}
		return byName
	}

	got := alerts("prometheus.rules.dailyBudgetUSD=120", "prometheus.rules.specWaits=4h")
	for _, name := range []string{"TheosDailyCostOverBudget", "TheosTasksFailing", "TheosSpecWaitsForAPerson",
		"TheosRunsWithoutCost"} {
		if got[name] == nil {
			t.Errorf("no alert %s", name)
		}
	}
	if expr, _ := got["TheosDailyCostOverBudget"]["expr"].(string); !strings.HasSuffix(expr, "> 120") {
		t.Errorf("the cost alert's expression %q doesn't use the values' budget", expr)
	}
	if f, _ := got["TheosSpecWaitsForAPerson"]["for"].(string); f != "4h" {
		t.Errorf("the spec alert waits %q, want the values' 4h", f)
	}
	summary, _ := got["TheosTasksFailing"]["annotations"].(map[string]any)["summary"].(string)
	if !strings.Contains(summary, "{{ $labels.namespace }}") {
		t.Errorf("the summary %q lost Prometheus's own template", summary)
	}

	without := alerts("prometheus.rules.dailyBudgetUSD=0")
	if without["TheosDailyCostOverBudget"] != nil || len(without) != 3 {
		t.Errorf("a budget of 0 leaves the cost alert out and keeps the rest: got %d alerts", len(without))
	}
}
