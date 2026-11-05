package model

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// A Task with a verifier has its approved change built, started and checked
// against its criteria before it merges (docs/plans/done/sdlc-loop.md, step 4).
func TestVerifySteps(t *testing.T) {
	done := func(result *RunResult) RunState { return RunState{Started: true, Done: true, Result: result} }
	approved := done(&RunResult{Verdict: VerdictApprove, Summary: "Every criterion met."})
	evidence := "Not met: GET /orders.csv lists every order; `curl -s localhost:8000/orders.csv` printed 404 Not Found."
	changes := done(&RunResult{Verdict: VerdictChanges, Summary: evidence})
	task := func(f TaskFacts) TaskFacts {
		f.Protected, f.ContainerReady, f.Container = true, true, "orders-api"
		if f.MaxRounds == 0 {
			f.MaxRounds = 3
		}
		if f.Round == 0 {
			f.Round = 1
		}
		return f
	}
	reviewing := func(f TaskFacts) TaskFacts { f.Phase = TaskReviewing; return task(f) }
	verifying := func(f TaskFacts) TaskFacts { f.Phase, f.Verify = TaskVerifying, true; return task(f) }
	tests := []struct {
		name    string
		facts   TaskFacts
		want    TaskAction
		message string
		reason  string
	}{
		{"with a verifier, an approved review goes to the verifier", reviewing(TaskFacts{Verify: true, Run: approved}),
			ActionVerify, "the verifier runs the change before it merges, in round 1", ""},
		{"without a verifier, an approved review merges as ever", reviewing(TaskFacts{Run: approved}), ActionMerge,
			"Approved; merging", ""},
		{"a review asking for changes is no verification", reviewing(TaskFacts{Verify: true,
			Run: done(&RunResult{Verdict: VerdictChanges, Summary: "Add a test."})}), ActionRetry, "", ""},
		{"a spec is never verified: it waits for a person", reviewing(TaskFacts{Verify: true, Spec: true,
			Run: approved}), ActionAwaitApproval, "", ""},
		{"a spec the reviewer may approve alone merges, unverified", reviewing(TaskFacts{Verify: true, Spec: true,
			SpecApproval: SpecApprovalReviewer, Run: approved}), ActionMerge, "Approved; merging", ""},
		{"a Standard change is never verified", reviewing(TaskFacts{Verify: true, AllowedPaths: []string{"Makefile"},
			Run: approved}), ActionMerge, "Approved; merging", ""},
		{"the verifier starts on the approved head", verifying(TaskFacts{}), ActionStartVerify,
			"Starting the verification", ""},
		{"the verifier is at work", verifying(TaskFacts{Run: RunState{Started: true}}), ActionWait,
			"The verifier is running the change", ""},
		{"the verifier's approval merges", verifying(TaskFacts{Run: approved}), ActionMerge, "Verified; merging", ""},
		{"approved, but main moved: main merged in, no new verification", verifying(TaskFacts{Run: approved,
			Behind: true}), ActionUpdateBranch, "main moved since the checks ran", ""},
		{"approved; the Git host still tests the pull request", verifying(TaskFacts{Run: approved,
			Unmergeable: true}), ActionWait, "waiting for the Git host", ""},
		{"the verifier's changes start the next round", verifying(TaskFacts{Run: changes}), ActionRetry,
			"Round 1 was not accepted", ""},
		{"the verifier's changes in the last round fail the Task", verifying(TaskFacts{Round: 3, Run: changes}),
			ActionFail, "Failed after 3 rounds", ""},
		{"a verifier without a verdict runs again", verifying(TaskFacts{Run: done(&RunResult{
			Error: "the verifier gave no JSON verdict"})}), ActionRetryVerify, "The verifier gave no verdict", ""},
		{"a verifier that ended without a result runs again", verifying(TaskFacts{ReviewAttempts: 1,
			Run: RunState{Started: true, Done: true, Failure: "OOMKilled"}}), ActionRetryVerify, "OOMKilled", ""},
		{"a verifier that failed three times fails the Task", verifying(TaskFacts{ReviewAttempts: 2,
			Run: done(&RunResult{Error: "no verdict"})}), ActionFail, "The verifier failed 3 times", ""},
		{"an unavailable verifier costs no round", verifying(TaskFacts{Run: done(&RunResult{Unavailable: true,
			Error: "overloaded"})}), ActionAgentUnavailable, "round 1 waits for it", ""},
		{"an interrupted verifier runs again", verifying(TaskFacts{Run: RunState{Started: true, Done: true,
			Interrupted: true, Failure: "evicted"}}), ActionRerun, "runs again", ""},
		{"a paused factory starts no verifier", verifying(TaskFacts{Pause: "maintenance"}), ActionWait,
			"no agent runs", ReasonPaused},
		{"a Task at its cost cap starts no verifier", verifying(TaskFacts{BudgetMilliUSD: 1000,
			CostMilliUSD: 1000}), ActionWait, "at its Crew's cap", ReasonOverBudget},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextTaskStep(tt.facts)
			if got.Action != tt.want || !strings.Contains(got.Message, tt.message) || got.Reason != tt.reason {
				t.Errorf("NextTaskStep() = %+v, want %s with %q, reason %q", got, tt.want, tt.message, tt.reason)
			}
		})
	}

	retry := NextTaskStep(verifying(TaskFacts{Run: changes}))
	if !strings.Contains(retry.Feedback, "The verifier built and ran the change and asked for changes") ||
		!strings.Contains(retry.Feedback, evidence) {
		t.Errorf("feedback = %q, want the verifier's evidence", retry.Feedback)
	}
	if slot := NextTaskStep(verifying(TaskFacts{NoFreeSlot: true})); slot.Action != ActionWait || !slot.SlotWait {
		t.Errorf("NextTaskStep() with no free slot = %+v, want a wait for a slot", slot)
	}
}

func TestVerifyProgress(t *testing.T) {
	p := VerifyChange(TaskProgress{Phase: TaskReviewing, Round: 2, ReviewAttempts: 2, Commit: "abc"})
	if p.Phase != TaskVerifying || p.Round != 2 || p.ReviewAttempts != 0 || p.Commit != "abc" {
		t.Errorf("VerifyChange() = %+v, want Verifying in round 2, its attempts counted afresh", p)
	}
	if p := StartRun(TaskProgress{Phase: TaskVerifying, Round: 2}, RoleVerifier); p.Phase != TaskVerifying {
		t.Errorf("StartRun(verify) = %+v, want Verifying", p)
	}
	name := SandboxName("orders-api-4", 2, 0, RoleVerifier, 1, 0)
	if name != "orders-api-4-r2-verify-a1" || SandboxRole(name) != RoleVerifier {
		t.Errorf("SandboxName() = %q (role %q), want orders-api-4-r2-verify-a1", name, SandboxRole(name))
	}
	rec, ok := RunState{Started: true, Result: &RunResult{Role: RoleVerifier}}.Record(2, name, time.Time{})
	if !ok || rec.Role != RoleVerifier {
		t.Errorf("Record() = %+v, want the verify role", rec)
	}
}

func TestOpenCriteria(t *testing.T) {
	review := []OpenCriterion{{Criterion: "A screenshot of the dashboard", Why: "No browser."}}
	verify := []OpenCriterion{{Criterion: "a screenshot  of the Dashboard", Why: "No display."},
		{Criterion: "The email reaches a real inbox", Why: "No mail account."}, {Criterion: " "}}
	want := []OpenCriterion{review[0], verify[1]}
	if got := OpenCriteria(review, verify); !reflect.DeepEqual(got, want) {
		t.Errorf("OpenCriteria() = %+v, want %+v", got, want)
	}
	if got := OpenCriteria(nil, nil); len(got) != 0 {
		t.Errorf("OpenCriteria(nil, nil) = %+v", got)
	}
}

func TestStandInVerify(t *testing.T) {
	tests := []struct {
		name, issue string
		round       int
		standIn     string
		verdict     string
		summary     string
	}{
		{"a stand-in change is met", "Issue #4: Export orders", 1, "Round 1 of the stand-in agent.\n", VerdictApprove,
			"`cat STAND-IN.md` printed \"Round 1 of the stand-in agent.\""},
		{"no stand-in file is no change", "Issue #4: Export orders", 1, "", VerdictChanges, "printed nothing"},
		{"the issue asks round 1 to fail", "Issue #4\n\nStand-in: verify fails", 1, "Round 1 of the stand-in agent.\n",
			VerdictChanges, "not met in round 1"},
		{"and round 2 to pass", "Issue #4\n\nstand-in: verify fails", 2, "Round 1.\nRound 2.\n", VerdictApprove,
			"\"Round 1. Round 2.\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict, summary := StandInVerify(tt.issue, tt.round, tt.standIn)
			if verdict != tt.verdict || !strings.Contains(summary, tt.summary) {
				t.Errorf("StandInVerify() = %q, %q; want %q with %q", verdict, summary, tt.verdict, tt.summary)
			}
		})
	}
}

func TestRouteVerifier(t *testing.T) {
	routes := []AgentRoute{{Label: "kind/docs", Implementer: "haiku"}, {Label: "kind/ui", Verifier: "browser"},
		{Label: "kind/web", Verifier: "chrome"}}
	tests := []struct {
		labels []string
		want   string
	}{
		{[]string{"kind/feature"}, ""},
		{[]string{"kind/docs"}, ""},
		{[]string{"kind/ui"}, "browser"},
		{[]string{"kind/web", "kind/ui"}, "browser"},
	}
	for _, tt := range tests {
		if got := RouteVerifier(routes, tt.labels); got != tt.want {
			t.Errorf("RouteVerifier(%v) = %q, want %q", tt.labels, got, tt.want)
		}
	}
}

// A route's verifier goes with the issue's Task; a spec and a Standard change
// get none.
func TestNextCrewStepVerifier(t *testing.T) {
	routes := []AgentRoute{{Label: "kind/ui", Verifier: "browser"}}
	ui := Issue{Number: 4, Body: criteria, Labels: []string{"kind/ui"}}
	spec := Issue{Number: 5, Body: "Big.", Spec: true, Labels: []string{"kind/ui"}}
	change := Issue{Number: 6, Body: criteria, StandardChange: true, Labels: []string{"kind/ui"}}
	got := NextCrewStep("orders-api", CrewFacts{ContainerReady: true, Parallel: 3, Routes: routes,
		Issues: []Issue{ui, spec, change},
		Tasks: []CrewTask{{Name: "orders-api-1", Issue: 1, Blocked: true,
			StandardChange: &IssueChange{Issue: 6, Paths: []string{"Makefile"}}}}})
	verifiers := map[int]string{}
	for _, s := range got.Start {
		verifiers[s.Issue] = s.Verifier
	}
	want := map[int]string{4: "browser", 5: "", 6: ""}
	if !reflect.DeepEqual(verifiers, want) {
		t.Errorf("verifiers by issue = %v, want %v (starts %+v)", verifiers, want, got.Start)
	}
}
