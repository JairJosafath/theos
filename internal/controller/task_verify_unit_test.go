/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"reflect"
	"testing"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// A Crew's Implement Tasks name its verifier, or their route's; a spec and a
// Standard change name none (docs/plans/sdlc-loop.md, step 4).
func TestCrewIssueTaskVerifier(t *testing.T) {
	container := &c4v1beta1.Container{ObjectMeta: objectMeta("apps", "orders-api")}
	withVerifier := newCrew("apps")
	withVerifier.Spec.Verifier = &c4v1beta1.LocalRef{Name: "verifier"}
	withVerifier.Spec.SpecWriter = &c4v1beta1.LocalRef{Name: "writer"}
	tests := []struct {
		name  string
		crew  *c4v1beta1.Crew
		start model.IssueStart
		want  *c4v1beta1.LocalRef
	}{
		{"the Crew's verifier", withVerifier, model.IssueStart{Issue: 4, Task: "orders-api-4"},
			&c4v1beta1.LocalRef{Name: "verifier"}},
		{"its route's verifier", withVerifier, model.IssueStart{Issue: 4, Task: "orders-api-4", Verifier: "browser"},
			&c4v1beta1.LocalRef{Name: "browser"}},
		{"a route's verifier in a Crew without one", newCrew("apps"),
			model.IssueStart{Issue: 4, Task: "orders-api-4", Verifier: "browser"}, &c4v1beta1.LocalRef{Name: "browser"}},
		{"none in a Crew without one", newCrew("apps"), model.IssueStart{Issue: 4, Task: "orders-api-4"}, nil},
		{"no spec is verified", withVerifier, model.IssueStart{Issue: 4, Task: "orders-api-4-spec", Spec: true}, nil},
		{"no Standard change is verified", withVerifier, model.IssueStart{Issue: 4, Task: "orders-api-4",
			AllowedPaths: []string{"Makefile"}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := crewIssueTask(tt.crew, container, tt.start).Spec.VerifierRef; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("verifierRef = %+v, want %+v", got, tt.want)
			}
		})
	}
	names := crewAgentNames(&c4v1beta1.CrewSpec{Implementer: c4v1beta1.LocalRef{Name: "claude"},
		Reviewer: c4v1beta1.LocalRef{Name: "reviewer"}, Verifier: &c4v1beta1.LocalRef{Name: "verifier"},
		Routes: []c4v1beta1.CrewRoute{{Label: "kind/ui", Verifier: &c4v1beta1.LocalRef{Name: "browser"}}}})
	if want := []string{"claude", "reviewer", "verifier", "browser"}; !reflect.DeepEqual(names, want) {
		t.Errorf("crewAgentNames() = %v, want %v: a missing verifier is AgentNotFound", names, want)
	}
}

// The verifier's verdict goes into status once its run gave one, whatever
// step follows; nothing else does.
func TestVerifyFacts(t *testing.T) {
	verified := &c4v1beta1.Task{Spec: c4v1beta1.TaskSpec{VerifierRef: &c4v1beta1.LocalRef{Name: "verifier"}}}
	done := model.RunState{Started: true, Done: true, Result: &model.RunResult{Verdict: model.VerdictChanges,
		Summary: "curl printed 404.", Open: []model.OpenCriterion{{Criterion: "x"}}}}
	tests := []struct {
		name  string
		task  *c4v1beta1.Task
		phase c4v1beta1.TaskPhase
		run   model.RunState
		want  *c4v1beta1.TaskVerification
	}{
		{"a verdict", verified, c4v1beta1.TaskVerifying, done, &c4v1beta1.TaskVerification{
			Verdict: model.VerdictChanges, Summary: "curl printed 404.", Commit: "sha-1",
			Open: []c4v1beta1.TaskOpenCriterion{{Criterion: "x"}}}},
		{"still running", verified, c4v1beta1.TaskVerifying, model.RunState{Started: true}, nil},
		{"no verdict", verified, c4v1beta1.TaskVerifying, model.RunState{Started: true, Done: true,
			Result: &model.RunResult{Error: "no JSON"}}, nil},
		{"a review's verdict is no verification", verified, c4v1beta1.TaskReviewing, done, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &c4v1beta1.TaskStatus{Phase: tt.phase, Commit: "sha-1"}
			facts := model.TaskFacts{Run: tt.run}
			verifyFacts(tt.task, status, &facts)
			if !reflect.DeepEqual(status.Verification, tt.want) || !facts.Verify {
				t.Errorf("verification = %+v, verify %v; want %+v", status.Verification, facts.Verify, tt.want)
			}
		})
	}
	facts := model.TaskFacts{}
	verifyFacts(&c4v1beta1.Task{}, &c4v1beta1.TaskStatus{}, &facts)
	if facts.Verify {
		t.Error("a Task without a verifierRef is verified")
	}
}

// A verified merge files the review's follow-ups and open criteria from
// status, the review run having ended, and the verifier's open criteria
// after them, once each.
func TestMergeFindings(t *testing.T) {
	reviewer := &c4v1beta1.Agent{ObjectMeta: objectMeta("apps", "reviewer")}
	verifier := &c4v1beta1.Agent{ObjectMeta: objectMeta("apps", "verifier")}
	agents := taskAgents{implementer: reviewer, reviewer: reviewer, verifier: verifier}
	verified := &c4v1beta1.Task{Spec: c4v1beta1.TaskSpec{Issue: 4, VerifierRef: &c4v1beta1.LocalRef{Name: "verifier"}}}
	status := &c4v1beta1.TaskStatus{
		Review: &c4v1beta1.TaskReview{Verdict: model.VerdictApprove,
			FollowUps: []c4v1beta1.TaskFollowUp{{Title: "Export customers", Criteria: []string{"A CSV"}}},
			Open:      []c4v1beta1.TaskOpenCriterion{{Criterion: "A screenshot", Why: "No browser."}}},
		Verification: &c4v1beta1.TaskVerification{Verdict: model.VerdictApprove, Open: []c4v1beta1.TaskOpenCriterion{
			{Criterion: "a screenshot", Why: "No display."}, {Criterion: "It opens in a spreadsheet", Why: "No desktop."}}},
	}

	followUps, open, by := mergeFindings(verified, status, agents, nil)
	if want := []model.FollowUp{{Title: "Export customers", Criteria: []string{"A CSV"}}}; !reflect.DeepEqual(followUps, want) {
		t.Errorf("follow-ups = %+v, want %+v", followUps, want)
	}
	if want := []model.OpenCriterion{{Criterion: "A screenshot", Why: "No browser."},
		{Criterion: "It opens in a spreadsheet", Why: "No desktop."}}; !reflect.DeepEqual(open, want) {
		t.Errorf("open = %+v, want %+v", open, want)
	}
	if by != "Agent `reviewer` and Agent `verifier`" {
		t.Errorf("by = %q", by)
	}

	status.Review.Open = nil
	if _, _, by := mergeFindings(verified, status, agents, nil); by != "Agent `verifier`" {
		t.Errorf("by = %q, want the verifier alone", by)
	}

	run := &model.RunResult{Verdict: model.VerdictApprove, FollowUps: []model.FollowUp{{Title: "From the run"}}}
	unverified := &c4v1beta1.Task{Spec: c4v1beta1.TaskSpec{Issue: 4}}
	followUps, open, by = mergeFindings(unverified, status, agents, run)
	if len(followUps) != 1 || followUps[0].Title != "From the run" || len(open) != 0 || by != "Agent `reviewer`" {
		t.Errorf("mergeFindings() without a verifier = %+v, %+v, %q; want the review run's alone", followUps, open, by)
	}
}
