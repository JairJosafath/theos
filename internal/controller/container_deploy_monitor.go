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
	"time"

	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// reasonDeployBugFiled is the Event of a bug filed for a deploy.
const reasonDeployBugFiled = "DeployBugFiled"

// monitorDeploys files the bug of each entry whose health turned Degraded or
// whose deploy failed (model.DeployMonitorDue) and records the issue in the
// entry's status.issue. Every entry keeps the issue its previous status
// recorded, through a recovery, so a state of a version files once; the
// marker in the Git host's issues covers a status that was lost. A failure is
// logged and tried again on the next pass.
func (r *ContainerReconciler) monitorDeploys(ctx context.Context, container *c4v1beta1.Container,
	previous, deploys []c4v1beta1.ContainerDeployStatus) {
	log := logf.FromContext(ctx)
	for i := range deploys {
		d := &deploys[i]
		for _, p := range previous {
			if p.Target == d.Target {
				d.Issue = p.Issue
			}
		}
		var filed *model.DeployMonitorFiled
		if d.Issue != nil {
			filed = &model.DeployMonitorFiled{Version: d.Issue.Version, State: d.Issue.State, Number: int(d.Issue.Number)}
		}
		report := model.DeployReport{Target: d.Target, Version: d.Version, WantedVersion: d.WantedVersion,
			Phase: d.Phase, Health: d.Health, URL: d.URL, Image: d.Image, Reason: d.Reason, Message: d.Message}
		version, state, due := model.DeployMonitorDue(report, filed)
		if !due {
			continue
		}
		provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, container.Namespace,
			container.Spec.GitProviderRef.Name)
		if err != nil {
			log.Error(err, "Failed to reach the Git host to file a bug for a deploy", "target", d.Target)
			continue
		}
		issue, isNew, err := fileDeployBug(ctx, provider, provider.Repository(container.Name), report, version, state,
			time.Now())
		if err != nil {
			log.Error(err, "Failed to file a bug for a deploy", "target", d.Target, "version", version, "state", state)
			continue
		}
		d.Issue = &c4v1beta1.ContainerDeployIssue{Number: int32(issue.Number), Version: version, State: state,
			URL: issue.URL}
		if !isNew {
			continue
		}
		log.Info("Filed a bug for a deploy", "target", d.Target, "version", version, "state", state, "issue", issue.URL)
		if r.Recorder != nil {
			r.Recorder.Eventf(container, nil, corev1.EventTypeWarning, reasonDeployBugFiled, "Monitor",
				"Filed %s: version %s is %s at %s", issue.URL, version, state, d.Target)
		}
	}
}

// fileDeployBug returns the open bug of the deploy in repo, found by its
// marker, or files it there as kind/bug and status/proposed. isNew says it was
// filed now.
func fileDeployBug(ctx context.Context, provider ports.Issues, repo ports.Repository, report model.DeployReport,
	version, state string, at time.Time) (ports.Issue, bool, error) {
	labels := monitorIssueLabels()
	if err := provider.EnsureLabels(ctx, repo, labels); err != nil {
		return ports.Issue{}, false, err
	}
	open, err := provider.ListIssues(ctx, repo, model.LabelBug)
	if err != nil {
		return ports.Issue{}, false, err
	}
	for _, issue := range open {
		if model.HasDeployMonitorMarker(issue.Body, report.Target, version, state) {
			return issue, false, nil
		}
	}
	title, body := model.DeployMonitorIssue(report, version, state, at)
	issue, err := provider.CreateIssue(ctx, repo, title, body, model.AlertIssueLabels(), 0)
	return issue, err == nil, err
}

// monitorIssueLabels are the labels the monitor's bugs need in their
// repository: the status labels, kind/bug and source/monitor
// (docs/plans/alerts.md, decision 12).
func monitorIssueLabels() []ports.Label {
	labels := issueLabels()
	for _, l := range []model.IssueLabel{model.BugLabel(), model.MonitorLabel()} {
		labels = append(labels, ports.Label{Name: l.Name, Color: l.Color, Description: l.Description,
			Exclusive: l.Exclusive})
	}
	return labels
}
