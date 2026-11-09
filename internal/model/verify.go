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
	"fmt"
	"strings"
)

// A Task with a verifier (docs/plans/done/sdlc-loop.md, "Verify") has the change
// its reviewer approved verified before it merges: the verifier builds and
// starts the change at the pull request's head the way the repository says
// (make VerifyTarget, else its README), exercises each acceptance criterion
// against what runs, and reports in the review's verdict format. Its request
// for changes starts the next round with its evidence, as a reviewer's does,
// and its approval merges the change. A criterion no sandbox can meet stays
// open, with why, as the reviewer's do, and goes to a person on merge.

// VerifyTarget is the make target that builds and starts a change for its
// verifier, when the repository's Makefile has one.
const VerifyTarget = "verify"

// ReasonVerifying is the Progressing reason of a Task whose approved change
// goes to its verifier.
const ReasonVerifying = "Verifying"

// Verifies reports whether a Task's approved change goes to its verifier
// before it merges: it has one, and is no spec, plan or triage, nor a
// Standard change, whose gated merge needs the approval of a review run that
// still lasts.
func Verifies(f TaskFacts) bool {
	return f.Verify && !f.Spec && !f.Plan && !f.Triage && len(f.AllowedPaths) == 0
}

// VerifyChange is the progress of a Task whose reviewer approved its change:
// the change is verified in the same round, and the verifier's runs without
// a verdict are counted afresh.
func VerifyChange(p TaskProgress) TaskProgress {
	p.Phase = TaskVerifying
	p.ReviewAttempts = 0
	return p
}

// verifyingStep decides what follows in the Verifying phase: the verifier's
// run, then its verdict. An approval merges the change (or first takes in a
// main that moved, as a review's does); a request for changes starts the
// next round with its evidence.
func verifyingStep(f TaskFacts) TaskStep {
	switch {
	case !f.Run.Started:
		return TaskStep{Action: ActionStartVerify, Message: "Starting the verification"}
	case f.Run.Suspended:
		return suspendedStep(f)
	case !f.Run.Done:
		return workingStep(f, "The verifier is running the change")
	case pausedRun(f.Run):
		return pausedRunStep(f)
	case AfterInterruption(f.Run, f.Resumes, f.Reruns) != InterruptionNone:
		return interruptedStep(f)
	case f.Run.Result != nil && f.Run.Result.Unavailable:
		return agentUnavailable(f)
	}
	if f.Run.Result == nil || f.Run.Result.Error != "" || f.Run.Result.Verdict == "" {
		return noVerificationStep(f)
	}
	if f.Run.Result.Verdict == VerdictApprove {
		return approvedStep(f, "Verified; merging")
	}
	return roundFailed(f, VerifierChanges(f.Run.Result.Summary))
}

// noVerificationStep decides what follows a verifier's run that ended without
// a verdict: another run, up to maxReviewAttempts, as for a reviewer's. A
// broken verifier is not the implementer's fault, so it costs no round.
func noVerificationStep(f TaskFacts) TaskStep {
	reason := f.Run.Failure
	if f.Run.Result != nil {
		reason = f.Run.Result.Error
	}
	if f.ReviewAttempts+1 < maxReviewAttempts {
		return TaskStep{Action: ActionRetryVerify, Message: "The verifier gave no verdict: " + orUnknown(reason)}
	}
	return TaskStep{Action: ActionFail,
		Message: fmt.Sprintf("The verifier failed %d times: %s", maxReviewAttempts, orUnknown(reason))}
}

// VerifierChanges is the next round's feedback from a verifier that asked for
// changes: its report, each failing criterion with the command it ran and
// what that printed.
func VerifierChanges(summary string) string {
	return "The verifier built and ran the change and asked for changes; each criterion it found unmet comes " +
		"with the command it ran and what that printed:\n" + orUnknown(strings.TrimSpace(summary))
}

// OpenCriteria are the criteria a merge leaves to a person: the reviewer's
// open ones, then the verifier's it doesn't name already, compared without
// case or spacing.
func OpenCriteria(review, verify []OpenCriterion) []OpenCriterion {
	out := make([]OpenCriterion, 0, len(review)+len(verify))
	seen := map[string]bool{}
	for _, o := range append(append([]OpenCriterion{}, review...), verify...) {
		key := strings.ToLower(strings.Join(strings.Fields(o.Criterion), " "))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, o)
	}
	return out
}

// StandInVerifyFails in an issue's text has the stand-in verifier find its
// round 1 change failing, so the verifier's next round can run without a model.
const StandInVerifyFails = "stand-in: verify fails"

// StandInVerify is the stand-in verifier's verdict and report on round of an
// issue's change, from standIn, what the change's STAND-IN.md holds (the
// stand-in implementer's file), so the whole loop can run without a model: a
// missing or empty file, or StandInVerifyFails in the issue in round 1, asks
// for changes; anything else approves. Its report gives the evidence as a
// verifier's must: the command and what it printed.
func StandInVerify(issue string, round int, standIn string) (verdict, summary string) {
	printed := strings.Join(strings.Fields(standIn), " ")
	evidence := fmt.Sprintf("`cat STAND-IN.md` printed %q", clip(printed, 200))
	switch {
	case printed == "":
		return VerdictChanges, "Stand-in verification: the change does what the issue asks: not met; " +
			"`cat STAND-IN.md` printed nothing."
	case round == 1 && strings.Contains(strings.ToLower(issue), StandInVerifyFails):
		return VerdictChanges, "Stand-in verification: the change does what the issue asks: not met in round 1, " +
			"as the issue says; " + evidence + "."
	}
	return VerdictApprove, "Stand-in verification: the change does what the issue asks: met; " + evidence + "."
}
