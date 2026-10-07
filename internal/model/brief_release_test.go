package model

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
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
		want   string
	}{
		{"no milestones", nil, "v0.1.0"},
		{"only titles that aren't versions", []string{"Backlog", "v1", "1.2.3", "v1.2", "v01.2.3", "v1.2.3-rc1", " v1.2.3"},
			"v0.1.0"},
		{"v0.1.0 open", []string{"v0.1.0"}, "v0.2.0"},
		{"v0.3.2 closed with v0.2.0 open", []string{"v0.2.0", "v0.3.2"}, "v0.4.0"},
		{"the newest by version, not by text", []string{"v0.10.0", "v0.9.5", "Backlog"}, "v0.11.0"},
		{"a major version", []string{"v1.0.0", "v0.7.3"}, "v1.1.0"},
		{"a version too large to tag is ignored", []string{"v0.2.0", "v0.99999999999999999999.0"}, "v0.3.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ReleaseMilestoneTitle(tc.titles); got != tc.want {
				t.Fatalf("ReleaseMilestoneTitle(%q) = %s, want %s", tc.titles, got, tc.want)
			}
			if !IsReleaseTitle(tc.want) {
				t.Fatalf("%s is not a title a Crew tags", tc.want)
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
