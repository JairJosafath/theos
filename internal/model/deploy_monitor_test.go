package model

import (
	"strings"
	"testing"
	"time"
)

func TestDeployMonitorDue(t *testing.T) {
	deployed := func(health string) DeployReport {
		return DeployReport{Target: "prod", Version: "1.2.0", WantedVersion: "1.2.0", Phase: DeployPhaseDeployed,
			Health: health}
	}
	failed := DeployReport{Target: "prod", WantedVersion: "1.3.0", Phase: DeployPhaseFailed, Reason: DeployFailed}
	filed := func(version, state string) *DeployMonitorFiled {
		return &DeployMonitorFiled{Version: version, State: state, Number: 4}
	}
	for _, tc := range []struct {
		name    string
		report  DeployReport
		filed   *DeployMonitorFiled
		version string
		state   string
		due     bool
	}{
		{"degraded", deployed(DeployHealthDegraded), nil, "1.2.0", DeployMonitorDegraded, true},
		{"deploy failed", failed, nil, "1.3.0", DeployMonitorFailed, true},
		{"healthy", deployed(DeployHealthHealthy), nil, "", "", false},
		{"progressing", deployed(DeployHealthProgressing), nil, "", "", false},
		{"unknown", deployed(DeployHealthUnknown), nil, "", "", false},
		{"no health yet", deployed(""), nil, "", "", false},
		{"still deploying", DeployReport{Target: "prod", WantedVersion: "1.3.0", Phase: DeployPhaseDeploying,
			Health: DeployHealthDegraded}, nil, "", "", false},
		{"waiting", DeployReport{Target: "prod", WantedVersion: "1.3.0", Phase: DeployPhaseWaiting}, nil, "", "", false},
		{"git host unreachable is not the version's", DeployReport{Target: "prod", WantedVersion: "1.3.0",
			Phase: DeployPhaseFailed, Reason: DeployGitHostUnreachable}, nil, "", "", false},
		{"same state of the same version", deployed(DeployHealthDegraded),
			filed("1.2.0", DeployMonitorDegraded), "1.2.0", DeployMonitorDegraded, false},
		{"healthy after a filed bug files nothing", deployed(DeployHealthHealthy),
			filed("1.2.0", DeployMonitorDegraded), "", "", false},
		{"new version", deployed(DeployHealthDegraded),
			filed("1.1.0", DeployMonitorDegraded), "1.2.0", DeployMonitorDegraded, true},
		{"another state of the same version", failed,
			filed("1.3.0", DeployMonitorDegraded), "1.3.0", DeployMonitorFailed, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version, state, due := DeployMonitorDue(tc.report, tc.filed)
			if due != tc.due || version != tc.version || state != tc.state {
				t.Fatalf("got (%q, %q, %v), want (%q, %q, %v)", version, state, due, tc.version, tc.state, tc.due)
			}
		})
	}
}

func TestDeployMonitorIssue(t *testing.T) {
	r := DeployReport{Target: "prod", Message: "3 of 3 pods crash-loop <!-- x -->", Reason: "Degraded"}
	at := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	title, body := DeployMonitorIssue(r, "1.2.0", DeployMonitorDegraded, at)
	if title != "Deploy 1.2.0 to prod is Degraded" {
		t.Fatalf("title = %q", title)
	}
	for _, want := range []string{"3 of 3 pods crash-loop", "2026-11-04T10:00:00Z",
		"<!-- theos-monitor: prod 1.2.0 Degraded -->"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if !HasDeployMonitorMarker(body, "prod", "1.2.0", DeployMonitorDegraded) {
		t.Error("marker not found on its own line")
	}
	if HasDeployMonitorMarker(body, "prod", "1.2.0", DeployMonitorFailed) ||
		HasDeployMonitorMarker(body, "prod", "1.2.1", DeployMonitorDegraded) {
		t.Error("marker matched another state or version")
	}
	if strings.Count(body, "<!--") != 1 {
		t.Errorf("the message's comment opener was not escaped:\n%s", body)
	}
}
