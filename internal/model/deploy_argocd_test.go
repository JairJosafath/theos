package model

import (
	"reflect"
	"testing"
)

// TestArgoCDDeployState covers docs/plans/k8s-deploy.md's decision 3 (#1586):
// the Application's sync, operation and health decide, the wanted version
// plays no part.
func TestArgoCDDeployState(t *testing.T) {
	// app's operation is the wanted tag's, v1.1.0; opAt moves it to another.
	app := func(sync, syncedRev, health, healthMsg, op, opMsg string) ArgoCDAppFacts {
		return ArgoCDAppFacts{Exists: true, TargetRevision: "v1.1.0", SyncStatus: sync, SyncedRevision: syncedRev,
			HealthStatus: health, HealthMessage: healthMsg, OperationPhase: op, OperationMessage: opMsg,
			OperationRevision: "v1.1.0"}
	}
	opAt := func(rev string, f ArgoCDAppFacts) ArgoCDAppFacts {
		f.OperationRevision = rev
		return f
	}
	tests := []struct {
		name string
		f    ArgoCDAppFacts
		want DeployRun
	}{
		{"no Application is the zero state", ArgoCDAppFacts{}, DeployRun{}},
		{"no sync yet is Deploying at the wanted tag",
			app("", "", "", "", "", ""),
			DeployRun{Version: "v1.1.0", Phase: DeployPhaseDeploying, Health: DeployHealthUnknown}},
		{"OutOfSync is Deploying",
			app("OutOfSync", "v1.1.0", "Progressing", "", "", ""),
			DeployRun{Version: "v1.1.0", Phase: DeployPhaseDeploying, Health: DeployHealthProgressing}},
		{"an operation Running is Deploying",
			app("OutOfSync", "v1.1.0", "Progressing", "", "Running", "syncing"),
			DeployRun{Version: "v1.1.0", Phase: DeployPhaseDeploying, Health: DeployHealthProgressing}},
		{"Synced at the previous tag with the old operation Succeeded is Deploying",
			app("Synced", "v1.0.0", "Healthy", "", "Succeeded", "successfully synced"),
			DeployRun{Version: "v1.1.0", Phase: DeployPhaseDeploying, Health: DeployHealthHealthy}},
		{"OutOfSync at the tag with the old operation Succeeded is Deploying",
			app("OutOfSync", "v1.1.0", "Healthy", "", "Succeeded", "successfully synced"),
			DeployRun{Version: "v1.1.0", Phase: DeployPhaseDeploying, Health: DeployHealthHealthy}},
		{"Synced at the tag with the operation Succeeded is Deployed",
			app("Synced", "v1.1.0", "Healthy", "", "Succeeded", "successfully synced (all tasks run)"),
			DeployRun{Version: "v1.1.0", Phase: DeployPhaseDeployed, Health: DeployHealthHealthy}},
		{"Deployed and Degraded passes Degraded through",
			app("Synced", "v1.1.0", "Degraded", "", "Succeeded", ""),
			DeployRun{Version: "v1.1.0", Phase: DeployPhaseDeployed, Health: DeployHealthDegraded}},
		{"an operation Failed is Failed with SyncFailed and the message",
			app("OutOfSync", "v1.1.0", "Progressing", "", "Failed",
				"one or more objects failed to apply"),
			DeployRun{Version: "v1.1.0", Phase: DeployPhaseFailed, Health: DeployHealthProgressing,
				Reason: DeployReasonSyncFailed, Message: "one or more objects failed to apply"}},
		{"an operation Error is Failed with SyncFailed and the message",
			app("Unknown", "v1.1.0", "Missing", "", "Error", "rpc error: repository not found"),
			DeployRun{Version: "v1.1.0", Phase: DeployPhaseFailed, Health: DeployHealthUnknown,
				Reason: DeployReasonSyncFailed, Message: "rpc error: repository not found"}},
		{"an operation Failed without a recorded revision counts as the wanted tag's",
			opAt("", app("OutOfSync", "v1.1.0", "Degraded", "", "Failed", "one or more objects failed to apply")),
			DeployRun{Version: "v1.1.0", Phase: DeployPhaseFailed, Health: DeployHealthDegraded,
				Reason: DeployReasonSyncFailed, Message: "one or more objects failed to apply"}},
		{"the previous version's operation Failed is Deploying at the new tag (#1667)",
			opAt("v1.0.0", app("OutOfSync", "v1.0.0", "Degraded", "", "Failed", "one or more objects failed to apply")),
			DeployRun{Version: "v1.1.0", Phase: DeployPhaseDeploying, Health: DeployHealthDegraded}},
		{"the previous version's operation Error is Deploying at the new tag (#1667)",
			opAt("v1.0.0", app("OutOfSync", "v1.1.0", "Missing", "", "Error", "rpc error: repository not found")),
			DeployRun{Version: "v1.1.0", Phase: DeployPhaseDeploying, Health: DeployHealthUnknown}},
		{"Suspended is Unknown with the message",
			app("Synced", "v1.1.0", "Suspended", "deployment is paused", "Succeeded", ""),
			DeployRun{Version: "v1.1.0", Phase: DeployPhaseDeployed, Health: DeployHealthUnknown,
				Message: "deployment is paused"}},
		{"Missing is Unknown with the message",
			app("OutOfSync", "v1.1.0", "Missing", "", "", ""),
			DeployRun{Version: "v1.1.0", Phase: DeployPhaseDeploying, Health: DeployHealthUnknown}},
		{"Unknown is Unknown with the message",
			app("Synced", "v1.1.0", "Unknown", "health check failed", "Succeeded", ""),
			DeployRun{Version: "v1.1.0", Phase: DeployPhaseDeployed, Health: DeployHealthUnknown,
				Message: "health check failed"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ArgoCDDeployState(tt.f); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ArgoCDDeployState() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestArgoCDApplicationName(t *testing.T) {
	if got := ArgoCDApplicationName("webshop/orders-api"); got != "webshop-orders-api" {
		t.Errorf("ArgoCDApplicationName() = %q, want webshop-orders-api", got)
	}
}
