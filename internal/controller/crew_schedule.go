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
	"fmt"
	"slices"
	"strings"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// ConditionScheduled says whether a Crew's schedules can be used and their
// due issues were filed, and why not.
const ConditionScheduled = "Scheduled"

// schedule files the issue of each of the Crew's schedules that came due
// (docs/design.md, "Schedules", #63), keeps status.schedules and the
// Scheduled condition, and returns how long until the next one is due, 0
// when the Crew has none. The Crew then picks the issue up as any ready one.
// A schedule whose last issue is still open passes its due time over, and a
// paused Crew files nothing and passes nothing over.
func (r *CrewReconciler) schedule(ctx context.Context, crew *c4v1beta1.Crew, status *c4v1beta1.CrewStatus,
	provider ports.Issues, repo ports.Repository) time.Duration {
	if len(crew.Spec.Schedules) == 0 {
		status.Schedules = nil
		apimeta.RemoveStatusCondition(&status.Conditions, ConditionScheduled)
		return 0
	}
	log := logf.FromContext(ctx)
	now := r.now()
	gitCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	// The open issues schedules filed, read once and only when one is due.
	var open []ports.Issue
	listed := false
	openIssue := func(name string) (bool, error) {
		if !listed {
			issues, err := provider.ListIssues(gitCtx, repo, model.LabelSchedule)
			if err != nil {
				return false, fmt.Errorf("could not read the open scheduled issues: %w", err)
			}
			open, listed = issues, true
		}
		return slices.ContainsFunc(open, func(i ports.Issue) bool { return model.HasScheduleMarker(i.Body, name) }), nil
	}

	var (
		kept            []c4v1beta1.CrewScheduleStatus
		invalid, failed []string
		next            time.Time
	)
	for _, spec := range crew.Spec.Schedules {
		s := model.Schedule{Name: spec.Name, Cron: spec.Cron, TimeZone: spec.TimeZone, Goal: spec.Goal}
		st := c4v1beta1.CrewScheduleStatus{Name: spec.Name, Since: metav1.NewTime(now)}
		if i := slices.IndexFunc(status.Schedules, func(o c4v1beta1.CrewScheduleStatus) bool { return o.Name == spec.Name }); i >= 0 {
			st = status.Schedules[i]
		}
		facts := model.ScheduleFacts{Schedule: s, Since: st.Since.Time}
		step, err := model.NextScheduleStep(facts, now)
		if err != nil {
			invalid = append(invalid, fmt.Sprintf("schedule %s: %s", spec.Name, err))
			st.Next = nil
			kept = append(kept, st)
			continue
		}
		if !step.Due.IsZero() && !crew.Spec.Paused {
			filed, err := r.fileScheduled(gitCtx, &st, s, step.Due, now, provider, repo, openIssue)
			switch {
			case err != nil:
				log.Error(err, "Failed to file a schedule's issue", "schedule", spec.Name)
				failed = append(failed, fmt.Sprintf("schedule %s: %s", spec.Name, err))
			case filed:
				log.Info("Filed a schedule's issue", "schedule", spec.Name, "issue", st.LastIssue, "due", step.Due)
			}
		}
		if !step.Next.IsZero() {
			at := metav1.NewTime(step.Next)
			st.Next = &at
			if next.IsZero() || step.Next.Before(next) {
				next = step.Next
			}
		}
		kept = append(kept, st)
	}
	status.Schedules = kept

	switch {
	case len(invalid) > 0:
		setCondition(&status.Conditions, crew.Generation, ConditionScheduled, metav1.ConditionFalse,
			model.ReasonInvalidSchedule, strings.Join(invalid, "; "))
	case len(failed) > 0:
		setCondition(&status.Conditions, crew.Generation, ConditionScheduled, metav1.ConditionFalse,
			model.ReasonScheduleNotFiled, strings.Join(failed, "; "))
	default:
		message := fmt.Sprintf("%d schedules", len(kept))
		if len(kept) == 1 {
			message = "1 schedule"
		}
		if !next.IsZero() {
			message += ", the next due at " + next.UTC().Format(time.RFC3339)
		}
		if crew.Spec.Paused {
			message += "; the Crew is paused and files none"
		}
		setCondition(&status.Conditions, crew.Generation, ConditionScheduled, metav1.ConditionTrue,
			model.ReasonScheduled, message)
	}
	if next.IsZero() {
		return 0
	}
	// A minute at least: a clock a second short of the time would spin.
	return max(next.Sub(now), time.Minute)
}

// fileScheduled files the issue of schedule s, due at due, unless an issue of
// its is still open, and moves st on either way; filed says it filed one. A
// failure leaves st as it was, so the next poll tries again; an issue filed
// whose status write was lost is found open then, and no second one is filed.
func (r *CrewReconciler) fileScheduled(ctx context.Context, st *c4v1beta1.CrewScheduleStatus, s model.Schedule,
	due, now time.Time, provider ports.Issues, repo ports.Repository,
	openIssue func(string) (bool, error)) (filed bool, err error) {
	isOpen, err := openIssue(s.Name)
	if err != nil {
		return false, err
	}
	if isOpen {
		st.Since = metav1.NewTime(now)
		return false, nil
	}
	l := model.ScheduleLabel()
	labels := append(issueLabels(),
		ports.Label{Name: l.Name, Color: l.Color, Description: l.Description, Exclusive: l.Exclusive})
	if err := provider.EnsureLabels(ctx, repo, labels); err != nil {
		return false, fmt.Errorf("labels: %w", err)
	}
	release, err := releaseTakingWork(ctx, provider, repo)
	if err != nil {
		return false, err
	}
	title, body := model.ScheduleIssue(s, due)
	issue, err := provider.CreateIssue(ctx, repo, title, body, []string{model.LabelSchedule, model.LabelReady}, release)
	if err != nil {
		return false, fmt.Errorf("could not file the issue: %w", err)
	}
	at := metav1.NewTime(now)
	st.Since, st.LastFiled, st.LastIssue = at, &at, int32(issue.Number)
	return true, nil
}
