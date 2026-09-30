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
}

type fakeRepo struct {
	commits []gitprovider.Commit // oldest first
	files   scaffold.Tree
}

func newFakeGit(templatesDir string) (*fakeGit, error) {
	g := &fakeGit{templates: map[string]scaffold.Tree{}, repos: map[string]*fakeRepo{}, histories: map[string]int{}}
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

// historyCount is how often the operator has read a repository's history.
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

func (p *fakeProvider) History(_ context.Context, repo gitprovider.Repository, limit int) ([]gitprovider.Commit, error) {
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
