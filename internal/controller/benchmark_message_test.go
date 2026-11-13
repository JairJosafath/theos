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

import "testing"

// #180: the Benchmark's over-budget message writes the budget as it writes the cost.
func TestBenchmarkOverBudgetMessage(t *testing.T) {
	for _, c := range []struct {
		cost         int64
		budget, a, b int
		want         string
	}{
		{30120, 30, 2, 6, "Its runs cost $30.12, its budgetUSD of $30.00: 2 of 6 runs made"},
		{5000, 5, 1, 2, "Its runs cost $5.00, its budgetUSD of $5.00: 1 of 2 runs made"},
	} {
		if got := benchmarkOverBudgetMessage(c.cost, c.budget, c.a, c.b); got != c.want {
			t.Errorf("#180 benchmarkOverBudgetMessage(%d, %d) = %q, want %q", c.cost, c.budget, got, c.want)
		}
	}
}
