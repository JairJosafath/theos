package model

import (
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// guideExample is a fenced block of PLANNER.md whose info string names a plan
// file, such as ```yaml plan/model.yaml.
var guideExample = regexp.MustCompile("(?ms)^```[a-z]+ (plan/\\S+)\n(.*?)^```$")

// guideExampleFiles returns the guide's example plan, by path.
func guideExampleFiles(t *testing.T, guide string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, m := range guideExample.FindAllStringSubmatch(guide, -1) {
		files[m[1]] = m[2]
	}
	for _, path := range []string{PlanModelFile, PlanChoicesFile, "plan/issues/01-web.md"} {
		if files[path] == "" {
			t.Fatalf("PLANNER.md has no example of %s", path)
		}
	}
	return files
}

// guideExampleCurrent is a first round's namespace: nothing yet, and a catalog
// with the entries the guide's example names. The round takes the idea, so
// it has a new prompt.
func guideExampleCurrent() CurrentModel {
	return CurrentModel{
		Namespace: "shop",
		NewPrompt: true,
		Catalog: []CatalogEntry{
			{Side: SideFramework, Name: "react-site", SupportedTypes: []string{TypeStaticSite}},
			{Side: SideFramework, Name: "go-service", SupportedTypes: []string{TypeService}, ReadsOperations: true},
			{Side: SideStandard, Name: "web-standard", SupportedTypes: []string{TypeStaticSite},
				SupportedFrameworks: []string{"react-site"}},
			{Side: SideStandard, Name: "go-standard", SupportedTypes: []string{TypeService},
				SupportedFrameworks: []string{"go-service"}},
			{Side: SideCompute, Name: "k8s", SupportedTypes: []string{TypeService}},
			{Side: SideDeployment, Name: "helm", SupportedTypes: []string{TypeService}},
			{Side: SideConnector, Name: "postgres", SupportedTypes: []string{TypeService}},
		},
		Bundles:     []Bundle{{Name: "react-web", Framework: "react-site", Standard: "web-standard"}},
		GitProvider: "gitea",
	}
}

// checkGuideExample returns why the example plan fails the plan check, or "".
func checkGuideExample(files map[string]string) string {
	plan, err := ParsePlan(files)
	if err != nil {
		return err.Error()
	}
	var reasons []string
	for _, r := range CheckPlan(plan, guideExampleCurrent()) {
		reasons = append(reasons, r.String())
	}
	return strings.Join(reasons, "\n")
}

// planSpecTags are the yaml field names of every plan spec type, the fields
// the plan check reads.
func planSpecTags() []string {
	var tags []string
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		if t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return
		}
		for f := range t.Fields() {
			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if name != "" && !slices.Contains(tags, name) {
				tags = append(tags, name)
			}
			walk(f.Type)
		}
	}
	for _, spec := range []any{planText{}, planSystemSpec{}, planContainerSpec{}, planDataStoreSpec{},
		planRelationshipSpec{}} {
		walk(reflect.TypeOf(spec))
	}
	return tags
}

func TestPlannerGuideExamplePassesThePlanCheck(t *testing.T) {
	files := guideExampleFiles(t, plannerGuideText)
	if reason := checkGuideExample(files); reason != "" {
		t.Fatalf("the example plan of PLANNER.md is refused:\n%s", reason)
	}
	plan, _ := ParsePlan(files)
	for _, kind := range planKinds {
		if !slices.ContainsFunc(plan.Objects, func(o PlanObject) bool { return o.Kind == kind }) {
			t.Errorf("the example plan has no %s", kind)
		}
	}
}

func TestPlannerGuideExampleOrdersItsIssues(t *testing.T) {
	plan, err := ParsePlan(guideExampleFiles(t, plannerGuideText))
	if err != nil {
		t.Fatal(err)
	}
	if got := IssueBlockers(plan.Issues, 3); !slices.Equal(got, []int{2}) {
		t.Fatalf("the web shop's order issue is after %v, want [2]", got)
	}
	order := make([]int, 0, len(plan.Issues))
	for _, issue := range FilingOrder(plan.Issues) {
		order = append(order, issue.N)
	}
	if slices.Index(order, 2) > slices.Index(order, 3) {
		t.Errorf("filing order %v files the order button before the service takes orders", order)
	}
	if !strings.Contains(plannerGuideText, "after: [2]") {
		t.Error("PLANNER.md doesn't show after:")
	}
}

func TestPlannerGuideShowsUses(t *testing.T) {
	for _, want := range []string{"uses: [cel-lens-core]", "go get it at that version.",
		"npm install --save-exact", "uv add"} {
		if !strings.Contains(plannerGuideText, want) {
			t.Errorf("PLANNER.md doesn't show %q", want)
		}
	}
	// #1233: the guide's lines are the operator's, one per ecosystem.
	for _, line := range []string{
		UsesLine(UsedPackage{Ecosystem: EcosystemGo, Name: "c4factory.local/<project>/cel-lens-core", Version: "v0.1.0"}),
		UsesLine(UsedPackage{Ecosystem: EcosystemNpm, Name: "<scope>/cel-lens-core", Version: "0.1.0"}),
		UsesLine(UsedPackage{Ecosystem: EcosystemPyPI, Name: "cel-lens-core", Version: "0.1.0", Index: "<index>"}),
	} {
		if !strings.Contains(plannerGuideText, line) {
			t.Errorf("PLANNER.md doesn't show the operator's line %q", line)
		}
	}
}

func TestPlannerGuideExampleHasEveryField(t *testing.T) {
	model := guideExampleFiles(t, plannerGuideText)[PlanModelFile]
	for _, tag := range planSpecTags() {
		if !regexp.MustCompile(`(?m)^\s+` + tag + `:|[{,] ?` + tag + `:`).MatchString(model) {
			t.Errorf("the example plan never writes %s", tag)
		}
	}
}

func TestPlannerGuideExampleWithARenamedFieldIsRefused(t *testing.T) {
	files := guideExampleFiles(t, plannerGuideText)
	for _, tag := range planSpecTags() {
		renamed := maps.Clone(files)
		field := regexp.MustCompile(`\b` + tag + `:`)
		renamed[PlanModelFile] = field.ReplaceAllString(files[PlanModelFile], tag+"Ref:")
		if tag == "name" {
			// metadata.name is a field too; rename only the spec's.
			renamed[PlanModelFile] = strings.ReplaceAll(renamed[PlanModelFile], "metadata:\n  nameRef:", "metadata:\n  name:")
		}
		if checkGuideExample(renamed) == "" {
			t.Errorf("a guide that writes %sRef for %s still passes the plan check", tag, tag)
		}
	}
}

func TestPlannerGuideGivesAgentsOnlyCriteriaTheyCanMeet(t *testing.T) {
	guide := strings.Join(strings.Fields(plannerGuideText), " ")
	for _, rule := range []string{
		"**Give agents only criteria they can meet.**",
		"each criterion is one an agent can meet there and a reviewer can check there",
		"A screenshot of a desktop app, a recording, a run on a phone or other device",
		"labelled `kind/by-hand`",
		"Where a text sample serves, ask the agent for that instead",
	} {
		if !strings.Contains(guide, rule) {
			t.Errorf("PLANNER.md doesn't say %q", rule)
		}
	}
	plan, err := ParsePlan(guideExampleFiles(t, plannerGuideText))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(plan.Issues, func(i PlanIssue) bool { return IsByHand(i.Labels) && len(i.After) > 0 }) {
		t.Error("the example plan has no kind/by-hand issue after the agents' work it shows")
	}
}

func TestPlannerGuideAsksForNoTestThatStartsADesktopApp(t *testing.T) {
	guide := strings.Join(strings.Fields(plannerGuideText), " ")
	for _, rule := range []string{
		"**No test starts a desktop app.**",
		"`@vscode/test-electron`",
		"can't run in `make check`, and no criterion asks for one",
		"An extension's tests run without a VS Code window",
		"trying it in a real editor is a `kind/by-hand` issue",
	} {
		if !strings.Contains(guide, rule) {
			t.Errorf("PLANNER.md doesn't say %q", rule)
		}
	}
}

func TestPlannerGuideLeavesReleasingToTheFactory(t *testing.T) {
	guide := strings.Join(strings.Fields(plannerGuideText), " ")
	for _, rule := range []string{
		"**The factory makes the releases; plan only what goes in them.**",
		"the Crew tags `vX.Y.Z` in Gitea",
		"builds the release's files on `" + ReleaseTags + "` tags",
		"as `" + GitHubTag("<prefix>", "vX.Y.Z") + "` with the operator's token",
		"no issue asks agents to tag or publish a release",
		"to change which tags start the release workflow",
		"to put a GitHub token in CI",
		"say in `choices.md` that the factory does it",
	} {
		if !strings.Contains(guide, rule) {
			t.Errorf("PLANNER.md doesn't say %q", rule)
		}
	}
}

// TestPlannerGuideSaysWhenASystemIsExternal: the guide tells the planner to
// mark a system someone else runs External, and shows one (#1150).
func TestPlannerGuideSaysWhenASystemIsExternal(t *testing.T) {
	for _, want := range []string{"**A system someone else runs is External.**", "location: External",
		"a later round keeps it"} {
		if !strings.Contains(plannerGuideText, want) {
			t.Errorf("PLANNER.md doesn't say %q", want)
		}
	}
	plan, err := ParsePlan(guideExampleFiles(t, plannerGuideText))
	if err != nil {
		t.Fatal(err)
	}
	if o, ok := findObject(plan.Objects, Ref{KindSoftwareSystem, "payments"}); !ok || o.Location != LocationExternal {
		t.Errorf("the example plan has no External system: %+v", o)
	}
}

// #1622: the guide tells the planner to prefer a Bundle of catalog.yaml, and
// its example plan has a Container on one and one on the sides.
func TestPlannerGuidePrefersABundle(t *testing.T) {
	for _, want := range []string{"**Use only the listed catalog, and prefer a Bundle.**",
		"Its `bundles` list the namespace's", "`bundleRef` and none of `frameworkRef`",
		"either `bundleRef` (rule 2) or `frameworkRef`", "never both"} {
		if !strings.Contains(plannerGuideText, want) {
			t.Errorf("PLANNER.md doesn't say %q", want)
		}
	}
	plan, err := ParsePlan(guideExampleFiles(t, plannerGuideText))
	if err != nil {
		t.Fatal(err)
	}
	var onBundle, onSides bool
	for _, o := range plan.Objects {
		if o.Kind != KindContainer {
			continue
		}
		switch {
		case o.Bundle != "" && o.Framework == "" && o.Standard == "" && o.Compute == "" && o.Deployment == "":
			onBundle = true
		case o.Bundle == "" && o.Framework != "":
			onSides = true
		}
	}
	if !onBundle || !onSides {
		t.Errorf("the example plan's Containers: on a Bundle %v, on the sides %v; want both", onBundle, onSides)
	}
}
