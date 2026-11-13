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

package contract

import (
	"cmp"
	"context"
	"slices"
	"testing"
	"time"
)

// The names the suites use more than once.
const (
	// firstTag is the tag of a repository's first release.
	firstTag = "v0.1.0"
	// patchTag is a patch title a cut gives a release of fixes.
	patchTag = "v0.0.1"
	// checkGlob matches the check contexts a branch rule asks for.
	checkGlob = "check*"
	// The labels the Issues suite files issues with.
	labelReady = "status/ready"
	labelAPI   = "area/api"
	labelBug   = "kind/bug"
	// milestoneLater is the milestone of what no release holds.
	milestoneLater = "Later"
	// The states of an issue.
	issueOpen   = "open"
	issueClosed = "closed"
)

// subtest is one subtest of a suite whose subtests share S.
type subtest[S any] struct {
	name string
	run  func(S, *testing.T)
}

// runSubtests runs tests on s, skipping those cannot names, with why.
func runSubtests[S any](t *testing.T, s S, cannot map[string]string, tests []subtest[S]) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if why, ok := cannot[test.name]; ok {
				t.Skip(why)
			}
			test.run(s, t)
		})
	}
}

// suiteContext bounds a suite's calls.
func suiteContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Minute)
}

// sorted is a sorted copy of s.
func sorted[E cmp.Ordered](s []E) []E {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}
