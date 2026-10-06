/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
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
	shas      int            // commits made other than the initial ones
	histories map[string]int // History calls per owner/name
	hangs     map[string]chan struct{}
	agent     *fakeAgentState
	issues    *fakeIssueState
	releases  *fakeReleaseState
}

type fakeRepo struct {
	commits []gitprovider.Commit // oldest first
	// files is main's tree. It is replaced, never changed in place, so a test
	// can keep reading the tree it got.
	files scaffold.Tree
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

// newSHA returns a fresh commit SHA, unlike the initial commits' ones.
func (g *fakeGit) newSHA() string {
	g.shas++
	return fmt.Sprintf("5a%038d", g.shas)
}

// ReadFiles reads main, or a branch the fake has: main's files with the
// edits PushBranch recorded for it, at the branch's head. A path ending in "/"
// reads every file below that directory.
func (p *fakeProvider) ReadFiles(_ context.Context, repo gitprovider.Repository, branch string,
	paths []string) (scaffold.Tree, string, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	key := repo.Owner + "/" + repo.Name
	r := p.git.repos[key]
	if r == nil || len(r.commits) == 0 {
		return nil, "", fmt.Errorf("clone %s at %s: branch not found", repo.CloneURL, branch)
	}
	tree, sha := r.files, r.commits[len(r.commits)-1].Hash
	if branch != gitprovider.DefaultBranch {
		s := p.git.agentState()
		head, ok := s.branches[key+"@"+branch]
		if !ok {
			return nil, "", fmt.Errorf("clone %s at %s: branch not found", repo.CloneURL, branch)
		}
		tree, sha = withEdits(r.files, s.edits[key+"@"+branch]), head
	}
	files := scaffold.Tree{}
	for _, filePath := range paths {
		if dir, ok := strings.CutSuffix(filePath, "/"); ok {
			for name, f := range tree {
				if strings.HasPrefix(name, dir+"/") {
					files[name] = f
				}
			}
			continue
		}
		if f, ok := tree[filePath]; ok {
			files[filePath] = f
		}
	}
	return files, sha, nil
}

// withEdits is files with edits applied: a path's new file, or nil to remove it.
func withEdits(files scaffold.Tree, edits map[string]*scaffold.File) scaffold.Tree {
	out := maps.Clone(files)
	if out == nil {
		out = scaffold.Tree{}
	}
	for filePath, f := range edits {
		if f == nil {
			delete(out, filePath)
		} else {
			out[filePath] = *f
		}
	}
	return out
}

// PushBranch records branch with what its commit changes on main, which a
// forced merge of its pull request applies. base must be a commit of main.
func (p *fakeProvider) PushBranch(_ context.Context, repo gitprovider.Repository, base, branch string,
	files scaffold.Tree, removes []string, _ string, _ gitprovider.Signature) (string, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	key := repo.Owner + "/" + repo.Name
	r := p.git.repos[key]
	if r == nil || !slices.ContainsFunc(r.commits, func(c gitprovider.Commit) bool { return c.Hash == base }) {
		return "", fmt.Errorf("find base %s on %s of %s: not found", base, gitprovider.DefaultBranch, key)
	}
	edits := map[string]*scaffold.File{}
	for _, filePath := range removes {
		if _, ok := r.files[filePath]; ok {
			edits[filePath] = nil
		}
	}
	for filePath, f := range files {
		if cur, ok := r.files[filePath]; !ok || cur.Executable != f.Executable || string(cur.Data) != string(f.Data) {
			edits[filePath] = &f
		}
	}
	if len(edits) == 0 {
		return "", fmt.Errorf("push %s: %w", branch, gitprovider.ErrEmptyChange)
	}
	sha := p.git.newSHA()
	s := p.git.agentState()
	s.branches[key+"@"+branch] = sha
	s.edits[key+"@"+branch] = edits
	return sha, nil
}

// PushDefaultBranch writes files to main as one commit, provided base is still
// main's head.
func (p *fakeProvider) PushDefaultBranch(_ context.Context, repo gitprovider.Repository, base string,
	files scaffold.Tree, removes []string, message string, _ gitprovider.Signature) (string, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	key := repo.Owner + "/" + repo.Name
	r := p.git.repos[key]
	if r == nil || len(r.commits) == 0 || r.commits[len(r.commits)-1].Hash != base {
		return "", fmt.Errorf("push %s of %s: %s is not its head", gitprovider.DefaultBranch, key, base)
	}
	edits := map[string]*scaffold.File{}
	for _, filePath := range removes {
		if _, ok := r.files[filePath]; ok {
			edits[filePath] = nil
		}
	}
	for filePath, f := range files {
		if cur, ok := r.files[filePath]; !ok || cur.Executable != f.Executable || string(cur.Data) != string(f.Data) {
			edits[filePath] = &f
		}
	}
	if len(edits) == 0 {
		return "", fmt.Errorf("push %s: %w", gitprovider.DefaultBranch, gitprovider.ErrEmptyChange)
	}
	p.git.applyToMain(key, edits, message)
	return r.commits[len(r.commits)-1].Hash, nil
}

// mainFiles returns main's tree in owner/name.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) mainFiles(owner, name string) scaffold.Tree {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.repos[owner+"/"+name].files
}

// mainHead returns the newest commit on main in owner/name.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) mainHead(owner, name string) gitprovider.Commit {
	g.mu.Lock()
	defer g.mu.Unlock()
	commits := g.repos[owner+"/"+name].commits
	return commits[len(commits)-1]
}

// commitToMain writes files to main as one commit, as a person's merged change would.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) commitToMain(owner, name string, files scaffold.Tree) {
	g.mu.Lock()
	defer g.mu.Unlock()
	edits := map[string]*scaffold.File{}
	for filePath, f := range files {
		edits[filePath] = &f
	}
	g.applyToMain(owner+"/"+name, edits, "A person's change")
}

// applyToMain makes one commit on main of owner/name that writes, or with nil
// removes, each path of edits.
func (g *fakeGit) applyToMain(key string, edits map[string]*scaffold.File, message string) {
	r := g.repos[key]
	r.files = withEdits(r.files, edits)
	r.commits = append(r.commits, gitprovider.Commit{Hash: g.newSHA(), Message: message})
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
	// edits are what the commit PushBranch made on a branch changes on main,
	// by owner/name@branch: a path's new file, or nil when it removes it.
	edits map[string]map[string]*scaffold.File
}

func (g *fakeGit) agentState() *fakeAgentState {
	if g.agent == nil {
		g.agent = &fakeAgentState{
			protected: map[string][]string{}, checks: map[string]gitprovider.CheckStatus{},
			rules: map[string]gitprovider.BranchRule{}, tags: map[string][]string{},
			agents: map[string]string{}, pulls: map[string][]*gitprovider.PullRequest{},
			comments: map[string][]string{}, branches: map[string]string{}, authors: map[string]string{},
			files: map[string][]string{}, forced: map[string][]int{},
			edits: map[string]map[string]*scaffold.File{},
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
	delete(g.agentState().edits, owner+"/"+name+"@"+branch)
}

// pushPlan pushes files as the plan of a Brief round, as the planner's run
// would: branch plan/<round> is main without its plan/ and with files instead.
// It returns the branch's head.
func (g *fakeGit) pushPlan(owner, name string, round int, files scaffold.Tree) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := owner + "/" + name
	edits := map[string]*scaffold.File{}
	if r := g.repos[key]; r != nil {
		for filePath := range r.files {
			if strings.HasPrefix(filePath, "plan/") {
				edits[filePath] = nil
			}
		}
	}
	for filePath, f := range files {
		edits[filePath] = &f
	}
	sha := g.newSHA()
	s := g.agentState()
	branch := model.PlanBranch(round)
	s.branches[key+"@"+branch] = sha
	s.edits[key+"@"+branch] = edits
	return sha
}

func (g *fakeGit) protectedBranches(owner, name string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.agentState().protected[owner+"/"+name]
}

// agentUser returns the bot user given write access to owner/name.
func (g *fakeGit) agentUser(owner, name string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.agentState().agents[owner+"/"+name]
}

// commitCount returns how many commits main of owner/name has; 0 when the
// repository doesn't exist.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) commitCount(owner, name string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r := g.repos[owner+"/"+name]; r != nil {
		return len(r.commits)
	}
	return 0
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
	return gitprovider.Credentials{Username: user, Token: rand.Text()}, nil
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
	headSHA, title, message string) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	return p.merge(repo, number, headSHA, title, message, false)
}

// ForceMergePullRequest merges whatever the pull request changes, and records it.
func (p *fakeProvider) ForceMergePullRequest(_ context.Context, repo gitprovider.Repository, number int,
	headSHA, title, message string) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	return p.merge(repo, number, headSHA, title, message, true)
}

// merge merges a pull request. One from a branch PushBranch made squashes its
// changes onto main as one commit and deletes the branch.
func (p *fakeProvider) merge(repo gitprovider.Repository, number int, headSHA, title, message string,
	force bool) error {
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
	if edits, ok := s.edits[key+"@"+pr.Head]; ok {
		p.git.applyToMain(key, edits, title+"\n\n"+message)
		delete(s.edits, key+"@"+pr.Head)
		delete(s.branches, key+"@"+pr.Head)
		commits := p.git.repos[key].commits
		pr.MergeCommit = commits[len(commits)-1].Hash
	} else {
		pr.MergeCommit = p.git.newSHA()
	}
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
	pr, err := p.pull(repo, number)
	if err != nil {
		return nil, err
	}
	s := p.git.agentState()
	if files, ok := s.files[fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, number)]; ok {
		return slices.Clone(files), nil
	}
	// A pull request from a branch PushBranch made changes what its commit does.
	return slices.Sorted(maps.Keys(s.edits[repo.Owner+"/"+repo.Name+"@"+pr.Head])), nil
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

func (p *fakeProvider) DeleteBranch(_ context.Context, repo gitprovider.Repository, branch string) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	delete(p.git.agentState().branches, repo.Owner+"/"+repo.Name+"@"+branch)
	delete(p.git.agentState().edits, repo.Owner+"/"+repo.Name+"@"+branch)
	return nil
}

// hasBranch reports whether the fake host has branch in owner/name.
//
//nolint:unparam // takes owner and name like the other helpers
func (g *fakeGit) hasBranch(owner, name, branch string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.agentState().branches[owner+"/"+name+"@"+branch]
	return ok
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

// The fake reads a plan branch as the planner leaves it: main with its plan/
// replaced, at the branch's head.
func TestFakeGitReadsAPlanBranch(t *testing.T) {
	g := &fakeGit{repos: map[string]*fakeRepo{}, histories: map[string]int{}, hangs: map[string]chan struct{}{}}
	p, _ := g.Provider(gitprovider.Config{URL: "http://gitea", Owner: "apps"})
	ctx := context.Background()
	repo, _ := p.EnsureRepository(ctx, "brief-cel-lens", "")
	if _, err := p.PushInitialCommit(ctx, repo, scaffold.Tree{
		"brief.md":       {Data: []byte("idea\n")},
		"plan/plan.yaml": {Data: []byte("round 1\n")},
		"plan/old.md":    {Data: []byte("gone in round 2\n")},
	}, "Workspace", gitprovider.Signature{}); err != nil {
		t.Fatal(err)
	}
	sha := g.pushPlan("apps", "brief-cel-lens", 2, scaffold.Tree{
		"plan/plan.yaml":         {Data: []byte("round 2\n")},
		"plan/issues/orders.yml": {Data: []byte("- title: x\n")},
	})

	files, at, err := p.ReadFiles(ctx, repo, "plan/2", []string{"plan/", "brief.md", "missing/"})
	if err != nil {
		t.Fatal(err)
	}
	want := scaffold.Tree{
		"brief.md":               {Data: []byte("idea\n")},
		"plan/plan.yaml":         {Data: []byte("round 2\n")},
		"plan/issues/orders.yml": {Data: []byte("- title: x\n")},
	}
	if at != sha || !maps.EqualFunc(files, want, func(a, b scaffold.File) bool { return string(a.Data) == string(b.Data) }) {
		t.Errorf("ReadFiles(plan/2) = %v at %s, want %v at %s", files, at, want, sha)
	}
	if main, _, _ := p.ReadFiles(ctx, repo, gitprovider.DefaultBranch, []string{"plan/"}); len(main) != 2 {
		t.Errorf("ReadFiles(main) = %v, want main's own plan/", main)
	}
	if _, _, err := p.ReadFiles(ctx, repo, "plan/3", []string{"plan/"}); err == nil {
		t.Error("ReadFiles() of a branch the fake doesn't have succeeded")
	}
}
