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
