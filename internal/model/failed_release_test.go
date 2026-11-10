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
	"reflect"
	"strings"
	"testing"
	"time"
)

// The engine's "Prepare v0.3.0" (#158): its try stopped at 15:36, finding
// the milestone's only change (#136) shipped in v0.2.0; #147, #148 and #153
// merged at 16:14.
func at(hour, minute int) time.Time { return time.Date(2026, 10, 8, hour, minute, 0, 0, time.UTC) }

func engineTasks() []IssueTask {
	return []IssueTask{
		{Name: "engine-136", Issue: 136, Merged: true, Completed: at(9, 0)},
		{Name: "engine-158", Issue: 158, Failed: true, Completed: at(15, 36)},
		{Name: "engine-147", Issue: 147, Merged: true, Completed: at(16, 14)},
		{Name: "engine-148-1", Issue: 148, Failed: true, Completed: at(14, 0)},
		{Name: "engine-148-2", Issue: 148, Merged: true, Completed: at(16, 13)},
		{Name: "engine-153", Issue: 153, Merged: true, Completed: at(16, 15)},
		{Name: "engine-160", Issue: 160, Merged: true, Completed: at(16, 20)}, // another milestone's
		{Name: "engine-157", Issue: 157, Merged: true, Completed: at(10, 0)},  // an earlier prepare issue
	}
}

var engineTitles = map[int]string{136: "Lint rules", 147: "Fix the parser", 148: "Fix the\nlexer", 153: "Fix the checker",
	157: "Prepare v0.3.0"}

func TestNewFailedRelease(t *testing.T) {
	got := NewFailedRelease("v0.3.0", 158, engineTitles, []int{157, 158}, engineTasks())
	want := &FailedRelease{Milestone: "v0.3.0", Stopped: at(15, 36), Merged: []ReleaseMerge{
		{136, "Lint rules", at(9, 0)}, {148, "Fix the\nlexer", at(16, 13)}, {147, "Fix the parser", at(16, 14)},
		{153, "Fix the checker", at(16, 15)},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewFailedRelease() = %+v\nwant %+v", got, want)
	}
	if since := got.MergedSince(); len(since) != 3 || since[0].Issue != 148 {
		t.Errorf("MergedSince() = %+v, want #148, #147 and #153", since)
	}
	untried := NewFailedRelease("v0.3.0", 158, engineTitles, nil, engineTasks()[:1])
	if since := untried.MergedSince(); len(since) != 1 || since[0].Issue != 136 {
		t.Errorf("MergedSince() of an untried step = %+v, want all of its merges", since)
	}
	var none *FailedRelease
	if since := none.MergedSince(); since != nil {
		t.Errorf("MergedSince() of no release = %+v", since)
	}
}

func TestCheckFailedAnswersRelease(t *testing.T) {
	stale := NewFailedRelease("v0.3.0", 158, engineTitles, []int{157, 158}, engineTasks())
	empty := NewFailedRelease("v0.3.0", 158, map[int]string{136: "Lint rules"}, []int{158}, engineTasks())
	waiting := []FailedIssue{
		{Container: "engine", Number: 158, Kind: FailedTask, Release: stale},
		{Container: "engine", Number: 159, Kind: FailedTask, Release: empty},
		{Container: "engine", Number: 1, Kind: FailedTask},
	}
	answer := func(number int, answer string, r *Recommendation) FailedAnswer {
		return FailedAnswer{Container: "engine", Issue: number, Answer: answer, Why: "Its only change shipped.",
			Recommend: r}
	}
	closeIt := &Recommendation{Answer: AnswerClose, Why: "Its only change shipped."}
	want := "the release step of v0.3.0, but #148, #147, #153 merged into v0.3.0 after its last try stopped at " +
		"2026-10-08T15:36:00Z, so it has work to release: retry it, or ask the person"
	tests := []struct {
		name    string
		answers []FailedAnswer
		want    []string
	}{
		{"closing a step that gained merges is refused", []FailedAnswer{answer(158, AnswerClose, nil)},
			[]string{"closes engine#158, " + want}},
		{"recommending it is refused", []FailedAnswer{answer(158, AnswerAsk, closeIt)},
			[]string{"recommends: closes engine#158, " + want}},
		{"retrying it is not", []FailedAnswer{answer(158, AnswerRetry, nil)}, nil},
		{"waiting on it is not", []FailedAnswer{answer(158, AnswerWait, nil)}, nil},
		{"closing a step with nothing merged since is not", []FailedAnswer{answer(159, AnswerClose, nil)}, nil},
		{"nor recommending it", []FailedAnswer{answer(159, AnswerAsk, closeIt)}, nil},
		{"closing another issue is not", []FailedAnswer{answer(1, AnswerClose, nil)}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckFailedAnswers(tc.answers, waiting)
			if len(got) != len(tc.want) {
				t.Fatalf("CheckFailedAnswers() = %v, want %d refusals", got, len(tc.want))
			}
			for i, w := range tc.want {
				if !strings.Contains(got[i].Reason, w) {
					t.Errorf("refusal %d = %q, want it to say %q", i, got[i].Reason, w)
				}
			}
		})
	}
}

func TestWorkspaceFailedRelease(t *testing.T) {
	release := NewFailedRelease("v0.3.0", 158, engineTitles, []int{157, 158}, engineTasks()[:4])
	got, err := marshalDocument(workspaceFailed([]FailedIssue{{Container: "engine", Number: 158,
		Title: "Prepare v0.3.0", Kind: FailedTask, Reason: "Task `engine-158` failed: its only change shipped",
		Tries: 1, Release: release}}, nil))
	if err != nil {
		t.Fatal(err)
	}
	want := "- container: engine\n  issue: 158\n  title: Prepare v0.3.0\n  kind: failed\n" +
		"  reason: 'Task `engine-158` failed: its only change shipped'\n  tries: 1\n  body: \"\"\n" +
		"  release:\n    milestone: v0.3.0\n    stopped: \"2026-10-08T15:36:00Z\"\n    merged:\n" +
		"        - issue: 136\n          title: Lint rules\n          mergedAt: \"2026-10-08T09:00:00Z\"\n" +
		"        - issue: 147\n          title: Fix the parser\n          mergedAt: \"2026-10-08T16:14:00Z\"\n" +
		"          afterStop: true\n"
	if got != want {
		t.Errorf("%s =\n%s\nwant\n%s", WorkspaceFailedFile, got, want)
	}
}
