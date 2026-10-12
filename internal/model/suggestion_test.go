package model

import (
	"reflect"
	"strings"
	"testing"
)

func TestIsSuggestion(t *testing.T) {
	filed := FollowUpIssue(FollowUp{Title: "Reject control characters", Body: "Todo text takes them.",
		Criteria: []string{"A todo with a control character is refused"}}, "Agent `reviewer`",
		"http://gitea/apps/todo/pulls/4", 3, "todo-3")
	tests := []struct {
		name   string
		labels []string
		body   string
		want   bool
	}{
		{"a filed follow-up, proposed", []string{LabelProposed}, filed, true},
		{"one filed before v0.6", []string{LabelProposed}, "Text.\n\n---\nSuggested by stand-in while reviewing " +
			"approve for #7 (Task `hello-7`). To have agents do it, label it status/ready.", true},
		{"taken: ready", []string{LabelReady}, filed, false},
		{"a person's proposed issue", []string{LabelProposed}, "Suggested by a colleague over lunch.", false},
		{"no labels", nil, filed, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsSuggestion(tc.labels, tc.body); got != tc.want {
				t.Errorf("IsSuggestion = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParsePlanAnswers(t *testing.T) {
	files := map[string]string{PlanModelFile: happyModel, PlanChoicesFile: "- one\n",
		PlanSuggestionsFile: "- container: todo\n  issue: 5\n  answer: take\n  why: The prompt takes input safety.\n"}
	plan, err := ParsePlan(files)
	if err != nil {
		t.Fatal(err)
	}
	want := []SuggestionAnswer{{Container: "todo", Issue: 5, Answer: AnswerTake, Why: "The prompt takes input safety."}}
	if !reflect.DeepEqual(plan.Answers, want) {
		t.Errorf("answers = %+v, want %+v", plan.Answers, want)
	}

	files[PlanSuggestionsFile] = "- container: todo\n  issue: 5\n  answer: take\n  why: It adds a test.\n  fix: true\n"
	if plan, err := ParsePlan(files); err != nil || len(plan.Answers) != 1 || !plan.Answers[0].Fix {
		t.Errorf("fix: true: answers %+v, err %v; want one fix", plan.Answers, err)
	}
	files[PlanSuggestionsFile] = "- container: todo\n  number: 5\n"
	if _, err := ParsePlan(files); err == nil || !strings.Contains(err.Error(), PlanSuggestionsFile) {
		t.Errorf("an unknown field: err = %v, want one naming %s", err, PlanSuggestionsFile)
	}
	files[PlanSuggestionsFile] = ""
	if plan, err := ParsePlan(files); err != nil || len(plan.Answers) != 0 {
		t.Errorf("an empty file: answers %v, err %v; want none and no error", plan.Answers, err)
	}
}

func TestCheckAnswers(t *testing.T) {
	waiting := []Suggestion{
		{Container: "todo", Number: 5, Title: "Reject control characters", Criteria: true},
		{Container: "todo", Number: 6, Title: "Think about caching"},
	}
	tests := []struct {
		name    string
		answers []SuggestionAnswer
		want    []string // substrings, one per refusal
	}{
		{"take and skip", []SuggestionAnswer{
			{"todo", 5, AnswerTake, "The prompt takes input safety.", false},
			{"todo", 6, AnswerSkip, "The prompt skips the rest.", false}}, nil},
		{"none", nil, nil},
		{"not waiting", []SuggestionAnswer{{"todo", 7, AnswerSkip, "why", false}}, []string{"todo#7, which is not a waiting"}},
		{"another Container's number", []SuggestionAnswer{{"web", 5, AnswerSkip, "why", false}}, []string{"web#5"}},
		{"twice", []SuggestionAnswer{{"todo", 5, AnswerSkip, "why", false}, {"todo", 5, AnswerTake, "why", false}},
			[]string{"twice"}},
		{"no criteria", []SuggestionAnswer{{"todo", 6, AnswerTake, "why", false}}, []string{"no acceptance criteria"}},
		{"a fix taken", []SuggestionAnswer{{"todo", 5, AnswerTake, "It adds a test.", true}}, nil},
		{"a fix skipped", []SuggestionAnswer{{"todo", 6, AnswerSkip, "The prompt skips it.", true}},
			[]string{"skips todo#6 as a fix"}},
		{"unknown answer", []SuggestionAnswer{{"todo", 5, "maybe", "why", false}}, []string{`"maybe"`}},
		{"no why", []SuggestionAnswer{{"todo", 5, AnswerTake, " ", false}}, []string{"no why"}},
		{"long why", []SuggestionAnswer{{"todo", 5, AnswerTake, strings.Repeat("a", MaxAnswerWhy+1), false}},
			[]string{"keep it under"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckAnswers(tc.answers, waiting)
			if len(got) != len(tc.want) {
				t.Fatalf("refusals = %v, want %d", got, len(tc.want))
			}
			for i, r := range got {
				if r.Subject != PlanSuggestionsFile || !strings.Contains(r.Reason, tc.want[i]) {
					t.Errorf("refusal %d = %v, want one on %s containing %q", i, r, PlanSuggestionsFile, tc.want[i])
				}
			}
		})
	}
}

func TestSuggestionComment(t *testing.T) {
	prompts := []string{"Take the reviewer's suggestions about input safety,\nskip the rest."}
	take := SuggestionComment(SuggestionAnswer{"todo", 5, AnswerTake, "The prompt takes input safety.", false}, "todo", 2, prompts)
	for _, want := range []string{"Taken for Brief `todo` in round 2", LabelReady, "The planner: The prompt takes input safety.",
		"> Take the reviewer's suggestions about input safety, skip the rest."} {
		if !strings.Contains(take, want) {
			t.Errorf("take comment %q lacks %q", take, want)
		}
	}
	skip := SuggestionComment(SuggestionAnswer{"todo", 6, AnswerSkip, "Skipped: the rest.", false}, "todo", 2, prompts)
	if !strings.HasPrefix(skip, "Closed for Brief `todo` in round 2: not taken.") || strings.Contains(skip, LabelReady) {
		t.Errorf("skip comment = %q", skip)
	}
}

func TestWorkspaceSuggestions(t *testing.T) {
	ws := BriefWorkspace{Brief: "todo", Idea: "A todo list", GitProvider: "gitea", Suggestions: []Suggestion{
		{Container: "web", Number: 2, Title: "Add\nalt text", Criteria: true},
		{Container: "api", Number: 9, Title: "Rate limit"},
	}}
	files, err := BriefWorkspaceFiles(ws)
	if err != nil {
		t.Fatal(err)
	}
	want := "- container: api\n  issue: 9\n  title: Rate limit\n  criteria: false\n" +
		"- container: web\n  issue: 2\n  title: Add alt text\n  criteria: true\n"
	if got := files[WorkspaceSuggestionsFile]; got != want {
		t.Errorf("%s =\n%s\nwant\n%s", WorkspaceSuggestionsFile, got, want)
	}
	if !strings.Contains(files[BriefFile], "## Reviewer suggestions\n\n2 wait") {
		t.Errorf("brief.md doesn't say suggestions wait:\n%s", files[BriefFile])
	}

	files, err = BriefWorkspaceFiles(BriefWorkspace{Brief: "todo", Idea: "A todo list", GitProvider: "gitea"})
	if err != nil {
		t.Fatal(err)
	}
	if files[WorkspaceSuggestionsFile] != "[]\n" || strings.Contains(files[BriefFile], "Reviewer suggestions") {
		t.Errorf("with none: %s = %q, brief.md:\n%s", WorkspaceSuggestionsFile, files[WorkspaceSuggestionsFile], files[BriefFile])
	}
}
