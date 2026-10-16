package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// A change to a Container's spec.operations after the scaffold reaches its
// repository the way a Standard bump does (plans/aws-production.md, step 12):
// the operator renders infra/operations.ts again from the block, and only that
// file, opens a pull request with it, and merges it as admin once CI passes.

// OperationsFile is the file a Framework renders the operations block into, the
// only one an operations update writes.
const OperationsFile = "infra/operations.ts"

// OperationsUpdateFacts is everything the next step of a Container's
// operations update depends on: the block spec.operations resolves to, the one
// the repository was last rendered with, the update from status, and what the
// operator read from the Git host so far in this pass. The reads start out
// unknown; a step that needs one asks for it.
type OperationsUpdateFacts struct {
	// Target is spec.operations resolved, and Recorded the block
	// infra/operations.ts was last rendered with (status.operations).
	Target, Recorded Operations
	// Update is the update in status, or nil. A merged one is no longer in
	// flight.
	Update *OperationsUpdateInFlight
	// PR is the update's pull request, nil until read.
	PR *UpdatePullRequest
	// Checks is the CI state on the update's commit, "" until read; ChecksURL is
	// where to see it and Contexts the state of each check.
	Checks    string
	ChecksURL string
	Contexts  map[string]string
	// CI is the Container's CI system, whose check contexts main requires.
	CI CI
	// Files are the paths the pull request changes, nil until read.
	Files *UpdateFiles
}

// OperationsUpdateInFlight is an operations update's state in status.
type OperationsUpdateInFlight struct {
	// Operations is the block the update renders.
	Operations Operations
	// Stopped is the reason the update stopped, or "" while it runs.
	Stopped string
	// PullRequest is the pull request's number, 0 when the update stopped
	// before opening one.
	PullRequest    int
	PullRequestURL string
	Branch         string
	// Commit is the update's commit, the head the operator pushed.
	Commit string
	// Merged says the update merged: main holds the block.
	Merged bool
}

// OperationsAction is what the operator does next for an operations update.
type OperationsAction string

// Operations update actions.
const (
	// OperationsNone leaves a stopped update as it is: it waits for
	// spec.operations to change, and the condition keeps saying why it stopped.
	OperationsNone OperationsAction = "None"
	// OperationsReadPR reads the update's pull request into PR.
	OperationsReadPR OperationsAction = "ReadPR"
	// OperationsReadChecks reads the CI state on the update's commit into Checks.
	OperationsReadChecks OperationsAction = "ReadChecks"
	// OperationsReadFiles reads the pull request's changed paths into Files.
	OperationsReadFiles OperationsAction = "ReadFiles"
	// OperationsUpToDate reports infra/operations.ts rendered from the block.
	OperationsUpToDate OperationsAction = "UpToDate"
	// OperationsPlan renders infra/operations.ts from Target and opens the pull
	// request that writes it.
	OperationsPlan OperationsAction = "Plan"
	// OperationsForget forgets a stopped update spec.operations moved past, so
	// the block is planned again.
	OperationsForget OperationsAction = "Forget"
	// OperationsRecordMerge records the merged pull request: the repository is
	// rendered with the update's block.
	OperationsRecordMerge OperationsAction = "RecordMerge"
	// OperationsDrop closes the pull request, deletes its branch and forgets the
	// update, so the block is planned again.
	OperationsDrop OperationsAction = "Drop"
	// OperationsStopClosed stops an update a person closed without merging and
	// deletes its branch.
	OperationsStopClosed OperationsAction = "StopClosed"
	// OperationsStopPushed stops an update someone else pushed to.
	OperationsStopPushed OperationsAction = "StopPushed"
	// OperationsRefuseMerge stops an update whose pull request may not merge.
	OperationsRefuseMerge OperationsAction = "RefuseMerge"
	// OperationsWaitForChecks waits for CI on the update's commit.
	OperationsWaitForChecks OperationsAction = "WaitForChecks"
	// OperationsChecksFailed reports failed CI; the update merges once a re-run
	// passes.
	OperationsChecksFailed OperationsAction = "ChecksFailed"
	// OperationsForceMerge merges the pull request with the admin override.
	OperationsForceMerge OperationsAction = "ForceMerge"
)

// OperationsStep is the next step of an operations update.
type OperationsStep struct {
	Action OperationsAction
	// Message is the condition's message for a step that stops or reports the
	// update, and the log line for OperationsDrop.
	Message string
	// Why is the reason a pull request may not merge, for OperationsRefuseMerge.
	Why string
	// Quiet means a Git error on this read is only logged: the update stopped,
	// and the condition keeps saying why.
	Quiet bool
}

// NextOperationsStep decides the next step of an operations update. The
// operator takes it, adds what it read or changed to the facts, and asks again
// until a step ends the pass: every step but the reads, RecordMerge, Drop and
// Forget does.
func NextOperationsStep(f OperationsUpdateFacts) OperationsStep {
	u := f.Update
	switch {
	case (u == nil || u.Merged) && f.Recorded == f.Target:
		return OperationsStep{Action: OperationsUpToDate}
	case u == nil || u.Merged:
		return OperationsStep{Action: OperationsPlan}
	case u.Operations != f.Target && u.Stopped != "":
		return OperationsStep{Action: OperationsForget}
	case u.Operations != f.Target:
		// The block changed again: end the update and plan the new block.
		switch {
		case f.PR == nil:
			return OperationsStep{Action: OperationsReadPR}
		case f.PR.Merged:
			return OperationsStep{Action: OperationsRecordMerge}
		}
		return OperationsStep{Action: OperationsDrop, Message: "Closed an operations update its block moved past"}
	case u.Stopped != "":
		// A person may merge a stopped update after all.
		switch {
		case u.PullRequest == 0:
			return OperationsStep{Action: OperationsNone}
		case f.PR == nil:
			return OperationsStep{Action: OperationsReadPR, Quiet: true}
		case f.PR.Merged:
			return OperationsStep{Action: OperationsRecordMerge}
		}
		return OperationsStep{Action: OperationsNone}
	}
	return followOperations(f)
}

// followOperations takes the update in flight one step: record its merge, stop
// it when a person closed it or pushed to it, wait for CI, or merge it.
func followOperations(f OperationsUpdateFacts) OperationsStep {
	u := f.Update
	switch pr := f.PR; {
	case pr == nil:
		return OperationsStep{Action: OperationsReadPR}
	case pr.Merged:
		return OperationsStep{Action: OperationsRecordMerge}
	case pr.Closed:
		return OperationsStep{Action: OperationsStopClosed, Message: fmt.Sprintf(
			"Pull request %s was closed without merging; %s stays as it is until spec.operations changes again",
			u.PullRequestURL, OperationsFile)}
	case !pr.Mergeable:
		// main moved with conflicting changes: plan again from the new main.
		return OperationsStep{Action: OperationsDrop, Message: "Closed an operations update that conflicts with main"}
	case pr.HeadSHA != u.Commit:
		return OperationsStep{Action: OperationsStopPushed, Message: fmt.Sprintf(
			"Someone else pushed to %s; a person looks at %s", u.Branch, u.PullRequestURL)}
	}

	switch f.Checks {
	case "":
		return OperationsStep{Action: OperationsReadChecks}
	case checkSuccess:
	case checkFailure, checkError:
		return OperationsStep{Action: OperationsChecksFailed, Message: fmt.Sprintf(
			"CI is %s on %s; the update merges once a re-run passes, or close %s to drop it",
			f.Checks, f.ChecksURL, u.PullRequestURL)}
	default:
		return OperationsStep{Action: OperationsWaitForChecks}
	}

	switch {
	case f.Files == nil:
		return OperationsStep{Action: OperationsReadFiles}
	case f.Files.Unchecked != "":
		return refuseOperations(u, "not every changed path can be checked: "+f.Files.Unchecked)
	}
	if why := StandardUpdateRefusal(f.CI, f.Checks, f.Contexts, f.Files.Changed, []string{OperationsFile}); why != "" {
		return refuseOperations(u, why)
	}
	return OperationsStep{Action: OperationsForceMerge}
}

func refuseOperations(u *OperationsUpdateInFlight, why string) OperationsStep {
	return OperationsStep{Action: OperationsRefuseMerge, Why: why, Message: fmt.Sprintf(
		"The update may not merge: %s; a person looks at %s", why, u.PullRequestURL)}
}

// OperationsUpdateBranch is the branch of the update that renders o:
// "operations-update/<the first 12 hex digits of the block's hash>", so each
// block has its own.
func OperationsUpdateBranch(o Operations) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%+v", o)))
	return "operations-update/" + hex.EncodeToString(sum[:6])
}

// OperationsUpdateTitle is the title of an operations update's pull request and
// of its commit.
func OperationsUpdateTitle() string {
	return "Render " + OperationsFile + " from spec.operations"
}

// OperationsSummary says the block o in one line, every field named as
// spec.operations names it.
func OperationsSummary(o Operations) string {
	or := func(s, none string) string {
		if s == "" {
			return none
		}
		return s
	}
	return fmt.Sprintf("backups.retentionDays %d, backups.copyToRegion %s, recovery.rpo %s, recovery.rto %s, "+
		"alerts.email %s", o.Backups.RetentionDays, or(o.Backups.CopyToRegion, "none"), o.Recovery.RPO,
		o.Recovery.RTO, or(o.Alerts.Email, "none"))
}

// OperationsUpdateBody is the body of the pull request that renders
// infra/operations.ts from the block o, which was rendered from from.
func OperationsUpdateBody(from, to Operations) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The operator opened this pull request because the Container's `spec.operations` changed: it "+
		"renders `%s` again from the block, and changes no other file. It merges once CI passes.\n\n", OperationsFile)
	fmt.Fprintf(&b, "- from: %s\n- to: %s\n", OperationsSummary(from), OperationsSummary(to))
	return b.String()
}
