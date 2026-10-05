package model

import (
	"reflect"
	"strings"
	"testing"
	"time"
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
			if got != "" {
				// The refusal shows a checklist that would pass, so a person fixes it in one go.
				example, ok := between(got, "```markdown\n", "```")
				if !ok || MissingAcceptanceCriteria(example) != "" {
					t.Errorf("MissingAcceptanceCriteria() = %q, want an example checklist that passes", got)
				}
			}
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
	start := func(issue int, task string, attempt int) []IssueStart {
		return []IssueStart{{Issue: issue, Task: task, Attempt: attempt}}
	}
	filer := CrewTask{Name: "cel-ls-1", Issue: 1, Blocked: true,
		StandardChange: &IssueChange{Issue: 2, Paths: []string{"Makefile", "AGENTS.md"}}}
	change := Issue{Number: 2, Body: criteria, StandardChange: true}
	v011 := &Release{Title: "v0.1.1", Due: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)}
	v020 := &Release{Title: "v0.2.0", Due: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)}
	in := func(release *Release, issue Issue) Issue {
		issue.Release = release
		return issue
	}
	urgent := func(issue Issue) Issue {
		issue.Urgent = true
		return issue
	}
	tests := []struct {
		name  string
		facts CrewFacts
		want  CrewStep
	}{
		{name: "paused", facts: CrewFacts{Paused: true, ContainerReady: true, Issues: []Issue{ready(1)}},
			want: CrewStep{Phase: CrewPaused, Ready: []int{1}, Message: "Paused; not picking up issues"}},
		{name: "paused while a Task finishes", facts: CrewFacts{Paused: true, ContainerReady: true,
			Tasks: []CrewTask{{Name: "cel-ls-1", Issue: 1}}},
			want: CrewStep{Phase: CrewPaused, Active: []string{"cel-ls-1"},
				Message: "Paused; not picking up issues; Task cel-ls-1 is working on issue #1 until done"}},
		{name: "waits for the Container", facts: CrewFacts{Issues: []Issue{ready(1)}},
			want: CrewStep{Phase: CrewWaiting, Ready: []int{1}, Message: "Waiting for Container cel-ls to be Ready"}},
		{name: "idle without ready issues", facts: CrewFacts{ContainerReady: true},
			want: CrewStep{Phase: CrewIdle, Message: "No ready issues"}},
		{name: "starts the lowest ready issue", facts: CrewFacts{ContainerReady: true,
			Issues: []Issue{ready(5), ready(3), ready(4, "#3")}},
			want: CrewStep{Phase: CrewWorking, Ready: []int{5}, Waiting: []int{4},
				Start: start(3, "cel-ls-3", 1), Message: "Starting Task cel-ls-3 for issue #3"}},
		{name: "waits on open dependencies", facts: CrewFacts{ContainerReady: true,
			Issues: []Issue{ready(2, "#1", "other/repo#7")}},
			want: CrewStep{Phase: CrewIdle, Waiting: []int{2},
				Message: "No ready issues; 1 waits on open dependencies"}},
		{name: "one Task at a time by default", facts: CrewFacts{ContainerReady: true, Issues: []Issue{ready(4)},
			Tasks: []CrewTask{{Name: "cel-ls-3", Issue: 3}}},
			want: CrewStep{Phase: CrewWorking, Active: []string{"cel-ls-3"}, Ready: []int{4},
				Message: "Task cel-ls-3 is working on issue #3; 1 ready issue waits for a free place"}},
		{name: "a Task from a goal also takes a place", facts: CrewFacts{ContainerReady: true,
			Issues: []Issue{ready(4)}, Tasks: []CrewTask{{Name: "by-hand"}}},
			want: CrewStep{Phase: CrewWorking, Active: []string{"by-hand"}, Ready: []int{4},
				Message: "Task by-hand is working; 1 ready issue waits for a free place"}},
		{name: "fills the free places", facts: CrewFacts{ContainerReady: true, Parallel: 3,
			Issues: []Issue{ready(1), ready(2), ready(3), ready(4), ready(5)}, Tasks: []CrewTask{{Name: "cel-ls-1", Issue: 1}}},
			want: CrewStep{Phase: CrewWorking, Active: []string{"cel-ls-1"}, Ready: []int{4, 5},
				Start:   []IssueStart{{Issue: 2, Task: "cel-ls-2", Attempt: 1}, {Issue: 3, Task: "cel-ls-3", Attempt: 1}},
				Message: "Starting Task cel-ls-2 for issue #2 and Task cel-ls-3 for issue #3"}},
		{name: "never two Tasks for one issue", facts: CrewFacts{ContainerReady: true, Parallel: 2,
			Issues: []Issue{ready(1)}, Tasks: []CrewTask{{Name: "cel-ls-1", Issue: 1}}},
			want: CrewStep{Phase: CrewWorking, Active: []string{"cel-ls-1"},
				Message: "Task cel-ls-1 is working on issue #1"}},
		{name: "retries a failed issue that is ready again", facts: CrewFacts{ContainerReady: true,
			Issues: []Issue{ready(3)}, Tasks: []CrewTask{{Name: "cel-ls-3", Issue: 3, Final: true, Failed: true}}},
			want: CrewStep{Phase: CrewWorking, Start: start(3, "cel-ls-3-2", 2), Message: "Starting Task cel-ls-3-2 for issue #3"}},
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
		{name: "hands back an issue without acceptance criteria, even while busy", facts: CrewFacts{ContainerReady: true,
			Issues: []Issue{{Number: 2, Title: "Vague", Body: "Make it better."}, ready(6)},
			Tasks:  []CrewTask{{Name: "cel-ls-1", Issue: 1}}},
			want: CrewStep{Phase: CrewWorking, Active: []string{"cel-ls-1"}, Ready: []int{6},
				NotReady: []IssueRejection{{Issue: 2, Reason: MissingAcceptanceCriteria("Make it better.")}},
				Message:  "Task cel-ls-1 is working on issue #1; 1 ready issue waits for a free place"}},
		{name: "a blocked Task leaves its place free, so its Standard change runs with the allowance", facts: CrewFacts{
			ContainerReady: true, Parallel: 1, Tasks: []CrewTask{filer}, Issues: []Issue{change}},
			want: CrewStep{Phase: CrewWorking, Active: []string{"cel-ls-1"},
				Start:   []IssueStart{{Issue: 2, Task: "cel-ls-2", Attempt: 1, AllowedPaths: []string{"Makefile"}}},
				Message: "Starting Task cel-ls-2 for issue #2"}},
		{name: "a Standard change with its allowance starts before older ready issues", facts: CrewFacts{
			ContainerReady: true, Parallel: 1, Issues: []Issue{ready(4), {Number: 9, Body: criteria, StandardChange: true},
				ready(5)},
			Tasks: []CrewTask{{Name: "cel-ls-3", Issue: 3, Blocked: true,
				StandardChange: &IssueChange{Issue: 9, Paths: []string{"Makefile"}}}}},
			want: CrewStep{Phase: CrewWorking, Active: []string{"cel-ls-3"}, Ready: []int{4, 5},
				Start:   []IssueStart{{Issue: 9, Task: "cel-ls-9", Attempt: 1, AllowedPaths: []string{"Makefile"}}},
				Message: "Starting Task cel-ls-9 for issue #9"}},
		{name: "a Task that is not blocked still takes the place", facts: CrewFacts{ContainerReady: true, Parallel: 1,
			Tasks: []CrewTask{{Name: "cel-ls-1", Issue: 1}}, Issues: []Issue{ready(3)}},
			want: CrewStep{Phase: CrewWorking, Active: []string{"cel-ls-1"}, Ready: []int{3},
				Message: "Task cel-ls-1 is working on issue #1; 1 ready issue waits for a free place"}},
		{name: "a Standard change waits until a Task names it", facts: CrewFacts{ContainerReady: true, Parallel: 1,
			Issues: []Issue{change}},
			want: CrewStep{Phase: CrewIdle, Waiting: []int{2}, Message: "No ready issues; 1 waits on open dependencies"}},
		{name: "a failed filing Task still grants its allowance", facts: CrewFacts{ContainerReady: true, Parallel: 1,
			Tasks:  []CrewTask{{Name: "cel-ls-1", Issue: 1, Final: true, Failed: true, StandardChange: filer.StandardChange}},
			Issues: []Issue{change}},
			want: CrewStep{Phase: CrewWorking,
				Start:   []IssueStart{{Issue: 2, Task: "cel-ls-2", Attempt: 1, AllowedPaths: []string{"Makefile"}}},
				Message: "Starting Task cel-ls-2 for issue #2"}},
		{name: "a status alone grants nothing without the label", facts: CrewFacts{ContainerReady: true, Parallel: 1,
			Tasks: []CrewTask{filer}, Issues: []Issue{{Number: 2, Body: criteria}}},
			want: CrewStep{Phase: CrewWorking, Active: []string{"cel-ls-1"},
				Start: start(2, "cel-ls-2", 1), Message: "Starting Task cel-ls-2 for issue #2"}},
		{name: "a later release waits while an earlier one has a ready issue", facts: CrewFacts{ContainerReady: true,
			Parallel: 2, Issues: []Issue{in(v020, ready(44)), in(v011, ready(58))}},
			want: CrewStep{Phase: CrewWorking, Waiting: []int{44},
				Start: start(58, "cel-ls-58", 1), Message: "Starting Task cel-ls-58 for issue #58"}},
		{name: "a later release waits while a Task works on an earlier one", facts: CrewFacts{ContainerReady: true,
			Parallel: 2, Issues: []Issue{in(v020, ready(45))},
			Tasks: []CrewTask{{Name: "cel-ls-60", Issue: 60, Release: v011}}},
			want: CrewStep{Phase: CrewWorking, Active: []string{"cel-ls-60"}, Waiting: []int{45},
				Message: "Task cel-ls-60 is working on issue #60; 1 ready issue waits for release v0.1.1"}},
		{name: "a working Crew also counts the issues on open dependencies", facts: CrewFacts{ContainerReady: true,
			Issues: []Issue{in(v011, ready(59)), in(v011, ready(61, "#46")), in(v020, ready(45))},
			Tasks:  []CrewTask{{Name: "cel-ls-58", Issue: 58, Release: v011}}},
			want: CrewStep{Phase: CrewWorking, Active: []string{"cel-ls-58"}, Ready: []int{59}, Waiting: []int{45, 61},
				Message: "Task cel-ls-58 is working on issue #58; 1 ready issue waits for a free place; " +
					"1 waits on open dependencies; 1 ready issue waits for release v0.1.1"}},
		{name: "an earlier release waiting on dependencies holds back a later one", facts: CrewFacts{
			ContainerReady: true, Issues: []Issue{in(v020, ready(6)), in(v011, ready(5, "#4"))}},
			want: CrewStep{Phase: CrewIdle, Waiting: []int{5, 6},
				Message: "No ready issues; 1 waits on open dependencies; 1 waits for release v0.1.1"}},
		{name: "a later release starts once the earlier one has no work left", facts: CrewFacts{ContainerReady: true,
			Issues: []Issue{in(v020, ready(45))},
			Tasks:  []CrewTask{{Name: "cel-ls-60", Issue: 60, Final: true, Release: v011}}},
			want: CrewStep{Phase: CrewWorking, Start: start(45, "cel-ls-45", 1),
				Message: "Starting Task cel-ls-45 for issue #45"}},
		{name: "a Task on a later release keeps running while an earlier one starts", facts: CrewFacts{
			ContainerReady: true, Parallel: 2, Issues: []Issue{in(v011, ready(58)), in(v020, ready(45))},
			Tasks: []CrewTask{{Name: "cel-ls-44", Issue: 44, Release: v020}}},
			want: CrewStep{Phase: CrewWorking, Active: []string{"cel-ls-44"}, Waiting: []int{45},
				Start: start(58, "cel-ls-58", 1), Message: "Starting Task cel-ls-58 for issue #58"}},
		{name: "issues outside a release come after every release", facts: CrewFacts{ContainerReady: true,
			Parallel: 2, Issues: []Issue{ready(3), in(v011, ready(9))}},
			want: CrewStep{Phase: CrewWorking, Waiting: []int{3},
				Start: start(9, "cel-ls-9", 1), Message: "Starting Task cel-ls-9 for issue #9"}},
		{name: "releases go by due date, then title", facts: CrewFacts{ContainerReady: true, Parallel: 3,
			Issues: []Issue{
				in(&Release{Title: "v0.9.0", Due: v020.Due}, ready(1)),
				in(&Release{Title: "v1.0.0", Due: v011.Due}, ready(2)),
				in(&Release{Title: "v0.9.9", Due: v011.Due}, ready(3)),
			}},
			want: CrewStep{Phase: CrewWorking, Waiting: []int{1, 2},
				Start: start(3, "cel-ls-3", 1), Message: "Starting Task cel-ls-3 for issue #3"}},
		{name: "issues outside a release go by number when no release has work", facts: CrewFacts{
			ContainerReady: true, Parallel: 2, Issues: []Issue{ready(8), ready(7)},
			Tasks: []CrewTask{{Name: "cel-ls-60", Issue: 60, Final: true, Release: v011}}},
			want: CrewStep{Phase: CrewWorking,
				Start:   []IssueStart{{Issue: 7, Task: "cel-ls-7", Attempt: 1}, {Issue: 8, Task: "cel-ls-8", Attempt: 1}},
				Message: "Starting Task cel-ls-7 for issue #7 and Task cel-ls-8 for issue #8"}},
		{name: "a blocker in a later release starts, so the earlier one doesn't wait for ever", facts: CrewFacts{
			ContainerReady: true, Parallel: 2, Issues: []Issue{in(v011, ready(5, "#6")), in(v020, ready(6)), in(v020, ready(7))}},
			want: CrewStep{Phase: CrewWorking, Waiting: []int{5, 7},
				Start: start(6, "cel-ls-6", 1), Message: "Starting Task cel-ls-6 for issue #6"}},
		{name: "so does a blocker's blocker", facts: CrewFacts{ContainerReady: true, Parallel: 2,
			Issues: []Issue{in(v011, ready(5, "#6")), in(v020, ready(6, "#8")), in(v020, ready(7)), ready(8)}},
			want: CrewStep{Phase: CrewWorking, Waiting: []int{5, 6, 7},
				Start: start(8, "cel-ls-8", 1), Message: "Starting Task cel-ls-8 for issue #8"}},
		{name: "a Standard change with its allowance starts whatever its release", facts: CrewFacts{
			ContainerReady: true, Parallel: 1,
			Issues: []Issue{in(v011, ready(59)), in(v020, Issue{Number: 70, Body: criteria, StandardChange: true})},
			Tasks: []CrewTask{{Name: "cel-ls-58", Issue: 58, Blocked: true, Release: v011,
				StandardChange: &IssueChange{Issue: 70, Paths: []string{"Makefile"}}}}},
			want: CrewStep{Phase: CrewWorking, Active: []string{"cel-ls-58"}, Ready: []int{59},
				Start:   []IssueStart{{Issue: 70, Task: "cel-ls-70", Attempt: 1, AllowedPaths: []string{"Makefile"}}},
				Message: "Starting Task cel-ls-70 for issue #70"}},
		{name: "an urgent issue starts at once, even when every place is taken", facts: CrewFacts{
			ContainerReady: true, Issues: []Issue{ready(4), urgent(ready(9))},
			Tasks: []CrewTask{{Name: "cel-ls-3", Issue: 3}}},
			want: CrewStep{Phase: CrewWorking, Active: []string{"cel-ls-3"}, Ready: []int{4},
				Start: start(9, "cel-ls-9", 1), Message: "Starting Task cel-ls-9 for urgent issue #9"}},
		{name: "an urgent issue takes the free place first, whatever its release", facts: CrewFacts{
			ContainerReady: true, Issues: []Issue{in(v011, ready(5)), in(v020, urgent(ready(8))), urgent(ready(12))}},
			want: CrewStep{Phase: CrewWorking, Ready: []int{5},
				Start:   []IssueStart{{Issue: 8, Task: "cel-ls-8", Attempt: 1}, {Issue: 12, Task: "cel-ls-12", Attempt: 1}},
				Message: "Starting Task cel-ls-8 for urgent issue #8 and Task cel-ls-12 for urgent issue #12"}},
		{name: "an urgent issue still waits on open dependencies", facts: CrewFacts{ContainerReady: true,
			Issues: []Issue{urgent(ready(9, "#3"))}},
			want: CrewStep{Phase: CrewIdle, Waiting: []int{9}, Message: "No ready issues; 1 waits on open dependencies"}},
		{name: "an urgent Standard change starts though the other places are taken", facts: CrewFacts{
			ContainerReady: true, Issues: []Issue{ready(4), urgent(Issue{Number: 70, Body: criteria, StandardChange: true})},
			Tasks: []CrewTask{{Name: "cel-ls-3", Issue: 3}, {Name: "cel-ls-9", Issue: 9, Blocked: true,
				StandardChange: &IssueChange{Issue: 70, Paths: []string{"Makefile"}}}}},
			want: CrewStep{Phase: CrewWorking, Active: []string{"cel-ls-3", "cel-ls-9"}, Ready: []int{4},
				Start:   []IssueStart{{Issue: 70, Task: "cel-ls-70", Attempt: 1, AllowedPaths: []string{"Makefile"}}},
				Message: "Starting Task cel-ls-70 for urgent issue #70"}},
		{name: "a paused Crew starts no urgent issue", facts: CrewFacts{Paused: true, ContainerReady: true,
			Issues: []Issue{urgent(ready(9))}},
			want: CrewStep{Phase: CrewPaused, Ready: []int{9}, Message: "Paused; not picking up issues"}},
		{name: "paused, a later release still shows as waiting", facts: CrewFacts{Paused: true, ContainerReady: true,
			Issues: []Issue{in(v011, ready(58)), in(v020, ready(44))}},
			want: CrewStep{Phase: CrewPaused, Ready: []int{58}, Waiting: []int{44},
				Message: "Paused; not picking up issues"}},
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

// between returns the text of s after the first start and before the next end.
func between(s, start, end string) (string, bool) {
	_, after, ok := strings.Cut(s, start)
	if !ok {
		return "", false
	}
	inner, _, ok := strings.Cut(after, end)
	return inner, ok
}
