/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// BriefReconciler turns a Brief's idea and prompts into the model, a round at
// a time (docs/plans/done/brief.md): it keeps the Brief's repository, the planner's
// workspace, up to date, starts a Plan Task per round, checks its plan,
// applies it, makes the new Containers' Crews and files their first issues.
type BriefReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// GitEvents is the receiver of the Git host's deliveries; nil when it is
	// off, and the reconciler polls alone.
	GitEvents *GitEventsReceiver
	// APIReader reads Secrets without caching them.
	APIReader client.Reader
	Git       GitClient
	// Recorder emits the Brief's Events: Planned, NeedsInput, PlanRefused,
	// ApplyFailed, BotNotDeleted, BotLeft, Tended, NeedsYou and Requests; nil
	// emits none.
	Recorder events.EventRecorder
	// GitTimeout bounds the Git calls of one reconcile; 0 means two minutes.
	GitTimeout time.Duration
	// PlanCheckTimeout is how long a Plan Task may report a plan the brief
	// repository doesn't show before the Brief refuses it; 0 means ten minutes.
	PlanCheckTimeout time.Duration
	// NeedsInputRecheck is how often a Brief waiting for the person, or for a
	// Container to be Ready, looks again, as Secrets aren't watched; 0 means a
	// minute.
	NeedsInputRecheck time.Duration
	// SuggestionsRecheck is how often a Brief that made a Container reads its
	// repositories again for reviewers' suggestions, besides when one of their
	// Tasks ends; 0 means ten minutes.
	SuggestionsRecheck time.Duration
	// BotDeleteTimeout is how long a deleted Brief tries to delete its bot on a
	// Git host that fails it before it goes without; 0 means ten minutes.
	BotDeleteTimeout time.Duration
	// SharedSecrets name the Secrets the operator shares from SourceNamespace,
	// its own, into opted-in project namespaces, as the ProjectReconciler's
	// do: a missing Agent token of one of these names says it can be given
	// there.
	SharedSecrets   []string
	SourceNamespace string
	// ModelProxyURL, when set, is the model proxy's URL: an Agent on a shared
	// Secret then needs it in SourceNamespace only, never in the project's.
	ModelProxyURL string
	// Issues reaches the GitHub repositories a Brief takes requests from.
	// cmd/main.go builds it.
	Issues IssueTrackers
	// requestsReads are what each Brief last read of each repository's issues.
	requestsMu    sync.Mutex
	requestsReads map[string]*requestsRead
	// requestsSaid are the comments the Brief knows it posted on requests,
	// so it looks for its marker once per request, not on every read.
	requestsSaid map[string]bool
	// requestsShip is when the Brief last looked whether each planned
	// request shipped.
	requestsShip map[string]shipCheck
	// requestsPublished is the newest release of each Container's Crew the
	// Brief saw published.
	requestsPublished map[string]model.Shipped
	// Now tells the time of the Brief's heal windows and tending rounds; nil
	// means time.Now.
	Now func() time.Time
}

func (r *BriefReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

const (
	defaultBriefGitTimeout        = 2 * time.Minute
	defaultBriefNeedsInputRecheck = time.Minute
	defaultPlanCheckTimeout       = 10 * time.Minute
	defaultBotDeleteTimeout       = 10 * time.Minute
	defaultSuggestionsRecheck     = 10 * time.Minute
)

func (r *BriefReconciler) suggestionsRecheck() time.Duration {
	if r.SuggestionsRecheck > 0 {
		return r.SuggestionsRecheck
	}
	return defaultSuggestionsRecheck
}

func (r *BriefReconciler) botDeleteTimeout() time.Duration {
	if r.BotDeleteTimeout > 0 {
		return r.BotDeleteTimeout
	}
	return defaultBotDeleteTimeout
}

func (r *BriefReconciler) gitTimeout() time.Duration {
	if r.GitTimeout > 0 {
		return r.GitTimeout
	}
	return defaultBriefGitTimeout
}

func (r *BriefReconciler) planCheckTimeout() time.Duration {
	if r.PlanCheckTimeout > 0 {
		return r.PlanCheckTimeout
	}
	return defaultPlanCheckTimeout
}

func (r *BriefReconciler) needsInputRecheck() time.Duration {
	if r.NeedsInputRecheck > 0 {
		return r.NeedsInputRecheck
	}
	return defaultBriefNeedsInputRecheck
}

// +kubebuilder:rbac:groups=theos.factory.io,resources=briefs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=theos.factory.io,resources=briefs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=theos.factory.io,resources=briefs/finalizers,verbs=update
// +kubebuilder:rbac:groups=theos.factory.io,resources=gitproviders;agents,verbs=get;list;watch
// +kubebuilder:rbac:groups=theos.factory.io,resources=projects;people;softwaresystems;containers;datastores;relationships,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=theos.factory.io,resources=crews;tasks,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=theos.factory.io,resources=frameworks;standards;computes;deployments;connectors;bundles,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;patch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile brings the Brief's repository up to date and takes the next step
// of its rounds, and deletes its bot when the Brief goes (B16). A missing GitProvider, credentials Secret, Agent or catalog
// entry sets NeedsInput, naming it, and nothing is applied while it lasts.
func (r *BriefReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var brief c4v1beta1.Brief
	if err := r.Get(ctx, req.NamespacedName, &brief); err != nil {
		if apierrors.IsNotFound(err) {
			brief.Namespace, brief.Name = req.Namespace, req.Name
			r.forgetReads(&brief, nil)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !brief.DeletionTimestamp.IsZero() {
		return r.deleteBot(ctx, &brief)
	}
	orig := brief.DeepCopy()
	brief.Status.ObservedGeneration = brief.Generation
	brief.Status.Autopilot = model.AutopilotMode(brief.Spec.Autopilot)

	result, err := r.reconcileBrief(ctx, &brief, orig)
	return r.finishReconcile(ctx, &brief, orig, result, err)
}

// finishReconcile records the outcome of a reconcile in the Brief's status:
// the operator's NeedsInput, and the phase. A Brief found stale is left as
// it is, for the next reconcile to read again.
func (r *BriefReconciler) finishReconcile(ctx context.Context, brief, orig *c4v1beta1.Brief,
	result ctrl.Result, err error) (ctrl.Result, error) {
	if errors.Is(err, errStaleBrief) {
		logf.FromContext(ctx).V(1).Info("Brief changed since it was read; reading it again")
		return ctrl.Result{RequeueAfter: staleBriefRetry}, nil
	}
	if sentence, needsInput := needsInputSentence(err); needsInput {
		operatorNeedsInput(brief, sentence)
		result, err = ctrl.Result{RequeueAfter: r.needsInputRecheck()}, nil
	} else if err == nil {
		clearOperatorNeedsInput(brief)
	}
	r.setPhase(ctx, brief)
	if patchErr := r.writeStatus(ctx, brief, orig); patchErr != nil {
		if errors.Is(patchErr, errStaleBrief) {
			logf.FromContext(ctx).V(1).Info("Brief changed since it was read; reading it again")
			return ctrl.Result{RequeueAfter: staleBriefRetry}, nil
		}
		return ctrl.Result{}, patchErr
	}
	return result, err
}

// reconcileBrief prepares the Brief's repository, then takes the next step of
// its rounds.
func (r *BriefReconciler) reconcileBrief(ctx context.Context, brief, orig *c4v1beta1.Brief) (ctrl.Result, error) {
	url, err := r.prepareWorkspace(ctx, brief)
	if err != nil {
		return ctrl.Result{}, err
	}
	if brief.Status.RepositoryURL != url {
		logf.FromContext(ctx).Info("Prepared the Brief's repository", "url", url)
		brief.Status.RepositoryURL = url
	}
	// A Crew that can't take its releases, parallel or pause doesn't stop
	// the rounds; the reconcile tries it again after them.
	syncErr := r.syncReleases(ctx, brief)
	if syncErr != nil {
		logf.FromContext(ctx).Info("Could not bring a Crew's releases up to date", "error", syncErr.Error())
	}
	if parallelErr := r.syncParallel(ctx, brief); parallelErr != nil {
		logf.FromContext(ctx).Info("Could not bring a Crew's parallel up to date", "error", parallelErr.Error())
		syncErr = errors.Join(syncErr, parallelErr)
	}
	if pausedErr := r.syncPaused(ctx, brief); pausedErr != nil {
		logf.FromContext(ctx).Info("Could not bring a Crew's pause up to date", "error", pausedErr.Error())
		syncErr = errors.Join(syncErr, pausedErr)
	}
	// Requests are read before the round's step, so a request waiting in Ask
	// is in needsYou when the Brief is tended.
	takesRequests := r.syncRequests(ctx, brief)
	owed := r.owesRequests(brief)
	result, err := r.reconcileRound(ctx, brief, orig)
	if err == nil && !owed && r.owesRequests(brief) {
		// The round just answered requests: say so on GitHub now, not at
		// the next read.
		r.syncRequests(ctx, brief)
	}
	if err == nil {
		// What the Brief made is tended once the round's step is taken, so a
		// round that just ended starts the tending round that follows it.
		var tendResult ctrl.Result
		if tendResult, err = r.tendRound(ctx, brief, orig); err == nil {
			result = sooner(result, tendResult)
		}
	}
	if apps, appsErr := r.listApps(ctx, brief); appsErr == nil {
		brief.Status.Apps = apps
	} else if err == nil {
		return ctrl.Result{}, fmt.Errorf("reading the apps' Crews: %w", appsErr)
	}
	if err == nil && (madeContainers(brief) || takesRequests) {
		// A person may answer a suggestion on the Git host, a Task of the
		// Brief's Containers may start waiting, and a request may be opened
		// on GitHub: look again now and then.
		result = sooner(result, ctrl.Result{RequeueAfter: r.suggestionsRecheck()})
	}
	if err == nil && syncErr != nil {
		return ctrl.Result{}, syncErr
	}
	return result, err
}

// sooner is the result that looks again first.
func sooner(a, b ctrl.Result) ctrl.Result {
	if a.RequeueAfter == 0 || (b.RequeueAfter > 0 && b.RequeueAfter < a.RequeueAfter) {
		return b
	}
	return a
}

// madeContainers reports whether the Brief made a Container, whose repository
// may get reviewers' suggestions.
func madeContainers(brief *c4v1beta1.Brief) bool {
	return slices.ContainsFunc(brief.Status.Created, func(o c4v1beta1.BriefObject) bool {
		return o.Kind == model.KindContainer
	})
}

// writeStatus patches the Brief's status when it changed. A reconcile that
// ends a round writes only the Brief it read: one that read a stale copy, from
// before an earlier reconcile ended the round, would end it again against a
// namespace that has changed since, and overwrite how it ended (#540).
func (r *BriefReconciler) writeStatus(ctx context.Context, brief, orig *c4v1beta1.Brief) error {
	if equality.Semantic.DeepEqual(brief.Status, orig.Status) {
		return nil
	}
	if !roundOver(&brief.Status) || roundOver(&orig.Status) {
		return r.Status().Patch(ctx, brief, client.MergeFrom(orig))
	}
	err := r.Status().Patch(ctx, brief, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{}))
	if apierrors.IsConflict(err) {
		return fmt.Errorf("%w: %w", errStaleBrief, err)
	}
	return err
}

// SetupWithManager sets up the controller with the Manager.
func (r *BriefReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// A GitProvider or Agent appearing or going can end or start a Brief's
	// wait for it.
	allInNamespace := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var briefs c4v1beta1.BriefList
		if err := mgr.GetClient().List(ctx, &briefs, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(briefs.Items))
		for _, b := range briefs.Items {
			reqs = append(reqs, requestFor(b.Namespace, b.Name))
		}
		return reqs
	})
	// A Container a Brief made becoming Ready lets its round go on.
	fromBriefLabel := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
		if name := obj.GetLabels()[c4v1beta1.LabelBrief]; name != "" {
			return []reconcile.Request{requestFor(obj.GetNamespace(), name)}
		}
		return nil
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1beta1.Brief{}, briefSpecOrOperatorWait()).
		// A Task's phase moving on, or its runs stopping for a pause, changes
		// what the Paused condition counts.
		Owns(&c4v1beta1.Task{}, taskPhaseOrPausedChanges()).
		// A Task on a Container the Brief made may file reviewers' suggestions.
		Watches(&c4v1beta1.Task{}, handler.EnqueueRequestsFromMapFunc(briefOfTaskContainer(mgr.GetClient())),
			taskPhaseOrPausedChanges()).
		Watches(&c4v1beta1.GitProvider{}, allInNamespace, createOrDelete()).
		Watches(&c4v1beta1.Agent{}, allInNamespace, createOrDelete()).
		Watches(&c4v1beta1.Container{}, fromBriefLabel, containerPhaseOrBehindChanges()).
		// A Crew the Brief made changing its spec (its releases, or a hand
		// edit of its parallel) or its latest release changes the Brief's apps;
		// one it didn't make is read again with the suggestions.
		Watches(&c4v1beta1.Crew{}, fromBriefLabel, crewReleaseChanges()).
		// A delivery of the Git host reconciles the Brief whose repository a delivery names at once
		// (docs/plans/git-events.md, decision 7).
		WatchesRawSource(r.GitEvents.Source(gitEventBriefRequest(mgr.GetClient()))).
		Named("brief").
		Complete(r)
}
