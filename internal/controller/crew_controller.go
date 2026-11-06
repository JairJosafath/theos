/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
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
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// Labels on Tasks a Crew starts.
const (
	// LabelCrew names the Crew that started a Task.
	LabelCrew = "theos.factory.io/crew"
	// LabelIssue is the number of the issue a Task works on.
	LabelIssue = "theos.factory.io/issue"
)

// ConditionReady says whether a Crew can pick up issues, and why not.
const ConditionReady = "Ready"

// crewWorkers is how many Crews reconcile at once; one Crew never runs twice
// at once.
const crewWorkers = 4

// defaultCrewPoll is how often a Crew reads its Container's issues.
const defaultCrewPoll = time.Minute

// CrewReconciler picks up a Container's ready issues and starts a Task for each,
// up to the Crew's parallel limit, lowest number first, once their dependencies
// are closed.
type CrewReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// GitEvents is the receiver of the Git host's deliveries; nil when it is
	// off, and the reconciler polls alone.
	GitEvents *GitEventsReceiver
	// APIReader reads the Crew, Secrets and Tasks without the cache.
	APIReader client.Reader
	// Git reaches the Git hosts.
	Git GitClient
	// PollInterval is how often a Crew reads the issues; zero means a minute.
	PollInterval time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Recorder emits the Crew's Events about releases; nil emits none.
	Recorder events.EventRecorder
	// Publish reaches the repositories releases are published to. cmd/main.go
	// builds it.
	Publish ReleaseTargets
	// Deployers ship releases by DeployTarget member; publishing a release
	// copies it through the github member's. cmd/main.go builds them.
	Deployers ports.Deployers
	// GoProxy is the Go proxy asked whether go get finds a released module
	// outside theos.local/; "" asks none.
	GoProxy string

	// releaseSaid is, by Crew UID, the reason and message of the last release
	// the Crew tagged or left to a person, so each held one gets one Warning and
	// a cached Crew that lags behind its status doesn't undo a release's
	// condition.
	releaseSaid sync.Map
	// runSaid is, by Crew UID, the tag and result of the last release run
	// reported, so each change gets one Event.
	runSaid sync.Map
	// runErrSaid is, by Crew UID, the last error reading the release run logged.
	runErrSaid sync.Map
	// runLateSaid is, by Crew UID, the tag and re-run time of the last release
	// whose overdue runs were reported.
	runLateSaid sync.Map
	// rerunSaid is, by Crew UID, the last rerun-release value answered: the
	// status write that records it can fail after the re-run.
	rerunSaid sync.Map
	// publishSaid is, by Crew UID, the tag and reason of the last publish
	// failure or success reported, so each gets one Event.
	publishSaid sync.Map
	// targetSaid is, by Crew UID, the target of the last Crew held because
	// its release target differs from its Brief's, so each gets one Event.
	targetSaid sync.Map
	// publishSources is, by Crew UID, the last release's source read from
	// Gitea (sourceRead), so each poll of its publish doesn't download the
	// archive again.
	publishSources sync.Map
	// moduleSaid is, by Crew UID, the tag and outcome of the last Go module
	// publish reported, so each gets one Event or log line.
	moduleSaid sync.Map
	// moduleAnswers is, by Crew UID, the Go proxy's last answer about the
	// last release's module (moduleAnswer).
	moduleAnswers sync.Map
	// moduleWarned is, by Crew UID, the version of the last module reported
	// not fetchable, so each release gets one Warning.
	moduleWarned sync.Map
	// packagesSaid is, by Crew UID, the tag, state and message of the last
	// release packages publish reported, so each gets one log line.
	packagesSaid sync.Map
	// packagesWarned is, by Crew UID, the tag of the last release whose
	// packages were reported Invalid, so each release gets one Warning.
	packagesWarned sync.Map
	// tagged is, by Crew UID, the tag of the last release the Crew tagged, so
	// a cached Crew that lags behind its status doesn't publish, or write the
	// module of, an earlier release over the newer one.
	tagged sync.Map
}

// +kubebuilder:rbac:groups=theos.factory.io,resources=crews,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=theos.factory.io,resources=crews/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=theos.factory.io,resources=crews/finalizers,verbs=update
// +kubebuilder:rbac:groups=theos.factory.io,resources=tasks,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=theos.factory.io,resources=containers;agents;gitproviders;relationships,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

// Reconcile reads the Crew's Container and its ready issues, hands unclear ones
// back to a person, and starts a Task for the next one.
func (r *CrewReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	// Read live: a reconcile queued before the last status write would see the
	// cached Crew without it, and a release whose milestone that write closed
	// would turn back into "No open release is done".
	var crew c4v1beta1.Crew
	if err := r.APIReader.Get(ctx, req.NamespacedName, &crew); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !crew.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	var container c4v1beta1.Container
	err := r.Get(ctx, client.ObjectKey{Namespace: crew.Namespace, Name: crew.Spec.ContainerRef.Name}, &container)
	if apierrors.IsNotFound(err) {
		status := r.observe(ctx, &crew)
		r.wait(&crew, status, "ContainerNotFound", "Container "+crew.Spec.ContainerRef.Name+" not found")
		return ctrl.Result{}, r.writeStatus(ctx, &crew, status)
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	labels := projectLabels(crew.Namespace)
	labels[c4v1beta1.LabelContainer] = container.Name
	if err := adopt(ctx, r.Client, r.Scheme, &container, &crew, labels); err != nil {
		return ctrl.Result{}, err
	}
	// Adopting reads the Crew back, newer than the cache: the status starts
	// from it, or a release written since the read is patched away (#591, #1455).
	status := r.observe(ctx, &crew)
	for _, name := range crewAgentNames(&crew.Spec) {
		var agent c4v1beta1.Agent
		err := r.Get(ctx, client.ObjectKey{Namespace: crew.Namespace, Name: name}, &agent)
		if apierrors.IsNotFound(err) {
			r.wait(&crew, status, "AgentNotFound", "Agent "+name+" not found")
			return ctrl.Result{}, r.writeStatus(ctx, &crew, status)
		}
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	facts := model.CrewFacts{Paused: crew.Spec.Paused, Parallel: int(crew.Spec.Parallel),
		ContainerReady: container.Status.Phase == c4v1beta1.ContainerReady, Routes: crewRoutes(&crew.Spec)}
	// Read live: a Task created a moment ago must count, or the Crew starts two.
	var tasks c4v1beta1.TaskList
	if err := r.APIReader.List(ctx, &tasks, client.InNamespace(crew.Namespace)); err != nil {
		return ctrl.Result{}, err
	}
	var containerTasks []c4v1beta1.Task
	for _, t := range tasks.Items {
		if t.Spec.ContainerRef.Name == container.Name {
			containerTasks = append(containerTasks, t)
		}
	}
	// A Triage Task is no try at its issue, takes no place from parallel and
	// merges nothing a release could hold: the Crew reads them apart.
	triage := model.TriageFacts{On: crew.Spec.Triager != nil, Paused: crew.Spec.Paused,
		ContainerReady: facts.ContainerReady}
	triage.Tasks, containerTasks = triageTasks(containerTasks)
	for i := range containerTasks {
		facts.Tasks = append(facts.Tasks, crewTask(&containerTasks[i]))
	}

	var provider interface {
		ports.GitHost
		ports.Issues
		ports.CI
		ports.Registry
	}
	var repo ports.Repository
	if facts.ContainerReady {
		provider, err = providerFor(ctx, r.Client, r.APIReader, r.Git, crew.Namespace, container.Spec.GitProviderRef.Name)
		if err != nil {
			r.wait(&crew, status, "GitProviderUnavailable", err.Error())
			setCrewPaused(&crew, status, tasks.Items)
			return ctrl.Result{RequeueAfter: r.poll()}, r.writeStatus(ctx, &crew, status)
		}
		repo = provider.Repository(container.Name)
		gitCtx, cancel := context.WithTimeout(ctx, time.Minute)
		err = readIssues(gitCtx, provider, repo, crew.Spec.SpecWriter != nil, &facts, &triage)
		cancel()
		if err != nil {
			log.Error(err, "Failed to read the ready issues", "container", container.Name)
			r.wait(&crew, status, "GitHostUnavailable", "Could not read the issues: "+err.Error())
			setCrewPaused(&crew, status, tasks.Items)
			return ctrl.Result{RequeueAfter: r.poll()}, r.writeStatus(ctx, &crew, status)
		}
		// Before the Crew picks issues: a prepare issue filed now keeps its
		// release the current one, so the next release's work waits for it.
		filed, preparing := r.release(ctx, &crew, &container, status, provider, repo, containerTasks)
		if filed != nil {
			facts.Issues = append(facts.Issues, *filed)
		}
		facts.Preparing = preparing
	}

	step := model.NextCrewStep(container.Name, facts)
	if len(step.NotReady) > 0 {
		handBackIssues(ctx, provider, repo, step.NotReady)
	}
	status.ActiveTasks = slices.Clone(step.Active)
	for _, s := range step.Start {
		task := crewIssueTask(&crew, &container, s)
		if err := r.Create(ctx, task); err != nil && !apierrors.IsAlreadyExists(err) {
			return ctrl.Result{}, err
		}
		log.Info("Started Task for issue", "task", s.Task, "issue", s.Issue, "attempt", s.Attempt, "spec", s.Spec,
			"allowedPaths", s.AllowedPaths, "implementer", task.Spec.AgentRef.Name,
			"reviewer", task.Spec.ReviewerRef.Name)
		status.ActiveTasks = append(status.ActiveTasks, s.Task)
	}
	if err := r.triage(ctx, &crew, &container, triage, status, &step); err != nil {
		return ctrl.Result{}, err
	}

	status.Phase = c4v1beta1.CrewPhase(step.Phase)
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
	setCrewPaused(&crew, status, tasks.Items)
	if !crew.Spec.TagReleases {
		apimeta.RemoveStatusCondition(&status.Conditions, ConditionReleased)
		apimeta.RemoveStatusCondition(&status.Conditions, ConditionModulePublished)
	}
	poll := r.poll()
	if facts.ContainerReady {
		// The poll of the ready issues waits for the host (#1222); a Crew
		// waiting for its Container is woken by the Container's watch.
		poll = hostRecheck(withHostEvents(ctx, r.Client, crew.Namespace, container.Spec.GitProviderRef.Name), poll)
	}
	return ctrl.Result{RequeueAfter: poll}, r.writeStatus(ctx, &crew, status)
}

// handBackIssues gives the issues agents must not take back to a person,
// status/needs-human with the reason, out of status/ready, or
// status/needs-spec for one whose spec they must not write.
func handBackIssues(ctx context.Context, provider ports.Issues, repo ports.Repository,
	rejections []model.IssueRejection) {
	gitCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	for _, n := range rejections {
		was := model.LabelReady
		if n.Spec {
			was = model.LabelNeedsSpec
		}
		notes := []error{
			provider.AddIssueLabel(gitCtx, repo, n.Issue, model.LabelNeedsHuman),
			// The exclusive scope drops it too, but not where the labels came
			// over non-exclusive from a migration (#2039).
			provider.RemoveIssueLabel(gitCtx, repo, n.Issue, was),
			provider.CommentIssue(gitCtx, repo, n.Issue, n.Reason),
		}
		for _, err := range notes {
			if err != nil {
				logf.FromContext(ctx).Error(err, "Failed to hand an issue back", "issue", n.Issue)
			}
		}
	}
}

// crewIssueTask is the Task a Crew starts for an issue, on the Agents its
// route gave it, else the Crew's own: a Spec Task, written by the Crew's spec
// writer, for an issue that needs a spec; and, with a verifier, a build the
// verifier runs once the reviewer approved it.
func crewIssueTask(crew *c4v1beta1.Crew, container *c4v1beta1.Container, s model.IssueStart) *c4v1beta1.Task {
	implementer, reviewer := crew.Spec.Implementer.Name, crew.Spec.Reviewer.Name
	if s.Implementer != "" {
		implementer = s.Implementer
	}
	if s.Reviewer != "" {
		reviewer = s.Reviewer
	}
	var kind c4v1beta1.TaskKind
	if s.Spec && crew.Spec.SpecWriter != nil {
		kind, implementer = c4v1beta1.TaskKindSpec, crew.Spec.SpecWriter.Name
	}
	// A spec and a Standard change are not verified; a route's verifier
	// verifies its issues even when the Crew has none.
	var verifier *c4v1beta1.LocalRef
	switch {
	case s.Spec || len(s.AllowedPaths) > 0:
	case s.Verifier != "":
		verifier = &c4v1beta1.LocalRef{Name: s.Verifier}
	case crew.Spec.Verifier != nil:
		verifier = &c4v1beta1.LocalRef{Name: crew.Spec.Verifier.Name}
	}
	return &c4v1beta1.Task{
		ObjectMeta: metav1.ObjectMeta{Namespace: crew.Namespace, Name: s.Task, Labels: map[string]string{
			LabelCrew: crew.Name, LabelIssue: strconv.Itoa(s.Issue)}},
		Spec: c4v1beta1.TaskSpec{
			Kind:         kind,
			ContainerRef: c4v1beta1.LocalRef{Name: container.Name},
			AgentRef:     c4v1beta1.LocalRef{Name: implementer},
			ReviewerRef:  &c4v1beta1.LocalRef{Name: reviewer},
			VerifierRef:  verifier,
			Issue:        int32(s.Issue),
			// Set only for a Standard change, from the Task that filed it.
			AllowedProtectedPaths: s.AllowedPaths,
		},
	}
}

// release re-runs the last release's failed runs when asked, watches its run,
// prepares or tags the next release, reports the last release's overdue runs,
// publishes its Go module, files its bumps and publishes it, as the Crew's
// switches say. It returns the prepare issue filed now, if any, and the
// release whose prepare issue waits for the Tasks at work, if any.
func (r *CrewReconciler) release(ctx context.Context, crew *c4v1beta1.Crew, container *c4v1beta1.Container,
	status *c4v1beta1.CrewStatus, provider interface {
		ports.GitHost
		ports.Issues
		ports.CI
		ports.Registry
	}, repo ports.Repository,
	tasks []c4v1beta1.Task) (*model.Issue, string) {
	if crew.Spec.TagReleases {
		r.rerunRelease(ctx, crew, status, provider, repo)
		r.watchReleaseRun(ctx, crew, status, provider, repo)
	}
	var filed *model.Issue
	var preparing string
	if crew.Spec.TagReleases || crew.Spec.PrepareReleases {
		filed, preparing = r.tagRelease(ctx, crew, container, status, provider, repo, tasks)
	}
	if crew.Spec.TagReleases {
		r.noteOverdueRun(ctx, crew, status)
	}
	r.publishRelease(ctx, crew, container, status, provider, repo, tasks)
	if crew.Spec.TagReleases {
		// After publishing, so the module reads a source tag put out this poll,
		// and a release published this poll files its bumps.
		r.publishGoModule(ctx, crew, status, provider, repo)
		r.publishPackages(ctx, crew, status, provider, repo)
		r.fileBumps(ctx, crew, status, repo)
	}
	return filed, preparing
}

// crewAgentNames names every Agent a Crew may start a Task with: its own two,
// its triager, its spec writer, its verifier and those its routes name, each
// once.
func crewAgentNames(spec *c4v1beta1.CrewSpec) []string {
	names := make([]string, 0, 5+3*len(spec.Routes))
	add := func(ref *c4v1beta1.LocalRef) {
		if ref != nil && !slices.Contains(names, ref.Name) {
			names = append(names, ref.Name)
		}
	}
	add(&spec.Implementer)
	add(&spec.Reviewer)
	add(spec.Triager)
	add(spec.SpecWriter)
	add(spec.Verifier)
	for _, route := range spec.Routes {
		add(route.Implementer)
		add(route.Reviewer)
		add(route.Verifier)
	}
	return names
}

// crewRoutes are a Crew's routes as the model reads them.
func crewRoutes(spec *c4v1beta1.CrewSpec) []model.AgentRoute {
	out := make([]model.AgentRoute, 0, len(spec.Routes))
	for _, route := range spec.Routes {
		r := model.AgentRoute{Label: route.Label}
		if route.Implementer != nil {
			r.Implementer = route.Implementer.Name
		}
		if route.Reviewer != nil {
			r.Reviewer = route.Reviewer.Name
		}
		if route.Verifier != nil {
			r.Verifier = route.Verifier.Name
		}
		out = append(out, r)
	}
	return out
}

// crewTask is what a Crew reads of a Task: its issue, whether it ended or waits on
// a Standard change or a person's approval, whether it writes a spec, and the
// Standard change it filed, which alone grants an allowance.
func crewTask(t *c4v1beta1.Task) model.CrewTask {
	out := model.CrewTask{Name: t.Name, Issue: int(t.Spec.Issue), Final: isFinal(t.Status.Phase),
		Failed: t.Status.Phase == c4v1beta1.TaskFailed, NoCI: failedNoCI(&t.Status),
		Unseen: failedUnseen(&t.Status),
		// A spec waiting for a person runs no agent: like a Task waiting on a
		// Standard change, it takes no place from parallel.
		Blocked: t.Status.Phase == c4v1beta1.TaskBlocked || t.Status.Phase == c4v1beta1.TaskAwaitingApproval,
		Spec:    isSpec(t)}
	if change := t.Status.StandardChange; change != nil {
		out.StandardChange = &model.IssueChange{Issue: int(change.Issue), Paths: slices.Clone(change.Paths)}
	}
	return out
}

// readyIssues reads the open issues labelled ready, what blocks each, and which
// are Standard changes.
func readyIssues(ctx context.Context, provider ports.Issues, repo ports.Repository) ([]model.Issue, error) {
	if err := provider.EnsureLabels(ctx, repo, issueLabels()); err != nil {
		return nil, err
	}
	return labelledIssues(ctx, provider, repo, model.LabelReady)
}

// specIssues reads the open issues labelled status/needs-spec, and what
// blocks each, for a Crew's spec writer.
func specIssues(ctx context.Context, provider ports.Issues, repo ports.Repository) ([]model.Issue, error) {
	issues, err := labelledIssues(ctx, provider, repo, model.LabelNeedsSpec)
	for i := range issues {
		issues[i].Spec = true
	}
	return issues, err
}

// labelledIssues reads the open issues labelled label, as a Crew picks them.
func labelledIssues(ctx context.Context, provider ports.Issues, repo ports.Repository,
	label string) ([]model.Issue, error) {
	issues, err := provider.ListIssues(ctx, repo, label)
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
			Urgent: slices.Contains(i.Labels, model.LabelUrgent), Prepare: slices.Contains(i.Labels, model.LabelRelease),
			Labels: slices.Clone(i.Labels)})
	}
	return out, nil
}

// readIssues reads what a Crew picks from: the ready issues, with a spec
// writer those that need a spec, the releases of its Tasks' issues, and, with
// a triager, the proposed issues.
func readIssues(ctx context.Context, provider ports.Issues, repo ports.Repository, specs bool,
	facts *model.CrewFacts, triage *model.TriageFacts) error {
	var err error
	if facts.Issues, err = readyIssues(ctx, provider, repo); err != nil {
		return err
	}
	if specs {
		spec, err := specIssues(ctx, provider, repo)
		if err != nil {
			return err
		}
		facts.Issues = append(facts.Issues, spec...)
	}
	if err := taskReleases(ctx, provider, repo, facts.Tasks, facts.Issues); err != nil {
		return err
	}
	if triage.On {
		triage.Proposed, err = proposedIssues(ctx, provider, repo)
	}
	return err
}

// triage starts the Triage Task model.NextTriage starts, if any, and adds
// the Triage Task at work, or started, to the Crew's step and active Tasks.
func (r *CrewReconciler) triage(ctx context.Context, crew *c4v1beta1.Crew, container *c4v1beta1.Container,
	facts model.TriageFacts, status *c4v1beta1.CrewStatus, step *model.CrewStep) error {
	triaging := model.NextTriage(container.Name, facts)
	if err := r.startTriage(ctx, crew, container, triaging.Start); err != nil {
		return err
	}
	switch {
	case triaging.Start != nil:
		status.ActiveTasks = append(status.ActiveTasks, triaging.Start.Task)
	case triaging.Active != "":
		status.ActiveTasks = append(status.ActiveTasks, triaging.Active)
	}
	step.AddTriage(triaging)
	return nil
}

// proposedIssues reads the open issues labelled status/proposed, a Crew's
// triager's to decide.
func proposedIssues(ctx context.Context, provider ports.Issues, repo ports.Repository) ([]model.ProposedIssue, error) {
	issues, err := provider.ListIssues(ctx, repo, model.LabelProposed)
	if err != nil {
		return nil, err
	}
	out := make([]model.ProposedIssue, 0, len(issues))
	for _, i := range issues {
		out = append(out, model.ProposedIssue{Number: i.Number, Milestone: i.MilestoneTitle})
	}
	return out, nil
}

// startTriage creates the Triage Task model.NextTriage starts, if any: the
// Crew's triager on the issue, with no reviewer.
func (r *CrewReconciler) startTriage(ctx context.Context, crew *c4v1beta1.Crew, container *c4v1beta1.Container,
	start *model.TriageStart) error {
	if start == nil || crew.Spec.Triager == nil {
		return nil
	}
	task := &c4v1beta1.Task{
		ObjectMeta: metav1.ObjectMeta{Namespace: crew.Namespace, Name: start.Task, Labels: map[string]string{
			LabelCrew: crew.Name, LabelIssue: strconv.Itoa(start.Issue)}},
		Spec: c4v1beta1.TaskSpec{
			Kind:         c4v1beta1.TaskTriage,
			ContainerRef: c4v1beta1.LocalRef{Name: container.Name},
			AgentRef:     c4v1beta1.LocalRef{Name: crew.Spec.Triager.Name},
			Issue:        int32(start.Issue),
		},
	}
	if err := r.Create(ctx, task); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	logf.FromContext(ctx).Info("Started Triage Task for issue", "task", start.Task, "issue", start.Issue,
		"attempt", start.Attempt, "triager", crew.Spec.Triager.Name)
	return nil
}

// taskReleases sets the release of each active Task's issue, so that a release
// with work in progress holds back the later ones, and whether it is a
// release's prepare issue, which runs alone.
func taskReleases(ctx context.Context, provider ports.Issues, repo ports.Repository,
	tasks []model.CrewTask, ready []model.Issue) error {
	for i, t := range tasks {
		if t.Final || t.Issue == 0 {
			continue
		}
		// A Task created a moment ago may still find its issue labelled ready.
		if j := slices.IndexFunc(ready, func(issue model.Issue) bool { return issue.Number == t.Issue }); j >= 0 {
			tasks[i].Release, tasks[i].Prepare = ready[j].Release, ready[j].Prepare
			continue
		}
		issue, err := provider.GetIssue(ctx, repo, t.Issue)
		if err != nil {
			return fmt.Errorf("issue #%d of Task %s: %w", t.Issue, t.Name, err)
		}
		tasks[i].Release, tasks[i].Prepare = release(issue), slices.Contains(issue.Labels, model.LabelRelease)
	}
	return nil
}

// release is an issue's milestone as a release; nil when it has no due date.
func release(issue ports.Issue) *model.Release {
	if issue.MilestoneDue.IsZero() {
		return nil
	}
	return &model.Release{Title: issue.MilestoneTitle, Due: issue.MilestoneDue}
}

func (r *CrewReconciler) wait(crew *c4v1beta1.Crew, status *c4v1beta1.CrewStatus, reason, message string) {
	status.Phase = c4v1beta1.CrewWaiting
	setCondition(&status.Conditions, crew.Generation, ConditionReady, metav1.ConditionFalse, reason, message)
}

// observe starts the status this reconcile writes from the Crew as read:
// its generation observed, its release target checked.
func (r *CrewReconciler) observe(ctx context.Context, crew *c4v1beta1.Crew) *c4v1beta1.CrewStatus {
	status := crew.Status.DeepCopy()
	status.ObservedGeneration = crew.Generation
	r.releaseTarget(ctx, crew, status)
	return status
}

func (r *CrewReconciler) writeStatus(ctx context.Context, crew *c4v1beta1.Crew, status *c4v1beta1.CrewStatus) error {
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

// SetupWithManager sets up the controller with the Manager. A Task ending, the
// Container becoming Ready, or an Agent coming or going wakes the Container's
// Crews at once; otherwise they poll the Git host.
func (r *CrewReconciler) SetupWithManager(mgr ctrl.Manager) error {
	crewsOf := func(ctx context.Context, ns, container string) []reconcile.Request {
		var crews c4v1beta1.CrewList
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
		For(&c4v1beta1.Crew{}).
		Watches(&c4v1beta1.Container{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				return crewsOf(ctx, obj.GetNamespace(), obj.GetName())
			}), containerPhaseChanges()).
		Watches(&c4v1beta1.Task{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				task, ok := obj.(*c4v1beta1.Task)
				if !ok {
					return nil
				}
				return crewsOf(ctx, task.Namespace, task.Spec.ContainerRef.Name)
			}), taskPhaseOrPausedChanges()).
		// A Crew read before its Agents were in the cache waits on AgentNotFound
		// with no requeue; their creation wakes it.
		Watches(&c4v1beta1.Agent{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				var crews c4v1beta1.CrewList
				if err := mgr.GetClient().List(ctx, &crews, client.InNamespace(obj.GetNamespace())); err != nil {
					return nil
				}
				var reqs []reconcile.Request
				for _, c := range crews.Items {
					if slices.Contains(crewAgentNames(&c.Spec), obj.GetName()) {
						reqs = append(reqs, requestFor(c.Namespace, c.Name))
					}
				}
				return reqs
			}), createOrDelete()).
		// A Crew uploading a release file can take minutes; the others go on.
		WithOptions(controller.Options{MaxConcurrentReconciles: crewWorkers}).
		// A delivery of the Git host reconciles the Crews of the Container whose repository a delivery names at once
		// (docs/plans/git-events.md, decision 7).
		WatchesRawSource(r.GitEvents.Source(gitEventCrewRequests(mgr.GetClient()))).
		Named("crew").
		Complete(r)
}

// setCrewPaused sets a paused Crew's Paused condition to its Tasks still
// running, or, when it pauses their runs too, to those whose agent still runs
// (step 12 of docs/plans/self-hosting.md); False once the Crew is resumed. A
// Crew never paused has none.
func setCrewPaused(crew *c4v1beta1.Crew, status *c4v1beta1.CrewStatus, all []c4v1beta1.Task) {
	if !crew.Spec.Paused {
		if apimeta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionPaused) != nil {
			setCondition(&status.Conditions, crew.Generation, c4v1beta1.ConditionPaused, metav1.ConditionFalse,
				reasonBriefResumed, "Not paused")
		}
		return
	}
	var tasks []*c4v1beta1.Task
	for i := range all {
		if all[i].Labels[LabelCrew] == crew.Name {
			tasks = append(tasks, &all[i])
		}
	}
	slices.SortFunc(tasks, func(a, b *c4v1beta1.Task) int { return strings.Compare(a.Name, b.Name) })
	setCondition(&status.Conditions, crew.Generation, c4v1beta1.ConditionPaused, metav1.ConditionTrue,
		reasonBriefPaused, pausedConditionMessage(tasks, crew.Spec.PauseRuns))
}
