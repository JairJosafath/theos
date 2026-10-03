/*
Copyright 2026.

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

package controller

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// fakeGit is an in-memory Git host. Templates under .../templates/<name> are the
// real ones from the repository's templates/ folder, at the versions in
// templates/versions.txt, so these tests also prove the real templates render
// together without conflicts.
type fakeGit struct {
	mu        sync.Mutex
	templates map[string]scaffold.Tree // url@ref
	repos     map[string]*fakeRepo     // owner/name
	pushes    int
	histories map[string]int // History calls per owner/name
	hangs     map[string]chan struct{}
	agent     *fakeAgentState
	issues    *fakeIssueState
}

type fakeRepo struct {
	commits []gitprovider.Commit // oldest first
	files   scaffold.Tree
}

func newFakeGit(templatesDir string) (*fakeGit, error) {
	g := &fakeGit{templates: map[string]scaffold.Tree{}, repos: map[string]*fakeRepo{},
		histories: map[string]int{}, hangs: map[string]chan struct{}{}}
	versions, err := os.Open(filepath.Join(templatesDir, "versions.txt"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = versions.Close() }()
	scanner := bufio.NewScanner(versions)
	for scanner.Scan() {
		name, version, ok := strings.Cut(strings.TrimSpace(scanner.Text()), " ")
		if !ok || strings.HasPrefix(name, "#") {
			continue
		}
		tree, err := readDir(filepath.Join(templatesDir, name))
		if err != nil {
			return nil, err
		}
		g.templates[templatesURL+name+"@"+version] = tree
	}
	return g, scanner.Err()
}

func readDir(root string) (scaffold.Tree, error) {
	tree := scaffold.Tree{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		tree[filepath.ToSlash(rel)] = scaffold.File{Data: data, Executable: info.Mode()&0o111 != 0}
		return nil
	})
	return tree, err
}

// addTemplate registers an extra template for one test.
func (g *fakeGit) addTemplate(url, ref string, tree scaffold.Tree) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.templates[url+"@"+ref] = tree
}

// seedRepo creates a repository with one commit, as if someone else made it.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) seedRepo(owner, name, message string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.repos[owner+"/"+name] = &fakeRepo{commits: []gitprovider.Commit{{Hash: "c0ffee", Message: message}}}
}

// clearRepo removes a repository's commits, as someone fixing it by hand would.
func (g *fakeGit) clearRepo(owner, name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.repos[owner+"/"+name] = &fakeRepo{}
}

// hang makes reading a repository's history wait until the caller gives up, as
// an unresponsive host would, until release is called.
func (g *fakeGit) hang(owner, name string) (release func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ch := make(chan struct{})
	g.hangs[owner+"/"+name] = ch
	return sync.OnceFunc(func() { close(ch) })
}

func (g *fakeGit) hanging(key string) <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hangs[key]
}

// historyCount is how often the operator has read a repository's history.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) historyCount(owner, name string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.histories[owner+"/"+name]
}

func (g *fakeGit) repo(owner, name string) *fakeRepo {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.repos[owner+"/"+name]
}

func (g *fakeGit) pushCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pushes
}

func (g *fakeGit) FetchTemplate(_ context.Context, url, ref string, _ *gitprovider.Credentials) (scaffold.Tree, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	tree, ok := g.templates[url+"@"+ref]
	if !ok {
		return nil, fmt.Errorf("%w: %s not found in %s", gitprovider.ErrUnpinnedRef, ref, url)
	}
	return tree, nil
}

func (g *fakeGit) Provider(cfg gitprovider.Config) (gitprovider.Provider, error) {
	return &fakeProvider{git: g, cfg: cfg}, nil
}

type fakeProvider struct {
	git *fakeGit
	cfg gitprovider.Config
}

func (p *fakeProvider) EnsureRepository(_ context.Context, name, _ string) (gitprovider.Repository, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	key := p.cfg.Owner + "/" + name
	if _, ok := p.git.repos[key]; !ok {
		p.git.repos[key] = &fakeRepo{}
	}
	web := strings.TrimSuffix(p.cfg.URL, "/") + "/" + path.Join(p.cfg.Owner, name)
	return gitprovider.Repository{Owner: p.cfg.Owner, Name: name, WebURL: web, CloneURL: web + ".git"}, nil
}

func (p *fakeProvider) History(ctx context.Context, repo gitprovider.Repository, limit int) ([]gitprovider.Commit, error) {
	if released := p.git.hanging(repo.Owner + "/" + repo.Name); released != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-released:
		}
	}
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	p.git.histories[repo.Owner+"/"+repo.Name]++
	r := p.git.repos[repo.Owner+"/"+repo.Name]
	var out []gitprovider.Commit
	for i := len(r.commits) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, r.commits[i])
	}
	return out, nil
}

func (p *fakeProvider) PushInitialCommit(_ context.Context, repo gitprovider.Repository, files scaffold.Tree,
	message string, _ gitprovider.Signature) (string, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	r := p.git.repos[repo.Owner+"/"+repo.Name]
	if len(r.commits) > 0 {
		return "", fmt.Errorf("push %s: branch main already exists", repo.CloneURL)
	}
	p.git.pushes++
	hash := fmt.Sprintf("%040d", p.git.pushes)
	r.commits = append(r.commits, gitprovider.Commit{Hash: hash, Message: message})
	r.files = files
	return hash, nil
}

// fakeAgentState is what the fake host keeps for the agent loop: protected
// branches, check states, bot users and pull requests, per owner/name.
type fakeAgentState struct {
	protected map[string][]string                   // owner/name -> branches
	rules     map[string]gitprovider.BranchRule     // owner/name -> rule of the default branch
	tags      map[string][]string                   // owner/name -> protected tag patterns
	checks    map[string]gitprovider.CheckStatus    // owner/name@ref
	agents    map[string]string                     // owner/name -> bot user
	pulls     map[string][]*gitprovider.PullRequest // owner/name
	comments  map[string][]string                   // owner/name#number
	branches  map[string]string                     // owner/name@branch -> head sha
	authors   map[string]string                     // owner/name#number -> who opened it
	log       string                                // served for every CI job
	files     map[string][]string                   // owner/name#number -> paths the pull request changes
	forced    map[string][]int                      // owner/name -> pull requests force-merged
}

func (g *fakeGit) agentState() *fakeAgentState {
	if g.agent == nil {
		g.agent = &fakeAgentState{
			protected: map[string][]string{}, checks: map[string]gitprovider.CheckStatus{},
			rules: map[string]gitprovider.BranchRule{}, tags: map[string][]string{},
			agents: map[string]string{}, pulls: map[string][]*gitprovider.PullRequest{},
			comments: map[string][]string{}, branches: map[string]string{}, authors: map[string]string{},
			files: map[string][]string{}, forced: map[string][]int{},
		}
	}
	return g.agent
}

// setChecks sets the combined check state of ref, a branch name or a commit, as a
// Standard's CI reports it: one check, under the context main requires.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) setChecks(owner, name, ref, state string) {
	g.setCheckContexts(owner, name, ref, state, map[string]string{"check / check (pull_request)": state})
}

// setCheckContexts sets the combined check state of ref and the state of each
// check by its context.
func (g *fakeGit) setCheckContexts(owner, name, ref, state string, contexts map[string]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.agentState().checks[owner+"/"+name+"@"+ref] = gitprovider.CheckStatus{
		Commit: ref, State: state, URL: "http://gitea/" + owner + "/" + name + "/actions/runs/1", Contexts: contexts,
	}
}

// pushBranch records a branch head, as the sandbox's push would.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) pushBranch(owner, name, branch, sha string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.agentState().branches[owner+"/"+name+"@"+branch] = sha
}

func (g *fakeGit) protectedBranches(owner, name string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.agentState().protected[owner+"/"+name]
}

// branchRule returns the default branch's rule and the protected tag patterns.
func (g *fakeGit) branchRule(owner, name string) (gitprovider.BranchRule, []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.agentState()
	return s.rules[owner+"/"+name], s.tags[owner+"/"+name]
}

// closePull closes a pull request without merging it, as a person might.
func (g *fakeGit) closePull(owner, name string, number int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, pr := range g.agentState().pulls[owner+"/"+name] {
		if pr.Number == number {
			pr.State = "closed"
		}
	}
}

//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) pullRequests(owner, name string) []gitprovider.PullRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	pulls := g.agentState().pulls[owner+"/"+name]
	out := make([]gitprovider.PullRequest, 0, len(pulls))
	for _, p := range pulls {
		out = append(out, *p)
	}
	return out
}

func (p *fakeProvider) Repository(name string) gitprovider.Repository {
	web := strings.TrimSuffix(p.cfg.URL, "/") + "/" + path.Join(p.cfg.Owner, name)
	return gitprovider.Repository{Owner: p.cfg.Owner, Name: name, WebURL: web, CloneURL: web + ".git"}
}

func (p *fakeProvider) ProtectBranch(_ context.Context, repo gitprovider.Repository, branch string,
	rule gitprovider.BranchRule) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	s := p.git.agentState()
	key := repo.Owner + "/" + repo.Name
	if !slices.Contains(s.protected[key], branch) {
		s.protected[key] = append(s.protected[key], branch)
	}
	s.rules[key] = rule
	return nil
}

func (p *fakeProvider) ProtectTags(_ context.Context, repo gitprovider.Repository, pattern string) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	s := p.git.agentState()
	key := repo.Owner + "/" + repo.Name
	if !slices.Contains(s.tags[key], pattern) {
		s.tags[key] = append(s.tags[key], pattern)
	}
	return nil
}

func (p *fakeProvider) CombinedStatus(_ context.Context, repo gitprovider.Repository, ref string) (gitprovider.CheckStatus, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	s := p.git.agentState()
	if status, ok := s.checks[repo.Owner+"/"+repo.Name+"@"+ref]; ok {
		return status, nil
	}
	return gitprovider.CheckStatus{Commit: ref}, nil
}

func (p *fakeProvider) EnsureAgentAccess(_ context.Context, repo gitprovider.Repository, user string) (gitprovider.Credentials, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	p.git.agentState().agents[repo.Owner+"/"+repo.Name] = user
	return gitprovider.Credentials{Username: user, Token: "b0t-" + user}, nil
}

func (p *fakeProvider) OpenPullRequest(_ context.Context, repo gitprovider.Repository, head, _, _, _ string) (gitprovider.PullRequest, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	s := p.git.agentState()
	key := repo.Owner + "/" + repo.Name
	sha, ok := s.branches[key+"@"+head]
	if !ok {
		return gitprovider.PullRequest{}, fmt.Errorf("branch %s does not exist", head)
	}
	for _, pr := range s.pulls[key] {
		if pr.Head == head && pr.State == "open" {
			pr.HeadSHA = sha
			return *pr, nil
		}
	}
	pr := &gitprovider.PullRequest{Number: len(s.pulls[key]) + 1, Head: head, HeadSHA: sha, State: "open", Mergeable: true}
	pr.URL = fmt.Sprintf("%s/pulls/%d", strings.TrimSuffix(p.cfg.URL, "/")+"/"+key, pr.Number)
	s.pulls[key] = append(s.pulls[key], pr)
	s.authors[fmt.Sprintf("%s#%d", key, pr.Number)] = p.cfg.Credentials.Username
	return *pr, nil
}

func (p *fakeProvider) pull(repo gitprovider.Repository, number int) (*gitprovider.PullRequest, error) {
	s := p.git.agentState()
	key := repo.Owner + "/" + repo.Name
	if number < 1 || number > len(s.pulls[key]) {
		return nil, fmt.Errorf("pull request %d of %s not found", number, key)
	}
	pr := s.pulls[key][number-1]
	if sha, ok := s.branches[key+"@"+pr.Head]; ok && !pr.Merged {
		pr.HeadSHA = sha
	}
	return pr, nil
}

func (p *fakeProvider) GetPullRequest(_ context.Context, repo gitprovider.Repository, number int) (gitprovider.PullRequest, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	pr, err := p.pull(repo, number)
	if err != nil {
		return gitprovider.PullRequest{}, err
	}
	return *pr, nil
}

func (p *fakeProvider) CommentPullRequest(_ context.Context, repo gitprovider.Repository, number int, body string) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	key := fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, number)
	s := p.git.agentState()
	s.comments[key] = append(s.comments[key], body)
	return nil
}

// MergePullRequest refuses a pull request that changes a protected path, as Gitea does.
func (p *fakeProvider) MergePullRequest(_ context.Context, repo gitprovider.Repository, number int,
	headSHA, _, _ string) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	return p.merge(repo, number, headSHA, false)
}

// ForceMergePullRequest merges whatever the pull request changes, and records it.
func (p *fakeProvider) ForceMergePullRequest(_ context.Context, repo gitprovider.Repository, number int,
	headSHA, _, _ string) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	return p.merge(repo, number, headSHA, true)
}

func (p *fakeProvider) merge(repo gitprovider.Repository, number int, headSHA string, force bool) error {
	pr, err := p.pull(repo, number)
	if err != nil {
		return err
	}
	if pr.Merged {
		return fmt.Errorf("pull request %d is merged already", number)
	}
	if pr.HeadSHA != headSHA {
		return fmt.Errorf("pull request %d moved to %s since %s", number, pr.HeadSHA, headSHA)
	}
	s := p.git.agentState()
	key := repo.Owner + "/" + repo.Name
	if force {
		s.forced[key] = append(s.forced[key], number)
	} else if touchesProtected(s.files[fmt.Sprintf("%s#%d", key, number)], s.rules[key].ProtectedPaths) {
		return fmt.Errorf("pull request %d changes protected files", number)
	}
	pr.Merged, pr.State = true, "closed"
	return nil
}

func touchesProtected(files, protected []string) bool {
	return slices.ContainsFunc(files, func(f string) bool {
		return slices.ContainsFunc(protected, func(p string) bool {
			return f == p || strings.HasSuffix(p, "/") && strings.HasPrefix(f, p)
		})
	})
}

// setPullFiles sets the paths a pull request changes.
func (g *fakeGit) setPullFiles(owner, name string, number int, files ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.agentState().files[fmt.Sprintf("%s/%s#%d", owner, name, number)] = files
}

// forcedMerges lists the pull requests merged with ForceMergePullRequest.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) forcedMerges(owner, name string) []int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.agentState().forced[owner+"/"+name])
}

func (p *fakeProvider) PullRequestFiles(_ context.Context, repo gitprovider.Repository, number int) ([]string, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	if _, err := p.pull(repo, number); err != nil {
		return nil, err
	}
	return slices.Clone(p.git.agentState().files[fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, number)]), nil
}

func (p *fakeProvider) ClosePullRequest(_ context.Context, repo gitprovider.Repository, number int) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	pr, err := p.pull(repo, number)
	if err != nil {
		return err
	}
	pr.State = "closed"
	return nil
}

// setCheckLog sets the log the fake host serves for every CI job.
func (g *fakeGit) setCheckLog(log string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.agentState().log = log
}

func (p *fakeProvider) CheckLog(_ context.Context, _ gitprovider.Repository, _ string) (string, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	return p.git.agentState().log, nil
}
