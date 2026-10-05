/*
Copyright 2026.

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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
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
	// mergeCommitRetry: a Merged Task whose merge commit couldn't be read tries again.
	mergeCommitRetry = time.Minute
)

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
	// MaxSandboxes caps the agent sandboxes running at once, across all projects;
	// zero means no cap. Agents and their checks are heavy.
	MaxSandboxes int
	// WorkspaceMemory, when set, gives each sandbox a workspace in memory of up to
	// this size instead of on the node's disk.
	WorkspaceMemory *resource.Quantity
	// SandboxGoCache, when set, is a directory on the node that sandboxes share as
	// their Go build cache.
	SandboxGoCache string
}

// +kubebuilder:rbac:groups=c4.factory.io,resources=tasks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=c4.factory.io,resources=tasks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=c4.factory.io,resources=tasks/finalizers,verbs=update
// +kubebuilder:rbac:groups=c4.factory.io,resources=agents;softwaresystems,verbs=get;list;watch
// +kubebuilder:rbac:groups=agents.x-k8s.io,resources=sandboxes,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;update;patch

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
	if isFinal(status.Phase) {
		if status.Phase != c4v1alpha1.TaskMerged || status.MergeCommit != "" || status.PullRequest == 0 {
			return ctrl.Result{}, r.writeStatus(ctx, &task, status)
		}
		// Merged before the Task recorded merge commits, or the read failed: a Crew
		// waits for it to tag the release.
		if provider, repo, err := r.gitRepo(ctx, &container); err == nil {
			status.MergeCommit = r.mergeCommit(ctx, provider, repo, status)
		}
		var result ctrl.Result
		if status.MergeCommit == "" {
			result.RequeueAfter = mergeCommitRetry
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
	return result, r.writeStatus(ctx, &task, status)
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

	if run := facts.Run; status.Phase == c4v1alpha1.TaskReviewing && run.Done && run.Result != nil &&
		run.Result.Verdict == model.VerdictApprove && status.PullRequest > 0 {
		conflicted, err := r.conflicted(ctx, container, status)
		if err != nil {
			return facts, err
		}
		facts.Conflicted = conflicted
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
	}
	return facts, nil
}

// noFreeSlot reports whether the Task must wait for an agent slot: all are taken,
// or a Task that has waited longer goes first.
func (r *TaskReconciler) noFreeSlot(ctx context.Context, task *c4v1alpha1.Task) (bool, error) {
	sandboxes := &unstructured.UnstructuredList{}
	sandboxes.SetGroupVersionKind(SandboxGVK.GroupVersion().WithKind("SandboxList"))
	// Counted live: the cache may not have the sandbox started a moment ago.
	if err := r.APIReader.List(ctx, sandboxes, client.MatchingLabels{LabelAgentSandbox: agentSandboxValue}); err != nil {
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
	free := r.MaxSandboxes - len(sandboxes.Items)
	return !model.HasSlotTurn(task.Namespace+"/"+task.Name, r.now(), waiting, free), nil
}

// conflicted reports whether the Task's pull request can no longer merge
// because main moved with conflicting changes.
func (r *TaskReconciler) conflicted(ctx context.Context, container *c4v1alpha1.Container,
	status *c4v1alpha1.TaskStatus) (bool, error) {
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return false, err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	pr, err := provider.GetPullRequest(gitCtx, repo, int(status.PullRequest))
	if err != nil {
		return false, err
	}
	return pr.State == "open" && !pr.Merged && !pr.Mergeable, nil
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
