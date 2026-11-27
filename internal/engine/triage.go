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
	"strings"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// TriageInput is what a Crew's triage tick is told rather than reads: the
// Container and its repository, the Crew's triager and its state.
type TriageInput struct {
	Container string
	Repo      ports.Repository
	// Triager is the Agent the Crew names to triage; empty for none, when
	// the tick only reads the Triage Tasks at work.
	Triager string
	Paused  bool
	// ContainerReady means the repository is scaffolded and its main
	// protected: no agent runs on any other, the triager included.
	ContainerReady bool
}

// TriageTick moves a Crew's triage one step (docs/design.md, "Triage"):
// it reads the Container's Triage Tasks from the runtime's store, which
// must keep them (ports.TriageStore), and, with a triager on a Container
// that is Ready, the open issues labelled status/proposed; decides with model.NextTriage
// and starts the Triage Task it picks. host may be nil while the Container
// isn't Ready. A failure to read the host is a *HostError.
func TriageTick(ctx context.Context, rt ports.Runtime, host ports.Issues, in TriageInput) (model.TriageStep, error) {
	store, ok := rt.Store().(ports.TriageStore)
	if !ok {
		if in.Triager == "" {
			return model.TriageStep{}, nil
		}
		return model.TriageStep{}, errors.New("the runtime's store keeps no Triage Tasks")
	}
	tasks, err := store.TriageTasks(ctx, in.Container)
	if err != nil {
		return model.TriageStep{}, err
	}
	facts := model.TriageFacts{On: in.Triager != "", Paused: in.Paused, ContainerReady: in.ContainerReady,
		Tasks: tasks}
	if facts.On && facts.ContainerReady {
		hostCtx, cancel := context.WithTimeout(ctx, HostTimeout)
		facts.Proposed, err = ProposedIssues(hostCtx, host, in.Repo)
		cancel()
		if err != nil {
			return model.TriageStep{}, &HostError{Err: err}
		}
	}
	step := model.NextTriage(in.Container, facts)
	if step.Start != nil {
		if err := store.StartTriage(ctx, in.Container, *step.Start, in.Triager); err != nil {
			return step, fmt.Errorf("start Triage Task %s for issue #%d: %w", step.Start.Task, step.Start.Issue, err)
		}
	}
	return step, nil
}

// ProposedIssues reads the open issues labelled status/proposed, a Crew's
// triager's to decide.
func ProposedIssues(ctx context.Context, host ports.Issues, repo ports.Repository) ([]model.ProposedIssue, error) {
	issues, err := host.ListIssues(ctx, repo, model.LabelProposed)
	if err != nil {
		return nil, err
	}
	out := make([]model.ProposedIssue, 0, len(issues))
	for _, i := range issues {
		out = append(out, model.ProposedIssue{Number: i.Number, Milestone: i.MilestoneTitle})
	}
	return out, nil
}

// ApplyTriageActions writes a triage decision's actions to the issue and
// says what it did: the body, the comment, the close, the environments'
// and the risk's labels, then the status label, last, so a write that
// fails before it is tried again on an issue still status/proposed. A host
// that won't close an issue with open dependencies leaves it open, said.
// Both runtimes call it.
func ApplyTriageActions(ctx context.Context, host ports.Issues, repo ports.Repository, number int,
	actions model.TriageActions) (string, error) {
	applied := actions.Applied
	if actions.Body != "" {
		if err := host.EditIssueBody(ctx, repo, number, actions.Body); err != nil {
			return "", err
		}
	}
	if err := host.CommentIssue(ctx, repo, number, actions.Comment); err != nil {
		return "", err
	}
	if actions.Close {
		switch err := host.CloseIssue(ctx, repo, number); {
		case errors.Is(err, ports.ErrOpenDependencies):
			applied = fmt.Sprintf("Left issue #%d open: the Git host won't close it while an issue it depends on is open",
				number)
		case err != nil:
			return "", err
		}
	}
	for _, l := range actions.EnvLabels {
		if err := host.AddIssueLabel(ctx, repo, number, l); err != nil {
			return "", err
		}
	}
	if actions.RiskLabel != "" {
		if err := host.AddIssueLabel(ctx, repo, number, actions.RiskLabel); err != nil {
			return "", err
		}
	}
	if actions.Label != "" {
		if err := host.AddIssueLabel(ctx, repo, number, actions.Label); err != nil {
			return "", err
		}
		// The exclusive scope drops it too, but not where the labels came over
		// non-exclusive from a migration (#2039).
		if err := host.RemoveIssueLabel(ctx, repo, number, model.LabelProposed); err != nil {
			return "", err
		}
	}
	return applied, nil
}

// TriageLabels are the labels a triage decision's actions may put on an
// issue: the factory's, and an env/<name> label for each it names.
func TriageLabels(actions model.TriageActions) []ports.Label {
	labels := hostLabels(append(model.StatusLabels(), model.UrgentLabel(), model.RiskLabel())...)
	for _, l := range actions.EnvLabels {
		labels = append(labels, hostLabels(model.EnvLabel(strings.TrimPrefix(l, model.LabelEnvPrefix)))...)
	}
	return labels
}

// hostLabels are labels as the host's port takes them.
func hostLabels(labels ...model.IssueLabel) []ports.Label {
	out := make([]ports.Label, 0, len(labels))
	for _, l := range labels {
		out = append(out, ports.Label{Name: l.Name, Color: l.Color, Description: l.Description, Exclusive: l.Exclusive})
	}
	return out
}

// reasonTriaged is the reason a Triage Task ends Merged with, its decision
// applied or found made by a person already.
const reasonTriaged = "Triaged"

// triaged applies the triager's decision to the issue as it reads now
// (model.PlanTriage), and ends the Triage Task Merged. A decision that
// can't be applied fails it.
func (t *taskTick) triaged(ctx context.Context, facts model.TaskFacts, step model.TaskStep) (model.TaskStep, error) {
	decision := model.NormalTriage(*facts.Run.Result.Triage)
	hostCtx, cancel := context.WithTimeout(ctx, t.in.hostTimeout())
	defer cancel()
	issue, err := t.host.GetIssue(hostCtx, t.in.Repo, t.in.Issue)
	if err != nil {
		return step, &HostError{Err: err}
	}
	actions := model.PlanTriage(t.in.Task, model.TriageIssue{Number: issue.Number, Body: issue.Body,
		Labels: issue.Labels, Closed: issue.State == stateClosed, Environments: t.in.Environments}, decision)
	if actions.Problem != "" {
		failed := model.TaskStep{Action: model.ActionFail, Feedback: actions.Problem,
			Message: "The triage failed: " + actions.Problem}
		return failed, t.failTriage(ctx, failed)
	}
	applied := actions.Skip
	if applied == "" {
		if err := t.host.EnsureLabels(hostCtx, t.in.Repo, TriageLabels(actions)); err != nil {
			return step, &HostError{Err: err}
		}
		if applied, err = ApplyTriageActions(hostCtx, t.host, t.in.Repo, issue.Number, actions); err != nil {
			return step, &HostError{Err: err}
		}
	}
	t.rec.Summary = decision.Reason
	step.Message = fmt.Sprintf("The triager decided %s: %s", decision.Decision, applied)
	return step, t.end(ctx, model.RoleTriager, model.TaskEnd{Phase: model.TaskMerged, Reason: reasonTriaged,
		Message: step.Message})
}

// failTriage ends the run and fails the Triage Task, and says so on the
// issue, which stays status/proposed: its Crew starts another Triage Task
// until model.MaxIssueAttempts of them failed.
func (t *taskTick) failTriage(ctx context.Context, step model.TaskStep) error {
	why := cmpOr(step.Feedback, step.Message)
	hostCtx, cancel := context.WithTimeout(ctx, t.in.hostTimeout())
	err := t.host.CommentIssue(hostCtx, t.in.Repo, t.in.Issue,
		model.TriageFailedComment(t.in.Task, why, t.in.TriageFailed+1))
	cancel()
	if err != nil {
		t.in.Log.Info("Could not comment on the issue", "issue", t.in.Issue, "error", err.Error())
	}
	p := t.rec.Progress
	p.Feedback = step.Feedback
	t.rec.Progress = p
	return t.end(ctx, model.RoleTriager, model.TaskEnd{Phase: model.TaskFailed, Reason: model.ReasonFailed,
		Message: step.Message})
}
