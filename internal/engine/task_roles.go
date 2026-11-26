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

// The spec writer's and the verifier's steps of a Task tick (#337;
// docs/design.md, "Spec" and "Verify"), and what a merge files. The
// decisions are model.NextTaskStep's; these read their facts on the host and
// carry them out.

// pullTitle is the title of the Task's pull request: a spec's says so.
func (t *taskTick) pullTitle() string {
	if t.in.Spec {
		return PullTitle(model.SpecTitle(cmpOr(t.in.Title, t.in.Task)), t.in.Task)
	}
	return PullTitle(t.in.Title, t.in.Task)
}

// specFacts reads, for a Spec Task whose round's checks passed, why its
// pull request can't go to review; and for a pull request that waits for a
// person, a spec's or one its Crew's approval rules hold, the pull request
// and what people said of it.
func (t *taskTick) specFacts(ctx context.Context, facts *model.TaskFacts) error {
	p, pull := t.rec.Progress, t.rec.PullRequest
	switch {
	case pull == 0:
		return nil
	case t.in.Spec && facts.Phase == model.TaskChecking && facts.Checks == ports.CheckSuccess &&
		!p.MergeWhenChecksPass:
		refusal, err := t.specRefusal(ctx)
		facts.SpecRefusal = refusal
		return err
	case facts.Phase == model.TaskAwaitingApproval:
		return t.waitingPull(ctx, facts)
	}
	return nil
}

// specRefusal reads the round's pull request and its spec file on the
// branch, and says why it can't go to review (model.SpecRefusal).
func (t *taskTick) specRefusal(ctx context.Context) (string, error) {
	path := model.SpecPath(t.in.Issue)
	changed, err := t.host.PullRequestFiles(ctx, t.in.Repo, t.rec.PullRequest)
	if errors.Is(err, ports.ErrTooManyFiles) {
		return model.SpecScopeRefusal(path, []string{"more files than the Git host lists"}), nil
	}
	if err != nil {
		return "", err
	}
	files, _, err := t.host.ReadFiles(ctx, t.in.Repo, model.TaskBranch(t.in.Task), []string{path})
	if err != nil {
		return "", err
	}
	f, found := files[path]
	return model.SpecRefusal(path, changed, string(f.Data), found), nil
}

// waitingPull reads the pull request of a Task that waits for a person:
// merged or closed on the host, moved past the reviewed commit, behind main
// or in conflict with it, and what people said of the reviewed commit:
// every reviewer but theos's own accounts (TaskInput.Ours).
func (t *taskTick) waitingPull(ctx context.Context, facts *model.TaskFacts) error {
	commit := t.rec.Progress.Commit
	pr, err := t.host.GetPullRequest(ctx, t.in.Repo, t.rec.PullRequest)
	if err != nil {
		return err
	}
	pull := &facts.WaitingPull
	pull.Merged, pull.Closed = pr.Merged, pr.State == stateClosed && !pr.Merged
	pull.HeadMoved = pr.HeadSHA != "" && pr.HeadSHA != commit
	if pull.Merged || pull.Closed || pull.HeadMoved {
		return nil
	}
	facts.Unmergeable, facts.Behind = !pr.Mergeable, pr.Behind
	reviews, err := t.host.PullRequestReviews(ctx, t.in.Repo, t.rec.PullRequest)
	if err != nil {
		return err
	}
	seen := make([]model.PullReview, 0, len(reviews))
	for _, r := range reviews {
		seen = append(seen, model.PullReview{User: r.User, State: r.State, Commit: r.Commit, Body: r.Body,
			Stale: r.Stale, Dismissed: r.Dismissed})
	}
	ours := t.in.Ours
	if ours == nil {
		ours = func(string) bool { return false }
	}
	pull.People = model.PersonReviews(seen, ours, commit)
	return nil
}

// awaitApproval ends the review's run, which approved, and has the pull
// request wait for a person: it says so on the pull request.
func (t *taskTick) awaitApproval(ctx context.Context, role string) error {
	says := "The agents approved this change; it waits for a person to approve it, or to merge it."
	if t.in.Spec {
		says = model.SpecWaitMessage(t.in.Issue, t.rec.PullRequestURL) + ". A person's merge approves it too."
	}
	t.comment(ctx, says)
	return t.next(ctx, role, model.AwaitApproval(t.rec.Progress))
}

// specMerged copies the merged spec's acceptance criteria into its issue,
// with a link to the spec, and labels the issue status/ready, so the Crew
// builds it; the issue stays open (model.PlanSpecMerged). A failed write is
// an error: the next tick finds the pull request merged and comes back here.
func (t *taskTick) specMerged(ctx context.Context) error {
	n, repo := t.in.Issue, t.in.Repo
	path := model.SpecPath(n)
	files, _, err := t.host.ReadFiles(ctx, repo, ports.DefaultBranch, []string{path})
	if err != nil {
		return fmt.Errorf("reading the merged spec %s: %w", path, err)
	}
	issue, err := t.host.GetIssue(ctx, repo, n)
	if err != nil {
		return err
	}
	plan := model.PlanSpecMerged(t.in.Task, model.SpecIssue{Number: n, Body: issue.Body,
		Closed: issue.State == stateClosed}, path, repo.FileURL(path), t.rec.PullRequestURL, string(files[path].Data))
	if plan.Skip != "" {
		t.in.Log.Info("Left the merged spec's issue as it is", "issue", n, "why", plan.Skip)
		return nil
	}
	calls := []func() error{
		func() error { return t.host.EnsureLabels(ctx, repo, hostLabels(model.StatusLabels()...)) },
	}
	if plan.Body != "" && plan.Body != issue.Body {
		calls = append(calls, func() error { return t.host.EditIssueBody(ctx, repo, n, plan.Body) })
	}
	calls = append(calls, func() error { return t.host.AddIssueLabel(ctx, repo, n, plan.Label) })
	// The exclusive scope drops them too, but not where the labels came over
	// non-exclusive from a migration (#2039).
	for _, l := range []string{model.LabelSpecReview, model.LabelInProgress, model.LabelNeedsSpec} {
		calls = append(calls, func() error { return t.host.RemoveIssueLabel(ctx, repo, n, l) })
	}
	calls = append(calls, func() error { return t.host.CommentIssue(ctx, repo, n, plan.Comment) })
	for _, call := range calls {
		if err := call(); err != nil {
			return err
		}
	}
	t.in.Log.Info("Copied the merged spec into its issue", "issue", n, "criteria", len(plan.Criteria))
	return nil
}

// fileFindings files what the round's approving review and verification
// left (TaskRecord.Findings): the reviewer's follow-ups as proposed issues,
// and the criteria the reviewer and the verifier accepted as open as one
// kind/by-hand issue for a person, found again by its marker when a tick
// files it twice; both in the Later milestone, outside any release. It says
// on the issue what it filed. A write that fails is logged: the merge stands.
func (t *taskTick) fileFindings(ctx context.Context) {
	f := t.rec.Findings
	if f == nil {
		return
	}
	repo, log := t.in.Repo, t.in.Log
	note := func(what string, err error) {
		if err != nil {
			log.Info("Could not file what the merge leaves", "what", what, "error", err.Error())
		}
	}
	milestone, err := t.host.EnsureMilestone(ctx, repo, model.FollowUpMilestone, model.FollowUpMilestoneDescription)
	note("the milestone", err)
	note("the labels", t.host.EnsureLabels(ctx, repo, hostLabels(append(model.StatusLabels(),
		model.ByHandLabel())...)))
	var filed []string
	for _, up := range f.FollowUps {
		body := model.FollowUpIssue(up, t.in.Reviewer, t.rec.PullRequestURL, t.in.Issue, t.in.Task)
		issue, err := t.host.CreateIssue(ctx, repo, up.Title, body, []string{model.LabelProposed}, milestone)
		note("a follow-up", err)
		if err == nil {
			filed = append(filed, issue.URL)
		}
	}
	var lines []string
	if open := model.OpenCriteria(f.Open, f.VerifyOpen); len(open) > 0 {
		by := openBy(t.in.Reviewer, t.in.Verifier, f)
		url, err := t.fileByHand(ctx, by, open, milestone)
		note("the by-hand issue", err)
		if err == nil {
			lines = append(lines, fmt.Sprintf("%s accepted criteria that need a person as open; they wait in %s, %s.",
				by, url, model.LabelByHand))
		}
	}
	if len(filed) > 0 {
		lines = append(lines, "The reviewer suggested follow-ups, filed as "+model.LabelProposed+":\n- "+
			strings.Join(filed, "\n- "))
	}
	if len(lines) > 0 {
		note("the comment", t.host.CommentIssue(ctx, repo, t.in.Issue, fmt.Sprintf("Merged in %s.\n\n%s",
			t.rec.PullRequestURL, strings.Join(lines, "\n\n"))))
	}
}

// openBy names the Agents that left the criteria open.
func openBy(reviewer, verifier string, f *model.Findings) string {
	switch {
	case len(f.VerifyOpen) == 0:
		return reviewer
	case len(f.Open) == 0:
		return verifier
	}
	return reviewer + " and " + verifier
}

// fileByHand files the open criteria as the Task's kind/by-hand issue,
// status/needs-human so no Crew picks it, and returns its address: the one
// filed already, found by its marker, else a new one.
func (t *taskTick) fileByHand(ctx context.Context, by string, open []model.OpenCriterion,
	milestone int64) (string, error) {
	existing, err := t.host.ListIssues(ctx, t.in.Repo, model.LabelByHand)
	if err != nil {
		return "", err
	}
	for _, issue := range existing {
		if model.HasByHandMarker(issue.Body, t.in.Task) {
			return issue.URL, nil
		}
	}
	title, body := model.ByHandIssue(t.in.Title, t.in.Issue, t.rec.PullRequestURL, t.in.Task, by, open, nil)
	issue, err := t.host.CreateIssue(ctx, t.in.Repo, title, body, []string{model.LabelByHand, model.LabelNeedsHuman},
		milestone)
	return issue.URL, err
}
