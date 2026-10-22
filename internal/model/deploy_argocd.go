package model

import "strings"

// DeployMemberArgoCD ships each release as one Argo CD Application that
// points at the Container's repository at the release tag and sets the image;
// Argo CD rolls it out (docs/plans/k8s-deploy.md). The member on
// DeployTargetSpec and its row in deployMembers come with #1588.
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

// ArgoCDApplicationName is the name of app's Application in the target's
// Argo CD namespace: the Container's namespace and name, joined with a dash,
// since Applications of every namespace share that one namespace.
func ArgoCDApplicationName(app string) string {
	return strings.Replace(app, "/", "-", 1)
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
	// no sync operation ran yet.
	OperationPhase, OperationMessage string
}

// ArgoCDDeployState is what the argoCD member reports for f. One version
// runs at a time and the Application says which, so the wanted version
// plays no part: no Application is the zero state; a sync operation Failed
// or Error is Failed at the wanted tag, reason SyncFailed, with Argo CD's
// message; Synced at the tag with the operation Succeeded is Deployed; all
// else (an operation Running, no sync yet, OutOfSync, Synced at another
// revision) is Deploying. Health is Argo CD's where the port has it
// (Healthy, Progressing, Degraded); Suspended, Missing and Unknown are
// Unknown, with Argo CD's message unless the operation's says more.
func ArgoCDDeployState(f ArgoCDAppFacts) DeployRun {
	if !f.Exists {
		return DeployRun{}
	}
	run := DeployRun{Version: f.TargetRevision, Phase: DeployPhaseDeploying}
	switch f.OperationPhase {
	case ArgoCDOperationFailed, ArgoCDOperationError:
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
