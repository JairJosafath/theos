/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// checksBeforeRelease reports whether the Container's repository runs its full
// check only before a release, as its Standard files were last rendered: the
// gate waits only for a full check the repository has.
func checksBeforeRelease(container *c4v1beta1.Container) bool {
	return container != nil && container.Status.Standard != nil &&
		container.Status.Standard.Checks == c4v1beta1.ChecksBeforeRelease
}

// fullCheck runs release step's full check before its tag: it pushes the
// branch release-check/<tag> at the commit the tag will name (a branch already
// there is reused, one at another commit moved), reads the runs that push
// started, and files the issue to fix it in the release when one failed, once
// per commit. A check cut off (cancelled, or its runner lost) runs its failed
// runs again first, up to model.MaxFullCheckReruns times (#2028). It returns
// step when the check passed, so the Crew tags it, else the step that holds
// the release instead: FullCheckRunning, FullCheckFailed or NoFullCheck.
// status.fullCheck records the check.
func (r *CrewReconciler) fullCheck(ctx context.Context, crew *c4v1beta1.Crew, status *c4v1beta1.CrewStatus,
	provider interface {
		ports.GitHost
		ports.Issues
		ports.CI
	}, repo ports.Repository, step model.ReleaseStep,
	release *model.ReleaseMilestone) (model.ReleaseStep, error) {
	log := logf.FromContext(ctx)
	branch := model.FullCheckBranch(step.Tag)
	check := status.FullCheck
	if check == nil || check.Tag != step.Tag || check.Commit != step.Commit {
		at, err := provider.BranchCommit(ctx, repo, branch)
		if err != nil {
			return step, fmt.Errorf("reading %s: %w", branch, err)
		}
		if at != step.Commit {
			if at != "" {
				if err := provider.DeleteBranch(ctx, repo, branch); err != nil {
					return step, fmt.Errorf("moving %s from %s: %w", branch, at, err)
				}
			}
			if err := provider.CreateBranch(ctx, repo, branch, step.Commit); err != nil {
				return step, fmt.Errorf("pushing %s: %w", branch, err)
			}
		}
		check = &c4v1beta1.CrewFullCheck{Tag: step.Tag, Commit: step.Commit, Time: metav1.NewTime(r.now())}
		status.FullCheck = check
		log.Info("Started the full check of a release", "tag", step.Tag, "commit", step.Commit, "branch", branch,
			"reused", at == step.Commit)
		r.event(crew, corev1.EventTypeNormal, model.FullCheckRunning, "TagRelease",
			fmt.Sprintf("Pushed %s at %s to run the full check of %s before tagging it", branch, step.Commit, step.Tag))
	}
	runs, err := fullCheckRuns(ctx, provider, repo, step.Tag, step.Commit)
	if err != nil {
		return step, err
	}
	check.Run, _ = model.ReleaseRunResult(runs)
	action, run := model.FullCheckGate(runs, check.Time.Time, r.now())
	check.RunURL = run.URL
	held := func(reason string) model.ReleaseStep {
		return model.ReleaseStep{Tag: step.Tag, Reason: reason,
			Held: model.FullCheckMessage(reason, step.Tag, step.Commit, run, int(check.Issue))}
	}
	switch action {
	case model.FullCheckTag:
		log.Info("Full check of a release passed", "tag", step.Tag, "commit", step.Commit, "url", run.URL)
		return step, nil
	case model.FullCheckLeave:
		return held(model.NoFullCheck), nil
	case model.FullCheckFile:
		// A person's re-run that failed holds the release again with its issue.
		check.Rerun = false
		if check.Issue != 0 || (release != nil && model.FullCheckFiled(*release, step.Commit)) {
			if err := r.fileFullCheckFix(ctx, provider, repo, step, release, run, check, false); err != nil {
				return step, err
			}
			return held(model.FullCheckFailed), nil
		}
		cutOff := fullCheckCutOff(ctx, provider, repo, run)
		if model.FullCheckFailure(cutOff, int(check.Reruns)) == model.FullCheckRerun {
			rerun, err := rerunFullCheck(ctx, provider, repo, runs)
			if err != nil {
				return step, fmt.Errorf("re-running the full check that was cut off: %w", err)
			}
			check.Reruns++
			check.Run = model.RunRunning
			log.Info("Re-ran the full check of a release that was cut off", "tag", step.Tag, "commit", step.Commit,
				"workflows", rerun, "url", run.URL)
			r.event(crew, corev1.EventTypeNormal, model.FullCheckRunning, "TagRelease",
				fmt.Sprintf("The full check of %s at %s was cut off (%s ended %s); re-ran %s", step.Tag, step.Commit,
					run.Workflow, run.Conclusion, strings.Join(rerun, ", ")))
			return held(model.FullCheckRunning), nil
		}
		if err := r.fileFullCheckFix(ctx, provider, repo, step, release, run, check, cutOff); err != nil {
			return step, err
		}
		return held(model.FullCheckFailed), nil
	}
	return held(model.FullCheckRunning), nil
}

// fullCheckRuns reads the runs at commit that the push of release tag's full
// check branch started, newest first.
func fullCheckRuns(ctx context.Context, provider ports.CI, repo ports.Repository, tag, commit string) (
	[]model.ReleaseRun, error) {
	found, err := provider.CommitRuns(ctx, repo, commit)
	if err != nil {
		return nil, fmt.Errorf("reading the full check's runs: %w", err)
	}
	var runs []model.ReleaseRun
	for _, f := range found {
		if f.Ref == "refs/heads/"+model.FullCheckBranch(tag) {
			runs = append(runs, model.ReleaseRun{ID: f.ID, Workflow: f.Workflow, Status: f.Status,
				Conclusion: f.Conclusion, URL: f.URL})
		}
	}
	return runs, nil
}

// rerunFullCheck runs the failed jobs of the full check's failed runs again,
// and returns the workflows it re-ran, with the first refusal of the Git
// host.
func rerunFullCheck(ctx context.Context, provider ports.CI, repo ports.Repository, runs []model.ReleaseRun) (
	rerun []string, err error) {
	for _, failed := range model.FailedReleaseRuns(runs) {
		if rerr := provider.RerunFailedJobs(ctx, repo, failed.ID); rerr != nil {
			err = cmp.Or(err, rerr)
			continue
		}
		rerun = append(rerun, failed.Workflow)
	}
	return rerun, err
}

// fullCheckCutOff reads whether the failed full check run was cut off
// (model.FullCheckCutOff): a cancelled run is, else it reads the run's failed
// jobs and their logs. Jobs or a log it can't read count as the check's own
// failure.
func fullCheckCutOff(ctx context.Context, provider ports.CI, repo ports.Repository, run model.ReleaseRun) bool {
	if model.FullCheckCutOff(run, nil) {
		return true
	}
	found, err := provider.RunJobs(ctx, repo, run.ID)
	if err != nil {
		logf.FromContext(ctx).Info("Could not read the jobs of a failed full check", "url", run.URL,
			"error", err.Error())
		return false
	}
	var jobs []model.FullCheckJob
	for _, j := range found {
		job := model.FullCheckJob{Conclusion: j.Conclusion}
		if j.Status == "completed" && j.Conclusion != "success" && j.Conclusion != "skipped" {
			// CheckLog reads the job's ID after /jobs/, as a check status links it.
			log, err := provider.CheckLog(ctx, repo, fmt.Sprintf("%s/jobs/%d", run.URL, j.ID))
			switch {
			case errors.Is(err, ports.ErrNoCILog):
				job.NoLog = true
			case err != nil:
				continue
			default:
				job.Log = log
			}
		}
		jobs = append(jobs, job)
	}
	return model.FullCheckCutOff(run, jobs)
}

// fileFullCheckFix files the issue to fix release step's full check, failed
// in run, cut off when cutOff says so, unless the release has it for this
// commit already, and records it.
func (r *CrewReconciler) fileFullCheckFix(ctx context.Context, provider ports.Issues,
	repo ports.Repository, step model.ReleaseStep, release *model.ReleaseMilestone, run model.ReleaseRun,
	check *c4v1beta1.CrewFullCheck, cutOff bool) error {
	if release != nil && model.FullCheckFiled(*release, step.Commit) {
		for _, f := range release.FullChecks {
			if f.Commit == step.Commit {
				check.Issue = int32(f.Issue)
			}
		}
		return nil
	}
	bug := model.BugLabel()
	labels := append(issueLabels(),
		ports.Label{Name: bug.Name, Color: bug.Color, Description: bug.Description, Exclusive: bug.Exclusive})
	if err := provider.EnsureLabels(ctx, repo, labels); err != nil {
		return err
	}
	title, body := model.FullCheckIssue(step.Tag, step.Commit, run, cutOff, int(check.Reruns))
	filed, err := provider.CreateIssue(ctx, repo, title, body, []string{model.LabelBug, model.LabelReady},
		step.Milestone)
	if err != nil {
		return fmt.Errorf("filing the full check's fix: %w", err)
	}
	logf.FromContext(ctx).Info("Filed the fix of a release's full check", "tag", step.Tag, "commit", step.Commit,
		"issue", filed.Number, "url", run.URL, "cutOff", cutOff)
	check.Issue = int32(filed.Number)
	return nil
}
