package model

import (
	"strings"
	"testing"
)

// withChecks is happyFiles with checks set on a Container of model.yaml;
// api exists in happyCurrent, web is new.
func withChecks(t *testing.T, container, checks string) Plan {
	t.Helper()
	files := happyFiles()
	if checks != "" {
		anchor := "  frameworkRef: {name: go-service}\n"
		if container == "web" {
			anchor = "  standardRef: {name: web-standard}\n"
		}
		files[PlanModelFile] = strings.Replace(files[PlanModelFile], anchor, anchor+"  checks: "+checks+"\n", 1)
	}
	plan, err := ParsePlan(files)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func planContainer(plan Plan, name string) PlanObject {
	o, _ := findObject(plan.Objects, Ref{KindContainer, name})
	return o
}

func TestParsePlanChecks(t *testing.T) {
	plan := withChecks(t, "api", "BeforeRelease")
	if got := planContainer(plan, "api").Checks; got != ChecksBeforeRelease {
		t.Errorf("api's checks = %q, want %q", got, ChecksBeforeRelease)
	}
	if got := planContainer(plan, "web").Checks; got != "" {
		t.Errorf("web's checks = %q, want it empty: model.yaml leaves it out", got)
	}
}

func TestCheckPlanChecks(t *testing.T) {
	for _, tc := range []struct {
		name      string
		container string
		checks    string // "" leaves checks out
		before    string // api's spec.checks in the namespace
		notMine   bool   // the Brief didn't make api
		newPrompt bool
		changed   bool     // api is among PlanDiff's changed
		refused   []string // what each refusal says, in order
	}{
		{name: "left out"},
		{name: "left out keeps a hand-set value", before: ChecksBeforeRelease},
		{name: "left out on a Container the Brief didn't make", before: ChecksBeforeRelease, notMine: true},
		{name: "a new prompt sets an existing one", container: "api", checks: "BeforeRelease", newPrompt: true,
			changed: true},
		{name: "a new prompt sets it back", container: "api", checks: "EveryChange", before: ChecksBeforeRelease,
			newPrompt: true, changed: true},
		{name: "unchanged needs no prompt", container: "api", checks: "BeforeRelease", before: ChecksBeforeRelease},
		{name: "EveryChange is the unset default", container: "api", checks: "EveryChange"},
		{name: "a new Container's", container: "web", checks: "BeforeRelease", newPrompt: true},
		{name: "a new Container's default needs no prompt", container: "web", checks: "EveryChange"},
		{name: "no new prompt", container: "api", checks: "BeforeRelease", changed: true,
			refused: []string{"sets checks: BeforeRelease in a round with no new prompt"}},
		{name: "a new Container's, no new prompt", container: "web", checks: "BeforeRelease",
			refused: []string{"in a round with no new prompt"}},
		{name: "not the Brief's", container: "api", checks: "BeforeRelease", notMine: true, newPrompt: true,
			changed: true, refused: []string{"the Brief didn't make this Container"}},
		{name: "an invalid value", container: "api", checks: "Nightly", newPrompt: true, changed: true,
			refused: []string{`checks is "Nightly"; write EveryChange or BeforeRelease`}},
		{name: "lowercase is invalid", container: "web", checks: "beforeRelease", newPrompt: true,
			refused: []string{`checks is "beforeRelease"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := withChecks(t, tc.container, tc.checks)
			current := happyCurrent()
			current.Objects[2].Checks = tc.before
			current.NewPrompt = tc.newPrompt
			if !tc.notMine {
				current.BriefContainers = []string{"api"}
			}
			_, changed := PlanDiff(plan, current)
			if got := len(changed) > 0; got != tc.changed {
				t.Errorf("changed = %v, want api changed %v", changed, tc.changed)
			}
			refusals := CheckPlan(plan, current)
			if len(refusals) != len(tc.refused) {
				t.Fatalf("refusals = %v, want %d saying %q", refusals, len(tc.refused), tc.refused)
			}
			for i, r := range refusals {
				subject := "Container/" + tc.container
				if r.Subject != subject || !strings.Contains(r.Reason, tc.refused[i]) {
					t.Errorf("refusal %d = %s, want one about %s saying %q", i, r, subject, tc.refused[i])
				}
			}
		})
	}
}

func TestChecksNotes(t *testing.T) {
	catalog := happyCurrent().Catalog // web-standard can't run the full check only before a release
	for _, tc := range []struct {
		name      string
		container string
		checks    string
		can       bool // web-standard has checksBeforeRelease
		want      string
	}{
		{name: "left out"},
		{name: "EveryChange", container: "web", checks: "EveryChange"},
		{name: "the Standard can", container: "web", checks: "BeforeRelease", can: true},
		{name: "the Standard can't", container: "web", checks: "BeforeRelease",
			want: "web asks for checks BeforeRelease, but its Standard web-standard can't run the full check " +
				"only before a release, so its CI runs it on every change."},
		{name: "no Standard", container: "api", checks: "BeforeRelease",
			want: "api asks for checks BeforeRelease, but has no Standard, so its CI runs no full check to move."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := withChecks(t, tc.container, tc.checks)
			entries := append([]CatalogEntry(nil), catalog...)
			for i := range entries {
				if entries[i].Name == "web-standard" {
					entries[i].ChecksBeforeRelease = tc.can
				}
			}
			got := strings.Join(ChecksNotes(plan, entries), " ")
			if got != tc.want {
				t.Errorf("ChecksNotes = %q, want %q", got, tc.want)
			}
			summary := AppliedRoundSummary(plan, entries)
			if want := strings.TrimSpace(RoundSummary(plan) + " " + tc.want); summary != want {
				t.Errorf("AppliedRoundSummary = %q, want %q", summary, want)
			}
		})
	}
}

// A long summary is clipped so the notes still fit the history.
func TestAppliedRoundSummaryKeepsTheNotes(t *testing.T) {
	plan := withChecks(t, "web", "BeforeRelease")
	plan.Summary = strings.Repeat("The shop grows a lot. ", 100)
	got := AppliedRoundSummary(plan, happyCurrent().Catalog)
	if len(got) > maxRoundSummary {
		t.Errorf("summary is %d bytes, more than %d", len(got), maxRoundSummary)
	}
	if !strings.HasSuffix(got, "so its CI runs it on every change.") {
		t.Errorf("summary drops the note: %q", got)
	}
}

// The planner reads each Container's checks and each Standard's say in
// containers.yaml and catalog.yaml; model.yaml leaves checks out, so a copy
// of it keeps a Container's setting.
func TestBriefWorkspaceShowsChecks(t *testing.T) {
	current := happyCurrent()
	current.Objects[2].Checks = ChecksBeforeRelease
	catalog := current.Catalog
	catalog[3].ChecksBeforeRelease = true // go-standard
	ws := BriefWorkspace{Brief: "shop", Idea: "A shop.", Namespace: "shop", GitProvider: "gitea",
		Objects: current.Objects, Catalog: catalog,
		Containers: []WorkspaceContainer{{Name: "api", Repository: "https://git/api", Checks: ChecksBeforeRelease},
			{Name: "web", Repository: "https://git/web"}}}
	files, err := BriefWorkspaceFiles(ws)
	if err != nil {
		t.Fatal(err)
	}
	if model := files[WorkspaceModelFile]; strings.Contains(model, "checks") {
		t.Errorf("%s says checks:\n%s", WorkspaceModelFile, model)
	}
	containers := files[WorkspaceContainersFile]
	for _, want := range []string{"checks: BeforeRelease", "checks: EveryChange"} {
		if !strings.Contains(containers, want) {
			t.Errorf("%s doesn't say %q:\n%s", WorkspaceContainersFile, want, containers)
		}
	}
	if cat := files[WorkspaceCatalogFile]; strings.Count(cat, "checksBeforeRelease: true") != 1 {
		t.Errorf("%s doesn't say go-standard alone has checksBeforeRelease:\n%s", WorkspaceCatalogFile, cat)
	}
}

func TestPlannerGuideNamesChecks(t *testing.T) {
	for _, want := range []string{"checks: BeforeRelease", "checksBeforeRelease", "(`checks`)"} {
		if !strings.Contains(plannerGuideText, want) {
			t.Errorf("PLANNER.md doesn't say %q", want)
		}
	}
}
