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
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	// taskSecretRecheck: Secrets are read uncached, so not watched.
	taskSecretRecheck = 15 * time.Second
	// slotRecheck: a Task waiting for a free agent slot looks again.
	slotRecheck = 20 * time.Second
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
	// MaxSandboxes caps the agent sandboxes running at once, across all projects;
	// zero means no cap. Agents and their checks are heavy.
	MaxSandboxes int
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
		return ctrl.Result{}, r.writeStatus(ctx, &task, status)
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

	facts, err := r.facts(ctx, &task, status, &container, &agent)
	if err != nil {
		return ctrl.Result{}, err
	}
	step := model.NextTaskStep(facts)
	logf.FromContext(ctx).V(1).Info("Took Task step", "phase", status.Phase, "round", status.Round,
		"action", step.Action, "message", step.Message)
	result, err := r.apply(ctx, &task, status, &container, &agent, step)
	if err != nil {
		return result, err
	}
	if facts.NoFreeSlot && step.Action == model.ActionWait {
		// Sandboxes of other Tasks don't wake this one; look again.
		result.RequeueAfter = slotRecheck
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
		ReviewAttempts: int(status.ReviewAttempts),
	}
	if status.PhaseChangedAt != nil {
		facts.InPhase = r.now().Sub(status.PhaseChangedAt.Time)
	}
	if r.MaxSandboxes > 0 {
		sandboxes := &unstructured.UnstructuredList{}
		sandboxes.SetGroupVersionKind(SandboxGVK.GroupVersion().WithKind("SandboxList"))
		if err := r.List(ctx, sandboxes, client.MatchingLabels{LabelAgentSandbox: agentSandboxValue}); err != nil {
			return facts, err
		}
		facts.NoFreeSlot = len(sandboxes.Items) >= r.MaxSandboxes
	}
	if facts.MaxRounds == 0 {
		facts.MaxRounds = 3
	}

	if status.Phase == c4v1alpha1.TaskPending {
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
		run, err := runState(ctx, r.Client, task.Namespace, sandboxName(task, status, role))
		if err != nil {
			return facts, err
		}
		facts.Run = run
	}

	if status.Phase == c4v1alpha1.TaskChecking && status.Commit != "" {
		provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, task.Namespace, container.Spec.GitProviderRef.Name)
		if err != nil {
			return facts, err
		}
		gitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		checks, err := provider.CombinedStatus(gitCtx, provider.Repository(container.Name), status.Commit)
		if err != nil {
			return facts, err
		}
		status.Checks = &c4v1alpha1.CheckStatus{Commit: checks.Commit, State: checks.State, URL: checks.URL}
		facts.Checks, facts.ChecksURL = checks.State, checks.URL
	}
	return facts, nil
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
	return model.SandboxName(task.Name, int(status.Round), role, int(status.ReviewAttempts)+1)
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
	if equality.Semantic.DeepEqual(&task.Status, status) {
		return nil
	}
	orig := task.DeepCopy()
	task.Status = *status
	// A stale write fails with a conflict and the Task is reconciled again.
	return r.Status().Patch(ctx, task, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{}))
}

// taskContext says what the application is for, from its SoftwareSystem and
// Container, for the agents' prompts.
func (r *TaskReconciler) taskContext(ctx context.Context, container *c4v1alpha1.Container) string {
	var b strings.Builder
	var system c4v1alpha1.SoftwareSystem
	if err := r.Get(ctx, client.ObjectKey{Namespace: container.Namespace, Name: container.Spec.SoftwareSystemRef.Name},
		&system); err == nil {
		fmt.Fprintf(&b, "System: %s", orName(system.Spec.DisplayName, system.Name))
		if system.Spec.Description != "" {
			fmt.Fprintf(&b, " — %s", system.Spec.Description)
		}
		b.WriteString("\n")
		for _, need := range system.Spec.Needs {
			fmt.Fprintf(&b, "Need: %s\n", need)
		}
	}
	fmt.Fprintf(&b, "Application: %s (%s)", orName(container.Spec.DisplayName, container.Name), container.Spec.Type)
	if container.Spec.Description != "" {
		fmt.Fprintf(&b, " — %s", container.Spec.Description)
	}
	return b.String()
}

func orName(display, name string) string {
	if display != "" {
		return display
	}
	return name
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
					if t.Spec.AgentRef.Name == obj.GetName() && !isFinal(t.Status.Phase) {
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

func (r *TaskReconciler) checksRecheck() time.Duration {
	if r.ChecksRecheck > 0 {
		return r.ChecksRecheck
	}
	return defaultChecksRecheck
}
