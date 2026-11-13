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
	"strings"
	"testing"
	"time"
)

func TestRecordTransition(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	waiting := Transition{At: at(0), Phase: TaskPending, Reason: "Waiting", Message: "Waiting for a free agent slot"}
	running := Transition{At: at(5), Phase: TaskImplementing, Round: 1, Reason: "Running", Message: "Starting round 1"}

	tests := []struct {
		name    string
		history []Transition
		next    Transition
		// want is the phases and reasons of the result, oldest first.
		want []string
	}{
		{name: "the first transition starts the history", next: waiting, want: []string{"Pending/Waiting"}},
		{name: "a new phase is recorded", history: []Transition{waiting}, next: running,
			want: []string{"Pending/Waiting", "Implementing/Running"}},
		{name: "the same phase, round and reason is not recorded again", history: []Transition{waiting},
			next: Transition{At: at(1), Phase: TaskPending, Reason: "Waiting", Message: "Waiting for Task add-crop"},
			want: []string{"Pending/Waiting"}},
		{name: "a new reason in the same phase is recorded", history: []Transition{running},
			next: Transition{At: at(6), Phase: TaskImplementing, Round: 1, Reason: "Waiting"},
			want: []string{"Implementing/Running", "Implementing/Waiting"}},
		{name: "a new round in the same phase is recorded", history: []Transition{running},
			next: Transition{At: at(9), Phase: TaskImplementing, Round: 2, Reason: "Running"},
			want: []string{"Implementing/Running", "Implementing/Running"}},
		{name: "an empty phase is not recorded", history: []Transition{waiting}, next: Transition{At: at(1)},
			want: []string{"Pending/Waiting"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, added := RecordTransition(tt.history, tt.next)
			if added != (len(got) > len(tt.history)) {
				t.Errorf("added = %v with %d transitions before and %d after", added, len(tt.history), len(got))
			}
			var keys []string
			for _, tr := range got {
				keys = append(keys, tr.Phase+"/"+tr.Reason)
			}
			if strings.Join(keys, ",") != strings.Join(tt.want, ",") {
				t.Errorf("got %v, want %v", keys, tt.want)
			}
		})
	}
}

func TestRecordTransitionKeepsTheNewest(t *testing.T) {
	var history []Transition
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	for i := range MaxTransitions + 10 {
		history, _ = RecordTransition(history, Transition{At: t0.Add(time.Duration(i) * time.Minute),
			Phase: TaskImplementing, Round: i + 1, Reason: "Running"})
	}
	if len(history) != MaxTransitions {
		t.Fatalf("kept %d transitions, want %d", len(history), MaxTransitions)
	}
	if history[0].Round != 11 || history[len(history)-1].Round != MaxTransitions+10 {
		t.Errorf("kept rounds %d to %d, want the newest", history[0].Round, history[len(history)-1].Round)
	}
}

func TestRecordTransitionShortensTheMessage(t *testing.T) {
	long := strings.Repeat("é", MaxTransitionMessage+50)
	got, _ := RecordTransition(nil, Transition{Phase: TaskFailed, Reason: "RoundsExhausted", Message: long})
	if n := len([]rune(got[0].Message)); n != MaxTransitionMessage {
		t.Errorf("message has %d runes, want %d", n, MaxTransitionMessage)
	}
	if !strings.HasSuffix(got[0].Message, "…") {
		t.Errorf("a shortened message ends with an ellipsis, got %q", got[0].Message[len(got[0].Message)-8:])
	}
}

func TestRecordTransitionDoesNotChangeItsInput(t *testing.T) {
	history := make([]Transition, 1, 4)
	history[0] = Transition{Phase: TaskPending, Reason: "Waiting"}
	_, _ = RecordTransition(history, Transition{Phase: TaskImplementing, Reason: "Running"})
	if spare := history[:2]; spare[1].Phase != "" {
		t.Error("RecordTransition wrote into its input's spare capacity")
	}
}

func TestRecordTransitionAtTheCapSaysItAdded(t *testing.T) {
	history := make([]Transition, MaxTransitions)
	for i := range history {
		history[i] = Transition{Phase: TaskImplementing, Round: i + 1, Reason: "Running"}
	}
	got, added := RecordTransition(history, Transition{Phase: TaskMerged, Round: MaxTransitions, Reason: "Merged"})
	if !added || len(got) != MaxTransitions || got[len(got)-1].Phase != TaskMerged {
		t.Errorf("added = %v, %d transitions ending in %s; want the merge added in place of the oldest",
			added, len(got), got[len(got)-1].Phase)
	}
}
