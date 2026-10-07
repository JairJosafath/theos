/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// A Brief's failed issues (plans/brief-failed-issues.md): the issues of the
// Containers it made that were handed back to a person, each with why, for
// the person to answer in a prompt.

const (
	// eventIssueFailed says an issue of the Brief's Containers was handed back.
	eventIssueFailed = "IssueFailed"
	// reasonIssuesWaiting is IssuesFailed's reason while failed issues wait.
	reasonIssuesWaiting = "IssuesWaiting"
)

// failedIssue is an open issue of Container name that was handed back to a
// person, with why, decided from the Container's Tasks.
func failedIssue(name string, number int, title, url, body string, tasks []model.IssueTask) model.FailedIssue {
	kind, reason, tries := model.FailedReason(number, body, tasks)
	return model.FailedIssue{Container: name, Number: number, Title: title, URL: url, Body: body, Kind: kind,
		Reason: reason, Tries: tries}
}

// issueTasks are the namespace's Tasks on an issue, by Container, as
// model.FailedReason sees them.
func issueTasks(tasks []c4v1alpha1.Task) map[string][]model.IssueTask {
	out := map[string][]model.IssueTask{}
	for _, t := range tasks {
		if t.Spec.Issue == 0 || t.Spec.ContainerRef.Name == "" {
			continue
		}
		it := model.IssueTask{Name: t.Name, Issue: int(t.Spec.Issue), Merged: t.Status.Phase == c4v1alpha1.TaskMerged,
			Failed: t.Status.Phase == c4v1alpha1.TaskFailed, Created: t.CreationTimestamp.Time}
		if cond := meta.FindStatusCondition(t.Status.Conditions, ConditionSucceeded); cond != nil {
			it.Message = cond.Message
		}
		out[t.Spec.ContainerRef.Name] = append(out[t.Spec.ContainerRef.Name], it)
	}
	return out
}

// recordFailed sets the Brief's failed issues to the ones found, by Container
// and number; the status holds the first model.MaxFailedIssues. When a
// repository couldn't be read (partial), found has only the ones the status
// listed for it, so the count doesn't go down. Each listed issue the status
// didn't list before gets an IssueFailed Event, and IssuesFailed says the
// first and how many more wait.
func (r *BriefReconciler) recordFailed(ctx context.Context, brief *c4v1alpha1.Brief, found []model.FailedIssue,
	partial bool) {
	found = slices.Clone(found)
	slices.SortFunc(found, func(a, b model.FailedIssue) int {
		if c := strings.Compare(a.Container, b.Container); c != 0 {
			return c
		}
		return a.Number - b.Number
	})
	count := int32(len(found))
	found = found[:min(len(found), model.MaxFailedIssues)]
	status := &brief.Status
	listed := make([]c4v1alpha1.BriefFailedIssue, 0, len(found))
	for _, f := range found {
		listed = append(listed, c4v1alpha1.BriefFailedIssue{Container: f.Container, Number: int32(f.Number),
			Title: model.Clip(f.Title, 255), URL: f.URL, Kind: f.Kind, Reason: model.Clip(f.Reason, 1000),
			Tries: int32(f.Tries)})
		if !slices.ContainsFunc(status.Failed, func(s c4v1alpha1.BriefFailedIssue) bool {
			return s.Container == f.Container && int(s.Number) == f.Number
		}) {
			logf.FromContext(ctx).Info("An issue was handed back to the person", "container", f.Container,
				"number", f.Number, "kind", f.Kind)
			r.event(brief, corev1.EventTypeWarning, eventIssueFailed, "ListFailedIssue",
				model.FailedMessage([]model.FailedIssue{f}, 1))
		}
	}
	if partial {
		count = max(count, status.FailedWaiting)
	}
	if status.FailedWaiting != count {
		logf.FromContext(ctx).Info("Failed issues wait for the person", "count", count)
	}
	status.FailedWaiting = count
	status.Failed = nil
	if len(listed) > 0 {
		status.Failed = listed
	}
	switch {
	case count > 0:
		setCondition(&status.Conditions, brief.Generation, c4v1alpha1.ConditionIssuesFailed, metav1.ConditionTrue,
			reasonIssuesWaiting, model.FailedMessage(found, int(count)))
	case meta.FindStatusCondition(status.Conditions, c4v1alpha1.ConditionIssuesFailed) != nil:
		setCondition(&status.Conditions, brief.Generation, c4v1alpha1.ConditionIssuesFailed, metav1.ConditionFalse,
			c4v1alpha1.ReasonResolved, "No failed issue waits for the person")
	}
}

// lastFailed are the failed issues the Brief last saw in Container name,
// without their bodies, which the status doesn't keep.
func lastFailed(brief *c4v1alpha1.Brief, name string) []model.FailedIssue {
	var out []model.FailedIssue
	for _, f := range brief.Status.Failed {
		if f.Container == name {
			out = append(out, model.FailedIssue{Container: f.Container, Number: int(f.Number), Title: f.Title,
				URL: f.URL, Kind: f.Kind, Reason: f.Reason, Tries: int(f.Tries)})
		}
	}
	return out
}
