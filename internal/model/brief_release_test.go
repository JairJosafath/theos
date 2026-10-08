package model

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParsePlanReleases(t *testing.T) {
	for _, tc := range []struct {
		name  string
		file  *string
		want  []string
		error string
	}{
		{name: "no file releases nothing"},
		{name: "an empty file releases nothing", file: new("\n")},
		{name: "an empty list releases nothing", file: new("containers: []\n")},
		{name: "sorted, each once", file: new("containers: [web, api, web]\n"), want: []string{"api", "web"}},
		{name: "a block list", file: new("containers:\n  - sprout\n"), want: []string{"sprout"}},
		{name: "an unknown key is refused", file: new("containers: [web]\npublish: true\n"),
			error: "field publish not found"},
		{name: "a misspelt key is refused", file: new("container: [web]\n"), error: "field container not found"},
		{name: "not a list is refused", file: new("containers: web\n"), error: "cannot unmarshal"},
		{name: "not YAML is refused", file: new("containers: [web\n"), error: "containers: [name, ...]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := happyFiles()
			if tc.file != nil {
				files[PlanReleasesFile] = *tc.file
			}
			plan, err := ParsePlan(files)
			if tc.error != "" {
				if err == nil || !strings.HasPrefix(err.Error(), PlanReleasesFile+": ") ||
					!strings.Contains(err.Error(), tc.error) {
					t.Fatalf("err = %v, want one naming %s and saying %q", err, PlanReleasesFile, tc.error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(plan.Releases, tc.want) {
				t.Fatalf("Releases = %v, want %v", plan.Releases, tc.want)
			}
		})
	}
}

func TestCheckPlanReleases(t *testing.T) {
	for _, tc := range []struct {
		name     string
		releases []string
		refused  []string // the names refused, in order
	}{
		{name: "nothing released"},
		{name: "a Container the plan creates", releases: []string{"web"}},
		{name: "a Container that exists", releases: []string{"api"}},
		{name: "a name that isn't in the model", releases: []string{"api", "sprout"}, refused: []string{"sprout"}},
		{name: "a DataStore is not a Container", releases: []string{"db"}, refused: []string{"db"}},
		{name: "the SoftwareSystem is not a Container", releases: []string{"shop"}, refused: []string{"shop"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := happyFiles()
			if tc.releases != nil {
				files[PlanReleasesFile] = "containers: [" + strings.Join(tc.releases, ", ") + "]\n"
			}
			plan, err := ParsePlan(files)
			if err != nil {
				t.Fatal(err)
			}
			refusals := CheckPlan(plan, happyCurrent())
			refused := make([]string, 0, len(refusals))
			for _, r := range refusals {
				if r.Subject != PlanReleasesFile {
					t.Fatalf("unexpected refusal %s", r)
				}
				if !strings.Contains(r.Reason, "which is not a Container of "+PlanModelFile) {
					t.Errorf("reason = %q", r.Reason)
				}
				name, _, _ := strings.Cut(strings.TrimPrefix(r.Reason, `names "`), `"`)
				refused = append(refused, name)
			}
			if !slices.Equal(refused, tc.refused) {
				t.Fatalf("refused %v, want %v", refused, tc.refused)
			}
		})
	}
}

func TestPlannerGuideExampleReleasesItsIssuesContainer(t *testing.T) {
	files := guideExampleFiles(t, plannerGuideText)
	plan, err := ParsePlan(files)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Releases, []string{"web"}) {
		t.Fatalf("the example releases %v, want [web]", plan.Releases)
	}
	for _, name := range plan.Releases {
		filed := false
		for _, issue := range plan.Issues {
			filed = filed || issue.Container == name
		}
		if !filed {
			t.Errorf("the example releases %s but files no issue for it (R7)", name)
		}
	}
	wrong := maps.Clone(files)
	wrong[PlanReleasesFile] = "containers: [orders-db]\n"
	if reason := checkGuideExample(wrong); !strings.Contains(reason, PlanReleasesFile) {
		t.Fatalf("releasing a DataStore passes the check: %q", reason)
	}
}

func TestReleaseMilestoneTitle(t *testing.T) {
	for _, tc := range []struct {
		name   string
		titles []string
		fixes  bool
		want   string
	}{
		{"no milestones", nil, false, "v0.1.0"},
		{"no milestones, a round of fixes", nil, true, "v0.1.0"},
		{"only titles that aren't versions", []string{"Backlog", "v1", "1.2.3", "v1.2", "v01.2.3", "v1.2.3-rc1", " v1.2.3"},
			false, "v0.1.0"},
		{"v0.1.0 open", []string{"v0.1.0"}, false, "v0.2.0"},
		{"v0.1.0 released, a round of fixes", []string{"v0.1.0"}, true, "v0.1.1"},
		{"patches made by hand, a round of fixes", []string{"v0.1.0", "v0.1.1", "v0.1.3", "v0.1.2"}, true, "v0.1.4"},
		{"patches made by hand, a round of features", []string{"v0.1.0", "v0.1.3", "Later"}, false, "v0.2.0"},
		{"fixes after an open minor go after it", []string{"v0.1.0", "v0.2.0"}, true, "v0.2.1"},
		{"v0.3.2 closed with v0.2.0 open", []string{"v0.2.0", "v0.3.2"}, false, "v0.4.0"},
		{"v0.3.2 closed with v0.2.0 open, a round of fixes", []string{"v0.2.0", "v0.3.2"}, true, "v0.3.3"},
		{"the newest by version, not by text", []string{"v0.10.0", "v0.9.5", "Backlog"}, false, "v0.11.0"},
		{"a major version", []string{"v1.0.0", "v0.7.3"}, false, "v1.1.0"},
		{"a major version, a round of fixes", []string{"v1.0.0", "v0.7.3"}, true, "v1.0.1"},
		{"a version too large to tag is ignored", []string{"v0.2.0", "v0.99999999999999999999.0"}, false, "v0.3.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ReleaseMilestoneTitle(tc.titles, tc.fixes)
			if got != tc.want {
				t.Fatalf("ReleaseMilestoneTitle(%q, %t) = %s, want %s", tc.titles, tc.fixes, got, tc.want)
			}
			if !IsReleaseTitle(tc.want) {
				t.Fatalf("%s is not a title a Crew tags", tc.want)
			}
			if slices.Contains(tc.titles, got) {
				t.Fatalf("%s reuses a title of %q", got, tc.titles)
			}
		})
	}
}

func TestReleaseMilestoneDueAt(t *testing.T) {
	now := time.Date(2026, 10, 8, 20, 0, 0, 0, time.UTC)
	week := now.Add(ReleaseMilestoneDue)
	for _, tc := range []struct {
		name string
		open []Release
		want time.Time
	}{
		{"none open", nil, week},
		{"open releases due sooner", []Release{{"v0.1.0", now.Add(time.Hour)}, {"v0.1.1", week.Add(-time.Minute)}}, week},
		{"one made by hand due later", []Release{{"v0.1.0", now}, {"v0.1.3", week.Add(7 * 24 * time.Hour)}},
			week.Add(7*24*time.Hour + time.Hour)},
		{"one due at the same time", []Release{{"v0.2.0", week}}, week.Add(time.Hour)},
		{"the latest of several", []Release{{"v0.1.2", week.Add(48 * time.Hour)}, {"v0.1.1", week.Add(24 * time.Hour)}},
			week.Add(49 * time.Hour)},
		{"a milestone that isn't a version, or has no date, is ignored",
			[]Release{{"Later", week.Add(time.Hour)}, {"v0.1.0", time.Time{}}}, week},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ReleaseMilestoneDueAt(tc.open, now); !got.Equal(tc.want) {
				t.Fatalf("ReleaseMilestoneDueAt = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestRoundFixes(t *testing.T) {
	issue := func(container string, labels ...string) PlanIssue {
		return PlanIssue{Container: container, Labels: labels}
	}
	for _, tc := range []struct {
		name string
		plan Plan
		want map[string]bool
	}{
		{"no issues", Plan{}, map[string]bool{}},
		{"all fixes", Plan{Issues: []PlanIssue{issue("core", "kind/bug"), issue("core", " kind/bug ", "area/cli")}},
			map[string]bool{"core": true}},
		{"a feature among fixes", Plan{Issues: []PlanIssue{issue("core", "kind/bug"), issue("core", "kind/feature"),
			issue("core", "kind/bug")}}, map[string]bool{"core": false}},
		{"an unlabelled issue is not a fix", Plan{Issues: []PlanIssue{issue("core")}}, map[string]bool{"core": false}},
		{"by Container", Plan{Issues: []PlanIssue{issue("core", "kind/bug"), issue("vscode", "kind/feature")}},
			map[string]bool{"core": true, "vscode": false}},
		{"a person's work doesn't count", Plan{Issues: []PlanIssue{issue("core", "kind/bug"),
			issue("core", "kind/docs", "kind/by-hand")}}, map[string]bool{"core": true}},
		{"only a person's work", Plan{Issues: []PlanIssue{issue("core", "kind/by-hand")}}, map[string]bool{}},
		{"a taken suggestion is new work", Plan{Issues: []PlanIssue{issue("core", "kind/bug")},
			Answers: []SuggestionAnswer{{Container: "core", Issue: 57, Answer: AnswerTake}}},
			map[string]bool{"core": false}},
		{"only a taken suggestion", Plan{Answers: []SuggestionAnswer{{Container: "vscode", Issue: 26, Answer: AnswerTake}}},
			map[string]bool{"vscode": false}},
		{"a skipped suggestion is no work", Plan{Issues: []PlanIssue{issue("core", "kind/bug")},
			Answers: []SuggestionAnswer{{Container: "core", Issue: 57, Answer: AnswerSkip}}},
			map[string]bool{"core": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RoundFixes(tc.plan); !maps.Equal(got, tc.want) {
				t.Fatalf("RoundFixes = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReleaseMilestoneMarker(t *testing.T) {
	if got, want := ReleaseMilestoneMarker("sprout", 2), "<!-- c4-brief: sprout/2 -->"; got != want {
		t.Fatalf("marker = %q, want %q", got, want)
	}
	description := ReleaseMilestoneDescription("sprout", 2)
	if !strings.HasSuffix(description, ReleaseMilestoneMarker("sprout", 2)+"\n") {
		t.Fatalf("the description doesn't end with the marker:\n%s", description)
	}
	for _, tc := range []struct {
		name        string
		description string
		want        bool
	}{
		{"the description it writes", description, true},
		{"the marker indented", "Text.\n  <!-- c4-brief: sprout/2 -->  \n", true},
		{"another round", ReleaseMilestoneDescription("sprout", 12), false},
		{"another Brief", ReleaseMilestoneDescription("sprout-2", 2), false},
		{"an issue's marker", IssueMarker("sprout", 2, 1), false},
		{"inside a line", "see <!-- c4-brief: sprout/2 --> here", false},
		{"no description", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasReleaseMilestoneMarker(tc.description, "sprout", 2); got != tc.want {
				t.Fatalf("HasReleaseMilestoneMarker(%q) = %v, want %v", tc.description, got, tc.want)
			}
		})
	}
}

func TestStrayWork(t *testing.T) {
	issues := []OpenIssue{
		{Number: 3, Labels: []string{LabelReady}},
		{Number: 10, Labels: []string{LabelInProgress, "kind/feature"}},
		{Number: 19, Labels: []string{LabelReady}, Milestone: 1},
		{Number: 20, Labels: []string{LabelProposed}},
		{Number: 21, Labels: []string{LabelNeedsHuman}},
		{Number: 22, Labels: []string{LabelNeedsHuman, LabelByHand}},
		{Number: 23, Labels: []string{LabelReady, LabelByHand}},
		{Number: 24, Labels: nil},
		{Number: 25, Labels: []string{LabelReady, LabelBump}},
	}
	if got, want := StrayWork(issues), []int{3, 10, 25}; !slices.Equal(got, want) {
		t.Fatalf("StrayWork = %v, want %v", got, want)
	}
	if got := StrayWork(nil); got != nil {
		t.Fatalf("StrayWork(nil) = %v, want none", got)
	}
}
