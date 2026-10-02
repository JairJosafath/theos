package model

import (
	"reflect"
	"strings"
	"testing"
)

const criteria = "## Goal\n\nDo it.\n\n## Acceptance criteria\n\n- [ ] It works\n"

func TestMissingAcceptanceCriteria(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"a checklist under the heading", criteria, ""},
		{"any heading level and case", "### acceptance Criteria\n* [x] done\n", ""},
		{"checked items count", "## Acceptance criteria\n- [X] already met\n", ""},
		{"no body", "", "has no description"},
		{"no heading", "## Goal\n- [ ] something\n", `no "Acceptance criteria" section`},
		{"a heading without a checklist", "## Acceptance criteria\n\nIt should work.\n", "no checklist items"},
		{"a checklist after the next heading doesn't count",
			"## Acceptance criteria\nSee below.\n## Notes\n- [ ] not a criterion\n", "no checklist items"},
		{"an empty item doesn't count", "## Acceptance criteria\n- [ ]   \n", "no checklist items"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MissingAcceptanceCriteria(tt.body)
			if tt.want == "" && got != "" || tt.want != "" && !strings.Contains(got, tt.want) {
				t.Errorf("MissingAcceptanceCriteria() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNextCrewStep(t *testing.T) {
	ready := func(n int, blockers ...string) Issue {
		return Issue{Number: n, Title: "Issue", Body: criteria, OpenBlockers: blockers}
	}
	tests := []struct {
		name  string
		facts CrewFacts
		want  CrewStep
	}{
		{name: "paused", facts: CrewFacts{Paused: true, ContainerReady: true, Issues: []Issue{ready(1)}},
			want: CrewStep{Phase: CrewPaused, Ready: []int{1}, Message: "Paused; not picking up issues"}},
		{name: "waits for the Container", facts: CrewFacts{Issues: []Issue{ready(1)}},
			want: CrewStep{Phase: CrewWaiting, Ready: []int{1}, Message: "Waiting for Container cel-ls to be Ready"}},
		{name: "idle without ready issues", facts: CrewFacts{ContainerReady: true},
			want: CrewStep{Phase: CrewIdle, Message: "No ready issues"}},
		{name: "starts the lowest ready issue", facts: CrewFacts{ContainerReady: true,
			Issues: []Issue{ready(5), ready(3), ready(4, "#3")}},
			want: CrewStep{Phase: CrewWorking, Ready: []int{3, 5}, Waiting: []int{4},
				Start: &IssueStart{Issue: 3, Task: "cel-ls-3", Attempt: 1}, Message: "Starting Task cel-ls-3 for issue #3"}},
		{name: "waits on open dependencies", facts: CrewFacts{ContainerReady: true,
			Issues: []Issue{ready(2, "#1", "other/repo#7")}},
			want: CrewStep{Phase: CrewIdle, Waiting: []int{2},
				Message: "No ready issues; 1 waits on open dependencies"}},
		{name: "one Task at a time", facts: CrewFacts{ContainerReady: true, Issues: []Issue{ready(4)},
			Tasks: []CrewTask{{Name: "cel-ls-3", Issue: 3}}},
			want: CrewStep{Phase: CrewWorking, Active: "cel-ls-3", Ready: []int{4},
				Message: "Task cel-ls-3 is working on issue #3"}},
		{name: "a Task from a goal also occupies the Container", facts: CrewFacts{ContainerReady: true,
			Issues: []Issue{ready(4)}, Tasks: []CrewTask{{Name: "by-hand"}}},
			want: CrewStep{Phase: CrewWorking, Active: "by-hand", Ready: []int{4}, Message: "Task by-hand is working"}},
		{name: "retries a failed issue that is ready again", facts: CrewFacts{ContainerReady: true,
			Issues: []Issue{ready(3)}, Tasks: []CrewTask{{Name: "cel-ls-3", Issue: 3, Final: true, Failed: true}}},
			want: CrewStep{Phase: CrewWorking, Ready: []int{3},
				Start: &IssueStart{Issue: 3, Task: "cel-ls-3-2", Attempt: 2}, Message: "Starting Task cel-ls-3-2 for issue #3"}},
		{name: "gives up after three tries", facts: CrewFacts{ContainerReady: true, Issues: []Issue{ready(3)},
			Tasks: []CrewTask{
				{Name: "cel-ls-3", Issue: 3, Final: true, Failed: true},
				{Name: "cel-ls-3-2", Issue: 3, Final: true, Failed: true},
				{Name: "cel-ls-3-3", Issue: 3, Final: true, Failed: true},
			}},
			want: CrewStep{Phase: CrewIdle, NotReady: []IssueRejection{{Issue: 3,
				Reason: "Agents tried this issue 3 times (Tasks cel-ls-3, cel-ls-3-2, cel-ls-3-3) and failed each time; " +
					"it needs a person: split it, clarify it, or do it by hand"}},
				Message: "No ready issues"}},
		{name: "an issue already merged is not done again", facts: CrewFacts{ContainerReady: true,
			Issues: []Issue{ready(3)}, Tasks: []CrewTask{{Name: "cel-ls-3", Issue: 3, Final: true}}},
			want: CrewStep{Phase: CrewIdle, NotReady: []IssueRejection{{Issue: 3,
				Reason: "Task cel-ls-3 already merged this issue; close it, or file the rest as a new issue"}},
				Message: "No ready issues"}},
		{name: "rejects an issue without acceptance criteria", facts: CrewFacts{ContainerReady: true,
			Issues: []Issue{{Number: 2, Title: "Vague", Body: "Make it better."}, ready(6)}},
			want: CrewStep{Phase: CrewWorking, Ready: []int{6},
				NotReady: []IssueRejection{{Issue: 2, Reason: "This issue has no \"Acceptance criteria\" section " +
					"with a checklist (\"- [ ] ...\"), so a reviewer could not tell when it is done. Add one, " +
					"then label it status/ready again."}},
				Start: &IssueStart{Issue: 6, Task: "cel-ls-6", Attempt: 1}, Message: "Starting Task cel-ls-6 for issue #6"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextCrewStep("cel-ls", tt.facts)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NextCrewStep()\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestIssueTaskName(t *testing.T) {
	long := strings.Repeat("c", 60)
	tests := []struct {
		container      string
		issue, attempt int
		want           string
	}{
		{"cel-ls", 3, 1, "cel-ls-3"},
		{"cel-ls", 3, 2, "cel-ls-3-2"},
		{long, 12, 1, long + "-12"},
	}
	for _, tt := range tests {
		if got := IssueTaskName(tt.container, tt.issue, tt.attempt); got != tt.want {
			t.Errorf("IssueTaskName(%q, %d, %d) = %q, want %q", tt.container, tt.issue, tt.attempt, got, tt.want)
		}
	}
}

func TestIssueGoal(t *testing.T) {
	got := IssueGoal(3, "Locate CEL in YAML", "## Goal\nFind it.\n")
	want := "Issue #3: Locate CEL in YAML\n\n## Goal\nFind it."
	if got != want {
		t.Errorf("IssueGoal() = %q, want %q", got, want)
	}
}
