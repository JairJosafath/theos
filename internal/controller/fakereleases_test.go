package controller

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/module"

	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

// The fake host's releases. A milestone's due date is the one its issues were
// given with setIssueRelease; closing one and tagging are recorded per repository.

type fakeReleaseState struct {
	closed map[string][]string                  // owner/name, closed milestones' titles
	tags   map[string]string                    // owner/name@tag, the commit it names
	runs   map[string][]gitprovider.WorkflowRun // owner/name@tag@commit, newest first
	// commitRuns is the runs at a commit, by owner/name@commit, and runners
	// how many runners are online, unknown while nil.
	commitRuns map[string][]gitprovider.WorkflowRun
	runners    *int
	// reruns counts the RerunFailedJobs calls, by owner/name#run ID, and
	// refused is the runs whose re-run the host refuses.
	reruns  map[string]int
	refused map[string]bool
	// published is the Gitea releases, by owner/name@tag, and assets their
	// files' contents, by UUID.
	published map[string]*gitprovider.Release
	assets    map[string][]byte
	// closeFailures is how many CloseMilestone calls fail before one succeeds.
	closeFailures int
	// created is the milestones CreateMilestone made, by owner/name#ID, for
	// their descriptions and due dates.
	created map[string]gitprovider.Milestone
	// goMods is the module path each repository's go.mod declares, by
	// owner/name; a repository without one has no go.mod. uploads counts the
	// module versions uploaded, by path@version (a repeat finds one there and
	// uploads nothing), and moduleFailures how many PublishGoModule calls fail
	// before one gets through, by owner/name. modulePublishes counts the
	// PublishGoModule calls, by owner/name@tag.
	goMods          map[string]string
	uploads         map[string]int
	moduleFailures  map[string]int
	modulePublishes map[string]int
	// sources is each repository's files at a tag, by owner/name@tag.
	sources map[string][]fakeSourceFile
}

// fakeSourceFile is a file of a repository at a tag, as ReadSource hands it on.
type fakeSourceFile struct {
	path string
	mode gitprovider.SourceMode
	data string
}

func (g *fakeGit) releaseState() *fakeReleaseState {
	if g.releases == nil {
		g.releases = &fakeReleaseState{closed: map[string][]string{}, tags: map[string]string{},
			runs: map[string][]gitprovider.WorkflowRun{}, published: map[string]*gitprovider.Release{},
			assets: map[string][]byte{}, created: map[string]gitprovider.Milestone{},
			goMods: map[string]string{}, uploads: map[string]int{}, moduleFailures: map[string]int{},
			modulePublishes: map[string]int{}, reruns: map[string]int{}, refused: map[string]bool{},
			commitRuns: map[string][]gitprovider.WorkflowRun{}, sources: map[string][]fakeSourceFile{}}
	}
	return g.releases
}

func (p *fakeProvider) ListMilestones(_ context.Context, repo gitprovider.Repository) ([]gitprovider.Milestone,
	error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	return p.git.milestones(repo.Owner+"/"+repo.Name, false), nil
}

func (p *fakeProvider) ListAllMilestones(_ context.Context, repo gitprovider.Repository) ([]gitprovider.Milestone,
	error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	return p.git.milestones(repo.Owner+"/"+repo.Name, true), nil
}

func (p *fakeProvider) CreateMilestone(_ context.Context, repo gitprovider.Repository, title, description string,
	due time.Time) (gitprovider.Milestone, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	key := repo.Owner + "/" + repo.Name
	s := p.git.issueState()
	s.milestones[key] = append(s.milestones[key], title)
	m := gitprovider.Milestone{ID: int64(len(s.milestones[key])), Title: title, Description: description, Due: due}
	p.git.releaseState().created[fmt.Sprintf("%s#%d", key, m.ID)] = m
	return m, nil
}

// milestones returns the milestones of owner/name: the open ones, or all of
// them. A milestone CreateMilestone made has its description and due date; any
// other is due when its issues were given one with setIssueRelease.
func (g *fakeGit) milestones(key string, all bool) []gitprovider.Milestone {
	s := g.issueState()
	var out []gitprovider.Milestone
	for i, title := range s.milestones[key] {
		closed := slices.Contains(g.releaseState().closed[key], title)
		if closed && !all {
			continue
		}
		m, made := g.releaseState().created[fmt.Sprintf("%s#%d", key, i+1)]
		if !made {
			m = gitprovider.Milestone{ID: int64(i + 1), Title: title}
			for _, issue := range s.issues[key] {
				if issue.milestone == title && !issue.MilestoneDue.IsZero() {
					m.Due = issue.MilestoneDue
				}
			}
		}
		m.Closed = closed
		out = append(out, m)
	}
	return out
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

func (p *fakeProvider) CommitRuns(_ context.Context, repo gitprovider.Repository, commit string) (
	[]gitprovider.WorkflowRun, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	return slices.Clone(p.git.releaseState().commitRuns[repo.Owner+"/"+repo.Name+"@"+commit]), nil
}

// RunnersOnline doesn't know until a test sets it.
func (p *fakeProvider) RunnersOnline(context.Context) (int, bool) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	if n := p.git.releaseState().runners; n != nil {
		return *n, true
	}
	return 0, false
}

// setCommitRuns sets the CI runs at commit.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) setCommitRuns(owner, name, commit string, runs ...gitprovider.WorkflowRun) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.releaseState().commitRuns[owner+"/"+name+"@"+commit] = runs
}

// RerunFailedJobs queues run id again, as Gitea's new attempt of it does.
func (p *fakeProvider) RerunFailedJobs(_ context.Context, repo gitprovider.Repository, id int64) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	s := p.git.releaseState()
	key := fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, id)
	s.reruns[key]++
	if s.refused[key] {
		return fmt.Errorf("re-run of run %d refused", id)
	}
	for _, byRef := range []map[string][]gitprovider.WorkflowRun{s.runs, s.commitRuns} {
		for key, runs := range byRef {
			if !strings.HasPrefix(key, repo.Owner+"/"+repo.Name+"@") {
				continue
			}
			for i := range runs {
				if runs[i].ID == id {
					runs[i].Status, runs[i].Conclusion = "queued", ""
					return nil
				}
			}
		}
	}
	return fmt.Errorf("run %d of %s/%s not found", id, repo.Owner, repo.Name)
}

// refuseRerun makes the host refuse, or no longer refuse, to re-run run id of
// owner/name.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) refuseRerun(owner, name string, id int64, refuse bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.releaseState().refused[fmt.Sprintf("%s/%s#%d", owner, name, id)] = refuse
}

// reruns is how many times a re-run of run id of owner/name was asked for.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) reruns(owner, name string, id int64) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.releaseState().reruns[fmt.Sprintf("%s/%s#%d", owner, name, id)]
}

func (p *fakeProvider) GetRelease(_ context.Context, repo gitprovider.Repository, tag string) (
	*gitprovider.Release, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	rel := p.git.releaseState().published[repo.Owner+"/"+repo.Name+"@"+tag]
	if rel == nil {
		return nil, nil
	}
	out := *rel
	out.Assets = slices.Clone(rel.Assets)
	return &out, nil
}

func (p *fakeProvider) OpenReleaseAsset(_ context.Context, _ gitprovider.Repository, asset gitprovider.ReleaseAsset) (
	io.ReadCloser, int64, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	data, ok := p.git.releaseState().assets[asset.UUID]
	if !ok {
		return nil, 0, fmt.Errorf("no asset %s", asset.UUID)
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

func (p *fakeProvider) SetReleaseBody(_ context.Context, repo gitprovider.Repository, id int64, body string) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	for key, rel := range p.git.releaseState().published {
		if strings.HasPrefix(key, repo.Owner+"/"+repo.Name+"@") && rel.ID == id {
			if body != "" {
				rel.Body = body
			}
			return nil
		}
	}
	return fmt.Errorf("no release %d in %s/%s", id, repo.Owner, repo.Name)
}

// PublishGoModule publishes the module setGoMod gave the repository, as Gitea
// would: no go.mod, a version Go refuses and one uploaded before are outcomes.
func (p *fakeProvider) PublishGoModule(_ context.Context, repo gitprovider.Repository, tag string) (
	gitprovider.GoModule, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	s := p.git.releaseState()
	out := gitprovider.GoModule{Outcome: gitprovider.GoModuleNoGoMod, Version: tag}
	key := repo.Owner + "/" + repo.Name
	s.modulePublishes[key+"@"+tag]++
	if s.moduleFailures[key] > 0 {
		s.moduleFailures[key]--
		return out, fmt.Errorf("gitea unavailable")
	}
	path, ok := s.goMods[key]
	if !ok {
		return out, nil
	}
	out.Path = path
	if err := module.Check(path, tag); err != nil {
		out.Outcome, out.Reason = gitprovider.GoModuleInvalid, err.Error()
		return out, nil
	}
	out.Outcome = gitprovider.GoModuleExists
	if s.uploads[path+"@"+tag] == 0 {
		out.Outcome = gitprovider.GoModulePublished
		s.uploads[path+"@"+tag]++
	}
	return out, nil
}

// ReadSource hands on the files setSource gave the repository at tag.
func (p *fakeProvider) ReadSource(_ context.Context, repo gitprovider.Repository, tag string,
	fn func(gitprovider.SourceFile, io.Reader) error) error {
	p.git.mu.Lock()
	key := repo.Owner + "/" + repo.Name + "@" + tag
	files, ok := p.git.releaseState().sources[key]
	p.git.mu.Unlock()
	if !ok {
		return fmt.Errorf("gitea has no archive of %s", key)
	}
	for _, f := range files {
		if err := fn(gitprovider.SourceFile{Path: f.path, Mode: f.mode, Size: int64(len(f.data))},
			strings.NewReader(f.data)); err != nil {
			return err
		}
	}
	return nil
}

// setSource gives owner/name the files at tag.
func (g *fakeGit) setSource(owner, name, tag string, files ...fakeSourceFile) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.releaseState().sources[owner+"/"+name+"@"+tag] = files
}

// setGoMod gives owner/name a go.mod declaring module path.
func (g *fakeGit) setGoMod(owner, name, path string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.releaseState().goMods[owner+"/"+name] = path
}

// failModulePublishes makes the next n PublishGoModule calls of owner/name fail.
func (g *fakeGit) failModulePublishes(owner, name string, n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.releaseState().moduleFailures[owner+"/"+name] = n
}

// modulePublishes is how many times the Crew asked to publish owner/name's
// module at tag.
func (g *fakeGit) modulePublishes(owner, name, tag string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.releaseState().modulePublishes[owner+"/"+name+"@"+tag]
}

// modulePublishFailuresLeft is how many PublishGoModule calls of owner/name
// still fail.
func (g *fakeGit) modulePublishFailuresLeft(owner, name string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.releaseState().moduleFailures[owner+"/"+name]
}

// moduleUploads is how many times module path@version was uploaded.
func (g *fakeGit) moduleUploads(path, version string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.releaseState().uploads[path+"@"+version]
}

// setRuns sets the workflow runs a push of tag at commit started, newest first.
//
//nolint:unparam // takes owner and name like the other helpers
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

// closeMilestone closes the milestone titled title, as a release did.
func (g *fakeGit) closeMilestone(owner, name, title string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.releaseState()
	s.closed[owner+"/"+name] = append(s.closed[owner+"/"+name], title)
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

// setGiteaRelease makes the Gitea release of tag, with body and files by name,
// in the order given.
func (g *fakeGit) setGiteaRelease(owner, name, tag, body string, files ...fakeFile) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.releaseState()
	rel := &gitprovider.Release{ID: int64(len(s.published) + 1), Tag: tag, Name: tag, Body: body}
	for i, f := range files {
		uuid := fmt.Sprintf("%s-%s-%s-%d", owner, name, tag, i)
		s.assets[uuid] = f.data
		rel.Assets = append(rel.Assets, gitprovider.ReleaseAsset{ID: int64(100 + i), Name: f.name,
			Size: int64(len(f.data)), UUID: uuid})
	}
	s.published[owner+"/"+name+"@"+tag] = rel
}

// giteaReleaseBody returns the notes of the Gitea release of tag.
func (g *fakeGit) giteaReleaseBody(owner, name, tag string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.releaseState().published[owner+"/"+name+"@"+tag].Body
}

// fakeFile is a file attached to a release.
type fakeFile struct {
	name string
	data []byte
}
