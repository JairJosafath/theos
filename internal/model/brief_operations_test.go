package model

import (
	"reflect"
	"strings"
	"testing"
)

// withOperations is happyFiles with block, YAML at the spec's indent, after a
// Container's frameworkRef line; api exists in happyCurrent, web is new.
func withOperations(container, block string) map[string]string {
	files := happyFiles()
	anchor := "  frameworkRef: {name: go-service}\n"
	if container == "web" {
		anchor = "  standardRef: {name: web-standard}\n"
	}
	files[PlanModelFile] = strings.Replace(files[PlanModelFile], anchor, anchor+block, 1)
	return files
}

func TestParsePlanOperations(t *testing.T) {
	plan, err := ParsePlan(withOperations("web", "  operations:\n    backups: {retentionDays: 0}\n"+
		"    recovery: {rpo: 15m}\n    alerts: {email: ops@shop.example}\n"))
	if err != nil {
		t.Fatal(err)
	}
	zero := int32(0)
	want := &OperationsChoices{RetentionDays: &zero, RPO: "15m", Email: "ops@shop.example"}
	if got := planContainer(plan, "web").Operations; !reflect.DeepEqual(got, want) {
		t.Errorf("web's operations = %+v, want %+v", got, want)
	}
	if got := planContainer(plan, "api").Operations; got != nil {
		t.Errorf("api's operations = %+v, want nil: model.yaml leaves it out", got)
	}
}

func TestParsePlanRefusesATier(t *testing.T) {
	for name, block := range map[string]string{
		"in spec":            "  tier: production\n",
		"in operations":      "  operations:\n    tier: production\n",
		"beside other field": "  operations:\n    recovery: {rpo: 1h}\n    tier: development\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParsePlan(withOperations("web", block))
			if err == nil || !strings.Contains(err.Error(), "a Container has no tier") {
				t.Errorf("ParsePlan = %v, want it to refuse the tier", err)
			}
		})
	}
}

func TestCheckPlanOperations(t *testing.T) {
	email := &OperationsChoices{Email: "a@b.io"}
	for _, tc := range []struct {
		name      string
		container string
		block     string
		before    *OperationsChoices // api's spec.operations in the namespace
		refused   []string
	}{
		{name: "left out"},
		{name: "a new Container's", container: "web",
			block: "  operations:\n    backups: {retentionDays: 90, copyToRegion: eu-west-1}\n"},
		{name: "an empty block", container: "web", block: "  operations: {}\n"},
		{name: "an existing Container's", container: "api", block: "  operations:\n    alerts: {email: a@b.io}\n",
			refused: []string{"sets operations on a Container that exists"}},
		{name: "the block an existing Container has, as a round applied again writes it", container: "api",
			block: "  operations:\n    alerts: {email: a@b.io}\n", before: email},
		{name: "another block than an existing Container has", container: "api",
			block: "  operations:\n    alerts: {email: c@d.io}\n", before: email,
			refused: []string{"sets operations on a Container that exists"}},
		{name: "values the API server refuses", container: "web",
			block: "  operations:\n    backups: {retentionDays: 4000}\n    recovery: {rpo: an hour, rto: 1w}\n",
			refused: []string{"operations.backups.retentionDays is 4000", `operations.recovery.rpo is "an hour"`,
				`operations.recovery.rto is "1w"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := ParsePlan(withOperations(tc.container, tc.block))
			if err != nil {
				t.Fatal(err)
			}
			current := happyCurrent()
			current.Objects[2].Operations = tc.before
			refusals := CheckPlan(plan, current)
			if len(refusals) != len(tc.refused) {
				t.Fatalf("refusals = %v, want %d saying %q", refusals, len(tc.refused), tc.refused)
			}
			for i, r := range refusals {
				if r.Subject != "Container/"+tc.container || !strings.Contains(r.Reason, tc.refused[i]) {
					t.Errorf("refusal %d = %s, want one about Container/%s saying %q", i, r, tc.container, tc.refused[i])
				}
			}
		})
	}
}
