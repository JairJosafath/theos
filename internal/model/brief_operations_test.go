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

// #1209: a Framework that doesn't read the block gets none; an empty block is none.
func TestCheckPlanRefusesOperationsOnAFrameworkThatDoesNotReadThem(t *testing.T) {
	for name, tc := range map[string]struct {
		block   string
		refused bool
	}{
		"a block":        {"  operations:\n    alerts: {email: a@b.io}\n", true},
		"an empty block": {"  operations: {}\n", false},
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := ParsePlan(withOperations("web", tc.block))
			if err != nil {
				t.Fatal(err)
			}
			current := happyCurrent()
			for i := range current.Catalog {
				if current.Catalog[i].Name == "react-site" {
					current.Catalog[i].ReadsOperations = false
				}
			}
			refusals := CheckPlan(plan, current)
			if !tc.refused {
				if len(refusals) != 0 {
					t.Fatalf("refusals = %v, want none", refusals)
				}
				return
			}
			want := "sets operations, which Framework react-site doesn't read (readsOperations is not set on it)"
			if len(refusals) != 1 || refusals[0].Subject != "Container/web" || !strings.Contains(refusals[0].Reason, want) {
				t.Fatalf("refusals = %v, want one about Container/web saying %q", refusals, want)
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
