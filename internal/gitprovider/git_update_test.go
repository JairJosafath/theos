package gitprovider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/JairJosafath/c4factory/internal/scaffold"
)

const updateBranch = "standard-update/go-standard-v1.1.1"

var updateAuthor = Signature{Name: "C4 Factory", Email: "factory@c4.factory.io", When: testAuthor.When.Add(24 * time.Hour)}

// localProvider is the Gitea provider, for calls that go over Git alone, so they
// can reach a local repository.
func localProvider(t *testing.T) Provider {
	t.Helper()
	p, err := Client{}.Provider(Config{URL: "http://gitea.invalid", Owner: "o"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// scaffolded makes a repository whose main holds files in one commit, and
// returns its path and the commit.
func scaffolded(t *testing.T, files scaffold.Tree) (string, string) {
	t.Helper()
	dir := bareRepo(t)
	hash, err := pushInitialCommit(context.Background(), dir, nil, files, "Scaffold", testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	return dir, hash
}

// commitOnMain pushes a commit writing files to main, as a person would, and
// returns it.
func commitOnMain(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	clone := t.TempDir()
	local, err := git.PlainClone(clone, false, &git.CloneOptions{URL: dir})
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := local.Worktree()
	for p, data := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(clone, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(clone, p), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	sig := &object.Signature{Name: "Someone", Email: "someone@example.com", When: testAuthor.When.Add(time.Hour)}
	hash, err := wt.Commit("Their change", &git.CommitOptions{Author: sig})
	if err != nil {
		t.Fatal(err)
	}
	if err := local.Push(&git.PushOptions{}); err != nil {
		t.Fatal(err)
	}
	return hash.String()
}

// branchAt returns the commit branch is at in the repository at dir, and every
// file of it, or nil when there is no such branch.
func branchAt(t *testing.T, dir, branch string) (*object.Commit, scaffold.Tree) {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := repo.Reference(plumbing.NewBranchReferenceName(branch), true)
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return nil, nil
	}
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		t.Fatal(err)
	}
	iter, err := commit.Files()
	if err != nil {
		t.Fatal(err)
	}
	tree := scaffold.Tree{}
	if err := iter.ForEach(func(f *object.File) error {
		data, err := f.Contents()
		tree[f.Name] = scaffold.File{Data: []byte(data), Executable: f.Mode == filemode.Executable}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return commit, tree
}

func TestReadFiles(t *testing.T) {
	ctx := context.Background()
	dir, first := scaffolded(t, scaffold.Tree{
		"AGENTS.md":                  {Data: []byte("# agents\n")},
		"scripts/check.sh":           {Data: []byte("#!/bin/sh\n"), Executable: true},
		"docs/agents/implementer.md": {Data: []byte("implement\n")},
	})
	p := localProvider(t)
	repo := Repository{CloneURL: dir}

	files, sha, err := p.ReadFiles(ctx, repo, defaultBranch, []string{
		"AGENTS.md", "scripts/check.sh", "docs/agents/implementer.md",
		"missing.md",        // not there
		"docs/missing/x.md", // in a directory that isn't there
		"docs/agents",       // a directory
		"AGENTS.md/x",       // under a file
	})
	if err != nil {
		t.Fatalf("ReadFiles() error = %v", err)
	}
	want := scaffold.Tree{
		"AGENTS.md":                  {Data: []byte("# agents\n")},
		"scripts/check.sh":           {Data: []byte("#!/bin/sh\n"), Executable: true},
		"docs/agents/implementer.md": {Data: []byte("implement\n")},
	}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("ReadFiles() = %+v, want %+v", files, want)
	}
	if sha != first {
		t.Errorf("ReadFiles() read at %s, want the head %s", sha, first)
	}

	// It reads the branch where it is now.
	second := commitOnMain(t, dir, map[string]string{"AGENTS.md": "# agents, theirs\n"})
	files, sha, err = p.ReadFiles(ctx, repo, defaultBranch, []string{"AGENTS.md"})
	if err != nil {
		t.Fatal(err)
	}
	if sha != second || string(files["AGENTS.md"].Data) != "# agents, theirs\n" {
		t.Errorf("ReadFiles() = %+v at %s, want their AGENTS.md at %s", files, sha, second)
	}

	if _, _, err := p.ReadFiles(ctx, repo, "nope", []string{"AGENTS.md"}); err == nil {
		t.Error("ReadFiles() of a branch that isn't there succeeded")
	}
}

func TestPushBranch(t *testing.T) {
	ctx := context.Background()
	dir, base := scaffolded(t, scaffold.Tree{
		"README.md":           {Data: []byte("# app\n")},
		"AGENTS.md":           {Data: []byte("# agents v1\n")},
		"lint.sh":             {Data: []byte("#!/bin/sh\nlint\n")},
		"docs/agents/old.md":  {Data: []byte("old\n")},
		"docs/agents/keep.md": {Data: []byte("keep\n")},
		"ci/only.yml":         {Data: []byte("ci\n")},
	})
	p := localProvider(t)
	const message = "Update the Standard go-standard to v1.1.1\n"

	sha, err := p.PushBranch(ctx, Repository{CloneURL: dir}, base, updateBranch, scaffold.Tree{
		"AGENTS.md":          {Data: []byte("# agents v2\n")},
		"lint.sh":            {Data: []byte("#!/bin/sh\nlint\n"), Executable: true},
		"docs/agents/new.md": {Data: []byte("new\n")},
		"hack/setup.sh":      {Data: []byte("#!/bin/sh\n"), Executable: true},
	}, []string{
		"docs/agents/old.md", "ci/only.yml",
		"never/there.md", // not there: no error
		"docs/agents",    // a directory, not a file: left alone
	}, message, updateAuthor)
	if err != nil {
		t.Fatalf("PushBranch() error = %v", err)
	}

	commit, files := branchAt(t, dir, updateBranch)
	if commit == nil || commit.Hash.String() != sha {
		t.Fatalf("branch %s is at %v, want the pushed %s", updateBranch, commit, sha)
	}
	if len(commit.ParentHashes) != 1 || commit.ParentHashes[0].String() != base {
		t.Errorf("parents = %v, want base %s", commit.ParentHashes, base)
	}
	for _, sig := range []object.Signature{commit.Author, commit.Committer} {
		if sig.Name != updateAuthor.Name || sig.Email != updateAuthor.Email || !sig.When.Equal(updateAuthor.When) {
			t.Errorf("signature = %+v, want %+v", sig, updateAuthor)
		}
	}
	if commit.Message != message {
		t.Errorf("message = %q, want %q", commit.Message, message)
	}
	want := scaffold.Tree{
		"README.md":           {Data: []byte("# app\n")},
		"AGENTS.md":           {Data: []byte("# agents v2\n")},
		"lint.sh":             {Data: []byte("#!/bin/sh\nlint\n"), Executable: true},
		"docs/agents/keep.md": {Data: []byte("keep\n")},
		"docs/agents/new.md":  {Data: []byte("new\n")},
		"hack/setup.sh":       {Data: []byte("#!/bin/sh\n"), Executable: true},
	}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("branch files = %+v, want %+v", files, want)
	}
	tree, err := commit.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.FindEntry("ci"); err == nil {
		t.Error("removing ci/'s last file left an empty ci directory")
	}

	if main, _ := branchAt(t, dir, defaultBranch); main.Hash.String() != base {
		t.Errorf("main moved to %s, want it left at %s", main.Hash, base)
	}

	// What ReadFiles reads back is what was pushed.
	read, at, err := p.ReadFiles(ctx, Repository{CloneURL: dir}, updateBranch, []string{"lint.sh", "ci/only.yml"})
	if err != nil {
		t.Fatal(err)
	}
	if at != sha || !reflect.DeepEqual(read, scaffold.Tree{"lint.sh": want["lint.sh"]}) {
		t.Errorf("ReadFiles(%s) = %+v at %s, want lint.sh at %s", updateBranch, read, at, sha)
	}
}

// main moved on since the update read it: the commit still builds on the base
// it read, so the pull request shows only the update's own change.
func TestPushBranchOnOlderBase(t *testing.T) {
	ctx := context.Background()
	dir, base := scaffolded(t, scaffold.Tree{
		"README.md": {Data: []byte("# app\n")},
		"AGENTS.md": {Data: []byte("# agents v1\n")},
	})
	newer := commitOnMain(t, dir, map[string]string{"NEWS.md": "news\n", "README.md": "# theirs\n"})

	_, err := localProvider(t).PushBranch(ctx, Repository{CloneURL: dir}, base, updateBranch,
		scaffold.Tree{"AGENTS.md": {Data: []byte("# agents v2\n")}}, nil, "Update", updateAuthor)
	if err != nil {
		t.Fatalf("PushBranch() error = %v", err)
	}

	commit, files := branchAt(t, dir, updateBranch)
	if len(commit.ParentHashes) != 1 || commit.ParentHashes[0].String() != base {
		t.Errorf("parents = %v, want the older base %s", commit.ParentHashes, base)
	}
	want := scaffold.Tree{
		"README.md": {Data: []byte("# app\n")},
		"AGENTS.md": {Data: []byte("# agents v2\n")},
	}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("branch files = %+v, want base's with the update, without main's newer change", files)
	}
	if main, _ := branchAt(t, dir, defaultBranch); main.Hash.String() != newer {
		t.Errorf("main moved to %s, want it left at %s", main.Hash, newer)
	}
}

func TestPushBranchReplacesBranch(t *testing.T) {
	ctx := context.Background()
	dir, base := scaffolded(t, scaffold.Tree{"AGENTS.md": {Data: []byte("# agents v1\n")}})
	p := localProvider(t)
	repo := Repository{CloneURL: dir}
	push := func(agents string) string {
		t.Helper()
		sha, err := p.PushBranch(ctx, repo, base, updateBranch,
			scaffold.Tree{"AGENTS.md": {Data: []byte(agents)}}, nil, "Update", updateAuthor)
		if err != nil {
			t.Fatalf("PushBranch() error = %v", err)
		}
		return sha
	}

	first := push("# agents v2\n")
	if again := push("# agents v2\n"); again != first {
		t.Errorf("pushing the same commit again = %s, want %s", again, first)
	}

	replaced := push("# agents v3\n")
	commit, files := branchAt(t, dir, updateBranch)
	if commit.Hash.String() != replaced || replaced == first {
		t.Fatalf("branch is at %s, want it replaced by %s (was %s)", commit.Hash, replaced, first)
	}
	if len(commit.ParentHashes) != 1 || commit.ParentHashes[0].String() != base {
		t.Errorf("parents = %v, want base %s, not the replaced commit", commit.ParentHashes, base)
	}
	if got := string(files["AGENTS.md"].Data); got != "# agents v3\n" {
		t.Errorf("AGENTS.md = %q, want the replacing commit's", got)
	}
}

func TestPushBranchRefuses(t *testing.T) {
	ctx := context.Background()
	agents := scaffold.Tree{"AGENTS.md": {Data: []byte("# agents v1\n")}}
	dir, base := scaffolded(t, agents)
	update := scaffold.Tree{"AGENTS.md": {Data: []byte("# agents v2\n")}}

	for _, tt := range []struct {
		name         string
		base, branch string
		files        scaffold.Tree
		removes      []string
		is           error
		want         string
	}{
		{name: "a base that isn't there", base: strings.Repeat("a", 40), branch: updateBranch, files: update,
			is: plumbing.ErrObjectNotFound},
		{name: "a base that is no commit SHA", base: "main", branch: updateBranch, files: update,
			want: "not a full commit SHA"},
		{name: "an empty change", base: base, branch: updateBranch, files: agents, removes: []string{"gone.md"},
			is: ErrEmptyChange},
		{name: "the default branch", base: base, branch: defaultBranch, files: update, want: "default branch"},
		{name: "a path out of the tree", base: base, branch: updateBranch,
			files: scaffold.Tree{"../x": {Data: []byte("x")}}, want: "invalid path"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := localProvider(t).PushBranch(ctx, Repository{CloneURL: dir}, tt.base, tt.branch, tt.files,
				tt.removes, "Update", updateAuthor)
			if err == nil {
				t.Fatal("PushBranch() succeeded")
			}
			if tt.is != nil && !errors.Is(err, tt.is) {
				t.Errorf("PushBranch() error = %v, want %v", err, tt.is)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("PushBranch() error = %v, want it to say %q", err, tt.want)
			}
			if commit, _ := branchAt(t, dir, updateBranch); commit != nil {
				t.Errorf("branch %s was pushed at %s", updateBranch, commit.Hash)
			}
			if main, _ := branchAt(t, dir, defaultBranch); main.Hash.String() != base {
				t.Errorf("main moved to %s", main.Hash)
			}
		})
	}
}

func TestPushDefaultBranch(t *testing.T) {
	ctx := context.Background()
	agents := scaffold.Tree{"AGENTS.md": {Data: []byte("# agents v1\n")}}
	dir, base := scaffolded(t, agents)
	p := localProvider(t)
	repo := Repository{CloneURL: dir}

	if _, err := p.PushDefaultBranch(ctx, repo, base, agents, nil, "Nothing", updateAuthor); !errors.Is(err, ErrEmptyChange) {
		t.Fatalf("PushDefaultBranch() of no change error = %v, want ErrEmptyChange", err)
	}

	update := scaffold.Tree{"brief.md": {Data: []byte("# Brief\n")}}
	sha, err := p.PushDefaultBranch(ctx, repo, base, update, nil, "Write the brief", updateAuthor)
	if err != nil {
		t.Fatalf("PushDefaultBranch() error = %v", err)
	}
	main, files := branchAt(t, dir, defaultBranch)
	if main.Hash.String() != sha || len(main.ParentHashes) != 1 || main.ParentHashes[0].String() != base {
		t.Fatalf("main is at %s (parents %v), want %s on %s", main.Hash, main.ParentHashes, sha, base)
	}
	if string(files["brief.md"].Data) != "# Brief\n" || string(files["AGENTS.md"].Data) != "# agents v1\n" {
		t.Errorf("main files = %+v", files)
	}

	// main moved past base: no force, so the push fails and main stays.
	again := scaffold.Tree{"brief.md": {Data: []byte("# Brief v2\n")}}
	if _, err := p.PushDefaultBranch(ctx, repo, base, again, nil, "Stale", updateAuthor); err == nil {
		t.Fatal("PushDefaultBranch() on a stale base succeeded")
	}
	if main, _ := branchAt(t, dir, defaultBranch); main.Hash.String() != sha {
		t.Errorf("main moved to %s, want it left at %s", main.Hash, sha)
	}
}
