/*
Copyright 2026 The theos Authors.

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
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// taskAgents are the Agents of a Task's roles; one Agent may play several.
// verifier is nil for a Task without one.
type taskAgents struct {
	implementer, reviewer, verifier *c4v1beta1.Agent
}

func (a taskAgents) forRole(role string) *c4v1beta1.Agent {
	switch role {
	case model.RoleReviewer:
		return a.reviewer
	case model.RoleVerifier:
		return a.verifier
	}
	return a.implementer
}

// apply does one step and records it in status.
func (r *TaskReconciler) apply(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents, step model.TaskStep) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if step.Unreviewed {
		// No agent judged this round's change (its Crew's approval rules ask
		// for no review): an earlier round's verdict is not its own, and
		// must not go on the pull request as an approval of it.
		status.Review, status.Verification = nil, nil
	}
	switch step.Action {
	case model.ActionWait:
		return r.wait(ctx, task, status, step), nil

	case model.ActionStartImplement, model.ActionStartReview, model.ActionStartVerify:
		return r.startRun(ctx, task, status, container, agents, startRole(step.Action), step.Message)

	case model.ActionVerify, model.ActionRetryVerify:
		return r.applyVerify(ctx, task, status, container, agents, step)

	case model.ActionOpenPR:
		return r.openPR(ctx, task, status, container, agents, step)

	case model.ActionPropose:
		return r.propose(ctx, task, status, container, step)

	case model.ActionResume:
		r.resume(ctx, task, status, container, step)
		return ctrl.Result{}, nil // the status write triggers the next step

	case model.ActionDoneByStandardChange:
		return ctrl.Result{}, r.doneByStandardChange(ctx, task, status, container, step)

	case model.ActionNothingToDo:
		return ctrl.Result{}, r.nothingToDo(ctx, task, status, container, step)

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

	case model.ActionRerun, model.ActionRerunPaused, model.ActionSuspendRun, model.ActionResumeRun,
		model.ActionPauseRun, model.ActionSuspendForAgent:
		return r.applyInterrupted(ctx, task, status, container, agents, step)

	case model.ActionRerunChecks:
		return r.rerunChecks(ctx, task, status, container, agents, step)

	case model.ActionAgentUnavailable:
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		back := model.AgentBackAt(step.RetryAt, r.now())
		status.AgentBackAt = &metav1.Time{Time: back}
		r.progress(task, status, metav1.ConditionTrue, reasonAgentUnavailable,
			fmt.Sprintf("%s; trying again at %s", step.Message, back.UTC().Format(time.RFC3339)))
		log.Info("Agent unavailable; Task waits", "until", back, "message", step.Message)
		return ctrl.Result{RequeueAfter: back.Sub(r.now())}, nil

	case model.ActionRetry:
		return ctrl.Result{}, r.nextRound(ctx, task, status, container, agents, step) // the status write triggers the next step

	case model.ActionReviewAgain:
		r.comment(ctx, task, status, container, step.Message+".")
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		setProgress(status, model.ReviewAgain(progressOf(status)))
		r.progress(task, status, metav1.ConditionTrue, "ApprovalEnded", step.Message)
		log.Info("Checking and reviewing an approved Standard change again", "round", status.Round)
		return ctrl.Result{}, nil // the status write triggers the next step

	case model.ActionAwaitApproval, model.ActionSpecDeclined:
		return r.applySpec(ctx, task, status, container, agents, step)

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

// startRole is the role of the run a start action starts.
func startRole(action model.TaskAction) string {
	switch action {
	case model.ActionStartReview:
		return model.RoleReviewer
	case model.ActionStartVerify:
		return model.RoleVerifier
	}
	return model.RoleImplementer
}

// nextRound starts the Task's next round with the step's feedback. A
// reviewer's or verifier's request for changes goes on the pull request as a
// review; any other reason, as a comment.
func (r *TaskReconciler) nextRound(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents, step model.TaskStep) error {
	review := r.reviewFrom(ctx, task, status)
	verification := status.Verification
	switch {
	case review != nil && review.Verdict == model.VerdictChanges && status.Phase == c4v1beta1.TaskReviewing:
		r.review(ctx, task, status, container, agents.reviewer, ports.ReviewRequestChanges, review.Summary)
	case verification != nil && verification.Verdict == model.VerdictChanges &&
		status.Phase == c4v1beta1.TaskVerifying:
		r.postVerification(ctx, task, status, container, agents.verifier, ports.ReviewRequestChanges)
	default:
		r.comment(ctx, task, status, container, fmt.Sprintf("Round %d was not accepted.\n\n%s", status.Round, step.Feedback))
	}
	if err := r.endRun(ctx, task, status); err != nil {
		return err
	}
	if review != nil {
		status.Review = review
	}
	setProgress(status, model.NextRound(progressOf(status), step.Feedback))
	r.progress(task, status, metav1.ConditionTrue, "NextRound", step.Message)
	logf.FromContext(ctx).Info("Started next round of Task", "round", status.Round, "feedback", firstLine(step.Feedback))
	return nil
}

// reasonAgentUnavailable is the Progressing reason of a Task waiting for its
// agent's service to be back.
const reasonAgentUnavailable = "AgentUnavailable"

// wait records what the Task waits for, and when to look again.
func (r *TaskReconciler) wait(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus, step model.TaskStep) ctrl.Result {
	if status.Phase == c4v1beta1.TaskBlocked {
		// The Task also wakes when one on its Container changes phase, as the
		// change's Task does when it ends.
		r.progress(task, status, metav1.ConditionTrue, "WaitingForStandardChange", step.Message)
		return ctrl.Result{RequeueAfter: hostRecheck(ctx, r.standardChangeRecheck())}
	}
	reason := "Waiting"
	if step.SlotWait {
		reason = "WaitingForSlot"
	}
	if status.AgentBackAt != nil && status.AgentBackAt.After(r.now()) && !step.SlotWait {
		reason = reasonAgentUnavailable
	}
	if step.Reason != "" {
		// At its Crew's cost cap (OverBudget): the Crew watch wakes the Task
		// when the cap is raised, so it needs no requeue. BaseRed rechecks.
		reason = step.Reason
	}
	r.progress(task, status, metav1.ConditionTrue, reason, step.Message)
	if status.Phase == c4v1beta1.TaskChecking || step.Recheck {
		return ctrl.Result{RequeueAfter: hostRecheck(ctx, r.checksRecheck())}
	}
	if status.AgentBackAt != nil && step.Reason == "" && !step.SlotWait {
		// Looked at again when the agent is back, also when that came while
		// this pass ran (#1996): a requeue after zero is none.
		return ctrl.Result{RequeueAfter: max(status.AgentBackAt.Sub(r.now()), agentBackRecheck)}
	}
	return ctrl.Result{}
}

// agentBackRecheck is the least time after which a Task waiting for its
// agent looks again: it is due back already.
const agentBackRecheck = time.Second

// openPR opens the round's pull request as the agent bot, and moves on to the checks.
func (r *TaskReconciler) openPR(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents, step model.TaskStep) (ctrl.Result, error) {
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
	pr, err := provider.OpenPullRequest(gitCtx, repo, status.Branch, ports.DefaultBranch,
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
	if isSpec(task) {
		r.labelSpecReview(ctx, task, container)
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
	return ctrl.Result{RequeueAfter: hostRecheck(ctx, r.checksRecheck())}, r.dropRun(ctx, task, ended)
}

// startRun creates the sandbox for this round's role, after making sure the
// Container's agent Git access and the Agent's credentials exist.
func (r *TaskReconciler) startRun(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents, role, message string) (ctrl.Result, error) {
	in, result, err := r.runInputs(ctx, task, status, container, agents, role)
	if in == nil {
		return result, err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	firstIssueRun := task.Spec.Issue > 0 && model.FirstRun(progressOf(status))
	if firstIssueRun {
		commit, err := r.continueEarlierTry(gitCtx, in.provider, in.repo, task, status)
		if err != nil {
			return ctrl.Result{}, err
		}
		if commit != "" {
			return r.startAtReview(ctx, task, status, commit)
		}
	}

	// The run's environments are leased before its sandbox, under the name
	// the sandbox will have: a run waiting for one starts nothing.
	next := status.DeepCopy()
	setProgress(next, model.StartRun(progressOf(next), role))
	envs, wait, err := r.leaseEnvironments(ctx, task, status, container, role, sandboxName(task, next, role), in.labels)
	if err != nil {
		return ctrl.Result{}, err
	}
	if wait != nil {
		r.waitForEnvironment(task, status, wait)
		return ctrl.Result{RequeueAfter: r.environmentRecheck()}, nil
	}

	setProgress(status, model.StartRun(progressOf(status), role))
	if status.Branch == "" {
		status.Branch = model.TaskBranch(task.Name)
	}
	if b := task.Spec.Benchmark; b != nil && role == model.RoleImplementer {
		// A Benchmark's Task branches from its pinned base: the implementer
		// works on the branch it finds.
		if err := ensureBranchAt(gitCtx, in.provider, in.repo, status.Branch, b.Base); err != nil {
			return ctrl.Result{}, err
		}
	}
	name := sandboxName(task, status, role)
	sandbox, err := r.runSandbox(ctx, task, status, container, agents, role, name, in, envs)
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
			r.noteContinued(gitCtx, in.provider, in.repo, task, status)
		}
		r.announce(gitCtx, in.provider, in.repo, task, container, agents)
	}
	status.Sandbox, status.SandboxPod, status.AgentBackAt = name, "", nil
	r.progress(task, status, metav1.ConditionTrue, "Running", fmt.Sprintf("%s in sandbox %s", message, name))
	logf.FromContext(ctx).Info("Started agent sandbox", "sandbox", name, "role", role, "round", status.Round)
	return ctrl.Result{}, nil
}

// runInput is what a run of a Container's Task gets from its Git host, read
// before each run, a resumed one too: the fence and the goal as they are now.
type runInput struct {
	provider interface {
		ports.GitHost
		ports.Issues
		ports.CI
		ports.AgentAccess
	}
	repo      ports.Repository
	protected []string
	gitSecret string
	goal      string
	// labels are the issue's, which may ask for test environments.
	labels []string
}

// runInputs makes sure the Agent's credentials and the Container's agent Git
// access exist, and reads the run's fence and goal. A nil input ends the
// step: the credentials are missing (look again after result's wait), or
// the Task's issue was closed, which ends the Task.
func (r *TaskReconciler) runInputs(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents, role string) (*runInput, ctrl.Result, error) {
	agent := agents.forRole(role)
	if found, err := r.agentCredentials(ctx, task, status, agent); err != nil {
		return nil, ctrl.Result{}, err
	} else if !found {
		return nil, ctrl.Result{RequeueAfter: r.secretRecheck()}, nil
	}
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	// Bring the Git host's rules up to date before an agent gets access: older
	// repositories were protected with fewer of them.
	protectCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	if err := protectRepository(protectCtx, r.Client, provider, repo, container); err != nil {
		return nil, ctrl.Result{}, err
	}
	protected, err := protectedPaths(ctx, r.Client, container)
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	gitSecret, err := r.ensureAgentGit(ctx, container, provider, repo)
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	in := &runInput{provider: provider, repo: repo, protected: protected, gitSecret: gitSecret, goal: task.Spec.Goal}
	if task.Spec.Issue > 0 {
		// Read before every run: a person may have clarified the issue since.
		issue, err := provider.GetIssue(protectCtx, repo, int(task.Spec.Issue))
		if err != nil {
			return nil, ctrl.Result{}, err
		}
		if issue.State == stateClosed {
			r.end(task, status, model.IssueClosed(issue.Number))
			return nil, ctrl.Result{}, nil
		}
		status.IssueTitle, status.IssueURL = issue.Title, issue.URL
		in.goal = model.IssueGoal(issue.Number, issue.Title, issue.Body)
		in.labels = issue.Labels
	}
	return in, ctrl.Result{}, nil
}

// runSandbox builds the Sandbox named name for a run of role on the Container.
func (r *TaskReconciler) runSandbox(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents, role, name string, in *runInput,
	envs []runEnvironment) (*unstructured.Unstructured, error) {
	agent := agents.forRole(role)
	story := c4Context(ctx, r.Client, container)
	npmSecret, pypiSecret, err := r.packagesSecrets(ctx, container)
	if err != nil {
		return nil, err
	}
	repo := in.repo
	return buildSandbox(sandboxRun{
		task: task, status: status, container: container, agent: agent, name: name, role: role,
		repoURL: repo.CloneURL, gitSecret: in.gitSecret, context: story.Text(), goal: in.goal,
		maxRounds: agents.implementer.Spec.MaxRounds, related: story.RelatedRepositories(),
		protectedPaths: in.protected, allowedPaths: task.Spec.AllowedProtectedPaths, resumed: resumedRun(status),
		workspaceMemory: r.WorkspaceMemory, checkpointClass: r.CheckpointClass,
		goCache: r.SandboxGoCache, goProxy: repo.GoProxy, checkCommand: container.Spec.CheckCommand,
		npmScope: repo.NpmScope, npmRegistry: repo.NpmRegistry, pypiIndex: repo.PyPIIndex,
		npmSecret: npmSecret, pypiSecret: pypiSecret,
		pullSecrets: r.SharedPullSecrets, modelProxyURL: r.sandboxModelProxy(agent),
		environments: envs,
	}, r.Scheme, r.now())
}

// startAtReview starts a try that continues an earlier try's green, unreviewed
// commit at its checks, with no implementer run: they pass, so the next step
// starts the review (model.StartsAtReview). The review's first sandbox posts
// the comments a first run does, so a failed status write here posts none.
func (r *TaskReconciler) startAtReview(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	commit string) (ctrl.Result, error) {
	setProgress(status, model.InheritedChecks(progressOf(status), commit))
	r.progress(task, status, metav1.ConditionTrue, "Checking", model.StartAtReviewMessage(commit, status.Continues))
	logf.FromContext(ctx).Info("Starting a try at the earlier try's review", "commit", commit,
		"earlierTask", status.Continues)
	return ctrl.Result{}, nil // the status write triggers the next step
}

// firstReviewAtStart reports whether role's run is the first of a try that
// started at review (startAtReview): its round 1 never implemented.
func firstReviewAtStart(status *c4v1beta1.TaskStatus, role string) bool {
	return role == model.RoleReviewer && status.Continues != "" && status.Round == 1 &&
		status.ReviewAttempts == 0 && status.ReviewAgains == 0 && status.Reruns == 0 &&
		!slices.ContainsFunc(status.History, func(h c4v1beta1.TaskTransition) bool {
			return h.Phase == c4v1beta1.TaskImplementing
		})
}

// agentCredentials reports whether the Agent's credentials Secret, if it names
// one, exists; when it doesn't, the Task says so and waits for it. An Agent on
// the shared token (onSharedToken) needs the Secret in the operator's
// namespace, where the model proxy reads it, and its own namespace opted in
// to the shared Secrets: the proxy admits only those, so a sandbox of another
// would start and time out.
func (r *TaskReconciler) agentCredentials(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	agent *c4v1beta1.Agent) (bool, error) {
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
		if ns.Labels[c4v1beta1.LabelSharedSecrets] != sharedSecretsOptIn {
			r.progress(task, status, metav1.ConditionFalse, "CredentialsNotFound",
				fmt.Sprintf("Agent %s is on the factory's shared Secret %s: label namespace %s %s=%s, so its "+
					"agents reach the factory's model proxy", agent.Name, ref.Name, task.Namespace,
					c4v1beta1.LabelSharedSecrets, sharedSecretsOptIn))
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
func (r *TaskReconciler) ensureAgentGit(ctx context.Context, container *c4v1beta1.Container,
	provider ports.AgentAccess, repo ports.Repository) (string, error) {
	r.revokeOldAgentTokens(ctx, container.Namespace, container.Name, "")
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	// The scopes are kept with the token, so a token made with fewer is made again.
	return ensureAgentSecret(ctx, r.Client, r.APIReader, container, agentGitSecret(container.Name),
		map[string]string{c4v1beta1.LabelProject: container.Namespace, c4v1beta1.LabelContainer: container.Name},
		map[string]string{"scopes": ports.AgentTokenScopes}, r.now(),
		func() (ports.Credentials, error) {
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
const annotationOldTokensBefore = "theos.factory.io/old-tokens-before" //nolint:gosec // an annotation's name

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
		client.MatchingLabels{c4v1beta1.LabelContainer: name, LabelAgentSandbox: agentSandboxValue}); err != nil {
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
	var container c4v1beta1.Container
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
func (r *TaskReconciler) merge(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents, step model.TaskStep) (ctrl.Result, error) {
	if task.Spec.Benchmark != nil {
		// A Benchmark's Task never merges: approved, it ends Approved, and its
		// Benchmark closes its pull request once its Scorers judged it.
		status.Review = r.reviewFrom(ctx, task, status)
		if err := r.endRun(ctx, task, status); err != nil {
			return ctrl.Result{}, err
		}
		r.finish(task, status, c4v1beta1.TaskApproved, metav1.ConditionTrue, model.TaskApproved,
			"Approved with green checks; a Benchmark's Task never merges")
		logf.FromContext(ctx).Info("Approved Benchmark Task", "url", status.PullRequestURL, "round", status.Round)
		return ctrl.Result{}, nil
	}
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return ctrl.Result{}, err
	}
	if wait, err := r.waitForRelease(ctx, task, status, provider, repo); err != nil || wait {
		return ctrl.Result{RequeueAfter: hostRecheck(ctx, r.checksRecheck())}, err
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
	var skip *pushSkip
	switch action {
	case model.MergeFailClosed:
		if isSpec(task) {
			// A person closed the spec's pull request: the spec is declined.
			return ctrl.Result{}, r.failWith(ctx, task, status, container, model.SpecDeclined(status.PullRequestURL))
		}
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
		return ctrl.Result{RequeueAfter: hostRecheck(ctx, r.checksRecheck())}, r.endRun(ctx, task, status)
	case model.MergeSquash:
		var refusal string
		refusal, skip, err = r.mergeReviewed(ctx, provider, repo, task, status, container, agents, pr.Behind)
		pending := errors.Is(err, errChecksPending)
		if err != nil && !pending {
			return ctrl.Result{}, err
		}
		switch model.AfterSquash(pending, refusal) {
		case model.SquashWaitForChecks:
			// Waits while the review run lasts; if it ends first, the merge is refused.
			r.progress(task, status, metav1.ConditionTrue, "WaitingForChecks",
				"Approved; CI runs again on "+status.Commit+", merging once it passes")
			return ctrl.Result{RequeueAfter: hostRecheck(ctx, r.checksRecheck())}, nil
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
		r.carryChecks(ctx, provider, repo, status, skip)
	}
	r.recordPull(ctx, provider, repo, status)
	if isSpec(task) {
		if err := r.specMerged(ctx, provider, repo, task, status); err != nil {
			return ctrl.Result{}, err
		}
	} else if task.Spec.Issue > 0 {
		followUps, open, openBy := mergeFindings(task, status, agents, reviewRun)
		status.FollowUps = r.closeIssue(ctx, provider, repo, task, status, agents.reviewer, openBy, followUps, open)
	}
	r.finish(task, status, c4v1beta1.TaskMerged, metav1.ConditionTrue, "Merged", step.Message)
	logf.FromContext(ctx).Info("Merged Task", "url", status.PullRequestURL, "round", status.Round)
	return ctrl.Result{}, nil
}

// waitForRelease holds a Crew's Task at its merge while the Crew's cut
// release waits for its tag, unless the Task's issue is in that release
// (docs/plans/done/release-cadence.md, C10): merged now, its work would land under
// the tag and miss the changelog the prepare issue writes. It costs no round,
// and the Task merges on a later reconcile, once the tag is made. A Standard
// change merges as usual: its gated merge needs a review run that still
// lasts. It reports whether the Task waits.
func (r *TaskReconciler) waitForRelease(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	provider ports.Issues, repo ports.Repository) (bool, error) {
	name := task.Labels[LabelCrew]
	if name == "" || task.Spec.Issue == 0 || len(task.Spec.AllowedProtectedPaths) > 0 {
		return false, nil
	}
	var crew c4v1beta1.Crew
	if err := r.Get(ctx, types.NamespacedName{Namespace: task.Namespace, Name: name}, &crew); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	cut := crew.Status.CutRelease
	if cut == "" {
		return false, nil
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	issue, err := provider.GetIssue(gitCtx, repo, int(task.Spec.Issue))
	if err != nil && !errors.Is(err, ports.ErrIssueNotFound) {
		return false, err
	}
	if !model.MergeWaitsForRelease(cut, issue.MilestoneTitle) {
		return false, nil
	}
	if cond := apimeta.FindStatusCondition(status.Conditions, ConditionProgressing); cond == nil ||
		cond.Reason != model.ReasonWaitingForRelease {
		logf.FromContext(ctx).Info("Holding a merge for the cut release's tag", "release", cut,
			"url", status.PullRequestURL)
	}
	r.progress(task, status, metav1.ConditionTrue, model.ReasonWaitingForRelease, model.WaitingForReleaseMessage(cut))
	return true, nil
}

// waitsForRelease reports whether a Task waits at its merge for its Crew's
// cut release to be tagged (waitForRelease).
func waitsForRelease(t *c4v1beta1.Task) bool {
	cond := apimeta.FindStatusCondition(t.Status.Conditions, ConditionProgressing)
	return !isFinal(t.Status.Phase) && cond != nil && cond.Reason == model.ReasonWaitingForRelease
}

// updateBranch merges main into an approved change's branch, as main moved since
// its checks ran, and waits for the checks on the new head, which merges once
// they pass. The review run stays for its follow-ups, and the verdict goes into
// status. A branch that conflicts with main goes back to its implementer: the
// merge, not the Git host's mergeable flag, tells a conflict.
func (r *TaskReconciler) updateBranch(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents, step model.TaskStep) (ctrl.Result, error) {
	provider, repo, err := r.gitRepo(ctx, container)
	if err != nil {
		return ctrl.Result{}, err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	err = provider.UpdatePullRequest(gitCtx, repo, int(status.PullRequest))
	if errors.Is(err, ports.ErrConflict) {
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
		return ctrl.Result{RequeueAfter: hostRecheck(ctx, r.checksRecheck())}, nil
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
	return ctrl.Result{RequeueAfter: hostRecheck(ctx, r.checksRecheck())}, nil
}

// mergeCommit reads the commit the pull request's merge made on the default
// branch; empty when it can't be read yet, for the Task to read again once Merged.
func (r *TaskReconciler) mergeCommit(ctx context.Context, provider ports.GitHost,
	repo ports.Repository, status *c4v1beta1.TaskStatus) string {
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	pr, err := provider.GetPullRequest(gitCtx, repo, int(status.PullRequest))
	if err != nil {
		logf.FromContext(ctx).Error(err, "Failed to read the merge commit", "url", status.PullRequestURL)
		return ""
	}
	return pr.MergeCommit
}

// recordPull keeps the merged pull request's size and how many of its
// commits a person wrote (docs/plans/done/measure-and-improve.md, decisions 3 and
// 4), once. A read that fails leaves them unset, for the merged Task to read
// again.
func (r *TaskReconciler) recordPull(ctx context.Context, provider ports.GitHost, repo ports.Repository,
	status *c4v1beta1.TaskStatus) {
	if status.PullRequest == 0 || status.PersonCommits != nil {
		return
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	pr, err := provider.GetPullRequest(gitCtx, repo, int(status.PullRequest))
	if err != nil {
		logf.FromContext(ctx).Info("Could not read the merged pull request's size; trying again later",
			"url", status.PullRequestURL, "error", err.Error())
		return
	}
	commits, err := provider.PullRequestCommits(gitCtx, repo, int(status.PullRequest))
	if err != nil {
		logf.FromContext(ctx).Info("Could not read the merged pull request's commits; trying again later",
			"url", status.PullRequestURL, "error", err.Error())
		return
	}
	read := make([]model.PullCommit, 0, len(commits))
	for _, c := range commits {
		read = append(read, model.PullCommit{AuthorEmail: c.AuthorEmail, Parents: c.Parents})
	}
	person := int32(model.PersonCommits(read))
	status.PersonCommits = &person
	status.PullRequestSize = c4v1beta1.TaskPullRequestSize{Additions: int32(pr.Additions),
		Deletions: int32(pr.Deletions), Files: int32(pr.ChangedFiles)}
}

// mergeReviewed squashes the reviewed commit into main, posting the reviewer's
// approval; a push since the review makes the merge fail. A Standard change goes
// through gatedMerge, which may refuse it or wait for CI. A merge that skips
// main's push run (#2060) returns what carryChecks needs.
func (r *TaskReconciler) mergeReviewed(ctx context.Context, provider ports.GitHost,
	repo ports.Repository, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents, behind bool) (refusal string, skip *pushSkip, err error) {
	if len(task.Spec.AllowedProtectedPaths) > 0 {
		refusal, err = r.gatedMerge(ctx, provider, repo, task, status, container, agents.reviewer)
		return refusal, nil, err
	}
	switch {
	case status.Phase == c4v1beta1.TaskAwaitingApproval:
		// The agents' approvals went on the pull request as it began to wait
		// for a person's, a spec's or a change's.
	case verifies(task):
		// The reviewer's approval went on the pull request as the verification
		// began; the verifier's goes with the merge.
		r.postVerification(ctx, task, status, container, agents.verifier, ports.ReviewApprove)
	default:
		r.approve(ctx, task, status, container, agents.reviewer)
	}
	skip = r.planSkip(ctx, provider, repo, status, container, behind)
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	err = provider.MergePullRequest(gitCtx, repo, int(status.PullRequest), status.Commit,
		model.SquashTitle(pullTitle(task, status), skip != nil), mergeMessage(task, status, container))
	return "", skip, err
}

// approve posts the reviewer's approval of the round's commit.
func (r *TaskReconciler) approve(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, reviewer *c4v1beta1.Agent) {
	if status.Review != nil {
		r.review(ctx, task, status, container, reviewer, ports.ReviewApprove, status.Review.Summary)
	}
}

// reviewFrom reads the verdict of the current review sandbox, if any.
func (r *TaskReconciler) reviewFrom(ctx context.Context, task *c4v1beta1.Task,
	status *c4v1beta1.TaskStatus) *c4v1beta1.TaskReview {
	run := r.reviewRun(ctx, task, status)
	if run == nil {
		return status.Review
	}
	return &c4v1beta1.TaskReview{Verdict: run.Verdict, Summary: run.Summary}
}

// reviewRun reads the current review sandbox's result, if it gave a verdict:
// in review, or while the approved change's branch, with main merged in, is
// checked again. The verifier's sandbox, which a verified change keeps then,
// is not a review's.
func (r *TaskReconciler) reviewRun(ctx context.Context, task *c4v1beta1.Task,
	status *c4v1beta1.TaskStatus) *model.RunResult {
	reviewing := status.Phase == c4v1beta1.TaskReviewing ||
		status.Phase == c4v1beta1.TaskChecking && status.MergeWhenChecksPass
	if !reviewing || status.Sandbox == "" || model.SandboxRole(status.Sandbox) == model.RoleVerifier {
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
func (r *TaskReconciler) endRun(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus) error {
	ended, err := r.recordRun(ctx, task, status)
	if err != nil {
		return err
	}
	return r.afterStatusWrite(ctx, task, ended)
}

// afterStatusWrite drops, suspends or resumes the ended run's sandbox once
// writeStatus wrote the status that records it, or now outside a Reconcile.
func (r *TaskReconciler) afterStatusWrite(ctx context.Context, task *c4v1beta1.Task, ended endedRun) error {
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
func (r *TaskReconciler) dropEndedRuns(ctx context.Context, task *c4v1beta1.Task) error {
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

// endedRun is a run recordRun took out of the Task's status, for dropRun,
// or one whose pod ended and whose sandbox suspends or resumes.
type endedRun struct {
	sandbox string
	round   int32
	result  *model.RunResult
	// suspend keeps the sandbox, and suspends it: its run resumes in place.
	// why, when set, says the run paused or waits for its agent, rather than
	// being interrupted.
	suspend bool
	why     suspension
	// resume, when set, resumes the suspended sandbox with it.
	resume *sandboxResume
	// leases are the test environments the run held, returned with it.
	leases []c4v1beta1.TaskEnvironmentLease
}

// recordRun adds what the current run cost to the Task's, records the run,
// unless suspendRun did, and takes its sandbox out of status; dropRun then deletes it. An empty
// sandbox means no run was current.
func (r *TaskReconciler) recordRun(ctx context.Context, task *c4v1beta1.Task,
	status *c4v1beta1.TaskStatus) (endedRun, error) {
	if status.Sandbox == "" {
		return endedRun{}, nil
	}
	run, err := runState(ctx, r.Client, task.Namespace, status.Sandbox, status.SandboxPod)
	if err != nil {
		return endedRun{}, err
	}
	// A run suspendRun counted, its sandbox suspended or its suspend failed,
	// is not counted again.
	if !runCounted(status, run) {
		r.countRun(status, run)
	}
	ended := endedRun{sandbox: status.Sandbox, round: status.Round, result: run.Result,
		leases: takeLeases(status, status.Sandbox)}
	status.Sandbox, status.SandboxPod, status.CountedPod = "", "", ""
	return ended, nil
}

// countRun adds what the current sandbox's run cost to the Task's, and
// records the run.
func (r *TaskReconciler) countRun(status *c4v1beta1.TaskStatus, run model.RunState) {
	milliUSD, withoutCost := run.Cost()
	status.CostMilliUSD += milliUSD
	status.RunsWithoutCost += withoutCost
	status.Tokens = addTokens(status.Tokens, run.Tokens())
	if rec, ok := run.Record(int(status.Round), status.Sandbox, r.now()); ok {
		status.Runs = model.KeepNewest(append(status.Runs, runRecord(rec)), model.MaxRunRecords)
	}
}

// dropRun deletes the sandbox of a run recordRun ended, or suspends or
// resumes it, and posts when the ended pod's sessions handed off.
func (r *TaskReconciler) dropRun(ctx context.Context, task *c4v1beta1.Task, ended endedRun) error {
	r.returnLeases(ctx, task, ended.leases)
	switch {
	case ended.sandbox == "":
		return nil
	case ended.resume != nil:
		return r.resumeSandbox(ctx, task.Namespace, ended.sandbox, ended.resume)
	case ended.suspend:
		if err := r.suspendSandbox(ctx, task.Namespace, ended.sandbox, ended.why); err != nil {
			return err
		}
	default:
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
	}
	if res := ended.result; res != nil && len(res.SessionHandoffs) > 0 && r.Recorder != nil {
		r.Recorder.Eventf(task, nil, corev1.EventTypeNormal, eventSessionHandedOff, "HandOff", "%s",
			model.HandoffNote(int(ended.round), res.Role, res.SessionHandoffs))
	}
	return nil
}

// runRecord is rec as Task status keeps it.
func runRecord(rec model.RunRecord) c4v1beta1.TaskAgentRun {
	out := c4v1beta1.TaskAgentRun{Round: int32(rec.Round), Role: rec.Role, Sandbox: rec.Sandbox,
		Agent: rec.Config.Agent, Harness: rec.Config.Harness, Model: rec.Config.Model,
		EndedAt: metav1.NewTime(rec.EndedAt), SessionHandoffs: rec.SessionHandoffs,
		Tokens: addTokens(nil, rec.Tokens)}
	if rec.CostReported {
		out.CostMilliUSD = &rec.CostMilliUSD
	}
	return out
}

// addTokens adds add to tokens, which stays nil while nothing is counted.
func addTokens(tokens *c4v1beta1.TokenUsage, add model.TokenUsage) *c4v1beta1.TokenUsage {
	if add == (model.TokenUsage{}) {
		return tokens
	}
	var sum model.TokenUsage
	if tokens != nil {
		sum = model.TokenUsage(*tokens)
	}
	sum.Add(add)
	out := c4v1beta1.TokenUsage(sum)
	return &out
}

// progressOf copies the part of status that the model's transitions change.
func progressOf(status *c4v1beta1.TaskStatus) model.TaskProgress {
	p := model.TaskProgress{Phase: string(status.Phase), Round: int(status.Round),
		ReviewAttempts: int(status.ReviewAttempts), ReviewAgains: int(status.ReviewAgains), Reruns: int(status.Reruns),
		Resumes:  int(status.Resumes),
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
func failedChecks(status *c4v1beta1.TaskStatus) bool {
	checks := status.Checks
	return checks != nil && checks.Commit == status.Commit &&
		(checks.State == ports.CheckFailure || checks.State == ports.CheckError)
}

// setProgress copies a transition's result back into status.
func setProgress(status *c4v1beta1.TaskStatus, p model.TaskProgress) {
	status.Phase, status.Round = c4v1beta1.TaskPhase(p.Phase), int32(p.Round)
	status.ReviewAttempts, status.ReviewAgains = int32(p.ReviewAttempts), int32(p.ReviewAgains)
	status.Reruns, status.Restarts = int32(p.Reruns), int32(p.Restarts)
	status.Resumes = int32(p.Resumes)
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
func (r *TaskReconciler) end(task *c4v1beta1.Task, status *c4v1beta1.TaskStatus, e model.TaskEnd) {
	succeeded := metav1.ConditionFalse
	if e.Phase == model.TaskMerged {
		succeeded = metav1.ConditionTrue
	}
	r.finish(task, status, c4v1beta1.TaskPhase(e.Phase), succeeded, e.Reason, e.Message)
}

func (r *TaskReconciler) finish(task *c4v1beta1.Task, status *c4v1beta1.TaskStatus, phase c4v1beta1.TaskPhase,
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
func (r *TaskReconciler) rerunChecks(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, agents taskAgents, step model.TaskStep) (ctrl.Result, error) {
	if status.Phase == c4v1beta1.TaskImplementing {
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
		return ctrl.Result{RequeueAfter: hostRecheck(ctx, r.checksRecheck())}, nil
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
func (r *TaskReconciler) fail(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, reason, message string) error {
	r.comment(ctx, task, status, container, "The Task failed. "+message)
	if err := r.endRun(ctx, task, status); err != nil {
		return err
	}
	r.dropProposal(ctx, task, status, container)
	r.finish(task, status, c4v1beta1.TaskFailed, metav1.ConditionFalse, reason, message)
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
	case model.ReasonIssueUnseen:
		why += fmt.Sprintf("\n\nThe factory gives the agent the issue's text, so the first such try on an issue "+
			"doesn't count towards the limit; after %d, each one counts.", model.MaxFreeUnseen)
	}
	r.handBack(ctx, task, container, why)
	logf.FromContext(ctx).Info("Failed Task", "reason", reason, "message", message)
	return nil
}

// failWith fails the Task as the model decided.
func (r *TaskReconciler) failWith(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, e model.TaskEnd) error {
	return r.fail(ctx, task, status, container, e.Reason, e.Message)
}

// comment adds a note to the Task's pull request, if it has one. The pull request
// is a report; failing to comment doesn't stop the Task.
func (r *TaskReconciler) comment(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, body string) {
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

func pullTitle(task *c4v1beta1.Task, status *c4v1beta1.TaskStatus) string {
	if b := task.Spec.Benchmark; b != nil {
		return model.BenchmarkPullTitle(b.BenchmarkRef.Name, b.Variant, int(b.Goal), int(b.Repetition),
			firstLine(task.Spec.Goal))
	}
	if isSpec(task) {
		return firstLine(model.SpecTitle(status.IssueTitle))
	}
	if task.Spec.Issue > 0 && status.IssueTitle != "" {
		return firstLine(status.IssueTitle)
	}
	return firstLine(task.Spec.Goal)
}

// ensureBranchAt makes branch at commit, unless the Git host has it already.
func ensureBranchAt(ctx context.Context, provider ports.GitHost, repo ports.Repository, branch, commit string) error {
	has, err := provider.HasBranch(ctx, repo, branch)
	if err != nil || has {
		return err
	}
	return provider.CreateBranch(ctx, repo, branch, commit)
}

// pullBody is a pull request's description; extra are the unprotected files a
// Standard change changes besides its allowed ones, named for the reviewer.
func pullBody(task *c4v1beta1.Task, agents taskAgents, round int32, summary string, extra []string) string {
	var b strings.Builder
	switch {
	case isSpec(task):
		// The spec's merge leaves the issue open: no closing keyword.
		fmt.Fprintf(&b, "The spec of issue #%d, in `%s`. Once it merges, its acceptance criteria go into the "+
			"issue, which stays open to be built.\n\n", task.Spec.Issue, model.SpecPath(int(task.Spec.Issue)))
	case task.Spec.Issue > 0:
		// Gitea links the issue, and closes it on merge.
		fmt.Fprintf(&b, "Closes #%d.\n\n", task.Spec.Issue)
	}
	fmt.Fprintf(&b, "Task `%s`: implemented by %s, reviewed by %s", task.Name,
		agentName(agents.implementer), agentName(agents.reviewer))
	if agents.verifier != nil {
		fmt.Fprintf(&b, ", verified by %s", agentName(agents.verifier))
	}
	b.WriteString(".\n")
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
func mergeMessage(task *c4v1beta1.Task, status *c4v1beta1.TaskStatus, container *c4v1beta1.Container) string {
	bot := model.AgentUsername(container.Namespace, container.Name)
	return fmt.Sprintf("Task %s, round %d.\n\nCo-authored-by: %s <%s>",
		task.Name, status.Round, bot, model.AgentEmail(bot))
}

// agentGitRepo is the Container's repository as its agent bot sees it. The
// implementer's pull requests are opened as the bot: they are its work, and the
// Git host refuses a review of one's own pull request, so the operator's user
// could not review them otherwise.
func (r *TaskReconciler) agentGitRepo(ctx context.Context, container *c4v1beta1.Container) (ports.GitHost,
	ports.Repository, error) {
	var gp c4v1beta1.GitProvider
	if err := r.Get(ctx, client.ObjectKey{Namespace: container.Namespace, Name: container.Spec.GitProviderRef.Name},
		&gp); err != nil {
		return nil, ports.Repository{}, fmt.Errorf("GitProvider %s: %w", container.Spec.GitProviderRef.Name, err)
	}
	var secret corev1.Secret
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: container.Namespace, Name: agentGitSecret(container.Name)},
		&secret); err != nil {
		return nil, ports.Repository{}, fmt.Errorf("agent Git access of %s: %w", container.Name, err)
	}
	provider, err := r.Git.Provider(ports.Config{
		Kind: string(gp.Spec.Provider), URL: gp.Spec.URL, Owner: gp.Spec.Owner,
		Credentials: ports.Credentials{Username: string(secret.Data["username"]), Token: string(secret.Data["token"])},
		Packages:    packagesOf(&gp),
	})
	if err != nil {
		return nil, ports.Repository{}, err
	}
	return provider, provider.Repository(container.Name), nil
}

// packagesSecrets names the Secrets of the npm registry and PyPI index of a
// Container's GitProvider, "" for one without.
func (r *TaskReconciler) packagesSecrets(ctx context.Context, container *c4v1beta1.Container) (npm, pypi string,
	err error) {
	var gp c4v1beta1.GitProvider
	if err := r.Get(ctx, client.ObjectKey{Namespace: container.Namespace, Name: container.Spec.GitProviderRef.Name},
		&gp); err != nil {
		return "", "", fmt.Errorf("GitProvider %s: %w", container.Spec.GitProviderRef.Name, err)
	}
	npm, pypi = packagesSecrets(&gp)
	return npm, pypi, nil
}

// extrasNote names the unprotected files a Standard change changes besides its
// allowed ones.
func extrasNote(task *c4v1beta1.Task, extra []string) string {
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
