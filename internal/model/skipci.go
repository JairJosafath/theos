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
	"fmt"
	"slices"
)

// SkipCI is the marker in a commit's message that makes the Git host run no
// push workflow for it.
const SkipCI = "[skip ci]"

// MovedDuringMerge is the description of the error status a squash merge gets
// when its tree is not the tree of the head whose checks passed.
const MovedDuringMerge = "merged without a check: main moved during the merge"

// SkipsPushRun reports whether the squash merge of a pull request head needs no
// push run on main: its required checks all passed (required, a CI system's
// CheckContexts, with the head's combined state and the state of each check by
// its context) and its pull request is not behind main, so the commit the
// merge makes has the head's tree, which has just passed.
func SkipsPushRun(required []string, state string, contexts map[string]string, behind bool) bool {
	return !behind && BaseState(required, state, contexts) == checkSuccess
}

// SquashTitle is the title of a squash commit: the pull request's title, with
// the SkipCI marker in it, not only in the message, when the merge needs no
// push run, as a host may read only the first line.
func SquashTitle(title string, skipPush bool) string {
	if skipPush {
		return title + " " + SkipCI
	}
	return title
}

// SameTreeDescription is the description of a status copied from the head of
// pull request number onto the commit its merge made.
func SameTreeDescription(number int, head string) string {
	return fmt.Sprintf("same tree as #%d head %s", number, head)
}

// RequiredChecks returns, sorted, the contexts of the checks on a commit that
// the required patterns match, skipped checks aside; with no patterns, all of
// them. These are the checks a merge's commit carries when its push run is
// skipped.
func RequiredChecks(required []string, contexts map[string]string) []string {
	var out []string
	for context, state := range contexts {
		if state != checkSkipped &&
			(len(required) == 0 || slices.ContainsFunc(required, func(p string) bool { return matchGlob(p, context) })) {
			out = append(out, context)
		}
	}
	slices.Sort(out)
	return out
}
