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
	"strings"
	"testing"
)

// measure-and-improve, step 6: the runs go repetition by repetition, so a
// Benchmark stopped early compared its variants on the same goals.
func TestBenchmarkRuns(t *testing.T) {
	got := BenchmarkRuns(2, []string{"a", "b"}, 2)
	want := []BenchmarkRun{
		{"a", 0, 1}, {"b", 0, 1}, {"a", 1, 1}, {"b", 1, 1},
		{"a", 0, 2}, {"b", 0, 2}, {"a", 1, 2}, {"b", 1, 2},
	}
	if len(got) != len(want) {
		t.Fatalf("BenchmarkRuns = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("run %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if got := BenchmarkTaskName("cmp", BenchmarkRun{"fast", 0, 3}); got != "cmp-fast-g1-r3" {
		t.Errorf("name = %q", got)
	}
	long := BenchmarkTaskName("a-benchmark-with-a-long-name", BenchmarkRun{"variant-long", 9, 5})
	if len(long) > 40 || len(SandboxName(long, 3, 0, RoleReviewer, 2, 1)) > 63 {
		t.Errorf("a long name %q, or its sandbox's, is too long", long)
	}
}

// The 95% Wilson interval, against published values.
func TestWilson(t *testing.T) {
	for _, tt := range []struct {
		k, n        int
		lo, hi, pct int
	}{
		{0, 0, 0, 100, 0},
		{1, 1, 21, 100, 100},
		{0, 1, 0, 79, 0},
		{5, 10, 24, 76, 50},
		{18, 20, 70, 97, 90},
		{2, 20, 3, 30, 10},
	} {
		r := NewRate(tt.k, tt.n)
		if r.Low != tt.lo || r.High != tt.hi || r.Percent != tt.pct {
			t.Errorf("NewRate(%d, %d) = %+v, want %d%% (%d to %d)", tt.k, tt.n, r, tt.pct, tt.lo, tt.hi)
		}
	}
}

// One variant is ahead only when the intervals don't overlap.
func TestCompareVariants(t *testing.T) {
	if got := CompareVariants([]VariantRate{{"a", NewRate(0, 0)}, {"b", NewRate(0, 0)}}); got != "No run has finished yet." {
		t.Errorf("nothing finished: %q", got)
	}
	close := CompareVariants([]VariantRate{{"a", NewRate(4, 5)}, {"b", NewRate(2, 5)}})
	if close != "No clear difference at 10 finished runs: the approval rates' 95% intervals overlap." {
		t.Errorf("overlapping: %q", close)
	}
	apart := CompareVariants([]VariantRate{{"a", NewRate(18, 20)}, {"b", NewRate(2, 20)}})
	if !strings.HasPrefix(apart, "Approval rate: a (90%, 70 to 97%) is ahead of b (10%, 3 to 30%)") {
		t.Errorf("apart: %q", apart)
	}
	if !strings.Contains(BenchmarkPullTitle("cmp", "fast", 0, 2, "Add crop"), "(never merges)") {
		t.Error("the pull request's title doesn't say it never merges")
	}
}
