package model

import "fmt"

// StandardUpdateFacts is everything the next step of a Container's Standard
// update depends on: the update's state from status, and what the operator read
// from the Git host so far in this pass. The reads start out unknown; a step
// that needs one asks for it.
type StandardUpdateFacts struct {
	// Name is the Standard, Ref the ref its files are at, and Target the
	// Standard's template ref.
	Name, Ref, Target string
	// Mode is when CI runs the full check as the files were last rendered, and
	// TargetMode the Container's effective checks (EffectiveChecks). Empty
	// reads as ChecksEveryChange.
	Mode, TargetMode string
	// Update is the update in flight, or nil.
	Update *StandardUpdateInFlight
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

// StandardUpdateInFlight is an update's state in status.
type StandardUpdateInFlight struct {
	// Ref is the ref the update brings the files to, and Mode the checks; empty
	// reads as ChecksEveryChange.
	Ref, Mode string
	// Stopped is the reason the update stopped, or "" while it runs.
	Stopped string
	// PullRequest is the pull request's number, 0 when the update stopped before
	// opening one.
	PullRequest    int
	PullRequestURL string
	Branch         string
	// Commit is the update's commit, the head the operator pushed.
	Commit string
	// Paths are the paths the update planned to change.
	Paths []string
	// Take says the pull request takes the Standard's version at Ref of the
	// files updates left out, as a person answered, instead of updating to Ref.
	Take bool
	// Held says the update stopped before opening a pull request for something
	// the repository may fix itself, such as a missing light check: each pass
	// plans it again.
	Held bool
}

// UpdatePullRequest is what the Git host says about an update's pull request.
type UpdatePullRequest struct {
	Merged, Closed bool
	// Mergeable is false when main moved with conflicting changes.
	Mergeable bool
	HeadSHA   string
}

// UpdateFiles are the paths an update's pull request changes. Unchecked says
// why not every changed path could be read, or is "".
type UpdateFiles struct {
	Changed   []string
	Unchecked string
}

// StandardAction is what the operator does next for a Standard update.
type StandardAction string

// Standard update actions.
const (
	// StandardNone leaves a stopped update as it is: it waits for the Standard's
	// ref or the Container's checks to change, and the condition keeps saying
	// why it stopped.
	StandardNone StandardAction = "None"
	// StandardReadPR reads the update's pull request into PR.
	StandardReadPR StandardAction = "ReadPR"
	// StandardReadChecks reads the CI state on the update's commit into Checks.
	StandardReadChecks StandardAction = "ReadChecks"
	// StandardReadFiles reads the pull request's changed paths into Files.
	StandardReadFiles StandardAction = "ReadFiles"
	// StandardUpToDate reports the Standard's files at its ref and checks.
	StandardUpToDate StandardAction = "UpToDate"
	// StandardPlan renders the Standard at Ref and Mode and at Target and
	// TargetMode, and opens the pull request that brings the files from one to
	// the other.
	StandardPlan StandardAction = "Plan"
	// StandardForget forgets a stopped update the Standard or the Container's
	// checks moved past, or a held one, so it is planned again.
	StandardForget StandardAction = "Forget"
	// StandardRecordMerge records the merged pull request: the files are at the
	// update's ref and checks.
	StandardRecordMerge StandardAction = "RecordMerge"
	// StandardDrop closes the pull request, deletes its branch and forgets the
	// update, so the Standard is planned again.
	StandardDrop StandardAction = "Drop"
	// StandardStopClosed stops an update a person closed without merging and
	// deletes its branch.
	StandardStopClosed StandardAction = "StopClosed"
	// StandardTakeClosed forgets a take a person closed without merging and
	// deletes its branch: the files stay behind, waiting on a person.
	StandardTakeClosed StandardAction = "TakeClosed"
	// StandardStopPushed stops an update someone else pushed to.
	StandardStopPushed StandardAction = "StopPushed"
	// StandardRefuseMerge stops an update whose pull request may not merge.
	StandardRefuseMerge StandardAction = "RefuseMerge"
	// StandardWaitForChecks waits for CI on the update's commit.
	StandardWaitForChecks StandardAction = "WaitForChecks"
	// StandardChecksFailed reports failed CI; the update merges once a re-run passes.
	StandardChecksFailed StandardAction = "ChecksFailed"
	// StandardForceMerge merges the pull request with the admin override.
	StandardForceMerge StandardAction = "ForceMerge"
)

// StandardStep is the next step of a Standard update.
type StandardStep struct {
	Action StandardAction
	// Message is the condition's message for a step that stops or reports the
	// update, and the log line for StandardDrop.
	Message string
	// Why is the reason a pull request may not merge, for StandardRefuseMerge.
	Why string
	// Quiet means a Git error on this read is only logged: the update stopped,
	// and the condition keeps saying why.
	Quiet bool
}

// NextStandardStep decides the next step of a Standard update. The operator
// takes it, adds what it read or changed to the facts, and asks again until a
// step ends the pass: every step but the reads, RecordMerge, Drop, Forget and
// TakeClosed does.
func NextStandardStep(f StandardUpdateFacts) StandardStep {
	u := f.Update
	target := StandardVersion{Ref: f.Target, Checks: f.TargetMode}.Normal()
	switch {
	case u == nil && (StandardVersion{Ref: f.Ref, Checks: f.Mode}).Normal() == target:
		return StandardStep{Action: StandardUpToDate}
	case u == nil:
		return StandardStep{Action: StandardPlan}
	case (StandardVersion{Ref: u.Ref, Checks: u.Mode}).Normal() != target && u.Stopped != "":
		return StandardStep{Action: StandardForget}
	case (StandardVersion{Ref: u.Ref, Checks: u.Mode}).Normal() != target:
		// The Standard or the checks moved on: end the update and plan towards
		// the new target.
		switch {
		case f.PR == nil:
			return StandardStep{Action: StandardReadPR}
		case f.PR.Merged:
			return StandardStep{Action: StandardRecordMerge}
		}
		return StandardStep{Action: StandardDrop, Message: "Closed a Standard update its Standard or checks moved past"}
	case u.Stopped != "" && u.Held && u.PullRequest == 0:
		return StandardStep{Action: StandardForget}
	case u.Stopped != "":
		// A person may merge a stopped update after all.
		switch {
		case u.PullRequest == 0:
			return StandardStep{Action: StandardNone}
		case f.PR == nil:
			return StandardStep{Action: StandardReadPR, Quiet: true}
		case f.PR.Merged:
			return StandardStep{Action: StandardRecordMerge}
		case f.PR.Closed && u.Take:
			return StandardStep{Action: StandardTakeClosed}
		}
		return StandardStep{Action: StandardNone}
	}
	return followStep(f)
}

// followStep takes the update in flight one step: record its merge, stop it
// when a person closed it or pushed to it, wait for CI, or merge it.
func followStep(f StandardUpdateFacts) StandardStep {
	u := f.Update
	switch pr := f.PR; {
	case pr == nil:
		return StandardStep{Action: StandardReadPR}
	case pr.Merged:
		return StandardStep{Action: StandardRecordMerge}
	case pr.Closed && u.Take:
		return StandardStep{Action: StandardTakeClosed}
	case pr.Closed:
		return StandardStep{Action: StandardStopClosed, Message: fmt.Sprintf(
			"Pull request %s was closed without merging; %s stays at %s until its ref or the checks change again",
			u.PullRequestURL, f.Name, StandardVersion{Ref: f.Ref, Checks: f.Mode})}
	case !pr.Mergeable:
		// main moved with conflicting changes: plan again from the new main.
		return StandardStep{Action: StandardDrop, Message: "Closed a Standard update that conflicts with main"}
	case pr.HeadSHA != u.Commit:
		return StandardStep{Action: StandardStopPushed, Message: fmt.Sprintf(
			"Someone else pushed to %s; a person looks at %s", u.Branch, u.PullRequestURL)}
	}

	switch f.Checks {
	case "":
		return StandardStep{Action: StandardReadChecks}
	case checkSuccess:
	case checkFailure, checkError:
		return StandardStep{Action: StandardChecksFailed, Message: fmt.Sprintf(
			"CI is %s on %s; the update merges once a re-run passes, or close %s to drop it",
			f.Checks, f.ChecksURL, u.PullRequestURL)}
	default:
		return StandardStep{Action: StandardWaitForChecks}
	}

	switch {
	case f.Files == nil:
		return StandardStep{Action: StandardReadFiles}
	case f.Files.Unchecked != "":
		return refuseMerge(u, "not every changed path can be checked: "+f.Files.Unchecked)
	}
	if why := StandardUpdateRefusal(f.CI, f.Checks, f.Contexts, f.Files.Changed, u.Paths); why != "" {
		return refuseMerge(u, why)
	}
	return StandardStep{Action: StandardForceMerge}
}

func refuseMerge(u *StandardUpdateInFlight, why string) StandardStep {
	return StandardStep{Action: StandardRefuseMerge, Why: why, Message: fmt.Sprintf(
		"The update may not merge: %s; a person looks at %s", why, u.PullRequestURL)}
}
