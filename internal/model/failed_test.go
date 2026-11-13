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
	"slices"
	"strings"
	"testing"
	"time"
)

const clearBody = "Add a CLI.\n\n## Acceptance criteria\n\n- [ ] `lens check` prints the result\n"

func TestIsFailedIssue(t *testing.T) {
	if !IsFailedIssue([]string{"kind/feature", LabelNeedsHuman}) {
		t.Error("an issue labelled needs-human is not failed")
	}
	if IsFailedIssue([]string{LabelReady}) || IsFailedIssue(nil) {
		t.Error("an issue not labelled needs-human is failed")
	}
}

func TestFailedReason(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 10, 7, 12, m, 0, 0, time.UTC) }
	failed := func(name string, m int, msg string) IssueTask {
		return IssueTask{Name: name, Issue: 1, Failed: true, Message: msg, Created: at(m)}
	}
	tests := []struct {
		name      string
		labels    []string
		body      string
		tasks     []IssueTask
		kind      string
		reason    []string // substrings
		tries     int
		notReason string
	}{
		{"no acceptance criteria", nil, "Add a CLI.", nil, FailedUnclear,
			[]string{`no "Acceptance criteria" section`, "Say in a prompt"}, 0, LabelReady},
		{"no body, after a failed Task", nil, "", []IssueTask{failed("cli-1", 1, "no changes")}, FailedUnclear,
			[]string{"no description"}, 1, ""},
		{"merged", nil, clearBody, []IssueTask{failed("cli-1", 1, "x"), {Name: "cli-1-2", Issue: 1, Merged: true, Created: at(2)}},
			FailedMerged, []string{"Task `cli-1-2` already merged it", "close it"}, 2, ""},
		{"exhausted", nil, clearBody, []IssueTask{failed("cli-1-3", 3, "c"), failed("cli-1", 1, "a"), failed("cli-1-2", 2, "b")},
			FailedExhausted, []string{"tried it 3 times (Tasks `cli-1`, `cli-1-2`, `cli-1-3`)"}, 3, ""},
		{"no-CI failures don't exhaust it", nil, clearBody, []IssueTask{failed("cli-1", 1, "a"), failed("cli-1-2", 2, "b"),
			{Name: "cli-1-3", Issue: 1, Failed: true, NoCI: true, Message: "No CI run started", Created: at(3)}},
			FailedTask, []string{"Task `cli-1-3` failed: No CI run started"}, 3, "tried"},
		{"#2037: a Task whose agent couldn't see the issue doesn't exhaust it", nil, clearBody, []IssueTask{
			{Name: "cli-1", Issue: 1, Failed: true, Unseen: true, Message: "The agent says it can't see the issue",
				Created: at(1)}, failed("cli-1-2", 2, "a"), failed("cli-1-3", 3, "b")},
			FailedTask, []string{"Task `cli-1-3` failed: b"}, 3, "tried"},
		{"the newest failed Task", nil, clearBody, []IssueTask{
			failed("cli-1-2", 2, "Failed after 3 rounds:\nthe agent made no changes"), failed("cli-1", 1, "older"),
			{Name: "cli-2", Issue: 2, Failed: true, Message: "another issue's", Created: at(5)}},
			FailedTask, []string{"Task `cli-1-2` failed: Failed after 3 rounds: the agent made no changes"}, 2, "older"},
		{"a blocked Task", nil, clearBody, []IssueTask{failed("cli-1", 1, "older"), {Name: "cli-1-2", Issue: 1, Failed: true,
			Blocked: true, Message: "The agent can't start the issue: engine#10 must be released", Created: at(2)}},
			FailedBlocked, []string{"Task `cli-1-2` is blocked: The agent can't start the issue: engine#10 must be released"},
			2, "older"},
		{"a failed Task without a message", nil, clearBody, []IssueTask{failed("cli-1", 1, "")},
			FailedTask, []string{"Task `cli-1` failed"}, 1, ":"},
		{"a person, a Task still working", nil, clearBody, []IssueTask{{Name: "cli-1", Issue: 1, Created: at(1)}},
			FailedPerson, []string{"Handed back by a person"}, 1, ""},
		{"by hand", []string{LabelNeedsHuman, LabelByHand}, byHandBody, []IssueTask{failed("cli-1", 1, "x")}, FailedByHand,
			[]string{"Needs a person: No desktop editor in the sandbox; no phone."}, 1, "failed"},
		{"by hand without a why", []string{LabelByHand}, clearBody, nil, FailedByHand,
			[]string{"Needs a person; see the issue."}, 0, ""},
		{"a person, no Tasks", nil, clearBody, []IssueTask{{Name: "goal", Failed: true}}, FailedPerson, nil, 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kind, reason, tries := FailedReason(1, tc.labels, tc.body, tc.tasks)
			if kind != tc.kind || tries != tc.tries {
				t.Errorf("kind, tries = %s, %d; want %s, %d (reason %q)", kind, tries, tc.kind, tc.tries, reason)
			}
			for _, want := range tc.reason {
				if !strings.Contains(reason, want) {
					t.Errorf("reason %q lacks %q", reason, want)
				}
			}
			if tc.notReason != "" && strings.Contains(reason, tc.notReason) {
				t.Errorf("reason %q has %q", reason, tc.notReason)
			}
		})
	}
}

func TestParsePlanFailedAnswers(t *testing.T) {
	files := map[string]string{PlanModelFile: happyModel, PlanChoicesFile: "- one\n",
		PlanFailedFile: "- container: cli\n  issue: 1\n  answer: retry\n  why: The engine's checker is merged.\n" +
			"  clarify: |\n    Import the checker.\n- container: cli\n  issue: 2\n  answer: close\n  why: Split.\n"}
	plan, err := ParsePlan(files)
	if err != nil {
		t.Fatal(err)
	}
	want := []FailedAnswer{
		{Container: "cli", Issue: 1, Answer: AnswerRetry, Why: "The engine's checker is merged.", Clarify: "Import the checker.\n"},
		{Container: "cli", Issue: 2, Answer: AnswerClose, Why: "Split."},
	}
	if !reflect.DeepEqual(plan.FailedAnswers, want) {
		t.Errorf("answers = %+v, want %+v", plan.FailedAnswers, want)
	}

	files[PlanFailedFile] = "- container: cli\n  number: 1\n"
	if _, err := ParsePlan(files); err == nil || !strings.Contains(err.Error(), PlanFailedFile) {
		t.Errorf("an unknown field: err = %v, want one naming %s", err, PlanFailedFile)
	}
	files[PlanFailedFile] = ""
	if plan, err := ParsePlan(files); err != nil || len(plan.FailedAnswers) != 0 {
		t.Errorf("an empty file: answers %v, err %v; want none and no error", plan.FailedAnswers, err)
	}
	delete(files, PlanFailedFile)
	if plan, err := ParsePlan(files); err != nil || plan.FailedAnswers != nil {
		t.Errorf("no file: answers %v, err %v; want none and no error", plan.FailedAnswers, err)
	}
}

// fa is a plan's answer to cli or web's issue number.
func fa(container string, number int, answer, why, clarify string) FailedAnswer {
	return FailedAnswer{Container: container, Issue: number, Answer: answer, Why: why, Clarify: clarify}
}

// ask is an ask about cli's issue number recommending r.
func ask(number int, r *Recommendation) FailedAnswer {
	return FailedAnswer{Container: "cli", Issue: number, Answer: AnswerAsk, Why: "It needs a paid API key.", Recommend: r}
}

func TestCheckFailedAnswers(t *testing.T) {
	waiting := []FailedIssue{
		{Container: "cli", Number: 1, Kind: FailedTask},
		{Container: "cli", Number: 2, Kind: FailedMerged},
		{Container: "cli", Number: 3, Kind: FailedExhausted},
		{Container: "cli", Number: 4, Kind: FailedUnclear, Body: "Add a CLI."},
		{Container: "cli", Number: 5, Kind: FailedPerson},
		{Container: "cli", Number: 6, Kind: FailedUnclear, Body: "Add a CLI.\n\n## Acceptance criteria\n\nIt works.\n"},
		{Container: "cli", Number: 7, Kind: FailedByHand},
		{Container: "cli", Number: 8, Kind: FailedTask, ProposalGone: "standard/cli-1"},
	}
	const criteria = "Use the engine's checker.\n\n## Acceptance criteria\n\n- [ ] `lens check` uses it\n"
	tests := []struct {
		name    string
		answers []FailedAnswer
		want    []string // substrings, one per refusal
	}{
		{"retry and close", []FailedAnswer{
			fa("cli", 1, AnswerRetry, "The checker is merged now.", "Import it from cel-lens-core."),
			fa("cli", 2, AnswerClose, "Merged already.", ""),
			fa("cli", 3, AnswerClose, "Filed again as #9, split.", ""),
			fa("cli", 4, AnswerRetry, "The prompt says what it does.", criteria),
			fa("cli", 5, AnswerRetry, "The prompt asks to try it again.", ""),
			fa("cli", 7, AnswerClose, "The prompt says the screenshot is in.", "")}, nil},
		{"none", nil, nil},
		{"unknown issue", []FailedAnswer{fa("cli", 9, AnswerClose, "why", "")}, []string{"cli#9, which is not a waiting"}},
		{"retry a Standard change whose proposal is gone", []FailedAnswer{fa("cli", 8, AnswerRetry, "why", "")},
			[]string{"proposal branch `standard/cli-1` is gone"}},
		{"close a Standard change whose proposal is gone", []FailedAnswer{fa("cli", 8, AnswerClose, "why", "")}, nil},
		{"ask recommending a retry of a Standard change whose proposal is gone", []FailedAnswer{
			ask(8, &Recommendation{Answer: AnswerRetry, Why: "why"})}, []string{"is gone"}},
		{"another Container's number", []FailedAnswer{fa("web", 1, AnswerClose, "why", "")}, []string{"web#1"}},
		{"answered twice", []FailedAnswer{fa("cli", 1, AnswerClose, "why", ""), fa("cli", 1, AnswerRetry, "why", "")},
			[]string{"twice"}},
		{"no why", []FailedAnswer{fa("cli", 1, AnswerRetry, " ", "")}, []string{"no why"}},
		{"long why", []FailedAnswer{fa("cli", 1, AnswerClose, strings.Repeat("a", MaxAnswerWhy+1), "")},
			[]string{"keep it under"}},
		{"unknown answer", []FailedAnswer{fa("cli", 1, "split", "why", "")}, []string{`"split"`}},
		{"retry merged", []FailedAnswer{fa("cli", 2, AnswerRetry, "why", "")}, []string{"merged already"}},
		{"retry by hand", []FailedAnswer{fa("cli", 7, AnswerRetry, "why", criteria)}, []string{"needs a person, not agents"}},
		{"retry exhausted", []FailedAnswer{fa("cli", 3, AnswerRetry, "why", criteria)}, []string{"tried 3 times"}},
		{"retry unclear without clarify", []FailedAnswer{fa("cli", 4, AnswerRetry, "why", "")},
			[]string{"no acceptance criteria"}},
		{"retry unclear, clarify without a checklist", []FailedAnswer{
			fa("cli", 4, AnswerRetry, "why", "## Acceptance criteria\n\nIt works.\n")}, []string{"no acceptance criteria"}},
		{"retry unclear whose empty criteria come first", []FailedAnswer{fa("cli", 6, AnswerRetry, "why", criteria)},
			[]string{"no clarify can add one: close it, and file it again as a new issue with criteria"}},
		{"retry unclear whose empty criteria come first, without clarify", []FailedAnswer{fa("cli", 6, AnswerRetry, "why", "")},
			[]string{"close it, and file it again"}},
		{"close unclear whose empty criteria come first", []FailedAnswer{fa("cli", 6, AnswerClose, "Filed again as #9.", "")},
			nil},
		{"long clarify", []FailedAnswer{fa("cli", 1, AnswerRetry, "why", strings.Repeat("a", MaxClarify+1))},
			[]string{"keep it under"}},
		{"close with clarify", []FailedAnswer{fa("cli", 1, AnswerClose, "why", "More.")}, []string{"only a retry clarifies"}},
		{"wait and ask", []FailedAnswer{
			fa("cli", 1, AnswerWait, "The engine has no release yet.", ""),
			ask(2, &Recommendation{Answer: AnswerClose, Why: "Merged already."}),
			ask(4, &Recommendation{Answer: AnswerRetry, Why: "Say what it does.", Clarify: criteria}),
			ask(5, &Recommendation{Answer: AnswerWait, Why: "Until the key is there."})}, nil},
		{"wait with clarify", []FailedAnswer{fa("cli", 1, AnswerWait, "why", "More.")},
			[]string{"waits on cli#1 with a clarify"}},
		{"wait with no why", []FailedAnswer{fa("cli", 1, AnswerWait, "", "")}, []string{"no why"}},
		{"wait with a recommend", []FailedAnswer{{Container: "cli", Issue: 1, Answer: AnswerWait, Why: "why",
			Recommend: &Recommendation{Answer: AnswerClose, Why: "why"}}}, []string{"only an ask recommends"}},
		{"ask with no recommend", []FailedAnswer{ask(1, nil)}, []string{"no recommend"}},
		{"ask recommending an ask", []FailedAnswer{ask(1, &Recommendation{Answer: AnswerAsk, Why: "why"})},
			[]string{`recommending "ask"`}},
		{"ask recommending with no why", []FailedAnswer{ask(1, &Recommendation{Answer: AnswerClose})},
			[]string{"recommends for cli#1 with no why"}},
		{"ask with a clarify", []FailedAnswer{{Container: "cli", Issue: 1, Answer: AnswerAsk, Why: "why", Clarify: "More.",
			Recommend: &Recommendation{Answer: AnswerRetry, Why: "why"}}}, []string{"put it in the recommend"}},
		{"ask recommending a retry of an exhausted issue", []FailedAnswer{
			ask(3, &Recommendation{Answer: AnswerRetry, Why: "why", Clarify: criteria})},
			[]string{"recommends: retries cli#3, which agents tried 3 times"}},
		{"ask recommending a retry of a merged issue", []FailedAnswer{
			ask(2, &Recommendation{Answer: AnswerRetry, Why: "why"})}, []string{"merged already"}},
		{"ask recommending a retry of an unclear issue without criteria", []FailedAnswer{
			ask(4, &Recommendation{Answer: AnswerRetry, Why: "why"})}, []string{"no acceptance criteria"}},
		{"ask recommending a close with clarify", []FailedAnswer{
			ask(1, &Recommendation{Answer: AnswerClose, Why: "why", Clarify: "More."})}, []string{"only a retry clarifies"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckFailedAnswers(tc.answers, waiting)
			if len(got) != len(tc.want) {
				t.Fatalf("refusals = %v, want %d", got, len(tc.want))
			}
			for i, r := range got {
				if r.Subject != PlanFailedFile || !strings.Contains(r.Reason, tc.want[i]) {
					t.Errorf("refusal %d = %v, want one on %s containing %q", i, r, PlanFailedFile, tc.want[i])
				}
			}
		})
	}
}

func TestClarifiedBody(t *testing.T) {
	if got := ClarifiedBody("Add a CLI.\n", 2, "  "); got != "Add a CLI.\n" {
		t.Errorf("no clarification: %q", got)
	}
	want := "Add a CLI.\n\n## Clarification (Brief round 2)\n\nImport the checker.\n"
	if got := ClarifiedBody("Add a CLI.\n\n", 2, "Import the checker.\n"); got != want {
		t.Errorf("ClarifiedBody = %q, want %q", got, want)
	}
	if got := ClarifiedBody(want, 2, "Import the checker.\n"); got != want {
		t.Errorf("clarified twice in one round: %q", got)
	}
	if got := ClarifiedBody(want, 3, "Use v2."); !strings.HasSuffix(got, "## Clarification (Brief round 3)\n\nUse v2.\n") ||
		!strings.Contains(got, "round 2") {
		t.Errorf("a later round's clarification: %q", got)
	}
}

// #976: a close the Git host refuses for an open dependency says so.
func TestCloseRefused(t *testing.T) {
	comment := CloseRefusedComment(FailedAnswer{Answer: AnswerClose, Why: "The prompt\ndrops it."}, "lens", 91, false,
		[]string{"#378", "lens/web#2"})
	for _, s := range []string{"Brief `lens`'s round 91 answered this with close", "it waits on #378, lens/web#2",
		"stays handed back", "The planner: The prompt drops it."} {
		if !strings.Contains(comment, s) {
			t.Errorf("CloseRefusedComment() lacks %q:\n%s", s, comment)
		}
	}
	if c := CloseRefusedComment(FailedAnswer{Answer: AnswerClose}, "lens", 4, true, []string{"#3"}); !strings.Contains(c,
		"tending round 4") {
		t.Errorf("CloseRefusedComment() of a tending round = %q", c)
	}
	tests := []struct {
		refused []string
		want    string
	}{
		{nil, ""},
		{[]string{"core#367 (waits on #378)"},
			"; could not close core#367 (waits on #378), which stays handed back: close what it waits on first"},
		{[]string{"core#1 (waits on #2)", "core#3 (waits on #4)"},
			"; could not close core#1 (waits on #2), core#3 (waits on #4), which stay handed back: " +
				"close what they wait on first"},
	}
	for _, tt := range tests {
		if got := CloseRefused(tt.refused); got != tt.want {
			t.Errorf("CloseRefused(%q) = %q, want %q", tt.refused, got, tt.want)
		}
	}
}

func TestFailedComment(t *testing.T) {
	prompts := []string{"Try the CLI again now that\nthe checker is merged."}
	retry := FailedComment(FailedAnswer{Answer: AnswerRetry, Why: "The prompt says so.", Clarify: "Import it."},
		"lens", 2, false, prompts, []string{"lens-cli#4"})
	for _, s := range []string{"Tried again for Brief `lens` in round 2", LabelReady, "clarification for round 2",
		"The planner: The prompt says so.", "> Try the CLI again now that the checker is merged."} {
		if !strings.Contains(retry, s) {
			t.Errorf("retry comment lacks %q:\n%s", s, retry)
		}
	}
	if strings.Contains(retry, "lens-cli#4") {
		t.Errorf("retry comment names the round's issues:\n%s", retry)
	}
	closed := FailedComment(FailedAnswer{Answer: AnswerClose, Why: "Split in two."}, "lens", 2, false, prompts,
		[]string{"lens-cli#4 Parse flags", "lens-cli#5 Print results"})
	for _, s := range []string{"Closed for Brief `lens` in round 2.", "The planner: Split in two.",
		"- lens-cli#4 Parse flags", "- lens-cli#5 Print results", "> Try the CLI again"} {
		if !strings.Contains(closed, s) {
			t.Errorf("close comment lacks %q:\n%s", s, closed)
		}
	}
	tended := FailedComment(FailedAnswer{Answer: AnswerRetry, Why: "The checker is merged now."}, "lens", 3, true,
		nil, nil)
	want := "Tried again by Brief `lens` in tending round 3, with no prompt: its autopilot is On. " +
		"The Crew picks it up as " + LabelReady + ".\n\nThe planner: The checker is merged now."
	if tended != want {
		t.Errorf("tending comment:\n%s\nwant:\n%s", tended, want)
	}
}

// The guide's plan/failed.yaml example parses and is accepted.
func TestPlannerGuideFailedExample(t *testing.T) {
	start := strings.Index(plannerGuideText, "- container: cel-lens-cli")
	end := strings.Index(plannerGuideText[start:], "```")
	if start < 0 || end < 0 {
		t.Fatal("PLANNER.md lacks the plan/failed.yaml example")
	}
	example := strings.ReplaceAll(plannerGuideText[start:start+end], "\n    ", "\n")
	answers, err := parseFailedAnswers(example)
	if err != nil || len(answers) != 1 {
		t.Fatalf("parseFailedAnswers(example) = %+v, %v", answers, err)
	}
	waiting := []FailedIssue{{Container: "cel-lens-cli", Number: 1, Kind: FailedTask, Body: clearBody}}
	if refusals := CheckFailedAnswers(answers, waiting); len(refusals) > 0 {
		t.Errorf("the example is refused: %v", refusals)
	}
}

func TestWorkspaceFailed(t *testing.T) {
	got, err := marshalDocument(workspaceFailed([]FailedIssue{
		{Container: "web", Number: 2, Title: "Add\nalt text", Kind: FailedPerson, Reason: "Handed back by a person; see the issue.",
			Body: clearBody, URL: "http://gitea/apps/web/issues/2"},
		{Container: "cli", Number: 1, Title: "The CLI", Kind: FailedTask, Reason: "Task `cli-1` failed:\nno changes",
			Tries: 1, Body: "Add a CLI.\n"},
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	want := "- container: cli\n  issue: 1\n  title: The CLI\n  kind: failed\n  reason: 'Task `cli-1` failed: no changes'\n" +
		"  tries: 1\n  body: Add a CLI.\n" +
		"- container: web\n  issue: 2\n  title: Add alt text\n  kind: person\n" +
		"  reason: Handed back by a person; see the issue.\n  tries: 0\n  body: |-\n    Add a CLI.\n\n" +
		"    ## Acceptance criteria\n\n    - [ ] `lens check` prints the result\n"
	if got != want {
		t.Errorf("%s =\n%s\nwant\n%s", WorkspaceFailedFile, got, want)
	}
	if got, err := marshalDocument(workspaceFailed(nil, nil)); err != nil || got != "[]\n" {
		t.Errorf("with none: %q, %v", got, err)
	}
}

func TestBriefWorkspaceFailed(t *testing.T) {
	failed := []FailedIssue{{Container: "cli", Number: 1, Title: "The CLI", Kind: FailedTask,
		Reason: "Task `cli-1` failed: no changes", Tries: 1, Body: "Add a CLI.\n"}}
	files, err := BriefWorkspaceFiles(BriefWorkspace{Brief: "todo", Idea: "A todo list", GitProvider: "gitea",
		Failed: failed})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(files[WorkspaceFailedFile], "issue: 1") ||
		!strings.Contains(files[BriefFile], "## Failed issues\n\n1 were handed back") {
		t.Errorf("%s:\n%s\nbrief.md:\n%s", WorkspaceFailedFile, files[WorkspaceFailedFile], files[BriefFile])
	}
	if !slices.Contains(WorkspaceFiles, WorkspaceFailedFile) {
		t.Errorf("WorkspaceFiles lacks %s", WorkspaceFailedFile)
	}
	files, err = BriefWorkspaceFiles(BriefWorkspace{Brief: "todo", Idea: "A todo list", GitProvider: "gitea"})
	if err != nil {
		t.Fatal(err)
	}
	if files[WorkspaceFailedFile] != "[]\n" || strings.Contains(files[BriefFile], "Failed issues") {
		t.Errorf("with none: %s = %q, brief.md:\n%s", WorkspaceFailedFile, files[WorkspaceFailedFile], files[BriefFile])
	}

	many := make([]FailedIssue, MaxFailedIssues+3)
	for i := range many {
		many[i] = FailedIssue{Container: "cli", Number: len(many) - i, Kind: FailedPerson}
	}
	out := workspaceFailed(many, nil)
	if len(out) != MaxFailedIssues || out[0].Issue != 1 || out[len(out)-1].Issue != MaxFailedIssues {
		t.Errorf("workspaceFailed of %d lists %d, from #%d to #%d", len(many), len(out), out[0].Issue,
			out[len(out)-1].Issue)
	}
}

func TestFailedMessage(t *testing.T) {
	cli := FailedIssue{Container: "cli", Number: 1, Title: "The\nCLI", Kind: FailedTask,
		Reason: "Task `cli-1` failed: Failed after 3 rounds: the agent made no changes."}
	for _, tc := range []struct {
		name    string
		listed  []FailedIssue
		waiting int
		want    string
	}{
		{"none", nil, 0, ""},
		{"one", []FailedIssue{cli}, 1, "Issue cli#1 \"The CLI\" was handed back: Task `cli-1` failed: Failed after 3 " +
			"rounds: the agent made no changes. Say in a prompt whether to try again."},
		{"more", []FailedIssue{cli, {Container: "web", Number: 2}}, 3, "Issue cli#1 \"The CLI\" was handed back: " +
			"Task `cli-1` failed: Failed after 3 rounds: the agent made no changes; and 2 more. " +
			"Say in a prompt whether to try again."},
	} {
		if got := FailedMessage(tc.listed, tc.waiting); got != tc.want {
			t.Errorf("%s: FailedMessage =\n%q\nwant\n%q", tc.name, got, tc.want)
		}
	}
}

// An unquoted why or clarify with " #" in it is cut there, as YAML reads the
// rest as a comment; the plan is refused rather than the text shortened (#630).
func TestPlanTextCutByAComment(t *testing.T) {
	const full = "Its only change, #136, already shipped in v0.2.0."
	ask := func(why string) string {
		return "- container: engine\n  issue: 158\n  answer: ask   # or wait\n  why: It needs you.\n" +
			"  recommend:\n    answer: close\n    why: " + why + "\n"
	}
	tests := []struct {
		name, failed, why, refused string
	}{
		{"unquoted, cut at #136", ask(full), "", `line 7: why: reads only "Its only change,", since YAML takes ` +
			`"#136, already shipped in v0.2.0." for a comment; quote the text`},
		{"quoted", ask(`"` + full + `"`), full, ""},
		{"quoted, with a comment after it", ask(`'` + full + `' # the release step`), full, ""},
		{"a block", ask("|\n      " + full), full + "\n", ""},
		{"a # with no space before it", ask("Merged in PR#136, so it shipped."), "Merged in PR#136, so it shipped.", ""},
		{"wrapped before #136", ask("Its only change,\n      #136, already shipped in v0.2.0."), "",
			`line 7: why: reads only "Its only change,", since YAML takes "#136, already shipped in v0.2.0."`},
		{"wrapped, whole", ask("Its only change, PR 136,\n      already shipped in v0.2.0."),
			"Its only change, PR 136, already shipped in v0.2.0.", ""},
		{"a cut clarify", "- container: engine\n  issue: 158\n  answer: retry\n  why: The prompt says so.\n" +
			"  clarify: Use the checker, #2 merged it.\n", "", `line 5: clarify: reads only "Use the checker,"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := ParseTendPlan(map[string]string{PlanFailedFile: tc.failed})
			if tc.refused != "" {
				if err == nil || !strings.Contains(err.Error(), tc.refused) {
					t.Fatalf("err = %v, want it to say %q", err, tc.refused)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r := plan.FailedAnswers[0].Recommend; r == nil || r.Why != tc.why {
				t.Errorf("recommend = %+v, want why %q", r, tc.why)
			}
		})
	}
	if _, err := parseAnswers("- {container: a, issue: 1, answer: take, why: x}\n- container: a\n  issue: 2\n" +
		"  why: Done in\n    #4 already.\n  answer: skip\n"); err == nil || !strings.Contains(err.Error(), "line 4: why: reads only \"Done in\"") {
		t.Errorf("suggestions: err = %v, want the cut why refused", err)
	}
	if _, err := parsePlanParallel("parallel: 2\nwhy: The prompt, #3, asks for two.\n"); err == nil ||
		!strings.Contains(err.Error(), "line 2: why:") {
		t.Errorf("parallel: err = %v, want the cut why refused", err)
	}
}
