package controller

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

// The fake host's releases. A milestone's due date is the one its issues were
// given with setIssueRelease; closing one and tagging are recorded per repository.

type fakeReleaseState struct {
	closed map[string][]string                  // owner/name, closed milestones' titles
	tags   map[string]string                    // owner/name@tag, the commit it names
	runs   map[string][]gitprovider.WorkflowRun // owner/name@tag@commit, newest first
	// closeFailures is how many CloseMilestone calls fail before one succeeds.
	closeFailures int
}

func (g *fakeGit) releaseState() *fakeReleaseState {
	if g.releases == nil {
		g.releases = &fakeReleaseState{closed: map[string][]string{}, tags: map[string]string{},
			runs: map[string][]gitprovider.WorkflowRun{}}
	}
	return g.releases
}

func (p *fakeProvider) ListMilestones(_ context.Context, repo gitprovider.Repository) ([]gitprovider.Milestone,
	error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	key := repo.Owner + "/" + repo.Name
	s := p.git.issueState()
	var out []gitprovider.Milestone
	for i, title := range s.milestones[key] {
		if slices.Contains(p.git.releaseState().closed[key], title) {
			continue
		}
		var due time.Time
		for _, issue := range s.issues[key] {
			if issue.milestone == title && !issue.MilestoneDue.IsZero() {
				due = issue.MilestoneDue
			}
		}
		out = append(out, gitprovider.Milestone{ID: int64(i + 1), Title: title, Due: due})
	}
	return out, nil
}

func (p *fakeProvider) MilestoneIssues(_ context.Context, repo gitprovider.Repository,
	id int64) ([]gitprovider.Issue, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	key := repo.Owner + "/" + repo.Name
	title, err := p.git.milestoneTitle(key, id)
	if err != nil {
		return nil, err
	}
	var out []gitprovider.Issue
	for _, issue := range p.git.issueState().issues[key] {
		if issue.milestone == title {
			i := issue.Issue
			i.Labels = slices.Clone(i.Labels)
			out = append(out, i)
		}
	}
	return out, nil
}

func (p *fakeProvider) CloseMilestone(_ context.Context, repo gitprovider.Repository, id int64) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	key := repo.Owner + "/" + repo.Name
	title, err := p.git.milestoneTitle(key, id)
	if err != nil {
		return err
	}
	if s := p.git.releaseState(); s.closeFailures > 0 {
		s.closeFailures--
		return fmt.Errorf("closing milestone %s: host unavailable", title)
	}
	if s := p.git.releaseState(); !slices.Contains(s.closed[key], title) {
		s.closed[key] = append(s.closed[key], title)
	}
	return nil
}

func (p *fakeProvider) GetTag(_ context.Context, repo gitprovider.Repository, tag string) (string, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	return p.git.releaseState().tags[repo.Owner+"/"+repo.Name+"@"+tag], nil
}

func (p *fakeProvider) CreateTag(_ context.Context, repo gitprovider.Repository, tag, commit, _ string) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	key := repo.Owner + "/" + repo.Name + "@" + tag
	s := p.git.releaseState()
	if _, ok := s.tags[key]; ok {
		return fmt.Errorf("tag %s exists", key)
	}
	s.tags[key] = commit
	return nil
}

func (p *fakeProvider) TagRuns(_ context.Context, repo gitprovider.Repository, tag, commit string) (
	[]gitprovider.WorkflowRun, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	return slices.Clone(p.git.releaseState().runs[repo.Owner+"/"+repo.Name+"@"+tag+"@"+commit]), nil
}

// setRuns sets the workflow runs a push of tag at commit started, newest first.
func (g *fakeGit) setRuns(owner, name, tag, commit string, runs ...gitprovider.WorkflowRun) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.releaseState().runs[owner+"/"+name+"@"+tag+"@"+commit] = runs
}

func (g *fakeGit) milestoneTitle(key string, id int64) (string, error) {
	titles := g.issueState().milestones[key]
	if id < 1 || id > int64(len(titles)) {
		return "", fmt.Errorf("no milestone %d in %s", id, key)
	}
	return titles[id-1], nil
}

// tag returns the commit a tag names, "" when there is none.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) tag(owner, name, tag string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.releaseState().tags[owner+"/"+name+"@"+tag]
}

// setTag makes a tag as a person would.
func (g *fakeGit) setTag(owner, name, tag, commit string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.releaseState().tags[owner+"/"+name+"@"+tag] = commit
}

// milestoneClosed reports whether the milestone titled title was closed.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) milestoneClosed(owner, name, title string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Contains(g.releaseState().closed[owner+"/"+name], title)
}

// failCloses makes the next n CloseMilestone calls fail.
func (g *fakeGit) failCloses(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.releaseState().closeFailures = n
}
