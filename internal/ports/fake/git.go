package fake

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// TemplatesURL is where NewGit serves the templates of the folder it reads:
// template <name> at TemplatesURL+<name>.
const TemplatesURL = "http://gitea.gitea.svc.cluster.local:3000/templates/"

// seedHash is the commit SeedRepo and its kin make, as someone else's.
const seedHash = "c0ffee"

// The states of an issue or a pull request.
const (
	stateOpen   = "open"
	stateClosed = "closed"
)

// Git is an in-memory Git host: every Git host port, its optional
// capabilities, and the templates GitClient fetches. NewGit serves the real
// templates of a folder, at the versions in its versions.txt, so the
// controllers' tests also prove the real templates render together without
// conflicts.
//
// A Git is one host's state seen as one GitProvider: For gives the view of
// another Config on the same state. The exported methods that are no port's
// set and read that state for a test.
type Git struct {
	s   *state
	cfg ports.Config
}

var (
	_ ports.GitHost      = (*Git)(nil)
	_ ports.Issues       = (*Git)(nil)
	_ ports.CI           = (*Git)(nil)
	_ ports.AgentAccess  = (*Git)(nil)
	_ ports.Registry     = (*Git)(nil)
	_ ports.OwnerRunners = (*Git)(nil)
	_ ports.OwnerActions = (*Git)(nil)
)

// state is what every view of one Git shares.
type state struct {
	mu        sync.Mutex
	templates map[string]scaffold.Tree // url@ref
	repos     map[string]*repository   // owner/name
	pushes    int
	shas      int            // commits made other than the initial ones
	histories map[string]int // History calls per owner/name
	hangs     map[string]chan struct{}
	agent     *agentStore
	issues    *issueStore
	releases  *releaseStore
	runners   *runnerStore
	actions   *actionsStore
	// copyImage copies an image for CopyImage, nil to only record the copy.
	copyImage ImageCopier
}

type repository struct {
	commits []ports.Commit // oldest first
	// files is main's tree. It is replaced, never changed in place, so a test
	// can keep reading the tree it got.
	files scaffold.Tree
	// branch is the name of the only branch when it is not main (#1908):
	// History reads it, as the real provider falls back to another branch,
	// and ReadFiles finds no main. Empty means main.
	branch string
}

// Repo is what a repository holds on main, as Repo reads it.
type Repo struct {
	// Commits are main's commits, oldest first.
	Commits []ports.Commit
	// Files is main's tree.
	Files scaffold.Tree
}

// New returns an empty Git host with no templates, seen with the zero Config.
func New() *Git {
	return &Git{s: &state{templates: map[string]scaffold.Tree{}, repos: map[string]*repository{},
		histories: map[string]int{}, hangs: map[string]chan struct{}{}}}
}

// NewGit returns an empty Git host serving the templates of templatesDir at
// TemplatesURL, each at the version templatesDir/versions.txt names.
func NewGit(templatesDir string) (*Git, error) {
	g := New()
	versions, err := os.Open(filepath.Join(templatesDir, "versions.txt")) //nolint:gosec // the folder a test names
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
		g.s.templates[TemplatesURL+name+"@"+version] = tree
	}
	return g, scanner.Err()
}

// For returns the view of the host a GitProvider with cfg has: its owner,
// URL, credentials and package settings, on the same state.
func (g *Git) For(cfg ports.Config) *Git {
	return &Git{s: g.s, cfg: cfg}
}

func readDir(root string) (scaffold.Tree, error) {
	tree := scaffold.Tree{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p) //nolint:gosec // a file of the folder a test names
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

// AddTemplate registers an extra template for one test.
func (g *Git) AddTemplate(url, ref string, tree scaffold.Tree) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.templates[url+"@"+ref] = tree
}

// DropTemplate removes the template at url and ref, as a host that lost it
// would: fetching it fails until it is added again.
func (g *Git) DropTemplate(url, ref string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	delete(g.s.templates, url+"@"+ref)
}

// LeaveMergeCommitsUnnamed makes reading a pull request of owner/name leave
// out the commit its merge made, as a host that doesn't say would.
func (g *Git) LeaveMergeCommitsUnnamed(owner, name string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.agentState().unnamed[owner+"/"+name] = true
}

// SeedRepo creates a repository with one commit, as if someone else made it.
func (g *Git) SeedRepo(owner, name, message string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.repos[owner+"/"+name] = &repository{commits: []ports.Commit{{Hash: seedHash, Message: message}}}
}

// SeedRepoFiles creates a repository with one commit, as if someone else made
// it, whose main holds files.
func (g *Git) SeedRepoFiles(owner, name, message string, files scaffold.Tree) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.repos[owner+"/"+name] = &repository{commits: []ports.Commit{{Hash: seedHash, Message: message}},
		files: maps.Clone(files)}
}

// SeedRepoBranch creates a repository with one commit on branch, not main,
// holding files, as an import whose default branch kept another name (#1908).
func (g *Git) SeedRepoBranch(owner, name, branch, message string, files scaffold.Tree) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.repos[owner+"/"+name] = &repository{commits: []ports.Commit{{Hash: seedHash, Message: message}},
		files: maps.Clone(files), branch: branch}
}

// ClearRepo removes a repository's commits, as someone fixing it by hand would.
func (g *Git) ClearRepo(owner, name string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.repos[owner+"/"+name] = &repository{}
}

// Hang makes reading a repository's history wait until the caller gives up, as
// an unresponsive host would, until release is called.
func (g *Git) Hang(owner, name string) (release func()) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	ch := make(chan struct{})
	g.s.hangs[owner+"/"+name] = ch
	return sync.OnceFunc(func() { close(ch) })
}

func (s *state) hanging(key string) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hangs[key]
}

// HistoryCount is how often the operator has read a repository's history.
func (g *Git) HistoryCount(owner, name string) int {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.histories[owner+"/"+name]
}

// Repo returns what owner/name holds on main, the zero Repo when there is no
// such repository. Its tree is the one main has now, which a later commit
// replaces rather than changes.
func (g *Git) Repo(owner, name string) Repo {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	r := g.s.repos[owner+"/"+name]
	if r == nil {
		return Repo{}
	}
	return Repo{Commits: slices.Clone(r.commits), Files: r.files}
}

// PushCount is how many initial commits the host took.
func (g *Git) PushCount() int {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.pushes
}

// FetchTemplate returns the template registered at url and ref, or
// ports.ErrUnpinnedRef.
func (g *Git) FetchTemplate(_ context.Context, url, ref string, _ *ports.Credentials) (scaffold.Tree, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	tree, ok := g.s.templates[url+"@"+ref]
	if !ok {
		return nil, fmt.Errorf("%w: %s not found in %s", ports.ErrUnpinnedRef, ref, url)
	}
	return tree, nil
}

func (g *Git) EnsureRepository(_ context.Context, name, _ string) (ports.Repository, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	key := g.cfg.Owner + "/" + name
	if _, ok := g.s.repos[key]; !ok {
		g.s.repos[key] = &repository{}
	}
	return g.Repository(name), nil
}

func (g *Git) History(ctx context.Context, repo ports.Repository, limit int) ([]ports.Commit, error) {
	if released := g.s.hanging(repo.Owner + "/" + repo.Name); released != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-released:
		}
	}
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.histories[repo.Owner+"/"+repo.Name]++
	r := g.s.repos[repo.Owner+"/"+repo.Name]
	var out []ports.Commit
	for i := len(r.commits) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, r.commits[i])
	}
	return out, nil
}

func (g *Git) PushInitialCommit(_ context.Context, repo ports.Repository, files scaffold.Tree,
	message string, _ ports.Signature) (string, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	r := g.s.repos[repo.Owner+"/"+repo.Name]
	if len(r.commits) > 0 {
		return "", fmt.Errorf("push %s: branch main already exists", repo.CloneURL)
	}
	g.s.pushes++
	hash := fmt.Sprintf("%040d", g.s.pushes)
	r.commits = append(r.commits, ports.Commit{Hash: hash, Message: message})
	r.files = files
	return hash, nil
}

// newSHA returns a fresh commit SHA, unlike the initial commits' ones.
func (s *state) newSHA() string {
	s.shas++
	return fmt.Sprintf("5a%038d", s.shas)
}

// ReadFiles reads main, or a branch the fake has: main's files with the
// edits PushBranch recorded for it, at the branch's head. A path ending in "/"
// reads every file below that directory. A branch the fake doesn't have, main
// of a repository seeded on another branch included, is ErrBranchNotFound.
func (g *Git) ReadFiles(_ context.Context, repo ports.Repository, branch string,
	paths []string) (scaffold.Tree, string, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	key := repo.Owner + "/" + repo.Name
	r := g.s.repos[key]
	if r == nil || len(r.commits) == 0 {
		return nil, "", fmt.Errorf("clone %s at %s: %w", repo.CloneURL, branch, ports.ErrBranchNotFound)
	}
	tree, sha := r.files, r.commits[len(r.commits)-1].Hash
	if branch == ports.DefaultBranch && r.branch != "" {
		return nil, "", fmt.Errorf("clone %s at %s: %w", repo.CloneURL, branch, ports.ErrBranchNotFound)
	}
	if branch != ports.DefaultBranch && branch != r.branch {
		s := g.s.agentState()
		head, ok := s.branches[key+"@"+branch]
		if !ok {
			return nil, "", fmt.Errorf("clone %s at %s: %w", repo.CloneURL, branch, ports.ErrBranchNotFound)
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
func (g *Git) PushBranch(_ context.Context, repo ports.Repository, base, branch string,
	files scaffold.Tree, removes []string, _ string, _ ports.Signature) (string, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	key := repo.Owner + "/" + repo.Name
	r := g.s.repos[key]
	if r == nil || !slices.ContainsFunc(r.commits, func(c ports.Commit) bool { return c.Hash == base }) {
		return "", fmt.Errorf("find base %s on %s of %s: not found", base, ports.DefaultBranch, key)
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
		return "", fmt.Errorf("push %s: %w", branch, ports.ErrEmptyChange)
	}
	sha := g.s.newSHA()
	s := g.s.agentState()
	s.branches[key+"@"+branch] = sha
	s.edits[key+"@"+branch] = edits
	return sha, nil
}

// PushDefaultBranch writes files to main as one commit, provided base is still
// main's head.
func (g *Git) PushDefaultBranch(_ context.Context, repo ports.Repository, base string,
	files scaffold.Tree, removes []string, message string, _ ports.Signature) (string, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	key := repo.Owner + "/" + repo.Name
	r := g.s.repos[key]
	if r == nil || len(r.commits) == 0 || r.commits[len(r.commits)-1].Hash != base {
		return "", fmt.Errorf("push %s of %s: %s is not its head", ports.DefaultBranch, key, base)
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
		return "", fmt.Errorf("push %s: %w", ports.DefaultBranch, ports.ErrEmptyChange)
	}
	g.s.applyToMain(key, edits, message)
	return r.commits[len(r.commits)-1].Hash, nil
}

// MainFiles returns main's tree in owner/name.
func (g *Git) MainFiles(owner, name string) scaffold.Tree {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.repos[owner+"/"+name].files
}

// MainHead returns the newest commit on main in owner/name.
func (g *Git) MainHead(owner, name string) ports.Commit {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	commits := g.s.repos[owner+"/"+name].commits
	return commits[len(commits)-1]
}

// CommitToMain writes files to main as one commit, as a person's merged change would.
func (g *Git) CommitToMain(owner, name string, files scaffold.Tree) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	edits := map[string]*scaffold.File{}
	for filePath, f := range files {
		edits[filePath] = &f
	}
	g.s.applyToMain(owner+"/"+name, edits, "A person's change")
}

// applyToMain makes one commit on main of owner/name that writes, or with nil
// removes, each path of edits.
func (s *state) applyToMain(key string, edits map[string]*scaffold.File, message string) {
	r := s.repos[key]
	r.files = withEdits(r.files, edits)
	hash := s.newSHA()
	r.commits = append(r.commits, ports.Commit{Hash: hash, Message: message})
	s.agentState().trees[key+"@"+hash] = treeID(r.files)
}

// treeID names a tree by what it holds.
func treeID(files scaffold.Tree) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%v", files)))
	return hex.EncodeToString(sum[:])
}

// agentStore is what the fake host keeps for the agent loop: protected
// branches, check states, bot users and pull requests, per owner/name.
type agentStore struct {
	protected map[string][]string             // owner/name -> branches
	rules     map[string]ports.BranchRule     // owner/name -> rule of the default branch
	tags      map[string][]string             // owner/name -> protected tag patterns
	checks    map[string]ports.CheckStatus    // owner/name@ref
	agents    map[string]string               // owner/name -> bot user
	pulls     map[string][]*ports.PullRequest // owner/name
	comments  map[string][]string             // owner/name#number
	branches  map[string]string               // owner/name@branch -> head sha
	authors   map[string]string               // owner/name#number -> who opened it
	texts     map[string][2]string            // owner/name#number -> its title and body
	log       string                          // served for every CI job
	files     map[string][]string             // owner/name#number -> paths the pull request changes
	commits   map[string][]ports.PullCommit   // owner/name#number -> the pull request's commits
	forced    map[string][]int                // owner/name -> pull requests force-merged
	conflicts map[string]bool                 // owner/name#N -> conflicts with its base
	gone      map[string]bool                 // owner/name#number -> pull request deleted
	readErrs  map[string]int                  // owner/name#number -> reads left to fail
	unnamed   map[string]bool                 // owner/name -> merge commits the host doesn't name
	// edits are what the commit PushBranch made on a branch changes on main,
	// by owner/name@branch: a path's new file, or nil when it removes it.
	edits map[string]map[string]*scaffold.File
	// removeErrs are what RemoveAgentAccess returns for a bot user, as a host
	// that refuses or doesn't answer would.
	removeErrs map[string]error
	// revoked counts the RevokeOldAgentTokens calls, by bot user.
	revoked map[string]int
	// trees are the tree ids set for commits, and the ones main's commits
	// got, by owner/name@sha.
	trees map[string]string
	// written are the statuses SetCommitStatus wrote, by owner/name@sha.
	written map[string][]ports.CommitStatus
}

func (s *state) agentState() *agentStore {
	if s.agent == nil {
		s.agent = &agentStore{
			protected: map[string][]string{}, checks: map[string]ports.CheckStatus{},
			rules: map[string]ports.BranchRule{}, tags: map[string][]string{},
			agents: map[string]string{}, pulls: map[string][]*ports.PullRequest{},
			comments: map[string][]string{}, branches: map[string]string{}, authors: map[string]string{},
			texts: map[string][2]string{},
			files: map[string][]string{}, commits: map[string][]ports.PullCommit{}, forced: map[string][]int{}, gone: map[string]bool{}, conflicts: map[string]bool{},
			readErrs: map[string]int{}, unnamed: map[string]bool{},
			edits: map[string]map[string]*scaffold.File{}, removeErrs: map[string]error{}, revoked: map[string]int{},
			trees: map[string]string{}, written: map[string][]ports.CommitStatus{},
		}
	}
	return s.agent
}

// SetChecks sets the combined check state of ref, a branch name or a commit, as a
// Standard's CI reports it: one check, under the context main requires.
func (g *Git) SetChecks(owner, name, ref, state string) {
	g.SetCheckContexts(owner, name, ref, state, map[string]string{"check / check (pull_request)": state})
}

// SetCheckContexts sets the combined check state of ref and the state of each
// check by its context.
func (g *Git) SetCheckContexts(owner, name, ref, state string, contexts map[string]string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.agentState().checks[owner+"/"+name+"@"+ref] = ports.CheckStatus{
		Commit: ref, State: state, URL: "http://gitea/" + owner + "/" + name + "/actions/runs/1", Contexts: contexts,
	}
}

// PushBranchHead records a branch head, as the sandbox's push would.
func (g *Git) PushBranchHead(owner, name, branch, sha string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.agentState().branches[owner+"/"+name+"@"+branch] = sha
	delete(g.s.agentState().edits, owner+"/"+name+"@"+branch)
}

// PushPlan pushes files as the plan of a Brief round, as the planner's run
// would: branch plan/<round> is main without its plan/ and with files instead.
// It returns the branch's head.
func (g *Git) PushPlan(owner, name string, round int, files scaffold.Tree) string {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	key := owner + "/" + name
	edits := map[string]*scaffold.File{}
	if r := g.s.repos[key]; r != nil {
		for filePath := range r.files {
			if strings.HasPrefix(filePath, "plan/") {
				edits[filePath] = nil
			}
		}
	}
	for filePath, f := range files {
		edits[filePath] = &f
	}
	sha := g.s.newSHA()
	s := g.s.agentState()
	branch := model.PlanBranch(round)
	s.branches[key+"@"+branch] = sha
	s.edits[key+"@"+branch] = edits
	return sha
}

// ProtectedBranches lists the branches of owner/name ProtectBranch protected.
func (g *Git) ProtectedBranches(owner, name string) []string {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.agentState().protected[owner+"/"+name]
}

// CommitCount returns how many commits main of owner/name has; 0 when the
// repository doesn't exist.
func (g *Git) CommitCount(owner, name string) int {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	if r := g.s.repos[owner+"/"+name]; r != nil {
		return len(r.commits)
	}
	return 0
}

// BranchRule returns the default branch's rule and the protected tag patterns.
func (g *Git) BranchRule(owner, name string) (ports.BranchRule, []string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.agentState()
	return s.rules[owner+"/"+name], s.tags[owner+"/"+name]
}

// imageHost is the host of a Git host's URL, its container registry's.
func imageHost(url string) string {
	host := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://")
	host, _, _ = strings.Cut(host, "/")
	return host
}

// Repository builds a repository's addresses as Gitea's adapter does, the
// package registries under api/packages/<owner> and the npm scope the owner's
// GitProvider names.
func (g *Git) Repository(name string) ports.Repository {
	web := strings.TrimSuffix(g.cfg.URL, "/") + "/" + path.Join(g.cfg.Owner, name)
	registry := strings.TrimSuffix(g.cfg.URL, "/") + "/api/packages/" + g.cfg.Owner
	or := func(set, fallback string) string {
		if set != "" {
			return set
		}
		return fallback
	}
	return ports.Repository{Owner: g.cfg.Owner, Name: name, WebURL: web, CloneURL: web + ".git",
		GoProxy:       registry + "/go",
		NpmScope:      or(g.cfg.Packages.NpmScope, ports.DefaultNpmScope),
		NpmRegistry:   or(g.cfg.Packages.NpmRegistry, registry+"/npm/"),
		PyPIIndex:     or(g.cfg.Packages.PyPIIndex, registry+"/pypi/simple"),
		PyPIUpload:    or(g.cfg.Packages.PyPIUpload, registry+"/pypi"),
		ImageRegistry: imageHost(g.cfg.URL) + "/" + strings.ToLower(g.cfg.Owner)}
}

func (g *Git) ProtectBranch(_ context.Context, repo ports.Repository, branch string,
	rule ports.BranchRule) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.agentState()
	key := repo.Owner + "/" + repo.Name
	if !slices.Contains(s.protected[key], branch) {
		s.protected[key] = append(s.protected[key], branch)
	}
	s.rules[key] = rule
	return nil
}

func (g *Git) ProtectTags(_ context.Context, repo ports.Repository, pattern string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.agentState()
	key := repo.Owner + "/" + repo.Name
	if !slices.Contains(s.tags[key], pattern) {
		s.tags[key] = append(s.tags[key], pattern)
	}
	return nil
}

func (g *Git) CombinedStatus(_ context.Context, repo ports.Repository, ref string) (ports.CheckStatus, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.agentState()
	if status, ok := s.checks[repo.Owner+"/"+repo.Name+"@"+ref]; ok {
		return status, nil
	}
	return ports.CheckStatus{Commit: ref}, nil
}

func (g *Git) DeleteBranch(_ context.Context, repo ports.Repository, branch string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	delete(g.s.agentState().branches, repo.Owner+"/"+repo.Name+"@"+branch)
	delete(g.s.agentState().edits, repo.Owner+"/"+repo.Name+"@"+branch)
	return nil
}

func (g *Git) HasBranch(_ context.Context, repo ports.Repository, branch string) (bool, error) {
	return g.BranchExists(repo.Owner, repo.Name, branch), nil
}

func (g *Git) BranchCommit(_ context.Context, repo ports.Repository, branch string) (string, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	if r := g.s.repos[repo.Owner+"/"+repo.Name]; branch == ports.DefaultBranch && r != nil &&
		len(r.commits) > 0 {
		return r.commits[len(r.commits)-1].Hash, nil
	}
	return g.s.agentState().branches[repo.Owner+"/"+repo.Name+"@"+branch], nil
}

func (g *Git) CreateBranch(_ context.Context, repo ports.Repository, branch, commit string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	key := repo.Owner + "/" + repo.Name + "@" + branch
	if _, ok := g.s.agentState().branches[key]; ok {
		return fmt.Errorf("branch %s exists", key)
	}
	g.s.agentState().branches[key] = commit
	return nil
}

// BranchHead is the commit branch of owner/name points at on the fake host,
// "" when it has no such branch.
func (g *Git) BranchHead(owner, name, branch string) string {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.agentState().branches[owner+"/"+name+"@"+branch]
}

// BranchExists reports whether the fake host has branch in owner/name.
func (g *Git) BranchExists(owner, name, branch string) bool {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	_, ok := g.s.agentState().branches[owner+"/"+name+"@"+branch]
	return ok
}

// BranchEdits are the files the commit PushBranch made on branch of owner/name
// writes, nil for a path it removes.
func (g *Git) BranchEdits(owner, name, branch string) map[string]*scaffold.File {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return maps.Clone(g.s.agentState().edits[owner+"/"+name+"@"+branch])
}

// Template is the template registered at url and ref.
func (g *Git) Template(url, ref string) scaffold.Tree {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.templates[url+"@"+ref]
}
