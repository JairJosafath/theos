/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// A test-run environment's runs (docs/plans/test-environments.md, T12, step
// 10): the run that holds a lease can't reach the operator, and the
// provider's credential never reaches the run, so the run asks over its
// Git host. `theos-env test` pushes a request commit to the lease's
// branch; the operator reads it on its next poll, as it reads CI's checks,
// runs it on the provider when the lease's token signed it and the day's
// budget allows, pushes the report and artifacts to the same branch, where
// theos-env reads them back, and comments the outcome on the holder Task's
// pull request, once it has one, where the reviewer and people see it.

// +kubebuilder:rbac:groups=theos.factory.io,resources=containers;gitproviders,verbs=get;list;watch

// testRunRecheck is how often a test-run environment with live leases, or
// with runs going, looks again.
const testRunRecheck = 15 * time.Second

// testRunGit is where a lease's branch is: the holder's Container's Git
// host and repository.
type testRunGit struct {
	host interface {
		ports.GitHost
		ports.Issues
	}
	repo ports.Repository
}

// tendTestRuns answers the requests on the branches of env's live leases,
// moves the runs started on, and forgets the runs of leases gone once their
// outcome is on the pull request. It reports whether to look again soon.
func (r *TestEnvironmentReconciler) tendTestRuns(ctx context.Context, env *c4v1beta1.TestEnvironment,
	status *c4v1beta1.TestEnvironmentStatus, environment ports.Environment, runner ports.TestRuns,
	live []ports.Lease) bool {
	log := logf.FromContext(ctx)
	soon := false
	for _, l := range live {
		if l.Holder == "" || l.State != model.LeaseReady {
			continue
		}
		soon = true
		if err := r.tendLease(ctx, env, status, environment, runner, l); err != nil {
			log.Error(err, "Failed to tend a test-run lease's requests; trying again", "lease", l.ID, "holder", l.Holder)
		}
	}
	for i := range status.TestRuns {
		t := &status.TestRuns[i]
		if t.Comment == "" {
			continue
		}
		soon = true
		if err := r.commentTestRun(ctx, env.Namespace, t); err != nil {
			log.Error(err, "Failed to comment a test run on its pull request; trying again", "testRun", t.ID)
		}
	}
	r.forgetTestRuns(ctx, env.Namespace, status, live)
	return soon
}

// tendLease answers the request on lease l's branch, or moves on the run it
// started for it.
func (r *TestEnvironmentReconciler) tendLease(ctx context.Context, env *c4v1beta1.TestEnvironment,
	status *c4v1beta1.TestEnvironmentStatus, environment ports.Environment, runner ports.TestRuns, l ports.Lease) error {
	var task c4v1beta1.Task
	if err := r.Get(ctx, client.ObjectKey{Namespace: env.Namespace, Name: l.Holder}, &task); err != nil {
		return client.IgnoreNotFound(err)
	}
	git, err := r.testRunGit(ctx, env.Namespace, task.Spec.ContainerRef.Name)
	if err != nil {
		return err
	}
	branch := model.TestRunBranch(l.ID)
	head, err := git.host.BranchCommit(ctx, git.repo, branch)
	if err != nil || head == "" {
		return err
	}
	i := slices.IndexFunc(status.TestRuns, func(t c4v1beta1.TestEnvironmentTestRun) bool {
		return t.Lease == l.ID && (t.Request == head || t.Answer == head)
	})
	switch {
	case i >= 0 && status.TestRuns[i].Answer == head:
		return nil // answered: the run reads it, and may ask again
	case i >= 0 && (status.TestRuns[i].State == model.TestRunReported || status.TestRuns[i].State == model.TestRunRejected):
		return nil
	case i >= 0:
		return r.moveTestRun(ctx, env, environment, runner, git, &status.TestRuns[i])
	}
	files, request, err := r.readRequest(ctx, git, branch, head)
	if err != nil || request == nil {
		return err
	}
	t := c4v1beta1.TestEnvironmentTestRun{ID: model.TestRunID(l.ID, head), Lease: l.ID, Holder: l.Holder,
		Container: task.Spec.ContainerRef.Name, Request: head, Commit: request.Commit, Type: request.Type,
		Since: &metav1.Time{Time: r.now()}}
	token, err := r.leaseTokenOf(ctx, &task, l.ID)
	if err != nil {
		return err
	}
	app, tests := files[model.TestRunAppDir+request.App].Data, files[model.TestRunTestsDir+request.Tests].Data
	var spent int64
	if status.SpentToday != nil {
		spent = status.SpentToday.MilliUSD
	}
	why := ""
	if err := model.CheckTestRun(token, l.ID, *request, app, tests); err != nil {
		why = err.Error()
	} else if model.LeaseMayStart(0, 1, spent, model.BudgetMilliUSD(env.Spec.Limits.BudgetUSDPerDay)) == model.EnvOverBudget {
		why = env.Name + " spent its budget for today; no test run starts until tomorrow, or a higher budget"
	}
	status.TestRuns = append(status.TestRuns, t)
	entry := &status.TestRuns[len(status.TestRuns)-1]
	if why != "" {
		logf.FromContext(ctx).Info("Rejected a test-run request", "lease", l.ID, "request", head, "why", why)
		return r.answer(ctx, env, git, entry, model.TestRunReport{Result: model.TestRunRefused, Message: why}, nil)
	}
	entry.State = model.TestRunPending
	logf.FromContext(ctx).Info("Starting a test run", "lease", l.ID, "testRun", entry.ID, "type", request.Type)
	return r.startTestRun(ctx, env, environment, runner, git, entry, l, app, tests, request)
}

// readRequest reads the request commit head on branch: its request and its
// files, nil when the commit holds none, such as one of the operator's.
func (r *TestEnvironmentReconciler) readRequest(ctx context.Context, git testRunGit, branch, head string) (scaffold.Tree,
	*model.TestRunRequest, error) {
	files, sha, err := git.host.ReadFiles(ctx, git.repo, branch, []string{model.TestRunRequestFile,
		model.TestRunAppDir, model.TestRunTestsDir})
	if err != nil || sha != head {
		return nil, nil, err // moved on since: the next pass reads it
	}
	data, ok := files[model.TestRunRequestFile]
	if !ok {
		return nil, nil, nil
	}
	var request model.TestRunRequest
	if err := json.Unmarshal(data.Data, &request); err != nil {
		request = model.TestRunRequest{} // CheckTestRun says what is wrong
	}
	return files, &request, nil
}

// startTestRun has the provider start t, or move on its uploads, and
// answers it once it is done.
func (r *TestEnvironmentReconciler) startTestRun(ctx context.Context, env *c4v1beta1.TestEnvironment,
	environment ports.Environment, runner ports.TestRuns, git testRunGit, t *c4v1beta1.TestEnvironmentTestRun,
	l ports.Lease, app, tests []byte, request *model.TestRunRequest) error {
	left := model.LeaseDeadline(int(env.Spec.Limits.MaxLeaseMinutes)) - r.now().Sub(l.Started)
	run, err := runner.StartTestRun(ctx, environment, ports.TestRunRequest{ID: t.ID, Lease: l.ID, Type: request.Type,
		App: ports.TestRunFile{Name: request.App, Data: app}, Tests: ports.TestRunFile{Name: request.Tests, Data: tests},
		Timeout: max(left, time.Minute)})
	if err != nil {
		return err
	}
	return r.followed(ctx, env, environment, runner, git, t, run)
}

// moveTestRun moves t on: its uploads, while the provider has no run yet,
// then the run, until it is done and answered.
func (r *TestEnvironmentReconciler) moveTestRun(ctx context.Context, env *c4v1beta1.TestEnvironment,
	environment ports.Environment, runner ports.TestRuns, git testRunGit, t *c4v1beta1.TestEnvironmentTestRun) error {
	run, err := runner.GetTestRun(ctx, environment, t.ID)
	if errors.Is(err, ports.ErrTestRunNotFound) {
		files, request, err := r.readRequest(ctx, git, model.TestRunBranch(t.Lease), t.Request)
		if err != nil || request == nil {
			return err
		}
		lease, err := r.leaseOf(ctx, environment, t.Lease)
		if err != nil {
			return err
		}
		return r.startTestRun(ctx, env, environment, runner, git, t, lease,
			files[model.TestRunAppDir+request.App].Data, files[model.TestRunTestsDir+request.Tests].Data, request)
	}
	if err != nil {
		return err
	}
	return r.followed(ctx, env, environment, runner, git, t, run)
}

// leaseOf is lease id of environment, as its provider has it.
func (r *TestEnvironmentReconciler) leaseOf(ctx context.Context, environment ports.Environment, id string) (ports.Lease,
	error) {
	provider, err := r.Providers.For(environment.Member)
	if err != nil {
		return ports.Lease{}, err
	}
	return provider.Get(ctx, environment, id)
}

// followed records what the provider says of t's run, and answers it once
// it is done.
func (r *TestEnvironmentReconciler) followed(ctx context.Context, env *c4v1beta1.TestEnvironment,
	environment ports.Environment, runner ports.TestRuns, git testRunGit, t *c4v1beta1.TestEnvironmentTestRun,
	run ports.TestRun) error {
	t.URL, t.CostMilliUSD = run.URL, run.CostMilliUSD
	if run.State != model.TestRunDone {
		t.State, t.Outcome = run.State, run.Message
		return nil
	}
	report, err := runner.TestRunReport(ctx, environment, t.ID)
	switch {
	case errors.Is(err, ports.ErrTestRunNotFound):
		// Done before it ran, such as an app the provider refused.
		report = ports.TestRunReport{Report: model.TestRunReport{Run: t.ID, Result: run.Result, Message: run.Message,
			URL: run.URL, Counters: run.Counters, CostMilliUSD: run.CostMilliUSD}}
	case err != nil:
		return err
	}
	return r.answer(ctx, env, git, t, report.Report, report.Artifacts)
}

// answer pushes t's report and artifacts to its lease's branch, where the
// run reads them, unless the run asked again since, and readies its comment
// on the pull request; a rejected request's report says why.
func (r *TestEnvironmentReconciler) answer(ctx context.Context, env *c4v1beta1.TestEnvironment, git testRunGit,
	t *c4v1beta1.TestEnvironmentTestRun, report model.TestRunReport, artifacts []ports.TestRunArtifact) error {
	report.Environment, report.Commit, report.Type = env.Name, t.Commit, t.Type
	if report.Run == "" && report.Result != model.TestRunRefused {
		report.Run = t.ID
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	files := scaffold.Tree{model.TestRunAnswerFile: {Data: []byte(t.Request + "\n")},
		model.TestRunReportFile: {Data: append(data, '\n')}}
	for _, a := range artifacts {
		files[model.TestRunArtifactsDir+a.Path] = scaffold.File{Data: a.Data}
	}
	branch := model.TestRunBranch(t.Lease)
	head, err := git.host.BranchCommit(ctx, git.repo, branch)
	if err != nil {
		return err
	}
	if head == t.Request {
		base, err := git.host.BranchCommit(ctx, git.repo, ports.DefaultBranch)
		if err != nil {
			return err
		}
		author := ports.Signature{Name: scaffold.AuthorName, Email: scaffold.AuthorEmail, When: r.now()}
		sha, err := git.host.PushBranch(ctx, git.repo, base, branch, files, nil,
			fmt.Sprintf("theos-env: the result of test run %s", t.ID), author)
		if err != nil {
			return err
		}
		t.Answer = sha
	}
	t.Result, t.Outcome = report.Result, model.TestRunOutcome(report)
	if report.Result == model.TestRunRefused {
		t.State = model.TestRunRejected
		return nil
	}
	t.State, t.CostMilliUSD = model.TestRunReported, report.CostMilliUSD
	if comment := model.TestRunComment(report); len(comment) <= 8192 {
		t.Comment = comment
	} else {
		t.Comment = comment[:8000] + "\n(cut: the whole report is report.json on the run's commit)\n"
	}
	logf.FromContext(ctx).Info("Reported a test run", "testRun", t.ID, "result", report.Result,
		"answer", t.Answer)
	return nil
}

// commentTestRun posts t's outcome on its holder's pull request once it has
// one; a holder that ended without one, or is gone, gets none.
func (r *TestEnvironmentReconciler) commentTestRun(ctx context.Context, ns string, t *c4v1beta1.TestEnvironmentTestRun) error {
	var task c4v1beta1.Task
	err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: t.Holder}, &task)
	switch {
	case apierrors.IsNotFound(err):
		t.Comment = ""
		return nil
	case err != nil:
		return err
	case task.Status.PullRequest == 0 && isFinal(task.Status.Phase):
		t.Comment = ""
		return nil
	case task.Status.PullRequest == 0:
		return nil // the comment waits for the pull request
	}
	git, err := r.testRunGit(ctx, ns, t.Container)
	if err != nil {
		return err
	}
	if err := git.host.CommentPullRequest(ctx, git.repo, int(task.Status.PullRequest), t.Comment); err != nil {
		return err
	}
	t.Comment = ""
	logf.FromContext(ctx).Info("Commented a test run on its pull request", "testRun", t.ID,
		"pullRequest", task.Status.PullRequest)
	return nil
}

// maxTestRuns is how many test runs a status keeps, as its schema allows.
const maxTestRuns = 32

// forgetTestRuns drops the runs of leases no longer live once their
// outcome is commented, deleting each such lease's branch, and keeps the
// newest maxTestRuns.
func (r *TestEnvironmentReconciler) forgetTestRuns(ctx context.Context, ns string, status *c4v1beta1.TestEnvironmentStatus,
	live []ports.Lease) {
	isLive := func(id string) bool { return slices.ContainsFunc(live, func(l ports.Lease) bool { return l.ID == id }) }
	gone := map[string]string{} // lease → Container
	var kept []c4v1beta1.TestEnvironmentTestRun
	for _, t := range status.TestRuns {
		if !isLive(t.Lease) && t.Comment == "" {
			gone[t.Lease] = t.Container
			continue
		}
		kept = append(kept, t)
	}
	for _, t := range kept {
		delete(gone, t.Lease) // a run of it still waits for its comment
	}
	for lease, container := range gone {
		git, err := r.testRunGit(ctx, ns, container)
		if err == nil {
			err = git.host.DeleteBranch(ctx, git.repo, model.TestRunBranch(lease))
		}
		if err != nil {
			logf.FromContext(ctx).Error(err, "Failed to delete a returned lease's test-run branch", "lease", lease)
		}
	}
	if len(kept) > maxTestRuns {
		slices.SortStableFunc(kept, func(a, b c4v1beta1.TestEnvironmentTestRun) int {
			return b.Since.Compare(a.Since.Time)
		})
		kept = kept[:maxTestRuns]
	}
	status.TestRuns = kept
}

// testRunGit is Container container's Git host and repository.
func (r *TestEnvironmentReconciler) testRunGit(ctx context.Context, ns, container string) (testRunGit, error) {
	if r.Git == nil {
		return testRunGit{}, errors.New("the operator has no Git client for test runs")
	}
	var c c4v1beta1.Container
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: container}, &c); err != nil {
		return testRunGit{}, err
	}
	host, err := providerFor(ctx, r.Client, r.reader(), r.Git, ns, c.Spec.GitProviderRef.Name)
	if err != nil {
		return testRunGit{}, err
	}
	return testRunGit{host: host, repo: host.Repository(c.Name)}, nil
}

// leaseTokenOf is lease id's token in task's token Secret, "" when it has
// none: only the Task reconciler makes tokens.
func (r *TestEnvironmentReconciler) leaseTokenOf(ctx context.Context, task *c4v1beta1.Task, id string) (string, error) {
	var secret corev1.Secret
	err := r.reader().Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: envTokenSecret(task)}, &secret)
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(secret.Data[id]), nil
}
