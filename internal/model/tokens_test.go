package model

import "testing"

func TestRunStateTokens(t *testing.T) {
	used := &TokenUsage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, LargestSession: 8}
	tests := []struct {
		name string
		run  RunState
		want TokenUsage
	}{
		{"no sandbox", RunState{}, TokenUsage{}},
		{"reported tokens", RunState{Started: true, Done: true, Result: &RunResult{Tokens: used}}, *used},
		{"a result without tokens", RunState{Started: true, Done: true, Result: &RunResult{CostReported: true}},
			TokenUsage{RunsWithout: 1}},
		{"no result", RunState{Started: true, Done: true, Failure: "OOMKilled"}, TokenUsage{RunsWithout: 1}},
		{"ended while running", RunState{Started: true}, TokenUsage{RunsWithout: 1}},
		{"handed off twice", RunState{Started: true, Done: true, Result: &RunResult{Tokens: used,
			SessionHandoffs: []int64{130500, 140000}}},
			TokenUsage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, LargestSession: 8, SessionHandoffs: 2}},
		{"handed off without tokens", RunState{Started: true, Done: true, Result: &RunResult{
			SessionHandoffs: []int64{130500}}}, TokenUsage{RunsWithout: 1, SessionHandoffs: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.run.Tokens(); got != tt.want {
				t.Errorf("Tokens() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestTokenUsageAdd(t *testing.T) {
	sum := TokenUsage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, LargestSession: 140, RunsWithout: 1}
	sum.Add(TokenUsage{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, LargestSession: 90, SessionHandoffs: 2})
	want := TokenUsage{Input: 11, Output: 22, CacheRead: 33, CacheWrite: 44, LargestSession: 140, RunsWithout: 1,
		SessionHandoffs: 2}
	if sum != want {
		t.Errorf("Add() = %+v, want %+v: the counts add up, the largest session is the larger", sum, want)
	}
}

func TestHandoffNote(t *testing.T) {
	tests := []struct {
		round int
		role  string
		sizes []int64
		want  string
	}{
		{2, "implement", []int64{134200}, "Round 2's implement run handed off at 134k tokens."},
		{1, "review", []int64{130500, 140000, 1260000}, "Round 1's review run handed off 3 times, at 131k, 140k and 1.3M tokens."},
		{3, "", []int64{999}, "Round 3's agent run handed off at 999 tokens."},
	}
	for _, tt := range tests {
		if got := HandoffNote(tt.round, tt.role, tt.sizes); got != tt.want {
			t.Errorf("HandoffNote(%d, %q, %v) = %q, want %q", tt.round, tt.role, tt.sizes, got, tt.want)
		}
	}
}
