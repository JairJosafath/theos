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

// ReasonDoneByStandardChange is the reason of a Task that ended done because
// its Standard change merged and left its issue nothing to do: the Task is
// Merged with no merge of its own, and the pull request it opened in an
// earlier round, if any, was closed, not merged.
const ReasonDoneByStandardChange = "DoneByStandardChange"

// MergedChange reports whether a Task that ended is a merged change, which
// the Prometheus series (theos_tasks_merged) and theos-session report both
// count (#204). Something merged: the Task's own pull request, or, for a Task
// that ended DoneByStandardChange, the Standard change's own, which its
// status records. Such a Task's own pull request, if it has one, was closed
// in an earlier round, and is no merge. A Task that ended with nothing to do
// merged nothing, and neither did one that has no pull request (a Plan, a
// Triage or a Score Task).
func MergedChange(f TaskMetricFacts) bool {
	switch {
	case f.Phase != TaskMerged || f.NoChange:
		return false
	case f.DoneByStandardChange:
		return f.StandardChange != 0
	}
	return f.PullRequest != 0
}
