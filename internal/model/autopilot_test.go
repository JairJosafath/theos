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
	"maps"
	"reflect"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func hours(h float64) time.Time { return t0.Add(time.Duration(h * float64(time.Hour))) }

func TestAutopilotMode(t *testing.T) {
	if AutopilotMode("") != AutopilotAsk {
		t.Errorf("unset = %s, want %s", AutopilotMode(""), AutopilotAsk)
	}
	for _, m := range []string{AutopilotOff, AutopilotAsk, AutopilotOn} {
		if AutopilotMode(m) != m {
			t.Errorf("AutopilotMode(%s) = %s", m, AutopilotMode(m))
		}
	}
}

// Each row of the heal-window table, inside its window and past it.
func TestBriefAttentionHealWindows(t *testing.T) {
	type want struct {
		healing bool
		actsAt  time.Time // when healing
		needs   []string  // substrings of the Needs you entry, when due
	}
	tests := []struct {
		name  string
		facts AttentionFacts
		now   time.Time
		want  want
	}{
		{"agent unavailable, before agentBackAt + 1h",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-1", Stage: StageImplement, Since: t0,
				Reason: "AgentUnavailable", AgentBackAt: hours(5)}}},
			hours(5.5), want{healing: true, actsAt: hours(6)}},
		{"agent unavailable, past agentBackAt + 1h",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-1", Stage: StageImplement, Since: t0,
				Reason: "AgentUnavailable", AgentBackAt: hours(5)}}},
			hours(6), want{needs: []string{"Task `cli-1` has waited 6h for its agent", "Check the Agent's credentials"}}},
		{"waiting for a slot, inside 4h",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-1", Stage: StageSlot, Since: t0, Reason: "WaitingForSlot"}}},
			hours(3.9), want{healing: true, actsAt: hours(4)}},
		{"at its Crew's cost cap, at once (#1893)",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-1", Stage: StageReview, Since: t0, Reason: "OverBudget",
				Message: "The Task's runs cost $1.20, at its Crew's cap of $1; raise the Crew's taskBudgetUSD to go on"}}},
			hours(0.1), want{needs: []string{"Task `cli-1` reached its Crew's cost cap: The Task's runs cost $1.20",
				"Raise the Crew's taskBudgetUSD"}}},
		{"a spec waiting for a person's approval, at once (sdlc-loop step 3)",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-4-spec", Stage: StageApproval, Since: t0, Spec: true,
				Reason: "WaitingForApproval", Message: "The spec of issue #4 passed CI and the reviewer; approve " +
					"pull request http://gitea/x/cli/pulls/5 on the Git host to merge it"}}},
			hours(0.1), want{needs: []string{"Task `cli-4-spec` waits for you to approve its spec: The spec of issue #4",
				"Approve the spec's pull request on the Git host"}}},
		{"a change its Crew's approval rules hold for a person, at once (#62)",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-9", Stage: StageApproval, Since: t0,
				Reason: "WaitingForApproval", Message: ChangeWaitMessage(9, "http://git.example/x/cli/pulls/12",
					"approval rule 1 (paths migrations/) asks for a person for migrations/001.sql")}}},
			hours(0.1), want{needs: []string{"Task `cli-9` waits for you to approve its change: The change for issue #9",
				"Approve the pull request on the Git host"}}},
		{"paused by a person, however long (step 12 of the self-hosting plan)",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-1", Stage: StageImplement, Since: t0, Reason: "Paused",
				Message: "Paused by the factory (Brief cel-lens paused its runs); no agent runs until the pause ends"}}},
			hours(24 * 7), want{healing: true}},
		{"waiting for a slot, past 4h",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-1", Stage: StageSlot, Since: t0, Reason: "WaitingForSlot"}}},
			hours(4), want{needs: []string{"waited 4h for an agent slot", "more agent slots"}}},
		{"waiting for a test environment the project lacks, at once (test-environments T2)",
			AttentionFacts{Tasks: []WatchedTask{{Name: "ext-3", Stage: StageQueued, Since: t0,
				Reason:  ReasonWaitingForEnvironment,
				Message: EnvironmentWaitMessage(EnvNotInProject, []string{"browser"}, "")}}},
			hours(0.1), want{needs: []string{"Task `ext-3` has waited", "a test environment the project lacks",
				"Add the TestEnvironment"}}},
		{"waiting for a test environment past its budget, at once (test-environments T10)",
			AttentionFacts{Tasks: []WatchedTask{{Name: "ext-3", Stage: StageQueued, Since: t0,
				Reason:  ReasonWaitingForEnvironment,
				Message: EnvironmentWaitMessage(EnvOverBudget, []string{"ios"}, "")}}},
			hours(0.1), want{needs: []string{"spent today's budget", "budgetUSDPerDay"}}},
		{"waiting for a test environment whose credential is refused, at once (test-environments T9)",
			AttentionFacts{Tasks: []WatchedTask{{Name: "ext-3", Stage: StageQueued, Since: t0,
				Reason:  ReasonWaitingForEnvironment,
				Message: EnvironmentWaitMessage(EnvCredentialRefused, []string{"gpu"}, "")}}},
			hours(0.1), want{needs: []string{"refuses the credential", "credentialsSecretRef"}}},
		{"waiting for a lease of a test environment, inside 4h",
			AttentionFacts{Tasks: []WatchedTask{{Name: "ext-3", Stage: StageQueued, Since: t0,
				Reason:  ReasonWaitingForEnvironment,
				Message: EnvironmentWaitMessage(EnvAllLeased, []string{"browser"}, "")}}},
			hours(3.9), want{healing: true, actsAt: hours(4)}},
		{"waiting for a lease of a test environment, past 4h",
			AttentionFacts{Tasks: []WatchedTask{{Name: "ext-3", Stage: StageQueued, Since: t0,
				Reason:  ReasonWaitingForEnvironment,
				Message: EnvironmentWaitMessage(EnvPending, []string{"browser"}, "the pod is not scheduled")}}},
			hours(4), want{needs: []string{"waited 4h for a test environment", "limits.maxLeases"}}},
		{"a Container asking for a test environment the project lacks, at once (test-environments T2)",
			AttentionFacts{Containers: []WatchedContainer{{Name: "ext", Phase: "Ready", Since: t0,
				Environments: MissingEnvironmentsMessage([]string{"browser"}), EnvironmentsSince: t0}}},
			hours(0.1), want{needs: []string{"Container `ext` asks for test environments the project lacks",
				"no TestEnvironment browser", "take them off its Standard or Framework"}}},
		{"blocked on a Standard change in review",
			AttentionFacts{Tasks: []WatchedTask{{Name: "core-8", Stage: StageBlocked, Since: t0,
				Reason: "WaitingForStandardChange", StandardChange: 27}}},
			hours(40), want{healing: true, actsAt: hours(48)}},
		{"blocked on a Standard change whose Task ended, inside 1h",
			AttentionFacts{Tasks: []WatchedTask{{Name: "core-8", Stage: StageBlocked, Since: t0,
				Reason: "WaitingForStandardChange", StandardChange: 27, StandardChangeEnded: hours(25)}}},
			hours(25.5), want{healing: true, actsAt: hours(26)}},
		{"blocked on a Standard change whose Task ended, past 1h",
			AttentionFacts{Tasks: []WatchedTask{{Name: "core-8", Stage: StageBlocked, Since: t0,
				Reason: "WaitingForStandardChange", StandardChange: 27, StandardChangeEnded: hours(25)}}},
			hours(26), want{needs: []string{"Task `core-8` has waited 26h for Standard change #27", "Review the change"}}},
		{"blocked on a Standard change nobody reviews",
			AttentionFacts{Tasks: []WatchedTask{{Name: "core-8", Stage: StageBlocked, Since: t0,
				Reason: "WaitingForStandardChange", StandardChange: 27}}},
			hours(48), want{needs: []string{"waited 48h for Standard change #27"}}},
		{"in checking, never due",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-1", Stage: StageChecks, Since: t0, Reason: "Checking"}}},
			hours(100), want{healing: true}},
		{"implementing, inside twice its stuck window",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-1", Stage: StageImplement, Since: t0, Reason: "Running"}}},
			hours(3.9), want{healing: true, actsAt: hours(4)}},
		{"implementing, past twice its stuck window",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-1", Stage: StageImplement, Since: t0, Reason: "Running",
				Message: "Round 1"}}},
			hours(4), want{needs: []string{"Task `cli-1` has been implement for 4h", "delete the Task", "Round 1."}}},
		{"agent unavailable with no agentBackAt, as stuck in its stage",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-1", Stage: StageImplement, Since: t0,
				Reason: "AgentUnavailable"}}},
			hours(1), want{healing: true, actsAt: hours(4)}},
		{"waiting on a red main, at once (#1892)",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-1", Stage: StageQueued, Since: t0, Reason: "BaseRed"}}},
			hours(0.1), want{needs: []string{"Task `cli-1` has waited", "for main to turn green", "Fix main's checks"}}},
		{"in the slot stage, another reason",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-1", Stage: StageSlot, Since: t0, Reason: "Pending"}}},
			hours(3), want{healing: true, actsAt: hours(4)}},
		{"waiting for a slot, queued stage",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-1", Stage: StageQueued, Since: t0, Reason: "WaitingForSlot"}}},
			hours(4), want{needs: []string{"for an agent slot"}}},
		{"in review, inside twice its stuck window",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-1", Stage: StageReview, Since: t0}}},
			hours(1.5), want{healing: true, actsAt: hours(2)}},
		{"queued, past twice its stuck window",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-1", Stage: StageQueued, Since: t0}}},
			hours(2), want{needs: []string{"has been queued for 2h"}}},
		{"Crew not ready for another reason, inside 1h",
			AttentionFacts{Crews: []WatchedCrew{{Name: "cli", Reason: "GitProviderUnavailable", Since: t0}}},
			hours(0.5), want{healing: true, actsAt: hours(1)}},
		{"Crew not ready for another reason, past 1h",
			AttentionFacts{Crews: []WatchedCrew{{Name: "cli", Reason: "GitProviderUnavailable", Since: t0}}},
			hours(1), want{needs: []string{"not been ready for 1h: GitProviderUnavailable."}}},
		{"in review, past twice its stuck window",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-1", Stage: StageReview, Since: t0}}},
			hours(2), want{needs: []string{"has been review for 2h"}}},
		{"queued, inside twice its stuck window",
			AttentionFacts{Tasks: []WatchedTask{{Name: "cli-1", Stage: StageQueued, Since: t0}}},
			hours(1), want{healing: true, actsAt: hours(2)}},
		{"Crew without its Git host, inside 1h",
			AttentionFacts{Crews: []WatchedCrew{{Name: "cli", Reason: "GitHostUnavailable", Since: t0}}},
			hours(0.5), want{healing: true, actsAt: hours(1)}},
		{"Crew without its Git host, past 1h",
			AttentionFacts{Crews: []WatchedCrew{{Name: "cli", Reason: "GitHostUnavailable", Since: t0,
				Message: "Could not read the issues: timeout"}}},
			hours(1), want{needs: []string{"Crew `cli` has not been ready for 1h: Could not read the issues: timeout.",
				"Check the GitProvider"}}},
		{"Crew waiting for its Container, inside 1h",
			AttentionFacts{Crews: []WatchedCrew{{Name: "cli", Reason: "ContainerNotReady", Since: t0}}},
			hours(0.9), want{healing: true, actsAt: hours(1)}},
		{"Crew waiting for its Container, past 1h",
			AttentionFacts{Crews: []WatchedCrew{{Name: "cli", Reason: "ContainerNotReady", Since: t0}}},
			hours(2), want{needs: []string{"See its Container"}}},
		{"Crew whose Agent is missing, at once",
			AttentionFacts{Crews: []WatchedCrew{{Name: "cel-lens-cli", Reason: "AgentNotFound", Since: t0,
				Message: "Agent implementer not found"}}},
			t0, want{needs: []string{"Crew `cel-lens-cli` can't start: Agent implementer not found.", "Create it"}}},
		{"Container failed, at once",
			AttentionFacts{Containers: []WatchedContainer{{Name: "cli", Phase: "Failed", Since: t0,
				Message: "Layer conflict on Makefile"}}},
			t0, want{needs: []string{"Container `cli` failed: Layer conflict on Makefile.", "delete it and plan it again"}}},
		{"failed issue, at once",
			AttentionFacts{Failed: []FailedIssue{{Container: "cli", Number: 1, Kind: FailedTask}}},
			t0, want{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := BriefAttention(tc.facts, tc.now)
			switch {
			case tc.want.healing:
				if len(a.Healing) != 1 || len(a.NeedsYou) != 0 || !a.Healing[0].ActsAt.Equal(tc.want.actsAt) {
					t.Fatalf("attention = %+v; want healing until %v", a, tc.want.actsAt)
				}
				if a.Healing[0].Reason == "" {
					t.Errorf("healing says no reason: %+v", a.Healing[0])
				}
			case tc.want.needs != nil:
				if len(a.NeedsYou) != 1 || len(a.Healing) != 0 {
					t.Fatalf("attention = %+v; want one Needs you", a)
				}
				e := a.NeedsYou[0]
				text := e.Question + " | " + e.Recommend + " | " + e.Why
				for _, s := range tc.want.needs {
					if !strings.Contains(text, s) {
						t.Errorf("Needs you %q lacks %q", text, s)
					}
				}
				if !e.Since.Equal(t0) || e.Recommend == "" {
					t.Errorf("Needs you since %v, recommend %q", e.Since, e.Recommend)
				}
			default:
				if len(a.Due) != 1 || len(a.Healing)+len(a.NeedsYou) != 0 {
					t.Errorf("attention = %+v; want one due failed issue", a)
				}
			}
		})
	}
}

// A by-hand issue is the person's: under Needs you, never due.
func TestBriefAttentionByHand(t *testing.T) {
	a := BriefAttention(AttentionFacts{Failed: []FailedIssue{{Container: "web", Number: 3, Title: "Screenshot",
		Kind: FailedByHand, Reason: "Needs a screenshot of the store page."}}}, t0)
	if len(a.Due) != 0 || len(a.NeedsYou) != 1 || a.NeedsYou[0].Name != "web#3" ||
		!strings.Contains(a.NeedsYou[0].Question, "needs a person: Needs a screenshot of the store page.") {
		t.Errorf("attention = %+v", a)
	}
}

func TestBriefAttentionIgnores(t *testing.T) {
	a := BriefAttention(AttentionFacts{Containers: []WatchedContainer{{Name: "cli", Phase: "Ready"}}}, t0)
	if len(a.Healing)+len(a.NeedsYou)+len(a.Due) != 0 {
		t.Errorf("a ready Container: %+v", a)
	}
}

// A failed issue a round answered is due again only when its tries grow or
// its wait lapses (decision 7).
func TestBriefAttentionAnswered(t *testing.T) {
	issue := FailedIssue{Container: "cli", Number: 1, Title: "The CLI", Kind: FailedTask, Tries: 1,
		Reason: "Task `cli-1` failed"}
	tended := func(answer string, tries int, r *Recommendation) []TendedIssue {
		return []TendedIssue{{Container: "cli", Issue: 1, Answer: answer, Why: "The engine has no release yet.",
			Recommend: r, Tries: tries, At: t0, Round: 3}}
	}
	tests := []struct {
		name    string
		facts   AttentionFacts
		now     time.Time
		due     bool
		healing bool
		needs   []string
	}{
		{"unanswered", AttentionFacts{}, t0, true, false, nil},
		{"another issue answered", AttentionFacts{Tended: []TendedIssue{{Container: "cli", Issue: 2, Answer: AnswerWait,
			At: t0}}}, t0, true, false, nil},
		{"waiting", AttentionFacts{Tended: tended(AnswerWait, 1, nil)}, hours(23), false, true, nil},
		{"wait lapses on a merge or a release", AttentionFacts{Tended: tended(AnswerWait, 1, nil), Changed: hours(2)},
			hours(3), true, false, nil},
		{"a change before the wait doesn't lapse it", AttentionFacts{Tended: tended(AnswerWait, 1, nil),
			Changed: hours(-1)}, hours(3), false, true, nil},
		{"wait lapses after 24h", AttentionFacts{Tended: tended(AnswerWait, 1, nil)}, hours(24), true, false, nil},
		{"tried again and failed again", AttentionFacts{Tended: tended(AnswerAsk, 0, nil)}, t0, true, false, nil},
		{"asked, recommending a retry", AttentionFacts{Tended: tended(AnswerAsk, 1,
			&Recommendation{Answer: AnswerRetry, Why: "The checker is merged.", Clarify: "Import it."})}, hours(100),
			false, false, []string{`Issue cli#1 "The CLI": The engine has no release yet.`, "Try it again, with a clarification.",
				"The checker is merged."}},
		{"asked, recommending nothing", AttentionFacts{Tended: tended(AnswerAsk, 1, nil)}, t0, false, false,
			[]string{"Say in a prompt whether to try it again", "Task `cli-1` failed"}},
		{"retried, back with no new try", AttentionFacts{Tended: tended(AnswerRetry, 1, nil)}, t0, false, false,
			[]string{"handed back again after round 3's retry, with no new try", "See the issue"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.facts.Failed = []FailedIssue{issue}
			a := BriefAttention(tc.facts, tc.now)
			if (len(a.Due) == 1) != tc.due || (len(a.Healing) == 1) != tc.healing || (len(a.NeedsYou) > 0) != (tc.needs != nil) {
				t.Fatalf("attention = %+v; want due %v, healing %v, needs you %v", a, tc.due, tc.healing, tc.needs)
			}
			if tc.healing && !a.Healing[0].ActsAt.Equal(hours(24)) {
				t.Errorf("a wait acts at %v, want 24h after it", a.Healing[0].ActsAt)
			}
			for _, s := range tc.needs {
				e := a.NeedsYou[0]
				if text := e.Question + " | " + e.Recommend + " | " + e.Why; !strings.Contains(text, s) {
					t.Errorf("Needs you %q lacks %q", text, s)
				}
			}
		})
	}
}

func TestBriefAttentionNeedsYouOrderAndLimit(t *testing.T) {
	containers := make([]WatchedContainer, 0, MaxNeedsYou+5)
	for i := range MaxNeedsYou + 5 {
		containers = append(containers, WatchedContainer{Name: "c", Phase: "Failed",
			Since: hours(float64(MaxNeedsYou + 5 - i))})
	}
	a := BriefAttention(AttentionFacts{Containers: containers}, hours(100))
	if len(a.NeedsYou) != MaxNeedsYou || !a.NeedsYou[0].Since.Equal(hours(1)) {
		t.Errorf("%d entries, the first since %v; want %d, oldest first", len(a.NeedsYou), a.NeedsYou[0].Since, MaxNeedsYou)
	}
}

func TestStartsTend(t *testing.T) {
	ready := TendFacts{Mode: AutopilotOn, Round: 2, RoundOver: true, Due: 1}
	tests := []struct {
		name string
		edit func(*TendFacts)
		now  time.Time
		want bool
	}{
		{"on", func(*TendFacts) {}, t0, true},
		{"ask", func(f *TendFacts) { f.Mode = AutopilotAsk }, t0, true},
		{"off", func(f *TendFacts) { f.Mode = AutopilotOff }, t0, false},
		{"unset means ask", func(f *TendFacts) { f.Mode = "" }, t0, true},
		{"no round yet", func(f *TendFacts) { f.Round = 0 }, t0, false},
		{"a round runs", func(f *TendFacts) { f.RoundOver = false }, t0, false},
		{"a prompt waits", func(f *TendFacts) { f.PromptWaiting = true }, t0, false},
		{"the operator needs input", func(f *TendFacts) { f.OperatorNeedsInput = true }, t0, false},
		{"nothing due", func(f *TendFacts) { f.Due = 0 }, t0, false},
		{"paused", func(f *TendFacts) { f.Paused = true }, t0, false},
		{"inside the interval", func(f *TendFacts) { f.LastTend = hours(-0.9) }, t0, false},
		{"past the interval", func(f *TendFacts) { f.LastTend = hours(-1) }, t0, true},
	}
	for _, tc := range tests {
		f := ready
		tc.edit(&f)
		if got := StartsTend(f, tc.now); got != tc.want {
			t.Errorf("%s: StartsTend = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// An issue is answered once per failure: once a round answered it, it is not
// due, so no second round starts, until its tries grow or its wait lapses.
func TestTendAnsweredOnce(t *testing.T) {
	failed := []FailedIssue{{Container: "cli", Number: 1, Kind: FailedTask, Tries: 1}}
	due := BriefAttention(AttentionFacts{Failed: failed}, t0).Due
	facts := TendFacts{Mode: AutopilotOn, Round: 2, RoundOver: true, Due: len(due)}
	if !StartsTend(facts, t0) {
		t.Fatal("a due failed issue starts no round")
	}
	answers := TendAnswers(AutopilotOn, []FailedAnswer{fa("cli", 1, AnswerWait, "No release yet.", "")}, due, true)
	tended := Tended(nil, answers, failed, 3, t0)
	for _, tc := range []struct {
		name    string
		tries   int
		changed time.Time
		now     time.Time
		want    bool
	}{
		{"an hour later, nothing changed", 1, time.Time{}, hours(2), false},
		{"a merge", 1, hours(1), hours(2), true},
		{"24h", 1, time.Time{}, hours(24), true},
		{"tried and failed again", 2, time.Time{}, hours(2), true},
	} {
		f := []FailedIssue{failed[0]}
		f[0].Tries = tc.tries
		facts.Due = len(BriefAttention(AttentionFacts{Failed: f, Tended: tended, Changed: tc.changed}, tc.now).Due)
		facts.LastTend = t0
		if got := StartsTend(facts, tc.now); got != tc.want {
			t.Errorf("%s: StartsTend = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTendAnswers(t *testing.T) {
	due := []FailedIssue{{Container: "cli", Number: 1}, {Container: "cli", Number: 2}, {Container: "cli", Number: 3}}
	answers := []FailedAnswer{
		fa("cli", 1, AnswerRetry, "The checker is merged.", "Import it."),
		fa("cli", 2, AnswerWait, "No release yet.", ""),
	}
	unanswered := FailedAnswer{Container: "cli", Issue: 3, Answer: AnswerAsk, Why: "The tending round didn't decide it."}

	got := TendAnswers(AutopilotOn, answers, due, true)
	if want := append(answers[:2:2], unanswered); !reflect.DeepEqual(got, want) {
		t.Errorf("on:\n%+v\nwant\n%+v", got, want)
	}

	got = TendAnswers(AutopilotAsk, answers, due, true)
	asked := FailedAnswer{Container: "cli", Issue: 1, Answer: AnswerAsk,
		Why:       "Should the Brief try it again? Autopilot is Ask, so it waits for your answer.",
		Recommend: &Recommendation{Answer: AnswerRetry, Why: "The checker is merged.", Clarify: "Import it."}}
	if want := []FailedAnswer{asked, answers[1], unanswered}; !reflect.DeepEqual(got, want) {
		t.Errorf("ask:\n%+v\nwant\n%+v", got, want)
	}

	// A prompt round's answers are the person's: applied as they are.
	if got := TendAnswers(AutopilotAsk, answers, due, false); !reflect.DeepEqual(got, answers) {
		t.Errorf("a prompt round in ask:\n%+v\nwant\n%+v", got, answers)
	}
	if got := TendAnswers(AutopilotOn, nil, due[:1], true); len(got) != 1 || got[0].Answer != AnswerAsk ||
		got[0].Recommend != nil {
		t.Errorf("a round that answers nothing: %+v; want every due issue asked, recommending none", got)
	}
}

func TestTended(t *testing.T) {
	failed := []FailedIssue{{Container: "web", Number: 2, Tries: 2}, {Container: "cli", Number: 1, Tries: 1}}
	earlier := []TendedIssue{
		{Container: "cli", Issue: 1, Answer: AnswerAsk, At: hours(-5), Round: 1},
		{Container: "web", Issue: 2, Answer: AnswerWait, At: hours(-5), Round: 1},
		{Container: "web", Issue: 9, Answer: AnswerWait, At: hours(-5), Round: 1}, // no longer failed
	}
	got := Tended(earlier, []FailedAnswer{fa("cli", 1, AnswerWait, "No release yet.", ""),
		fa("cli", 7, AnswerClose, "Gone.", "")}, failed, 2, t0)
	want := []TendedIssue{
		{Container: "cli", Issue: 1, Answer: AnswerWait, Why: "No release yet.", Tries: 1, At: t0, Round: 2},
		{Container: "web", Issue: 2, Answer: AnswerWait, At: hours(-5), Round: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tended =\n%+v\nwant\n%+v", got, want)
	}
}

func TestBriefWorkspaceTending(t *testing.T) {
	failed := []FailedIssue{{Container: "cli", Number: 1, Title: "The CLI", Kind: FailedTask, Tries: 1, Body: "Add a CLI.\n"},
		{Container: "cli", Number: 2, Title: "Docs", Kind: FailedPerson}}
	tended := []TendedIssue{
		{Container: "cli", Issue: 1, Answer: AnswerAsk, Why: "It needs a paid API key.", Round: 3, At: t0,
			Recommend: &Recommendation{Answer: AnswerRetry, Why: "The key is there.", Clarify: "Use it."}},
		{Container: "cli", Issue: 2, Answer: AnswerWait, Why: "No release\nyet.", Round: 3, At: t0},
	}
	files, err := BriefWorkspaceFiles(BriefWorkspace{Brief: "todo", Idea: "A todo list", GitProvider: "gitea",
		Failed: failed, Tended: tended, Tending: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(files[BriefFile], "No new prompt: decide the failed issues in `model/failed.yaml`") ||
		strings.Contains(files[BriefFile], "Answer one only when") {
		t.Errorf("brief.md of a tending round:\n%s", files[BriefFile])
	}
	if !strings.Contains(files[WorkspaceFailedFile], "  asked: It needs a paid API key.\n  recommend:\n"+
		"    answer: retry\n    why: The key is there.\n    clarify: Use it.\n") ||
		strings.Count(files[WorkspaceFailedFile], "asked:") != 1 {
		t.Errorf("%s:\n%s", WorkspaceFailedFile, files[WorkspaceFailedFile])
	}
	want := "- container: cli\n  issue: 1\n  round: 3\n  answer: ask\n  why: It needs a paid API key.\n" +
		"  recommend:\n    answer: retry\n    why: The key is there.\n    clarify: Use it.\n  at: \"2026-10-07T12:00:00Z\"\n" +
		"- container: cli\n  issue: 2\n  round: 3\n  answer: wait\n  why: No release yet.\n  at: \"2026-10-07T12:00:00Z\"\n"
	if files[WorkspaceTendedFile] != want {
		t.Errorf("%s =\n%s\nwant\n%s", WorkspaceTendedFile, files[WorkspaceTendedFile], want)
	}

	files, err = BriefWorkspaceFiles(BriefWorkspace{Brief: "todo", Idea: "A todo list", GitProvider: "gitea"})
	if err != nil {
		t.Fatal(err)
	}
	if files[WorkspaceTendedFile] != "[]\n" || strings.Contains(files[BriefFile], "Tending round") {
		t.Errorf("with none: %s = %q, brief.md:\n%s", WorkspaceTendedFile, files[WorkspaceTendedFile], files[BriefFile])
	}
	for _, f := range []string{WorkspaceFailedFile, WorkspaceTendedFile} {
		if _, ok := files[f]; !ok {
			t.Errorf("BriefWorkspaceFiles lacks %s", f)
		}
	}
}

func TestFailedAnswerRetriesAndApplies(t *testing.T) {
	for _, tc := range []struct {
		a                FailedAnswer
		retries, applies bool
	}{
		{fa("cli", 1, AnswerRetry, "", ""), true, true},
		{fa("cli", 1, AnswerClose, "", ""), false, true},
		{fa("cli", 1, AnswerWait, "", ""), false, false},
		{ask(1, &Recommendation{Answer: AnswerRetry}), true, false},
		{ask(1, &Recommendation{Answer: AnswerClose}), false, false},
		{ask(1, nil), false, false},
	} {
		if tc.a.Retries() != tc.retries || tc.a.Applies() != tc.applies {
			t.Errorf("%+v: retries %v, applies %v", tc.a, tc.a.Retries(), tc.a.Applies())
		}
	}
}

func TestParseTendPlan(t *testing.T) {
	files := map[string]string{
		PlanFailedFile:          "- {container: api, issue: 3, answer: close, why: Split in two.}\n",
		PlanSummaryFile:         "Split the API's flags issue in two.\n",
		"plan/issues/01-api.md": readyIssue,
		"plan/issues/.gitkeep":  "",
		"brief.md":              "not part of the plan",
	}
	plan, err := ParseTendPlan(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.FailedAnswers) != 1 || len(plan.Issues) != 1 || plan.Summary == "" || len(plan.Objects) != 0 {
		t.Errorf("plan = %+v", plan)
	}
	if refusals := CheckTendPlan(plan, happyCurrent()); len(refusals) > 0 {
		t.Errorf("refused: %v", refusals)
	}
	for _, extra := range []string{PlanModelFile, PlanChoicesFile, PlanNeedsInputFile, PlanSuggestionsFile,
		PlanReleasesFile} {
		withExtra := maps.Clone(files)
		withExtra[extra] = "anything"
		_, err := ParseTendPlan(withExtra)
		if err == nil || !strings.Contains(err.Error(), "leave out "+extra) {
			t.Errorf("with %s: err = %v, want it refused", extra, err)
		}
	}
	plan.Issues[0].Container = "web"
	if refusals := CheckTendPlan(plan, happyCurrent()); len(refusals) == 0 {
		t.Error("an issue for a Container the namespace lacks passed")
	}
}

func TestTendSummaryAndCounts(t *testing.T) {
	answers := []FailedAnswer{
		{Container: "cli", Issue: 1, Answer: AnswerRetry, Why: "The engine is merged now"},
		{Container: "cli", Issue: 2, Answer: AnswerWait, Why: "The engine has no release yet."},
		{Container: "cli", Issue: 3, Answer: AnswerAsk, Why: "It needs a paid API."},
	}
	want := "Tried cli#1 again: The engine is merged now. Waits on cli#2: The engine has no release yet. " +
		"Asks you about cli#3: It needs a paid API."
	if got := TendSummary(AutopilotOn, "", answers); got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if got := TendSummary(AutopilotOn, " Retried\nthe CLI. ", answers); got != "Retried the CLI." {
		t.Errorf("summary with summary.md = %q", got)
	}
	if got := TendSummary(AutopilotAsk, "", nil); got != "Decided nothing." {
		t.Errorf("empty summary = %q", got)
	}
	if r, c, w, a := TendCounts(answers); r != 1 || c != 0 || w != 1 || a != 1 {
		t.Errorf("counts = %d %d %d %d", r, c, w, a)
	}
}

// In Ask, a tending round that proposes a retry or a close says it proposes
// and waits, whatever summary.md says, so it agrees with the counts and with
// Needs you (#872).
func TestTendSummaryInAsk(t *testing.T) {
	planned := []FailedAnswer{
		{Container: "ext", Issue: 299, Answer: AnswerRetry, Why: "Fetch the engine from Gitea as CI does."},
		{Container: "ext", Issue: 353, Answer: AnswerClose, Why: "It is filed again, split."},
		{Container: "ext", Issue: 4, Answer: AnswerAsk, Why: "Store pictures need the person."},
	}
	answers := TendAnswers(AutopilotAsk, planned, nil, true)
	got := TendSummary(AutopilotAsk, "The fix goes back to its Crew. #353 is closed.", answers)
	want := "Autopilot is Ask, so each proposal waits for your answer. " +
		"Proposes trying ext#299 again: Fetch the engine from Gitea as CI does. " +
		"Proposes closing ext#353: It is filed again, split. Asks you about ext#4: Store pictures need the person."
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	if r, c, _, a := TendCounts(answers); r != 0 || c != 0 || a != 3 {
		t.Errorf("counts = %d retried, %d closed, %d asked", r, c, a)
	}
	// With nothing proposed, the planner's summary.md stands.
	waits := []FailedAnswer{{Container: "ext", Issue: 4, Answer: AnswerWait, Why: "The engine has no release."}}
	if got := TendSummary(AutopilotAsk, "Waits for the engine.", waits); got != "Waits for the engine." {
		t.Errorf("summary without a proposal = %q", got)
	}
	// In On the same asks are the planner's own, and its summary.md stands.
	if got := TendSummary(AutopilotOn, "Asks about three.", answers); got != "Asks about three." {
		t.Errorf("summary in On = %q", got)
	}
}

func TestNeedsYouMessage(t *testing.T) {
	listed := []NeedsYou{{Question: "Crew `x` can't start.", Recommend: "Create it."}, {Question: "Another."}}
	if got := NeedsYouMessage(listed, 3); got != "Crew `x` can't start. Create it. (2 more)" {
		t.Errorf("message = %q", got)
	}
	if got := NeedsYouMessage(listed[:1], 1); got != "Crew `x` can't start. Create it." {
		t.Errorf("message = %q", got)
	}
	a := BriefAttention(AttentionFacts{Containers: []WatchedContainer{{Name: "a", Phase: "Failed"}}}, t0)
	if a.Waiting != 1 {
		t.Errorf("waiting = %d, want 1", a.Waiting)
	}
}
