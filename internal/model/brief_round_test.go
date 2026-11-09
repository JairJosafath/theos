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
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestStartsRound(t *testing.T) {
	for _, tc := range []struct {
		name  string
		facts BriefRoundFacts
		want  bool
	}{
		{"the idea starts round 1", BriefRoundFacts{}, true},
		{"the idea with prompts starts round 1", BriefRoundFacts{Prompts: 2}, true},
		{"a running round runs on", BriefRoundFacts{Round: 1, TaskPhase: "Implementing"}, false},
		{"a prompt appended during a round waits", BriefRoundFacts{Round: 1, Prompts: 1, TaskPhase: "Checking"}, false},
		{"a prompt appended while applying waits", BriefRoundFacts{Round: 1, Prompts: 1, TaskPhase: "Merged"}, false},
		{"an applied round with no new prompt is idle", BriefRoundFacts{Round: 1, RoundOver: true}, false},
		{"a prompt appended while idle starts a round", BriefRoundFacts{Round: 1, Prompts: 1, RoundOver: true}, true},
		{"the prompt the last round took starts none", BriefRoundFacts{Round: 2, Prompts: 1, RoundPrompts: 1,
			RoundOver: true}, false},
		{"a prompt after a failed round starts one", BriefRoundFacts{Round: 2, Prompts: 2, RoundPrompts: 1,
			RoundOver: true, Outcome: ReasonRoundFailed}, true},
		{"a failed round with no new prompt starts none", BriefRoundFacts{Round: 2, Prompts: 1, RoundPrompts: 1,
			RoundOver: true, Outcome: ReasonRoundFailed}, false},
		{"a request let through while idle starts a round", BriefRoundFacts{Round: 1, RoundOver: true,
			NewRequests: 1}, true},
		{"a request let through during a round waits", BriefRoundFacts{Round: 1, NewRequests: 1,
			TaskPhase: "Checking"}, false},
		{"a request to a paused Brief waits", BriefRoundFacts{Round: 1, RoundOver: true, NewRequests: 1,
			Paused: true}, false},
		{"a paused Brief doesn't plan its idea", BriefRoundFacts{Paused: true}, false},
		{"a prompt to a paused Brief waits", BriefRoundFacts{Round: 1, Prompts: 1, RoundOver: true,
			Paused: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := StartsRound(tc.facts); got != tc.want {
				t.Errorf("StartsRound(%+v) = %v, want %v", tc.facts, got, tc.want)
			}
		})
	}
}

func TestBriefPausedMessage(t *testing.T) {
	many := make([]string, 22)
	for i := range many {
		many[i] = fmt.Sprintf("t%d", i)
	}
	for _, tc := range []struct {
		running []string
		want    string
	}{
		{nil, "Paused; nothing is running"},
		{[]string{"brief-plan-2"}, "Paused; 1 Task still running: brief-plan-2"},
		{[]string{"cli-1", "cli-2"}, "Paused; 2 Tasks still running: cli-1, cli-2"},
		{many, "Paused; 22 Tasks still running: " + strings.Join(many[:20], ", ") + " and 2 more"},
	} {
		if got := BriefPausedMessage(tc.running); got != tc.want {
			t.Errorf("BriefPausedMessage(%v) = %q, want %q", tc.running, got, tc.want)
		}
	}
}

func TestRunsPausedMessage(t *testing.T) {
	many := make([]string, 21)
	for i := range many {
		many[i] = fmt.Sprintf("t%d", i)
	}
	for _, tc := range []struct {
		stopping []string
		want     string
	}{
		{nil, "Paused, runs too; every run stopped"},
		{[]string{"cel-lens-plan-2"}, "Paused, runs too; 1 Task still stopping: cel-lens-plan-2"},
		{[]string{"cli-1", "cli-2"}, "Paused, runs too; 2 Tasks still stopping: cli-1, cli-2"},
		{many, "Paused, runs too; 21 Tasks still stopping: " + strings.Join(many[:20], ", ") + " and 1 more"},
	} {
		if got := RunsPausedMessage(tc.stopping); got != tc.want {
			t.Errorf("RunsPausedMessage(%v) = %q, want %q", tc.stopping, got, tc.want)
		}
	}
}

func TestBriefPhaseOf(t *testing.T) {
	for _, tc := range []struct {
		name  string
		facts BriefRoundFacts
		want  string
	}{
		{"no round yet", BriefRoundFacts{}, BriefPending},
		{"the operator waits before a round", BriefRoundFacts{OperatorNeedsInput: true}, BriefNeedsInput},
		{"a Task not made yet", BriefRoundFacts{Round: 1}, BriefPlanning},
		{"the planner works", BriefRoundFacts{Round: 1, TaskPhase: "Implementing"}, BriefPlanning},
		{"the plan is checked", BriefRoundFacts{Round: 1, TaskPhase: "Checking"}, BriefPlanning},
		{"the plan is accepted", BriefRoundFacts{Round: 1, TaskPhase: "Merged"}, BriefApplying},
		{"the Task failed", BriefRoundFacts{Round: 1, TaskPhase: "Failed"}, BriefFailed},
		{"the operator waits while applying", BriefRoundFacts{Round: 1, TaskPhase: "Merged", OperatorNeedsInput: true},
			BriefNeedsInput},
		{"applied", BriefRoundFacts{Round: 1, RoundOver: true, Outcome: ReasonApplied}, BriefPlanned},
		{"the planner asked", BriefRoundFacts{Round: 1, RoundOver: true, Outcome: ReasonPlannerNeedsInput},
			BriefNeedsInput},
		{"failed", BriefRoundFacts{Round: 1, RoundOver: true, Outcome: ReasonRoundFailed}, BriefFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := BriefPhaseOf(tc.facts); got != tc.want {
				t.Errorf("BriefPhaseOf(%+v) = %q, want %q", tc.facts, got, tc.want)
			}
		})
	}
}

func TestBriefIssueLabels(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels []string
		want   []string
	}{
		{"none", nil, []string{LabelReady}},
		{"the planner's kept", []string{"kind/feature", "area/ui"}, []string{LabelReady, "kind/feature", "area/ui"}},
		{"every status replaced", []string{"status/design", "kind/feature", "status/proposed"},
			[]string{LabelReady, "kind/feature"}},
		{"each once", []string{"kind/feature", " kind/feature", "status/ready", ""}, []string{LabelReady, "kind/feature"}},
		{"by hand, for a person", []string{"status/ready", LabelByHand}, []string{LabelNeedsHuman, LabelByHand}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := BriefIssueLabels(tc.labels); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("BriefIssueLabels(%q) = %q, want %q", tc.labels, got, tc.want)
			}
		})
	}
}

func TestApplyOrder(t *testing.T) {
	plan, err := ParsePlan(happyFiles())
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(plan.Objects))
	for _, o := range ApplyOrder(plan.Objects) {
		got = append(got, o.Ref().String())
	}
	want := []string{"Project/shop", "SoftwareSystem/shop", "DataStore/db", "Relationship/api-to-db",
		"Container/api", "Container/web"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ApplyOrder = %q, want %q", got, want)
	}
}

func TestRoundContainers(t *testing.T) {
	issues := []PlanIssue{{N: 1, Container: "web"}, {N: 2, Container: "api"}, {N: 3, Container: "web"}}
	for _, tc := range []struct {
		name    string
		created []string
		issues  []PlanIssue
		want    []string
	}{
		{"nothing", nil, nil, []string{}},
		{"created ones first, then issue targets, each once", []string{"web", "worker"}, issues,
			[]string{"web", "worker", "api"}},
		{"issues for existing Containers only", nil, issues, []string{"web", "api"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RoundContainers(tc.created, tc.issues); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("RoundContainers = %q, want %q", got, tc.want)
			}
		})
	}
	if got := IssuesFor(issues, "web"); len(got) != 2 || got[0].N != 1 || got[1].N != 3 {
		t.Errorf("IssuesFor(web) = %+v", got)
	}
}

func TestMissingCatalog(t *testing.T) {
	plan, err := ParsePlan(happyFiles())
	if err != nil {
		t.Fatal(err)
	}
	current := happyCurrent()
	if got := MissingCatalog(plan, current); got != nil {
		t.Errorf("MissingCatalog with the whole catalog = %q, want none", got)
	}
	current.Catalog = current.Catalog[:2] // the Frameworks only
	want := []string{"Standard web-standard", "Connector postgres"}
	if got := MissingCatalog(plan, current); !reflect.DeepEqual(got, want) {
		t.Errorf("MissingCatalog = %q, want %q (an existing Container's refs aren't asked for)", got, want)
	}
	if !HasFramework(current.Catalog) || HasFramework(current.Catalog[2:]) {
		t.Error("HasFramework is wrong")
	}
	// #1622: a Container on a Bundle needs the Bundle's entries as the sides
	// would be, and the Bundle itself.
	files := happyFiles()
	files[PlanModelFile] = webOnBundle(files[PlanModelFile])
	if plan, err = ParsePlan(files); err != nil {
		t.Fatal(err)
	}
	if got := MissingCatalog(plan, current); !reflect.DeepEqual(got, want) {
		t.Errorf("MissingCatalog on a Bundle = %q, want %q (the Bundle's Standard is needed)", got, want)
	}
	current.Bundles = nil
	if got, want := MissingCatalog(plan, current), []string{"Bundle react-web", "Connector postgres"}; !reflect.DeepEqual(got, want) {
		t.Errorf("MissingCatalog without the Bundle = %q, want %q", got, want)
	}
}

func TestRefusalTextAndKeepNewest(t *testing.T) {
	got := RefusalText([]Refusal{{"Container/web", "type is missing"}, {PlanChoicesFile, "is empty"}})
	if want := "Container/web: type is missing\nplan/choices.md: is empty"; got != want {
		t.Errorf("RefusalText = %q, want %q", got, want)
	}
	if got := KeepNewest([]int{1, 2, 3}, 2); !reflect.DeepEqual(got, []int{2, 3}) {
		t.Errorf("KeepNewest = %v", got)
	}
	if got := KeepNewest([]int{1}, 2); !reflect.DeepEqual(got, []int{1}) {
		t.Errorf("KeepNewest = %v", got)
	}
}

func TestClip(t *testing.T) {
	if got := Clip("héllo", 2); got != "h" {
		t.Errorf("Clip cut a character: %q", got)
	}
	if got := Clip("abc", 5); got != "abc" {
		t.Errorf("Clip = %q", got)
	}
	if got := ClipList([]string{"abc", "de", "f"}, 2, 2); !reflect.DeepEqual(got, []string{"ab", "de"}) {
		t.Errorf("ClipList = %q", got)
	}
}

func TestClipSentences(t *testing.T) {
	for _, tc := range []struct {
		name, s string
		n       int
		want    string
	}{
		{"fits", "  Planned Sprout.\n", 20, "Planned Sprout."},
		{"ends on the last whole sentence", "Planned Sprout. It is a todo app for one person, t", 30, "Planned Sprout."},
		{"keeps every sentence that fits", "One. Two! Three? Four.", 17, "One. Two! Three?"},
		{"a dot inside a word ends no sentence", "Uses v1.5 now and more", 12, "Uses v1.5…"},
		{"no whole sentence: the whole words", "A tiny garden-themed todo app", 18, "A tiny…"},
		{"a cut on a space keeps the word before", "Plan Sprout now", 10, "Plan…"},
		{"never past n, on a character", "héé", 4, "h…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ClipSentences(tc.s, tc.n)
			if got != tc.want || len(got) > tc.n {
				t.Errorf("ClipSentences(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
			}
		})
	}
}

func TestWorkspacePrompts(t *testing.T) {
	prompts := []string{"one", "two", "three"}
	for _, tc := range []struct {
		name         string
		roundPrompts int
		runs         bool
		want         []string
	}{
		{"between rounds, every prompt", 1, false, prompts},
		{"during a round, the ones it took", 1, true, []string{"one"}},
		{"during the idea's round, none", 0, true, []string{}},
		{"during a round that took them all", 3, true, prompts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := WorkspacePrompts(prompts, tc.roundPrompts, tc.runs); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("WorkspacePrompts = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIdeaPlanned(t *testing.T) {
	for _, tc := range []struct {
		name     string
		outcomes []string
		planned  int
		want     bool
	}{
		{"no round", nil, 0, false},
		{"a failed round", []string{ReasonRoundFailed}, 0, false},
		{"a round that asked", []string{ReasonPlannerNeedsInput}, 0, false},
		{"an applied round", []string{ReasonRoundFailed, ReasonApplied}, 0, true},
		{"prompts planned, the round gone from the history", []string{ReasonRoundFailed}, 2, true},
		{"tending rounds only, the applied one gone from the history", []string{ReasonTended, ReasonRoundFailed}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IdeaPlanned(tc.outcomes, tc.planned); got != tc.want {
				t.Errorf("IdeaPlanned(%q, %d) = %v, want %v", tc.outcomes, tc.planned, got, tc.want)
			}
		})
	}
}
