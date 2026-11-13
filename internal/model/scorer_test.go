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

// measure-and-improve, step 4: which finished Tasks a Scorer judges.
func TestScorerSelects(t *testing.T) {
	made := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	after, before := made.Add(time.Hour), made.Add(-time.Hour)
	merged := ScoredTask{Name: "api-4", Kind: "Implement", Container: "api", Phase: TaskMerged, Completed: after}
	tests := []struct {
		name string
		sel  ScorerSelection
		task ScoredTask
		want bool
	}{
		{"a merged Implement Task by default", ScorerSelection{}, merged, true},
		{"and one with no kind, which is Implement", ScorerSelection{}, ScoredTask{Container: "api",
			Phase: TaskMerged, Completed: after}, true},
		{"not one finished before the Scorer", ScorerSelection{}, ScoredTask{Kind: "Implement", Container: "api",
			Phase: TaskMerged, Completed: before}, false},
		{"not one still going", ScorerSelection{}, ScoredTask{Kind: "Implement", Phase: TaskMerged}, false},
		{"not a failed one by default", ScorerSelection{}, ScoredTask{Kind: "Implement", Phase: TaskFailed,
			Completed: after}, false},
		{"a failed one when asked", ScorerSelection{Phases: []string{TaskFailed}}, ScoredTask{Kind: "Implement",
			Phase: TaskFailed, Completed: after}, true},
		{"not a Spec Task by default", ScorerSelection{}, ScoredTask{Kind: "Spec", Phase: TaskMerged,
			Completed: after}, false},
		{"a Spec Task when asked", ScorerSelection{Kinds: []string{"Spec"}}, ScoredTask{Kind: "Spec",
			Phase: TaskMerged, Completed: after}, true},
		{"never a Score Task", ScorerSelection{Kinds: []string{"Score"}}, ScoredTask{Kind: "Score",
			Phase: TaskMerged, Completed: after}, false},
		{"only the Containers named", ScorerSelection{Containers: []string{"web"}}, merged, false},
		{"a Benchmark's Task whatever the selection, Approved too", ScorerSelection{Containers: []string{"web"}},
			ScoredTask{Kind: "Implement", Container: "api", Phase: TaskApproved, Completed: after, Benchmarked: true},
			true},
		{"but not one finished before the Scorer", ScorerSelection{}, ScoredTask{Kind: "Implement",
			Phase: TaskApproved, Completed: before, Benchmarked: true}, false},
		{"nor an Approved Task of no Benchmark", ScorerSelection{Phases: []string{TaskMerged, TaskFailed}},
			ScoredTask{Kind: "Implement", Phase: TaskApproved, Completed: after}, false},
	}
	for _, tt := range tests {
		if got := ScorerSelects(tt.sel, made, tt.task); got != tt.want {
			t.Errorf("%s: ScorerSelects = %t", tt.name, got)
		}
	}
}

// The sample is the same every time, and near its percent.
func TestScoreSampled(t *testing.T) {
	if ScoreSampled("uid", "api-4", 0) || !ScoreSampled("uid", "api-4", 100) {
		t.Error("0% judges some, or 100% not all")
	}
	n := 0
	for i := range 1000 {
		task := "api-" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + string(rune('0'+i%10)) + "-" +
			string(rune('a'+i/26%26))
		first, again := ScoreSampled("uid", task, 30), ScoreSampled("uid", task, 30)
		if first != again {
			t.Fatal("the sample changed between two calls")
		}
		if first {
			n++
		}
	}
	if n < 150 || n > 450 {
		t.Errorf("30%% sampled %d of 1000", n)
	}
}

// A Score Task's name is one per Scorer and Task, short enough for its
// sandbox's.
func TestScoreTaskName(t *testing.T) {
	if got := ScoreTaskName("tests", "add-crop"); got != "tests-add-crop" {
		t.Errorf("name = %q", got)
	}
	long1 := ScoreTaskName("tests-with-a-long-name", "orders-api-1234-implement-the-refund")
	long2 := ScoreTaskName("tests-with-a-long-name", "orders-api-1234-implement-the-refunds")
	if len(long1) > 40 || long1 == long2 {
		t.Errorf("long names %q and %q", long1, long2)
	}
	if len(SandboxName(long1, 1, 0, RoleScorer, 1, 0)) > 63 {
		t.Errorf("its sandbox's name is too long: %q", SandboxName(long1, 1, 0, RoleScorer, 1, 0))
	}
}

// The evidence says what the Task did and where the change is.
func TestScoreGoal(t *testing.T) {
	two := 2
	goal := ScoreGoal(ScoreEvidence{Task: "api-4", Container: "api", Phase: TaskMerged, Issue: 4,
		IssueTitle: "Export orders", Rounds: 2, PullRequest: 9, Commit: "abc123", Summary: "Added /export.",
		Checks: "success", ReviewVerdict: "approve", ReviewSummary: "Tested.", PersonCommits: &two})
	for _, want := range []string{
		"Task api-4 (Implement) on Container api ended Merged after 2 round(s).",
		"Its issue: #4 Export orders",
		"merged as commit abc123 on the default branch (pull request #9): read it with git show abc123.",
		"The implementer's summary: Added /export.",
		"The reviewer's verdict: approve: Tested.",
		"Commits a person pushed to it: 2.",
	} {
		if !strings.Contains(goal, want) {
			t.Errorf("goal lacks %q:\n%s", want, goal)
		}
	}
	approved := ScoreGoal(ScoreEvidence{Task: "b-fast-g1-r1", Container: "api", Phase: TaskApproved,
		Branch: "task/b-fast-g1-r1", Head: "def456", Base: "abc123", PullRequest: 11})
	if !strings.Contains(approved, "its change is commit def456 on branch task/b-fast-g1-r1 (pull request #11), "+
		"from abc123: read it with git diff abc123 def456.") {
		t.Errorf("a Benchmark's Task's goal:\n%s", approved)
	}
	failed := ScoreGoal(ScoreEvidence{Task: "api-5", Kind: "Implement", Container: "api", Phase: TaskFailed,
		Branch: "task/api-5", PullRequest: 10, FailedReason: "Out of rounds"})
	if !strings.Contains(failed, "merged nothing; its last commit was on branch task/api-5") ||
		!strings.Contains(failed, "Why it failed: Out of rounds") || strings.Contains(failed, "reviewer") {
		t.Errorf("a failed Task's goal:\n%s", failed)
	}
}

// A judge's verdicts: one per criterion, in order, what it skipped Unclear.
func TestParseScore(t *testing.T) {
	criteria := []string{"Has a test", "Says why", "Is small"}
	out := "I read it.\n{\"note\": 1}\n" +
		`{"verdicts": [{"criterion": 2, "verdict": "FAIL", "reason": "No body."}, ` +
		`{"criterion": 1, "verdict": "pass", "reason": "  crop_test.go\n fails without it. "}, ` +
		`{"criterion": 9, "verdict": "pass"}, {"criterion": 3, "verdict": "maybe"}]}`
	got, err := ParseScore(out, criteria)
	if err != nil {
		t.Fatal(err)
	}
	want := []ScoreCriterion{
		{Criterion: "Has a test", Verdict: ScorePass, Reason: "crop_test.go fails without it."},
		{Criterion: "Says why", Verdict: ScoreFail, Reason: "No body."},
		{Criterion: "Is small", Verdict: ScoreUnclear},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("verdict %d = %+v, want %+v", i+1, got[i], want[i])
		}
	}
	partial, _ := ParseScore(`{"verdicts": [{"criterion": 1, "verdict": "pass"}]}`, criteria)
	if partial[1].Verdict != ScoreUnclear || partial[1].Reason != "the judge said nothing of it" {
		t.Errorf("a criterion the judge skipped: %+v", partial[1])
	}
	if _, err := ParseScore("All good!", criteria); err == nil {
		t.Error("output without verdicts parsed")
	}
	if got, err := ParseScore(StandInScore(criteria), criteria); err != nil || got[2].Verdict != ScorePass {
		t.Errorf("the stand-in judge's verdicts = %+v, %v", got, err)
	}
}

// The Scorer counts each criterion's verdicts, and stops at its cap or
// budget.
func TestTallyScoresAndLimits(t *testing.T) {
	criteria := []string{"Has a test", "Says why"}
	got := TallyScores(criteria, [][]ScoreCriterion{
		{{Verdict: ScorePass}, {Verdict: ScoreFail}},
		{{Verdict: ScorePass}, {Verdict: ScoreUnclear}},
	})
	if got[0] != (ScoreCount{Criterion: "Has a test", Pass: 2}) ||
		got[1] != (ScoreCount{Criterion: "Says why", Fail: 1, Unclear: 1}) {
		t.Errorf("TallyScores = %+v", got)
	}
	if r, _ := ScorerLimit(49, 50, 0, 0); r != "" {
		t.Errorf("under the cap: %s", r)
	}
	if r, _ := ScorerLimit(50, 50, 0, 0); r != ScorerDailyCapReached {
		t.Errorf("at the cap: %q", r)
	}
	if r, m := ScorerLimit(3, 50, 5000, 5); r != ScorerBudgetReached || !strings.Contains(m, "$5.00") {
		t.Errorf("at the budget: %q %q", r, m)
	}
	if r, _ := ScorerLimit(3, 50, 4999, 5); r != "" {
		t.Errorf("under the budget: %s", r)
	}
	if ScorerDay(time.Date(2026, 11, 5, 23, 30, 0, 0, time.FixedZone("x", -3600))) != "2026-11-06" {
		t.Error("the day is not UTC's")
	}
}
