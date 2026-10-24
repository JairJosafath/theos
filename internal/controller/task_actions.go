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
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// taskAgents are the Agents of a Task's roles; one Agent may play both.
type taskAgents struct {
	implementer, reviewer *c4v1alpha1.Agent
}

func (a taskAgents) forRole(role string) *c4v1alpha1.Agent {
	if role == model.RoleReviewer {
		return a.reviewer
	}
	return a.implementer
}

// apply does one step and records it in status.
func (r *TaskReconciler) apply(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, agents taskAgents, step model.TaskStep) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	switch step.Action {
	case model.ActionWait:
		return r.wait(task, status, step), nil

	case model.ActionStartImplement, model.ActionStartReview:
		role := model.RoleImplementer
		if step.Action == model.ActionStartReview {
			role = model.RoleReviewer
		}
		return r.startRun(ctx, task, status, container, agents, role, step.Message)

	case model.ActionOpenPR:
		return r.openPR(ctx, task, status, container, agents, step)

	case model.ActionPropose:
		return r.propose(ctx, task, status, container, step)

	case model.ActionResume:
		r.resume(ctx, task, status, container, step)
		return ctrl.Result{}, nil // the status write triggers the next step

	case model.ActionDoneByStandardChange:
		return ctrl.Result{}, r.doneByStandardChange(ctx, task, status, container, step)

	case model.ActionRetryReview:
		r.progress(task, status, metav1.ConditionTrue, "ReviewerFailed", step.Message)
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		setProgress(status, model.RetryReview(progressOf(status)))
		return ctrl.Result{}, nil // the status write triggers the next step

	case model.ActionMerge:
		return r.merge(ctx, task, status, container, agents, step)

	case model.ActionUpdateBranch:
		return r.updateBranch(ctx, task, status, container, agents, step)

	case model.ActionResolveConflicts:
		review := r.reviewFrom(ctx, task, status)
		r.comment(ctx, task, status, container, step.Message+".")
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		status.Review = review
		setProgress(status, model.ResolveConflicts(progressOf(status), step.Feedback))
		r.progress(task, status, metav1.ConditionTrue, "ResolvingConflicts", step.Message)
		log.Info("Sent an approved Task back to resolve conflicts", "round", status.Round)
		return ctrl.Result{}, nil // the status write triggers the next step

	case model.ActionRerun:
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		setProgress(status, model.Rerun(progressOf(status)))
		r.progress(task, status, metav1.ConditionTrue, "Interrupted", step.Message)
		log.Info("Rerunning an interrupted run", "round", status.Round, "reruns", status.Reruns)
		return ctrl.Result{}, nil // the status write triggers the next step

	case model.ActionRerunChecks:
		return r.rerunChecks(ctx, task, status, container, agents, step)

	case model.ActionAgentUnavailable:
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		back := model.AgentBackAt(step.RetryAt, r.now())
		status.AgentBackAt = &metav1.Time{Time: back}
		r.progress(task, status, metav1.ConditionTrue, "AgentUnavailable",
			fmt.Sprintf("%s; trying again at %s", step.Message, back.UTC().Format(time.RFC3339)))
		log.Info("Agent unavailable; Task waits", "until", back, "message", step.Message)
		return ctrl.Result{RequeueAfter: back.Sub(r.now())}, nil

	case model.ActionRetry:
		review := r.reviewFrom(ctx, task, status)
		if review != nil && review.Verdict == model.VerdictChanges && status.Phase == c4v1alpha1.TaskReviewing {
			r.review(ctx, task, status, container, agents.reviewer, gitprovider.ReviewRequestChanges, review.Summary)
		} else {
			r.comment(ctx, task, status, container, fmt.Sprintf("Round %d was not accepted.\n\n%s", status.Round, step.Feedback))
		}
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		if review != nil {
			status.Review = review
		}
		setProgress(status, model.NextRound(progressOf(status), step.Feedback))
		r.progress(task, status, metav1.ConditionTrue, "NextRound", step.Message)
		log.Info("Started next round of Task", "round", status.Round, "feedback", firstLine(step.Feedback))
		return ctrl.Result{}, nil // the status write triggers the next step

	case model.ActionReviewAgain:
		r.comment(ctx, task, status, container, step.Message+".")
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		setProgress(status, model.ReviewAgain(progressOf(status)))
		r.progress(task, status, metav1.ConditionTrue, "ApprovalEnded", step.Message)
		log.Info("Checking and reviewing an approved Standard change again", "round", status.Round)
		return ctrl.Result{}, nil // the status write triggers the next step

	case model.ActionFail:
		if review := r.reviewFrom(ctx, task, status); review != nil {
			status.Review = review
		}
		status.Feedback = step.Feedback
		reason := model.ReasonFailed
		if step.Reason != "" {
			reason = step.Reason
		}
		return ctrl.Result{}, r.fail(ctx, task, status, container, reason, step.Message)
	}
	return ctrl.Result{}, nil
}

// wait records what the Task waits for, and when to look again.
func (r *TaskReconciler) wait(task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus, step model.TaskStep) ctrl.Result {
	if status.Phase == c4v1alpha1.TaskBlocked {
		// The Task also wakes when one on its Container changes phase, as the
		// change's Task does when it ends.
		r.progress(task, status, metav1.ConditionTrue, "WaitingForStandardChange", step.Message)
		return ctrl.Result{RequeueAfter: r.standardChangeRecheck()}
	}
	reason := "Waiting"
	if step.SlotWait {
		reason = "WaitingForSlot"
	}
	if status.AgentBackAt != nil && status.AgentBackAt.After(r.now()) && !step.SlotWait {
		reason = "AgentUnavailable"
	}
	if step.Reason != "" {
		reason = step.Reason
	}
	r.progress(task, status, metav1.ConditionTrue, reason, step.Message)
	if status.Phase == c4v1alpha1.TaskChecking || step.Recheck {
		return ctrl.Result{RequeueAfter: r.checksRecheck()}
	}
	if reason == "AgentUnavailable" {
		return ctrl.Result{RequeueAfter: status.AgentBackAt.Sub(r.now())}
	}
	return ctrl.Result{}
}

// openPR opens the round's pull request as the agent bot, and moves on to the checks.
func (r *TaskReconciler) openPR(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, agents taskAgents, step model.TaskStep) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	provider, repo, err := r.agentGitRepo(ctx, container)
	if err != nil {
		return ctrl.Result{}, err
	}
	run, err := runState(ctx, r.Client, task.Namespace, status.Sandbox, status.SandboxPod)
	if err != nil || run.Result == nil {
		return ctrl.Result{}, fmt.Errorf("reading the implement result of %s: %v", status.Sandbox, err)
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	pr, err := provider.OpenPullRequest(gitCtx, repo, status.Branch, gitprovider.DefaultBranch,
		pullTitle(task, status), pullBody(task, agents, status.Round, run.Result.Summary, run.Result.Extra))
	if err != nil {
		return ctrl.Result{}, err
	}
	log.Info("Opened pull request", "url", pr.URL, "commit", run.Result.Commit, "round", status.Round)
	reopened := status.PullRequest != 0
	ended, err := r.recordRun(ctx, task, status)
	if err != nil {
		return ctrl.Result{}, err
	}
	if step.Action == model.ActionRerunChecks {
		setProgress(status, model.PushedAgain(progressOf(status), run.Result.Commit))
	} else {
		setProgress(status, model.OpenedPR(progressOf(status), run.Result.Commit))
	}
	status.Summary = run.Result.Summary
	status.PullRequest, status.PullRequestURL = int32(pr.Number), pr.URL
	r.progress(task, status, metav1.ConditionTrue, "Checking", step.Message+"; waiting for checks")
	// Write the status before the sandbox goes and the notes are posted: when
	// the write fails, the next pass finds the run's result again, the pull
	// request open already, and no note posted yet (#906).
	if err := r.writeStatus(ctx, task, status); err != nil {
		return ctrl.Result{}, err
	}
	if reopened && len(run.Result.Extra) > 0 {
		// A later round's pull request is the first one, whose description stays.
		r.comment(ctx, task, status, container, fmt.Sprintf("Round %d: %s", status.Round,
			extrasNote(task, run.Result.Extra)))
	}
	if failed := run.Result.CheckFailsOnBase; failed != "" {
		r.comment(ctx, task, status, container, model.CheckFailsOnBaseComment(int(status.Round), failed,
			recordedChecks(&container.Status)))
		if r.Recorder != nil {
			r.Recorder.Eventf(task, nil, corev1.EventTypeNormal, eventCheckFailsOnBase, "Check", "%s",
				model.CheckFailsOnBaseEvent(int(status.Round), failed))
		}
	}
	return ctrl.Result{RequeueAfter: r.checksRecheck()}, r.dropRun(ctx, task, ended)
}

// startRun creates the sandbox for this round's role, after making sure the
// Container's agent Git access and the Agent's credentials exist.
func (r *TaskReconciler) startRun(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, agents taskAgents, role, message string) (ctrl.Result, error) {
	agent := agents.forRole(role)
	if found, err := r.agentCredentials(ctx, task, status, agent); err != nil {
		return ctrl.Result{}, err
	} else if !found {
		return ctrl.Result{RequeueAfter: r.secretRecheck()}, nil
	}
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return ctrl.Result{}, err
	}
	// Bring the Git host's rules up to date before an agent gets access: older
	// repositories were protected with fewer of them.
	protectCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	if err := protectRepository(protectCtx, r.Client, provider, repo, container); err != nil {
		return ctrl.Result{}, err
	}
	protected, err := protectedPaths(ctx, r.Client, container)
	if err != nil {
		return ctrl.Result{}, err
	}
	gitSecret, err := r.ensureAgentGit(ctx, container, provider, repo)
	if err != nil {
		return ctrl.Result{}, err
	}

	goal := task.Spec.Goal
	firstIssueRun := task.Spec.Issue > 0 && model.FirstRun(progressOf(status))
	if task.Spec.Issue > 0 {
		// Read before every run: a person may have clarified the issue since.
		issue, err := provider.GetIssue(protectCtx, repo, int(task.Spec.Issue))
		if err != nil {
			return ctrl.Result{}, err
		}
		if issue.State == stateClosed {
			r.end(task, status, model.IssueClosed(issue.Number))
			return ctrl.Result{}, nil
		}
		status.IssueTitle, status.IssueURL = issue.Title, issue.URL
		goal = model.IssueGoal(issue.Number, issue.Title, issue.Body)
		if firstIssueRun {
			commit, err := r.continueEarlierTry(protectCtx, provider, repo, task, status)
			if err != nil {
				return ctrl.Result{}, err
			}
			if commit != "" {
				return r.startAtReview(ctx, task, status, commit)
			}
		}
	}

	setProgress(status, model.StartRun(progressOf(status), role))
	if status.Branch == "" {
		status.Branch = model.TaskBranch(task.Name)
	}
	name := sandboxName(task, status, role)
	story := c4Context(ctx, r.Client, container)
	npmSecret, pypiSecret, err := r.packagesSecrets(ctx, container)
	if err != nil {
		return ctrl.Result{}, err
	}
	sandbox, err := buildSandbox(sandboxRun{
		task: task, status: status, container: container, agent: agent, name: name, role: role,
		repoURL: repo.CloneURL, gitSecret: gitSecret, context: story.Text(), goal: goal,
		maxRounds: agents.implementer.Spec.MaxRounds, related: story.RelatedRepositories(),
		protectedPaths: protected, allowedPaths: task.Spec.AllowedProtectedPaths, resumed: resumedRun(status),
		workspaceMemory: r.WorkspaceMemory, goCache: r.SandboxGoCache, goProxy: repo.GoProxy,
		npmScope: repo.NpmScope, npmRegistry: repo.NpmRegistry, pypiIndex: repo.PyPIIndex,
		npmSecret: npmSecret, pypiSecret: pypiSecret,
		pullSecrets: r.SharedPullSecrets, modelProxyURL: r.sandboxModelProxy(agent),
	}, r.Scheme, r.now())
	if err != nil {
		return ctrl.Result{}, err
	}
	err = r.Create(ctx, sandbox)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, err
	}
	// Comment with the sandbox that starts the first run, not when a retry
	// finds it made: then the status write after it failed, and the comments
	// are there.
	if err == nil && (firstIssueRun || firstReviewAtStart(status, role)) {
		if status.Continues != "" {
			r.noteContinued(protectCtx, provider, repo, task, status)
		}
		r.announce(protectCtx, provider, repo, task, container, agents)
	}
	status.Sandbox, status.SandboxPod, status.AgentBackAt = name, "", nil
	r.progress(task, status, metav1.ConditionTrue, "Running", fmt.Sprintf("%s in sandbox %s", message, name))
	logf.FromContext(ctx).Info("Started agent sandbox", "sandbox", name, "role", role, "round", status.Round)
	return ctrl.Result{}, nil
}

// startAtReview starts a try that continues an earlier try's green, unreviewed
// commit at its checks, with no implementer run: they pass, so the next step
// starts the review (model.StartsAtReview). The review's first sandbox posts
// the comments a first run does, so a failed status write here posts none.
func (r *TaskReconciler) startAtReview(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	commit string) (ctrl.Result, error) {
	setProgress(status, model.InheritedChecks(progressOf(status), commit))
	r.progress(task, status, metav1.ConditionTrue, "Checking", model.StartAtReviewMessage(commit, status.Continues))
	logf.FromContext(ctx).Info("Starting a try at the earlier try's review", "commit", commit,
		"earlierTask", status.Continues)
	return ctrl.Result{}, nil // the status write triggers the next step
}

// firstReviewAtStart reports whether role's run is the first of a try that
// started at review (startAtReview): its round 1 never implemented.
func firstReviewAtStart(status *c4v1alpha1.TaskStatus, role string) bool {
	return role == model.RoleReviewer && status.Continues != "" && status.Round == 1 &&
		status.ReviewAttempts == 0 && status.ReviewAgains == 0 && status.Reruns == 0 &&
		!slices.ContainsFunc(status.History, func(h c4v1alpha1.TaskTransition) bool {
			return h.Phase == c4v1alpha1.TaskImplementing
		})
}

// agentCredentials reports whether the Agent's credentials Secret, if it names
// one, exists; when it doesn't, the Task says so and waits for it. An Agent on
// the shared token (onSharedToken) needs the Secret in the operator's
// namespace, where the model proxy reads it, and its own namespace opted in
// to the shared Secrets: the proxy admits only those, so a sandbox of another
// would start and time out.
func (r *TaskReconciler) agentCredentials(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	agent *c4v1alpha1.Agent) (bool, error) {
	ref := agent.Spec.CredentialsSecretRef
	if ref == nil {
		return true, nil
	}
	namespace := task.Namespace
	if onSharedToken(agent, r.SharedSecrets, r.ModelProxyURL) {
		namespace = r.SourceNamespace
		var ns corev1.Namespace
		if err := r.APIReader.Get(ctx, client.ObjectKey{Name: task.Namespace}, &ns); err != nil {
			return false, err
		}
		if ns.Labels[c4v1alpha1.LabelSharedSecrets] != sharedSecretsOptIn {
			r.progress(task, status, metav1.ConditionFalse, "CredentialsNotFound",
				fmt.Sprintf("Agent %s is on the factory's shared Secret %s: label namespace %s %s=%s, so its "+
					"agents reach the factory's model proxy", agent.Name, ref.Name, task.Namespace,
					c4v1alpha1.LabelSharedSecrets, sharedSecretsOptIn))
			return false, nil
		}
	}
	var secret corev1.Secret
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, &secret)
	if apierrors.IsNotFound(err) {
		msg := fmt.Sprintf("Secret %s of Agent %s not found", ref.Name, agent.Name)
		if namespace != task.Namespace {
			msg += " in namespace " + namespace
		}
		r.progress(task, status, metav1.ConditionFalse, "CredentialsNotFound", msg)
		return false, nil
	}
	return err == nil, err
}

// ensureAgentGit returns the Secret with the Container's agent Git credentials,
// creating the bot user and the Secret the first time. The Secret is owned by the
// Container, so it goes when the Container goes.
func (r *TaskReconciler) ensureAgentGit(ctx context.Context, container *c4v1alpha1.Container,
	provider gitprovider.Provider, repo gitprovider.Repository) (string, error) {
	r.revokeOldAgentTokens(ctx, container.Namespace, container.Name, "")
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	// The scopes are kept with the token, so a token made with fewer is made again.
	return ensureAgentSecret(ctx, r.Client, r.APIReader, container, agentGitSecret(container.Name),
		map[string]string{c4v1alpha1.LabelProject: container.Namespace, c4v1alpha1.LabelContainer: container.Name},
		map[string]string{"scopes": gitprovider.AgentTokenScopes}, r.now(),
		func() (gitprovider.Credentials, error) {
			return provider.EnsureAgentAccess(gitCtx, repo, model.AgentUsername(container.Namespace, container.Name))
		})
}

// agentGitSecret is the name of the Secret with Container's agent Git credentials.
func agentGitSecret(container string) string {
	return container + "-agent-git"
}

// annotationOldTokensBefore, on an agent Git Secret, is when its token was made
// again (RFC 3339): the bot's tokens made before it, with other scopes, may
// still be live in sandboxes started before it.
const annotationOldTokensBefore = "c4.factory.io/old-tokens-before" //nolint:gosec // an annotation's name

// revokeOldAgentTokens revokes the bot tokens of Container name made before
// its Secret's annotationOldTokensBefore, once no sandbox of the Container may
// still hold one, and drops the annotation. ending is a sandbox being deleted,
// which no longer counts. It is best effort: a failure is logged, and the next
// run's start or end tries again.
func (r *TaskReconciler) revokeOldAgentTokens(ctx context.Context, namespace, name, ending string) {
	log := logf.FromContext(ctx)
	if err := r.revokeOldAgentTokensOnce(ctx, namespace, name, ending); err != nil {
		log.Error(err, "Revoking old agent tokens failed; trying again at the next run", "container", name)
	}
}

func (r *TaskReconciler) revokeOldAgentTokensOnce(ctx context.Context, namespace, name, ending string) error {
	var secret corev1.Secret
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: agentGitSecret(name)}, &secret)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	value, ok := secret.Annotations[annotationOldTokensBefore]
	if !ok {
		return nil
	}
	since, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return fmt.Errorf("annotation %s of Secret %s: %w", annotationOldTokensBefore, secret.Name, err)
	}
	sandboxes := &unstructured.UnstructuredList{}
	sandboxes.SetGroupVersionKind(SandboxGVK.GroupVersion().WithKind("SandboxList"))
	// Read live: the cache may not have the sandbox started a moment ago.
	if err := r.APIReader.List(ctx, sandboxes, client.InNamespace(namespace),
		client.MatchingLabels{c4v1alpha1.LabelContainer: name, LabelAgentSandbox: agentSandboxValue}); err != nil {
		return err
	}
	var started []time.Time
	for _, s := range sandboxes.Items {
		if s.GetName() != ending {
			started = append(started, s.GetCreationTimestamp().Time)
		}
	}
	if !model.OldTokensUnused(since, started) {
		return nil
	}
	var container c4v1alpha1.Container
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &container); err != nil {
		return client.IgnoreNotFound(err)
	}
	provider, _, err := r.gitRepo(ctx, &container)
	if err != nil {
		return err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	user := model.AgentUsername(namespace, name)
	if err := provider.RevokeOldAgentTokens(gitCtx, user); err != nil {
		return err
	}
	// Optimistic: a token made again meanwhile keeps its newer annotation.
	orig := secret.DeepCopy()
	delete(secret.Annotations, annotationOldTokensBefore)
	if err := r.Patch(ctx, &secret, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{})); err != nil {
		return err
	}
	logf.FromContext(ctx).Info("Revoked old agent tokens", "user", user, "secret", secret.Name)
	return nil
}

// merge squashes the approved pull request into main.
func (r *TaskReconciler) merge(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, agents taskAgents, step model.TaskStep) (ctrl.Result, error) {
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return ctrl.Result{}, err
	}
	reviewRun := r.reviewRun(ctx, task, status)
	if len(task.Spec.AllowedProtectedPaths) > 0 && status.MergeWhenChecksPass && reviewRun == nil {
		// The model reviews such a change again (ApprovalEnded); never merge it
		// without the approval of a review run that lasts.
		return ctrl.Result{}, r.failWith(ctx, task, status, container, model.GatedMergeRefused(
			"the review run that approved it ended while CI ran again with main merged in"))
	}
	status.Review = r.reviewFrom(ctx, task, status)
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	pr, err := provider.GetPullRequest(gitCtx, repo, int(status.PullRequest))
	if err != nil {
		return ctrl.Result{}, err
	}
	action := model.DecideMerge(model.PullFacts{Merged: pr.Merged, Closed: pr.State == stateClosed,
		HeadSHA: pr.HeadSHA}, status.Commit)
	switch action {
	case model.MergeFailClosed:
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		r.end(task, status, model.PullRequestClosed(status.PullRequestURL))
		return ctrl.Result{}, nil
	case model.MergeRecheck:
		// Someone pushed after the review: check and review that commit instead.
		setProgress(status, model.Recheck(progressOf(status), pr.HeadSHA))
		r.progress(task, status, metav1.ConditionTrue, "BranchMoved",
			"The branch moved after the review; checking "+pr.HeadSHA)
		return ctrl.Result{RequeueAfter: r.checksRecheck()}, r.endRun(ctx, task, status)
	case model.MergeSquash:
		refusal, err := r.mergeReviewed(ctx, provider, repo, task, status, container, agents.reviewer)
		pending := errors.Is(err, errChecksPending)
		if err != nil && !pending {
			return ctrl.Result{}, err
		}
		switch model.AfterSquash(pending, refusal) {
		case model.SquashWaitForChecks:
			// Waits while the review run lasts; if it ends first, the merge is refused.
			r.progress(task, status, metav1.ConditionTrue, "WaitingForChecks",
				"Approved; CI runs again on "+status.Commit+", merging once it passes")
			return ctrl.Result{RequeueAfter: r.checksRecheck()}, nil
		case model.SquashRefused:
			return ctrl.Result{}, r.failWith(ctx, task, status, container, model.GatedMergeRefused(refusal))
		}
	}
	if err := r.endRun(ctx, task, status); err != nil {
		return ctrl.Result{}, err
	}
	status.MergeCommit = pr.MergeCommit
	if action == model.MergeSquash {
		status.MergeCommit = r.mergeCommit(ctx, provider, repo, status)
	}
	if task.Spec.Issue > 0 {
		var followUps []model.FollowUp
		var open []model.OpenCriterion
		if reviewRun != nil {
			followUps, open = reviewRun.FollowUps, reviewRun.Open
		}
		status.FollowUps = r.closeIssue(ctx, provider, repo, task, status, agents.reviewer, followUps, open)
	}
	r.finish(task, status, c4v1alpha1.TaskMerged, metav1.ConditionTrue, "Merged", step.Message)
	logf.FromContext(ctx).Info("Merged Task", "url", status.PullRequestURL, "round", status.Round)
	return ctrl.Result{}, nil
}

// updateBranch merges main into an approved change's branch, as main moved since
// its checks ran, and waits for the checks on the new head, which merges once
// they pass. The review run stays for its follow-ups, and the verdict goes into
// status. A branch that conflicts with main goes back to its implementer: the
// merge, not the Git host's mergeable flag, tells a conflict.
func (r *TaskReconciler) updateBranch(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, agents taskAgents, step model.TaskStep) (ctrl.Result, error) {
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return ctrl.Result{}, err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	err = provider.UpdatePullRequest(gitCtx, repo, int(status.PullRequest))
	if errors.Is(err, gitprovider.ErrConflict) {
		return r.apply(ctx, task, status, container, agents, model.ConflictStep(int(status.Round)))
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	pr, err := provider.GetPullRequest(gitCtx, repo, int(status.PullRequest))
	if err != nil {
		return ctrl.Result{}, err
	}
	if pr.HeadSHA == status.Commit {
		// Nothing merged: the branch had main already, and the Git host had not
		// yet seen it. Look again once it has.
		return ctrl.Result{RequeueAfter: r.checksRecheck()}, nil
	}
	status.Review = r.reviewFrom(ctx, task, status)
	r.comment(ctx, task, status, container, step.Message+".")
	setProgress(status, model.UpdatedBranch(progressOf(status), pr.HeadSHA))
	// The checks' wait starts again on the new head, also when main moved again
	// while the Task was already waiting for checks.
	now := metav1.NewTime(r.now())
	status.PhaseChangedAt = &now
	r.progress(task, status, metav1.ConditionTrue, "UpdatingBranch", step.Message)
	logf.FromContext(ctx).Info("Merged main into an approved Task's branch", "round", status.Round,
		"commit", pr.HeadSHA)
	return ctrl.Result{RequeueAfter: r.checksRecheck()}, nil
}

// mergeCommit reads the commit the pull request's merge made on the default
// branch; empty when it can't be read yet, for the Task to read again once Merged.
func (r *TaskReconciler) mergeCommit(ctx context.Context, provider gitprovider.Provider,
	repo gitprovider.Repository, status *c4v1alpha1.TaskStatus) string {
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	pr, err := provider.GetPullRequest(gitCtx, repo, int(status.PullRequest))
	if err != nil {
		logf.FromContext(ctx).Error(err, "Failed to read the merge commit", "url", status.PullRequestURL)
		return ""
	}
	return pr.MergeCommit
}

// mergeReviewed squashes the reviewed commit into main, posting the reviewer's
// approval; a push since the review makes the merge fail. A Standard change goes
// through gatedMerge, which may refuse it or wait for CI.
func (r *TaskReconciler) mergeReviewed(ctx context.Context, provider gitprovider.Provider,
	repo gitprovider.Repository, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, reviewer *c4v1alpha1.Agent) (refusal string, err error) {
	if len(task.Spec.AllowedProtectedPaths) > 0 {
		return r.gatedMerge(ctx, provider, repo, task, status, container, reviewer)
	}
	r.approve(ctx, task, status, container, reviewer)
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	return "", provider.MergePullRequest(gitCtx, repo, int(status.PullRequest), status.Commit,
		pullTitle(task, status), mergeMessage(task, status, container))
}

// approve posts the reviewer's approval of the round's commit.
func (r *TaskReconciler) approve(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, reviewer *c4v1alpha1.Agent) {
	if status.Review != nil {
		r.review(ctx, task, status, container, reviewer, gitprovider.ReviewApprove, status.Review.Summary)
	}
}

// reviewFrom reads the verdict of the current review sandbox, if any.
func (r *TaskReconciler) reviewFrom(ctx context.Context, task *c4v1alpha1.Task,
	status *c4v1alpha1.TaskStatus) *c4v1alpha1.TaskReview {
	run := r.reviewRun(ctx, task, status)
	if run == nil {
		return status.Review
	}
	return &c4v1alpha1.TaskReview{Verdict: run.Verdict, Summary: run.Summary}
}

// reviewRun reads the current review sandbox's result, if it gave a verdict:
// in review, or while the approved change's branch, with main merged in, is
// checked again.
func (r *TaskReconciler) reviewRun(ctx context.Context, task *c4v1alpha1.Task,
	status *c4v1alpha1.TaskStatus) *model.RunResult {
	reviewing := status.Phase == c4v1alpha1.TaskReviewing ||
		status.Phase == c4v1alpha1.TaskChecking && status.MergeWhenChecksPass
	if !reviewing || status.Sandbox == "" {
		return nil
	}
	run, err := runState(ctx, r.Client, task.Namespace, status.Sandbox, status.SandboxPod)
	if err != nil || run.Result == nil || run.Result.Verdict == "" {
		return nil
	}
	return run.Result
}

// endRun adds what the current run cost to the Task's and records the run;
// its sandbox goes, and the note that its sessions handed off is posted, once
// writeStatus wrote that status. When the write fails, the next pass finds the
// run and its result again instead of starting the role's run anew (#922).
func (r *TaskReconciler) endRun(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus) error {
	ended, err := r.recordRun(ctx, task, status)
	if err != nil {
		return err
	}
	if pending, ok := ctx.Value(endedRunsKey{}).(*[]endedRun); ok {
		if ended.sandbox != "" {
			*pending = append(*pending, ended)
		}
		return nil
	}
	return r.dropRun(ctx, task, ended)
}

// endedRunsKey keys the runs a Reconcile ended and writeStatus drops.
type endedRunsKey struct{}

// withEndedRuns gives ctx a place for the runs endRun ends, which writeStatus
// drops once the status that records them is written.
func withEndedRuns(ctx context.Context) context.Context {
	return context.WithValue(ctx, endedRunsKey{}, &[]endedRun{})
}

// dropEndedRuns drops the runs endRun ended since the last status write.
func (r *TaskReconciler) dropEndedRuns(ctx context.Context, task *c4v1alpha1.Task) error {
	pending, ok := ctx.Value(endedRunsKey{}).(*[]endedRun)
	if !ok {
		return nil
	}
	runs := *pending
	*pending = nil
	var errs []error
	for _, ended := range runs {
		errs = append(errs, r.dropRun(ctx, task, ended))
	}
	return errors.Join(errs...)
}

// endedRun is a run recordRun took out of the Task's status, for dropRun.
type endedRun struct {
	sandbox string
	round   int32
	result  *model.RunResult
}

// recordRun adds what the current run cost to the Task's, records the run,
// and takes its sandbox out of status; dropRun then deletes it. An empty
// sandbox means no run was current.
func (r *TaskReconciler) recordRun(ctx context.Context, task *c4v1alpha1.Task,
	status *c4v1alpha1.TaskStatus) (endedRun, error) {
	if status.Sandbox == "" {
		return endedRun{}, nil
	}
	run, err := runState(ctx, r.Client, task.Namespace, status.Sandbox, status.SandboxPod)
	if err != nil {
		return endedRun{}, err
	}
	milliUSD, withoutCost := run.Cost()
	status.CostMilliUSD += milliUSD
	status.RunsWithoutCost += withoutCost
	status.Tokens = addTokens(status.Tokens, run.Tokens())
	if rec, ok := run.Record(int(status.Round), status.Sandbox, r.now()); ok {
		status.Runs = model.KeepNewest(append(status.Runs, runRecord(rec)), model.MaxRunRecords)
	}
	ended := endedRun{sandbox: status.Sandbox, round: status.Round, result: run.Result}
	status.Sandbox, status.SandboxPod = "", ""
	return ended, nil
}

// dropRun deletes the sandbox of a run recordRun ended, and posts when its
// sessions handed off.
func (r *TaskReconciler) dropRun(ctx context.Context, task *c4v1alpha1.Task, ended endedRun) error {
	if ended.sandbox == "" {
		return nil
	}
	sandbox := newSandbox()
	sandbox.SetNamespace(task.Namespace)
	sandbox.SetName(ended.sandbox)
	if err := r.Delete(ctx, sandbox, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil &&
		!apierrors.IsNotFound(err) {
		return err
	}
	if container := task.Spec.ContainerRef.Name; container != "" {
		r.revokeOldAgentTokens(ctx, task.Namespace, container, ended.sandbox)
	}
	if res := ended.result; res != nil && len(res.SessionHandoffs) > 0 && r.Recorder != nil {
		r.Recorder.Eventf(task, nil, corev1.EventTypeNormal, eventSessionHandedOff, "HandOff", "%s",
			model.HandoffNote(int(ended.round), res.Role, res.SessionHandoffs))
	}
	return nil
}

// runRecord is rec as Task status keeps it.
func runRecord(rec model.RunRecord) c4v1alpha1.TaskAgentRun {
	out := c4v1alpha1.TaskAgentRun{Round: int32(rec.Round), Role: rec.Role, Sandbox: rec.Sandbox,
		EndedAt: metav1.NewTime(rec.EndedAt), SessionHandoffs: rec.SessionHandoffs,
		Tokens: addTokens(nil, rec.Tokens)}
	if rec.CostReported {
		out.CostMilliUSD = &rec.CostMilliUSD
	}
	return out
}

// addTokens adds add to tokens, which stays nil while nothing is counted.
func addTokens(tokens *c4v1alpha1.TokenUsage, add model.TokenUsage) *c4v1alpha1.TokenUsage {
	if add == (model.TokenUsage{}) {
		return tokens
	}
	var sum model.TokenUsage
	if tokens != nil {
		sum = model.TokenUsage(*tokens)
	}
	sum.Add(add)
	out := c4v1alpha1.TokenUsage(sum)
	return &out
}

// progressOf copies the part of status that the model's transitions change.
func progressOf(status *c4v1alpha1.TaskStatus) model.TaskProgress {
	p := model.TaskProgress{Phase: string(status.Phase), Round: int(status.Round),
		ReviewAttempts: int(status.ReviewAttempts), ReviewAgains: int(status.ReviewAgains), Reruns: int(status.Reruns),
		Restarts: int(status.Restarts), ChecksReruns: int(status.ChecksReruns), ChecksRerunDue: status.ChecksRerunDue,
		Feedback: status.Feedback, ChecksFailed: failedChecks(status),
		Commit: status.Commit, HasChecks: status.Checks != nil, MergeWhenChecksPass: status.MergeWhenChecksPass}
	if status.ChecksRerunAt != nil {
		p.ChecksRerunAt = status.ChecksRerunAt.Time
	}
	if status.StandardChange != nil {
		p.ResumedRound = int(status.StandardChange.ResumedRound)
	}
	return p
}

// failedChecks reports whether the Task's recorded checks failed on its commit.
func failedChecks(status *c4v1alpha1.TaskStatus) bool {
	checks := status.Checks
	return checks != nil && checks.Commit == status.Commit &&
		(checks.State == gitprovider.CheckFailure || checks.State == gitprovider.CheckError)
}

// setProgress copies a transition's result back into status.
func setProgress(status *c4v1alpha1.TaskStatus, p model.TaskProgress) {
	status.Phase, status.Round = c4v1alpha1.TaskPhase(p.Phase), int32(p.Round)
	status.ReviewAttempts, status.ReviewAgains = int32(p.ReviewAttempts), int32(p.ReviewAgains)
	status.Reruns, status.Restarts = int32(p.Reruns), int32(p.Restarts)
	status.Feedback, status.Commit = p.Feedback, p.Commit
	status.MergeWhenChecksPass = p.MergeWhenChecksPass
	status.ChecksReruns, status.ChecksRerunAt = int32(p.ChecksReruns), nil
	status.ChecksRerunDue = p.ChecksRerunDue
	if !p.ChecksRerunAt.IsZero() {
		status.ChecksRerunAt = &metav1.Time{Time: p.ChecksRerunAt}
	}
	if !p.HasChecks {
		status.Checks = nil
	}
	if status.StandardChange != nil {
		status.StandardChange.ResumedRound = int32(p.ResumedRound)
	}
}

// end finishes the Task as the model decided; only a merge succeeds.
func (r *TaskReconciler) end(task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus, e model.TaskEnd) {
	succeeded := metav1.ConditionFalse
	if e.Phase == model.TaskMerged {
		succeeded = metav1.ConditionTrue
	}
	r.finish(task, status, c4v1alpha1.TaskPhase(e.Phase), succeeded, e.Reason, e.Message)
}

func (r *TaskReconciler) finish(task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus, phase c4v1alpha1.TaskPhase,
	succeeded metav1.ConditionStatus, reason, message string) {
	now := metav1.NewTime(r.now())
	status.Phase, status.CompletedAt = phase, &now
	r.progress(task, status, metav1.ConditionFalse, reason, message)
	setCondition(&status.Conditions, task.Generation, ConditionSucceeded, succeeded, reason, message)
}

// rerunChecks runs again the failed jobs of the CI runs at the round's commit,
// which the machine killed for lack of memory or the round pushed again, and
// waits for their result in the same round. Ending an implement run, it opens
// the pull request first, and the checks run again once Checking. It waits while a run at the commit still runs, as the
// Git host runs a run's jobs again only once it ended. A rerun the Git host
// refuses fails the Task at no try.
func (r *TaskReconciler) rerunChecks(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, agents taskAgents, step model.TaskStep) (ctrl.Result, error) {
	if status.Phase == c4v1alpha1.TaskImplementing {
		return r.openPR(ctx, task, status, container, agents, step)
	}
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return ctrl.Result{}, err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	found, err := provider.CommitRuns(gitCtx, repo, status.Commit)
	if err != nil {
		return ctrl.Result{}, err
	}
	runs := workflowRuns(found)
	if !model.RunsEnded(runs) {
		r.progress(task, status, metav1.ConditionTrue, "Waiting",
			step.Message+"; waiting for the CI runs at the commit to end first")
		return ctrl.Result{RequeueAfter: r.checksRecheck()}, nil
	}
	failed := model.FailedCommitRuns(runs)
	if len(failed) == 0 && status.ChecksRerunDue {
		// Nothing to run again: the failure is read as it stands.
		status.ChecksRerunDue = false
		r.progress(task, status, metav1.ConditionTrue, "Checking", fmt.Sprintf(
			"The Git host lists no failed CI run at %s to run again; reading its checks", status.Commit))
		return ctrl.Result{}, nil
	}
	if len(failed) == 0 {
		return ctrl.Result{}, r.fail(ctx, task, status, container, model.ReasonNoCIResult, fmt.Sprintf(
			"%s, but the Git host lists no failed CI run at %s to run again", step.Message, status.Commit))
	}
	for _, run := range failed {
		if err := provider.RerunFailedJobs(gitCtx, repo, run.ID); err != nil {
			why := "failed through its runner's fault"
			if status.ChecksRerunDue {
				why = "failed at a commit the agent pushed again"
			}
			return ctrl.Result{}, r.fail(ctx, task, status, container, model.ReasonNoCIResult, fmt.Sprintf(
				"CI run %s %s, and the Git host refused to run its failed jobs again: %v", run.URL, why, err))
		}
	}
	log := logf.FromContext(ctx)
	if status.ChecksRerunDue {
		setProgress(status, model.RerunPushedChecks(progressOf(status), r.now()))
		log.Info("Rerunning the checks of a commit pushed again", "round", status.Round, "commit", status.Commit,
			"runs", len(failed))
	} else {
		setProgress(status, model.RerunChecks(progressOf(status), r.now()))
		log.Info("Rerunning checks the runner failed", "round", status.Round,
			"commit", status.Commit, "checksReruns", status.ChecksReruns, "runs", len(failed))
	}
	r.progress(task, status, metav1.ConditionTrue, "RerunningChecks", step.Message)
	return ctrl.Result{}, nil
}

// fail ends the run and fails the Task for reason, saying why on its pull request
// and handing its issue back to a person.
func (r *TaskReconciler) fail(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, reason, message string) error {
	r.comment(ctx, task, status, container, "The Task failed. "+message)
	if err := r.endRun(ctx, task, status); err != nil {
		return err
	}
	r.dropProposal(ctx, task, status, container)
	r.finish(task, status, c4v1alpha1.TaskFailed, metav1.ConditionFalse, reason, message)
	why := message
	switch reason {
	case model.ReasonNoCIResult:
		why += "\n\nCI never reported, so this try doesn't count towards the limit; start or fix the runner first."
	case model.ReasonRunnerOutOfSpace:
		why += "\n\nThe runner ran out of room, so this try doesn't count towards the limit; free its space first."
	case model.ReasonRunnerOutOfMemory:
		why += "\n\nThe machine kept killing the checks for lack of memory, so this try doesn't count towards " +
			"the limit; free memory on the runner or run fewer jobs at once first."
	case model.ReasonRunnerLost:
		why += "\n\nThe runner kept abandoning the checks, so this try doesn't count towards the limit; keep the " +
			"runners up first."
	case model.ReasonApprovalRanOut:
		why += "\n\nCI kept taking longer than a review run lasts, so this try doesn't count towards the limit; " +
			"free or speed up the runner first."
	}
	r.handBack(ctx, task, container, why)
	logf.FromContext(ctx).Info("Failed Task", "reason", reason, "message", message)
	return nil
}

// failWith fails the Task as the model decided.
func (r *TaskReconciler) failWith(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, e model.TaskEnd) error {
	return r.fail(ctx, task, status, container, e.Reason, e.Message)
}

// comment adds a note to the Task's pull request, if it has one. The pull request
// is a report; failing to comment doesn't stop the Task.
func (r *TaskReconciler) comment(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, body string) {
	if status.PullRequest == 0 {
		return
	}
	provider, repo, err := r.gitRepo(ctx, container)
	if err == nil {
		gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
		err = provider.CommentPullRequest(gitCtx, repo, int(status.PullRequest), body)
		cancel()
	}
	if err != nil {
		logf.FromContext(ctx).Info("Could not comment on pull request", "task", task.Name, "error", err.Error())
	}
}

func pullTitle(task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus) string {
	if task.Spec.Issue > 0 && status.IssueTitle != "" {
		return firstLine(status.IssueTitle)
	}
	return firstLine(task.Spec.Goal)
}

// pullBody is a pull request's description; extra are the unprotected files a
// Standard change changes besides its allowed ones, named for the reviewer.
func pullBody(task *c4v1alpha1.Task, agents taskAgents, round int32, summary string, extra []string) string {
	var b strings.Builder
	if task.Spec.Issue > 0 {
		// Gitea links the issue, and closes it on merge.
		fmt.Fprintf(&b, "Closes #%d.\n\n", task.Spec.Issue)
	}
	fmt.Fprintf(&b, "Task `%s`: implemented by %s, reviewed by %s.\n", task.Name,
		agentName(agents.implementer), agentName(agents.reviewer))
	if task.Spec.Issue == 0 {
		fmt.Fprintf(&b, "\n## Goal\n\n%s\n", task.Spec.Goal)
	}
	if len(extra) > 0 {
		fmt.Fprintf(&b, "\n%s\n", extrasNote(task, extra))
	}
	if summary = strings.TrimSpace(summary); summary != "" {
		fmt.Fprintf(&b, "\n## Summary of round %d\n\n%s\n", round, summary)
	}
	return b.String()
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if len(line) > 72 {
		line = line[:69] + "..."
	}
	return line
}

// mergeMessage credits the agent's bot user as co-author: the squash commit is
// authored by the operator's Git user, who only merged.
func mergeMessage(task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus, container *c4v1alpha1.Container) string {
	bot := model.AgentUsername(container.Namespace, container.Name)
	return fmt.Sprintf("Task %s, round %d.\n\nCo-authored-by: %s <%s@agents.c4.factory.io>",
		task.Name, status.Round, bot, bot)
}

// agentGitRepo is the Container's repository as its agent bot sees it. The
// implementer's pull requests are opened as the bot: they are its work, and the
// Git host refuses a review of one's own pull request, so the operator's user
// could not review them otherwise.
func (r *TaskReconciler) agentGitRepo(ctx context.Context, container *c4v1alpha1.Container) (gitprovider.Provider,
	gitprovider.Repository, error) {
	var gp c4v1alpha1.GitProvider
	if err := r.Get(ctx, client.ObjectKey{Namespace: container.Namespace, Name: container.Spec.GitProviderRef.Name},
		&gp); err != nil {
		return nil, gitprovider.Repository{}, fmt.Errorf("GitProvider %s: %w", container.Spec.GitProviderRef.Name, err)
	}
	var secret corev1.Secret
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: container.Namespace, Name: agentGitSecret(container.Name)},
		&secret); err != nil {
		return nil, gitprovider.Repository{}, fmt.Errorf("agent Git access of %s: %w", container.Name, err)
	}
	provider, err := r.Git.Provider(gitprovider.Config{
		Kind: string(gp.Spec.Provider), URL: gp.Spec.URL, Owner: gp.Spec.Owner,
		Credentials: gitprovider.Credentials{Username: string(secret.Data["username"]), Token: string(secret.Data["token"])},
		Packages:    packagesOf(&gp),
	})
	if err != nil {
		return nil, gitprovider.Repository{}, err
	}
	return provider, provider.Repository(container.Name), nil
}

// packagesSecrets names the Secrets of the npm registry and PyPI index of a
// Container's GitProvider, "" for one without.
func (r *TaskReconciler) packagesSecrets(ctx context.Context, container *c4v1alpha1.Container) (npm, pypi string,
	err error) {
	var gp c4v1alpha1.GitProvider
	if err := r.Get(ctx, client.ObjectKey{Namespace: container.Namespace, Name: container.Spec.GitProviderRef.Name},
		&gp); err != nil {
		return "", "", fmt.Errorf("GitProvider %s: %w", container.Spec.GitProviderRef.Name, err)
	}
	npm, pypi = packagesSecrets(&gp)
	return npm, pypi, nil
}

// extrasNote names the unprotected files a Standard change changes besides its
// allowed ones.
func extrasNote(task *c4v1alpha1.Task, extra []string) string {
	return fmt.Sprintf("This Standard change may change %s. It also changes %s, which no Standard protects, "+
		"so the repository's checks pass with it.", codeList(task.Spec.AllowedProtectedPaths), codeList(extra))
}

// codeList lists paths as `a`, `b`, for Markdown.
func codeList(paths []string) string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = "`" + p + "`"
	}
	return strings.Join(out, ", ")
}
