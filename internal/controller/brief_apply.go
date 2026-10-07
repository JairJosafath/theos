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
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// briefLabelColor is the color of a label a planner asks for that the factory
// doesn't define.
const briefLabelColor = "#ededed"

// roundApply is one reconcile's work on applying a round.
type roundApply struct {
	brief, orig *c4v1alpha1.Brief
	round       string
	// made are the objects this reconcile created, which the cache may not
	// show yet.
	made []c4v1alpha1.BriefObject
	// released are the Containers the round releases.
	released map[string]bool
	// took and skipped count the reviewers' suggestions the round answered.
	took, skipped int
	// retried and closed count the failed issues the round answered.
	retried, closed int
	// uses holds what the round's uses: issues wait for, filled once a
	// reconcile (brief_uses.go).
	uses *usesLookup
	// usesWaits say which filed issues wait for a used app's module.
	usesWaits []string
}

// applyRound applies the plan the Brief accepted (B8), in order and each step
// idempotent, so a restart applies the rest: the story objects, then for each
// Container the round waits for, once it is Ready, a new one's Crew and the
// round's issues for it, and last the plan merged into the brief repository's main.
func (r *BriefReconciler) applyRound(ctx context.Context, brief, orig *c4v1alpha1.Brief, task *c4v1alpha1.Task) (
	ctrl.Result, error) {
	tree, ok, err := r.readPlan(ctx, brief, task.Status.Commit)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ok {
		return ctrl.Result{}, fmt.Errorf("branch %s moved past the accepted plan %s",
			model.PlanBranch(int(brief.Status.Round)), task.Status.Commit)
	}
	plan, err := model.ParsePlan(planFiles(tree))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reading the accepted plan: %w", err)
	}
	if plan.NeedsInput != "" {
		r.plannerNeedsInput(ctx, brief, task, plan)
		return ctrl.Result{}, nil
	}
	if err := r.needAgents(ctx, brief, "implementer", "reviewer"); err != nil {
		return ctrl.Result{}, err
	}
	current, err := r.currentModel(ctx, brief)
	if err != nil {
		return ctrl.Result{}, err
	}
	if missing := model.MissingCatalog(plan, current); len(missing) > 0 {
		return ctrl.Result{}, errNeedsInput{fmt.Sprintf("The plan of round %d needs %s, missing in namespace %s: "+
			"install it, so the Brief can apply the round", brief.Status.Round, strings.Join(missing, ", "), brief.Namespace)}
	}
	// The namespace may have changed since the plan was checked: someone made
	// an object of the same name, or one the plan doesn't keep. What this
	// round applied already is in the plan as it is, so a restart passes.
	if refusals := model.CheckPlan(plan, current); len(refusals) > 0 {
		endRoundFailed(ctx, brief, task,
			"the plan no longer fits the namespace: "+model.RefusalText(refusals))
		return ctrl.Result{}, nil
	}

	run := &roundApply{brief: brief, orig: orig, round: strconv.Itoa(int(brief.Status.Round))}
	var created []string
	newRelationships := false
	for _, o := range model.ApplyOrder(plan.Objects) {
		if o.Kind == model.KindContainer && newRelationships {
			// A Container is scaffolded with the Relationships its controller
			// sees; the cache it shares with this one must show them first.
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		isNew, err := r.applyObject(ctx, run, o)
		if err != nil {
			return ctrl.Result{}, r.applyFailed(ctx, brief, "at "+o.Ref().String(), err)
		}
		newRelationships = newRelationships || (isNew && o.Kind == model.KindRelationship)
		if o.Kind == model.KindContainer && (isNew || r.madeByRound(ctx, run, o.Name)) {
			created = append(created, o.Name)
		}
	}
	released, err := r.releasedContainers(ctx, brief, plan)
	if err != nil {
		return ctrl.Result{}, err
	}
	run.released = released
	waiting, err := r.finishContainers(ctx, run, model.RoundContainers(created, plan.Issues), created, plan.Issues)
	if err != nil {
		return ctrl.Result{}, r.applyFailed(ctx, brief, "making the Crews and filing the issues", err)
	}
	if len(waiting) > 0 || len(run.usesWaits) > 0 {
		setCondition(&brief.Status.Conditions, brief.Generation, c4v1alpha1.ConditionPlanned, metav1.ConditionFalse,
			"Applying", roundWaitMessage(int(brief.Status.Round), waiting, run.usesWaits))
		return ctrl.Result{RequeueAfter: r.needsInputRecheck()}, nil
	}
	// A Container the plan releases that the round neither made nor files
	// issues for still gets releases on its Crew.
	for _, name := range plan.Releases {
		if err := r.releaseCrew(ctx, brief, name); err != nil {
			return ctrl.Result{}, r.applyFailed(ctx, brief, "turning on the releases of Crew "+name, err)
		}
	}
	if run.took, run.skipped, err = r.answerSuggestions(ctx, brief, plan.Answers); err != nil {
		return ctrl.Result{}, r.applyFailed(ctx, brief, "answering the reviewers' suggestions", err)
	}
	if run.retried, run.closed, err = r.answerFailed(ctx, brief, plan.FailedAnswers); err != nil {
		return ctrl.Result{}, r.applyFailed(ctx, brief, "answering the failed issues", err)
	}
	if err := r.mergePlan(ctx, brief, task.Status.Commit); err != nil {
		return ctrl.Result{}, r.applyFailed(ctx, brief, "merging the plan", err)
	}
	return ctrl.Result{}, r.roundApplied(ctx, run, task, plan)
}

// applyFailed says why applying the round stopped, so the Brief never looks
// busy without a reason (#371): its Planned condition, reason ApplyFailed,
// names where and the error, and an ApplyFailed Event says it once per
// message. It returns err, wrapped, for the reconcile to try again; the
// Brief stays Applying until a try gets past it. The operator's own
// NeedsInput and a stale Brief are left to finishReconcile.
func (r *BriefReconciler) applyFailed(ctx context.Context, brief *c4v1alpha1.Brief, where string, err error) error {
	if _, needsInput := needsInputSentence(err); needsInput || errors.Is(err, errStaleBrief) {
		return err
	}
	message := model.Clip(fmt.Sprintf("Round %d could not be applied %s: %v. The operator tries again.",
		brief.Status.Round, where, err), 1000)
	cond := meta.FindStatusCondition(brief.Status.Conditions, c4v1alpha1.ConditionPlanned)
	if cond == nil || cond.Reason != model.ReasonApplyFailed || cond.Message != message {
		logf.FromContext(ctx).Info("Could not apply the round", "round", brief.Status.Round, "where", where,
			"error", err.Error())
		r.event(brief, corev1.EventTypeWarning, eventApplyFailed, "Apply", message)
	}
	setCondition(&brief.Status.Conditions, brief.Generation, c4v1alpha1.ConditionPlanned, metav1.ConditionFalse,
		model.ReasonApplyFailed, message)
	return fmt.Errorf("applying round %d %s: %w", brief.Status.Round, where, err)
}

// madeByRound reports whether the Container carries this round's label: a
// restart finds the Containers an earlier reconcile created.
func (r *BriefReconciler) madeByRound(ctx context.Context, run *roundApply, name string) bool {
	var c c4v1alpha1.Container
	if err := r.Get(ctx, client.ObjectKey{Namespace: run.brief.Namespace, Name: name}, &c); err != nil {
		return false
	}
	return c.Labels[c4v1alpha1.LabelBrief] == run.brief.Name && c.Labels[c4v1alpha1.LabelBriefRound] == run.round
}

// finishContainers gives each of names that is Ready its Crew, when the round
// created it, then files the round's issues of the Ready ones. It returns the
// ones that aren't Ready yet.
func (r *BriefReconciler) finishContainers(ctx context.Context, run *roundApply, names, created []string,
	issues []model.PlanIssue) ([]string, error) {
	var waiting []string
	ready := map[string]*c4v1alpha1.Container{}
	for _, name := range names {
		var c c4v1alpha1.Container
		err := r.Get(ctx, client.ObjectKey{Namespace: run.brief.Namespace, Name: name}, &c)
		if apierrors.IsNotFound(err) {
			waiting = append(waiting, "Container "+name)
			continue
		}
		if err != nil {
			return nil, err
		}
		if c.Status.Phase != c4v1alpha1.ContainerReady {
			waiting = append(waiting, fmt.Sprintf("Container %s (%s)", name, c.Status.Phase))
			continue
		}
		if slices.Contains(created, name) {
			if err := r.ensureCrew(ctx, run, &c); err != nil {
				return nil, fmt.Errorf("making the Crew of %s: %w", name, err)
			}
		}
		if run.released[name] {
			if err := r.releaseCrew(ctx, run.brief, name); err != nil {
				return nil, fmt.Errorf("turning on the releases of Crew %s: %w", name, err)
			}
		}
		ready[name] = &c
	}
	return waiting, r.fileIssues(ctx, run, ready, issues)
}

// applyObject creates a plan's object the namespace doesn't have, labelled
// with the Brief and the round and with no ownerReference to the Brief (B8),
// or brings an existing one's story text up to date. It reports whether it
// is new: created now, or so recently the cache doesn't show it.
func (r *BriefReconciler) applyObject(ctx context.Context, run *roundApply, o model.PlanObject) (bool, error) {
	desired, err := storyObject(run.brief.Namespace, o)
	if err != nil {
		return false, err
	}
	existing, ok := desired.DeepCopyObject().(client.Object)
	if !ok {
		return false, fmt.Errorf("copying %s", o.Ref())
	}
	err = r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		desired.SetLabels(map[string]string{
			c4v1alpha1.LabelBrief: run.brief.Name, c4v1alpha1.LabelBriefRound: run.round,
		})
		if err := r.Create(ctx, desired); apierrors.IsAlreadyExists(err) {
			return true, nil // made by an earlier reconcile the cache doesn't show yet
		} else if err != nil {
			return false, err
		}
		run.made = append(run.made, c4v1alpha1.BriefObject{Kind: o.Kind, Name: o.Name})
		logf.FromContext(ctx).Info("Created story object", "kind", o.Kind, "name", o.Name, "round", run.round)
		return true, nil
	}
	if err != nil {
		return false, err
	}
	orig, _ := existing.DeepCopyObject().(client.Object)
	copyStoryText(existing, desired)
	if equality.Semantic.DeepEqual(existing, orig) {
		return false, nil
	}
	if err := r.Patch(ctx, existing, client.MergeFrom(orig)); err != nil {
		return false, err
	}
	logf.FromContext(ctx).Info("Updated story text", "kind", o.Kind, "name", o.Name, "round", run.round)
	return false, nil
}

// storyObject is a plan's object as the kind it is.
func storyObject(ns string, o model.PlanObject) (client.Object, error) {
	objMeta := metav1.ObjectMeta{Namespace: ns, Name: o.Name}
	switch o.Kind {
	case model.KindProject:
		return &c4v1alpha1.Project{ObjectMeta: objMeta,
			Spec: c4v1alpha1.ProjectSpec{DisplayName: o.DisplayName, Description: o.Description}}, nil
	case model.KindPerson:
		return &c4v1alpha1.Person{ObjectMeta: objMeta,
			Spec: c4v1alpha1.PersonSpec{DisplayName: o.DisplayName, Description: o.Description}}, nil
	case model.KindSoftwareSystem:
		return &c4v1alpha1.SoftwareSystem{ObjectMeta: objMeta, Spec: c4v1alpha1.SoftwareSystemSpec{
			DisplayName: o.DisplayName, Description: o.Description, Needs: o.Needs, KPIs: o.KPIs}}, nil
	case model.KindContainer:
		return &c4v1alpha1.Container{ObjectMeta: objMeta, Spec: c4v1alpha1.ContainerSpec{
			SoftwareSystemRef: c4v1alpha1.LocalRef{Name: o.SoftwareSystem},
			DisplayName:       o.DisplayName, Description: o.Description,
			Type:           c4v1alpha1.ContainerType(o.Type),
			GitProviderRef: c4v1alpha1.LocalRef{Name: o.GitProvider},
			FrameworkRef:   c4v1alpha1.LocalRef{Name: o.Framework},
			StandardRef:    optionalLocalRef(o.Standard),
			ComputeRef:     optionalLocalRef(o.Compute),
			DeploymentRef:  optionalLocalRef(o.Deployment),
		}}, nil
	case model.KindDataStore:
		return &c4v1alpha1.DataStore{ObjectMeta: objMeta, Spec: c4v1alpha1.DataStoreSpec{
			SoftwareSystemRef: c4v1alpha1.LocalRef{Name: o.SoftwareSystem},
			DisplayName:       o.DisplayName, Description: o.Description,
			ConnectorRef: c4v1alpha1.LocalRef{Name: o.Connector},
		}}, nil
	case model.KindRelationship:
		return &c4v1alpha1.Relationship{ObjectMeta: objMeta, Spec: c4v1alpha1.RelationshipSpec{
			Source:      c4v1alpha1.ElementRef{Kind: c4v1alpha1.ElementKind(o.Source.Kind), Name: o.Source.Name},
			Target:      c4v1alpha1.ElementRef{Kind: c4v1alpha1.ElementKind(o.Target.Kind), Name: o.Target.Name},
			Description: o.Description, Bundles: o.Bundles,
		}}, nil
	}
	return nil, fmt.Errorf("a plan doesn't write %s", o.Kind)
}

func optionalLocalRef(name string) *c4v1alpha1.LocalRef {
	if name == "" {
		return nil
	}
	return &c4v1alpha1.LocalRef{Name: name}
}

// copyStoryText copies the story text a plan may change, displayName,
// description, needs and kpis, from src to dst, of the same kind.
func copyStoryText(dst, src client.Object) {
	switch d := dst.(type) {
	case *c4v1alpha1.Project:
		s, _ := src.(*c4v1alpha1.Project)
		d.Spec.DisplayName, d.Spec.Description = s.Spec.DisplayName, s.Spec.Description
	case *c4v1alpha1.Person:
		s, _ := src.(*c4v1alpha1.Person)
		d.Spec.DisplayName, d.Spec.Description = s.Spec.DisplayName, s.Spec.Description
	case *c4v1alpha1.SoftwareSystem:
		s, _ := src.(*c4v1alpha1.SoftwareSystem)
		d.Spec.DisplayName, d.Spec.Description = s.Spec.DisplayName, s.Spec.Description
		d.Spec.Needs, d.Spec.KPIs = s.Spec.Needs, s.Spec.KPIs
	case *c4v1alpha1.Container:
		s, _ := src.(*c4v1alpha1.Container)
		d.Spec.DisplayName, d.Spec.Description = s.Spec.DisplayName, s.Spec.Description
	case *c4v1alpha1.DataStore:
		s, _ := src.(*c4v1alpha1.DataStore)
		d.Spec.DisplayName, d.Spec.Description = s.Spec.DisplayName, s.Spec.Description
	case *c4v1alpha1.Relationship:
		s, _ := src.(*c4v1alpha1.Relationship)
		d.Spec.Description = s.Spec.Description
	}
}

// ensureCrew makes the Crew of a Container the round created (B9): named
// after it, with the Brief's implementer and reviewer, one Task at a time, and
// releases on only when the round releases the Container (R4, R5). An
// existing Crew of that name is left alone.
func (r *BriefReconciler) ensureCrew(ctx context.Context, run *roundApply, c *c4v1alpha1.Container) error {
	brief := run.brief
	crew := &c4v1alpha1.Crew{
		ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: c.Name, Labels: map[string]string{
			c4v1alpha1.LabelBrief: brief.Name, c4v1alpha1.LabelBriefRound: run.round,
		}},
		Spec: c4v1alpha1.CrewSpec{
			ContainerRef: c4v1alpha1.LocalRef{Name: c.Name},
			Implementer:  brief.Spec.Agents.Implementer,
			Reviewer:     brief.Spec.Agents.Reviewer,
			Parallel:     1,
		},
	}
	if run.released[c.Name] {
		releaseSpec(&crew.Spec, briefRelease(brief, c.Name))
	}
	err := r.Get(ctx, client.ObjectKeyFromObject(crew), &c4v1alpha1.Crew{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	if err := r.Create(ctx, crew); err != nil {
		return client.IgnoreAlreadyExists(err)
	}
	run.made = append(run.made, c4v1alpha1.BriefObject{Kind: "Crew", Name: crew.Name})
	logf.FromContext(ctx).Info("Created Crew", "crew", crew.Name, "round", run.round)
	return nil
}

// issueRepo is a Container's repository as this reconcile files issues in it:
// its labels made and its open issues listed once.
type issueRepo struct {
	provider  gitprovider.Provider
	repo      gitprovider.Repository
	open      []gitprovider.Issue
	milestone int64
	// later is the Later milestone's ID, once a by-hand issue needs it.
	later int64
}

// fileIssues files the round's issues of the Ready Containers in their
// repositories (B11), blockers first: status/ready, each body ending with its
// marker, in the round's release milestone when the round releases the
// Container (R6) and else in none. A by-hand issue, a person's work, is
// labelled status/needs-human instead, in the Later milestone, and never
// ready. An issue with after: is filed only once
// each issue it names is filed and recorded; until then it waits with the
// Container that isn't Ready. It is filed without status/ready, made to
// depend in the Git host on each of them, across repositories, and only then
// labelled ready, so no Crew picks it up before its order is there. An issue
// with uses: is filed the same way, and readied, with a line pinning each used
// module, only once each app it uses has published one; until then it is
// filed but not recorded, and run.usesWaits says what it waits for. An issue
// already filed is found by its marker among the open issues, so a restart
// files no duplicate and finishes what it left. Each is recorded in the
// Brief's status once done.
func (r *BriefReconciler) fileIssues(ctx context.Context, run *roundApply, ready map[string]*c4v1alpha1.Container,
	issues []model.PlanIssue) error {
	repos := map[string]*issueRepo{}
	for _, issue := range model.FilingOrder(issues) {
		c := ready[issue.Container]
		if c == nil || run.filed(issue.N) != nil {
			continue // its Container isn't Ready, or it is filed and recorded, maybe closed since
		}
		blockers := model.IssueBlockers(issues, issue.N)
		var buildsOn []model.BuildsOn
		for _, m := range blockers {
			if b := run.filed(m); b != nil {
				buildsOn = append(buildsOn, model.BuildsOn{Repository: b.Repository, Number: int(b.Number),
					Title: b.Title})
			}
		}
		if len(buildsOn) < len(blockers) {
			continue // a blocker isn't filed: it waits with its Container
		}
		uses, err := r.usedModules(ctx, run, issues, issue)
		if err != nil {
			return fmt.Errorf("reading the modules issue %d of %s uses: %w", issue.N, c.Name, err)
		}
		if err := r.fileIssue(ctx, run, repos, c, issues, issue, buildsOn, uses); err != nil {
			return fmt.Errorf("filing issue %d of %s: %w", issue.N, c.Name, err)
		}
	}
	return nil
}

// filed returns the Brief's record of the round's issue n, or nil when it
// isn't recorded.
func (run *roundApply) filed(n int) *c4v1alpha1.BriefIssue {
	status := &run.brief.Status
	i := slices.IndexFunc(status.Issues, func(i c4v1alpha1.BriefIssue) bool {
		return i.Round == status.Round && int(i.N) == n
	})
	if i < 0 {
		return nil
	}
	return &status.Issues[i]
}

// fileIssue files one of the round's issues, or finds it by its marker, then
// makes it depend on what it builds on and labels it ready, and records it.
func (r *BriefReconciler) fileIssue(ctx context.Context, run *roundApply, repos map[string]*issueRepo,
	c *c4v1alpha1.Container, issues []model.PlanIssue, issue model.PlanIssue, buildsOn []model.BuildsOn,
	uses usedModules) error {
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	ir := repos[c.Name]
	if ir == nil {
		provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, c.Namespace, c.Spec.GitProviderRef.Name)
		if err != nil {
			return err
		}
		repo := provider.Repository(c.Name)
		if err := provider.EnsureLabels(gitCtx, repo, briefIssueLabels(model.IssuesFor(issues, c.Name))); err != nil {
			return err
		}
		open, err := provider.ListIssues(gitCtx, repo, "")
		if err != nil {
			return err
		}
		ir = &issueRepo{provider: provider, repo: repo, open: open}
		repos[c.Name] = ir
	}
	brief, round := run.brief, int(run.brief.Status.Round)
	provider, repo := ir.provider, ir.repo
	byHand := model.IsByHand(issue.Labels)
	// Filed, but not recorded before a restart: found by its marker.
	i := slices.IndexFunc(ir.open, func(o gitprovider.Issue) bool {
		return model.HasIssueMarker(o.Body, brief.Name, round, issue.N)
	})
	var filed gitprovider.Issue
	if i >= 0 {
		filed = ir.open[i]
	} else {
		// Made only once an issue is to be filed: a release with no work
		// is never released (R7). A person's work waits in Later, so no
		// release waits on it.
		var err error
		milestone := &ir.milestone
		switch {
		case byHand:
			milestone = &ir.later
			if ir.later == 0 {
				if ir.later, err = provider.EnsureMilestone(gitCtx, repo, model.FollowUpMilestone,
					model.FollowUpMilestoneDescription); err != nil {
					return err
				}
			}
		case run.released[c.Name] && ir.milestone == 0:
			if ir.milestone, err = releaseMilestone(gitCtx, provider, repo, brief.Name, round); err != nil {
				return err
			}
		}
		labels := model.BriefIssueLabels(issue.Labels)
		if len(buildsOn) > 0 || len(issue.Uses) > 0 {
			labels = slices.DeleteFunc(labels, func(l string) bool { return l == model.LabelReady })
		}
		body := model.BriefIssueBodyUsing(issue, brief.Name, round, buildsOn, uses.modules)
		filed, err = provider.CreateIssue(gitCtx, repo, issue.Title, body, labels, *milestone)
		if err != nil {
			return err
		}
		logf.FromContext(ctx).Info("Filed Brief issue", "container", c.Name, "number", filed.Number, "round", round)
	}
	if len(buildsOn) > 0 || len(issue.Uses) > 0 {
		for _, b := range buildsOn {
			owner, name, _ := strings.Cut(b.Repository, "/")
			if err := provider.AddIssueDependency(gitCtx, repo, filed.Number,
				gitprovider.Repository{Owner: owner, Name: name}, b.Number); err != nil {
				return fmt.Errorf("making it depend on %s#%d: %w", b.Repository, b.Number, err)
			}
		}
		if len(uses.waits) > 0 {
			run.usesWaits = append(run.usesWaits, fmt.Sprintf("issue %s/%s#%d waits for %s",
				repo.Owner, repo.Name, filed.Number, strings.Join(uses.waits, " and ")))
			return nil // readied once the modules are published, so not recorded yet
		}
		if err := r.pinUses(gitCtx, provider, repo, filed, model.BriefIssueBodyUsing(issue, brief.Name, round,
			buildsOn, uses.modules), uses.modules); err != nil {
			return err
		}
		if !byHand && !slices.Contains(filed.Labels, model.LabelReady) {
			if err := provider.AddIssueLabel(gitCtx, repo, filed.Number, model.LabelReady); err != nil {
				return err
			}
			logf.FromContext(ctx).Info("Ordered Brief issue", "container", c.Name, "number", filed.Number,
				"round", round, "blockers", len(buildsOn), "uses", len(uses.modules))
		}
	}
	return r.recordIssue(ctx, run, repo.Owner+"/"+repo.Name, issue.N, filed)
}

// briefIssueLabels are the labels the issues need: the factory's, and any
// other the planner asked for.
func briefIssueLabels(issues []model.PlanIssue) []gitprovider.Label {
	labels := issueLabels()
	for _, issue := range issues {
		for _, name := range model.BriefIssueLabels(issue.Labels) {
			switch {
			case slices.ContainsFunc(labels, func(l gitprovider.Label) bool { return l.Name == name }):
			case name == model.LabelByHand:
				l := model.ByHandLabel()
				labels = append(labels,
					gitprovider.Label{Name: l.Name, Color: l.Color, Description: l.Description, Exclusive: l.Exclusive})
			default:
				labels = append(labels, gitprovider.Label{Name: name, Color: briefLabelColor})
			}
		}
	}
	return labels
}

// recordIssue adds a filed issue to the Brief's status, once, with its round
// and number in the plan, and writes the status at once, so a later reconcile
// never files it again, even once it is closed.
func (r *BriefReconciler) recordIssue(ctx context.Context, run *roundApply, repository string, n int,
	issue gitprovider.Issue) error {
	status := &run.brief.Status
	if slices.ContainsFunc(status.Issues, func(i c4v1alpha1.BriefIssue) bool {
		return i.Repository == repository && int(i.Number) == issue.Number
	}) {
		return nil
	}
	status.Issues = model.KeepNewest(append(status.Issues, c4v1alpha1.BriefIssue{
		Repository: repository, Number: int32(issue.Number), URL: issue.URL, Title: model.Clip(issue.Title, 255),
		Round: status.Round, N: int32(n),
	}), 500)
	if err := r.Status().Patch(ctx, run.brief, client.MergeFrom(run.orig)); err != nil {
		return err
	}
	*run.orig = *run.brief.DeepCopy()
	return nil
}

// mergePlan puts the round's plan on the brief repository's main, in place of
// the earlier round's: one commit by the operator, pushed without force. A
// plan main already holds is merged.
func (r *BriefReconciler) mergePlan(ctx context.Context, brief *c4v1alpha1.Brief, commit string) error {
	provider, err := r.briefGit(ctx, brief)
	if err != nil {
		return err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	repo := provider.Repository(model.BriefRepository(brief.Name))
	dir := []string{model.PlanDir + "/"}
	plan, sha, err := provider.ReadFiles(gitCtx, repo, model.PlanBranch(int(brief.Status.Round)), dir)
	if err != nil {
		return err
	}
	if sha != commit {
		return fmt.Errorf("branch %s is at %s, not the accepted plan %s", model.PlanBranch(int(brief.Status.Round)),
			sha, commit)
	}
	onMain, head, err := provider.ReadFiles(gitCtx, repo, gitprovider.DefaultBranch, dir)
	if err != nil {
		return err
	}
	var removes []string
	for p := range onMain {
		if _, ok := plan[p]; !ok {
			removes = append(removes, p)
		}
	}
	slices.Sort(removes)
	message := fmt.Sprintf("Plan round %d of Brief %s\n", brief.Status.Round, brief.Name)
	_, err = provider.PushDefaultBranch(gitCtx, repo, head, plan, removes, message, briefAuthor())
	if errors.Is(err, gitprovider.ErrEmptyChange) {
		return nil
	}
	if err != nil {
		return err
	}
	logf.FromContext(ctx).Info("Merged Brief plan", "round", brief.Status.Round, "commit", commit)
	return nil
}

// roundApplied records an applied round: its history entry, the choices, the
// prompts it took and everything the Brief made.
func (r *BriefReconciler) roundApplied(ctx context.Context, run *roundApply, task *c4v1alpha1.Task,
	plan model.Plan) error {
	brief := run.brief
	status := &brief.Status
	created, err := r.createdObjects(ctx, run)
	if err != nil {
		return err
	}
	summary := model.RoundSummary(plan)
	recordRound(status, task, c4v1alpha1.BriefRound{Outcome: model.ReasonApplied, Summary: summary, Choices: plan.Choices})
	status.Choices = model.ClipList(plan.Choices, 50, 500)
	status.PromptsPlanned = status.RoundPrompts
	status.Created = created
	message := fmt.Sprintf("Applied round %d", status.Round)
	setCondition(&status.Conditions, brief.Generation, c4v1alpha1.ConditionPlanned, metav1.ConditionTrue,
		model.ReasonApplied, message)
	answered := ""
	if run.took+run.skipped > 0 {
		answered = fmt.Sprintf(", took %d and skipped %d reviewers' suggestions", run.took, run.skipped)
	}
	if run.retried+run.closed > 0 {
		answered += fmt.Sprintf(", retried %d and closed %d failed issues", run.retried, run.closed)
	}
	r.event(brief, corev1.EventTypeNormal, eventPlanned, "Apply",
		fmt.Sprintf("%s: %d objects, %d issues%s. %s", message, len(plan.Objects), len(plan.Issues), answered,
			summary))
	logf.FromContext(ctx).Info("Applied Brief round", "round", status.Round, "issues", len(plan.Issues),
		"suggestionsTaken", run.took, "suggestionsSkipped", run.skipped, "failedRetried", run.retried,
		"failedClosed", run.closed)
	return nil
}

// createdObjects lists every story object and Crew labelled with the Brief,
// with what this reconcile made and the cache may not show yet; at most the
// 500 the status holds.
func (r *BriefReconciler) createdObjects(ctx context.Context, run *roundApply) ([]c4v1alpha1.BriefObject, error) {
	opts := []client.ListOption{client.InNamespace(run.brief.Namespace),
		client.MatchingLabels{c4v1alpha1.LabelBrief: run.brief.Name}}
	lists := []struct {
		kind string
		list client.ObjectList
	}{
		{model.KindProject, &c4v1alpha1.ProjectList{}}, {model.KindPerson, &c4v1alpha1.PersonList{}},
		{model.KindSoftwareSystem, &c4v1alpha1.SoftwareSystemList{}}, {model.KindDataStore, &c4v1alpha1.DataStoreList{}},
		{model.KindRelationship, &c4v1alpha1.RelationshipList{}}, {model.KindContainer, &c4v1alpha1.ContainerList{}},
		{"Crew", &c4v1alpha1.CrewList{}},
	}
	out := slices.Clone(run.brief.Status.Created)
	add := func(o c4v1alpha1.BriefObject) {
		if !slices.Contains(out, o) {
			out = append(out, o)
		}
	}
	for _, l := range lists {
		if err := r.List(ctx, l.list, opts...); err != nil {
			return nil, err
		}
		items, err := metaItems(l.list)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			add(c4v1alpha1.BriefObject{Kind: l.kind, Name: item.GetName()})
		}
	}
	for _, o := range run.made {
		add(o)
	}
	if len(out) > 500 {
		out = out[:500]
	}
	return out, nil
}
