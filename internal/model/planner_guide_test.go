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
// with the entries the guide's example names.
func guideExampleCurrent() CurrentModel {
	return CurrentModel{
		Namespace: "shop",
		Catalog: []CatalogEntry{
			{Side: SideFramework, Name: "react-site", SupportedTypes: []string{TypeStaticSite}},
			{Side: SideFramework, Name: "go-service", SupportedTypes: []string{TypeService}},
			{Side: SideStandard, Name: "web-standard", SupportedTypes: []string{TypeStaticSite},
				SupportedFrameworks: []string{"react-site"}},
			{Side: SideCompute, Name: "k8s", SupportedTypes: []string{TypeService}},
			{Side: SideDeployment, Name: "helm", SupportedTypes: []string{TypeService}},
			{Side: SideConnector, Name: "postgres", SupportedTypes: []string{TypeService}},
		},
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
	var order []int
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
