package model

import (
	"strings"
	"testing"
)

// readyFacts are an entry at a ready, compatible github target that needs no
// approval, with v1.1.0 the newest release and v1.0.0 running.
func readyFacts() DeployFacts {
	return DeployFacts{Container: "orders-cli", Target: "staging", Newest: "v1.1.0", TargetFound: true,
		TargetChecked: true, TargetReady: true, Tool: "GitHub", Approval: DeployApprovalNone,
		Run: &DeployRun{Version: "v1.0.0", Phase: DeployPhaseDeployed, Health: DeployHealthHealthy,
			URL: "https://example.com/v1.0.0"}}
}

func TestNextDeploy(t *testing.T) {
	deployed := func(target, version string) *DeployReport {
		return &DeployReport{Target: target, Version: version, WantedVersion: version, Phase: DeployPhaseDeployed,
			Health: DeployHealthHealthy}
	}
	tests := []struct {
		name   string
		change func(*DeployFacts)
		// ship, phase, reason and cause are the step's; message a part of its
		// report's message; version, when given, the version it reports
		// running.
		ship, phase, reason, cause, message, version string
	}{
		{name: "the newest release ships", ship: "v1.1.0", phase: DeployPhaseDeploying, reason: DeployDeploying,
			cause: DeployDeploying, message: "Deploying v1.1.0 to staging", version: "v1.0.0"},
		{name: "a pin ships, older or not: a rollback",
			change: func(f *DeployFacts) { f.Version = "v0.9.0" },
			ship:   "v0.9.0", phase: DeployPhaseDeploying, reason: DeployDeploying, cause: DeployDeploying,
			version: "v1.0.0"},
		{name: "running the wanted version is Deployed",
			change: func(f *DeployFacts) { f.Newest = "v1.0.0" },
			phase:  DeployPhaseDeployed, reason: DeployDeployed, cause: DeployDeployed, message: "staging runs v1.0.0",
			version: "v1.0.0"},
		{name: "no release", change: func(f *DeployFacts) { f.Newest = "" },
			phase: DeployPhaseWaiting, reason: DeployNoRelease, cause: DeployNoRelease,
			message: "No release of orders-cli"},
		{name: "a missing target", change: func(f *DeployFacts) { f.TargetFound = false },
			phase: DeployPhaseWaiting, reason: DeployTargetNotReady, cause: DeployTargetNotReady,
			message: "DeployTarget staging not found"},
		{name: "a target not checked yet", change: func(f *DeployFacts) { f.TargetChecked = false },
			phase: DeployPhaseWaiting, reason: DeployTargetNotReady, cause: DeployTargetNotReady,
			message: "has not been checked yet"},
		{name: "a target not ready says why",
			change: func(f *DeployFacts) { f.TargetReady, f.TargetMessage = false, "Secret x is missing" },
			phase:  DeployPhaseWaiting, reason: DeployTargetNotReady, cause: DeployTargetNotReady,
			message: "DeployTarget staging is not ready: Secret x is missing"},
		// Before readiness: a Container that can't ship there never can.
		{name: "an incompatible Container", change: func(f *DeployFacts) {
			f.Lacks, f.TargetReady = []string{"a Deployment that writes deploy/k8s/"}, false
		}, phase: DeployPhaseWaiting, reason: DeployIncompatible, cause: DeployIncompatible,
			message: "Container orders-cli can't ship to DeployTarget staging: it lacks a Deployment"},
		{name: "a state that can't be read keeps the last phase",
			change: func(f *DeployFacts) {
				f.Run, f.StateFailure = nil, "timeout"
				f.Prev = &DeployReport{Target: "staging", Version: "v1.0.0", WantedVersion: "v1.1.0",
					Phase: DeployPhaseDeploying}
			},
			phase: DeployPhaseDeploying, reason: DeployTargetNotReady, cause: DeployTargetNotReady,
			message: "GitHub could not say what runs at DeployTarget staging: timeout", version: "v1.0.0"},
		{name: "a name the tool can't take is Failed, not a wait, naming the other Container (#1677, #1669)",
			change: func(f *DeployFacts) {
				f.Run, f.Rejected = nil, "Application a-b-c in namespace argocd belongs to Container a/b-c, whose "+
					"name joins to the same: rename one of them"
			},
			phase: DeployPhaseFailed, reason: DeployNameRejected, cause: DeployFailed,
			message: "GitHub can't take orders-cli as named, so v1.1.0 doesn't ship to staging: Application a-b-c in " +
				"namespace argocd belongs to Container a/b-c, whose name joins to the same: rename one of them"},
		{name: "a name no longer rejected ships again on the next poll (#1677)",
			change: func(f *DeployFacts) {
				f.Prev = &DeployReport{Target: "staging", WantedVersion: "v1.1.0", Phase: DeployPhaseFailed,
					Reason: DeployNameRejected}
			},
			phase: DeployPhaseDeploying, reason: DeployDeploying, cause: DeployDeploying, ship: "v1.1.0",
			message: "Deploying v1.1.0 to staging"},
		{name: "waits for the entry it comes after",
			change: func(f *DeployFacts) { f.After, f.Previous = "dev", deployed("dev", "v1.0.0") },
			phase:  DeployPhaseWaiting, reason: DeployWaitingForPrevious, cause: DeployWaitingForPrevious,
			message: "v1.1.0 waits for dev to run it before it ships to staging", version: "v1.0.0"},
		{name: "waits for the entry it comes after to be healthy",
			change: func(f *DeployFacts) {
				f.After, f.Previous = "dev", deployed("dev", "v1.1.0")
				f.Previous.Health = DeployHealthDegraded
			},
			phase: DeployPhaseWaiting, reason: DeployWaitingForPrevious, cause: DeployWaitingForPrevious,
			message: "waits for dev to run it healthy (it is degraded)"},
		{name: "ships once the entry it comes after runs it",
			change: func(f *DeployFacts) { f.After, f.Previous = "dev", deployed("dev", "v1.1.0") },
			ship:   "v1.1.0", phase: DeployPhaseDeploying, reason: DeployDeploying, cause: DeployDeploying},
		{name: "a pin skips the entry it comes after: a rollback of prod alone",
			change: func(f *DeployFacts) {
				f.Version, f.After, f.Previous = "v0.9.0", "dev", deployed("dev", "v1.1.0")
			},
			ship: "v0.9.0", phase: DeployPhaseDeploying, reason: DeployDeploying, cause: DeployDeploying},
		{name: "a pin still waits for approval", change: func(f *DeployFacts) {
			f.Version, f.Approval, f.Approved = "v0.9.0", DeployApprovalRequired, "v1.1.0"
		}, phase: DeployPhaseWaiting, reason: DeployWaitingForApproval, cause: DeployWaitingForApproval},
		{name: "a target that can't be read keeps the last phase",
			change: func(f *DeployFacts) {
				f.TargetUnread, f.Run = "timeout", nil
				f.Prev = &DeployReport{Target: "staging", Version: "v1.0.0", WantedVersion: "v1.1.0",
					Phase: DeployPhaseDeploying}
			},
			phase: DeployPhaseDeploying, reason: DeployTargetNotReady, cause: DeployTargetNotReady,
			message: "DeployTarget staging could not be read: timeout", version: "v1.0.0"},
		{name: "a tool with no deploy runs nothing", change: func(f *DeployFacts) {
			f.Run = &DeployRun{}
			f.Prev = &DeployReport{Target: "staging", Version: "v1.0.0", URL: "https://x", WantedVersion: "v1.1.0",
				Phase: DeployPhaseDeployed}
		}, ship: "v1.1.0", phase: DeployPhaseDeploying, reason: DeployDeploying, cause: DeployDeploying},
		{name: "waits for approval", change: func(f *DeployFacts) { f.Approval = DeployApprovalRequired },
			phase: DeployPhaseWaiting, reason: DeployWaitingForApproval, cause: DeployWaitingForApproval,
			message: "set approved to v1.1.0"},
		{name: "an approval of another version still waits", change: func(f *DeployFacts) {
			f.Approval, f.Approved = DeployApprovalRequired, "v1.0.0"
		}, phase: DeployPhaseWaiting, reason: DeployWaitingForApproval, cause: DeployWaitingForApproval},
		{name: "ships once approved", change: func(f *DeployFacts) {
			f.Approval, f.Approved = DeployApprovalRequired, "v1.1.0"
		}, ship: "v1.1.0", phase: DeployPhaseDeploying, reason: DeployDeploying, cause: DeployDeploying},
		// The previous entry is waited for first: approval is asked once it
		// is the last thing in the way.
		{name: "waits for the previous entry before approval", change: func(f *DeployFacts) {
			f.Approval, f.After, f.Previous = DeployApprovalRequired, "dev", deployed("dev", "v1.0.0")
		}, phase: DeployPhaseWaiting, reason: DeployWaitingForPrevious, cause: DeployWaitingForPrevious},
		{name: "a deploy in flight ships on, the wanted version waits",
			change: func(f *DeployFacts) {
				f.Newest = "v1.2.0"
				f.Run = &DeployRun{Version: "v1.1.0", Phase: DeployPhaseDeploying, Health: DeployHealthProgressing}
				f.Prev = &DeployReport{Target: "staging", Version: "v1.0.0", WantedVersion: "v1.1.0"}
			},
			ship: "v1.1.0", phase: DeployPhaseDeploying, reason: DeployDeploying, cause: DeployDeploying,
			message: "Deploying v1.1.0 to staging; v1.2.0 ships once it ends", version: "v1.0.0"},
		// A deploy in flight was let in when it started.
		{name: "a deploy in flight of the wanted version ships on unapproved",
			change: func(f *DeployFacts) {
				f.Approval = DeployApprovalRequired
				f.Run = &DeployRun{Version: "v1.1.0", Phase: DeployPhaseDeploying}
			},
			ship: "v1.1.0", phase: DeployPhaseDeploying, reason: DeployDeploying, cause: DeployDeploying},
		{name: "a rollout the tool failed isn't shipped again",
			change: func(f *DeployFacts) {
				f.Run = &DeployRun{Version: "v1.1.0", Phase: DeployPhaseFailed, Reason: "StackRolledBack",
					Message: "the stack rolled back"}
			},
			phase: DeployPhaseFailed, reason: "StackRolledBack", cause: DeployFailed,
			message: "GitHub reports deploying v1.1.0 to staging failed: the stack rolled back"},
		{name: "a failed rollout of an older version doesn't hold a newer one",
			change: func(f *DeployFacts) { f.Run = &DeployRun{Version: "v1.0.0", Phase: DeployPhaseFailed} },
			ship:   "v1.1.0", phase: DeployPhaseDeploying, reason: DeployDeploying, cause: DeployDeploying},
		{name: "a missing file fails", change: func(f *DeployFacts) {
			f.Failure = &DeployShipFailure{Reason: DeployArtifactMissing, Detail: "no files"}
		}, phase: DeployPhaseFailed, reason: DeployArtifactMissing, cause: DeployFailed,
			message: "Release v1.1.0 lacks what DeployTarget staging needs: no files"},
		{name: "a missing file isn't shipped again", change: func(f *DeployFacts) {
			f.Prev = &DeployReport{Target: "staging", WantedVersion: "v1.1.0", Phase: DeployPhaseFailed,
				Reason: DeployArtifactMissing, Message: "lacks"}
		}, phase: DeployPhaseFailed, reason: DeployArtifactMissing, cause: DeployFailed, message: "lacks",
			version: "v1.0.0"},
		// A failed write can leave a GitHub draft the tool shows Deploying.
		{name: "a failure held while the tool shows the version Deploying", change: func(f *DeployFacts) {
			f.Run = &DeployRun{Version: "v1.1.0", Phase: DeployPhaseDeploying}
			f.Prev = &DeployReport{Target: "staging", WantedVersion: "v1.1.0", Phase: DeployPhaseFailed,
				Reason: DeployFailed, Message: "422"}
		}, phase: DeployPhaseFailed, reason: DeployFailed, cause: DeployFailed, message: "422"},
		{name: "a newer release ships after a failure", change: func(f *DeployFacts) {
			f.Newest = "v1.2.0"
			f.Prev = &DeployReport{Target: "staging", WantedVersion: "v1.1.0", Phase: DeployPhaseFailed,
				Reason: DeployArtifactMissing}
		}, ship: "v1.2.0", phase: DeployPhaseDeploying, reason: DeployDeploying, cause: DeployDeploying},
		{name: "a refused credential is shipped again", change: func(f *DeployFacts) {
			f.Prev = &DeployReport{Target: "staging", WantedVersion: "v1.1.0", Phase: DeployPhaseFailed,
				Reason: DeployCredentialRefused}
		}, ship: "v1.1.0", phase: DeployPhaseDeploying, reason: DeployDeploying, cause: DeployDeploying},
		{name: "a refused credential fails", change: func(f *DeployFacts) {
			f.Failure = &DeployShipFailure{Reason: DeployCredentialRefused, Detail: "403"}
		}, phase: DeployPhaseFailed, reason: DeployCredentialRefused, cause: DeployFailed,
			message: "GitHub refused the credential of DeployTarget staging shipping v1.1.0: 403"},
		{name: "a missing prerequisite fails (#1608)", change: func(f *DeployFacts) {
			f.Failure = &DeployShipFailure{Reason: DeployPrerequisiteMissing, Detail: "run cdk bootstrap"}
		}, phase: DeployPhaseFailed, reason: DeployPrerequisiteMissing, cause: DeployFailed,
			message: "DeployTarget staging lacks what shipping v1.1.0 needs: run cdk bootstrap; tried again"},
		{name: "a missing prerequisite is shipped again (#1608)", change: func(f *DeployFacts) {
			f.Prev = &DeployReport{Target: "staging", WantedVersion: "v1.1.0", Phase: DeployPhaseFailed,
				Reason: DeployPrerequisiteMissing}
		}, ship: "v1.1.0", phase: DeployPhaseDeploying, reason: DeployDeploying, cause: DeployDeploying},
		{name: "an unreachable target waits", change: func(f *DeployFacts) {
			f.Failure = &DeployShipFailure{Reason: DeployUnreachable, Detail: "502"}
		}, phase: DeployPhaseWaiting, reason: DeployUnreachable, cause: DeployTargetNotReady,
			message: "GitHub could not be reached to ship v1.1.0 to staging: 502"},
		{name: "a release the Git host lacks", change: func(f *DeployFacts) {
			f.Failure = &DeployShipFailure{Reason: DeployReleaseNotFound}
		}, phase: DeployPhaseFailed, reason: DeployReleaseNotFound, cause: DeployFailed,
			message: "The Git host has no release v1.1.0"},
		{name: "a Git host that fails to give the release", change: func(f *DeployFacts) {
			f.Failure = &DeployShipFailure{Reason: DeployGitHostUnreachable, Detail: "EOF"}
		}, phase: DeployPhaseWaiting, reason: DeployGitHostUnreachable, cause: DeployDeploying,
			message: "The Git host failed to give release v1.1.0 to ship to staging: EOF"},
		{name: "any other failure isn't shipped again", change: func(f *DeployFacts) {
			f.Failure = &DeployShipFailure{Reason: DeployFailed, Detail: "tag taken"}
		}, phase: DeployPhaseFailed, reason: DeployFailed, cause: DeployFailed,
			message: "Shipping v1.1.0 to staging failed: tag taken; it isn't tried again"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := readyFacts()
			if tt.change != nil {
				tt.change(&f)
			}
			got := NextDeploy(f)
			if got.Ship != tt.ship || got.Report.Phase != tt.phase || got.Report.Reason != tt.reason ||
				got.Cause != tt.cause {
				t.Errorf("NextDeploy() = ship %q, phase %q, reason %q, cause %q; want %q, %q, %q, %q", got.Ship,
					got.Report.Phase, got.Report.Reason, got.Cause, tt.ship, tt.phase, tt.reason, tt.cause)
			}
			if !strings.Contains(got.Report.Message, tt.message) {
				t.Errorf("message %q lacks %q", got.Report.Message, tt.message)
			}
			if tt.version != "" && got.Report.Version != tt.version {
				t.Errorf("version %q, want %q", got.Report.Version, tt.version)
			}
		})
	}
}

func TestDeployReadsState(t *testing.T) {
	f := readyFacts()
	if !DeployReadsState(f) {
		t.Error("a ready entry with a release reads no state")
	}
	for name, change := range map[string]func(*DeployFacts){
		"no release":     func(f *DeployFacts) { f.Newest = "" },
		"not ready":      func(f *DeployFacts) { f.TargetReady = false },
		"incompatible":   func(f *DeployFacts) { f.Lacks = []string{"x"} },
		"missing target": func(f *DeployFacts) { f.TargetFound = false },
	} {
		f := readyFacts()
		change(&f)
		if DeployReadsState(f) {
			t.Errorf("%s: reads the state", name)
		}
	}
}

func TestDeployedCondition(t *testing.T) {
	step := func(target, cause, message string) DeployStep {
		return DeployStep{Report: DeployReport{Target: target, Version: "v1", Message: message}, Cause: cause}
	}
	tests := []struct {
		name    string
		steps   []DeployStep
		ok      bool
		reason  string
		message string
	}{
		{name: "all deployed", steps: []DeployStep{step("a", DeployDeployed, ""), step("b", DeployDeployed, "")},
			ok: true, reason: DeployDeployed, message: "Each target runs its wanted version: a v1, b v1"},
		// The reason's order, not the list's, picks the first.
		{name: "a failure before a later entry's wait", steps: []DeployStep{
			step("a", DeployNoRelease, "none"), step("b", DeployFailed, "failed"),
			step("c", DeployWaitingForApproval, "approve"),
		}, reason: DeployFailed, message: "failed"},
		{name: "incompatible first", steps: []DeployStep{
			step("a", DeployTargetNotReady, "not ready"), step("b", DeployIncompatible, "lacks"),
		}, reason: DeployIncompatible, message: "lacks"},
		{name: "the list order among the same reason", steps: []DeployStep{
			step("a", DeployDeploying, "first"), step("b", DeployDeploying, "second"),
		}, reason: DeployDeploying, message: "first"},
		{name: "approval before previous", steps: []DeployStep{
			step("a", DeployWaitingForPrevious, "prev"), step("b", DeployWaitingForApproval, "approve"),
		}, reason: DeployWaitingForApproval, message: "approve"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason, message := DeployedCondition(tt.steps)
			if ok != tt.ok || reason != tt.reason || message != tt.message {
				t.Errorf("DeployedCondition() = %v, %q, %q; want %v, %q, %q", ok, reason, message, tt.ok,
					tt.reason, tt.message)
			}
		})
	}
}

func TestDeployEvent(t *testing.T) {
	report := func(phase, reason, version, wanted string) DeployReport {
		return DeployReport{Target: "prod", Phase: phase, Reason: reason, Version: version, WantedVersion: wanted}
	}
	deployed := report(DeployPhaseDeployed, DeployDeployed, "v2", "v2")
	failed := report(DeployPhaseFailed, DeployArtifactMissing, "v1", "v2")
	approval := report(DeployPhaseWaiting, DeployWaitingForApproval, "v1", "v2")
	tests := []struct {
		name       string
		prev       *DeployReport
		next       DeployReport
		reason     string
		wantSaidIn string
	}{
		{name: "turns deployed", prev: &approval, next: deployed, reason: DeployEventDeployed, wantSaidIn: "v2"},
		{name: "a new entry deployed", next: deployed, reason: DeployEventDeployed},
		{name: "stays deployed", prev: &deployed, next: deployed},
		{name: "fails", prev: &approval, next: failed, reason: DeployEventFailed, wantSaidIn: "ArtifactMissing"},
		{name: "fails the same way again", prev: &failed, next: failed},
		{name: "fails another way", prev: &failed, next: report(DeployPhaseFailed, DeployFailed, "v1", "v2"),
			reason: DeployEventFailed},
		{name: "fails for another version", prev: &failed,
			next: report(DeployPhaseFailed, DeployArtifactMissing, "v1", "v3"), reason: DeployEventFailed},
		{name: "needs approval", next: approval, reason: DeployEventApprovalNeeded},
		{name: "still needs approval", prev: &approval, next: approval},
		{name: "needs approval of a newer version", prev: &approval,
			next: report(DeployPhaseWaiting, DeployWaitingForApproval, "v1", "v3"), reason: DeployEventApprovalNeeded},
		{name: "waits otherwise", next: report(DeployPhaseWaiting, DeployNoRelease, "", "")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, said := DeployEvent(tt.prev, tt.next)
			if reason != tt.reason || (reason == "") != (said == "") || !strings.Contains(said, tt.wantSaidIn) {
				t.Errorf("DeployEvent() = %q, %q; want %q", reason, said, tt.reason)
			}
		})
	}
}
