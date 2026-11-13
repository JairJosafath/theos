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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// endedTaskOf is a Task that ended Merged, as these tests need it: its kind,
// the reason it ended, its own pull request and its Standard change's record.
func endedTaskOf(name string, kind c4v1beta1.TaskKind, reason string, pull int32,
	change *c4v1beta1.TaskStandardChange) c4v1beta1.Task {
	t := c4v1beta1.Task{ObjectMeta: metav1.ObjectMeta{Name: name}}
	t.Spec.Kind, t.Spec.Issue, t.Spec.ContainerRef.Name = kind, 7, "cli"
	t.Status.Phase, t.Status.PullRequest, t.Status.StandardChange = c4v1beta1.TaskMerged, pull, change
	t.Status.Conditions = []metav1.Condition{{Type: ConditionSucceeded, Status: metav1.ConditionTrue, Reason: reason}}
	return t
}

// The places that decide "this Task's own change merged" ask model.MergedChange,
// as the metrics, the report, the chat and the overview do (#220): a release
// waits for the merge of those Tasks, its notes list their issues, and a failed
// issue is "already merged" by one of them. A table of how a Task ends.
func TestMergedChangePlacesAgree220(t *testing.T) {
	change := &c4v1beta1.TaskStandardChange{Issue: 41}
	for _, tc := range []struct {
		name string
		task c4v1beta1.Task
		// merged is what model.MergedChange says of it.
		merged bool
	}{
		{"its own pull request merged", endedTaskOf("a", "", "Merged", 3, nil), true},
		{"nothing to do, no change (#220)", endedTaskOf("b", "", model.ReasonNothingToDo, 0, nil), false},
		{"done by a Standard change, which merged", endedTaskOf("d", "", reasonDoneByStandardChange, 5, change), true},
		{"done by a Standard change with no record (#220)", endedTaskOf("e", "", reasonDoneByStandardChange, 0, nil), false},
		{"an Implement Task with no pull request (#220)", endedTaskOf("g", "", "Merged", 0, nil), false},
	} {
		if got := mergedItsIssue(&tc.task); got != tc.merged {
			t.Errorf("%s: mergedItsIssue = %v, want %v", tc.name, got, tc.merged)
		}
		if notes := mergedIssues([]c4v1beta1.Task{tc.task}); (len(notes) == 1) != tc.merged {
			t.Errorf("%s: mergedIssues = %v, want the issue listed: %v", tc.name, notes, tc.merged)
		}
		if failed := issueTasks([]c4v1beta1.Task{tc.task})["cli"]; len(failed) != 1 || failed[0].Merged != tc.merged {
			t.Errorf("%s: issueTasks = %+v, want Merged %v", tc.name, failed, tc.merged)
		}
	}
}

// A Triage Task ends Merged with an issue and no pull request, and a merged spec
// leaves its issue to be built: neither merged the issue (#220).
func TestTriageAndSpecMergedNoIssue220(t *testing.T) {
	for _, task := range []c4v1beta1.Task{
		endedTaskOf("triage", c4v1beta1.TaskTriage, "Triaged", 0, nil),
		endedTaskOf("spec", c4v1beta1.TaskKindSpec, "Merged", 6, nil),
	} {
		if mergedItsIssue(&task) {
			t.Errorf("%s: a release waits for a merge of its issue it never had", task.Name)
		}
		if got := mergedIssues([]c4v1beta1.Task{task}); len(got) != 0 {
			t.Errorf("%s: its issue %v is in the release notes", task.Name, got)
		}
	}
}

// A Merged Task reads its pull request's merge commit and size only when that
// pull request merged: one that ended DoneByStandardChange closed its own in an
// earlier round, and would ask the host about it every minute, then hour, for a
// merge commit it never had (#220).
func TestReadsMergedPull220(t *testing.T) {
	change := &c4v1beta1.TaskStandardChange{Issue: 41}
	working := endedTaskOf("d", "", "Merged", 3, nil)
	working.Status.Phase = c4v1beta1.TaskReviewing
	for _, tc := range []struct {
		name string
		task c4v1beta1.Task
		want bool
	}{
		{"merged its own pull request", endedTaskOf("a", "", "Merged", 3, nil), true},
		{"no pull request", endedTaskOf("b", "", model.ReasonNothingToDo, 0, nil), false},
		{"done by a Standard change after a closed round-1 pull request (#220)",
			endedTaskOf("c", "", reasonDoneByStandardChange, 3, change), false},
		{"still at work", working, false},
	} {
		if got := readsMergedPull(&tc.task); got != tc.want {
			t.Errorf("%s: readsMergedPull = %v, want %v", tc.name, got, tc.want)
		}
	}
}
