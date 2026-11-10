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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
)

// Benchmarks (docs/plans/done/measure-and-improve.md, step 6): the same goals,
// from one pinned commit, under two or more variants, each repeated; their
// Tasks end Approved instead of merging, and the Benchmark compares what came
// out with the uncertainty said.

// TaskApproved is the phase a Benchmark's Task ends in when its change
// passed its checks and its review: it would have merged.
const TaskApproved = "Approved"

// BenchmarkRun is one Task a Benchmark makes.
type BenchmarkRun struct {
	Variant    string
	Goal       int
	Repetition int
}

// BenchmarkRuns are every run of goals goals under variants, repetitions
// each, in the order they start: repetition by repetition, goal by goal,
// each variant in turn, so a Benchmark stopped early (at its budget) has
// compared the variants on the same goals.
func BenchmarkRuns(goals int, variants []string, repetitions int) []BenchmarkRun {
	out := make([]BenchmarkRun, 0, goals*len(variants)*repetitions)
	for r := 1; r <= repetitions; r++ {
		for g := range goals {
			for _, v := range variants {
				out = append(out, BenchmarkRun{Variant: v, Goal: g, Repetition: r})
			}
		}
	}
	return out
}

// BenchmarkTaskName is the name of the Task of run r of Benchmark bench: one
// per run, so a second create fails. A long one is cut and told apart by a
// hash of the whole, leaving room in its sandbox's name.
func BenchmarkTaskName(bench string, r BenchmarkRun) string {
	const maxName = 40
	name := fmt.Sprintf("%s-%s-g%d-r%d", bench, r.Variant, r.Goal+1, r.Repetition)
	if len(name) <= maxName {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	return strings.TrimRight(name[:maxName-9], "-") + "-" + hex.EncodeToString(sum[:])[:8]
}

// Wilson is the 95% Wilson score interval of k successes in n tries: lo and
// hi between 0 and 1; 0 and 1 when n is 0, as nothing is known.
func Wilson(k, n int) (lo, hi float64) {
	if n <= 0 {
		return 0, 1
	}
	const z = 1.959963984540054
	p := float64(k) / float64(n)
	nf := float64(n)
	denom := 1 + z*z/nf
	center := (p + z*z/(2*nf)) / denom
	half := z * math.Sqrt(p*(1-p)/nf+z*z/(4*nf*nf)) / denom
	return math.Max(0, center-half), math.Min(1, center+half)
}

// Rate is a share of n with its 95% Wilson interval, in whole percent.
type Rate struct {
	K, N               int
	Percent, Low, High int
}

// NewRate is k of n as a Rate.
func NewRate(k, n int) Rate {
	lo, hi := Wilson(k, n)
	r := Rate{K: k, N: n, Low: int(math.Round(lo * 100)), High: int(math.Round(hi * 100))}
	if n > 0 {
		r.Percent = int(math.Round(float64(k) / float64(n) * 100))
	}
	return r
}

// VariantRate is a variant's approval rate.
type VariantRate struct {
	Name string
	Rate Rate
}

// CompareVariants says in a sentence what the variants' approval rates show:
// one is ahead of another only when their intervals don't overlap; else no
// clear difference, at how many runs. Nothing finished says so.
func CompareVariants(variants []VariantRate) string {
	runs := 0
	for _, v := range variants {
		runs += v.Rate.N
	}
	if runs == 0 {
		return "No run has finished yet."
	}
	var ahead []string
	for _, a := range variants {
		for _, b := range variants {
			if a.Rate.N > 0 && b.Rate.N > 0 && a.Name != b.Name && a.Rate.Low > b.Rate.High {
				ahead = append(ahead, fmt.Sprintf("%s (%d%%, %d to %d%%) is ahead of %s (%d%%, %d to %d%%)", a.Name,
					a.Rate.Percent, a.Rate.Low, a.Rate.High, b.Name, b.Rate.Percent, b.Rate.Low, b.Rate.High))
			}
		}
	}
	if len(ahead) > 0 {
		return "Approval rate: " + strings.Join(ahead, "; ") + ", their 95% intervals apart."
	}
	return fmt.Sprintf("No clear difference at %d finished runs: the approval rates' 95%% intervals overlap.", runs)
}

// The reasons of a Benchmark's Done condition.
const (
	BenchmarkRunning           = "Running"
	BenchmarkFinished          = "Finished"
	BenchmarkOverBudget        = "OverBudget"
	BenchmarkContainerNotFound = "ContainerNotFound"
	BenchmarkIssueUnreadable   = "IssueUnreadable"
)

// BenchmarkPullTitle is the pull request title of a Benchmark's Task: says it
// is one, so nobody merges it.
func BenchmarkPullTitle(bench, variant string, goal, repetition int, title string) string {
	return fmt.Sprintf("Benchmark %s, %s, goal %d run %d (never merges): %s", bench, variant, goal+1, repetition, title)
}
