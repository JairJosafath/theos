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
	"reflect"
	"strings"
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
	tests := []struct {
		name, app, want, err string
	}{
		{"joins with a dash", "webshop/orders-api", "webshop-orders-api", ""},
		{"a-b/c joins to a-b-c (#1669)", "a-b/c", "a-b-c", ""},
		{"a/b-c joins to a-b-c too (#1669)", "a/b-c", "a-b-c", ""},
		{"63 characters fit (#1669)", "webshop/" + strings.Repeat("a", 55), "webshop-" + strings.Repeat("a", 55), ""},
		{"64 characters are refused (#1669)", "webshop/" + strings.Repeat("a", 56), "",
			"the Application name webshop-" + strings.Repeat("a", 56) + " is 64 characters, over the 63 a label value holds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ArgoCDApplicationName(tt.app)
			if got != tt.want {
				t.Errorf("ArgoCDApplicationName(%q) = %q, want %q", tt.app, got, tt.want)
			}
			if (err == nil) != (tt.err == "") || (err != nil && !strings.Contains(err.Error(), tt.err)) {
				t.Errorf("ArgoCDApplicationName(%q) error = %v, want %q", tt.app, err, tt.err)
			}
		})
	}
}

func TestArgoCDApplicationConflict(t *testing.T) {
	tests := []struct {
		name, app, namespace, container, err string
	}{
		{"its own labels", "a-b/c", "a-b", "c", ""},
		{"no labels", "a-b/c", "", "", ""},
		{"another Container's labels name it (#1669)", "a-b/c", "a", "b-c",
			"belongs to Container a/b-c, whose name joins to the same"},
		{"the other way round (#1669)", "a/b-c", "a-b", "c", "belongs to Container a-b/c"},
		{"one label is still another's", "a-b/c", "", "c", "belongs to Container /c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ArgoCDApplicationConflict(tt.app, tt.namespace, tt.container)
			if (err == nil) != (tt.err == "") || (err != nil && !strings.Contains(err.Error(), tt.err)) {
				t.Errorf("ArgoCDApplicationConflict(%q, %q, %q) = %v, want %q", tt.app, tt.namespace, tt.container, err, tt.err)
			}
		})
	}
}
