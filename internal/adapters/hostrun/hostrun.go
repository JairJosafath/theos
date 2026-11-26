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

// Package hostrun is the lead runtime's ports.Runtime on the Git host
// (docs/plans/lead-runtime.md, decisions 3 to 5): a Task is its branch,
// task/<task>, and its issue's marker comments. The engine creates the
// branch, posts the offer and, at each transition, the Task's progress; a
// session claims the offer and answers it. Nothing is kept anywhere but
// the host, so a lead session that goes away loses nothing.
package hostrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// Host is what the lead runtime reaches: the repository's branches, its
// issues, and their stamped comments.
type Host interface {
	ports.GitHost
	ports.Issues
	ports.IssueComments
}

// Runtime is the lead runtime for one Container's repository.
type Runtime struct {
	host      Host
	container string
	repo      ports.Repository
	rules     model.ClaimRules
	now       func() time.Time
}

var (
	_ ports.Runtime     = (*Runtime)(nil)
	_ ports.TriageStore = store{}
)

// New is the lead runtime for container on host, its markers counted by
// rules (model.DefaultClaimRules of the credential's login, unless the
// settings say otherwise). now is the engine's clock, which only decides
// when an ended is due; nil is time.Now.
func New(host Host, container string, rules model.ClaimRules, now func() time.Time) *Runtime {
	if now == nil {
		now = time.Now
	}
	return &Runtime{host: host, container: container, repo: host.Repository(container), rules: rules, now: now}
}

// Store keeps the Tasks on the Task's branch and its issue's markers.
func (r *Runtime) Store() ports.TaskStore { return store{r} }

// Workers offers the turns as marker comments a session pulls.
func (r *Runtime) Workers() ports.Workers { return workers{r} }

// Capabilities are the lead runtime's (decision 6).
func (r *Runtime) Capabilities() ports.Capabilities { return model.LeadCapabilities() }

// issueOf is the issue of task, a Task of the Container
// (model.IssueTaskName): <container>-<issue>, or -<issue>-<attempt>.
func (r *Runtime) issueOf(task string) (int, error) {
	rest, ok := strings.CutPrefix(task, r.container+"-")
	issue, _, _ := strings.Cut(rest, "-")
	n, err := strconv.Atoi(issue)
	if !ok || err != nil || n < 1 {
		return 0, fmt.Errorf("%s is no Task of an issue of %s", task, r.container)
	}
	return n, nil
}

// comments are issue's comments as the markers read them.
func (r *Runtime) comments(ctx context.Context, issue int) ([]model.HostComment, error) {
	found, err := r.host.ListIssueComments(ctx, r.repo, issue)
	if err != nil {
		return nil, err
	}
	out := make([]model.HostComment, 0, len(found))
	for _, c := range found {
		out = append(out, model.HostComment{ID: c.ID, Author: c.Author, Created: c.Created, Body: c.Body})
	}
	return out, nil
}

// counts reports whether a marker by author counts: the claimants'
// (decision 10).
func (r *Runtime) counts(author string) bool {
	return slices.ContainsFunc(r.rules.Claimants, func(l string) bool { return strings.EqualFold(l, author) })
}

// store is the Runtime's TaskStore.
type store struct{ r *Runtime }

// Tasks are the Tasks at work: the issues labelled status/in-progress, each
// named by its last progress marker. A Task that ended is not listed: its
// issue is closed, or handed back to a person.
func (s store) Tasks(ctx context.Context, container string) ([]model.CrewTask, error) {
	r := s.r
	if container != r.container {
		return nil, nil // a Container's lead runtime keeps none of another's Tasks
	}
	issues, err := r.host.ListIssues(ctx, r.repo, model.LabelInProgress)
	if err != nil {
		return nil, err
	}
	out := make([]model.CrewTask, 0, len(issues))
	for _, i := range issues {
		comments, err := r.comments(ctx, i.Number)
		if err != nil {
			return nil, err
		}
		task := model.CrewTask{Name: model.IssueTaskName(r.container, i.Number, 1), Issue: i.Number}
		if p, ok := r.lastAtWork(comments); ok {
			task.Name, task.Spec = p.task, p.record.Spec
		}
		out = append(out, task)
	}
	return out, nil
}

// lastAtWork is the last counted progress marker among comments of a Task
// at work that a Crew counts: none of a Triage Task, which the Crew reads
// apart, and none that ended, as a spec that merged before its issue's
// build started.
func (r *Runtime) lastAtWork(comments []model.HostComment) (progressMarker, bool) {
	var last progressMarker
	found := false
	for _, p := range r.progresses(comments) {
		if phase := p.record.Progress.Phase; p.record.Triage || phase == model.TaskMerged || phase == model.TaskFailed {
			continue
		}
		last, found = p, true
	}
	return last, found
}

// TriageTasks are the Triage Tasks of the issues labelled status/proposed,
// each as its last progress says (model.HostTriageTask): a Triage Task
// pushes nothing, so it has no branch, and leaves its issue's status label
// as it is while it works. One whose issue a person took out of
// status/proposed meanwhile is not read.
func (s store) TriageTasks(ctx context.Context, container string) ([]model.TriageTask, error) {
	r := s.r
	if container != r.container {
		return nil, nil
	}
	issues, err := r.host.ListIssues(ctx, r.repo, model.LabelProposed)
	if err != nil {
		return nil, err
	}
	var out []model.TriageTask
	for _, i := range issues {
		comments, err := r.comments(ctx, i.Number)
		if err != nil {
			return nil, err
		}
		last := map[string]string{}
		var names []string
		for _, p := range r.progresses(comments) {
			if !p.record.Triage {
				continue
			}
			if _, ok := last[p.task]; !ok {
				names = append(names, p.task)
			}
			last[p.task] = p.record.Progress.Phase
		}
		for _, name := range names {
			out = append(out, model.HostTriageTask(name, i.Number, last[name]))
		}
	}
	return out, nil
}

// StartTriage posts the Triage Task's first progress, Pending, on its
// issue, unless it has one: no branch, and no label, as the issue stays
// status/proposed until the decision is applied. The offer is the Task
// tick's first step.
func (s store) StartTriage(ctx context.Context, container string, start model.TriageStart, _ string) error {
	r := s.r
	if container != r.container {
		return fmt.Errorf("the lead runtime of %s starts no Tasks on %s", r.container, container)
	}
	comments, err := r.comments(ctx, start.Issue)
	if err != nil {
		return err
	}
	if _, ok := r.lastProgress(comments, start.Task); ok {
		return nil
	}
	return r.postProgress(ctx, start.Issue, start.Task, ports.TaskRecord{
		Progress: model.TaskProgress{Phase: model.TaskPending}, PhaseSince: r.now(), Triage: true})
}

// Start creates the Task's branch at main's head, labels its issue
// status/in-progress and posts its first progress, Pending. The host lets
// one creation of a branch through; a branch there already is this
// Task's, from a start that stopped halfway or a lead that raced this one,
// and the start goes on from it. The offer is the Task tick's first step.
func (s store) Start(ctx context.Context, container string, start model.IssueStart) error {
	r := s.r
	if container != r.container {
		return fmt.Errorf("the lead runtime of %s starts no Tasks on %s", r.container, container)
	}
	branch := model.TaskBranch(start.Task)
	base, err := r.host.BranchCommit(ctx, r.repo, ports.DefaultBranch)
	if err != nil {
		return err
	}
	if err := r.host.CreateBranch(ctx, r.repo, branch, base); err != nil {
		has, herr := r.host.HasBranch(ctx, r.repo, branch)
		if herr != nil || !has {
			return errors.Join(err, herr)
		}
	}
	if err := r.host.AddIssueLabel(ctx, r.repo, start.Issue, model.LabelInProgress); err != nil {
		return err
	}
	comments, err := r.comments(ctx, start.Issue)
	if err != nil {
		return err
	}
	if _, ok := r.lastProgress(comments, start.Task); ok {
		return nil
	}
	return r.postProgress(ctx, start.Issue, start.Task,
		ports.TaskRecord{Progress: model.TaskProgress{Phase: model.TaskPending}, PhaseSince: r.now(), Spec: start.Spec})
}

// Progress is the Task's last progress marker; Pending, round 0, before
// the first. A Task with no branch is none.
func (s store) Progress(ctx context.Context, task string) (ports.TaskRecord, error) {
	r := s.r
	issue, err := r.issueOf(task)
	if err != nil {
		return ports.TaskRecord{}, err
	}
	has, err := r.host.HasBranch(ctx, r.repo, model.TaskBranch(task))
	if err != nil {
		return ports.TaskRecord{}, err
	}
	comments, err := r.comments(ctx, issue)
	if err != nil {
		return ports.TaskRecord{}, err
	}
	p, ok := r.lastProgress(comments, task)
	switch {
	case ok:
		return p.record, nil
	case !has:
		return ports.TaskRecord{}, fmt.Errorf("no Task %s: %s has no branch %s", task, r.repo.Name,
			model.TaskBranch(task))
	}
	return ports.TaskRecord{Progress: model.TaskProgress{Phase: model.TaskPending}}, nil
}

// SaveProgress posts rec as the Task's progress. A Task that ended leaves
// status/in-progress: Merged, its issue is closed; Failed, a person has it.
func (s store) SaveProgress(ctx context.Context, task string, rec ports.TaskRecord) error {
	r := s.r
	issue, err := r.issueOf(task)
	if err != nil {
		return err
	}
	if err := r.postProgress(ctx, issue, task, rec); err != nil {
		return err
	}
	if phase := rec.Progress.Phase; phase == model.TaskMerged || phase == model.TaskFailed {
		return r.host.RemoveIssueLabel(ctx, r.repo, issue, model.LabelInProgress)
	}
	return nil
}

// progressMarker is a progress marker as read back: the Task it names and
// its record.
type progressMarker struct {
	task   string
	record ports.TaskRecord
}

// markerKeyTask names the Task in a progress marker.
const markerKeyTask = "task"

// postProgress posts rec as task's progress on issue: the marker line, then
// the record as a fenced json block.
func (r *Runtime) postProgress(ctx context.Context, issue int, task string, rec ports.TaskRecord) error {
	line, err := model.MarkerLine(model.MarkerProgress, map[string]string{markerKeyTask: task})
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	body := fmt.Sprintf("%s\n\nTask %s is %s, round %d.\n\n```json\n%s\n```\n", line, task, rec.Progress.Phase,
		rec.Progress.Round, data)
	_, err = r.host.PostIssueComment(ctx, r.repo, issue, body)
	return err
}

// lastProgress is the last counted progress marker among comments, of task
// or, when task is "", of any Task, in the host's order. One whose record
// doesn't read is none.
func (r *Runtime) lastProgress(comments []model.HostComment, task string) (progressMarker, bool) {
	var last progressMarker
	found := false
	for _, p := range r.progresses(comments) {
		if task == "" || p.task == task {
			last, found = p, true
		}
	}
	return last, found
}

// progresses are the counted progress markers among comments, in the
// host's order. One whose record doesn't read is none.
func (r *Runtime) progresses(comments []model.HostComment) []progressMarker {
	ordered := slices.Clone(comments)
	slices.SortStableFunc(ordered, func(a, b model.HostComment) int {
		if c := a.Created.Compare(b.Created); c != 0 {
			return c
		}
		return int(a.ID - b.ID)
	})
	var out []progressMarker
	for _, c := range ordered {
		m, ok := model.ParseMarker(c)
		if !ok || m.Kind != model.MarkerProgress || !r.counts(c.Author) {
			continue
		}
		rec, err := recordIn(c.Body)
		if err != nil {
			continue
		}
		out = append(out, progressMarker{task: m.Attrs[markerKeyTask], record: rec})
	}
	return out
}

// recordIn reads the one fenced json block of a progress marker strictly.
func recordIn(body string) (ports.TaskRecord, error) {
	_, rest, ok := strings.Cut(body, "```json\n")
	block, _, closed := strings.Cut(rest, "\n```")
	if !ok || !closed {
		return ports.TaskRecord{}, errors.New("no json block")
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(block)))
	dec.DisallowUnknownFields()
	var rec ports.TaskRecord
	return rec, dec.Decode(&rec)
}

// workers is the Runtime's Workers.
type workers struct{ r *Runtime }

// run is the issue's run as its markers say, and the Task, round and role
// its offer names; a zero RunState when no offer counts.
func (r *Runtime) run(ctx context.Context, issue int, task string) (model.RunState, error) {
	comments, err := r.comments(ctx, issue)
	if err != nil {
		return model.RunState{}, err
	}
	head, err := r.host.BranchCommit(ctx, r.repo, model.TaskBranch(task))
	if err != nil {
		head = "" // no branch: no done names its head
	}
	return model.RunFromMarkers(model.RunFacts{Comments: comments, Head: head, Rules: r.rules, Now: r.now()}), nil
}

// names reports whether run is the offered turn of task's round and role.
func names(run model.RunState, task string, round int, role string) bool {
	m := run.Markers
	return run.Started && m != nil && m.Offer.Attrs[markerKeyTask] == task && m.Round() == round && m.Role() == role
}

// Offer posts the offer marker of w's turn, and the work order's text, unless
// that turn is offered already and not answered or ended.
func (w workers) Offer(ctx context.Context, o ports.WorkOrder) error {
	r := w.r
	issue := o.Issue
	if issue == 0 {
		var err error
		if issue, err = r.issueOf(o.Task); err != nil {
			return err
		}
	}
	run, err := r.run(ctx, issue, o.Task)
	if err != nil {
		return err
	}
	if names(run, o.Task, o.Round, o.Role) && !run.Done {
		return nil
	}
	line, err := model.OfferMarker(o.Task, o.Round, o.Role, o.Agent)
	if err != nil {
		return err
	}
	_, err = r.host.PostIssueComment(ctx, r.repo, issue, line+"\n\n"+orderText(o))
	return err
}

// orderText is what an offer says: the work order's text, else what the
// turn is.
func orderText(o ports.WorkOrder) string {
	if o.Text != "" {
		return o.Text
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Round %d of Task %s: the %s's turn, on branch `%s`.", o.Round, o.Task, o.Role, o.Branch)
	if o.Lease > 0 {
		fmt.Fprintf(&b, " A claim holds for %s; renew it before then.", o.Lease)
	}
	if f := strings.TrimSpace(o.Feedback); f != "" {
		fmt.Fprintf(&b, "\n\nWhat this round addresses:\n\n%s", f)
	}
	return b.String()
}

// Run is the turn's run, read from the issue's markers and the head of the
// Task's branch: not started while no offer of it counts.
func (w workers) Run(ctx context.Context, task string, round int, role string) (model.RunState, error) {
	issue, err := w.r.issueOf(task)
	if err != nil {
		return model.RunState{}, err
	}
	run, err := w.r.run(ctx, issue, task)
	if err != nil || !names(run, task, round, role) {
		return model.RunState{}, err
	}
	return run, nil
}

// End posts the ended marker of the turn, why the engine's word, unless the
// turn is answered, ended already or not the one offered: a done run needs
// no end, and the next offer starts afresh.
func (w workers) End(ctx context.Context, task string, round int, role, why string) error {
	issue, err := w.r.issueOf(task)
	if err != nil {
		return err
	}
	run, err := w.r.run(ctx, issue, task)
	if err != nil || !names(run, task, round, role) || run.Done {
		return err
	}
	line, err := model.EndedMarker(why)
	if err != nil {
		return err
	}
	_, err = w.r.host.PostIssueComment(ctx, w.r.repo, issue, line)
	return err
}
