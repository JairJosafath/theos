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
	"slices"
	"strconv"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// Labels on Tasks a Crew starts.
const (
	// LabelCrew names the Crew that started a Task.
	LabelCrew = "c4.factory.io/crew"
	// LabelIssue is the number of the issue a Task works on.
	LabelIssue = "c4.factory.io/issue"
)

// ConditionReady says whether a Crew can pick up issues, and why not.
const ConditionReady = "Ready"

// defaultCrewPoll is how often a Crew reads its Container's issues.
const defaultCrewPoll = time.Minute

// CrewReconciler picks up a Container's ready issues and starts a Task for each,
// up to the Crew's parallel limit, lowest number first, once their dependencies
// are closed.
type CrewReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader reads Secrets and Tasks without the cache.
	APIReader client.Reader
	// Git reaches the Git hosts.
	Git GitClient
	// PollInterval is how often a Crew reads the issues; zero means a minute.
	PollInterval time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Recorder emits the Crew's Events about releases; nil emits none.
	Recorder events.EventRecorder

	// releaseSaid is, by Crew UID, the reason and message of the last release
	// the Crew tagged or left to a person, so each held one gets one Warning and
	// a cached Crew that lags behind its status doesn't undo a release's
	// condition.
	releaseSaid sync.Map
}

// +kubebuilder:rbac:groups=c4.factory.io,resources=crews,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=c4.factory.io,resources=crews/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=c4.factory.io,resources=crews/finalizers,verbs=update
// +kubebuilder:rbac:groups=c4.factory.io,resources=tasks,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=c4.factory.io,resources=containers;agents;gitproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile reads the Crew's Container and its ready issues, hands unclear ones
// back to a person, and starts a Task for the next one.
func (r *CrewReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	var crew c4v1alpha1.Crew
	if err := r.Get(ctx, req.NamespacedName, &crew); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !crew.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	status := crew.Status.DeepCopy()
	status.ObservedGeneration = crew.Generation

	var container c4v1alpha1.Container
	err := r.Get(ctx, client.ObjectKey{Namespace: crew.Namespace, Name: crew.Spec.ContainerRef.Name}, &container)
	if apierrors.IsNotFound(err) {
		r.wait(&crew, status, "ContainerNotFound", "Container "+crew.Spec.ContainerRef.Name+" not found")
		return ctrl.Result{}, r.writeStatus(ctx, &crew, status)
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	labels := projectLabels(crew.Namespace)
	labels[c4v1alpha1.LabelContainer] = container.Name
	if err := adopt(ctx, r.Client, r.Scheme, &container, &crew, labels); err != nil {
		return ctrl.Result{}, err
	}
	for _, ref := range []c4v1alpha1.LocalRef{crew.Spec.Implementer, crew.Spec.Reviewer} {
		var agent c4v1alpha1.Agent
		err := r.Get(ctx, client.ObjectKey{Namespace: crew.Namespace, Name: ref.Name}, &agent)
		if apierrors.IsNotFound(err) {
			r.wait(&crew, status, "AgentNotFound", "Agent "+ref.Name+" not found")
			return ctrl.Result{}, r.writeStatus(ctx, &crew, status)
		}
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	facts := model.CrewFacts{Paused: crew.Spec.Paused, Parallel: int(crew.Spec.Parallel),
		ContainerReady: container.Status.Phase == c4v1alpha1.ContainerReady}
	// Read live: a Task created a moment ago must count, or the Crew starts two.
	var tasks c4v1alpha1.TaskList
	if err := r.APIReader.List(ctx, &tasks, client.InNamespace(crew.Namespace)); err != nil {
		return ctrl.Result{}, err
	}
	var containerTasks []c4v1alpha1.Task
	for _, t := range tasks.Items {
		if t.Spec.ContainerRef.Name == container.Name {
			facts.Tasks = append(facts.Tasks, crewTask(&t))
			containerTasks = append(containerTasks, t)
		}
	}

	var provider gitprovider.Provider
	var repo gitprovider.Repository
	if facts.ContainerReady {
		provider, err = providerFor(ctx, r.Client, r.APIReader, r.Git, crew.Namespace, container.Spec.GitProviderRef.Name)
		if err != nil {
			r.wait(&crew, status, "GitProviderUnavailable", err.Error())
			return ctrl.Result{RequeueAfter: r.poll()}, r.writeStatus(ctx, &crew, status)
		}
		repo = provider.Repository(container.Name)
		gitCtx, cancel := context.WithTimeout(ctx, time.Minute)
		facts.Issues, err = readyIssues(gitCtx, provider, repo)
		if err == nil {
			err = taskReleases(gitCtx, provider, repo, facts.Tasks, facts.Issues)
		}
		cancel()
		if err != nil {
			log.Error(err, "Failed to read the ready issues", "container", container.Name)
			r.wait(&crew, status, "GitHostUnavailable", "Could not read the issues: "+err.Error())
			return ctrl.Result{RequeueAfter: r.poll()}, r.writeStatus(ctx, &crew, status)
		}
	}

	step := model.NextCrewStep(container.Name, facts)
	if len(step.NotReady) > 0 {
		gitCtx, cancel := context.WithTimeout(ctx, time.Minute)
		for _, n := range step.NotReady {
			notes := []error{
				provider.AddIssueLabel(gitCtx, repo, n.Issue, model.LabelNeedsHuman),
				provider.CommentIssue(gitCtx, repo, n.Issue, n.Reason),
			}
			for _, err := range notes {
				if err != nil {
					log.Error(err, "Failed to hand an issue back", "issue", n.Issue)
				}
			}
		}
		cancel()
	}
	status.ActiveTasks = slices.Clone(step.Active)
	for _, s := range step.Start {
		task := &c4v1alpha1.Task{
			ObjectMeta: metav1.ObjectMeta{Namespace: crew.Namespace, Name: s.Task, Labels: map[string]string{
				LabelCrew: crew.Name, LabelIssue: strconv.Itoa(s.Issue)}},
			Spec: c4v1alpha1.TaskSpec{
				ContainerRef: c4v1alpha1.LocalRef{Name: container.Name},
				AgentRef:     crew.Spec.Implementer,
				ReviewerRef:  &c4v1alpha1.LocalRef{Name: crew.Spec.Reviewer.Name},
				Issue:        int32(s.Issue),
				// Set only for a Standard change, from the Task that filed it.
				AllowedProtectedPaths: s.AllowedPaths,
			},
		}
		if err := r.Create(ctx, task); err != nil && !apierrors.IsAlreadyExists(err) {
			return ctrl.Result{}, err
		}
		log.Info("Started Task for issue", "task", s.Task, "issue", s.Issue, "attempt", s.Attempt,
			"allowedPaths", s.AllowedPaths)
		status.ActiveTasks = append(status.ActiveTasks, s.Task)
	}

	status.Phase = c4v1alpha1.CrewPhase(step.Phase)
	status.Ready, status.Waiting = int32s(step.Ready), int32s(step.Waiting)
	if facts.ContainerReady {
		now := metav1.NewTime(r.now())
		status.LastPolled = &now
	}
	ready := metav1.ConditionTrue
	reason := step.Phase
	if step.Phase == model.CrewWaiting {
		ready, reason = metav1.ConditionFalse, "ContainerNotReady"
	}
	setCondition(&status.Conditions, crew.Generation, ConditionReady, ready, reason, step.Message)
	if !crew.Spec.TagReleases {
		apimeta.RemoveStatusCondition(&status.Conditions, ConditionReleased)
	}
	if (crew.Spec.TagReleases || crew.Spec.PrepareReleases) && facts.ContainerReady {
		r.tagRelease(ctx, &crew, status, provider, repo, containerTasks)
	}
	return ctrl.Result{RequeueAfter: r.poll()}, r.writeStatus(ctx, &crew, status)
}

// crewTask is what a Crew reads of a Task: its issue, whether it ended or waits on
// a Standard change, and the Standard change it filed, which alone grants an
// allowance.
func crewTask(t *c4v1alpha1.Task) model.CrewTask {
	out := model.CrewTask{Name: t.Name, Issue: int(t.Spec.Issue), Final: isFinal(t.Status.Phase),
		Failed: t.Status.Phase == c4v1alpha1.TaskFailed, Blocked: t.Status.Phase == c4v1alpha1.TaskBlocked}
	if change := t.Status.StandardChange; change != nil {
		out.StandardChange = &model.IssueChange{Issue: int(change.Issue), Paths: slices.Clone(change.Paths)}
	}
	return out
}

// readyIssues reads the open issues labelled ready, what blocks each, and which
// are Standard changes.
func readyIssues(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository) ([]model.Issue, error) {
	if err := provider.EnsureLabels(ctx, repo, issueLabels()); err != nil {
		return nil, err
	}
	issues, err := provider.ListIssues(ctx, repo, model.LabelReady)
	if err != nil {
		return nil, err
	}
	out := make([]model.Issue, 0, len(issues))
	for _, i := range issues {
		blockers, err := provider.OpenBlockers(ctx, repo, i.Number)
		if err != nil {
			return nil, fmt.Errorf("issue #%d: %w", i.Number, err)
		}
		out = append(out, model.Issue{Number: i.Number, Title: i.Title, Body: i.Body, OpenBlockers: blockers,
			StandardChange: slices.Contains(i.Labels, model.LabelStandardChange), Release: release(i),
			Urgent: slices.Contains(i.Labels, model.LabelUrgent)})
	}
	return out, nil
}

// taskReleases sets the release of each active Task's issue, so that a release
// with work in progress holds back the later ones.
func taskReleases(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	tasks []model.CrewTask, ready []model.Issue) error {
	for i, t := range tasks {
		if t.Final || t.Issue == 0 {
			continue
		}
		// A Task created a moment ago may still find its issue labelled ready.
		if j := slices.IndexFunc(ready, func(issue model.Issue) bool { return issue.Number == t.Issue }); j >= 0 {
			tasks[i].Release = ready[j].Release
			continue
		}
		issue, err := provider.GetIssue(ctx, repo, t.Issue)
		if err != nil {
			return fmt.Errorf("issue #%d of Task %s: %w", t.Issue, t.Name, err)
		}
		tasks[i].Release = release(issue)
	}
	return nil
}

// release is an issue's milestone as a release; nil when it has no due date.
func release(issue gitprovider.Issue) *model.Release {
	if issue.MilestoneDue.IsZero() {
		return nil
	}
	return &model.Release{Title: issue.MilestoneTitle, Due: issue.MilestoneDue}
}

func (r *CrewReconciler) wait(crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus, reason, message string) {
	status.Phase = c4v1alpha1.CrewWaiting
	setCondition(&status.Conditions, crew.Generation, ConditionReady, metav1.ConditionFalse, reason, message)
}

func (r *CrewReconciler) writeStatus(ctx context.Context, crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus) error {
	if equality.Semantic.DeepEqual(&crew.Status, status) {
		return nil
	}
	orig := crew.DeepCopy()
	crew.Status = *status
	return r.Status().Patch(ctx, crew, client.MergeFrom(orig))
}

func (r *CrewReconciler) poll() time.Duration {
	if r.PollInterval > 0 {
		return r.PollInterval
	}
	return defaultCrewPoll
}

func (r *CrewReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func int32s(in []int) []int32 {
	if len(in) == 0 {
		return nil
	}
	out := make([]int32, 0, len(in))
	for _, n := range in {
		out = append(out, int32(n))
	}
	return out
}

// SetupWithManager sets up the controller with the Manager. A Task ending, or the
// Container becoming Ready, wakes the Container's Crews at once; otherwise they
// poll the Git host.
func (r *CrewReconciler) SetupWithManager(mgr ctrl.Manager) error {
	crewsOf := func(ctx context.Context, ns, container string) []reconcile.Request {
		var crews c4v1alpha1.CrewList
		if err := mgr.GetClient().List(ctx, &crews, client.InNamespace(ns),
			client.MatchingFields{indexContainer: container}); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(crews.Items))
		for _, c := range crews.Items {
			reqs = append(reqs, requestFor(c.Namespace, c.Name))
		}
		return reqs
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1alpha1.Crew{}).
		Watches(&c4v1alpha1.Container{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				return crewsOf(ctx, obj.GetNamespace(), obj.GetName())
			}), containerPhaseChanges()).
		Watches(&c4v1alpha1.Task{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				task, ok := obj.(*c4v1alpha1.Task)
				if !ok {
					return nil
				}
				return crewsOf(ctx, task.Namespace, task.Spec.ContainerRef.Name)
			}), taskPhaseChanges()).
		Named("crew").
		Complete(r)
}
