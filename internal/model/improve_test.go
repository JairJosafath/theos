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
	"time"
)

// measure-and-improve, step 7: a criterion that failed often enough among the
// newest Scores of one configuration is due an issue.
func TestImprovementsDue(t *testing.T) {
	criteria := []string{"Has a test", "Says why"}
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	score := (func(task, config string, hours int, first, second string) ScoreRecord {
		return ScoreRecord{Task: task, Container: "api", Config: config, Completed: t0.Add(time.Duration(hours) * time.Hour),
			Verdicts: []ScoreCriterion{{Verdict: first, Reason: task + " why"}, {Verdict: second}}}
	})
	scores := []ScoreRecord{
		score("old", "sonnet", 0, ScoreFail, ScorePass),
		score("a", "sonnet", 1, ScoreFail, ScorePass),
		score("b", "sonnet", 2, ScorePass, ScorePass),
		score("c", "sonnet", 3, ScoreFail, ScoreUnclear),
		score("d", "opus", 1, ScoreFail, ScorePass),
	}
	due := ImprovementsDue(criteria, scores, 2, 3)
	if len(due) != 1 {
		t.Fatalf("ImprovementsDue = %+v, want one", due)
	}
	if d := due[0]; d.Criterion != "Has a test" || d.Config != "sonnet" || d.Window != 3 || len(d.Failures) != 2 ||
		d.Failures[0].Task != "c" || d.Failures[1].Task != "a" {
		t.Errorf("due = %+v: want c and a, the newest three's failures, not old's", d)
	}
	if got := ImprovementsDue(criteria, scores, 3, 3); len(got) != 0 {
		t.Errorf("three failures in the window asked, two there: %+v", got)
	}
	if got := ImprovementsDue(criteria, scores, 3, 4); len(got) != 1 {
		t.Errorf("a window of four holds old's failure too: %+v", got)
	}
	if got := ImprovementsDue(criteria, scores, 1, 10); len(got) != 2 || got[0].Config != "opus" {
		t.Errorf("one failure enough: %+v, want opus then sonnet", got)
	}
}

// The issue says what fails, why, what to change, and carries its marker.
func TestImprovementIssue(t *testing.T) {
	imp := Improvement{Criterion: "Has a test", Config: "claude-code · sonnet", Window: 3, Failures: []ScoreFailure{
		{Task: "api-4", Container: "api", PullURL: "http://git/api/pulls/3", Reason: "No test."},
		{Task: "api-5", Container: "api"},
	}}
	title, body := ImprovementIssue("tests", imp)
	if title != `Scorer tests: "Has a test" keeps failing (claude-code · sonnet)` {
		t.Errorf("title = %q", title)
	}
	for _, want := range []string{
		"failed in 2 of the newest 3 judged changes whose implementer ran on `claude-code · sonnet`",
		"- Task `api-4` on `api` ([pull request](http://git/api/pulls/3)): No test.",
		"- Task `api-5` on `api`: unknown",
		"`docs/project-notes.md`",
		"## Acceptance criteria",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if !HasImprovementMarker(body, "tests", "Has a test", "claude-code · sonnet") ||
		HasImprovementMarker(body, "tests", "Says why", "claude-code · sonnet") ||
		HasImprovementMarker(body, "style", "Has a test", "claude-code · sonnet") {
		t.Error("the marker doesn't tell its Scorer, criterion and configuration apart")
	}
}
