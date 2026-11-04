package model

import (
	"fmt"
	"strings"
	"time"
)

// The monitor files a bug when a deploy goes wrong (docs/plans/sdlc-loop.md,
// "Monitor"): the entry's health turns Degraded, or its deploy fails, for the
// version shipped. One issue per target, version and state, so the same state
// of the same version files once.

// Monitored states of a deploy entry.
const (
	DeployMonitorDegraded = "Degraded"
	DeployMonitorFailed   = "Failed"
)

// DeployMonitorFiled is the issue the monitor last filed for an entry, as its
// status records it.
type DeployMonitorFiled struct {
	Version, State string
	Number         int
}

// DeployMonitorState returns the version and the state of a deploy entry the
// monitor files a bug for, or "" when it files nothing: a failed deploy is
// Failed for the version it tried to ship, a deployed entry whose health is
// Degraded is Degraded for the version that runs. Healthy, Progressing and
// Unknown file nothing, nor does an entry that is only waiting or deploying.
// A failure to reach the Git host is not the version's, and the bug could not
// be filed there anyway.
func DeployMonitorState(r DeployReport) (version, state string) {
	switch {
	case r.Phase == DeployPhaseFailed:
		if r.Reason == DeployGitHostUnreachable {
			return "", ""
		}
		state, version = DeployMonitorFailed, r.WantedVersion
		if version == "" {
			version = r.Version
		}
	case r.Phase == DeployPhaseDeployed && r.Health == DeployHealthDegraded:
		state, version = DeployMonitorDegraded, r.Version
		if version == "" {
			version = r.WantedVersion
		}
	}
	if version == "" {
		return "", ""
	}
	return version, state
}

// DeployMonitorDue says whether the entry needs a bug filed, and for which
// version and state. filed is what status records of the last bug filed for
// the entry, kept through a recovery: the same state of the same version
// never files twice, a new version or another state does.
func DeployMonitorDue(r DeployReport, filed *DeployMonitorFiled) (version, state string, due bool) {
	version, state = DeployMonitorState(r)
	if state == "" {
		return "", "", false
	}
	if filed != nil && filed.Version == version && filed.State == state {
		return version, state, false
	}
	return version, state, true
}

// DeployMonitorMarker is the line that ties a monitor issue to its target,
// version and state.
func DeployMonitorMarker(target, version, state string) string {
	return "<!-- theos-monitor: " + target + " " + version + " " + state + " -->"
}

// HasDeployMonitorMarker reports whether body carries the marker on a line of
// its own, as DeployMonitorIssue writes it.
func HasDeployMonitorMarker(body, target, version, state string) bool {
	marker := DeployMonitorMarker(target, version, state)
	for line := range strings.SplitSeq(body, "\n") {
		if strings.TrimSpace(line) == marker {
			return true
		}
	}
	return false
}

// DeployMonitorIssue renders the bug for a deploy of version to r's target in
// state: the entry's message, when the operator saw it, and the marker.
func DeployMonitorIssue(r DeployReport, version, state string, at time.Time) (title, body string) {
	title = fmt.Sprintf("Deploy %s to %s is %s", version, r.Target, state)
	message := r.Message
	if message == "" {
		message = "The target's tool reports " + strings.ToLower(state) + "."
	}
	var s strings.Builder
	fmt.Fprintf(&s, "Version %s is %s at the deploy target %s.\n\n", version, strings.ToLower(state), r.Target)
	fmt.Fprintf(&s, "%s\n\n", strings.ReplaceAll(message, "<!--", "&lt;!--"))
	fmt.Fprintf(&s, "Seen by the operator at %s.", at.UTC().Format(time.RFC3339))
	if r.Reason != "" {
		fmt.Fprintf(&s, " Reason: %s.", r.Reason)
	}
	if r.URL != "" {
		fmt.Fprintf(&s, " It runs at %s.", r.URL)
	}
	s.WriteString("\n\n")
	return title, s.String() + DeployMonitorMarker(r.Target, version, state) + "\n"
}
