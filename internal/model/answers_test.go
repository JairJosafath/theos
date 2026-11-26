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
)

// #337 (#311): the triager's parser is the model's, the one both the
// operator's runner and the lead runtime's done marker read with.
func TestParseTriage337(t *testing.T) {
	d, err := ParseTriage("Thinking {not json}\n" + `{"decision": "Park", "reason": "Unclear.", "question": "Which?"}` + "\n")
	if err != nil || d.Decision != TriagePark || d.Question != "Which?" {
		t.Errorf("ParseTriage() = %+v, %v", d, err)
	}
	if _, err := ParseTriage(`{"verdict": "approve"}`); err == nil {
		t.Error("ParseTriage() of a review verdict: no error")
	}
	if _, err := ParseTriage(`{"decision": "later", "reason": "x"}`); err == nil ||
		!strings.Contains(err.Error(), `unknown decision "later"`) {
		t.Errorf("ParseTriage() of an unknown decision: %v", err)
	}
	d, err = ParseTriage(`{"decision": "ready", "reason": "Clear.", "criteria": ["- [ ] Rejects an empty order", ""]}`)
	if err != nil || len(d.Criteria) != 1 || d.Criteria[0] != "Rejects an empty order" {
		t.Errorf("ParseTriage() keeps %+v, %v; want the criterion normalised", d, err)
	}
}

// #337: the reviewer's and the verifier's parser is the model's too.
func TestParseVerdict337(t *testing.T) {
	v, err := ParseVerdict(`{"verdict": "Approve", "summary": " Fine. ", "open": [{"criterion": " On a phone ", "why": "no phone"}],`+
		` "followUps": [{"title": "Later", "criteria": [" a ", "", "b", "c", "d"]}, {"title": ""}]}`, "verifier")
	if err != nil || v.Verdict != VerdictApprove || v.Summary != "Fine." || len(v.Open) != 1 ||
		v.Open[0].Criterion != "On a phone" || len(v.FollowUps) != 1 || len(v.FollowUps[0].Criteria) != MaxFollowUpCriteria {
		t.Errorf("ParseVerdict() = %+v, %v", v, err)
	}
	if _, err := ParseVerdict("no verdict", "verifier"); err == nil || err.Error() != "the verifier gave no JSON verdict" {
		t.Errorf("ParseVerdict() of none: %v", err)
	}
	if v, err := ParseVerdict(`{"verdict": "changes", "open": [{"criterion": "x"}]}`, "reviewer"); err != nil ||
		len(v.Open) != 0 {
		t.Errorf("a request for changes keeps open criteria: %+v, %v", v, err)
	}
}
