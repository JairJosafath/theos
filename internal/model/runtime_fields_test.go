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
	"testing"
)

// #333: decision 6's table, a row per field. That every field of CrewSpec
// and AgentSpec has a row is internal/nocluster's test, which sees the API
// (TestEveryFieldHasARow333).
func TestFieldRows333(t *testing.T) {
	for _, tt := range []struct {
		field, crew, agent string
	}{
		{field: "implementer"}, {field: "reviewer"}, {field: "routes"}, {field: "parallel"}, {field: "paused"},
		// #337: every runtime plays the triager, the spec writer and the verifier.
		{field: "triager"}, {field: "specWriter"}, {field: "specApproval"}, {field: "verifier"},
		{field: "schedules", crew: ReasonNoScheduler}, {field: "tagReleases", crew: ReasonNoScheduler},
		{field: "prepareReleases", crew: ReasonNoScheduler}, {field: "releaseCadence", crew: ReasonNoScheduler},
		{field: "publishReleases", crew: ReasonNoScheduler}, {field: "approval", crew: ReasonPersonMerges},
		{field: "taskBudgetUSD", crew: ReasonCostUnreported}, {field: "pauseRuns", crew: ReasonNoSandbox},
	} {
		if got, ok := CrewFields[tt.field]; !ok || got != tt.crew {
			t.Errorf("CrewFields[%s] = %q, %v; want %q", tt.field, got, ok, tt.crew)
		}
	}
	for field, want := range map[string]string{
		"model": "", "maxRounds": "", "harness": ReasonHarnessNotBuilt, "sessionTokenLimit": ReasonCostUnreported,
		"image": ReasonNoSandbox, "resources": ReasonNoSandbox, "env": ReasonNoSandbox,
		"credentialsSecretRef": ReasonNoSandbox, "timeoutMinutes": ReasonNoSandbox,
	} {
		if got, ok := AgentFields[field]; !ok || got != want {
			t.Errorf("AgentFields[%s] = %q, %v; want %q", field, got, ok, want)
		}
	}
}

// #333: what each runtime says of a Crew and its Agents.
func TestNotHonoured333(t *testing.T) {
	crew := CrewSetup{Name: "orders", Fields: []string{"containerRef", "implementer", "reviewer", "schedules",
		"verifier", "parallel", "taskBudgetUSD", "approval"},
		Agents: []AgentSetup{
			{Name: "implementer", Harness: HarnessClaudeCode, Fields: []string{"image", "harness", "model",
				"timeoutMinutes"}},
			{Name: "reviewer", Harness: "codex", Fields: []string{"image", "harness", "model", "futureField"}},
		}}
	for _, tt := range []struct {
		name string
		caps Capabilities
		want []FieldNote
	}{
		{name: "the operator honours everything", caps: OperatorCapabilities()},
		{name: "the lead runtime", caps: LeadCapabilities(), want: []FieldNote{
			{Object: "Crew orders", Reason: ReasonPersonMerges},
			{Object: "Crew orders", Field: "spec.approval", Reason: ReasonPersonMerges},
			{Object: "Crew orders", Field: "spec.schedules", Reason: ReasonNoScheduler},
			{Object: "Crew orders", Field: "spec.taskBudgetUSD", Reason: ReasonCostUnreported},
			{Object: "Agent implementer", Field: "spec.image", Reason: ReasonNoSandbox},
			{Object: "Agent implementer", Field: "spec.timeoutMinutes", Reason: ReasonNoSandbox},
			{Object: "Agent reviewer", Field: "spec.harness", Reason: ReasonHarnessNotBuilt},
			{Object: "Agent reviewer", Field: "spec.futureField", Reason: ReasonUnknownField},
			{Object: "Agent reviewer", Field: "spec.image", Reason: ReasonNoSandbox},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := NotHonoured(crew, tt.caps)
			for i := range got {
				if got[i].Says == "" {
					t.Errorf("note %+v says nothing", got[i])
				}
				got[i].Says = ""
			}
			if tt.name == "the operator honours everything" {
				// The operator has a row for every field but a future one.
				if len(got) != 1 || got[0].Reason != ReasonUnknownField {
					t.Errorf("notes = %+v, want the unknown field alone", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("notes =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

// #333: a note as theos status prints it.
func TestFieldNoteLine333(t *testing.T) {
	got := FieldNoteLine(FieldNote{Object: "Agent reviewer", Field: "spec.image", Reason: ReasonNoSandbox,
		Says: "no sandbox"})
	if want := "Agent reviewer: spec.image: NoSandbox: no sandbox"; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
	got = FieldNoteLine(FieldNote{Object: "Crew orders", Reason: ReasonPersonMerges, Says: "a person merges"})
	if want := "Crew orders: PersonMerges: a person merges"; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}
