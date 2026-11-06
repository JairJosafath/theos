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

func TestIsFix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels []string
		want   bool
	}{
		{"a bug", []string{"kind/bug", "area/cli"}, true},
		{"a test", []string{" kind/test "}, true},
		{"a feature", []string{"kind/feature"}, false},
		{"no labels", nil, false},
		{"a bug done by hand", []string{"kind/bug", LabelByHand}, false},
		{"a test done by hand", []string{LabelByHand, "kind/test"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsFix(tc.labels); got != tc.want {
				t.Fatalf("IsFix(%v) = %v, want %v", tc.labels, got, tc.want)
			}
		})
	}
}

func TestPatchSlot(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	day := func(n int) time.Time { return now.Add(time.Duration(n) * 24 * time.Hour) }
	m := func(id int64, title string, due time.Time) ReleaseMilestone {
		return ReleaseMilestone{Release: Release{Title: title, Due: due}, ID: id}
	}
	closed := func(r ReleaseMilestone) ReleaseMilestone { r.MilestoneClosed = true; return r }
	prepared := func(r ReleaseMilestone) ReleaseMilestone { r.Prepared = []int{90}; return r }
	planned := []ReleaseMilestone{m(2, "v0.14.0", day(10)), m(3, "v0.15.0", day(20))}
	for _, tc := range []struct {
		name       string
		milestones []ReleaseMilestone
		want       Slot
	}{
		{"no milestone at all: v0.1.0 stays the first", nil, Slot{Title: "v0.1.0", Due: day(7)}},
		{"only milestones that aren't versions", []ReleaseMilestone{m(1, "Later", day(1))},
			Slot{Title: "v0.1.0", Due: day(7)}},
		{"only closed milestones: the patch after the newest",
			[]ReleaseMilestone{closed(m(1, "v0.12.0", day(-20))), closed(m(2, "v0.13.0", day(-10))),
				closed(m(3, "v0.2.0", day(-30)))},
			Slot{Title: "v0.13.1", Due: day(7)}},
		{"v0.13.0 in progress with v0.14.0 and v0.15.0 planned: halfway to v0.14.0",
			append([]ReleaseMilestone{m(1, "v0.13.0", day(2))}, planned...),
			Slot{Title: "v0.13.1", Due: day(6)}},
		{"v0.13.1 open and taking work: joined",
			append([]ReleaseMilestone{m(1, "v0.13.0", day(2)), m(4, "v0.13.1", day(5))}, planned...),
			Slot{ID: 4, Title: "v0.13.1", Due: day(5)}},
		{"v0.13.1 prepared: a new v0.13.2 halfway between it and v0.14.0",
			append([]ReleaseMilestone{m(1, "v0.13.0", day(2)), prepared(m(4, "v0.13.1", day(4)))}, planned...),
			Slot{Title: "v0.13.2", Due: day(7)}},
		{"v0.13.1 closed: v0.13.2",
			append([]ReleaseMilestone{m(1, "v0.13.0", day(2)), closed(m(4, "v0.13.1", day(4)))}, planned...),
			Slot{Title: "v0.13.2", Due: day(7)}},
		{"v0.13.1 closed, due before the release in progress: v0.13.2 still after it",
			append([]ReleaseMilestone{m(1, "v0.13.0", day(2)), closed(m(4, "v0.13.1", day(-2)))}, planned...),
			Slot{Title: "v0.13.2", Due: day(6)}},
		{"every patch closed: the patch after the newest",
			[]ReleaseMilestone{closed(m(1, "v0.13.0", day(-4))), closed(m(4, "v0.13.1", day(-2)))},
			Slot{Title: "v0.13.2", Due: day(7)}},
		{"v0.13.0 being prepared is still the release in progress",
			append([]ReleaseMilestone{prepared(m(1, "v0.13.0", day(2)))}, planned...),
			Slot{Title: "v0.13.1", Due: day(6)}},
		{"a closed and an open v0.13.1 taking work: the open one is joined",
			append([]ReleaseMilestone{m(1, "v0.13.0", day(2)), closed(m(5, "v0.13.1", day(1))), m(4, "v0.13.1", day(5))},
				planned...),
			Slot{ID: 4, Title: "v0.13.1", Due: day(5)}},
		{"nothing open after the release in progress: P4's date",
			[]ReleaseMilestone{m(1, "v0.13.0", day(9))}, Slot{Title: "v0.13.1", Due: day(9).Add(time.Hour)}},
		{"nothing open after it, due soon: a week from now",
			[]ReleaseMilestone{m(1, "v0.13.0", day(1))}, Slot{Title: "v0.13.1", Due: day(7)}},
		{"an undated open milestone isn't the release in progress",
			[]ReleaseMilestone{m(1, "v0.13.0", time.Time{}), closed(m(2, "v0.12.0", day(-3)))},
			Slot{Title: "v0.13.1", Due: day(7)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := PatchSlot(tc.milestones, now)
			if got.ID != tc.want.ID || got.Title != tc.want.Title || !got.Due.Equal(tc.want.Due) {
				t.Fatalf("PatchSlot = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestPatchMilestoneMarker(t *testing.T) {
	if got, want := PatchMilestoneMarker("sprout", 2), "<!-- c4-brief: sprout/2 fixes -->"; got != want {
		t.Fatalf("marker = %q, want %q", got, want)
	}
	description := PatchMilestoneDescription("sprout", 2)
	if !strings.HasSuffix(description, PatchMilestoneMarker("sprout", 2)+"\n") {
		t.Fatalf("the description doesn't end with the marker:\n%s", description)
	}
	for _, tc := range []struct {
		name        string
		description string
		patch       bool
		release     bool
	}{
		{"the patch description", description, true, false},
		{"the release description", ReleaseMilestoneDescription("sprout", 2), false, true},
		{"another round's patch", PatchMilestoneDescription("sprout", 12), false, false},
		{"the marker indented", "Text.\n  <!-- c4-brief: sprout/2 fixes -->  \n", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasPatchMilestoneMarker(tc.description, "sprout", 2); got != tc.patch {
				t.Errorf("HasPatchMilestoneMarker = %v, want %v", got, tc.patch)
			}
			if got := HasReleaseMilestoneMarker(tc.description, "sprout", 2); got != tc.release {
				t.Errorf("HasReleaseMilestoneMarker = %v, want %v", got, tc.release)
			}
		})
	}
}

func TestEmptyReleaseToJoin(t *testing.T) {
	due := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	m := func(id int64, title string, closed bool, issues ...int) ReleaseMilestone {
		return ReleaseMilestone{ID: id, Release: Release{Title: title, Due: due}, MilestoneClosed: closed, Open: issues}
	}
	undated := m(3, "v0.22.0", false)
	undated.Due = time.Time{}
	tests := []struct {
		name       string
		milestones []ReleaseMilestone
		made       []int64
		want       int64
	}{
		{"#2025: the empty next minor release an earlier round made is joined",
			[]ReleaseMilestone{m(1, "v0.21.0", true, 5), m(2, "v0.22.0", false)}, []int64{1, 2}, 2},
		{"#2025: so is one after an open release with work",
			[]ReleaseMilestone{m(1, "v0.21.0", false, 5), m(2, "v0.22.0", false), m(4, "Later", false, 9)},
			[]int64{2}, 2},
		{"#2025: after an empty release a person made, which stays theirs",
			[]ReleaseMilestone{m(1, "v0.21.0", true, 5), m(2, "v0.22.0", false), m(3, "v0.23.0", false)},
			[]int64{3}, 3},
		{"an empty release a person made is not joined",
			[]ReleaseMilestone{m(1, "v0.21.0", true, 5), m(2, "v0.22.0", false)}, nil, 0},
		{"a next minor release with work is not empty",
			[]ReleaseMilestone{m(1, "v0.21.0", true, 5), m(2, "v0.22.0", false, 6)}, []int64{2}, 0},
		{"a closed one is not joined",
			[]ReleaseMilestone{m(1, "v0.21.0", true, 5), m(2, "v0.22.0", true)}, []int64{2}, 0},
		{"an undated one is no release",
			[]ReleaseMilestone{m(1, "v0.21.0", true, 5), undated}, []int64{3}, 0},
		{"an empty patch release is not the next minor",
			[]ReleaseMilestone{m(1, "v0.21.0", true, 5), m(2, "v0.21.1", false)}, []int64{2}, 0},
		{"an empty release past the next minor is not it",
			[]ReleaseMilestone{m(1, "v0.21.0", true, 5), m(2, "v0.23.0", false)}, []int64{2}, 0},
		{"with no release at all, an empty v0.1.0 a round made is joined",
			[]ReleaseMilestone{m(1, "v0.1.0", false), m(2, "Later", false)}, []int64{1}, 1},
		{"nothing to join", nil, nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got int64
			if join := EmptyReleaseToJoin(tt.milestones, tt.made); join != nil {
				got = join.ID
			}
			if got != tt.want {
				t.Errorf("EmptyReleaseToJoin() = milestone %d, want %d", got, tt.want)
			}
		})
	}
}

func TestHasAnyReleaseMilestoneMarker(t *testing.T) {
	for description, want := range map[string]bool{
		ReleaseMilestoneDescription("b", 3):                          true,
		ReleaseMilestoneDescription("other", 3):                      false,
		PatchMilestoneDescription("b", 3):                            false,
		"<!-- c4-brief: b/x -->":                                     false,
		"Made by hand":                                               false,
		"text <!-- c4-brief: b/3 --> inline":                         false,
		"by hand\n  " + ReleaseMilestoneMarker("b", 12) + "  \nmore": true,
	} {
		if got := HasAnyReleaseMilestoneMarker(description, "b"); got != want {
			t.Errorf("HasAnyReleaseMilestoneMarker(%q) = %v, want %v", description, got, want)
		}
	}
}

func TestAddMarker(t *testing.T) {
	marker := ReleaseMilestoneMarker("b", 4)
	for _, tt := range []struct{ in, want string }{
		{"", marker + "\n"},
		{ReleaseMilestoneDescription("b", 3), ReleaseMilestoneDescription("b", 3) + "\n" + marker + "\n"},
		{"Made by hand", "Made by hand\n\n" + marker + "\n"},
		{"x\n" + marker + "\n", "x\n" + marker + "\n"},
	} {
		got := AddMarker(tt.in, marker)
		if got != tt.want {
			t.Errorf("AddMarker(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if !HasReleaseMilestoneMarker(got, "b", 4) {
			t.Errorf("AddMarker(%q) = %q has no round 4 marker", tt.in, got)
		}
	}
}
