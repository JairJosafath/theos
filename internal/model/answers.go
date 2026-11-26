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
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// The parsers of an agent's answer, one for each runtime (#337, as #311
// asks for an outside triager): the operator's runner reads them from the
// agent's output, and the lead runtime from the text of a session's done
// marker (RunFromMarkers). Both take the last JSON object that answers, so
// an agent may think aloud before it.

// ParseTriage reads the triager's decision from the last JSON object in its
// output that has one, as the operator keeps it (NormalTriage).
func ParseTriage(output string) (Triage, error) {
	for end := strings.LastIndex(output, "}"); end >= 0; end = strings.LastIndex(output[:end], "}") {
		for start := strings.LastIndex(output[:end], "{"); start >= 0; start = strings.LastIndex(output[:start], "{") {
			var d Triage
			if json.Unmarshal([]byte(output[start:end+1]), &d) != nil || strings.TrimSpace(d.Decision) == "" {
				continue
			}
			d = NormalTriage(d)
			if !slices.Contains(TriageDecisions, d.Decision) {
				return Triage{}, fmt.Errorf("unknown decision %q", d.Decision)
			}
			return d, nil
		}
	}
	return Triage{}, errors.New("the triager gave no JSON decision")
}

// Verdict is a reviewer's or a verifier's answer.
type Verdict struct {
	Verdict   string
	Summary   string
	FollowUps []FollowUp
	// Open are the criteria the reviewer accepted as left open; only an
	// approval carries them.
	Open []OpenCriterion
	// Risk is why the reviewer calls the change it approves high-risk; empty
	// when it doesn't. Only an approval carries it.
	Risk string
}

// noRiskReason stands for the reason of a reviewer that called a change
// high-risk and gave none.
const noRiskReason = "it gave no reason"

// ParseVerdict finds who's JSON verdict, the reviewer's or the verifier's,
// which reports in the same format: the last JSON object in the output that
// has a verdict. It keeps the titled follow-ups, up to MaxFollowUps, and on
// an approval the named open criteria, trimmed, up to MaxFollowUpCriteria,
// and the reason of a "risk" that is "high".
func ParseVerdict(output, who string) (Verdict, error) {
	for end := strings.LastIndex(output, "}"); end >= 0; end = strings.LastIndex(output[:end], "}") {
		for start := strings.LastIndex(output[:end], "{"); start >= 0; start = strings.LastIndex(output[:start], "{") {
			var v struct {
				Verdict    string          `json:"verdict"`
				Summary    string          `json:"summary"`
				FollowUps  []FollowUp      `json:"followUps"`
				Open       []OpenCriterion `json:"open"`
				Risk       string          `json:"risk"`
				RiskReason string          `json:"riskReason"`
			}
			if json.Unmarshal([]byte(output[start:end+1]), &v) != nil || v.Verdict == "" {
				continue
			}
			verdict := strings.ToLower(strings.TrimSpace(v.Verdict))
			if !slices.Contains([]string{VerdictApprove, VerdictChanges}, verdict) {
				return Verdict{}, fmt.Errorf("unknown verdict %q", v.Verdict)
			}
			out := Verdict{Verdict: verdict, Summary: strings.TrimSpace(v.Summary)}
			for _, f := range v.FollowUps {
				f.Title, f.Body = strings.TrimSpace(f.Title), strings.TrimSpace(f.Body)
				f.Criteria = followUpCriteria(f.Criteria)
				if f.Title != "" && len(out.FollowUps) < MaxFollowUps {
					out.FollowUps = append(out.FollowUps, f)
				}
			}
			for _, o := range v.Open {
				o.Criterion, o.Why = strings.TrimSpace(o.Criterion), strings.TrimSpace(o.Why)
				if verdict == VerdictApprove && o.Criterion != "" && len(out.Open) < MaxFollowUpCriteria {
					out.Open = append(out.Open, o)
				}
			}
			if verdict == VerdictApprove && strings.EqualFold(strings.TrimSpace(v.Risk), RiskHigh) {
				if out.Risk = strings.Join(strings.Fields(v.RiskReason), " "); out.Risk == "" {
					out.Risk = noRiskReason
				}
			}
			return out, nil
		}
	}
	return Verdict{}, fmt.Errorf("the %s gave no JSON verdict", who)
}

// followUpCriteria keeps a follow-up's non-empty criteria, trimmed, up to
// MaxFollowUpCriteria.
func followUpCriteria(in []string) []string {
	var out []string
	for _, c := range in {
		if c = strings.TrimSpace(c); c != "" && len(out) < MaxFollowUpCriteria {
			out = append(out, c)
		}
	}
	return out
}
