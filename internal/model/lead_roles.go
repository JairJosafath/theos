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
	"regexp"
	"strconv"
	"strings"
)

// The triager, the spec writer and the verifier in the lead runtime (#337;
// docs/plans/lead-runtime.md, step 13). Their decisions are the operator's
// (NextTriage, PlanTriage, the spec's and the verification's steps of
// NextTaskStep, PlanSpecMerged); what is the lead runtime's alone is how
// it reads them back from the Git host.

// The kinds of Task a name says, besides an issue's build.
const (
	TaskNameSpec   = "spec"
	TaskNameTriage = "triage"
)

// taskName is a Task name of an issue's Spec or Triage Task:
// <container>-<issue>-spec or -triage, with the attempt from the second on.
var taskName = regexp.MustCompile(`^(\d+)-(spec|triage)(?:-(\d+))?$`)

// TaskNameKind is what the name of a Task on container says it is:
// TaskNameSpec for a Spec Task (SpecTaskName), TaskNameTriage for a Triage
// Task (TriageTaskName), "" for any other.
func TaskNameKind(container, name string) string {
	rest, ok := strings.CutPrefix(name, container+"-")
	if !ok {
		return ""
	}
	m := taskName.FindStringSubmatch(rest)
	if m == nil {
		return ""
	}
	if n, err := strconv.Atoi(m[1]); err != nil || n < 1 {
		return ""
	}
	return m[2]
}

// HostTriageTask is a Triage Task as the lead runtime reads it from its
// issue's progress, phase its last one: Final once Merged or Failed. The
// lead reads the Triage Tasks of the issues labelled status/proposed, so
// one that decided was sent back by a person who labelled the issue
// status/proposed again: it decided nothing, as a park a person answered
// from chat decides nothing on a cluster (Answered).
func HostTriageTask(name string, issue int, phase string) TriageTask {
	final := phase == TaskMerged || phase == TaskFailed
	failed := phase == TaskFailed
	return TriageTask{Name: name, Issue: issue, Final: final, Failed: failed, Answered: final && !failed}
}

// Ours reports whether user is one of the accounts theos and its sessions
// post as by r: a claimant, or an engine account. Their review of a pull
// request is no person's (PersonReviews): where they share the person's own
// account, as by default, the person's approval is their merge.
func (r ClaimRules) Ours(user string) bool {
	return r.counts(user) || loginIn(r.Engine, user)
}

// Findings are what a round's approving review and verification leave for
// the merge to file: the reviewer's follow-ups, each a proposed issue, and
// the criteria the reviewer and then the verifier accepted as open, which a
// person does (ByHandIssue).
type Findings struct {
	FollowUps  []FollowUp      `json:",omitempty"`
	Open       []OpenCriterion `json:",omitempty"`
	VerifyOpen []OpenCriterion `json:",omitempty"`
}

// KeepFindings is f with what an approving run of role left for the merge:
// a review's follow-ups and open criteria, a verification's open criteria.
// Any other result, or a run that didn't approve, leaves f as it is.
func KeepFindings(f *Findings, role string, res *RunResult) *Findings {
	if res == nil || res.Verdict != VerdictApprove {
		return f
	}
	out := Findings{}
	if f != nil {
		out = *f
	}
	switch role {
	case RoleReviewer:
		out.FollowUps, out.Open = res.FollowUps, res.Open
	case RoleVerifier:
		out.VerifyOpen = res.Open
	default:
		return f
	}
	if len(out.FollowUps) == 0 && len(out.Open) == 0 && len(out.VerifyOpen) == 0 {
		return nil
	}
	return &out
}
