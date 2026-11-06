package model

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalTriage(t *testing.T) {
	got := NormalTriage(Triage{Decision: " Ready ", Reason: " clear ",
		Criteria: []string{"- [ ] Rejects an empty order", "", "* [x]  A test\ncovers it ", "[ ] plain box", "  "}})
	want := Triage{Decision: TriageReady, Reason: "clear",
		Criteria: []string{"Rejects an empty order", "A test covers it", "plain box"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NormalTriage() = %+v, want %+v", got, want)
	}
	many := make([]string, MaxTriageCriteria+3)
	for i := range many {
		many[i] = strings.Repeat("x", 600)
	}
	got = NormalTriage(Triage{Decision: TriageReady, Reason: strings.Repeat("é", 2000), Criteria: many})
	if len(got.Criteria) != MaxTriageCriteria || len(got.Criteria[0]) > maxTriageCriterion ||
		len(got.Reason) > maxTriageText || !strings.HasSuffix(got.Reason, "…") {
		t.Errorf("NormalTriage() kept %d criteria of %d bytes and a reason of %d bytes, want them bounded",
			len(got.Criteria), len(got.Criteria[0]), len(got.Reason))
	}
	// A decision's environments are TestEnvironment names, each once, an
	// env/ label taken for its name (docs/plans/test-environments.md, T13).
	got = NormalTriage(Triage{Decision: TriageReady, Environments: []string{" browser", "env/godot", "browser",
		"Not A Name", "../x", ""}})
	if !reflect.DeepEqual(got.Environments, []string{"browser", "godot"}) {
		t.Errorf("NormalTriage() environments = %v, want browser and godot", got.Environments)
	}
}

func TestTriageProblem(t *testing.T) {
	tests := []struct {
		name string
		d    Triage
		want string
	}{
		{"ready", Triage{Decision: TriageReady, Reason: "clear"}, ""},
		{"spec", Triage{Decision: TriageSpec, Reason: "big"}, ""},
		{"park with its question", Triage{Decision: TriagePark, Reason: "unclear", Question: "Which one?"}, ""},
		{"a duplicate naming its original", Triage{Decision: TriageDuplicate, Reason: "same", Duplicate: 3}, ""},
		{"an unknown decision", Triage{Decision: "wontfix", Reason: "no"}, `"wontfix" is none of ready, spec`},
		{"no decision", Triage{Reason: "no"}, `"" is none of`},
		{"no reason", Triage{Decision: TriageSpec}, "no reason"},
		{"park without a question", Triage{Decision: TriagePark, Reason: "unclear"}, "without the question"},
		{"a duplicate of nothing", Triage{Decision: TriageDuplicate, Reason: "same"}, "without naming the issue"},
		{"a duplicate of itself", Triage{Decision: TriageDuplicate, Reason: "same", Duplicate: 7}, "of itself"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TriageProblem(7, tt.d)
			if tt.want == "" && got != "" || tt.want != "" && !strings.Contains(got, tt.want) {
				t.Errorf("TriageProblem() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlanTriage(t *testing.T) {
	proposed := []string{LabelProposed, "kind/bug"}
	open := func(body string) TriageIssue { return TriageIssue{Number: 7, Body: body, Labels: proposed} }
	ready := Triage{Decision: TriageReady, Reason: "Clear enough.", Criteria: []string{"Rejects an empty order"}}
	tests := []struct {
		name  string
		issue TriageIssue
		d     Triage
		want  TriageActions
	}{
		{"ready, the body without criteria gets them", open("## Goal\n\nValidate orders.\n"), ready, TriageActions{
			Label: LabelReady, Body: "## Goal\n\nValidate orders.\n\n## Acceptance criteria\n\n- [ ] Rejects an empty order\n",
			Comment: "Triaged by Task api-7-triage: ready to build. Clear enough.\n\n" +
				"The acceptance criteria are now in the description.",
			Applied: "Labelled status/ready and wrote 1 acceptance criterion into it"}},
		{"ready, the body with criteria keeps its own", open(criteria), ready, TriageActions{
			Label: LabelReady, Comment: "Triaged by Task api-7-triage: ready to build. Clear enough.",
			Applied: "Labelled status/ready"}},
		// docs/plans/test-environments.md, T13: the triager labels env/<name>
		// where it would have sent the issue to a person for missing tools.
		{"ready, needing a test environment the project has, gets its label",
			TriageIssue{Number: 7, Body: criteria, Labels: proposed, Environments: []string{"browser"}},
			Triage{Decision: TriageReady, Reason: "Clear enough.", Environments: []string{"browser", "android"}},
			TriageActions{Label: LabelReady, EnvLabels: []string{"env/browser"},
				Comment: "Triaged by Task api-7-triage: ready to build. Clear enough.\n\n" +
					"Its runs lease the test environment browser, which the project has.",
				Applied: "Labelled status/ready and env/browser"}},
		{"ready keeps a body with criteria though it gave none", open(criteria),
			Triage{Decision: TriageReady, Reason: "Clear."}, TriageActions{Label: LabelReady,
				Comment: "Triaged by Task api-7-triage: ready to build. Clear.", Applied: "Labelled status/ready"}},
		{"ready with no criteria for a body with none fails", open("Validate orders."),
			Triage{Decision: TriageReady, Reason: "Clear."},
			TriageActions{Problem: "the triager called the issue ready, but gave no acceptance criteria and the issue has none"}},
		{"spec", open("Rebuild billing."), Triage{Decision: TriageSpec, Reason: "Too big for one change."},
			TriageActions{Label: LabelNeedsSpec, Applied: "Labelled status/needs-spec",
				Comment: "Triaged by Task api-7-triage: this issue needs a spec before it is built. Too big for one change."}},
		{"park", open("Make it faster."), Triage{Decision: TriagePark, Reason: "No target given.",
			Question: "How fast, measured how?"}, TriageActions{Label: LabelNeedsHuman,
			Applied: "Labelled status/needs-human and asked its question",
			Comment: "Triaged by Task api-7-triage: this issue needs a person's answer before agents go on.\n\n" +
				"**Question:** How fast, measured how?\n\nWhy: No target given.\n\nAnswer here, make the description " +
				"say what to build with an \"Acceptance criteria\" checklist, and label it status/ready."}},
		{"duplicate", open("Validate orders."), Triage{Decision: TriageDuplicate, Reason: "Same ask.", Duplicate: 3},
			TriageActions{Close: true, Applied: "Closed as a duplicate of #3",
				Comment: "Triaged by Task api-7-triage: a duplicate of #3. Same ask."}},
		{"a decision the operator can't apply", open("x"), Triage{Decision: TriagePark, Reason: "unclear"},
			TriageActions{Problem: "the triager parked the issue without the question a person must answer"}},
		{"an issue closed meanwhile is left alone", TriageIssue{Number: 7, Labels: proposed, Closed: true}, ready,
			TriageActions{Skip: "Issue #7 was closed while it was triaged; nothing done"}},
		{"an issue a person triaged meanwhile is left alone", TriageIssue{Number: 7, Labels: []string{"kind/bug",
			LabelReady}}, Triage{Decision: TriageSpec, Reason: "big"}, TriageActions{
			Skip: "Issue #7 was triaged by a person meanwhile (it has status/ready); nothing done"}},
		{"also when the decision is invalid", TriageIssue{Number: 7}, Triage{Decision: "nonsense"},
			TriageActions{Skip: "Issue #7 was triaged by a person meanwhile (it has no status label); nothing done"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PlanTriage("api-7-triage", tt.issue, tt.d)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("PlanTriage() =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func TestWithAcceptanceCriteria(t *testing.T) {
	two := []string{"Rejects an empty order", "A test covers it"}
	tests := []struct {
		name, body, want string
	}{
		{"appends a section", "## Goal\n\nValidate orders.\n\n", "## Goal\n\nValidate orders.\n\n## Acceptance criteria\n\n" +
			"- [ ] Rejects an empty order\n- [ ] A test covers it\n"},
		{"an empty body becomes the section", "", "## Acceptance criteria\n\n- [ ] Rejects an empty order\n" +
			"- [ ] A test covers it\n"},
		{"fills an empty heading in place", "## Acceptance criteria\n\nTBD\n\n## Notes\nnone\n",
			"## Acceptance criteria\n\n- [ ] Rejects an empty order\n- [ ] A test covers it\n\nTBD\n\n## Notes\nnone\n"},
		{"a heading on the last line", "Validate orders.\n\n## Acceptance criteria",
			"Validate orders.\n\n## Acceptance criteria\n\n- [ ] Rejects an empty order\n- [ ] A test covers it\n"},
		{"leaves a checklist alone", criteria, criteria},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WithAcceptanceCriteria(tt.body, two)
			if got != tt.want {
				t.Errorf("WithAcceptanceCriteria() = %q, want %q", got, tt.want)
			}
			// What the triager wrote must pass the Crew's own check for status/ready.
			if reason := MissingAcceptanceCriteria(got); reason != "" {
				t.Errorf("MissingAcceptanceCriteria(%q) = %q, want none", got, reason)
			}
		})
	}
}

func TestTriageFailedComment(t *testing.T) {
	if got := TriageFailedComment("api-7-triage", "no decision", 1); !strings.Contains(got,
		"stays status/proposed; the Crew triages it again (try 1 of 3 failed)") {
		t.Errorf("TriageFailedComment() = %q", got)
	}
	if got := TriageFailedComment("api-7-triage-3", "no decision", 3); !strings.Contains(got,
		"that was the last of 3 tries, so a person triages it") {
		t.Errorf("TriageFailedComment() = %q", got)
	}
}

func TestNextTriage(t *testing.T) {
	on := func(proposed []ProposedIssue, tasks ...TriageTask) TriageFacts {
		return TriageFacts{On: true, ContainerReady: true, Proposed: proposed, Tasks: tasks}
	}
	issues := []ProposedIssue{{Number: 9}, {Number: 4}, {Number: 2, Milestone: FollowUpMilestone}}
	tests := []struct {
		name  string
		facts TriageFacts
		want  TriageStep
	}{
		{"the lowest proposed issue outside Later", on(issues),
			TriageStep{Start: &TriageStart{Issue: 4, Task: "api-4-triage", Attempt: 1}}},
		{"a Crew without a triager starts none", TriageFacts{ContainerReady: true, Proposed: issues}, TriageStep{}},
		{"nor a paused Crew", TriageFacts{On: true, Paused: true, ContainerReady: true, Proposed: issues}, TriageStep{}},
		{"nor one whose Container isn't Ready", TriageFacts{On: true, Proposed: issues}, TriageStep{}},
		{"one at a time", on(issues, TriageTask{Name: "api-9-triage", Issue: 9}),
			TriageStep{Active: "api-9-triage", ActiveIssue: 9}},
		{"an issue a Triage Task decided is not triaged again", on(issues,
			TriageTask{Name: "api-4-triage", Issue: 4, Final: true}),
			TriageStep{Start: &TriageStart{Issue: 9, Task: "api-9-triage", Attempt: 1}}},
		{"a park a person answered from chat is triaged again, with the next name", on(issues,
			TriageTask{Name: "api-4-triage", Issue: 4, Final: true, Answered: true}),
			TriageStep{Start: &TriageStart{Issue: 4, Task: "api-4-triage-2", Attempt: 2}}},
		{"a failed triage is tried again, with the next name", on(issues,
			TriageTask{Name: "api-4-triage", Issue: 4, Final: true, Failed: true}),
			TriageStep{Start: &TriageStart{Issue: 4, Task: "api-4-triage-2", Attempt: 2}}},
		{"until it failed three times", on(issues,
			TriageTask{Name: "api-4-triage", Issue: 4, Final: true, Failed: true},
			TriageTask{Name: "api-4-triage-2", Issue: 4, Final: true, Failed: true},
			TriageTask{Name: "api-4-triage-3", Issue: 4, Final: true, Failed: true}),
			TriageStep{Start: &TriageStart{Issue: 9, Task: "api-9-triage", Attempt: 1}}},
		{"reviewers' follow-ups in Later only", on([]ProposedIssue{{Number: 2, Milestone: FollowUpMilestone}}),
			TriageStep{}},
		{"a Crew without a triager still reports the one at work", TriageFacts{ContainerReady: true,
			Tasks: []TriageTask{{Name: "api-9-triage", Issue: 9}}}, TriageStep{Active: "api-9-triage", ActiveIssue: 9}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextTriage("api", tt.facts)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NextTriage() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCrewStepAddTriage(t *testing.T) {
	idle := CrewStep{Phase: CrewIdle, Message: "No ready issues"}
	idle.AddTriage(TriageStep{Start: &TriageStart{Issue: 4, Task: "api-4-triage"}})
	if idle.Phase != CrewWorking || idle.Message != "No ready issues; starting Triage Task api-4-triage for issue #4" {
		t.Errorf("AddTriage() = %+v", idle)
	}
	paused := CrewStep{Phase: CrewPaused, Message: "Paused; not picking up issues"}
	paused.AddTriage(TriageStep{Active: "api-4-triage", ActiveIssue: 4})
	if paused.Phase != CrewPaused || paused.Message != "Paused; not picking up issues; Task api-4-triage triages issue #4" {
		t.Errorf("AddTriage() = %+v", paused)
	}
	none := CrewStep{Phase: CrewIdle, Message: "No ready issues"}
	none.AddTriage(TriageStep{})
	if none.Phase != CrewIdle || none.Message != "No ready issues" {
		t.Errorf("AddTriage() = %+v", none)
	}
}

func TestTriageStep(t *testing.T) {
	done := func(result *RunResult) RunState { return RunState{Started: true, Done: true, Result: result} }
	ready := &Triage{Decision: "Ready", Reason: "clear", Criteria: []string{"It works"}}
	tests := []struct {
		name    string
		facts   TaskFacts
		want    TaskAction
		message string
	}{
		{"starts the triager", TaskFacts{Triage: true, MaxRounds: 1}, ActionStartImplement, "Starting the triage"},
		{"waits for it", TaskFacts{Triage: true, Phase: TaskImplementing, Round: 1, MaxRounds: 1,
			Run: RunState{Started: true}}, ActionWait, ""},
		{"applies its decision", TaskFacts{Triage: true, Phase: TaskImplementing, Round: 1, MaxRounds: 1,
			Run: done(&RunResult{Triage: ready})}, ActionTriaged, "The triager decided ready"},
		{"fails at once on an error, with no second round", TaskFacts{Triage: true, Phase: TaskImplementing,
			Round: 1, MaxRounds: 3, Run: done(&RunResult{Error: "the triager gave no JSON decision"})},
			ActionFail, "The triage failed: the triager gave no JSON decision"},
		{"fails on no decision", TaskFacts{Triage: true, Phase: TaskImplementing, Round: 1, MaxRounds: 1,
			Run: done(&RunResult{Summary: "hm"})}, ActionFail, "the triager reported no decision"},
		{"fails on a decision it can't apply", TaskFacts{Triage: true, Phase: TaskImplementing, Round: 1,
			MaxRounds: 1, Run: done(&RunResult{Triage: &Triage{Decision: TriagePark, Reason: "unclear"}})},
			ActionFail, "without the question"},
		{"fails on a sandbox that ended without a result", TaskFacts{Triage: true, Phase: TaskImplementing,
			Round: 1, MaxRounds: 1, Run: RunState{Started: true, Done: true, Failure: "OOMKilled"}},
			ActionFail, "The triage failed: The agent's sandbox ended without a result: OOMKilled"},
		{"a blocked note is no decision", TaskFacts{Triage: true, Phase: TaskImplementing, Round: 1,
			MaxRounds: 1, Run: done(&RunResult{Blocked: "needs #3"})}, ActionFail, "reported no decision"},
		{"waits for an unavailable agent at no cost", TaskFacts{Triage: true, Phase: TaskImplementing, Round: 1,
			MaxRounds: 1, Run: done(&RunResult{Unavailable: true})}, ActionAgentUnavailable, ""},
		{"waits for a free slot", TaskFacts{Triage: true, MaxRounds: 1, NoFreeSlot: true}, ActionWait,
			"Waiting for a free agent slot"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextTaskStep(tt.facts)
			if got.Action != tt.want || !strings.Contains(got.Message, tt.message) {
				t.Errorf("NextTaskStep() = %+v, want %s with %q", got, tt.want, tt.message)
			}
		})
	}
}

func TestStandInTriage(t *testing.T) {
	tests := []struct {
		issue string
		want  string
	}{
		{"Issue #4: Validate orders\n\nReject empty orders.", TriageReady},
		{"Issue #4: Billing\n\nstand-in: spec", TriageSpec},
		{"Issue #4: Faster\n\nStand-in: park", TriagePark},
		{"Issue #4: Again\n\nstand-in: duplicate of #2", TriageDuplicate},
	}
	for _, tt := range tests {
		got := StandInTriage(tt.issue)
		if got.Decision != tt.want || TriageProblem(4, got) != "" {
			t.Errorf("StandInTriage(%q) = %+v, want a valid %s", tt.issue, got, tt.want)
		}
	}
	if got := StandInTriage("stand-in: duplicate of #2"); got.Duplicate != 2 {
		t.Errorf("StandInTriage() duplicates #%d, want #2", got.Duplicate)
	}
}
