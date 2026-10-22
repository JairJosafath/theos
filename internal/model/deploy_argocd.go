package model

import (
	"fmt"
	"strings"
)

// DeployMemberArgoCD ships each release as one Argo CD Application that
// points at the Container's repository at the release tag and sets the image;
// Argo CD rolls it out (docs/plans/k8s-deploy.md); the member on
// DeployTargetSpec and its row in deployMembers are #1588's.
const DeployMemberArgoCD = "argoCD"

// Settings of an argoCD target, as its adapter reads them.
const (
	// ArgoCDSettingNamespace is where Argo CD runs and its Applications live.
	ArgoCDSettingNamespace = "namespace"
	// ArgoCDSettingProject is the Argo CD project the Application belongs to.
	ArgoCDSettingProject = "project"
	// ArgoCDSettingDestinationNamespace is where the app runs.
	ArgoCDSettingDestinationNamespace = "destinationNamespace"
	// ArgoCDSettingDestinationServer is the cluster the app runs on, as Argo
	// CD names it.
	ArgoCDSettingDestinationServer = "destinationServer"
)

// Defaults of an argoCD target's settings, the CRD's defaults too.
const (
	ArgoCDDefaultNamespace = "argocd"
	ArgoCDDefaultProject   = "default"
	ArgoCDDefaultServer    = "https://kubernetes.default.svc"
)

// ArgoCDManifestsPath is the path in the repository the Application syncs,
// where the k8s-manifests Deployment writes.
const ArgoCDManifestsPath = "deploy/k8s"

// DeployReasonSyncFailed is the reason of a deploy whose Argo CD sync
// operation failed; the message is Argo CD's.
const DeployReasonSyncFailed = "SyncFailed"

// ArgoCDApplicationNameMax is the longest name an Application may have:
// under label tracking Argo CD writes the name into the
// app.kubernetes.io/instance label of every resource it manages, and a label
// value is 63 characters at most (#1669).
const ArgoCDApplicationNameMax = 63

// ArgoCDApplicationName is the name of app's Application in the target's
// Argo CD namespace: the Container's namespace and name, joined with a dash,
// since Applications of every namespace share that one namespace. An error
// when the joined name is over ArgoCDApplicationNameMax, though each part
// fits on its own.
func ArgoCDApplicationName(app string) (string, error) {
	name := strings.Replace(app, "/", "-", 1)
	if len(name) > ArgoCDApplicationNameMax {
		return "", fmt.Errorf("the Application name %s is %d characters, over the %d a label value holds: "+
			"shorten the Container's namespace or name", name, len(name), ArgoCDApplicationNameMax)
	}
	return name, nil
}

// ArgoCDApplicationConflict says whether app may ship to, or read, the
// Application whose c4.factory.io/namespace and c4.factory.io/container
// labels are namespace and container: nil when they name app, or name
// nothing; else the error names the Container they do name. Two Containers'
// names can join to the same (a-b/c and a/b-c), and the second must not
// repoint the first's Application or report its deploy (#1669).
func ArgoCDApplicationConflict(app, namespace, container string) error {
	if namespace == "" && container == "" {
		return nil
	}
	if owner := namespace + "/" + container; owner != app {
		return fmt.Errorf("belongs to Container %s, whose name joins to the same: rename one of them", owner)
	}
	return nil
}

// Argo CD's sync statuses, health statuses and operation phases, as the
// Application's status spells them.
const (
	ArgoCDSyncSynced    = "Synced"
	ArgoCDSyncOutOfSync = "OutOfSync"

	ArgoCDHealthHealthy     = "Healthy"
	ArgoCDHealthProgressing = "Progressing"
	ArgoCDHealthDegraded    = "Degraded"
	ArgoCDHealthSuspended   = "Suspended"
	ArgoCDHealthMissing     = "Missing"
	ArgoCDHealthUnknown     = "Unknown"

	ArgoCDOperationRunning   = "Running"
	ArgoCDOperationSucceeded = "Succeeded"
	ArgoCDOperationFailed    = "Failed"
	ArgoCDOperationError     = "Error"
)

// ArgoCDAppFacts are what the argoCD member read of an app's Application
// (docs/plans/k8s-deploy.md, decision 3).
type ArgoCDAppFacts struct {
	// Exists says the Application is there; the other fields are its when it is.
	Exists bool
	// TargetRevision is spec.source.targetRevision, the tag the Application
	// wants: the version Ship last applied.
	TargetRevision string
	// SyncStatus is status.sync.status (Synced, OutOfSync, Unknown), "" before
	// Argo CD compared anything; SyncedRevision is the revision it compared
	// to, status.sync.comparedTo.source.targetRevision.
	SyncStatus, SyncedRevision string
	// HealthStatus and HealthMessage are status.health's.
	HealthStatus, HealthMessage string
	// OperationPhase and OperationMessage are status.operationState's, "" when
	// no sync operation ran yet. OperationRevision is the tag that operation
	// synced, status.operationState.syncResult.source.targetRevision (or
	// operation.sync.source.targetRevision, when the result isn't there yet);
	// "" when Argo CD recorded none.
	OperationPhase, OperationMessage, OperationRevision string
}

// ArgoCDDeployState is what the argoCD member reports for f. One version
// runs at a time and the Application says which, so the wanted version
// plays no part: no Application is the zero state; a sync operation Failed
// or Error at the wanted tag is Failed at it, reason SyncFailed, with Argo
// CD's message; Synced at the tag with the operation Succeeded is Deployed;
// all else (an operation Running, no sync yet, OutOfSync, Synced at another
// revision, or a Failed or Error operation of another revision, the previous
// version's that Argo CD hasn't replaced yet, #1667) is Deploying. An
// operation whose revision Argo CD didn't record counts as the wanted tag's.
// Health is Argo CD's where the port has it (Healthy, Progressing,
// Degraded); Suspended, Missing and Unknown are Unknown, with Argo CD's
// message unless the operation's says more.
func ArgoCDDeployState(f ArgoCDAppFacts) DeployRun {
	if !f.Exists {
		return DeployRun{}
	}
	run := DeployRun{Version: f.TargetRevision, Phase: DeployPhaseDeploying}
	switch f.OperationPhase {
	case ArgoCDOperationFailed, ArgoCDOperationError:
		if f.OperationRevision != "" && f.OperationRevision != f.TargetRevision {
			break // the previous version's operation; the new one hasn't started
		}
		run.Phase = DeployPhaseFailed
		run.Reason = DeployReasonSyncFailed
		run.Message = f.OperationMessage
	case ArgoCDOperationSucceeded:
		if f.SyncStatus == ArgoCDSyncSynced && f.SyncedRevision == f.TargetRevision {
			run.Phase = DeployPhaseDeployed
		}
	}
	switch f.HealthStatus {
	case ArgoCDHealthHealthy:
		run.Health = DeployHealthHealthy
	case ArgoCDHealthProgressing:
		run.Health = DeployHealthProgressing
	case ArgoCDHealthDegraded:
		run.Health = DeployHealthDegraded
	default:
		run.Health = DeployHealthUnknown
		if run.Message == "" {
			run.Message = f.HealthMessage
		}
	}
	return run
}
