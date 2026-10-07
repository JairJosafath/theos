/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"errors"
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// BriefReconciler turns a Brief's idea and prompts into the model, a round at
// a time (docs/plans/done/brief.md): it keeps the Brief's repository, the planner's
// workspace, up to date, starts a Plan Task per round, checks its plan,
// applies it, makes the new Containers' Crews and files their first issues.
type BriefReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader reads Secrets without caching them.
	APIReader client.Reader
	Git       GitClient
	// Recorder emits the Brief's Events: Planned, NeedsInput, PlanRefused,
	// ApplyFailed, BotNotDeleted and BotLeft; nil emits none.
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

// +kubebuilder:rbac:groups=c4.factory.io,resources=briefs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=c4.factory.io,resources=briefs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=c4.factory.io,resources=briefs/finalizers,verbs=update
// +kubebuilder:rbac:groups=c4.factory.io,resources=gitproviders;agents,verbs=get;list;watch
// +kubebuilder:rbac:groups=c4.factory.io,resources=projects;people;softwaresystems;containers;datastores;relationships,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=c4.factory.io,resources=crews;tasks,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=c4.factory.io,resources=frameworks;standards;computes;deployments;connectors,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;patch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile brings the Brief's repository up to date and takes the next step
// of its rounds, and deletes its bot when the Brief goes (B16). A missing GitProvider, credentials Secret, Agent or catalog
// entry sets NeedsInput, naming it, and nothing is applied while it lasts.
func (r *BriefReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var brief c4v1alpha1.Brief
	if err := r.Get(ctx, req.NamespacedName, &brief); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !brief.DeletionTimestamp.IsZero() {
		return r.deleteBot(ctx, &brief)
	}
	orig := brief.DeepCopy()
	brief.Status.ObservedGeneration = brief.Generation

	result, err := r.reconcileBrief(ctx, &brief, orig)
	return r.finishReconcile(ctx, &brief, orig, result, err)
}

// finishReconcile records the outcome of a reconcile in the Brief's status:
// the operator's NeedsInput, and the phase. A Brief found stale is left as
// it is, for the next reconcile to read again.
func (r *BriefReconciler) finishReconcile(ctx context.Context, brief, orig *c4v1alpha1.Brief,
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
		return ctrl.Result{}, patchErr
	}
	return result, err
}

// reconcileBrief prepares the Brief's repository, then takes the next step of
// its rounds.
func (r *BriefReconciler) reconcileBrief(ctx context.Context, brief, orig *c4v1alpha1.Brief) (ctrl.Result, error) {
	url, err := r.prepareWorkspace(ctx, brief)
	if err != nil {
		return ctrl.Result{}, err
	}
	if brief.Status.RepositoryURL != url {
		logf.FromContext(ctx).Info("Prepared the Brief's repository", "url", url)
		brief.Status.RepositoryURL = url
	}
	// A Crew that can't take its releases doesn't stop the rounds; the
	// reconcile tries it again after them.
	releasesErr := r.syncReleases(ctx, brief)
	if releasesErr != nil {
		logf.FromContext(ctx).Info("Could not bring a Crew's releases up to date", "error", releasesErr.Error())
	}
	result, err := r.reconcileRound(ctx, brief, orig)
	if err == nil && result.IsZero() && madeContainers(brief) {
		// A person may answer a suggestion on the Git host: look again now and then.
		result.RequeueAfter = r.suggestionsRecheck()
	}
	if err == nil && releasesErr != nil {
		return ctrl.Result{}, releasesErr
	}
	return result, err
}

// madeContainers reports whether the Brief made a Container, whose repository
// may get reviewers' suggestions.
func madeContainers(brief *c4v1alpha1.Brief) bool {
	return slices.ContainsFunc(brief.Status.Created, func(o c4v1alpha1.BriefObject) bool {
		return o.Kind == model.KindContainer
	})
}

// writeStatus patches the Brief's status when it changed.
func (r *BriefReconciler) writeStatus(ctx context.Context, brief, orig *c4v1alpha1.Brief) error {
	if equality.Semantic.DeepEqual(brief.Status, orig.Status) {
		return nil
	}
	return r.Status().Patch(ctx, brief, client.MergeFrom(orig))
}

// SetupWithManager sets up the controller with the Manager.
func (r *BriefReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// A GitProvider or Agent appearing or going can end or start a Brief's
	// wait for it.
	allInNamespace := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var briefs c4v1alpha1.BriefList
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
		if name := obj.GetLabels()[c4v1alpha1.LabelBrief]; name != "" {
			return []reconcile.Request{requestFor(obj.GetNamespace(), name)}
		}
		return nil
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1alpha1.Brief{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&c4v1alpha1.Task{}, taskPhaseChanges()).
		// A Task on a Container the Brief made may file reviewers' suggestions.
		Watches(&c4v1alpha1.Task{}, handler.EnqueueRequestsFromMapFunc(briefOfTaskContainer(mgr.GetClient())),
			taskPhaseChanges()).
		Watches(&c4v1alpha1.GitProvider{}, allInNamespace, createOrDelete()).
		Watches(&c4v1alpha1.Agent{}, allInNamespace, createOrDelete()).
		Watches(&c4v1alpha1.Container{}, fromBriefLabel, containerPhaseChanges()).
		Named("brief").
		Complete(r)
}
