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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// A Task with a verifier (docs/design.md, "Verify") has the change its
// reviewer approved verified before it merges, in the phase Verifying: the
// review's approval goes on the pull request and what it left for the merge
// (its follow-ups and open criteria) into status.review, as its run ends;
// then the verifier's own sandbox builds and starts the change at the
// reviewed head and checks each acceptance criterion against what runs,
// pushing nothing. Its approval merges the change, as a review's does; its
// request for changes starts the next round with its evidence. Its verdict
// stays in status.verification.

// verifies reports whether the Task's approved change goes to its verifier
// before it merges: it names one, and is an Implement Task without
// allowedProtectedPaths, as CEL requires of a verifierRef anyway.
func verifies(task *c4v1beta1.Task) bool {
	kind := task.Spec.Kind
	return task.Spec.VerifierRef != nil && (kind == "" || kind == c4v1beta1.TaskImplement) &&
		len(task.Spec.AllowedProtectedPaths) == 0
}

// verifyFacts adds whether the Task has a verifier, and records the
// verifier's verdict in status once its run gave one, whatever step follows.
func verifyFacts(task *c4v1beta1.Task, status *c4v1beta1.TaskStatus, facts *model.TaskFacts) {
	facts.Verify = task.Spec.VerifierRef != nil
	run := facts.Run
	if status.Phase != c4v1beta1.TaskVerifying || !run.Done || run.Result == nil || run.Result.Verdict == "" {
		return
	}
	commit := run.Result.Commit
	if commit == "" {
		commit = status.Commit
	}
	status.Verification = &c4v1beta1.TaskVerification{Verdict: run.Result.Verdict, Summary: run.Result.Summary,
		Commit: commit, Open: taskOpen(run.Result.Open)}
}

// applyVerify does a step only a verified Task takes: have the change the
// reviewer approved verified, or run the verifier again after a run without a
// verdict.
func (r *TaskReconciler) applyVerify(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents, step model.TaskStep) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if step.Action == model.ActionRetryVerify {
		r.progress(task, status, metav1.ConditionTrue, "VerifierFailed", step.Message)
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		setProgress(status, model.RetryReview(progressOf(status)))
		return ctrl.Result{}, nil // the status write triggers the next step
	}
	// The review's run ends here: what it left for the merge stays in status.
	status.Review = r.reviewFindings(ctx, task, status)
	r.approve(ctx, task, status, container, agents.reviewer)
	if err := r.endRun(ctx, task, status); err != nil {
		return ctrl.Result{}, err
	}
	setProgress(status, model.VerifyChange(progressOf(status)))
	r.progress(task, status, metav1.ConditionTrue, model.ReasonVerifying, step.Message)
	log.Info("Sent an approved Task to its verifier", "round", status.Round, "commit", status.Commit,
		"verifier", task.Spec.VerifierRef)
	return ctrl.Result{}, nil // the status write triggers the next step
}

// reviewFindings is the current review's verdict with what it left for the
// merge, its follow-ups and the criteria it accepted as open, which outlive
// its run in status.review; status.review when no review run gave a verdict.
func (r *TaskReconciler) reviewFindings(ctx context.Context, task *c4v1beta1.Task,
	status *c4v1beta1.TaskStatus) *c4v1beta1.TaskReview {
	run := r.reviewRun(ctx, task, status)
	if run == nil {
		return status.Review
	}
	review := &c4v1beta1.TaskReview{Verdict: run.Verdict, Summary: run.Summary, Open: taskOpen(run.Open)}
	for _, f := range run.FollowUps[:min(len(run.FollowUps), model.MaxFollowUps)] {
		review.FollowUps = append(review.FollowUps, c4v1beta1.TaskFollowUp{Title: f.Title, Body: f.Body,
			Criteria: f.Criteria[:min(len(f.Criteria), model.MaxFollowUpCriteria)]})
	}
	return review
}

// postVerification posts the verifier's verdict on the round's commit as a
// review of the pull request: its request for changes, or its approval as
// the change merges.
func (r *TaskReconciler) postVerification(ctx context.Context, task *c4v1beta1.Task,
	status *c4v1beta1.TaskStatus, container *c4v1beta1.Container, verifier *c4v1beta1.Agent, event string) {
	v := status.Verification
	if v == nil || verifier == nil {
		return
	}
	r.postReview(ctx, task, status, container, event,
		fmt.Sprintf("Verification by %s, round %d.\n\n%s", agentName(verifier), status.Round, v.Summary))
}

// mergeFindings are what a merge files: the approving review's follow-ups and
// the criteria it left open, from its run, or from status.review once the run
// ended for the change's verification; then the criteria the verifier left
// open, which the review didn't name. openBy names the Agents that left them.
func mergeFindings(task *c4v1beta1.Task, status *c4v1beta1.TaskStatus, agents taskAgents,
	reviewRun *model.RunResult) (followUps []model.FollowUp, open []model.OpenCriterion, openBy string) {
	openBy = agentName(agents.reviewer)
	switch {
	case reviewRun != nil:
		followUps, open = reviewRun.FollowUps, reviewRun.Open
	case status.Review != nil:
		for _, f := range status.Review.FollowUps {
			followUps = append(followUps, model.FollowUp{Title: f.Title, Body: f.Body, Criteria: f.Criteria})
		}
		open = modelOpen(status.Review.Open)
	}
	if !verifies(task) || status.Verification == nil || len(status.Verification.Open) == 0 ||
		agents.verifier == nil {
		return followUps, open, openBy
	}
	if len(open) == 0 {
		openBy = agentName(agents.verifier)
	} else {
		openBy += " and " + agentName(agents.verifier)
	}
	return followUps, model.OpenCriteria(open, modelOpen(status.Verification.Open)), openBy
}

// taskOpen is open as status keeps it, at most model.MaxFollowUpCriteria;
// nil for none.
func taskOpen(open []model.OpenCriterion) []c4v1beta1.TaskOpenCriterion {
	if len(open) == 0 {
		return nil
	}
	out := make([]c4v1beta1.TaskOpenCriterion, 0, min(len(open), model.MaxFollowUpCriteria))
	for _, o := range open[:cap(out)] {
		out = append(out, c4v1beta1.TaskOpenCriterion{Criterion: o.Criterion, Why: o.Why})
	}
	return out
}

// modelOpen is open as the model reads it; nil for none.
func modelOpen(open []c4v1beta1.TaskOpenCriterion) []model.OpenCriterion {
	if len(open) == 0 {
		return nil
	}
	out := make([]model.OpenCriterion, 0, len(open))
	for _, o := range open {
		out = append(out, model.OpenCriterion{Criterion: o.Criterion, Why: o.Why})
	}
	return out
}
