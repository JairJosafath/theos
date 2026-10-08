package model

import (
	"fmt"
	"strings"
	"testing"
)

func TestBriefParallel(t *testing.T) {
	for _, tc := range []struct {
		name         string
		spec, status int32
		want         int32
	}{
		{"nothing says: the default", 0, 0, BriefParallelDefault},
		{"spec.parallel", 2, 0, 2},
		{"a prompt's number", 0, 4, 4},
		{"spec.parallel wins over a prompt", 1, 4, 1},
	} {
		if got := BriefParallel(tc.spec, tc.status); got != tc.want {
			t.Errorf("%s: BriefParallel(%d, %d) = %d, want %d", tc.name, tc.spec, tc.status, got, tc.want)
		}
	}
}

func TestParsePlanParallel(t *testing.T) {
	for _, tc := range []struct {
		name  string
		file  *string
		want  *PlanParallel
		error string
	}{
		{name: "no file says nothing"},
		{name: "a number and why", file: new("parallel: 2\nwhy: 'The prompt says \"up to two at a time\".'\n"),
			want: &PlanParallel{N: 2, Why: `The prompt says "up to two at a time".`}},
		{name: "why on several lines is one line", file: new("parallel: 1\nwhy: |\n  One at\n  a time.\n"),
			want: &PlanParallel{N: 1, Why: "One at a time."}},
		{name: "out of range parses, for the check to refuse", file: new("parallel: 9\nwhy: Nine.\n"),
			want: &PlanParallel{N: 9, Why: "Nine."}},
		{name: "an empty file is refused", file: new("\n"), error: "parallel: is missing"},
		{name: "no number is refused", file: new("why: Two.\n"), error: "parallel: is missing"},
		{name: "no why is refused", file: new("parallel: 2\n"), error: "why: is empty"},
		{name: "a blank why is refused", file: new("parallel: 2\nwhy: '  '\n"), error: "why: is empty"},
		{name: "an unknown key is refused", file: new("parallel: 2\nwhy: Two.\ncontainer: web\n"),
			error: "field container not found"},
		{name: "a word is refused", file: new("parallel: two\nwhy: Two.\n"), error: "cannot unmarshal"},
		{name: "not YAML is refused", file: new("parallel: [2\n"), error: "parallel: N and why:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := happyFiles()
			if tc.file != nil {
				files[PlanParallelFile] = *tc.file
			}
			plan, err := ParsePlan(files)
			if tc.error != "" {
				if err == nil || !strings.HasPrefix(err.Error(), PlanParallelFile+": ") ||
					!strings.Contains(err.Error(), tc.error) {
					t.Fatalf("err = %v, want one naming %s and saying %q", err, PlanParallelFile, tc.error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tc.want == nil && plan.Parallel != nil:
				t.Fatalf("Parallel = %+v, want nil", *plan.Parallel)
			case tc.want != nil && (plan.Parallel == nil || *plan.Parallel != *tc.want):
				t.Fatalf("Parallel = %v, want %+v", plan.Parallel, *tc.want)
			}
		})
	}
}

func TestCheckPlanParallel(t *testing.T) {
	for _, tc := range []struct {
		name      string
		n         int32 // 0 leaves parallel.yaml out
		newPrompt bool
		spec      int32
		refused   []string // what each refusal says, in order
	}{
		{name: "no file, no prompt", newPrompt: false},
		{name: "a new prompt asks for 2", n: 2, newPrompt: true},
		{name: "1 is the least", n: 1, newPrompt: true},
		{name: "5 is the most", n: 5, newPrompt: true},
		{name: "6 is too many", n: 6, newPrompt: true, refused: []string{"a Crew builds 1 to 5 issues at once"}},
		{name: "a negative number", n: -1, newPrompt: true, refused: []string{"says parallel: -1"}},
		{name: "no new prompt", n: 2, refused: []string{"no new prompt"}},
		{name: "spec.parallel is set", n: 2, newPrompt: true, spec: 4,
			refused: []string{"spec.parallel sets it to 4, which wins over a prompt"}},
		{name: "spec.parallel without the file", newPrompt: true, spec: 4},
		{name: "every reason at once", n: 7, spec: 1,
			refused: []string{"says parallel: 7", "no new prompt", "spec.parallel sets it to 1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := happyFiles()
			if tc.n != 0 {
				files[PlanParallelFile] = fmt.Sprintf("parallel: %d\nwhy: The prompt asks.\n", tc.n)
			}
			plan, err := ParsePlan(files)
			if err != nil {
				t.Fatal(err)
			}
			current := happyCurrent()
			current.NewPrompt, current.SpecParallel = tc.newPrompt, tc.spec
			refusals := CheckPlan(plan, current)
			if len(refusals) != len(tc.refused) {
				t.Fatalf("refusals = %v, want %d saying %q", refusals, len(tc.refused), tc.refused)
			}
			for i, r := range refusals {
				if r.Subject != PlanParallelFile || !strings.Contains(r.Reason, tc.refused[i]) {
					t.Errorf("refusal %d = %s, want one about %s saying %q", i, r, PlanParallelFile, tc.refused[i])
				}
			}
		})
	}
}

// A tending round has no new prompt, so it has no say on parallel either.
func TestParseTendPlanRefusesParallel(t *testing.T) {
	_, err := ParseTendPlan(map[string]string{PlanParallelFile: "parallel: 2\nwhy: Two.\n"})
	if err == nil || !strings.Contains(err.Error(), PlanParallelFile) {
		t.Fatalf("err = %v, want a refusal naming %s", err, PlanParallelFile)
	}
}

func TestBriefWorkspaceSaysParallel(t *testing.T) {
	for _, tc := range []struct {
		name                string
		spec, prompt, round int32
		want                []string
		not                 []string
	}{
		{name: "the default", want: []string{"up to 3 issues at once, the operator's default",
			"Write `plan/parallel.yaml` only when a **new** prompt asks how many"}},
		{name: "a prompt's number", prompt: 2, round: 4,
			want: []string{"up to 2 issues at once, as a prompt of round 4 asked", "asks for another number"}},
		{name: "spec.parallel wins", spec: 5, prompt: 2, round: 4,
			want: []string{"up to 5 issues at once, set on the Brief (`spec.parallel`)",
				"Don't write `plan/parallel.yaml`", "say in `plan/choices.md` that it is set on the Brief"},
			not: []string{"round 4"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := BriefWorkspace{Brief: "shop", Idea: "A shop.", Namespace: "shop", GitProvider: "gitea",
				SpecParallel: tc.spec, PromptParallel: tc.prompt, PromptParallelRound: tc.round}
			files, err := BriefWorkspaceFiles(ws)
			if err != nil {
				t.Fatal(err)
			}
			brief := files[BriefFile]
			if !strings.Contains(brief, "\n## Building at once\n") {
				t.Fatalf("brief.md has no Building at once section:\n%s", brief)
			}
			for _, s := range tc.want {
				if !strings.Contains(brief, s) {
					t.Errorf("brief.md doesn't say %q:\n%s", s, brief)
				}
			}
			for _, s := range tc.not {
				if strings.Contains(brief, s) {
					t.Errorf("brief.md says %q:\n%s", s, brief)
				}
			}
		})
	}
}

// The guide's parallel.yaml example parses and passes a round with a new
// prompt.
func TestPlannerGuideShowsParallel(t *testing.T) {
	// The example is in a list item, indented four spaces.
	const fence = "```yaml\n    parallel: "
	start := strings.Index(plannerGuideText, fence)
	if start < 0 {
		t.Fatal("PLANNER.md has no parallel.yaml example")
	}
	start += len("```yaml\n")
	end := strings.Index(plannerGuideText[start:], "```")
	files := happyFiles()
	files[PlanParallelFile] = strings.ReplaceAll("\n"+plannerGuideText[start:start+end], "\n    ", "\n")
	plan, err := ParsePlan(files)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Parallel == nil || plan.Parallel.N != 2 {
		t.Fatalf("the example's Parallel = %v, want 2", plan.Parallel)
	}
	current := happyCurrent()
	current.NewPrompt = true
	if refusals := CheckPlan(plan, current); len(refusals) > 0 {
		t.Fatalf("the example is refused: %v", refusals)
	}
	if !strings.Contains(plannerGuideText, PlanParallelFile) {
		t.Errorf("PLANNER.md doesn't name %s", PlanParallelFile)
	}
}
