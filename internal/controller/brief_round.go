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
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// The Brief's Events (docs/plans/done/brief.md, step 5).
const (
	eventPlanned     = "Planned"
	eventNeedsInput  = "NeedsInput"
	eventPlanRefused = "PlanRefused"
	eventApplyFailed = "ApplyFailed"
)

// planPushRecheck is how often a Brief looks again for a plan its Task says
// was pushed and the brief repository doesn't show yet.
const planPushRecheck = 5 * time.Second

// errStaleBrief says the Brief changed since this reconcile read it: its
// copy, status included, must not be written.
var errStaleBrief = errors.New("the Brief changed since it was read")

// staleBriefRetry is how soon a reconcile that found its Brief stale runs again.
const staleBriefRetry = time.Second

// planTaskName names the Plan Task of a Brief's round.
func planTaskName(brief string, round int32) string { return fmt.Sprintf("%s-plan-%d", brief, round) }

// roundOver reports whether the Brief's round is in its history: it applied,
// failed or asked for input.
func roundOver(status *c4v1alpha1.BriefStatus) bool {
	n := len(status.History)
	return n > 0 && status.History[n-1].Round == status.Round
}

// operatorWaits reports whether the operator's own NeedsInput holds: it waits
// for the person to make an Agent, the GitProvider, its Secret or a catalog entry.
func operatorWaits(status *c4v1alpha1.BriefStatus) bool {
	cond := meta.FindStatusCondition(status.Conditions, c4v1alpha1.ConditionNeedsInput)
	return cond != nil && cond.Status == metav1.ConditionTrue && cond.Reason == c4v1alpha1.ReasonNotFound
}

// roundFacts gathers what the Brief's next round and its phase depend on.
func roundFacts(brief *c4v1alpha1.Brief, task *c4v1alpha1.Task) model.BriefRoundFacts {
	status := &brief.Status
	facts := model.BriefRoundFacts{
		Prompts:            len(brief.Spec.Prompts),
		Round:              int(status.Round),
		RoundPrompts:       int(status.RoundPrompts),
		RoundOver:          roundOver(status),
		OperatorNeedsInput: operatorWaits(status),
	}
	if cond := meta.FindStatusCondition(status.Conditions, c4v1alpha1.ConditionPlanned); cond != nil {
		facts.Outcome = cond.Reason
	}
	if task != nil {
		facts.TaskPhase = string(task.Status.Phase)
	}
	return facts
}

// reconcileRound takes the next step of the Brief's rounds: start one, check
// its plan, apply it, or record how it ended.
func (r *BriefReconciler) reconcileRound(ctx context.Context, brief, orig *c4v1alpha1.Brief) (ctrl.Result, error) {
	task, err := r.roundTask(ctx, brief)
	if err != nil {
		return ctrl.Result{}, err
	}
	facts := roundFacts(brief, task)
	if model.StartsRound(facts) {
		return ctrl.Result{}, r.startRound(ctx, brief, orig)
	}
	if facts.RoundOver {
		return ctrl.Result{}, nil
	}
	if task == nil {
		return ctrl.Result{}, r.ensureRoundTask(ctx, brief)
	}
	// A Plan Task that hasn't started waits for its Agent: say so on the Brief,
	// such as the token Secret made after the round began.
	if task.Status.Phase == "" || task.Status.Phase == c4v1alpha1.TaskPending {
		if err := r.needAgents(ctx, brief, "planner"); err != nil {
			return ctrl.Result{}, err
		}
	}
	var result ctrl.Result
	switch task.Status.Phase {
	case c4v1alpha1.TaskChecking:
		return r.checkPlan(ctx, brief, task)
	case c4v1alpha1.TaskMerged:
		if result, err = r.applyRound(ctx, brief, orig, task); err != nil {
			return result, err
		}
	case c4v1alpha1.TaskFailed:
		r.failRound(ctx, brief, task)
	}
	if !roundOver(&brief.Status) {
		return result, nil
	}
	// The round just ended: the workspace shows it before anyone reads it, and
	// a prompt appended during the round starts the next one.
	if _, err := r.prepareWorkspace(ctx, brief); err != nil {
		return ctrl.Result{}, err
	}
	if model.StartsRound(roundFacts(brief, task)) {
		return ctrl.Result{}, r.startRound(ctx, brief, orig)
	}
	return result, nil
}

// roundTask returns the Plan Task of the Brief's round, or nil when the round
// has none (yet). A Task of that name the Brief doesn't own is in the way.
func (r *BriefReconciler) roundTask(ctx context.Context, brief *c4v1alpha1.Brief) (*c4v1alpha1.Task, error) {
	if brief.Status.Round == 0 {
		return nil, nil
	}
	name := planTaskName(brief.Name, brief.Status.Round)
	var task c4v1alpha1.Task
	err := r.Get(ctx, client.ObjectKey{Namespace: brief.Namespace, Name: name}, &task)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !metav1.IsControlledBy(&task, brief) {
		return nil, errNeedsInput{fmt.Sprintf("Task %s has the name of the Brief's Plan Task: delete or rename it, "+
			"so the Brief can plan round %d", name, brief.Status.Round)}
	}
	return &task, nil
}

// needAgents returns errNeedsInput naming the first of the roles' Agents that
// is missing, or whose credentials Secret is: the person makes that Secret
// (the Agents' token), and an Agent without it can't start.
func (r *BriefReconciler) needAgents(ctx context.Context, brief *c4v1alpha1.Brief, roles ...string) error {
	agents := map[string]c4v1alpha1.LocalRef{
		"planner": brief.Spec.Agents.Planner, "implementer": brief.Spec.Agents.Implementer,
		"reviewer": brief.Spec.Agents.Reviewer,
	}
	for _, role := range roles {
		name := agents[role].Name
		var agent c4v1alpha1.Agent
		err := r.Get(ctx, client.ObjectKey{Namespace: brief.Namespace, Name: name}, &agent)
		if apierrors.IsNotFound(err) {
			return errNeedsInput{fmt.Sprintf("Agent %s is missing: make it in namespace %s, or name another in "+
				"spec.agents.%s, so the Brief has its %s", name, brief.Namespace, role, role)}
		}
		if err != nil {
			return err
		}
		if agent.Spec.CredentialsSecretRef == nil {
			continue
		}
		secret := agent.Spec.CredentialsSecretRef.Name
		err = r.APIReader.Get(ctx, client.ObjectKey{Namespace: brief.Namespace, Name: secret}, &corev1.Secret{})
		if apierrors.IsNotFound(err) {
			return errNeedsInput{fmt.Sprintf("Secret %s of Agent %s is missing: make it in namespace %s with the "+
				"Agents' token, so the Brief's %s can start", secret, name, brief.Namespace, role)}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// startRound starts the Brief's next round, which takes every prompt it has
// now: it records the round, then makes its Plan Task. The record is written
// first, guarded by the Brief's resourceVersion, so a reconcile on a stale
// copy can't start the same round twice.
func (r *BriefReconciler) startRound(ctx context.Context, brief, orig *c4v1alpha1.Brief) error {
	if err := r.needAgents(ctx, brief, "planner"); err != nil {
		return err
	}
	catalog, err := installedCatalog(ctx, r.Client, brief.Namespace)
	if err != nil {
		return err
	}
	if !model.HasFramework(catalog) {
		return errNeedsInput{fmt.Sprintf("No Framework is installed in namespace %s: install the catalog, "+
			"so the planner has something to build Containers with", brief.Namespace)}
	}
	status := &brief.Status
	taken := model.RoundPrompts(brief.Spec.Prompts, int(status.PromptsPlanned))
	status.Round++
	status.RoundPrompts = int32(len(brief.Spec.Prompts))
	// A new round answers the planner's question, if it had one.
	setCondition(&status.Conditions, brief.Generation, c4v1alpha1.ConditionNeedsInput,
		metav1.ConditionFalse, c4v1alpha1.ReasonResolved, "The Brief has what it needs")
	if err := r.Status().Patch(ctx, brief, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})); err != nil {
		if apierrors.IsConflict(err) {
			return fmt.Errorf("%w: %w", errStaleBrief, err)
		}
		return err
	}
	*orig = *brief.DeepCopy()
	logf.FromContext(ctx).Info("Started Brief round", "round", status.Round, "newPrompts", len(taken))
	return r.ensureRoundTask(ctx, brief)
}

// ensureRoundTask makes the Plan Task of the Brief's round, owned by the Brief
// so it goes with it.
func (r *BriefReconciler) ensureRoundTask(ctx context.Context, brief *c4v1alpha1.Brief) error {
	round := brief.Status.Round
	task := &c4v1alpha1.Task{
		ObjectMeta: metav1.ObjectMeta{Namespace: brief.Namespace, Name: planTaskName(brief.Name, round),
			Labels: map[string]string{
				c4v1alpha1.LabelProject:    brief.Namespace,
				c4v1alpha1.LabelBrief:      brief.Name,
				c4v1alpha1.LabelBriefRound: strconv.Itoa(int(round)),
			}},
		Spec: c4v1alpha1.TaskSpec{
			Kind:     c4v1alpha1.TaskPlan,
			BriefRef: &c4v1alpha1.LocalRef{Name: brief.Name},
			AgentRef: brief.Spec.Agents.Planner,
		},
	}
	if err := controllerutil.SetControllerReference(brief, task, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, task); err != nil {
		return client.IgnoreAlreadyExists(err)
	}
	logf.FromContext(ctx).Info("Created Plan Task", "task", task.Name, "round", round)
	return nil
}

// readPlan reads plan/ of the round's branch. ok is false while the branch is
// not at commit yet.
func (r *BriefReconciler) readPlan(ctx context.Context, brief *c4v1alpha1.Brief, commit string) (
	tree scaffold.Tree, ok bool, err error) {
	provider, err := r.briefGit(ctx, brief)
	if err != nil {
		return nil, false, err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	repo := provider.Repository(model.BriefRepository(brief.Name))
	tree, sha, err := provider.ReadFiles(gitCtx, repo, model.PlanBranch(int(brief.Status.Round)),
		[]string{model.PlanDir + "/"})
	if err != nil {
		return nil, false, err
	}
	return tree, sha == commit, nil
}

// planFiles are a plan's files by path, as ParsePlan reads them.
func planFiles(tree scaffold.Tree) map[string]string {
	files := make(map[string]string, len(tree))
	for p, f := range tree {
		files[p] = string(f.Data)
	}
	return files
}

// currentModel is what the Brief's plans are checked against. Crews have no
// designer yet, so a design idea needs acceptance criteria too.
func (r *BriefReconciler) currentModel(ctx context.Context, brief *c4v1alpha1.Brief) (model.CurrentModel, error) {
	current := model.CurrentModel{Namespace: brief.Namespace, GitProvider: brief.Spec.GitProviderRef.Name}
	var err error
	if current.Objects, err = storyObjects(ctx, r.Client, brief.Namespace); err != nil {
		return current, err
	}
	current.Catalog, err = installedCatalog(ctx, r.Client, brief.Namespace)
	return current, err
}

// checkPlan checks the plan the round's Task pushed and writes the verdict on
// the Task: a plan that doesn't parse or that CheckPlan refuses is refused
// with the reasons; one that asks for input is accepted unchecked.
func (r *BriefReconciler) checkPlan(ctx context.Context, brief *c4v1alpha1.Brief, task *c4v1alpha1.Task) (
	ctrl.Result, error) {
	commit := task.Status.Commit
	if commit == "" || task.Annotations[c4v1alpha1.AnnotationPlanChecked] == commit {
		return ctrl.Result{}, nil
	}
	tree, ok, err := r.readPlan(ctx, brief, commit)
	if _, needsInput := needsInputSentence(err); needsInput {
		return ctrl.Result{}, err
	}
	if err != nil || !ok {
		return r.awaitPlan(ctx, brief, task, err)
	}
	var refusals string
	plan, err := model.ParsePlan(planFiles(tree))
	switch {
	case err != nil:
		refusals = err.Error()
	case plan.NeedsInput == "":
		current, err := r.currentModel(ctx, brief)
		if err != nil {
			return ctrl.Result{}, err
		}
		refusals = model.RefusalText(append(model.CheckPlan(plan, current),
			model.CheckAnswers(plan.Answers, waitingSuggestions(brief))...))
	}
	if err := r.writeVerdict(ctx, task, commit, refusals); err != nil {
		return ctrl.Result{}, err
	}
	log := logf.FromContext(ctx)
	if refusals != "" {
		log.Info("Refused Brief plan", "round", brief.Status.Round, "commit", commit, "reasons", firstLine(refusals))
		r.event(brief, corev1.EventTypeWarning, eventPlanRefused, "CheckPlan",
			fmt.Sprintf("Refused the plan of round %d (try %d):\n%s", brief.Status.Round, task.Status.Round, refusals))
		return ctrl.Result{}, nil
	}
	log.Info("Accepted Brief plan", "round", brief.Status.Round, "commit", commit)
	return ctrl.Result{}, nil
}

// awaitPlan waits for the plan the Task reports to show on plan/<round>, as
// a read of the branch can lag its push. A plan that isn't there once the Task
// has been Checking for PlanCheckTimeout is refused, so the planner pushes it
// again, or the Task fails after its tries, instead of waiting forever. A Git
// host down that long costs the planner a try.
func (r *BriefReconciler) awaitPlan(ctx context.Context, brief *c4v1alpha1.Brief, task *c4v1alpha1.Task,
	readErr error) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	commit, branch := task.Status.Commit, model.PlanBranch(int(brief.Status.Round))
	var waited time.Duration
	if task.Status.PhaseChangedAt != nil {
		waited = time.Since(task.Status.PhaseChangedAt.Time)
	}
	if left := r.planCheckTimeout() - waited; left > 0 {
		if readErr != nil {
			log.Info("Could not read the plan", "branch", branch, "error", readErr.Error())
		} else {
			log.V(1).Info("Waiting for the plan on the brief repository", "branch", branch, "commit", commit)
		}
		return ctrl.Result{RequeueAfter: min(planPushRecheck, left+time.Second)}, nil
	}
	refusal := fmt.Sprintf("The plan isn't on %s at %s; push it again", branch, commit)
	if err := r.writeVerdict(ctx, task, commit, refusal); err != nil {
		return ctrl.Result{}, err
	}
	log.Info("Refused Brief plan", "round", brief.Status.Round, "commit", commit, "reasons", refusal)
	r.event(brief, corev1.EventTypeWarning, eventPlanRefused, "CheckPlan",
		fmt.Sprintf("Refused the plan of round %d (try %d): %s", brief.Status.Round, task.Status.Round, refusal))
	return ctrl.Result{}, nil
}

// writeVerdict records the Brief's verdict on commit as the Task's
// annotations; no refusals accepts it.
func (r *BriefReconciler) writeVerdict(ctx context.Context, task *c4v1alpha1.Task, commit, refusals string) error {
	orig := task.DeepCopy()
	if task.Annotations == nil {
		task.Annotations = map[string]string{}
	}
	task.Annotations[c4v1alpha1.AnnotationPlanChecked] = commit
	if refusals == "" {
		delete(task.Annotations, c4v1alpha1.AnnotationPlanRefusals)
	} else {
		task.Annotations[c4v1alpha1.AnnotationPlanRefusals] = refusals
	}
	return r.Patch(ctx, task, client.MergeFrom(orig))
}

// failRound records a round whose Plan Task failed: the planner ran out of
// tries, or couldn't run.
func (r *BriefReconciler) failRound(ctx context.Context, brief *c4v1alpha1.Brief, task *c4v1alpha1.Task) {
	message := "the Plan Task failed"
	if cond := meta.FindStatusCondition(task.Status.Conditions, ConditionSucceeded); cond != nil && cond.Message != "" {
		message = cond.Message
	}
	endRoundFailed(ctx, brief, task, message)
}

// endRoundFailed records a round that failed, and why.
func endRoundFailed(ctx context.Context, brief *c4v1alpha1.Brief, task *c4v1alpha1.Task, message string) {
	status := &brief.Status
	recordRound(status, task, c4v1alpha1.BriefRound{Outcome: model.ReasonRoundFailed, Summary: task.Status.Summary})
	setCondition(&status.Conditions, brief.Generation, c4v1alpha1.ConditionPlanned, metav1.ConditionFalse,
		model.ReasonRoundFailed, model.Clip(fmt.Sprintf("Round %d failed: %s", status.Round, message), 32768))
	logf.FromContext(ctx).Info("Brief round failed", "round", status.Round, "message", firstLine(message))
}

// plannerNeedsInput records a round whose planner asked the person for input.
func (r *BriefReconciler) plannerNeedsInput(ctx context.Context, brief *c4v1alpha1.Brief, task *c4v1alpha1.Task,
	plan model.Plan) {
	status := &brief.Status
	sentence := model.Clip(plan.NeedsInput, 1000)
	recordRound(status, task, c4v1alpha1.BriefRound{Outcome: model.ReasonPlannerNeedsInput,
		Summary: model.RoundSummary(plan),
		Choices: plan.Choices, NeedsInput: sentence})
	setCondition(&status.Conditions, brief.Generation, c4v1alpha1.ConditionNeedsInput, metav1.ConditionTrue,
		model.ReasonPlannerNeedsInput, sentence)
	setCondition(&status.Conditions, brief.Generation, c4v1alpha1.ConditionPlanned, metav1.ConditionFalse,
		model.ReasonPlannerNeedsInput, fmt.Sprintf("Round %d asks for input; add a prompt that answers it", status.Round))
	logf.FromContext(ctx).Info("Planner needs input for Brief", "round", status.Round, "reason", sentence)
}

// recordRound adds the running round to the history as entry says it ended,
// with what its Plan Task cost, once; the newest 20 are kept.
func recordRound(status *c4v1alpha1.BriefStatus, task *c4v1alpha1.Task, entry c4v1alpha1.BriefRound) {
	if roundOver(status) {
		return
	}
	entry.CostMilliUSD, entry.RunsWithoutCost = task.Status.CostMilliUSD, task.Status.RunsWithoutCost
	status.CostMilliUSD += entry.CostMilliUSD
	status.RunsWithoutCost += entry.RunsWithoutCost
	entry.Round = status.Round
	entry.Summary = model.ClipSentences(entry.Summary, 1000)
	entry.Choices = model.ClipList(entry.Choices, 50, 500)
	status.History = model.KeepNewest(append(status.History, entry), 20)
}

// operatorNeedsInput sets the operator's own NeedsInput.
func operatorNeedsInput(brief *c4v1alpha1.Brief, sentence string) {
	setCondition(&brief.Status.Conditions, brief.Generation, c4v1alpha1.ConditionNeedsInput,
		metav1.ConditionTrue, c4v1alpha1.ReasonNotFound, sentence)
}

// clearOperatorNeedsInput ends the operator's own NeedsInput. The planner's
// question stays open, or comes back if the operator's wait showed over it,
// until a new round answers it.
func clearOperatorNeedsInput(brief *c4v1alpha1.Brief) {
	status := &brief.Status
	cond := meta.FindStatusCondition(status.Conditions, c4v1alpha1.ConditionNeedsInput)
	if cond != nil && cond.Reason == model.ReasonPlannerNeedsInput {
		return
	}
	if n := len(status.History); roundOver(status) && status.History[n-1].Outcome == model.ReasonPlannerNeedsInput {
		setCondition(&status.Conditions, brief.Generation, c4v1alpha1.ConditionNeedsInput,
			metav1.ConditionTrue, model.ReasonPlannerNeedsInput, status.History[n-1].NeedsInput)
		return
	}
	setCondition(&status.Conditions, brief.Generation, c4v1alpha1.ConditionNeedsInput,
		metav1.ConditionFalse, c4v1alpha1.ReasonResolved, "The Brief has what it needs")
}

// setPhase derives the Brief's phase from its round, and says when it starts
// waiting for the person.
func (r *BriefReconciler) setPhase(ctx context.Context, brief *c4v1alpha1.Brief) {
	task, _ := r.roundTask(ctx, brief) // a Task in the way already set NeedsInput
	phase := c4v1alpha1.BriefPhase(model.BriefPhaseOf(roundFacts(brief, task)))
	if phase == brief.Status.Phase {
		return
	}
	brief.Status.Phase = phase
	logf.FromContext(ctx).Info("Changed Brief phase", "phase", phase, "round", brief.Status.Round)
	if phase == c4v1alpha1.BriefNeedsInput {
		message := ""
		if cond := meta.FindStatusCondition(brief.Status.Conditions, c4v1alpha1.ConditionNeedsInput); cond != nil {
			message = cond.Message
		}
		r.event(brief, corev1.EventTypeWarning, eventNeedsInput, "WaitForInput", message)
	}
}

// event emits one of the Brief's Events; a reconciler without a recorder emits none.
func (r *BriefReconciler) event(brief *c4v1alpha1.Brief, eventType, reason, action, note string) {
	if r.Recorder != nil {
		// An Event's note holds at most 1kB.
		r.Recorder.Eventf(brief, nil, eventType, reason, action, "%s", model.Clip(note, 1000))
	}
}

// briefAuthor signs the operator's commits on a brief repository.
func briefAuthor() gitprovider.Signature {
	return gitprovider.Signature{Name: scaffold.AuthorName, Email: scaffold.AuthorEmail, When: time.Now()}
}
