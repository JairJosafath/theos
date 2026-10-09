/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
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
	// taskSecretRecheck: Secrets are read uncached, so not watched.
	taskSecretRecheck = 15 * time.Second
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
	// APIReader reads Secrets without caching them.
	APIReader client.Reader
	// Git reaches the Git hosts.
	Git GitClient
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// ChecksRecheck is how often a Checking Task reads its checks; zero means 15 seconds.
	ChecksRecheck time.Duration
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
	// SandboxGoCache, when set, is a directory on the node that sandboxes share as
	// their Go build cache.
	SandboxGoCache string
	// SharedPullSecrets name the pull Secrets the Project reconciler shares into
	// project namespaces; every sandbox pod lists them, so pulling a private image
	// needs no patched default ServiceAccount.
	SharedPullSecrets []string
	// Recorder posts the Task's Events; nil posts none.
	Recorder events.EventRecorder
}

// eventSessionHandedOff is the Normal Event of a run whose agent sessions
// handed off to fresh ones.
const eventSessionHandedOff = "SessionHandedOff"

// +kubebuilder:rbac:groups=c4.factory.io,resources=tasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=c4.factory.io,resources=tasks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=c4.factory.io,resources=tasks/finalizers,verbs=update
// +kubebuilder:rbac:groups=c4.factory.io,resources=agents;softwaresystems;briefs,verbs=get;list;watch
// +kubebuilder:rbac:groups=agents.x-k8s.io,resources=sandboxes,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;update;patch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile takes the next step of a Task.
func (r *TaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Read the live Task: steps have side effects (sandboxes, merges), and the cache
	// can lag behind our own last status write.
	var task c4v1alpha1.Task
	if err := r.APIReader.Get(ctx, req.NamespacedName, &task); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !task.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	if task.Spec.Kind == c4v1alpha1.TaskPlan {
		return r.reconcilePlan(ctx, &task)
	}
	status := task.Status.DeepCopy()
	if status.Phase == "" {
		status.Phase = c4v1alpha1.TaskPending
	}

	var container c4v1alpha1.Container
	err := r.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: task.Spec.ContainerRef.Name}, &container)
	if apierrors.IsNotFound(err) {
		r.progress(&task, status, metav1.ConditionFalse, "ContainerNotFound",
			"Container "+task.Spec.ContainerRef.Name+" not found")
		return ctrl.Result{}, r.writeStatus(ctx, &task, status)
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	labels := projectLabels(task.Namespace)
	labels[c4v1alpha1.LabelContainer] = container.Name
	if err := adopt(ctx, r.Client, r.Scheme, &container, &task, labels); err != nil {
		return ctrl.Result{}, err
	}
	// Adopting reads the Task back with a newer resourceVersion: take its status
	// too, or a status written since the read would be overwritten (#591).
	status = task.Status.DeepCopy()
	if status.Phase == "" {
		status.Phase = c4v1alpha1.TaskPending
	}
	if isFinal(status.Phase) {
		if status.Phase == c4v1alpha1.TaskFailed {
			result := r.tendAbandonedPull(ctx, &task, status, &container)
			return result, r.writeStatus(ctx, &task, status)
		}
		if status.Phase != c4v1alpha1.TaskMerged || status.MergeCommit != "" || status.PullRequest == 0 {
			return ctrl.Result{}, r.writeStatus(ctx, &task, status)
		}
		// Merged before the Task recorded merge commits, or the read failed: a Crew
		// waits for it to tag the release.
		provider, repo, err := r.gitRepo(ctx, &container)
		if err != nil {
			logf.FromContext(ctx).Error(err, "Failed to reach the Git host for the merge commit")
		} else {
			status.MergeCommit = r.mergeCommit(ctx, provider, repo, status)
		}
		var result ctrl.Result
		if status.MergeCommit == "" {
			result.RequeueAfter = mergeCommitRetryAfter(status.CompletedAt, r.now())
		}
		return result, r.writeStatus(ctx, &task, status)
	}

	var agent c4v1alpha1.Agent
	err = r.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: task.Spec.AgentRef.Name}, &agent)
	if apierrors.IsNotFound(err) {
		r.progress(&task, status, metav1.ConditionFalse, "AgentNotFound", "Agent "+task.Spec.AgentRef.Name+" not found")
		return ctrl.Result{}, r.writeStatus(ctx, &task, status)
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	agents := taskAgents{implementer: &agent, reviewer: &agent}
	if ref := task.Spec.ReviewerRef; ref != nil {
		agents.reviewer = &c4v1alpha1.Agent{}
		err = r.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: ref.Name}, agents.reviewer)
		if apierrors.IsNotFound(err) {
			r.progress(&task, status, metav1.ConditionFalse, "AgentNotFound", "Agent "+ref.Name+" not found")
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
	result, err := r.apply(ctx, &task, status, &container, agents, step)
	if err != nil {
		return result, err
	}
	r.queueForSlot(status, step, &result)
	return result, r.writeStatus(ctx, &task, status)
}

// queueForSlot keeps the Task's place in the queue for an agent slot while its
// step waits for one, and gives it up otherwise.
func (r *TaskReconciler) queueForSlot(status *c4v1alpha1.TaskStatus, step model.TaskStep, result *ctrl.Result) {
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
func (r *TaskReconciler) facts(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, agent *c4v1alpha1.Agent) (model.TaskFacts, error) {
	facts := model.TaskFacts{
		Phase:          string(status.Phase),
		Container:      container.Name,
		Round:          int(status.Round),
		MaxRounds:      int(agent.Spec.MaxRounds),
		ContainerReady: container.Status.Phase == c4v1alpha1.ContainerReady,
		Protected:      model.Protected(container.Spec.StandardRef != nil),
		ReviewAttempts: int(status.ReviewAttempts),
		Reruns:         int(status.Reruns),

		MergeWaitsForChecks: mergeWaitsForChecks(status),
		MergeWhenChecksPass: status.MergeWhenChecksPass,
		StandardChanges:     standardChangeCount(status),
		Resumed:             resumedRun(status),
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

	// A Crew's Tasks leave their concurrency to the Crew; others take turns.
	if status.Phase == c4v1alpha1.TaskPending && task.Labels[LabelCrew] == "" {
		var tasks c4v1alpha1.TaskList
		if err := r.List(ctx, &tasks, client.InNamespace(task.Namespace),
			client.MatchingFields{indexTaskContainer: container.Name}); err != nil {
			return facts, err
		}
		refs := make([]model.TaskRef, 0, len(tasks.Items))
		for _, t := range tasks.Items {
			refs = append(refs, model.TaskRef{Name: t.Name, Created: t.CreationTimestamp.Unix(),
				Active: !isFinal(t.Status.Phase) && t.DeletionTimestamp.IsZero()})
		}
		facts.BlockedBy = model.BlockingTask(task.Name, refs)
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
		facts.Run = run
	}

	if status.Phase == c4v1alpha1.TaskChecking && status.Commit != "" {
		provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, task.Namespace, container.Spec.GitProviderRef.Name)
		if err != nil {
			return facts, err
		}
		gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
		defer cancel()
		checks, err := provider.CombinedStatus(gitCtx, provider.Repository(container.Name), status.Commit)
		if err != nil {
			return facts, err
		}
		status.Checks = &c4v1alpha1.CheckStatus{Commit: checks.Commit, State: checks.State, URL: checks.URL}
		facts.Checks, facts.ChecksURL = checks.State, checks.URL
		if checks.State == gitprovider.CheckFailure || checks.State == gitprovider.CheckError {
			// Best effort: without the log the agent still gets the link.
			if log, err := provider.CheckLog(gitCtx, provider.Repository(container.Name), checks.URL); err == nil {
				facts.ChecksLog = log
			}
		}
		if checks.State != gitprovider.CheckSuccess && checks.State != gitprovider.CheckFailure &&
			checks.State != gitprovider.CheckError {
			facts.CIRun = r.ciRun(gitCtx, provider, provider.Repository(container.Name), status.Commit)
		}
		if status.PhaseChangedAt != nil {
			facts.CheckingSince = status.PhaseChangedAt.Time
		}
	}

	if err := r.pullFacts(ctx, container, status, &facts); err != nil {
		return facts, err
	}
	// A gated merge merges only with the approval of a review run that still
	// lasts; asked once the checks with main merged in passed.
	if len(task.Spec.AllowedProtectedPaths) > 0 && status.Phase == c4v1alpha1.TaskChecking &&
		status.MergeWhenChecksPass && facts.Checks == gitprovider.CheckSuccess {
		facts.ApprovalEnded = r.reviewRun(ctx, task, status) == nil
	}
	return facts, nil
}

// ciRun reads the state of the CI runs at commit, whose checks have no result
// yet, and whether a runner is online while one is queued. Runs that can't be
// read are unknown, which waits up to model.QueuedChecksLimit.
func (r *TaskReconciler) ciRun(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	commit string) model.CIRun {
	ci := model.CIRun{Commit: commit}
	runs, err := provider.CommitRuns(ctx, repo, commit)
	if err != nil {
		logf.FromContext(ctx).Info("Could not read the CI runs of the Task's commit", "commit", commit, "error", err.Error())
		ci.State = model.CIRunUnknown
		return ci
	}
	seen := make([]model.WorkflowRun, 0, len(runs))
	for _, run := range runs {
		seen = append(seen, model.WorkflowRun{Status: run.Status, Started: run.Started, URL: run.URL})
	}
	state, run := model.CommitRunState(seen)
	ci.State, ci.URL, ci.Started = state, run.URL, run.Started
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

// noFreeSlot reports whether the Task must wait for an agent slot: all are taken,
// or a Task that has waited longer goes first.
func (r *TaskReconciler) noFreeSlot(ctx context.Context, task *c4v1alpha1.Task) (bool, error) {
	held, err := r.slotHolders(ctx)
	if err != nil {
		return false, err
	}
	var tasks c4v1alpha1.TaskList
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
// failed, a reviewer's while its Task waits for CI, holds none.
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
	ended := map[string]*corev1.Pod{}
	for i, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			ended[pod.Namespace+"/"+pod.Name] = &pods.Items[i]
		}
	}
	var held []string
	for i := range sandboxes.Items {
		sandbox := &sandboxes.Items[i]
		name := sandbox.GetNamespace() + "/" + sandbox.GetName()
		// A pod of an earlier sandbox with this name is not this one's run.
		if pod := ended[name]; pod == nil || !metav1.IsControlledBy(pod, sandbox) {
			held = append(held, name)
		}
	}
	return held, nil
}

// pullFacts reads whether an approved change's pull request misses what merged
// into main since its checks ran, and whether the Git host says it merges: an approved change merges
// only on checks that ran against the main it merges into.
func (r *TaskReconciler) pullFacts(ctx context.Context, container *c4v1alpha1.Container,
	status *c4v1alpha1.TaskStatus, facts *model.TaskFacts) error {
	run := facts.Run
	approved := status.Phase == c4v1alpha1.TaskReviewing && run.Done && run.Result != nil &&
		run.Result.Verdict == model.VerdictApprove
	updated := status.Phase == c4v1alpha1.TaskChecking && status.MergeWhenChecksPass &&
		facts.Checks == gitprovider.CheckSuccess
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
func (r *TaskReconciler) pullRequest(ctx context.Context, container *c4v1alpha1.Container,
	status *c4v1alpha1.TaskStatus) (gitprovider.PullRequest, error) {
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return gitprovider.PullRequest{}, err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	return provider.GetPullRequest(gitCtx, repo, int(status.PullRequest))
}

// runRole is the role of the sandbox a phase waits on.
func runRole(phase c4v1alpha1.TaskPhase) string {
	switch phase {
	case c4v1alpha1.TaskImplementing:
		return model.RoleImplementer
	case c4v1alpha1.TaskReviewing:
		return model.RoleReviewer
	}
	return ""
}

func sandboxName(task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus, role string) string {
	return model.SandboxName(task.Name, int(status.Round), role, int(status.ReviewAttempts)+1, int(status.Reruns))
}

func isFinal(phase c4v1alpha1.TaskPhase) bool {
	return phase == c4v1alpha1.TaskMerged || phase == c4v1alpha1.TaskFailed
}

// failedNoCI reports whether a Task failed because CI never reported on its
// commit: the runner's fault, so it costs its issue no try.
func failedNoCI(status *c4v1alpha1.TaskStatus) bool {
	if status.Phase != c4v1alpha1.TaskFailed {
		return false
	}
	cond := meta.FindStatusCondition(status.Conditions, ConditionSucceeded)
	return cond != nil && cond.Reason == model.ReasonNoCIResult
}

func (r *TaskReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *TaskReconciler) progress(task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	s metav1.ConditionStatus, reason, message string) {
	setCondition(&status.Conditions, task.Generation, ConditionProgressing, s, reason, message)
}

func (r *TaskReconciler) writeStatus(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus) error {
	status.ObservedGeneration = task.Generation
	if status.Phase != task.Status.Phase || status.PhaseChangedAt == nil {
		now := metav1.NewTime(r.now())
		status.PhaseChangedAt = &now
	}
	status.History = recordTransition(status, r.now())
	if equality.Semantic.DeepEqual(&task.Status, status) {
		return nil
	}
	orig := task.DeepCopy()
	task.Status = *status
	// A stale write fails with a conflict and the Task is reconciled again.
	return r.Status().Patch(ctx, task, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{}))
}

// recordTransition adds the Task's current phase, round and Progressing reason to
// its timeline when they changed.
func recordTransition(status *c4v1alpha1.TaskStatus, now time.Time) []c4v1alpha1.TaskTransition {
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
	out := make([]c4v1alpha1.TaskTransition, 0, len(recorded))
	for _, tr := range recorded {
		out = append(out, c4v1alpha1.TaskTransition{At: metav1.NewTime(tr.At), Phase: c4v1alpha1.TaskPhase(tr.Phase),
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
		var tasks c4v1alpha1.TaskList
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
		task, ok := obj.(*c4v1alpha1.Task)
		if !ok {
			return nil
		}
		return containerTasks(ctx, task.Namespace, task.Spec.ContainerRef.Name)
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1alpha1.Task{}).
		Owns(newSandbox()).
		Watches(&corev1.Pod{}, podTask, podFinished()).
		Watches(&c4v1alpha1.Container{}, fromContainer, containerPhaseChanges()).
		Watches(&c4v1alpha1.Task{}, fromTask, taskPhaseChanges()).
		Watches(&c4v1alpha1.Agent{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				var tasks c4v1alpha1.TaskList
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
		Named("task").
		Complete(r)
}

// gitRepo returns the Provider and repository of a Task's Container.
func (r *TaskReconciler) gitRepo(ctx context.Context, container *c4v1alpha1.Container) (gitprovider.Provider,
	gitprovider.Repository, error) {
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, container.Namespace, container.Spec.GitProviderRef.Name)
	if err != nil {
		return nil, gitprovider.Repository{}, err
	}
	return provider, provider.Repository(container.Name), nil
}

func (r *TaskReconciler) gitTimeout() time.Duration {
	if r.GitTimeout > 0 {
		return r.GitTimeout
	}
	return defaultGitTimeout
}

func (r *TaskReconciler) checksRecheck() time.Duration {
	if r.ChecksRecheck > 0 {
		return r.ChecksRecheck
	}
	return defaultChecksRecheck
}
