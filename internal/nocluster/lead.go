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

package nocluster

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/engine"
	"github.com/JairJosafath/theos/internal/method"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/ports"
)

// The lead runtime's commands (docs/plans/lead-runtime.md, decision 8):
// next runs the engine's ticks and hands out the open offers; claim,
// heartbeat, release and done post a session's markers. Each command starts
// from the host, so a lead or a session that goes away loses nothing. There
// is no merge: a person merges (#292).

// LeadHost is what the lead runtime reaches on a Container's host: its
// repository, issues and their stamped comments, and its CI.
type LeadHost interface {
	ports.GitHost
	ports.Issues
	ports.CI
	ports.IssueComments
}

// WorkDir is where a claimed Task's clone goes, under the project
// repository, which git ignores.
const WorkDir = "work"

// Offer is an open offer a lead hands to a subagent.
type Offer struct {
	Task      string `json:"task"`
	Container string `json:"container"`
	Issue     int    `json:"issue"`
	Round     int    `json:"round"`
	Role      string `json:"role"`
	// Subagent is the subagent the lead starts with WorkOrder.
	Subagent string `json:"subagent"`
	// WorkOrder is the offer's text, word for word: the issue in it is
	// fenced as data.
	WorkOrder string `json:"workOrder"`
}

// TaskLine is what a Task at work is doing, as next leaves it.
type TaskLine struct {
	Task   string `json:"task"`
	Phase  string `json:"phase"`
	Round  int    `json:"round"`
	Reason string `json:"reason,omitempty"`
	Says   string `json:"says"`
}

// NextResult is what next did: the offers open now, up to each Crew's
// parallel, and every Task at work.
type NextResult struct {
	Offers []Offer    `json:"offers"`
	Tasks  []TaskLine `json:"tasks"`
}

// lead is the lead runtime of one Crew's Container.
type lead struct {
	crew      *c4v1beta1.Crew
	container container
	host      LeadHost
	repo      ports.Repository
	rt        ports.Runtime
	rules     model.ClaimRules
	login     string
}

// leads resolves each Crew of m, sorted by name, on its host, with the
// lead runtime env builds.
func leads(ctx context.Context, env Env, m *Model) ([]lead, error) {
	if env.Runtime == nil {
		return nil, errors.New("this theos has no lead runtime")
	}
	if len(m.Crews) == 0 {
		return nil, fmt.Errorf("%s holds no Crew: the lead hands out a Crew's work, so add one first", SetupFile)
	}
	cs, err := containers(ctx, env, m)
	if err != nil {
		return nil, err
	}
	crews := slices.Clone(m.Crews)
	slices.SortFunc(crews, func(a, b c4v1beta1.Crew) int { return strings.Compare(a.Name, b.Name) })
	out := make([]lead, 0, len(crews))
	for i := range crews {
		c := &crews[i]
		j := slices.IndexFunc(cs, func(x container) bool { return x.obj.Name == c.Spec.ContainerRef.Name })
		if j < 0 {
			return nil, fmt.Errorf("the Crew %s: no Container %s", c.Name, c.Spec.ContainerRef.Name)
		}
		host, ok := cs[j].host.(LeadHost)
		if !ok {
			return nil, fmt.Errorf("the Crew %s: the host of %s lists no issue comments, which the lead runtime's "+
				"markers are", c.Name, cs[j].obj.Name)
		}
		login, err := host.Login(ctx)
		if err != nil {
			return nil, fmt.Errorf("the Crew %s: the credential names no account: %w", c.Name, err)
		}
		rules, err := m.Settings.ClaimRules(login)
		if err != nil {
			return nil, err
		}
		out = append(out, lead{crew: c, container: cs[j], host: host, repo: host.Repository(cs[j].obj.Name),
			rt: env.Runtime(host, cs[j].obj.Name, rules), rules: rules, login: login})
	}
	return out, nil
}

// leadOf is the lead of the Crew whose Container task is a Task of.
func leadOf(ls []lead, task string) (lead, int, error) {
	for _, l := range ls {
		rest, ok := strings.CutPrefix(task, l.container.obj.Name+"-")
		issue, _, _ := strings.Cut(rest, "-")
		if n, err := strconv.Atoi(issue); ok && err == nil && n > 0 {
			return l, n, nil
		}
	}
	return lead{}, 0, fmt.Errorf("%s is no Task of a Container a Crew works", task)
}

// crewLabels are the labels the lead keeps in a Container's repository.
func crewLabels() []ports.Label {
	labels := append(model.StatusLabels(), model.UrgentLabel())
	out := make([]ports.Label, 0, len(labels))
	for _, l := range labels {
		out = append(out, ports.Label{Name: l.Name, Color: l.Color, Description: l.Description, Exclusive: l.Exclusive})
	}
	return out
}

// Next runs each Crew's tick and each of its Tasks', and returns the
// offers open now, each naming its subagent, and every Task at work.
func Next(ctx context.Context, env Env, m *Model) (NextResult, error) {
	ls, err := leads(ctx, env, m)
	if err != nil {
		return NextResult{}, err
	}
	var res NextResult
	for _, l := range ls {
		if err := l.next(ctx, env, m, &res); err != nil {
			return NextResult{}, fmt.Errorf("the Crew %s: %w", l.crew.Name, err)
		}
	}
	return res, nil
}

func (l lead) next(ctx context.Context, env Env, m *Model, res *NextResult) error {
	name := l.container.obj.Name
	commits, err := l.host.History(ctx, l.repo, 1)
	if err != nil && !errors.Is(err, ports.ErrRepositoryNotFound) {
		return err
	}
	ready := len(commits) > 0
	var standard *c4v1beta1.Standard
	if s := l.container.view.Choices().Standard; s != "" {
		standard = modelfacts.Find(m.Objects.Standards, s)
	}
	rule, protected, err := modelfacts.Protection(l.container.view, l.container.res.Provider, standard)
	if err != nil {
		return err
	}
	routes := make([]model.AgentRoute, 0, len(l.crew.Spec.Routes))
	for _, r := range l.crew.Spec.Routes {
		route := model.AgentRoute{Label: r.Label}
		if r.Implementer != nil {
			route.Implementer = r.Implementer.Name
		}
		if r.Reviewer != nil {
			route.Reviewer = r.Reviewer.Name
		}
		routes = append(routes, route)
	}
	unprotected := ""
	if !protected {
		unprotected = "its Container has no Standard, so main has no checks to protect it"
	}
	if _, err := engine.CrewTick(ctx, l.rt, l.host, engine.CrewInput{Container: name, Repo: l.repo,
		Labels: crewLabels(), Facts: model.CrewFacts{Paused: l.crew.Spec.Paused, Parallel: int(l.crew.Spec.Parallel),
			ContainerReady: ready, Unprotected: unprotected, Routes: routes}}); err != nil {
		return err
	}
	tasks, err := l.rt.Store().Tasks(ctx, name)
	if err != nil {
		return err
	}
	provider := string(l.container.res.Provider.Spec.Provider)
	for _, t := range tasks {
		issue, err := l.host.GetIssue(ctx, l.repo, t.Issue)
		if err != nil {
			return err
		}
		impl, rev := model.RouteAgents(routes, issue.Labels)
		impl, rev = cmpOr(impl, l.crew.Spec.Implementer.Name), cmpOr(rev, l.crew.Spec.Reviewer.Name)
		maxRounds := 0
		if a := modelfacts.Find(m.Agents, impl); a != nil {
			maxRounds = int(a.Spec.MaxRounds)
		}
		tick, err := engine.TaskTick(ctx, l.rt, l.host, engine.TaskInput{Task: t.Name, Container: name, Repo: l.repo,
			Issue: t.Issue, Title: issue.Title, Goal: model.IssueGoal(issue.Number, issue.Title, issue.Body),
			Implementer: impl, Reviewer: rev, MaxRounds: maxRounds, ContainerReady: ready, Protected: protected,
			CheckContexts: rule.StatusContexts, Lease: l.rules.Lease, Now: env.Now(),
			OrderText: func(o model.WorkOrder) string { return method.WorkOrder(o, name, provider) }})
		if err != nil {
			return fmt.Errorf("the Task %s: %w", t.Name, err)
		}
		p := tick.Record.Progress
		res.Tasks = append(res.Tasks, TaskLine{Task: t.Name, Phase: p.Phase, Round: p.Round, Reason: tick.Step.Reason,
			Says: tick.Step.Message})
		role := engine.RunRole(p.Phase)
		if role == "" {
			continue
		}
		run, err := l.rt.Workers().Run(ctx, t.Name, p.Round, role)
		if err != nil {
			return err
		}
		if !run.Started || run.Done || run.Markers == nil || !run.Markers.Unclaimed {
			continue
		}
		agent := run.Markers.Offer.Attrs["agent"]
		_, text, _ := strings.Cut(run.Markers.Offer.Comment.Body, "\n")
		res.Offers = append(res.Offers, Offer{Task: t.Name, Container: name, Issue: t.Issue, Round: p.Round,
			Role: role, Subagent: method.SubagentName(agent), WorkOrder: strings.TrimSpace(text)})
	}
	return nil
}

func cmpOr(s, or string) string {
	if s != "" {
		return s
	}
	return or
}

// ClaimResult is what a session's claim, heartbeat, release or done did.
type ClaimResult struct {
	Task string `json:"task"`
	// Step is the claim's step (model.ClaimStep), or Released or Done.
	Step string `json:"step"`
	// Held means the session holds the work, Until when its claim runs out.
	Held  bool      `json:"held"`
	Until time.Time `json:"until,omitzero"`
	// Branch is the Task's branch, and Clone the folder its work goes in.
	Branch string `json:"branch,omitempty"`
	Clone  string `json:"clone,omitempty"`
	Says   string `json:"says"`
}

// The steps besides a claim's.
const (
	stepReleased = "Released"
	stepDone     = "Done"
)

// session reads task's claim on the host.
func (l lead) claimFacts(ctx context.Context, env Env, task string, issue int) (model.ClaimFacts, error) {
	has, err := l.host.HasBranch(ctx, l.repo, model.TaskBranch(task))
	if err != nil {
		return model.ClaimFacts{}, err
	}
	comments, err := l.comments(ctx, issue)
	return model.ClaimFacts{Branch: has, Comments: comments, Now: env.Now()}, err
}

func (l lead) comments(ctx context.Context, issue int) ([]model.HostComment, error) {
	found, err := l.host.ListIssueComments(ctx, l.repo, issue)
	out := make([]model.HostComment, 0, len(found))
	for _, c := range found {
		out = append(out, model.HostComment{ID: c.ID, Author: c.Author, Created: c.Created, Body: c.Body})
	}
	return out, err
}

// Claim claims task's offered work for session: it posts the claim, reads
// the issue back and holds the work only if the host's order names the
// session; then it clones the Task's branch into work/<task>.
func Claim(ctx context.Context, env Env, m *Model, task, session string) (ClaimResult, error) {
	return heldStep(ctx, env, m, task, session, true)
}

// Heartbeat renews session's claim of task when under half its lease is
// left, and says how long it holds.
func Heartbeat(ctx context.Context, env Env, m *Model, task, session string) (ClaimResult, error) {
	return heldStep(ctx, env, m, task, session, false)
}

func heldStep(ctx context.Context, env Env, m *Model, task, session string, claim bool) (ClaimResult, error) {
	ls, err := leads(ctx, env, m)
	if err != nil {
		return ClaimResult{}, err
	}
	l, issue, err := leadOf(ls, task)
	if err != nil {
		return ClaimResult{}, err
	}
	facts, err := l.claimFacts(ctx, env, task, issue)
	if err != nil {
		return ClaimResult{}, err
	}
	d, err := model.NextClaim(facts, l.rules, l.login, session)
	if err != nil {
		return ClaimResult{}, err
	}
	res := ClaimResult{Task: task, Step: string(d.Step), Branch: model.TaskBranch(task)}
	switch {
	case d.Step == model.ClaimNotOffered:
		res.Says = "nothing is offered on this Task now; call theos_next"
		return res, nil
	case d.Step == model.ClaimTaken:
		res.Says = "another session holds this work; take another offer"
		return res, nil
	case d.Step == model.ClaimPost && !claim:
		res.Says = "this session holds no claim of this Task; claim it first"
		return res, nil
	}
	if d.Post != "" {
		if _, err := l.host.PostIssueComment(ctx, l.repo, issue, d.Post); err != nil {
			return ClaimResult{}, err
		}
		if facts.Comments, err = l.comments(ctx, issue); err != nil {
			return ClaimResult{}, err
		}
	}
	c := model.ClaimOf(facts.Comments, l.rules)
	if !c.Names(l.login, session) {
		res.Says = "another session's claim came first; take another offer"
		return res, nil
	}
	res.Held, res.Until = true, c.Until
	res.Says = fmt.Sprintf("held until %s", c.Until.UTC().Format(time.RFC3339))
	if claim && env.Clone != nil {
		dir := path.Join(WorkDir, task)
		if err := env.Clone(ctx, l.repo, res.Branch, dir); err != nil {
			res.Says += fmt.Sprintf("; the clone in %s failed (%v): clone %s at branch %s yourself", dir, err,
				l.repo.CloneURL, res.Branch)
		} else {
			res.Clone = dir
		}
	}
	return res, nil
}

// Release gives session's claim of task up, for another session to take.
func Release(ctx context.Context, env Env, m *Model, task, session string) (ClaimResult, error) {
	return holderPost(ctx, env, m, task, session, stepReleased, func() (string, error) {
		return model.SessionMarker(model.MarkerRelease, session)
	})
}

// Done says session's turn on task is done: an implementer's commit, which
// counts once it is the branch's head, or a reviewer's verdict, and a
// summary.
func Done(ctx context.Context, env Env, m *Model, task, session, commit, verdict, summary string) (ClaimResult,
	error) {
	if verdict != "" && verdict != model.VerdictApprove && verdict != model.VerdictChanges {
		return ClaimResult{}, fmt.Errorf("verdict %q: give %s or %s", verdict, model.VerdictApprove,
			model.VerdictChanges)
	}
	return holderPost(ctx, env, m, task, session, stepDone, func() (string, error) {
		return model.DoneMarker(session, commit, verdict, summary)
	})
}

// holderPost posts body as session's marker on task, when session holds it.
func holderPost(ctx context.Context, env Env, m *Model, task, session, step string,
	body func() (string, error)) (ClaimResult, error) {
	if err := model.CheckSessionName(session); err != nil {
		return ClaimResult{}, err
	}
	ls, err := leads(ctx, env, m)
	if err != nil {
		return ClaimResult{}, err
	}
	l, issue, err := leadOf(ls, task)
	if err != nil {
		return ClaimResult{}, err
	}
	comments, err := l.comments(ctx, issue)
	if err != nil {
		return ClaimResult{}, err
	}
	res := ClaimResult{Task: task, Branch: model.TaskBranch(task)}
	if c := model.ClaimOf(comments, l.rules); !c.Names(l.login, session) || c.Closed {
		res.Step, res.Says = string(model.ClaimNotOffered), "this session holds no claim of this Task"
		return res, nil
	}
	text, err := body()
	if err != nil {
		return ClaimResult{}, err
	}
	if _, err := l.host.PostIssueComment(ctx, l.repo, issue, text); err != nil {
		return ClaimResult{}, err
	}
	res.Step, res.Says = step, "posted; call theos_next for what follows"
	return res, nil
}
