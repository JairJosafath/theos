/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

// The fake host's issues, labels and reviews. Issue numbers count separately
// from pull request numbers, which Gitea shares; the tests don't mix them.

type fakeIssue struct {
	gitprovider.Issue
	blockers  []string
	assignees []string
	comments  []string
	// milestone is the title of the milestone the issue was filed in.
	milestone string
	// filedLabels are the labels the issue was filed with, before anyone
	// (a Crew taking it) changed them.
	filedLabels []string
}

type fakeReview struct {
	Event, Body, Commit string
}

type fakeIssueState struct {
	issues     map[string][]*fakeIssue // owner/name, by number - 1
	labels     map[string][]string     // owner/name
	milestones map[string][]string     // owner/name, titles by ID - 1
	reviews    map[string][]fakeReview // owner/name#number
	// late makes CreateIssue file the issue but answer only once the caller has
	// given up, as a host under load does.
	late bool
	// listErrs makes ListIssues of an owner/name fail.
	listErrs map[string]error
	// depErrs makes AddIssueDependency on an issue of an owner/name fail.
	depErrs map[string]error
}

// failDependencies makes adding a dependency to an issue of owner/name fail
// with err from now on; a nil err adds them again.
func (g *fakeGit) failDependencies(owner, name string, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.issueState()
	if s.depErrs == nil {
		s.depErrs = map[string]error{}
	}
	s.depErrs[owner+"/"+name] = err
}

// failListIssues makes listing the issues of owner/name fail with err from
// now on; a nil err lists them again.
func (g *fakeGit) failListIssues(owner, name string, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.issueState()
	if s.listErrs == nil {
		s.listErrs = map[string]error{}
	}
	s.listErrs[owner+"/"+name] = err
}

func (g *fakeGit) issueState() *fakeIssueState {
	if g.issues == nil {
		g.issues = &fakeIssueState{issues: map[string][]*fakeIssue{}, labels: map[string][]string{},
			milestones: map[string][]string{}, reviews: map[string][]fakeReview{}}
	}
	return g.issues
}

// addIssue files an issue as a person would, and returns its number.
func (g *fakeGit) addIssue(owner, name, title, body string, labels []string, blockers ...string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.issueState()
	key := owner + "/" + name
	n := len(s.issues[key]) + 1
	s.issues[key] = append(s.issues[key], &fakeIssue{Issue: gitprovider.Issue{Number: n, Title: title, Body: body,
		State: "open", Labels: labels, URL: fmt.Sprintf("http://gitea/%s/issues/%d", key, n)}, blockers: blockers})
	return n
}

// issue returns a copy of an issue, with its blockers, assignees and comments.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) issue(owner, name string, number int) fakeIssue {
	g.mu.Lock()
	defer g.mu.Unlock()
	i := g.issueState().find(owner+"/"+name, number)
	if i == nil {
		return fakeIssue{}
	}
	out := *i
	out.Labels, out.comments, out.blockers = slices.Clone(i.Labels), slices.Clone(i.comments), slices.Clone(i.blockers)
	return out
}

// setBlockers replaces the open issues an issue depends on.
func (g *fakeGit) setBlockers(owner, name string, number int, blockers ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.issueState().find(owner+"/"+name, number).blockers = blockers
}

// reviews lists the reviews posted on a pull request.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) reviews(owner, name string, number int) []fakeReview {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.issueState().reviews[fmt.Sprintf("%s/%s#%d", owner, name, number)])
}

// openIssues lists the open issues of a repository, by number.
func (g *fakeGit) openIssues(owner, name string) []fakeIssue {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []fakeIssue
	for _, i := range g.issueState().issues[owner+"/"+name] {
		if i.State == "open" {
			out = append(out, *i)
		}
	}
	return out
}

func (s *fakeIssueState) find(key string, number int) *fakeIssue {
	if number < 1 || number > len(s.issues[key]) {
		return nil
	}
	return s.issues[key][number-1]
}

func (p *fakeProvider) issueOf(repo gitprovider.Repository, number int) (*fakeIssue, error) {
	i := p.git.issueState().find(repo.Owner+"/"+repo.Name, number)
	if i == nil {
		return nil, fmt.Errorf("issue %d of %s/%s: %w", number, repo.Owner, repo.Name, gitprovider.ErrIssueNotFound)
	}
	return i, nil
}

// ReviewPullRequest refuses a review by the pull request's author, as Gitea does.
func (p *fakeProvider) ReviewPullRequest(_ context.Context, repo gitprovider.Repository, number int,
	commit, event, body string) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	key := fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, number)
	if p.git.agentState().authors[key] == p.cfg.Credentials.Username {
		return fmt.Errorf("review %s: approve your own pull is not allowed", key)
	}
	s := p.git.issueState()
	s.reviews[key] = append(s.reviews[key], fakeReview{Event: event, Body: body, Commit: commit})
	return nil
}

func (p *fakeProvider) EnsureLabels(_ context.Context, repo gitprovider.Repository, labels []gitprovider.Label) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	s := p.git.issueState()
	key := repo.Owner + "/" + repo.Name
	for _, l := range labels {
		if !slices.Contains(s.labels[key], l.Name) {
			s.labels[key] = append(s.labels[key], l.Name)
		}
	}
	return nil
}

func (p *fakeProvider) EnsureMilestone(_ context.Context, repo gitprovider.Repository, title, _ string) (int64, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	s := p.git.issueState()
	key := repo.Owner + "/" + repo.Name
	if i := slices.Index(s.milestones[key], title); i >= 0 {
		return int64(i + 1), nil
	}
	s.milestones[key] = append(s.milestones[key], title)
	return int64(len(s.milestones[key])), nil
}

func (p *fakeProvider) ListIssues(_ context.Context, repo gitprovider.Repository, label string) ([]gitprovider.Issue, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	if err := p.git.issueState().listErrs[repo.Owner+"/"+repo.Name]; err != nil {
		return nil, err
	}
	var out []gitprovider.Issue
	for _, i := range p.git.issueState().issues[repo.Owner+"/"+repo.Name] {
		if i.State == "open" && (label == "" || slices.Contains(i.Labels, label)) {
			issue := i.Issue
			if i.milestone != "" {
				issue.Milestone = int64(slices.Index(p.git.issueState().milestones[repo.Owner+"/"+repo.Name],
					i.milestone) + 1)
				issue.MilestoneTitle = i.milestone
			}
			out = append(out, issue)
		}
	}
	return out, nil
}

func (p *fakeProvider) GetIssue(_ context.Context, repo gitprovider.Repository, number int) (gitprovider.Issue, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	i, err := p.issueOf(repo, number)
	if err != nil {
		return gitprovider.Issue{}, err
	}
	out := i.Issue
	if i.milestone != "" {
		out.Milestone = int64(slices.Index(p.git.issueState().milestones[repo.Owner+"/"+repo.Name], i.milestone) + 1)
		out.MilestoneTitle = i.milestone
	}
	return out, nil
}

// AddIssueDependency makes an issue depend on another, of the same repository
// ("#3") or another ("owner/name#3").
func (p *fakeProvider) AddIssueDependency(_ context.Context, repo gitprovider.Repository, number int,
	blockerRepo gitprovider.Repository, blocker int) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	if err := p.git.issueState().depErrs[repo.Owner+"/"+repo.Name]; err != nil {
		return err
	}
	i, err := p.issueOf(repo, number)
	if err != nil {
		return err
	}
	ref := fmt.Sprintf("#%d", blocker)
	if blockerRepo.Owner != repo.Owner || blockerRepo.Name != repo.Name {
		ref = blockerRepo.Owner + "/" + blockerRepo.Name + ref
	}
	if !slices.Contains(i.blockers, ref) {
		i.blockers = append(i.blockers, ref)
	}
	return nil
}

// setIssueMilestone puts an issue in the milestone titled title, creating it if needed.
func (g *fakeGit) setIssueMilestone(owner, name string, number int, title string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.issueState()
	key := owner + "/" + name
	if !slices.Contains(s.milestones[key], title) {
		s.milestones[key] = append(s.milestones[key], title)
	}
	s.find(key, number).milestone = title
}

// setIssueRelease puts an issue in the milestone titled title, due on due.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) setIssueRelease(owner, name string, number int, title string, due time.Time) {
	g.setIssueMilestone(owner, name, number, title)
	g.mu.Lock()
	defer g.mu.Unlock()
	i := g.issueState().find(owner+"/"+name, number)
	i.MilestoneTitle, i.MilestoneDue = title, due
}

func (p *fakeProvider) OpenBlockers(_ context.Context, repo gitprovider.Repository, number int) ([]string, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	i, err := p.issueOf(repo, number)
	if err != nil {
		return nil, err
	}
	return slices.Clone(i.blockers), nil
}

// AddIssueLabel emulates Gitea's exclusive scoped labels for the "status/" scope.
func (p *fakeProvider) AddIssueLabel(_ context.Context, repo gitprovider.Repository, number int, label string) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	i, err := p.issueOf(repo, number)
	if err != nil {
		return err
	}
	if !slices.Contains(p.git.issueState().labels[repo.Owner+"/"+repo.Name], label) {
		return fmt.Errorf("no label %s", label)
	}
	scope, _, _ := strings.Cut(label, "/")
	i.Labels = slices.DeleteFunc(i.Labels, func(l string) bool {
		return l == label || scope == "status" && strings.HasPrefix(l, "status/")
	})
	i.Labels = append(i.Labels, label)
	return nil
}

func (p *fakeProvider) RemoveIssueLabel(ctx context.Context, repo gitprovider.Repository, number int, label string) error {
	if err := ctx.Err(); err != nil {
		return err // a request on a spent context fails, as over HTTP
	}
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	i, err := p.issueOf(repo, number)
	if err != nil {
		return err
	}
	i.Labels = slices.DeleteFunc(i.Labels, func(l string) bool { return l == label })
	return nil
}

func (p *fakeProvider) CommentIssue(ctx context.Context, repo gitprovider.Repository, number int, body string) error {
	if err := ctx.Err(); err != nil {
		return err // a request on a spent context fails, as over HTTP
	}
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	i, err := p.issueOf(repo, number)
	if err != nil {
		return err
	}
	i.comments = append(i.comments, body)
	return nil
}

func (p *fakeProvider) CloseIssue(ctx context.Context, repo gitprovider.Repository, number int) error {
	if err := ctx.Err(); err != nil {
		return err // a request on a spent context fails, as over HTTP
	}
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	i, err := p.issueOf(repo, number)
	if err != nil {
		return err
	}
	i.State = "closed"
	return nil
}

func (p *fakeProvider) EditIssueBody(ctx context.Context, repo gitprovider.Repository, number int, body string) error {
	if err := ctx.Err(); err != nil {
		return err // a request on a spent context fails, as over HTTP
	}
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	i, err := p.issueOf(repo, number)
	if err != nil {
		return err
	}
	i.Body = body
	return nil
}

func (p *fakeProvider) SetIssueMilestone(_ context.Context, repo gitprovider.Repository, number int, milestone int64) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	i, err := p.issueOf(repo, number)
	if err != nil {
		return err
	}
	key := repo.Owner + "/" + repo.Name
	titles := p.git.issueState().milestones[key]
	switch {
	case milestone == 0:
		i.milestone = ""
		i.MilestoneTitle, i.MilestoneDue = "", time.Time{}
	case int(milestone) <= len(titles):
		// The issue takes its new milestone's due date, as the host reads it.
		for _, m := range p.git.milestones(key, true) {
			if m.ID == milestone {
				i.MilestoneTitle, i.MilestoneDue = m.Title, m.Due
			}
		}
		i.milestone = titles[milestone-1]
	default:
		return fmt.Errorf("no milestone %d", milestone)
	}
	return nil
}

func (p *fakeProvider) AssignIssue(_ context.Context, repo gitprovider.Repository, number int, users []string) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	i, err := p.issueOf(repo, number)
	if err != nil {
		return err
	}
	i.assignees = slices.Clone(users)
	return nil
}

func (p *fakeProvider) CreateIssue(ctx context.Context, repo gitprovider.Repository, title, body string,
	labels []string, milestone int64) (gitprovider.Issue, error) {
	p.git.mu.Lock()
	s := p.git.issueState()
	key := repo.Owner + "/" + repo.Name
	for _, l := range labels {
		if !slices.Contains(s.labels[key], l) {
			p.git.mu.Unlock()
			return gitprovider.Issue{}, fmt.Errorf("no label %s", l)
		}
	}
	var milestoneTitle string
	if milestone != 0 {
		if milestone < 0 || milestone > int64(len(s.milestones[key])) {
			p.git.mu.Unlock()
			return gitprovider.Issue{}, fmt.Errorf("no milestone %d", milestone)
		}
		milestoneTitle = s.milestones[key][milestone-1]
	}
	p.git.mu.Unlock()
	n := p.git.addIssue(repo.Owner, repo.Name, title, body, labels)
	p.git.mu.Lock()
	filed := s.find(key, n)
	filed.milestone = milestoneTitle
	filed.filedLabels = slices.Clone(labels)
	late := s.late
	p.git.mu.Unlock()
	if late {
		<-ctx.Done()
		return gitprovider.Issue{}, ctx.Err()
	}
	return p.GetIssue(context.Background(), repo, n)
}

// labelIssue adds a label to an issue, as a person would in the Git host.
//
//nolint:unparam // a person may add any label
func (g *fakeGit) labelIssue(owner, name string, number int, label string) error {
	p := &fakeProvider{git: g}
	return p.AddIssueLabel(context.Background(), gitprovider.Repository{Owner: owner, Name: name}, number, label)
}

// closeIssue closes an issue, as a person would in the Git host.
func (g *fakeGit) closeIssue(owner, name string, number int) error {
	p := &fakeProvider{git: g}
	return p.CloseIssue(context.Background(), gitprovider.Repository{Owner: owner, Name: name}, number)
}

// fallBehind makes a pull request's branch miss what merged into main since it
// took main in.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) fallBehind(owner, name string, number int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, pr := range g.agentState().pulls[owner+"/"+name] {
		if pr.Number == number {
			pr.Behind = true
		}
	}
}

// pullAuthor is who opened a pull request.
func (g *fakeGit) pullAuthor(owner, name string, number int) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.agentState().authors[fmt.Sprintf("%s/%s#%d", owner, name, number)]
}

// conflict makes a pull request conflict with its base, as main moved with
// conflicting changes, or merge cleanly again.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) conflict(owner, name string, number int, conflicted bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.agentState().conflicts[fmt.Sprintf("%s/%s#%d", owner, name, number)] = conflicted
	for _, pr := range g.agentState().pulls[owner+"/"+name] {
		if pr.Number == number {
			pr.Mergeable = !conflicted
			pr.Behind = conflicted // resolving it takes in main
		}
	}
}

// retest makes the Git host say a pull request doesn't merge while it tests it
// against its base, which conflicts with it no more than before, or finish.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) retest(owner, name string, number int, testing bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, pr := range g.agentState().pulls[owner+"/"+name] {
		if pr.Number == number {
			pr.Mergeable = !testing
		}
	}
}
