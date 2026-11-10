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

package fake

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// The fake host's releases. A milestone's due date is the one its issues were
// given with SetIssueRelease; closing one and tagging are recorded per repository.

type releaseStore struct {
	closed map[string][]string            // owner/name, closed milestones' titles
	tags   map[string]string              // owner/name@tag, the commit it names
	runs   map[string][]ports.WorkflowRun // owner/name@tag@commit, newest first
	// commitRuns is the runs at a commit, by owner/name@commit, and runners
	// how many runners are online, unknown while nil.
	commitRuns map[string][]ports.WorkflowRun
	runners    *int
	// runJobs is the jobs of a run, by owner/name#run ID, and onlineRunners
	// the IDs of the runners online; any other runner is offline.
	runJobs       map[string][]ports.WorkflowJob
	onlineRunners map[int64]bool
	// reruns counts the RerunFailedJobs calls, by owner/name#run ID, and
	// refused is the runs whose re-run the host refuses.
	reruns  map[string]int
	refused map[string]bool
	// published is the host's releases, by owner/name@tag, and assets their
	// files' contents, by UUID.
	published map[string]*ports.Release
	assets    map[string][]byte
	// closeFailures is how many CloseMilestone calls fail before one succeeds.
	closeFailures int
	// created is the milestones CreateMilestone made, by owner/name#ID, for
	// their descriptions and due dates.
	created map[string]ports.Milestone
	// goMods is the module path each repository's go.mod declares, by
	// owner/name; a repository without one has no go.mod. uploads counts the
	// module versions uploaded, by path@version (a repeat finds one there and
	// uploads nothing), and moduleFailures how many PublishGoModule calls fail
	// before one gets through, by owner/name. modulePublishes counts the
	// PublishGoModule calls, by owner/name@tag, and moduleDelays how long each
	// call takes, by owner/name.
	goMods          map[string]string
	uploads         map[string]int
	moduleFailures  map[string]int
	modulePublishes map[string]int
	moduleDelays    map[string]time.Duration
	// sources is each repository's files at a tag, by owner/name@tag.
	sources map[string][]SourceFile
	// images is the outcome PublishImage gives each release, by
	// owner/name@tag, none when unset; imageFailures how many PublishImage
	// calls fail before one gets through, by owner/name; imagePushes counts
	// the calls, by owner/name@tag; and registry is the digest each tag
	// names in the owner's registry, by owner/name@tag; imageHolds holds
	// each PublishImage call of owner/name open until its channel closes.
	images        map[string]ports.Image
	imageFailures map[string]int
	imagePushes   map[string]int
	registry      map[string]string
	imageHolds    map[string]chan struct{}
	// imageCopies is the digest CopyImage copied to each
	// host/repository:tag.
	imageCopies map[string]string
	// packages is the package each file of a release holds, by
	// owner/name@tag, as SetReleasePackages set them; packageFailures how
	// many PublishReleasePackages calls fail before one gets through, by
	// owner/name; packagePublishes counts the calls, by owner/name@tag;
	// packageUploads the package versions uploaded, by
	// owner:ecosystem/name@version, each owner's registry its own as Gitea's
	// are; packageTokens the registry token each upload carried, by the same
	// key.
	packages         map[string][]Package
	packageFailures  map[string]int
	packagePublishes map[string]int
	packageUploads   map[string]int
	packageTokens    map[string]string
}

// SourceFile is a file of a repository at a tag, as ReadSource hands it on.
type SourceFile struct {
	Path string
	Mode ports.SourceMode
	Data string
}

func (s *state) releaseState() *releaseStore {
	if s.releases == nil {
		s.releases = &releaseStore{closed: map[string][]string{}, tags: map[string]string{},
			runs: map[string][]ports.WorkflowRun{}, published: map[string]*ports.Release{},
			assets: map[string][]byte{}, created: map[string]ports.Milestone{},
			goMods: map[string]string{}, uploads: map[string]int{}, moduleFailures: map[string]int{},
			modulePublishes: map[string]int{}, moduleDelays: map[string]time.Duration{}, reruns: map[string]int{},
			refused: map[string]bool{}, commitRuns: map[string][]ports.WorkflowRun{},
			sources: map[string][]SourceFile{}, runJobs: map[string][]ports.WorkflowJob{},
			onlineRunners: map[int64]bool{}, images: map[string]ports.Image{},
			imageFailures: map[string]int{}, imagePushes: map[string]int{}, registry: map[string]string{},
			imageHolds: map[string]chan struct{}{},
			packages:   map[string][]Package{}, packageFailures: map[string]int{}, packagePublishes: map[string]int{},
			packageUploads: map[string]int{}, packageTokens: map[string]string{}}
	}
	return s.releases
}

func (g *Git) ListMilestones(_ context.Context, repo ports.Repository) ([]ports.Milestone,
	error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.milestones(repo.Owner+"/"+repo.Name, false), nil
}

func (g *Git) ListAllMilestones(_ context.Context, repo ports.Repository) ([]ports.Milestone,
	error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.milestones(repo.Owner+"/"+repo.Name, true), nil
}

func (g *Git) CreateMilestone(_ context.Context, repo ports.Repository, title, description string,
	due time.Time) (ports.Milestone, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	key := repo.Owner + "/" + repo.Name
	s := g.s.issueState()
	s.milestones[key] = append(s.milestones[key], title)
	m := ports.Milestone{ID: int64(len(s.milestones[key])), Title: title, Description: description, Due: due}
	g.s.releaseState().created[fmt.Sprintf("%s#%d", key, m.ID)] = m
	return m, nil
}

// milestones returns the milestones of owner/name: the open ones, or all of
// them. A milestone CreateMilestone made has its description and due date; any
// other is due when its issues were given one with SetIssueRelease.
func (s *state) milestones(key string, all bool) []ports.Milestone {
	is := s.issueState()
	var out []ports.Milestone
	for i, title := range is.milestones[key] {
		closed := slices.Contains(s.releaseState().closed[key], title)
		if closed && !all {
			continue
		}
		m, made := s.releaseState().created[fmt.Sprintf("%s#%d", key, i+1)]
		if !made {
			m = ports.Milestone{ID: int64(i + 1), Title: title,
				Description: is.descriptions[fmt.Sprintf("%s#%d", key, i+1)]}
			for _, issue := range is.issues[key] {
				if issue.InMilestone == title && !issue.MilestoneDue.IsZero() {
					m.Due = issue.MilestoneDue
				}
			}
		}
		m.Closed = closed
		out = append(out, m)
	}
	return out
}

func (g *Git) MilestoneIssues(_ context.Context, repo ports.Repository,
	id int64) ([]ports.Issue, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	key := repo.Owner + "/" + repo.Name
	title, err := g.s.milestoneTitle(key, id)
	if err != nil {
		return nil, err
	}
	var out []ports.Issue
	for _, issue := range g.s.issueState().issues[key] {
		if issue.InMilestone == title {
			i := issue.Issue
			i.Labels = slices.Clone(i.Labels)
			out = append(out, i)
		}
	}
	return out, nil
}

func (g *Git) DescribeMilestone(_ context.Context, repo ports.Repository, id int64, description string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	key := repo.Owner + "/" + repo.Name
	if _, err := g.s.milestoneTitle(key, id); err != nil {
		return err
	}
	// A milestone made by hand takes its due date from its issues, as listed.
	made := fmt.Sprintf("%s#%d", key, id)
	m, ok := g.s.releaseState().created[made]
	if !ok {
		for _, listed := range g.s.milestones(key, true) {
			if listed.ID == id {
				m = listed
			}
		}
		m.Closed = false
	}
	m.Description = description
	g.s.releaseState().created[made] = m
	return nil
}

// EditMilestoneTitle retitles milestone id. The fake keeps a milestone's
// issues and state by its title, so they follow it to the new one.
func (g *Git) EditMilestoneTitle(_ context.Context, repo ports.Repository, id int64, title string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	key := repo.Owner + "/" + repo.Name
	old, err := g.s.milestoneTitle(key, id)
	if err != nil || old == title {
		return err
	}
	is, rs := g.s.issueState(), g.s.releaseState()
	is.milestones[key][id-1] = title
	for _, issue := range is.issues[key] {
		if issue.InMilestone == old {
			issue.InMilestone = title
			if issue.MilestoneTitle == old {
				issue.MilestoneTitle = title
			}
		}
	}
	if i := slices.Index(rs.closed[key], old); i >= 0 {
		rs.closed[key][i] = title
	}
	made := fmt.Sprintf("%s#%d", key, id)
	if m, ok := rs.created[made]; ok {
		m.Title = title
		rs.created[made] = m
	}
	return nil
}

func (g *Git) CloseMilestone(_ context.Context, repo ports.Repository, id int64) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	key := repo.Owner + "/" + repo.Name
	title, err := g.s.milestoneTitle(key, id)
	if err != nil {
		return err
	}
	if s := g.s.releaseState(); s.closeFailures > 0 {
		s.closeFailures--
		return fmt.Errorf("closing milestone %s: host unavailable", title)
	}
	if s := g.s.releaseState(); !slices.Contains(s.closed[key], title) {
		s.closed[key] = append(s.closed[key], title)
	}
	return nil
}

func (g *Git) GetTag(_ context.Context, repo ports.Repository, tag string) (string, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.releaseState().tags[repo.Owner+"/"+repo.Name+"@"+tag], nil
}

func (g *Git) CreateTag(_ context.Context, repo ports.Repository, tag, commit, _ string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	key := repo.Owner + "/" + repo.Name + "@" + tag
	s := g.s.releaseState()
	if _, ok := s.tags[key]; ok {
		return fmt.Errorf("tag %s exists", key)
	}
	s.tags[key] = commit
	return nil
}

func (g *Git) GetRelease(_ context.Context, repo ports.Repository, tag string) (
	*ports.Release, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	rel := g.s.releaseState().published[repo.Owner+"/"+repo.Name+"@"+tag]
	if rel == nil {
		return nil, nil
	}
	out := *rel
	out.Assets = slices.Clone(rel.Assets)
	return &out, nil
}

func (g *Git) OpenReleaseAsset(_ context.Context, _ ports.Repository, asset ports.ReleaseAsset) (
	io.ReadCloser, int64, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	data, ok := g.s.releaseState().assets[asset.UUID]
	if !ok {
		return nil, 0, fmt.Errorf("no asset %s", asset.UUID)
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

func (g *Git) SetReleaseBody(_ context.Context, repo ports.Repository, id int64, body string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	for key, rel := range g.s.releaseState().published {
		if strings.HasPrefix(key, repo.Owner+"/"+repo.Name+"@") && rel.ID == id {
			if body != "" {
				rel.Body = body
			}
			return nil
		}
	}
	return fmt.Errorf("no release %d in %s/%s", id, repo.Owner, repo.Name)
}

// ReadSource hands on the files SetSource gave the repository at tag, else
// those of the commit the tag names, by path.
func (g *Git) ReadSource(_ context.Context, repo ports.Repository, tag string,
	fn func(ports.SourceFile, io.Reader) error) error {
	g.s.mu.Lock()
	key := repo.Owner + "/" + repo.Name + "@" + tag
	rs := g.s.releaseState()
	files, ok := rs.sources[key]
	if !ok {
		var tree scaffold.Tree
		if commit, tagged := rs.tags[key]; tagged {
			tree, ok = g.s.commitTrees[repo.Owner+"/"+repo.Name+"@"+commit]
		}
		for _, filePath := range slices.Sorted(maps.Keys(tree)) {
			mode := ports.SourcePlain
			if tree[filePath].Executable {
				mode = ports.SourceExecutable
			}
			files = append(files, SourceFile{Path: filePath, Mode: mode, Data: string(tree[filePath].Data)})
		}
	}
	g.s.mu.Unlock()
	if !ok {
		return fmt.Errorf("gitea has no archive of %s", key)
	}
	for _, f := range files {
		if err := fn(ports.SourceFile{Path: f.Path, Mode: f.Mode, Size: int64(len(f.Data))},
			strings.NewReader(f.Data)); err != nil {
			return err
		}
	}
	return nil
}

// SetSource gives owner/name the files at tag.
func (g *Git) SetSource(owner, name, tag string, files ...SourceFile) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.releaseState().sources[owner+"/"+name+"@"+tag] = files
}

func (s *state) milestoneTitle(key string, id int64) (string, error) {
	titles := s.issueState().milestones[key]
	if id < 1 || id > int64(len(titles)) {
		return "", fmt.Errorf("no milestone %d in %s", id, key)
	}
	return titles[id-1], nil
}

// Tag returns the commit a tag names, "" when there is none.
func (g *Git) Tag(owner, name, tag string) string {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.releaseState().tags[owner+"/"+name+"@"+tag]
}

// SetTag makes a tag as a person would.
func (g *Git) SetTag(owner, name, tag, commit string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.releaseState().tags[owner+"/"+name+"@"+tag] = commit
}

// MarkMilestoneClosed closes the milestone titled title, as a release did.
func (g *Git) MarkMilestoneClosed(owner, name, title string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.releaseState()
	s.closed[owner+"/"+name] = append(s.closed[owner+"/"+name], title)
}

// MilestoneClosed reports whether the milestone titled title was closed.
func (g *Git) MilestoneClosed(owner, name, title string) bool {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return slices.Contains(g.s.releaseState().closed[owner+"/"+name], title)
}

// FailCloses makes the next n CloseMilestone calls fail.
func (g *Git) FailCloses(n int) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.releaseState().closeFailures = n
}

// SetRelease makes the host's release of tag, with body and files by name,
// in the order given.
func (g *Git) SetRelease(owner, name, tag, body string, files ...ReleaseFile) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.releaseState()
	rel := &ports.Release{ID: int64(len(s.published) + 1), Tag: tag, Name: tag, Body: body}
	for i, f := range files {
		uuid := fmt.Sprintf("%s-%s-%s-%d", owner, name, tag, i)
		s.assets[uuid] = f.Data
		rel.Assets = append(rel.Assets, ports.ReleaseAsset{ID: int64(100 + i), Name: f.Name,
			Size: int64(len(f.Data)), UUID: uuid})
	}
	s.published[owner+"/"+name+"@"+tag] = rel
}

// ReleaseBody returns the notes of the host's release of tag.
func (g *Git) ReleaseBody(owner, name, tag string) string {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.releaseState().published[owner+"/"+name+"@"+tag].Body
}

// ReleaseFile is a file attached to a release.
type ReleaseFile struct {
	Name string
	Data []byte
}

// AllMilestones returns the milestones of owner/name, open and closed.
func (g *Git) AllMilestones(owner, name string) []ports.Milestone {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.milestones(owner+"/"+name, true)
}

// AddMilestone makes a milestone of owner/name as CreateMilestone would.
func (g *Git) AddMilestone(owner, name, title, description string, due time.Time) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	key := owner + "/" + name
	s := g.s.issueState()
	s.milestones[key] = append(s.milestones[key], title)
	id := int64(len(s.milestones[key]))
	g.s.releaseState().created[fmt.Sprintf("%s#%d", key, id)] = ports.Milestone{ID: id, Title: title,
		Description: description, Due: due}
}
