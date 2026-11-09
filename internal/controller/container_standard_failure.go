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

	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// reasonStandardFailureFiled is the Event of a filed failure issue.
const reasonStandardFailureFiled = "StandardFailureFiled"

// fileFailure files the issue that asks the Container's Crew to fix what fails
// the update's CI, once per target ref (model.StandardFailureDue), and keeps it
// in status.standard.failureIssue. The issue is kind/bug and status/ready, in
// the repository's first release that can still take work, as a bump is. A
// failure is logged and tried again on the next pass.
func (u *standardUpdate) fileFailure(ctx context.Context, gitCtx context.Context, f model.StandardUpdateFacts) {
	s := u.status.Standard
	update := s.Update
	var filed string
	if s.FailureIssue != nil {
		filed = s.FailureIssue.Ref
	}
	if !model.StandardFailureDue(filed, update.Ref) {
		return
	}
	git, ok := u.access(ctx)
	if !ok {
		return
	}
	failure := model.StandardFailure{Standard: s.Name, Ref: update.Ref, Checks: f.Checks, ChecksURL: f.ChecksURL,
		PullRequestURL: update.PullRequestURL, Branch: update.Branch, Commit: update.Commit}
	issue, isNew, err := fileStandardFailure(gitCtx, git.provider, git.provider.Repository(u.container.Name), failure)
	log := logf.FromContext(ctx)
	if err != nil {
		log.Error(err, "Failed to file the issue of a Standard update whose CI failed", "ref", update.Ref)
		return
	}
	s.FailureIssue = &c4v1beta1.StandardFailureIssue{Ref: update.Ref, Number: int32(issue.Number), URL: issue.URL}
	if !isNew {
		return
	}
	log.Info("Filed the issue of a Standard update whose CI failed", "ref", update.Ref, "issue", issue.URL)
	if u.r.Recorder != nil {
		u.r.Recorder.Eventf(u.container, nil, corev1.EventTypeNormal, reasonStandardFailureFiled, "FileFailure",
			"Filed %s for the Crew: the Standard %s %s fails this repository's check", issue.URL, s.Name, update.Ref)
	}
}

// fileStandardFailure returns the open issue of the failure in repo, or files
// it there. isNew says it was filed now.
func fileStandardFailure(ctx context.Context, provider ports.Issues, repo ports.Repository,
	failure model.StandardFailure) (ports.Issue, bool, error) {
	l := model.BugLabel()
	labels := append(issueLabels(),
		ports.Label{Name: l.Name, Color: l.Color, Description: l.Description, Exclusive: l.Exclusive})
	if err := provider.EnsureLabels(ctx, repo, labels); err != nil {
		return ports.Issue{}, false, err
	}
	open, err := provider.ListIssues(ctx, repo, model.LabelBug)
	if err != nil {
		return ports.Issue{}, false, err
	}
	for _, issue := range open {
		if model.HasStandardFailureMarker(issue.Body, failure.Standard, failure.Ref) {
			return issue, false, nil
		}
	}
	release, err := releaseTakingWork(ctx, provider, repo)
	if err != nil {
		return ports.Issue{}, false, err
	}
	title, body := model.StandardFailureIssue(failure)
	issue, err := provider.CreateIssue(ctx, repo, title, body, []string{model.LabelBug, model.LabelReady}, release)
	return issue, err == nil, err
}
