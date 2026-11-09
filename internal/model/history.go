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

import "time"

// MaxTransitions is how many transitions a Task keeps: enough for every step of
// a long Task, small enough for its status.
const MaxTransitions = 64

// MaxTransitionMessage is the longest message a transition keeps, in runes.
const MaxTransitionMessage = 200

// Transition is one step in a Task's timeline: from At on, the Task was in Phase
// and Round, for Reason.
type Transition struct {
	At      time.Time
	Phase   string
	Round   int
	Reason  string
	Message string
}

// RecordTransition adds next to a Task's history when its phase, round or reason
// differs from the latest one, keeps the newest MaxTransitions, and says whether
// it added one. A message
// change alone is not a step: messages repeat what the reason already says.
func RecordTransition(history []Transition, next Transition) ([]Transition, bool) {
	if next.Phase == "" {
		return history, false
	}
	if n := len(history); n > 0 {
		last := history[n-1]
		if last.Phase == next.Phase && last.Round == next.Round && last.Reason == next.Reason {
			return history, false
		}
	}
	if runes := []rune(next.Message); len(runes) > MaxTransitionMessage {
		next.Message = string(runes[:MaxTransitionMessage-1]) + "…"
	}
	out := make([]Transition, 0, min(len(history)+1, MaxTransitions))
	if len(history) >= MaxTransitions {
		history = history[len(history)-MaxTransitions+1:]
	}
	out = append(out, history...)
	return append(out, next), true
}
