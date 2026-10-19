package model

import (
	"fmt"
	"strings"
)

// Phases of a Container's deploy to one target, as status.deploys names them.
const (
	DeployPhaseWaiting   = "Waiting"
	DeployPhaseDeploying = "Deploying"
	DeployPhaseDeployed  = "Deployed"
	DeployPhaseFailed    = "Failed"
)

// Health of what runs at a target, as its tool reports it.
const (
	DeployHealthHealthy     = "Healthy"
	DeployHealthProgressing = "Progressing"
	DeployHealthDegraded    = "Degraded"
	DeployHealthUnknown     = "Unknown"
)

// Reasons of a Container's Deployed condition, in the order the first that
// holds of any entry is the condition's.
const (
	DeployIncompatible       = "Incompatible"
	DeployTargetNotReady     = "TargetNotReady"
	DeployFailed             = "DeployFailed"
	DeployWaitingForApproval = "WaitingForApproval"
	DeployWaitingForPrevious = "WaitingForPrevious"
	DeployDeploying          = "Deploying"
	DeployNoRelease          = "NoRelease"
	// DeployDeployed: the entry runs its wanted version.
	DeployDeployed = "Deployed"
)

// deployOrder is the order of the Deployed condition's reasons.
var deployOrder = []string{DeployIncompatible, DeployTargetNotReady, DeployFailed, DeployWaitingForApproval,
	DeployWaitingForPrevious, DeployDeploying, DeployNoRelease}

// Reasons of one entry besides its condition reason: why a deploy failed or
// a call didn't reach the target.
const (
	// DeployUnreachable: the target's tool could not be reached; asked again
	// on the next poll.
	DeployUnreachable = "Unreachable"
	// DeployCredentialRefused: the tool refused the target's credential when
	// shipping; shipped again on the next poll.
	DeployCredentialRefused = "CredentialRefused" //nolint:gosec // a reason, not a credential
	// DeployArtifactMissing: the release lacks a file the target needs; not
	// shipped again until another version is wanted.
	DeployArtifactMissing = "ArtifactMissing"
	// DeployReleaseNotFound: the Git host has no release of the wanted
	// version; looked for again on the next poll.
	DeployReleaseNotFound = "ReleaseNotFound"
	// DeployGitHostUnreachable: the Git host failed to give the release;
	// tried again on the next poll.
	DeployGitHostUnreachable = "GitHostUnreachable"
)

// DeployRun is a deploy as the target's tool reports it.
type DeployRun struct {
	// Version is the newest deploy's version, "" when none.
	Version string
	// Phase is DeployPhaseDeploying, Deployed or Failed, "" with no deploy.
	Phase  string
	Health string
	URL    string
	// Reason and Message say why a deploy failed.
	Reason, Message string
}

// DeployReport is one entry of a Container's status.deploys.
type DeployReport struct {
	Target, Version, WantedVersion, Phase, Health, URL, Reason, Message string
}

// DeployShipFailure is how shipping a version failed.
type DeployShipFailure struct {
	// Reason is DeployUnreachable, DeployCredentialRefused,
	// DeployArtifactMissing, DeployReleaseNotFound, DeployGitHostUnreachable,
	// or DeployFailed for any other failure.
	Reason string
	// Detail is what the tool or the Git host said.
	Detail string
}

// DeployFacts are what the operator read for one entry of a Container's
// spec.deploy.
type DeployFacts struct {
	// Container is the Container's name.
	Container string
	// Target, After, Version and Approved are the entry's.
	Target, After, Version, Approved string
	// Newest is the newest release of the Container whose runs succeeded,
	// "" when none.
	Newest string
	// TargetUnread says why the DeployTarget could not be read, "" when it
	// was or isn't there.
	TargetUnread string
	// TargetFound says the DeployTarget exists; TargetReady and TargetMessage
	// are its Ready condition, TargetChecked says it has one.
	TargetFound, TargetChecked, TargetReady bool
	TargetMessage                           string
	// Tool names who owns the place, such as GitHub.
	Tool string
	// Approval is DeployApprovalRequired or DeployApprovalNone, as
	// DeployApproval works it out.
	Approval string
	// Lacks are what the Container lacks of what the target's member needs.
	Lacks []string
	// Run is what the target reports, when it was read; StateFailure says
	// why it couldn't be.
	Run          *DeployRun
	StateFailure string
	// Prev is the entry's last report, nil for a new entry.
	Prev *DeployReport
	// Previous is this pass's report of the entry After names, nil when After
	// is empty.
	Previous *DeployReport
	// Failure is how shipping failed in this pass.
	Failure *DeployShipFailure
}

// DeployStep is what to do for an entry: ship Ship when it isn't "", and
// report Report, whose condition reason is Cause.
type DeployStep struct {
	Ship   string
	Report DeployReport
	Cause  string
}

// DeployWanted is the version an entry should run: its pin, else the newest
// release.
func DeployWanted(pinned, newest string) string {
	if pinned != "" {
		return pinned
	}
	return newest
}

// DeployReadsState says whether the operator asks the target what runs there
// for f: the target is there, ready and compatible, and some version is
// wanted.
func DeployReadsState(f DeployFacts) bool {
	return f.TargetFound && f.TargetChecked && f.TargetReady && len(f.Lacks) == 0 &&
		DeployWanted(f.Version, f.Newest) != ""
}

// NextDeploy decides one entry of a Container's spec.deploy: what it reports
// and whether a version ships now. A version ships once the target is ready
// and compatible, the entry After names runs it (unless the entry pins it),
// and a person approved it where the target needs approval; one deploy runs at a time per entry; a
// rollout the tool reports failed, a missing file or an unknown failure is
// not shipped again until another version is wanted, while a call the tool
// never had (unreachable, a refused credential, a release not yet on the Git
// host) is tried again on the next poll.
func NextDeploy(f DeployFacts) DeployStep {
	wanted := DeployWanted(f.Version, f.Newest)
	r := DeployReport{Target: f.Target, WantedVersion: wanted, Phase: DeployPhaseWaiting}
	if p := f.Prev; p != nil {
		r.Version, r.Health, r.URL = p.Version, p.Health, p.URL
	}
	if step, held := deployHeldBefore(f, r, wanted); held {
		return step
	}
	wait := func(cause, message string) DeployStep {
		r.Reason, r.Message = cause, message
		return DeployStep{Report: r, Cause: cause}
	}

	run := *f.Run
	switch run.Phase {
	case DeployPhaseDeployed:
		r.Version, r.URL = run.Version, run.URL
	case "":
		// The tool has no deploy of the app: nothing runs there.
		r.Version, r.URL = "", ""
	}
	if run.Health != "" {
		r.Health = run.Health
	}
	if run.Version == wanted {
		switch run.Phase {
		case DeployPhaseDeployed:
			r.Phase, r.Reason, r.Message = DeployPhaseDeployed, DeployDeployed,
				fmt.Sprintf("%s runs %s", f.Target, wanted)
			return DeployStep{Report: r, Cause: DeployDeployed}
		case DeployPhaseFailed:
			reason := run.Reason
			if reason == "" {
				reason = DeployFailed
			}
			detail := ""
			if run.Message != "" {
				detail = ": " + run.Message
			}
			r.Phase, r.Reason, r.Message = DeployPhaseFailed, reason, fmt.Sprintf(
				"%s reports deploying %s to %s failed%s; it isn't tried again until another version is wanted",
				f.Tool, wanted, f.Target, detail)
			return DeployStep{Report: r, Cause: DeployFailed}
		}
	}
	if run.Phase == DeployPhaseDeploying && run.Version != wanted {
		// One deploy at a time: the one in flight goes on, the wanted one waits.
		return deployShip(f, r, run.Version, fmt.Sprintf("Deploying %s to %s; %s ships once it ends",
			run.Version, f.Target, wanted))
	}
	// Held even while the tool shows the version Deploying: a GitHub draft a
	// failed write left behind is not gone on with by itself.
	if p := f.Prev; p != nil && p.Phase == DeployPhaseFailed && p.WantedVersion == wanted &&
		!deployRetried(p.Reason) {
		r.Phase, r.Reason, r.Message = p.Phase, p.Reason, p.Message
		return DeployStep{Report: r, Cause: DeployFailed}
	}
	if run.Phase != DeployPhaseDeploying {
		// An entry's own pin is a person's choice, as a rollback is: it
		// skips after, and still needs approval where the target does.
		if prev := f.Previous; prev != nil && f.Version == "" && !deployRuns(*prev, wanted) {
			return wait(DeployWaitingForPrevious, fmt.Sprintf("%s waits for %s to run it%s before it ships to %s",
				wanted, f.After, healthNote(*prev, wanted), f.Target))
		}
		if f.Approval == DeployApprovalRequired && f.Approved != wanted {
			return wait(DeployWaitingForApproval, fmt.Sprintf(
				"%s waits for a person's approval to ship to %s: set approved to %s in its spec.deploy entry",
				wanted, f.Target, wanted))
		}
	}
	return deployShip(f, r, wanted, fmt.Sprintf("Deploying %s to %s", wanted, f.Target))
}

// deployHeldBefore holds an entry before what runs at its target matters:
// the target isn't there, ready or compatible, no version is wanted, or what
// runs there couldn't be read. r is the entry's report so far.
func deployHeldBefore(f DeployFacts, r DeployReport, wanted string) (DeployStep, bool) {
	wait := func(cause, message string) (DeployStep, bool) {
		r.Reason, r.Message = cause, message
		return DeployStep{Report: r, Cause: cause}, true
	}
	switch {
	case f.TargetUnread != "":
		if p := f.Prev; p != nil && p.WantedVersion == wanted {
			r.Phase = p.Phase
		}
		return wait(DeployTargetNotReady, fmt.Sprintf("DeployTarget %s could not be read: %s; read again on "+
			"the next poll", f.Target, f.TargetUnread))
	case !f.TargetFound:
		return wait(DeployTargetNotReady, fmt.Sprintf("DeployTarget %s not found", f.Target))
	case len(f.Lacks) > 0:
		return wait(DeployIncompatible, fmt.Sprintf("Container %s can't ship to DeployTarget %s: it lacks %s",
			f.Container, f.Target, strings.Join(f.Lacks, " and ")))
	case !f.TargetChecked:
		return wait(DeployTargetNotReady, fmt.Sprintf("DeployTarget %s has not been checked yet", f.Target))
	case !f.TargetReady:
		return wait(DeployTargetNotReady, fmt.Sprintf("DeployTarget %s is not ready: %s", f.Target,
			f.TargetMessage))
	case wanted == "":
		return wait(DeployNoRelease, fmt.Sprintf(
			"No release of %s has succeeded its runs yet; nothing ships to %s", f.Container, f.Target))
	case f.Run == nil:
		if p := f.Prev; p != nil && p.WantedVersion == wanted {
			r.Phase = p.Phase
		}
		return wait(DeployTargetNotReady, fmt.Sprintf("%s could not say what runs at DeployTarget %s: %s; "+
			"asked again on the next poll", f.Tool, f.Target, f.StateFailure))
	}
	return DeployStep{}, false
}

// deployShip ships version, or reports how shipping it failed.
func deployShip(f DeployFacts, r DeployReport, version, message string) DeployStep {
	if fail := f.Failure; fail != nil {
		switch fail.Reason {
		case DeployUnreachable:
			r.Reason, r.Message = DeployUnreachable, fmt.Sprintf("%s could not be reached to ship %s to %s: %s; "+
				"tried again on the next poll", f.Tool, version, f.Target, fail.Detail)
			if f.Run != nil && f.Run.Phase == DeployPhaseDeploying {
				r.Phase = DeployPhaseDeploying
			}
			return DeployStep{Report: r, Cause: DeployTargetNotReady}
		case DeployGitHostUnreachable:
			// The factory's side, not the target's: still on its way.
			r.Reason, r.Message = DeployGitHostUnreachable, fmt.Sprintf("The Git host failed to give release %s "+
				"to ship to %s: %s; tried again on the next poll", version, f.Target, fail.Detail)
			if f.Run != nil && f.Run.Phase == DeployPhaseDeploying {
				r.Phase = DeployPhaseDeploying
			}
			return DeployStep{Report: r, Cause: DeployDeploying}
		case DeployCredentialRefused:
			r.Message = fmt.Sprintf("%s refused the credential of DeployTarget %s shipping %s: %s; tried again "+
				"on the next poll", f.Tool, f.Target, version, fail.Detail)
		case DeployArtifactMissing:
			r.Message = fmt.Sprintf("Release %s lacks what DeployTarget %s needs: %s; a newer release or a "+
				"pinned version ships instead", version, f.Target, fail.Detail)
		case DeployReleaseNotFound:
			r.Message = fmt.Sprintf("The Git host has no release %s to ship to %s; looked for again on the "+
				"next poll", version, f.Target)
		default:
			r.Message = fmt.Sprintf("Shipping %s to %s failed: %s; it isn't tried again until another version "+
				"is wanted", version, f.Target, fail.Detail)
		}
		r.Phase, r.Reason = DeployPhaseFailed, fail.Reason
		return DeployStep{Report: r, Cause: DeployFailed}
	}
	r.Phase, r.Reason, r.Message = DeployPhaseDeploying, DeployDeploying, message
	return DeployStep{Ship: version, Report: r, Cause: DeployDeploying}
}

// deployRetried says a Failed entry with reason is shipped again on the next
// poll: the tool never had the deploy.
func deployRetried(reason string) bool {
	return reason == DeployCredentialRefused || reason == DeployReleaseNotFound
}

// deployRuns says the entry r reports runs version, healthy where its target
// reads health.
func deployRuns(r DeployReport, version string) bool {
	return r.Phase == DeployPhaseDeployed && r.Version == version &&
		(r.Health == "" || r.Health == DeployHealthHealthy || r.Health == DeployHealthUnknown)
}

// healthNote says what the entry r lacks besides running version.
func healthNote(r DeployReport, version string) string {
	if r.Phase == DeployPhaseDeployed && r.Version == version {
		return " healthy (it is " + strings.ToLower(r.Health) + ")"
	}
	return ""
}

// DeployedCondition sums a Container's deploy steps, in spec.deploy's order:
// True when each runs its wanted version, else False with the first reason in
// deployOrder that any step has, and that step's message.
func DeployedCondition(steps []DeployStep) (ok bool, reason, message string) {
	for _, cause := range deployOrder {
		for _, s := range steps {
			if s.Cause == cause {
				return false, cause, s.Report.Message
			}
		}
	}
	runs := make([]string, 0, len(steps))
	for _, s := range steps {
		runs = append(runs, s.Report.Target+" "+s.Report.Version)
	}
	return true, DeployDeployed, "Each target runs its wanted version: " + strings.Join(runs, ", ")
}

// Events of a Container's deploys.
const (
	// DeployEventDeployed is a Normal Event when an entry turns Deployed.
	DeployEventDeployed = "Deployed"
	// DeployEventFailed is a Warning Event once per version and reason.
	DeployEventFailed = "DeployFailed"
	// DeployEventApprovalNeeded is a Normal Event once per version.
	DeployEventApprovalNeeded = "ApprovalNeeded"
)

// DeployEvent is the Event an entry's new report next says against its last
// one prev: its reason, and the key that says it once ("" with no Event).
func DeployEvent(prev *DeployReport, next DeployReport) (reason, said string) {
	switch {
	case next.Phase == DeployPhaseDeployed:
		if prev != nil && prev.Phase == DeployPhaseDeployed && prev.Version == next.Version {
			return "", ""
		}
		return DeployEventDeployed, DeployEventDeployed + "\n" + next.Version
	case next.Phase == DeployPhaseFailed:
		if prev != nil && prev.Phase == DeployPhaseFailed && prev.WantedVersion == next.WantedVersion &&
			prev.Reason == next.Reason {
			return "", ""
		}
		return DeployEventFailed, DeployEventFailed + "\n" + next.WantedVersion + "\n" + next.Reason
	case next.Reason == DeployWaitingForApproval:
		if prev != nil && prev.Reason == DeployWaitingForApproval && prev.WantedVersion == next.WantedVersion {
			return "", ""
		}
		return DeployEventApprovalNeeded, DeployEventApprovalNeeded + "\n" + next.WantedVersion
	}
	return "", ""
}
