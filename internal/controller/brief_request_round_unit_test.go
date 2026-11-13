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

package controller

import (
	"slices"
	"testing"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// A round takes the requests let through; one that ends without applying
// hands them back, and they start no round by themselves.
func TestTakeAndReleaseRequests(t *testing.T) {
	status := &c4v1beta1.BriefStatus{Round: 3, Requests: []c4v1beta1.BriefRequest{
		{Repository: "o/r", Number: 1, Stage: model.RequestAccepted},
		{Repository: "o/r", Number: 2, Stage: model.RequestWaiting},
		{Repository: "o/r", Number: 3, Stage: model.RequestPlanned, Round: 2},
		{Repository: "o/r", Number: 4, Stage: model.RequestAccepted},
	}}
	status.Round++
	status.RoundRequests = takeRequests(status)
	if want := []string{"o/r#1", "o/r#4"}; !slices.Equal(status.RoundRequests, want) {
		t.Fatalf("takeRequests = %v, want %v", status.RoundRequests, want)
	}
	for _, i := range []int{0, 3} {
		if r := status.Requests[i]; r.Stage != model.RequestPlanning || r.Round != 4 {
			t.Errorf("request %d = %s in round %d, want Planning in round 4", r.Number, r.Stage, r.Round)
		}
	}
	status.Requests = slices.Delete(status.Requests, 3, 4) // #4 closed on GitHub
	took, open := roundRequests(status)
	if !slices.Equal(took, []string{"o/r#1", "o/r#4"}) || !slices.Equal(open, []string{"o/r#1"}) {
		t.Errorf("roundRequests = %v, %v; want both taken, #1 open", took, open)
	}

	releaseRequests(status)
	if r := status.Requests[0]; r.Stage != model.RequestAccepted || r.Round != 0 {
		t.Errorf("released request = %s in round %d, want Accepted in none", r.Stage, r.Round)
	}
	if r := status.Requests[2]; r.Stage != model.RequestPlanned || r.Round != 2 {
		t.Errorf("an earlier round's request = %s in round %d, want it kept", r.Stage, r.Round)
	}
	status.History = []c4v1beta1.BriefRound{{Round: 4, Outcome: model.ReasonRoundFailed}}
	if n := model.NewRequests(modelRequests(status.Requests), heldRequests(status)); n != 0 {
		t.Errorf("NewRequests = %d after a failed round, want 0: its requests wait for something new", n)
	}
	status.History[0].Outcome = model.ReasonApplied
	if n := model.NewRequests(modelRequests(status.Requests), heldRequests(status)); n != 1 {
		t.Errorf("NewRequests = %d after an applied round, want 1: a request it took that came back, "+
			"reopened, is new", n)
	}
}
