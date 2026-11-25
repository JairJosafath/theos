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

package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// CrewInput is what a Crew tick is told rather than reads: the Container
// and its repository, and the Crew's own facts.
type CrewInput struct {
	Container string
	Repo      ports.Repository
	// Facts are the Crew's own: Paused, Parallel, ContainerReady,
	// Unprotected, Cadence and Routes. Their Tasks and Issues are the
	// tick's to read.
	Facts model.CrewFacts
	// Labels are the labels the Crew keeps in the repository, made before
	// the ready issues are read.
	Labels []ports.Label
	// Specs means the Crew has a spec writer: the issues that need a spec
	// are read beside the ready ones.
	Specs bool
	// Before runs once the issues are read, before the decision, with the
	// facts the decision takes: what the runtime adds to them, as the
	// operator's releases add the prepare issue they file. nil adds
	// nothing. It doesn't run while the Container isn't Ready.
	Before func(ctx context.Context, facts *model.CrewFacts)
}

// CrewResult is what a Crew tick did.
type CrewResult struct {
	// Step is the Crew's decision. Each of its Start was started.
	Step model.CrewStep
	// HandBack are the hand-backs of NotReady issues that failed. The tick
	// goes on past them; the next one hands the issue back again while it
	// is still labelled ready.
	HandBack []IssueError
}

// IssueError is a write to an issue that failed.
type IssueError struct {
	Issue int
	Err   error
}

// CrewTick moves a Crew one step: it reads the Container's Tasks from the
// runtime's store and, once the Container is Ready, the issues the Crew
// picks from, decides with model.NextCrewStep, hands the issues agents
// must not take back to a person and starts a Task for each issue picked.
// host may be nil while the Container isn't Ready: nothing is read then.
// A failure to read the host is a *HostError.
func CrewTick(ctx context.Context, rt ports.Runtime, host ports.Issues, in CrewInput) (CrewResult, error) {
	store := rt.Store()
	facts := in.Facts
	tasks, err := store.Tasks(ctx, in.Container)
	if err != nil {
		return CrewResult{}, err
	}
	facts.Tasks, facts.Issues = tasks, nil
	if facts.ContainerReady {
		hostCtx, cancel := context.WithTimeout(ctx, HostTimeout)
		err := readIssues(hostCtx, host, in, &facts)
		cancel()
		if err != nil {
			return CrewResult{}, &HostError{Err: err}
		}
		if in.Before != nil {
			in.Before(ctx, &facts)
		}
	}
	res := CrewResult{Step: model.NextCrewStep(in.Container, facts)}
	if len(res.Step.NotReady) > 0 {
		res.HandBack = handBack(ctx, host, in.Repo, res.Step.NotReady)
	}
	for _, s := range res.Step.Start {
		if err := store.Start(ctx, in.Container, s); err != nil {
			return res, fmt.Errorf("start Task %s for issue #%d: %w", s.Task, s.Issue, err)
		}
	}
	return res, nil
}

// readIssues reads what a Crew picks from: the ready issues, with a spec
// writer those that need a spec, and the releases of its Tasks' issues.
func readIssues(ctx context.Context, host ports.Issues, in CrewInput, facts *model.CrewFacts) error {
	if err := host.EnsureLabels(ctx, in.Repo, in.Labels); err != nil {
		return err
	}
	var err error
	if facts.Issues, err = labelledIssues(ctx, host, in.Repo, model.LabelReady); err != nil {
		return err
	}
	if in.Specs {
		spec, err := labelledIssues(ctx, host, in.Repo, model.LabelNeedsSpec)
		if err != nil {
			return err
		}
		for i := range spec {
			spec[i].Spec = true
		}
		facts.Issues = append(facts.Issues, spec...)
	}
	return taskReleases(ctx, host, in.Repo, facts.Tasks, facts.Issues)
}

// labelledIssues reads the open issues labelled label, as a Crew picks them:
// what blocks each, and which are Standard changes, urgent or a release's.
func labelledIssues(ctx context.Context, host ports.Issues, repo ports.Repository,
	label string) ([]model.Issue, error) {
	issues, err := host.ListIssues(ctx, repo, label)
	if err != nil {
		return nil, err
	}
	out := make([]model.Issue, 0, len(issues))
	for _, i := range issues {
		blockers, err := host.OpenBlockers(ctx, repo, i.Number)
		if err != nil {
			return nil, fmt.Errorf("issue #%d: %w", i.Number, err)
		}
		out = append(out, model.Issue{Number: i.Number, Title: i.Title, Body: i.Body, OpenBlockers: blockers,
			StandardChange: slices.Contains(i.Labels, model.LabelStandardChange), Release: issueRelease(i),
			Urgent: slices.Contains(i.Labels, model.LabelUrgent), Prepare: slices.Contains(i.Labels, model.LabelRelease),
			Labels: slices.Clone(i.Labels)})
	}
	return out, nil
}

// taskReleases sets the release of each active Task's issue, so that a release
// with work in progress holds back the later ones, and whether it is a
// release's prepare issue, which runs alone.
func taskReleases(ctx context.Context, host ports.Issues, repo ports.Repository,
	tasks []model.CrewTask, ready []model.Issue) error {
	for i, t := range tasks {
		if t.Final || t.Issue == 0 {
			continue
		}
		// A Task created a moment ago may still find its issue labelled ready.
		if j := slices.IndexFunc(ready, func(issue model.Issue) bool { return issue.Number == t.Issue }); j >= 0 {
			tasks[i].Release, tasks[i].Prepare = ready[j].Release, ready[j].Prepare
			continue
		}
		issue, err := host.GetIssue(ctx, repo, t.Issue)
		if err != nil {
			return fmt.Errorf("issue #%d of Task %s: %w", t.Issue, t.Name, err)
		}
		tasks[i].Release, tasks[i].Prepare = issueRelease(issue), slices.Contains(issue.Labels, model.LabelRelease)
	}
	return nil
}

// issueRelease is an issue's milestone as a release; nil when it has no due date.
func issueRelease(issue ports.Issue) *model.Release {
	if issue.MilestoneDue.IsZero() {
		return nil
	}
	return &model.Release{Title: issue.MilestoneTitle, Due: issue.MilestoneDue}
}

// handBack gives the issues agents must not take back to a person,
// status/needs-human with the reason, out of status/ready, or
// status/needs-spec for one whose spec they must not write. It returns the
// writes that failed.
func handBack(ctx context.Context, host ports.Issues, repo ports.Repository,
	rejections []model.IssueRejection) []IssueError {
	hostCtx, cancel := context.WithTimeout(ctx, HostTimeout)
	defer cancel()
	var failed []IssueError
	for _, n := range rejections {
		was := model.LabelReady
		if n.Spec {
			was = model.LabelNeedsSpec
		}
		err := errors.Join(
			host.AddIssueLabel(hostCtx, repo, n.Issue, model.LabelNeedsHuman),
			// The exclusive scope drops it too, but not where the labels came
			// over non-exclusive from a migration (#2039).
			host.RemoveIssueLabel(hostCtx, repo, n.Issue, was),
			host.CommentIssue(hostCtx, repo, n.Issue, n.Reason),
		)
		if err != nil {
			failed = append(failed, IssueError{Issue: n.Issue, Err: err})
		}
	}
	return failed
}
