/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// A Brief tends its own handed-back work (docs/plans/done/brief-autopilot.md): while
// its autopilot is Ask or On, each reconcile classifies what it made as
// healing or needing the person, and a failed issue that is due starts a
// tending round, a round with no new prompt that decides it.

// The Brief's tending Events.
const (
	// eventTended says what a tending round did, or why it failed.
	eventTended = "Tended"
	// eventNeedsYou says an item first needs the person.
	eventNeedsYou = "NeedsYou"
	// reasonNeedsYouWaiting is NeedsYou's reason while an item waits.
	reasonNeedsYouWaiting = "Waiting"
	// maxAttention is how many healing items the status lists.
	maxAttention = 100
)

// tending reports whether the Brief's round is a tending round.
func tending(status *c4v1alpha1.BriefStatus) bool {
	return status.Round > 0 && status.TendRound == status.Round
}

// attentionFacts gathers what the Brief watches: its failed issues and what
// rounds answered them, the Tasks of the Containers it made that are not
// final, its Crews that are not Ready and its Containers, and when a Task of
// its Containers last merged or one of its apps last released.
func (r *BriefReconciler) attentionFacts(ctx context.Context, brief *c4v1alpha1.Brief) (model.AttentionFacts, error) {
	status := &brief.Status
	facts := model.AttentionFacts{Tended: modelTended(status.Tended)}
	for _, f := range status.Failed {
		facts.Failed = append(facts.Failed, model.FailedIssue{Container: f.Container, Number: int(f.Number),
			Title: f.Title, URL: f.URL, Kind: f.Kind, Reason: f.Reason, Tries: int(f.Tries)})
	}
	mine := client.MatchingLabels{c4v1alpha1.LabelBrief: brief.Name}
	var containers c4v1alpha1.ContainerList
	if err := r.List(ctx, &containers, client.InNamespace(brief.Namespace), mine); err != nil {
		return facts, err
	}
	made := map[string]bool{}
	for _, c := range containers.Items {
		made[c.Name] = true
		watched := model.WatchedContainer{Name: c.Name, Phase: string(c.Status.Phase), Since: c.CreationTimestamp.Time}
		for _, cond := range c.Status.Conditions {
			if cond.Status == metav1.ConditionFalse {
				watched.Message, watched.Since = cond.Message, cond.LastTransitionTime.Time
				break
			}
		}
		facts.Containers = append(facts.Containers, watched)
	}
	var crews c4v1alpha1.CrewList
	if err := r.List(ctx, &crews, client.InNamespace(brief.Namespace), mine); err != nil {
		return facts, err
	}
	for _, crew := range crews.Items {
		cond := meta.FindStatusCondition(crew.Status.Conditions, ConditionReady)
		if cond != nil && cond.Status == metav1.ConditionFalse {
			facts.Crews = append(facts.Crews, model.WatchedCrew{Name: crew.Name, Reason: cond.Reason,
				Message: cond.Message, Since: cond.LastTransitionTime.Time})
		}
	}
	var tasks c4v1alpha1.TaskList
	if err := r.List(ctx, &tasks, client.InNamespace(brief.Namespace)); err != nil {
		return facts, err
	}
	for _, t := range tasks.Items {
		if !made[t.Spec.ContainerRef.Name] {
			continue
		}
		switch t.Status.Phase {
		case c4v1alpha1.TaskMerged:
			if at := t.Status.PhaseChangedAt; at != nil && at.After(facts.Changed) {
				facts.Changed = at.Time
			}
		case c4v1alpha1.TaskFailed:
		default:
			facts.Tasks = append(facts.Tasks, watchedTask(&t))
		}
	}
	for _, app := range status.Apps {
		if app.LastRelease != nil && app.LastRelease.Time != nil && app.LastRelease.Time.After(facts.Changed) {
			facts.Changed = app.LastRelease.Time.Time
		}
	}
	return facts, nil
}

// watchedTask is a Task that is not final as model.BriefAttention sees it:
// its stage and since when, as the Factory view shows them.
func watchedTask(t *c4v1alpha1.Task) model.WatchedTask {
	w := model.WatchedTask{Name: t.Name, Container: t.Spec.ContainerRef.Name}
	if cond := meta.FindStatusCondition(t.Status.Conditions, "Progressing"); cond != nil {
		w.Reason, w.Message = cond.Reason, cond.Message
	}
	switch {
	case t.Status.SlotWaitSince != nil || w.Reason == "WaitingForSlot":
		w.Stage = model.StageSlot
	case t.Status.Phase == c4v1alpha1.TaskImplementing:
		w.Stage = model.StageImplement
	case t.Status.Phase == c4v1alpha1.TaskChecking:
		w.Stage = model.StageChecks
	case t.Status.Phase == c4v1alpha1.TaskReviewing:
		w.Stage = model.StageReview
	case t.Status.Phase == c4v1alpha1.TaskBlocked:
		w.Stage = model.StageBlocked
	default:
		w.Stage = model.StageQueued
	}
	switch {
	case t.Status.SlotWaitSince != nil:
		w.Since = t.Status.SlotWaitSince.Time
	case len(t.Status.History) > 0:
		w.Since = t.Status.History[len(t.Status.History)-1].At.Time
	case t.Status.PhaseChangedAt != nil:
		w.Since = t.Status.PhaseChangedAt.Time
	default:
		w.Since = t.CreationTimestamp.Time
	}
	if t.Status.AgentBackAt != nil {
		w.AgentBackAt = t.Status.AgentBackAt.Time
	}
	// The Standard change's own Task is in the Standard's repository, which
	// the Brief doesn't read: the change counts as in review until the
	// blocked window ends.
	if t.Status.StandardChange != nil {
		w.StandardChange = int(t.Status.StandardChange.Issue)
	}
	return w
}

// tend classifies what the Brief watches and records it: status.attention,
// status.needsYou, the NeedsYou condition and an Event per new entry. With
// autopilot Off it lists only the requests waiting in Ask. It returns the classification and when the
// Brief acts next on its own, zero when it waits for a change.
func (r *BriefReconciler) tend(ctx context.Context, brief *c4v1alpha1.Brief, now time.Time) (
	model.Attention, time.Time, error) {
	status := &brief.Status
	var att model.Attention
	if mode := model.AutopilotMode(brief.Spec.Autopilot); mode != model.AutopilotOff {
		facts, err := r.attentionFacts(ctx, brief)
		if err != nil {
			return att, time.Time{}, err
		}
		att = model.BriefAttention(facts, now)
	}
	// A request waiting in Ask is a door, whatever the autopilot says.
	att = model.WithNeedsYou(att, waitingRequests(brief))
	var next time.Time
	attention := make([]c4v1alpha1.BriefHealing, 0, len(att.Healing))
	for _, h := range att.Healing[:min(len(att.Healing), maxAttention)] {
		entry := c4v1alpha1.BriefHealing{Kind: h.Kind, Name: model.Clip(h.Name, 320), Reason: model.Clip(h.Reason, 1000)}
		if !h.ActsAt.IsZero() {
			entry.ActsAt = &metav1.Time{Time: h.ActsAt}
			if next.IsZero() || h.ActsAt.Before(next) {
				next = h.ActsAt
			}
		}
		attention = append(attention, entry)
	}
	needsYou := make([]c4v1alpha1.BriefNeedsYou, 0, len(att.NeedsYou))
	for _, n := range att.NeedsYou {
		entry := c4v1alpha1.BriefNeedsYou{Kind: n.Kind, Name: model.Clip(n.Name, 320),
			Question: model.Clip(n.Question, 1000), Recommend: model.Clip(n.Recommend, 1000), Why: model.Clip(n.Why, 1000)}
		if !n.Since.IsZero() {
			entry.Since = &metav1.Time{Time: n.Since}
		}
		if !slices.ContainsFunc(status.NeedsYou, func(s c4v1alpha1.BriefNeedsYou) bool {
			return s.Kind == n.Kind && s.Name == n.Name
		}) {
			logf.FromContext(ctx).Info("Something needs the person", "kind", n.Kind, "name", n.Name)
			r.event(brief, corev1.EventTypeNormal, eventNeedsYou, "Tend", n.Question+" "+n.Recommend)
		}
		needsYou = append(needsYou, entry)
	}
	status.Attention, status.NeedsYou = nil, nil
	if len(attention) > 0 {
		status.Attention = attention
	}
	if len(needsYou) > 0 {
		status.NeedsYou = needsYou
	}
	status.NeedsYouWaiting = int32(att.Waiting)
	switch {
	case att.Waiting > 0:
		setCondition(&status.Conditions, brief.Generation, c4v1alpha1.ConditionNeedsYou, metav1.ConditionTrue,
			reasonNeedsYouWaiting, model.Clip(model.NeedsYouMessage(att.NeedsYou, att.Waiting), 32768))
	case meta.FindStatusCondition(status.Conditions, c4v1alpha1.ConditionNeedsYou) != nil:
		setCondition(&status.Conditions, brief.Generation, c4v1alpha1.ConditionNeedsYou, metav1.ConditionFalse,
			c4v1alpha1.ReasonResolved, model.NeedsYouMessage(nil, 0))
	}
	if len(att.Due) > 0 && status.LastTendTime != nil {
		if at := status.LastTendTime.Add(model.TendInterval); at.After(now) && (next.IsZero() || at.Before(next)) {
			next = at
		}
	}
	return att, next, nil
}

// tendFacts is what a tending round's start depends on. A Brief whose last
// round didn't apply waits for a prompt first: its planner asked a question,
// or its round failed, and a tending round would hide that.
func tendFacts(brief *c4v1alpha1.Brief, due int) model.TendFacts {
	status := &brief.Status
	facts := model.TendFacts{Mode: brief.Spec.Autopilot, Round: int(status.Round), RoundOver: roundOver(status),
		PromptWaiting: len(briefPrompts(brief)) > int(status.RoundPrompts) ||
			model.NewRequests(modelRequests(status.Requests), heldRequests(status)) > 0,
		Due: due, Paused: brief.Spec.Paused,
		OperatorNeedsInput: operatorWaits(status) ||
			!meta.IsStatusConditionTrue(status.Conditions, c4v1alpha1.ConditionPlanned)}
	if status.LastTendTime != nil {
		facts.LastTend = status.LastTendTime.Time
	}
	return facts
}

// tendRound classifies what the Brief watches and starts a tending round when
// a failed issue is due. It returns when to look again.
func (r *BriefReconciler) tendRound(ctx context.Context, brief, orig *c4v1alpha1.Brief) (ctrl.Result, error) {
	now := r.now()
	att, next, err := r.tend(ctx, brief, now)
	if err != nil {
		return ctrl.Result{}, err
	}
	if model.StartsTend(tendFacts(brief, len(att.Due)), now) {
		logf.FromContext(ctx).Info("Failed issues are due", "count", len(att.Due))
		return ctrl.Result{}, r.startRound(ctx, brief, orig, true)
	}
	if next.IsZero() {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: max(next.Sub(now), time.Second)}, nil
}

// applyTend applies a tending round's plan, each step idempotent like a
// round's: the new issues it files, then its answers to the failed issues,
// by the Brief's mode, and last the plan merged into main.
func (r *BriefReconciler) applyTend(ctx context.Context, brief, orig *c4v1alpha1.Brief, task *c4v1alpha1.Task) (
	ctrl.Result, error) {
	tree, ok, err := r.readPlan(ctx, brief, task.Status.Commit)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ok {
		return ctrl.Result{}, fmt.Errorf("branch %s moved past the accepted plan %s",
			model.PlanBranch(int(brief.Status.Round)), task.Status.Commit)
	}
	plan, err := model.ParseTendPlan(planFiles(tree))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reading the accepted plan: %w", err)
	}
	current, err := r.currentModel(ctx, brief)
	if err != nil {
		return ctrl.Result{}, err
	}
	if refusals := model.CheckTendPlan(plan, current); len(refusals) > 0 {
		r.endRoundFailed(ctx, brief, task, "the plan no longer fits the namespace: "+model.RefusalText(refusals))
		return ctrl.Result{}, nil
	}
	released, err := r.releasedContainers(ctx, brief, plan)
	if err != nil {
		return ctrl.Result{}, err
	}
	run := &roundApply{brief: brief, orig: orig, round: fmt.Sprint(brief.Status.Round), released: released,
		fixes: model.RoundFixes(plan)}
	waiting, err := r.finishContainers(ctx, run, model.RoundContainers(nil, plan.Issues), nil, plan.Issues)
	if err != nil {
		return ctrl.Result{}, r.applyFailed(ctx, brief, "filing the issues", err)
	}
	if len(waiting) > 0 {
		return ctrl.Result{RequeueAfter: r.needsInputRecheck()}, nil
	}
	facts, err := r.attentionFacts(ctx, brief)
	if err != nil {
		return ctrl.Result{}, err
	}
	answers := model.TendAnswers(brief.Spec.Autopilot, plan.FailedAnswers,
		model.BriefAttention(facts, r.now()).Due, true)
	if run.retried, run.closed, err = r.answerFailedRemembering(ctx, brief, answers); err != nil {
		return ctrl.Result{}, r.applyFailed(ctx, brief, "answering the failed issues", err)
	}
	if err := r.mergePlan(ctx, brief, task.Status.Commit); err != nil {
		return ctrl.Result{}, r.applyFailed(ctx, brief, "merging the plan", err)
	}
	r.tendApplied(ctx, brief, task, plan, answers)
	return ctrl.Result{}, nil
}

// answerFailedRemembering answers the failed issues as answerFailed does and
// remembers every answer in status.tended, so an answered issue is raised
// again only when it changed.
func (r *BriefReconciler) answerFailedRemembering(ctx context.Context, brief *c4v1alpha1.Brief,
	answers []model.FailedAnswer) (retried, closed int, err error) {
	if len(answers) == 0 {
		return 0, 0, nil
	}
	status := &brief.Status
	failed := make([]model.FailedIssue, 0, len(status.Failed))
	for _, f := range status.Failed {
		failed = append(failed, model.FailedIssue{Container: f.Container, Number: int(f.Number), Tries: int(f.Tries)})
	}
	status.Tended = apiTended(model.Tended(modelTended(status.Tended), answers, failed, int(status.Round), r.now()))
	return r.answerFailed(ctx, brief, answers)
}

// tendApplied records a tending round that applied: a Tended history entry
// and Event. The story's state, the Planned condition and the prompts
// planned, is the last prompt round's and stays.
func (r *BriefReconciler) tendApplied(ctx context.Context, brief *c4v1alpha1.Brief, task *c4v1alpha1.Task,
	plan model.Plan, answers []model.FailedAnswer) {
	status := &brief.Status
	summary := model.TendSummary(brief.Spec.Autopilot, plan.Summary, answers)
	recordRound(status, task, c4v1alpha1.BriefRound{Outcome: model.ReasonTended, Summary: summary})
	retried, closed, waited, asked := model.TendCounts(answers)
	r.event(brief, corev1.EventTypeNormal, eventTended, "Tend",
		fmt.Sprintf("Tending round %d retried %d, closed %d, waits on %d and asks you about %d failed issues, "+
			"and filed %d issues. %s", status.Round, retried, closed, waited, asked, len(plan.Issues), summary))
	logf.FromContext(ctx).Info("Applied tending round", "round", status.Round, "retried", retried,
		"closed", closed, "waited", waited, "asked", asked, "issues", len(plan.Issues))
}

// modelTended is status.tended as the model sees it.
func modelTended(tended []c4v1alpha1.BriefTendedIssue) []model.TendedIssue {
	out := make([]model.TendedIssue, 0, len(tended))
	for _, t := range tended {
		m := model.TendedIssue{Container: t.Container, Issue: int(t.Number), Answer: t.Answer, Why: t.Why,
			Tries: int(t.Tries), At: t.At.Time, Round: int(t.Round)}
		if rec := t.Recommend; rec != nil {
			m.Recommend = &model.Recommendation{Answer: rec.Answer, Why: rec.Why, Clarify: rec.Clarify}
		}
		out = append(out, m)
	}
	return out
}

// apiTended is the model's answered issues as status.tended holds them.
func apiTended(tended []model.TendedIssue) []c4v1alpha1.BriefTendedIssue {
	if len(tended) == 0 {
		return nil
	}
	out := make([]c4v1alpha1.BriefTendedIssue, 0, len(tended))
	for _, t := range tended {
		a := c4v1alpha1.BriefTendedIssue{Container: t.Container, Number: int32(t.Issue), Answer: t.Answer,
			Why: model.Clip(t.Why, 1000), Tries: int32(t.Tries), At: metav1.Time{Time: t.At}, Round: int32(t.Round)}
		if rec := t.Recommend; rec != nil {
			a.Recommend = &c4v1alpha1.BriefRecommendation{Answer: rec.Answer, Why: model.Clip(rec.Why, 1000),
				Clarify: model.Clip(rec.Clarify, model.MaxClarify)}
		}
		out = append(out, a)
	}
	return out
}
