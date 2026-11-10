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

package controller

import (
	"context"
	"errors"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// Task condition types.
const (
	// ConditionProgressing says what the Task is doing or waiting for.
	ConditionProgressing = "Progressing"
	// ConditionSucceeded is True once merged and False once failed.
	ConditionSucceeded = "Succeeded"
)

// Polling intervals for what the cluster doesn't tell us about.
const (
	// defaultChecksRecheck: CI runs on the Git host, which doesn't call back.
	defaultChecksRecheck = 15 * time.Second
	// defaultGitTimeout: how long the operator waits on a Git host at a time.
	defaultGitTimeout = 30 * time.Second
	// defaultTaskSecretRecheck: Secrets are read uncached, so not watched.
	defaultTaskSecretRecheck = 15 * time.Second
	// slotRecheck: a Task waiting for a free agent slot looks again.
	slotRecheck = 20 * time.Second
	// mergeCommitRetry: a Merged Task whose merge commit couldn't be read tries
	// again, less often once it merged long ago (mergeCommitSlowRetry).
	mergeCommitRetry     = time.Minute
	mergeCommitSlowRetry = time.Hour
)

// mergeCommitRetryAfter is when a Merged Task without its merge commit reads it
// again: each minute for the first hour after it completed, then each hour, so a
// pull request that is gone doesn't cost a Git request a minute forever.
func mergeCommitRetryAfter(completed *metav1.Time, now time.Time) time.Duration {
	if completed != nil && now.Sub(completed.Time) > mergeCommitSlowRetry {
		return mergeCommitSlowRetry
	}
	return mergeCommitRetry
}

// TaskReconciler drives a Task through its rounds: an agent implements in a
// sandbox, CI checks the pull request, an agent reviews, and the operator merges.
type TaskReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// GitEvents is the receiver of the Git host's deliveries; nil when it is
	// off, and the reconciler polls alone.
	GitEvents *GitEventsReceiver
	// APIReader reads Secrets without caching them.
	APIReader client.Reader
	// Git reaches the Git hosts.
	Git GitClient
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// ChecksRecheck is how often a Checking Task reads its checks; zero means 15 seconds.
	ChecksRecheck time.Duration
	// SecretRecheck is how often a Task waiting for a credentials Secret looks
	// again; zero means 15 seconds.
	SecretRecheck time.Duration
	// StandardChangeRecheck is how often a Blocked Task looks at its Standard
	// change; zero means a minute.
	StandardChangeRecheck time.Duration
	// GitTimeout is how long the operator waits on a Git host at a time; zero
	// means 30 seconds.
	GitTimeout time.Duration
	// PullRequestRecheck is how often a Failed Task looks whether its open pull
	// request is still wanted; zero means 10 minutes.
	PullRequestRecheck time.Duration
	// MaxSandboxes caps the agent sandboxes running at once, across all projects;
	// zero means no cap. Agents and their checks are heavy.
	MaxSandboxes int
	// WorkspaceMemory, when set, gives each sandbox a workspace in memory of up to
	// this size instead of on the node's disk.
	WorkspaceMemory *resource.Quantity
	// CheckpointClass is the StorageClass of each sandbox's checkpoint volume:
	// empty is the cluster's default class, NoCheckpointClass none, so an
	// interrupted run is rerun.
	CheckpointClass string
	// SandboxGoCache, when set, is a directory on the node that sandboxes share as
	// their Go build cache.
	SandboxGoCache string
	// SharedPullSecrets name the pull Secrets the Project reconciler shares into
	// project namespaces; every sandbox pod lists them, so pulling a private image
	// needs no patched default ServiceAccount.
	SharedPullSecrets []string
	// SharedSecrets name the Secrets the Project reconciler shares from
	// SourceNamespace, the operator's own. An Agent whose credentialsSecretRef
	// names one is on the factory's shared token: its pre-flight reads the
	// Secret there, and with ModelProxyURL set its sandbox holds no token and
	// reaches Claude through the model proxy at that URL. An Agent naming any
	// other Secret gets it as env, as before.
	SharedSecrets   []string
	SourceNamespace string
	ModelProxyURL   string
	// Recorder posts the Task's Events; nil posts none.
	Recorder events.EventRecorder
	// TestEnvironments are the test environments' providers by member, as
	// cmd/main.go builds them: a run leases the environments it needs from
	// them before its sandbox is made.
	TestEnvironments ports.TestEnvironmentProviders
	// EnvironmentRecheck is how often a Task whose run waits for a test
	// environment looks again; zero means 15 seconds.
	EnvironmentRecheck time.Duration
}

// eventSessionHandedOff is the Normal Event of a run whose agent sessions
// handed off to fresh ones.
const eventSessionHandedOff = "SessionHandedOff"

// eventCheckFailsOnBase is the Normal Event of a round whose check fails on
// main in the agent's workspace too: CI decides it.
const eventCheckFailsOnBase = "CheckFailsOnBase"

// +kubebuilder:rbac:groups=theos.factory.io,resources=tasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=theos.factory.io,resources=tasks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=theos.factory.io,resources=tasks/finalizers,verbs=update
// +kubebuilder:rbac:groups=theos.factory.io,resources=agents;softwaresystems;briefs,verbs=get;list;watch
// +kubebuilder:rbac:groups=agents.x-k8s.io,resources=sandboxes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;update;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile takes the next step of a Task.
func (r *TaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// A run a step ends keeps its sandbox until the status write records it.
	ctx = withEndedRuns(ctx)
	// Read the live Task: steps have side effects (sandboxes, merges), and the cache
	// can lag behind our own last status write.
	var task c4v1beta1.Task
	if err := r.APIReader.Get(ctx, req.NamespacedName, &task); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !task.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	switch task.Spec.Kind {
	case c4v1beta1.TaskPlan:
		return r.reconcilePlan(ctx, &task)
	case c4v1beta1.TaskTriage:
		return r.reconcileTriage(ctx, &task)
	case c4v1beta1.TaskScore:
		return r.reconcileScore(ctx, &task)
	}
	status := task.Status.DeepCopy()
	if status.Phase == "" {
		status.Phase = c4v1beta1.TaskPending
	}

	var container c4v1beta1.Container
	err := r.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: task.Spec.ContainerRef.Name}, &container)
	if apierrors.IsNotFound(err) {
		r.progress(&task, status, metav1.ConditionFalse, "ContainerNotFound",
			"Container "+task.Spec.ContainerRef.Name+" not found")
		return ctrl.Result{}, r.writeStatus(ctx, &task, status)
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	// The waits for the host's checks stretch while its deliveries arrive (#1222).
	ctx = withHostEvents(ctx, r.Client, task.Namespace, container.Spec.GitProviderRef.Name)
	labels := projectLabels(task.Namespace)
	labels[c4v1beta1.LabelContainer] = container.Name
	if err := adoptTask(ctx, r.Client, r.Scheme, &container, &task, labels); err != nil {
		return ctrl.Result{}, err
	}
	// Adopting reads the Task back with a newer resourceVersion: take its status
	// too, or a status written since the read would be overwritten (#591).
	status = task.Status.DeepCopy()
	if status.Phase == "" {
		status.Phase = c4v1beta1.TaskPending
	}
	if isFinal(status.Phase) {
		r.returnFinalLeases(ctx, &task, status)
		if status.Phase == c4v1beta1.TaskFailed {
			result := r.tendAbandonedPull(ctx, &task, status, &container)
			return result, r.writeStatus(ctx, &task, status)
		}
		if status.Phase != c4v1beta1.TaskMerged || status.PullRequest == 0 ||
			status.MergeCommit != "" && status.PersonCommits != nil {
			return ctrl.Result{}, r.writeStatus(ctx, &task, status)
		}
		// Merged before the Task recorded merge commits, or pull requests'
		// sizes, or the read failed: a Crew waits for the merge commit to tag
		// the release; the size and person commits are for the metrics.
		provider, repo, err := r.gitRepo(ctx, &container)
		if err != nil {
			logf.FromContext(ctx).Error(err, "Failed to reach the Git host for the merged pull request")
		} else {
			if status.MergeCommit == "" {
				status.MergeCommit = r.mergeCommit(ctx, provider, repo, status)
			}
			r.recordPull(ctx, provider, repo, status)
		}
		var result ctrl.Result
		if status.MergeCommit == "" {
			result.RequeueAfter = mergeCommitRetryAfter(status.CompletedAt, r.now())
		}
		return result, r.writeStatus(ctx, &task, status)
	}

	var agent c4v1beta1.Agent
	err = r.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: task.Spec.AgentRef.Name}, &agent)
	if apierrors.IsNotFound(err) {
		r.progress(&task, status, metav1.ConditionFalse, "AgentNotFound", "Agent "+task.Spec.AgentRef.Name+" not found")
		return ctrl.Result{}, r.writeStatus(ctx, &task, status)
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	agents := taskAgents{implementer: &agent, reviewer: &agent}
	for _, role := range []struct {
		ref   *c4v1beta1.LocalRef
		agent **c4v1beta1.Agent
	}{{task.Spec.ReviewerRef, &agents.reviewer}, {task.Spec.VerifierRef, &agents.verifier}} {
		if role.ref == nil {
			continue
		}
		*role.agent = &c4v1beta1.Agent{}
		err = r.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: role.ref.Name}, *role.agent)
		if apierrors.IsNotFound(err) {
			r.progress(&task, status, metav1.ConditionFalse, "AgentNotFound", "Agent "+role.ref.Name+" not found")
			return ctrl.Result{}, r.writeStatus(ctx, &task, status)
		}
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	facts, err := r.facts(ctx, &task, status, &container, &agent)
	if err != nil {
		return ctrl.Result{}, err
	}
	step := model.NextTaskStep(facts)
	logf.FromContext(ctx).V(1).Info("Took Task step", "phase", status.Phase, "round", status.Round,
		"action", step.Action, "message", step.Message)
	if role := runRole(status.Phase); role != "" {
		if err := r.syncPause(ctx, &task, status, sandboxName(&task, status, role), facts); err != nil {
			return ctrl.Result{}, err
		}
	}
	result, err := r.apply(ctx, &task, status, &container, agents, step)
	if err != nil {
		return result, err
	}
	r.setPaused(&task, status, step, facts.Pause)
	r.queueForSlot(status, step, &result)
	syncNeedsYou(&task, status)
	return result, r.writeStatus(ctx, &task, status)
}

// queueForSlot keeps the Task's place in the queue for an agent slot while its
// step waits for one, and gives it up otherwise.
func (r *TaskReconciler) queueForSlot(status *c4v1beta1.TaskStatus, step model.TaskStep, result *ctrl.Result) {
	if step.SlotWait {
		// Queue for a slot. Sandboxes of other Tasks don't wake this one; look again.
		if status.SlotWaitSince == nil {
			now := metav1.NewTime(r.now())
			status.SlotWaitSince = &now
		}
		result.RequeueAfter = slotRecheck
	} else {
		// Only a Task that would start a sandbox holds a place in the queue: one
		// waiting for anything else must not keep others from the slot.
		status.SlotWaitSince = nil
	}
}

// facts gathers what the next step depends on.
func (r *TaskReconciler) facts(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agent *c4v1beta1.Agent) (model.TaskFacts, error) {
	facts := model.TaskFacts{
		Phase:          string(status.Phase),
		Container:      container.Name,
		Round:          int(status.Round),
		MaxRounds:      int(agent.Spec.MaxRounds),
		ContainerReady: container.Status.Phase == c4v1beta1.ContainerReady,
		Protected:      model.Protected(container.Choices().Standard != ""),
		ReviewAttempts: int(status.ReviewAttempts),
		ReviewAgains:   int(status.ReviewAgains),
		Reruns:         int(status.Reruns),
		Resumes:        int(status.Resumes),

		MergeWaitsForChecks: mergeWaitsForChecks(status),
		MergeWhenChecksPass: status.MergeWhenChecksPass,
		StandardChanges:     standardChangeCount(status),
		Resumed:             resumedRun(status),
		AllowedPaths:        task.Spec.AllowedProtectedPaths,
		Feedback:            status.Feedback,
	}
	change, err := r.changeFacts(ctx, task, status, container)
	if err != nil {
		return facts, err
	}
	facts.Change = change
	if status.PhaseChangedAt != nil {
		facts.InPhase = r.now().Sub(status.PhaseChangedAt.Time)
	}
	if failedChecks(status) {
		facts.FailedCommit = status.Commit
	}
	if status.AgentBackAt != nil {
		facts.AgentBackIn = status.AgentBackAt.Sub(r.now())
	}
	if r.MaxSandboxes > 0 {
		noSlot, err := r.noFreeSlot(ctx, task)
		if err != nil {
			return facts, err
		}
		facts.NoFreeSlot = noSlot
	}
	if facts.MaxRounds == 0 {
		facts.MaxRounds = 3
	}

	if err := r.crewFacts(ctx, task, status, &facts); err != nil {
		return facts, err
	}

	if err := r.turnFacts(ctx, task, status, container, &facts); err != nil {
		return facts, err
	}

	if role := runRole(status.Phase); role != "" {
		name := sandboxName(task, status, role)
		startedOn := ""
		if name == status.Sandbox {
			startedOn = status.SandboxPod
		}
		run, err := runState(ctx, r.Client, task.Namespace, name, startedOn)
		if err != nil {
			return facts, err
		}
		if name == status.Sandbox && startedOn == "" {
			status.SandboxPod = run.Pod // the pod the run started on
		}
		facts.Run = r.withSuspendedFor(run)
	}

	if status.Phase == c4v1beta1.TaskChecking && status.Commit != "" {
		if err := r.checkFacts(ctx, task, status, container, &facts); err != nil {
			return facts, err
		}
	}

	if err := r.pullFacts(ctx, task, container, status, &facts); err != nil {
		return facts, err
	}
	r.approvalFacts(ctx, task, status, &facts)
	if err := r.specFacts(ctx, task, status, container, &facts); err != nil {
		return facts, err
	}
	verifyFacts(task, status, &facts)
	return facts, nil
}

// crewFacts adds the cost cap and the pause of the Crew that started the Task:
// a Crew's Task stops at the Crew's cost cap (#1893), and its runs pause with
// the Crew's. A Crew that is gone caps and pauses nothing.
func (r *TaskReconciler) crewFacts(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	facts *model.TaskFacts) error {
	crewName := task.Labels[LabelCrew]
	if crewName == "" {
		return nil
	}
	var crew c4v1beta1.Crew
	switch err := r.Get(ctx, types.NamespacedName{Namespace: task.Namespace, Name: crewName}, &crew); {
	case apierrors.IsNotFound(err):
	case err != nil:
		return err
	default:
		facts.BudgetMilliUSD = int64(crew.Spec.TaskBudgetUSD) * 1000
		facts.CostMilliUSD = status.CostMilliUSD
		facts.Pause = crewPause(&crew)
		facts.SpecApproval = string(crew.Spec.SpecApproval)
	}
	return nil
}

// turnFacts reads, for a Pending Task, whether an earlier Task on its
// Container holds it up, and whether main is green to start from. A Crew's
// Tasks leave their concurrency to the Crew, and a Benchmark's to the
// Benchmark, as they merge nothing; others take turns. A Benchmark's Task
// starts from its pinned base, not main's head.
func (r *TaskReconciler) turnFacts(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, facts *model.TaskFacts) error {
	if status.Phase != c4v1beta1.TaskPending || task.Spec.Benchmark != nil {
		return nil
	}
	if task.Labels[LabelCrew] == "" {
		var tasks c4v1beta1.TaskList
		if err := r.List(ctx, &tasks, client.InNamespace(task.Namespace),
			client.MatchingFields{indexTaskContainer: container.Name}); err != nil {
			return err
		}
		refs := make([]model.TaskRef, 0, len(tasks.Items))
		for _, t := range tasks.Items {
			if pushesNothing(&t) {
				continue // a triage and a judge push nothing, so they hold no other Task up
			}
			refs = append(refs, model.TaskRef{Name: t.Name, Created: t.CreationTimestamp.Unix(),
				Active: !isFinal(t.Status.Phase) && t.DeletionTimestamp.IsZero()})
		}
		facts.BlockedBy = model.BlockingTask(task.Name, refs)
	}
	if facts.Protected && facts.ContainerReady && facts.BlockedBy == "" {
		return r.baseFacts(ctx, container, facts)
	}
	return nil
}

// baseFacts reads the CI state of main's head for the checks main requires,
// before round 1: round 1 starts only on a green base.
func (r *TaskReconciler) baseFacts(ctx context.Context, container *c4v1beta1.Container,
	facts *model.TaskFacts) error {
	git, err := gitAccessFor(ctx, r.Client, r.APIReader, r.Git, container.Namespace,
		container.Spec.GitProviderRef.Name)
	if err != nil {
		return err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	checks, err := git.provider.CombinedStatus(gitCtx, git.provider.Repository(container.Name),
		ports.DefaultBranch)
	if err != nil {
		return err
	}
	facts.Base = model.BaseState(git.ci.CheckContexts, checks.State, checks.Contexts)
	facts.BaseURL = checks.URL
	return nil
}

// checkFacts reads the CI result on the round's commit while the Task is
// Checking: its state and failed log, and the runs at the commit while it
// waits, or while a failure may still be the one from before a rerun.
func (r *TaskReconciler) checkFacts(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, facts *model.TaskFacts) error {
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, task.Namespace, container.Spec.GitProviderRef.Name)
	if err != nil {
		return err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	repo := provider.Repository(container.Name)
	checks, err := provider.CombinedStatus(gitCtx, repo, status.Commit)
	if err != nil {
		return err
	}
	status.Checks = &c4v1beta1.CheckStatus{Commit: checks.Commit, State: checks.State, URL: checks.URL}
	facts.Checks, facts.ChecksURL = checks.State, checks.URL
	failed := checks.State == ports.CheckFailure || checks.State == ports.CheckError
	facts.ChecksReruns, facts.ChecksRerunDue = int(status.ChecksReruns), status.ChecksRerunDue
	if status.ChecksRerunAt != nil {
		facts.ChecksRerunAt = status.ChecksRerunAt.Time
		facts.SinceChecksRerun = r.now().Sub(status.ChecksRerunAt.Time)
		if failed {
			// Until the rerun ends, the failure is the killed run's.
			facts.CIRun, facts.ChecksRerunEnded = r.rerunState(gitCtx, provider, repo, status)
		}
	}
	read := failed && (facts.ChecksRerunAt.IsZero() || facts.ChecksRerunEnded)
	if read {
		// Best effort: without the log the agent still gets the link.
		log, err := provider.CheckLog(gitCtx, repo, checks.URL)
		facts.ChecksLog, facts.ChecksNoLog = log, errors.Is(err, ports.ErrNoCILog)
	}
	// A failure whose runs still run waits for them before its jobs run again.
	if checks.State != ports.CheckSuccess && facts.CIRun == (model.CIRun{}) {
		facts.CIRun = r.ciRun(gitCtx, provider, repo, task.Namespace, status.Commit, read)
	}
	if status.PhaseChangedAt != nil {
		facts.CheckingSince = status.PhaseChangedAt.Time
	}
	return nil
}

// approvalFacts reads whether the review run that approved a Standard change
// ended while its branch, with main merged in, was checked again: a gated
// merge merges only with the approval of a review run that still lasts. Asked
// once those checks passed.
func (r *TaskReconciler) approvalFacts(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	facts *model.TaskFacts) {
	if len(task.Spec.AllowedProtectedPaths) > 0 && status.Phase == c4v1beta1.TaskChecking &&
		status.MergeWhenChecksPass && facts.Checks == ports.CheckSuccess {
		facts.ApprovalEnded = r.reviewRun(ctx, task, status) == nil
	}
}

// rerunState reads whether the runs at the Task's commit whose failed jobs ran
// again have given their result, and the state of its runs while they haven't. Runs that can't
// be read are unknown, which waits.
func (r *TaskReconciler) rerunState(ctx context.Context, provider ports.CI, repo ports.Repository,
	status *c4v1beta1.TaskStatus) (model.CIRun, bool) {
	runs, err := provider.CommitRuns(ctx, repo, status.Commit)
	if err != nil {
		logf.FromContext(ctx).Info("Could not read the CI runs of the Task's commit", "commit", status.Commit,
			"error", err.Error())
		return model.CIRun{Commit: status.Commit, State: model.CIRunUnknown}, false
	}
	seen := workflowRuns(runs)
	if model.RerunEnded(seen, status.ChecksRerunAt.Time) {
		return model.CIRun{}, true
	}
	return r.runState(ctx, provider, repo, "", status.Commit, seen, false), false
}

// workflowRuns is the model's view of the Git host's runs.
func workflowRuns(runs []ports.WorkflowRun) []model.WorkflowRun {
	seen := make([]model.WorkflowRun, 0, len(runs))
	for _, run := range runs {
		seen = append(seen, model.WorkflowRun{ID: run.ID, Workflow: run.Workflow, Event: run.Event,
			Status: run.Status, Conclusion: run.Conclusion, Started: run.Started, URL: run.URL})
	}
	return seen
}

// ciRun reads the state of the CI runs at commit, whose checks have no result
// yet, whether a runner is online while one is queued, and, when canceller,
// who cancelled a failed one. Runs that can't be read are unknown, which waits
// up to model.QueuedChecksLimit.
func (r *TaskReconciler) ciRun(ctx context.Context, provider ports.CI, repo ports.Repository,
	namespace, commit string, canceller bool) model.CIRun {
	ci := model.CIRun{Commit: commit}
	runs, err := provider.CommitRuns(ctx, repo, commit)
	if err != nil {
		logf.FromContext(ctx).Info("Could not read the CI runs of the Task's commit", "commit", commit, "error", err.Error())
		ci.State = model.CIRunUnknown
		return ci
	}
	return r.runState(ctx, provider, repo, namespace, commit, workflowRuns(runs), canceller)
}

// runState sums up the runs at commit, whether a runner is online while one is
// queued, and, when canceller, who cancelled a failed one that ended
// cancelled: asked only when the failure is read, as it costs a call a job.
// namespace is the Task's, where the CI runner pool's pods are.
func (r *TaskReconciler) runState(ctx context.Context, provider ports.CI, repo ports.Repository,
	namespace, commit string, seen []model.WorkflowRun, canceller bool) model.CIRun {
	ci := model.CIRun{Commit: commit}
	state, run := model.CommitRunState(seen)
	ci.State, ci.URL, ci.Started = state, run.URL, run.Started
	if cancelled := model.CancelledRuns(seen); canceller && len(cancelled) > 0 {
		ci.CancelledBy = r.canceller(ctx, provider, repo, namespace, cancelled)
	}
	switch state {
	case model.CIRunRunning:
		if !run.Started.IsZero() {
			ci.Running = r.now().Sub(run.Started)
		}
	case model.CIRunQueued:
		online, known := provider.RunnersOnline(ctx)
		ci.NoRunnerOnline = known && online == 0
	}
	return ci
}

// canceller tells who cancelled the cancelled runs at a Task's commit: by the
// fate of the pool pod a cancelled job ran on (#1140), else whether the job's
// runner stayed online (model.Canceller).
func (r *TaskReconciler) canceller(ctx context.Context, provider ports.CI, repo ports.Repository,
	namespace string, cancelled []model.WorkflowRun) string {
	var jobs []model.CIJob
	online, known := map[int64]bool{}, map[int64]bool{}
	for _, run := range cancelled {
		found, err := provider.RunJobs(ctx, repo, run.ID)
		if err != nil {
			logf.FromContext(ctx).Info("Could not read the jobs of a cancelled CI run", "run", run.URL,
				"error", err.Error())
			return model.Canceller(nil, false, r.now())
		}
		for _, j := range found {
			job := model.CIJob{Conclusion: j.Conclusion, Runner: j.RunnerID, RunnerName: j.RunnerName,
				Ended: j.Completed}
			if job.Cancelled() && job.Runner != 0 {
				job.PodFate = r.runnerPodFate(ctx, namespace, job.RunnerName)
				if _, asked := known[job.Runner]; !asked && job.PodFate == "" {
					online[job.Runner], known[job.Runner] = provider.RunnerOnline(ctx, job.Runner)
				}
				job.RunnerOnline, job.RunnerUnknown = online[job.Runner], !known[job.Runner]
			}
			jobs = append(jobs, job)
		}
	}
	return model.Canceller(jobs, true, r.now())
}

// runnerPodFate is the fate of the pool pod named runner in namespace, the
// pool's verdict (AnnotationRunnerFate) or, before the pool wrote one, what
// the pod shows; empty when no pool pod of that name is kept. Read live: the
// Task's cache holds no pods.
func (r *TaskReconciler) runnerPodFate(ctx context.Context, namespace, runner string) string {
	if runner == "" {
		return ""
	}
	var pod corev1.Pod
	if err := r.APIReader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: runner}, &pod); err != nil ||
		pod.Labels[LabelCIRunner] != ciRunnerValue {
		return ""
	}
	if fate := pod.Annotations[AnnotationRunnerFate]; fate != "" {
		return fate
	}
	return runnerPodFate(&pod)
}

// noFreeSlot reports whether the Task must wait for an agent slot: all are taken,
// or a Task that has waited longer goes first.
func (r *TaskReconciler) noFreeSlot(ctx context.Context, task *c4v1beta1.Task) (bool, error) {
	held, err := r.slotHolders(ctx)
	if err != nil {
		return false, err
	}
	var tasks c4v1beta1.TaskList
	if err := r.List(ctx, &tasks); err != nil {
		return false, err
	}
	var waiting []model.SlotWaiter
	for _, t := range tasks.Items {
		if t.Status.SlotWaitSince != nil && !isFinal(t.Status.Phase) {
			waiting = append(waiting, model.SlotWaiter{Name: t.Namespace + "/" + t.Name, Since: t.Status.SlotWaitSince.Time})
		}
	}
	return model.NoFreeSlot(task.Namespace+"/"+task.Name, r.now(), waiting, r.MaxSandboxes, len(held)), nil
}

// slotHolders lists the agent sandboxes, as namespace/name, that hold a slot:
// every one whose run hasn't ended. A sandbox kept after its pod succeeded or
// failed, a reviewer's while its Task waits for CI, holds none; nor does a
// suspended one whose run paused or waits for its agent, once its pod is
// gone (#1996): its resume waits for a free slot.
func (r *TaskReconciler) slotHolders(ctx context.Context) ([]string, error) {
	sandboxes := &unstructured.UnstructuredList{}
	sandboxes.SetGroupVersionKind(SandboxGVK.GroupVersion().WithKind("SandboxList"))
	agents := client.MatchingLabels{LabelAgentSandbox: agentSandboxValue}
	// Counted live: the cache may not have the sandbox started a moment ago.
	if err := r.APIReader.List(ctx, sandboxes, agents); err != nil {
		return nil, err
	}
	var pods corev1.PodList
	if err := r.APIReader.List(ctx, &pods, agents); err != nil {
		return nil, err
	}
	byName := map[string]*corev1.Pod{}
	for i, pod := range pods.Items {
		byName[pod.Namespace+"/"+pod.Name] = &pods.Items[i]
	}
	var held []string
	for i := range sandboxes.Items {
		sandbox := &sandboxes.Items[i]
		name := sandbox.GetNamespace() + "/" + sandbox.GetName()
		// A pod of an earlier sandbox with this name is not this one's run.
		pod := byName[name]
		own := pod != nil && metav1.IsControlledBy(pod, sandbox)
		switch {
		case own && (pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed):
		case !own && waitsSuspended(sandbox):
		default:
			held = append(held, name)
		}
	}
	return held, nil
}

// waitsSuspended reports whether the sandbox is suspended for a run that
// paused or waits for its agent, which may wait for hours with no agent
// running.
func waitsSuspended(sandbox *unstructured.Unstructured) bool {
	if !sandboxSuspended(sandbox) {
		return false
	}
	annotations := sandbox.GetAnnotations()
	_, paused := annotations[annotationPausedRun]
	_, waits := annotations[annotationAgentBack]
	return paused || waits
}

// pullFacts reads whether an approved change's pull request misses what merged
// into main since its checks ran, and whether the Git host says it merges: an approved change merges
// only on checks that ran against the main it merges into.
func (r *TaskReconciler) pullFacts(ctx context.Context, task *c4v1beta1.Task, container *c4v1beta1.Container,
	status *c4v1beta1.TaskStatus, facts *model.TaskFacts) error {
	if task.Spec.Benchmark != nil {
		// A Benchmark's Task stays on its pinned base and never merges:
		// whether main moved, or would conflict, is nothing to it.
		return nil
	}
	run := facts.Run
	judging := status.Phase == c4v1beta1.TaskReviewing || status.Phase == c4v1beta1.TaskVerifying
	approved := judging && run.Done && run.Result != nil && run.Result.Verdict == model.VerdictApprove
	updated := status.Phase == c4v1beta1.TaskChecking && status.MergeWhenChecksPass &&
		facts.Checks == ports.CheckSuccess
	if !approved && !updated || status.PullRequest == 0 {
		return nil
	}
	pr, err := r.pullRequest(ctx, container, status)
	if err != nil {
		return err
	}
	open := pr.State == stateOpen && !pr.Merged
	facts.Unmergeable, facts.Behind = open && !pr.Mergeable, open && pr.Behind
	return nil
}

// pullRequest reads the Task's pull request: whether main moved since its
// branch took it in, and whether it conflicts with main.
func (r *TaskReconciler) pullRequest(ctx context.Context, container *c4v1beta1.Container,
	status *c4v1beta1.TaskStatus) (ports.PullRequest, error) {
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return ports.PullRequest{}, err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	return provider.GetPullRequest(gitCtx, repo, int(status.PullRequest))
}

// runRole is the role of the sandbox a phase waits on.
func runRole(phase c4v1beta1.TaskPhase) string {
	switch phase {
	case c4v1beta1.TaskImplementing:
		return model.RoleImplementer
	case c4v1beta1.TaskReviewing:
		return model.RoleReviewer
	case c4v1beta1.TaskVerifying:
		return model.RoleVerifier
	}
	return ""
}

func sandboxName(task *c4v1beta1.Task, status *c4v1beta1.TaskStatus, role string) string {
	return model.SandboxName(task.Name, int(status.Round), int(status.Restarts), role,
		int(status.ReviewAttempts+status.ReviewAgains)+1, int(status.Reruns))
}

// adoptTask has the Container adopt task, unless a Benchmark made it: then it
// is its Benchmark's, which gave it these labels.
func adoptTask(ctx context.Context, c client.Client, scheme *runtime.Scheme, container *c4v1beta1.Container,
	task *c4v1beta1.Task, labels map[string]string) error {
	if task.Spec.Benchmark != nil {
		return nil
	}
	return adopt(ctx, c, scheme, container, task, labels)
}

// pushesNothing reports whether t holds no other Task on its Container up: a
// Triage or a Score Task pushes nothing, and a Benchmark's Task merges
// nothing.
func pushesNothing(t *c4v1beta1.Task) bool {
	return t.Spec.Kind == c4v1beta1.TaskTriage || t.Spec.Kind == c4v1beta1.TaskScore || t.Spec.Benchmark != nil
}

func isFinal(phase c4v1beta1.TaskPhase) bool {
	return phase == c4v1beta1.TaskMerged || phase == c4v1beta1.TaskFailed || phase == c4v1beta1.TaskApproved
}

// failedNoCI reports whether a Task failed through its CI runner's fault (CI
// never reported on its commit, or the runner ran out of room), so it costs
// its issue no try.
func failedNoCI(status *c4v1beta1.TaskStatus) bool {
	if status.Phase != c4v1beta1.TaskFailed {
		return false
	}
	cond := meta.FindStatusCondition(status.Conditions, ConditionSucceeded)
	return cond != nil && model.RunnerFault(cond.Reason)
}

// failedUnseen reports whether a Task failed because its agent said it
// can't see the issue (model.ReasonIssueUnseen), which model.TryCounter lets
// cost its issue no try once.
func failedUnseen(status *c4v1beta1.TaskStatus) bool {
	if status.Phase != c4v1beta1.TaskFailed {
		return false
	}
	cond := meta.FindStatusCondition(status.Conditions, ConditionSucceeded)
	return cond != nil && cond.Reason == model.ReasonIssueUnseen
}

func (r *TaskReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *TaskReconciler) progress(task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	s metav1.ConditionStatus, reason, message string) {
	setCondition(&status.Conditions, task.Generation, ConditionProgressing, s, reason, message)
}

func (r *TaskReconciler) writeStatus(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus) error {
	status.ObservedGeneration = task.Generation
	if status.Phase != task.Status.Phase || status.PhaseChangedAt == nil {
		now := metav1.NewTime(r.now())
		status.PhaseChangedAt = &now
	}
	status.History = recordTransition(status, r.now())
	if !equality.Semantic.DeepEqual(&task.Status, status) {
		orig := task.DeepCopy()
		task.Status = *status
		// A stale write fails with a conflict and the Task is reconciled again.
		if err := r.Status().Patch(ctx, task, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})); err != nil {
			return err
		}
	}
	// The ended runs' results are in the written status: their sandboxes can go.
	return r.dropEndedRuns(ctx, task)
}

// recordTransition adds the Task's current phase, round and Progressing reason to
// its timeline when they changed.
func recordTransition(status *c4v1beta1.TaskStatus, now time.Time) []c4v1beta1.TaskTransition {
	next := model.Transition{At: now, Phase: string(status.Phase), Round: int(status.Round)}
	if cond := meta.FindStatusCondition(status.Conditions, ConditionProgressing); cond != nil {
		next.Reason, next.Message = cond.Reason, cond.Message
	}
	history := make([]model.Transition, 0, len(status.History))
	for _, tr := range status.History {
		history = append(history, model.Transition{At: tr.At.Time, Phase: string(tr.Phase), Round: int(tr.Round),
			Reason: tr.Reason, Message: tr.Message})
	}
	recorded, added := model.RecordTransition(history, next)
	if !added {
		return status.History
	}
	out := make([]c4v1beta1.TaskTransition, 0, len(recorded))
	for _, tr := range recorded {
		out = append(out, c4v1beta1.TaskTransition{At: metav1.NewTime(tr.At), Phase: c4v1beta1.TaskPhase(tr.Phase),
			Round: int32(tr.Round), Reason: tr.Reason, Message: tr.Message})
	}
	return out
}

// SetupWithManager sets up the controller with the Manager.
func (r *TaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// A Task's pods are its sandboxes' pods; they carry the Task's name.
	podTask := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
		if name := obj.GetLabels()[LabelTask]; name != "" {
			return []reconcile.Request{requestFor(obj.GetNamespace(), name)}
		}
		return nil
	})
	// A Container becoming Ready, or a Task finishing, can start the Tasks waiting on it.
	containerTasks := func(ctx context.Context, ns, container string) []reconcile.Request {
		var tasks c4v1beta1.TaskList
		if err := mgr.GetClient().List(ctx, &tasks, client.InNamespace(ns),
			client.MatchingFields{indexTaskContainer: container}); err != nil {
			return nil
		}
		var reqs []reconcile.Request
		for _, t := range tasks.Items {
			if !isFinal(t.Status.Phase) {
				reqs = append(reqs, requestFor(t.Namespace, t.Name))
			}
		}
		return reqs
	}
	fromContainer := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		return containerTasks(ctx, obj.GetNamespace(), obj.GetName())
	})
	fromTask := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		task, ok := obj.(*c4v1beta1.Task)
		if !ok {
			return nil
		}
		return containerTasks(ctx, task.Namespace, task.Spec.ContainerRef.Name)
	})
	// A Crew's cost cap changing, or the Crew going away, resumes its Tasks
	// waiting at the cap (#1893); a pause of its runs starting or ending
	// pauses or resumes them.
	fromCrew := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var tasks c4v1beta1.TaskList
		if err := mgr.GetClient().List(ctx, &tasks, client.InNamespace(obj.GetNamespace()),
			client.MatchingLabels{LabelCrew: obj.GetName()}); err != nil {
			return nil
		}
		var reqs []reconcile.Request
		for _, t := range tasks.Items {
			if !isFinal(t.Status.Phase) {
				reqs = append(reqs, requestFor(t.Namespace, t.Name))
			}
		}
		return reqs
	})
	// A Brief's pause of its runs starting or ending pauses or resumes its
	// planner's.
	fromBrief := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var tasks c4v1beta1.TaskList
		if err := mgr.GetClient().List(ctx, &tasks, client.InNamespace(obj.GetNamespace()),
			client.MatchingLabels{c4v1beta1.LabelBrief: obj.GetName()}); err != nil {
			return nil
		}
		var reqs []reconcile.Request
		for _, t := range tasks.Items {
			if t.Spec.Kind == c4v1beta1.TaskPlan && !isFinal(t.Status.Phase) {
				reqs = append(reqs, requestFor(t.Namespace, t.Name))
			}
		}
		return reqs
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1beta1.Task{}).
		Owns(newSandbox()).
		Watches(&corev1.Pod{}, podTask, podFinished()).
		Watches(&c4v1beta1.Container{}, fromContainer, containerPhaseChanges()).
		Watches(&c4v1beta1.Task{}, fromTask, taskPhaseChanges()).
		Watches(&c4v1beta1.Crew{}, fromCrew, crewBudgetOrPauseChanges()).
		Watches(&c4v1beta1.Brief{}, fromBrief, briefPauseChanges()).
		Watches(&c4v1beta1.Agent{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				var tasks c4v1beta1.TaskList
				if err := mgr.GetClient().List(ctx, &tasks, client.InNamespace(obj.GetNamespace())); err != nil {
					return nil
				}
				var reqs []reconcile.Request
				for _, t := range tasks.Items {
					uses := t.Spec.AgentRef.Name == obj.GetName() ||
						t.Spec.ReviewerRef != nil && t.Spec.ReviewerRef.Name == obj.GetName()
					if uses && !isFinal(t.Status.Phase) {
						reqs = append(reqs, requestFor(t.Namespace, t.Name))
					}
				}
				return reqs
			}), createOrDelete()).
		// A delivery of the Git host reconciles the Tasks of the issue, pull request or branch a delivery names at once
		// (docs/plans/done/git-events.md, decision 7).
		WatchesRawSource(r.GitEvents.Source(gitEventTaskRequests(mgr.GetClient()))).
		Named("task").
		Complete(r)
}

// gitRepo returns the Provider and repository of a Task's Container.
func (r *TaskReconciler) gitRepo(ctx context.Context, container *c4v1beta1.Container) (interface {
	ports.GitHost
	ports.Issues
	ports.CI
	ports.AgentAccess
},
	ports.Repository, error) {
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, container.Namespace, container.Spec.GitProviderRef.Name)
	if err != nil {
		return nil, ports.Repository{}, err
	}
	return provider, provider.Repository(container.Name), nil
}

func (r *TaskReconciler) gitTimeout() time.Duration {
	if r.GitTimeout > 0 {
		return r.GitTimeout
	}
	return defaultGitTimeout
}

func (r *TaskReconciler) secretRecheck() time.Duration {
	if r.SecretRecheck > 0 {
		return r.SecretRecheck
	}
	return defaultTaskSecretRecheck
}

func (r *TaskReconciler) checksRecheck() time.Duration {
	if r.ChecksRecheck > 0 {
		return r.ChecksRecheck
	}
	return defaultChecksRecheck
}
