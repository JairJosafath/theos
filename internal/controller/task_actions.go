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
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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

	case model.ActionRetryReview:
		r.progress(task, status, metav1.ConditionTrue, "ReviewerFailed", step.Message)
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		status.ReviewAttempts++
		return ctrl.Result{}, nil // the status write triggers the next step

	case model.ActionMerge:
		return r.merge(ctx, task, status, container, agents, step)

	case model.ActionResolveConflicts:
		review := r.reviewFrom(ctx, task, status)
		r.comment(ctx, task, status, container, step.Message+".")
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		status.Review = review
		status.Feedback = step.Feedback
		status.ReviewAttempts = 0
		status.Checks = nil
		status.Phase = c4v1alpha1.TaskImplementing
		r.progress(task, status, metav1.ConditionTrue, "ResolvingConflicts", step.Message)
		log.Info("Sent an approved Task back to resolve conflicts", "round", status.Round)
		return ctrl.Result{}, nil // the status write triggers the next step

	case model.ActionAgentUnavailable:
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		back := step.RetryAt
		if now := r.now(); !back.After(now) {
			back = now.Add(model.AgentRetryDelay)
		}
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
		status.Round++
		status.Feedback = step.Feedback
		status.ReviewAttempts = 0
		status.Checks = nil
		status.Phase = c4v1alpha1.TaskImplementing
		r.progress(task, status, metav1.ConditionTrue, "NextRound", step.Message)
		log.Info("Started next round of Task", "round", status.Round, "feedback", firstLine(step.Feedback))
		return ctrl.Result{}, nil // the status write triggers the next step

	case model.ActionRefuseMerge:
		return ctrl.Result{}, r.fail(ctx, task, status, container, "GatedMergeRefused",
			"The gated merge was refused: "+step.Message)

	case model.ActionFail:
		if review := r.reviewFrom(ctx, task, status); review != nil {
			status.Review = review
		}
		status.Feedback = step.Feedback
		return ctrl.Result{}, r.fail(ctx, task, status, container, "Failed", step.Message)
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
	r.progress(task, status, metav1.ConditionTrue, reason, step.Message)
	if status.Phase == c4v1alpha1.TaskChecking {
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
	run, err := runState(ctx, r.Client, task.Namespace, status.Sandbox)
	if err != nil || run.Result == nil {
		return ctrl.Result{}, fmt.Errorf("reading the implement result of %s: %v", status.Sandbox, err)
	}
	gitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pr, err := provider.OpenPullRequest(gitCtx, repo, status.Branch, gitprovider.DefaultBranch,
		pullTitle(task, status), pullBody(task, agents, status.Round, run.Result.Summary))
	if err != nil {
		return ctrl.Result{}, err
	}
	log.Info("Opened pull request", "url", pr.URL, "commit", run.Result.Commit, "round", status.Round)
	status.Commit, status.Summary = run.Result.Commit, run.Result.Summary
	status.PullRequest, status.PullRequestURL = int32(pr.Number), pr.URL
	status.Checks = nil
	status.Phase = c4v1alpha1.TaskChecking
	r.progress(task, status, metav1.ConditionTrue, "Checking", step.Message+"; waiting for checks")
	return ctrl.Result{RequeueAfter: r.checksRecheck()}, r.endRun(ctx, task, status)
}

// startRun creates the sandbox for this round's role, after making sure the
// Container's agent Git access and the Agent's credentials exist.
func (r *TaskReconciler) startRun(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, agents taskAgents, role, message string) (ctrl.Result, error) {
	agent := agents.forRole(role)
	if ref := agent.Spec.CredentialsSecretRef; ref != nil {
		var secret corev1.Secret
		err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: ref.Name}, &secret)
		if apierrors.IsNotFound(err) {
			r.progress(task, status, metav1.ConditionFalse, "CredentialsNotFound",
				fmt.Sprintf("Secret %s of Agent %s not found", ref.Name, agent.Name))
			return ctrl.Result{RequeueAfter: taskSecretRecheck}, nil
		}
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return ctrl.Result{}, err
	}
	// Bring the Git host's rules up to date before an agent gets access: older
	// repositories were protected with fewer of them.
	protectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
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
	if task.Spec.Issue > 0 {
		// Read before every run: a person may have clarified the issue since.
		issue, err := provider.GetIssue(protectCtx, repo, int(task.Spec.Issue))
		if err != nil {
			return ctrl.Result{}, err
		}
		if issue.State == "closed" {
			r.finish(task, status, c4v1alpha1.TaskFailed, metav1.ConditionFalse, "IssueClosed",
				fmt.Sprintf("Issue #%d was closed before the Task finished", issue.Number))
			return ctrl.Result{}, nil
		}
		status.IssueTitle, status.IssueURL = issue.Title, issue.URL
		goal = model.IssueGoal(issue.Number, issue.Title, issue.Body)
		if status.Round == 0 {
			r.announce(protectCtx, provider, repo, task, container, agents)
		}
	}

	if status.Round == 0 {
		status.Round = 1
	}
	status.Branch = model.TaskBranch(task.Name)
	if role == model.RoleImplementer {
		status.Phase = c4v1alpha1.TaskImplementing
	} else {
		status.Phase = c4v1alpha1.TaskReviewing
	}
	name := sandboxName(task, status, role)
	story := c4Context(ctx, r.Client, container)
	sandbox, err := buildSandbox(sandboxRun{
		task: task, status: status, container: container, agent: agent, name: name, role: role,
		repoURL: repo.CloneURL, gitSecret: gitSecret, context: story.Text(), goal: goal,
		maxRounds: agents.implementer.Spec.MaxRounds, related: story.RelatedRepositories(),
		protectedPaths: protected, allowedPaths: task.Spec.AllowedProtectedPaths, workspaceMemory: r.WorkspaceMemory,
	}, r.Scheme, r.now())
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, sandbox); err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, err
	}
	status.Sandbox, status.AgentBackAt = name, nil
	r.progress(task, status, metav1.ConditionTrue, "Running", fmt.Sprintf("%s in sandbox %s", message, name))
	logf.FromContext(ctx).Info("Started agent sandbox", "sandbox", name, "role", role, "round", status.Round)
	return ctrl.Result{}, nil
}

// ensureAgentGit returns the Secret with the Container's agent Git credentials,
// creating the bot user and the Secret the first time. The Secret is owned by the
// Container, so it goes when the Container goes.
func (r *TaskReconciler) ensureAgentGit(ctx context.Context, container *c4v1alpha1.Container,
	provider gitprovider.Provider, repo gitprovider.Repository) (string, error) {
	name := container.Name + "-agent-git"
	var secret corev1.Secret
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: container.Namespace, Name: name}, &secret)
	if err == nil && len(secret.Data["token"]) > 0 {
		return name, nil
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return "", err
	}
	found := err == nil
	gitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	creds, err := provider.EnsureAgentAccess(gitCtx, repo, model.AgentUsername(container.Namespace, container.Name))
	if err != nil {
		return "", err
	}
	// Written without reading through the cache: the operator doesn't watch Secrets.
	desired := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: container.Namespace, Name: name, Labels: map[string]string{
			c4v1alpha1.LabelProject: container.Namespace, c4v1alpha1.LabelContainer: container.Name}},
		Data: map[string][]byte{"username": []byte(creds.Username), "token": []byte(creds.Token)},
	}
	if err := controllerutil.SetControllerReference(container, &desired, r.Scheme); err != nil {
		return "", err
	}
	if found {
		orig := secret.DeepCopy()
		secret.Labels, secret.Data, secret.OwnerReferences = desired.Labels, desired.Data, desired.OwnerReferences
		err = r.Patch(ctx, &secret, client.MergeFrom(orig))
	} else {
		err = r.Create(ctx, &desired)
	}
	if err != nil {
		return "", err
	}
	logf.FromContext(ctx).Info("Created agent Git access", "user", creds.Username, "secret", name)
	return name, nil
}

// merge squashes the approved pull request into main.
func (r *TaskReconciler) merge(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, agents taskAgents, step model.TaskStep) (ctrl.Result, error) {
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return ctrl.Result{}, err
	}
	reviewRun := r.reviewRun(ctx, task, status)
	status.Review = r.reviewFrom(ctx, task, status)
	gitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pr, err := provider.GetPullRequest(gitCtx, repo, int(status.PullRequest))
	if err != nil {
		return ctrl.Result{}, err
	}
	if !pr.Merged && pr.State == "closed" {
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		r.finish(task, status, c4v1alpha1.TaskFailed, metav1.ConditionFalse, "PullRequestClosed",
			"Pull request "+status.PullRequestURL+" was closed without merging")
		return ctrl.Result{}, nil
	}
	if !pr.Merged {
		if pr.HeadSHA != "" && pr.HeadSHA != status.Commit {
			// Someone pushed after the review: check and review that commit instead.
			status.Commit, status.Checks, status.Phase = pr.HeadSHA, nil, c4v1alpha1.TaskChecking
			r.progress(task, status, metav1.ConditionTrue, "BranchMoved",
				"The branch moved after the review; checking "+pr.HeadSHA)
			return ctrl.Result{RequeueAfter: r.checksRecheck()}, r.endRun(ctx, task, status)
		}
		refusal, err := r.mergeReviewed(ctx, provider, repo, task, status, container, agents.reviewer)
		if errors.Is(err, errChecksPending) {
			// Waits while the review run lasts; if it ends first, the merge is refused.
			r.progress(task, status, metav1.ConditionTrue, "WaitingForChecks",
				"Approved; CI runs again on "+status.Commit+", merging once it passes")
			return ctrl.Result{RequeueAfter: r.checksRecheck()}, nil
		}
		if err != nil {
			return ctrl.Result{}, err
		}
		if refusal != "" {
			return ctrl.Result{}, r.fail(ctx, task, status, container, "GatedMergeRefused",
				"The gated merge was refused: "+refusal)
		}
	}
	if err := r.endRun(ctx, task, status); err != nil {
		return ctrl.Result{}, err
	}
	if task.Spec.Issue > 0 {
		var followUps []model.FollowUp
		if reviewRun != nil {
			followUps = reviewRun.FollowUps
		}
		status.FollowUps = r.closeIssue(gitCtx, provider, repo, task, status, agents.reviewer, followUps)
	}
	r.finish(task, status, c4v1alpha1.TaskMerged, metav1.ConditionTrue, "Merged", step.Message)
	logf.FromContext(ctx).Info("Merged Task", "url", status.PullRequestURL, "round", status.Round)
	return ctrl.Result{}, nil
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
	gitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
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

// reviewRun reads the current review sandbox's result, if it gave a verdict.
func (r *TaskReconciler) reviewRun(ctx context.Context, task *c4v1alpha1.Task,
	status *c4v1alpha1.TaskStatus) *model.RunResult {
	if status.Phase != c4v1alpha1.TaskReviewing || status.Sandbox == "" {
		return nil
	}
	run, err := runState(ctx, r.Client, task.Namespace, status.Sandbox)
	if err != nil || run.Result == nil || run.Result.Verdict == "" {
		return nil
	}
	return run.Result
}

// endRun deletes the current sandbox, once its result is in status.
func (r *TaskReconciler) endRun(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus) error {
	if status.Sandbox == "" {
		return nil
	}
	sandbox := newSandbox()
	sandbox.SetNamespace(task.Namespace)
	sandbox.SetName(status.Sandbox)
	if err := r.Delete(ctx, sandbox, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil &&
		!apierrors.IsNotFound(err) {
		return err
	}
	status.Sandbox = ""
	return nil
}

func (r *TaskReconciler) finish(task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus, phase c4v1alpha1.TaskPhase,
	succeeded metav1.ConditionStatus, reason, message string) {
	now := metav1.NewTime(r.now())
	status.Phase, status.CompletedAt = phase, &now
	r.progress(task, status, metav1.ConditionFalse, reason, message)
	setCondition(&status.Conditions, task.Generation, ConditionSucceeded, succeeded, reason, message)
}

// fail ends the run and fails the Task for reason, saying why on its pull request
// and handing its issue back to a person.
func (r *TaskReconciler) fail(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus,
	container *c4v1alpha1.Container, reason, message string) error {
	r.comment(ctx, task, status, container, "The Task failed. "+message)
	if err := r.endRun(ctx, task, status); err != nil {
		return err
	}
	r.finish(task, status, c4v1alpha1.TaskFailed, metav1.ConditionFalse, reason, message)
	r.handBack(ctx, task, container, message)
	logf.FromContext(ctx).Info("Failed Task", "reason", reason, "message", message)
	return nil
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
		gitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
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

func pullBody(task *c4v1alpha1.Task, agents taskAgents, round int32, summary string) string {
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
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: container.Namespace, Name: container.Name + "-agent-git"},
		&secret); err != nil {
		return nil, gitprovider.Repository{}, fmt.Errorf("agent Git access of %s: %w", container.Name, err)
	}
	provider, err := r.Git.Provider(gitprovider.Config{
		Kind: string(gp.Spec.Provider), URL: gp.Spec.URL, Owner: gp.Spec.Owner,
		Credentials: gitprovider.Credentials{Username: string(secret.Data["username"]), Token: string(secret.Data["token"])},
	})
	if err != nil {
		return nil, gitprovider.Repository{}, err
	}
	return provider, provider.Repository(container.Name), nil
}
