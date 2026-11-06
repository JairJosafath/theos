package fake

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// The fake host's pull requests: opening, reading, commenting, merging and
// updating them, and what a test sets or reads of them.

// ClosePull closes a pull request without merging it, as a person might.
func (g *Git) ClosePull(owner, name string, number int) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	for _, pr := range g.s.agentState().pulls[owner+"/"+name] {
		if pr.Number == number {
			pr.State = stateClosed
		}
	}
}

// DeletePull makes a pull request gone, as deleting it or making the
// repository again would: reading it answers 404.
func (g *Git) DeletePull(owner, name string, number int) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.agentState().gone[fmt.Sprintf("%s/%s#%d", owner, name, number)] = true
}

// FailPullReads makes the next n reads of a pull request fail, as a host
// that doesn't answer would.
func (g *Git) FailPullReads(owner, name string, number, n int) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.agentState().readErrs[fmt.Sprintf("%s/%s#%d", owner, name, number)] = n
}

// PullReadsToFail is how many of a pull request's reads are still to fail.
func (g *Git) PullReadsToFail(owner, name string, number int) int {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.agentState().readErrs[fmt.Sprintf("%s/%s#%d", owner, name, number)]
}

// PullText is the title and body pull request number was opened with.
func (g *Git) PullText(owner, name string, number int) (title, body string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	text := g.s.agentState().texts[fmt.Sprintf("%s/%s#%d", owner, name, number)]
	return text[0], text[1]
}

// PullRequests returns a copy of each pull request of owner/name, by number.
func (g *Git) PullRequests(owner, name string) []ports.PullRequest {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	pulls := g.s.agentState().pulls[owner+"/"+name]
	out := make([]ports.PullRequest, 0, len(pulls))
	for _, p := range pulls {
		out = append(out, *p)
	}
	return out
}

// PullComments are the comments on pull request number of owner/name.
func (g *Git) PullComments(owner, name string, number int) []string {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return slices.Clone(g.s.agentState().comments[fmt.Sprintf("%s/%s#%d", owner, name, number)])
}

func (g *Git) OpenPullRequest(_ context.Context, repo ports.Repository, head, _, title, body string) (ports.PullRequest, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.agentState()
	key := repo.Owner + "/" + repo.Name
	sha, ok := s.branches[key+"@"+head]
	if !ok {
		return ports.PullRequest{}, fmt.Errorf("branch %s does not exist", head)
	}
	for _, pr := range s.pulls[key] {
		if pr.Head == head && pr.State == stateOpen {
			pr.HeadSHA = sha
			return *pr, nil
		}
	}
	pr := &ports.PullRequest{Number: len(s.pulls[key]) + 1, Head: head, HeadSHA: sha, State: stateOpen, Mergeable: true}
	pr.URL = fmt.Sprintf("%s/pulls/%d", strings.TrimSuffix(g.cfg.URL, "/")+"/"+key, pr.Number)
	s.pulls[key] = append(s.pulls[key], pr)
	s.authors[fmt.Sprintf("%s#%d", key, pr.Number)] = g.cfg.Credentials.Username
	s.texts[fmt.Sprintf("%s#%d", key, pr.Number)] = [2]string{title, body}
	return *pr, nil
}

func (g *Git) pull(repo ports.Repository, number int) (*ports.PullRequest, error) {
	s := g.s.agentState()
	key := repo.Owner + "/" + repo.Name
	if number < 1 || number > len(s.pulls[key]) || s.gone[fmt.Sprintf("%s#%d", key, number)] {
		return nil, fmt.Errorf("pull request %d of %s: %w", number, key, ports.ErrPullRequestNotFound)
	}
	pr := s.pulls[key][number-1]
	if sha, ok := s.branches[key+"@"+pr.Head]; ok && !pr.Merged {
		pr.HeadSHA = sha
	}
	return pr, nil
}

func (g *Git) GetPullRequest(_ context.Context, repo ports.Repository, number int) (ports.PullRequest, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	key := fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, number)
	if s := g.s.agentState(); s.readErrs[key] > 0 {
		s.readErrs[key]--
		return ports.PullRequest{}, fmt.Errorf("reading pull request %d: connection reset", number)
	}
	pr, err := g.pull(repo, number)
	if err != nil {
		return ports.PullRequest{}, err
	}
	read := *pr
	if g.s.agentState().unnamed[repo.Owner+"/"+repo.Name] {
		read.MergeCommit = ""
	}
	return read, nil
}

func (g *Git) CommentPullRequest(_ context.Context, repo ports.Repository, number int, body string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	key := fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, number)
	s := g.s.agentState()
	s.comments[key] = append(s.comments[key], body)
	return nil
}

// MergePullRequest refuses a pull request that changes a protected path, as Gitea does.
func (g *Git) MergePullRequest(_ context.Context, repo ports.Repository, number int,
	headSHA, title, message string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.merge(repo, number, headSHA, title, message, false)
}

// ForceMergePullRequest merges whatever the pull request changes, and records it.
func (g *Git) ForceMergePullRequest(_ context.Context, repo ports.Repository, number int,
	headSHA, title, message string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.merge(repo, number, headSHA, title, message, true)
}

// merge merges a pull request. One from a branch PushBranch made squashes its
// changes onto main as one commit and deletes the branch.
func (g *Git) merge(repo ports.Repository, number int, headSHA, title, message string,
	force bool) error {
	pr, err := g.pull(repo, number)
	if err != nil {
		return err
	}
	if pr.Merged {
		return fmt.Errorf("pull request %d is merged already", number)
	}
	if pr.HeadSHA != headSHA {
		return fmt.Errorf("pull request %d moved to %s since %s", number, pr.HeadSHA, headSHA)
	}
	s := g.s.agentState()
	key := repo.Owner + "/" + repo.Name
	if force {
		s.forced[key] = append(s.forced[key], number)
	} else if touchesProtected(s.pullFiles(key, number, pr.Head), s.rules[key].ProtectedPaths) {
		return fmt.Errorf("pull request %d changes protected files", number)
	}
	pr.Merged, pr.State = true, stateClosed
	if edits, ok := s.edits[key+"@"+pr.Head]; ok {
		g.s.applyToMain(key, edits, title+"\n\n"+message)
		delete(s.edits, key+"@"+pr.Head)
		delete(s.branches, key+"@"+pr.Head)
		commits := g.s.repos[key].commits
		pr.MergeCommit = commits[len(commits)-1].Hash
	} else {
		pr.MergeCommit = g.s.newSHA()
		s.trees[key+"@"+pr.MergeCommit] = treeID(g.s.repos[key].files)
	}
	return nil
}

// UpdatePullRequest merges main into a pull request's branch: a new head that
// is no longer behind, or ErrConflict when the pull request conflicts.
func (g *Git) UpdatePullRequest(_ context.Context, repo ports.Repository, number int) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	pr, err := g.pull(repo, number)
	if err != nil {
		return err
	}
	s := g.s.agentState()
	key := repo.Owner + "/" + repo.Name
	if s.conflicts[fmt.Sprintf("%s#%d", key, number)] || g.s.conflicts(key, pr.Head) {
		return fmt.Errorf("pull request %d: %w", number, ports.ErrConflict)
	}
	pr.HeadSHA, pr.Behind = g.s.newSHA(), false
	s.branches[key+"@"+pr.Head] = pr.HeadSHA
	if r := g.s.repos[key]; r != nil && len(r.commits) > 0 {
		if _, ok := s.bases[key+"@"+pr.Head]; ok {
			s.bases[key+"@"+pr.Head] = r.commits[len(r.commits)-1].Hash
		}
		g.s.commitTrees[key+"@"+pr.HeadSHA] = withEdits(r.files, s.edits[key+"@"+pr.Head])
	}
	return nil
}

// conflicts reports whether a branch PushBranch made edits a path main
// changed otherwise since the branch's base, as merging main into it would.
func (s *state) conflicts(key, branch string) bool {
	agent := s.agentState()
	base, ok := agent.bases[key+"@"+branch]
	r := s.repos[key]
	if !ok || r == nil {
		return false
	}
	// A base whose tree the fake never recorded, a seeded commit, is taken
	// for main as it is now.
	was, ok := s.commitTrees[key+"@"+base]
	if !ok {
		return false
	}
	for filePath, edit := range agent.edits[key+"@"+branch] {
		then, hadThen := was[filePath]
		now, hasNow := r.files[filePath]
		if hadThen == hasNow && sameFile(then, now) {
			continue
		}
		if edit == nil && !hasNow || edit != nil && hasNow && sameFile(*edit, now) {
			continue
		}
		return true
	}
	return false
}

func sameFile(a, b scaffold.File) bool {
	return a.Executable == b.Executable && string(a.Data) == string(b.Data)
}

func touchesProtected(files, protected []string) bool {
	return slices.ContainsFunc(files, func(f string) bool {
		return slices.ContainsFunc(protected, func(p string) bool {
			return f == p || strings.HasSuffix(p, "/") && strings.HasPrefix(f, p)
		})
	})
}

// SetPullFiles sets the paths a pull request changes.
func (g *Git) SetPullFiles(owner, name string, number int, files ...string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.agentState().files[fmt.Sprintf("%s/%s#%d", owner, name, number)] = files
}

// ForcedMerges lists the pull requests merged with ForceMergePullRequest.
func (g *Git) ForcedMerges(owner, name string) []int {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return slices.Clone(g.s.agentState().forced[owner+"/"+name])
}

func (g *Git) PullRequestFiles(_ context.Context, repo ports.Repository, number int) ([]string, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	pr, err := g.pull(repo, number)
	if err != nil {
		return nil, err
	}
	return g.s.agentState().pullFiles(repo.Owner+"/"+repo.Name, number, pr.Head), nil
}

// pullFiles are the paths pull request number of owner/name changes: those
// SetPullFiles set, else those the commit PushBranch made on its head does.
func (s *agentStore) pullFiles(key string, number int, head string) []string {
	if files, ok := s.files[fmt.Sprintf("%s#%d", key, number)]; ok {
		return slices.Clone(files)
	}
	return slices.Sorted(maps.Keys(s.edits[key+"@"+head]))
}

// SetPullCommits sets a pull request's commits, as PullRequestCommits
// returns them.
func (g *Git) SetPullCommits(owner, name string, number int, commits ...ports.PullCommit) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.agentState().commits[fmt.Sprintf("%s/%s#%d", owner, name, number)] = commits
}

// SetPullSize sets the size of a pull request's change.
func (g *Git) SetPullSize(owner, name string, number, additions, deletions, files int) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	for _, pr := range g.s.agentState().pulls[owner+"/"+name] {
		if pr.Number == number {
			pr.Additions, pr.Deletions, pr.ChangedFiles = additions, deletions, files
		}
	}
}

// PullRequestCommits returns what SetPullCommits set; none by default.
func (g *Git) PullRequestCommits(_ context.Context, repo ports.Repository, number int) ([]ports.PullCommit, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	if _, err := g.pull(repo, number); err != nil {
		return nil, err
	}
	return slices.Clone(g.s.agentState().commits[fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, number)]), nil
}

func (g *Git) ClosePullRequest(_ context.Context, repo ports.Repository, number int) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	pr, err := g.pull(repo, number)
	if err != nil {
		return err
	}
	pr.State = stateClosed
	return nil
}
