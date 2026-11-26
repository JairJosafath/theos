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

// #337: a Task's name says its kind on its Container.
func TestTaskNameKind337(t *testing.T) {
	for name, want := range map[string]string{
		SpecTaskName("orders-api", 4, 1):   TaskNameSpec,
		SpecTaskName("orders-api", 4, 3):   TaskNameSpec,
		TriageTaskName("orders-api", 4, 1): TaskNameTriage,
		TriageTaskName("orders-api", 4, 2): TaskNameTriage,
		IssueTaskName("orders-api", 4, 1):  "",
		IssueTaskName("orders-api", 4, 2):  "",
		"orders-api-0-spec":                "",
		"orders-api-x-spec":                "",
		"payments-4-spec":                  "",
		"orders-api-4-spec-extra":          "",
	} {
		if got := TaskNameKind("orders-api", name); got != want {
			t.Errorf("TaskNameKind(%s) = %q, want %q", name, got, want)
		}
	}
}

// #337: a Triage Task read from its issue's progress; one that decided on
// an issue proposed again was sent back by a person, and decided nothing.
func TestHostTriageTask337(t *testing.T) {
	for phase, want := range map[string]TriageTask{
		TaskPending:      {Name: "t", Issue: 4},
		TaskImplementing: {Name: "t", Issue: 4},
		TaskFailed:       {Name: "t", Issue: 4, Final: true, Failed: true},
		TaskMerged:       {Name: "t", Issue: 4, Final: true, Answered: true},
	} {
		if got := HostTriageTask("t", 4, phase); got != want {
			t.Errorf("HostTriageTask(%s) = %+v, want %+v", phase, got, want)
		}
	}
	// So NextTriage triages the issue again, and gives the next try a name of its own.
	step := NextTriage("orders-api", TriageFacts{On: true, ContainerReady: true,
		Proposed: []ProposedIssue{{Number: 4}}, Tasks: []TriageTask{HostTriageTask("orders-api-4-triage", 4, TaskMerged)}})
	if step.Start == nil || step.Start.Task != "orders-api-4-triage-2" {
		t.Errorf("NextTriage = %+v, want try 2", step)
	}
}

// #337: what an approving review and verification leave for the merge.
func TestKeepFindings337(t *testing.T) {
	ups := []FollowUp{{Title: "Later"}}
	open := []OpenCriterion{{Criterion: "On a phone"}}
	f := KeepFindings(nil, RoleReviewer, &RunResult{Verdict: VerdictApprove, FollowUps: ups})
	f = KeepFindings(f, RoleVerifier, &RunResult{Verdict: VerdictApprove, Open: open})
	if want := (&Findings{FollowUps: ups, VerifyOpen: open}); !reflect.DeepEqual(f, want) {
		t.Errorf("findings = %+v, want %+v", f, want)
	}
	for name, res := range map[string]*RunResult{"no result": nil,
		"a request for changes": {Verdict: VerdictChanges, Open: open}} {
		if got := KeepFindings(f, RoleVerifier, res); got != f {
			t.Errorf("%s changed the findings to %+v", name, got)
		}
	}
	if got := KeepFindings(nil, RoleImplementer, &RunResult{Verdict: VerdictApprove, Open: open}); got != nil {
		t.Errorf("an implementer left findings %+v", got)
	}
	if got := KeepFindings(nil, RoleReviewer, &RunResult{Verdict: VerdictApprove}); got != nil {
		t.Errorf("an approval that leaves nothing left %+v", got)
	}
}

// #337: theos's own accounts, whose review is no person's.
func TestClaimRulesOurs337(t *testing.T) {
	r := DefaultClaimRules("Jair")
	r.Engine = []string{"theos-bot"}
	for user, want := range map[string]bool{"jair": true, "THEOS-BOT": true, "ada": false} {
		if got := r.Ours(user); got != want {
			t.Errorf("Ours(%s) = %v, want %v", user, got, want)
		}
	}
}
